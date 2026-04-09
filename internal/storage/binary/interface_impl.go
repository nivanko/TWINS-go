package binary

import (
	"encoding/binary"
	"fmt"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/sirupsen/logrus"
	"github.com/twins-dev/twins-core/internal/storage"
	"github.com/twins-dev/twins-core/pkg/crypto"
	"github.com/twins-dev/twins-core/pkg/types"
)

// Implement remaining Storage interface methods for BinaryStorage

// GetBlock retrieves a block by hash
func (bs *BinaryStorage) GetBlock(hash types.Hash) (*types.Block, error) {
	// Check if database is still valid
	if bs.db == nil {
		return nil, fmt.Errorf("storage database is not initialized")
	}

	// Get the compact block data using 0x01 prefix
	blockKey := BlockKey(hash)
	data, closer, err := bs.db.Get(blockKey)
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, storage.NewStorageError("BLOCK_NOT_FOUND", "block not found", nil)
		}
		return nil, err
	}
	defer closer.Close()

	// Decode compact block format
	compact, err := DecodeCompactBlock(data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode compact block: %w", err)
	}

	// Reconstruct full block
	block := &types.Block{
		Header: &types.BlockHeader{
			Version:       compact.Version,
			PrevBlockHash: compact.PrevBlock,
			MerkleRoot:    compact.Merkle,
			Timestamp:     compact.Timestamp,
			Bits:          compact.Bits,
			Nonce:         compact.Nonce,
		},
		Transactions: make([]*types.Transaction, len(compact.TxHashes)),
		Signature:    compact.Signature, // PoS block signature (required for validation)
	}

	// Fetch all transactions in batch to avoid N+1 queries
	// This significantly improves performance for blocks with many transactions
	transactions, err := bs.GetTransactionBatch(compact.TxHashes)
	if err != nil {
		return nil, fmt.Errorf("failed to get block transactions: %w", err)
	}
	block.Transactions = transactions

	// For genesis block at height 0, set the canonical hash to the requested hash
	// This preserves the Quark hash instead of recalculating SHA256
	if compact.Height == 0 {
		block.SetCanonicalHash(hash)
	}

	return block, nil
}

// GetBlockByHeight retrieves a block by height
func (bs *BinaryStorage) GetBlockByHeight(height uint32) (*types.Block, error) {
	// Check if database is still valid
	if bs.db == nil {
		return nil, fmt.Errorf("storage database is not initialized")
	}

	// First get the block hash at this height
	hash, err := bs.GetBlockHashByHeight(height)
	if err != nil {
		return nil, err
	}

	// Now get the block using the hash
	return bs.GetBlock(hash)
}

// HasBlock checks if a block exists
func (bs *BinaryStorage) HasBlock(hash types.Hash) (bool, error) {
	// Simple check using the new schema
	blockKey := BlockKey(hash)
	if _, closer, err := bs.db.Get(blockKey); err == nil {
		closer.Close()
		return true, nil
	} else if err == pebble.ErrNotFound {
		return false, nil
	} else {
		return false, err
	}
}

// GetBlockParentHash retrieves the parent block hash from compact block data
// without loading transactions. This is used for corrupt block recovery where
// the block header exists but transactions are missing.
func (bs *BinaryStorage) GetBlockParentHash(hash types.Hash) (types.Hash, error) {
	if bs.db == nil {
		return types.Hash{}, fmt.Errorf("storage database is not initialized")
	}

	blockKey := BlockKey(hash)
	data, closer, err := bs.db.Get(blockKey)
	if err != nil {
		if err == pebble.ErrNotFound {
			return types.Hash{}, storage.NewStorageError("BLOCK_NOT_FOUND", "block not found", nil)
		}
		return types.Hash{}, err
	}
	defer closer.Close()

	// CompactBlock layout: Height(4) + Version(4) + PrevBlock(32) + ...
	// PrevBlock starts at offset 8
	if len(data) < 40 {
		return types.Hash{}, fmt.Errorf("invalid compact block data: too short for parent hash")
	}

	var parentHash types.Hash
	copy(parentHash[:], data[8:40])
	return parentHash, nil
}

// DeleteBlock removes a block from storage
func (bs *BinaryStorage) DeleteBlock(hash types.Hash) error {
	height, err := bs.GetBlockHeight(hash)
	if err != nil {
		return err
	}

	// First, get the block to find all transactions
	block, err := bs.GetBlock(hash)
	if err != nil {
		return fmt.Errorf("failed to get block for deletion: %w", err)
	}

	batch := bs.db.NewBatch()
	defer batch.Close()

	// Delete block data
	blockKey := BlockKey(hash)
	if err := batch.Delete(blockKey, nil); err != nil {
		return err
	}

	// Delete all transactions from this block (0x04 prefix)
	// Also clean up any mempool entries (0x11 prefix) to prevent orphaned data during reorgs
	for _, tx := range block.Transactions {
		txHash := tx.Hash()

		// Delete blockchain transaction (0x04)
		txKey := TransactionKey(txHash)
		if err := batch.Delete(txKey, nil); err != nil {
			return fmt.Errorf("failed to delete transaction %x: %w", txHash, err)
		}

		// Also delete from mempool namespace if it exists (0x11)
		// This handles the case where a transaction was in mempool,
		// then included in a block, and now the block is being reorged
		mempoolKey := MempoolTransactionKey(txHash)
		// We don't check for errors here as the transaction might not be in mempool
		batch.Delete(mempoolKey, nil)
	}

	// Delete address history entries for this block (0x05 prefix)
	// Address history uses: 0x05 + scriptHash(20) + height(4) + txHash(32) + txIndex(2)
	// We need to scan for all entries at this height
	addressHistoryPrefix := []byte{PrefixAddressHistory}
	iter, err := bs.db.NewIter(&pebble.IterOptions{
		LowerBound: addressHistoryPrefix,
		UpperBound: NextPrefix(addressHistoryPrefix),
	})
	if err != nil {
		return fmt.Errorf("failed to create iterator for address history cleanup: %w", err)
	}
	defer iter.Close()

	// Find all address history entries at this height
	for iter.First(); iter.Valid(); iter.Next() {
		key := iter.Key()
		// Key format: 0x05 + scriptHash(20) + height(4) + txHash(32) + txIndex(2)
		if len(key) >= 25 {
			// Extract height from key (bytes 21-25)
			entryHeight := binary.LittleEndian.Uint32(key[21:25])
			if entryHeight == height {
				// Delete this entry
				if err := batch.Delete(key, nil); err != nil {
					return fmt.Errorf("failed to delete address history entry: %w", err)
				}
			}
		}
	}

	// Delete hash-to-height index
	hashToHeightKey := HashToHeightKey(hash)
	if err := batch.Delete(hashToHeightKey, nil); err != nil {
		return err
	}

	// Delete height-to-hash mapping
	heightToHashKey := HeightToHashKey(height)
	if err := batch.Delete(heightToHashKey, nil); err != nil {
		return err
	}

	// Delete stake modifier (0x08) to prevent stale modifiers after rollback
	stakeModKey := StakeModifierKey(hash)
	batch.Delete(stakeModKey, nil) // Ignore not-found errors

	// Delete PoS metadata (checksum + proof hash) to prevent stale PoS data
	posMetaKey := BlockPoSMetadataKey(hash)
	batch.Delete(posMetaKey, nil) // Ignore not-found errors

	// Commit the batch
	if err := batch.Commit(nil); err != nil {
		return fmt.Errorf("failed to commit block deletion: %w", err)
	}

	// CRITICAL: Sync to ensure indexes are flushed to disk
	// This prevents corruption if node crashes during reorg
	if err := bs.Sync(); err != nil {
		return fmt.Errorf("failed to sync after block deletion: %w", err)
	}

	return nil
}

// DeleteBlockIndex removes only the hash→height and height→hash index entries for a block.
// Unlike DeleteBlock, this does not require the block data to exist in storage.
// Used for cleaning up orphaned index entries after rollback where block data is already gone
// but stale index entries remain.
func (bs *BinaryStorage) DeleteBlockIndex(hash types.Hash) error {
	// Look up the height from hash→height index
	hashToHeightKey := HashToHeightKey(hash)
	heightData, closer, err := bs.db.Get(hashToHeightKey)
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil // Index entry already gone
		}
		return fmt.Errorf("failed to read hash→height index: %w", err)
	}

	var height uint32
	if len(heightData) >= 4 {
		height = binary.LittleEndian.Uint32(heightData)
	}
	closer.Close()

	batch := bs.db.NewBatch()
	defer batch.Close()

	// Delete hash→height index
	if err := batch.Delete(hashToHeightKey, nil); err != nil {
		return fmt.Errorf("failed to delete hash→height index: %w", err)
	}

	// Delete height→hash index (only if it points to this hash)
	if height > 0 || len(heightData) >= 4 {
		heightToHashKey := HeightToHashKey(height)
		existingHash, hCloser, hErr := bs.db.Get(heightToHashKey)
		if hErr == nil {
			var storedHash types.Hash
			if len(existingHash) >= 32 {
				copy(storedHash[:], existingHash[:32])
			}
			hCloser.Close()

			if storedHash == hash {
				if err := batch.Delete(heightToHashKey, nil); err != nil {
					return fmt.Errorf("failed to delete height→hash index: %w", err)
				}
			}
		}
	}

	if err := batch.Commit(nil); err != nil {
		return fmt.Errorf("failed to commit index deletion: %w", err)
	}

	return nil
}

