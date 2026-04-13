package binary

import (
	"encoding/binary"
	"fmt"

	"github.com/cockroachdb/pebble"

	"github.com/twins-dev/twins-core/internal/storage"
	"github.com/twins-dev/twins-core/pkg/types"
)

// isStorageNotFound checks if an error is a storage "not found" error
// (as opposed to IO/decode errors which should fail closed).
func isStorageNotFound(err error) bool {
	if storageErr, ok := err.(*storage.StorageError); ok {
		switch storageErr.Code {
		case "NOT_FOUND", "HEIGHT_NOT_FOUND", "TX_NOT_FOUND",
			"BLOCK_NOT_FOUND", "UTXO_NOT_FOUND":
			return true
		}
	}
	return false
}

// BinaryBatch implements the storage.Batch interface for atomic operations
type BinaryBatch struct {
	storage *BinaryStorage
	batch   *pebble.Batch
	size    int
}

// StoreBlock stores a block in the batch
func (b *BinaryBatch) StoreBlock(block *types.Block) error {
	if block == nil || block.Header == nil {
		return fmt.Errorf("invalid block: nil header")
	}

	blockHash := block.Hash()

	// Get the height from the block index (which must be stored BEFORE calling this)
	// This ensures we use the same height as the index, avoiding mismatches
	newHeight, err := b.storage.GetBlockHeight(blockHash)
	if err != nil {
		return fmt.Errorf("failed to get block height from index (must call StoreBlockIndex first): %w", err)
	}

	return b.StoreBlockWithHeight(block, newHeight)
}

// StoreBlockWithHeight stores a block in the batch with a known height
// This is used during batch processing where the height is already known
// Phase 1 improvement: Stores compact block + separate transaction storage for O(1) access
func (b *BinaryBatch) StoreBlockWithHeight(block *types.Block, newHeight uint32) error {
	if block == nil {
		return fmt.Errorf("invalid block: nil")
	}

	blockHash := block.Hash()

	// Create compact block (header + tx hashes only, not full transactions)
	// This reduces storage size and improves block retrieval performance
	compact := &CompactBlock{
		Height:    newHeight,
		Version:   block.Header.Version,
		PrevBlock: block.Header.PrevBlockHash,
		Merkle:    block.Header.MerkleRoot,
		Timestamp: block.Header.Timestamp,
		Bits:      block.Header.Bits,
		Nonce:     block.Header.Nonce,
		StakeMod:  0, // PoS fields would come from block metadata, not header
		StakeTime: 0, // PoS fields would come from block metadata, not header
		TxCount:   uint32(len(block.Transactions)),
		TxHashes:  make([]types.Hash, len(block.Transactions)),
		Signature: block.Signature, // PoS block signature (required for validation)
	}

	for i, tx := range block.Transactions {
		compact.TxHashes[i] = tx.Hash()
	}

	// Store compact block data (0x01 prefix - new schema)
	blockKey := BlockKey(blockHash)
	blockData, err := EncodeCompactBlock(compact)
	if err != nil {
		return fmt.Errorf("failed to serialize block: %w", err)
	}
	if err := b.batch.Set(blockKey, blockData, nil); err != nil {
		return err
	}
	b.size += len(blockKey) + len(blockData)

	// Store transactions with direct access (0x04 prefix - new schema)
	// This is the KEY IMPROVEMENT in Phase 1: O(1) transaction retrieval
	for i, tx := range block.Transactions {
		// Each transaction is stored with its block location
		// This allows GetTransaction to work without deserializing the entire block
		txData := &TransactionData{
			BlockHash: blockHash,
			Height:    newHeight,
			TxIndex:   uint32(i),
			TxData:    tx,
		}

		data, err := EncodeTransactionData(txData)
		if err != nil {
			return fmt.Errorf("failed to encode transaction %d: %w", i, err)
		}

		txKey := TransactionKey(tx.Hash())
		if err := b.batch.Set(txKey, data, nil); err != nil {
			return err
		}
		b.size += len(txKey) + len(data)
	}

	return nil
}

