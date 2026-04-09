package wallet

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/twins-dev/twins-core/pkg/crypto"
	"github.com/twins-dev/twins-core/pkg/types"
)

// buildP2PKHScript constructs a 25-byte P2PKH scriptPubKey from a 20-byte hash160.
// Format: OP_DUP OP_HASH160 <push 20> <hash160> OP_EQUALVERIFY OP_CHECKSIG
func buildP2PKHScript(hash160 []byte) []byte {
	if len(hash160) != 20 {
		panic("hash160 must be exactly 20 bytes")
	}
	script := make([]byte, 25)
	script[0] = 0x76 // OP_DUP
	script[1] = 0xa9 // OP_HASH160
	script[2] = 0x14 // Push 20 bytes
	copy(script[3:23], hash160)
	script[23] = 0x88 // OP_EQUALVERIFY
	script[24] = 0xac // OP_CHECKSIG
	return script
}

// scriptForWalletAddress returns the P2PKH script for a given wallet-generated address.
func scriptForWalletAddress(t *testing.T, address string) []byte {
	t.Helper()
	addr, err := crypto.DecodeAddress(address)
	require.NoError(t, err)
	return buildP2PKHScript(addr.Hash160())
}

// newCoinstakeTx builds a coinstake transaction structure:
//   - One input (the wallet's stake input referenced by outpoint)
//   - outputs[0] is the empty coinstake marker (added automatically)
//   - outputs[1..] are the provided outputs (stake return, MN payment, dev payment, etc.)
func newCoinstakeTx(stakeInput types.Outpoint, outputs []*types.TxOutput) *types.Transaction {
	return &types.Transaction{
		Version: 2,
		Inputs: []*types.TxInput{
			{
				PreviousOutput: stakeInput,
				ScriptSig:      []byte{0x01, 0x02}, // non-empty so it looks like a signed input
				Sequence:       0xffffffff,
			},
		},
		Outputs: append(
			[]*types.TxOutput{{Value: 0, ScriptPubKey: nil}}, // output[0]: empty coinstake marker
			outputs...,
		),
		LockTime: 0,
	}
}

