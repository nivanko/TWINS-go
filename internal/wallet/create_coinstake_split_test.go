package wallet

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twins-dev/twins-core/pkg/types"
)

// buildStakeableUTXOForWalletAddress builds a StakeableUTXO owned by the given
// wallet address. Value/outpoint are dummy placeholders; CreateCoinstakeTx only
// uses Address for private-key lookup and Amount for the total-reward sum.
func buildStakeableUTXOForWalletAddress(addr string, amount int64) *StakeableUTXO {
	return &StakeableUTXO{
		Outpoint:     types.Outpoint{Hash: types.Hash{0xaa}, Index: 0},
		Amount:       amount,
		Address:      addr,
		BlockHeight:  100,
		BlockTime:    1600000000,
		ScriptPubKey: []byte{},
	}
}

// TestCreateCoinstakeTxHardFloorNoSplitBelow100K verifies the belt-and-
// suspenders hard floor in CreateCoinstakeTx: even if a malicious or stale
// persisted `stakesplitthreshold` in the wallet DB bypasses the clamp in
// SetStakeSplitThreshold (e.g. a value written by an older build), the
// coinstake must NOT be split when totalReward < MinStakeSplitThresholdSatoshis.
// This prevents producing blocks whose vout[1] falls below legacy's
// StakingMinInput (12000 TWINS).
func TestCreateCoinstakeTxHardFloorNoSplitBelow100K(t *testing.T) {
	w := createTestWallet(t)
	seed := []byte("test seed for create coinstake split hard floor test12")
	require.NoError(t, w.CreateWallet(seed, nil))

	addr, err := w.GetNewAddress("stake")
	require.NoError(t, err)

	// Inject a below-floor threshold directly into wdb, bypassing the clamp.
	// This simulates a stale persisted value from an older build.
	require.NoError(t, w.wdb.SetStakeSplitThreshold(int64(1*100000000))) // 1 TWINS

	// totalReward = stakeAmount + blockReward = 99999 TWINS  (< hard floor 100000)
	stakeAmount := int64(99998 * 100000000)
	blockReward := int64(1 * 100000000)
	tx, err := w.CreateCoinstakeTx(
		buildStakeableUTXOForWalletAddress(addr, stakeAmount),
		blockReward,
		1600000000,
	)
	require.NoError(t, err)

	// Expect 2 outputs total: empty marker + single stake output (NO split).
	assert.Len(t, tx.Outputs, 2,
		"totalReward below MinStakeSplitThresholdSatoshis must not split")
	assert.Equal(t, int64(0), tx.Outputs[0].Value, "vout[0] is empty marker")
	assert.Equal(t, stakeAmount+blockReward, tx.Outputs[1].Value,
		"single stake output carries the full reward")
}

// TestCreateCoinstakeTxSplitsAboveHardFloor verifies that the hard floor does
// not interfere with splitting once totalReward is above the floor and the
// user-configured stakeSplitThreshold allows it.
func TestCreateCoinstakeTxSplitsAboveHardFloor(t *testing.T) {
	w := createTestWallet(t)
	seed := []byte("test seed for create coinstake split above floor test1")
	require.NoError(t, w.CreateWallet(seed, nil))

	addr, err := w.GetNewAddress("stake")
	require.NoError(t, err)

	// User threshold at the hard floor (via the public clamped setter).
	require.NoError(t, w.SetStakeSplitThreshold(MinStakeSplitThresholdSatoshis))

	// totalReward = 300000 TWINS, well above the floor, and
	// totalReward/2 = 150000 > stakeSplitThreshold (100000), so split expected.
	stakeAmount := int64(299999 * 100000000)
	blockReward := int64(1 * 100000000)
	tx, err := w.CreateCoinstakeTx(
		buildStakeableUTXOForWalletAddress(addr, stakeAmount),
		blockReward,
		1600000000,
	)
	require.NoError(t, err)

	assert.Len(t, tx.Outputs, 3, "expected split into two stake outputs")
	assert.Equal(t, int64(0), tx.Outputs[0].Value, "vout[0] is empty marker")
	// Both halves must be safely above legacy StakingMinInput (12000 TWINS).
	const legacyMinInputSatoshis = int64(12000 * 100000000)
	assert.Greater(t, tx.Outputs[1].Value, legacyMinInputSatoshis,
		"vout[1] must exceed legacy StakingMinInput")
	assert.Greater(t, tx.Outputs[2].Value, legacyMinInputSatoshis,
		"vout[2] must exceed legacy StakingMinInput")
}
