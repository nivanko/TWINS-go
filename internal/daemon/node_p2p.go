package daemon

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/twins-dev/twins-core/internal/blockchain"
	"github.com/twins-dev/twins-core/internal/config"
	"github.com/twins-dev/twins-core/internal/consensus"
	"github.com/twins-dev/twins-core/internal/masternode"
	"github.com/twins-dev/twins-core/internal/mempool"
	"github.com/twins-dev/twins-core/internal/p2p"
	"github.com/twins-dev/twins-core/internal/rpc"
	"github.com/twins-dev/twins-core/internal/spork"
	adjustedtime "github.com/twins-dev/twins-core/internal/time"
	"github.com/twins-dev/twins-core/pkg/types"
)

// P2PConfig provides configuration for P2P initialization.
type P2PConfig struct {
	ListenAddr string // P2P listen address (host:port or just port)
	Seeds      []string
	MaxPeers   int
	TestNet    bool
	Listen     bool // Whether to accept incoming connections
}

// InitP2P initializes the P2P server, wires all handlers, and starts services.
// This replaces:
//   - cmd/twinsd/startup_improved.go:startP2PAndSync()
//   - cmd/twins-gui/app.go:StartP2P()
//
// Bug fixes applied by this unified path:
//  1. SetBlockBroadcaster — always wired (GUI was missing this)
//  2. SetSyncedFunc — always wired (GUI was missing this)
//  3. SetBlockAnnouncementNotifier — always wired (GUI was missing this)
//  4. Peer time IP deduplication — always uses AddTimeDataFromPeer (GUI used simple AddTimeSample)
func (n *Node) InitP2P(ctx context.Context, cfg P2PConfig) error {
	if n.Config.OnProgress != nil {
		n.Config.OnProgress("p2p", 0)
	}

	// Build P2P configuration: start from a ConfigManager snapshot so all
	// registered network settings (proxy, banScore, addNodes, etc.) are included.
	var snapshot *config.Config
	if n.ConfigManager != nil {
		snapshot = n.ConfigManager.Snapshot()
	} else {
		snapshot = config.DefaultConfig()
	}
	p2pCfg := buildP2PConfig(n.Config.DataDir, n.Config.Network, cfg, snapshot)

	// Create P2P server
	p2pServer := p2p.NewServer(p2pCfg, n.ChainParams, logrus.StandardLogger())
	p2pServer.SetBlockchain(n.Blockchain)
	p2pServer.SetMempool(n.Mempool)
	p2pServer.SetMasternodeManager(n.Masternode)

	// Wire debug collector to P2P server for masternode activity tracing
	if n.DebugCollector != nil {
		p2pServer.SetDebugCollector(n.DebugCollector)
	}

	// Inject cached masternode addresses as priority bootstrap peers.
	// Must be called after SetMasternodeManager and after LoadMasternodeCache.
	p2pServer.InjectMasternodeCachePeers()

	if n.Config.OnProgress != nil {
		n.Config.OnProgress("p2p", 20)
	}

	// Wire all event handlers
	syncer := n.wireP2PHandlers(p2pServer)

	// BUG FIX #3: Wire block announcement notifier for peer height tracking.
	// GUI path was missing this, so peer heights were only updated on explicit
	// block relay rather than when blocks are saved to disk.
	if healthTracker := syncer.GetHealthTracker(); healthTracker != nil {
		if bc, ok := n.Blockchain.(*blockchain.BlockChain); ok {
			bc.SetBlockAnnouncementNotifier(healthTracker)
			n.logger.Debug("Block announcement notifier wired to blockchain")
		}
	}

	// Store components
	n.mu.Lock()
	n.P2PServer = p2pServer
	n.Syncer = syncer
	n.mu.Unlock()

	// Wire P2P server as transaction broadcaster to wallet
	n.mu.RLock()
	w := n.Wallet
	n.mu.RUnlock()
	if w != nil {
		w.SetBroadcaster(p2pServer)
		n.logger.Debug("Transaction broadcaster wired to wallet")
	}

	// Relay orphan transactions that become valid after parent arrival.
	// This mirrors legacy orphan cascade behavior without duplicating normal tx relay path.
	if mp, ok := n.Mempool.(*mempool.TxMempool); ok {
		mp.SetOnOrphanAccepted(func(tx *types.Transaction) {
			if err := p2pServer.RelayTransaction(tx); err != nil {
				n.logger.WithError(err).WithField("tx", tx.Hash().String()).
					Debug("Failed to relay accepted orphan transaction")
			}
		})
	}

	if n.Config.OnProgress != nil {
		n.Config.OnProgress("p2p", 50)
	}

	// Start P2P server
	if err := p2pServer.Start(ctx); err != nil {
		return fmt.Errorf("failed to start P2P server: %w", err)
	}

	// Start blockchain syncer
	if err := syncer.Start(); err != nil {
		p2pServer.Stop()
		return fmt.Errorf("failed to start syncer: %w", err)
	}

	// Wire consensus validator for staking sync validation
	if cv := syncer.GetConsensusValidator(); cv != nil {
		n.Consensus.SetConsensusProvider(cv)
		n.logger.Debug("Consensus validator wired to PoS engine")
	}

	// BUG FIX #1: Wire block broadcaster for staking (relay staked blocks to P2P network).
	// GUI path was missing this entirely — staked blocks were never relayed.
	n.Consensus.SetBlockBroadcaster(p2pServer.RelayBlock)
	n.logger.Debug("Block broadcaster wired to PoS engine")

	if n.Config.OnProgress != nil {
		n.Config.OnProgress("p2p", 100)
	}
	n.logger.Info("P2P server initialized")
	return nil
}