// CleanOrphanedBlocks removes all blocks whose hash→height entry points to a height
// above maxValidHeight. This catches orphaned entries from fork blocks that were stored
// but not properly cleaned during rollback. For each orphaned entry, performs full cleanup
// of block data, transaction indexes, metadata, and index entries.
// Returns the number of orphaned entries removed.
func (bs *BinaryStorage) CleanOrphanedBlocks(maxValidHeight uint32) (int, error) {
	// Collect orphaned hashes (can't modify DB during iteration)
	type orphanEntry struct {
		hash   types.Hash
		height uint32
	}
	var orphans []orphanEntry

	err := bs.IterateHashToHeight(func(hash types.Hash, height uint32) bool {
		if height > maxValidHeight {
			orphans = append(orphans, orphanEntry{hash: hash, height: height})
		}
		return true
	})
	if err != nil {
		return 0, fmt.Errorf("iterate hash→height for orphan cleanup: %w", err)
	}

	if len(orphans) == 0 {
		return 0, nil
	}

	cleaned := 0
	for _, entry := range orphans {
		if err := bs.deleteOrphanedBlock(entry.hash, entry.height); err != nil {
			// Best effort — log would happen at caller level
			continue
		}
		cleaned++
	}

	if cleaned > 0 {
		if err := bs.Sync(); err != nil {
			return cleaned, fmt.Errorf("sync after orphan cleanup: %w", err)
		}
	}

	return cleaned, nil
}

// deleteOrphanedBlock removes a single orphaned block's data and indexes.
// Unlike DeleteBlock, this method:
//   - Only deletes height→hash if it points to this specific hash (preserves the correct block)
//   - Handles missing block data gracefully (still cleans address history at the orphan height)
//
// When block data is available, performs a full UTXO rollback for every transaction
// in the block (deletes UTXOs created by the block and unspends UTXOs consumed by
// it), matching disconnectBlock semantics. This prevents stuck-spent UTXOs and
// stale address-UTXO entries after orphan cleanup.
func (bs *BinaryStorage) deleteOrphanedBlock(hash types.Hash, height uint32) error {
	batch := bs.db.NewBatch()
	defer batch.Close()

	// 1. Delete hash→height index (this IS the orphaned entry)
	if err := batch.Delete(HashToHeightKey(hash), nil); err != nil {
		return fmt.Errorf("delete hash→height: %w", err)
	}

	// 2. Delete height→hash only if it points to THIS hash (not the correct block)
	heightToHashKey := HeightToHashKey(height)
	if data, closer, err := bs.db.Get(heightToHashKey); err == nil {
		var storedHash types.Hash
		if len(data) >= 32 {
			copy(storedHash[:], data[:32])
		}
		closer.Close()
		if storedHash == hash {
			batch.Delete(heightToHashKey, nil)
		}
	}

	// 3. Try to load block for transaction index cleanup AND full UTXO rollback.
	block, blockErr := bs.GetBlock(hash)
	if blockErr == nil && block != nil {
		for _, tx := range block.Transactions {
			txHash := tx.Hash()

			// Delete transaction index + mempool namespace
			batch.Delete(TransactionKey(txHash), nil)
			batch.Delete(MempoolTransactionKey(txHash), nil)

			// Rollback outputs: delete UTXOExist entries and the corresponding
			// address UTXO index entries so balance queries no longer see them.
			for outIdx, output := range tx.Outputs {
				outpointIndex := uint32(outIdx)
				utxoKey := UTXOExistKey(txHash, outpointIndex)

				// Best-effort read to know the scriptHash for address UTXO
				// cleanup. If missing, we still delete the UTXOExist key.
				if existing, closer, getErr := bs.db.Get(utxoKey); getErr == nil {
					if decoded, decErr := DecodeUTXOData(existing); decErr == nil && decoded.ScriptHash != [20]byte{} {
						addrKey := AddressUTXOKey(decoded.ScriptHash, txHash, outpointIndex)
						batch.Delete(addrKey, nil)
					}
					closer.Close()
				} else if output != nil && len(output.ScriptPubKey) > 0 {
					// Fallback: derive scriptHash from tx output
					if _, scriptHash := AnalyzeScript(output.ScriptPubKey); scriptHash != [20]byte{} {
						addrKey := AddressUTXOKey(scriptHash, txHash, outpointIndex)
						batch.Delete(addrKey, nil)
					}
				}

				batch.Delete(utxoKey, nil)
			}

			// Rollback inputs: unspend UTXOs consumed by this tx if the
			// recorded spender is this tx. Skip null prevouts (coinbase /
			// coinstake marker inputs).
			for _, input := range tx.Inputs {
				prev := input.PreviousOutput
				if prev.Hash == (types.Hash{}) && prev.Index == 0xffffffff {
					continue // coinbase-style null input
				}

				prevKey := UTXOExistKey(prev.Hash, prev.Index)
				prevData, prevCloser, prevErr := bs.db.Get(prevKey)
				if prevErr != nil {
					continue // UTXO already gone — nothing to unspend
				}
				decoded, decErr := DecodeUTXOData(prevData)
				prevCloser.Close()
				if decErr != nil {
					continue
				}
				if decoded.IsUnspent() {
					continue
				}
				// Only unspend if this transaction is the recorded spender
				// AND the spend is attributed to the orphan block we are
				// removing. The height guard is defensive: if the same txid
				// were re-included on the winning chain at a different
				// height (disallowed by BIP34, but checked for safety), the
				// current SpendingHeight would differ from the orphan
				// height and we must not clear that valid spend.
				if decoded.SpendingTxHash != txHash || decoded.SpendingHeight != height {
					continue
				}

				decoded.SpendingHeight = 0
				decoded.SpendingTxHash = types.Hash{}
				newData, encErr := EncodeUTXOData(decoded)
				if encErr != nil {
					continue
				}
				batch.Set(prevKey, newData, nil)

				// Restore address UTXO index entry so balances see the UTXO.
				if decoded.ScriptHash != [20]byte{} {
					addrKey := AddressUTXOKey(decoded.ScriptHash, prev.Hash, prev.Index)
					var valueBuf [8]byte
					binary.LittleEndian.PutUint64(valueBuf[:], decoded.Value)
					batch.Set(addrKey, valueBuf[:], nil)
				}
			}

		}
	}

	// 4. Delete address history entries at this height regardless of whether
	//    block data was loaded. This prevents stale 0x05 entries from surviving
	//    when block data was already removed by an earlier disconnect/crash.
	addressHistoryPrefix := []byte{PrefixAddressHistory}
	iter, iterErr := bs.db.NewIter(&pebble.IterOptions{
		LowerBound: addressHistoryPrefix,
		UpperBound: NextPrefix(addressHistoryPrefix),
	})
	if iterErr != nil {
		return fmt.Errorf("create address history iterator: %w", iterErr)
	}
	for iter.First(); iter.Valid(); iter.Next() {
		key := iter.Key()
		// Key format: 0x05 + scriptHash(20) + height(4) + txHash(32) + txIndex(2)
		if len(key) >= 25 {
			entryHeight := binary.LittleEndian.Uint32(key[21:25])
			if entryHeight == height {
				keyCopy := make([]byte, len(key))
				copy(keyCopy, key)
				batch.Delete(keyCopy, nil)
			}
		}
	}
	if err := iter.Error(); err != nil {
		iter.Close()
		return fmt.Errorf("iterate address history: %w", err)
	}
	iter.Close()

	// 5. Delete block data (no-op if already gone)
	batch.Delete(BlockKey(hash), nil)

	// 6. Delete associated metadata
	batch.Delete(StakeModifierKey(hash), nil)
	batch.Delete(BlockPoSMetadataKey(hash), nil)

	return batch.Commit(nil)
}

