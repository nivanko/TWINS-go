// Package p2p implements peer-to-peer networking and blockchain synchronization
// for TWINS cryptocurrency. It provides:
//
// - Block synchronization with parent-chain ordering (sync.go:2258-2330)
// - Peer health tracking and adaptive scoring (sync_health.go)
// - Network consensus calculation (sync_consensus.go)
// - Protocol message handlers (handlers.go)
// - Peer discovery and management (discovery.go)
//
// Key Components:
//
// BlockchainSyncer - Main sync orchestration with batch processing
// PeerHealthTracker - Adaptive peer scoring based on behavior
// ConsensusValidator - Network consensus calculation with outlier detection
// SyncStateMachine - Sync state management (IBD, FAST_SYNC, SYNCED)
//
// Critical Patterns:
//
// Parent-Chain Ordering (sync.go:2258-2330):
//
//	Blocks arrive asynchronously and out of order from peers.
//	Reconstructs correct block order by analyzing parent-child relationships.
//	Uses cryptographic verification (actual parent hashes), not trust.
//	Detects malicious batches (cycles, gaps, wrong chains).
//	O(n) complexity with O(1) lookups to prevent DoS attacks.
//
// Pre-validation (sync.go:2332-2389):
//
//	Validates batch integrity before expensive ProcessBlockBatch():
//	- First block parent exists (ensures batch connects to our chain)
//	- Batch continuity (each block references previous block)
//	- Sequencing gap detection (distinguishes our fault from peer fault)
//
// Peer Fault Handling:
//
//	Penalize peer: Wrong chain, non-sequential batch, checkpoint failure
//	Don't penalize: Sequencing gap, block exists, database errors
//
// Error Handling:
//
// Uses blockchain package typed errors with errors.Is():
//   - blockchainpkg.ErrCheckpointFailed triggers recovery
//   - blockchainpkg.ErrParentNotFound triggers recovery
//   - blockchainpkg.ErrSequencingGap rotates peer (not their fault)
//
// Integration:
//
// The p2p package integrates with:
//   - blockchain package for block processing and recovery
//   - protocol package for Bitcoin-compatible message encoding
//   - network package for connection management
//
// See Also:
//   - internal/CLAUDE.md for architecture overview
//   - sync.go:2258-2330 for parent-chain ordering algorithm
//   - sync_health.go for peer scoring details
package p2p

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
	blockchainpkg "github.com/twins-dev/twins-core/internal/blockchain"
	"github.com/twins-dev/twins-core/internal/storage"
	"github.com/twins-dev/twins-core/pkg/types"
)

// ConsensusEngine interface for block validation
type ConsensusEngine interface {
	ValidateBlock(block *types.Block) error
}

// RateSample represents a single rate measurement sample
type RateSample struct {
	timestamp time.Time
	blocks    uint64
	duration  time.Duration
}

// RateWindow manages a sliding window of rate samples for spike-resistant metrics
type RateWindow struct {
	samples        [60]RateSample // 60 samples = 1 minute window at 1 sample/sec
	index          int
	sampleCount    int // Track actual samples (before buffer is full)
	lastSampleTime time.Time
	mu             sync.RWMutex
}

// NewRateWindow creates a new rate window tracker
func NewRateWindow() *RateWindow {
	return &RateWindow{}
}

// AddSample adds a new rate sample to the sliding window
func (rw *RateWindow) AddSample(blocks uint64, duration time.Duration) {
	rw.mu.Lock()
	defer rw.mu.Unlock()

	now := time.Now()
	rw.samples[rw.index] = RateSample{
		timestamp: now,
		blocks:    blocks,
		duration:  duration,
	}
	rw.lastSampleTime = now

	rw.index = (rw.index + 1) % len(rw.samples)
	if rw.sampleCount < len(rw.samples) {
		rw.sampleCount++
	}
}

// TimeSinceLastSample returns duration since the last sample was added
func (rw *RateWindow) TimeSinceLastSample() time.Duration {
	rw.mu.RLock()
	defer rw.mu.RUnlock()
	if rw.lastSampleTime.IsZero() {
		return 0
	}
	return time.Since(rw.lastSampleTime)
}

// GetRate calculates the rate over the specified number of recent samples
func (rw *RateWindow) GetRate(samples int) float64 {
	rw.mu.RLock()
	defer rw.mu.RUnlock()

	if rw.sampleCount == 0 || samples <= 0 {
		return 0
	}

	// Limit to actual samples available
	if samples > rw.sampleCount {
		samples = rw.sampleCount
	}

	var totalBlocks uint64
	var totalDuration time.Duration
	validSamples := 0

	// Walk backwards from most recent sample
	for i := 0; i < samples; i++ {
		idx := (rw.index - 1 - i + len(rw.samples)) % len(rw.samples)
		sample := rw.samples[idx]

		// Only use samples from last 5 minutes
		if !sample.timestamp.IsZero() && time.Since(sample.timestamp) < 5*time.Minute {
			totalBlocks += sample.blocks
			totalDuration += sample.duration
			validSamples++
		}
	}

	if validSamples == 0 || totalDuration.Seconds() == 0 {
		return 0
	}

	return float64(totalBlocks) / totalDuration.Seconds()
}

// GetMinRate returns the minimum rate from recent samples for conservative estimation.
// Using minimum instead of median prevents false high values during error recovery.
func (rw *RateWindow) GetMinRate() float64 {
	rw.mu.RLock()
	defer rw.mu.RUnlock()

	if rw.sampleCount == 0 {
		return 0
	}

	// Collect rates from different time windows
	rates := []float64{
		rw.getRateInternal(10), // Last 10 seconds
		rw.getRateInternal(30), // Last 30 seconds
		rw.getRateInternal(60), // Last 60 seconds
	}

	// Use minimum for conservative estimation during unstable sync
	minRate := rates[0]
	for _, r := range rates[1:] {
		if r > 0 && (minRate == 0 || r < minRate) {
			minRate = r
		}
	}
	return minRate
}

// getRateInternal calculates rate without locking (must be called with lock held)
func (rw *RateWindow) getRateInternal(samples int) float64 {
	if rw.sampleCount == 0 || samples <= 0 {
		return 0
	}

	if samples > rw.sampleCount {
		samples = rw.sampleCount
	}

	var totalBlocks uint64
	var totalDuration time.Duration

	for i := 0; i < samples; i++ {
		idx := (rw.index - 1 - i + len(rw.samples)) % len(rw.samples)
		sample := rw.samples[idx]

		if !sample.timestamp.IsZero() && time.Since(sample.timestamp) < 5*time.Minute {
			totalBlocks += sample.blocks
			totalDuration += sample.duration
		}
	}

	if totalDuration.Seconds() == 0 {
		return 0
	}

	return float64(totalBlocks) / totalDuration.Seconds()
}

// BlockchainSyncer handles blockchain synchronization with peers
type BlockchainSyncer struct {
	// Configuration
	storage     storage.Storage
	blockchain  blockchainpkg.Blockchain // Proper blockchain layer for validation
	consensus   ConsensusEngine
	chainParams *types.ChainParams
	logger      *logrus.Entry
	server      *Server

	// Sync state
	syncing       atomic.Bool
	started       atomic.Bool
	initialSync   atomic.Bool
	syncPeer      atomic.Pointer[Peer]
	bestHeight    atomic.Uint32
	syncHeight    atomic.Uint32
	lastBlockTime atomic.Int64

	// Enhanced sync components
	healthTracker      *PeerHealthTracker
	peerList           *SyncPeerList
	stateMachine       *SyncStateMachine
	consensusValidator *ConsensusValidator
	currentBatchPeer   string
	currentBatchStart  time.Time
	batchMu            sync.RWMutex

	// Block management
	pendingBlocks  map[types.Hash]*PendingBlock // Blocks waiting for dependencies
	requestedBlock map[types.Hash]*BlockRequest // Blocks we've requested
	blockMu        sync.RWMutex

	// Peer sync state
	peerStates map[string]*PeerSyncState
	peerMu     sync.RWMutex

	// Channels for coordination
	newBlocks        chan *peerBlock
	blockRequests    chan types.Hash
	syncRequests     chan *Peer
	invAnnouncements chan *InvAnnouncement

	// Synchronous batch processing
	pendingInv      chan []InventoryVector // INV messages during sync
	incomingBlocks  chan *types.Block      // Blocks during sync batch
	batchInProgress atomic.Bool            // True when processing a batch

	// Sync health monitoring
	lastSyncProgress time.Time     // Last time we made progress
	syncStallTimeout time.Duration // Timeout for stall detection

	// Statistics
	blocksDownloaded  atomic.Uint64
	headersDownloaded atomic.Uint64
	duplicateBlocks   atomic.Uint64
	invalidBlocks     atomic.Uint64
	syncStartTime     time.Time
	lastInvSize       atomic.Int32  // Track last inventory size to detect 500-block limit
	peerTipHeight     atomic.Uint32 // Track the peer's reported tip height
	lastProgressLog      time.Time      // Track when we last logged progress
	progressLogInterval  time.Duration  // Interval between progress log messages (from config)

	// Rate tracking for spike-resistant metrics
	rateWindow     *RateWindow
	lastRateUpdate time.Time
	lastRateBlocks uint64

	// Batch tracking for peer rotation
	batchStartHeight    atomic.Uint32 // Height when current batch started
	batchProcessedCount atomic.Uint32 // Blocks processed in current batch
	lastRequestTime     atomic.Int64  // Unix timestamp of last getblocks request

	// Sticky peer sync tracking
	batchesFromCurrentPeer    atomic.Uint32 // Number of batches downloaded from current peer
	lastHashContinueTime      atomic.Int64  // Unix timestamp of last hashContinue detection
	expectingHashContinue     atomic.Bool   // True when expecting hashContinue inv
	consecutivePartialBatches atomic.Uint32 // Track consecutive partial batches before rotation
	lastBatchDurationMs       atomic.Int64  // Duration of last batch in milliseconds

	// Consensus tracking
	consensusHeight     atomic.Uint32
	consensusConfidence atomic.Value // float64

	// IBD (Initial Block Download) mode
	isInIBD           atomic.Bool  // True when in Initial Block Download mode
	ibdThreshold      uint32       // Blocks behind to trigger IBD mode (default: 1000)
	recentBlocksQueue []types.Hash // Queue for recent blocks received during IBD

	// Shutdown coordination
	shutdown  chan struct{} // Signal to stop sync goroutine
	done      chan struct{} // Signal that sync goroutine has exited
	doneMu    sync.RWMutex  // Protects done channel access during recreation
	closeOnce sync.Once     // Ensures done channel is closed only once

	// Height-based inventory filtering
	maxHeightAhead    uint32                      // Maximum blocks ahead to process immediately (default: 1000)
	deferredInventory map[types.Hash]*DeferredInv // Inventory too far ahead, deferred for later
	deferredMu        sync.RWMutex                // Mutex for deferred inventory

	// Masternode peer management
	masternodePeers *MasternodePeerManager

	// Block processed callback for masternode winner voting
	// Called with the new chain tip height after successful block batch processing
	// Used by masternode layer to trigger winner vote generation
	onBlockProcessedCallback func(height uint32)
	callbackMu               sync.RWMutex

	// Fork search state - for detecting chain divergence during sync
	// When all blocks in a batch already exist, we may be on a different fork
	// Virtual tip allows requesting blocks as if we're at a lower height
	forkSearchMode        atomic.Bool   // True when actively searching for fork
	forkSearchStartHeight atomic.Uint32 // Height when fork search began
	forkSearchVirtualTip  atomic.Uint32 // Virtual tip for locator generation
	forkSearchDepth       atomic.Uint32 // Current search depth (max 10,000)

	// Proactive fork detection via protocol 70928 chainstate queries
	forkDetectionMu sync.Mutex // Protects lastForkCheck
	lastForkCheck   time.Time  // Time of last fork check cycle

	// Reactive validation failure tracking
	// When the same block fails validation from multiple peers, this signals
	// local index corruption rather than a peer problem.
	validationFailures   map[types.Hash]*validationFailureRecord
	validationFailuresMu sync.Mutex
	recoveryInProgress   atomic.Bool // Singleflight guard for tryReactiveIndexRecovery

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	quit   chan struct{}
}

// PendingBlock represents a block waiting for its dependencies
type PendingBlock struct {
	Block    *types.Block
	Peer     *Peer
	Received time.Time
	Retries  int32
}

// peerBlock wraps a block with the address of the peer that delivered it.
// Used by the single-block processing channel to track block provenance
// for reactive validation failure detection.
type peerBlock struct {
	block    *types.Block
	peerAddr string // Empty when peer is unknown (e.g. pending blocks)
}

// BlockRequest represents a block download request
type BlockRequest struct {
	Hash      types.Hash
	Peer      *Peer
	Requested time.Time
	Timeout   time.Time
	Retries   int32
}

// PeerSyncState tracks synchronization state for each peer
type PeerSyncState struct {
	BestHeight       uint32
	LastUpdate       time.Time
	Syncing          bool
	BlocksRequested  int32
	BlocksReceived   int32
	HeadersRequested int32
	HeadersReceived  int32
	LastBlockHash    types.Hash
}

// InvAnnouncement represents an inventory announcement from a peer
type InvAnnouncement struct {
	Peer *Peer
	Inv  []InventoryVector
}

// DeferredInv represents an inventory item deferred due to being too far ahead
type DeferredInv struct {
	Hash            types.Hash
	Type            uint32
	ReceivedFrom    *Peer
	ReceivedAt      time.Time
	EstimatedHeight uint32 // Estimated height if known
}

// Sync constants
const (
	MaxBlocksInTransit    = 128              // Maximum blocks downloading simultaneously
	BlockDownloadTimeout  = 10 * time.Minute // Timeout for block downloads
	SyncInactivityTimeout = 30 * time.Second // Timeout when no blocks received
	SyncStallTimeout      = 60 * time.Second // Timeout when no height progress
	BlockSyncBatchSize    = 128              // Blocks to request in each batch

	// Sticky peer sync constants
	SlowBatchThreshold  = 5 * time.Second  // Rotate peer if batch takes longer than this
	HashContinueTimeout = 10 * time.Second // Timeout waiting for hashContinue inv

	// Reactive validation failure detection constants
	// When the same block fails validation from this many unique peers,
	// trigger a targeted index consistency check.
	ValidationFailurePeerThreshold = 3
	// Maximum age of a failure record before it's cleaned up
	ValidationFailureMaxAge = 10 * time.Minute
)

// validationFailureRecord tracks repeated validation failures for a specific block.
// When the same block fails from multiple peers, this signals local index corruption.
type validationFailureRecord struct {
	blockHash  types.Hash
	height     uint32          // Estimated height (from error or chain tip)
	errorMsg   string          // First error message seen
	peers      map[string]bool // Unique peer addresses that failed
	firstSeen  time.Time
	lastSeen   time.Time
	recovered  bool // True if recovery was already attempted
}

// NewBlockchainSyncer creates a new blockchain syncer
func NewBlockchainSyncer(storage storage.Storage, blockchain blockchainpkg.Blockchain, chainParams *types.ChainParams, logger *logrus.Entry, server *Server) *BlockchainSyncer {
	ctx, cancel := context.WithCancel(context.Background())

	syncer := &BlockchainSyncer{
		storage:        storage,
		blockchain:     blockchain,
		chainParams:    chainParams,
		logger:         logger.WithField("component", "blockchain-sync"),
		server:         server,
		pendingBlocks:  make(map[types.Hash]*PendingBlock),
		requestedBlock: make(map[types.Hash]*BlockRequest),
		peerStates:     make(map[string]*PeerSyncState),

		newBlocks:        make(chan *peerBlock, 100),
		blockRequests:    make(chan types.Hash, MaxBlocksInTransit),
		syncRequests:     make(chan *Peer, 10),
		invAnnouncements: make(chan *InvAnnouncement, 100),

		// Synchronous batch processing
		pendingInv:     make(chan []InventoryVector, 500),
		incomingBlocks: make(chan *types.Block, 500),

		// Sync health monitoring
		lastSyncProgress:    time.Now(),
		syncStallTimeout:    2 * time.Minute,
		progressLogInterval: 10 * time.Second, // Default; overridden by config below

		// IBD settings
		ibdThreshold:      blockchainpkg.DefaultIBDThreshold, // Overridden by config below
		recentBlocksQueue: make([]types.Hash, 0, 1000),       // Buffer for recent blocks

		// Height filtering settings
		maxHeightAhead:    1000, // Don't process blocks >1000 ahead of current position
		deferredInventory: make(map[types.Hash]*DeferredInv),

		// Reactive validation failure tracking
		validationFailures: make(map[types.Hash]*validationFailureRecord),

		// Rate tracking
		rateWindow:     NewRateWindow(),
		lastRateUpdate: time.Now(),

		// Shutdown coordination
		shutdown: make(chan struct{}),
		done:     make(chan struct{}),

		ctx:    ctx,
		cancel: cancel,
		quit:   make(chan struct{}),
	}
	syncer.consensusConfidence.Store(float64(0))

	// Initialize masternode peer manager
	syncer.masternodePeers = NewMasternodePeerManager(logger.Logger)

	// Initialize enhanced sync components
	// Use server's shared health tracker if available, otherwise create new one
	if server != nil && server.healthTracker != nil {
		syncer.healthTracker = server.healthTracker
		logger.Debug("Using server's shared health tracker for sync")
	} else {
		syncer.healthTracker = NewPeerHealthTracker()
		logger.Debug("Created new health tracker for sync (server health tracker not available)")
	}
	syncer.peerList = NewSyncPeerList(syncer.healthTracker)
	syncer.consensusValidator = NewConsensusValidator(syncer.healthTracker)

	// Initialize state machine
	syncer.stateMachine = NewSyncStateMachine(
		syncer.peerList,
		syncer.healthTracker,
		syncer.consensusValidator,
		logger.WithField("component", "statemachine"),
	)

	// Apply sync config from server configuration.
	// Both twinsd and twins-gui flow through this path via daemon.InitP2P.
	if server != nil && server.config != nil {
		syncCfg := server.config.Sync

		// IBD threshold
		if syncCfg.IBDThreshold > 0 {
			syncer.ibdThreshold = syncCfg.IBDThreshold
			syncer.stateMachine.ibdThreshold = syncCfg.IBDThreshold
		} else {
			syncer.stateMachine.ibdThreshold = syncer.ibdThreshold
		}

		// Consensus strategy
		switch syncCfg.ConsensusStrategy {
		case "all":
			syncer.consensusValidator.SetDefaultStrategy(StrategyAll)
		default: // "outbound_only" or empty
			syncer.consensusValidator.SetDefaultStrategy(StrategyOutboundOnly)
		}

		// Max sync peers
		if syncCfg.MaxSyncPeers > 0 {
			syncer.peerList.SetMaxPeers(syncCfg.MaxSyncPeers)
		}

		// Batch timeout (rotate/restart if no progress)
		if syncCfg.BatchTimeout > 0 {
			syncer.syncStallTimeout = time.Duration(syncCfg.BatchTimeout) * time.Second
		}

		// Reorg protection
		if syncCfg.ReorgWindow > 0 {
			syncer.stateMachine.SetReorgWindow(time.Duration(syncCfg.ReorgWindow) * time.Second)
		}
		// Always apply MaxAutoReorgs — 0 is a valid value meaning "disable auto-reorgs"
		syncer.stateMachine.SetMaxAutoReorgs(syncCfg.MaxAutoReorgs)

		// Progress logging interval
		if syncCfg.ProgressLogInterval > 0 {
			syncer.progressLogInterval = time.Duration(syncCfg.ProgressLogInterval) * time.Second
		}

		logger.WithFields(logrus.Fields{
			"ibdThreshold":        syncer.ibdThreshold,
			"consensusStrategy":   syncCfg.ConsensusStrategy,
			"maxSyncPeers":        syncCfg.MaxSyncPeers,
			"batchTimeout":        syncer.syncStallTimeout,
			"reorgWindow":         syncCfg.ReorgWindow,
			"maxAutoReorgs":       syncCfg.MaxAutoReorgs,
			"progressLogInterval": syncer.progressLogInterval,
		}).Debug("Sync config applied from settings")
	} else {
		// No server config — use defaults (test scenarios)
		syncer.stateMachine.ibdThreshold = syncer.ibdThreshold
	}

	// Set state machine callbacks
	syncer.stateMachine.SetStateChangeCallback(syncer.handleStateChange)

	syncer.stateMachine.SetMempoolControlCallback(func(enable bool) {
		if enable {
			syncer.logger.Debug("Mempool enabled by state machine")
			// Resume mempool operations after sync completes
			if syncer.server != nil && syncer.server.mempool != nil {
				// Clear any stale transactions from during sync
				if err := syncer.server.mempool.Clear(); err != nil {
					syncer.logger.WithError(err).Warn("Failed to clear mempool after sync")
				}
				// Mempool will accept new transactions via normal tx message handling
				syncer.logger.Debug("Mempool ready to accept transactions")
			}
		} else {
			syncer.logger.Debug("Mempool disabled by state machine")
			// Clear mempool during initial sync to avoid memory issues
			if syncer.server != nil && syncer.server.mempool != nil {
				if err := syncer.server.mempool.Clear(); err != nil {
					syncer.logger.WithError(err).Warn("Failed to clear mempool for sync")
				}
				syncer.logger.Debug("Mempool cleared for blockchain sync")
			}
		}
	})

	return syncer
}