// wireP2PHandlers configures all P2P event handlers, callbacks, and component wiring.
func (n *Node) wireP2PHandlers(p2pServer *p2p.Server) *p2p.BlockchainSyncer {
	// BUG FIX #4: Wire peer time handler with IP deduplication.
	// GUI path used simple AddTimeSample without IP dedup.
	wirePeerTimeHandler(p2pServer)

	// Wire spork handler
	wireSporkHandler(p2pServer, n.Spork, n.logger)

	// Wire masternode relay handlers
	wireMasternodeHandlers(p2pServer, n.Masternode, n.logger)

	// Wire masternode sync manager
	wireSyncManager(p2pServer, n.Masternode, n.Blockchain, n.logger)

	// Verify masternode dependencies
	n.Masternode.CheckDependencies()

	// Initialize blockchain syncer
	syncer := p2p.NewBlockchainSyncer(
		n.Storage,
		n.Blockchain,
		n.ChainParams,
		n.logger.WithField("component", types.ComponentSyncer),
		p2pServer,
	)
	p2pServer.SetSyncer(syncer)

	// Wire peer lifecycle events
	p2pServer.SetEventHandlers(
		syncer.OnPeerDiscovered,
		syncer.OnPeerDisconnected,
		nil,
	)

	// Wire block processed callback for masternode voting
	mn := n.Masternode
	syncer.SetBlockProcessedCallback(func(height uint32) {
		if _, err := mn.ProcessBlockForWinner(height); err != nil {
			n.logger.WithError(err).Debug("Failed to process block for winner vote")
		}
	})

	// BUG FIX #2: Wire sync state to consensus for payment validation.
	// GUI path was missing SetSyncedFunc, so payment enforcement had no sync awareness.
	wireSyncStateToConsensus(p2pServer, n.Consensus, n.logger)

	return syncer
}

// wirePeerTimeHandler sets up the peer time handler for network time adjustment.
// Matches legacy GetAdjustedTime behavior with IP-based deduplication.
func wirePeerTimeHandler(p2pServer *p2p.Server) {
	p2pServer.SetPeerTimeHandler(func(peerID string, timestamp uint32) {
		host, _, err := net.SplitHostPort(peerID)
		if err != nil {
			peerTime := time.Unix(int64(timestamp), 0)
			adjustedtime.AddTimeSample(peerTime, time.Now())
			return
		}
		peerIP := net.ParseIP(host)
		if peerIP == nil {
			peerTime := time.Unix(int64(timestamp), 0)
			adjustedtime.AddTimeSample(peerTime, time.Now())
			return
		}
		adjustedtime.AddTimeDataFromPeer(peerIP, int64(timestamp))
	})
}

// wireSporkHandler sets up the handler for forwarding received sporks to the global spork manager.
func wireSporkHandler(p2pServer *p2p.Server, sporkMgr *spork.Manager, logger *logrus.Entry) {
	if sporkMgr == nil {
		return
	}

	p2pServer.SetExternalSporkHandler(func(sporkID int32, value int64, timestamp int64, signature []byte) {
		msg := &spork.Message{
			SporkID:    sporkID,
			Value:      value,
			TimeSigned: timestamp,
			Signature:  signature,
		}

		if err := sporkMgr.ProcessMessage(msg, false); err != nil {
			logger.WithError(err).WithField("spork_id", sporkID).Debug("Failed to process spork in global manager")
		} else {
			logger.WithFields(logrus.Fields{
				"spork_id": sporkID,
				"value":    value,
			}).Debug("Spork synced to global manager")
		}
	})
}

