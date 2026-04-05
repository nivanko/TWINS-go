package consensus

import (
	"errors"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/twins-dev/twins-core/pkg/types"
)

// mockConsensusProvider implements ConsensusHeightProvider for testing
type mockConsensusProvider struct {
	height     uint32
	confidence float64
	peerCount  int
	err        error
}

func (m *mockConsensusProvider) GetConsensusHeightInfo() (uint32, float64, int, error) {
	return m.height, m.confidence, m.peerCount, m.err
}

// mockBlockchainForStaking implements BlockchainInterface for staking tests
type mockBlockchainForStaking struct {
	isIBD       bool
	blockHeight uint32
}

func (m *mockBlockchainForStaking) IsInitialBlockDownload() bool {
	return m.isIBD
}

func (m *mockBlockchainForStaking) GetBlock(hash types.Hash) (*types.Block, error) {
	return nil, nil
}

func (m *mockBlockchainForStaking) GetBlockByHeight(height uint32) (*types.Block, error) {
	return nil, nil
}

func (m *mockBlockchainForStaking) GetBlockHeight(hash types.Hash) (uint32, error) {
	return m.blockHeight, nil
}

func (m *mockBlockchainForStaking) GetBestHeight() (uint32, error) {
	return m.blockHeight, nil
}

func (m *mockBlockchainForStaking) GetUTXO(outpoint types.Outpoint) (*types.UTXO, error) {
	return nil, nil
}

func (m *mockBlockchainForStaking) GetStakeModifier(blockHash types.Hash) (uint64, error) {
	return 0, nil
}

func (m *mockBlockchainForStaking) GetCheckpointManager() types.CheckpointManager {
	return nil
}

func (m *mockBlockchainForStaking) GetTransaction(hash types.Hash) (*types.Transaction, error) {
	return nil, nil
}

func (m *mockBlockchainForStaking) GetTransactionBlock(hash types.Hash) (*types.Block, error) {
	return nil, nil
}

func (m *mockBlockchainForStaking) GetBlockWithPoSMetadata(hash types.Hash) (*types.Block, error) {
	return m.GetBlock(hash)
}

func (m *mockBlockchainForStaking) ProcessBlock(block *types.Block) error {
	return nil
}

// Create a test logger
func newTestLogger() *logrus.Entry {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)
	return logger.WithField("test", true)
}

func TestIsAtConsensusHeight_NoProvider(t *testing.T) {
	// When no consensus provider is set, should fall back to IBD check
	sw := &StakingWorker{
		consensusProvider:  nil,
		blockchain:         &mockBlockchainForStaking{isIBD: false},
		logger:             newTestLogger(),
	}

	ok, reason := sw.isAtConsensusHeight()
	if !ok {
		t.Errorf("Expected true when no provider and not in IBD, got false: %s", reason)
	}

	// Test IBD fallback
	sw.blockchain = &mockBlockchainForStaking{isIBD: true}
	ok, reason = sw.isAtConsensusHeight()
	if ok {
		t.Error("Expected false when no provider and in IBD")
	}
	if reason != "chain is syncing (IBD)" {
		t.Errorf("Unexpected reason: %s", reason)
	}
}

func TestIsAtConsensusHeight_WithProvider(t *testing.T) {
	tests := []struct {
		name            string
		localHeight     uint32
		consensusHeight uint32
		expectOK        bool
	}{
		{
			name:            "at consensus height",
			localHeight:     10000,
			consensusHeight: 10000,
			expectOK:        true,
		},
		{
			name:            "1 block behind",
			localHeight:     9999,
			consensusHeight: 10000,
			expectOK:        false,
		},
		{
			name:            "3 blocks behind",
			localHeight:     9997,
			consensusHeight: 10000,
			expectOK:        false,
		},
		{
			name:            "far behind consensus",
			localHeight:     5000,
			consensusHeight: 10000,
			expectOK:        false,
		},
		{
			name:            "1 block ahead of consensus",
			localHeight:     10001,
			consensusHeight: 10000,
			expectOK:        false,
		},
		{
			name:            "far ahead of consensus",
			localHeight:     10050,
			consensusHeight: 10000,
			expectOK:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &mockConsensusProvider{
				height:     tt.consensusHeight,
				confidence: 0.9,
				peerCount:  5,
			}

			mockStorage := NewMockStorage()
			dummyBlock := &types.Block{
				Header: &types.BlockHeader{
					Version: 1,
				},
			}
			mockStorage.StoreBlock(dummyBlock)

			sw := &StakingWorker{
				consensusProvider: provider,
				blockchain:        &mockBlockchainForStaking{isIBD: false, blockHeight: tt.localHeight},
				consensus:         &ProofOfStake{storage: mockStorage},
				logger:            newTestLogger(),
			}

			ok, _ := sw.isAtConsensusHeight()
			if ok != tt.expectOK {
				t.Errorf("Expected %v, got %v", tt.expectOK, ok)
			}
		})
	}
}

func TestIsAtConsensusHeight_ProviderError(t *testing.T) {
	// When provider returns error, should fall back to IBD check
	provider := &mockConsensusProvider{
		err: errors.New("consensus unavailable"),
	}

	sw := &StakingWorker{
		consensusProvider:  provider,
		blockchain:         &mockBlockchainForStaking{isIBD: false},
		logger:             newTestLogger(),
	}

	ok, _ := sw.isAtConsensusHeight()
	if !ok {
		t.Error("Expected true when provider error and not in IBD")
	}

	// With IBD
	sw.blockchain = &mockBlockchainForStaking{isIBD: true}
	ok, reason := sw.isAtConsensusHeight()
	if ok {
		t.Error("Expected false when provider error and in IBD")
	}
	if reason != "chain is syncing (IBD, no consensus)" {
		t.Errorf("Unexpected reason: %s", reason)
	}
}

func TestSetConsensusProvider(t *testing.T) {
	sw := &StakingWorker{
		logger: newTestLogger(),
	}

	if sw.consensusProvider != nil {
		t.Error("Expected nil provider initially")
	}

	provider := &mockConsensusProvider{height: 10000}
	sw.SetConsensusProvider(provider)

	if sw.consensusProvider == nil {
		t.Error("Expected provider to be set")
	}
}

func TestSetBlockBroadcaster(t *testing.T) {
	sw := &StakingWorker{
		logger: newTestLogger(),
	}

	if sw.blockBroadcaster != nil {
		t.Error("Expected nil broadcaster initially")
	}

	// Track if broadcaster was called
	called := false
	broadcaster := func(block *types.Block) {
		called = true
	}

	sw.SetBlockBroadcaster(broadcaster)

	if sw.blockBroadcaster == nil {
		t.Error("Expected broadcaster to be set")
	}

	// Verify callback works
	sw.blockBroadcaster(nil)
	if !called {
		t.Error("Expected broadcaster callback to be invoked")
	}
}

func TestDefaultStakingWorkerConfig(t *testing.T) {
	config := DefaultStakingWorkerConfig()

	if config.MaxSearchTime != 30 {
		t.Errorf("Expected default MaxSearchTime=30, got %d", config.MaxSearchTime)
	}
}