// TestCategorizeCoinstake verifies categorizeTransactionLocked handles all relevant
// coinstake layouts including the staker+MN address-overlap case.
//
// The cases cover:
//   - Case A: pure stake, no MN payment (len=2)
//   - Case B: wallet is both staker and MN operator, DIFFERENT addresses (len=4, common case)
//   - Case C: wallet is both staker and MN operator, SAME address — the bug fix (len=4)
//   - Case D: wallet stakes, MN reward goes to an external address (len=4)
//   - Case E: wallet does NOT stake, only receives MN reward (spentAmount == 0, len=4)
//
// All cases use the TWINS mainnet canonical coinstake layout:
//
//	[empty(0), stake_return..., mn_payment, dev_payment]
//
// where dev_payment is always at the last position.
func TestCategorizeCoinstake(t *testing.T) {
	w := createTestWallet(t)
	seed := []byte("test seed for coinstake categorization with enough entropy")
	require.NoError(t, w.CreateWallet(seed, nil))

	// Generate two wallet-owned addresses.
	addrX, err := w.GetNewAddress("stake address X")
	require.NoError(t, err)
	addrY, err := w.GetNewAddress("mn address Y")
	require.NoError(t, err)

	scriptX := scriptForWalletAddress(t, addrX)
	scriptY := scriptForWalletAddress(t, addrY)

	// Build scripts for non-wallet addresses using raw hash160 values that do not
	// match any wallet-derived address. isOurScriptLocked will return false for
	// these because the binary key is not in w.addressesBinary.
	scriptOther := buildP2PKHScript(bytes.Repeat([]byte{0xde}, 20))
	scriptDev := buildP2PKHScript(bytes.Repeat([]byte{0xef}, 20))

	// Typical block reward split (arbitrary values for testing — only the
	// relative structure matters, not the exact amounts).
	const (
		stakeInputValue int64 = 10_000_000_000 // 100 TWINS
		stakerReward    int64 = 100_000_000    // 1 TWINS (staker portion)
		mnReward        int64 = 400_000_000    // 4 TWINS (masternode portion)
		devReward       int64 = 50_000_000     // 0.5 TWINS (dev fund)
	)

	outpoint := types.Outpoint{Hash: types.Hash{0x01, 0x02, 0x03}, Index: 0}

	// checkInputFromX simulates the wallet owning the stake input at outpoint,
	// sourced from addrX. stakingInputAddrs will contain {addrX}.
	checkInputFromX := func(op types.Outpoint) (int64, string, bool) {
		if op == outpoint {
			return stakeInputValue, addrX, true
		}
		return 0, "", false
	}

	// checkInputFromOther simulates an external staker (spentAmount stays 0 from
	// the wallet's perspective).
	checkInputFromOther := func(op types.Outpoint) (int64, string, bool) {
		return 0, "", false
	}

	tests := []struct {
		name              string
		outputs           []*types.TxOutput
		checkInput        func(types.Outpoint) (int64, string, bool)
		wantCategory      TxCategory
		wantAmount        int64
		wantAddress       string
		wantHasExtra      bool
		wantExtraCategory TxCategory
		wantExtraNet      int64
		wantExtraAddress  string
	}{
		{
			name: "Case A: pure stake, no MN payment (len=2)",
			outputs: []*types.TxOutput{
				// [empty, X(return+reward)]
				{Value: stakeInputValue + stakerReward, ScriptPubKey: scriptX},
			},
			checkInput:   checkInputFromX,
			wantCategory: TxCategoryCoinStake,
			// net = received - spent = (stake + stakerReward) - stake = stakerReward
			wantAmount:  stakerReward,
			wantAddress: addrX,
		},
		{
			name: "Case B: disjoint staker+MN with dev (len=4, common)",
			outputs: []*types.TxOutput{
				// [empty, X(return+reward), Y(MN), DEV]
				{Value: stakeInputValue + stakerReward, ScriptPubKey: scriptX},
				{Value: mnReward, ScriptPubKey: scriptY},
				{Value: devReward, ScriptPubKey: scriptDev},
			},
			checkInput:        checkInputFromX,
			wantCategory:      TxCategoryMasternode,
			wantAmount:        mnReward,
			wantAddress:       addrY,
			wantHasExtra:      true,
			wantExtraCategory: TxCategoryCoinStake,
			wantExtraNet:      stakerReward,
			wantExtraAddress:  addrX,
		},
		{
			name: "Case C: BUG FIX — staker and MN on SAME address with dev (len=4)",
			outputs: []*types.TxOutput{
				// [empty, X(return+reward), X(MN), DEV]
				{Value: stakeInputValue + stakerReward, ScriptPubKey: scriptX},
				{Value: mnReward, ScriptPubKey: scriptX}, // MN pays to the same address as the staker
				{Value: devReward, ScriptPubKey: scriptDev},
			},
			checkInput:        checkInputFromX,
			wantCategory:      TxCategoryMasternode, // FIXED: previously returned TxCategoryCoinStake
			wantAmount:        mnReward,
			wantAddress:       addrX,
			wantHasExtra:      true,
			wantExtraCategory: TxCategoryCoinStake,
			wantExtraNet:      stakerReward,
			wantExtraAddress:  addrX,
		},
		{
			name: "Case D: wallet stakes, MN reward goes to external address (len=4)",
			outputs: []*types.TxOutput{
				// [empty, X(return+reward), Other(MN not ours), DEV]
				{Value: stakeInputValue + stakerReward, ScriptPubKey: scriptX},
				{Value: mnReward, ScriptPubKey: scriptOther},
				{Value: devReward, ScriptPubKey: scriptDev},
			},
			checkInput:   checkInputFromX,
			wantCategory: TxCategoryCoinStake,
			// Only the stake return is wallet-owned; net = stakerReward
			wantAmount:  stakerReward,
			wantAddress: addrX,
		},
		{
			name: "Case E: pure MN receive, external staker (spentAmount==0, len=4)",
			outputs: []*types.TxOutput{
				// [empty, Other(stake return), X(MN to us), DEV]
				{Value: stakeInputValue + stakerReward, ScriptPubKey: scriptOther},
				{Value: mnReward, ScriptPubKey: scriptX},
				{Value: devReward, ScriptPubKey: scriptDev},
			},
			checkInput:   checkInputFromOther,
			wantCategory: TxCategoryMasternode,
			// Only the MN output is wallet-owned; received = mnReward
			wantAmount:  mnReward,
			wantAddress: addrX,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tx := newCoinstakeTx(outpoint, tc.outputs)

			w.mu.RLock()
			category, amount, address, extra := w.categorizeTransactionLocked(tx, 1, tc.checkInput)
			w.mu.RUnlock()

			assert.Equal(t, tc.wantCategory, category, "category mismatch")
			assert.Equal(t, tc.wantAmount, amount, "amount mismatch")
			assert.Equal(t, tc.wantAddress, address, "address mismatch")

			if tc.wantHasExtra {
				require.NotNil(t, extra, "expected categorizationExtra to be non-nil")
				assert.Equal(t, tc.wantExtraCategory, extra.Category, "extra.Category mismatch")
				assert.Equal(t, tc.wantExtraNet, extra.NetAmount, "extra.NetAmount mismatch")
				assert.Equal(t, tc.wantExtraAddress, extra.Address, "extra.Address mismatch")
			} else {
				assert.Nil(t, extra, "expected categorizationExtra to be nil")
			}
		})
	}
}

