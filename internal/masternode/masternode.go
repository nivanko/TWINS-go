// Package masternode implements the TWINS masternode system with 4-tier support.
//
// LEGACY FEATURES NOT IMPLEMENTED IN GO:
//
// The following legacy C++ features are intentionally NOT implemented in the Go version:
//
// 1. Budget/Governance System (permanently disabled via SPORK_13_ENABLE_SUPERBLOCKS)
//   - Budget proposals and voting (preparebudget, submitbudget, mnbudgetvote)
//   - Superblock payments for funded proposals
//   - Budget finalization and payout processing
//   - Legacy files: masternode-budget.cpp, masternode-budget.h
//   - Reason: Feature was disabled on mainnet and never actively used
//
// 2. Masternode-Sync State Machine
//   - Multi-phase synchronization states (MASTERNODE_SYNC_*)
//   - Progressive sync: sporks -> list -> winners -> budget
//   - RequestedMasternodeAssets tracking
//   - Legacy files: masternode-sync.cpp, masternode-sync.h
//   - Current approach: Simpler sync using P2P inventory system
//   - Impact: Go nodes sync masternode data via standard inv/getdata protocol
//
// 3. SwiftTX/InstantSend
//   - Transaction locking via masternode consensus
//   - Lock voting and conflict resolution
//   - Legacy files: swiftx.cpp, swiftx.h, instantx.cpp
//   - Reason: Feature was deprecated in favor of faster block times
//
// These features were either:
// - Permanently disabled via sporks (budget)
// - Deprecated (swiftx)
// - Replaced with simpler implementations (sync state machine)
//
// The Go implementation focuses on core masternode functionality:
// - 4-tier masternode system (Bronze/Silver/Gold/Platinum)
// - Payment selection algorithm matching legacy C++ exactly
// - Broadcast and ping message processing
// - Score calculation for deterministic ordering
// - Collateral validation and UTXO checking
//
// REQUIRED DEPENDENCIES FOR FULL LEGACY COMPATIBILITY:
//
// The Manager requires external dependencies to be set via setter methods for
// full legacy C++ compatibility. Without these, behavior may differ from legacy nodes:
//
// 1. SporkManager (SetSporkManager)
//   - Required for: Multi-tier masternode support (SPORK_TWINS_01_ENABLE_MASTERNODE_TIERS)
//   - Without it: Only Bronze tier (1M TWINS) is accepted, higher tiers are rejected
//   - Impact: Critical for mainnet operation after tier spork activation
//   - Spork IDs used: 20190001 (tier enable), 10009 (payment proto enforcement)
//
// 2. Blockchain (SetBlockchain)
//   - Required for: Cycle data validation, score calculation, collateral checking
//   - Without it: Fallback behaviors are used which may differ from legacy
//   - Specific uses:
//   - cycleDataValidWithBlockchain() - looks up block by hash for time validation
//   - Score calculation - looks up block hash at height-100
//   - Input age verification - checks UTXO confirmations
//   - Block timestamp retrieval - for deterministic time sources
//
// 3. PingRelayHandler (SetPingRelayHandler)
//   - Required for: Relaying valid masternode pings to P2P network
//   - Without it: Pings are validated but not relayed to peers
//   - Impact: Local masternode won't propagate ping updates
//
// Example initialization:
//
//	manager, _ := masternode.NewManager(config, logger)
//	manager.SetBlockchain(blockchain)
//	manager.SetSporkManager(sporkManager)
//	manager.SetPingRelayHandler(p2pServer.RelayMasternodePing)
//	manager.Start()
package masternode

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/twins-dev/twins-core/internal/blockchain"
	"github.com/twins-dev/twins-core/internal/consensus"
	"github.com/twins-dev/twins-core/internal/masternode/debug"
	adjustedtime "github.com/twins-dev/twins-core/internal/time"
	"github.com/twins-dev/twins-core/pkg/crypto"
	"github.com/twins-dev/twins-core/pkg/types"
)

// Sentinel errors for masternode operations.
// Use errors.Is() to check for specific error types.
var (
	// ErrMasternodeNotFound indicates the requested masternode was not found in the list.
	ErrMasternodeNotFound = errors.New("masternode not found")

	// ErrInvalidProtocol indicates a masternode has an invalid or outdated protocol version.
	ErrInvalidProtocol = errors.New("invalid protocol version")
)

// SporkInterface defines methods needed for spork-aware validation
type SporkInterface interface {
	IsActive(sporkID int32) bool
	GetValue(sporkID int32) int64
}

// Manager manages all masternodes in the network
// PaymentVotesProvider interface for accessing payment vote data
// Used by GetLastPaid to scan blockchain for payment history
type PaymentVotesProvider interface {
	// HasPayeeWithVotesAtHeight checks if payee has at least votesRequired votes at given height
	HasPayeeWithVotesAtHeight(blockHeight uint32, payAddress []byte, votesRequired int) bool
}

type Manager struct {
	// Storage
	masternodes  map[types.Outpoint]*Masternode
	addressIndex map[string]*Masternode
	pubkeyIndex  map[string]*Masternode

	// Configuration
	config               *Config
	confFile             *MasternodeConfFile // Masternode.conf for collateral UTXO checking
	logger               *logrus.Entry
	blockchain           blockchain.Blockchain // Optional blockchain reference for enhanced features
	sporkManager         SporkInterface        // Optional spork manager for tier validation
	paymentVotesProvider PaymentVotesProvider  // Optional payment votes for GetLastPaid scanning

	// P2P relay callbacks (matching legacy CMasternodePing::Relay pattern)
	pingRelayFunc      func(*MasternodePing)              // Callback for relaying valid pings to P2P network
	winnerRelayFunc    func(*MasternodeWinnerVote)        // Callback for relaying winner votes to P2P network
	broadcastRelayFunc func(*MasternodeBroadcast, string) // Callback for relaying broadcasts to P2P network; string is excludeAddr (legacy mnb.Relay())

	// Active masternode state (for nodes running as masternodes)
	activeMasternode *ActiveMasternode

	// Payment tracking
	paymentQueue      *PaymentQueue
	lastPaid          map[types.Outpoint]time.Time
	scheduledPayments map[uint32][]byte // blockHeight -> scheduled payee script (LEGACY COMPAT: stores script, not outpoint)

	// Synchronization
	mu          sync.RWMutex
	synced      bool
	syncManager *SyncManager // Masternode sync state machine

	// Ping deduplication (matches legacy mapSeenMasternodePing)
	// Maps ping hash to sigTime to prevent duplicate processing and relay
	seenPings        map[types.Hash]int64
	seenPingMessages map[types.Hash]*MasternodePing // Full ping payloads keyed by hash (legacy mapSeenMasternodePing behavior)
	seenPingsMu      sync.RWMutex
	maxSeenPingsAge  int64 // Max age in seconds for seenPings entries (legacy: MASTERNODE_REMOVAL_SECONDS * 2)

	// Broadcast deduplication (matches legacy mapSeenMasternodeBroadcast)
	// Maps broadcast hash to full broadcast (for lastPing updates per Issue #2)
	// LEGACY COMPATIBILITY: C++ stores full CMasternodeBroadcast, not just sigTime,
	// because CheckAndUpdate updates mapSeenMasternodeBroadcast[hash].lastPing on each ping
	seenBroadcasts   map[types.Hash]*MasternodeBroadcast
	seenBroadcastsMu sync.RWMutex

	// Winner vote deduplication (matches legacy mapSeenMasternodePaymentVote)
	seenWinners   map[types.Hash]int64
	seenWinnersMu sync.RWMutex

	// Winner vote storage (matches legacy mapMasternodePayeeVotes)
	// Stores full winner votes with signatures for persistence and serving mnget requests
	winnerVotes   map[types.Hash]*PaymentWinnerCacheEntry
	winnerVotesMu sync.RWMutex

	// LEGACY COMPAT: mapMasternodesLastVote - tracks last vote height per masternode
	// C++ Reference: masternode-payments.h:242,268-281
	// Prevents same masternode from voting multiple times for same block
	masternodesLastVote   map[string]uint32 // outpoint key (hash+index) -> lastVoteHeight
	masternodesLastVoteMu sync.RWMutex

	// LEGACY COMPAT: mapMasternodeBlocks - per-block payee vote aggregation
	// C++ Reference: masternode-payments.h:90-159
	// Stores accumulated votes per payee for each block height
	masternodeBlocks   map[uint32]*MasternodeBlockPayees
	masternodeBlocksMu sync.RWMutex

	// Cache metadata for quick-restart detection
	// When cache is loaded from mncache.dat, these track freshness for sync skip
	cacheLoadedAt    time.Time // file modification time of mncache.dat (when cache was saved)
	cacheLoadedCount int       // how many masternodes were loaded from cache

	// Cached fulfilled request maps loaded from mncache.dat
	// Temporarily stored here until pushed to SyncManager during startup wiring
	cachedFulfilledMNSync  map[string]int64 // WeAskedForList from cache
	cachedFulfilledMNWSync map[string]int64 // WeAskedForEntry from cache

	// Misbehavior reporting callback for bad peers (matches legacy Misbehaving())
	// Called with (peerAddr, score, reason) when peer sends invalid data
	misbehaviorFunc func(peerAddr string, score int32, reason string)

	// AskForMN callback for requesting unknown masternode broadcasts (Issue #5)
	// Legacy: masternodeman.cpp:885-892 - AskForMN(pfrom, mnp.vin) when ping received for unknown MN
	// Called with outpoint of unknown masternode to request its broadcast from peers
	askForMNFunc func(outpoint types.Outpoint)

	// Debug event collector (nil when disabled, zero-cost atomic check)
	debugCollector atomic.Pointer[debug.Collector]

	// IBD-skip tracking for transition-only logging (prevents log spam during extended IBD)
	lastUpdateSkippedIBD bool

	// Lifecycle
	started  bool
	stopCh   chan struct{}
	stopOnce sync.Once // Ensures stopCh is closed exactly once
	wg       sync.WaitGroup
}

// NewManager creates a new masternode manager
func NewManager(config *Config, logger *logrus.Logger) (*Manager, error) {
	if config == nil {
		config = DefaultConfig()
	}

	if logger == nil {
		logger = logrus.New()
	}

	m := &Manager{
		masternodes:  make(map[types.Outpoint]*Masternode),
		addressIndex: make(map[string]*Masternode),
		pubkeyIndex:  make(map[string]*Masternode),
		config:       config,
		logger:       logger.WithField("component", "masternode"),
		paymentQueue: &PaymentQueue{
			queue:    make([]*Masternode, 0),
			lastPaid: make(map[types.Outpoint]time.Time),
		},
		lastPaid:            make(map[types.Outpoint]time.Time),
		scheduledPayments:   make(map[uint32][]byte),
		seenPings:           make(map[types.Hash]int64),
		seenPingMessages:    make(map[types.Hash]*MasternodePing),
		maxSeenPingsAge:     RemovalSeconds * 2, // Legacy: MASTERNODE_REMOVAL_SECONDS * 2 = 15600 seconds (4.33 hours)
		seenBroadcasts:      make(map[types.Hash]*MasternodeBroadcast),
		seenWinners:         make(map[types.Hash]int64),
		winnerVotes:         make(map[types.Hash]*PaymentWinnerCacheEntry),
		masternodesLastVote: make(map[string]uint32),
		masternodeBlocks:    make(map[uint32]*MasternodeBlockPayees),
		stopCh:              make(chan struct{}),
		syncManager:         NewSyncManager(logger),
	}

	return m, nil
}

// Start starts the masternode manager with strict dependency validation.
// CRITICAL: This method enforces that sporkManager and blockchain are configured.
// For unit testing where dependencies are intentionally omitted, use StartWithoutValidation().
//
// LEGACY COMPATIBILITY: Legacy C++ always has spork/blockchain available as part of
// the monolithic binary. Go's modular architecture requires explicit wiring, but
// in production these dependencies MUST be configured to maintain consensus compatibility.
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.started {
		return fmt.Errorf("masternode manager already started")
	}

	// STRICT VALIDATION: Enforce critical dependencies for production operation
	// Missing dependencies cause consensus divergence with mainnet:
	// - No sporkManager: Only Bronze tier accepted, higher tiers rejected
	// - No blockchain: No UTXO validation, fake masternodes can register
	if m.sporkManager == nil {
		return fmt.Errorf("masternode manager: sporkManager is REQUIRED for production operation (tier validation, protocol enforcement)")
	}
	if m.blockchain == nil {
		return fmt.Errorf("masternode manager: blockchain is REQUIRED for production operation (UTXO validation, ping block hash verification)")
	}

	// pingRelayFunc is optional but warn if not set (pings won't be broadcast)
	if m.pingRelayFunc == nil {
		m.logger.Warn("Masternode manager: ping relay function not set - pings will not be broadcast to network")
	}

	m.started = true
	m.logger.Info("Masternode manager started with all critical dependencies")

	// Start background update routine
	m.wg.Add(1)
	go m.updateLoop()

	return nil
}

// StartWithoutValidation starts the masternode manager WITHOUT dependency validation.
// WARNING: This method should ONLY be used for unit testing where dependencies are
// intentionally omitted. Using this in production will cause consensus divergence.
//
// Consequences of missing dependencies:
// - No sporkManager: All masternodes treated as Bronze tier, higher tiers rejected
// - No blockchain: No UTXO validation, fake masternodes can register, wrong winners
// - No relayFunc: Pings not broadcast, local masternode expires after 2 hours
func (m *Manager) StartWithoutValidation() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.started {
		return fmt.Errorf("masternode manager already started")
	}

	// Log warnings about missing dependencies (for debugging test issues)
	var missing []string
	if m.sporkManager == nil {
		missing = append(missing, "sporkManager")
	}
	if m.blockchain == nil {
		missing = append(missing, "blockchain")
	}
	if m.pingRelayFunc == nil {
		missing = append(missing, "pingRelayFunc")
	}
	if len(missing) > 0 {
		m.logger.Warnf("Masternode manager: starting WITHOUT validation - missing: %v (TEST MODE ONLY)", missing)
	}

	m.started = true
	m.logger.Info("Masternode manager started (without dependency validation)")

	// Start background update routine
	m.wg.Add(1)
	go m.updateLoop()

	return nil
}

// Stop stops the masternode manager
func (m *Manager) Stop() error {
	// CRITICAL: Stop sync manager FIRST, without ANY locks.
	// m.syncManager is set once in NewManager and never changes, so it's safe to read.
	// We must NOT hold ANY lock when calling StopProcessLoop because the processLoop
	// calls getMasternodeCount() which needs m.mu.RLock() - any lock here causes deadlock.
	syncMgr := m.syncManager // Safe: set once in NewManager, never modified

	if syncMgr != nil {
		syncMgr.StopProcessLoop()
	}

	// CRITICAL: Signal shutdown BEFORE acquiring any locks.
	// updateLoop holds m.mu.Lock() while doing blockchain operations in updateMasternodes().
	// If we try to acquire the lock first, we deadlock:
	// - Stop() waits for m.mu.Lock()
	// - updateLoop holds the lock, waiting for stopCh or next ticker
	// - stopCh can't be closed because Stop() is blocked
	//
	// Solution: Use sync.Once to safely close stopCh, then wait for updateLoop to exit.
	m.stopOnce.Do(func() {
		close(m.stopCh)
	})
	m.wg.Wait()

	// Now acquire the lock for final cleanup (safe because updateLoop has exited)
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.started {
		return nil
	}

	// Stop active masternode auto-management loop
	if m.activeMasternode != nil {
		m.activeMasternode.StopAutoManagement()
	}

	m.started = false
	m.logger.Info("Masternode manager stopped")
	return nil
}

// SetBlockchain sets the blockchain instance for enhanced features
func (m *Manager) SetBlockchain(bc blockchain.Blockchain) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blockchain = bc
	m.logger.Debug("Blockchain configured for masternode manager")
}

// SetSporkManager sets the spork manager for spork-aware tier validation
func (m *Manager) SetSporkManager(sm SporkInterface) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sporkManager = sm
	m.logger.Debug("Spork manager configured for masternode manager")
}

// SetConfFile sets the masternode.conf file for collateral UTXO checking.
// Used by wallet to filter masternode collateral UTXOs from Coin Control and automatic selection.
// Legacy: Provides CMasternodeConfig data for wallet UTXO locking (mnconflock feature)
func (m *Manager) SetConfFile(cf *MasternodeConfFile) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.confFile = cf
	m.logger.Debug("Masternode.conf configured for collateral UTXO filtering")
}

// ValidateDependencies checks that critical dependencies are configured
// LEGACY COMPATIBILITY: Legacy C++ always has spork/blockchain available as part of
// the monolithic binary. Go's modular architecture allows optional dependencies for
// testing, but in production these must be configured to avoid behavioral drift.
// Call this after all Set*() methods to ensure production-ready configuration.
// Returns error if dependencies missing - caller decides whether to abort or continue.
func (m *Manager) ValidateDependencies() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var missing []string

	if m.sporkManager == nil {
		missing = append(missing, "spork manager (required for tier validation, protocol enforcement)")
	}

	if m.blockchain == nil {
		missing = append(missing, "blockchain (required for UTXO validation, ping block hash verification)")
	}

	if len(missing) > 0 {
		// Return error - caller decides whether to abort (mainnet) or warn (testnet/regtest)
		return fmt.Errorf("masternode manager missing critical dependencies: %v", missing)
	}

	m.logger.Debug("Masternode manager dependencies validated")
	return nil
}

// SetPaymentVotesProvider sets the payment votes provider for GetLastPaid scanning
// This enables proper blockchain scanning with HasPayeeWithVotes logic
func (m *Manager) SetPaymentVotesProvider(pvp PaymentVotesProvider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.paymentVotesProvider = pvp
	m.logger.Debug("Payment votes provider configured for masternode manager")
}

// IsValidCollateral validates collateral amount with spork-aware tier support
// This matches legacy C++ isMasternodeCollateral() from main.cpp:114-120
//
// Legacy behavior:
//   - If SPORK_TWINS_01_ENABLE_MASTERNODE_TIERS is OFF: Only Bronze (1M TWINS) accepted
//   - If SPORK_TWINS_01_ENABLE_MASTERNODE_TIERS is ON: All 4 tiers accepted
//
// Returns:
//   - tier: The masternode tier for the collateral amount
//   - valid: Whether the collateral amount is valid given current spork state
func (m *Manager) IsValidCollateral(amount int64) (MasternodeTier, bool) {
	// Bronze (1M TWINS) is always valid regardless of spork state
	if amount == TierBronzeCollateral {
		return Bronze, true
	}

	// Higher tiers require SPORK_TWINS_01_ENABLE_MASTERNODE_TIERS to be active
	m.mu.RLock()
	tiersEnabled := m.sporkManager != nil && m.sporkManager.IsActive(SporkTwinsEnableMasternodeTiers)
	m.mu.RUnlock()

	if !tiersEnabled {
		// Spork OFF or no spork manager - only Bronze tier allowed
		// Log at debug level since this is expected behavior when spork is off
		m.logger.WithField("amount", amount).Debug("Multi-tier collateral rejected: SPORK_TWINS_01 not active")
		return Bronze, false
	}

	// Check higher tier collateral amounts
	switch amount {
	case TierSilverCollateral:
		return Silver, true
	case TierGoldCollateral:
		return Gold, true
	case TierPlatinumCollateral:
		return Platinum, true
	default:
		// Invalid collateral amount (doesn't match any tier)
		return Bronze, false
	}
}

// IsTierSporkActive returns whether multi-tier masternode support is enabled
// This is a convenience method for checking SPORK_TWINS_01_ENABLE_MASTERNODE_TIERS
func (m *Manager) IsTierSporkActive() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sporkManager != nil && m.sporkManager.IsActive(SporkTwinsEnableMasternodeTiers)
}

// SetPingRelayHandler sets the callback for relaying valid pings to P2P network
// This matches legacy CMasternodePing::Relay() which calls RelayInv(MSG_MASTERNODE_PING, hash)
// The P2P layer should call this to register its relay function
func (m *Manager) SetPingRelayHandler(handler func(*MasternodePing)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pingRelayFunc = handler
	m.logger.Debug("Ping relay handler configured for masternode manager")
}

// SetWinnerRelayHandler sets the callback for relaying winner votes to P2P network
// This matches legacy CMasternodePaymentWinner::Relay() which calls RelayInv(MSG_MASTERNODE_WINNER, hash)
// The P2P layer should call this to register its relay function
func (m *Manager) SetWinnerRelayHandler(handler func(*MasternodeWinnerVote)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.winnerRelayFunc = handler
	m.logger.Debug("Winner relay handler configured for masternode manager")
}

// SetBroadcastRelayHandler sets the callback for relaying broadcasts to P2P network
// This matches legacy CMasternodeBroadcast::Relay() which calls RelayInv(MSG_MASTERNODE_ANNOUNCE, hash)
// CRITICAL: Without this, local masternode broadcasts are never sent to the network
// Legacy: activemasternode.cpp:119 calls mnb.Relay() after CreateBroadcast
func (m *Manager) SetBroadcastRelayHandler(handler func(*MasternodeBroadcast, string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.broadcastRelayFunc = handler
	m.logger.Debug("Broadcast relay handler configured for masternode manager")
}

// SetDebugCollector sets the debug event collector for masternode activity tracing.
// When enabled, all masternode operations emit structured events to a JSONL file.
// The collector uses an atomic.Bool check so there is zero cost when disabled.
// All fields are atomic.Pointer so no mutex locking is needed.
func (m *Manager) SetDebugCollector(collector *debug.Collector) {
	m.debugCollector.Store(collector)
	if m.syncManager != nil {
		m.syncManager.debugCollector.Store(collector)
	}
	if m.activeMasternode != nil {
		m.activeMasternode.debugCollector.Store(collector)
	}
	m.logger.Debug("Debug collector configured for masternode manager")
}

// SetAskForMNHandler sets the callback for requesting unknown masternode broadcasts (Issue #5)
// Legacy: masternodeman.cpp:885-892 - when a ping arrives for an unknown masternode,
// AskForMN(pfrom, mnp.vin) is called to request the masternode broadcast from peers.
// This enables faster network recovery when receiving pings for masternodes we don't know about.
func (m *Manager) SetAskForMNHandler(handler func(outpoint types.Outpoint)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.askForMNFunc = handler
	m.logger.Debug("AskForMN handler configured for masternode manager")
}

// SetActiveMasternodeInstance sets the active masternode instance for this manager
// This enables winner vote generation and broadcasting when the masternode is started
// The ActiveMasternode should be initialized separately and passed here
// LEGACY COMPATIBILITY: Also starts the auto-management loop that periodically calls ManageStatus()
// This replicates C++ behavior from obfuscation.cpp:2305 where ManageStatus() is called every 5 minutes
func (m *Manager) SetActiveMasternodeInstance(am *ActiveMasternode) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Stop existing auto-management if switching instances
	if m.activeMasternode != nil && m.activeMasternode != am {
		m.activeMasternode.StopAutoManagement()
	}

	m.activeMasternode = am
	if am != nil {
		m.logger.WithField("outpoint", am.Vin.String()).Debug("Active masternode instance configured")
		// Start auto-management loop for periodic ManageStatus() calls
		// This ensures pings are sent regularly to keep the masternode active
		am.StartAutoManagement()
		m.logger.Debug("Active masternode auto-management loop started")
	}
}

// GetActiveMasternode returns the active masternode state (nil if not running as masternode)
func (m *Manager) GetActiveMasternode() *ActiveMasternode {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.activeMasternode
}