// wireMasternodeHandlers sets up relay handlers for masternode broadcasts.
func wireMasternodeHandlers(p2pServer *p2p.Server, mnManager *masternode.Manager, logger *logrus.Entry) {
	mnManager.SetBroadcastRelayHandler(func(mnb *masternode.MasternodeBroadcast, excludeAddr string) {
		if err := p2pServer.BroadcastMasternodeBroadcast(mnb, excludeAddr); err != nil {
			logger.WithError(err).Warn("Failed to broadcast masternode announcement")
		}
	})

	mnManager.SetWinnerRelayHandler(func(vote *masternode.MasternodeWinnerVote) {
		if err := p2pServer.BroadcastMasternodeWinner(vote); err != nil {
			logger.WithError(err).Warn("Failed to broadcast masternode winner vote")
		}
	})

	mnManager.SetPingRelayHandler(func(ping *masternode.MasternodePing) {
		if err := p2pServer.BroadcastMasternodePing(ping); err != nil {
			logger.WithError(err).Warn("Failed to broadcast masternode ping")
		}
	})
}

// wireSyncManager configures the masternode sync manager with P2P server.
func wireSyncManager(p2pServer *p2p.Server, mnManager *masternode.Manager, bc blockchain.Blockchain, logger *logrus.Entry) {
	syncManager := mnManager.GetSyncManager()
	if syncManager == nil {
		return
	}

	syncManager.SetPeerRequester(p2pServer)
	syncManager.SetMasternodeCountGetter(func() int {
		return mnManager.CountEnabled(0)
	})
	syncManager.SetBlockchain(bc)
	syncManager.SetNetworkSyncStatus(p2pServer)
	syncManager.StartProcessLoop()

	logger.Debug("Masternode sync manager process loop started")
}

// wireSyncStateToConsensus connects sync state to PoS consensus for payment validation.
func wireSyncStateToConsensus(p2pServer *p2p.Server, consensusEngine consensus.Engine, logger *logrus.Entry) {
	posEngine, ok := consensusEngine.(*consensus.ProofOfStake)
	if !ok {
		return
	}

	blockValidator := posEngine.GetBlockValidator()
	if blockValidator == nil {
		return
	}

	blockValidator.SetSyncedFunc(p2pServer.IsSynced)
	logger.Debug("Wired sync state to consensus for masternode payment enforcement")
}

// buildP2PConfig creates P2P network configuration from settings.
// Starts from snapshot (a deep copy of the ConfigManager's current config) so
// that all registered network settings (proxy, banScore, banTime, addNodes,
// externalIP, maxOutboundPeers, upnp, timeout, keepAlive, etc.) flow through
// automatically. Only the CLI-derived fields from P2PConfig are then applied
// on top.
func buildP2PConfig(dataDir, network string, cfg P2PConfig, snapshot *config.Config) *config.Config {
	host, port := parseHostPort(cfg.ListenAddr, types.DefaultP2PHost, types.DefaultP2PPort)

	c := snapshot.Clone()
	c.DataDir = dataDir
	c.Network.TestNet = network == "testnet"
	c.Network.ListenAddr = host
	c.Network.Port = port
	c.Network.Listen = cfg.Listen
	if cfg.MaxPeers > 0 {
		c.Network.MaxPeers = cfg.MaxPeers
	}
	if len(cfg.Seeds) > 0 {
		c.Network.Seeds = cfg.Seeds
	} else if len(c.Network.Seeds) == 0 {
		c.Network.Seeds = config.GetDefaultSeeds(network)
	}
	return c
}

// RPCConfig provides configuration for RPC server initialization.
type RPCConfig struct {
	ListenAddr string // RPC listen address (host:port)
	Username   string // RPC username (empty for cookie auth)
	Password   string // RPC password (empty for cookie auth)
	AllowedIPs []string
	MaxClients int

	// Config file settings
	FullConfig     *config.Config
	ExplicitConfig bool // Whether config was explicitly specified via --config

	// Active masternode configuration
	MasternodeEnabled     bool
	MasternodePrivateKey  string
	MasternodeServiceAddr string

	// Shutdown function for "stop" RPC command
	ShutdownFunc func()
}