// StoreTransaction stores a mempool transaction in the batch
// Mempool transactions use prefix 0x11 and are stored without block location data.
// When the transaction is included in a block, it will be stored with prefix 0x04
// via StoreBlockWithHeight with full location data, and the mempool entry can be deleted.
func (b *BinaryBatch) StoreTransaction(tx *types.Transaction) error {
	txHash := tx.Hash()

	// For mempool transactions, we only store the transaction data itself
	// No need for TransactionData wrapper since there's no block location
	data, err := tx.Serialize()
	if err != nil {
		return fmt.Errorf("failed to serialize mempool transaction: %w", err)
	}

	// Use mempool prefix (0x11) instead of regular transaction prefix (0x04)
	key := MempoolTransactionKey(txHash)
	if err := b.batch.Set(key, data, nil); err != nil {
		return err
	}
	b.size += len(key) + len(data)

	return nil
}

// StoreUTXO stores a UTXO in the batch
func (b *BinaryBatch) StoreUTXO(outpoint types.Outpoint, output *types.TxOutput, height uint32, isCoinbase bool) error {
	// Extract script hash for address verification
	_, scriptHash := AnalyzeScript(output.ScriptPubKey)

	utxo := &UTXOData{
		Value:      uint64(output.Value), // Convert int64 to uint64
		ScriptHash: scriptHash,
		Height:     height,
		IsCoinbase: isCoinbase,
		Script:     output.ScriptPubKey,
	}

	data, err := EncodeUTXOData(utxo)
	if err != nil {
		return fmt.Errorf("failed to encode UTXO: %w", err)
	}

	key := UTXOExistKey(outpoint.Hash, outpoint.Index)
	if err := b.batch.Set(key, data, nil); err != nil {
		return fmt.Errorf("failed to store UTXO: %w", err)
	}
	b.size += len(key) + len(data)

	// Also store in address UTXO index if we have a valid script hash
	if scriptHash != [20]byte{} {
		addrKey := AddressUTXOKey(scriptHash, outpoint.Hash, outpoint.Index)
		var valueBuf [8]byte
		binary.LittleEndian.PutUint64(valueBuf[:], uint64(output.Value))
		if err := b.batch.Set(addrKey, valueBuf[:], nil); err != nil {
			return fmt.Errorf("failed to store address UTXO: %w", err)
		}
		b.size += len(addrKey) + 8
	}

	return nil
}

// DeleteUTXOWithData deletes a UTXO and its address index using provided UTXO data
// This is the preferred method when UTXO data is already available (e.g., from batch cache)
func (b *BinaryBatch) DeleteUTXOWithData(outpoint types.Outpoint, utxo *types.UTXO) error {
	key := UTXOExistKey(outpoint.Hash, outpoint.Index)

	// Delete from UTXO existence index
	if err := b.batch.Delete(key, nil); err != nil {
		return fmt.Errorf("failed to delete UTXO: %w", err)
	}
	b.size += len(key)

	// Delete from address UTXO index if we have a valid script
	if utxo != nil && utxo.Output != nil {
		_, scriptHash := AnalyzeScript(utxo.Output.ScriptPubKey)
		if scriptHash != [20]byte{} {
			addrKey := AddressUTXOKey(scriptHash, outpoint.Hash, outpoint.Index)
			if err := b.batch.Delete(addrKey, nil); err != nil {
				return fmt.Errorf("failed to delete address UTXO: %w", err)
			}
			b.size += len(addrKey)
		}
	}

	return nil
}