// CreateBroadcastForRemote creates a broadcast for a remote masternode (cold wallet scenario).
// This method is designed for cold wallet operation where the GUI controls masternodes remotely.
// Unlike ActiveMasternode.CreateBroadcastFromConf(), this doesn't require an active masternode instance.
//
// Parameters:
//   - entry: Masternode entry from masternode.conf containing alias, IP, private key, and collateral info
//   - collateralKey: Private key for the collateral address (from wallet)
//
// Returns the signed broadcast ready for ProcessBroadcast() and network relay.
func (m *Manager) CreateBroadcastForRemote(entry *MasternodeEntry, collateralKey *crypto.PrivateKey) (*MasternodeBroadcast, error) {
	m.mu.RLock()
	bc := m.blockchain
	m.mu.RUnlock()

	if bc == nil {
		return nil, fmt.Errorf("blockchain not available - ensure node is initialized")
	}

	// Parse masternode private key from entry
	mnPrivKey, err := crypto.DecodeWIF(entry.PrivKey)
	if err != nil {
		return nil, fmt.Errorf("invalid masternode private key for %s: %w", entry.Alias, err)
	}

	// Parse service address
	addr, err := net.ResolveTCPAddr("tcp", entry.IP)
	if err != nil {
		return nil, fmt.Errorf("invalid service address for %s: %w", entry.Alias, err)
	}

	// LEGACY COMPATIBILITY: Use GetAdjustedTime() like legacy C++
	sigTime := consensus.GetAdjustedTimeUnix()

	// Get recent block hash (tip - 12) for ping validation
	// Legacy C++ requires valid block hash for ping validation:
	// - Ping must reference a block within last 24 blocks
	// - Broadcasts with ZeroHash are rejected by network peers immediately
	blockHash, err := m.getRecentBlockHashForBroadcast()
	if err != nil {
		return nil, fmt.Errorf("cannot create broadcast for %s: %w", entry.Alias, err)
	}

	// Create ping with recent block hash
	ping := &MasternodePing{
		OutPoint:  entry.GetOutpoint(),
		BlockHash: blockHash,
		SigTime:   sigTime,
	}

	// Sign ping with masternode key
	pingMsg := ping.getSignatureMessage()
	pingSig, err := crypto.SignCompact(mnPrivKey, pingMsg)
	if err != nil {
		return nil, fmt.Errorf("failed to sign ping: %w", err)
	}
	ping.Signature = pingSig

	// Create broadcast
	broadcast := &MasternodeBroadcast{
		OutPoint:         entry.GetOutpoint(),
		Addr:             addr,
		PubKeyCollateral: collateralKey.PublicKey(),
		PubKeyMasternode: mnPrivKey.PublicKey(),
		SigTime:          sigTime,
		Protocol:         ActiveProtocolVersion, // Must match legacy (70928)
		LastPing:         ping,
	}

	// Sign broadcast with collateral key
	broadcastMsg := broadcast.getNewSignatureMessage()
	broadcastSig, err := crypto.SignCompact(collateralKey, broadcastMsg)
	if err != nil {
		return nil, fmt.Errorf("failed to sign broadcast: %w", err)
	}
	broadcast.Signature = broadcastSig

	return broadcast, nil
}

// getRecentBlockHashForBroadcast returns a recent block hash (tip - PingBlockDepth) for broadcast/ping creation.
// This is used by CreateBroadcastForRemote for cold wallet scenarios.
func (m *Manager) getRecentBlockHashForBroadcast() (types.Hash, error) {
	m.mu.RLock()
	bc := m.blockchain
	m.mu.RUnlock()

	if bc == nil {
		return types.ZeroHash, fmt.Errorf("blockchain not available")
	}

	height, err := bc.GetBestHeight()
	if err != nil {
		return types.ZeroHash, fmt.Errorf("failed to get best height: %w", err)
	}

	if height < PingBlockDepth {
		return types.ZeroHash, fmt.Errorf("chain too short: height %d < %d (not synced)", height, PingBlockDepth)
	}

	targetHeight := height - PingBlockDepth
	block, err := bc.GetBlockByHeight(targetHeight)
	if err != nil {
		return types.ZeroHash, fmt.Errorf("failed to get block at height %d: %w", targetHeight, err)
	}
	if block == nil {
		return types.ZeroHash, fmt.Errorf("block at height %d is nil", targetHeight)
	}

	return block.Hash(), nil
}

// CheckDependencies verifies all external dependencies are configured and logs warnings for missing ones.
// Returns a list of missing dependency names. Empty list means all dependencies are configured.
// This should be called after all Set* methods to verify proper initialization.
func (m *Manager) CheckDependencies() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var missing []string

	if m.blockchain == nil {
		missing = append(missing, "blockchain")
		m.logger.Warn("Masternode manager: blockchain not configured - collateral validation and UTXO checks will fail")
	}

	if m.sporkManager == nil {
		missing = append(missing, "sporkManager")
		m.logger.Warn("Masternode manager: spork manager not configured - multi-tier support disabled, only Bronze (1M) accepted")
	}

	if m.pingRelayFunc == nil {
		missing = append(missing, "pingRelayHandler")
		m.logger.Warn("Masternode manager: ping relay handler not configured - pings will not be broadcast to network")
	}

	if m.winnerRelayFunc == nil {
		missing = append(missing, "winnerRelayHandler")
		m.logger.Warn("Masternode manager: winner relay handler not configured - winner votes will not be broadcast")
	}

	if len(missing) == 0 {
		m.logger.Debug("Masternode manager: all external dependencies configured")
	}

	return missing
}

// IsActiveMasternode returns true if this node is configured as an active masternode and started
func (m *Manager) IsActiveMasternode() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.activeMasternode != nil && m.activeMasternode.IsStarted()
}

// CanVoteForWinner returns true if this node can vote for a winner at the given height
// Checks: active masternode configured, started, and not already voted for this height
func (m *Manager) CanVoteForWinner(height uint32) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.activeMasternode == nil || !m.activeMasternode.IsStarted() {
		return false
	}

	// Check if already voted for this height
	return !m.activeMasternode.HasVotedForHeight(height)
}

// CanVote checks if a masternode can vote for the given block height.
// LEGACY COMPATIBILITY: Matches CMasternodePayments::CanVote()
// C++ Reference: masternode-payments.cpp:268-281
//
// This provides GLOBAL vote deduplication (prevents any masternode from voting
// twice for the same block), while CanVoteForWinner only checks LOCAL state.
//
// Returns true if the masternode has not yet voted for this height.
// If the masternode has already voted, returns false.
// IMPORTANT: Also records the vote when returning true (atomic check-and-set).
func (m *Manager) CanVote(outpoint types.Outpoint, blockHeight uint32) bool {
	// Create unique key for this masternode: hash + index
	key := outpoint.Hash.String() + fmt.Sprintf("%d", outpoint.Index)

	m.masternodesLastVoteMu.Lock()
	defer m.masternodesLastVoteMu.Unlock()

	if lastHeight, exists := m.masternodesLastVote[key]; exists {
		if lastHeight == blockHeight {
			return false // Already voted for this block
		}
	}

	// Record the vote
	m.masternodesLastVote[key] = blockHeight
	return true
}

// CreateWinnerVote creates a winner vote for the given block height.
// This implements the core logic from legacy CMasternodePayments::ProcessBlock():
//  1. Get the winning masternode using GetNextMasternodeInQueueForPayment
//  2. Create the vote message with voter outpoint, block height, and payee script
//  3. Sign the vote with the active masternode's private key
//
// Returns the signed vote or an error if vote creation fails.
// The caller is responsible for broadcasting the vote via RelayWinnerVote.
func (m *Manager) CreateWinnerVote(blockHeight uint32) (*MasternodeWinnerVote, error) {
	m.mu.RLock()
	am := m.activeMasternode
	m.mu.RUnlock()

	if am == nil {
		return nil, fmt.Errorf("no active masternode configured")
	}

	if !am.IsStarted() {
		return nil, fmt.Errorf("active masternode not started")
	}

	if am.HasVotedForHeight(blockHeight) {
		return nil, fmt.Errorf("already voted for height %d", blockHeight)
	}

	privKey := am.GetPrivateKey()
	if privKey == nil {
		return nil, fmt.Errorf("active masternode has no private key")
	}

	// LEGACY COMPATIBILITY FIX: Rank gate - only top 10 masternodes can vote
	// Legacy: masternode-payments.cpp:757-766
	// int n = mnodeman.GetMasternodeRank(activeMasternode.vin, nBlockHeight - 100, ActiveProtocol());
	// if (n == -1) return false; // Unknown Masternode
	// if (n > MNPAYMENTS_SIGNATURES_TOTAL) return false; // Not in top 10
	//
	// CRITICAL: For blockHeight < 100 (ScoreBlockDepth), legacy C++ has underflow:
	// nBlockHeight - 100 underflows to huge value, GetBlockHash fails, rank becomes -1,
	// vote is rejected. We must match this behavior by explicitly rejecting votes for
	// blocks < 100 to maintain consensus compatibility.
	if blockHeight < ScoreBlockDepth {
		// Legacy implicitly rejects due to underflow → GetBlockHash failure → rank=-1
		m.logger.WithFields(map[string]interface{}{
			"height":          blockHeight,
			"scoreBlockDepth": ScoreBlockDepth,
		}).Debug("Rejecting vote for block below ScoreBlockDepth (legacy compatibility)")
		return nil, fmt.Errorf("cannot vote for blocks below height %d", ScoreBlockDepth)
	}

	minProto := m.getMinMasternodePaymentsProto()
	rank := m.GetMasternodeRank(am.Vin, blockHeight-ScoreBlockDepth, minProto, true)
	if rank == -1 {
		return nil, fmt.Errorf("unknown masternode, cannot vote")
	}
	if rank > consensus.TotalPaymentSignatures {
		m.logger.WithFields(map[string]interface{}{
			"rank":     rank,
			"max_rank": consensus.TotalPaymentSignatures,
			"height":   blockHeight,
		}).Debug("Masternode not in top 10, cannot vote")
		return nil, fmt.Errorf("masternode not in top %d (rank: %d)", consensus.TotalPaymentSignatures, rank)
	}
	m.logger.WithFields(map[string]interface{}{
		"rank":   rank,
		"height": blockHeight,
	}).Debug("Masternode eligible to vote")

	// Get the winning masternode for this block height
	// Legacy: CMasternodePayments::ProcessBlock calls mnodeman.GetNextMasternodeInQueueForPayment
	winner, _ := m.GetNextMasternodeInQueueForPayment(blockHeight, true)
	if winner == nil {
		return nil, fmt.Errorf("no masternode winner found for height %d", blockHeight)
	}

	// Get payee script from winner's collateral public key
	payeeScript := winner.GetPayeeScript()
	if payeeScript == nil {
		return nil, fmt.Errorf("winner has no payee script")
	}

	// Create the vote
	vote := &MasternodeWinnerVote{
		VoterOutpoint: am.Vin,
		BlockHeight:   blockHeight,
		PayeeScript:   payeeScript,
	}

	// Sign the vote
	// Legacy format: hash-index + blockHeight + payeeScript (hex)
	message := vote.GetSignatureMessage()
	signature, err := crypto.SignCompact(privKey, message)
	if err != nil {
		return nil, fmt.Errorf("failed to sign vote: %w", err)
	}
	vote.Signature = signature

	// Mark this height as voted
	am.MarkVotedForHeight(blockHeight)

	m.logger.WithFields(map[string]interface{}{
		"block_height": blockHeight,
		"voter":        am.Vin.String(),
		"winner":       winner.OutPoint.String(),
	}).Debug("Created winner vote")

	return vote, nil
}

// ProcessBlockForWinner processes a new block and creates/relays a winner vote if applicable.
// This is the main entry point called when a new block is connected to the chain.
// Implements legacy CMasternodePayments::ProcessBlock() flow:
//  1. Check if we're an active masternode
//  2. Calculate vote height (currentHeight + 10)
//  3. Check if we haven't already voted
//  4. Create and sign the vote
//  5. Relay to network
//
// Returns the created vote (if any) or nil if no vote was created.
func (m *Manager) ProcessBlockForWinner(currentHeight uint32) (*MasternodeWinnerVote, error) {
	// Calculate the block height we're voting for (current + 10)
	voteHeight := currentHeight + WinnerVoteBlocksAhead

	// Check if we can vote
	if !m.CanVoteForWinner(voteHeight) {
		return nil, nil // Not an error, just not eligible to vote
	}

	// Create the vote
	vote, err := m.CreateWinnerVote(voteHeight)
	if err != nil {
		return nil, err
	}

	// Relay the vote if we have a relay handler
	m.mu.RLock()
	relayFunc := m.winnerRelayFunc
	m.mu.RUnlock()

	if relayFunc != nil {
		relayFunc(vote)
		m.logger.WithFields(map[string]interface{}{
			"block_height": voteHeight,
			"voter":        vote.VoterOutpoint.String(),
		}).Debug("Relayed winner vote")
	} else {
		m.logger.Warn("Winner vote created but no relay handler configured")
	}

	return vote, nil
}

// GetSyncManager returns the masternode sync manager
// Used by consensus layer for isSynced checks
func (m *Manager) GetSyncManager() *SyncManager {
	return m.syncManager
}

// GetCacheInfo returns cache freshness metadata for sync skip decisions.
// loadedAt is the mncache.dat file modification time (when cache was last saved).
// count is the number of masternodes successfully loaded from cache.
func (m *Manager) GetCacheInfo() (loadedAt time.Time, count int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cacheLoadedAt, m.cacheLoadedCount
}

// GetCachedFulfilledMaps returns fulfilled request maps loaded from mncache.dat.
// Called by startup code to push these to SyncManager after wiring.
func (m *Manager) GetCachedFulfilledMaps() (mnsync map[string]int64, mnwsync map[string]int64) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cachedFulfilledMNSync, m.cachedFulfilledMNWSync
}

// SetMisbehaviorReporter sets the callback for reporting peer misbehavior
// This is called when a peer sends invalid masternode data (e.g., mismatched pubkey/vin)
// Legacy: Misbehaving(pfrom->GetId(), score) in masternodeman.cpp
func (m *Manager) SetMisbehaviorReporter(fn func(peerAddr string, score int32, reason string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.misbehaviorFunc = fn
}

// IsSynced returns true if masternode sync is complete
// This is the main entry point for legacy masternodeSync.IsSynced() checks
// Legacy: RequestedMasternodeAssets == MASTERNODE_SYNC_FINISHED
func (m *Manager) IsSynced() bool {
	if m.syncManager == nil {
		return false
	}
	return m.syncManager.IsSynced()
}

// GetSyncStatus returns a human-readable sync status
func (m *Manager) GetSyncStatus() string {
	if m.syncManager == nil {
		return "Sync manager not initialized"
	}
	return m.syncManager.GetSyncStatus()
}

// AddedMasternodeWinner delegates winner vote notification to SyncManager.
// Implements MasternodeManager interface for P2P layer.
// Legacy: masternodeSync.AddedMasternodeWinner(hash) called from masternode-payments.cpp
func (m *Manager) AddedMasternodeWinner(hash types.Hash) {
	if m.syncManager == nil {
		return
	}
	m.syncManager.AddedMasternodeWinner(hash)
}

// ProcessSyncStatusCount forwards sync status count to the sync manager
// Implements MasternodeManager interface for P2P layer
func (m *Manager) ProcessSyncStatusCount(peerAddr string, syncType int, count int) {
	if m.syncManager == nil {
		return
	}
	m.syncManager.ProcessSyncStatusCount(peerAddr, syncType, count)
}

// HasFulfilledRequest checks if a peer has been asked for a sync request type
// Implements MasternodeManager interface for P2P layer
// requestType is "mnsync" for LIST or "mnwsync" for MNW
func (m *Manager) HasFulfilledRequest(peerAddr string, requestType string) bool {
	if m.syncManager == nil {
		return false
	}
	return m.syncManager.HasFulfilledRequest(peerAddr, requestType)
}

// FulfilledRequest marks a peer as having been asked for a sync request type
// Implements MasternodeManager interface for P2P layer
func (m *Manager) FulfilledRequest(peerAddr string, requestType string) {
	if m.syncManager == nil {
		return
	}
	m.syncManager.FulfilledRequest(peerAddr, requestType)
}

// cycleDataValidWithBlockchain checks if masternode cycle data is valid using blockchain lookup
// This implements the exact legacy C++ CMasternode::cycleDataValid() logic:
//
// Legacy C++ (masternode.cpp:276-285):
//
//	bool CMasternode::cycleDataValid() {
//	    CBlock block;
//	    CBlockIndex* pblockindex = mapBlockIndex[prevCycleLastPaymentHash];
//	    if (!ReadBlockFromDisk(block, pblockindex))
//	        return false;
//	    if (abs(block.GetBlockTime() - prevCycleLastPaymentTime) > 600)
//	        return false;
//	    return true;
//	}
//
// The check validates that prevCycleLastPaymentTime is consistent with the block
// at prevCycleLastPaymentHash. This catches corrupted or stale cycle data.
//
// CRITICAL: This is NOT a freshness check against current time!
// It validates internal consistency of the stored cycle data.
//
// Returns false (invalid) if:
// - prevCycleLastPaymentTime is 0 (never set)
// - prevCycleLastPaymentHash is zero (never set)
// - Block lookup fails (block not found or blockchain unavailable)
// - |block.Timestamp - prevCycleLastPaymentTime| > 600 seconds
//
// The mn parameter must have its mutex held by the caller (RLock or Lock)
func (m *Manager) cycleDataValidWithBlockchain(mn *Masternode) bool {
	// If no cycle data set, it's invalid
	if mn.PrevCycleLastPaymentTime == 0 {
		return false
	}

	// If hash is zero, cycle was never properly initialized
	var zeroHash types.Hash
	if mn.PrevCycleLastPaymentHash == zeroHash {
		return false
	}

	// If no blockchain, return false to match legacy behavior
	// Legacy: ReadBlockFromDisk failing = return false
	// This forces cycle data reset when blockchain is unavailable
	if m.blockchain == nil {
		return false
	}

	// Look up the block by hash - this is the core of the legacy check
	block, err := m.blockchain.GetBlock(mn.PrevCycleLastPaymentHash)
	if err != nil || block == nil {
		// Block not found - same as legacy ReadBlockFromDisk failing
		return false
	}

	// Compare block timestamp with stored prevCycleLastPaymentTime
	// Legacy: abs(block.GetBlockTime() - prevCycleLastPaymentTime) > 600
	blockTime := int64(block.Header.Timestamp)
	timeDiff := blockTime - mn.PrevCycleLastPaymentTime
	if timeDiff < 0 {
		timeDiff = -timeDiff // abs
	}

	return timeDiff <= 600
}

// validateCollateralWithSpork validates collateral amount respecting SPORK_TWINS_01_ENABLE_MASTERNODE_TIERS
// When spork is disabled, only Bronze (1M TWINS) is accepted (single-tier mode)
// When spork is active, all tier collaterals are valid
func (m *Manager) validateCollateralWithSpork(collateral int64) (MasternodeTier, error) {
	// CRITICAL: Must match legacy spork ID from spork.h (20190001, NOT 10020)
	const SPORK_TWINS_01_ENABLE_MASTERNODE_TIERS = int32(20190001)

	// Check if tiers are enabled via spork
	// CRITICAL: Default to DISABLED (false) if no spork manager - matches legacy behavior
	// Tiers are OFF by default and only activate when spork timestamp <= current time
	tiersEnabled := false // Default to disabled - matches legacy single-tier mode
	if m.sporkManager != nil {
		tiersEnabled = m.sporkManager.IsActive(SPORK_TWINS_01_ENABLE_MASTERNODE_TIERS)
	}

	if !tiersEnabled {
		// Single-tier mode - only Bronze collateral is valid
		if collateral == TierBronzeCollateral {
			return Bronze, nil
		}
		return Bronze, fmt.Errorf("masternode tiers disabled: only %d TWINS collateral accepted (got %d)",
			TierBronzeCollateral/CoinUnit, collateral/CoinUnit)
	}

	// Multi-tier mode - validate using standard function
	return GetTierFromCollateral(collateral)
}

// AddMasternode adds a new masternode to the manager
func (m *Manager) AddMasternode(mn *Masternode) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if masternode already exists
	if _, exists := m.masternodes[mn.OutPoint]; exists {
		return fmt.Errorf("masternode already exists")
	}

	// Validate masternode
	if err := m.validateMasternode(mn); err != nil {
		return fmt.Errorf("invalid masternode: %w", err)
	}

	// Add to storage
	m.masternodes[mn.OutPoint] = mn
	m.addressIndex[mn.Addr.String()] = mn
	if mn.PubKey != nil {
		m.pubkeyIndex[mn.PubKey.Hex()] = mn
	}

	// Add to payment queue if active
	if mn.IsActive() {
		m.paymentQueue.mu.Lock()
		m.paymentQueue.queue = append(m.paymentQueue.queue, mn)
		m.paymentQueue.mu.Unlock()
	}

	m.logger.WithFields(logrus.Fields{
		"address": mn.GetPayee(),
		"tier":    mn.Tier.String(),
		"status":  mn.Status.String(),
	}).Info("Masternode added")

	return nil
}

// RemoveMasternode removes a masternode from the manager
func (m *Manager) RemoveMasternode(outpoint types.Outpoint) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	mn, exists := m.masternodes[outpoint]
	if !exists {
		return ErrMasternodeNotFound
	}

	// Remove from indexes
	delete(m.masternodes, outpoint)
	delete(m.addressIndex, mn.Addr.String())
	if mn.PubKey != nil {
		delete(m.pubkeyIndex, mn.PubKey.Hex())
	}

	// Remove from payment queue
	m.paymentQueue.mu.Lock()
	for i, qmn := range m.paymentQueue.queue {
		if qmn.OutPoint == outpoint {
			m.paymentQueue.queue = append(m.paymentQueue.queue[:i], m.paymentQueue.queue[i+1:]...)
			break
		}
	}
	m.paymentQueue.mu.Unlock()

	m.logger.WithField("outpoint", outpoint.String()).Info("Masternode removed")
	return nil
}

// GetMasternode returns a masternode by outpoint
func (m *Manager) GetMasternode(outpoint types.Outpoint) (*Masternode, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	mn, exists := m.masternodes[outpoint]
	if !exists {
		return nil, ErrMasternodeNotFound
	}

	return mn, nil
}

// GetMasternodes returns all masternodes
func (m *Manager) GetMasternodes() map[types.Outpoint]*Masternode {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return a copy to avoid concurrent access issues
	copy := make(map[types.Outpoint]*Masternode)
	for k, v := range m.masternodes {
		copy[k] = v
	}

	return copy
}

// GetMasternodeConfFile returns the masternode configuration file
// This is used by wallet to check if UTXOs are masternode collateral
// Returns interface{} to avoid circular import (actual type is *MasternodeConfFile)
func (m *Manager) GetMasternodeConfFile() interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.confFile
}

// IsCollateralOutpoint checks if the given outpoint is a masternode collateral from masternode.conf.
// Returns true if the outpoint matches any configured masternode's collateral transaction.
// Used by wallet to filter collateral UTXOs from spending operations.
// Legacy: Equivalent to CMasternodeConfig::HasCollateral() in C++
func (m *Manager) IsCollateralOutpoint(outpoint types.Outpoint) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.confFile == nil {
		return false
	}

	for _, entry := range m.confFile.GetEntries() {
		if entry == nil {
			continue
		}
		entryOutpoint := entry.GetOutpoint()
		// Skip zero-value outpoints (malformed entries)
		if entryOutpoint.Hash.IsZero() {
			continue
		}
		if entryOutpoint == outpoint {
			return true
		}
	}
	return false
}

// GetMasternodesByTier returns all masternodes of a specific tier
func (m *Manager) GetMasternodesByTier(tier MasternodeTier) []*Masternode {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*Masternode, 0)
	for _, mn := range m.masternodes {
		if mn.Tier == tier && mn.IsActive() {
			result = append(result, mn)
		}
	}

	return result
}

// UpdateMasternode updates an existing masternode
func (m *Manager) UpdateMasternode(mn *Masternode) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, exists := m.masternodes[mn.OutPoint]
	if !exists {
		return ErrMasternodeNotFound
	}

	// Update fields
	existing.mu.Lock()
	existing.LastPing = mn.LastPing
	existing.LastSeen = mn.LastSeen
	existing.Status = mn.Status
	existing.Score = mn.Score
	existing.SentinelPing = mn.SentinelPing
	existing.SentinelVersion = mn.SentinelVersion
	existing.mu.Unlock()

	return nil
}