// SetBlockProcessedCallback sets the callback function to be called after successful block batch processing.
// This is used by the masternode layer to trigger winner vote generation when new blocks are connected.
// The callback receives the new chain tip height after processing.
// Only called when not in IBD mode (gap < 5000 blocks) to avoid voting for old blocks during sync.
func (bs *BlockchainSyncer) SetBlockProcessedCallback(callback func(height uint32)) {
	bs.callbackMu.Lock()
	defer bs.callbackMu.Unlock()
	bs.onBlockProcessedCallback = callback
	bs.logger.Debug("Block processed callback configured")
}

// Start begins blockchain synchronization
func (bs *BlockchainSyncer) Start() error {
	if !bs.started.CompareAndSwap(false, true) {
		bs.logger.Debug("Blockchain synchronizer already started")
		return nil
	}

	bs.logger.Debug("Starting blockchain synchronizer")

	// Get current chain state
	if err := bs.updateChainState(); err != nil {
		return fmt.Errorf("failed to get chain state: %w", err)
	}

	// Start worker goroutines (legacy handlers for compatibility)
	bs.wg.Add(5) // Updated to 5 to include rate tracker
	go bs.blockProcessor()
	go bs.requestProcessor()
	go bs.syncProcessor()
	go bs.invProcessor()
	go bs.updateRateMetrics() // Start rate tracking

	// DON'T start sync maintenance here - it will be started after bootstrap completes
	// This prevents sync from starting before peer consensus is established

	bs.logger.WithField("current_height", bs.bestHeight.Load()).
		Debug("Blockchain synchronizer started")

	return nil
}

// resetRateTracking resets all rate tracking state for a new sync session
// This prevents false high values when sync restarts after errors
func (bs *BlockchainSyncer) resetRateTracking() {
	bs.blocksDownloaded.Store(0)
	bs.lastRateBlocks = 0
	bs.lastRateUpdate = time.Now()
	bs.rateWindow = NewRateWindow()
}

// updateRateMetrics periodically updates the sliding window with current sync rates
func (bs *BlockchainSyncer) updateRateMetrics() {
	defer bs.wg.Done()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	bs.lastRateBlocks = bs.blocksDownloaded.Load()
	bs.lastRateUpdate = time.Now()

	for {
		select {
		case <-ticker.C:
			currentBlocks := bs.blocksDownloaded.Load()
			currentTime := time.Now()

			// Calculate blocks processed since last update
			blocksProcessed := currentBlocks - bs.lastRateBlocks
			duration := currentTime.Sub(bs.lastRateUpdate)

			// Add sample to sliding window only if blocks were actually processed
			// Don't add zero-block samples as they skew the rate calculation
			if duration > 0 && blocksProcessed > 0 {
				bs.rateWindow.AddSample(blocksProcessed, duration)
				// Only update lastRateUpdate when blocks were processed
				// This ensures duration reflects actual time to process blocks
				bs.lastRateBlocks = currentBlocks
				bs.lastRateUpdate = currentTime
			}

		case <-bs.ctx.Done():
			return
		case <-bs.quit:
			return
		}
	}
}

// Stop stops the blockchain synchronizer
func (bs *BlockchainSyncer) Stop() {
	if !bs.started.CompareAndSwap(true, false) {
		bs.logger.Debug("Blockchain synchronizer stop requested but it was not running")
		return
	}

	bs.logger.Debug("Stopping blockchain synchronizer")

	// Signal shutdown to batch sync goroutine and workers
	close(bs.shutdown)
	close(bs.quit)

	// Wait for batch sync goroutine to exit (with timeout)
	// FIXED: Use read lock to safely access done channel
	bs.doneMu.RLock()
	syncDone := bs.done
	bs.doneMu.RUnlock()

	// FIX: Only wait if done channel exists (sync was started)
	// If syncDone is nil, no sync goroutine was ever started, so nothing to wait for
	// Reduced timeouts for faster shutdown
	if syncDone != nil {
		select {
		case <-syncDone:
			bs.logger.Debug("Batch sync goroutine exited cleanly")
		case <-time.After(2 * time.Second):
			bs.logger.Warn("Batch sync goroutine did not exit within timeout")
		}
	} else {
		bs.logger.Debug("No active sync goroutine to wait for (sync never started)")
	}

	// Cancel context to signal all other goroutines
	bs.cancel()

	// Force-drain channels to unblock senders
	go func() {
		for range bs.newBlocks {
		}
	}()
	go func() {
		for range bs.blockRequests {
		}
	}()
	go func() {
		for range bs.syncRequests {
		}
	}()
	go func() {
		for range bs.invAnnouncements {
		}
	}()
	go func() {
		for range bs.pendingInv {
		}
	}()
	go func() {
		for range bs.incomingBlocks {
		}
	}()

	// Wait for workers to finish with hard deadline (reduced timeout)
	done := make(chan struct{})
	go func() {
		bs.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		bs.logger.Debug("Blockchain synchronizer stopped")
	case <-time.After(2 * time.Second):
		bs.logger.Warn("Blockchain synchronizer stop timeout - forcing shutdown")
		// Goroutines are leaked but process can exit
	}
}

// OnPeerDiscovered notifies the health tracker when a new peer is discovered
func (bs *BlockchainSyncer) OnPeerDiscovered(peer *Peer) {
	if peer == nil || peer.GetVersion() == nil {
		return
	}

	version := peer.GetVersion()
	addr := peer.GetAddress().String()
	// Use StartHeight as initial estimate - will be updated when we receive HEADERS/INV
	tipHeight := uint32(version.StartHeight)

	// Check if peer is a masternode
	isMasternode := (version.Services & ServiceFlagMasternode) != 0
	tier := TierNone

	// Detect masternode tier from service flags
	if version.Services&ServiceFlagMasternodePlat != 0 {
		tier = TierPlatinum
	} else if version.Services&ServiceFlagMasternodeGold != 0 {
		tier = TierGold
	} else if version.Services&ServiceFlagMasternodeSilver != 0 {
		tier = TierSilver
	} else if version.Services&ServiceFlagMasternodeBronze != 0 {
		tier = TierBronze
	}

	// Record peer direction (outbound if we initiated the connection)
	isOutbound := !peer.inbound
	bs.healthTracker.RecordPeerDiscovered(addr, tipHeight, isMasternode, tier, isOutbound)

	// Give peer a reference to the health tracker for EffectivePeerHeight()
	peer.SetHealthTracker(bs.healthTracker)

	bs.logger.WithFields(logrus.Fields{
		"peer":       addr,
		"height":     tipHeight,
		"masternode": isMasternode,
		"tier":       tier,
		"outbound":   isOutbound,
	}).Debug("Peer discovered and added to health tracker")

	// Incrementally add peer to sync list (no reshuffle)
	if stats := bs.healthTracker.GetPeerStats(addr); stats != nil {
		bs.peerList.AddPeer(addr, stats)
	}
}

// OnPeerDisconnected handles peer disconnection cleanup
func (bs *BlockchainSyncer) OnPeerDisconnected(peer *Peer) {
	if peer == nil {
		return
	}

	addr := peer.GetAddress().String()
	bs.healthTracker.RemovePeer(addr)

	// Incrementally remove peer from sync list (no reshuffle)
	bs.peerList.RemovePeer(addr)

	bs.logger.WithField("peer", addr).Debug("Peer removed from health tracker and sync list on disconnect")
}

// UpdatePeerHeight updates the tracked height for a peer
func (bs *BlockchainSyncer) UpdatePeerHeight(peer *Peer, height uint32) {
	if peer == nil {
		return
	}
	addr := peer.GetAddress().String()
	bs.healthTracker.UpdateTipHeight(addr, height)
}

// OnBlockProcessed is called when a block is received during sync.
// Routes the block to the batch processing channel for sequential processing.
// peerAddr identifies the peer that delivered the block (empty if unknown).
func (bs *BlockchainSyncer) OnBlockProcessed(block *types.Block, peerAddr string) {
	batchInProgress := bs.batchInProgress.Load()

	// During batch sync, send to synchronous processing channel
	// The processBatch() function will handle blockchain.ProcessBlock()
	if batchInProgress {
		// Bug #4 Fix: Remove the 5-second timeout that was causing blocks to be dropped
		// The channel has a 500-block buffer, so it can hold entire batches
		// Blocking here is intentional - backpressure prevents memory issues
		select {
		case bs.incomingBlocks <- block:
			bs.logger.WithField("hash", block.Hash().String()).
				Debug("Routed block to batch processing channel")
		case <-bs.ctx.Done():
			return
		case <-bs.quit:
			return
		}
		return
	}

	// Not in batch sync mode - enqueue for single-threaded processing.
	// Multiple peers may call this concurrently; newBlocks is drained by one goroutine.
	// Timeout prevents peer handler goroutines from blocking indefinitely if
	// the processor is stalled (e.g. lock contention). Dropped blocks will be
	// rediscovered via the next INV announcement.
	select {
	case bs.newBlocks <- &peerBlock{block: block, peerAddr: peerAddr}:
		bs.logger.WithField("hash", block.Hash().String()).
			Debug("Queued block for single-block processor")
	case <-time.After(5 * time.Second):
		bs.logger.WithField("hash", block.Hash().String()).
			Warn("Block processor backlogged, dropping block (will re-fetch via INV)")
	case <-bs.ctx.Done():
		return
	case <-bs.quit:
		return
	}
}

// processSingleBlock processes one block on the syncer's single blockProcessor goroutine.
// peerAddr identifies the delivering peer for reactive validation failure tracking.
func (bs *BlockchainSyncer) processSingleBlock(block *types.Block, peerAddr string) {
	blockHash := block.Hash()

	// Process the block through the blockchain layer.
	if err := bs.blockchain.ProcessBlock(block); err != nil {
		// Check for common non-fatal errors
		if errors.Is(err, blockchainpkg.ErrBlockExists) {
			bs.logger.WithField("hash", blockHash.String()).
				Debug("Block already exists, skipping")
			return
		}
		if errors.Is(err, blockchainpkg.ErrHeightNotAdvancing) {
			bs.logger.WithField("hash", blockHash.String()).
				Debug("Block height does not advance chain, skipping")
			return
		}
		if errors.Is(err, blockchainpkg.ErrAllBlocksExist) {
			bs.logger.WithField("hash", blockHash.String()).
				Debug("All blocks in batch already exist, skipping")
			return
		}
		if errors.Is(err, blockchainpkg.ErrParentNotFound) {
			// Parent not found - store as orphan and request parent by hash.
			// No height estimation needed - orphan processing doesn't require height.
			// Fork detection is handled by blockchain layer when conflicting blocks arrive.
			if bs.server != nil {
				bs.server.AddOrphanBlock(block)
				bs.server.RequestBlockFromPeers(block.Header.PrevBlockHash)
			}
			bs.logger.WithFields(logrus.Fields{
				"hash":   blockHash.String(),
				"parent": block.Header.PrevBlockHash.String(),
			}).Debug("Block parent not found, stored as orphan")
			return
		}
		// Log other errors
		bs.logger.WithError(err).WithField("hash", blockHash.String()).
			Warn("Failed to process single block")

		// Track validation failure for reactive index corruption detection.
		// Uses the actual peer address so the threshold (3 unique peers) only
		// triggers when genuinely different peers fail on the same block.
		estimatedHeight := bs.bestHeight.Load() + 1
		if bs.recordValidationFailure(blockHash, estimatedHeight, peerAddr, err.Error()) {
			bs.logger.WithField("hash", blockHash.String()).
				Warn("Same block failed repeatedly - triggering reactive index recovery")
			if recoveryErr := bs.tryReactiveIndexRecovery(estimatedHeight); recoveryErr != nil {
				bs.logger.WithError(recoveryErr).Error("Reactive index recovery failed for single block")
			}
		}
		return
	}

	// Block accepted - update height tracking
	height, err := bs.blockchain.GetBestHeight()
	if err == nil {
		bs.bestHeight.Store(height)
	}
	// Process any orphans waiting for this block
	if bs.server != nil {
		bs.processOrphansForBlock(blockHash)
	}

	// Relay the block to other peers
	if bs.server != nil {
		bs.server.RelayBlock(block)
		bs.logger.WithFields(logrus.Fields{
			"hash":   blockHash.String(),
			"height": height,
		}).Debug("Block accepted and relayed")
	} else {
		bs.logger.WithFields(logrus.Fields{
			"hash":   blockHash.String(),
			"height": height,
		}).Debug("Block accepted")
	}
}

// processOrphansForBlock processes any orphan blocks waiting for this block as their parent
func (bs *BlockchainSyncer) processOrphansForBlock(parentHash types.Hash) {
	orphans := bs.server.GetOrphansForParent(parentHash)
	if len(orphans) == 0 {
		return
	}

	for _, orphan := range orphans {
		orphanHash := orphan.Hash()

		// Remove from orphan pool first
		bs.server.RemoveOrphan(orphanHash)

		// Try to process the orphan
		if err := bs.blockchain.ProcessBlock(orphan); err != nil {
			if errors.Is(err, blockchainpkg.ErrBlockExists) {
				continue
			}
			if errors.Is(err, blockchainpkg.ErrParentNotFound) {
				// Still an orphan (shouldn't happen but handle it)
				bs.server.AddOrphanBlock(orphan)
				continue
			}
			bs.logger.WithError(err).WithField("hash", orphanHash.String()).
				Warn("Failed to process orphan block")
			continue
		}

		// Orphan accepted - update height and relay
		height, err := bs.blockchain.GetBestHeight()
		if err == nil {
			bs.bestHeight.Store(height)
		}

		// Relay the newly accepted block
		bs.server.RelayBlock(orphan)
		bs.logger.WithFields(logrus.Fields{
			"hash":   orphanHash.String(),
			"height": height,
		}).Debug("Orphan block accepted")

		// Recursively process any orphans waiting for this one
		bs.processOrphansForBlock(orphanHash)
	}
}

// RecordPeerSuccess records successful block delivery from a peer
func (bs *BlockchainSyncer) RecordPeerSuccess(peer *Peer, blocksReceived uint64, bytesReceived uint64, duration time.Duration) {
	if peer == nil {
		return
	}
	addr := peer.GetAddress().String()
	bs.healthTracker.RecordSuccess(addr, blocksReceived, bytesReceived, duration)
}

// RecordPeerError records an error from a peer
func (bs *BlockchainSyncer) RecordPeerError(peer *Peer, errorType ErrorType) {
	if peer == nil {
		return
	}
	addr := peer.GetAddress().String()
	bs.healthTracker.RecordError(addr, errorType)
}

// isNetworkOrTimeoutError returns true if the error is a network/timeout issue
// rather than an actual block validation failure. These errors should NOT be
// counted toward the reactive index recovery threshold because they are normal
// at the chain tip where blocks arrive every ~60s.
func isNetworkOrTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "timeout waiting for INV") ||
		strings.Contains(msg, "timeout waiting") && strings.Contains(msg, "for blocks") ||
		strings.Contains(msg, "no valid peers found") ||
		strings.Contains(msg, "batch sequencing gap") ||
		strings.Contains(msg, "send getdata") ||
		strings.Contains(msg, "syncer shutting down")
}

// recordValidationFailure tracks a block validation failure from a specific peer.
// Returns true if the failure threshold is reached and recovery should be attempted.
func (bs *BlockchainSyncer) recordValidationFailure(blockHash types.Hash, estimatedHeight uint32, peerAddr string, errMsg string) bool {
	bs.validationFailuresMu.Lock()
	defer bs.validationFailuresMu.Unlock()

	now := time.Now()

	// Clean up stale entries
	for hash, record := range bs.validationFailures {
		if now.Sub(record.lastSeen) > ValidationFailureMaxAge {
			delete(bs.validationFailures, hash)
		}
	}

	record, exists := bs.validationFailures[blockHash]
	if !exists {
		bs.validationFailures[blockHash] = &validationFailureRecord{
			blockHash: blockHash,
			height:    estimatedHeight,
			errorMsg:  errMsg,
			peers:     map[string]bool{peerAddr: true},
			firstSeen: now,
			lastSeen:  now,
		}
		return false
	}

	// Update existing record
	record.peers[peerAddr] = true
	record.lastSeen = now
	if estimatedHeight > 0 && record.height == 0 {
		record.height = estimatedHeight
	}

	// Check if threshold reached and recovery not already attempted
	if len(record.peers) >= ValidationFailurePeerThreshold && !record.recovered {
		record.recovered = true
		return true
	}

	return false
}

// clearValidationFailures removes all tracked validation failures.
// Called after successful recovery or when sync progresses past the failing height.
func (bs *BlockchainSyncer) clearValidationFailures() {
	bs.validationFailuresMu.Lock()
	bs.validationFailures = make(map[types.Hash]*validationFailureRecord)
	bs.validationFailuresMu.Unlock()
}

// tryReactiveIndexRecovery attempts index consistency recovery when repeated
// validation failures suggest local index corruption.
func (bs *BlockchainSyncer) tryReactiveIndexRecovery(failHeight uint32) error {
	// Singleflight guard: prevent concurrent/repeated recovery calls from
	// different callsites (single block, batch sync, processBatch) that may
	// use the same stale failHeight after a previous recovery already ran.
	// Returns nil (not error) so callers don't log misleading "recovery failed"
	// messages when recovery is actively running from another callsite.
	if !bs.recoveryInProgress.CompareAndSwap(false, true) {
		bs.logger.Debug("Recovery already in progress from another callsite, skipping")
		return nil
	}
	defer bs.recoveryInProgress.Store(false)

	bc, ok := bs.blockchain.(*blockchainpkg.BlockChain)
	if !ok {
		return fmt.Errorf("blockchain type assertion failed")
	}

	bs.logger.WithField("fail_height", failHeight).Warn(
		"Repeated validation failure from multiple peers detected - triggering reactive index recovery")

	// Create cancellable context from syncer shutdown channel
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-bs.shutdown:
			cancel()
		case <-ctx.Done():
		}
	}()

	err := bc.TriggerRecoveryForRepeatedValidationFailure(ctx, failHeight)
	cancel()

	if err != nil {
		bs.logger.WithError(err).Error("Reactive index recovery failed")
		return err
	}

	// Recovery succeeded - clear all tracked failures
	bs.clearValidationFailures()

	// Update local height to reflect potential rollback
	if newHeight, heightErr := bs.blockchain.GetBestHeight(); heightErr == nil {
		bs.bestHeight.Store(newHeight)
		bs.logger.WithField("new_height", newHeight).Info("Height updated after reactive index recovery")
	}

	return nil
}