// InitRPC initializes and starts the RPC server.
// This replaces:
//   - cmd/twinsd/startup_improved.go:startRPCServer()
//   - cmd/twins-gui/app.go:StartRPCServer()
func (n *Node) InitRPC(cfg RPCConfig) error {
	if n.Config.OnProgress != nil {
		n.Config.OnProgress("rpc", 0)
	}

	// Check if RPC is disabled
	if cfg.FullConfig != nil && !cfg.FullConfig.RPC.Enabled {
		n.logger.Debug("RPC server disabled in configuration")
		return nil
	}

	rpcHost, rpcPort := parseHostPort(cfg.ListenAddr, types.DefaultRPCHost, types.DefaultRPCPort)

	rpcConfig := rpc.DefaultConfig()
	rpcConfig.Host = rpcHost
	rpcConfig.Port = rpcPort
	rpcConfig.DataDir = n.Config.DataDir

	// Apply config file settings
	if cfg.FullConfig != nil {
		if len(cfg.FullConfig.RPC.AllowedIPs) > 0 {
			rpcConfig.AllowedIPs = cfg.FullConfig.RPC.AllowedIPs
		}
		if cfg.FullConfig.RPC.MaxClients > 0 {
			rpcConfig.MaxClients = cfg.FullConfig.RPC.MaxClients
		}
		if cfg.FullConfig.RPC.RateLimit > 0 {
			rpcConfig.RateLimit = cfg.FullConfig.RPC.RateLimit
		}
		if cfg.FullConfig.RPC.Timeout > 0 {
			rpcConfig.ReadTimeout = time.Duration(cfg.FullConfig.RPC.Timeout) * time.Second
			rpcConfig.WriteTimeout = time.Duration(cfg.FullConfig.RPC.Timeout) * time.Second
		}
	}

	// Apply explicit overrides
	if len(cfg.AllowedIPs) > 0 {
		rpcConfig.AllowedIPs = cfg.AllowedIPs
	}
	if cfg.MaxClients > 0 {
		rpcConfig.MaxClients = cfg.MaxClients
	}

	// Load credentials with priority: explicit > config file > twins.conf > cookie auth
	username, password := cfg.Username, cfg.Password
	var credSource string

	if username != "" && password != "" {
		credSource = "explicit"
	} else if cfg.FullConfig != nil && cfg.FullConfig.RPC.Username != "" && cfg.FullConfig.RPC.Password != "" {
		username = cfg.FullConfig.RPC.Username
		password = cfg.FullConfig.RPC.Password
		credSource = "config file"
	} else if !cfg.ExplicitConfig {
		var err error
		username, password, err = parseTwinsConf(n.Config.DataDir)
		if err != nil {
			if !os.IsNotExist(err) {
				n.logger.WithError(err).Error("twins.conf exists but could not be parsed")
			}
		} else if username != "" && password != "" {
			credSource = "twins.conf"
		}
	}

	if username != "" && password != "" {
		rpcConfig.Username = username
		rpcConfig.Password = password
	}

	// Initialize authentication (credentials or cookie)
	effectiveUser, effectivePass, err := rpc.InitializeAuthentication(rpcConfig, n.logger)
	if err != nil {
		return fmt.Errorf("failed to initialize RPC authentication: %w", err)
	}
	if effectiveUser == "" || effectivePass == "" {
		return fmt.Errorf("RPC authentication not properly initialized: credentials are empty")
	}
	rpcConfig.Username = effectiveUser
	rpcConfig.Password = effectivePass

	authMethod := "cookie"
	if credSource != "" {
		authMethod = credSource
	}
	n.logger.WithField("method", authMethod).Debug("RPC authentication initialized")

	if n.Config.OnProgress != nil {
		n.Config.OnProgress("rpc", 50)
	}

	rpcServer := rpc.NewServer(rpcConfig, n.logger.WithField("component", types.ComponentRPC))

	// Wire all RPC dependencies
	n.wireRPCDependencies(rpcServer, cfg)

	// Start RPC server
	go func() {
		if err := rpcServer.Start(); err != nil {
			n.logger.WithError(err).Error("RPC server error")
		}
	}()

	// Give RPC server time to start
	time.Sleep(types.RPCStartDelay)

	n.mu.Lock()
	n.RPCServer = rpcServer
	n.rpcConfig = rpcConfig // Store for cleanup during shutdown
	n.mu.Unlock()

	if n.Config.OnProgress != nil {
		n.Config.OnProgress("rpc", 100)
	}
	n.logger.WithFields(logrus.Fields{
		"host": rpcConfig.Host,
		"port": rpcConfig.Port,
	}).Info("RPC server started")

	return nil
}