// UpdateFromBroadcast handles full state refresh from a rebroadcast message.
// Unlike UpdateMasternode which only updates timestamps/status, this method
// copies ALL fields that can legitimately change on a rebroadcast:
// - Addr (IP/port can change when masternode relocates)
// - PubKey/PubKeyCollateral (key rotation)
// - Protocol (software upgrade)
// - SigTime/Signature (new broadcast signature)
// - LastPingMessage (updated ping for relay)
//
// This matches legacy CMasternode::UpdateFromNewBroadcast behavior.
// Lock ordering: Manager lock (m.mu) must be held before masternode lock (existing.mu).
func (m *Manager) UpdateFromBroadcast(mn *Masternode) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, exists := m.masternodes[mn.OutPoint]
	if !exists {
		return ErrMasternodeNotFound
	}

	existing.mu.Lock()
	defer existing.mu.Unlock()

	// Track if we're recovering from spent/expired status
	wasDisabled := existing.Status == StatusVinSpent || existing.Status == StatusExpired

	// Capture old values for logging before updating (fixes logging bug)
	var oldAddr, oldPubkeyHex string
	var oldProtocol int32

	// Update address and fix addressIndex if changed
	if existing.Addr != nil && mn.Addr != nil && existing.Addr.String() != mn.Addr.String() {
		oldAddr = existing.Addr.String()
		// Validate index consistency before delete (detect corruption)
		if _, inIndex := m.addressIndex[oldAddr]; !inIndex {
			m.logger.WithField("address", oldAddr).Warn("Address index inconsistency: old address not in index")
		}
		delete(m.addressIndex, oldAddr)
		existing.Addr = mn.Addr
		m.addressIndex[mn.Addr.String()] = existing
	} else if existing.Addr == nil && mn.Addr != nil {
		existing.Addr = mn.Addr
		m.addressIndex[mn.Addr.String()] = existing
	}

	// Update pubkey and fix pubkeyIndex if changed
	if existing.PubKey != nil && mn.PubKey != nil && existing.PubKey.Hex() != mn.PubKey.Hex() {
		oldPubkeyHex = existing.PubKey.Hex()
		// Validate index consistency before delete (detect corruption)
		if _, inIndex := m.pubkeyIndex[oldPubkeyHex]; !inIndex {
			m.logger.WithField("pubkey", oldPubkeyHex).Warn("Pubkey index inconsistency: old pubkey not in index")
		}
		delete(m.pubkeyIndex, oldPubkeyHex)
		existing.PubKey = mn.PubKey
		m.pubkeyIndex[mn.PubKey.Hex()] = existing
	} else if existing.PubKey == nil && mn.PubKey != nil {
		existing.PubKey = mn.PubKey
		m.pubkeyIndex[mn.PubKey.Hex()] = existing
	}

	// Update collateral pubkey (rare but allowed)
	if mn.PubKeyCollateral != nil {
		existing.PubKeyCollateral = mn.PubKeyCollateral
	}

	// Update protocol version (important for payment eligibility)
	oldProtocol = existing.Protocol
	existing.Protocol = mn.Protocol

	// Update signature fields (cryptographic proof of fresh broadcast)
	existing.SigTime = mn.SigTime
	existing.Signature = mn.Signature

	// Update ping message (needed for P2P relay)
	// Use embedded ping's SigTime for LastSeen/LastPing when available
	// (legacy calls lastPing.CheckAndUpdate() which uses the ping's own sigTime)
	existing.LastPingMessage = mn.LastPingMessage
	if mn.LastPingMessage != nil && mn.LastPingMessage.SigTime > 0 {
		pingTime := time.Unix(mn.LastPingMessage.SigTime, 0)
		existing.LastPing = pingTime
		existing.LastSeen = pingTime
	} else {
		existing.LastPing = mn.LastPing
		existing.LastSeen = mn.LastSeen
	}

	// Always update ActiveSince from the new broadcast's sigTime
	// C++ UpdateFromNewBroadcast updates sigTime on every broadcast (masternode.cpp:163)
	// and activetime = lastPing.sigTime - sigTime. If we don't update ActiveSince,
	// masternodes that re-broadcast show stale active time from their first-ever broadcast.
	if !mn.ActiveSince.IsZero() {
		existing.ActiveSince = mn.ActiveSince
	}

	// Update status from the new broadcast
	existing.Status = mn.Status

	// Update tier and collateral (critical for correct payment selection)
	existing.Tier = mn.Tier
	existing.Collateral = mn.Collateral

	// If recovering from spent/expired, reset cycle tracking and add to payment queue
	// This matches legacy CheckInputsAndAdd behavior that removes and re-adds
	if wasDisabled && (mn.Status == StatusEnabled || mn.Status == StatusPreEnabled) {
		existing.PrevCycleLastPaymentTime = 0
		existing.PrevCycleLastPaymentHash = types.ZeroHash
		existing.WinsThisCycle = 0

		// Add to payment queue on recovery (matches AddMasternode behavior)
		// Note: Check status directly since we already hold existing.mu.Lock()
		// Safe from duplicates: m.mu.Lock() prevents concurrent UpdateFromBroadcast
		// calls for the same masternode, so no race on queue membership check.
		if existing.Status == StatusEnabled || existing.Status == StatusPreEnabled {
			m.paymentQueue.mu.Lock()
			inQueue := false
			for _, qmn := range m.paymentQueue.queue {
				if qmn.OutPoint == existing.OutPoint {
					inQueue = true
					break
				}
			}
			if !inQueue {
				m.paymentQueue.queue = append(m.paymentQueue.queue, existing)
			}
			m.paymentQueue.mu.Unlock()
		}

		m.logger.WithFields(logrus.Fields{
			"outpoint":   mn.OutPoint.String(),
			"old_status": "VinSpent/Expired",
			"new_status": mn.Status.String(),
		}).Debug("Masternode recovered from disabled state, cycle tracking reset")
	}

	// Log changes outside critical path (using captured values)
	if oldAddr != "" {
		m.logger.WithFields(logrus.Fields{
			"outpoint":    mn.OutPoint.String(),
			"old_address": oldAddr,
			"new_address": mn.Addr.String(),
		}).Debug("Masternode address updated from broadcast")
	}
	if oldPubkeyHex != "" {
		m.logger.WithFields(logrus.Fields{
			"outpoint":   mn.OutPoint.String(),
			"old_pubkey": oldPubkeyHex,
			"new_pubkey": mn.PubKey.Hex(),
		}).Debug("Masternode pubkey updated from broadcast")
	}
	if oldProtocol != mn.Protocol {
		m.logger.WithFields(logrus.Fields{
			"outpoint":     mn.OutPoint.String(),
			"old_protocol": oldProtocol,
			"new_protocol": mn.Protocol,
		}).Debug("Masternode protocol changed from broadcast")
	}

	return nil
}

// ValidateMasternode validates a masternode
func (m *Manager) ValidateMasternode(mn *Masternode) error {
	return m.validateMasternode(mn)
}

// validateMasternode performs validation (internal, must be called with lock)
func (m *Manager) validateMasternode(mn *Masternode) error {
	// Check collateral amount matches tier
	expectedCollateral := mn.Tier.Collateral()
	if mn.Collateral != expectedCollateral {
		return fmt.Errorf("invalid collateral for tier %s: got %d, expected %d",
			mn.Tier.String(), mn.Collateral, expectedCollateral)
	}

	// Check protocol version
	// CRITICAL FIX: Use spork-aware minimum protocol version instead of static config
	// Legacy: masternode.cpp:550 uses masternodePayments.GetMinMasternodePaymentsProto()
	// This ensures consistency with payment selection and counting logic
	// NOTE: Using Locked version since validateMasternode is called from AddMasternode which holds m.mu
	minProto := m.getMinMasternodePaymentsProtoLocked()
	if mn.Protocol < minProto {
		return fmt.Errorf("%w: %d < %d", ErrInvalidProtocol, mn.Protocol, minProto)
	}

	// Check address is valid
	if mn.Addr == nil {
		return fmt.Errorf("missing address")
	}

	// Check public key
	if mn.PubKey == nil {
		return fmt.Errorf("missing public key")
	}

	return nil
}

// GetMasternodeCount returns the total number of masternodes
func (m *Manager) GetMasternodeCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.masternodes)
}

// GetMasternodeCountByTier returns the number of masternodes for a tier
func (m *Manager) GetMasternodeCountByTier(tier MasternodeTier) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, mn := range m.masternodes {
		if mn.Tier == tier && mn.IsActive() {
			count++
		}
	}
	return count
}

// CountEnabled counts enabled masternodes with protocol >= protocolVersion
// Implements legacy CMasternodeMan::CountEnabled(int protocolVersion) from masternodeman.cpp:378-390
// If protocolVersion is -1, use GetMinMasternodePaymentsProto() equivalent
// CRITICAL: Legacy calls mn.Check() before each status check to refresh status
func (m *Manager) CountEnabled(protocolVersion int32) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Legacy: protocolVersion = protocolVersion == -1 ? GetMinMasternodePaymentsProto() : protocolVersion
	// CRITICAL FIX: Use spork-aware minimum when -1 is passed (matches legacy exactly)
	// NOTE: Must use Locked variant — caller holds m.mu.RLock via defer above.
	minProto := protocolVersion
	if minProto == -1 {
		minProto = m.getMinMasternodePaymentsProtoLocked()
	}

	currentTime := time.Now()
	count := 0
	for _, mn := range m.masternodes {
		// Legacy: mn.Check() is called before status check (masternodeman.cpp:380-422)
		// This refreshes the masternode status based on ping times and UTXO state
		mn.UpdateStatus(currentTime, time.Duration(ExpirationSeconds)*time.Second)

		// Legacy: if (mn.protocolVersion < protocolVersion || !mn.IsEnabled()) continue
		if mn.Protocol < minProto || !mn.IsActive() {
			continue
		}
		count++
	}
	return count
}

// getScheduledPaymentsKeepBlocks returns the number of blocks to keep in scheduledPayments
// Legacy: int nLimit = std::max(int(mnodeman.size() * 1.25), 1000)
// See legacy/src/masternode-payments.cpp:700-702
func (m *Manager) getScheduledPaymentsKeepBlocks() uint32 {
	mnCount := len(m.masternodes)
	// Calculate 125% of masternode count (1.25 * mnCount = mnCount + mnCount/4)
	limit := mnCount + mnCount/4
	if limit < 1000 {
		limit = 1000
	}
	return uint32(limit)
}

// GetStableCount returns the count of "stable" masternodes - those older than MN_WINNER_MINIMUM_AGE (8000 seconds).
// Implements legacy CMasternodeMan::stable_size() from masternodeman.cpp:351-376
//
// This method is used when SPORK_8 (MASTERNODE_PAYMENT_ENFORCEMENT) is active to get
// a stable count of masternodes that excludes newly activated nodes. This prevents
// payment queue manipulation by rapidly joining/leaving the network.
//
// The stable_size concept:
//   - Only counts masternodes with sigTime older than 8000 seconds
//   - Only counts masternodes with protocol >= ActiveProtocol (minProto)
//   - Only counts enabled masternodes (calls Check() first)
//   - Age check only applied when SPORK_8 is active (like legacy)
//
// Usage: When SPORK_8 is active, use GetStableCount() + drift instead of GetActiveCount() + drift
// for payment calculation in IsTransactionValid().
func (m *Manager) GetStableCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Get minimum protocol version
	// NOTE: Must use Locked variant — caller holds m.mu.RLock via defer above.
	minProto := m.getMinMasternodePaymentsProtoLocked()

	// Check SPORK_8 for minimum age enforcement
	// Legacy: stable_size() checks IsSporkActive(SPORK_8_MASTERNODE_PAYMENT_ENFORCEMENT)
	minAgeEnforced := false
	if m.sporkManager != nil && m.sporkManager.IsActive(int32(10007)) { // SPORK_8_MASTERNODE_PAYMENT_ENFORCEMENT
		minAgeEnforced = true
	}

	currentTime := adjustedtime.GetAdjustedTime()
	count := 0

	for _, mn := range m.masternodes {
		// Legacy: masternodeman.cpp:359-361 - skip obsolete protocol versions
		if mn.Protocol < minProto {
			continue
		}

		// Legacy: masternodeman.cpp:362-367 - skip masternodes younger than 8000 sec when SPORK_8 active
		if minAgeEnforced {
			masternodeAge := currentTime.Unix() - mn.SigTime
			if masternodeAge < MasternodeWinnerMinAge {
				continue // Skip masternodes younger than 8000 sec
			}
		}

		// Legacy: masternodeman.cpp:368-370 - call Check() and skip non-enabled
		mn.UpdateStatus(currentTime, time.Duration(ExpirationSeconds)*time.Second)
		if !mn.IsActive() {
			continue
		}

		count++
	}

	return count
}

// IsMasternodeActive checks if a masternode is active
func (m *Manager) IsMasternodeActive(outpoint types.Outpoint) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	mn, exists := m.masternodes[outpoint]
	if !exists {
		return false
	}

	return mn.IsActive()
}

// GetNextPayee returns the next masternode to receive payment
// Now delegates to GetNextMasternodeInQueueForPayment for legacy algorithm
func (m *Manager) GetNextPayee() (*Masternode, error) {
	// Get current blockchain height
	var blockHeight uint32
	if m.blockchain != nil {
		if h, err := m.blockchain.GetBestHeight(); err == nil {
			blockHeight = h
		}
	}

	// Use legacy payment selection algorithm
	mn, _ := m.GetNextMasternodeInQueueForPayment(blockHeight, true)
	if mn == nil {
		return nil, fmt.Errorf("no masternodes eligible for payment")
	}
	return mn, nil
}

// GetNextMasternodeInQueueForPayment deterministically selects the best masternode for payment
// This MUST match the legacy C++ algorithm EXACTLY from masternodeman.cpp:562-631
//
// Legacy Algorithm:
// 1. Filter: IsEnabled() && protocol >= GetMinMasternodePaymentsProto()
// 2. Filter: !IsScheduled(mn, blockHeight) UNLESS multi-tier masternode
// 3. Filter: sigTime + (millionsLocked * 2.6 * 60) <= GetAdjustedTime()
// 4. Filter: GetMasternodeInputAge() >= millionsLocked
// 5. Build vector of (SecondsSincePayment, outpoint) pairs
// 6. Sort by SecondsSincePayment DESCENDING (oldest paid first)
// 7. Take TOP 1/10 oldest only
// 8. From 1/10: pick the one with best CalculateScore(1, blockHeight - 100)
func (m *Manager) GetNextMasternodeInQueueForPayment(blockHeight uint32, filterSigTime bool) (*Masternode, int) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// LEGACY COMPATIBILITY FIX: Use GetAdjustedTime() (network-adjusted time) for all time comparisons
	// This matches legacy C++ behavior exactly - all nodes use network-synchronized time
	// Previous implementation used block timestamp which caused divergence when block timestamps
	// drifted from network time (up to ±2 hours allowed by protocol)
	currentTime := adjustedtime.GetAdjustedUnix()

	// CRITICAL FIX: Create UTXO checker for status freshness
	// Legacy C++ calls mn.Check() inside GetNextMasternodeInQueueForPayment to recompute
	// enabled/expired/spent status on every pass. This ensures stale statuses don't keep
	// expired nodes eligible when background updateLoop is delayed or stalled.
	var utxoChecker UTXOChecker
	if m.blockchain != nil {
		utxoChecker = &blockchainUTXOChecker{bc: m.blockchain}
	}
	currentTimeT := time.Unix(currentTime, 0)

	// Get multi-tier enabled state once for efficiency
	multiTierEnabled := m.isMultiTierEnabledLocked()

	// CRITICAL FIX: Call countEnabledLocked with UTXO checker to refresh statuses
	// Legacy CountEnabled() calls mn.Check() for each masternode before counting
	nMnCount := m.countEnabledLocked(currentTimeT, utxoChecker, multiTierEnabled)

	// Build vector of eligible masternodes with their LastPaid time
	type mnLastPaid struct {
		secondsSincePaid int64
		mn               *Masternode
	}
	var eligible []mnLastPaid

	// Note: multiTierEnabled already obtained above for countEnabledLocked

	for _, mn := range m.masternodes {
		// CRITICAL FIX: Call Check() equivalent to refresh status before eligibility check
		// Legacy: mn.Check() is called inside the loop (masternodeman.cpp:580)
		// This ensures expired/spent nodes are filtered even if updateLoop hasn't run
		mn.UpdateStatusWithUTXO(currentTimeT, m.config.ExpireTime, utxoChecker, multiTierEnabled)

		// Compute payee script BEFORE acquiring RLock to avoid recursive RLock deadlock.
		// GetPayeeScript() internally acquires mn.mu.RLock(); calling it while already
		// holding RLock deadlocks when a concurrent writer (CountEnabled → UpdateStatus)
		// has a pending WLock on the same MN.
		payeeScript := mn.GetPayeeScript()

		mn.mu.RLock()

		// 1. Must be enabled
		if mn.Status != StatusEnabled {
			mn.mu.RUnlock()
			continue
		}

		// 2. Check protocol version
		if mn.Protocol < m.getMinMasternodePaymentsProtoLocked() {
			mn.mu.RUnlock()
			continue
		}

		// 3. Check scheduling (skip single-tier if already scheduled)
		// Multi-tier masternodes can be scheduled multiple times
		// Use actual tier weight (1/5/20/100) - matches C++ GetMasternodeTierRounds
		tierRounds := m.getEffectiveTierWeight(mn.Tier)
		if m.isScheduledLocked(mn, blockHeight, payeeScript) && tierRounds == 1 {
			mn.mu.RUnlock()
			continue
		}

		// 4. Calculate millionsLocked at launch (masternodes younger than this one don't count)
		// CRITICAL FIX: Pass currentTime and utxoChecker to refresh status inside countMillionsLockedLaunchLocked
		// CRITICAL: Release mn.mu before calling countMillionsLockedLaunchLocked to avoid deadlock
		// countMillionsLockedLaunchLocked iterates ALL masternodes and calls UpdateStatusWithUTXO which needs Lock
		mnSigTime := mn.SigTime
		mn.mu.RUnlock()
		millionsLocked := m.countMillionsLockedLaunchLocked(mnSigTime, currentTimeT, utxoChecker)
		mn.mu.RLock()

		// Re-check status after releasing lock (might have changed)
		if mn.Status != StatusEnabled {
			mn.mu.RUnlock()
			continue
		}

		// 5. Filter by sigTime age: sigTime + (millionsLocked * 2.6 * 60) <= currentTime
		// This ensures new masternodes wait for a full cycle before becoming eligible
		if filterSigTime {
			minAgeSeconds := int64(float64(millionsLocked) * 2.6 * 60)
			if mn.SigTime+minAgeSeconds > currentTime {
				mn.mu.RUnlock()
				continue
			}
		}

		// 6. Check input age (confirmations) >= millionsLocked
		inputAge := m.getInputAgeLocked(mn)
		if inputAge < int64(millionsLocked) {
			mn.mu.RUnlock()
			continue
		}

		// 7. Update cycle data if invalid (legacy behavior)
		// Legacy C++ (masternodeman.cpp:594-599):
		//   if (!mn.cycleDataValid()) {
		//       mn.prevCycleLastPaymentHash = chainActive.Tip()->GetBlockHash();
		//       mn.prevCycleLastPaymentTime = GetAdjustedTime();
		//       mn.wins = 0;
		//   }
		//
		// NOTE: We use double-check pattern to avoid TOCTOU race condition:
		// 1. Check with RLock (optimistic path - most cases cycle data is valid)
		// 2. If invalid, release RLock, acquire Lock, re-check, then update
		// This ensures we don't miss updates from concurrent goroutines
		if !m.cycleDataValidWithBlockchain(mn) {
			mn.mu.RUnlock()
			mn.mu.Lock()
			// Double-check after acquiring write lock (state may have changed)
			if !m.cycleDataValidWithBlockchain(mn) {
				// Reset cycle data with current chain tip (legacy: chainActive.Tip()->GetBlockHash())
				if m.blockchain != nil {
					if h, err := m.blockchain.GetBestHeight(); err == nil {
						if block, err := m.blockchain.GetBlockByHeight(h); err == nil && block != nil {
							mn.PrevCycleLastPaymentHash = block.Hash()
						}
					}
				}
				// Legacy uses GetAdjustedTime() for cycle reset tracking
				// Use GetAdjustedTimeUnix() for consistency with other methods (AddWin, SecondsSincePayment)
				mn.PrevCycleLastPaymentTime = consensus.GetAdjustedTimeUnix()
				mn.WinsThisCycle = 0
			}
			mn.mu.Unlock()
			mn.mu.RLock()
		}

		// Release lock before calling SecondsSincePayment which may need write lock
		mn.mu.RUnlock()

		// Add to eligible list
		// SecondsSincePayment now uses GetAdjustedTime() internally (legacy compatibility)
		// NOTE: SecondsSincePayment() acquires its own lock (may need Lock not RLock due to mutation)
		secondsSincePaid := mn.SecondsSincePayment()
		eligible = append(eligible, mnLastPaid{
			secondsSincePaid: secondsSincePaid,
			mn:               mn,
		})
	}

	nCount := len(eligible)

	// If filtering by sigTime and we have < 1/3 of network, try again without filter
	// This prevents penalizing nodes during network upgrades
	if filterSigTime && nCount < nMnCount/3 {
		return m.GetNextMasternodeInQueueForPayment(blockHeight, false)
	}

	if nCount == 0 {
		return nil, 0
	}

	// Sort by SecondsSincePayment DESCENDING (oldest paid first = highest seconds)
	sort.Slice(eligible, func(i, j int) bool {
		return eligible[i].secondsSincePaid > eligible[j].secondsSincePaid
	})

	// Look at top 1/10 of oldest (by last payment)
	// Then pick the one with best score from that subset
	nTenthNetwork := nMnCount / 10
	if nTenthNetwork < 1 {
		nTenthNetwork = 1
	}

	// Get block hash at height - 100 for score calculation
	// Legacy: CalculateScore(1, nBlockHeight - 100) - no guards
	// If height < 100, underflow causes GetBlockHash to fail → returns 0 hash → score = 0
	// We replicate this by letting scoreBlockHash remain zero when block not found
	var scoreBlockHash types.Hash
	if m.blockchain != nil && blockHeight >= ScoreBlockDepth {
		scoreHeight := blockHeight - ScoreBlockDepth
		if block, err := m.blockchain.GetBlockByHeight(scoreHeight); err == nil && block != nil {
			scoreBlockHash = block.Hash()
		}
	}
	// For heights < ScoreBlockDepth (100), scoreBlockHash stays zero, which matches
	// legacy behavior where underflow causes GetBlockHash to fail

	var bestMN *Masternode
	var bestScore types.Hash

	for i := 0; i < len(eligible) && i < nTenthNetwork; i++ {
		mn := eligible[i].mn
		score := mn.CalculateScore(scoreBlockHash)
		// Compare full 32-byte hash like legacy: if (n > nHigh) { nHigh = n; pBestMasternode = pmn; }
		// CRITICAL: Use CompareTo which compares from most significant byte first (like C++ uint256)
		// NOT bytes.Compare which compares from least significant byte first
		if score.CompareTo(bestScore) > 0 {
			bestScore = score
			bestMN = mn
		}
	}

	return bestMN, nCount
}

// countEnabledLocked returns count of enabled masternodes that meet protocol requirements (must hold m.mu)
// CRITICAL: Must filter by GetMinMasternodePaymentsProto to match legacy CountEnabled()
// Legacy: masternodeman.cpp:378-423 - filters by protocol before counting for top-10% window
// CRITICAL FIX: Now calls UpdateStatusWithUTXO for each masternode to match legacy mn.Check() behavior
// Legacy CountEnabled() calls mn.Check() inside the loop (masternodeman.cpp:384)
func (m *Manager) countEnabledLocked(currentTime time.Time, utxoChecker UTXOChecker, multiTierEnabled bool) int {
	count := 0
	minProto := m.getMinMasternodePaymentsProtoLocked()
	for _, mn := range m.masternodes {
		// CRITICAL FIX: Call Check() equivalent to refresh status before counting
		// Legacy: mn.Check() is called inside CountEnabled loop (masternodeman.cpp:384)
		// This ensures expired/spent nodes are excluded even if updateLoop hasn't run
		mn.UpdateStatusWithUTXO(currentTime, m.config.ExpireTime, utxoChecker, multiTierEnabled)

		mn.mu.RLock()
		// CRITICAL FIX: Must also filter by protocol version (legacy behavior)
		if mn.Status == StatusEnabled && mn.Protocol >= minProto {
			count++
		}
		mn.mu.RUnlock()
	}
	return count
}