// RebuildPeerList rebuilds the sync peer rotation list
func (bs *BlockchainSyncer) RebuildPeerList() {
	// Always sync health tracker with server's actual connected peers.
	// This ensures newly connected peers (especially outbound) are registered
	// and stale entries from disconnected peers are pruned.
	if bs.server != nil {
		serverPeers := bs.server.GetPeersList()

		// Build connected address set for pruning
		connectedAddrs := make(map[string]struct{}, len(serverPeers))
		for _, peer := range serverPeers {
			if peer.IsHandshakeComplete() {
				connectedAddrs[peer.GetAddress().String()] = struct{}{}
			}
		}

		// Prune stale entries for peers that are no longer connected
		if pruned := bs.healthTracker.PruneDisconnectedPeers(connectedAddrs); pruned > 0 {
			bs.logger.WithField("pruned", pruned).
				Debug("Pruned stale entries from health tracker")
		}

		// Merge connected peers into health tracker (registers any missing peers)
		for _, peer := range serverPeers {
			if peer.IsHandshakeComplete() {
				addr := peer.GetAddress().String()
				version := peer.GetVersion()

				isMasternode := false
				tier := TierNone
				tipHeight := uint32(0)

				if version != nil {
					tipHeight = uint32(version.StartHeight)
					isMasternode = (version.Services & ServiceFlagMasternode) != 0

					if version.Services&ServiceFlagMasternodePlat != 0 {
						tier = TierPlatinum
					} else if version.Services&ServiceFlagMasternodeGold != 0 {
						tier = TierGold
					} else if version.Services&ServiceFlagMasternodeSilver != 0 {
						tier = TierSilver
					} else if version.Services&ServiceFlagMasternodeBronze != 0 {
						tier = TierBronze
					}
				}

				isOutbound := !peer.inbound
				bs.healthTracker.RecordPeerDiscovered(addr, tipHeight, isMasternode, tier, isOutbound)
				peer.SetHealthTracker(bs.healthTracker)
			}
		}
	}

	allPeers := bs.healthTracker.GetAllPeers()
	bs.peerList.Rebuild(allPeers)

	bs.logger.WithField("peer_count", len(bs.peerList.GetAllPeers())).
		Debug("Rebuilt sync peer list")

	if bs.stateMachine != nil {
		if err := bs.stateMachine.OnListRebuild(); err != nil {
			bs.logger.WithError(err).Debug("State machine failed to update on peer list rebuild")
		}
	}

	bs.updateConsensusState("peerlist-rebuild")
}

// GetNextSyncPeer gets the next peer in rotation for sync
func (bs *BlockchainSyncer) GetNextSyncPeer() (*Peer, error) {
	// Try up to 5 times to find a valid peer (in case some were just pruned)
	maxAttempts := 5
	for attempt := 0; attempt < maxAttempts; attempt++ {
		addr, err := bs.peerList.Next()
		if err != nil {
			return nil, err
		}

		// Find the actual peer object from the server
		peers := bs.server.GetPeersList()
		for _, peer := range peers {
			if peer.GetAddress().String() == addr && peer.IsConnected() && peer.IsHandshakeComplete() {
				return peer, nil
			}
		}

		// Peer was in rotation list but not found in server (likely just pruned)
		// Log and try next peer in rotation
		bs.logger.WithFields(logrus.Fields{
			"peer":    addr,
			"attempt": attempt + 1,
		}).Debug("Peer in rotation list not found in server (may have been pruned), trying next")
	}

	// After 5 attempts, just fail - caller will handle retry
	// NOTE: Do NOT call RebuildPeerList() here - it causes infinite recursion:
	// RebuildPeerList -> updateConsensusState -> ensureSyncAlignment -> GetNextSyncPeer -> RebuildPeerList
	bs.logger.Warn("Failed to find valid peer after multiple attempts")
	return nil, fmt.Errorf("no valid peers found after %d attempts", maxAttempts)
}

// blockProcessor processes incoming blocks
func (bs *BlockchainSyncer) blockProcessor() {
	defer bs.wg.Done()

	for {
		select {
		case pb := <-bs.newBlocks:
			bs.processSingleBlock(pb.block, pb.peerAddr)

		case <-bs.quit:
			return
		}
	}
}

// requestProcessor handles block download requests
func (bs *BlockchainSyncer) requestProcessor() {
	defer bs.wg.Done()

	for {
		select {
		case hash := <-bs.blockRequests:
			bs.requestBlock(hash)

		case <-bs.quit:
			return
		}
	}
}

// syncProcessor handles sync requests from peers
func (bs *BlockchainSyncer) syncProcessor() {
	defer bs.wg.Done()

	for {
		select {
		case peer := <-bs.syncRequests:
			bs.handleSyncRequest(peer)

		case <-bs.quit:
			return
		}
	}
}

// invProcessor handles inventory announcements
func (bs *BlockchainSyncer) invProcessor() {
	defer bs.wg.Done()

	for {
		select {
		case inv := <-bs.invAnnouncements:
			bs.handleInventoryAnnouncement(inv)

		case <-bs.quit:
			return
		}
	}
}

// syncMaintenance performs periodic sync maintenance
func (bs *BlockchainSyncer) syncMaintenance() {
	defer bs.wg.Done()

	ticker := time.NewTicker(10 * time.Second) // Check more frequently
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			bs.maintainSync()

			// Prune unhealthy peers - disconnects outbound peers with health score below threshold
			bs.pruneUnhealthyPeers()

			// Prune stale peers - disconnects outbound peers significantly behind consensus
			// Runs even during IBD: stale peers are useless for sync and waste connection slots
			bs.pruneStalePeers()

			// Prune peers with stale headers - disconnects outbound peers that haven't
			// updated synced_headers within StaleHeaderTimeout (skipped during IBD)
			bs.pruneStaleHeaderPeers()

			// Prune peers with stale ping height - disconnects 70928 peers whose
			// height hasn't advanced within StalePingHeightTimeout while network advances
			// (skipped during IBD, persistent peers get warnings only)
			bs.pruneStaleHeightPeers()

			// Proactive fork detection: query chainstate from multiple 70928 peers
			// and use quorum voting to detect if we're on a minority fork.
			// Skipped during IBD and active sync (rate-limited internally).
			bs.checkForForks()

			// Also try to continue sync if we're not syncing but significantly behind
			// Only trigger if 3+ blocks behind - batch getblocks is more efficient than individual INVs
			const syncTriggerThreshold = uint32(3)
			if !bs.syncing.Load() {
				currentHeight := bs.bestHeight.Load()
				// Check if any peer has higher height (using real-time height from ping/inv)
				if peer := bs.findSyncPeer(); peer != nil {
					peerHeight := peer.EffectivePeerHeight()
					if peerHeight >= currentHeight+syncTriggerThreshold {
						bs.logger.WithFields(logrus.Fields{
							"current":       currentHeight,
							"peer_height":   peerHeight,
							"blocks_behind": peerHeight - currentHeight,
							"threshold":     syncTriggerThreshold,
						}).Debug("Restarting sync - behind threshold")
						bs.startSync(peer)
					}
				}
			}

		case <-bs.quit:
			return
		}
	}
}

// pruneUnhealthyPeers disconnects outbound peers whose health score has dropped
// below HealthThreshold. Frees connection slots for healthier replacement peers.
// Never disconnects the peer we're currently syncing with.
func (bs *BlockchainSyncer) pruneUnhealthyPeers() {
	if bs.healthTracker == nil || bs.server == nil {
		return
	}

	// Get current sync peer to avoid disconnecting it
	var currentSyncPeer string
	if sp := bs.syncPeer.Load(); sp != nil {
		currentSyncPeer = sp.GetAddress().String()
	}

	unhealthyPeers := bs.healthTracker.GetUnhealthyPeers()
	for _, addr := range unhealthyPeers {
		// Never disconnect the peer we're currently syncing with
		if currentSyncPeer != "" && addr == currentSyncPeer {
			continue
		}

		score := bs.healthTracker.GetHealthScore(addr)
		bs.logger.WithFields(logrus.Fields{
			"peer":             addr,
			"health_score":     score,
			"health_threshold": HealthThreshold,
		}).Info("Disconnecting unhealthy peer")

		if err := bs.server.DisconnectPeer(addr); err != nil {
			bs.logger.WithError(err).WithField("peer", addr).
				Debug("Failed to disconnect unhealthy peer")
		}
	}
}

// pruneStalePeers disconnects outbound peers significantly behind network consensus.
// This runs even during IBD - peers significantly behind consensus are useless for sync
// and waste connection slots that could be used for peers with current chain data.
func (bs *BlockchainSyncer) pruneStalePeers() {
	// Need consensus validator and server to prune
	if bs.consensusValidator == nil || bs.server == nil {
		return
	}

	// Calculate network consensus height
	result, err := bs.consensusValidator.CalculateConsensusHeight()
	if err != nil {
		// Can't determine consensus, skip pruning
		return
	}

	// Only prune if we have reliable consensus (enough peers agreeing)
	if result.Confidence < 0.5 || result.PeerCount < 3 {
		return
	}

	// Prune peers more than OutlierReportThreshold (1000) blocks behind consensus
	pruned := bs.server.PruneOutdatedOutboundPeers(result.Height, OutlierReportThreshold)
	if pruned > 0 {
		bs.logger.WithFields(logrus.Fields{
			"pruned":           pruned,
			"consensus_height": result.Height,
			"threshold":        OutlierReportThreshold,
			"confidence":       result.Confidence,
		}).Info("Pruned stale outbound peers behind consensus")
	}
}

// pruneStaleHeaderPeers disconnects outbound peers whose synced_headers
// (BestKnownHeight) hasn't been updated within StaleHeaderTimeout.
// Skipped during IBD since headers aren't updated during initial block download.
func (bs *BlockchainSyncer) pruneStaleHeaderPeers() {
	if bs.healthTracker == nil || bs.server == nil {
		return
	}

	// Skip during IBD - headers aren't updated during initial block download
	if bs.initialSync.Load() {
		return
	}

	stalePeers := bs.healthTracker.GetStaleHeaderPeers(StaleHeaderTimeout)
	for _, addr := range stalePeers {
		bs.logger.WithFields(logrus.Fields{
			"peer":    addr,
			"timeout": StaleHeaderTimeout,
		}).Info("Disconnecting peer with stale headers")

		if err := bs.server.DisconnectPeer(addr); err != nil {
			bs.logger.WithError(err).WithField("peer", addr).
				Debug("Failed to disconnect stale header peer")
		}
	}
}

// pruneStaleHeightPeers disconnects protocol 70928 peers whose ping-reported height
// hasn't advanced within StalePingHeightTimeout while the network is advancing.
// Persistent peers (masternodes) get warnings but are not disconnected.
// Skipped during IBD since peers may have a valid shorter chain.
func (bs *BlockchainSyncer) pruneStaleHeightPeers() {
	if bs.healthTracker == nil || bs.server == nil || bs.consensusValidator == nil {
		return
	}

	// Skip during IBD - peers may legitimately have shorter chains
	if bs.initialSync.Load() {
		return
	}

	// Check that network is actually advancing (consensus exists and height > 0)
	result, err := bs.consensusValidator.CalculateConsensusHeight()
	if err != nil || result.PeerCount < 3 {
		return
	}

	stalePeers := bs.healthTracker.GetStalePingHeightPeers(StalePingHeightTimeout)
	for _, addr := range stalePeers {
		stats := bs.healthTracker.GetPeerStats(addr)
		if stats == nil {
			continue
		}

		// Skip peers whose height matches or exceeds consensus — network may have
		// stalled (no new blocks), in which case no one is advancing.
		if stats.LastPingHeight >= result.Height {
			continue
		}

		// Masternode peers get warnings but are not disconnected
		if stats.IsMasternode {
			bs.logger.WithFields(logrus.Fields{
				"peer":             addr,
				"timeout":          StalePingHeightTimeout,
				"consensus_height": result.Height,
			}).Warn("Masternode peer has stale ping height (not disconnecting)")
			continue
		}

		bs.logger.WithFields(logrus.Fields{
			"peer":             addr,
			"timeout":          StalePingHeightTimeout,
			"consensus_height": result.Height,
		}).Info("Disconnecting peer with stale ping height")

		if err := bs.server.DisconnectPeer(addr); err != nil {
			bs.logger.WithError(err).WithField("peer", addr).
				Debug("Failed to disconnect stale ping height peer")
		}
	}
}

// updateChainState updates the current chain state
func (bs *BlockchainSyncer) updateChainState() error {
	// Use blockchain layer for authoritative height
	height, err := bs.blockchain.GetBestHeight()
	if err != nil {
		// Fallback to storage if blockchain layer fails
		height, err = bs.storage.GetChainHeight()
		if err != nil {
			return err
		}
	}

	bs.bestHeight.Store(height)
	bs.syncHeight.Store(height)

	// Get the tip block for timestamp
	if height > 0 {
		tipHash, err := bs.storage.GetChainTip()
		if err == nil {
			if tipBlock, err := bs.storage.GetBlock(tipHash); err == nil {
				bs.lastBlockTime.Store(int64(tipBlock.Header.Timestamp))
			}
		}
	}

	return nil
}

// getCurrentHeight returns the current blockchain height from the authoritative source
func (bs *BlockchainSyncer) getCurrentHeight() uint32 {
	// Always query blockchain layer for real-time height
	if height, err := bs.blockchain.GetBestHeight(); err == nil {
		return height
	}
	// Fallback to cached value if blockchain query fails
	return bs.bestHeight.Load()
}

// IsSynced returns true when the node is fully synced with the network.
// This matches the legacy masternodeSync.IsSynced() behavior where sync is complete
// when the node has caught up with peers and is no longer in Initial Block Download.
//
// Used by consensus layer for masternode payment validation:
// - Returns false during IBD → payment validation skipped (allow sync)
// - Returns true after sync → payment validation enforced (consensus rules)
//
// Legacy equivalent: masternodeSync.IsSynced() returns true when
// RequestedMasternodeAssets == MASTERNODE_SYNC_FINISHED (999)
func (bs *BlockchainSyncer) IsSynced() bool {
	// PRIORITY 0: Require minimum peer count for any sync determination.
	// With fewer than MinSyncPeers peers, consensus is unreliable.
	if bs.server != nil && bs.server.GetPeerCount() < int32(blockchainpkg.MinSyncPeers) {
		return false
	}

	// PRIORITY 1: Use state machine if available - it has the authoritative sync state
	// The state machine evaluates consensus and transitions through states properly.
	// This fixes the bug where peerTipHeight could be 0/stale while state is SYNCED.
	if bs.stateMachine != nil && bs.stateMachine.GetState() == StateSynced {
		return true
	}

	// PRIORITY 2: Fallback to flag-based checks when state machine not available or not synced
	// During initial block download, we're not synced
	if bs.isInIBD.Load() {
		return false
	}

	// If actively syncing a batch, we're not fully synced yet
	if bs.syncing.Load() {
		return false
	}

	// Check if we're close enough to network consensus height
	// We consider synced if within 3 blocks of best known peer height
	currentHeight := bs.getCurrentHeight()
	peerTipHeight := bs.peerTipHeight.Load()

	// If we don't know peer heights yet, assume not synced
	if peerTipHeight == 0 {
		return false
	}

	// Synced if within 3 blocks of best peer (matches regularSyncThreshold)
	// At 3+ blocks behind, batch sync is more efficient than individual INV relay
	const syncedThreshold = 3
	return currentHeight+syncedThreshold >= peerTipHeight
}

// processBlock is retained for compatibility with legacy call sites.
func (bs *BlockchainSyncer) processBlock(block *types.Block) {
	bs.processSingleBlock(block, "")
	bs.processPendingBlocks()

	// Log progress
	if bs.syncing.Load() && time.Since(bs.lastProgressLog) > bs.progressLogInterval {
		bs.logSyncProgress()
		bs.lastProgressLog = time.Now()
	}
}

// processPendingBlocks processes blocks that were waiting for dependencies
func (bs *BlockchainSyncer) processPendingBlocks() {
	bs.blockMu.Lock()
	defer bs.blockMu.Unlock()

	processed := true
	for processed {
		processed = false

		for hash, pending := range bs.pendingBlocks {
			// Check if we now have the previous block
			prevHash := pending.Block.Header.PrevBlockHash
			if prevHash.IsZero() || bs.hasBlock(prevHash) {
				// We can process this block now
				delete(bs.pendingBlocks, hash)
				processed = true

				// Route to syncer for processing via OnBlockProcessed.
				// Peer address from original delivery may be stale, but still
				// provides diversity signal for reactive failure detection.
				pendingPeerAddr := ""
				if pending.Peer != nil {
					pendingPeerAddr = pending.Peer.GetAddress().String()
				}
				bs.OnBlockProcessed(pending.Block, pendingPeerAddr)
			}
		}
	}
}

// hasBlock checks if we have a block (helper that doesn't require error handling)
func (bs *BlockchainSyncer) hasBlock(hash types.Hash) bool {
	has, err := bs.storage.HasBlock(hash)
	return err == nil && has
}

// requestBlock requests a block from a peer
func (bs *BlockchainSyncer) requestBlock(hash types.Hash) {
	bs.blockMu.Lock()
	defer bs.blockMu.Unlock()

	// Check if already requested
	if req, exists := bs.requestedBlock[hash]; exists {
		if time.Since(req.Requested) < BlockDownloadTimeout {
			return // Recently requested
		}
		// Timeout, retry with different peer if possible
	}

	// Find a suitable peer
	peer := bs.findSyncPeer()
	if peer == nil {
		bs.logger.Debug("No suitable peer found for block request")
		return
	}

	// Create getdata message
	inv := InventoryVector{
		Type: InvTypeBlock,
		Hash: hash,
	}

	getDataMsg := &GetDataMessage{
		InvList: []InventoryVector{inv},
	}

	// Serialize and send
	payload, err := bs.serializeGetDataMessage(getDataMsg)
	if err != nil {
		bs.logger.WithError(err).Error("Failed to serialize getdata message")
		return
	}

	msg := NewMessage(MsgGetData, payload, bs.server.params.NetMagicBytes)
	if err := peer.SendMessage(msg); err != nil {
		bs.logger.WithError(err).WithField("peer", peer.GetAddress().String()).
			Error("Failed to send getdata message")
		return
	}

	// Track the request
	bs.requestedBlock[hash] = &BlockRequest{
		Hash:      hash,
		Peer:      peer,
		Requested: time.Now(),
		Timeout:   time.Now().Add(BlockDownloadTimeout),
	}

	bs.logger.WithFields(logrus.Fields{
		"hash": hash.String(),
		"peer": peer.GetAddress().String(),
	}).Debug("Sent getdata request")
}

// RequestBlock is the public API for requesting a block (used by blockchain for orphan resolution)
func (bs *BlockchainSyncer) RequestBlock(hash types.Hash) {
	bs.requestBlock(hash)
}