// TestCategorizeCoinstake_Pass2GuardedByLength verifies the len >= 4 guard on the
// position-based Pass 2 fallback: hypothetical len=3 coinstakes (no dev output)
// should NOT trigger Pass 2 to avoid misclassifying split-stake scenarios.
func TestCategorizeCoinstake_Pass2GuardedByLength(t *testing.T) {
	w := createTestWallet(t)
	seed := []byte("test seed for pass 2 length guard with enough entropy")
	require.NoError(t, w.CreateWallet(seed, nil))

	addrX, err := w.GetNewAddress("address X")
	require.NoError(t, err)
	scriptX := scriptForWalletAddress(t, addrX)

	const (
		stakeInputValue int64 = 10_000_000_000
		stakerReward    int64 = 100_000_000
		secondOutput    int64 = 50_000_000
	)

	outpoint := types.Outpoint{Hash: types.Hash{0xaa}, Index: 0}
	checkInput := func(op types.Outpoint) (int64, string, bool) {
		if op == outpoint {
			return stakeInputValue, addrX, true
		}
		return 0, "", false
	}

	// len=3 layout: [empty, X(return), X(extra stake split)] — no dev, no MN.
	// Pass 1 finds nothing (both X outputs are in stakingInputAddrs). Pass 2 is
	// gated by len >= 4 so it should NOT fire. Result: TxCategoryCoinStake.
	tx := newCoinstakeTx(outpoint, []*types.TxOutput{
		{Value: stakeInputValue + stakerReward, ScriptPubKey: scriptX},
		{Value: secondOutput, ScriptPubKey: scriptX},
	})

	w.mu.RLock()
	category, amount, address, extra := w.categorizeTransactionLocked(tx, 1, checkInput)
	w.mu.RUnlock()

	assert.Equal(t, TxCategoryCoinStake, category,
		"len=3 coinstake without dev output must not trigger Pass 2 fallback")
	assert.Equal(t, stakerReward+secondOutput, amount)
	assert.Equal(t, addrX, address)
	assert.Nil(t, extra)
}