// countMillionsLockedLaunchLocked counts weighted millions locked for masternodes
// older than the given sigTime (must hold m.mu)
// Legacy: CountMillionsLockedLaunch() from masternodeman.cpp:409-422
// Masternodes wait only for older masternodes, not newer ones
// CRITICAL FIX: Now calls UpdateStatusWithUTXO for each masternode to match legacy mn.Check() behavior
// Legacy calls mn.Check() inside CountMillionsLockedLaunch loop to refresh status before counting
func (m *Manager) countMillionsLockedLaunchLocked(sigTime int64, currentTime time.Time, utxoChecker UTXOChecker) int {
	count := 0
	// Use Locked version since caller holds m.mu
	minProto := m.getMinMasternodePaymentsProtoLocked()

	// Get spork state once before loop for efficiency (use locked version since caller holds m.mu)
	multiTierEnabled := m.isMultiTierEnabledLocked()

	for _, mn := range m.masternodes {
		// CRITICAL FIX: Call Check() equivalent to refresh status before counting
		// Legacy: mn.Check() is called inside CountMillionsLockedLaunch loop (masternodeman.cpp:415)
		// This ensures expired/spent nodes are excluded even if updateLoop hasn't run
		mn.UpdateStatusWithUTXO(currentTime, m.config.ExpireTime, utxoChecker, multiTierEnabled)

		mn.mu.RLock()
		// Only count enabled masternodes with correct protocol that are OLDER
		if mn.Status == StatusEnabled &&
			mn.Protocol >= minProto &&
			mn.SigTime < sigTime {
			// Add actual tier weight (1/5/20/100) - matches C++ GetMasternodeTierRounds
			// Higher-tier MNs with spork OFF are already filtered out by
			// UpdateStatusWithUTXO above (marks them VIN_SPENT)
			count += m.getEffectiveTierWeightLocked(mn.Tier)
		}
		mn.mu.RUnlock()
	}
	return count
}

// CountMillionsLocked is a public wrapper around countMillionsLockedLaunchLocked
// that handles locking. Used by tests and external callers.
// Returns weighted count of enabled masternodes older than sigTime.
func (m *Manager) CountMillionsLocked(sigTime int64, currentTime time.Time, utxoChecker UTXOChecker) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.countMillionsLockedLaunchLocked(sigTime, currentTime, utxoChecker)
}

// MasternodeWinnerMinAge is the minimum age in seconds a masternode must have
// before it can participate in winner voting. Matches legacy MN_WINNER_MINIMUM_AGE.
// Legacy: masternodeman.cpp:16 - #define MN_WINNER_MINIMUM_AGE 8000
const MasternodeWinnerMinAge = 8000

// GetMasternodeRank returns the rank of a masternode at a specific block height.
// Returns -1 if masternode not found or not eligible, 1 for highest ranked.
// Legacy: CMasternodeMan::GetMasternodeRank from masternodeman.cpp:689-734
//
// The rank is determined by:
// 1. Getting block hash at blockHeight (for score calculation)
// 2. Filtering masternodes by protocol version and enabled status
// 3. Optionally enforcing minimum age when SPORK_8 is active
// 4. Sorting by score descending (highest score = rank 1)
//
// Parameters:
//   - outpoint: The masternode's collateral outpoint
//   - blockHeight: Block height to calculate rank for
//   - minProtocol: Minimum protocol version required
//   - fOnlyActive: If true, only check enabled masternodes (calls Check())
func (m *Manager) GetMasternodeRank(outpoint types.Outpoint, blockHeight uint32, minProtocol int32, fOnlyActive bool) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.getMasternodeRankLocked(outpoint, blockHeight, minProtocol, fOnlyActive)
}

// getMasternodeRankLocked is the internal implementation (caller must hold m.mu)
// LEGACY COMPATIBILITY: Calls mn.Check() (UpdateStatusWithUTXO) inline when fOnlyActive=true
func (m *Manager) getMasternodeRankLocked(outpoint types.Outpoint, blockHeight uint32, minProtocol int32, fOnlyActive bool) int {
	// Get block hash for score calculation
	var blockHash types.Hash
	if m.blockchain != nil {
		var err error
		blockHash, err = m.blockchain.GetBlockHash(blockHeight)
		if err != nil {
			m.logger.WithError(err).WithField("height", blockHeight).Debug("GetMasternodeRank: cannot get block hash")
			return -1
		}
	} else {
		m.logger.Debug("GetMasternodeRank: no blockchain interface, cannot calculate rank")
		return -1
	}

	// Build sorted score list
	// Legacy uses pair<int64_t, CTxIn> and sorts descending by score
	type scoreEntry struct {
		score    types.Hash
		outpoint types.Outpoint
	}
	var scores []scoreEntry

	// Check SPORK_8 for minimum age enforcement
	// Legacy: masternodeman.cpp:706-712
	minAgeEnforced := false
	if m.sporkManager != nil && m.sporkManager.IsActive(int32(10007)) { // SPORK_8_MASTERNODE_PAYMENT_ENFORCEMENT
		minAgeEnforced = true
	}
	currentTime := adjustedtime.GetAdjustedTime()

	// LEGACY COMPATIBILITY FIX: Create UTXO checker for inline mn.Check() calls
	// Legacy C++ at masternodeman.cpp:713-716 calls mn.Check() inline before ranking
	// This ensures we validate CURRENT UTXO state, not stale cached status
	var utxoChecker UTXOChecker
	if fOnlyActive && m.blockchain != nil {
		utxoChecker = &blockchainUTXOChecker{bc: m.blockchain}
	}
	// NOTE: Must use Locked variant — caller holds m.mu.RLock via GetMasternodeRank.
	multiTierEnabled := m.isMultiTierEnabledLocked()

	for _, mn := range m.masternodes {
		mn.mu.RLock()
		protocol := mn.Protocol
		sigTime := mn.SigTime
		mnOutpoint := mn.OutPoint
		mn.mu.RUnlock()

		// Skip obsolete protocol versions
		// Legacy: masternodeman.cpp:701-704
		if protocol < minProtocol {
			continue
		}

		// Skip masternodes younger than MN_WINNER_MINIMUM_AGE when SPORK_8 active
		// Legacy: masternodeman.cpp:706-712
		if minAgeEnforced {
			age := currentTime.Unix() - sigTime
			if age < MasternodeWinnerMinAge {
				continue
			}
		}

		// If fOnlyActive, skip non-enabled masternodes
		// LEGACY COMPATIBILITY FIX: Call mn.Check() (UpdateStatusWithUTXO) inline like legacy
		// Legacy: masternodeman.cpp:713-716 calls mn.Check() then checks IsEnabled()
		// This ensures we validate CURRENT UTXO state before ranking, not stale cached status
		if fOnlyActive {
			// Call UpdateStatusWithUTXO inline to match legacy mn.Check() behavior
			mn.UpdateStatusWithUTXO(currentTime, m.config.ExpireTime, utxoChecker, multiTierEnabled)
			mn.mu.RLock()
			status := mn.Status
			mn.mu.RUnlock()
			if status != StatusEnabled {
				continue
			}
		}

		// Calculate score
		score := mn.CalculateScore(blockHash)
		scores = append(scores, scoreEntry{score: score, outpoint: mnOutpoint})
	}

	// Sort descending by score (highest score = rank 1)
	// Legacy: sort(vecMasternodeScores.rbegin(), vecMasternodeScores.rend(), CompareScoreTxIn())
	// Legacy uses n.GetCompact(false) to convert uint256 to int64 for comparison
	sort.Slice(scores, func(i, j int) bool {
		// Use GetCompact() to match legacy comparison behavior
		return int64(scores[i].score.GetCompact()) > int64(scores[j].score.GetCompact())
	})

	// Find rank of target masternode
	for rank, entry := range scores {
		if entry.outpoint == outpoint {
			return rank + 1 // 1-based rank
		}
	}

	return -1 // Not found
}

// MasternodeRankEntry represents a masternode with its rank
type MasternodeRankEntry struct {
	Rank       int
	Masternode *Masternode
}

// GetMasternodeRanks returns all masternodes sorted by rank at a specific block height.
// Legacy: CMasternodeMan::GetMasternodeRanks from masternodeman.cpp:736-771
//
// Unlike GetMasternodeRank, this always calls Check() on each masternode and includes
// disabled masternodes with a placeholder score of 9999 (they appear at the end).
//
// Parameters:
//   - blockHeight: Block height to calculate ranks for
//   - minProtocol: Minimum protocol version required
func (m *Manager) GetMasternodeRanks(blockHeight uint32, minProtocol int32) []MasternodeRankEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Get block hash for score calculation
	var blockHash types.Hash
	if m.blockchain != nil {
		var err error
		blockHash, err = m.blockchain.GetBlockHash(blockHeight)
		if err != nil {
			m.logger.WithError(err).WithField("height", blockHeight).Debug("GetMasternodeRanks: cannot get block hash")
			return nil
		}
	} else {
		m.logger.Debug("GetMasternodeRanks: no blockchain interface, cannot calculate ranks")
		return nil
	}

	// Build sorted score list
	// Legacy uses pair<int64_t, CMasternode> for scores
	type scoreEntry struct {
		score      types.Hash
		isDisabled bool // Use placeholder score (9999) for disabled nodes
		mn         *Masternode
	}
	var scores []scoreEntry

	for _, mn := range m.masternodes {
		mn.mu.RLock()
		protocol := mn.Protocol
		status := mn.Status
		mn.mu.RUnlock()

		// Skip obsolete protocol versions
		// Legacy: masternodeman.cpp:749
		if protocol < minProtocol {
			continue
		}

		// Legacy calls mn.Check() here to refresh status
		// We rely on periodic updateLoop instead

		// Disabled masternodes get placeholder score
		// Legacy: masternodeman.cpp:751-754
		if status != StatusEnabled {
			scores = append(scores, scoreEntry{
				score:      types.Hash{}, // Will be sorted to end
				isDisabled: true,
				mn:         mn,
			})
			continue
		}

		// Calculate score for enabled masternodes
		score := mn.CalculateScore(blockHash)
		scores = append(scores, scoreEntry{
			score:      score,
			isDisabled: false,
			mn:         mn,
		})
	}

	// Sort descending by score, disabled nodes at end
	// Legacy: sort(vecMasternodeScores.rbegin(), vecMasternodeScores.rend(), CompareScoreMN())
	// Legacy uses n.GetCompact(false) to convert uint256 to int64 for comparison
	sort.Slice(scores, func(i, j int) bool {
		// Disabled nodes always sort to the end
		if scores[i].isDisabled && !scores[j].isDisabled {
			return false
		}
		if !scores[i].isDisabled && scores[j].isDisabled {
			return true
		}
		// Both enabled or both disabled: sort by score descending using GetCompact
		return int64(scores[i].score.GetCompact()) > int64(scores[j].score.GetCompact())
	})

	// Build result with 1-based ranks
	result := make([]MasternodeRankEntry, len(scores))
	for i, entry := range scores {
		result[i] = MasternodeRankEntry{
			Rank:       i + 1,
			Masternode: entry.mn,
		}
	}

	return result
}

// getEffectiveTierWeight returns the actual selection weight for a tier.
// LEGACY COMPATIBILITY: C++ GetMasternodeTierRounds(vin) always returns the actual tier
// weight from the UTXO value (1/5/20/100), regardless of spork state.
// The tier spork (SPORK_TWINS_01) only gates:
//   - New registrations: IsValidCollateral() rejects higher-tier collateral when spork is OFF
//   - Status updates: UpdateStatusWithUTXO() marks higher-tier MNs as VIN_SPENT when spork is OFF
//
// Both of those already handle the spork correctly, so by the time a masternode reaches
// the payment queue or score calculation, it's either disabled (VIN_SPENT) or has valid
// collateral for the current spork state. The weight function should always return the
// actual tier weight, matching C++ GetMasternodeTierRounds behavior.
func (m *Manager) getEffectiveTierWeight(tier MasternodeTier) int {
	weight := tier.SelectionWeight()
	if weight == 0 {
		// Match C++ fallback: GetMasternodeTierRounds returns 1 for unknown UTXO values
		return 1
	}
	return weight
}

// getEffectiveTierWeightLocked returns the actual selection weight for a tier.
// Caller must hold m.mu (read or write lock). Same logic as getEffectiveTierWeight.
func (m *Manager) getEffectiveTierWeightLocked(tier MasternodeTier) int {
	return m.getEffectiveTierWeight(tier)
}

// GetMinMasternodePaymentsProto returns minimum protocol version for payment eligibility
// Exported wrapper for consensus.MasternodeInterface compliance
// Legacy: masternodePayments.GetMinMasternodePaymentsProto()
func (m *Manager) GetMinMasternodePaymentsProto() int32 {
	return m.getMinMasternodePaymentsProto()
}

// isMultiTierEnabled checks if SPORK_TWINS_01_ENABLE_MASTERNODE_TIERS is active
// Legacy: IsSporkActive(SPORK_TWINS_01_ENABLE_MASTERNODE_TIERS) from main.cpp:115
// Thread-safe: Uses RLock to protect sporkManager access
func (m *Manager) isMultiTierEnabled() bool {
	const SPORK_TWINS_01_ENABLE_MASTERNODE_TIERS = int32(20190001)

	m.mu.RLock()
	sm := m.sporkManager
	m.mu.RUnlock()

	if sm == nil {
		return false // Default to single-tier mode if no spork manager
	}
	return sm.IsActive(SPORK_TWINS_01_ENABLE_MASTERNODE_TIERS)
}

// isMultiTierEnabledLocked checks if SPORK_TWINS_01_ENABLE_MASTERNODE_TIERS is active
// MUST be called while holding m.mu lock (either Lock or RLock)
// This version avoids deadlock when called from methods that already hold the lock
func (m *Manager) isMultiTierEnabledLocked() bool {
	const SPORK_TWINS_01_ENABLE_MASTERNODE_TIERS = int32(20190001)

	if m.sporkManager == nil {
		return false // Default to single-tier mode if no spork manager
	}
	return m.sporkManager.IsActive(SPORK_TWINS_01_ENABLE_MASTERNODE_TIERS)
}

// getMinMasternodePaymentsProto returns minimum protocol version for payments
// Legacy: masternodePayments.GetMinMasternodePaymentsProto() uses SPORK_10 (10009)
// CRITICAL: Must use SPORK_10_MASTERNODE_PAY_UPDATED_NODES = 10009, NOT SPORK_14 (10013)
// Thread-safe: Uses RLock to protect sporkManager access
func (m *Manager) getMinMasternodePaymentsProto() int32 {
	// Protect sporkManager access from concurrent SetSporkManager calls
	m.mu.RLock()
	sm := m.sporkManager
	m.mu.RUnlock()

	return m.getMinMasternodePaymentsProtoWithSporkManager(sm)
}

// getMinMasternodePaymentsProtoLocked returns minimum protocol version for payments
// MUST be called while holding m.mu lock (either Lock or RLock)
// This version avoids deadlock when called from methods that already hold the lock
func (m *Manager) getMinMasternodePaymentsProtoLocked() int32 {
	return m.getMinMasternodePaymentsProtoWithSporkManager(m.sporkManager)
}

// getMinMasternodePaymentsProtoWithSporkManager is the core implementation
// that takes sporkManager as a parameter to support both locked and unlocked calls
func (m *Manager) getMinMasternodePaymentsProtoWithSporkManager(sm SporkInterface) int32 {
	// SPORK_10 logic: If SPORK_10 is active, require newer protocol
	// Otherwise allow older protocol
	// Legacy: spork.h:41 - SPORK_10_MASTERNODE_PAY_UPDATED_NODES = 10009
	if sm != nil && sm.IsActive(10009) { // SPORK_10_MASTERNODE_PAY_UPDATED_NODES
		return MinPeerProtoAfterEnforcement // 70927
	}
	return MinPeerProtoBeforeEnforcement // 70926
}

// isScheduledLocked checks if masternode is already scheduled for payment in upcoming blocks.
// payeeScript must be obtained by the caller (under mn.mu.RLock if needed) to avoid
// nested mn.mu.RLock deadlock when this method is called from a context that already
// holds mn.mu.RLock (e.g. GetNextMasternodeInQueueForPayment loop).
// Legacy: masternodePayments.IsScheduled(mn, nBlockHeight) from masternode-payments.cpp:528-555
// LEGACY COMPATIBILITY FIX: Compare by payee script (not outpoint) to match C++ mapMasternodeBlocks behavior
// CRITICAL: Legacy checks from chain tip height to GetNewestBlock() (highest block in mapMasternodeBlocks)
// CRITICAL: notBlockHeight is skipped in the loop (used to exclude the block being validated)
// C++ Reference: masternode-payments.cpp:532-555
func (m *Manager) isScheduledLocked(mn *Masternode, notBlockHeight uint32, payeeScript []byte) bool {
	// LEGACY FIX #3: Get current chain tip height (not use parameter as starting height)
	// C++ Reference: masternode-payments.cpp:532-537
	// int nHeight;
	// {
	//     TRY_LOCK(cs_main, locked);
	//     if (!locked || chainActive.Tip() == NULL) return false;
	//     nHeight = chainActive.Tip()->nHeight;
	// }
	var currentHeight uint32
	if m.blockchain != nil {
		if h, err := m.blockchain.GetBestHeight(); err == nil {
			currentHeight = h
		}
	}
	if currentHeight == 0 {
		return false
	}

	// payeeScript is now passed by caller to avoid nested mn.mu.RLock deadlock.

	// LEGACY FIX #2: Use masternodeBlocks (vote aggregation) not scheduledPayments
	// C++ Reference: masternode-payments.cpp:545-548
	// if (mapMasternodeBlocks.count(h)) {
	//     if (mapMasternodeBlocks[h].GetPayee(payee)) {
	newestBlock := m.getNewestVoteBlockLocked()
	if newestBlock < currentHeight {
		return false // No scheduled payments at or after current height
	}

	// Check masternodeBlocks from current height to newest block with votes
	// Legacy: for (int64_t h = nHeight; h <= GetNewestBlock(); h++)
	for h := currentHeight; h <= newestBlock; h++ {
		// LEGACY FIX #1: Skip notBlockHeight in the loop
		// C++ Reference: masternode-payments.cpp:543-544
		// if (h == nNotBlockHeight) continue;
		if h == notBlockHeight {
			continue
		}

		m.masternodeBlocksMu.RLock()
		blockPayees, exists := m.masternodeBlocks[h]
		if exists {
			bestPayee := blockPayees.GetPayee()
			if bytes.Equal(bestPayee, payeeScript) {
				m.masternodeBlocksMu.RUnlock()
				return true
			}
		}
		m.masternodeBlocksMu.RUnlock()
	}
	return false
}

// getNewestScheduledBlockLocked returns the highest block height with scheduled payments
// Legacy: CMasternodePayments::GetNewestBlock() from masternode-payments.cpp:904-918
// Must hold m.mu (read or write)
func (m *Manager) getNewestScheduledBlockLocked() uint32 {
	var newestBlock uint32 = 0
	for height := range m.scheduledPayments {
		if height > newestBlock {
			newestBlock = height
		}
	}
	return newestBlock
}

// getNewestVoteBlockLocked returns the highest block height with payment votes
// LEGACY COMPATIBILITY: Matches CMasternodePayments::GetNewestBlock()
// C++ Reference: masternode-payments.cpp:904-918
// Must hold m.mu (read or write). Uses masternodeBlocks for vote aggregation.
func (m *Manager) getNewestVoteBlockLocked() uint32 {
	m.masternodeBlocksMu.RLock()
	defer m.masternodeBlocksMu.RUnlock()

	var newestBlock uint32 = 0
	for height := range m.masternodeBlocks {
		if height > newestBlock {
			newestBlock = height
		}
	}

	// Also check scheduledPayments for completeness
	for height := range m.scheduledPayments {
		if height > newestBlock {
			newestBlock = height
		}
	}
	return newestBlock
}

// getOldestVoteBlockLocked returns the lowest block height with payment votes
// LEGACY COMPATIBILITY: Matches CMasternodePayments::GetOldestBlock()
// C++ Reference: masternode-payments.cpp:890-902
// Must hold m.mu (read or write). Uses masternodeBlocks for vote aggregation.
func (m *Manager) getOldestVoteBlockLocked() uint32 {
	m.masternodeBlocksMu.RLock()
	defer m.masternodeBlocksMu.RUnlock()

	var oldestBlock uint32 = ^uint32(0) // Max uint32 as initial value
	found := false

	for height := range m.masternodeBlocks {
		if height < oldestBlock {
			oldestBlock = height
			found = true
		}
	}

	// Also check scheduledPayments
	for height := range m.scheduledPayments {
		if height < oldestBlock {
			oldestBlock = height
			found = true
		}
	}

	if !found {
		return 0
	}
	return oldestBlock
}

// getInputAgeLocked returns the input age (confirmations) for a masternode (must hold m.mu)
// Legacy: GetInputAge() from main.cpp:904-921
// Input age = (current chain height + 1) - collateral TX confirmation height
//
// CRITICAL FIX: Legacy C++ re-fetches confirmations from the UTXO set on each check,
// not from a cached value. This ensures masternodes added via gossip (without broadcast)
// or with stale cached heights still get proper confirmation counts.
// We now always try to fetch fresh data, updating the cache on success.
func (m *Manager) getInputAgeLocked(mn *Masternode) int64 {
	if m.blockchain == nil {
		// No blockchain - use cached value if available
		return 0
	}

	currentHeight, err := m.blockchain.GetBestHeight()
	if err != nil {
		return 0
	}

	// CRITICAL FIX: Always try to fetch fresh confirmation height from blockchain
	// Legacy C++ calls GetInputAge() which queries the UTXO set (coins view) on each call
	// This ensures masternodes received via gossip get proper input age even if
	// CollateralTxHeight was not set during initial registration
	if txBlock, err := m.blockchain.GetTransactionBlock(mn.OutPoint.Hash); err == nil && txBlock != nil {
		if txHeight, err := m.blockchain.GetBlockHeight(txBlock.Hash()); err == nil && txHeight > 0 {
			// Update cached value for future use (optimization, not relied upon)
			if mn.CollateralTxHeight != txHeight {
				mn.CollateralTxHeight = txHeight
			}
			// Legacy formula: return (chainActive.Tip()->nHeight + 1) - coins->nHeight
			if uint32(currentHeight) >= txHeight {
				return int64(uint32(currentHeight)+1) - int64(txHeight)
			}
		}
	}

	// Fallback to cached value only if blockchain lookup failed
	// This maintains backwards compatibility for edge cases
	if mn.CollateralTxHeight > 0 && uint32(currentHeight) >= mn.CollateralTxHeight {
		return int64(uint32(currentHeight)+1) - int64(mn.CollateralTxHeight)
	}

	return 0
}

// ProcessPayment is DEPRECATED and will panic if called.
// CRITICAL: Always use ProcessPaymentWithBlockTime with proper blockTime and blockHash.
// Using wall-clock time breaks deterministic consensus - different nodes would record
// different payment timestamps for the same block, and empty blockHash breaks cycle tracking.
// Legacy C++ ALWAYS uses block.nTime and block.GetBlockHash() for payment processing.
func (m *Manager) ProcessPayment(outpoint types.Outpoint, blockHeight int32) error {
	panic("ProcessPayment is deprecated: use ProcessPaymentWithBlockTime(outpoint, blockHeight, blockTime, blockHash) instead")
}