// handleSyncRequest handles a sync request with a peer
func (bs *BlockchainSyncer) handleSyncRequest(peer *Peer) {
	bs.logger.WithField("peer", peer.GetAddress().String()).Debug("Received sync request")

	if !peer.IsHandshakeComplete() {
		bs.logger.WithField("peer", peer.GetAddress().String()).Warn("Sync request from peer with incomplete handshake")
		return
	}

	peerVersion := peer.GetVersion()
	if peerVersion == nil {
		bs.logger.WithField("peer", peer.GetAddress().String()).Warn("Sync request from peer with no version info")
		return
	}

	peerHeight := peer.EffectivePeerHeight()
	currentHeight := bs.bestHeight.Load()
	alreadySyncing := bs.syncing.Load()

	bs.logger.WithFields(logrus.Fields{
		"peer":            peer.GetAddress().String(),
		"peer_height":     peerHeight,
		"local_height":    currentHeight,
		"already_syncing": alreadySyncing,
	}).Debug("Handling sync request")

	// Update peer state
	bs.peerMu.Lock()
	bs.peerStates[peer.GetAddress().String()] = &PeerSyncState{
		BestHeight: peerHeight,
		LastUpdate: time.Now(),
	}
	bs.peerMu.Unlock()
	bs.healthTracker.UpdateTipHeight(peer.GetAddress().String(), peerHeight)

	// Attempt consensus-driven evaluation first
	bs.updateConsensusState("sync-request")

	if bs.syncing.Load() {
		bs.logger.WithField("peer", peer.GetAddress().String()).
			Debug("Consensus-driven sync already in progress")
		return
	}

	// Start sync if peer has more blocks (3+ behind)
	// Below 3, broadcast mechanism handles individual blocks
	const syncTriggerThreshold = uint32(3)
	blocksBehind := uint32(0)
	if peerHeight > currentHeight {
		blocksBehind = peerHeight - currentHeight
	}
	if blocksBehind >= syncTriggerThreshold {
		if !alreadySyncing {
			bs.logger.WithFields(logrus.Fields{
				"peer":          peer.GetAddress().String(),
				"blocks_behind": blocksBehind,
				"threshold":     syncTriggerThreshold,
			}).Debug("Starting sync with peer")
			bs.startSync(peer)
		} else {
			bs.logger.WithField("peer", peer.GetAddress().String()).Debug("Already syncing, skipping")
		}
	} else {
		bs.logger.WithFields(logrus.Fields{
			"peer":          peer.GetAddress().String(),
			"peer_height":   peerHeight,
			"local_height":  currentHeight,
			"blocks_behind": blocksBehind,
		}).Debug("Peer ahead but below threshold, relying on broadcast")
	}
}

// startSync starts synchronization with a peer
func (bs *BlockchainSyncer) startSync(peer *Peer) {
	if !bs.syncing.CompareAndSwap(false, true) {
		bs.logger.WithField("peer", peer.GetAddress().String()).
			Debug("Sync already in progress, skipping startSync")
		return // Already syncing
	}

	bs.logger.WithField("syncing", bs.syncing.Load()).
		Warn("Setting sync state to TRUE in startSync")

	// FIXED: Recreate done channel and closeOnce for new sync session
	// This prevents "close of closed channel" panic when multiple syncs occur
	bs.doneMu.Lock()
	bs.done = make(chan struct{})
	bs.closeOnce = sync.Once{}
	bs.doneMu.Unlock()

	bs.syncPeer.Store(peer)
	bs.syncStartTime = time.Now()
	bs.resetRateTracking() // Reset rate counters for new sync session
	bs.lastProgressLog = time.Now()
	bs.lastSyncProgress = time.Now() // Reset stall detection

	currentHeight := bs.bestHeight.Load()
	peerHeight := peer.EffectivePeerHeight()

	consensusTip := bs.consensusHeight.Load()
	if consensusTip > 0 && consensusTip > peerHeight {
		peerHeight = consensusTip
	}

	// Store the peer's tip height for progress tracking
	bs.peerTipHeight.Store(peerHeight)
	bs.healthTracker.UpdateTipHeight(peer.GetAddress().String(), peerHeight)

	// Initialize batch tracking
	bs.batchStartHeight.Store(currentHeight)
	bs.batchProcessedCount.Store(0)

	bs.logger.WithFields(logrus.Fields{
		"peer":         peer.GetAddress().String(),
		"peer_height":  peerHeight,
		"local_height": currentHeight,
		"blocks_behind": func() uint32 {
			if peerHeight > currentHeight {
				return peerHeight - currentHeight
			}
			return 0
		}(),
		"ibd_mode": bs.isInIBD.Load(),
	}).Debug("Starting blockchain synchronization")

	// Determine if we need initial block download
	if bs.isInIBD.Load() {
		bs.initialSync.Store(true)

		bs.logger.WithFields(logrus.Fields{
			"num_peers":     len(bs.server.GetPeersList()),
			"blocks_behind": peerHeight - currentHeight,
		}).Debug("Starting sequential sync")
	}

	// Start batch sync in a goroutine to avoid blocking
	// This will process blocks synchronously in batches of ~500 with peer rotation
	go func() {
		// Phase 1 Diagnostics: Add panic recovery and detailed logging
		defer func() {
			if r := recover(); r != nil {
				// Print full stack trace to identify nil pointer location
				buf := make([]byte, 4096)
				n := runtime.Stack(buf, false)
				bs.logger.WithFields(logrus.Fields{
					"panic": r,
					"stack": string(buf[:n]),
				}).Error("Batch sync goroutine PANICKED with stack trace")
				// CRITICAL: Mark syncing as false to allow restart
				bs.syncing.Store(false)

				// FIXED: Use sync.Once to prevent double-close panic
				bs.closeOnce.Do(func() {
					bs.doneMu.RLock()
					done := bs.done
					bs.doneMu.RUnlock()
					if done != nil {
						close(done)
					}
				})
				// Exit immediately - don't try to continue with corrupted state
				return
			}
			bs.syncing.Store(false)

			// FIXED: Use sync.Once to prevent double-close panic
			bs.closeOnce.Do(func() {
				bs.doneMu.RLock()
				done := bs.done
				bs.doneMu.RUnlock()
				if done != nil {
					close(done)
				}
			})
			bs.logger.Debug("Batch sync goroutine exiting")
		}()

		bs.logger.WithFields(logrus.Fields{
			"peer":         peer.GetAddress().String(),
			"peer_height":  bs.peerTipHeight.Load(),
			"local_height": bs.bestHeight.Load(),
		}).Debug("Batch sync goroutine launched")

		currentPeer := peer
		batchCount := 0

		for {
			// Check for shutdown signal first
			select {
			case <-bs.shutdown:
				bs.logger.Debug("Shutdown signal received")
				return
			default:
				// Continue with sync
			}

			// Check if we're done syncing
			currentHeight := bs.bestHeight.Load()
			peerHeight := bs.peerTipHeight.Load()

			// Phase 1 Diagnostics: Enhanced loop iteration logging
			bs.logger.WithFields(logrus.Fields{
				"iteration":      batchCount,
				"current_height": currentHeight,
				"peer_height":    peerHeight,
				"blocks_behind":  peerHeight - currentHeight,
			}).Debug("Batch sync loop iteration")

			if currentHeight >= peerHeight {
				bs.logger.WithFields(logrus.Fields{
					"current": currentHeight,
					"peer":    peerHeight,
				}).Debug("Sync complete - caught up")
				return
			}

			// Check if stop was requested before starting new batch
			if !bs.syncing.Load() {
				bs.logger.Debug("Stop requested")
				return
			}

			// Process one batch with current peer
			batchStartHeight := currentHeight
			if err := bs.processBatch(currentPeer); err != nil {
				// Check if this is a recovery restart signal
				if strings.Contains(err.Error(), "sync restart needed after recovery") {
					bs.logger.Debug("Recovery completed, continuing...")

					// Update our local height to reflect the rollback
					newHeight, heightErr := bs.blockchain.GetBestHeight()
					if heightErr == nil {
						bs.bestHeight.Store(newHeight)
						bs.logger.WithFields(logrus.Fields{
							"old_height":  currentHeight,
							"new_height":  newHeight,
							"rolled_back": currentHeight - newHeight,
						}).Debug("Height updated after recovery")
					}

					// Continue sync loop from the new height
					continue
				}

				bs.logger.WithFields(logrus.Fields{
					"peer":  currentPeer.GetAddress().String(),
					"error": err,
				}).Warn("Batch processing failed, rotating peer")

				// Reactive index corruption detection: track this failure.
				// If multiple peers fail at the same height with the same error,
				// this signals local index corruption rather than a peer problem.
				// IMPORTANT: Only count actual block validation failures, NOT
				// network timeouts or peer availability issues. Timeouts are normal
				// at the chain tip (blocks arrive every ~60s) and must not trigger
				// the nuclear recovery path.
				if !isNetworkOrTimeoutError(err) {
					failHeight := batchStartHeight + 1
					failHash := types.Hash{}
					binary.LittleEndian.PutUint32(failHash[:4], failHeight)
					rotatingPeerAddr := currentPeer.GetAddress().String()
					if bs.recordValidationFailure(failHash, failHeight, rotatingPeerAddr, err.Error()) {
						bs.logger.WithField("fail_height", failHeight).
							Warn("Same batch height failed from multiple peers - triggering reactive index recovery")
						if recoveryErr := bs.tryReactiveIndexRecovery(failHeight); recoveryErr == nil {
							// Recovery succeeded - update height and continue sync
							if newHeight, heightErr := bs.blockchain.GetBestHeight(); heightErr == nil {
								bs.bestHeight.Store(newHeight)
							}
							continue
						}
					}
				}

				// Try to get next peer on error
				nextPeer, err := bs.GetNextSyncPeer()
				if err != nil || nextPeer == nil {
					bs.logger.WithError(err).Warn("No alternative peer available, stopping sync")
					return
				}
				currentPeer = nextPeer
				bs.syncPeer.Store(currentPeer)
				bs.batchesFromCurrentPeer.Store(0)    // Reset batch counter for new peer
				bs.expectingHashContinue.Store(false) // Clear hashContinue expectation
				bs.lastHashContinueTime.Store(0)      // Reset timeout tracking
				bs.logger.WithField("new_peer", currentPeer.GetAddress().String()).
					Info("Rotated to new peer after error")
				continue
			}

			batchCount++
			batchEndHeight := bs.bestHeight.Load()
			blocksInBatch := batchEndHeight - batchStartHeight

			// Clear validation failure tracking on successful progress
			bs.clearValidationFailures()

			// Increment batches from current peer
			bs.batchesFromCurrentPeer.Add(1)
			currentPeerBatches := bs.batchesFromCurrentPeer.Load()

			bs.logger.WithFields(logrus.Fields{
				"batch":            batchCount,
				"blocks":           blocksInBatch,
				"start_height":     batchStartHeight,
				"end_height":       batchEndHeight,
				"peer":             currentPeer.GetAddress().String(),
				"peer_batch_count": currentPeerBatches,
			}).Debug("Batch complete")

			// If we received less than 500 blocks, check if we're actually caught up
			lastInvSize := bs.lastInvSize.Load()
			if lastInvSize > 1 && lastInvSize < 500 {
				// Only consider sync complete if we're actually close to tip
				blocksBehind := peerHeight - currentHeight
				if blocksBehind < 1000 {
					bs.logger.Debug("Received partial batch, sync complete")
					bs.consecutivePartialBatches.Store(0) // Reset counter
					return
				}
				// Still far behind - peer just sent partial batch
				// Track consecutive partial batches before forcing rotation
				partialCount := bs.consecutivePartialBatches.Add(1)
				bs.logger.WithFields(logrus.Fields{
					"inv_size":             lastInvSize,
					"blocks_behind":        blocksBehind,
					"consecutive_partials": partialCount,
				}).Debug("Partial batch but still far behind")
				// Only force peer rotation after 3 consecutive partial batches
				// This prevents premature rotation during temporary rate limiting
				if partialCount >= 3 {
					bs.logger.Debug("Multiple partial batches, rotating peer")
					// Force rotation by setting slow batch duration
					bs.lastBatchDurationMs.Store(SlowBatchThreshold.Milliseconds() + 1000)
					bs.consecutivePartialBatches.Store(0) // Reset for next peer
				}
			} else {
				// Full batch received, reset partial counter
				bs.consecutivePartialBatches.Store(0)
			}

			// Check if we should rebuild the peer list (every 3 rounds)
			if bs.peerList.ShouldRebuild() {
				bs.logger.Debug("Rebuilding peer rotation list")
				bs.RebuildPeerList()
			}

			// Smart rotation logic for sticky peer sync
			// Stay with fast peers, rotate only when peer becomes slow or exhausted
			shouldRotate := false
			rotationReason := ""
			lastBatchMs := bs.lastBatchDurationMs.Load()

			if lastInvSize < 500 && lastInvSize > 0 {
				// Peer has no more blocks
				shouldRotate = true
				rotationReason = "peer exhausted (< 500 blocks)"
			} else if lastBatchMs > SlowBatchThreshold.Milliseconds() {
				// Peer is too slow - rotate to find faster peer
				shouldRotate = true
				rotationReason = fmt.Sprintf("slow batch (%.1fs > %.1fs threshold)", float64(lastBatchMs)/1000, SlowBatchThreshold.Seconds())
			} else if lastInvSize == 500 {
				// Full batch received, check for hashContinue timeout
				if bs.expectingHashContinue.Load() {
					// We're already expecting hashContinue, check timeout
					lastHashContinueTime := bs.lastHashContinueTime.Load()
					if lastHashContinueTime > 0 && time.Since(time.Unix(lastHashContinueTime, 0)) > HashContinueTimeout {
						shouldRotate = true
						rotationReason = "hashContinue timeout (no continuation inv)"
						bs.expectingHashContinue.Store(false) // Clear the flag
						bs.logger.Warn("hashContinue timeout - rotating to next peer")
					}
				} else {
					// First time expecting hashContinue
					bs.expectingHashContinue.Store(true)
					bs.lastHashContinueTime.Store(time.Now().Unix())

					bs.logger.WithFields(logrus.Fields{
						"peer":          currentPeer.GetAddress().String(),
						"batch_count":   currentPeerBatches,
						"last_batch_ms": lastBatchMs,
					}).Debug("Staying with peer")
				}
			}

			if shouldRotate {
				// Reset batch counter for new peer
				bs.batchesFromCurrentPeer.Store(0)

				// Rotate to next peer
				nextPeer, err := bs.GetNextSyncPeer()
				if err == nil && nextPeer != nil {
					oldPeerAddr := currentPeer.GetAddress().String()
					newPeerAddr := nextPeer.GetAddress().String()

					// Only rotate if it's actually a different peer
					if oldPeerAddr != newPeerAddr {
						currentPeer = nextPeer
						bs.syncPeer.Store(currentPeer)
						bs.logger.WithFields(logrus.Fields{
							"from":   oldPeerAddr,
							"to":     newPeerAddr,
							"reason": rotationReason,
						}).Debug("Rotating to next peer")
					} else {
						bs.logger.WithField("peer", oldPeerAddr).
							Debug("Next peer is same as current, no rotation needed")
					}
				} else {
					// Rotation failed - GetNextSyncPeer already rebuilt the list if needed
					// Just log and continue with current peer
					bs.logger.WithError(err).Debug("Peer rotation failed, continuing with current peer")
				}
			}

			// Small delay between batches
			time.Sleep(100 * time.Millisecond)
		}
	}()
}

// getBlockLocator returns a block locator for sync protocol
// Returns hashes starting from best height, then exponentially back
// In fork search mode, uses virtual tip instead of real tip
func (bs *BlockchainSyncer) getBlockLocator() []types.Hash {
	locator := make([]types.Hash, 0, 32)

	currentHeight := uint32(bs.bestHeight.Load())

	// Fork search mode: use virtual tip instead of real tip
	// This allows requesting blocks as if we're at a lower height
	if bs.forkSearchMode.Load() {
		virtualTip := bs.forkSearchVirtualTip.Load()
		bs.logger.WithFields(logrus.Fields{
			"actual_height":  currentHeight,
			"virtual_height": virtualTip,
			"search_depth":   bs.forkSearchDepth.Load(),
		}).Debug("Using virtual tip for fork search locator")
		currentHeight = virtualTip
	}

	if currentHeight == 0 {
		// Genesis block only - use the hardcoded genesis hash from chain params
		// This ensures we send the correct legacy hash even if our locally constructed genesis differs
		if bs.chainParams != nil {
			locator = append(locator, bs.chainParams.GenesisHash)
		} else if bestHash, err := bs.storage.GetChainTip(); err == nil {
			locator = append(locator, bestHash)
		}
		return locator
	}

	// Add block at current height (virtual or real tip)
	if bs.forkSearchMode.Load() {
		// In fork search mode, get block by height instead of chain tip
		if block, err := bs.storage.GetBlockByHeight(currentHeight); err == nil && block != nil && block.Header != nil {
			locator = append(locator, block.Header.Hash())
		}
	} else if bestHash, err := bs.storage.GetChainTip(); err == nil {
		locator = append(locator, bestHash)
	}

	// Add blocks exponentially further back
	step := uint32(1)
	for height := int64(currentHeight) - int64(step); height > 0; height -= int64(step) {
		if block, err := bs.storage.GetBlockByHeight(uint32(height)); err == nil {
			// Safety check: ensure header is not nil
			if block != nil && block.Header != nil {
				locator = append(locator, block.Header.Hash())
			}
		}

		// Increase step exponentially
		if len(locator) > 10 {
			step *= 2
		}

		// Limit locator size
		if len(locator) >= 32 {
			break
		}
	}

	// Always add genesis block
	if genesisBlock, err := bs.storage.GetBlockByHeight(0); err == nil {
		// Safety check: ensure header is not nil
		if genesisBlock != nil && genesisBlock.Header != nil {
			locator = append(locator, genesisBlock.Header.Hash())
		} else {
			bs.logger.Warn("Genesis block retrieved but has nil header - storage deserialization issue")
		}
	} else {
		bs.logger.WithError(err).Warn("Failed to retrieve genesis block for locator")
	}

	return locator
}

// GetBlockLocator returns a block locator for external callers (e.g., chainstate responses).
// Exported wrapper around the private getBlockLocator method.
func (bs *BlockchainSyncer) GetBlockLocator() []types.Hash {
	return bs.getBlockLocator()
}

// GetBestHeight returns the syncer's current best known chain height.
func (bs *BlockchainSyncer) GetBestHeight() uint32 {
	return uint32(bs.bestHeight.Load())
}

// maxForkSearchDepth is the maximum number of blocks to search when looking for a fork
// This prevents infinite searching on corrupted chains
const maxForkSearchDepth = 100000

// enterForkSearchMode activates fork search mode when all blocks in a batch already exist
// This indicates we may be on a different fork than the peer
// We start searching forward from lastBatchHeight to find where chains diverge
func (bs *BlockchainSyncer) enterForkSearchMode(lastBatchHeight uint32) {
	if bs.forkSearchMode.Load() {
		return // Already in fork search mode
	}

	bs.forkSearchMode.Store(true)
	bs.forkSearchStartHeight.Store(lastBatchHeight) // Start point of search (not our real height)
	bs.forkSearchVirtualTip.Store(lastBatchHeight)
	bs.forkSearchDepth.Store(0)

	bs.logger.WithFields(logrus.Fields{
		"search_start": lastBatchHeight,
		"our_height":   bs.bestHeight.Load(),
	}).Warn("Entering fork search mode - all batch blocks already exist, searching forward for fork point")
}

// exitForkSearchMode deactivates fork search mode and resets state
func (bs *BlockchainSyncer) exitForkSearchMode() {
	if !bs.forkSearchMode.Load() {
		return // Not in fork search mode
	}

	bs.logger.WithFields(logrus.Fields{
		"search_depth": bs.forkSearchDepth.Load(),
		"start_height": bs.forkSearchStartHeight.Load(),
	}).Info("Exiting fork search mode")

	bs.forkSearchMode.Store(false)
	bs.forkSearchStartHeight.Store(0)
	bs.forkSearchVirtualTip.Store(0)
	bs.forkSearchDepth.Store(0)
}

