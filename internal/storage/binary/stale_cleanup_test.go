package binary

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twins-dev/twins-core/internal/storage"
	"github.com/twins-dev/twins-core/pkg/types"
)

// newTestStorage creates a fresh BinaryStorage for tests in a temp dir.
func newTestStorage(t *testing.T) *BinaryStorage {
	t.Helper()
	config := &storage.StorageConfig{
		Path:            t.TempDir(),
		CacheSize:       1 << 20,
		WriteBuffer:     2,
		MaxOpenFiles:    10,
		CompressionType: "snappy",
	}
	stor, err := NewBinaryStorage(config)
	require.NoError(t, err)
	t.Cleanup(func() { stor.Close() })
	return stor
}

// writeRawUTXO directly stores a UTXOData record bypassing the high-level batch.
// Used by tests that need to construct specific UTXO states (e.g. stuck-spent).
func writeRawUTXO(t *testing.T, stor *BinaryStorage, outpoint types.Outpoint, data *UTXOData) {
	t.Helper()
	encoded, err := EncodeUTXOData(data)
	require.NoError(t, err)
	require.NoError(t, stor.db.Set(UTXOExistKey(outpoint.Hash, outpoint.Index), encoded, nil))
	if data.ScriptHash != [20]byte{} && data.IsUnspent() {
		// Mirror the address UTXO index for unspent entries
		addrKey := AddressUTXOKey(data.ScriptHash, outpoint.Hash, outpoint.Index)
		var buf [8]byte
		binary.LittleEndian.PutUint64(buf[:], data.Value)
		require.NoError(t, stor.db.Set(addrKey, buf[:], nil))
	}
}

// readRawUTXO fetches a UTXOData directly. Returns nil if not found.
func readRawUTXO(t *testing.T, stor *BinaryStorage, outpoint types.Outpoint) *UTXOData {
	t.Helper()
	val, closer, err := stor.db.Get(UTXOExistKey(outpoint.Hash, outpoint.Index))
	if err != nil {
		return nil
	}
	defer closer.Close()
	decoded, err := DecodeUTXOData(val)
	require.NoError(t, err)
	return decoded
}

// TestDeleteOrphanedBlockRollsBackUTXOs verifies that CleanOrphanedBlocks
// fully rolls back UTXO state for an orphaned block when block data is
// available: created UTXOs are removed and consumed UTXOs are unspent.
func TestDeleteOrphanedBlockRollsBackUTXOs(t *testing.T) {
	stor := newTestStorage(t)

	// Build an orphan block at height 101. Its "regularTx" will consume a
	// pre-seeded UTXO and produce its own outputs.
	orphanBlock := createUniqueTestBlock(50)
	orphanHeight := uint32(101)

	// Store the orphan block (block data + hash→height at the orphan height).
	batch := stor.NewBatch()
	bb := batch.(*BinaryBatch)
	require.NoError(t, bb.StoreBlockIndex(orphanBlock.Hash(), orphanHeight))
	require.NoError(t, bb.StoreBlockWithHeight(orphanBlock, orphanHeight))
	require.NoError(t, batch.Commit())

	// Store UTXOExist entries for outputs of the orphan block so the
	// rollback path has something to delete.
	for _, tx := range orphanBlock.Transactions {
		txHash := tx.Hash()
		for outIdx, output := range tx.Outputs {
			_, scriptHash := AnalyzeScript(output.ScriptPubKey)
			writeRawUTXO(t, stor, types.Outpoint{Hash: txHash, Index: uint32(outIdx)}, &UTXOData{
				Value:      uint64(output.Value),
				ScriptHash: scriptHash,
				Height:     orphanHeight,
				Script:     output.ScriptPubKey,
			})
		}
	}

	// Pre-seed the prev UTXO consumed by regularTx (index 1) as spent by
	// that transaction. This simulates the UTXO being locked before the
	// orphan block is cleaned up.
	regularTx := orphanBlock.Transactions[1]
	require.NotEmpty(t, regularTx.Inputs)
	prevOutpoint := regularTx.Inputs[0].PreviousOutput
	var prevScriptHash [20]byte
	// Use a deterministic fake scriptHash so we can verify address UTXO
	// reindexing happens on unspend.
	copy(prevScriptHash[:], []byte("prev-utxo-scripthash"))

	writeRawUTXO(t, stor, prevOutpoint, &UTXOData{
		Value:          5_000_000_000,
		ScriptHash:     prevScriptHash,
		Height:         orphanHeight - 1,
		Script:         []byte{0x01, 0x02, 0x03},
		SpendingHeight: orphanHeight,
		SpendingTxHash: regularTx.Hash(),
	})

	// Run orphan cleanup with maxValidHeight=100 (so height 101 is above).
	cleaned, err := stor.CleanOrphanedBlocks(100)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, cleaned, 1)

	// Outputs created by the orphan block must be gone.
	for _, tx := range orphanBlock.Transactions {
		txHash := tx.Hash()
		for outIdx := range tx.Outputs {
			got := readRawUTXO(t, stor, types.Outpoint{Hash: txHash, Index: uint32(outIdx)})
			assert.Nil(t, got, "orphan output UTXO should be deleted")
		}
	}

	// The prev UTXO must be unspent and restored to the address UTXO index.
	restored := readRawUTXO(t, stor, prevOutpoint)
	require.NotNil(t, restored, "prev UTXO should still exist")
	assert.True(t, restored.IsUnspent(), "prev UTXO should be unspent after rollback")
	assert.Equal(t, types.Hash{}, restored.SpendingTxHash)

	// Address UTXO index entry must be restored for the unspent UTXO.
	addrKey := AddressUTXOKey(prevScriptHash, prevOutpoint.Hash, prevOutpoint.Index)
	val, closer, err := stor.db.Get(addrKey)
	require.NoError(t, err, "address UTXO entry should be re-added on unspend")
	assert.Equal(t, uint64(5_000_000_000), binary.LittleEndian.Uint64(val))
	closer.Close()
}