// ProcessPaymentWithBlockTime processes a masternode payment using block timestamp
// CRITICAL: Must use block timestamp (not wall-clock) for deterministic ordering
// Legacy: Uses block.nTime for lastPaid to ensure consistent payment ordering across nodes
// blockHash parameter is used as fallback if blockchain is not available
// LEGACY COMPATIBILITY: AddWin uses chainActive.Tip()->GetBlockHash(), NOT the payment block hash
func (m *Manager) ProcessPaymentWithBlockTime(outpoint types.Outpoint, blockHeight int32, blockTime int64, blockHash types.Hash) error {
	blockTimeT := time.Unix(blockTime, 0)
	m.mu.Lock()
	defer m.mu.Unlock()

	mn, exists := m.masternodes[outpoint]
	if !exists {
		return ErrMasternodeNotFound
	}

	// Update payment info using block timestamp (legacy compatible)
	mn.mu.Lock()
	mn.BlockHeight = blockHeight // Persist last-paid block height for eligibility checks
	mn.mu.Unlock()

	// LEGACY COMPATIBILITY FIX: AddWin must use chainActive.Tip()->GetBlockHash() (masternode.cpp:188)
	// NOT the payment block hash. The chain tip is the current best block at processing time.
	// This ensures cycle data references the latest chain state for cycleDataValid() checks.
	chainTipHash := blockHash // Fallback to payment block if blockchain unavailable
	if m.blockchain != nil {
		if bestHeight, err := m.blockchain.GetBestHeight(); err == nil {
			if tipBlock, err := m.blockchain.GetBlockByHeight(bestHeight); err == nil {
				chainTipHash = tipBlock.Header.Hash()
			}
		}
	}

	// Call AddWin for proper tier cycle tracking (legacy: mn.AddWin(nWins))
	// AddWin handles: WinsThisCycle++, cycle reset when wins >= tierWeight,
	// LastPaid update, and PaymentCount increment
	// AddWin now uses GetAdjustedTime() internally (legacy compatibility)
	mn.AddWin(chainTipHash)

	// Update payment queue (used for sorting) - use block time
	m.paymentQueue.mu.Lock()
	m.paymentQueue.lastPaid[outpoint] = blockTimeT
	// Guard against empty queue panic (legacy C++ handles gracefully)
	if len(m.paymentQueue.queue) > 0 {
		m.paymentQueue.paymentPos = (m.paymentQueue.paymentPos + 1) % len(m.paymentQueue.queue)
	}
	m.paymentQueue.mu.Unlock()

	// Update manager's lastPaid map for consistent sorting during rebuilds
	// Note: m.mu is already held from the beginning of this function
	m.lastPaid[outpoint] = blockTimeT

	// Schedule payment to prevent re-selection in upcoming blocks
	// Legacy: masternodePayments.AddWinningMasternode(pBestMasternode->vin.prevout, nBlockHeight)
	// LEGACY COMPATIBILITY FIX: Store payee script (not outpoint) to match C++ mapMasternodeBlocks
	// Note: m.mu is already held, so we update scheduledPayments directly
	m.scheduledPayments[uint32(blockHeight)] = mn.GetPayeeScript()

	// Clean up old scheduled payments
	// Legacy: int nLimit = std::max(int(mnodeman.size() * 1.25), 1000)
	keepBlocks := m.getScheduledPaymentsKeepBlocks()
	if uint32(blockHeight) > keepBlocks {
		cutoff := uint32(blockHeight) - keepBlocks
		for h := range m.scheduledPayments {
			if h < cutoff {
				delete(m.scheduledPayments, h)
			}
		}
	}

	m.logger.WithFields(logrus.Fields{
		"outpoint":   outpoint.String(),
		"height":     blockHeight,
		"count":      mn.PaymentCount,
		"block_time": blockTime,
	}).Info("Masternode payment processed")

	return nil
}

// GetPaymentQueue returns the current payment queue
func (m *Manager) GetPaymentQueue() []*Masternode {
	m.paymentQueue.mu.RLock()
	defer m.paymentQueue.mu.RUnlock()

	// Return a copy
	queue := make([]*Masternode, len(m.paymentQueue.queue))
	copy(queue, m.paymentQueue.queue)
	return queue
}

// IsScheduled checks if a masternode is already scheduled for payment in upcoming blocks
// Implements legacy CMasternodePayments::IsScheduled() to prevent same MN winning consecutive blocks
// Legacy: Searches the payment map for the masternode in blocks [currentHeight, currentHeight+10]
// LEGACY COMPATIBILITY FIX: Compare by payee script (not outpoint) to match C++ mapMasternodeBlocks behavior
func (m *Manager) IsScheduled(outpoint types.Outpoint, currentHeight uint32) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Get masternode to retrieve payee script
	mn, exists := m.masternodes[outpoint]
	if !exists {
		return false
	}

	// GetPayeeScript acquires mn.mu.RLock internally — do not nest under another mn.mu.RLock
	payeeScript := mn.GetPayeeScript()

	return m.isScheduledByScriptLocked(payeeScript, currentHeight)
}

// IsScheduledByMasternode checks if a masternode is scheduled to get paid soon
// LEGACY COMPATIBILITY: Matches CMasternodePayments::IsScheduled()
// C++ Reference: masternode-payments.cpp:527-555
// Uses the masternode struct directly instead of an outpoint lookup.
func (m *Manager) IsScheduledByMasternode(mn *Masternode, notBlockHeight uint32) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// GetPayeeScript acquires mn.mu.RLock internally — do not nest under another mn.mu.RLock
	payeeScript := mn.GetPayeeScript()

	return m.isScheduledByScriptLocked(payeeScript, notBlockHeight)
}

// isScheduledByScriptLocked checks if a payee script is scheduled for payment
// LEGACY COMPATIBILITY: Core logic from CMasternodePayments::IsScheduled()
// C++ Reference: masternode-payments.cpp:527-555
// Must hold m.mu (read or write)
func (m *Manager) isScheduledByScriptLocked(payeeScript []byte, notBlockHeight uint32) bool {
	// Check from current height to newest block with votes
	// Legacy: for (int64_t h = nHeight; h <= GetNewestBlock(); h++)
	newestBlock := m.getNewestVoteBlockLocked()

	for h := notBlockHeight; h <= newestBlock; h++ {
		if h == notBlockHeight {
			continue // Skip the block we're checking for (notBlockHeight)
		}

		// Check masternodeBlocks (vote aggregation - primary source)
		m.masternodeBlocksMu.RLock()
		if blockPayees, exists := m.masternodeBlocks[h]; exists {
			bestPayee := blockPayees.GetPayee()
			if bytes.Equal(bestPayee, payeeScript) {
				m.masternodeBlocksMu.RUnlock()
				return true
			}
		}
		m.masternodeBlocksMu.RUnlock()

		// Also check scheduledPayments (fallback)
		if scheduledScript, exists := m.scheduledPayments[h]; exists {
			if bytes.Equal(scheduledScript, payeeScript) {
				return true
			}
		}
	}
	return false
}

// SchedulePayment marks a masternode as scheduled for payment at a specific block height
// Called when a masternode is selected as winner for a block
// LEGACY COMPATIBILITY FIX: Store payee script (not outpoint) to match C++ mapMasternodeBlocks
func (m *Manager) SchedulePayment(outpoint types.Outpoint, blockHeight uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Get masternode to retrieve payee script
	mn, exists := m.masternodes[outpoint]
	if !exists {
		return // Can't schedule unknown masternode
	}

	// GetPayeeScript acquires mn.mu.RLock internally — do not nest under another mn.mu.RLock
	payeeScript := mn.GetPayeeScript()

	m.scheduledPayments[blockHeight] = payeeScript

	// Clean up old entries
	// Legacy: int nLimit = std::max(int(mnodeman.size() * 1.25), 1000)
	keepBlocks := m.getScheduledPaymentsKeepBlocks()
	if blockHeight > keepBlocks {
		cutoff := blockHeight - keepBlocks
		for h := range m.scheduledPayments {
			if h < cutoff {
				delete(m.scheduledPayments, h)
			}
		}
	}
}

// ClearScheduledPayment removes a scheduled payment (used if block is orphaned)
func (m *Manager) ClearScheduledPayment(blockHeight uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.scheduledPayments, blockHeight)
}

// CleanPaymentList removes old payment votes to prevent unbounded memory growth
// LEGACY COMPATIBILITY: Matches CMasternodePayments::CleanPaymentList()
// C++ Reference: masternode-payments.cpp:690-717
//
// This function should be called periodically (e.g., in updateMasternodes goroutine)
// to clean up stale data and prevent memory leaks.
func (m *Manager) CleanPaymentList() {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Get current blockchain height
	var currentHeight uint32
	if m.blockchain != nil {
		if height, err := m.blockchain.GetBestHeight(); err == nil {
			currentHeight = height
		} else {
			return // Can't clean without knowing current height
		}
	} else {
		return // No blockchain reference
	}

	// Legacy: nLimit = max(mnodeman.size() * 1.25, 1000)
	// Keep up to five cycles for historical sake
	nLimit := uint32(len(m.masternodes))
	nLimit = uint32(float64(nLimit) * 1.25)
	if nLimit < 1000 {
		nLimit = 1000
	}

	// Clean masternodeBlocks (vote aggregation)
	m.masternodeBlocksMu.Lock()
	for height := range m.masternodeBlocks {
		if currentHeight > nLimit && height < currentHeight-nLimit {
			delete(m.masternodeBlocks, height)
		}
	}
	m.masternodeBlocksMu.Unlock()

	// Clean masternodesLastVote (duplicate vote prevention)
	m.masternodesLastVoteMu.Lock()
	for key, voteHeight := range m.masternodesLastVote {
		if currentHeight > nLimit && voteHeight < currentHeight-nLimit {
			delete(m.masternodesLastVote, key)
		}
	}
	m.masternodesLastVoteMu.Unlock()

	// Clean scheduledPayments (already has cleanup in SchedulePayment, but do full sweep)
	for height := range m.scheduledPayments {
		if currentHeight > nLimit && height < currentHeight-nLimit {
			delete(m.scheduledPayments, height)
		}
	}

	// Clean winnerVotes (full winner vote storage)
	m.winnerVotesMu.Lock()
	for hash, vote := range m.winnerVotes {
		if currentHeight > nLimit && vote.BlockHeight < currentHeight-nLimit {
			delete(m.winnerVotes, hash)
		}
	}
	m.winnerVotesMu.Unlock()
}

// MarkPayeeScheduled marks a payee (by payment script) as scheduled for a given block height
// LEGACY COMPATIBILITY FIX: In C++, AddWinningMasternode populates mapMasternodeBlocks[height]
// with payee script REGARDLESS of whether the masternode is known locally.
// This enables isScheduledLocked() to see network votes even for unknown masternodes,
// preventing duplicate masternode selection for payment.
// Called by consensus.MasternodePaymentValidator when a payee reaches vote threshold.
func (m *Manager) MarkPayeeScheduled(payAddress []byte, blockHeight uint32) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// LEGACY COMPATIBILITY FIX: Store payee script directly without requiring local MN knowledge
	// C++ stores the script in mapMasternodeBlocks regardless of whether we know the masternode
	// This allows votes for masternodes we haven't synced yet to still block duplicate selection
	m.scheduledPayments[blockHeight] = payAddress

	// Clean up old entries
	// Legacy: int nLimit = std::max(int(mnodeman.size() * 1.25), 1000)
	keepBlocks := m.getScheduledPaymentsKeepBlocks()
	if blockHeight > keepBlocks {
		cutoff := blockHeight - keepBlocks
		for h := range m.scheduledPayments {
			if h < cutoff {
				delete(m.scheduledPayments, h)
			}
		}
	}
	return nil
}