// advanceForkSearch moves the virtual tip forward after another all-exist batch
// Returns error if max depth exceeded
func (bs *BlockchainSyncer) advanceForkSearch(lastBatchHeight uint32) error {
	startHeight := bs.forkSearchStartHeight.Load()

	// Calculate how far we've searched forward from the start point
	var newDepth uint32
	if lastBatchHeight > startHeight {
		newDepth = lastBatchHeight - startHeight
	} else {
		// Shouldn't happen normally, but handle gracefully
		newDepth = 0
	}

	if newDepth > maxForkSearchDepth {
		bs.exitForkSearchMode()
		return fmt.Errorf("fork search exceeded max depth %d blocks - chain may be corrupted, consider deleting blockchain data", maxForkSearchDepth)
	}

	bs.forkSearchDepth.Store(newDepth)
	bs.forkSearchVirtualTip.Store(lastBatchHeight)

	bs.logger.WithFields(logrus.Fields{
		"virtual_tip":  lastBatchHeight,
		"search_depth": newDepth,
		"max_depth":    maxForkSearchDepth,
	}).Debug("Advanced fork search virtual tip")

	return nil
}

// detectFork checks if any block in the batch represents a fork point
// Returns true and the divergent block if a fork is detected
// Fork is detected when: block is new, parent exists, but we have different block at that height
func (bs *BlockchainSyncer) detectFork(blocks []*types.Block) (bool, *types.Block) {
	for _, block := range blocks {
		blockHash := block.Hash()

		// Check if this block already exists
		existingBlock, err := bs.blockchain.GetBlock(blockHash)
		if err == nil && existingBlock != nil {
			continue // Block exists, not a fork point
		}

		// New block found - check if parent exists
		parentHash := block.Header.PrevBlockHash
		parentHeight, err := bs.blockchain.GetBlockHeight(parentHash)
		if err != nil {
			continue // Parent doesn't exist, not a fork point yet
		}

		// Check if we have a different block at parentHeight+1
		forkHeight := parentHeight + 1
		ourHash, err := bs.storage.GetBlockHashByHeight(forkHeight)
		if err != nil {
			// No block at this height on our chain - this is chain extension, not fork
			continue
		}

		if ourHash != blockHash {
			// FORK DETECTED: We have different block at same height
			bs.logger.WithFields(logrus.Fields{
				"fork_height":   forkHeight,
				"our_block":     ourHash.String(),
				"peer_block":    blockHash.String(),
				"parent_hash":   parentHash.String(),
				"parent_height": parentHeight,
			}).Warn("Fork point detected!")
			return true, block
		}
	}

	return false, nil
}

// handleInventoryAnnouncement handles inventory announcements
func (bs *BlockchainSyncer) handleInventoryAnnouncement(inv *InvAnnouncement) {
	// Filter inventory by height during sync
	filtered, deferred := bs.filterInventoryByHeight(inv.Inv, inv.Peer)

	if len(deferred) > 0 {
		bs.logger.WithFields(logrus.Fields{
			"deferred": len(deferred),
			"filtered": len(filtered),
			"peer":     inv.Peer.GetAddress().String(),
		}).Debug("Deferred inventory items too far ahead")
	}

	// Process only the filtered inventory
	if len(filtered) == 0 {
		return
	}

	// Track inventory size to detect when we hit the 500 block limit
	blockCount := int32(0)
	txCount := int32(0)
	for _, item := range filtered {
		switch item.Type {
		case InvTypeBlock:
			blockCount++
			// Note: The server's handleInvMessage already requests blocks via getdata
			// We don't need to request them here to avoid duplicate requests
			// Just track the count for sync continuation logic

		case InvTypeTx:
			txCount++
			// Note: The server's handleInvMessage already requests txs via getdata
			// We don't need to request them here to avoid duplicate requests
		}
	}

	if blockCount > 0 || txCount > 0 {
		bs.logger.WithFields(logrus.Fields{
			"blocks":  blockCount,
			"txs":     txCount,
			"peer":    inv.Peer.GetAddress().String(),
			"syncing": bs.syncing.Load(),
		}).Debug("Received inventory announcement")
	}

	// Store the block count from this inventory
	// IMPORTANT: Only track inventory from our current sync peer!
	if blockCount > 0 {
		syncPeer := bs.syncPeer.Load()
		isSyncPeer := syncPeer != nil && inv.Peer.GetAddress().String() == syncPeer.GetAddress().String()

		// NOTE: Peer height is now updated via announcement tracking mechanism:
		// RecordBlockAnnouncement (in handlers.go) + NotifyBlocksProcessed (after block saved)
		// This provides accurate height based on actual block heights, not estimates.

		// Update peerTipHeight for sync progress tracking (internal use only)
		if isSyncPeer {
			currentHeight := bs.getCurrentHeight()
			estimatedHeight := currentHeight + uint32(blockCount)
			if estimatedHeight > bs.peerTipHeight.Load() {
				bs.peerTipHeight.Store(estimatedHeight)
			}
		}

		if isSyncPeer && bs.syncing.Load() {
			// Check for hashContinue pattern: single block inv after we expected it
			if blockCount == 1 && bs.expectingHashContinue.Load() {
				// This is hashContinue! Peer is signaling it has more blocks
				bs.expectingHashContinue.Store(false) // Clear the flag
				bs.logger.WithFields(logrus.Fields{
					"peer":        inv.Peer.GetAddress().String(),
					"hash":        inv.Inv[0].Hash.String(),
					"batch_count": bs.batchesFromCurrentPeer.Load(),
				}).Debug("Detected hashContinue inv")
			}

			bs.lastInvSize.Store(blockCount)
			bs.logger.WithFields(logrus.Fields{
				"blocks_in_inv": blockCount,
				"peer":          inv.Peer.GetAddress().String(),
				"syncing":       true,
			}).Debug("Processed block inventory")
		} else {
			bs.logger.WithFields(logrus.Fields{
				"blocks_in_inv": blockCount,
				"peer":          inv.Peer.GetAddress().String(),
				"sync_peer": func() string {
					if syncPeer != nil {
						return syncPeer.GetAddress().String()
					}
					return "none"
				}(),
				"is_sync_peer": isSyncPeer,
			}).Debug("Received inventory from non-sync peer, ignoring for batch tracking")
		}

		// If we received exactly 500 blocks, there are likely more available
		// This is the protocol limit for inventory messages
		if isSyncPeer && blockCount == 500 && bs.syncing.Load() {
			bs.logger.Debug("Received full inventory (500 blocks)")

			// NOTE: We do NOT request the next batch here!
			// Requesting immediately causes problems:
			// 1. Blocks haven't been processed yet (async processing)
			// 2. Our chain tip hasn't advanced
			// 3. Next getblocks would use same locator = duplicate requests
			//
			// Instead, continueSyncIfNeeded() is called after EACH block processes
			// (see line 638). Once we've processed ~500 blocks, it will:
			// - Detect lastInvSize == 500
			// - Rotate to next peer
			// - Request next batch with updated locator

			// Just update peer height tracking (using real-time height from ping/inv)
			if syncPeer := bs.syncPeer.Load(); syncPeer != nil {
				peerHeight := syncPeer.EffectivePeerHeight()
				currentPeerTip := bs.peerTipHeight.Load()
				if peerHeight > currentPeerTip {
					bs.peerTipHeight.Store(peerHeight)
					bs.logger.WithFields(logrus.Fields{
						"old_peer_tip": currentPeerTip,
						"new_peer_tip": peerHeight,
					}).Debug("Updated peer tip height")
				}
			}
		}
	}
}

// requestMissingBlocks requests blocks that we're missing
func (bs *BlockchainSyncer) requestMissingBlocks() {
	// Use authoritative height from blockchain layer
	currentHeight := bs.getCurrentHeight()
	syncPeer := bs.syncPeer.Load()
	if syncPeer == nil {
		bs.logger.Warn("No sync peer available for requesting missing blocks")
		return
	}

	peerVersion := syncPeer.GetVersion()
	if peerVersion == nil {
		bs.logger.Warn("Sync peer has no version info")
		return
	}

	// Update peer tip height using real-time height from ping/inv
	effectiveHeight := syncPeer.EffectivePeerHeight()
	currentPeerTip := bs.peerTipHeight.Load()
	if effectiveHeight > currentPeerTip {
		bs.peerTipHeight.Store(effectiveHeight)
		bs.logger.WithFields(logrus.Fields{
			"old_peer_tip": currentPeerTip,
			"new_peer_tip": effectiveHeight,
		}).Debug("Peer tip height increased")
	}

	peerHeight := bs.peerTipHeight.Load()

	// Calculate how many blocks we're behind (using actual blockchain height)
	blocksBehind := uint32(0)
	if peerHeight > currentHeight {
		blocksBehind = peerHeight - currentHeight
	}

	// Don't stop syncing if we hit the 500 block limit - there might be more
	lastInvSize := bs.lastInvSize.Load()

	// CRITICAL: Always request more if we received a full inventory (500 blocks)
	// This indicates the peer has more blocks to send
	if lastInvSize == 500 {
		bs.logger.WithFields(logrus.Fields{
			"current_height": currentHeight,
			"peer_height":    peerHeight,
			"blocks_behind":  blocksBehind,
			"last_inv_size":  lastInvSize,
		}).Debug("Hit 500-block limit, requesting more")
	} else if blocksBehind == 0 {
		// Only stop if we're truly caught up AND didn't receive a full inventory
		bs.logger.WithFields(logrus.Fields{
			"current_height": currentHeight,
			"peer_height":    peerHeight,
			"last_inv_size":  lastInvSize,
		}).Debug("Appears synced (blocks_behind=0, last_inv!=500)")
		return
	}

	// Request more blocks if needed
	if lastInvSize == 500 || blocksBehind > 0 {
		bs.logger.WithFields(logrus.Fields{
			"current_height": currentHeight,
			"peer_height":    peerHeight,
			"blocks_behind":  blocksBehind,
			"last_inv_size":  lastInvSize,
		}).Debug("Requesting missing blocks")
	} else {
		return
	}

	// Since we don't have block hashes for heights we haven't synced yet,
	// we need to request headers first to get the hashes, then request blocks.
	// For now, as a fallback, we'll use getblocks message which asks peer to send inv messages
	// with block hashes, which we can then request.

	// Create getblocks message with block locator
	locator := bs.getBlockLocator()

	getBlocksMsg := &GetBlocksMessage{
		Version:      uint32(ProtocolVersion),
		BlockLocator: locator,
		HashStop:     types.Hash{}, // Empty = get all
	}

	payload, err := bs.serializeGetBlocksMessage(getBlocksMsg)
	if err != nil {
		bs.logger.WithError(err).Error("Failed to serialize getblocks message")
		return
	}

	msg := NewMessage(MsgGetBlocks, payload, bs.server.params.NetMagicBytes)
	if err := syncPeer.SendMessage(msg); err != nil {
		bs.logger.WithError(err).WithField("peer", syncPeer.GetAddress().String()).
			Error("Failed to send getblocks message")
		return
	}

	// Record request time for throttling
	bs.lastRequestTime.Store(time.Now().Unix())

	bs.logger.WithFields(logrus.Fields{
		"peer":           syncPeer.GetAddress().String(),
		"locator_count":  len(locator),
		"current_height": currentHeight,
		"peer_height":    peerHeight,
		"blocks_behind":  blocksBehind,
		"expecting_blocks": func() string {
			if blocksBehind > 500 {
				return "up to 500"
			}
			return fmt.Sprintf("~%d", blocksBehind)
		}(),
	}).Debug("Sent getblocks request")
}

// continueSyncIfNeeded is deprecated in the new sync system
// Kept for compatibility but no longer used
func (bs *BlockchainSyncer) continueSyncIfNeeded() {
	// This method is no longer called in the new synchronous batch sync system
	// Sync continuation is now handled by syncLoop() which processes batches sequentially
}

// logSyncProgress logs the current sync progress with useful metrics
func (bs *BlockchainSyncer) logSyncProgress() {
	// Use authoritative blockchain height for accurate progress reporting
	currentHeight := bs.getCurrentHeight()
	peerHeight := bs.peerTipHeight.Load()
	blocksDownloaded := bs.blocksDownloaded.Load()

	// Calculate sync metrics
	blocksBehind := uint32(0)
	if peerHeight > currentHeight {
		blocksBehind = peerHeight - currentHeight
	}

	// Use spike-resistant blocks per second calculation
	blocksPerSecond := bs.GetBlocksPerSec()

	// Estimate time to completion
	var eta time.Duration
	if blocksPerSecond > 0 {
		secondsRemaining := float64(blocksBehind) / blocksPerSecond
		eta = time.Duration(secondsRemaining) * time.Second
	}

	percentComplete := float64(currentHeight) * 100.0 / float64(peerHeight)
	if percentComplete > 100 {
		percentComplete = 100
	}

	bs.logger.WithFields(logrus.Fields{
		"height":        currentHeight,
		"peer_tip":      peerHeight,
		"blocks_behind": blocksBehind,
		"progress":      fmt.Sprintf("%.1f%%", percentComplete),
		"blocks/sec":    fmt.Sprintf("%.1f", blocksPerSecond),
		"eta":           eta.Round(time.Second),
		"downloaded":    blocksDownloaded,
	}).Debug("Sync progress")
}

// stopSync stops synchronization and waits for batch goroutine to exit
func (bs *BlockchainSyncer) stopSync() {
	if bs.syncing.CompareAndSwap(true, false) {
		bs.logger.WithField("syncing", bs.syncing.Load()).
			Warn("Setting sync state to FALSE in stopSync")

		// Wait for batch goroutine to exit before returning
		// This prevents race condition where new sync starts while old goroutine still runs
		bs.doneMu.RLock()
		done := bs.done
		bs.doneMu.RUnlock()

		if done != nil {
			select {
			case <-done:
				bs.logger.Debug("✓ Batch goroutine exited cleanly")
			case <-time.After(60 * time.Second):
				bs.logger.Warn("Timeout waiting for batch goroutine to exit")
			case <-bs.shutdown:
				bs.logger.Debug("Shutdown during stopSync wait")
			}
		}

		bs.syncPeer.Store(nil)
		bs.initialSync.Store(false)
		bs.isInIBD.Store(false)

		syncDuration := time.Since(bs.syncStartTime)
		blocksDownloaded := bs.blocksDownloaded.Load()

		bs.logger.WithFields(logrus.Fields{
			"duration":          syncDuration,
			"blocks_downloaded": blocksDownloaded,
			"current_height":    bs.bestHeight.Load(),
		}).Debug("Blockchain synchronization completed")
	} else {
		bs.logger.Debug("stopSync called but syncing was already false")
	}
}

// handleStateChange reacts to state machine transitions
func (bs *BlockchainSyncer) handleStateChange(oldState, newState SyncState) {
	bs.logger.WithFields(logrus.Fields{
		"old": oldState.String(),
		"new": newState.String(),
	}).Debug("Sync state changed")

	switch newState {
	case StateIBD:
		bs.isInIBD.Store(true)
		bs.initialSync.Store(true)
	case StateRegularSync:
		bs.isInIBD.Store(false)
	case StateSynced:
		bs.isInIBD.Store(false)
		bs.initialSync.Store(false)
	case StateBootstrap, StateSyncDecision:
		// No direct flag changes
	}

	bs.ensureSyncAlignment(fmt.Sprintf("state-change:%s->%s", oldState.String(), newState.String()))
}

// updateConsensusState recalculates consensus height and drives state transitions
func (bs *BlockchainSyncer) updateConsensusState(reason string) {
	if bs.consensusValidator == nil || bs.stateMachine == nil {
		return
	}

	// Always update peer count to blockchain layer for sync detection
	if bs.server != nil {
		bs.blockchain.SetNetworkPeerCount(bs.server.GetPeerCount())
	}

	result, err := bs.consensusValidator.CalculateConsensusHeight()
	if err != nil {
		// Consensus unavailable (e.g. < MinSyncPeers peers) - reset to 0
		// so blockchain layer knows consensus is unknown
		bs.consensusHeight.Store(0)
		bs.consensusConfidence.Store(float64(0))
		bs.blockchain.SetNetworkConsensusHeight(0)

		// If node was considered synced but consensus is now unavailable,
		// transition out of StateSynced - can't be synced without consensus
		if bs.stateMachine != nil && bs.stateMachine.GetState() == StateSynced {
			bs.logger.WithError(err).WithField("reason", reason).
				Warn("Consensus unavailable while in SYNCED state - transitioning to REGULAR_SYNC")
			bs.stateMachine.Transition(StateRegularSync)
		} else {
			bs.logger.WithError(err).WithField("reason", reason).
				Debug("Consensus evaluation unavailable, reset to 0")
		}
		return
	}

	bs.consensusHeight.Store(result.Height)
	bs.consensusConfidence.Store(result.Confidence)

	// Update blockchain layer with network consensus height for dynamic validation
	bs.blockchain.SetNetworkConsensusHeight(result.Height)

	// Log consensus result with strategy used
	bs.logger.WithFields(logrus.Fields{
		"consensus_height": result.Height,
		"confidence":       fmt.Sprintf("%.1f%%", result.Confidence*100),
		"peer_count":       result.PeerCount,
		"strategy":         result.Strategy.String(),
		"outliers":         len(result.Outliers),
		"reason":           reason,
	}).Debug("Consensus calculated")

	currentHeight := bs.getCurrentHeight()

	// During batch sync, use max(consensus_height, peer_tip_height) for IBD evaluation
	// This ensures timely IBD->Regular transition when sync peer height is known but
	// network consensus hasn't updated yet due to slow peer INV propagation
	targetHeight := result.Height
	if bs.batchInProgress.Load() {
		peerHeight := bs.peerTipHeight.Load()
		if peerHeight > targetHeight {
			targetHeight = peerHeight
			bs.logger.WithFields(logrus.Fields{
				"consensus_height": result.Height,
				"peer_height":      peerHeight,
				"using":            "peer_height",
			}).Debug("Using peer height for IBD evaluation during batch sync")
		}
	}

	targetState, err := bs.stateMachine.EvaluateSyncNeeded(currentHeight, targetHeight)
	if err != nil {
		bs.logger.WithError(err).WithFields(logrus.Fields{
			"reason":           reason,
			"current_height":   currentHeight,
			"consensus_height": result.Height,
			"target_height":    targetHeight,
		}).Debug("Failed to evaluate sync mode")
		return
	}

	// Don't transition out of StateBootstrap here - let OnBootstrapComplete() handle that
	// Bootstrap is a special initialization phase that must complete before sync starts
	currentState := bs.stateMachine.GetState()
	if currentState == StateBootstrap {
		bs.logger.WithFields(logrus.Fields{
			"reason":           reason,
			"target_state":     targetState.String(),
			"consensus_height": result.Height,
		}).Debug("Still in bootstrap phase, deferring state transition to OnBootstrapComplete()")
		return
	}

	// For all other states, transition to target state if different
	if targetState != currentState {
		if err := bs.stateMachine.Transition(targetState); err != nil {
			bs.logger.WithError(err).WithFields(logrus.Fields{
				"reason":       reason,
				"current":      currentState.String(),
				"target_state": targetState.String(),
			}).Warn("Failed to transition sync state")
			return
		}
		bs.logger.WithFields(logrus.Fields{
			"reason":    reason,
			"old_state": currentState.String(),
			"new_state": targetState.String(),
		}).Debug("Sync state transitioned")
	}

	bs.logger.WithFields(logrus.Fields{
		"reason":           reason,
		"consensus_height": result.Height,
		"confidence":       fmt.Sprintf("%.1f%%", result.Confidence*100),
		"current_height":   currentHeight,
		"state":            bs.stateMachine.GetState().String(),
	}).Debug("Consensus state evaluated")

	bs.ensureSyncAlignment(reason)
}

