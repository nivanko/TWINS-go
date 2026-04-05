package consensus

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	adjustedtime "github.com/twins-dev/twins-core/internal/time"
	"github.com/twins-dev/twins-core/pkg/types"
)

// ConsensusHeightProvider provides network consensus height information.
// Implemented by p2p.ConsensusValidator to avoid import cycles.
// Uses simple return values instead of struct to avoid type compatibility issues.
type ConsensusHeightProvider interface {
	// GetConsensusHeightInfo returns the current network consensus height.
	// Returns: height, confidence (0.0-1.0), peerCount, error
	// Returns error if consensus cannot be determined (not enough peers, etc.)
	GetConsensusHeightInfo() (height uint32, confidence float64, peerCount int, err error)
}

// maxBlockTxCount is the maximum number of mempool transactions to include in a staked block.
// The legacy C++ CreateNewBlock (miner.cpp:190-460) uses only size and sigops limits with no
// count cap, but a conservative count limit provides an additional safety bound. The real
// constraint is MaxBlockSize (1MB) which limits to ~2000-4000 typical transactions; this
// count cap is unlikely to be the binding constraint in practice.
const maxBlockTxCount = 500

// StakingWorker is the main staking goroutine that searches for valid stakes.
//
// CRITICAL ARCHITECTURE DECISION:
// This worker operates INDEPENDENTLY of masternode sync status.
// It checks:
// 1. Wallet is unlocked
// 2. Local chain is at network consensus height (or IBD fallback if no peers)
// 3. Has stakeable UTXOs
//
// This breaks the circular dependency that killed the legacy C++ chain:
// Legacy: staking -> masternode sync -> blockchain sync (time-based) -> DEADLOCK
// Go: staking -> consensus height check (peer-based) -> works with proper sync
type StakingWorker struct {
	consensus         *ProofOfStake
	wallet            StakingWalletInterface
	blockchain        BlockchainInterface
	builder           *BlockBuilder
	params            *types.ChainParams
	logger            *logrus.Entry
	consensusProvider ConsensusHeightProvider     // Optional: for network consensus check
	paymentValidator  *MasternodePaymentValidator // For masternode/dev payment outputs
	blockBroadcaster  func(*types.Block)          // Callback to broadcast block to P2P network
	mempool           MempoolInterface            // For including mempool transactions in staked blocks

	// Lifecycle management (channel-based, no mutex contention)
	stopCh  chan struct{}
	doneCh  chan struct{}
	mu      sync.Mutex
	running bool

	// Configuration
	searchInterval time.Duration // How often to try staking (default: 1 second)
	maxSearchTime  uint32        // Max future time to search in seconds (default: 60)

	// Statistics
	statsMu          sync.RWMutex
	lastStakeAttempt time.Time
	stakesFound      uint64
	stakesAccepted   uint64
	kernelsChecked   uint64
}

// StakingWorkerConfig contains configuration for the staking worker.
type StakingWorkerConfig struct {
	SearchInterval time.Duration
	MaxSearchTime  uint32
}

// DefaultStakingWorkerConfig returns default configuration.
func DefaultStakingWorkerConfig() *StakingWorkerConfig {
	return &StakingWorkerConfig{
		SearchInterval:     1 * time.Second,
		MaxSearchTime:      30, // Search up to 30 seconds in the future (legacy: nHashDrift = 30)
	}
}

// NewStakingWorker creates a new staking worker.
func NewStakingWorker(
	consensus *ProofOfStake,
	wallet StakingWalletInterface,
	blockchain BlockchainInterface,
	builder *BlockBuilder,
	params *types.ChainParams,
	logger *logrus.Logger,
	config *StakingWorkerConfig,
) *StakingWorker {
	if config == nil {
		config = DefaultStakingWorkerConfig()
	}

	return &StakingWorker{
		consensus:          consensus,
		wallet:             wallet,
		blockchain:         blockchain,
		builder:            builder,
		params:             params,
		logger:             logger.WithField("component", "staking_worker"),
		searchInterval: config.SearchInterval,
		maxSearchTime:  config.MaxSearchTime,
	}
}

// SetConsensusProvider sets the consensus height provider for network sync checking.
// This should be called after P2P layer is initialized to avoid import cycles.
func (sw *StakingWorker) SetConsensusProvider(provider ConsensusHeightProvider) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	sw.consensusProvider = provider
	sw.logger.Debug("Consensus height provider configured")
}