// UnspendUTXOsBySpendingTx iterates the UTXO set and resets the spending
// reference on any UTXO whose SpendingTxHash is in txHashes. Returns the
// number of UTXOs that were unspent. Used to reconcile stuck-spent UTXOs
// whose spending transaction no longer exists in storage (stale references
// after incomplete orphan cleanup or corrupt block recovery).
//
// Concurrency: this is a read-modify-write pass without an explicit
// transaction. The caller (wallet rescan) is expected to run during node
// startup or under user-triggered RPC, when block processing is either
// idle or naturally serialised via the wallet mutex and higher-layer
// processing locks. The only UTXOs touched have SpendingTxHash set to a
// txid that is absent from storage — no active block can legitimately be
// modifying them concurrently, because unspending requires disconnecting
// the (nonexistent) spending block.
func (bs *BinaryStorage) UnspendUTXOsBySpendingTx(txHashes map[types.Hash]struct{}) (int, error) {
	if len(txHashes) == 0 {
		return 0, nil
	}

	prefix := []byte{PrefixUTXOExist}
	iter, err := bs.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: NextPrefix(prefix),
	})
	if err != nil {
		return 0, fmt.Errorf("create UTXO iterator: %w", err)
	}
	defer iter.Close()

	batch := bs.db.NewBatch()
	defer batch.Close()

	unspent := 0
	for iter.First(); iter.Valid(); iter.Next() {
		key := iter.Key()
		if len(key) != 37 { // [0x07][txhash:32][index:4]
			continue
		}

		value := iter.Value()
		decoded, decErr := DecodeUTXOData(value)
		if decErr != nil {
			continue
		}
		if decoded.IsUnspent() {
			continue
		}
		if _, match := txHashes[decoded.SpendingTxHash]; !match {
			continue
		}

		// Extract outpoint from key
		var outpointHash types.Hash
		copy(outpointHash[:], key[1:33])
		outpointIndex := binary.LittleEndian.Uint32(key[33:37])

		// Reset spending reference
		decoded.SpendingHeight = 0
		decoded.SpendingTxHash = types.Hash{}

		newData, encErr := EncodeUTXOData(decoded)
		if encErr != nil {
			continue
		}

		// Copy key (iterator data is reused)
		keyCopy := make([]byte, len(key))
		copy(keyCopy, key)
		if setErr := batch.Set(keyCopy, newData, nil); setErr != nil {
			return 0, fmt.Errorf("update UTXO: %w", setErr)
		}

		// Restore address UTXO index entry
		if decoded.ScriptHash != [20]byte{} {
			addrKey := AddressUTXOKey(decoded.ScriptHash, outpointHash, outpointIndex)
			var valueBuf [8]byte
			binary.LittleEndian.PutUint64(valueBuf[:], decoded.Value)
			if setErr := batch.Set(addrKey, valueBuf[:], nil); setErr != nil {
				return 0, fmt.Errorf("restore address UTXO: %w", setErr)
			}
		}

		unspent++
	}

	// CRITICAL: check iterator error before committing. A mid-scan Pebble
	// error would otherwise produce a partial batch that still commits —
	// exactly the transient I/O state the wallet self-heal path tries to
	// avoid treating as a successful reconciliation.
	if err := iter.Error(); err != nil {
		return 0, fmt.Errorf("iterate UTXO set: %w", err)
	}

	if unspent == 0 {
		return 0, nil
	}

	if err := batch.Commit(nil); err != nil {
		return 0, fmt.Errorf("commit unspend batch: %w", err)
	}

	return unspent, nil
}

// FindAndMarkSpendersForOutpoints scans the entire transaction index once
// and finds any transaction whose inputs consume one of the given
// outpoints. For each match, validates the transaction is on the active
// main chain, verifies the target UTXO is still unspent, and marks it as
// spent in a single batch (also removing the address UTXO index entry).
// Returns a map of outpoint → spender info for callers that need to
// update their in-memory state.
//
// This is the recovery path for phantom-unspent UTXOs whose spending
// transaction was persisted in storage but whose mark-spent and
// address-index input-side writes were both skipped by an interrupted
// batch commit. When both writes are missing, address-history-based
// reconciliation cannot find the spender, and a full transaction scan
// is the only way to recover.
//
// Concurrency: caller is expected to hold an appropriate higher-layer
// lock (wallet mutex during rescan); this method does not take any
// storage-level lock but pebble provides snapshot consistency for the
// iterator. Writes are coalesced into a single batch commit.
func (bs *BinaryStorage) FindAndMarkSpendersForOutpoints(outpoints map[types.Outpoint]struct{}) (map[types.Outpoint]storage.SpenderInfo, error) {
	if len(outpoints) == 0 {
		return nil, nil
	}

	// Local working copy so we can delete found entries and avoid mutating
	// the caller's map.
	pending := make(map[types.Outpoint]struct{}, len(outpoints))
	for op := range outpoints {
		pending[op] = struct{}{}
	}

	prefix := []byte{PrefixTransaction}
	iter, err := bs.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: NextPrefix(prefix),
	})
	if err != nil {
		return nil, fmt.Errorf("create transaction iterator: %w", err)
	}
	defer iter.Close()

	batch := bs.db.NewBatch()
	defer batch.Close()

	results := make(map[types.Outpoint]storage.SpenderInfo)

	for iter.First(); iter.Valid(); iter.Next() {
		if len(pending) == 0 {
			break // all targets resolved — early exit
		}

		value := iter.Value()
		txData, decErr := DecodeTransactionData(value)
		if decErr != nil || txData == nil || txData.TxData == nil {
			continue
		}
		tx := txData.TxData

		// Coinbase has no consumable inputs.
		if tx.IsCoinbase() {
			continue
		}

		// Fast-path: check if any input matches a pending outpoint
		// before doing the expensive main-chain validation.
		var matched []types.Outpoint
		for _, input := range tx.Inputs {
			prev := input.PreviousOutput
			if prev.Hash == (types.Hash{}) && prev.Index == 0xffffffff {
				continue // coinstake-style null prevout
			}
			if _, wanted := pending[prev]; wanted {
				matched = append(matched, prev)
			}
		}
		if len(matched) == 0 {
			continue
		}

		// Validate that this transaction is on the active main chain.
		// If the block at the stored height points to a different hash,
		// the transaction is from an orphaned chain and its inputs must
		// not be marked as spent.
		canonicalHash, chkErr := bs.GetBlockHashByHeight(txData.Height)
		if chkErr != nil {
			continue // height not found — treat as orphan, skip
		}
		if canonicalHash != txData.BlockHash {
			continue // tx on orphan/fork chain, skip
		}

		spenderHash := tx.Hash()

		for _, outpoint := range matched {
			utxoKey := UTXOExistKey(outpoint.Hash, outpoint.Index)
			existing, closer, getErr := bs.db.Get(utxoKey)
			if getErr != nil {
				if getErr == pebble.ErrNotFound {
					// UTXO no longer present — nothing to reconcile.
					delete(pending, outpoint)
					continue
				}
				// Transient I/O error: fail closed rather than silently
				// dropping the outpoint. A repair pass that silently
				// skipped real failures would hide the very bugs it is
				// meant to fix.
				return nil, fmt.Errorf("read UTXO %s: %w", outpoint.String(), getErr)
			}
			decoded, decUTXOErr := DecodeUTXOData(existing)
			closer.Close()
			if decUTXOErr != nil {
				return nil, fmt.Errorf("decode UTXO %s: %w", outpoint.String(), decUTXOErr)
			}
			if decoded.IsSpent() {
				// Already spent — no work. This is a normal end state
				// reached via a different repair path or a concurrent
				// writer; drop from pending and continue.
				delete(pending, outpoint)
				continue
			}

			// Pre-mutation main-chain re-check: re-read the canonical
			// block hash at the spender's height and bail out if it no
			// longer matches the tx's stored BlockHash. Closes the race
			// window between the initial main-chain validation above
			// and this write — a concurrent block disconnect could
			// invalidate the spender's canonicality after our iterator
			// snapshot but before we commit. Without this re-check we
			// might mark a UTXO as spent by a transaction that is no
			// longer on the active chain.
			recheckHash, recheckErr := bs.GetBlockHashByHeight(txData.Height)
			if recheckErr != nil {
				if storage.IsNotFoundError(recheckErr) {
					delete(pending, outpoint)
					continue
				}
				return nil, fmt.Errorf("revalidate main chain for %s at height %d: %w",
					spenderHash.String(), txData.Height, recheckErr)
			}
			if recheckHash != txData.BlockHash {
				// Spender no longer canonical — skip this outpoint for
				// this pass. A subsequent rescan can retry.
				delete(pending, outpoint)
				continue
			}

			decoded.SpendingHeight = txData.Height
			decoded.SpendingTxHash = spenderHash
			newData, encErr := EncodeUTXOData(decoded)
			if encErr != nil {
				return nil, fmt.Errorf("encode UTXO %s: %w", outpoint.String(), encErr)
			}

			if setErr := batch.Set(UTXOExistKey(outpoint.Hash, outpoint.Index), newData, nil); setErr != nil {
				return nil, fmt.Errorf("update UTXO %s: %w", outpoint.String(), setErr)
			}

			// Drop the address UTXO index entry so balance queries skip it.
			if decoded.ScriptHash != [20]byte{} {
				addrKey := AddressUTXOKey(decoded.ScriptHash, outpoint.Hash, outpoint.Index)
				batch.Delete(addrKey, nil)
			}

			results[outpoint] = storage.SpenderInfo{
				SpenderTxHash: spenderHash,
				SpenderHeight: txData.Height,
			}
			delete(pending, outpoint)
		}
	}

	if err := iter.Error(); err != nil {
		return nil, fmt.Errorf("iterate transactions: %w", err)
	}

	if len(results) > 0 {
		if err := batch.Commit(nil); err != nil {
			return nil, fmt.Errorf("commit mark-spent batch: %w", err)
		}
	}

	return results, nil
}

