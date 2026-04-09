package consensus

import (
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twins-dev/twins-core/pkg/types"
)

// fakeSporkInactive is a minimal SporkInterface that reports every spork as
// inactive, simulating a fresh Go node that never received a spork broadcast.
type fakeSporkInactive struct{}

func (fakeSporkInactive) IsActive(int32) bool  { return false }
func (fakeSporkInactive) GetValue(int32) int64 { return 0 }

// buildLowVout1Block builds a minimal 2-tx PoS block whose coinstake vout[1]
// carries the supplied satoshi value (the rest of the tx fields are zeroed).
func buildLowVout1Block(vout1Value int64) *types.Block {
	coinbase := &types.Transaction{Version: 1}
	coinstake := &types.Transaction{
		Version: 1,
		Outputs: []*types.TxOutput{
			{Value: 0, ScriptPubKey: []byte{}},
			{Value: vout1Value, ScriptPubKey: []byte{0xac}},
		},
	}
	return &types.Block{
		Header:       &types.BlockHeader{Version: 4},
		Transactions: []*types.Transaction{coinbase, coinstake},
	}
}

// TestValidateMinStakeOutputMainnetForcedEnforcement reproduces the legacy
// CheckBlock() : stake under min. stake value rule in the Go validator on
// mainnet, using the exact vout[1] value (9865 TWINS) from block fbf23a39...
// Before the task m-enforce-stake-split-min-threshold fix the rule was
// spork-gated and a fresh Go node with SPORK_TWINS_02_MIN_STAKE_AMOUNT OFF
// would silently accept this block. The fix makes mainnet enforcement
// unconditional past height 333500 regardless of spork state.
func TestValidateMinStakeOutputMainnetForcedEnforcement(t *testing.T) {
	bv := createTestBlockValidator(t) // mainnet params
	// Spork manager absent / inactive: mainnet enforcement must still run.
	bv.SetSporkManager(fakeSporkInactive{})

	block := buildLowVout1Block(9865 * 100000000)

	err := bv.validateMinStakeOutput(block, 333500)
	require.Error(t, err, "mainnet: vout[1] below MinStakeAmount must be rejected with or without spork")
	assert.Contains(t, err.Error(), "stake output value")

	// Boundary accept: vout[1] == MinStakeAmount is allowed.
	block.Transactions[1].Outputs[1].Value = int64(bv.params.MinStakeAmount)
	require.NoError(t, bv.validateMinStakeOutput(block, 333500))

	// Below the activation height the rule does not apply even on mainnet.
	block.Transactions[1].Outputs[1].Value = 9865 * 100000000
	require.NoError(t, bv.validateMinStakeOutput(block, 333499),
		"mainnet below activation height: rule should not apply")
}

// TestValidateMinStakeOutputTestnetStillSporkGated guards against the
// consensus-divergence risk of forcing enforcement on networks where the
// spork was never activated. On testnet/regtest the rule must remain gated
// on SPORK_TWINS_02_MIN_STAKE_AMOUNT, so a fresh node with the spork OFF
// (default) must NOT reject a coinstake with a small vout[1].
func TestValidateMinStakeOutputTestnetStillSporkGated(t *testing.T) {
	storage := NewMockStorage()
	params := types.TestnetParams()
	logger := logrus.New()
	pos := NewProofOfStake(storage, params, logger)
	bv := NewBlockValidator(pos, storage, params)
	bv.SetSporkManager(fakeSporkInactive{})

	block := buildLowVout1Block(9865 * 100000000)

	// Testnet activation height per main.cpp:3976 is 192500.
	require.NoError(t, bv.validateMinStakeOutput(block, 192500),
		"testnet with spork OFF: rule must be skipped to match legacy testnet")

	// With spork active on testnet the rule engages and rejects the block.
	bv.SetSporkManager(fakeSporkAlwaysActive{})
	err := bv.validateMinStakeOutput(block, 192500)
	require.Error(t, err, "testnet with spork ON: rule must enforce")
	assert.Contains(t, err.Error(), "stake output value")
}

// fakeSporkAlwaysActive is a minimal SporkInterface that reports every spork
// as active. Used by the testnet test to verify spork-gated enforcement.
type fakeSporkAlwaysActive struct{}

func (fakeSporkAlwaysActive) IsActive(int32) bool  { return true }
func (fakeSporkAlwaysActive) GetValue(int32) int64 { return 0 }