// wireRPCDependencies wires all component adapters to the RPC server.
func (n *Node) wireRPCDependencies(rpcServer *rpc.Server, cfg RPCConfig) {
	rpcServer.SetBlockchain(rpc.NewBlockchainAdapter(n.Blockchain))
	rpcServer.SetConsensus(rpc.NewConsensusAdapter(n.Consensus))
	rpcServer.SetChainParams(n.ChainParams)
	mnAdapter := rpc.NewMasternodeAdapter(n.Masternode)
	if n.PaymentTracker != nil {
		mnAdapter.SetPaymentTracker(n.PaymentTracker)
	}
	rpcServer.SetMasternode(mnAdapter)

	// Wire mempool (requires type assertion to concrete TxMempool for RPC adapter)
	if mp, ok := n.Mempool.(*mempool.TxMempool); ok {
		rpcServer.SetMempool(rpc.NewMempoolAdapter(mp, n.Blockchain))
	}

	// Initialize active masternode if configured
	if cfg.MasternodeEnabled && cfg.MasternodePrivateKey != "" && cfg.MasternodeServiceAddr != "" {
		activeMN := masternode.NewActiveMasternode()
		isMainnet := n.Config.Network == "mainnet"
		activeMN.SetDependencies(n.Masternode, nil, isMainnet)
		activeMN.SetBlockchain(n.Blockchain)

		if err := activeMN.Initialize(cfg.MasternodePrivateKey, cfg.MasternodeServiceAddr); err != nil {
			n.logger.WithError(err).Warn("Failed to initialize active masternode")
		} else {
			rpcServer.SetActiveMasternode(&ActiveMasternodeAdapter{AM: activeMN})
			n.Masternode.SetActiveMasternodeInstance(activeMN)

			if syncMgr := n.Masternode.GetSyncManager(); syncMgr != nil {
				activeMN.SetSyncChecker(func() bool {
					return syncMgr.IsBlockchainSynced()
				})
			}

			n.logger.WithField("service", cfg.MasternodeServiceAddr).Debug("Active masternode initialized")
		}
	}

	// Wire optional components
	n.mu.RLock()
	w := n.Wallet
	p2pSrv := n.P2PServer
	mnConf := n.MasternodeConf
	n.mu.RUnlock()

	if w != nil {
		rpcServer.SetWallet(rpc.NewWalletAdapter(w))
	}
	if n.ConfigManager != nil {
		rpcServer.SetConfigSetter(n.ConfigManager)
	}
	if p2pSrv != nil {
		rpcServer.SetP2P(p2pSrv)
	}
	if mnConf != nil {
		rpcServer.SetMasternodeConf(rpc.NewMasternodeConfAdapter(mnConf))
	}
	if n.Spork != nil {
		rpcServer.SetSporkManager(n.Spork)
	}

	if cfg.ShutdownFunc != nil {
		rpcServer.SetShutdownFunc(cfg.ShutdownFunc)
	}
}

// parseHostPort splits an address into host and port components.
// Falls back to defaults if the address is empty or incomplete.
func parseHostPort(addr, defaultHost string, defaultPort int) (string, int) {
	if addr == "" {
		return defaultHost, defaultPort
	}

	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		// Try as just a port number
		if port := parsePort(addr); port > 0 {
			return defaultHost, port
		}
		// Try as just a host
		return addr, defaultPort
	}

	port := parsePort(portStr)
	if port == 0 {
		port = defaultPort
	}
	if host == "" {
		host = defaultHost
	}
	return host, port
}

// parsePort converts a port string to an integer.
func parsePort(s string) int {
	port, err := strconv.Atoi(s)
	if err != nil || port < 1 || port > 65535 {
		return 0
	}
	return port
}

// parseTwinsConf reads rpcuser and rpcpassword from legacy twins.conf file.
func parseTwinsConf(dataDir string) (username, password string, err error) {
	confPath := filepath.Join(dataDir, "twins.conf")
	file, err := os.Open(confPath)
	if err != nil {
		return "", "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "rpcuser":
			username = value
		case "rpcpassword":
			password = value
		}
	}

	if err := scanner.Err(); err != nil {
		return "", "", err
	}

	return username, password, nil
}
