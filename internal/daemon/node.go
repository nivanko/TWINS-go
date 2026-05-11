package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/sirupsen/logrus"

	"github.com/twins-dev/twins-core/internal/blockchain"
	"github.com/twins-dev/twins-core/internal/config"
	"github.com/twins-dev/twins-core/internal/consensus"
	"github.com/twins-dev/twins-core/internal/masternode"
	"github.com/twins-dev/twins-core/internal/masternode/debug"
	"github.com/twins-dev/twins-core/internal/mempool"
	"github.com/twins-dev/twins-core/internal/p2p"
	"github.com/twins-dev/twins-core/internal/rpc"
	"github.com/twins-dev/twins-core/internal/spork"
	"github.com/twins-dev/twins-core/internal/storage"
	"github.com/twins-dev/twins-core/internal/storage/binary"
	adjustedtime "github.com/twins-dev/twins-core/internal/time"
	"github.com/twins-dev/twins-core/internal/wallet"
	"github.com/twins-dev/twins-core/pkg/types"
)

// NodeConfig provides configuration for creating a new Node.
type NodeConfig struct {
	Network    string          // "mainnet", "testnet", or "regtest"
	DataDir    string          // Data directory path
	Logger     *logrus.Entry   // Logger instance (nil → default)
	OnProgress func(phase string, pct float64) // Optional progress callback for GUI (nil for daemon)
	Reindex    bool            // Clear database before opening

	// Performance tuning (0 = use defaults)
	Workers           int // Number of worker goroutines
	ValidationWorkers int // Number of block validation workers
	DbCacheMB         int // Database cache size in MB

	// ConfigManager is the unified configuration authority (optional, nil for GUI until Phase 3)
	ConfigManager *config.ConfigManager

	// Masternode debug (zero-cost when disabled)
	MasternodeDebug         bool // Enable debug event collection
	MasternodeDebugMaxMB    int  // Max JSONL file size before rotation
	MasternodeDebugMaxFiles int  // Max rotated files to keep
}

// Node manages the full lifecycle of a TWINS daemon.
// Both twinsd and twins-gui use Node to ensure identical initialization.
type Node struct {
	Config      NodeConfig
	ChainParams *types.ChainParams

	// Core components (set by NewNode)
	Storage         storage.Storage
	Blockchain      blockchain.Blockchain
	Consensus       consensus.Engine
	Mempool         mempool.Mempool
	Masternode      *masternode.Manager
	Spork           *spork.Manager
	// DebugCollector is the masternode debug event collector (nil-pointer when disabled).
	// Stored as atomic.Pointer so GUI handlers and other lock-free readers can
	// observe the current collector without taking n.mu, while the subscriber
	// path still uses n.mu for ordering with shuttingDown.
	DebugCollector  atomic.Pointer[debug.Collector]
	PaymentTracker  *masternode.PaymentTracker // In-memory masternode payment statistics

	// Configuration authority (optional, nil for GUI until Phase 3)
	ConfigManager *config.ConfigManager

	// Runtime components (set by Init* methods)
	Wallet         *wallet.Wallet
	P2PServer      *p2p.Server
	Syncer         *p2p.BlockchainSyncer
	RPCServer      *rpc.Server
	MasternodeConf *masternode.MasternodeConfFile

	// Internal
	mu            sync.RWMutex
	shutdownOnce  sync.Once
	shuttingDown  atomic.Bool // Set true at the start of doShutdown so post-shutdown subscriber callbacks can bail out before allocating new resources
	rpcConfig     *rpc.Config // Stored for cleanup during shutdown
	logger        *logrus.Entry
}