// MarkUTXOSpent marks a UTXO as spent without deleting it
// This is the key operation in the mark-as-spent model for faster block disconnect
// Returns the UTXO data for validation and address index updates
func (b *BinaryBatch) MarkUTXOSpent(outpoint types.Outpoint, spendingHeight uint32, spendingTxHash types.Hash) (*types.UTXO, error) {
	key := UTXOExistKey(outpoint.Hash, outpoint.Index)

	// Read current UTXO data from batch (includes uncommitted writes) + committed storage
	// This is critical for intra-batch UTXO spending where UTXO is created in block N
	// and spent in block N+1 within the same batch
	data, closer, err := b.batch.Get(key)
	if err != nil {
		// Detailed error diagnostics
		if err == pebble.ErrNotFound {
			// Check committed DB directly to understand the situation
			dbData, dbCloser, dbErr := b.storage.db.Get(key)
			if dbErr == nil {
				defer dbCloser.Close()
				// UTXO exists in committed DB - check if already spent
				if existingUTXO, decErr := DecodeUTXOData(dbData); decErr == nil {
					if existingUTXO.IsSpent() {
						// Check if the spending block/transaction still exists.
						// If the block was deleted by corrupt block recovery but UTXOs
						// were not rolled back, the spending reference is orphaned and
						// this UTXO should be allowed to be spent again.
						spentBlockHash, blockErr := b.storage.GetBlockHashByHeight(existingUTXO.SpendingHeight)
						txData, txErr := b.storage.GetTransactionData(existingUTXO.SpendingTxHash)

						// Fail closed on unexpected IO/decode errors
						if blockErr != nil && !isStorageNotFound(blockErr) {
							return nil, fmt.Errorf("failed to validate UTXO %s:%d spending reference: %w",
								outpoint.Hash.String(), outpoint.Index, blockErr)
						}
						if txErr != nil && !isStorageNotFound(txErr) {
							return nil, fmt.Errorf("failed to validate UTXO %s:%d spending reference: %w",
								outpoint.Hash.String(), outpoint.Index, txErr)
						}

						orphanedRef := false
						if blockErr != nil || txErr != nil || txData == nil {
							// Spending block or transaction no longer exists (not-found)
							orphanedRef = true
						} else if txData.BlockHash != spentBlockHash {
							// Transaction exists but is NOT in the block at the spending height.
							// Stale reference after fork recovery.
							orphanedRef = true
						}

						if orphanedRef {
							// Allow re-spend: reset to new spending info.
							existingUTXO.SpendingHeight = spendingHeight
							existingUTXO.SpendingTxHash = spendingTxHash

							newData, encErr := EncodeUTXOData(existingUTXO)
							if encErr != nil {
								return nil, fmt.Errorf("failed to re-encode UTXO for orphaned spend fix: %w", encErr)
							}

							if setErr := b.batch.Set(key, newData, nil); setErr != nil {
								return nil, fmt.Errorf("failed to update UTXO for orphaned spend fix: %w", setErr)
							}
							b.size += len(key) + len(newData)

							// Delete from address UTXO index (may already be absent)
							if existingUTXO.ScriptHash != [20]byte{} {
								addrKey := AddressUTXOKey(existingUTXO.ScriptHash, outpoint.Hash, outpoint.Index)
								b.batch.Delete(addrKey, nil)
								b.size += len(addrKey)
							}

							return &types.UTXO{
								Outpoint: outpoint,
								Output: &types.TxOutput{
									Value:        int64(existingUTXO.Value),
									ScriptPubKey: existingUTXO.Script,
								},
								Height:         existingUTXO.Height,
								IsCoinbase:     existingUTXO.IsCoinbase,
								SpendingHeight: spendingHeight,
								SpendingTxHash: spendingTxHash,
							}, nil
						}

						// Fork detection: same tx spending same UTXO at a different height
						// and the spending reference is still valid (not orphaned) — our chain
						// has a fork block at the old spending height.
						if existingUTXO.SpendingTxHash == spendingTxHash && existingUTXO.SpendingHeight != spendingHeight {
							return nil, fmt.Errorf("fork duplicate spend: UTXO %s:%d spent by same tx %s at height %d and %d [fork_height=%d]",
								outpoint.Hash.String(), outpoint.Index, spendingTxHash.String(),
								existingUTXO.SpendingHeight, spendingHeight, existingUTXO.SpendingHeight)
						}

						return nil, fmt.Errorf("UTXO %s:%d already spent in committed DB at height %d (block %s) by tx %s; current spend: height %d, tx %s",
							outpoint.Hash.String(), outpoint.Index,
							existingUTXO.SpendingHeight, spentBlockHash.String(), existingUTXO.SpendingTxHash.String(),
							spendingHeight, spendingTxHash.String())
					}
					return nil, fmt.Errorf("UTXO %s:%d exists in committed DB but not in batch (unexpected state)",
						outpoint.Hash.String(), outpoint.Index)
				}
				return nil, fmt.Errorf("UTXO %s:%d exists in committed DB but decode failed",
					outpoint.Hash.String(), outpoint.Index)
			}
			// Not in batch AND not in committed DB
			return nil, fmt.Errorf("UTXO %s:%d not found (never created or pruned)",
				outpoint.Hash.String(), outpoint.Index)
		}
		// Other pebble error (IO error, etc.)
		return nil, fmt.Errorf("failed to read UTXO %s:%d: %w",
			outpoint.Hash.String(), outpoint.Index, err)
	}
	defer closer.Close()

	// Decode existing UTXO
	utxoData, err := DecodeUTXOData(data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode UTXO: %w", err)
	}

	// Check if already spent
	if utxoData.IsSpent() {
		// Idempotent re-spend: same transaction at same height already marked this UTXO as spent.
		// This happens during crash recovery when a batch was partially committed -
		// UTXO spending was persisted but block processing didn't complete.
		if utxoData.SpendingHeight == spendingHeight && utxoData.SpendingTxHash == spendingTxHash {
			// Already spent by exactly this transaction - return as-is (no-op)
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

		// Validate spending reference BEFORE fork detection.
		// Critical: intermediate batch commits in processBatchUnified can overwrite
		// fork blocks with correct-chain blocks (updating height→hash), while the
		// old fork block's UTXO spending state remains in DB. Without checking
		// reference validity first, fork detection triggers an infinite rollback
		// loop — each rollback disconnects the correct chain's blocks (which don't
		// contain the fork tx), leaving stale UTXO spending state permanently stuck.
		spentBlockHash, blockErr := b.storage.GetBlockHashByHeight(utxoData.SpendingHeight)
		txData, txErr := b.storage.GetTransactionData(utxoData.SpendingTxHash)

		// Fail closed on unexpected IO/decode errors
		if blockErr != nil && !isStorageNotFound(blockErr) {
			return nil, fmt.Errorf("failed to validate UTXO %s:%d spending reference: %w",
				outpoint.Hash.String(), outpoint.Index, blockErr)
		}
		if txErr != nil && !isStorageNotFound(txErr) {
			return nil, fmt.Errorf("failed to validate UTXO %s:%d spending reference: %w",
				outpoint.Hash.String(), outpoint.Index, txErr)
		}

		orphanedRef := false
		if blockErr != nil || txErr != nil || txData == nil {
			// Spending block or transaction no longer exists (not-found)
			orphanedRef = true
		} else if txData.BlockHash != spentBlockHash {
			// Transaction exists but is NOT in the block at the spending height.
			// Block at this height was replaced with a different block after fork recovery.
			orphanedRef = true
		}

		if orphanedRef {
			// Stale spending reference — allow re-spend
			utxoData.SpendingHeight = 0
			utxoData.SpendingTxHash = types.Hash{}
			// Fall through to normal spend logic below
		} else if utxoData.SpendingTxHash == spendingTxHash && utxoData.SpendingHeight != spendingHeight {
			// Fork detection: same tx spending same UTXO at a different height,
			// AND the spending reference is still valid (block at fork height
			// actually contains this tx). Our chain has a fork block.
			return nil, fmt.Errorf("fork duplicate spend: UTXO %s:%d spent by same tx %s at height %d and %d [fork_height=%d]",
				outpoint.Hash.String(), outpoint.Index, spendingTxHash.String(),
				utxoData.SpendingHeight, spendingHeight, utxoData.SpendingHeight)
		} else {
			return nil, fmt.Errorf("UTXO %s:%d already spent at height %d (block %s) by tx %s; current spend: height %d, tx %s",
				outpoint.Hash.String(), outpoint.Index,
				utxoData.SpendingHeight, spentBlockHash.String(), utxoData.SpendingTxHash.String(),
				spendingHeight, spendingTxHash.String())
		}
	}

	// Update spending info
	utxoData.SpendingHeight = spendingHeight
	utxoData.SpendingTxHash = spendingTxHash

	// Re-encode with updated spending info
	newData, err := EncodeUTXOData(utxoData)
	if err != nil {
		return nil, fmt.Errorf("failed to encode updated UTXO: %w", err)
	}

	// Update in batch
	if err := b.batch.Set(key, newData, nil); err != nil {
		return nil, fmt.Errorf("failed to update UTXO: %w", err)
	}
	b.size += len(key) + len(newData)

	// Delete from address UTXO index (spent UTXOs shouldn't be in balance)
	if utxoData.ScriptHash != [20]byte{} {
		addrKey := AddressUTXOKey(utxoData.ScriptHash, outpoint.Hash, outpoint.Index)
		if err := b.batch.Delete(addrKey, nil); err != nil {
			return nil, fmt.Errorf("failed to delete address UTXO: %w", err)
		}
		b.size += len(addrKey)
	}

	// Convert to types.UTXO for return
	result := &types.UTXO{
		Outpoint: outpoint,
		Output: &types.TxOutput{
			Value:        int64(utxoData.Value),
			ScriptPubKey: utxoData.Script,
		},
		Height:         utxoData.Height,
		IsCoinbase:     utxoData.IsCoinbase,
		SpendingHeight: spendingHeight,
		SpendingTxHash: spendingTxHash,
	}

	return result, nil
}

// UnspendUTXO marks a spent UTXO as unspent again (for block disconnect)
// Resets SpendingHeight to 0 and SpendingTxHash to empty
func (b *BinaryBatch) UnspendUTXO(outpoint types.Outpoint) error {
	key := UTXOExistKey(outpoint.Hash, outpoint.Index)

	// Read current UTXO data from batch (includes uncommitted writes) + committed storage
	data, closer, err := b.batch.Get(key)
	if err != nil {
		return fmt.Errorf("UTXO not found for unspend: %s:%d", outpoint.Hash.String(), outpoint.Index)
	}
	defer closer.Close()

	// Decode existing UTXO
	utxoData, err := DecodeUTXOData(data)
	if err != nil {
		return fmt.Errorf("failed to decode UTXO: %w", err)
	}

	// Check if actually spent
	if utxoData.IsUnspent() {
		// Already unspent, nothing to do
		return nil
	}

	// Reset spending info
	utxoData.SpendingHeight = 0
	utxoData.SpendingTxHash = types.Hash{}

	// Re-encode with reset spending info
	newData, err := EncodeUTXOData(utxoData)
	if err != nil {
		return fmt.Errorf("failed to encode updated UTXO: %w", err)
	}

	// Update in batch
	if err := b.batch.Set(key, newData, nil); err != nil {
		return fmt.Errorf("failed to update UTXO: %w", err)
	}
	b.size += len(key) + len(newData)

	// Re-add to address UTXO index (unspent UTXOs should be in balance)
	if utxoData.ScriptHash != [20]byte{} {
		addrKey := AddressUTXOKey(utxoData.ScriptHash, outpoint.Hash, outpoint.Index)
		var valueBuf [8]byte
		binary.LittleEndian.PutUint64(valueBuf[:], utxoData.Value)
		if err := b.batch.Set(addrKey, valueBuf[:], nil); err != nil {
			return fmt.Errorf("failed to restore address UTXO: %w", err)
		}
		b.size += len(addrKey) + 8
	}

	return nil
}

// DeleteBlockDisconnect deletes block data and both index entries within the batch.
// Used by disconnectBlock so the entire disconnect (UTXO rollback + tx deletion +
// block removal + chain state update) is committed atomically.
func (b *BinaryBatch) DeleteBlockDisconnect(hash types.Hash, height uint32) error {
	// Delete block data (0x01 + hash)
	blockKey := BlockKey(hash)
	if err := b.batch.Delete(blockKey, nil); err != nil {
		return fmt.Errorf("failed to delete block data: %w", err)
	}
	b.size += len(blockKey)

	// Delete hash→height index (0x03 + hash)
	h2hKey := HashToHeightKey(hash)
	if err := b.batch.Delete(h2hKey, nil); err != nil {
		return fmt.Errorf("failed to delete hash→height index: %w", err)
	}
	b.size += len(h2hKey)

	// Delete height→hash index (0x02 + height)
	// Safe to delete unconditionally during disconnect: this block IS the main chain
	// block at this height, and the new block (if any) will re-write this key on connect.
	heightKey := HeightToHashKey(height)
	if err := b.batch.Delete(heightKey, nil); err != nil {
		return fmt.Errorf("failed to delete height→hash index: %w", err)
	}
	b.size += len(heightKey)

	return nil
}

// SetChainState sets the chain height and tip hash atomically in the batch
func (b *BinaryBatch) SetChainState(height uint32, hash types.Hash) error {
	var data [36]byte
	binary.LittleEndian.PutUint32(data[:4], height)
	copy(data[4:], hash[:])

	key := ChainStateKey()
	if err := b.batch.Set(key, data[:], nil); err != nil {
		return err
	}
	b.size += len(key) + 36
	return nil
}

// StoreMoneySupply stores the money supply at a given height in the batch
func (b *BinaryBatch) StoreMoneySupply(height uint32, supply int64) error {
	key := MoneySupplyKey(height)
	value := make([]byte, 8)
	binary.LittleEndian.PutUint64(value, uint64(supply))

	if err := b.batch.Set(key, value, nil); err != nil {
		return err
	}
	b.size += len(key) + 8
	return nil
}

// StoreBlockIndex stores block index information in the batch
func (b *BinaryBatch) StoreBlockIndex(hash types.Hash, height uint32) error {
	// Store height -> hash mapping
	heightKey := HeightToHashKey(height)
	if err := b.batch.Set(heightKey, hash[:], nil); err != nil {
		return err
	}
	b.size += len(heightKey) + 32

	// Store hash -> height mapping
	hashKey := HashToHeightKey(hash)
	var heightBuf [4]byte
	binary.LittleEndian.PutUint32(heightBuf[:], height)
	if err := b.batch.Set(hashKey, heightBuf[:], nil); err != nil {
		return err
	}
	b.size += len(hashKey) + 4

	return nil
}

// StoreStakeModifier stores a stake modifier in the batch
func (b *BinaryBatch) StoreStakeModifier(blockHash types.Hash, modifier uint64) error {
	key := StakeModifierKey(blockHash)
	value := make([]byte, 8)
	binary.LittleEndian.PutUint64(value, modifier)

	if err := b.batch.Set(key, value, nil); err != nil {
		return err
	}
	b.size += len(key) + 8
	return nil
}

// DeleteStakeModifier removes a stake modifier from the batch
func (b *BinaryBatch) DeleteStakeModifier(blockHash types.Hash) error {
	key := StakeModifierKey(blockHash)
	if err := b.batch.Delete(key, nil); err != nil {
		return err
	}
	return nil
}

// StoreBlockPoSMetadata stores PoS checksum chain metadata in the batch
// Format: checksum:4 + proofHash:32 = 36 bytes total
func (b *BinaryBatch) StoreBlockPoSMetadata(blockHash types.Hash, checksum uint32, proofHash types.Hash) error {
	key := BlockPoSMetadataKey(blockHash)
	value := make([]byte, 36) // 4 + 32
	binary.LittleEndian.PutUint32(value[0:4], checksum)
	copy(value[4:36], proofHash[:])

	if err := b.batch.Set(key, value, nil); err != nil {
		return err
	}
	b.size += len(key) + 36
	return nil
}

// Commit commits all batch operations atomically
func (b *BinaryBatch) Commit() error {
	if err := b.batch.Commit(nil); err != nil {
		return fmt.Errorf("batch commit failed: %w", err)
	}
	b.batch.Close()
	return nil
}

// Rollback discards all batch operations
func (b *BinaryBatch) Rollback() error {
	b.batch.Close()
	return nil
}

// Size returns the approximate size of the batch
func (b *BinaryBatch) Size() int {
	return b.size
}

// Reset clears the batch for reuse
func (b *BinaryBatch) Reset() {
	b.batch.Reset()
	b.size = 0
}

// SetRaw allows raw key-value insertion into the batch (for special cases like genesis block)
func (b *BinaryBatch) SetRaw(key, value []byte) error {
	if err := b.batch.Set(key, value, nil); err != nil {
		return err
	}
	b.size += len(key) + len(value)
	return nil
}

// DeleteTransaction deletes a transaction from the transaction index and its address history entries
// This is called during block disconnect to remove stale transaction data during reorgs
func (b *BinaryBatch) DeleteTransaction(txHash types.Hash, height uint32) error {
	// First, get the transaction data so we can clean up address history entries
	txData, err := b.storage.GetTransactionData(txHash)
	if err != nil {
		// If transaction doesn't exist, nothing to delete
		if isStorageNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get transaction for deletion: %w", err)
	}

	// Delete from transaction index (prefix 0x04)
	txKey := TransactionKey(txHash)
	if err := b.batch.Delete(txKey, nil); err != nil {
		return fmt.Errorf("failed to delete transaction: %w", err)
	}
	b.size += len(txKey)

	// Delete address history entries for outputs
	if txData != nil && txData.TxData != nil {
		for outIdx, output := range txData.TxData.Outputs {
			scriptType, scriptHash := AnalyzeScript(output.ScriptPubKey)
			if scriptType != ScriptTypeUnknown && scriptHash != [20]byte{} {
				// Delete address history entry
				historyKey := AddressHistoryKey(scriptHash, height, txHash, uint16(outIdx))
				if err := b.batch.Delete(historyKey, nil); err != nil {
					return fmt.Errorf("failed to delete address history for output %d: %w", outIdx, err)
				}
				b.size += len(historyKey)
			}
		}

		// Delete address history entries for inputs (if not coinbase)
		if !txData.TxData.IsCoinbase() {
			for inIdx, input := range txData.TxData.Inputs {
				// Get the previous output to find the address
				prevTxData, err := b.storage.GetTransactionData(input.PreviousOutput.Hash)
				if err != nil || prevTxData == nil || prevTxData.TxData == nil {
					continue // Skip if we can't find the previous tx
				}

				if int(input.PreviousOutput.Index) < len(prevTxData.TxData.Outputs) {
					prevOutput := prevTxData.TxData.Outputs[input.PreviousOutput.Index]
					scriptType, scriptHash := AnalyzeScript(prevOutput.ScriptPubKey)
					if scriptType != ScriptTypeUnknown && scriptHash != [20]byte{} {
						// Delete address history entry for this input
						// Note: inputs are indexed with txIndex = inIdx but marked as IsInput=true
						historyKey := AddressHistoryKey(scriptHash, height, txHash, uint16(inIdx))
						if err := b.batch.Delete(historyKey, nil); err != nil {
							return fmt.Errorf("failed to delete address history for input %d: %w", inIdx, err)
						}
						b.size += len(historyKey)
					}
				}
			}
		}
	}

	return nil
}

// IndexTransactionByAddress stores address → transaction mapping in the batch
// addressBinary is the decoded address (netID + hash160 = 21 bytes)
func (b *BinaryBatch) IndexTransactionByAddress(addressBinary []byte, txHash types.Hash, height uint32, txIndex uint32, value int64, isInput bool, blockHash types.Hash) error {
	if len(addressBinary) != 21 {
		return fmt.Errorf("invalid address binary length: %d", len(addressBinary))
	}

	var scriptHash [20]byte
	copy(scriptHash[:], addressBinary[1:21])

	// Create complete index entry with full transaction context
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
	if err := b.batch.Set(key, data, nil); err != nil {
		return fmt.Errorf("failed to store address history: %w", err)
	}
	b.size += len(key) + len(data)
	return nil
}