// DeleteBlockData removes only the block data (compact block key) without touching indexes.
// Used for cleaning up orphaned blocks that have data but no indexes (e.g. after interrupted sync).
// After deletion, HasBlock() will return false so the block gets reprocessed with proper indexes.
func (bs *BinaryStorage) DeleteBlockData(hash types.Hash) error {
	blockKey := BlockKey(hash)
	if err := bs.db.Delete(blockKey, nil); err != nil {
		if err == pebble.ErrNotFound {
			return nil
		}
		return fmt.Errorf("failed to delete block data: %w", err)
	}
	return nil
}

// DeleteCorruptBlock removes a corrupt block (header exists but transactions missing)
// and rolls back any UTXO changes that were applied when the block was originally processed.
// Unlike DeleteBlock, this doesn't need full transaction data - it reads tx hashes from the
// compact block header and uses height-based UTXO scanning to find affected entries.
// Returns (unspentCount, deletedUTXOCount, error).
func (bs *BinaryStorage) DeleteCorruptBlock(hash types.Hash, height uint32) (int, int, error) {
	// Step 1: Read compact block to get tx hashes (compact block stores only header + tx hashes)
	blockKey := BlockKey(hash)
	data, closer, err := bs.db.Get(blockKey)
	if err != nil {
		if err == pebble.ErrNotFound {
			// No compact block either - nothing to clean up for block data,
			// but still need to scan UTXOs by height
			return bs.deleteCorruptBlockByHeightScan(hash, height, nil)
		}
		return 0, 0, fmt.Errorf("failed to read compact block: %w", err)
	}

	compactBlock, err := DecodeCompactBlock(data)
	closer.Close()
	if err != nil {
		// Corrupt compact block data - fall back to height-only scan
		bs.logger.WithError(err).Warn("Failed to decode compact block, falling back to height-based scan")
		return bs.deleteCorruptBlockByHeightScan(hash, height, nil)
	}

	return bs.deleteCorruptBlockByHeightScan(hash, height, compactBlock.TxHashes)
}

// deleteCorruptBlockByHeightScan performs the actual UTXO rollback and block cleanup.
// txHashes may be nil if the compact block couldn't be read.
func (bs *BinaryStorage) deleteCorruptBlockByHeightScan(blockHash types.Hash, height uint32, txHashes []types.Hash) (int, int, error) {
	batch := bs.db.NewBatch()
	defer batch.Close()

	unspentCount := 0
	deletedCount := 0

	// Build a set of tx hashes from the compact block for fast lookup
	txHashSet := make(map[types.Hash]struct{}, len(txHashes))
	for _, h := range txHashes {
		txHashSet[h] = struct{}{}
	}

	// Step 2: Delete created UTXOs (outputs of this block's transactions)
	// If we have tx hashes, do targeted scans per tx hash
	if len(txHashes) > 0 {
		for _, txHash := range txHashes {
			utxoPrefix := make([]byte, 33)
			utxoPrefix[0] = PrefixUTXOExist
			copy(utxoPrefix[1:], txHash[:])

			iter, err := bs.db.NewIter(&pebble.IterOptions{
				LowerBound: utxoPrefix,
				UpperBound: NextPrefix(utxoPrefix),
			})
			if err != nil {
				return 0, 0, fmt.Errorf("failed to create UTXO iterator for tx %s: %w", txHash.String(), err)
			}

			for iter.First(); iter.Valid(); iter.Next() {
				val := iter.Value()
				if len(val) < 8 {
					continue
				}

				utxoHeight := binary.LittleEndian.Uint32(val[0:4])
				if utxoHeight != height {
					continue
				}

				// This UTXO was created by this block - delete it
				utxoData, decErr := DecodeUTXOData(val)
				if decErr == nil && utxoData != nil {
					// Delete address UTXO index entry
					key := iter.Key()
					idx := binary.LittleEndian.Uint32(key[33:37])
					addrKey := AddressUTXOKey(utxoData.ScriptHash, txHash, idx)
					batch.Delete(addrKey, nil)
				}

				keyCopy := make([]byte, len(iter.Key()))
				copy(keyCopy, iter.Key())
				batch.Delete(keyCopy, nil)
				deletedCount++
			}
			iter.Close()
		}
	}

	// Step 3: Unspend UTXOs that were spent by this block's transactions
	// Full scan of UTXO existence index for SpendingHeight == height
	utxoPrefix := []byte{PrefixUTXOExist}
	iter, err := bs.db.NewIter(&pebble.IterOptions{
		LowerBound: utxoPrefix,
		UpperBound: NextPrefix(utxoPrefix),
	})
	if err != nil {
		return 0, 0, fmt.Errorf("failed to create UTXO scan iterator: %w", err)
	}

	for iter.First(); iter.Valid(); iter.Next() {
		val := iter.Value()
		if len(val) < 8 {
			continue
		}

		spendingHeight := binary.LittleEndian.Uint32(val[4:8])
		if spendingHeight != height {
			continue
		}

		// Check if this UTXO's tx hash is in the compact block's tx set
		// (i.e. created AND spent by the same block) - already handled by step 2 deletion
		key := iter.Key()
		if len(key) >= 33 {
			var utxoTxHash types.Hash
			copy(utxoTxHash[:], key[1:33])
			if _, isCreatedByBlock := txHashSet[utxoTxHash]; isCreatedByBlock {
				// Created by this block, check if Height also matches
				creationHeight := binary.LittleEndian.Uint32(val[0:4])
				if creationHeight == height {
					// Already deleted in step 2, skip
					continue
				}
			}
		}

		// This UTXO was spent by a tx in the corrupt block - unspend it
		utxoData, decErr := DecodeUTXOData(val)
		if decErr != nil {
			continue
		}

		utxoData.SpendingHeight = 0
		utxoData.SpendingTxHash = types.Hash{}

		encoded, encErr := EncodeUTXOData(utxoData)
		if encErr != nil {
			continue
		}

		keyCopy := make([]byte, len(key))
		copy(keyCopy, key)
		batch.Set(keyCopy, encoded, nil)

		// Restore address UTXO index entry (spent UTXOs were removed from balance)
		if len(key) >= 37 {
			var txH types.Hash
			copy(txH[:], key[1:33])
			idx := binary.LittleEndian.Uint32(key[33:37])
			addrUTXOKey := AddressUTXOKey(utxoData.ScriptHash, txH, idx)
			valueBytes := make([]byte, 8)
			binary.LittleEndian.PutUint64(valueBytes, utxoData.Value)
			batch.Set(addrUTXOKey, valueBytes, nil)
		}

		unspentCount++
	}
	iter.Close()

	// Step 4: If we don't have tx hashes, also scan for created UTXOs by height
	// (fallback when compact block was unreadable)
	if len(txHashes) == 0 {
		iter2, err := bs.db.NewIter(&pebble.IterOptions{
			LowerBound: utxoPrefix,
			UpperBound: NextPrefix(utxoPrefix),
		})
		if err != nil {
			return 0, 0, fmt.Errorf("failed to create UTXO fallback scan iterator: %w", err)
		}

		for iter2.First(); iter2.Valid(); iter2.Next() {
			val := iter2.Value()
			if len(val) < 8 {
				continue
			}

			creationHeight := binary.LittleEndian.Uint32(val[0:4])
			if creationHeight != height {
				continue
			}

			// Note: If SpendingHeight == height, this UTXO was both created and spent
			// in the same block. Step 3 may have already written an unspent version
			// to the batch, but the Delete below supersedes it (last batch op wins).

			utxoData, decErr := DecodeUTXOData(val)
			if decErr == nil && utxoData != nil {
				key := iter2.Key()
				if len(key) >= 37 {
					var txH types.Hash
					copy(txH[:], key[1:33])
					idx := binary.LittleEndian.Uint32(key[33:37])
					addrKey := AddressUTXOKey(utxoData.ScriptHash, txH, idx)
					batch.Delete(addrKey, nil)
				}
			}

			keyCopy := make([]byte, len(iter2.Key()))
			copy(keyCopy, iter2.Key())
			batch.Delete(keyCopy, nil)
			deletedCount++
		}
		iter2.Close()
	}

	// Step 5: Delete compact block data
	batch.Delete(BlockKey(blockHash), nil)

	// Step 6: Delete block indexes
	batch.Delete(HeightToHashKey(height), nil)
	batch.Delete(HashToHeightKey(blockHash), nil)

	// Step 7: Delete address history entries for this height
	addressHistoryPrefix := []byte{PrefixAddressHistory}
	histIter, err := bs.db.NewIter(&pebble.IterOptions{
		LowerBound: addressHistoryPrefix,
		UpperBound: NextPrefix(addressHistoryPrefix),
	})
	if err != nil {
		return 0, 0, fmt.Errorf("failed to create address history iterator: %w", err)
	}
	for histIter.First(); histIter.Valid(); histIter.Next() {
		key := histIter.Key()
		// Key format: 0x05 + scriptHash(20) + height(4) + txHash(32) + txIndex(2)
		if len(key) >= 25 {
			entryHeight := binary.LittleEndian.Uint32(key[21:25])
			if entryHeight == height {
				keyCopy := make([]byte, len(key))
				copy(keyCopy, key)
				batch.Delete(keyCopy, nil)
			}
		}
	}
	histIter.Close()

	// Step 8: Delete any remaining transaction entries (they might partially exist)
	for _, txHash := range txHashes {
		batch.Delete(TransactionKey(txHash), nil)
		batch.Delete(MempoolTransactionKey(txHash), nil)
	}

	// Commit batch
	if err := batch.Commit(nil); err != nil {
		return 0, 0, fmt.Errorf("failed to commit corrupt block rollback: %w", err)
	}

	// Sync storage
	if err := bs.Sync(); err != nil {
		return 0, 0, fmt.Errorf("failed to sync after corrupt block rollback: %w", err)
	}

	return unspentCount, deletedCount, nil
}