// NewNode creates a Node with all core components initialized.
// This replaces:
//   - daemon.InitializeCoreComponents()
//   - core.InitializeDaemonWithProgress()
//   - startup_improved.initializeCoreComponents()
//
// Bug fixes applied by this unified path:
//  1. adjustedtime.InitGlobalTimeData() — always called
//  2. Spork storage — always uses real PebbleStorage (never nil)
//  3. Masternode network type — always set from config
//  4. Payment validator spork manager — always wired
//  5. Payment vote storage — always initialized
//  6. Masternode notifier — always wired to blockchain
func NewNode(cfg NodeConfig) (*Node, error) {
	if cfg.Logger == nil {
		cfg.Logger = logrus.NewEntry(logrus.StandardLogger())
	}
	logger := cfg.Logger

	// Resolve chain params
	chainParams, err := ResolveChainParams(cfg.Network)
	if err != nil {
		return nil, err
	}

	n := &Node{
		Config:        cfg,
		ChainParams:   chainParams,
		ConfigManager: cfg.ConfigManager,
		logger:        logger,
	}

	progress := func(phase string, pct float64) {
		if cfg.OnProgress != nil {
			cfg.OnProgress(phase, pct)
		}
	}

	// BUG FIX #1: Initialize global adjusted time data.
	// GUI path (daemon_initializer.go) was missing this entirely.
	adjustedtime.InitGlobalTimeData(logrus.StandardLogger())

	// --- Phase: Storage ---
	progress("storage", 0)

	dbPath := filepath.Join(cfg.DataDir, "blockchain.db")
	storageConfig := storage.DefaultStorageConfig()
	storageConfig.Path = dbPath

	// Apply CLI db-cache override if provided.
	// Maintain default 2:1 ratio between application cache and block cache.
	if cfg.DbCacheMB > 0 {
		storageConfig.BlockCacheSize = int64(cfg.DbCacheMB)
		storageConfig.CacheSize = int64(cfg.DbCacheMB) * 2
	}

	// Log CPU count and effective performance settings.
	// Note: Workers/ValidationWorkers are available for future per-component
	// override; existing components already fall back to runtime.NumCPU().
	logger.WithFields(logrus.Fields{
		"cpus":              runtime.NumCPU(),
		"workers":           cfg.Workers,
		"validation_workers": cfg.ValidationWorkers,
		"block_cache_mb":    storageConfig.BlockCacheSize,
		"app_cache_mb":      storageConfig.CacheSize,
		"write_buffer_mb":   storageConfig.WriteBufferSize,
		"max_open_files":    storageConfig.MaxOpenFiles,
	}).Info("Performance settings")

	// Handle reindex: clear database before opening
	if cfg.Reindex {
		logger.Warn("REINDEX requested - clearing blockchain database...")
		if err := os.RemoveAll(dbPath); err != nil {
			return nil, fmt.Errorf("failed to clear database for reindex: %w", err)
		}
	}

	progress("storage", 30)
	stor, err := binary.NewBinaryStorage(storageConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize storage: %w", err)
	}
	n.Storage = stor

	progress("storage", 100)

	// --- Phase: Genesis ---
	progress("genesis", 0)
	if err := EnsureGenesisBlock(stor, chainParams, logger); err != nil {
		stor.Close()
		return nil, fmt.Errorf("failed to ensure genesis block: %w", err)
	}
	progress("genesis", 100)

	// --- Phase: Consensus ---
	progress("consensus", 0)
	consensusEngine := consensus.NewProofOfStake(stor, chainParams, logrus.StandardLogger())
	if err := consensusEngine.Start(context.Background()); err != nil {
		stor.Close()
		return nil, fmt.Errorf("failed to start consensus engine: %w", err)
	}
	n.Consensus = consensusEngine
	progress("consensus", 100)

	// --- Phase: Blockchain ---
	progress("blockchain", 0)
	blockchainConfig := blockchain.DefaultConfig()
	blockchainConfig.Storage = stor
	blockchainConfig.Consensus = consensusEngine
	blockchainConfig.ChainParams = chainParams
	blockchainConfig.Network = cfg.Network

	// Apply sync.ibdThreshold from config if available
	if cfg.ConfigManager != nil {
		if v := cfg.ConfigManager.GetUint32("sync.ibdThreshold"); v > 0 {
			blockchainConfig.IBDThreshold = v
		}
	}

	bc, err := blockchain.New(blockchainConfig)
	if err != nil {
		consensusEngine.Stop()
		stor.Close()
		return nil, fmt.Errorf("failed to initialize blockchain: %w", err)
	}
	n.Blockchain = bc
	progress("blockchain", 100)

	// --- Phase: Mempool ---
	progress("mempool", 0)
	mempoolConfig := mempool.DefaultConfig()
	mempoolConfig.Blockchain = bc
	mempoolConfig.Consensus = consensusEngine
	mempoolConfig.ChainParams = chainParams
	mempoolConfig.UTXOSet = bc

	mp, err := mempool.New(mempoolConfig)
	if err != nil {
		consensusEngine.Stop()
		stor.Close()
		return nil, fmt.Errorf("failed to initialize mempool: %w", err)
	}
	n.Mempool = mp
	bc.SetMempool(mp)
	progress("mempool", 100)

	// --- Phase: Spork ---
	progress("spork", 0)

	// BUG FIX #2: Always use real PebbleStorage for spork persistence.
	// GUI path (daemon_initializer.go:244) passed nil, causing sporks to be lost on restart.
	sporkStorage := spork.NewPebbleStorage(stor.GetDB())

	sporkManager, err := spork.NewManager(chainParams.SporkPubKey, chainParams.SporkPubKeyOld, sporkStorage)
	if err != nil {
		mp.Stop()
		consensusEngine.Stop()
		stor.Close()
		return nil, fmt.Errorf("failed to initialize spork manager: %w", err)
	}
	n.Spork = sporkManager
	progress("spork", 100)

	// --- Phase: Masternode ---
	progress("masternode", 0)
	mnConfig := masternode.DefaultConfig()

	// BUG FIX #3: Always set network type for proper port validation.
	// GUI path never set this, defaulting to mainnet regardless of config.
	switch cfg.Network {
	case "mainnet":
		mnConfig.NetworkType = masternode.NetworkMainnet
	case "testnet":
		mnConfig.NetworkType = masternode.NetworkTestnet
	case "regtest":
		mnConfig.NetworkType = masternode.NetworkRegtest
	}

	mnManager, err := masternode.NewManager(mnConfig, logrus.StandardLogger())
	if err != nil {
		mp.Stop()
		consensusEngine.Stop()
		stor.Close()
		return nil, fmt.Errorf("failed to initialize masternode manager: %w", err)
	}
	n.Masternode = mnManager
	mnManager.SetBlockchain(bc)

	// Wire masternode notifier to blockchain for automatic winner vote creation
	bc.SetMasternodeManager(&MasternodeNotifierAdapter{mnManager})

	// Wire spork manager to masternode manager for tier validation
	mnManager.SetSporkManager(sporkManager)

	// Wire masternode payment validator
	progress("masternode", 50)
	paymentValidator := consensus.NewMasternodePaymentValidator(mnManager, chainParams)

	// BUG FIX #4: Wire spork manager to payment validator for SPORK_8/9 enforcement.
	// GUI path was missing this, so ValidateBlockPayment() treated sporks as always inactive.
	paymentValidator.SetSporkManager(sporkManager)

	blockValidator := consensusEngine.GetBlockValidator()
	blockValidator.SetPaymentValidator(paymentValidator)
	consensusEngine.SetPaymentValidator(paymentValidator)

	// BUG FIX #5: Initialize payment vote storage for persistence.
	// GUI path was missing this entirely — votes were lost on restart.
	rawAdapter := consensus.NewRawAccessAdapter(stor)
	paymentVoteDB := consensus.NewPaymentVoteDB(rawAdapter)
	paymentValidator.SetStorage(paymentVoteDB)

	// Load existing votes (non-fatal if missing)
	if err := paymentValidator.LoadFromStorage(); err != nil {
		logger.WithError(err).Warn("Failed to load payment votes from storage (non-fatal)")
	}

	// Initialize masternode debug collector if enabled (zero-cost when disabled)
	if cfg.MasternodeDebug {
		collector := debug.NewCollector(cfg.DataDir, cfg.MasternodeDebugMaxMB, cfg.MasternodeDebugMaxFiles)
		if err := collector.Enable(); err != nil {
			logger.WithError(err).Warn("Failed to enable masternode debug collector (non-fatal)")
		} else {
			n.DebugCollector.Store(collector)
			mnManager.SetDebugCollector(collector)
			logger.Info("Masternode debug collector enabled")
		}
	}

	// --- Phase: Payment Tracker ---
	// Create in-memory payment tracker and wire to payment validator for incremental updates.
	// Blockchain scan happens after init to populate historical stats.
	paymentTracker := masternode.NewPaymentTracker()
	n.PaymentTracker = paymentTracker
	paymentValidator.SetPaymentRecorder(paymentTracker)

	// Scan blockchain for historical payment data
	bestHeight, err := bc.GetBestHeight()
	if err == nil && bestHeight > 0 {
		if scanErr := paymentTracker.ScanBlockchain(
			stor,
			bestHeight,
			chainParams.LastPOWBlock,
			chainParams.DevAddress,
			0, // Use default scan depth
		); scanErr != nil {
			logger.WithError(scanErr).Warn("Payment tracker blockchain scan failed (non-fatal)")
		}
	}

	progress("masternode", 100)
	logger.Info("Core blockchain components initialized")

	return n, nil
}

// ValidateChain runs chain integrity validation using Smart mode.
func (n *Node) ValidateChain(ctx context.Context) error {
	bc, ok := n.Blockchain.(*blockchain.BlockChain)
	if !ok {
		return fmt.Errorf("blockchain is not *blockchain.BlockChain type")
	}

	n.logger.Debug("Validating chain integrity (Smart mode)...")
	if n.Config.OnProgress != nil {
		n.Config.OnProgress("validation", 0)
	}

	validator := blockchain.NewChainValidator(bc)
	validator.SetValidationMode(blockchain.ValidationSmart)

	if err := validator.ValidateChainIntegrity(ctx); err != nil {
		return fmt.Errorf("chain validation failed: %w", err)
	}

	// Run block index consistency check as separate pass
	// Verifies hash→height and height→hash indexes agree
	if err := validator.ValidateBlockIndexConsistency(ctx); err != nil {
		return fmt.Errorf("block index consistency check failed: %w", err)
	}

	if n.Config.OnProgress != nil {
		n.Config.OnProgress("validation", 100)
	}
	n.logger.Debug("Chain integrity validated")
	return nil
}