// SetPaymentValidator sets the masternode payment validator for block rewards.
// This should be called after masternode manager is initialized.
// Without this, blocks will not include masternode/dev fund outputs (legacy: FillBlockPayee).
func (sw *StakingWorker) SetPaymentValidator(validator *MasternodePaymentValidator) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	sw.paymentValidator = validator
	sw.logger.Debug("Payment validator configured for masternode/dev outputs")
}

// SetBlockBroadcaster sets the callback to broadcast staked blocks to the P2P network.
// This should be called after P2P layer is initialized to avoid import cycles.
// The callback is typically p2p.Server.RelayBlock.
func (sw *StakingWorker) SetBlockBroadcaster(broadcaster func(*types.Block)) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	sw.blockBroadcaster = broadcaster
	sw.logger.Debug("Block broadcaster configured for P2P relay")
}

// SetMempool sets the mempool for including pending transactions in staked blocks.
// This should be called after mempool is initialized.
// Without this, staked blocks will only contain the coinstake transaction.
func (sw *StakingWorker) SetMempool(mp MempoolInterface) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	sw.mempool = mp
	sw.logger.Debug("Mempool configured for transaction inclusion in staked blocks")
}

// Start begins the staking worker goroutine.
func (sw *StakingWorker) Start(ctx context.Context) error {
	sw.mu.Lock()
	if sw.running {
		sw.mu.Unlock()
		return fmt.Errorf("staking worker already running")
	}
	sw.running = true
	sw.stopCh = make(chan struct{})
	sw.doneCh = make(chan struct{})
	sw.mu.Unlock()

	go sw.run(ctx)

	sw.logger.Info("Staking worker started")
	return nil
}

// Stop gracefully stops the staking worker.
func (sw *StakingWorker) Stop() error {
	sw.mu.Lock()
	if !sw.running {
		sw.mu.Unlock()
		return fmt.Errorf("staking worker not running")
	}
	close(sw.stopCh)
	sw.mu.Unlock()

	// Wait for worker to finish
	<-sw.doneCh

	sw.mu.Lock()
	sw.running = false
	sw.mu.Unlock()

	sw.logger.Info("Staking worker stopped")
	return nil
}

// IsRunning returns whether the worker is currently running.
func (sw *StakingWorker) IsRunning() bool {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	return sw.running
}

// run is the main staking loop.
func (sw *StakingWorker) run(ctx context.Context) {
	defer close(sw.doneCh)

	ticker := time.NewTicker(sw.searchInterval)
	defer ticker.Stop()

	sw.logger.Debug("Staking worker loop started")

	for {
		select {
		case <-ctx.Done():
			sw.logger.Debug("Staking worker context cancelled")
			return

		case <-sw.stopCh:
			sw.logger.Debug("Staking worker stop signal received")
			return

		case <-ticker.C:
			sw.tryStake()
		}
	}
}

// tryStake attempts to find a valid stake and create a block.
func (sw *StakingWorker) tryStake() {
	sw.statsMu.Lock()
	sw.lastStakeAttempt = time.Now()
	sw.statsMu.Unlock()

	// CRITICAL: Check staking prerequisites
	// Note: NO masternode sync check here - this is intentional!
	canStake, reason := sw.canStake()
	if !canStake {
		sw.logger.WithField("reason", reason).Debug("Cannot stake")
		return
	}

	// Get chain tip
	tipHash, err := sw.consensus.storage.GetChainTip()
	if err != nil {
		sw.logger.WithError(err).Debug("Failed to get chain tip")
		return
	}

	tipBlock, err := sw.blockchain.GetBlock(tipHash)
	if err != nil {
		sw.logger.WithError(err).Debug("Failed to get tip block")
		return
	}

	tipHeight, err := sw.blockchain.GetBlockHeight(tipHash)
	if err != nil {
		sw.logger.WithError(err).Debug("Failed to get tip height")
		return
	}

	// Get current time for staking using network-adjusted time
	// Legacy: GetAdjustedTime() in timedata.cpp
	currentTime := uint32(adjustedtime.GetAdjustedUnix())

	// Get stakeable UTXOs
	utxos, err := sw.wallet.GetStakeableUTXOs(tipHeight, currentTime)
	if err != nil {
		sw.logger.WithError(err).Debug("Failed to get stakeable UTXOs")
		return
	}

	if len(utxos) == 0 {
		sw.logger.Debug("No stakeable UTXOs available")
		return
	}

	// CRITICAL: Calculate difficulty retarget for the NEW block (legacy miner.cpp:152)
	// C++: pblock->nBits = GetNextWorkRequired(pindexPrev, pblock)
	// We need to calculate what nBits should be for the block we're creating,
	// NOT just use the previous block's bits
	newBlockHeight := tipHeight + 1

	// Create a temporary header for the new block to calculate its target
	newBlockHeader := &types.BlockHeader{
		Version:       types.CurrentBlockVersion,
		PrevBlockHash: tipHash,
		Timestamp:     currentTime,
	}

	// Use the same CalculateNextWorkRequired that the validator uses
	// This ensures identical calculation path and data source, preventing
	// difficulty mismatch errors between staker and validator
	nextBits, err := sw.consensus.CalculateNextWorkRequired(newBlockHeader, newBlockHeight)
	if err != nil {
		sw.logger.WithError(err).Debug("Failed to calculate next target")
		return
	}

	// Convert bits to target for kernel hash comparison
	target := GetTargetFromBits(nextBits)

	// Try each UTXO
	for _, utxo := range utxos {
		block, err := sw.tryStakeWithUTXO(utxo, tipBlock, tipHeight, currentTime, target)
		if err != nil {
			continue // Try next UTXO
		}

		if block != nil {
			sw.handleSuccessfulStake(block)
			return // Only create one block per attempt
		}
	}
}