// tryGetTransaction attempts to retrieve a transaction from blockchain first, then mempool.
// This consolidates the common lookup pattern used by GetTransaction and GetTransactionBatch.
// Returns the transaction, a boolean indicating if it was found in blockchain (vs mempool), and an error.
func (bs *BinaryStorage) tryGetTransaction(hash types.Hash) (*types.Transaction, bool, error) {
	// Try blockchain transactions first (0x04 prefix) - most common case
	txKey := TransactionKey(hash)
	val, closer, err := bs.db.Get(txKey)
	if err == nil {
		txData, decodeErr := DecodeTransactionData(val)
		closer.Close()
		if decodeErr != nil {
			return nil, false, fmt.Errorf("failed to decode blockchain transaction: %w", decodeErr)
		}
		return txData.TxData, true, nil
	} else if err != pebble.ErrNotFound {
		return nil, false, err
	}

	// Not in blockchain, check mempool (0x11 prefix)
	mempoolKey := MempoolTransactionKey(hash)
	val, closer, err = bs.db.Get(mempoolKey)
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, false, storage.NewStorageError("TX_NOT_FOUND", "transaction not found in blockchain or mempool", nil)
		}
		return nil, false, err
	}

	// Mempool transactions are stored as raw serialized data
	tx, deserializeErr := types.DeserializeTransaction(val)
	closer.Close()
	if deserializeErr != nil {
		return nil, false, fmt.Errorf("failed to decode mempool transaction: %w", deserializeErr)
	}

	return tx, false, nil
}

// StoreTransaction stores a mempool transaction (not yet in blocks)
func (bs *BinaryStorage) StoreTransaction(tx *types.Transaction) error {
	// Store in mempool namespace (0x11 prefix) with direct serialization
	txHash := tx.Hash()
	data, err := tx.Serialize()
	if err != nil {
		return fmt.Errorf("failed to serialize mempool transaction: %w", err)
	}

	key := MempoolTransactionKey(txHash)
	if err := bs.db.Set(key, data, nil); err != nil {
		return fmt.Errorf("failed to store mempool transaction: %w", err)
	}

	return nil
}

// GetTransaction retrieves a transaction by hash (checks both blockchain and mempool)
func (bs *BinaryStorage) GetTransaction(hash types.Hash) (*types.Transaction, error) {
	tx, _, err := bs.tryGetTransaction(hash)
	return tx, err
}

// GetTransactionData retrieves a transaction with block location metadata
func (bs *BinaryStorage) GetTransactionData(hash types.Hash) (*storage.TransactionData, error) {
	// Only check blockchain transactions (0x04 prefix) - mempool transactions don't have block location
	txKey := TransactionKey(hash)
	val, closer, err := bs.db.Get(txKey)
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, storage.NewStorageError("TX_NOT_FOUND", "transaction not found in blockchain", nil)
		}
		return nil, err
	}
	defer closer.Close()

	// Decode transaction data with location info
	txData, err := DecodeTransactionData(val)
	if err != nil {
		return nil, fmt.Errorf("failed to decode transaction data: %w", err)
	}

	// Convert to storage.TransactionData
	return &storage.TransactionData{
		BlockHash: txData.BlockHash,
		Height:    txData.Height,
		TxIndex:   txData.TxIndex,
		TxData:    txData.TxData,
	}, nil
}

// HasTransaction checks if a transaction exists (in blockchain or mempool)
func (bs *BinaryStorage) HasTransaction(hash types.Hash) (bool, error) {
	// Check blockchain transactions first (0x04 prefix)
	txKey := TransactionKey(hash)
	_, closer, err := bs.db.Get(txKey)
	if err == nil {
		closer.Close()
		return true, nil
	} else if err != pebble.ErrNotFound {
		return false, err
	}

	// Check mempool transactions (0x11 prefix)
	mempoolKey := MempoolTransactionKey(hash)
	_, closer, err = bs.db.Get(mempoolKey)
	if err == nil {
		closer.Close()
		return true, nil
	} else if err == pebble.ErrNotFound {
		return false, nil
	}
	return false, err
}

// DeleteMempoolTransaction removes a transaction from mempool storage
// Called when transaction is included in a block or rejected
func (bs *BinaryStorage) DeleteMempoolTransaction(hash types.Hash) error {
	key := MempoolTransactionKey(hash)
	if err := bs.db.Delete(key, nil); err != nil {
		// Not an error if transaction doesn't exist in mempool
		if err != pebble.ErrNotFound {
			return fmt.Errorf("failed to delete mempool transaction: %w", err)
		}
	}
	return nil
}

// GetTransactionBatch retrieves multiple transactions efficiently in a single operation
// This avoids N+1 query problem when loading blocks with many transactions
func (bs *BinaryStorage) GetTransactionBatch(hashes []types.Hash) ([]*types.Transaction, error) {
	if len(hashes) == 0 {
		return []*types.Transaction{}, nil
	}

	transactions := make([]*types.Transaction, len(hashes))

	for i, txHash := range hashes {
		tx, _, err := bs.tryGetTransaction(txHash)
		if err != nil {
			return nil, fmt.Errorf("transaction %d: %w", i, err)
		}
		transactions[i] = tx
	}

	return transactions, nil
}

// GetBlockHeight retrieves the height of a block by its hash
func (bs *BinaryStorage) GetBlockHeight(hash types.Hash) (uint32, error) {
	// Check if database is still valid
	if bs.db == nil {
		return 0, fmt.Errorf("storage database is not initialized")
	}

	key := HashToHeightKey(hash)
	data, closer, err := bs.db.Get(key)
	if err != nil {
		if err == pebble.ErrNotFound {
			return 0, storage.NewStorageError("BLOCK_NOT_INDEXED", "block height not found", nil)
		}
		return 0, fmt.Errorf("failed to read block height: %w", err)
	}
	defer closer.Close()

	if len(data) != 4 {
		return 0, fmt.Errorf("invalid height data length: %d", len(data))
	}

	height := binary.LittleEndian.Uint32(data)
	return height, nil
}

// GetBlockHashByHeight retrieves the hash of a block at a specific height
func (bs *BinaryStorage) GetBlockHashByHeight(height uint32) (types.Hash, error) {
	key := HeightToHashKey(height)
	data, closer, err := bs.db.Get(key)
	if err != nil {
		if err == pebble.ErrNotFound {
			return types.Hash{}, storage.NewStorageError("HEIGHT_NOT_FOUND", "no block at height", nil)
		}
		return types.Hash{}, fmt.Errorf("failed to read block hash: %w", err)
	}
	defer closer.Close()

	if len(data) != 32 {
		return types.Hash{}, fmt.Errorf("invalid hash data length: %d", len(data))
	}

	var hash types.Hash
	copy(hash[:], data)
	return hash, nil
}

// StoreBlock stores a block to the database
func (bs *BinaryStorage) StoreBlock(block *types.Block) error {
	// Create a single-use indexed batch to support potential reads during store
	batch := &BinaryBatch{
		storage: bs,
		batch:   bs.db.NewIndexedBatch(),
	}
	defer batch.batch.Close()

	if err := batch.StoreBlock(block); err != nil {
		return err
	}

	return batch.Commit()
}