// TestDeleteOrphanedBlockCleansAddressHistoryWithoutBlockData verifies that
// stale PrefixAddressHistory entries at an orphan height are removed even
// when block data is not in storage.
func TestDeleteOrphanedBlockCleansAddressHistoryWithoutBlockData(t *testing.T) {
	stor := newTestStorage(t)

	orphanHeight := uint32(200)
	forkHash := types.Hash{0xde, 0xad, 0xbe, 0xef}
	fakeTxHash := types.Hash{0xfe, 0xed, 0xfa, 0xce}

	// Write only the hash→height entry for the fork block (no block data,
	// no height→hash mapping — simulating a dangling index entry).
	var heightBytes [4]byte
	binary.LittleEndian.PutUint32(heightBytes[:], orphanHeight)
	require.NoError(t, stor.db.Set(HashToHeightKey(forkHash), heightBytes[:], nil))

	// Write a stale address history entry at the orphan height.
	var scriptHash [20]byte
	copy(scriptHash[:], []byte("stale-history-script"))
	historyKey := AddressHistoryKey(scriptHash, orphanHeight, fakeTxHash, 0)
	require.NoError(t, stor.db.Set(historyKey, []byte{0x00}, nil))

	// Sanity: entry exists.
	_, closer, err := stor.db.Get(historyKey)
	require.NoError(t, err)
	closer.Close()

	// Run orphan cleanup with maxValidHeight=199 so height 200 is above.
	cleaned, err := stor.CleanOrphanedBlocks(199)
	require.NoError(t, err)
	assert.Equal(t, 1, cleaned)

	// Stale address history entry must be gone despite missing block data.
	_, _, err = stor.db.Get(historyKey)
	assert.Error(t, err, "stale address history entry should be deleted")
}