// canStake checks if staking is currently possible.
// CRITICAL: This does NOT check masternode sync status!
func (sw *StakingWorker) canStake() (bool, string) {
	// 1. Wallet must be unlocked
	if sw.wallet.IsLocked() {
		return false, "wallet is locked"
	}

	// 2. Check network consensus height (preferred) or fall back to IBD check
	if ok, reason := sw.isAtConsensusHeight(); !ok {
		return false, reason
	}

	// EXPLICITLY: NO masternode sync check here!
	// This is the critical architectural decision that prevents the
	// deadlock that killed the legacy C++ chain.
	// Masternode payments are validated during block validation,
	// not as a prerequisite for block creation.

	return true, ""
}

// isAtConsensusHeight checks if local chain is at network consensus height.
// Falls back to IBD check if consensus provider is not available or has no peers.
func (sw *StakingWorker) isAtConsensusHeight() (bool, string) {
	// If no consensus provider configured, fall back to IBD check
	if sw.consensusProvider == nil {
		if sw.blockchain.IsInitialBlockDownload() {
			return false, "chain is syncing (IBD)"
		}
		return true, ""
	}

	// Try to get network consensus height
	consensusHeight, confidence, peerCount, err := sw.consensusProvider.GetConsensusHeightInfo()
	if err != nil {
		// Consensus unavailable (not enough peers, etc.) - fall back to IBD check
		sw.logger.WithError(err).Debug("Consensus height unavailable, falling back to IBD check")
		if sw.blockchain.IsInitialBlockDownload() {
			return false, "chain is syncing (IBD, no consensus)"
		}
		return true, ""
	}

	// Get local chain height
	tipHash, err := sw.consensus.storage.GetChainTip()
	if err != nil {
		return false, fmt.Sprintf("failed to get chain tip: %v", err)
	}

	localHeight, err := sw.blockchain.GetBlockHeight(tipHash)
	if err != nil {
		return false, fmt.Sprintf("failed to get local height: %v", err)
	}

	// Staking requires exact match with consensus height.
	// Behind: our staked block will be orphaned when we sync the missing blocks.
	// Ahead: we may be on a fork; staking extends the wrong chain.
	if localHeight != consensusHeight {
		direction := "behind"
		if localHeight > consensusHeight {
			direction = "ahead of"
		}
		return false, fmt.Sprintf("%s consensus height: local=%d, consensus=%d (confidence=%.1f%%, peers=%d)",
			direction, localHeight, consensusHeight, confidence*100, peerCount)
	}

	return true, ""
}