// StoreBlockIndex stores the height index for a block
func (bs *BinaryStorage) StoreBlockIndex(hash types.Hash, height uint32) error {
	batch := bs.db.NewBatch()
	defer batch.Close()

	// Store hash -> height mapping
	hashToHeightKey := HashToHeightKey(hash)
	heightData := make([]byte, 4)
	binary.LittleEndian.PutUint32(heightData, height)
	if err := batch.Set(hashToHeightKey, heightData, nil); err != nil {
		return err
	}

	// Store height -> hash mapping
	heightToHashKey := HeightToHashKey(height)
	if err := batch.Set(heightToHashKey, hash[:], nil); err != nil {
		return err
	}

	return batch.Commit(nil)
}

// StoreUTXO stores a UTXO with proper interface signature
func (bs *BinaryStorage) StoreUTXO(outpoint types.Outpoint, output *types.TxOutput, height uint32, isCoinbase bool) error {
	// Convert to internal UTXO type
	utxo := &types.UTXO{
		Outpoint:   outpoint,
		Output:     output,
		Height:     height,
		IsCoinbase: isCoinbase,
	}

	// Use the existing implementation
	return bs.storeUTXOInternal(outpoint, utxo)
}

// storeUTXOInternal is the internal implementation
func (bs *BinaryStorage) storeUTXOInternal(outpoint types.Outpoint, utxo *types.UTXO) error {
	// Extract script hash for address indexing
	_, scriptHash := AnalyzeScript(utxo.Output.ScriptPubKey)

	// Create UTXO data
	utxoData := &UTXOData{
		Value:      uint64(utxo.Output.Value),
		ScriptHash: scriptHash,
		Height:     utxo.Height,
		IsCoinbase: utxo.IsCoinbase,
		Script:     utxo.Output.ScriptPubKey,
	}

	data, err := EncodeUTXOData(utxoData)
	if err != nil {
		return err
	}

	utxoKey := UTXOExistKey(outpoint.Hash, outpoint.Index)
	return bs.db.Set(utxoKey, data, nil)
}

// DeleteUTXOWithData removes a UTXO from storage with proper address index cleanup
func (bs *BinaryStorage) DeleteUTXOWithData(outpoint types.Outpoint, utxo *types.UTXO) error {
	utxoKey := UTXOExistKey(outpoint.Hash, outpoint.Index)

	// Delete from main UTXO index
	if err := bs.db.Delete(utxoKey, nil); err != nil {
		return err
	}

	// Delete from address UTXO index if we have valid output data
	if utxo != nil && utxo.Output != nil {
		_, scriptHash := AnalyzeScript(utxo.Output.ScriptPubKey)
		if scriptHash != [20]byte{} {
			addrKey := AddressUTXOKey(scriptHash, outpoint.Hash, outpoint.Index)
			if err := bs.db.Delete(addrKey, nil); err != nil {
				return fmt.Errorf("failed to delete address UTXO: %w", err)
			}
		}
	}

	return nil
}

// GetUTXO retrieves a UTXO by outpoint
func (bs *BinaryStorage) GetUTXO(outpoint types.Outpoint) (*types.UTXO, error) {
	key := UTXOExistKey(outpoint.Hash, outpoint.Index)
	val, closer, err := bs.db.Get(key)
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, storage.NewStorageError("UTXO_NOT_FOUND", "UTXO not found", nil)
		}
		return nil, err
	}
	defer closer.Close()

	utxoData, err := DecodeUTXOData(val)
	if err != nil {
		return nil, err
	}

	return &types.UTXO{
		Outpoint: outpoint,
		Output: &types.TxOutput{
			Value:        int64(utxoData.Value),
			ScriptPubKey: utxoData.Script,
		},
		Height:         utxoData.Height,
		IsCoinbase:     utxoData.IsCoinbase,
		SpendingHeight: utxoData.SpendingHeight,
		SpendingTxHash: utxoData.SpendingTxHash,
	}, nil
}

// ValidateUTXOSpend checks if a UTXO is already spent
// Returns (isSpent, spendingTxHash, error)
// This is used in the mark-as-spent model for double-spend detection
func (bs *BinaryStorage) ValidateUTXOSpend(outpoint types.Outpoint) (bool, types.Hash, error) {
	key := UTXOExistKey(outpoint.Hash, outpoint.Index)
	val, closer, err := bs.db.Get(key)
	if err != nil {
		if err == pebble.ErrNotFound {
			return false, types.Hash{}, storage.NewStorageError("UTXO_NOT_FOUND", "UTXO not found", nil)
		}
		return false, types.Hash{}, err
	}
	defer closer.Close()

	utxoData, err := DecodeUTXOData(val)
	if err != nil {
		return false, types.Hash{}, err
	}

	return utxoData.IsSpent(), utxoData.SpendingTxHash, nil
}

// GetUTXOsByAddress retrieves UTXOs for an address
func (bs *BinaryStorage) GetUTXOsByAddress(address string) ([]*types.UTXO, error) {
	// Decode address to validate it first
	decoded, err := crypto.Base58CheckDecode(address)
	if err != nil {
		bs.logger.WithFields(logrus.Fields{
			"address": address,
			"error":   err,
		}).Warn("Failed to decode address")
		return nil, fmt.Errorf("invalid address format: %w", err)
	}

	if len(decoded) != 21 {
		bs.logger.WithFields(logrus.Fields{
			"address": address,
			"length":  len(decoded),
		}).Warn("Address has invalid length (expected 21 bytes)")
		return nil, fmt.Errorf("invalid address length: expected 21 bytes, got %d", len(decoded))
	}

	// Extract script hash from decoded address
	var scriptHash [20]byte
	copy(scriptHash[:], decoded[1:21])

	utxos, err := bs.GetUTXOsByScriptHash(scriptHash)
	if err != nil {
		return nil, err
	}

	return utxos, nil
}

// GetUTXOsByScriptHash retrieves UTXOs by script hash
func (bs *BinaryStorage) GetUTXOsByScriptHash(scriptHash [20]byte) ([]*types.UTXO, error) {
	utxos := make([]*types.UTXO, 0)
	prefix := AddressUTXOPrefix(scriptHash)

	iter, err := bs.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: NextPrefix(prefix),
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	for iter.First(); iter.Valid(); iter.Next() {
		// Extract outpoint from key
		key := iter.Key()

		if len(key) < 57 { // prefix(1) + scripthash(20) + txhash(32) + index(4)
			continue
		}

		// Reconstruct outpoint from key components
		txHash := types.Hash{}
		copy(txHash[:], key[21:53]) // Skip prefix(1) + scripthash(20)
		index := binary.LittleEndian.Uint32(key[53:57])

		outpoint := types.Outpoint{
			Hash:  txHash,
			Index: index,
		}

		// Get the full UTXO data
		utxo, err := bs.GetUTXO(outpoint)
		if err != nil {
			continue // Skip if UTXO not found (shouldn't happen, but address index might be stale)
		}

		utxos = append(utxos, utxo)
	}

	return utxos, nil
}

// IterateHashToHeight iterates all hash→height index entries (0x03 prefix).
// Callback receives (blockHash, height). Return false to stop iteration.
func (bs *BinaryStorage) IterateHashToHeight(fn func(hash types.Hash, height uint32) bool) error {
	prefix := []byte{PrefixHashToHeight}
	iter, err := bs.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: NextPrefix(prefix),
	})
	if err != nil {
		return fmt.Errorf("failed to create hash-to-height iterator: %w", err)
	}
	defer iter.Close()

	for iter.First(); iter.Valid(); iter.Next() {
		key := iter.Key()
		val := iter.Value()

		// Key: [0x03][hash:32], Value: [height:4]
		if len(key) != 33 || len(val) != 4 {
			continue
		}

		var hash types.Hash
		copy(hash[:], key[1:33])
		height := binary.LittleEndian.Uint32(val)

		if !fn(hash, height) {
			break
		}
	}

	return nil
}

// Compact runs database compaction
func (bs *BinaryStorage) Compact() error {
	return bs.db.Compact(nil, nil, true)
}

// Sync flushes pending writes to disk
func (bs *BinaryStorage) Sync() error {
	return bs.db.Flush()
}

// NewBatch creates a new batch for atomic operations
// Uses NewIndexedBatch to support Get() operations during batch processing
// This is critical for intra-batch UTXO spending (UTXO created in block N, spent in block N+1)
func (bs *BinaryStorage) NewBatch() storage.Batch {
	return &BinaryBatch{
		storage: bs,
		batch:   bs.db.NewIndexedBatch(),
		size:    0,
	}
}