// ensureSyncAlignment makes sure the running sync matches the current state machine directive
func (bs *BlockchainSyncer) ensureSyncAlignment(reason string) {
	if bs.stateMachine == nil {
		return
	}

	state := bs.stateMachine.GetState()
	switch state {
	case StateSynced:
		if bs.syncing.Load() {
			bs.logger.WithField("reason", reason).
				Debug("Stopping sync because state is SYNCED")
			bs.stopSync()
		}

	case StateIBD, StateRegularSync:
		if bs.syncing.Load() {
			return
		}

		peer, err := bs.GetNextSyncPeer()
		if err != nil || peer == nil {
			// Attempt fallback selection
			if peer == nil {
				peer = bs.findSyncPeer()
			}
			if peer == nil {
				entry := bs.logger.WithField("reason", reason)
				if err != nil {
					entry = entry.WithError(err)
				}
				entry.Warn("No suitable peer available to satisfy sync state")
				return
			}
		}

		bs.logger.WithFields(logrus.Fields{
			"peer":          peer.GetAddress().String(),
			"state":         state.String(),
			"reason":        reason,
			"consensus_tip": bs.consensusHeight.Load(),
		}).Debug("Starting sync")
		bs.startSync(peer)

	case StateBootstrap, StateSyncDecision:
		// No action yet; waiting for consensus/decision
	}
}

// OnBootstrapComplete signals that peer bootstrap finished and sync decisions can begin
func (bs *BlockchainSyncer) OnBootstrapComplete() {
	bs.logger.Debug("Bootstrap complete, evaluating sync state")

	if bs.stateMachine != nil && bs.stateMachine.GetState() == StateBootstrap {
		if err := bs.stateMachine.Transition(StateSyncDecision); err != nil {
			bs.logger.WithError(err).Warn("Failed to enter SYNC_DECISION after bootstrap")
		}
	}

	// Start sync maintenance AFTER bootstrap completes
	// This ensures peer consensus is established before any sync attempts
	bs.wg.Add(1)
	go bs.syncMaintenance()
	bs.logger.Debug("Sync maintenance started")

	bs.updateConsensusState("bootstrap-complete")
}

// maintainSync performs sync maintenance tasks
func (bs *BlockchainSyncer) maintainSync() {
	// Clean up timed-out block requests
	bs.cleanupTimeoutRequests()

	// Refresh consensus and state alignment
	bs.updateConsensusState("maintenance-loop")

	// Use authoritative blockchain height for accurate status
	currentHeight := bs.getCurrentHeight()

	// Determine network target height (consensus preferred, fall back to peer scan)
	consensusHeight := bs.consensusHeight.Load()
	confidence := 0.0
	if consensusHeight != 0 {
		if val := bs.consensusConfidence.Load(); val != nil {
			if conf, ok := val.(float64); ok {
				confidence = conf
			}
		}
	}

	bestPeerHeight := consensusHeight
	if bestPeerHeight == 0 {
		syncPeer := bs.syncPeer.Load()
		if syncPeer != nil {
			bestPeerHeight = syncPeer.EffectivePeerHeight()
			if bestPeerHeight > bs.peerTipHeight.Load() {
				bs.peerTipHeight.Store(bestPeerHeight)
			}
		}

		if bestPeerHeight == 0 {
			// Iterate actual Peer objects for EffectivePeerHeight()
			bs.server.peers.Range(func(key, value interface{}) bool {
				p := value.(*Peer)
				if p.IsConnected() && p.IsHandshakeComplete() {
					h := p.EffectivePeerHeight()
					if h > bestPeerHeight {
						bestPeerHeight = h
					}
				}
				return true
			})
		}

		// If we still have no consensus or peer height but are behind, fallback to legacy start
		// Only trigger sync if we're 3+ blocks behind - batch sync is more efficient
		const syncTriggerThreshold = 3
		blocksBehind := uint32(0)
		if bestPeerHeight > currentHeight {
			blocksBehind = bestPeerHeight - currentHeight
		}
		if blocksBehind >= syncTriggerThreshold && !bs.syncing.Load() {
			bs.logger.WithFields(logrus.Fields{
				"current_height": currentHeight,
				"peer_height":    bestPeerHeight,
				"blocks_behind":  blocksBehind,
				"threshold":      syncTriggerThreshold,
			}).Debug("Legacy sync fallback triggered")

			if peer := bs.findSyncPeer(); peer != nil {
				bs.startSync(peer)
			}
		}
	}

	if bs.syncing.Load() {
		// Use spike-resistant blocks per second calculation
		blocksPerSec := bs.GetBlocksPerSec()

		// Calculate blocks behind
		blocksBehind := uint32(0)
		if bestPeerHeight > currentHeight {
			blocksBehind = bestPeerHeight - currentHeight
		}

		// Calculate ETA
		var eta string
		if blocksBehind > 0 && blocksPerSec > 0 {
			etaSec := float64(blocksBehind) / blocksPerSec
			eta = fmt.Sprintf("ETA: %s", time.Duration(etaSec*float64(time.Second)).Round(time.Second))
		}

		// Calculate percentage if we know peer height
		var percentage float64
		if bestPeerHeight > 0 {
			percentage = float64(currentHeight) / float64(bestPeerHeight) * 100
		}

		logFields := logrus.Fields{
			"current_height": currentHeight,
			"peer_height":    bestPeerHeight,
			"blocks_behind":  blocksBehind,
			"blocks_per_sec": fmt.Sprintf("%.2f", blocksPerSec),
			"percentage":     fmt.Sprintf("%.1f%%", percentage),
			"eta":            eta,
		}
		if consensusHeight != 0 {
			logFields["consensus_confidence"] = fmt.Sprintf("%.1f%%", confidence*100)
		}

		bs.logger.WithFields(logFields).Debug("Sync status")

		// Check for sync stall (no progress)
		// Only timeout if NOTHING is happening (no blocks received, no height progress)
		timeSinceProgress := time.Since(bs.lastSyncProgress)
		if timeSinceProgress > bs.syncStallTimeout {
			bs.logger.WithFields(logrus.Fields{
				"stall_duration": timeSinceProgress,
				"blocks_per_sec": blocksPerSec,
			}).Error("Sync stalled - no progress detected, restarting")
			bs.stopSync()

			// Flush UTXO cache to eliminate stale entries that may cause
			// repeated validation failures across sync restarts.
			// Without this, the daemon can enter a livelock where the same
			// block fails validation indefinitely due to cached stale UTXOs.
			if bc, ok := bs.blockchain.(*blockchainpkg.BlockChain); ok {
				bc.FlushUTXOCache()
			}
			bs.clearValidationFailures()

			// Try to find a new sync peer
			if peer := bs.findSyncPeer(); peer != nil {
				bs.startSync(peer)
			}
		}

		// No hard time limit - sync runs as long as it's making progress
		// The stall detection above will catch truly stuck syncs
	}
}

// cleanupTimeoutRequests cleans up timed-out block requests
func (bs *BlockchainSyncer) cleanupTimeoutRequests() {
	bs.blockMu.Lock()
	defer bs.blockMu.Unlock()

	now := time.Now()
	for hash, req := range bs.requestedBlock {
		if now.After(req.Timeout) {
			delete(bs.requestedBlock, hash)

			// Retry with different peer if possible
			go func(h types.Hash) {
				select {
				case bs.blockRequests <- h:
				case <-bs.quit:
				}
			}(hash)
		}
	}
}

// peerSwitchHysteresis is the minimum block advantage a new peer must have over the
// current sync peer before we switch. Prevents thrashing between peers at similar heights.
const peerSwitchHysteresis = uint32(10)

// findSyncPeer finds a suitable peer for synchronization using quality scoring
// and real-time height data. Applies hysteresis to avoid thrashing between peers.
func (bs *BlockchainSyncer) findSyncPeer() *Peer {
	var (
		availablePeers []*Peer
		bestPeer       *Peer
		bestScore      float64 = -1
		bestHeight     uint32
	)

	bs.server.peers.Range(func(key, value interface{}) bool {
		peer := value.(*Peer)
		if !peer.IsConnected() || !peer.IsHandshakeComplete() {
			return true
		}

		availablePeers = append(availablePeers, peer)

		addr := peer.GetAddress().String()
		if !bs.healthTracker.IsHealthy(addr) {
			return true
		}

		score := bs.healthTracker.GetHealthScore(addr)
		peerH := peer.EffectivePeerHeight()

		// Prefer taller healthy peers: relative height bonus capped at 20 points.
		// Health score is 0-100, so a max +20 bonus biases toward taller peers
		// without overwhelming health. Only blocks ahead of our tip count.
		currentH := bs.bestHeight.Load()
		heightBonus := float64(0)
		if peerH > currentH {
			heightBonus = float64(peerH-currentH) * 0.1
			if heightBonus > 20 {
				heightBonus = 20
			}
		}
		adjustedScore := score + heightBonus

		if adjustedScore > bestScore {
			bestScore = adjustedScore
			bestPeer = peer
			bestHeight = peerH
		}
		return true
	})

	// Apply hysteresis: if current sync peer is healthy and close to best, keep it
	if bestPeer != nil {
		if currentSyncPeer := bs.syncPeer.Load(); currentSyncPeer != nil &&
			currentSyncPeer.IsConnected() &&
			bs.healthTracker.IsHealthy(currentSyncPeer.GetAddress().String()) {

			currentHeight := currentSyncPeer.EffectivePeerHeight()
			if bestHeight <= currentHeight+peerSwitchHysteresis {
				// Best candidate is not significantly ahead, keep current peer
				return currentSyncPeer
			}
			bs.logger.WithFields(logrus.Fields{
				"old_peer":       currentSyncPeer.GetAddress().String(),
				"old_height":     currentHeight,
				"new_peer":       bestPeer.GetAddress().String(),
				"new_height":     bestHeight,
				"height_advantage": bestHeight - currentHeight,
			}).Debug("Switching sync peer (height advantage exceeds hysteresis)")
		}

		bs.logger.WithFields(logrus.Fields{
			"peer":   bestPeer.GetAddress().String(),
			"score":  fmt.Sprintf("%.1f", bestScore),
			"height": bestHeight,
		}).Debug("Selected peer for sync based on health score and height")
		return bestPeer
	}

	if len(availablePeers) > 0 {
		return availablePeers[0]
	}

	return nil
}

// Public API methods

// AddBlock adds a block received from a peer.
// Uses the same 5s timeout as OnBlockProcessed to prevent goroutine pile-up.
func (bs *BlockchainSyncer) AddBlock(block *types.Block) {
	select {
	case bs.newBlocks <- &peerBlock{block: block}:
	case <-time.After(5 * time.Second):
		bs.logger.WithField("hash", block.Hash().String()).
			Warn("Block processor backlogged in AddBlock, dropping block")
	case <-bs.quit:
	}
}