// tryStakeWithUTXO attempts to find a valid stake kernel for a specific UTXO.
// NOTE: Only regular UTXO staking is supported.
// zTWINS/zPoS disabled via SPORK_16_ZEROCOIN_MAINTENANCE_MODE on mainnet.
// See legacy/src/wallet.cpp:2426 for zTWINS stake selection (disabled).
func (sw *StakingWorker) tryStakeWithUTXO(
	utxo *StakeableUTXO,
	tipBlock *types.Block,
	tipHeight uint32,
	currentTime uint32,
	target *big.Int,
) (*types.Block, error) {
	// Convert to StakeInput
	stakeInput := utxo.ToStakeInput()

	// Get stake modifier for this UTXO
	utxoBlock, err := sw.blockchain.GetBlockByHeight(utxo.BlockHeight)
	if err != nil {
		return nil, fmt.Errorf("failed to get UTXO block: %w", err)
	}

	modifier, _, _, err := sw.consensus.modifierCache.GetKernelStakeModifier(utxoBlock.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to get stake modifier: %w", err)
	}

	// Hash drift loop - matches legacy kernel.cpp:336-352 Stake() function
	// Legacy iterates 30 times BACKWARD from (currentTime + 30):
	//   for (int i = 0; i < nHashDrift; i++) { nTryTime = nTimeTx + nHashDrift - i; }
	// This searches timestamps from (currentTime+30) down to (currentTime+1)
	const hashDrift = 30

	// Record starting height to detect new blocks during search (legacy: kernel.cpp:339)
	startHeight := tipHeight

	for i := 0; i < hashDrift; i++ {
		// Check if a new block came in - abort search (legacy: kernel.cpp:339-340)
		// if (chainActive.Height() != nHeightStart) break;
		// Uses atomic bestHeight.Load() — nanosecond in-memory read, no DB lookup
		currentHeight, err := sw.blockchain.GetBestHeight()
		if err == nil && currentHeight != startHeight {
			sw.logger.Debug("New block detected during stake search, aborting")
			return nil, nil
		}

		// Calculate try time - searching BACKWARD from future (legacy compliance)
		tryTime := currentTime + uint32(hashDrift) - uint32(i)

		// Ensure block time is after previous block
		if tryTime <= tipBlock.Header.Timestamp {
			continue
		}

		sw.statsMu.Lock()
		sw.kernelsChecked++
		sw.statsMu.Unlock()

		// Check if kernel is valid
		isValid, kernelHash := CheckStakeKernelHash(modifier, stakeInput, tryTime, target, sw.params)
		if !isValid {
			continue
		}

		sw.logger.WithFields(logrus.Fields{
			"utxo":        utxo.Outpoint.String(),
			"block_time":  tryTime,
			"kernel_hash": kernelHash.String(),
			"modifier":    modifier,
			"drift_iter":  i,
		}).Info("Found valid stake kernel!")

		// Create the block with retargeted difficulty
		block, err := sw.createStakeBlock(utxo, tipBlock, tipHeight, tryTime, target)
		if err != nil {
			sw.logger.WithError(err).Error("Failed to create stake block")
			return nil, err
		}

		sw.statsMu.Lock()
		sw.stakesFound++
		sw.statsMu.Unlock()

		return block, nil
	}

	return nil, nil // No valid kernel found in hash drift range
}

// createStakeBlock creates a complete signed stake block.
func (sw *StakingWorker) createStakeBlock(
	utxo *StakeableUTXO,
	prevBlock *types.Block,
	prevHeight uint32,
	blockTime uint32,
	target *big.Int, // Retargeted difficulty from CalculateNextTarget
) (*types.Block, error) {
	// Calculate block reward
	newHeight := prevHeight + 1
	blockReward := sw.builder.GetBlockReward(newHeight)

	// Create coinstake transaction
	coinstakeTx, err := sw.wallet.CreateCoinstakeTx(utxo, blockReward, blockTime)
	if err != nil {
		return nil, fmt.Errorf("failed to create coinstake: %w", err)
	}

	// Add masternode and dev fund outputs (legacy: FillBlockPayee)
	// CRITICAL: Must be done BEFORE signing to include in transaction hash
	// Legacy: wallet.cpp:3337 FillBlockPayee() adds outputs, then 3341 CreateTxIn() signs
	if sw.paymentValidator != nil {
		prevHash := prevBlock.Header.Hash()
		err := sw.paymentValidator.FillBlockPayment(coinstakeTx, newHeight, blockReward, prevHash, true)
		if err != nil {
			sw.logger.WithError(err).Warn("Failed to add masternode/dev payments, block may be rejected")
			// Continue anyway - block might still be valid without MN payments during early sync
		}
	} else {
		sw.logger.Debug("Payment validator not configured, skipping masternode/dev outputs")
	}

	// Sign the coinstake transaction AFTER all outputs are added
	// Legacy: wallet.cpp:3341 - stakeInput->CreateTxIn(this, in, hashTxOut)
	if err := sw.wallet.SignCoinstakeTx(coinstakeTx, utxo); err != nil {
		return nil, fmt.Errorf("failed to sign coinstake: %w", err)
	}

	// Convert target to compact bits format for block header.
	// Use consensus encoder to match validation path exactly.
	// CRITICAL: Use retargeted bits, not prevBlock.Header.Bits (legacy miner.cpp:152)
	bits := GetBitsFromTarget(target)

	// Collect pending mempool transactions for inclusion in the block
	// Use lock-copy-invoke pattern to safely read the mempool reference
	sw.mu.Lock()
	mp := sw.mempool
	sw.mu.Unlock()

	var mempoolTxs []*types.Transaction
	if mp != nil {
		mempoolTxs = mp.GetTransactionsForBlock(sw.params.MaxBlockSize, maxBlockTxCount)
		if len(mempoolTxs) > 0 {
			sw.logger.WithField("tx_count", len(mempoolTxs)).Debug("Including mempool transactions in stake block")
		}
	}

	// Create block template with mempool transactions
	block, err := sw.builder.CreateStakeBlock(coinstakeTx, prevBlock, prevHeight, blockTime, bits, mempoolTxs)
	if err != nil {
		return nil, fmt.Errorf("failed to create block: %w", err)
	}

	// Sign the block
	if err := sw.builder.SignBlock(block, sw.wallet); err != nil {
		return nil, fmt.Errorf("failed to sign block: %w", err)
	}

	return block, nil
}