// TestUnspendUTXOsBySpendingTx verifies that the method correctly resets
// spending state for UTXOs matching the supplied txid set, restores the
// address UTXO index, and leaves non-matching UTXOs untouched.
func TestUnspendUTXOsBySpendingTx(t *testing.T) {
	stor := newTestStorage(t)

	txStale := types.Hash{0x11, 0x22, 0x33, 0x44}
	txOther := types.Hash{0x55, 0x66, 0x77, 0x88}

	var scriptHashA, scriptHashB [20]byte
	copy(scriptHashA[:], []byte("scripthash-A"))
	copy(scriptHashB[:], []byte("scripthash-B"))

	// Three UTXOs: two spent by txStale, one by txOther.
	u1 := types.Outpoint{Hash: types.Hash{0xaa}, Index: 0}
	u2 := types.Outpoint{Hash: types.Hash{0xbb}, Index: 1}
	u3 := types.Outpoint{Hash: types.Hash{0xcc}, Index: 0}

	writeRawUTXO(t, stor, u1, &UTXOData{
		Value: 1_000, ScriptHash: scriptHashA, Height: 10,
		Script: []byte{0x01}, SpendingHeight: 11, SpendingTxHash: txStale,
	})
	writeRawUTXO(t, stor, u2, &UTXOData{
		Value: 2_000, ScriptHash: scriptHashA, Height: 10,
		Script: []byte{0x02}, SpendingHeight: 11, SpendingTxHash: txStale,
	})
	writeRawUTXO(t, stor, u3, &UTXOData{
		Value: 3_000, ScriptHash: scriptHashB, Height: 10,
		Script: []byte{0x03}, SpendingHeight: 11, SpendingTxHash: txOther,
	})

	unspent, err := stor.UnspendUTXOsBySpendingTx(map[types.Hash]struct{}{txStale: {}})
	require.NoError(t, err)
	assert.Equal(t, 2, unspent)

	// u1, u2 must be unspent now.
	assert.True(t, readRawUTXO(t, stor, u1).IsUnspent())
	assert.True(t, readRawUTXO(t, stor, u2).IsUnspent())
	// u3 (spent by txOther) must remain spent.
	assert.True(t, readRawUTXO(t, stor, u3).IsSpent())
	assert.Equal(t, txOther, readRawUTXO(t, stor, u3).SpendingTxHash)

	// Address UTXO index entries for u1 and u2 must be restored.
	for _, op := range []types.Outpoint{u1, u2} {
		addrKey := AddressUTXOKey(scriptHashA, op.Hash, op.Index)
		_, closer, err := stor.db.Get(addrKey)
		require.NoError(t, err, "address UTXO entry should be restored on unspend")
		closer.Close()
	}

	// Calling again should find nothing to do.
	unspent, err = stor.UnspendUTXOsBySpendingTx(map[types.Hash]struct{}{txStale: {}})
	require.NoError(t, err)
	assert.Equal(t, 0, unspent)

	// Empty set is a no-op.
	unspent, err = stor.UnspendUTXOsBySpendingTx(nil)
	require.NoError(t, err)
	assert.Equal(t, 0, unspent)
}