// GetStats returns database statistics
func (bs *BinaryStorage) GetStats() (*storage.DatabaseStats, error) {
	metrics := bs.db.Metrics()

	// Count different types of entries
	var blocks, txs, utxos int64
	iter, err := bs.db.NewIter(nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	for iter.First(); iter.Valid(); iter.Next() {
		key := iter.Key()
		if len(key) > 0 {
			switch key[0] {
			case PrefixBlock:
				blocks++
			case PrefixTransaction:
				txs++
			case PrefixUTXOExist:
				utxos++
			}
		}
	}

	return &storage.DatabaseStats{
		Size:         int64(metrics.DiskSpaceUsage()),
		Keys:         int64(blocks + txs + utxos), // Total counted keys
		Blocks:       blocks,
		Transactions: txs,
		UTXOs:        utxos,
	}, nil
}

// GetSize returns the size of the database
func (bs *BinaryStorage) GetSize() (int64, error) {
	metrics := bs.db.Metrics()
	return int64(metrics.DiskSpaceUsage()), nil
}

// GetChainHeight gets the current chain height
func (bs *BinaryStorage) GetChainHeight() (uint32, error) {
	key := ChainStateKey()
	val, closer, err := bs.db.Get(key)
	if err != nil {
		if err == pebble.ErrNotFound {
			return 0, nil
		}
		return 0, err
	}
	defer closer.Close()
	if len(val) < 4 {
		return 0, fmt.Errorf("invalid chain state data")
	}

	return binary.LittleEndian.Uint32(val[:4]), nil
}

// GetChainTip gets the chain tip hash
func (bs *BinaryStorage) GetChainTip() (types.Hash, error) {
	key := ChainStateKey()
	val, closer, err := bs.db.Get(key)
	if err != nil {
		if err == pebble.ErrNotFound {
			return types.Hash{}, nil
		}
		return types.Hash{}, err
	}
	defer closer.Close()

	if len(val) < 36 {
		return types.Hash{}, fmt.Errorf("invalid chain state data")
	}

	var tip types.Hash
	copy(tip[:], val[4:36])
	return tip, nil
}

// SetChainState sets the current chain height and tip hash atomically
func (bs *BinaryStorage) SetChainState(height uint32, hash types.Hash) error {
	var data [36]byte
	binary.LittleEndian.PutUint32(data[:4], height)
	copy(data[4:], hash[:])

	key := ChainStateKey()
	return bs.db.Set(key, data[:], nil)
}

// GetMoneySupply retrieves the money supply at a given height
func (bs *BinaryStorage) GetMoneySupply(height uint32) (int64, error) {
	key := MoneySupplyKey(height)
	value, closer, err := bs.db.Get(key)
	if err != nil {
		if err == pebble.ErrNotFound {
			return 0, nil // No money supply stored for this height
		}
		return 0, err
	}
	defer closer.Close()

	if len(value) != 8 {
		return 0, fmt.Errorf("invalid money supply data length: %d", len(value))
	}
	return int64(binary.LittleEndian.Uint64(value)), nil
}

// StoreMoneySupply stores the money supply at a given height
func (bs *BinaryStorage) StoreMoneySupply(height uint32, supply int64) error {
	key := MoneySupplyKey(height)
	value := make([]byte, 8)
	binary.LittleEndian.PutUint64(value, uint64(supply))
	return bs.db.Set(key, value, nil)
}

// StoreStakeModifier stores a stake modifier for a given block hash
func (bs *BinaryStorage) StoreStakeModifier(blockHash types.Hash, modifier uint64) error {
	key := StakeModifierKey(blockHash)
	value := make([]byte, 8)
	binary.LittleEndian.PutUint64(value, modifier)
	return bs.db.Set(key, value, nil)
}

// GetStakeModifier retrieves the stake modifier for a given block hash
func (bs *BinaryStorage) GetStakeModifier(blockHash types.Hash) (uint64, error) {
	key := StakeModifierKey(blockHash)
	value, closer, err := bs.db.Get(key)
	if err != nil {
		return 0, fmt.Errorf("stake modifier not found: %w", err)
	}
	defer closer.Close()

	if len(value) != 8 {
		return 0, fmt.Errorf("invalid stake modifier data length: %d", len(value))
	}
	return binary.LittleEndian.Uint64(value), nil
}

// HasStakeModifier checks if a stake modifier exists for a given block hash
func (bs *BinaryStorage) HasStakeModifier(blockHash types.Hash) (bool, error) {
	key := StakeModifierKey(blockHash)
	_, closer, err := bs.db.Get(key)
	if err != nil {
		if err == pebble.ErrNotFound {
			return false, nil
		}
		return false, err
	}
	closer.Close()
	return true, nil
}

// DeleteStakeModifier removes a stake modifier for a given block hash
func (bs *BinaryStorage) DeleteStakeModifier(blockHash types.Hash) error {
	key := StakeModifierKey(blockHash)
	err := bs.db.Delete(key, nil)
	if err != nil && err != pebble.ErrNotFound {
		return err
	}
	return nil
}

// StoreBlockPoSMetadata stores PoS checksum chain metadata (checksum + proofHash) for a block
// Format: checksum:4 + proofHash:32 = 36 bytes total
func (bs *BinaryStorage) StoreBlockPoSMetadata(blockHash types.Hash, checksum uint32, proofHash types.Hash) error {
	key := BlockPoSMetadataKey(blockHash)
	value := make([]byte, 36) // 4 + 32
	binary.LittleEndian.PutUint32(value[0:4], checksum)
	copy(value[4:36], proofHash[:])
	return bs.db.Set(key, value, nil)
}

// GetBlockPoSMetadata retrieves PoS checksum chain metadata for a block
// Returns checksum, proofHash (kernel hash), and error
func (bs *BinaryStorage) GetBlockPoSMetadata(blockHash types.Hash) (uint32, types.Hash, error) {
	key := BlockPoSMetadataKey(blockHash)
	value, closer, err := bs.db.Get(key)
	if err != nil {
		return 0, types.ZeroHash, fmt.Errorf("PoS metadata not found: %w", err)
	}
	defer closer.Close()

	if len(value) != 36 {
		return 0, types.ZeroHash, fmt.Errorf("invalid PoS metadata length: %d", len(value))
	}

	checksum := binary.LittleEndian.Uint32(value[0:4])
	var proofHash types.Hash
	copy(proofHash[:], value[4:36])

	return checksum, proofHash, nil
}

// HasBlockPoSMetadata checks if PoS metadata exists for a block
func (bs *BinaryStorage) HasBlockPoSMetadata(blockHash types.Hash) (bool, error) {
	key := BlockPoSMetadataKey(blockHash)
	_, closer, err := bs.db.Get(key)
	if err != nil {
		if err == pebble.ErrNotFound {
			return false, nil
		}
		return false, err
	}
	closer.Close()
	return true, nil
}

// Helper functions

// HashAddressToScriptHash converts an address string to a script hash
func HashAddressToScriptHash(address string) [20]byte {
	// Decode the TWINS address to extract hash160
	decoded, err := crypto.Base58CheckDecode(address)
	if err != nil || len(decoded) != 21 {
		// Return zero hash if address is invalid
		return [20]byte{}
	}

	// decoded is: [netID (1 byte)][hash160 (20 bytes)]
	var hash [20]byte
	copy(hash[:], decoded[1:21])
	return hash
}

// NextPrefix returns the next prefix for range scans
func NextPrefix(prefix []byte) []byte {
	next := make([]byte, len(prefix))
	copy(next, prefix)
	for i := len(next) - 1; i >= 0; i-- {
		if next[i] < 0xFF {
			next[i]++
			break
		}
		next[i] = 0
		if i == 0 {
			// Overflow, append a byte
			next = append(next, 0)
		}
	}
	return next
}

// IndexTransactionByAddress stores address → transaction mapping
// This is a direct API - batch version is in batch.go
// addressBinary is the decoded address (netID + hash160 = 21 bytes)
func (bs *BinaryStorage) IndexTransactionByAddress(addressBinary []byte, txHash types.Hash, height uint32, txIndex uint32, value int64, isInput bool, blockHash types.Hash) error {
	if len(addressBinary) != 21 {
		return fmt.Errorf("invalid address binary length: %d", len(addressBinary))
	}

	var scriptHash [20]byte
	copy(scriptHash[:], addressBinary[1:21])

	// Create complete index entry
	entry := &AddressHistoryEntry{
		IsInput:   isInput,
		Value:     uint64(value), // Convert int64 to uint64
		BlockHash: blockHash,
	}

	data, err := EncodeAddressHistoryEntry(entry)
	if err != nil {
		return fmt.Errorf("failed to encode history entry: %w", err)
	}

	key := AddressHistoryKey(scriptHash, height, txHash, uint16(txIndex))
	return bs.db.Set(key, data, nil)
}

// GetTransactionsByAddress retrieves all transactions for a specific address
// addressBinary is the decoded address (netID + hash160 = 21 bytes)
func (bs *BinaryStorage) GetTransactionsByAddress(addressBinary []byte) ([]storage.AddressTransaction, error) {
	if len(addressBinary) != 21 {
		return nil, fmt.Errorf("invalid address binary length: %d", len(addressBinary))
	}

	// Extract script hash (skip netID byte)
	var scriptHash [20]byte
	copy(scriptHash[:], addressBinary[1:21])

	// Use binary address history prefix
	prefix := AddressHistoryPrefix(scriptHash)

	iter, err := bs.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: NextPrefix(prefix),
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	var transactions []storage.AddressTransaction
	for iter.First(); iter.Valid(); iter.Next() {
		// Key format: [0x05][scripthash:20][height:4][txhash:32][index:2]
		key := iter.Key()
		if len(key) != 59 {
			continue // Invalid key
		}

		// Extract height (bytes 21-25)
		height := binary.LittleEndian.Uint32(key[21:25])

		// Extract txHash (bytes 25-57)
		var txHash types.Hash
		copy(txHash[:], key[25:57])

		// Extract txIndex (bytes 57-59)
		txIndex := uint32(binary.LittleEndian.Uint16(key[57:59]))

		transactions = append(transactions, storage.AddressTransaction{
			TxHash:  txHash,
			Height:  height,
			TxIndex: txIndex,
		})
	}

	return transactions, nil
}

// DeleteAddressIndex removes address index entry (for reorg handling)
// addressBinary is the decoded address (netID + hash160 = 21 bytes)
func (bs *BinaryStorage) DeleteAddressIndex(addressBinary []byte, txHash types.Hash) error {
	if len(addressBinary) != 21 {
		return fmt.Errorf("invalid address binary length: %d", len(addressBinary))
	}

	var scriptHash [20]byte
	copy(scriptHash[:], addressBinary[1:21])

	// Delete all history entries for this address+tx combination
	// We need to scan because we don't know the height and txIndex
	prefix := AddressHistoryPrefix(scriptHash)

	iter, err := bs.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: NextPrefix(prefix),
	})
	if err != nil {
		return err
	}
	defer iter.Close()

	// Collect keys to delete
	var keysToDelete [][]byte
	for iter.First(); iter.Valid(); iter.Next() {
		key := iter.Key()
		if len(key) != 59 {
			continue
		}

		// Extract txHash from key (bytes 25-57)
		var keyTxHash types.Hash
		copy(keyTxHash[:], key[25:57])

		// If this is the transaction we're looking for, mark for deletion
		if keyTxHash == txHash {
			keyCopy := make([]byte, len(key))
			copy(keyCopy, key)
			keysToDelete = append(keysToDelete, keyCopy)
		}
	}

	// Delete collected keys
	for _, key := range keysToDelete {
		if err := bs.db.Delete(key, nil); err != nil {
			return err
		}
	}

	return nil
}