// handleSuccessfulStake processes a successfully created stake block.
func (sw *StakingWorker) handleSuccessfulStake(block *types.Block) {
	blockHash := block.Header.Hash()

	// Network difficulty recovery mechanism:
	// After long network pause, difficulty crashes due to ppcoin retarget formula.
	// At low difficulty, blocks can be created every second (spam).
	// Solution: enforce minimum 10-second gap between blocks.
	// This allows difficulty to recover naturally (+17.9% per block at 10s gap).
	// Full recovery from crash takes ~80 blocks / ~13 minutes.
	const minBlockGap uint32 = 10 // seconds

	prevBlock, err := sw.blockchain.GetBlock(block.Header.PrevBlockHash)
	if err == nil && prevBlock != nil {
		blockGap := block.Header.Timestamp - prevBlock.Header.Timestamp

		if blockGap < minBlockGap {
			sw.logger.WithFields(logrus.Fields{
				"hash":      blockHash.String(),
				"block_gap": blockGap,
				"min_gap":   minBlockGap,
				"prev_time": prevBlock.Header.Timestamp,
				"new_time":  block.Header.Timestamp,
			}).Debug("Block gap too small, waiting before retry to allow difficulty recovery")

			// Wait for block gap with shutdown awareness - will retry on next stake attempt
			select {
			case <-sw.stopCh:
				return
			case <-time.After(time.Duration(minBlockGap) * time.Second):
			}
			return
		}
	}

	sw.logger.WithFields(logrus.Fields{
		"hash":   blockHash.String(),
		"height": "pending",
	}).Info("Successfully created stake block, submitting...")

	// Process the block through blockchain layer (validates, stores to DB, updates UTXOs)
	if err := sw.blockchain.ProcessBlock(block); err != nil {
		sw.logger.WithError(err).Error("Failed to process stake block locally")
		return
	}

	sw.statsMu.Lock()
	sw.stakesAccepted++
	sw.statsMu.Unlock()

	sw.logger.WithField("hash", blockHash.String()).Info("Stake block accepted!")

	// Note: Mempool cleanup (RemoveConfirmedTransactions) is handled by
	// unified_processor.processBatchInternal after ProcessBlock commits,
	// so we don't call it here to avoid double-removal.

	// Broadcast block to P2P network (legacy: RelayBlock in miner.cpp)
	// Use lock-copy-invoke pattern to prevent race conditions
	sw.mu.Lock()
	broadcaster := sw.blockBroadcaster
	sw.mu.Unlock()

	if broadcaster != nil {
		broadcaster(block)
		sw.logger.WithField("hash", blockHash.String()).Info("Stake block broadcast to network")
	} else {
		sw.logger.Warn("No block broadcaster configured - stake block not relayed to peers")
	}

	// Cooldown after successful stake to let the network propagate the block
	// before attempting to stake again. Shutdown-aware via stopCh.
	const postStakeCooldown = 10 * time.Second
	select {
	case <-sw.stopCh:
		return
	case <-time.After(postStakeCooldown):
	}
}

// GetStats returns staking worker statistics.
func (sw *StakingWorker) GetStats() StakingWorkerStats {
	sw.statsMu.RLock()
	defer sw.statsMu.RUnlock()

	return StakingWorkerStats{
		LastStakeAttempt: sw.lastStakeAttempt,
		StakesFound:      sw.stakesFound,
		StakesAccepted:   sw.stakesAccepted,
		KernelsChecked:   sw.kernelsChecked,
		IsRunning:        sw.running,
	}
}

// StakingWorkerStats contains statistics about staking activity.
type StakingWorkerStats struct {
	LastStakeAttempt time.Time
	StakesFound      uint64
	StakesAccepted   uint64
	KernelsChecked   uint64
	IsRunning        bool
}

