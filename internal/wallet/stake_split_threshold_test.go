package wallet

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSetStakeSplitThresholdClampsBelowMinimum verifies that values below the
// hard floor MinStakeSplitThresholdSatoshis (100000 TWINS) are clamped up to
// the floor, preventing the wallet from producing split coinstakes whose vout[1]
// would fall below legacy's StakingMinInput (12000 TWINS). See task
// m-enforce-stake-split-min-threshold.
func TestSetStakeSplitThresholdClampsBelowMinimum(t *testing.T) {
	wallet := createTestWallet(t)
	seed := []byte("test seed for stake split threshold clamping with entropy")
	require.NoError(t, wallet.CreateWallet(seed, nil))

	// Try to set a value well below the hard floor (50000 TWINS).
	lowThreshold := int64(50000 * 100000000)
	require.NoError(t, wallet.SetStakeSplitThreshold(lowThreshold))

	got, err := wallet.GetStakeSplitThreshold()
	require.NoError(t, err)
	assert.Equal(t, MinStakeSplitThresholdSatoshis, got,
		"threshold below hard floor must be clamped to MinStakeSplitThresholdSatoshis")
}

// TestSetStakeSplitThresholdZeroNotClamped verifies that the sentinel value 0
// ("splitting disabled") passes through unchanged.
func TestSetStakeSplitThresholdZeroNotClamped(t *testing.T) {
	wallet := createTestWallet(t)
	seed := []byte("test seed for stake split threshold zero sentinel with e")
	require.NoError(t, wallet.CreateWallet(seed, nil))

	require.NoError(t, wallet.SetStakeSplitThreshold(0))

	got, err := wallet.GetStakeSplitThreshold()
	require.NoError(t, err)
	assert.Equal(t, int64(0), got, "zero must remain zero (splitting disabled sentinel)")
}

// TestSetStakeSplitThresholdAtOrAboveMinimumUnchanged verifies that values at
// or above the hard floor are accepted unchanged.
func TestSetStakeSplitThresholdAtOrAboveMinimumUnchanged(t *testing.T) {
	wallet := createTestWallet(t)
	seed := []byte("test seed for stake split threshold above minimum ent123")
	require.NoError(t, wallet.CreateWallet(seed, nil))

	// Exactly at the floor.
	require.NoError(t, wallet.SetStakeSplitThreshold(MinStakeSplitThresholdSatoshis))
	got, err := wallet.GetStakeSplitThreshold()
	require.NoError(t, err)
	assert.Equal(t, MinStakeSplitThresholdSatoshis, got)

	// Well above the floor.
	higher := int64(500000 * 100000000)
	require.NoError(t, wallet.SetStakeSplitThreshold(higher))
	got, err = wallet.GetStakeSplitThreshold()
	require.NoError(t, err)
	assert.Equal(t, higher, got)
}