// processBatch performs synchronous batch processing with a peer
// This function blocks until the batch is complete or fails
func (bs *BlockchainSyncer) processBatch(peer *Peer) error {
	if peer == nil {
		return fmt.Errorf("nil peer")
	}

	peerAddr := peer.GetAddress().String()

	// Phase 1 Diagnostics: Entry point logging
	bs.logger.WithFields(logrus.Fields{
		"peer":         peerAddr,
		"local_height": bs.bestHeight.Load(),
		"peer_height":  bs.peerTipHeight.Load(),
	}).Debug("Starting batch sync with peer")

	// Mark batch in progress
	bs.batchInProgress.Store(true)
	defer func() {
		bs.batchInProgress.Store(false)
	}()
	batchStart := time.Now()

	// CRITICAL FIX: Drain stale data from channels before starting new batch
	// This prevents mixing blocks/INVs from previous failed batches with new ones
	drainedBlocks := 0
	drainedInvs := 0
DrainLoop:
	for {
		select {
		case <-bs.incomingBlocks:
			drainedBlocks++
		case <-bs.pendingInv:
			drainedInvs++
		default:
			break DrainLoop
		}
	}
	if drainedBlocks > 0 || drainedInvs > 0 {
		bs.logger.WithFields(logrus.Fields{
			"drained_blocks": drainedBlocks,
			"drained_invs":   drainedInvs,
		}).Debug("Drained stale data from channels before new batch")
	}

	// Build locator from current chain
	locator := bs.getBlockLocator()

	// Send getblocks request using peer's helper method
	if err := peer.SendGetBlocks(locator, types.Hash{}); err != nil {
		bs.RecordPeerError(peer, ErrorTypeSendFailed)
		bs.updateConsensusState("batch-getblocks-error")
		return fmt.Errorf("send getblocks: %w", err)
	}

	bs.lastRequestTime.Store(time.Now().Unix())
	bs.logger.WithField("peer", peerAddr).Debug("Sent getblocks request")

	// Wait for INV message (with timeout)
	// Phase 1 Diagnostics: Track inventory reception
	bs.logger.Debug("⏳ Waiting for INV message from peer...")
	var invVectors []InventoryVector
	select {
	case invVectors = <-bs.pendingInv:
		bs.logger.WithFields(logrus.Fields{
			"count": len(invVectors),
			"peer":  peerAddr,
		}).Debug("Received INV for batch")
	case <-time.After(30 * time.Second):
		bs.RecordPeerError(peer, ErrorTypeTimeout)
		bs.updateConsensusState("batch-inv-timeout")
		return fmt.Errorf("timeout waiting for INV")
	case <-bs.quit:
		return fmt.Errorf("syncer shutting down")
	}

	if len(invVectors) == 0 {
		bs.logger.Debug("Empty INV received, peer is synced")
		return nil // Peer has no more blocks
	}

	// Track batch size
	bs.lastInvSize.Store(int32(len(invVectors)))

	// Log INV info for debugging
	if len(invVectors) > 0 {
		bs.logger.WithFields(logrus.Fields{
			"first_inv": invVectors[0].Hash.String(),
			"last_inv":  invVectors[len(invVectors)-1].Hash.String(),
			"count":     len(invVectors),
		}).Debug("INV received from peer")
	}

	// Request all blocks via getdata - create message manually
	buf := make([]byte, 0)
	// Add count varint
	countBytes := encodeVarInt(uint64(len(invVectors)))
	buf = append(buf, countBytes...)
	// Add each inventory vector (4 bytes type + 32 bytes hash)
	for _, inv := range invVectors {
		typeBytes := make([]byte, 4)
		binary.LittleEndian.PutUint32(typeBytes, uint32(inv.Type))
		buf = append(buf, typeBytes...)
		buf = append(buf, inv.Hash[:]...)
	}

	msg := NewMessage(MsgGetData, buf, peer.magic)
	if err := peer.SendMessage(msg); err != nil {
		bs.RecordPeerError(peer, ErrorTypeSendFailed)
		bs.updateConsensusState("batch-getdata-error")
		return fmt.Errorf("send getdata: %w", err)
	}

	bs.logger.WithFields(logrus.Fields{
		"count": len(invVectors),
		"peer":  peerAddr,
	}).Debug("Sent getdata request for batch")

	// Collect all blocks for batch processing
	blocksToProcess := len(invVectors)
	blocks := make([]*types.Block, 0, blocksToProcess)
	startHeight := bs.bestHeight.Load()

	blockWaitTimeout := 30 * time.Second
	collectStart := time.Now()

	// First, collect all blocks (they may arrive out of order)
	for len(blocks) < blocksToProcess {
		select {
		case block := <-bs.incomingBlocks:
			blocks = append(blocks, block)

			bs.logger.WithFields(logrus.Fields{
				"collected": len(blocks),
				"total":     blocksToProcess,
				"hash":      block.Hash().String(),
			}).Debug("Collected block for batch")

		case <-time.After(blockWaitTimeout):
			bs.RecordPeerError(peer, ErrorTypeTimeout)
			bs.updateConsensusState("batch-block-timeout")
			return fmt.Errorf("timeout waiting %.0fs for blocks (%d/%d received)", blockWaitTimeout.Seconds(), len(blocks), blocksToProcess)

		case <-bs.quit:
			return fmt.Errorf("syncer shutting down")
		}
	}

	collectDuration := time.Since(collectStart)
	bs.logger.WithFields(logrus.Fields{
		"blocks":     len(blocks),
		"collect_ms": collectDuration.Milliseconds(),
		"peer":       peerAddr,
	}).Debug("Collected blocks for batch")

	// CRITICAL: Sort blocks by parent-child relationships
	// Blocks arrive asynchronously and may be out of order
	// We must reconstruct the correct chain order based on actual parent hashes
	if len(blocks) > 1 {
		// Build hash -> block map for O(1) lookups
		blockMap := make(map[types.Hash]*types.Block)
		for _, block := range blocks {
			blockMap[block.Hash()] = block
		}

		// Build parent -> child map for O(1) child lookups (prevents O(n²) DoS)
		childMap := make(map[types.Hash]*types.Block)
		for _, block := range blocks {
			childMap[block.Header.PrevBlockHash] = block
		}

		// Find the first block (one whose parent is NOT in the batch)
		// This block's parent should already be in our blockchain
		// Fixed: Use blockMap for O(1) lookup instead of O(n) inner loop
		var firstBlock *types.Block
		for _, block := range blocks {
			// O(1) lookup in blockMap instead of O(n) search
			if _, parentInBatch := blockMap[block.Header.PrevBlockHash]; !parentInBatch {
				firstBlock = block
				break
			}
		}

		if firstBlock == nil {
			// No block found with parent outside batch - this is invalid
			// All blocks reference each other in a cycle or batch is malformed
			bs.RecordPeerError(peer, ErrorTypeInvalidBlock)
			return fmt.Errorf("batch has no starting block - all blocks reference parents within batch")
		}

		// Build ordered chain starting from first block
		sortedBlocks := make([]*types.Block, 0, len(blocks))
		current := firstBlock
		visited := make(map[types.Hash]bool)

		for current != nil {
			currentHash := current.Hash()

			// Detect cycles
			if visited[currentHash] {
				bs.RecordPeerError(peer, ErrorTypeInvalidBlock)
				return fmt.Errorf("batch contains cycle at block %s", currentHash.String())
			}
			visited[currentHash] = true

			sortedBlocks = append(sortedBlocks, current)

			// O(1) lookup in childMap instead of O(n) search (prevents DoS)
			current = childMap[currentHash]
		}

		// Verify we sorted all blocks (batch must be a single continuous chain)
		if len(sortedBlocks) != len(blocks) {
			bs.logger.WithFields(logrus.Fields{
				"expected": len(blocks),
				"sorted":   len(sortedBlocks),
				"missing":  len(blocks) - len(sortedBlocks),
			}).Warn("Batch contains orphaned blocks - processing valid chain only")
		}
		blocks = sortedBlocks

		// Log block order after sorting
		bs.logger.WithFields(logrus.Fields{
			"first_block": blocks[0].Hash().String(),
			"last_block":  blocks[len(blocks)-1].Hash().String(),
			"count":       len(blocks),
		}).Debug("Blocks sorted by parent-child chain, ready for processing")
	}

	// FORK DETECTION: If in fork search mode, check if we found the fork point
	// Fork is detected when we receive a new block whose parent exists but we have
	// a different block at that height
	if bs.forkSearchMode.Load() && len(blocks) > 0 {
		forkDetected, forkBlock := bs.detectFork(blocks)
		if forkDetected && forkBlock != nil {
			// Get parent height - this is the common ancestor (fork point)
			parentHash := forkBlock.Header.PrevBlockHash
			parentHeight, err := bs.blockchain.GetBlockHeight(parentHash)
			if err != nil {
				bs.logger.WithError(err).Error("Failed to get parent height for fork rollback")
				bs.exitForkSearchMode()
				return fmt.Errorf("fork detection failed: %w", err)
			}

			bs.logger.WithFields(logrus.Fields{
				"fork_block":  forkBlock.Hash().String()[:16] + "...",
				"parent_hash": parentHash.String()[:16] + "...",
				"rollback_to": parentHeight,
			}).Info("Fork detected, rolling back to common ancestor")

			// Rollback to parent height (common ancestor)
			bc, ok := bs.blockchain.(*blockchainpkg.BlockChain)
			if ok {
				rm := bc.GetRecoveryManager()
				if rm == nil {
					bs.logger.Error("Recovery manager not available for rollback")
					bs.exitForkSearchMode()
					return fmt.Errorf("recovery manager not available")
				}

				if rollbackErr := rm.RollbackToHeight(parentHeight); rollbackErr != nil {
					bs.logger.WithError(rollbackErr).Error("Fork rollback failed")
					bs.RecordPeerError(peer, ErrorTypeForkDetected)
					bs.exitForkSearchMode()
					return fmt.Errorf("fork rollback failed: %w", rollbackErr)
				}
				bs.logger.WithField("new_height", parentHeight).Info("Fork rollback successful")
			}

			// Exit fork search mode after successful rollback
			bs.exitForkSearchMode()

			// Update our best height after rollback
			if newHeight, err := bs.blockchain.GetBestHeight(); err == nil {
				bs.bestHeight.Store(newHeight)
			}

			// Continue normal sync - peer will send us the correct chain
			return nil
		}
	}

	// PRE-VALIDATION: Check first block's parent and batch continuity
	// This catches issues early before expensive ProcessBlockBatch operation
	if len(blocks) > 0 {
		firstBlock := blocks[0]
		firstBlockHash := firstBlock.Hash()

		// Check if first block's parent exists (skip for genesis)
		if !firstBlock.Header.PrevBlockHash.IsZero() {
			_, err := bs.blockchain.GetBlockHeight(firstBlock.Header.PrevBlockHash)
			if err != nil {
				// Parent not found - distinguish between sequencing gap vs peer fault
				localTip := bs.bestHeight.Load()

				// If error contains "not found", check if it's a sequencing gap
				if strings.Contains(err.Error(), "not found") {
					bs.logger.WithFields(logrus.Fields{
						"local_tip":    localTip,
						"first_block":  firstBlockHash.String(),
						"first_parent": firstBlock.Header.PrevBlockHash.String(),
						"peer":         peer.GetAddress().String(),
					}).Warn("First block parent not found - possible sequencing gap")

					// This is likely our fault (requested wrong range), not peer's fault
					// Don't penalize peer, just return error to try again
					return fmt.Errorf("%w: first block parent not found", blockchainpkg.ErrSequencingGap)
				}

				// Other error (database, etc.)
				return fmt.Errorf("failed to check first block parent: %w", err)
			}

			bs.logger.WithFields(logrus.Fields{
				"first_block":  firstBlockHash.String(),
				"first_parent": firstBlock.Header.PrevBlockHash.String(),
			}).Debug("✓ First block parent exists")
		}

		// Validate batch continuity - each block must reference previous block
		for i := 1; i < len(blocks); i++ {
			prevBlockHash := blocks[i-1].Hash()
			currentBlock := blocks[i]

			if currentBlock.Header.PrevBlockHash != prevBlockHash {
				bs.logger.WithFields(logrus.Fields{
					"index":       i,
					"prev_hash":   prevBlockHash.String(),
					"expected_by": currentBlock.Hash().String(),
					"actual_prev": currentBlock.Header.PrevBlockHash.String(),
					"peer":        peer.GetAddress().String(),
				}).Error("Batch blocks not sequential - peer sent non-contiguous blocks")

				// This is peer's fault - they sent us non-sequential blocks
				bs.RecordPeerError(peer, ErrorTypeInvalidBlock)
				return fmt.Errorf("batch blocks not sequential at index %d", i)
			}
		}

		bs.logger.WithField("blocks", len(blocks)).Debug("✓ Batch continuity validated")
	}

	// Check for shutdown signal before processing batch
	select {
	case <-bs.shutdown:
		bs.logger.Debug("Shutdown requested, aborting batch")
		return fmt.Errorf("syncer shutting down")
	default:
		// Continue processing
	}

	// Process all blocks in a single batch
	// Note: Checkpoint validation happens in ProcessBlock for each block, using correct
	// heights calculated from parent relationships in the blockchain layer.
	batchProcessStart := time.Now()

	if err := bs.blockchain.ProcessBlockBatch(blocks); err != nil {
		// Use errors.Is() / errors.As() for proper error type detection
		var forkDupSpend *blockchainpkg.ErrForkDuplicateSpend
		isForkDuplicateSpend := errors.As(err, &forkDupSpend)
		isBlockExists := errors.Is(err, blockchainpkg.ErrBlockExists)
		isAllBlocksExist := errors.Is(err, blockchainpkg.ErrAllBlocksExist)
		isParentNotFound := errors.Is(err, blockchainpkg.ErrParentNotFound)
		isCheckpointFailed := errors.Is(err, blockchainpkg.ErrCheckpointFailed)
		isInvalidBlock := errors.Is(err, blockchainpkg.ErrInvalidBlock)
		isSequencingGap := errors.Is(err, blockchainpkg.ErrSequencingGap)
		isUTXONotFound := errors.Is(err, blockchainpkg.ErrUTXONotFound)
		// Detect UTXO spending errors that may indicate index inconsistency
		// This uses string matching because MarkUTXOSpent returns plain errors, not typed
		isUTXOSpentError := strings.Contains(err.Error(), "failed to mark UTXO as spent")
		// Detect corrupt blocks: header exists but transactions missing
		// This uses string matching because storage layer returns StorageError, not typed error
		isTransactionNotFound := strings.Contains(err.Error(), "transaction not found")

		// Handle ErrAllBlocksExist - all blocks in batch already exist
		// This may indicate we're on a different fork than the peer
		if isAllBlocksExist {
			// Get height of last block in batch for fork search
			lastBlockHeight := uint32(0)
			if len(blocks) > 0 {
				lastBlock := blocks[len(blocks)-1]
				if h, err := bs.blockchain.GetBlockHeight(lastBlock.Hash()); err == nil {
					lastBlockHeight = h
				} else {
					// Estimate from first block's parent
					firstBlock := blocks[0]
					if parentHeight, err := bs.blockchain.GetBlockHeight(firstBlock.Header.PrevBlockHash); err == nil {
						lastBlockHeight = parentHeight + uint32(len(blocks))
					}
				}
			}

			if !bs.forkSearchMode.Load() {
				// Enter fork search mode
				bs.enterForkSearchMode(lastBlockHeight)
			} else {
				// Already in fork search, advance virtual tip
				if err := bs.advanceForkSearch(lastBlockHeight); err != nil {
					bs.logger.WithError(err).Error("Fork search failed - depth exceeded")
					return err
				}
			}
			// Continue to next batch with virtual tip locator
			return nil
		}

		// Handle ErrBlockExists - not an error, blocks already processed
		if isBlockExists {
			bs.logger.WithField("blocks", len(blocks)).Debug("Blocks already exist, skipping batch")
			return nil
		}

		// Handle sequencing gap - not peer's fault, we requested wrong range
		if isSequencingGap {
			bs.logger.WithError(err).Warn("Batch sequencing gap detected - retrying with correct range")
			// Don't penalize peer, this is our fault
			return err
		}

		// Handle checkpoint failure - peer is on wrong fork
		if isCheckpointFailed {
			bs.logger.WithError(err).Error("Peer sent blocks on wrong fork (checkpoint mismatch)")
			bs.RecordPeerError(peer, ErrorTypeForkDetected)

			// Attempt automatic recovery
			currentHeight, _ := bs.blockchain.GetBestHeight()
			bc, ok := bs.blockchain.(*blockchainpkg.BlockChain)
			if ok {
				if recoveryErr := bc.TriggerRecovery(currentHeight, err); recoveryErr != nil {
					bs.logger.WithError(recoveryErr).Error("Automatic recovery failed")
					return fmt.Errorf("unrecoverable fork: %w", recoveryErr)
				}
				bs.logger.Debug("Recovery successful, restarting sync")
				return fmt.Errorf("sync restart needed after recovery")
			}

			return fmt.Errorf("checkpoint validation failed: %w", err)
		}

		// Handle parent not found - could be sequencing issue or corruption
		if isParentNotFound {
			bs.logger.WithError(err).Warn("Parent block not found during batch processing")

			// Attempt recovery
			currentHeight, _ := bs.blockchain.GetBestHeight()
			bc, ok := bs.blockchain.(*blockchainpkg.BlockChain)
			if ok {
				if recoveryErr := bc.TriggerRecovery(currentHeight, err); recoveryErr != nil {
					bs.logger.WithError(recoveryErr).Error("Recovery failed")
					return fmt.Errorf("unrecoverable error: %w", recoveryErr)
				}
				bs.logger.Debug("Recovery successful, restarting sync")
				return fmt.Errorf("sync restart needed after recovery")
			}

			return fmt.Errorf("parent not found: %w", err)
		}

		// Handle fork detected via duplicate transaction spend.
		// Same tx spends same UTXO at a different height — our chain has a fork block.
		// Roll back to forkHeight-1 so the correct chain can replace the fork block.
		if isForkDuplicateSpend {
			// Guard: cannot roll back below genesis
			if forkDupSpend.ForkHeight == 0 {
				bs.logger.Error("Fork detected at genesis height - cannot roll back")
				return fmt.Errorf("fork detected at genesis height, manual intervention required")
			}
			rollbackHeight := forkDupSpend.ForkHeight - 1
			// Note: Do NOT penalize the peer - they are sending the correct chain.
			// The fork is in our local chain state, not in the peer's data.
			bs.logger.WithFields(logrus.Fields{
				"fork_height":    forkDupSpend.ForkHeight,
				"rollback_to":    rollbackHeight,
				"tx":             forkDupSpend.TxHash,
				"utxo":           forkDupSpend.Outpoint,
			}).Warn("Fork detected via duplicate transaction spend, rolling back...")

			bc, ok := bs.blockchain.(*blockchainpkg.BlockChain)
			if ok {
				rm := bc.GetRecoveryManager()
				if rm == nil {
					return fmt.Errorf("recovery manager not available for fork rollback")
				}
				if rollbackErr := rm.RollbackToHeight(rollbackHeight); rollbackErr != nil {
					bs.logger.WithError(rollbackErr).Error("Fork duplicate spend rollback failed")
					return fmt.Errorf("fork rollback failed: %w", rollbackErr)
				}
				bs.logger.WithField("new_height", rollbackHeight).Info("Fork rollback successful after duplicate spend detection")
				return fmt.Errorf("sync restart needed after fork rollback from height %d", forkDupSpend.ForkHeight)
			}

			return fmt.Errorf("fork duplicate spend at height %d: %w", forkDupSpend.ForkHeight, err)
		}

		// Handle UTXO spending error - possible index inconsistency
		// Runs full chain index check before fallback to standard recovery
		if isUTXOSpentError {
			bs.logger.WithError(err).Error("UTXO spending error during batch processing - checking chain index consistency")

			bc, ok := bs.blockchain.(*blockchainpkg.BlockChain)
			if ok {
				// Create cancellable context from syncer shutdown channel
				ctx, cancel := context.WithCancel(context.Background())
				go func() {
					select {
					case <-bs.shutdown:
						cancel()
					case <-ctx.Done():
					}
				}()
				recoveryErr := bc.TriggerRecoveryForIndexInconsistency(ctx, err)
				cancel() // Clean up goroutine
				if recoveryErr != nil {
					bs.logger.WithError(recoveryErr).Error("Index inconsistency recovery failed")
					return fmt.Errorf("unrecoverable UTXO error: %w", recoveryErr)
				}
				bs.logger.Debug("Recovery successful after UTXO spending error")
				return fmt.Errorf("sync restart needed after index recovery")
			}

			return fmt.Errorf("UTXO spending error: %w", err)
		}

		// Handle UTXO not found - database corruption or missing blocks
		if isUTXONotFound {
			bs.logger.WithError(err).Error("UTXO not found during batch processing - possible database corruption")

			// Attempt recovery
			currentHeight, _ := bs.blockchain.GetBestHeight()
			bc, ok := bs.blockchain.(*blockchainpkg.BlockChain)
			if ok {
				if recoveryErr := bc.TriggerRecovery(currentHeight, err); recoveryErr != nil {
					bs.logger.WithError(recoveryErr).Error("Recovery failed")
					return fmt.Errorf("unrecoverable UTXO error: %w", recoveryErr)
				}
				bs.logger.Debug("Recovery successful after UTXO error")
				return fmt.Errorf("sync restart needed after recovery")
			}

			return fmt.Errorf("UTXO not found: %w", err)
		}

		// Handle transaction not found - corrupt block (header exists but transactions missing)
		if isTransactionNotFound {
			bs.logger.WithError(err).Error("Transaction not found - corrupt block detected (header exists but transactions missing)")

			// Attempt recovery - this is a critical corruption that needs rollback
			currentHeight, _ := bs.blockchain.GetBestHeight()
			bc, ok := bs.blockchain.(*blockchainpkg.BlockChain)
			if ok {
				if recoveryErr := bc.TriggerRecoveryForCorruptBlock(currentHeight, err); recoveryErr != nil {
					bs.logger.WithError(recoveryErr).Error("Recovery failed for corrupt block")
					return fmt.Errorf("unrecoverable corrupt block error: %w", recoveryErr)
				}
				bs.logger.Debug("Recovery successful after corrupt block detection")
				return fmt.Errorf("sync restart needed after recovery")
			}

			return fmt.Errorf("corrupt block - transaction not found: %w", err)
		}

		// Handle invalid block - peer sent cryptographically invalid data
		if isInvalidBlock {
			bs.logger.WithError(err).Error("Peer sent invalid block")
			bs.RecordPeerError(peer, ErrorTypeInvalidBlock)
			bs.invalidBlocks.Add(uint64(len(blocks)))

			// Track for reactive detection: if multiple peers fail on the same
			// block with the same error, it's likely local index corruption.
			if len(blocks) > 0 {
				failHash := blocks[0].Hash()
				estimatedHeight := bs.bestHeight.Load() + 1
				if bs.recordValidationFailure(failHash, estimatedHeight, peerAddr, err.Error()) {
					bs.logger.Warn("Same block failed validation from multiple peers - triggering reactive index recovery")
					if recoveryErr := bs.tryReactiveIndexRecovery(estimatedHeight); recoveryErr == nil {
						return fmt.Errorf("sync restart needed after recovery")
					}
				}
			}

			return fmt.Errorf("invalid block in batch: %w", err)
		}

		// Unknown error - log and penalize peer conservatively
		bs.logger.WithError(err).Error("Failed to process block batch")
		bs.RecordPeerError(peer, ErrorTypeInvalidBlock)
		bs.invalidBlocks.Add(uint64(len(blocks)))
		bs.updateConsensusState("batch-processing-failed")

		// Track for reactive detection (same logic as invalid block above)
		if len(blocks) > 0 {
			failHash := blocks[0].Hash()
			estimatedHeight := bs.bestHeight.Load() + 1
			if bs.recordValidationFailure(failHash, estimatedHeight, peerAddr, err.Error()) {
				bs.logger.Warn("Same batch failed from multiple peers - triggering reactive index recovery")
				if recoveryErr := bs.tryReactiveIndexRecovery(estimatedHeight); recoveryErr == nil {
					return fmt.Errorf("sync restart needed after recovery")
				}
			}
		}

		return fmt.Errorf("batch processing failed: %w", err)
	}

	batchProcessDuration := time.Since(batchProcessStart)

	bs.blocksDownloaded.Add(uint64(len(blocks)))

	// Update sync progress
	bs.lastSyncProgress = time.Now()

	var currentHeight uint32
	if height, err := bs.blockchain.GetBestHeight(); err == nil {
		currentHeight = height
		bs.bestHeight.Store(height)
		bs.syncHeight.Store(height)

		// Update common height for this peer (for getpeerinfo RPC synced_blocks field)
		bs.healthTracker.UpdateCommonHeight(peer.GetAddress().String(), height)
	} else {
		currentHeight = bs.bestHeight.Load()
	}

	// Periodic chain validation every 100,000 blocks
	// This detects forks early during sync
	if currentHeight > 0 && currentHeight%100000 == 0 {
		bs.logger.WithField("height", currentHeight).Debug("Performing periodic chain validation")

		// Calculate validation range (validate last 100k blocks or from genesis)
		fromHeight := uint32(0)
		if currentHeight > 100000 {
			fromHeight = currentHeight - 100000
		}

		// Validate the chain segment
		if err := bs.blockchain.ValidateChainSegment(fromHeight, currentHeight); err != nil {
			bs.logger.WithError(err).Error("❌ Chain validation failed during sync - possible fork detected!")

			// Record this as a critical error
			bs.RecordPeerError(peer, ErrorTypeForkDetected)

			// Stop sync and return error
			return fmt.Errorf("chain validation failed at height %d: %w", currentHeight, err)
		}

		bs.logger.WithField("height", currentHeight).Debug("Chain validation passed")
	}

	// Log batch processing performance
	blocksPerSecBatch := float64(len(blocks)) / batchProcessDuration.Seconds()
	msPerBlock := batchProcessDuration.Milliseconds() / int64(len(blocks))

	bs.logger.WithFields(logrus.Fields{
		"blocks_processed": len(blocks),
		"height":           currentHeight,
		"duration_ms":      batchProcessDuration.Milliseconds(),
		"blocks_per_sec":   fmt.Sprintf("%.2f", blocksPerSecBatch),
		"ms_per_block":     msPerBlock,
		"peer":             peer.GetAddress().String(),
	}).Debug("Batch processed successfully")

	endHeight := bs.getCurrentHeight()
	duration := time.Since(batchStart)
	var blocksPerSec float64
	if duration > 0 {
		blocksPerSec = float64(len(blocks)) / duration.Seconds()
	}

	bs.logger.WithFields(logrus.Fields{
		"blocks":       len(blocks),
		"start_height": startHeight,
		"end_height":   endHeight,
		"peer":         peer.GetAddress().String(),
		"duration":     duration.Round(time.Millisecond),
		"blocks_per_s": fmt.Sprintf("%.2f", blocksPerSec),
	}).Debug("Batch processing complete")

	if len(blocks) > 0 {
		batchDuration := time.Since(batchStart)
		bs.RecordPeerSuccess(peer, uint64(len(blocks)), 0, batchDuration)
		// Store batch duration for rotation decision
		bs.lastBatchDurationMs.Store(batchDuration.Milliseconds())
		// NOTE: Don't update peer's advertised tip to our local blockchain height!
		// Peer tips are tracked from their INV/HEADERS messages, not from our processing
	}

	bs.updateConsensusState("batch-complete")

	// Notify masternode layer of new block height for winner voting
	// Only notify when not in IBD mode to avoid voting for old blocks during initial sync
	// Uses IBDThreshold (5000 blocks) to determine if we're close enough to chain tip
	peerHeight := bs.peerTipHeight.Load()
	gap := uint32(0)
	if peerHeight > currentHeight {
		gap = peerHeight - currentHeight
	}
	if gap < blockchainpkg.IBDThreshold {
		bs.callbackMu.RLock()
		callback := bs.onBlockProcessedCallback
		bs.callbackMu.RUnlock()
		if callback != nil {
			callback(currentHeight)
		}
	}

	return nil
}

// RequestSync requests synchronization with a peer
func (bs *BlockchainSyncer) RequestSync(peer *Peer) {
	// Register with masternode manager if it's a masternode
	if bs.masternodePeers != nil {
		bs.masternodePeers.RegisterPeer(peer)
	}

	logFields := logrus.Fields{
		"peer":         peer.GetAddress().String(),
		"peer_height":  peer.EffectivePeerHeight(),
		"local_height": bs.bestHeight.Load(),
	}

	// Add masternode tier if applicable
	if bs.masternodePeers != nil {
		tier := bs.masternodePeers.GetMasternodeTier(peer.services)
		if tier != TierNone {
			logFields["masternode_tier"] = bs.masternodePeers.tierToString(tier)
		}
	}

	bs.logger.WithFields(logFields).Debug("Sync requested with peer")

	select {
	case bs.syncRequests <- peer:
	case <-bs.quit:
	}
}