// ProcessBroadcast processes a masternode broadcast message.
// originAddr is the P2P peer address that sent this broadcast (used to exclude from relay).
// Pass "" when there is no origin peer (e.g., local broadcast from active masternode or RPC).
// Implements legacy CMasternodeBroadcast::CheckAndUpdate() validation rules.
func (m *Manager) ProcessBroadcast(mnb *MasternodeBroadcast, originAddr string) error {
	source := originAddr
	if source == "" {
		source = "local"
	}

	// Derive payee address for debug events
	var debugPayee string
	if mnb.PubKeyCollateral != nil {
		if pa := crypto.NewAddressFromPubKey(mnb.PubKeyCollateral, m.config.NetworkType.GetNetworkID()); pa != nil {
			debugPayee = pa.String()
		}
	}

	// Emit debug event at entry
	if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
		dc.EmitBroadcast("broadcast_received", source, fmt.Sprintf("Broadcast received for %s @ %s (proto=%d)", mnb.OutPoint.String(), mnb.Addr.String(), mnb.Protocol), map[string]any{
			"outpoint": mnb.OutPoint.String(),
			"addr":     mnb.Addr.String(),
			"payee":    debugPayee,
			"protocol": mnb.Protocol,
		})
	}

	// CRITICAL: Gate on blockchain sync (legacy masternodeman.cpp:823)
	// Legacy: if (!masternodeSync.IsBlockchainSynced()) return;
	// Reject all masternode broadcasts during IBD to prevent processing stale data
	if m.syncManager != nil && !m.syncManager.IsBlockchainSynced() {
		if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
			dc.EmitBroadcast(debug.TypeBroadcastSkipped, source, fmt.Sprintf("Broadcast skipped for %s during IBD", mnb.OutPoint.String()), map[string]any{
				"outpoint": mnb.OutPoint.String(),
				"addr":     mnb.Addr.String(),
				"payee":    debugPayee,
				"reason":   "ibd_gate",
			})
		}
		return nil // Silent reject during IBD, not an error
	}

	// CRITICAL FIX: Use network-adjusted time for drift checks (matches legacy GetAdjustedTime)
	// This ensures consistent validation across nodes with different local clocks
	currentTime := adjustedtime.GetAdjustedUnix()

	// Check deduplication FIRST (matches legacy mapSeenMasternodeBroadcast)
	// This prevents DoS via replaying same broadcast and reduces validation load
	bcHash := mnb.GetHash()
	m.seenBroadcastsMu.RLock()
	_, seen := m.seenBroadcasts[bcHash]
	m.seenBroadcastsMu.RUnlock()

	if seen {
		// Still update sync state even for duplicates (matches legacy masternodeman.cpp:832)
		if m.syncManager != nil {
			m.syncManager.AddedMasternodeList(bcHash)
		}
		m.logger.WithField("hash", bcHash.String()).Debug("Skipping duplicate broadcast")
		if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
			dc.EmitBroadcast(debug.TypeBroadcastSkipped, source, fmt.Sprintf("Broadcast skipped for %s (already seen)", mnb.OutPoint.String()), map[string]any{
				"outpoint": mnb.OutPoint.String(),
				"addr":     mnb.Addr.String(),
				"payee":    debugPayee,
				"reason":   "already_seen",
			})
		}
		return ErrBroadcastAlreadySeen // Distinguishable from nil so callers know not to relay
	}

	// CRITICAL: Insert into seenBroadcasts BEFORE validation (legacy masternodeman.cpp:835)
	// Legacy: mapSeenMasternodeBroadcast.insert(make_pair(mnb.GetHash(), mnb));
	// This is DoS protection - even invalid broadcasts are remembered to prevent reprocessing
	// Invalid broadcasts will trigger Misbehaving() but won't be validated again
	// NOTE: Store full broadcast (not just sigTime) so lastPing can be updated later (Issue #2)
	m.seenBroadcastsMu.Lock()
	m.seenBroadcasts[bcHash] = mnb
	m.seenBroadcastsMu.Unlock()

	// Get existing masternode if any
	existingMN, _ := m.GetMasternode(mnb.OutPoint)

	// Use explicit network type from config for port validation
	// This matches legacy CheckDefaultPort behavior which uses Params().GetDefaultPort()
	// Previously used unreliable heuristic: isMainnet := m.config.MinProtocolVersion >= ActiveProtocolVersion
	network := m.config.NetworkType

	// CRITICAL FIX: Use spork-aware minimum protocol version for broadcast validation
	// Legacy: masternode.cpp:550 uses masternodePayments.GetMinMasternodePaymentsProto()
	// This ensures broadcasts with old protocol versions are rejected when SPORK_10 is active
	minProto := m.getMinMasternodePaymentsProto()

	// Run full CheckAndUpdate validation (matches legacy CMasternodeBroadcast::CheckAndUpdate)
	// This validates: sigTime, lastPing, protocol, pubkey scripts, port, signature, existing MN comparison
	validationResult := mnb.CheckAndUpdate(currentTime, minProto, network, existingMN)
	if !validationResult.Valid {
		// Call Misbehaving with DoS score (legacy masternodeman.cpp:838-840)
		// Legacy: if (!mnb.CheckAndUpdate(nDoS)) { if (nDoS > 0) Misbehaving(pfrom->GetId(), nDoS); }
		if validationResult.DoS > 0 && m.misbehaviorFunc != nil {
			m.misbehaviorFunc("", int32(validationResult.DoS), validationResult.Error)
		}
		if validationResult.ShouldSkip {
			// Skip processing but don't return error (e.g., duplicate broadcast)
			m.logger.WithField("reason", validationResult.Error).Debug("Skipping broadcast")
			if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
				dc.EmitBroadcast(debug.TypeBroadcastSkipped, source, fmt.Sprintf("Broadcast skipped for %s: %s", mnb.OutPoint.String(), validationResult.Error), map[string]any{
					"outpoint": mnb.OutPoint.String(),
					"addr":     mnb.Addr.String(),
					"payee":    debugPayee,
					"reason":   validationResult.Error,
				})
			}
			return nil
		}

		// Emit debug event on rejection
		if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
			dc.EmitBroadcast("broadcast_rejected", source, fmt.Sprintf("Broadcast rejected for %s @ %s: %s", mnb.OutPoint.String(), mnb.Addr.String(), validationResult.Error), map[string]any{
				"outpoint": mnb.OutPoint.String(),
				"addr":     mnb.Addr.String(),
				"payee":    debugPayee,
				"dos":      validationResult.DoS,
				"reason":   validationResult.Error,
			})
		}
		return fmt.Errorf("broadcast validation failed: %s", validationResult.Error)
	}

	// CRITICAL: Self-broadcast early return (legacy masternode.cpp:624-629)
	// Legacy: if (fMasterNode && vin.prevout == activeMasternode.vin.prevout && pubKeyMasternode == activeMasternode.pubKeyMasternode)
	//             return true;
	// If this broadcast matches our own active masternode, skip full validation.
	// We already know our own collateral is valid and this just wastes resources.
	if m.activeMasternode != nil {
		m.activeMasternode.mu.RLock()
		amVin := m.activeMasternode.Vin
		amPubKey := m.activeMasternode.PubKeyMasternode
		amStatus := m.activeMasternode.Status
		m.activeMasternode.mu.RUnlock()

		// Only skip if we're already started and this matches our vin and pubkey
		if amStatus == ActiveStarted && amVin == mnb.OutPoint && amPubKey != nil && mnb.PubKeyMasternode != nil {
			if amPubKey.IsEqual(mnb.PubKeyMasternode) {
				m.logger.WithFields(logrus.Fields{
					"outpoint": mnb.OutPoint.String(),
				}).Debug("Skipping self-broadcast validation (our own active masternode)")
				// Still update sync state for self-broadcasts
				if m.syncManager != nil {
					m.syncManager.AddedMasternodeList(bcHash)
				}
				if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
					dc.EmitBroadcast(debug.TypeBroadcastSkipped, source, fmt.Sprintf("Broadcast skipped for %s (self-broadcast from local active masternode)", mnb.OutPoint.String()), map[string]any{
						"outpoint": mnb.OutPoint.String(),
						"addr":     mnb.Addr.String(),
						"payee":    debugPayee,
						"reason":   "self_broadcast",
					})
				}
				return nil
			}
		}
	}

	// LEGACY FIX: Use IsBroadcastedWithin for timing check (masternode.cpp:611)
	// C++ Reference:
	// if (pmn->pubKeyCollateralAddress == pubKeyCollateralAddress &&
	//     !pmn->IsBroadcastedWithin(MASTERNODE_MIN_MNB_SECONDS)) {
	//         // proceed with update
	// }
	// IsBroadcastedWithin checks: (GetAdjustedTime() - sigTime) < seconds
	// So we only update if the existing MN was broadcasted MORE than MIN_MNB_SECONDS ago
	if existingMN != nil {
		// Check if existing MN was recently broadcasted (within MIN_MNB_SECONDS of current time)
		if existingMN.IsBroadcastedWithin(MinBroadcastSeconds) {
			if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
				dc.EmitBroadcast(debug.TypeBroadcastRejected, source, fmt.Sprintf("Broadcast rejected for %s: existing MN broadcasted within %d seconds", mnb.OutPoint.String(), MinBroadcastSeconds), map[string]any{
					"outpoint": mnb.OutPoint.String(),
					"addr":     mnb.Addr.String(),
					"payee":    debugPayee,
					"reason":   "recent_broadcast",
				})
			}
			return fmt.Errorf("existing masternode was broadcasted within %d seconds, update rejected",
				MinBroadcastSeconds)
		}
	}

	// 5. Derive tier and collateral from blockchain with full validation
	var collateral int64
	var tier MasternodeTier
	var confirmations uint32
	var collateralTxHeight uint32 // Height when collateral TX was confirmed (for input age)

	if m.blockchain != nil {
		// Query blockchain for collateral transaction
		tx, err := m.blockchain.GetTransaction(mnb.OutPoint.Hash)
		if err != nil {
			// CRITICAL FIX: Remove from seenBroadcasts on temporary error
			// Legacy: mnodeman.mapSeenMasternodeBroadcast.erase(GetHash()) in CheckInputsAndAdd
			// TX may not be indexed yet (startup, reindex, partial sync) — allow retry
			m.seenBroadcastsMu.Lock()
			delete(m.seenBroadcasts, bcHash)
			m.seenBroadcastsMu.Unlock()
			m.logger.WithFields(logrus.Fields{
				"outpoint": mnb.OutPoint.String(),
				"hash":     bcHash.String(),
			}).Warn("Broadcast removed from seen (collateral TX not found, will retry)")
			if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
				dc.EmitBroadcast(debug.TypeBroadcastRejected, source, fmt.Sprintf("Broadcast rejected for %s: collateral TX not found", mnb.OutPoint.String()), map[string]any{
					"outpoint": mnb.OutPoint.String(),
					"addr":     mnb.Addr.String(),
					"payee":    debugPayee,
					"reason":   "collateral_tx_not_found",
					"error":    err.Error(),
				})
			}
			return fmt.Errorf("collateral transaction %s not found: %w", mnb.OutPoint.Hash.String(), err)
		}

		// Extract output value from the outpoint index
		if int(mnb.OutPoint.Index) >= len(tx.Outputs) {
			if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
				dc.EmitBroadcast(debug.TypeBroadcastRejected, source, fmt.Sprintf("Broadcast rejected for %s: outpoint index %d out of range (tx has %d outputs)", mnb.OutPoint.String(), mnb.OutPoint.Index, len(tx.Outputs)), map[string]any{
					"outpoint": mnb.OutPoint.String(),
					"addr":     mnb.Addr.String(),
					"payee":    debugPayee,
					"reason":   "outpoint_index_out_of_range",
				})
			}
			return fmt.Errorf("outpoint index %d out of range (tx has %d outputs)",
				mnb.OutPoint.Index, len(tx.Outputs))
		}

		collateral = tx.Outputs[mnb.OutPoint.Index].Value

		// 6. Check collateral confirmations (MIN_CONFIRMATIONS = 15)
		// Legacy: GetInputAge(vin) < MASTERNODE_MIN_CONFIRMATIONS
		txBlock, txErr := m.blockchain.GetTransactionBlock(mnb.OutPoint.Hash)
		if txErr == nil && txBlock != nil {
			txHeight, heightErr := m.blockchain.GetBlockHeight(txBlock.Hash())
			if heightErr == nil {
				collateralTxHeight = txHeight // Save for input age calculation
				currentHeight, _ := m.blockchain.GetBestHeight()
				if currentHeight > txHeight {
					confirmations = currentHeight - txHeight
				}
			}
		}

		if confirmations < uint32(MinConfirmations) {
			// CRITICAL FIX: Remove from seenBroadcasts on temporary error (legacy masternode.cpp:669-671)
			// Legacy: mnodeman.mapSeenMasternodeBroadcast.erase(GetHash());
			// This allows the broadcast to be reprocessed later when it has enough confirmations
			m.seenBroadcastsMu.Lock()
			delete(m.seenBroadcasts, bcHash)
			m.seenBroadcastsMu.Unlock()
			m.logger.WithFields(logrus.Fields{
				"hash":          bcHash.String(),
				"confirmations": confirmations,
				"required":      MinConfirmations,
			}).Debug("Removed broadcast from seen (insufficient confirmations, will retry)")

			reason := fmt.Sprintf("collateral has %d confirmations, minimum %d required", confirmations, MinConfirmations)
			if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
				dc.EmitBroadcast("broadcast_rejected", source, fmt.Sprintf("Broadcast rejected for %s @ %s: %s", mnb.OutPoint.String(), mnb.Addr.String(), reason), map[string]any{
					"outpoint": mnb.OutPoint.String(),
					"addr":     mnb.Addr.String(),
					"payee":    debugPayee,
					"reason":   reason,
				})
			}
			return fmt.Errorf("collateral has %d confirmations, minimum %d required",
				confirmations, MinConfirmations)
		}

		// 6b. Verify sigTime is not earlier than when collateral reached MIN_CONFIRMATIONS
		// Legacy: masternode.cpp:674-688 - CheckInputsAndAdd() sigTime maturity check
		// The sigTime must be >= the block time when the collateral TX got MIN_CONFIRMATIONS
		// This prevents pre-mature or backdated masternode announcements
		if collateralTxHeight > 0 {
			// pConfIndex = height where collateral got MIN_CONFIRMATIONS
			// Legacy: chainActive[pMNIndex->nHeight + MASTERNODE_MIN_CONFIRMATIONS - 1]
			confBlockHeight := collateralTxHeight + uint32(MinConfirmations) - 1
			confBlock, err := m.blockchain.GetBlockByHeight(confBlockHeight)
			if err == nil && confBlock != nil {
				confBlockTime := int64(confBlock.Header.Timestamp)
				if confBlockTime > mnb.SigTime {
					if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
						dc.EmitBroadcast(debug.TypeBroadcastRejected, source, fmt.Sprintf("Broadcast rejected for %s: sigTime %d before collateral maturity block time %d", mnb.OutPoint.String(), mnb.SigTime, confBlockTime), map[string]any{
							"outpoint": mnb.OutPoint.String(),
							"addr":     mnb.Addr.String(),
							"payee":    debugPayee,
							"reason":   "sigtime_before_collateral_maturity",
						})
					}
					return fmt.Errorf("bad sigTime %d for masternode %s (%d conf block at height %d has time %d)",
						mnb.SigTime, mnb.OutPoint.String(), MinConfirmations, confBlockHeight, confBlockTime)
				}
			}
			// If we can't get the block, skip this check (may be IBD or block not available)
			// Legacy behavior allows processing to continue if block lookup fails
		}

		// 7. Check collateral is not spent (vin availability)
		utxo, err := m.blockchain.GetUTXO(mnb.OutPoint)
		if err != nil {
			// CRITICAL FIX: Remove from seenBroadcasts on temporary error
			// Legacy: mnodeman.mapSeenMasternodeBroadcast.erase(GetHash()) in CheckInputsAndAdd
			// UTXO may be temporarily unavailable (reorg, cache flush) — allow retry
			m.seenBroadcastsMu.Lock()
			delete(m.seenBroadcasts, bcHash)
			m.seenBroadcastsMu.Unlock()
			m.logger.WithFields(logrus.Fields{
				"outpoint": mnb.OutPoint.String(),
				"hash":     bcHash.String(),
			}).Warn("Broadcast removed from seen (UTXO lookup failed, will retry)")
			if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
				dc.EmitBroadcast(debug.TypeBroadcastRejected, source, fmt.Sprintf("Broadcast rejected for %s: UTXO lookup failed", mnb.OutPoint.String()), map[string]any{
					"outpoint": mnb.OutPoint.String(),
					"addr":     mnb.Addr.String(),
					"payee":    debugPayee,
					"reason":   "utxo_lookup_failed",
					"error":    err.Error(),
				})
			}
			return fmt.Errorf("%w: %w", ErrCollateralSpent, err)
		}
		if utxo == nil {
			// nil UTXO without error = collateral definitively spent — permanent rejection
			// Do NOT clean seenBroadcasts here (DoS protection, matches C++ behavior)
			if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
				dc.EmitBroadcast(debug.TypeBroadcastRejected, source, fmt.Sprintf("Broadcast rejected for %s: collateral spent", mnb.OutPoint.String()), map[string]any{
					"outpoint": mnb.OutPoint.String(),
					"addr":     mnb.Addr.String(),
					"payee":    debugPayee,
					"reason":   "collateral_spent",
				})
			}
			return ErrCollateralSpent
		}

		// 7b. CRITICAL SECURITY: Verify vin is associated with collateral pubkey
		// Legacy: obfuScationSigner.IsVinAssociatedWithPubkey(mnb.vin, mnb.pubKeyCollateralAddress)
		// This prevents attacker from registering masternode using someone else's UTXO
		// by supplying their own pubkey while pointing to victim's collateral
		if mnb.PubKeyCollateral == nil {
			if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
				dc.EmitBroadcast(debug.TypeBroadcastRejected, source, fmt.Sprintf("Broadcast rejected for %s: missing PubKeyCollateral", mnb.OutPoint.String()), map[string]any{
					"outpoint": mnb.OutPoint.String(),
					"addr":     mnb.Addr.String(),
					"payee":    debugPayee,
					"reason":   "missing_pubkey_collateral",
				})
			}
			return fmt.Errorf("broadcast missing required PubKeyCollateral")
		}
		{
			// Generate expected P2PKH script from the collateral pubkey
			// Legacy: GetScriptForDestination(pubkey.GetID()) creates P2PKH script
			expectedAddr := crypto.NewAddressFromPubKey(mnb.PubKeyCollateral, m.config.NetworkType.GetNetworkID())
			expectedScript := expectedAddr.CreateScriptPubKey()

			// Get actual script from UTXO output
			actualScript := utxo.Output.ScriptPubKey

			// Compare with actual UTXO script (must match exactly)
			if !bytes.Equal(actualScript, expectedScript) {
				// Report misbehavior if callback is set (matches legacy Misbehaving(pfrom->GetId(), 33))
				if m.misbehaviorFunc != nil {
					m.misbehaviorFunc("", 33, "mismatched pubkey and vin")
				}
				m.logger.WithFields(logrus.Fields{
					"outpoint":        mnb.OutPoint.String(),
					"expected_script": fmt.Sprintf("%x", expectedScript),
					"actual_script":   fmt.Sprintf("%x", actualScript),
				}).Warn("Broadcast rejected: vin not associated with pubkey")
				if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
					dc.EmitBroadcast(debug.TypeBroadcastRejected, source, fmt.Sprintf("Broadcast rejected for %s: vin not associated with pubkey", mnb.OutPoint.String()), map[string]any{
						"outpoint": mnb.OutPoint.String(),
						"addr":     mnb.Addr.String(),
						"payee":    debugPayee,
						"reason":   "vin_pubkey_mismatch",
						"dos":      33,
					})
				}
				return fmt.Errorf("vin not associated with pubkey: script mismatch")
			}
		}

		// 8. Derive tier from collateral amount with spork-aware validation
		derivedTier, err := m.validateCollateralWithSpork(collateral)
		if err != nil {
			if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
				dc.EmitBroadcast(debug.TypeBroadcastRejected, source, fmt.Sprintf("Broadcast rejected for %s: invalid collateral (%d)", mnb.OutPoint.String(), collateral), map[string]any{
					"outpoint":   mnb.OutPoint.String(),
					"addr":       mnb.Addr.String(),
					"payee":      debugPayee,
					"reason":     "invalid_collateral",
					"collateral": collateral,
					"error":      err.Error(),
				})
			}
			return fmt.Errorf("invalid collateral for masternode: %w", err)
		}

		tier = derivedTier
		m.logger.WithFields(logrus.Fields{
			"tier":          tier.String(),
			"collateral":    collateral,
			"confirmations": confirmations,
			"outpoint":      mnb.OutPoint.String(),
		}).Debug("Validated masternode broadcast")
	} else {
		// Blockchain not wired yet — temporary condition, allow retry
		m.seenBroadcastsMu.Lock()
		delete(m.seenBroadcasts, bcHash)
		m.seenBroadcastsMu.Unlock()
		if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
			dc.EmitBroadcast(debug.TypeBroadcastSkipped, source, fmt.Sprintf("Broadcast skipped for %s: blockchain unavailable", mnb.OutPoint.String()), map[string]any{
				"outpoint": mnb.OutPoint.String(),
				"addr":     mnb.Addr.String(),
				"payee":    debugPayee,
				"reason":   "blockchain_unavailable",
			})
		}
		return fmt.Errorf("blockchain not available for collateral validation")
	}

	// Get current block height for activation tracking
	var activeHeight uint32
	if m.blockchain != nil {
		if height, err := m.blockchain.GetBestHeight(); err == nil {
			activeHeight = height
		}
	}

	// Create or update masternode
	// CRITICAL FIX: Use broadcast's sigTime for ActiveSince instead of wall-clock
	// Legacy stores the signed sigTime, which drives expiry/eligibility calculations
	// Using wall-clock makes status/expiry non-deterministic under clock skew
	sigTimeT := time.Unix(mnb.SigTime, 0)

	// BUG FIX: Use embedded ping's SigTime for LastSeen/LastPing when available
	// Legacy calls lastPing.CheckAndUpdate() on the embedded ping (masternode.cpp:172-173)
	// which updates lastPing with the CURRENT sigTime, not the broadcast creation time.
	// Without this fix, LastSeen shows the broadcast creation date (potentially years old)
	// instead of the most recent ping time.
	lastPingTime := sigTimeT
	if mnb.LastPing != nil && mnb.LastPing.SigTime > 0 {
		lastPingTime = time.Unix(mnb.LastPing.SigTime, 0)
	}

	// LEGACY FIX: Initialize PrevCycleLastPaymentTime to GetAdjustedTime() (masternode.cpp:98)
	// Legacy constructor: prevCycleLastPaymentTime = GetAdjustedTime();
	// This ensures new masternodes have valid cycle data from creation
	currentBlockHash := types.ZeroHash
	if m.blockchain != nil {
		if h, err := m.blockchain.GetBestHeight(); err == nil {
			if block, err := m.blockchain.GetBlockByHeight(h); err == nil && block != nil {
				currentBlockHash = block.Hash()
			}
		}
	}

	mn := &Masternode{
		OutPoint:                 mnb.OutPoint,
		Addr:                     mnb.Addr,
		PubKey:                   mnb.PubKeyMasternode, // Operator key
		PubKeyCollateral:         mnb.PubKeyCollateral, // Collateral key (preserve from broadcast)
		Signature:                mnb.Signature,        // Original broadcast signature
		SigTime:                  mnb.SigTime,          // Signature timestamp
		Protocol:                 mnb.Protocol,
		Tier:                     tier,
		Collateral:               collateral,
		Status:                   StatusPreEnabled,
		ActiveSince:              sigTimeT,                        // Use signed time, not wall-clock (legacy compatible)
		ActiveHeight:             activeHeight,                    // Track block height when masternode became active
		CollateralTxHeight:       collateralTxHeight,              // Block height when collateral TX was confirmed (for input age)
		LastSeen:                 lastPingTime,                    // Use embedded ping time when available (legacy compatible)
		LastPingMessage:          mnb.LastPing,                    // Store lastPing from broadcast (critical for status checks)
		LastPing:                 lastPingTime,                    // Use embedded ping time when available (legacy compatible)
		PrevCycleLastPaymentTime: consensus.GetAdjustedTimeUnix(), // LEGACY FIX: Initialize like C++ constructor
		PrevCycleLastPaymentHash: currentBlockHash,                // LEGACY FIX: Initialize to chain tip
		WinsThisCycle:            0,                               // LEGACY FIX: Initialize wins to 0
	}

	// Check if masternode exists
	_, existErr := m.GetMasternode(mn.OutPoint)
	var addErr error
	if existErr == nil {
		// Update existing - use UpdateFromBroadcast for full state refresh
		// This copies all broadcast-relevant fields (addr, pubkeys, protocol, sigTime, etc.)
		// unlike UpdateMasternode which only updates timestamps/status
		addErr = m.UpdateFromBroadcast(mn)
	} else {
		// Add new masternode
		addErr = m.AddMasternode(mn)
	}

	if addErr != nil {
		if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
			dc.EmitBroadcast(debug.TypeBroadcastRejected, source, fmt.Sprintf("Broadcast rejected for %s: add/update failed: %v", mnb.OutPoint.String(), addErr), map[string]any{
				"outpoint": mnb.OutPoint.String(),
				"addr":     mnb.Addr.String(),
				"payee":    debugPayee,
				"reason":   "add_or_update_failed",
				"error":    addErr.Error(),
			})
		}
		return addErr
	}

	// Update sync state machine (matches legacy masternodeSync.AddedMasternodeList)
	// This drives IsSynced() to become true after receiving enough broadcasts
	if m.syncManager != nil {
		m.syncManager.AddedMasternodeList(bcHash)
	}

	// CRITICAL FIX: Hot-Cold Remote Activation (legacy masternode.cpp:694-697)
	// Legacy: if (pubKeyMasternode == activeMasternode.pubKeyMasternode && protocolVersion == PROTOCOL_VERSION)
	//             activeMasternode.EnableHotColdMasterNode(vin, addr);
	// This enables remote start: cold wallet sends broadcast, hot VPS node auto-activates
	if m.activeMasternode != nil && mnb.PubKeyMasternode != nil {
		m.activeMasternode.mu.RLock()
		localPubKey := m.activeMasternode.PubKeyMasternode
		m.activeMasternode.mu.RUnlock()

		if localPubKey != nil && mnb.Protocol == ActiveProtocolVersion {
			// Compare public keys
			localPubKeyBytes := localPubKey.SerializeCompressed()
			remotePubKeyBytes := mnb.PubKeyMasternode.SerializeCompressed()
			if bytes.Equal(localPubKeyBytes, remotePubKeyBytes) {
				// This broadcast matches our masternode key - enable hot-cold mode
				m.logger.WithFields(logrus.Fields{
					"outpoint": mnb.OutPoint.String(),
					"addr":     mnb.Addr.String(),
				}).Info("Received broadcast matching local masternode key - enabling hot-cold mode")
				m.activeMasternode.EnableHotColdMasterNodeRemote(mnb.OutPoint, mnb.Addr)
			}
		}
	}

	// CRITICAL FIX: Relay broadcast to network (legacy masternode.cpp:702)
	// Legacy: if (!isLocal) Relay();
	// This ensures the masternode announcement propagates to all peers
	// Without relay, the masternode is only known locally and never receives payments
	// NOTE: Use lock-copy-invoke pattern to prevent race condition with SetBroadcastRelayHandler
	m.mu.RLock()
	relayFunc := m.broadcastRelayFunc
	m.mu.RUnlock()
	if relayFunc != nil {
		// Check if address is not local (legacy: !addr.IsRFC1918() && !addr.IsLocal())
		// For simplicity, relay all broadcasts - local addresses won't hurt network propagation
		relayFunc(mnb, originAddr)
		m.logger.WithField("hash", bcHash.String()).Debug("Relayed masternode broadcast to network")
	} else {
		m.logger.WithField("outpoint", mnb.OutPoint.String()).
			Warn("Broadcast accepted but NOT relayed - broadcast relay handler not configured")
	}

	// Emit debug event on successful acceptance
	if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
		dc.EmitBroadcast("broadcast_accepted", source, fmt.Sprintf("Broadcast accepted for %s @ %s tier=%s", mnb.OutPoint.String(), mnb.Addr.String(), tier.String()), map[string]any{
			"outpoint": mnb.OutPoint.String(),
			"addr":     mnb.Addr.String(),
			"payee":    debugPayee,
			"tier":     tier.String(),
			"protocol": mnb.Protocol,
			"is_new":   existErr != nil,
		})
	}

	return nil
}