// TestFindAndMarkSpendersForOutpoints verifies the full PrefixTransaction
// scan recovery path for phantom-unspent UTXOs: a spender transaction
// stored in the tx index with NO corresponding address-history entry for
// the consumed input is still correctly discovered and its input UTXO
// is marked as spent, with the address UTXO index entry removed.
func TestFindAndMarkSpendersForOutpoints(t *testing.T) {
	stor := newTestStorage(t)

	// Seed two wallet-owned UTXOs. U1 will be phantom-consumed by a real
	// on-main-chain transaction; U2 will remain genuinely unspent (no
	// spender in storage) so we can verify the scan leaves it alone.
	var scriptHash [20]byte
	copy(scriptHash[:], []byte("test-script-hash-20b"))

	u1 := types.Outpoint{Hash: types.Hash{0x11}, Index: 0}
	u2 := types.Outpoint{Hash: types.Hash{0x22}, Index: 0}

	writeRawUTXO(t, stor, u1, &UTXOData{
		Value:      7_500_000_000,
		ScriptHash: scriptHash,
		Height:     100,
		Script:     []byte{0x01, 0x02, 0x03},
	})
	writeRawUTXO(t, stor, u2, &UTXOData{
		Value:      2_500_000_000,
		ScriptHash: scriptHash,
		Height:     100,
		Script:     []byte{0x01, 0x02, 0x03},
	})

	// Build a spender transaction that consumes u1 (but NOT u2).
	// Store it in the tx index and register its block in height→hash.
	// Deliberately do NOT call IndexTransactionByAddress — this simulates
	// the corruption class where address indexing was missed.
	spenderTx := &types.Transaction{
		Version: 1,
		Inputs: []*types.TxInput{
			{
				PreviousOutput: u1,
				ScriptSig:      []byte{0x01},
				Sequence:       0xffffffff,
			},
		},
		Outputs: []*types.TxOutput{
			{Value: 7_499_999_000, ScriptPubKey: []byte{0x6a}}, // OP_RETURN sink
		},
	}
	spenderHash := spenderTx.Hash()
	spenderBlock := &types.Block{
		Header: &types.BlockHeader{
			Version:       1,
			PrevBlockHash: types.Hash{0xaa},
			MerkleRoot:    spenderHash,
			Timestamp:     1_600_000_000,
			Bits:          0x1d00ffff,
			Nonce:         1,
		},
		Transactions: []*types.Transaction{spenderTx},
	}
	spenderBlockHash := spenderBlock.Hash()
	spenderHeight := uint32(150)

	batch := stor.NewBatch()
	require.NoError(t, batch.StoreBlockIndex(spenderBlockHash, spenderHeight))
	require.NoError(t, batch.StoreBlockWithHeight(spenderBlock, spenderHeight))
	require.NoError(t, batch.StoreTransaction(spenderTx))
	require.NoError(t, batch.Commit())

	// Also build a second transaction that is on an ORPHAN chain (its
	// stored BlockHash does not match the canonical block at that height).
	// The scan must NOT mark its input as spent even though the input
	// matches our target set. To trigger this, reuse u2 as input for an
	// orphan tx at height 160 but store canonical hash for height 160 as
	// something different.
	orphanTx := &types.Transaction{
		Version: 1,
		Inputs: []*types.TxInput{
			{
				PreviousOutput: u2,
				ScriptSig:      []byte{0x02},
				Sequence:       0xffffffff,
			},
		},
		Outputs: []*types.TxOutput{
			{Value: 2_499_999_000, ScriptPubKey: []byte{0x6a}},
		},
	}
	orphanTxHash := orphanTx.Hash()
	orphanHeight := uint32(160)

	// Store orphan tx via direct encoding: its BlockHash refers to a
	// non-canonical block, while height→hash for 160 points to the
	// "winning" chain's block. The scan must skip this tx even though
	// its input matches u2.
	canonicalAt160 := types.Hash{0xc0, 0xde}
	orphanBlockHash := types.Hash{0x99, 0x88}
	require.NoError(t, stor.db.Set(HeightToHashKey(orphanHeight), canonicalAt160[:], nil))
	orphanTxData := &TransactionData{
		BlockHash: orphanBlockHash,
		Height:    orphanHeight,
		TxIndex:   0,
		TxData:    orphanTx,
	}
	orphanEncoded, err := EncodeTransactionData(orphanTxData)
	require.NoError(t, err)
	require.NoError(t, stor.db.Set(TransactionKey(orphanTxHash), orphanEncoded, nil))

	// Run the scan.
	targets := map[types.Outpoint]struct{}{
		u1: {},
		u2: {},
	}
	results, err := stor.FindAndMarkSpendersForOutpoints(targets)
	require.NoError(t, err)

	// u1 must be marked spent by the main-chain spender tx.
	require.Contains(t, results, u1, "u1 should be reconciled as spent")
	assert.Equal(t, spenderHash, results[u1].SpenderTxHash)
	assert.Equal(t, spenderHeight, results[u1].SpenderHeight)

	// u2 must NOT be marked spent (its only candidate spender is on an
	// orphan chain).
	assert.NotContains(t, results, u2, "u2 orphan spender must be ignored")

	// Storage state for u1: SpendingHeight/SpendingTxHash populated.
	u1Stored := readRawUTXO(t, stor, u1)
	require.NotNil(t, u1Stored)
	assert.Equal(t, spenderHeight, u1Stored.SpendingHeight)
	assert.Equal(t, spenderHash, u1Stored.SpendingTxHash)

	// Storage state for u2: unchanged.
	u2Stored := readRawUTXO(t, stor, u2)
	require.NotNil(t, u2Stored)
	assert.True(t, u2Stored.IsUnspent())

	// The address UTXO index entry for u1 must be gone; for u2 still present.
	addrKeyU1 := AddressUTXOKey(scriptHash, u1.Hash, u1.Index)
	_, _, err = stor.db.Get(addrKeyU1)
	assert.Error(t, err, "address UTXO index entry for u1 should be deleted")

	addrKeyU2 := AddressUTXOKey(scriptHash, u2.Hash, u2.Index)
	_, closer, err := stor.db.Get(addrKeyU2)
	require.NoError(t, err)
	closer.Close()

	// Caller's input map must not be mutated.
	assert.Contains(t, targets, u1, "caller's map must not be mutated")
	assert.Contains(t, targets, u2, "caller's map must not be mutated")
}