// AnnounceInventory announces inventory from a peer
func (bs *BlockchainSyncer) AnnounceInventory(peer *Peer, inv []InventoryVector) {
	announcement := &InvAnnouncement{
		Peer: peer,
		Inv:  inv,
	}

	select {
	case bs.invAnnouncements <- announcement:
	case <-bs.quit:
	}
}

// RouteInventoryToBatch routes inventory to the batch processing channel
// Returns true if routed to batch, false if should be processed normally
func (bs *BlockchainSyncer) RouteInventoryToBatch(inv []InventoryVector) bool {
	// Phase 1 Diagnostics: Track routing attempts
	batchInProgress := bs.batchInProgress.Load()
	bs.logger.WithFields(logrus.Fields{
		"inv_count":         len(inv),
		"batch_in_progress": batchInProgress,
	}).Debug("RouteInventoryToBatch called")

	if !batchInProgress {
		bs.logger.Debug("Not routing - batch not in progress")
		return false
	}

	// Only ignore small INV if we're in IBD (far from consensus tip)
	// Near the tip, we need single-block notifications for the final blocks
	const MinBatchSize = 10
	if len(inv) < MinBatchSize {
		currentHeight := bs.getCurrentHeight()
		consensusHeight, _, err := bs.stateMachine.GetConsensusHeight()
		if err == nil {
			var gap uint32
			if consensusHeight > currentHeight {
				gap = consensusHeight - currentHeight
			}

			// Use IBDThreshold for consistency with blockchain.IsInitialBlockDownload()
			// When gap >= IBDThreshold (5000 blocks): We're in IBD, ignore single blocks
			// When gap < IBDThreshold: Near consensus tip, process single blocks
			if gap >= blockchainpkg.IBDThreshold {
				bs.logger.WithFields(logrus.Fields{
					"inv_count": len(inv),
					"gap":       gap,
					"height":    currentHeight,
					"threshold": blockchainpkg.IBDThreshold,
				}).Debug("Ignoring small INV during IBD")
				return true
			}

			// Near consensus tip - allow small INV to be processed
			bs.logger.WithFields(logrus.Fields{
				"inv_count": len(inv),
				"gap":       gap,
				"height":    currentHeight,
			}).Debug("Allowing small INV - near consensus tip")
		}
	}

	// Route to batch processing with non-blocking send
	bs.logger.Debug("Sending inventory to pendingInv channel")
	select {
	case bs.pendingInv <- inv:
		// Reset hashContinue expectation - we received the continuation INV
		bs.expectingHashContinue.Store(false)
		bs.logger.WithField("count", len(inv)).
			Debug("Successfully routed INV to batch processing channel")
		return true
	case <-time.After(3 * time.Second):
		bs.logger.Debug("Timeout routing INV to batch")
		return false
	case <-bs.ctx.Done():
		return false
	case <-bs.quit:
		return false
	}
}

// IsSyncing returns whether we're currently syncing
func (bs *BlockchainSyncer) IsSyncing() bool {
	return bs.syncing.Load()
}

// IsBatchInProgress returns whether a batch sync is currently in progress
func (bs *BlockchainSyncer) IsBatchInProgress() bool {
	return bs.batchInProgress.Load()
}

// GetNetworkHeight returns the network consensus height from the blockchain.
// This is the same value used by the getsyncstatus RPC.
func (bs *BlockchainSyncer) GetNetworkHeight() uint32 {
	return bs.blockchain.GetNetworkConsensusHeight()
}

// GetSyncProgress returns the current sync progress
func (bs *BlockchainSyncer) GetSyncProgress() (current, target uint32, peer string) {
	current = bs.bestHeight.Load()
	target = bs.syncHeight.Load()

	if syncPeer := bs.syncPeer.Load(); syncPeer != nil {
		peer = syncPeer.GetAddress().String()
		// Use max to prevent progress bar regression if effective height temporarily dips
		if h := syncPeer.EffectivePeerHeight(); h > target {
			target = h
		}
	}

	return current, target, peer
}

// GetStats returns synchronization statistics
func (bs *BlockchainSyncer) GetStats() map[string]interface{} {
	current, target, peer := bs.GetSyncProgress()

	return map[string]interface{}{
		"syncing":            bs.syncing.Load(),
		"initial_sync":       bs.initialSync.Load(),
		"current_height":     current,
		"target_height":      target,
		"sync_peer":          peer,
		"blocks_downloaded":  bs.blocksDownloaded.Load(),
		"headers_downloaded": bs.headersDownloaded.Load(),
		"duplicate_blocks":   bs.duplicateBlocks.Load(),
		"invalid_blocks":     bs.invalidBlocks.Load(),
		"pending_blocks":     len(bs.pendingBlocks),
		"requested_blocks":   len(bs.requestedBlock),
	}
}

// GetHealthTracker returns the peer health tracker (for RPC access)
func (bs *BlockchainSyncer) GetHealthTracker() *PeerHealthTracker {
	return bs.healthTracker
}

// GetPeerList returns the sync peer list (for RPC access)
func (bs *BlockchainSyncer) GetPeerList() *SyncPeerList {
	return bs.peerList
}

// GetStateMachine returns the sync state machine (for RPC access)
func (bs *BlockchainSyncer) GetStateMachine() *SyncStateMachine {
	return bs.stateMachine
}

// GetSyncStartTime returns the time when the current sync session started
func (bs *BlockchainSyncer) GetSyncStartTime() time.Time {
	return bs.syncStartTime
}

// GetBlocksPerSec returns the spike-resistant blocks per second rate
func (bs *BlockchainSyncer) GetBlocksPerSec() float64 {
	blocksDownloaded := bs.blocksDownloaded.Load()
	if blocksDownloaded == 0 || bs.syncStartTime.IsZero() {
		return 0
	}

	// Check if sync has stalled (no samples added recently)
	if bs.rateWindow != nil {
		timeSinceLastSample := bs.rateWindow.TimeSinceLastSample()

		// If no blocks received in last 30+ seconds, calculate based on stall time
		// Use 30s threshold to avoid premature decay during peer rotation or batch processing
		if timeSinceLastSample > 30*time.Second {
			// Return rate that accounts for stall: 0 blocks in stall period
			// This makes the rate drop when sync is stalled
			recentRate := bs.rateWindow.GetMinRate()
			if recentRate > 0 {
				// Gentler decay rate based on stall duration
				// After 30s stall: rate = 1.0x (no decay yet)
				// After 60s stall: rate = 0.5x
				// After 90s stall: rate = 0.33x
				stallSeconds := timeSinceLastSample.Seconds()
				decayFactor := 30.0 / stallSeconds // 30s->1.0, 60s->0.5, 90s->0.33
				if decayFactor > 1.0 {
					decayFactor = 1.0
				}
				return recentRate * decayFactor
			}
			return 0
		}

		// Normal case: use recent rate from window
		recentRate := bs.rateWindow.GetMinRate()
		if recentRate > 0 {
			return recentRate
		}
	}

	// Fallback: calculate overall rate
	syncDuration := time.Since(bs.syncStartTime)
	if syncDuration.Seconds() <= 0 {
		return 0
	}
	return float64(blocksDownloaded) / syncDuration.Seconds()
}

// GetCurrentPeer returns the current batch peer address (for RPC access)
func (bs *BlockchainSyncer) GetCurrentPeer() string {
	bs.batchMu.RLock()
	defer bs.batchMu.RUnlock()
	return bs.currentBatchPeer
}

// GetCurrentHeight returns the current blockchain height (for RPC access)
func (bs *BlockchainSyncer) GetCurrentHeight() uint32 {
	return bs.bestHeight.Load()
}

// UpdateLocalHeight updates bestHeight when a block is produced locally (staking, submitblock).
// Only advances the height forward to prevent regression from out-of-order calls.
func (bs *BlockchainSyncer) UpdateLocalHeight(height uint32) {
	for {
		current := bs.bestHeight.Load()
		if height <= current {
			return
		}
		if bs.bestHeight.CompareAndSwap(current, height) {
			bs.logger.WithFields(map[string]interface{}{
				"old_height": current,
				"new_height": height,
			}).Debug("Updated syncer height from locally produced block")
			return
		}
	}
}

// Helper serialization methods (simplified implementations)

func (bs *BlockchainSyncer) serializeGetDataMessage(msg *GetDataMessage) ([]byte, error) {
	// Serialize getdata message:
	// - VarInt count
	// - For each inventory vector: type (4 bytes) + hash (32 bytes)
	buf := make([]byte, 0, 1+len(msg.InvList)*36)

	// Add count as varint
	count := uint64(len(msg.InvList))
	buf = append(buf, byte(count)) // Simplified varint for small counts

	// Add each inventory vector
	for _, inv := range msg.InvList {
		// Type (4 bytes, little-endian)
		buf = append(buf,
			byte(inv.Type),
			byte(inv.Type>>8),
			byte(inv.Type>>16),
			byte(inv.Type>>24))

		// Hash (32 bytes)
		buf = append(buf, inv.Hash[:]...)
	}

	return buf, nil
}

func (bs *BlockchainSyncer) serializeGetHeadersMessage(msg *GetHeadersMessage) ([]byte, error) {
	// Serialize getheaders message (Bitcoin protocol format):
	// CBlockLocator format:
	// - Version (4 bytes) - protocol version
	// - Hash count (varint)
	// - Block locator hashes (32 bytes each)
	// Then:
	// - Hash stop (32 bytes)
	buf := make([]byte, 0, 4+1+len(msg.BlockLocator)*32+32)

	// Protocol version (4 bytes, little-endian) - this is part of the locator
	version := uint32(ProtocolVersion)
	buf = append(buf,
		byte(version),
		byte(version>>8),
		byte(version>>16),
		byte(version>>24))

	// Hash count as varint
	count := uint64(len(msg.BlockLocator))
	buf = append(buf, byte(count)) // Simplified varint for small counts

	// Block locator hashes
	for _, hash := range msg.BlockLocator {
		buf = append(buf, hash[:]...)
	}

	// Hash stop (empty hash = get all headers)
	buf = append(buf, msg.HashStop[:]...)

	return buf, nil
}

func (bs *BlockchainSyncer) serializeGetBlocksMessage(msg *GetBlocksMessage) ([]byte, error) {
	// Serialize getblocks message (same format as getheaders):
	// CBlockLocator format:
	// - Version (4 bytes) - protocol version
	// - Hash count (varint)
	// - Block locator hashes (32 bytes each)
	// Then:
	// - Hash stop (32 bytes)
	buf := make([]byte, 0, 4+1+len(msg.BlockLocator)*32+32)

	// Protocol version (4 bytes, little-endian) - this is part of the locator
	version := uint32(ProtocolVersion)
	buf = append(buf,
		byte(version),
		byte(version>>8),
		byte(version>>16),
		byte(version>>24))

	// Hash count as varint
	count := uint64(len(msg.BlockLocator))
	buf = append(buf, byte(count)) // Simplified varint for small counts

	// Block locator hashes
	for _, hash := range msg.BlockLocator {
		buf = append(buf, hash[:]...)
	}

	// Hash stop (empty hash = get all blocks)
	buf = append(buf, msg.HashStop[:]...)

	return buf, nil
}

// requestTransaction requests a transaction from a peer
func (bs *BlockchainSyncer) requestTransaction(hash types.Hash, peer *Peer) {
	// Create getdata message for transaction
	inv := InventoryVector{
		Type: InvTypeTx,
		Hash: hash,
	}

	getDataMsg := &GetDataMessage{
		InvList: []InventoryVector{inv},
	}

	// Serialize and send
	payload, err := bs.serializeGetDataMessage(getDataMsg)
	if err != nil {
		bs.logger.WithError(err).Error("Failed to serialize getdata message for transaction")
		return
	}

	// Convert NetMagicBytes to uint32
	msg := NewMessage(MsgGetData, payload, bs.server.params.NetMagicBytes)
	if err := peer.SendMessage(msg); err != nil {
		bs.logger.WithError(err).WithField("peer", peer.GetAddress().String()).
			Error("Failed to send getdata message for transaction")
		return
	}

	bs.logger.WithFields(logrus.Fields{
		"hash": hash.String(),
		"peer": peer.GetAddress().String(),
	}).Debug("Requested transaction from peer")
}

// checkIBDMode checks if we should enter or exit IBD mode
func (bs *BlockchainSyncer) checkIBDMode() {
	currentHeight := bs.bestHeight.Load()
	peerHeight := bs.peerTipHeight.Load()

	blocksBehind := uint32(0)
	if peerHeight > currentHeight {
		blocksBehind = peerHeight - currentHeight
	}

	isInIBD := bs.isInIBD.Load()

	// Enter IBD mode if we're far behind
	if !isInIBD && blocksBehind > bs.ibdThreshold {
		bs.isInIBD.Store(true)
		bs.logger.WithFields(logrus.Fields{
			"current_height": currentHeight,
			"peer_height":    peerHeight,
			"blocks_behind":  blocksBehind,
			"threshold":      bs.ibdThreshold,
		}).Debug("Entering IBD mode")
	}

	// Exit IBD mode if we're caught up (within 10 blocks)
	if isInIBD && blocksBehind < 10 {
		bs.isInIBD.Store(false)
		bs.logger.WithFields(logrus.Fields{
			"current_height": currentHeight,
			"peer_height":    peerHeight,
			"blocks_behind":  blocksBehind,
		}).Debug("Exiting IBD mode")

		// Process queued recent blocks if any
		if len(bs.recentBlocksQueue) > 0 {
			bs.logger.WithField("queued_blocks", len(bs.recentBlocksQueue)).
				Info("Processing queued recent blocks from IBD")

			// Request queued blocks from sync peer
			if syncPeer := bs.syncPeer.Load(); syncPeer != nil {
				invVectors := make([]InventoryVector, 0, len(bs.recentBlocksQueue))
				for _, hash := range bs.recentBlocksQueue {
					invVectors = append(invVectors, InventoryVector{
						Type: InvTypeBlock,
						Hash: hash,
					})
				}
				if err := syncPeer.SendGetData(invVectors); err != nil {
					bs.logger.WithError(err).Warn("Failed to request queued blocks")
				} else {
					bs.logger.WithField("count", len(invVectors)).
						Debug("Requested queued blocks from sync peer")
				}
			}
			bs.recentBlocksQueue = bs.recentBlocksQueue[:0] // Clear queue
		}
	}
}

// isInInitialBlockDownload returns true if node is in IBD mode
func (bs *BlockchainSyncer) isInInitialBlockDownload() bool {
	return bs.isInIBD.Load()
}

// shouldProcessInventory decides if we should process an inventory message during IBD
func (bs *BlockchainSyncer) shouldProcessInventory(inv []InventoryVector) bool {
	if !bs.isInInitialBlockDownload() {
		return true // Not in IBD, process all inventories
	}

	// During IBD, process all inventories - we cannot filter by height
	// because block hashes don't encode height information.
	// Far-ahead blocks are queued and processed when exiting IBD.
	for _, item := range inv {
		if item.Type == InvTypeBlock {
			return true
		}
	}

	// Process non-block inventories normally
	return true
}

// filterInventoryByHeight filters inventory items based on current sync height
func (bs *BlockchainSyncer) filterInventoryByHeight(inv []InventoryVector, peer *Peer) (filtered, deferred []InventoryVector) {
	currentHeight := bs.bestHeight.Load()
	maxAcceptableHeight := currentHeight + bs.maxHeightAhead

	// If we're not syncing or in IBD, process everything immediately
	if !bs.syncing.Load() && !bs.isInInitialBlockDownload() {
		return inv, nil
	}

	filtered = make([]InventoryVector, 0, len(inv))
	deferred = make([]InventoryVector, 0)

	bs.deferredMu.Lock()
	defer bs.deferredMu.Unlock()

	for _, item := range inv {
		// Only filter block inventory
		if item.Type != InvTypeBlock {
			filtered = append(filtered, item)
			continue
		}

		// Check if we already have this block deferred
		if _, exists := bs.deferredInventory[item.Hash]; exists {
			// Check if we're now close enough to process it
			def := bs.deferredInventory[item.Hash]
			if def.EstimatedHeight <= maxAcceptableHeight {
				// We can now process this block
				filtered = append(filtered, item)
				delete(bs.deferredInventory, item.Hash)
				bs.logger.WithField("hash", item.Hash.String()[:8]).
					Debug("Processing previously deferred block")
			} else {
				// Still too far ahead
				deferred = append(deferred, item)
			}
			continue
		}

		// For new blocks, we can't determine exact height from hash alone during sync
		// But we can make educated guesses based on:
		// 1. If we're in IBD and getting inventory from sync peer
		// 2. The pattern of blocks we're receiving

		// During active sync, assume blocks are roughly in order
		if bs.syncing.Load() {
			// If we've been receiving blocks sequentially, this is likely close to our current height
			// But if peer sends us their tip blocks mixed with old blocks, defer the far ones

			// Simple heuristic: if we're way behind the peer, defer blocks that seem to be near peer's tip
			if peer != nil {
				peerHeight := peer.EffectivePeerHeight()
				if peerHeight > currentHeight+bs.maxHeightAhead*2 {
					// Peer is way ahead, this might be a tip block
					// Defer it for now
					bs.deferredInventory[item.Hash] = &DeferredInv{
						Hash:            item.Hash,
						Type:            uint32(item.Type),
						ReceivedFrom:    peer,
						ReceivedAt:      time.Now(),
						EstimatedHeight: peerHeight, // Estimate it's near peer's tip
					}
					deferred = append(deferred, item)
					continue
				}
			}
		}

		// Default: process the block
		filtered = append(filtered, item)
	}

	// Clean up old deferred inventory
	now := time.Now()
	for hash, def := range bs.deferredInventory {
		// Remove deferred items older than 5 minutes
		if now.Sub(def.ReceivedAt) > 5*time.Minute {
			delete(bs.deferredInventory, hash)
			bs.logger.WithField("hash", hash.String()[:8]).
				Debug("Removed stale deferred inventory")
		}
	}

	return filtered, deferred
}

// processDeferredInventory processes deferred inventory items that are now within acceptable range
func (bs *BlockchainSyncer) processDeferredInventory() {
	bs.deferredMu.Lock()
	defer bs.deferredMu.Unlock()

	if len(bs.deferredInventory) == 0 {
		return
	}

	currentHeight := bs.bestHeight.Load()
	maxAcceptableHeight := currentHeight + bs.maxHeightAhead
	toProcess := make([]InventoryVector, 0)

	for hash, def := range bs.deferredInventory {
		if def.EstimatedHeight <= maxAcceptableHeight {
			toProcess = append(toProcess, InventoryVector{
				Type: InvType(def.Type),
				Hash: hash,
			})
			delete(bs.deferredInventory, hash)
		}
	}

	if len(toProcess) > 0 {
		bs.logger.WithField("count", len(toProcess)).
			Info("Processing deferred inventory items now in range")

		// Request the deferred blocks
		// Note: This would normally go through the regular inventory processing
		// but since we deferred them, we need to request them explicitly
		for _, item := range toProcess {
			if item.Type == InvTypeBlock {
				// Queue for download
				select {
				case bs.blockRequests <- item.Hash:
				default:
					// Queue full, skip
				}
			}
		}
	}
}

// GetConsensusValidator returns the consensus validator for external use.
// This allows other components (e.g., consensus engine) to check network consensus.
func (bs *BlockchainSyncer) GetConsensusValidator() *ConsensusValidator {
	return bs.consensusValidator
}