// ProcessPing processes a masternode ping message
// Implements legacy CMasternodePing::CheckAndUpdate() validation rules
// CRITICAL: Uses network-adjusted time (GetAdjustedTime) and deduplication matching legacy C++
func (m *Manager) ProcessPing(mnp *MasternodePing, peerAddr string) error {
	// Emit debug event at entry
	if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
		dc.EmitPing("ping_received", peerAddr, fmt.Sprintf("Ping received for %s (sigTime=%d)", mnp.OutPoint.String(), mnp.SigTime), map[string]any{
			"outpoint":   mnp.OutPoint.String(),
			"sig_time":   mnp.SigTime,
			"block_hash": mnp.BlockHash.String(),
		})
	}

	// CRITICAL: Gate on blockchain sync (legacy masternodeman.cpp:823)
	// Legacy: if (!masternodeSync.IsBlockchainSynced()) return;
	// Reject all masternode pings during IBD to prevent processing stale data
	if m.syncManager != nil && !m.syncManager.IsBlockchainSynced() {
		if peerAddr == "local" {
			m.logger.WithField("outpoint", mnp.OutPoint.String()).
				Warn("Local masternode ping dropped - blockchain not synced (IBD)")
		}
		if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
			dc.EmitPing(debug.TypePingSkipped, peerAddr, fmt.Sprintf("Ping skipped for %s during IBD", mnp.OutPoint.String()), map[string]any{
				"outpoint": mnp.OutPoint.String(),
				"sig_time": mnp.SigTime,
				"reason":   "ibd_gate",
			})
		}
		return nil // Silent reject during IBD, not an error
	}

	// CRITICAL FIX: Use network-adjusted time for drift checks (matches legacy GetAdjustedTime)
	// This ensures consistent validation across nodes with different local clocks
	currentTime := adjustedtime.GetAdjustedUnix()

	// 1. Check if we've already seen this ping (deduplication)
	// Legacy: mapSeenMasternodePing prevents duplicate processing and relay
	// LEGACY COMPATIBILITY FIX: C++ inserts into mapSeenMasternodePing IMMEDIATELY after
	// dedup check, BEFORE any validation (masternodeman.cpp:874-875):
	//   if (mapSeenMasternodePing.count(mnp.GetHash())) return;
	//   mapSeenMasternodePing.insert(make_pair(mnp.GetHash(), mnp));
	// This means failed pings cannot be retried - they stay in seenPings.
	pingHash := mnp.GetHash()
	m.seenPingsMu.Lock()
	_, seen := m.seenPings[pingHash]
	if seen {
		m.seenPingsMu.Unlock()
		m.logger.WithFields(logrus.Fields{
			"outpoint":  mnp.OutPoint.String(),
			"peer_addr": peerAddr,
		}).Debug("Ping already seen (dedup) - skipping")
		if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
			dc.EmitPing(debug.TypePingSkipped, peerAddr, fmt.Sprintf("Ping skipped for %s (already seen)", mnp.OutPoint.String()), map[string]any{
				"outpoint": mnp.OutPoint.String(),
				"sig_time": mnp.SigTime,
				"reason":   "already_seen",
			})
		}
		return nil
	}
	// Insert dedup marker immediately after duplicate check (matches legacy semantics:
	// failed pings are still considered seen and won't be retried).
	m.seenPings[pingHash] = mnp.SigTime
	m.seenPingsMu.Unlock()

	// 2. Validate sigTime is within ±1 hour of current time
	// Legacy: if (sigTime > GetAdjustedTime() + 60 * 60) nDos = 1; return false
	// Legacy: if (sigTime <= GetAdjustedTime() - 60 * 60) nDos = 1; return false
	if mnp.SigTime > currentTime+PingTimeTolerance {
		// Misbehaving with DoS=1 (legacy masternode.cpp:819)
		if m.misbehaviorFunc != nil {
			m.misbehaviorFunc(peerAddr, 1, "ping sigTime too far in future")
		}
		if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
			dc.EmitPing(debug.TypePingRejected, peerAddr, fmt.Sprintf("Ping rejected for %s: sigTime %d too far in future", mnp.OutPoint.String(), mnp.SigTime), map[string]any{
				"outpoint": mnp.OutPoint.String(),
				"sig_time": mnp.SigTime,
				"reason":   "sigtime_too_new",
				"dos":      1,
			})
		}
		return fmt.Errorf("ping sigTime %d is too far in the future (now=%d, max drift=%d)",
			mnp.SigTime, currentTime, PingTimeTolerance)
	}
	if mnp.SigTime <= currentTime-PingTimeTolerance {
		// Misbehaving with DoS=1 (legacy masternode.cpp:825)
		if m.misbehaviorFunc != nil {
			m.misbehaviorFunc(peerAddr, 1, "ping sigTime too far in past")
		}
		if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
			dc.EmitPing(debug.TypePingRejected, peerAddr, fmt.Sprintf("Ping rejected for %s: sigTime %d too far in past", mnp.OutPoint.String(), mnp.SigTime), map[string]any{
				"outpoint": mnp.OutPoint.String(),
				"sig_time": mnp.SigTime,
				"reason":   "sigtime_too_old",
				"dos":      1,
			})
		}
		return fmt.Errorf("ping sigTime %d is too far in the past (now=%d, max drift=%d)",
			mnp.SigTime, currentTime, PingTimeTolerance)
	}

	// 3. Get masternode (need public key for verification)
	mn, err := m.GetMasternode(mnp.OutPoint)
	if err != nil {
		// LEGACY COMPATIBILITY (Issue #5): Request unknown masternode broadcast from peers
		// Legacy C++ (masternodeman.cpp:885-892):
		//   CMasternode* pmn = Find(mnp.vin);
		//   if (pmn != NULL) return;
		//   AskForMN(pfrom, mnp.vin);  // Request broadcast for unknown MN
		// This enables faster network recovery - we learn about masternodes from their pings
		m.mu.RLock()
		askForMN := m.askForMNFunc
		m.mu.RUnlock()
		if askForMN != nil {
			askForMN(mnp.OutPoint)
		}
		if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
			dc.EmitPing(debug.TypePingRejected, peerAddr, fmt.Sprintf("Ping rejected for %s: masternode not found", mnp.OutPoint.String()), map[string]any{
				"outpoint": mnp.OutPoint.String(),
				"sig_time": mnp.SigTime,
				"reason":   "masternode_not_found",
			})
		}
		return fmt.Errorf("%w: %w", ErrMasternodeNotFound, err)
	}

	// 3.5 LEGACY COMPATIBILITY: Protocol version gate
	// Legacy: if (pmn != NULL && pmn->protocolVersion >= masternodePayments.GetMinMasternodePaymentsProto())
	// Masternodes with old protocol versions should not have their pings accepted
	minProto := m.getMinMasternodePaymentsProto()
	mn.mu.RLock()
	mnProtocol := mn.Protocol
	mn.mu.RUnlock()
	if mnProtocol < minProto {
		if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
			dc.EmitPing(debug.TypePingRejected, peerAddr, fmt.Sprintf("Ping rejected for %s: protocol %d below minimum %d", mnp.OutPoint.String(), mnProtocol, minProto), map[string]any{
				"outpoint": mnp.OutPoint.String(),
				"sig_time": mnp.SigTime,
				"reason":   "protocol_too_old",
				"protocol": mnProtocol,
			})
		}
		return fmt.Errorf("%w: %d < %d", ErrInvalidProtocol, mnProtocol, minProto)
	}

	// 4. Check masternode status BEFORE expensive signature verification (Issue #4)
	// LEGACY COMPATIBILITY: Legacy C++ (masternode.cpp:840) checks fRequireEnabled BEFORE
	// signature verification for performance - no point verifying signatures for disabled nodes
	// Legacy: if (fRequireEnabled && !pmn->IsEnabled()) return false;
	// RECOVERY FIX: Also allow EXPIRED masternodes to accept pings, breaking the deadlock
	// where EXPIRED→pings rejected→lastPing never updates→stays EXPIRED/REMOVED.
	// C++ recovers via broadcasts, but allowing EXPIRED pings provides faster recovery.
	mn.mu.RLock()
	currentStatus := mn.Status
	mn.mu.RUnlock()
	if currentStatus != StatusEnabled && currentStatus != StatusPreEnabled && currentStatus != StatusExpired {
		if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
			dc.EmitPing(debug.TypePingRejected, peerAddr, fmt.Sprintf("Ping rejected for %s: masternode not eligible (status=%s)", mnp.OutPoint.String(), currentStatus.String()), map[string]any{
				"outpoint": mnp.OutPoint.String(),
				"sig_time": mnp.SigTime,
				"reason":   "masternode_disabled",
				"status":   currentStatus.String(),
			})
		}
		return fmt.Errorf("ping rejected from non-enabled masternode (status: %s)", currentStatus.String())
	}

	// 5. Check minimum time between pings
	// Legacy: if (!pmn->IsPingedWithin(MASTERNODE_MIN_MNP_SECONDS - 60, sigTime))
	// MinPingSeconds = 300, so threshold is 240 seconds
	// The 60-second grace period allows for network latency and clock drift
	pingSpacingThreshold := MinPingSeconds - PingSpacingGrace
	mn.mu.RLock()
	lastPingSigTime := mn.LastPingMessage
	mn.mu.RUnlock()

	if lastPingSigTime != nil {
		timeSinceLastPing := mnp.SigTime - lastPingSigTime.SigTime
		if timeSinceLastPing < pingSpacingThreshold {
			if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
				dc.EmitPing(debug.TypePingRejected, peerAddr, fmt.Sprintf("Ping rejected for %s: spacing violation (%d seconds, need %d)", mnp.OutPoint.String(), timeSinceLastPing, pingSpacingThreshold), map[string]any{
					"outpoint": mnp.OutPoint.String(),
					"sig_time": mnp.SigTime,
					"reason":   "spacing_violation",
				})
			}
			return fmt.Errorf("ping too soon: %d seconds since last ping, minimum %d required",
				timeSinceLastPing, pingSpacingThreshold)
		}
	}

	// 6. Check blockHash is recent (within 24 blocks)
	// Legacy: if ((*mi).second->nHeight < chainActive.Height() - 24)
	if m.blockchain == nil {
		m.logger.Debug("Skipping ping block hash validation - blockchain not available")
	}
	if m.blockchain != nil {
		currentHeight, err := m.blockchain.GetBestHeight()
		if err != nil {
			m.logger.WithError(err).Warn("Skipping ping block hash validation - GetBestHeight failed")
		} else {
			blockHeight, err := m.blockchain.GetBlockHeight(mnp.BlockHash)
			if err != nil {
				if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
					dc.EmitPing(debug.TypePingRejected, peerAddr, fmt.Sprintf("Ping rejected for %s: blockHash %s not found in chain", mnp.OutPoint.String(), mnp.BlockHash.String()), map[string]any{
						"outpoint":   mnp.OutPoint.String(),
						"sig_time":   mnp.SigTime,
						"block_hash": mnp.BlockHash.String(),
						"reason":     "blockhash_unknown",
					})
				}
				return fmt.Errorf("ping blockHash %s not found in chain", mnp.BlockHash.String())
			}
			if currentHeight-blockHeight > PingBlockHashMaxDepth {
				if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
					dc.EmitPing(debug.TypePingRejected, peerAddr, fmt.Sprintf("Ping rejected for %s: blockHash too old (height %d, current %d, max age %d)", mnp.OutPoint.String(), blockHeight, currentHeight, PingBlockHashMaxDepth), map[string]any{
						"outpoint":   mnp.OutPoint.String(),
						"sig_time":   mnp.SigTime,
						"block_hash": mnp.BlockHash.String(),
						"reason":     "blockhash_too_old",
					})
				}
				return fmt.Errorf("ping blockHash is too old: height %d, current %d, max age %d blocks",
					blockHeight, currentHeight, PingBlockHashMaxDepth)
			}
		}
	}

	// 7. Verify signature using masternode's public key
	if err := mnp.Verify(mn.PubKey); err != nil {
		// Misbehaving with DoS=33 (legacy masternode.cpp:809)
		if m.misbehaviorFunc != nil {
			m.misbehaviorFunc(peerAddr, 33, "invalid ping signature")
		}
		if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
			dc.EmitPing(debug.TypePingRejected, peerAddr, fmt.Sprintf("Ping rejected for %s: invalid signature", mnp.OutPoint.String()), map[string]any{
				"outpoint": mnp.OutPoint.String(),
				"sig_time": mnp.SigTime,
				"reason":   "signature_invalid",
				"dos":      33,
				"error":    err.Error(),
			})
		}
		return fmt.Errorf("invalid ping signature: %w", err)
	}

	// 9. Update ping time and store the full ping message for later serialization
	// CRITICAL: Use ping's sigTime instead of receipt time (time.Now())
	// Legacy uses the signed sigTime for expiration/spacing calculations, not receipt time
	// This ensures consistent behavior across nodes with different network latencies
	sigTimeT := time.Unix(mnp.SigTime, 0)
	mn.mu.Lock()
	mn.LastPing = sigTimeT   // Use ping's signed time, not receipt time
	mn.LastSeen = sigTimeT   // Use ping's signed time, not receipt time
	mn.LastPingMessage = mnp // Store the full ping for broadcast serialization
	if mnp.SentinelPing {
		mn.SentinelPing = sigTimeT // Use ping's signed time for sentinel too
		mn.SentinelVersion = mnp.SentinelVersion
	}
	mn.mu.Unlock()

	// 9.1 LEGACY COMPATIBILITY (Issue #2): Update seenBroadcasts lastPing
	// Legacy C++ (masternode.cpp:868-873):
	//   pmn->lastPing = *this;
	//   CMasternodeBroadcast mnb(*pmn);
	//   uint256 hash = mnb.GetHash();
	//   if (mnodeman.mapSeenMasternodeBroadcast.count(hash)) {
	//       mnodeman.mapSeenMasternodeBroadcast[hash].lastPing = *this;
	//   }
	// This ensures peers requesting this broadcast via getdata receive fresh lastPing data
	m.updateSeenBroadcastLastPing(mn, mnp)

	// 9.5 LEGACY COMPATIBILITY: Force status check after ping update
	// Legacy: pmn->Check(true); if (!pmn->IsEnabled()) return false; (masternode.cpp:875-876)
	// After updating ping data, re-evaluate masternode status and reject if not enabled
	var utxoChecker UTXOChecker
	if m.blockchain != nil {
		utxoChecker = &blockchainUTXOChecker{bc: m.blockchain}
	}
	multiTierEnabled := m.isMultiTierEnabled()
	mn.UpdateStatusWithUTXO(sigTimeT, m.config.ExpireTime, utxoChecker, multiTierEnabled)

	// Check if masternode is enabled after status update
	mn.mu.RLock()
	status := mn.Status
	mn.mu.RUnlock()
	if status != StatusEnabled && status != StatusPreEnabled {
		m.logger.WithFields(logrus.Fields{
			"outpoint":  mnp.OutPoint.String(),
			"status":    status.String(),
			"peer_addr": peerAddr,
		}).Warn("Ping rejected: masternode not enabled after post-ping status update")
		if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
			dc.EmitPing(debug.TypePingRejected, peerAddr, fmt.Sprintf("Ping rejected for %s: masternode not enabled after status update (status=%s)", mnp.OutPoint.String(), status.String()), map[string]any{
				"outpoint": mnp.OutPoint.String(),
				"sig_time": mnp.SigTime,
				"reason":   "masternode_disabled_post_update",
				"status":   status.String(),
			})
		}
		return fmt.Errorf("masternode not enabled after ping update (status: %s)", status.String())
	}

	// Store full payload only for validated and accepted pings.
	// This keeps getdata-serving behavior while avoiding retention of invalid payloads.
	m.seenPingsMu.Lock()
	m.seenPingMessages[pingHash] = mnp
	m.seenPingsMu.Unlock()

	// 10. Relay valid ping to P2P network (only once per unique ping)
	// Legacy: CMasternodePing::CheckAndUpdate calls Relay() at the end on success
	// The seenPings check above ensures we don't relay the same ping multiple times
	// NOTE: Use lock-copy-invoke pattern to prevent race condition with SetPingRelayHandler
	m.mu.RLock()
	pingRelay := m.pingRelayFunc
	m.mu.RUnlock()
	if pingRelay != nil {
		pingRelay(mnp)
		m.logger.WithField("outpoint", mnp.OutPoint.String()).Debug("Ping relayed to P2P network")
	} else {
		m.logger.WithField("outpoint", mnp.OutPoint.String()).
			Warn("Ping accepted but NOT relayed - ping relay handler not configured")
	}

	// Emit debug event on successful ping acceptance
	if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
		dc.EmitPing("ping_accepted", peerAddr, fmt.Sprintf("Ping accepted for %s (status=%s)", mnp.OutPoint.String(), status.String()), map[string]any{
			"outpoint": mnp.OutPoint.String(),
			"status":   status.String(),
			"sig_time": mnp.SigTime,
		})
	}

	return nil
}

// CleanSeenPings removes old entries from seenPings map
// Should be called periodically to prevent memory growth
// Matching legacy: mapSeenMasternodePing entries are cleaned when sigTime exceeds tolerance
func (m *Manager) CleanSeenPings() {
	m.seenPingsMu.Lock()
	defer m.seenPingsMu.Unlock()

	// LEGACY COMPATIBILITY: Use network-adjusted time like legacy C++
	currentTime := int64(consensus.GetAdjustedTime())
	cleaned := 0
	for hash, sigTime := range m.seenPings {
		if currentTime-sigTime > m.maxSeenPingsAge {
			delete(m.seenPings, hash)
			delete(m.seenPingMessages, hash)
			cleaned++
		}
	}

	if cleaned > 0 {
		m.logger.WithField("cleaned", cleaned).Debug("Cleaned old seen pings")
	}
}

// CleanSeenBroadcasts removes old entries from seenBroadcasts map
// Should be called periodically to prevent memory growth
// Legacy: masternodeman.cpp:317-326 - removes entries when lastPing.sigTime < now - MASTERNODE_REMOVAL_SECONDS*2
func (m *Manager) CleanSeenBroadcasts() {
	m.seenBroadcastsMu.Lock()
	defer m.seenBroadcastsMu.Unlock()

	// LEGACY COMPATIBILITY: Use network-adjusted time like legacy C++
	// Legacy condition: lastPing.sigTime < GetTime() - (MASTERNODE_REMOVAL_SECONDS * 2)
	currentTime := int64(consensus.GetAdjustedTime())
	maxAge := RemovalSeconds * 2 // 15600 seconds (4.33 hours)
	cleaned := 0

	for hash, mnb := range m.seenBroadcasts {
		// Use lastPing.sigTime if available, otherwise use broadcast sigTime
		// This matches legacy C++ behavior where broadcasts track their lastPing
		var sigTime int64
		if mnb.LastPing != nil {
			sigTime = mnb.LastPing.SigTime
		} else {
			sigTime = mnb.SigTime
		}
		if currentTime-sigTime > maxAge {
			delete(m.seenBroadcasts, hash)
			cleaned++
			// Note: Legacy also clears masternodeSync.mapSeenSyncMNB here
			// Our sync manager handles its own cleanup
		}
	}

	if cleaned > 0 {
		m.logger.WithField("cleaned", cleaned).Debug("Cleaned old seen broadcasts")
	}
}

// CleanSeenMaps cleans all seen deduplication maps
// Should be called periodically (e.g., every minute) from the maintenance loop
// Legacy: CMasternodeMan::CheckAndRemove() in masternodeman.cpp:285-336
func (m *Manager) CleanSeenMaps() {
	m.CleanSeenPings()
	m.CleanSeenBroadcasts()
}

// clearSeenBroadcastsForOutpoints removes seenBroadcasts entries matching the given outpoints.
// Called when masternodes are physically removed from the map, so that re-sync can accept
// new broadcasts for those masternodes without waiting for the ~4.33h natural cleanup.
func (m *Manager) clearSeenBroadcastsForOutpoints(outpoints []types.Outpoint) {
	outpointSet := make(map[types.Outpoint]struct{}, len(outpoints))
	for _, op := range outpoints {
		outpointSet[op] = struct{}{}
	}

	m.seenBroadcastsMu.Lock()
	defer m.seenBroadcastsMu.Unlock()

	cleaned := 0
	for hash, mnb := range m.seenBroadcasts {
		if _, match := outpointSet[mnb.OutPoint]; match {
			delete(m.seenBroadcasts, hash)
			cleaned++
		}
	}

	if cleaned > 0 {
		m.logger.WithField("cleaned", cleaned).Debug("Cleared seenBroadcasts for removed masternodes")
	}
}

// MarkBroadcastSeen adds a broadcast to the seenBroadcasts deduplication map.
// Used by the dseg handler to prevent relay-bounce: when we send mnb messages via dseg,
// we must seed seenBroadcasts so that if peers relay them back, we recognize them as
// already-seen and don't reprocess/re-relay them. Without this, dseg responses create
// a relay storm because the node treats its own broadcasts as new when they return.
func (m *Manager) MarkBroadcastSeen(mnb *MasternodeBroadcast) {
	bcHash := mnb.GetHash()
	m.seenBroadcastsMu.Lock()
	m.seenBroadcasts[bcHash] = mnb
	m.seenBroadcastsMu.Unlock()
}

// HasSeenPing returns true if ping hash is already known in dedup cache.
// This is a constant-time check suitable for hot inv processing paths.
func (m *Manager) HasSeenPing(hash types.Hash) bool {
	m.seenPingsMu.RLock()
	_, exists := m.seenPings[hash]
	if !exists {
		_, exists = m.seenPingMessages[hash]
	}
	m.seenPingsMu.RUnlock()
	return exists
}

// GetPingByHash retrieves a masternode ping by its hash.
// Searches through all masternodes' LastPingMessage for a matching hash.
// Matches C++ mapSeenMasternodePing lookup used by getdata handler.
func (m *Manager) GetPingByHash(hash types.Hash) *MasternodePing {
	m.seenPingsMu.RLock()
	ping := m.seenPingMessages[hash]
	m.seenPingsMu.RUnlock()
	if ping != nil {
		return ping
	}

	// Fallback for legacy cache entries restored without full ping payload map.
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, mn := range m.masternodes {
		mn.mu.RLock()
		last := mn.LastPingMessage
		mn.mu.RUnlock()
		if last != nil && last.GetHash() == hash {
			return last
		}
	}
	return nil
}

// updateSeenBroadcastLastPing updates the lastPing in seenBroadcasts for a masternode
// LEGACY COMPATIBILITY (Issue #2): C++ updates mapSeenMasternodeBroadcast[hash].lastPing
// when processing a valid ping (masternode.cpp:868-873). This ensures that when peers
// request this broadcast via getdata, they receive the most recent ping data.
func (m *Manager) updateSeenBroadcastLastPing(mn *Masternode, ping *MasternodePing) {
	// Recreate broadcast hash from masternode state (like legacy CMasternodeBroadcast mnb(*pmn))
	// The broadcast hash is based on sigTime + pubKeyCollateral, which don't change
	mn.mu.RLock()
	sigTime := mn.SigTime
	pubKeyCollateral := mn.PubKeyCollateral
	mn.mu.RUnlock()

	if pubKeyCollateral == nil {
		return
	}

	// Create a temporary broadcast just to compute the hash
	// Legacy: CMasternodeBroadcast mnb(*pmn); uint256 hash = mnb.GetHash();
	tempBroadcast := &MasternodeBroadcast{
		SigTime:          sigTime,
		PubKeyCollateral: pubKeyCollateral,
	}
	broadcastHash := tempBroadcast.GetHash()

	// Update lastPing in seenBroadcasts if entry exists
	m.seenBroadcastsMu.Lock()
	if existingBroadcast, exists := m.seenBroadcasts[broadcastHash]; exists {
		existingBroadcast.LastPing = ping
	}
	m.seenBroadcastsMu.Unlock()
}

// GetMasternodeInfo returns masternode info for RPC
func (m *Manager) GetMasternodeInfo(outpoint types.Outpoint) (*MasternodeInfo, error) {
	mn, err := m.GetMasternode(outpoint)
	if err != nil {
		return nil, err
	}

	return mn.ToInfo(), nil
}

// GetBroadcastByHash retrieves a masternode broadcast by its hash
// The hash is computed from sigTime + pubKeyCollateral (matching legacy GetHash)
func (m *Manager) GetBroadcastByHash(hash types.Hash) (*MasternodeBroadcast, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Search for masternode with matching broadcast hash
	for _, mn := range m.masternodes {
		broadcast := mn.ToBroadcast()
		if broadcast.GetHash() == hash {
			return broadcast, nil
		}
	}

	return nil, fmt.Errorf("masternode broadcast not found for hash %s", hash.String())
}

// GetMasternodeList returns the complete masternode list
func (m *Manager) GetMasternodeList() *MasternodeList {
	m.mu.RLock()
	defer m.mu.RUnlock()

	mns := make([]*Masternode, 0, len(m.masternodes))
	for _, mn := range m.masternodes {
		mns = append(mns, mn)
	}

	return &MasternodeList{
		Masternodes: mns,
		Synced:      m.synced,
		UpdateTime:  time.Now(),
	}
}

// GetPeerAddresses returns all known masternode network addresses from the cache.
// Used by P2P layer to inject cached masternodes as priority bootstrap peers.
func (m *Manager) GetPeerAddresses() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	addrs := make([]string, 0, len(m.addressIndex))
	for addr := range m.addressIndex {
		addrs = append(addrs, addr)
	}
	return addrs
}

// updateLoop runs periodic updates for masternodes
func (m *Manager) updateLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.config.UpdateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.updateMasternodes()
			// Clean old seen messages to prevent memory growth
			// LEGACY COMPATIBILITY: Clean both seenPings and seenBroadcasts maps
			// Legacy: CMasternodeMan::CheckAndRemove() cleans mapSeenMasternodePing and mapSeenMasternodeBroadcast
			m.CleanSeenMaps()
		case <-m.stopCh:
			return
		}
	}
}

// updateMasternodes updates the status of all masternodes
// Uses UpdateStatusWithUTXO to check for spent collateral (legacy: IsOutpointSpent)
// LEGACY COMPATIBILITY: Uses GetAdjustedTime() for all time comparisons (masternode.cpp uses GetAdjustedTime)
//
// CRITICAL FIX: This function now uses a two-phase approach to avoid blocking RPC calls:
// Phase 1: Copy masternode list with RLock, do slow blockchain I/O without lock
// Phase 2: Take Lock only for quick in-memory updates (scores, payment queue)
func (m *Manager) updateMasternodes() {
	// CRITICAL: Skip updates during Initial Block Download (IBD)
	if m.syncManager != nil && !m.syncManager.IsBlockchainSynced() {
		if !m.lastUpdateSkippedIBD {
			m.lastUpdateSkippedIBD = true
			m.logger.Warn("Skipping masternode status updates - blockchain not synced (IBD)")
		}
		return
	}
	if m.lastUpdateSkippedIBD {
		m.lastUpdateSkippedIBD = false
		m.logger.Info("Resuming masternode status updates - blockchain synced")
	}

	// LEGACY COMPATIBILITY FIX: Use network-adjusted time instead of system clock
	currentTime := adjustedtime.GetAdjustedTime()

	// Get block hash for scoring BEFORE taking any lock (blockchain I/O)
	blockHash := types.ZeroHash
	if m.blockchain != nil {
		if h, err := m.blockchain.GetBestHeight(); err == nil && h > ScoreBlockDepth {
			scoreHeight := h - ScoreBlockDepth
			if block, err := m.blockchain.GetBlockByHeight(scoreHeight); err == nil && block != nil {
				blockHash = block.Hash()
			}
		}
	}

	// Create UTXO checker if blockchain is available
	var utxoChecker UTXOChecker
	if m.blockchain != nil {
		utxoChecker = &blockchainUTXOChecker{bc: m.blockchain}
	}

	// PHASE 1: Copy masternode pointers with brief RLock
	m.mu.RLock()
	mnList := make([]*Masternode, 0, len(m.masternodes))
	for _, mn := range m.masternodes {
		mnList = append(mnList, mn)
	}
	multiTierEnabled := m.isMultiTierEnabledLocked()
	m.mu.RUnlock()

	// PHASE 2: Do slow blockchain I/O WITHOUT holding manager lock
	// UpdateStatusWithUTXO only takes mn.mu (individual masternode lock)
	// Collect REMOVED/VIN_SPENT masternodes for physical removal (matches C++ CheckAndRemove)
	var removedOutpoints []types.Outpoint
	for _, mn := range mnList {
		// Capture status before update for debug event emission
		mn.mu.RLock()
		prevStatus := mn.Status
		mn.mu.RUnlock()

		mn.UpdateStatusWithUTXO(currentTime, m.config.ExpireTime, utxoChecker, multiTierEnabled)

		mn.mu.RLock()
		status := mn.Status
		mn.mu.RUnlock()

		// Log and emit debug event on status change
		if status != prevStatus {
			fields := logrus.Fields{
				"outpoint":    mn.OutPoint.String(),
				"prev_status": prevStatus.String(),
				"new_status":  status.String(),
			}

			// Add diagnostic reason based on new status
			currentUnix := currentTime.Unix()
			mn.mu.RLock()
			var lastPingSigTime int64
			if mn.LastPingMessage != nil {
				lastPingSigTime = mn.LastPingMessage.SigTime
			} else {
				lastPingSigTime = mn.SigTime
			}
			mn.mu.RUnlock()

			switch status {
			case StatusExpired:
				pingAge := currentUnix - lastPingSigTime
				fields["last_ping_age_s"] = pingAge
				fields["expiration_s"] = ExpirationSeconds
				fields["reason"] = "not pinged within expiration window"
			case StatusRemoved:
				pingAge := currentUnix - lastPingSigTime
				fields["last_ping_age_s"] = pingAge
				fields["removal_s"] = RemovalSeconds
				fields["reason"] = "not pinged within removal window"
			case StatusVinSpent, StatusOutpointSpent:
				fields["reason"] = "collateral UTXO spent or invalid"
			case StatusPreEnabled:
				fields["reason"] = "ping-broadcast sigTime gap below minimum"
			}

			m.logger.WithFields(fields).Info("Masternode status changed")

			if dc := m.debugCollector.Load(); dc != nil && dc.IsEnabled() {
				dc.EmitStatus(
					"status_update",
					mn.OutPoint.String(),
					prevStatus.String()+" -> "+status.String(),
					map[string]any{
						"outpoint":   mn.OutPoint.String(),
						"prevStatus": prevStatus.String(),
						"newStatus":  status.String(),
					},
				)
			}
		}

		if status == StatusRemoved || status == StatusVinSpent || status == StatusOutpointSpent {
			removedOutpoints = append(removedOutpoints, mn.OutPoint)
		}
	}

	// PHASE 2a: Physical removal of REMOVED/VIN_SPENT masternodes from the map
	// Matches C++ CMasternodeMan::CheckAndRemove (masternodeman.cpp:315-333)
	// C++ erases masternodes with MASTERNODE_REMOVE or MASTERNODE_VIN_SPENT status
	if len(removedOutpoints) > 0 {
		var actuallyRemoved []types.Outpoint
		m.mu.Lock()
		for _, outpoint := range removedOutpoints {
			if mn, exists := m.masternodes[outpoint]; exists {
				// Re-check status under lock (may have been updated by a concurrent broadcast)
				mn.mu.RLock()
				status := mn.Status
				mn.mu.RUnlock()
				if status == StatusRemoved || status == StatusVinSpent || status == StatusOutpointSpent {
					delete(m.masternodes, outpoint)
					delete(m.addressIndex, mn.Addr.String())
					if mn.PubKey != nil {
						delete(m.pubkeyIndex, mn.PubKey.Hex())
					}
					actuallyRemoved = append(actuallyRemoved, outpoint)
					m.logger.WithFields(logrus.Fields{
						"outpoint": outpoint.String(),
						"status":   status.String(),
					}).Warn("Masternode physically removed from list")
				}
			}
		}
		m.mu.Unlock()

		// Clear seenBroadcasts only for masternodes that were actually removed (not recovered by concurrent broadcast)
		// Without this, seenBroadcasts dedup blocks re-sync for ~4.33 hours (RemovalSeconds*2)
		if len(actuallyRemoved) > 0 {
			m.clearSeenBroadcastsForOutpoints(actuallyRemoved)
		}
	}

	// PHASE 2b: Update scores WITHOUT manager lock
	// NOTE: CalculateScore takes RLock internally, so we must NOT hold Lock when calling it
	// Calculate first, then take Lock only for the brief assignment
	for _, mn := range mnList {
		// Skip removed masternodes (already deleted from map)
		mn.mu.RLock()
		status := mn.Status
		mn.mu.RUnlock()
		if status == StatusRemoved || status == StatusVinSpent || status == StatusOutpointSpent {
			continue
		}
		score := mn.CalculateScore(blockHash) // Takes RLock internally
		mn.mu.Lock()
		mn.Score = score
		mn.ScoreCompact = float64(binary.LittleEndian.Uint64(score[0:8]))
		mn.mu.Unlock()
	}

	// PHASE 3: Take Lock ONLY for rebuildPaymentQueue (very brief)
	m.mu.Lock()
	m.rebuildPaymentQueue()
	m.mu.Unlock()
}

