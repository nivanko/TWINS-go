package wallet

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twins-dev/twins-core/pkg/types"
)

// TestMarkAddressesUsedFromStateLocked verifies that the helper correctly
// restores the in-memory Used flag on addresses that appear in the
// transaction map, UTXO map, or balance map. This is the same logic the
// cache-first startup path uses after LoadTransactionCache to keep
// ListAddressGroupings consistent with wallet state.
func TestMarkAddressesUsedFromStateLocked(t *testing.T) {
	w := createTestWallet(t)

	addrA, err := w.GetNewAddress("A")
	require.NoError(t, err)
	addrB, err := w.GetNewAddress("B")
	require.NoError(t, err)
	addrC, err := w.GetNewAddress("C") // Will remain unused
	require.NoError(t, err)

	// Freshly-generated addresses start with Used=false.
	w.mu.Lock()
	w.addresses[addrA].Used = false
	w.addresses[addrB].Used = false
	w.addresses[addrC].Used = false

	// Simulate state restored from txcache.dat:
	// - addrA has a wallet transaction entry
	// - addrB has a UTXO and a balance entry
	// - addrC has nothing and must stay unused
	w.transactions[txKey{types.Hash{0x01}, 0}] = &WalletTransaction{
		Hash:    types.Hash{0x01},
		Address: addrA,
		Amount:  1000,
	}
	w.utxos[types.Outpoint{Hash: types.Hash{0x02}, Index: 0}] = &UTXO{
		Outpoint: types.Outpoint{Hash: types.Hash{0x02}, Index: 0},
		Output:   &types.TxOutput{Value: 500},
		Address:  addrB,
	}
	w.balances[addrB] = &Balance{Confirmed: 500}

	// Also reset the pool.used map to simulate cache-first startup state.
	if w.addrMgr != nil && w.addrMgr.pool != nil {
		w.addrMgr.pool.mu.Lock()
		delete(w.addrMgr.pool.used, addrA)
		delete(w.addrMgr.pool.used, addrB)
		delete(w.addrMgr.pool.used, addrC)
		w.addrMgr.pool.mu.Unlock()
	}

	marked := w.markAddressesUsedFromStateLocked()
	w.mu.Unlock()

	assert.Equal(t, 2, marked, "exactly two addresses should be newly marked")
	assert.True(t, w.addresses[addrA].Used, "addrA should be marked used (from tx map)")
	assert.True(t, w.addresses[addrB].Used, "addrB should be marked used (from utxo/balance map)")
	assert.False(t, w.addresses[addrC].Used, "addrC should remain unused")

	// Both sources of truth must be consistent: IsAddressUsed reads the
	// separate pool.used map, which the helper must also update.
	assert.True(t, w.IsAddressUsed(addrA), "IsAddressUsed(addrA) should be true")
	assert.True(t, w.IsAddressUsed(addrB), "IsAddressUsed(addrB) should be true")
	assert.False(t, w.IsAddressUsed(addrC), "IsAddressUsed(addrC) should be false")

	// ListAddressGroupings should now include addrA and addrB but not addrC.
	groupings, err := w.ListAddressGroupings()
	require.NoError(t, err)

	seen := map[string]bool{}
	for _, group := range groupings {
		for _, entry := range group {
			if s, ok := entry[0].(string); ok {
				seen[s] = true
			}
		}
	}
	assert.True(t, seen[addrA], "ListAddressGroupings should include addrA")
	assert.True(t, seen[addrB], "ListAddressGroupings should include addrB")
	assert.False(t, seen[addrC], "ListAddressGroupings should skip addrC (unused)")

	// Calling again is idempotent (no new marks).
	w.mu.Lock()
	marked = w.markAddressesUsedFromStateLocked()
	w.mu.Unlock()
	assert.Equal(t, 0, marked, "second call should mark nothing")
}
