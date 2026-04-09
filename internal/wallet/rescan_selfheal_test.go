package wallet

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twins-dev/twins-core/pkg/crypto"
	"github.com/twins-dev/twins-core/pkg/types"
)

// TestRescanSelfHealStaleAddressIndex verifies that RescanAllAddresses
// removes stale address index entries whose transactions no longer exist
// in storage and unspends any UTXO stuck as spent by those stale txids.
func TestRescanSelfHealStaleAddressIndex(t *testing.T) {
	w := createTestWallet(t)

	// Create an address to own the stale index entry.
	address, err := w.GetNewAddress("test")
	require.NoError(t, err)

	addr, err := crypto.DecodeAddress(address)
	require.NoError(t, err)

	addressBinary := make([]byte, 21)
	addressBinary[0] = addr.NetID()
	copy(addressBinary[1:], addr.Hash160())

	// Inject a stale address index entry pointing to a nonexistent tx.
	staleTx := types.Hash{0xde, 0xad, 0xbe, 0xef, 0xaa}
	blockHash := types.Hash{0x11, 0x22}

	batch := w.storage.NewBatch()
	require.NoError(t, batch.IndexTransactionByAddress(
		addressBinary, staleTx, 100, 0, 1000, false, blockHash,
	))
	require.NoError(t, batch.Commit())

	// Sanity: the stale entry is visible via the address index.
	entries, err := w.storage.GetTransactionsByAddress(addressBinary)
	require.NoError(t, err)
	found := false
	for _, e := range entries {
		if e.TxHash == staleTx {
			found = true
			break
		}
	}
	require.True(t, found, "stale address index entry should be present before rescan")

	// Pre-seed a UTXO marked as spent by the stale tx. After reconciliation
	// it must be unspent. We use StoreUTXO then patch spending state via
	// the raw storage API.
	prevOutpoint := types.Outpoint{Hash: types.Hash{0xca, 0xfe}, Index: 0}
	dummyOutput := &types.TxOutput{
		Value: 7_500_000_000,
		ScriptPubKey: []byte{
			0x76, 0xa9, 0x14,
			0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff,
			0x00, 0x01, 0x02, 0x03, 0x04, 0x05,
			0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b,
			0x0c, 0x0d,
			0x88, 0xac,
		},
	}
	require.NoError(t, w.storage.StoreUTXO(prevOutpoint, dummyOutput, 50, false))

	// Mark as spent by the stale tx via batch.MarkUTXOSpent. That path has
	// orphaned-reference detection but without a spending block/tx in
	// storage, our rescan self-heal is what's expected to clear it.
	// To avoid the orphaned-ref fast-path (which would reset on next spend),
	// we directly update the record via a batch that reads and re-encodes.
	spendBatch := w.storage.NewBatch()
	_, err = spendBatch.MarkUTXOSpent(prevOutpoint, 100, staleTx)
	require.NoError(t, err)
	require.NoError(t, spendBatch.Commit())

	// Verify UTXO is currently spent.
	utxoBefore, err := w.storage.GetUTXO(prevOutpoint)
	require.NoError(t, err)
	require.NotNil(t, utxoBefore)
	assert.NotEqual(t, types.Hash{}, utxoBefore.SpendingTxHash, "UTXO should be spent before rescan")

	// Run rescan. It should detect the stale address index entry, delete
	// it, and unspend the UTXO stuck as spent by the stale tx.
	require.NoError(t, w.RescanAllAddresses())

	// Stale address index entry must be gone.
	entries, err = w.storage.GetTransactionsByAddress(addressBinary)
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotEqual(t, staleTx, e.TxHash, "stale address index entry should be removed")
	}

	// UTXO must be unspent.
	utxoAfter, err := w.storage.GetUTXO(prevOutpoint)
	require.NoError(t, err)
	require.NotNil(t, utxoAfter)
	assert.Equal(t, uint32(0), utxoAfter.SpendingHeight, "UTXO should be unspent after rescan self-heal")
	assert.Equal(t, types.Hash{}, utxoAfter.SpendingTxHash)
}