// Close closes the database connection
func (bs *BinaryStorage) Close() error {
	if bs.db != nil {
		return bs.db.Close()
	}
	return nil
}

// GetDB returns the underlying Pebble database
// This is needed for low-level operations like index rebuilding
func (bs *BinaryStorage) GetDB() *pebble.DB {
	return bs.db
}

// GetBlockContainingTx returns the block that contains a given transaction
func (bs *BinaryStorage) GetBlockContainingTx(txHash types.Hash) (*types.Block, error) {
	// Get transaction data which includes block location
	key := TransactionKey(txHash)
	data, closer, err := bs.db.Get(key)
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, fmt.Errorf("transaction %x not found", txHash)
		}
		return nil, err
	}
	defer closer.Close()

	// Decode transaction data to get block hash
	txData, err := DecodeTransactionData(data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode transaction data: %w", err)
	}

	// Now get the block containing this transaction
	return bs.GetBlock(txData.BlockHash)
}

// MarkBlockInvalid marks a block as invalid in the database
func (bs *BinaryStorage) MarkBlockInvalid(hash types.Hash) error {
	key := InvalidBlockKey(hash)
	// Store current timestamp as the value (8 bytes)
	timestamp := uint64(time.Now().Unix())
	var data [8]byte
	binary.LittleEndian.PutUint64(data[:], timestamp)
	return bs.db.Set(key, data[:], nil)
}

// RemoveBlockInvalid removes the invalid status from a block
func (bs *BinaryStorage) RemoveBlockInvalid(hash types.Hash) error {
	key := InvalidBlockKey(hash)
	return bs.db.Delete(key, nil)
}

// IsBlockInvalid checks if a block is marked as invalid
func (bs *BinaryStorage) IsBlockInvalid(hash types.Hash) (bool, error) {
	key := InvalidBlockKey(hash)
	_, closer, err := bs.db.Get(key)
	if err != nil {
		if err == pebble.ErrNotFound {
			return false, nil
		}
		return false, err
	}
	closer.Close()
	return true, nil
}

// GetInvalidBlocks returns all blocks marked as invalid
func (bs *BinaryStorage) GetInvalidBlocks() ([]types.Hash, error) {
	var invalidBlocks []types.Hash

	// Create iterator for invalid block prefix
	prefix := []byte{PrefixInvalidBlock}
	iter, err := bs.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: NextPrefix(prefix),
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	for iter.First(); iter.Valid(); iter.Next() {
		key := iter.Key()
		if len(key) == 33 { // prefix + hash
			var hash types.Hash
			copy(hash[:], key[1:])
			invalidBlocks = append(invalidBlocks, hash)
		}
	}

	return invalidBlocks, nil
}

// AddDynamicCheckpoint adds a dynamic checkpoint to the database
func (bs *BinaryStorage) AddDynamicCheckpoint(height uint32, hash types.Hash) error {
	key := DynamicCheckpointKey(height)
	return bs.db.Set(key, hash[:], nil)
}

// RemoveDynamicCheckpoint removes a dynamic checkpoint from the database
func (bs *BinaryStorage) RemoveDynamicCheckpoint(height uint32) error {
	key := DynamicCheckpointKey(height)
	return bs.db.Delete(key, nil)
}

// GetDynamicCheckpoint retrieves a dynamic checkpoint by height
func (bs *BinaryStorage) GetDynamicCheckpoint(height uint32) (types.Hash, error) {
	key := DynamicCheckpointKey(height)
	data, closer, err := bs.db.Get(key)
	if err != nil {
		if err == pebble.ErrNotFound {
			return types.Hash{}, fmt.Errorf("checkpoint at height %d not found", height)
		}
		return types.Hash{}, err
	}
	defer closer.Close()

	if len(data) != 32 {
		return types.Hash{}, fmt.Errorf("invalid checkpoint data length: %d", len(data))
	}

	var hash types.Hash
	copy(hash[:], data)
	return hash, nil
}

// GetAllDynamicCheckpoints retrieves all dynamic checkpoints
func (bs *BinaryStorage) GetAllDynamicCheckpoints() (map[uint32]types.Hash, error) {
	checkpoints := make(map[uint32]types.Hash)

	// Create iterator for dynamic checkpoint prefix
	prefix := []byte{PrefixDynamicCheckpoint}
	iter, err := bs.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: NextPrefix(prefix),
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	for iter.First(); iter.Valid(); iter.Next() {
		key := iter.Key()
		if len(key) == 5 { // prefix + height
			height := binary.LittleEndian.Uint32(key[1:])

			value := iter.Value()
			if len(value) == 32 {
				var hash types.Hash
				copy(hash[:], value)
				checkpoints[height] = hash
			}
		}
	}

	return checkpoints, nil
}

// RawGet retrieves raw data by key (implements RawStorage interface for consensus.PaymentVoteDB)
// Returns nil, nil if key not found
func (bs *BinaryStorage) RawGet(key []byte) ([]byte, error) {
	data, closer, err := bs.db.Get(key)
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, nil // Return nil for not found (not an error for this use case)
		}
		return nil, err
	}
	defer closer.Close()

	// Copy data since it's only valid until closer.Close()
	result := make([]byte, len(data))
	copy(result, data)
	return result, nil
}

// RawSet stores raw key-value pair (implements RawStorage interface)
func (bs *BinaryStorage) RawSet(key, value []byte) error {
	return bs.db.Set(key, value, pebble.Sync)
}

// RawDelete removes a key (implements RawStorage interface)
func (bs *BinaryStorage) RawDelete(key []byte) error {
	return bs.db.Delete(key, pebble.Sync)
}

// RawIterPrefix iterates over keys with given prefix (implements RawStorage interface)
// The callback receives key and value; return false to stop iteration
func (bs *BinaryStorage) RawIterPrefix(prefix []byte, fn func(key, value []byte) bool) error {
	iter, err := bs.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: NextPrefix(prefix),
	})
	if err != nil {
		return err
	}
	defer iter.Close()

	for iter.First(); iter.Valid(); iter.Next() {
		if !fn(iter.Key(), iter.Value()) {
			break
		}
	}

	return nil
}