// blockchainUTXOChecker implements UTXOChecker using blockchain.GetUTXO
type blockchainUTXOChecker struct {
	bc blockchain.Blockchain
}

// IsUTXOSpent checks if a UTXO has been spent by checking if it exists
// Legacy: Uses CCoinsViewCache to check if vin.prevout exists
func (c *blockchainUTXOChecker) IsUTXOSpent(outpoint types.Outpoint) (bool, error) {
	utxo, err := c.bc.GetUTXO(outpoint)
	if err != nil {
		// Error could mean spent or database issue
		// Treat as spent to be safe (conservative approach)
		return true, err
	}
	// UTXO nil means spent
	return utxo == nil, nil
}

// GetUTXOValue returns the value of a UTXO in satoshis
// Legacy: coins->vout[vin.prevout.n].nValue for collateral validation
func (c *blockchainUTXOChecker) GetUTXOValue(outpoint types.Outpoint) (int64, error) {
	utxo, err := c.bc.GetUTXO(outpoint)
	if err != nil {
		return 0, err
	}
	if utxo == nil {
		return 0, fmt.Errorf("UTXO not found (spent)")
	}
	if utxo.Output == nil {
		return 0, fmt.Errorf("UTXO has nil output")
	}
	return utxo.Output.Value, nil
}

// rebuildPaymentQueue rebuilds the payment queue based on scores
func (m *Manager) rebuildPaymentQueue() {
	// Collect active masternodes
	active := make([]*Masternode, 0)
	for _, mn := range m.masternodes {
		if mn.IsActive() {
			active = append(active, mn)
		}
	}

	// Sort by score (higher score = paid sooner)
	// Secondary sort by last payment time (older = paid sooner)
	sort.Slice(active, func(i, j int) bool {
		// First compare scores (higher score first)
		// CRITICAL: Use CompareTo which compares from most significant byte (like C++ uint256)
		// NOT bytes.Compare which compares from least significant byte first
		scoreCmp := active[i].Score.CompareTo(active[j].Score)
		if scoreCmp != 0 {
			return scoreCmp > 0 // Higher score first
		}

		// If scores are equal, compare last payment time
		lastPaidI := m.lastPaid[active[i].OutPoint]
		lastPaidJ := m.lastPaid[active[j].OutPoint]

		// If neither has been paid, sort by outpoint (deterministic)
		if lastPaidI.IsZero() && lastPaidJ.IsZero() {
			return active[i].OutPoint.String() < active[j].OutPoint.String()
		}

		// Sort by last payment time (older first)
		return lastPaidI.Before(lastPaidJ)
	})

	// Update payment queue
	m.paymentQueue.mu.Lock()
	oldLen := len(m.paymentQueue.queue)
	m.paymentQueue.queue = active
	newLen := len(active)

	// Reset or clamp paymentPos if queue size changed
	if newLen == 0 {
		m.paymentQueue.paymentPos = 0
	} else if m.paymentQueue.paymentPos >= newLen {
		// Clamp to valid range (wrap around to start)
		m.paymentQueue.paymentPos = 0
	}
	m.paymentQueue.mu.Unlock()

	m.logger.WithFields(logrus.Fields{
		"old_size": oldLen,
		"new_size": newLen,
		"position": m.paymentQueue.paymentPos,
	}).Debug("Payment queue rebuilt")
}

// P2P Interface Implementation
// These methods are required by internal/p2p/server.go:MasternodeManager interface

// GetPublicKey returns the public key bytes for a masternode
func (m *Manager) GetPublicKey(outpoint types.Outpoint) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	mn, exists := m.masternodes[outpoint]
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrMasternodeNotFound, outpoint.String())
	}

	if mn.PubKey == nil {
		return nil, fmt.Errorf("masternode has no public key: %s", outpoint.String())
	}

	// Return serialized public key bytes
	return mn.PubKey.SerializeCompressed(), nil
}

// IsActive checks if a masternode is active at a given block height
func (m *Manager) IsActive(outpoint types.Outpoint, height uint32) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	mn, exists := m.masternodes[outpoint]
	if !exists {
		return false
	}

	// Check if masternode is in active status
	if !mn.IsActive() {
		return false
	}

	// Check if masternode was active at this height
	// If blockHeight is stored, use it for historical checks
	if mn.BlockHeight > 0 && height < uint32(mn.BlockHeight) {
		return false
	}

	return true
}

// GetTier returns the tier of a masternode
func (m *Manager) GetTier(outpoint types.Outpoint) (uint8, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	mn, exists := m.masternodes[outpoint]
	if !exists {
		return 0, fmt.Errorf("%w: %s", ErrMasternodeNotFound, outpoint.String())
	}

	// Return tier as uint8 (0=Bronze, 1=Silver, 2=Gold, 3=Platinum)
	return uint8(mn.Tier), nil
}

// GetPaymentQueuePosition returns the position in the payment queue
func (m *Manager) GetPaymentQueuePosition(outpoint types.Outpoint, height uint32) (int, error) {
	m.paymentQueue.mu.RLock()
	defer m.paymentQueue.mu.RUnlock()

	// Find position in payment queue
	for i, mn := range m.paymentQueue.queue {
		if mn.OutPoint == outpoint {
			return i, nil
		}
	}

	return -1, fmt.Errorf("masternode not in payment queue: %s", outpoint.String())
}

// GetLastPaidBlock returns the block height where masternode was last paid.
// Legacy: CMasternode::GetLastPaid() from masternode.cpp:313-359
// This now scans payment votes using HasPayeeWithVotes(payee, 2) logic when available.
// Returns 0 if masternode has never been paid.
func (m *Manager) GetLastPaidBlock(outpoint types.Outpoint) (uint32, error) {
	m.mu.RLock()
	mn, exists := m.masternodes[outpoint]
	pvp := m.paymentVotesProvider
	bc := m.blockchain
	m.mu.RUnlock()

	if !exists {
		return 0, fmt.Errorf("%w: %s", ErrMasternodeNotFound, outpoint.String())
	}

	// LEGACY COMPATIBILITY: If payment votes provider available, scan blockchain
	// Legacy: for (unsigned int i = 1; BlockReading && BlockReading->nHeight > 0; i++)
	//         if (masternodePayments.mapMasternodeBlocks[BlockReading->nHeight].HasPayeeWithVotes(payee, 2))
	if pvp != nil && bc != nil {
		tipHeight, err := bc.GetBestHeight()
		if err == nil {
			payeeScript := mn.GetPayeeScript()
			if payeeScript != nil {
				// LEGACY COMPATIBILITY FIX: Use CountMillionsLocked() for weighted count
				// Legacy (masternode.cpp:333): int nMnCount = mnodeman.CountMillionsLocked() * 1.25;
				// CountMillionsLocked returns tier-weighted count (Platinum=100, Gold=20, Silver=5, Bronze=1)
				// NOT simple len(masternodes) - this is CRITICAL for multi-tier networks
				// Legacy does NOT have a minimum floor - we must match exactly
				currentTime := time.Now()
				millionsLocked := m.CountMillionsLocked(0, currentTime, nil) // 0 sigTime = count all
				scanLimit := millionsLocked * 125 / 100
				// No minimum floor - legacy doesn't have one (masternode.cpp:333-337)

				// Scan backwards from tip looking for payment with >= 2 votes
				scanned := 0
				for height := tipHeight; height > 0 && scanned < scanLimit; height-- {
					scanned++
					if pvp.HasPayeeWithVotesAtHeight(height, payeeScript, 2) {
						return height, nil
					}
				}
			}
		}
	}

	// Fallback: Use the tracked lastPaid timestamp
	lastPaidTime, hasLastPaid := m.lastPaid[outpoint]
	if !hasLastPaid || lastPaidTime.IsZero() {
		if mn.LastPaid.IsZero() {
			return 0, nil // Never paid
		}
		lastPaidTime = mn.LastPaid
	}

	// Convert timestamp to approximate block height
	if m.blockchain != nil {
		currentHeight, err := m.blockchain.GetBestHeight()
		if err != nil {
			return 0, nil
		}
		now := time.Now()
		secsSincePaid := now.Sub(lastPaidTime).Seconds()
		blocksSincePaid := uint32(secsSincePaid / 60)
		if blocksSincePaid >= currentHeight {
			return 0, nil
		}
		return currentHeight - blocksSincePaid, nil
	}

	return 0, nil
}

// GetLastPaid returns the last payment timestamp with deterministic offset.
// Legacy: CMasternode::GetLastPaid() from masternode.cpp:313-359
// Returns 0 if masternode has never been paid within the search range.
func (m *Manager) GetLastPaid(outpoint types.Outpoint) int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	mn, exists := m.masternodes[outpoint]
	if !exists {
		return 0
	}

	// Calculate deterministic offset to break ties (matches legacy: 2.5 minutes max)
	// Legacy: hash(vin + sigTime).GetCompact(false) % 150
	buf := make([]byte, 0, 49)
	buf = append(buf, mn.OutPoint.Hash[:]...)
	indexBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(indexBytes, mn.OutPoint.Index)
	buf = append(buf, indexBytes...)
	buf = append(buf, 0x00)                   // scriptSig length (empty)
	buf = append(buf, 0xFF, 0xFF, 0xFF, 0xFF) // nSequence
	sigTimeBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(sigTimeBytes, uint64(mn.SigTime))
	buf = append(buf, sigTimeBytes...)

	// Double SHA256
	first := sha256.Sum256(buf)
	second := sha256.Sum256(first[:])
	var hash types.Hash
	copy(hash[:], second[:])
	nOffset := int64(hash.GetCompact() % 150)

	// Check tracked lastPaid time
	lastPaidTime, hasLastPaid := m.lastPaid[outpoint]
	if !hasLastPaid || lastPaidTime.IsZero() {
		if mn.LastPaid.IsZero() {
			return 0
		}
		lastPaidTime = mn.LastPaid
	}

	// Return block time + deterministic offset
	return lastPaidTime.Unix() + nOffset
}

// StoreWinnerVote stores a validated winner vote for persistence in mnpayments.dat.
// Legacy: Stores in mapMasternodePayeeVotes for CMasternodePaymentDB::Write
func (m *Manager) StoreWinnerVote(voterOutpoint types.Outpoint, blockHeight uint32, payeeScript, signature []byte) {
	// Calculate vote hash for deduplication (matches legacy GetHash())
	buf := make([]byte, 0, 100)
	buf = append(buf, voterOutpoint.Hash[:]...)
	indexBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(indexBytes, voterOutpoint.Index)
	buf = append(buf, indexBytes...)
	heightBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(heightBytes, blockHeight)
	buf = append(buf, heightBytes...)
	buf = append(buf, payeeScript...)

	first := sha256.Sum256(buf)
	second := sha256.Sum256(first[:])
	var voteHash types.Hash
	copy(voteHash[:], second[:])

	// Store the winner vote
	m.winnerVotesMu.Lock()
	m.winnerVotes[voteHash] = &PaymentWinnerCacheEntry{
		VoterOutpoint: voterOutpoint,
		BlockHeight:   blockHeight,
		PayeeScript:   payeeScript,
		Signature:     signature,
	}
	m.winnerVotesMu.Unlock()

	// Also mark as seen for deduplication
	m.seenWinnersMu.Lock()
	m.seenWinners[voteHash] = time.Now().Unix()
	m.seenWinnersMu.Unlock()

	// LEGACY COMPAT: Add to masternodeBlocks for vote aggregation
	// C++ Reference: masternode-payments.cpp:558-563 (AddWinningMasternode)
	m.addWinningMasternode(payeeScript, blockHeight)

	if m.logger != nil {
		m.logger.WithFields(map[string]interface{}{
			"vote_hash":    voteHash.String(),
			"block_height": blockHeight,
			"voter":        voterOutpoint.String(),
		}).Debug("Stored winner vote for persistence")
	}
}

// AddWinningMasternode adds a payee to the vote aggregation for a block height
// LEGACY COMPATIBILITY: Matches CMasternodePayments::AddWinningMasternode()
// C++ Reference: masternode-payments.cpp:558-563
//
// This validates block existence and adds the payee to masternodeBlocks.
// Returns error if validation fails, nil on success.
func (m *Manager) AddWinningMasternode(payeeScript []byte, blockHeight uint32) error {
	// LEGACY COMPAT: Verify block at height-100 exists (if applicable)
	// C++ Reference: masternode-payments.cpp:558-563
	// Legacy uses BlockReading to traverse from tip, we verify via GetBlockByHeight
	if blockHeight >= ScoreBlockDepth && m.blockchain != nil {
		checkHeight := blockHeight - ScoreBlockDepth
		_, err := m.blockchain.GetBlockByHeight(checkHeight)
		if err != nil {
			return fmt.Errorf("block at height %d not found: %w", checkHeight, err)
		}
	}

	m.addWinningMasternode(payeeScript, blockHeight)
	return nil
}

// addWinningMasternode is the internal implementation without validation
// Must be called after any needed validation is done
func (m *Manager) addWinningMasternode(payeeScript []byte, blockHeight uint32) {
	m.masternodeBlocksMu.Lock()
	defer m.masternodeBlocksMu.Unlock()

	// Get or create block payees entry
	blockPayees, exists := m.masternodeBlocks[blockHeight]
	if !exists {
		blockPayees = NewMasternodeBlockPayees(blockHeight)
		m.masternodeBlocks[blockHeight] = blockPayees
	}

	// Add the payee vote (increment by 1)
	blockPayees.AddPayee(payeeScript, 1)
}

// ==================== Consensus Interface Methods ====================
// These methods implement the consensus.MasternodeInterface required for
// PoS payment validation

// GetActiveCount returns the number of active masternodes
func (m *Manager) GetActiveCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, mn := range m.masternodes {
		if mn.IsActive() {
			count++
		}
	}
	return count
}

// GetBestHeight returns the current chain tip height for vote window validation.
// LEGACY COMPATIBILITY: Used in ProcessMessageMasternodePayments to validate vote height window.
// Legacy C++ (masternode-payments.cpp:453-457) uses nHeight (current tip) for window calculation:
//
//	int nFirstBlock = nHeight - (mnodeman.CountEnabled() * 1.25);
//	if (winner.nBlockHeight < nFirstBlock || winner.nBlockHeight > nHeight + 20) return;
func (m *Manager) GetBestHeight() (uint32, error) {
	// No lock needed - we're just delegating to blockchain which has its own locking
	if m.blockchain == nil {
		return 0, fmt.Errorf("blockchain not initialized")
	}
	return m.blockchain.GetBestHeight()
}

// GetMasternodeByOutpoint returns masternode info by outpoint for consensus validation
func (m *Manager) GetMasternodeByOutpoint(outpoint types.Outpoint) (consensus.MasternodeInfo, error) {
	mn, err := m.GetMasternode(outpoint)
	if err != nil {
		return consensus.MasternodeInfo{}, err
	}

	// Convert to consensus.MasternodeInfo format
	info := consensus.MasternodeInfo{
		Outpoint:        mn.OutPoint,
		Tier:            int(mn.Tier),
		PayAddress:      mn.GetPayeeScript(), // Use script bytes, not address string
		ProtocolVersion: int(mn.Protocol),
		LastPaid:        uint32(mn.BlockHeight),
		Score:           mn.ScoreCompact, // Use compact score for consensus interface
		PubKey:          mn.PubKey.SerializeCompressed(),
	}

	return info, nil
}

// GetNextPaymentWinner returns the masternode that should receive payment for the given block
// CRITICAL: Must use blockHeight parameter for deterministic selection (not current tip!)
// Legacy: GetNextMasternodeInQueueForPayment(nBlockHeight, ...) at masternodeman.cpp:562
func (m *Manager) GetNextPaymentWinner(blockHeight uint32, blockHash types.Hash) (consensus.MasternodeInfo, error) {
	// CRITICAL FIX: Use the provided blockHeight, not current tip
	// Legacy passes nBlockHeight into GetNextMasternodeInQueueForPayment for historical blocks
	mn, _ := m.GetNextMasternodeInQueueForPayment(blockHeight, true)
	if mn == nil {
		return consensus.MasternodeInfo{}, fmt.Errorf("no masternodes eligible for payment at height %d", blockHeight)
	}

	// Legacy C++ uses blockHeight - 100 for score calculation (deterministic ordering)
	// This ensures consensus on payment winner across all nodes
	scoreBlockHash := blockHash // fallback if blockchain not available
	if m.blockchain != nil && blockHeight > ScoreBlockDepth {
		scoreHeight := blockHeight - ScoreBlockDepth
		if block, err := m.blockchain.GetBlockByHeight(scoreHeight); err == nil && block != nil {
			scoreBlockHash = block.Hash()
		}
	}

	// Return in consensus.MasternodeInfo format
	// Use compact score (float64) for consensus.MasternodeInfo API compatibility
	info := consensus.MasternodeInfo{
		Outpoint:        mn.OutPoint,
		Tier:            int(mn.Tier),
		PayAddress:      mn.GetPayeeScript(), // Use script bytes, not address string
		ProtocolVersion: int(mn.Protocol),
		LastPaid:        uint32(mn.BlockHeight),
		Score:           mn.CalculateScoreCompact(scoreBlockHash),
		PubKey:          mn.PubKey.SerializeCompressed(),
	}

	return info, nil
}

// IsActiveAtHeight checks if a masternode was active at a specific block height
// This version only checks historical registration state (correct semantics for vote validation).
// For legacy C++ compatibility with UTXO validation, use IsActiveAtHeightLegacy instead.
func (m *Manager) IsActiveAtHeight(outpoint types.Outpoint, height uint32) bool {
	return m.isActiveAtHeightInternal(outpoint, height, false)
}

// IsActiveAtHeightLegacy checks if a masternode was active at a specific block height
// WITH current UTXO validation to match legacy C++ behavior.
//
// LEGACY COMPATIBILITY: C++ calls mn.Check() in GetMasternodeRank which validates CURRENT
// UTXO state. This is actually a legacy bug - it rejects valid historical votes if UTXO
// was spent after voting but before validation. However, for network consensus we must
// match this behavior exactly.
//
// Use this function when validating MNW (masternode winner) votes to match legacy behavior.
func (m *Manager) IsActiveAtHeightLegacy(outpoint types.Outpoint, height uint32) bool {
	return m.isActiveAtHeightInternal(outpoint, height, true)
}

// isActiveAtHeightInternal is the shared implementation for IsActiveAtHeight variants
func (m *Manager) isActiveAtHeightInternal(outpoint types.Outpoint, height uint32, validateCurrentUTXO bool) bool {
	m.mu.RLock()
	mn, exists := m.masternodes[outpoint]
	if !exists {
		m.mu.RUnlock()
		return false
	}
	blockchain := m.blockchain
	m.mu.RUnlock()

	// LEGACY COMPATIBILITY FIX: When validateCurrentUTXO=true, check current UTXO state
	// This matches legacy C++ behavior where mn.Check() is called in GetMasternodeRank
	// even when validating historical votes. This is a legacy bug but required for consensus.
	if validateCurrentUTXO && blockchain != nil {
		utxoChecker := &blockchainUTXOChecker{bc: blockchain}
		multiTierEnabled := m.isMultiTierEnabled()
		currentTime := adjustedtime.GetAdjustedTime()

		// Call UpdateStatusWithUTXO to match legacy mn.Check() behavior
		mn.UpdateStatusWithUTXO(currentTime, m.config.ExpireTime, utxoChecker, multiTierEnabled)

		mn.mu.RLock()
		status := mn.Status
		mn.mu.RUnlock()

		// If UTXO is spent or masternode expired, reject (matches legacy)
		if status != StatusEnabled && status != StatusPreEnabled {
			return false
		}
	}

	mn.mu.RLock()
	defer mn.mu.RUnlock()

	// Check if masternode was registered at or before the requested height
	if mn.ActiveHeight == 0 || mn.ActiveHeight > height {
		// Masternode was not yet active at this height
		return false
	}

	// Check sigTime - masternode must have been announced before the block at given height
	// Use actual block timestamp if available, otherwise fall back to approximation
	var heightTime int64
	if blockchain != nil {
		if block, err := blockchain.GetBlockByHeight(height); err == nil && block != nil {
			heightTime = int64(block.Header.Timestamp)
		}
	}
	if heightTime == 0 {
		// Fallback: approximate using 60 seconds per block from genesis
		heightTime = int64(height) * 60
	}

	// Allow 2 hour tolerance for network time drift
	if mn.SigTime > heightTime+7200 {
		return false
	}

	// Masternode was registered and active at the given height
	return true
}

// GetMasternodePublicKey returns the public key of a masternode for signature verification
func (m *Manager) GetMasternodePublicKey(outpoint types.Outpoint) ([]byte, error) {
	return m.GetPublicKey(outpoint)
}

// GetMasternodeByPayAddress finds a masternode by its payment script (P2PKH scriptPubKey)
// Used for payment queue advancement when votes determine the payee instead of queue calculation
func (m *Manager) GetMasternodeByPayAddress(payAddress []byte) (consensus.MasternodeInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Search all masternodes for matching pay address
	for _, mn := range m.masternodes {
		mnScript := mn.GetPayeeScript()
		if bytes.Equal(mnScript, payAddress) {
			return consensus.MasternodeInfo{
				Outpoint:        mn.OutPoint,
				Tier:            int(mn.Tier),
				PayAddress:      mnScript,
				ProtocolVersion: int(mn.Protocol),
				LastPaid:        uint32(mn.LastPaid.Unix()),
				Score:           mn.ScoreCompact,
			}, nil
		}
	}

	return consensus.MasternodeInfo{}, fmt.Errorf("%w for pay address", ErrMasternodeNotFound)
}

// FindByPubKey finds a masternode by its operator public key
func (m *Manager) FindByPubKey(pubKey *crypto.PublicKey) *Masternode {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if pubKey == nil {
		return nil
	}

	// Use pubkey index for O(1) lookup
	if mn, exists := m.pubkeyIndex[pubKey.Hex()]; exists {
		return mn
	}

	return nil
}

// ResetSync resets the masternode sync state, clearing all data and allowing resync
func (m *Manager) ResetSync() {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Clear all masternode data
	m.masternodes = make(map[types.Outpoint]*Masternode)
	m.addressIndex = make(map[string]*Masternode)
	m.pubkeyIndex = make(map[string]*Masternode)

	// Clear payment tracking
	m.lastPaid = make(map[types.Outpoint]time.Time)

	// Reset payment queue
	m.paymentQueue.mu.Lock()
	m.paymentQueue.queue = make([]*Masternode, 0)
	m.paymentQueue.lastPaid = make(map[types.Outpoint]time.Time)
	m.paymentQueue.paymentPos = 0
	m.paymentQueue.mu.Unlock()

	// Mark as not synced to trigger resync
	m.synced = false

	m.logger.Info("Masternode sync reset - all data cleared, awaiting resync")
}
