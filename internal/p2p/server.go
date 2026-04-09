package p2p

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/twins-dev/twins-core/internal/blockchain"
	"github.com/twins-dev/twins-core/internal/cli"
	"github.com/twins-dev/twins-core/internal/config"
	"github.com/twins-dev/twins-core/internal/consensus"
	"github.com/twins-dev/twins-core/internal/masternode"
	"github.com/twins-dev/twins-core/internal/masternode/debug"
	"github.com/twins-dev/twins-core/internal/mempool"
	"github.com/twins-dev/twins-core/pkg/crypto"
	"github.com/twins-dev/twins-core/pkg/types"
)

// MasternodeManager interface defines operations needed by P2P layer
type MasternodeManager interface {
	// GetPublicKey returns the public key for a masternode
	GetPublicKey(outpoint types.Outpoint) ([]byte, error)
	// IsActive checks if a masternode is active at a given height
	IsActive(outpoint types.Outpoint, height uint32) bool
	// GetTier returns the tier of a masternode
	GetTier(outpoint types.Outpoint) (uint8, error)
	// GetPaymentQueuePosition returns the payment queue position
	GetPaymentQueuePosition(outpoint types.Outpoint, height uint32) (int, error)
	// GetLastPaidBlock returns the last block height where masternode was paid
	GetLastPaidBlock(outpoint types.Outpoint) (uint32, error)
	// ProcessBroadcast processes a masternode broadcast message.
	// originAddr is the P2P peer that sent this broadcast (excluded from relay); pass "" for local broadcasts.
	ProcessBroadcast(mnb *masternode.MasternodeBroadcast, originAddr string) error
	// ProcessPing processes a masternode ping message.
	// peerAddr is the P2P peer that sent this ping (used for debug logging).
	ProcessPing(mnp *masternode.MasternodePing, peerAddr string) error
	// GetMasternodeList returns the full list of masternodes
	GetMasternodeList() *masternode.MasternodeList
	// GetBroadcastByHash retrieves a masternode broadcast by its hash
	GetBroadcastByHash(hash types.Hash) (*masternode.MasternodeBroadcast, error)
	// CountEnabled returns the count of enabled masternodes
	// Legacy: CMasternodeMan::CountEnabled(int protocolVersion) from masternodeman.cpp:378-390
	// Pass -1 to use minimum payment protocol version
	CountEnabled(protocolVersion int32) int
	// StoreWinnerVote stores a validated winner vote for persistence
	// Legacy: mapMasternodePayeeVotes storage for mnpayments.dat
	StoreWinnerVote(voterOutpoint types.Outpoint, blockHeight uint32, payeeScript, signature []byte)
	// GetMinMasternodePaymentsProto returns minimum protocol version for payment eligibility
	// Legacy: masternodePayments.GetMinMasternodePaymentsProto() - uses SPORK_10 (10009)
	GetMinMasternodePaymentsProto() int32
	// GetMasternodeRank returns the payment rank of a masternode at a given height
	// Legacy: CMasternodeMan::GetMasternodeRank() from masternodeman.cpp:521-605
	// Returns -1 if masternode not found, otherwise rank (1-based, lower is better)
	GetMasternodeRank(outpoint types.Outpoint, blockHeight uint32, minProtocol int32, filterTier bool) int
	// GetMasternode returns a masternode by outpoint (for AskForMN logic)
	GetMasternode(outpoint types.Outpoint) (*masternode.Masternode, error)

	// Sync tracking methods for per-peer fulfilled request tracking
	// Legacy: pnode->HasFulfilledRequest(), pnode->FulfilledRequest()

	// AddedMasternodeWinner notifies sync manager that a winner vote was received
	// Legacy: masternodeSync.AddedMasternodeWinner(hash)
	AddedMasternodeWinner(hash types.Hash)
	// ProcessSyncStatusCount handles sync status count messages from peers
	// Used to track per-peer sync responses for advancement decisions
	ProcessSyncStatusCount(peerAddr string, syncType int, count int)
	// HasFulfilledRequest checks if a peer has been asked for a sync request type
	// requestType is "mnsync" for LIST or "mnwsync" for MNW
	HasFulfilledRequest(peerAddr string, requestType string) bool
	// FulfilledRequest marks a peer as having been asked for a sync request type
	FulfilledRequest(peerAddr string, requestType string)
	// MarkBroadcastSeen seeds the seenBroadcasts map to prevent relay-bounce
	// when sending mnb messages directly (e.g., dseg responses)
	MarkBroadcastSeen(mnb *masternode.MasternodeBroadcast)
	// GetPingByHash retrieves a masternode ping by its hash from known masternodes.
	// Used by getdata handler to serve ping requests (matches C++ mapSeenMasternodePing lookup).
	GetPingByHash(hash types.Hash) *masternode.MasternodePing
	// GetPeerAddresses returns all known masternode network addresses from the cache.
	// Used to inject cached masternodes as priority bootstrap peers.
	GetPeerAddresses() []string
}

// Server represents the P2P network server
type Server struct {
	// Configuration
	config      *config.Config
	params      *types.ChainParams
	services    ServiceFlag
	logger      *logrus.Entry
	maxOutbound int32 // Maximum outbound connections (from config or default 16)
	maxInbound  int32 // Maximum inbound connections (derived from maxPeers - maxOutbound)

	// Dependencies
	blockchain       blockchain.Blockchain
	mempool          mempool.Mempool
	mnManager        MasternodeManager                     // Masternode manager interface
	paymentValidator *consensus.MasternodePaymentValidator // Payment vote validator
	syncer           *BlockchainSyncer                     // Blockchain syncer
	debugCollector   atomic.Pointer[debug.Collector]       // Debug event collector for masternode tracing

	// Network state
	listening  atomic.Bool
	started    atomic.Bool
	listener   net.Listener
	userAgent  string
	localAddr  *NetAddress
	externalIP atomic.Pointer[net.IP] // Learned from outbound peer AddrRecv; atomic for cross-goroutine reads

	// Peer management
	peers                sync.Map                                                            // Active peers (addr string -> *Peer)
	peerCount            atomic.Int32                                                        // Current peer count
	inbounds             atomic.Int32                                                        // Inbound connection count
	outbounds            atomic.Int32                                                        // Outbound connection count
	discovery            *PeerDiscovery                                                      // Peer discovery and address management
	sporkMgr             *SporkManager                                                       // Spork management (internal P2P)
	externalSporkHandler func(sporkID int32, value int64, timestamp int64, signature []byte) // Forward sporks to global manager
	mnWinners            sync.Map                                                            // Masternode winners (blockHeight -> *MasternodeWinner)
	mnQuorums            sync.Map                                                            // Masternode quorums (blockHeight -> *MasternodeQuorum)

	// Masternode sync rate limiting (matches legacy mAskedUsForMasternodeList)
	dsegRateLimitMu sync.RWMutex
	dsegRateLimit   map[string]int64 // peer addr -> next allowed dseg request time (Unix timestamp)

	// AskForMN rate limiting (matches legacy mWeAskedForMasternodeListEntry)
	askForMNMu sync.RWMutex
	askForMN   map[types.Outpoint]int64 // outpoint -> next allowed ask time (Unix timestamp)

	// Addr relay dedup cache: recently relayed addresses are not relayed again
	addrRelayDedupMu sync.RWMutex
	addrRelayDedup   map[string]int64 // "IP:Port" -> Unix timestamp when relay expires

	// Enhanced sync components
	healthTracker *PeerHealthTracker // Shared peer health tracking
	bootstrap     *BootstrapManager  // Bootstrap phase manager

	// Connection management
	connRequests  chan *connRequest
	newPeers      chan *Peer
	donePeers     chan *Peer
	banPeers      chan banRequest   // Carries peer + reason for ban
	msgChan       chan *PeerMessage // Incoming messages from peers
	handshakeChan chan *PeerMessage // Highest-priority channel for version/verack (prevents handshake stall)
	addrChan      chan *PeerMessage // Low-priority channel for addr messages (prevents sync stall)
	handshakeTTL  time.Duration     // Maximum time allowed for version handshake completion

	// Service coordination
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Lifecycle channels
	quit chan struct{}
	done chan struct{}

	// Statistics
	totalConnections atomic.Uint64
	totalMessages    atomic.Uint64
	droppedMessages  atomic.Uint64 // Messages dropped due to full msgChan
	bytesReceived    atomic.Uint64
	bytesSent        atomic.Uint64
	msgTypeRecv      sync.Map // command string -> *atomic.Uint64
	msgTypeSent      sync.Map // command string -> *atomic.Uint64

	// Rate limiting
	rateLimiter      chan struct{}
	bandwidthMonitor *BandwidthMonitor // Bandwidth monitoring and rate limiting

	// Semaphore for heavy message handlers (dseg, getblocks, getheaders, mempool).
	// Limits concurrent goroutines to prevent resource exhaustion when many peers
	// send expensive requests simultaneously.  Handlers that exceed the limit
	// are silently dropped (peer can retry).
	heavyHandlerSem chan struct{}

	// Connection backoff tracking
	connBackoff   map[string]*ConnectionBackoff // Track failed connection attempts
	connBackoffMu sync.RWMutex                  // Protect connBackoff map

	// Block request deduplication (prevent duplicate getdata requests)
	pendingBlockRequests   map[types.Hash]time.Time // hash -> request time
	pendingBlockRequestsMu sync.RWMutex

	// Orphan block handling (blocks received before their parent)
	orphanBlocks         map[types.Hash]*types.Block // hash -> orphan block
	orphanBlocksByParent map[types.Hash][]types.Hash // parent hash -> list of orphan hashes
	orphanBlocksMu       sync.RWMutex

	// Seed retry backoff tracking
	seedRetryCount    atomic.Int32 // Consecutive seed connection failures
	lastSeedRetryTime time.Time    // Time of last seed retry attempt

	// Bootstrap phase tracking - seeds are emergency fallback only
	seedsActivated         atomic.Bool  // True when seeds have been activated (emergency mode)
	bootstrapStartTime     time.Time    // When outbound connection manager started
	knownPeersAttemptCount atomic.Int32 // Number of known peers connection attempts

	// Legacy connection mode (from config)
	addNodes    []string // Nodes to connect to and keep open (legacy: -addnode)
	connectOnly []string // Connect ONLY to these nodes (legacy: -connect)

	// UPnP port mapping (legacy: -upnp)
	upnpMgr *UPnPManager // UPnP manager, nil when disabled; stopped in Stop()

	// Proxy support (legacy: -proxy, -onion)
	proxyDialer      *ProxyDialer // SOCKS5 proxy for regular connections
	onionProxyDialer *ProxyDialer // Separate proxy for .onion addresses

	// Whitelist support (legacy: -whitelist, -whitebind)
	whitelist *WhitelistManager // IP whitelist for incoming connections

	// Socket options (legacy: -maxreceivebuffer, -maxsendbuffer)
	socketOpts *SocketOptions

	// Event handlers
	onNewPeer    func(*Peer)
	onPeerDone   func(*Peer)
	onNewMessage func(*PeerMessage)
	onPeerTime   func(peerID string, timestamp uint32) // For network time adjustment

	// Protocol 70928: cached chainstate response (invalidated on new block)
	chainStateCacheMu sync.RWMutex
	chainStateCache   *ChainStateMessage // Cached response, nil = stale

	// Shutdown coordination
	stopOnce sync.Once // Ensures Stop() is idempotent (prevents panic on double close)

	// Self-connection detection: nonces we sent in version messages.
	// If a received version carries one of our nonces, the peer is us.
	// Legacy: CNode::nLocalHostNonce + fSelf detection (main.cpp:5760-5766)
	sentNoncesMu sync.RWMutex
	sentNonces   map[uint64]struct{}

	// Transaction relay stability pipeline
	txRelayMu    sync.Mutex
	txRelayCache *txRelayCache
	peerTxRelay  map[string]*peerTxRelayState
}

// ConnectionBackoff tracks failed connection attempts for backoff calculation
type ConnectionBackoff struct {
	FailedAttempts int32
	LastAttempt    time.Time
	LastFailure    time.Time
	BackoffUntil   time.Time
}

// banRequest carries a peer and reason through the ban channel.
type banRequest struct {
	peer   *Peer
	reason string
}

// connRequest represents a connection request
type connRequest struct {
	addr      string
	permanent bool
	reply     chan error
}

// ServerStats contains server statistics
type ServerStats struct {
	Listening        bool
	PeerCount        int32
	InboundCount     int32
	OutboundCount    int32
	TotalConnections uint64
	TotalMessages    uint64
	BytesReceived    uint64
	BytesSent        uint64
	LocalAddress     string
	ListenPort       uint16
	ExternalIP       string
	Services         ServiceFlag
}

// NewServer creates a new P2P server
func NewServer(cfg *config.Config, params *types.ChainParams, logger *logrus.Logger) *Server {
	ctx, cancel := context.WithCancel(context.Background())

	userAgent := fmt.Sprintf("/TWINS-Go:%s/", cli.Version)

	// Initialize peer discovery
	seeds := cfg.Network.Seeds
	var dnsSeeds []string // DNS seeds can be added to config later

	discovery := NewPeerDiscovery(DiscoveryConfig{
		Logger:         logger,
		Network:        params.Name,
		Seeds:          seeds,
		DNSSeeds:       dnsSeeds,
		MaxPeers:       MaxInboundConnections + MaxOutboundConnections,
		DataDir:        cfg.DataDir,
		DNSSeedEnabled: cfg.Network.DNSSeed, // Legacy: -dnsseed flag
	})

	// Initialize spork manager with chain spork public key
	var sporkPubKey *crypto.PublicKey
	if params.SporkPubKey != "" {
		var err error
		sporkPubKey, err = crypto.ParsePublicKeyFromHex(params.SporkPubKey)
		if err != nil {
			logger.WithError(err).Warn("Failed to parse spork public key, spork verification disabled")
			sporkPubKey = nil
		} else {
			logger.Debug("Spork public key configured for signature verification")
		}
	}
	sporkMgr := NewSporkManager(sporkPubKey)

	// Set old spork key for backward compatibility during transitions
	if params.SporkPubKeyOld != "" {
		oldKey, err := crypto.ParsePublicKeyFromHex(params.SporkPubKeyOld)
		if err != nil {
			logger.WithError(err).Warn("Failed to parse old spork public key")
		} else {
			sporkMgr.SetOldPublicKey(oldKey)
			logger.Debug("Old spork public key configured for transition period")
		}
	}

	// Initialize enhanced sync components
	healthTracker := NewPeerHealthTracker()

	// Bootstrap settings come from config (validated: minPeers >= 1, maxWait >= 10)
	bootstrap := NewBootstrapManager(
		cfg.Sync.BootstrapMinPeers,
		time.Duration(cfg.Sync.BootstrapMinWait)*time.Second,
		time.Duration(cfg.Sync.BootstrapMaxWait)*time.Second,
		logger.WithField("component", "bootstrap"),
	)

	// Initialize bandwidth monitor with upload rate limiting
	var bandwidthMonitor *BandwidthMonitor
	if cfg.Network.MaxUploadBytesPerSec > 0 {
		bandwidthMonitor = NewBandwidthMonitor(
			logger,
			uint64(cfg.Network.MaxUploadBytesPerSec),
			0, // No download limit for now
		)
		logger.WithField("max_upload_bytes_per_sec", cfg.Network.MaxUploadBytesPerSec).
			Debug("Bandwidth monitoring enabled")
	}

	// Initialize proxy dialers if configured
	var proxyDialer, onionProxyDialer *ProxyDialer
	if cfg.Network.Proxy != "" {
		proxyDialer = NewProxyDialer(cfg.Network.Proxy)
		logger.WithField("proxy", cfg.Network.Proxy).Debug("SOCKS5 proxy configured for outbound connections")
	}
	if cfg.Network.OnionProxy != "" {
		onionProxyDialer = NewProxyDialer(cfg.Network.OnionProxy)
		logger.WithField("onion_proxy", cfg.Network.OnionProxy).Debug("Separate SOCKS5 proxy configured for .onion addresses")
	} else if proxyDialer != nil {
		// Use main proxy for .onion if no separate onion proxy
		onionProxyDialer = proxyDialer
	}

	// Initialize whitelist manager
	whitelist := NewWhitelistManager(cfg.Network.Whitelist)
	if whitelist.IsEnabled() {
		logger.WithField("entries", whitelist.Count()).Debug("IP whitelist enabled for incoming connections")
	}

	// Determine max outbound connections from config or use default
	maxOutbound := int32(MaxOutboundConnections) // default: 16
	if cfg.Network.MaxOutboundPeers > 0 {
		maxOutbound = int32(cfg.Network.MaxOutboundPeers)
	}

	// Cap outbound by maxPeers (legacy: nMaxOutbound = min(MAX_OUTBOUND, nMaxConnections), net.cpp:1722)
	if cfg.Network.MaxPeers > 0 && int32(cfg.Network.MaxPeers) < maxOutbound {
		maxOutbound = int32(cfg.Network.MaxPeers)
	}

	// Determine max inbound connections from config.Network.MaxPeers (legacy: -maxconnections)
	// MaxPeers controls total connections; inbound = total - outbound.
	maxInbound := int32(MaxInboundConnections) // default: 125
	if cfg.Network.MaxPeers > 0 {
		maxInbound = int32(cfg.Network.MaxPeers) - maxOutbound
		if maxInbound < 0 {
			maxInbound = 0
		}
	}

	server := &Server{
		config:      cfg,
		params:      params,
		services:    SFNodeNetwork | SFNodeBloom, // Advertise full node + bloom filter (masternode flag added when active)
		logger:      logger.WithField("component", "p2p-server"),
		maxOutbound: maxOutbound,
		maxInbound:  maxInbound,
		userAgent:   userAgent,
		discovery:   discovery,
		sporkMgr:    sporkMgr,

		healthTracker:    healthTracker,
		bootstrap:        bootstrap,
		bandwidthMonitor: bandwidthMonitor,
		connBackoff:      make(map[string]*ConnectionBackoff), // Track connection failures
		dsegRateLimit:    make(map[string]int64),              // Rate limit dseg requests (mAskedUsForMasternodeList)
		askForMN:         make(map[types.Outpoint]int64),      // Rate limit AskForMN requests (mWeAskedForMasternodeListEntry)
		addrRelayDedup:   make(map[string]int64),              // Addr relay dedup cache (IP:Port -> expiry)
		sentNonces:       make(map[uint64]struct{}),           // Self-connection detection nonces

		// Block request deduplication and orphan handling
		pendingBlockRequests: make(map[types.Hash]time.Time),
		orphanBlocks:         make(map[types.Hash]*types.Block),
		orphanBlocksByParent: make(map[types.Hash][]types.Hash),

		// Legacy connection mode configuration
		addNodes:    cfg.Network.AddNodes,    // Nodes to connect to and keep open (legacy: -addnode)
		connectOnly: cfg.Network.ConnectOnly, // Connect ONLY to these nodes (legacy: -connect)

		// Proxy support (legacy: -proxy, -onion)
		proxyDialer:      proxyDialer,
		onionProxyDialer: onionProxyDialer,

		// Whitelist support (legacy: -whitelist)
		whitelist: whitelist,

		// Socket options (legacy: -maxreceivebuffer, -maxsendbuffer)
		socketOpts: &SocketOptions{
			MaxReceiveBuffer: cfg.Network.MaxReceiveBuffer * 1000, // Config is in KB
			MaxSendBuffer:    cfg.Network.MaxSendBuffer * 1000,    // Config is in KB
		},

		connRequests:  make(chan *connRequest, 10),
		newPeers:      make(chan *Peer, MaxInboundConnections+MaxOutboundConnections),
		donePeers:     make(chan *Peer, MaxInboundConnections+MaxOutboundConnections),
		banPeers:      make(chan banRequest, 10),
		msgChan:       make(chan *PeerMessage, 1000), // Large buffer for message processing
		handshakeChan: make(chan *PeerMessage, 50),   // Highest-priority: version/verack never blocked by full msgChan
		addrChan:      make(chan *PeerMessage, 500),  // Low-priority addr messages (prevents sync stall from addr floods)
		handshakeTTL:  HandshakeTimeout,

		ctx:    ctx,
		cancel: cancel,
		quit:   make(chan struct{}),
		done:   make(chan struct{}),

		rateLimiter:     make(chan struct{}, 100), // Rate limit concurrent connections
		heavyHandlerSem: make(chan struct{}, 8),   // Limit concurrent heavy handlers (dseg, getblocks, getheaders, mempool)

		txRelayCache: newTxRelayCache(),
		peerTxRelay:  make(map[string]*peerTxRelayState),
	}

	return server
}

// Start starts the P2P server
// The context is used to make the bootstrap phase interruptible for graceful shutdown
func (s *Server) Start(ctx context.Context) error {
	if s.started.Load() {
		return errors.New("server already started")
	}

	s.logger.Info("Starting P2P server")

	// Initialize rate limiter
	for i := 0; i < 100; i++ {
		s.rateLimiter <- struct{}{}
	}

	// Start bandwidth monitor if configured
	if s.bandwidthMonitor != nil {
		s.bandwidthMonitor.Start()
	}

	// Apply configured external IP (legacy: -externalip)
	if s.config.Network.ExternalIP != "" {
		if ip := net.ParseIP(s.config.Network.ExternalIP); ip != nil {
			s.SetExternalIP(ip)
			s.logger.WithField("ip", s.config.Network.ExternalIP).Info("External IP set from configuration")
		} else {
			s.logger.WithField("ip", s.config.Network.ExternalIP).Warn("Invalid external IP in configuration, ignoring")
		}
	}

	// Start UPnP port mapping if enabled (legacy: -upnp)
	if s.config.Network.UPnP && s.config.Network.Listen {
		mgr := NewUPnPManager(s.config.Network.Port, s.logger.Logger)
		if err := mgr.Start(); err != nil {
			s.logger.WithError(err).Debug("UPnP port mapping failed")
		} else {
			s.upnpMgr = mgr // Store for cleanup in Stop()
			if extIP := mgr.GetExternalIP(); extIP != nil && s.getExternalIP() == nil {
				s.SetExternalIP(extIP)
				s.logger.WithField("ip", extIP.String()).Info("External IP discovered via UPnP")
			}
		}
	}

	// Start listening if configured
	listenAddr := s.config.GetListenAddress()
	if listenAddr != "" {
		if err := s.startListener(listenAddr); err != nil {
			return fmt.Errorf("failed to start listener: %w", err)
		}
	}

	// Start peer discovery
	s.discovery.Start()

	// Set up discovery handler to request addresses from connected peers
	s.discovery.SetRequestAddressesHandler(func() {
		s.requestAddressesFromPeers()
	})

	// Start core goroutines
	s.wg.Add(6)
	go s.peerHandler()
	go s.handshakeHandler() // Dedicated goroutine: version/verack never blocked by slow message handlers
	go s.messageHandler()
	go s.connectionManager()
	go s.txRelayLoop()
	go s.statsLoop()

	// Handle connection modes (legacy: -connect, -addnode)
	if len(s.connectOnly) > 0 {
		// ConnectOnly mode: connect ONLY to specified nodes, no discovery
		s.logger.WithField("nodes", s.connectOnly).Debug("ConnectOnly mode: connecting only to specified nodes")
		s.wg.Add(1)
		go s.connectOnlyManager()
	} else {
		// Normal mode: use seeds and discovery
		// Start outbound connection routine
		if len(s.config.Network.Seeds) > 0 || len(s.addNodes) > 0 {
			s.wg.Add(1)
			go s.outboundConnectionManager()
		}
	}

	s.started.Store(true)

	// Start the blockchain syncer (if configured) before bootstrap
	if s.syncer != nil {
		s.logger.Debug("Starting blockchain syncer")
		if err := s.syncer.Start(); err != nil {
			return fmt.Errorf("failed to start blockchain syncer: %w", err)
		}
	}

	// Run bootstrap phase before starting sync
	s.logger.Debug("Starting bootstrap phase - discovering peers")
	s.bootstrap.Start()

	// Wait for bootstrap to complete or context cancellation (shutdown)
	// The bootstrap manager has its own internal timers (minWait and maxWait)
	select {
	case <-ctx.Done():
		s.logger.Info("Bootstrap interrupted by shutdown signal")
		return ctx.Err()
	case <-s.bootstrap.Done():
	}

	peerCount := s.bootstrap.PeerCount()
	mnCount := s.bootstrap.MasternodeCount()
	s.logger.WithFields(logrus.Fields{
		"peers":       peerCount,
		"masternodes": mnCount,
	}).Debug("Bootstrap phase complete")

	// After bootstrap, rebuild peer list and let the sync state machine decide next steps
	if s.syncer != nil {
		// Wait a moment for any in-flight handshakes to complete
		// Bootstrap waits for peer discovery, but handshakes may still be completing
		time.Sleep(1 * time.Second)

		s.logger.Debug("Rebuilding sync peer list from discovered peers")
		s.syncer.RebuildPeerList()

		s.syncer.OnBootstrapComplete()
	}

	s.logger.Info("P2P server started successfully")

	return nil
}

// Stop gracefully stops the P2P server.
// Safe to call multiple times — uses sync.Once to prevent panic on double close.
func (s *Server) Stop() {
	// Always stop bootstrap manager (may be running even if Start() returned error)
	if s.bootstrap != nil {
		s.bootstrap.Stop()
	}

	if !s.started.Load() {
		return
	}

	s.stopOnce.Do(func() {
		s.logger.Info("Stopping P2P server")

		if s.syncer != nil {
			s.logger.Debug("Stopping blockchain syncer")
			s.syncer.Stop()
		}

		// Stop peer discovery
		s.discovery.Stop()

		// Stop bandwidth monitor if running
		if s.bandwidthMonitor != nil {
			s.bandwidthMonitor.Stop()
		}

		// Stop UPnP port mapping if running
		if s.upnpMgr != nil {
			s.upnpMgr.Stop()
		}

		// Signal shutdown
		s.cancel()
		close(s.quit)

		// Close listener
		if s.listener != nil {
			s.listener.Close()
		}

		// Disconnect all peers in parallel (each Peer.Stop has a 5s timeout,
		// sequential iteration with N peers = N*5s worst case).
		var peerWg sync.WaitGroup
		s.peers.Range(func(key, value interface{}) bool {
			peer := value.(*Peer)
			peerWg.Add(1)
			go func() {
				defer peerWg.Done()
				peer.Stop()
			}()
			return true
		})

		peerDone := make(chan struct{})
		go func() {
			peerWg.Wait()
			close(peerDone)
		}()

		select {
		case <-peerDone:
			s.logger.Debug("All peers disconnected")
		case <-time.After(5 * time.Second):
			s.logger.Warn("Peer disconnect timeout - continuing shutdown")
		}

		// Wait for server goroutines
		done := make(chan struct{})
		go func() {
			s.wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			s.logger.Debug("P2P server stopped gracefully")
		case <-time.After(5 * time.Second):
			s.logger.Warn("P2P server stop timeout - forcing shutdown")
		}

		close(s.done)
		s.started.Store(false)
	})
}

// startListener starts the network listener
func (s *Server) startListener(listenAddr string) error {
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", listenAddr, err)
	}

	s.listener = listener
	s.listening.Store(true)

	// Extract local address
	if tcpAddr, ok := listener.Addr().(*net.TCPAddr); ok {
		s.localAddr = &NetAddress{
			Time:     uint32(time.Now().Unix()),
			Services: s.services,
			IP:       tcpAddr.IP,
			Port:     uint16(tcpAddr.Port),
		}
	}

	s.logger.WithField("address", listenAddr).Info("P2P server listening")

	// Start accepting connections
	s.wg.Add(1)
	go s.listenLoop()

	return nil
}

// listenLoop handles incoming connections
func (s *Server) listenLoop() {
	defer s.wg.Done()

	for {
		select {
		case <-s.quit:
			return
		default:
		}

		// Set accept timeout
		if tcpListener, ok := s.listener.(*net.TCPListener); ok {
			tcpListener.SetDeadline(time.Now().Add(time.Second))
		}

		conn, err := s.listener.Accept()
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue // Timeout is expected
			}
			if !s.started.Load() {
				return // Server is shutting down
			}
			s.logger.WithError(err).Warning("Failed to accept connection")
			continue
		}

		// Rate limit connections
		select {
		case <-s.rateLimiter:
			go s.handleInboundConnection(conn)
		default:
			s.logger.Warn("Connection rate limit exceeded, dropping connection")
			conn.Close()
		}
	}
}

// handleInboundConnection processes a new inbound connection
func (s *Server) handleInboundConnection(conn net.Conn) {
	defer func() {
		// Return rate limit token
		select {
		case s.rateLimiter <- struct{}{}:
		default:
		}
	}()

	// Check whitelist (legacy: -whitelist)
	if s.whitelist != nil && s.whitelist.IsEnabled() {
		if !s.whitelist.IsWhitelistedAddr(conn.RemoteAddr()) {
			s.logger.WithField("remote_addr", conn.RemoteAddr().String()).Debug("Connection rejected: IP not in whitelist")
			conn.Close()
			return
		}
	}

	// Check connection limits (maxInbound derived from config.Network.MaxPeers)
	if s.inbounds.Load() >= s.maxInbound {
		s.logger.Debug("Inbound connection limit reached, rejecting connection")
		conn.Close()
		return
	}

	// Apply socket buffer options (legacy: -maxreceivebuffer, -maxsendbuffer)
	if s.socketOpts != nil {
		ApplySocketOptions(conn, s.socketOpts, s.logger)
	}

	// Create peer with network magic and configurable queue size
	queueSize := s.config.Network.PeerWriteQueueSize
	if queueSize == 0 {
		queueSize = DefaultBlockQueueSize // Default if not configured
	}
	peer := NewPeerWithQueueSize(conn, true, s.params.NetMagicBytes, s.logger.Logger, queueSize)
	peer.server = s
	s.applyConfigToPeer(peer)
	s.totalConnections.Add(1)

	s.logger.WithFields(logrus.Fields{
		"remote_addr": conn.RemoteAddr().String(),
		"inbound":     true,
	}).Debug("New inbound connection")

	// Add to peer management
	select {
	case s.newPeers <- peer:
	case <-s.quit:
		peer.Stop()
		return
	}

	// Start peer processing
	peer.Start(s)
}

// peerHandler manages peer lifecycle
func (s *Server) peerHandler() {
	defer s.wg.Done()

	for {
		select {
		case peer := <-s.newPeers:
			s.addPeer(peer)

		case peer := <-s.donePeers:
			s.removePeer(peer)

		case req := <-s.banPeers:
			s.banPeer(req.peer, req.reason)

		case <-s.quit:
			return
		}
	}
}

// handshakeHandler processes version/verack messages in a dedicated goroutine.
// This ensures handshake completion is NEVER blocked by slow message handlers
// (e.g., dseg/mnb flood with ECDSA verification, blockchain lock contention).
// Previously, handshake messages shared the messageHandler goroutine and could
// starve when any handler blocked for > 30s (handshake watchdog timeout),
// causing all new peers to be disconnected before completing version exchange.
func (s *Server) handshakeHandler() {
	defer s.wg.Done()

	for {
		select {
		case msg := <-s.handshakeChan:
			s.handlePeerMessage(msg)
			s.totalMessages.Add(1)
		case <-s.quit:
			return
		}
	}
}

// messageHandler processes incoming messages from peers.
// Uses a two-tier priority-drain pattern:
//   - Tier 1 (high): msgChan — blocks, headers, inv, tx, etc.
//   - Tier 2 (low): addrChan — addr messages that can flood during peer discovery
//
// Note: handshakeChan is processed by the dedicated handshakeHandler goroutine,
// ensuring version/verack are never blocked by slow handlers here.
func (s *Server) messageHandler() {
	defer s.wg.Done()

	for {
		// Phase 1: Drain ALL pending high-priority messages before checking addr.
		drained := true
		for drained {
			select {
			case msg := <-s.msgChan:
				s.handlePeerMessage(msg)
				s.totalMessages.Add(1)
			case <-s.quit:
				return
			default:
				drained = false
			}
		}

		// Phase 2: Fair select across both channels (including low-priority addr).
		// Only reached when msgChan is empty.
		select {
		case msg := <-s.msgChan:
			s.handlePeerMessage(msg)
			s.totalMessages.Add(1)

		case msg := <-s.addrChan:
			s.handlePeerMessage(msg)
			s.totalMessages.Add(1)

		case <-s.quit:
			return
		}
	}
}

// runHeavyHandler offloads an expensive message handler to a goroutine,
// gated by heavyHandlerSem to limit concurrency.  If the semaphore is full
// the request is silently dropped — the peer can retry.  This prevents
// slow handlers (dseg, getblocks, getheaders, mempool) from blocking the
// single messageHandler goroutine and causing msgChan overflow.
func (s *Server) runHeavyHandler(peer *Peer, msg *Message, handler func(*Peer, *Message)) {
	select {
	case s.heavyHandlerSem <- struct{}{}:
		go func() {
			defer func() { <-s.heavyHandlerSem }()
			handler(peer, msg)
		}()
	default:
		s.logger.WithFields(logrus.Fields{
			"peer":    peer.GetAddress().String(),
			"command": msg.GetCommand(),
		}).Debug("Heavy handler semaphore full, dropping request")
	}
}

// incrMsgType atomically increments a per-command-type counter in a sync.Map.
func (s *Server) incrMsgType(m *sync.Map, cmd string) {
	if v, ok := m.Load(cmd); ok {
		v.(*atomic.Uint64).Add(1)
		return
	}
	c := &atomic.Uint64{}
	c.Add(1)
	if actual, loaded := m.LoadOrStore(cmd, c); loaded {
		actual.(*atomic.Uint64).Add(1)
	}
}

// snapshotMsgTypes returns a map of command->count and resets all counters.
func snapshotMsgTypes(m *sync.Map) map[string]uint64 {
	result := make(map[string]uint64)
	m.Range(func(key, value any) bool {
		cmd := key.(string)
		counter := value.(*atomic.Uint64)
		if n := counter.Swap(0); n > 0 {
			result[cmd] = n
		}
		return true
	})
	return result
}

// formatMsgTypes formats a map as "inv:523 block:12 tx:890".
func formatMsgTypes(m map[string]uint64) string {
	if len(m) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(m))
	for cmd, n := range m {
		parts = append(parts, fmt.Sprintf("%s:%d", cmd, n))
	}
	return strings.Join(parts, " ")
}

// statsLoop logs P2P throughput and queue health every 30 seconds.
func (s *Server) statsLoop() {
	defer s.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	var prevRecv, prevDropped uint64

	for {
		select {
		case <-ticker.C:
			recv := s.totalMessages.Load()
			dropped := s.droppedMessages.Load()

			deltaRecv := recv - prevRecv
			deltaDropped := dropped - prevDropped
			prevRecv = recv
			prevDropped = dropped

			// Count connected peers and sum sent messages
			var peerCount int
			var totalPeerSent uint64
			s.peers.Range(func(_, value any) bool {
				if p, ok := value.(*Peer); ok && p.IsHandshakeComplete() {
					peerCount++
					totalPeerSent += p.bytesSent.Load()
				}
				return true
			})

			recvTypes := snapshotMsgTypes(&s.msgTypeRecv)
			sentTypes := snapshotMsgTypes(&s.msgTypeSent)

			s.logger.WithFields(logrus.Fields{
				"recv_30s":      deltaRecv,
				"dropped_30s":   deltaDropped,
				"peers":         peerCount,
				"msgChan":       len(s.msgChan),
				"msgChan_cap":   cap(s.msgChan),
				"handshakeChan": len(s.handshakeChan),
				"addrChan":      len(s.addrChan),
				"heavyHandlers": len(s.heavyHandlerSem),
				"total_recv":    recv,
				"total_dropped": dropped,
				"recv_types":    formatMsgTypes(recvTypes),
				"sent_types":    formatMsgTypes(sentTypes),
			}).Debug("P2P stats")

		case <-s.quit:
			return
		}
	}
}

// connectionManager handles outbound connection requests
func (s *Server) connectionManager() {
	defer s.wg.Done()

	for {
		select {
		case req := <-s.connRequests:
			go s.handleConnectionRequest(req)

		case <-s.quit:
			return
		}
	}
}

// outboundConnectionManager maintains outbound connections
func (s *Server) outboundConnectionManager() {
	defer s.wg.Done()

	ticker := time.NewTicker(5 * time.Second) // 6x faster than previous 30s
	defer ticker.Stop()

	// Initialize bootstrap phase tracking
	s.bootstrapStartTime = time.Now()
	s.seedsActivated.Store(false)
	s.knownPeersAttemptCount.Store(1) // First attempt

	// Initial connections to known peers (NOT seeds - they are emergency fallback)
	s.connectToKnownPeers()

	for {
		select {
		case <-ticker.C:
			s.maintainConnections()

		case <-s.quit:
			return
		}
	}
}

// addPeer adds a peer to the server
func (s *Server) addPeer(peer *Peer) {
	addr := peer.GetAddress().String()

	// Check if peer already exists
	if _, exists := s.peers.LoadOrStore(addr, peer); exists {
		s.logger.WithField("peer", addr).Debug("Peer already exists, closing duplicate")
		peer.Stop()
		return
	}

	// Update counters
	s.peerCount.Add(1)
	if peer.inbound {
		s.inbounds.Add(1)
	} else {
		s.outbounds.Add(1)
	}

	s.logger.WithFields(logrus.Fields{
		"peer":           addr,
		"inbound":        peer.inbound,
		"total":          s.peerCount.Load(),
		"inbound_count":  s.inbounds.Load(),
		"outbound_count": s.outbounds.Load(),
	}).Debug("Peer connected")

	// Enforce handshake completion deadline for newly connected peers.
	// This prevents half-open peers from lingering indefinitely.
	s.startHandshakeWatchdog(peer)

	// Mark address as successfully connected in discovery system
	if s.discovery != nil {
		s.discovery.MarkSuccess(peer.GetAddress())
	}

	// For outbound connections, we initiate the handshake by sending version message
	if !peer.inbound {
		s.logger.WithField("peer", addr).Debug("Sending version message to outbound peer")

		ourVersion := s.createVersionMessage(peer.GetAddress())

		// Register nonce for self-connection detection before sending.
		s.registerSentNonce(ourVersion.Nonce)
		peer.localNonce.Store(ourVersion.Nonce)

		versionPayload, err := SerializeVersionMessage(ourVersion)
		if err != nil {
			s.logger.WithError(err).WithField("peer", addr).
				Error("Failed to serialize version message")
			select {
			case s.donePeers <- peer:
			case <-s.quit:
			}
			return
		}

		versionMsg := NewMessage(MsgVersion, versionPayload, s.params.NetMagicBytes)
		if err := peer.SendMessage(versionMsg); err != nil {
			s.logger.WithError(err).WithField("peer", addr).
				Error("Failed to send version message to outbound peer")
			select {
			case s.donePeers <- peer:
			case <-s.quit:
			}
			return
		}

		s.logger.WithField("peer", addr).Debug("Version message sent to outbound peer")
	}

	// Trigger handler
	if s.onNewPeer != nil {
		s.onNewPeer(peer)
	}
}

func (s *Server) handshakeTimeout() time.Duration {
	if s.handshakeTTL <= 0 {
		return HandshakeTimeout
	}
	return s.handshakeTTL
}

func (s *Server) startHandshakeWatchdog(peer *Peer) {
	timeout := s.handshakeTimeout()
	if timeout <= 0 {
		return
	}

	go func() {
		timer := time.NewTimer(timeout)
		defer timer.Stop()

		select {
		case <-timer.C:
			if peer.IsHandshakeComplete() {
				return
			}

			s.logger.WithField("peer", peer.GetAddress().String()).
				Warn("Disconnecting peer due to handshake timeout")

			select {
			case s.donePeers <- peer:
			case <-s.quit:
			}
		case <-peer.quit:
			return
		case <-s.quit:
			return
		}
	}()
}

// removePeer removes a peer from the server
func (s *Server) removePeer(peer *Peer) {
	addr := peer.GetAddress().String()

	if _, exists := s.peers.LoadAndDelete(addr); !exists {
		return
	}

	// Stop peer connection and goroutines to prevent zombie peers.
	// Without this, peers removed by PruneOutdatedOutboundPeers or DisconnectPeer
	// keep their TCP connection open and continue sending/receiving messages,
	// causing ghost entries in the health tracker via INV message processing.
	peer.Stop()

	s.txRelayMu.Lock()
	delete(s.peerTxRelay, addr)
	s.txRelayMu.Unlock()

	// Clean up self-connection detection nonce for this peer.
	if nonce := peer.localNonce.Load(); nonce != 0 {
		s.removeSentNonce(nonce)
	}

	// Update counters
	s.peerCount.Add(-1)
	if peer.inbound {
		s.inbounds.Add(-1)
	} else {
		s.outbounds.Add(-1)
	}

	s.logger.WithFields(logrus.Fields{
		"peer":           addr,
		"inbound":        peer.inbound,
		"total":          s.peerCount.Load(),
		"inbound_count":  s.inbounds.Load(),
		"outbound_count": s.outbounds.Load(),
	}).Debug("Peer disconnected")

	// Trigger handler
	if s.onPeerDone != nil {
		s.onPeerDone(peer)
	}
}

// banPeer bans a peer with the given reason.
func (s *Server) banPeer(peer *Peer, reason string) {
	addr := peer.GetAddress()
	s.logger.WithFields(logrus.Fields{
		"peer":   addr.String(),
		"reason": reason,
	}).Warn("Banning peer")

	// Mark address as bad in discovery system
	if s.discovery != nil {
		s.discovery.MarkBad(addr, reason)
	}

	// Remove peer (also stops peer connection via peer.Stop())
	s.removePeer(peer)

	// Add IP to ban list using configured ban time (legacy: -bantime)
	// Convert single IP to CIDR subnet format
	subnet := fmt.Sprintf("%s/32", addr.IP.String()) // /32 = single IP for IPv4
	if addr.IP.To4() == nil {
		subnet = fmt.Sprintf("%s/128", addr.IP.String()) // /128 = single IP for IPv6
	}

	banTime := int64(s.config.Network.BanTime) // Use configured ban time (default 86400 = 24h)
	if err := s.BanSubnet(subnet, banTime, false, reason); err != nil {
		s.logger.WithError(err).WithField("subnet", subnet).
			Error("Failed to add peer to ban list")
	}
}

// Misbehaving increases peer's misbehavior score and bans if threshold exceeded.
// Legacy: implements TWINS Misbehaving() function from main.cpp
// howmuch: Points to add to the misbehavior score
// Returns true if peer was banned
func (s *Server) Misbehaving(peer *Peer, howmuch int32, reason string) bool {
	if peer == nil {
		return false
	}

	newScore := peer.AddMisbehavior(howmuch)
	banThreshold := int32(s.config.Network.BanScore) // Default: 100
	oldScore := newScore - howmuch

	s.logger.WithFields(logrus.Fields{
		"peer":      peer.GetAddress().String(),
		"added":     howmuch,
		"score":     newScore,
		"threshold": banThreshold,
		"reason":    reason,
	}).Debug("Peer misbehavior recorded")

	// Only ban on first threshold crossing to prevent repeated ban requests
	// from messages still in the TCP buffer after the initial ban is queued
	if newScore >= banThreshold && oldScore < banThreshold {
		s.logger.WithFields(logrus.Fields{
			"peer":   peer.GetAddress().String(),
			"score":  newScore,
			"reason": reason,
		}).Warn("Banning peer due to misbehavior threshold exceeded")

		select {
		case s.banPeers <- banRequest{peer: peer, reason: reason}:
			return true
		case <-s.quit:
			return false
		}
	}

	return newScore >= banThreshold
}

// PruneOutdatedOutboundPeers disconnects outbound peers significantly off consensus
// Returns the number of peers pruned
func (s *Server) PruneOutdatedOutboundPeers(consensusHeight uint32, maxDiff uint32) int {
	if s.healthTracker == nil {
		return 0
	}

	pruned := 0
	var peersToRemove []*Peer

	// Get current sync peer to avoid pruning it
	var currentSyncPeer string
	if s.syncer != nil && s.syncer.syncPeer.Load() != nil {
		currentSyncPeer = s.syncer.syncPeer.Load().GetAddress().String()
	}

	s.peers.Range(func(key, value interface{}) bool {
		peer := value.(*Peer)
		peerAddr := peer.GetAddress().String()

		// Only prune outbound peers (we connected to them)
		if peer.inbound {
			return true // Keep inbound peers
		}

		// Never prune the peer we're currently syncing with
		if currentSyncPeer != "" && peerAddr == currentSyncPeer {
			s.logger.WithField("peer", peerAddr).
				Debug("Skipping prune of current sync peer")
			return true
		}

		// Get peer's reported height from health tracker
		stats := s.healthTracker.GetStats(peerAddr)
		if stats == nil {
			return true // No stats, keep for now
		}

		// Calculate height difference
		var diff uint32
		if consensusHeight > stats.TipHeight {
			diff = consensusHeight - stats.TipHeight
		} else {
			diff = stats.TipHeight - consensusHeight
		}

		// Mark for removal if too far from consensus
		if diff > maxDiff {
			s.logger.WithFields(logrus.Fields{
				"peer":             peerAddr,
				"peer_height":      stats.TipHeight,
				"consensus_height": consensusHeight,
				"diff":             diff,
				"max_allowed":      maxDiff,
			}).Debug("Pruning outdated outbound peer")

			peersToRemove = append(peersToRemove, peer)
			pruned++
		}

		return true
	})

	// Remove peers outside the Range loop to avoid concurrent modification
	for _, peer := range peersToRemove {
		select {
		case s.donePeers <- peer:
		case <-s.quit:
			return pruned
		}
	}

	if pruned > 0 {
		s.logger.WithFields(logrus.Fields{
			"pruned":           pruned,
			"consensus_height": consensusHeight,
			"max_diff":         maxDiff,
		}).Debug("Pruned outdated outbound peers")
	}

	return pruned
}

// handlePeerMessage processes a message from a peer
func (s *Server) handlePeerMessage(peerMsg *PeerMessage) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.WithFields(logrus.Fields{
				"peer":    peerMsg.Peer.GetAddress().String(),
				"command": peerMsg.Message.GetCommand(),
				"panic":   r,
			}).Error("Panic in message handler")
		}
	}()

	msg := peerMsg.Message
	peer := peerMsg.Peer

	// Drop messages from disconnected peers. The ban is processed asynchronously
	// by peerHandler, so messageHandler may still have queued messages from a
	// peer that was already banned. Without this check, those messages produce
	// repeated "subnet already banned" log spam.
	// Check quit channel (not connected flag) because connected is only set
	// after handshake — pre-handshake messages must still be processed.
	select {
	case <-peer.quit:
		return
	default:
	}

	cmd := msg.GetCommand()

	s.logger.WithFields(logrus.Fields{
		"peer":    peer.GetAddress().String(),
		"command": cmd,
		"size":    len(msg.Payload),
	}).Debug("Processing peer message")

	// Update statistics
	s.bytesReceived.Add(uint64(len(msg.Payload) + 24)) // 24 bytes header
	s.incrMsgType(&s.msgTypeRecv, cmd)

	// Reject all messages (except version/verack) before handshake is complete.
	// Both version and verack are part of the handshake sequence and must pass through.
	// Legacy: main.cpp:5871-5875 — Misbehaving(pfrom->GetId(), 1)
	if MessageType(cmd) != MsgVersion && MessageType(cmd) != MsgVerAck && !peer.IsHandshakeComplete() {
		s.logger.WithFields(logrus.Fields{
			"peer":    peer.GetAddress().String(),
			"command": cmd,
		}).Debug("Received message before version handshake")
		s.Misbehaving(peer, 1, "message before version handshake")
		return
	}

	// Handle message based on type
	switch MessageType(cmd) {
	case MsgVersion:
		s.handleVersionMessage(peer, msg)
	case MsgVerAck:
		s.handleVerAckMessage(peer, msg)
	case MsgPing:
		s.handlePingMessage(peer, msg)
	// Note: MsgPong handled directly in peer.readLoop() to avoid msgChan bottleneck
	case MsgAddr:
		s.handleAddrMessage(peer, msg)
	case MsgGetAddr:
		s.handleGetAddrMessage(peer, msg)
	case MsgInv:
		s.handleInvMessage(peer, msg)
	case MsgGetData:
		s.handleGetDataMessage(peer, msg)
	case MsgGetBlocks:
		s.runHeavyHandler(peer, msg, s.handleGetBlocksMessage)
	case MsgGetHeaders:
		s.runHeavyHandler(peer, msg, s.handleGetHeadersMessage)
	case MsgHeaders:
		s.handleHeadersMessage(peer, msg)
	case MsgBlock:
		s.handleBlockMessage(peer, msg)
	case MsgTx:
		s.handleTxMessage(peer, msg)
	// Compatibility handlers for deprecated/legacy messages
	case MsgAlert:
		s.handleAlertMessage(peer, msg)
	case MsgFilterLoad:
		s.handleFilterLoadMessage(peer, msg)
	case MsgFilterAdd:
		s.handleFilterAddMessage(peer, msg)
	case MsgFilterClear:
		s.handleFilterClearMessage(peer, msg)
	case MsgMemPool:
		s.runHeavyHandler(peer, msg, s.handleMemPoolMessage)
	case MsgMerkleBlock:
		s.handleMerkleBlockMessage(peer, msg)
	case MsgMNFinal:
		s.handleMNFinalMessage(peer, msg)
	case MsgFBVote:
		s.handleFBVoteMessage(peer, msg)
	// Advanced message handlers
	// NOTE: SwiftTX, Budget, and PrivateSend handlers removed (permanently disabled)
	case MsgSpork:
		s.handleSpork(peer, msg)
	case MsgGetSporks:
		s.handleGetSporks(peer, msg)
	case MsgMasternode:
		s.handleMasternodeBroadcast(peer, msg)
	case MsgMNPing:
		s.handleMasternodePing(peer, msg)
	case MsgMasternodeWinner:
		s.handleMasternodeWinner(peer, msg)
	case MsgMNGet:
		s.handleMNGet(peer, msg)
	case MsgDSEG:
		s.runHeavyHandler(peer, msg, s.handleDSEG)
	case MsgMasternodeScanningError:
		s.handleMasternodeScanningError(peer, msg)
	case MsgMasternodeQuorum:
		s.handleMasternodeQuorum(peer, msg)
	case MsgSSC:
		s.handleSyncStatusCount(peer, msg)
	// Protocol 70928: chain state query messages
	case MsgGetChainState:
		s.handleGetChainStateMessage(peer, msg)
	case MsgChainState:
		s.handleChainStateMessage(peer, msg)
	default:
		s.logger.WithFields(logrus.Fields{
			"peer":    peer.GetAddress().String(),
			"command": msg.GetCommand(),
		}).Debug("Unknown message type")
	}

	// Trigger custom handler
	if s.onNewMessage != nil {
		s.onNewMessage(peerMsg)
	}
}

// calculateBackoff calculates exponential backoff duration based on failed attempts
func (s *Server) calculateBackoff(attempts int32) time.Duration {
	if attempts <= 0 {
		return 0
	}
	// Exponential backoff: 30s, 1m, 2m, 4m, 8m, max 15m
	backoff := time.Duration(30*attempts*attempts) * time.Second
	maxBackoff := 15 * time.Minute
	if backoff > maxBackoff {
		backoff = maxBackoff
	}
	return backoff
}

// shouldConnectToPeer checks if we should attempt to connect based on backoff
func (s *Server) shouldConnectToPeer(addr string) bool {
	s.connBackoffMu.RLock()
	backoff, exists := s.connBackoff[addr]
	s.connBackoffMu.RUnlock()

	if !exists {
		return true // No history, allow connection
	}

	// Check if backoff period has expired
	if time.Now().Before(backoff.BackoffUntil) {
		s.logger.WithFields(logrus.Fields{
			"peer":            addr,
			"failed_attempts": backoff.FailedAttempts,
			"backoff_until":   backoff.BackoffUntil,
			"time_remaining":  time.Until(backoff.BackoffUntil).Round(time.Second),
		}).Debug("Peer in backoff period, skipping connection")
		return false
	}

	return true
}

// recordConnectionFailure records a failed connection attempt
func (s *Server) recordConnectionFailure(addr string, err error) {
	s.connBackoffMu.Lock()
	defer s.connBackoffMu.Unlock()

	backoff, exists := s.connBackoff[addr]
	if !exists {
		backoff = &ConnectionBackoff{}
		s.connBackoff[addr] = backoff
	}

	backoff.FailedAttempts++
	backoff.LastAttempt = time.Now()
	backoff.LastFailure = time.Now()

	// Calculate backoff duration
	backoffDuration := s.calculateBackoff(backoff.FailedAttempts)
	backoff.BackoffUntil = time.Now().Add(backoffDuration)

	s.logger.WithFields(logrus.Fields{
		"peer":             addr,
		"failed_attempts":  backoff.FailedAttempts,
		"backoff_until":    backoff.BackoffUntil,
		"backoff_duration": backoffDuration,
		"error":            err.Error(),
	}).Debug("Recorded connection failure")
}

// recordConnectionSuccess resets backoff on successful connection
func (s *Server) recordConnectionSuccess(addr string) {
	s.connBackoffMu.Lock()
	defer s.connBackoffMu.Unlock()

	// Reset backoff on successful connection
	delete(s.connBackoff, addr)
}

// IsSynced returns true when the node is fully synced with the network.
// This exposes the syncer's sync state for use by the consensus layer
// for masternode payment validation enforcement.
//
// Used by: consensus.BlockValidator.SetSyncedFunc(server.IsSynced)
func (s *Server) IsSynced() bool {
	if s.syncer == nil {
		return false
	}
	return s.syncer.IsSynced()
}

// handleConnectionRequest processes an outbound connection request
func (s *Server) handleConnectionRequest(req *connRequest) {
	if s.outbounds.Load() >= s.maxOutbound {
		req.reply <- errors.New("outbound connection limit reached")
		return
	}

	// Check if peer is in backoff period (unless permanent)
	if !req.permanent && !s.shouldConnectToPeer(req.addr) {
		req.reply <- errors.New("peer in backoff period")
		return
	}

	// Parse address to NetAddress for discovery tracking
	host, portStr, err := net.SplitHostPort(req.addr)
	if err == nil {
		ip := net.ParseIP(host)
		if ip != nil {
			var port uint16
			if p, err := net.ResolveTCPAddr("tcp", net.JoinHostPort("", portStr)); err == nil {
				port = uint16(p.Port)
			}
			netAddr := &NetAddress{
				IP:   ip,
				Port: port,
			}
			// Mark connection attempt in discovery system
			if s.discovery != nil {
				s.discovery.MarkAttempt(netAddr)
			}
		}
	}

	peer, err := Connect(req.addr, s.params.NetMagicBytes, s.logger.Logger)
	if err != nil {
		s.recordConnectionFailure(req.addr, err)
		req.reply <- err
		return
	}

	peer.SetPersistent(req.permanent)

	select {
	case s.newPeers <- peer:
		peer.Start(s)
		s.recordConnectionSuccess(req.addr)
		req.reply <- nil
	case <-s.quit:
		peer.Stop()
		req.reply <- errors.New("server shutting down")
	}
}

// connectToKnownPeers connects to known peers from discovery (peers.json) instead of seeds
// Seeds are reserved as emergency fallback if we can't reach enough known peers
func (s *Server) connectToKnownPeers() {
	// First connect to addNodes as permanent connections (legacy: -addnode)
	// These always take priority regardless of seed/known peer logic
	for _, node := range s.addNodes {
		if s.outbounds.Load() >= s.maxOutbound {
			break
		}

		s.logger.WithField("addnode", node).Debug("Connecting to addnode (permanent)")
		s.ConnectPeer(node, true) // AddNodes are permanent - auto-reconnect
	}

	// Then try known peers from discovery (NOT seeds)
	// Discovery prioritizes peers with successful connection history from peers.json
	addresses := s.discovery.GetAddresses(int(s.maxOutbound) * 2)

	if len(addresses) > 0 {
		s.logger.WithField("known_peers", len(addresses)).Debug("Connecting to known peers from discovery")
	} else {
		s.logger.Debug("No known peers in discovery, will wait for bootstrap timeout before trying seeds")
	}

	groupCounts := s.getNetworkGroupCounts()

	for _, addr := range addresses {
		if s.outbounds.Load() >= s.maxOutbound {
			break
		}

		if s.isSelfAddress(addr) {
			s.logger.WithField("peer", addr.String()).Debug("Skipping self known-peer address")
			continue
		}

		// Check network group diversity - allow up to MaxConnectionsPerNetworkGroup per /16
		group := getNetworkGroup(addr.IP)
		if groupCounts[group] >= MaxConnectionsPerNetworkGroup {
			continue
		}

		s.logger.WithField("peer", addr.String()).Debug("Connecting to known peer")
		s.ConnectPeer(addr.String(), false)
		groupCounts[group]++
	}
}

// connectToSeeds connects to seed nodes (emergency fallback)
func (s *Server) connectToSeeds() {
	s.logger.Debug("Emergency: Activating seed nodes as fallback")

	// Connect to seed nodes
	for _, seed := range s.config.Network.Seeds {
		if s.outbounds.Load() >= s.maxOutbound {
			break
		}

		s.logger.WithField("seed", seed).Debug("Connecting to seed node")
		s.ConnectPeer(seed, false) // Seeds are not permanent
	}
}

// connectOnlyManager manages connections in -connect mode
// Only connects to specified nodes, no peer discovery
func (s *Server) connectOnlyManager() {
	defer s.wg.Done()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	// Initial connections to connectOnly nodes
	s.connectToConnectOnlyNodes()

	for {
		select {
		case <-ticker.C:
			// Maintain connections to connectOnly nodes
			s.maintainConnectOnlyConnections()

		case <-s.quit:
			return
		}
	}
}

// connectToConnectOnlyNodes connects to all connectOnly nodes
func (s *Server) connectToConnectOnlyNodes() {
	for _, node := range s.connectOnly {
		// Check if already connected
		if _, exists := s.peers.Load(node); exists {
			continue
		}

		if s.outbounds.Load() >= s.maxOutbound {
			break
		}

		s.logger.WithField("connect", node).Debug("Connecting to connect-only node (permanent)")
		s.ConnectPeer(node, true) // ConnectOnly nodes are permanent
	}
}

// maintainConnectOnlyConnections ensures connections to connectOnly nodes
func (s *Server) maintainConnectOnlyConnections() {
	for _, node := range s.connectOnly {
		if s.outbounds.Load() >= s.maxOutbound {
			break
		}
		// Check if already connected
		if _, exists := s.peers.Load(node); exists {
			continue
		}

		// Try to reconnect
		s.logger.WithField("connect", node).Debug("Reconnecting to connect-only node")
		s.ConnectPeer(node, true)
	}
}

// getNetworkGroup returns the /16 network group for an IP address
func getNetworkGroup(ip net.IP) string {
	if ip4 := ip.To4(); ip4 != nil {
		// IPv4: Use /16 prefix (first two octets)
		return fmt.Sprintf("%d.%d.0.0/16", ip4[0], ip4[1])
	} else if ip.To16() != nil {
		// IPv6: Use /32 prefix
		return fmt.Sprintf("%x:%x::/32",
			uint16(ip[0])<<8|uint16(ip[1]),
			uint16(ip[2])<<8|uint16(ip[3]))
	}
	return "unknown"
}

// getNetworkGroupCounts returns count of connections per /16 group
func (s *Server) getNetworkGroupCounts() map[string]int {
	groups := make(map[string]int)

	s.peers.Range(func(key, value interface{}) bool {
		peer := value.(*Peer)
		if !peer.inbound { // Only count outbound connections
			addr := peer.GetAddress()
			group := getNetworkGroup(addr.IP)
			groups[group]++
		}
		return true
	})

	return groups
}

// maintainConnections ensures we have enough connections
// maintainConnections manages outbound peer connections.
// Logic similar to legacy ThreadOpenConnections:
// 1. Always try addnodes (permanent connections)
// 2. If no known peers -> connect to seeds immediately
// 3. If 60 seconds pass without minimum peers -> activate seeds as fallback
// 4. Try known addresses with network diversity enforcement
func (s *Server) maintainConnections() {
	// Step 1: Always try addnodes (permanent connections)
	s.connectAddNodes()

	// Step 2: Check if we need more connections
	outboundCount := s.outbounds.Load()
	if outboundCount >= s.maxOutbound {
		return
	}

	// Step 3: Get known addresses and check seed activation
	knownAddresses := s.discovery.GetAddresses(int(s.maxOutbound) * 3)
	s.checkAndActivateSeeds(outboundCount, len(knownAddresses) > 0)

	// Step 4: Connect to seeds if activated and below minimum
	s.connectToSeedNodes(outboundCount)

	// Step 5: Connect to known peers with diversity enforcement
	groupCounts := s.getNetworkGroupCounts()
	s.attemptKnownAddressConnections(knownAddresses, groupCounts, outboundCount)

}

// connectAddNodes connects to configured addnode addresses (permanent connections).
func (s *Server) connectAddNodes() {
	for _, node := range s.addNodes {
		if s.outbounds.Load() >= s.maxOutbound {
			break
		}
		if _, exists := s.peers.Load(node); exists {
			continue
		}
		s.logger.WithField("addnode", node).Debug("Connecting to addnode")
		s.ConnectPeer(node, true)
	}
}

// checkAndActivateSeeds determines if seed nodes should be activated.
// Seeds are activated if: no known peers OR timeout reached without minimum peers.
func (s *Server) checkAndActivateSeeds(outboundCount int32, hasKnownPeers bool) {
	if s.seedsActivated.Load() {
		return // Already activated
	}

	const seedTimeout = 60 * time.Second
	timeSinceStart := time.Since(s.bootstrapStartTime)

	var reason string
	switch {
	case !hasKnownPeers:
		reason = "no known peers"
	case outboundCount < MinPeersBeforeSeeds && timeSinceStart > seedTimeout:
		reason = "seed timeout (60s without min peers)"
	default:
		return // No need to activate seeds
	}

	s.logger.WithFields(logrus.Fields{
		"outbound": outboundCount,
		"elapsed":  timeSinceStart.Round(time.Second),
		"reason":   reason,
	}).Debug("Activating seed nodes")
	s.seedsActivated.Store(true)
}

// connectToSeedNodes connects to configured seed nodes when activated.
func (s *Server) connectToSeedNodes(outboundCount int32) {
	if !s.seedsActivated.Load() || outboundCount >= MinPeersBeforeSeeds {
		return
	}

	for _, seed := range s.config.Network.Seeds {
		if s.outbounds.Load() >= s.maxOutbound {
			break
		}
		if s.isSelfAddressString(seed) {
			s.logger.WithField("seed", seed).Debug("Skipping self seed address")
			continue
		}
		if _, exists := s.peers.Load(seed); exists {
			continue
		}
		s.logger.WithField("seed", seed).Debug("Connecting to seed")
		s.ConnectPeer(seed, false)
	}
}

// attemptKnownAddressConnections connects to addresses from discovery with network diversity enforcement.
// Similar to legacy addrman.Select() loop with /16 subnet diversity check.
func (s *Server) attemptKnownAddressConnections(addresses []*NetAddress, groupCounts map[string]int, outboundCount int32) {
	const maxTries = 100
	triesLeft := maxTries
	skippedDiversity := 0
	attemptedCount := 0

	for _, addr := range addresses {
		if triesLeft <= 0 {
			break
		}
		triesLeft--

		// Never connect to our own advertised address.
		if s.isSelfAddress(addr) {
			continue
		}

		// Skip if already connected
		if _, exists := s.peers.Load(addr.String()); exists {
			continue
		}

		// Skip banned peers
		if s.IsBanned(addr.IP) {
			continue
		}

		// Check network group diversity
		group := getNetworkGroup(addr.IP)
		if groupCounts[group] >= MaxConnectionsPerNetworkGroup {
			skippedDiversity++
			continue
		}

		if s.outbounds.Load() >= s.maxOutbound {
			break
		}

		// Mark attempt and reserve group slot
		s.discovery.MarkAttempt(addr)
		groupCounts[group]++
		attemptedCount++

		if err := s.ConnectPeer(addr.String(), false); err != nil {
			s.logger.WithError(err).WithField("peer", addr.String()).Debug("Failed to connect")
		} else {
			s.logger.WithField("peer", addr.String()).Debug("Connected to peer")
		}
	}

	// Log if diversity is limiting connections
	if skippedDiversity > 10 && attemptedCount == 0 {
		s.logger.WithFields(logrus.Fields{
			"skipped_diversity": skippedDiversity,
			"known_addresses":   len(addresses),
			"outbound":          outboundCount,
			"groups":            len(groupCounts),
		}).Debug("All addresses skipped due to network diversity")
	}
}

// isSelfAddress returns true when the candidate address resolves to this node's
// own listener endpoint. This prevents accidental self-connections via discovery.
func (s *Server) isSelfAddress(addr *NetAddress) bool {
	if addr == nil || addr.IP == nil {
		return false
	}

	// Loopback endpoint is self if we listen on the same port.
	if addr.IP.IsLoopback() && s.localAddr != nil && addr.Port == s.localAddr.Port {
		return true
	}

	// Match against explicit local listener address when it is specific.
	if s.localAddr != nil && s.localAddr.IP != nil &&
		!s.localAddr.IP.IsUnspecified() && addr.Port == s.localAddr.Port &&
		addr.IP.Equal(s.localAddr.IP) {
		return true
	}

	// Match against externally detected/advertised IP when known.
	extIP := s.getExternalIP()
	if extIP != nil && s.localAddr != nil && addr.Port == s.localAddr.Port &&
		addr.IP.Equal(extIP) {
		return true
	}

	return false
}

func (s *Server) isSelfAddressString(addr string) bool {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}

	portNum, err := strconv.Atoi(portStr)
	if err != nil || portNum <= 0 || portNum > 65535 {
		return false
	}

	return s.isSelfAddress(&NetAddress{
		IP:   ip,
		Port: uint16(portNum),
	})
}

// registerSentNonce records a nonce from a version message we sent.
func (s *Server) registerSentNonce(nonce uint64) {
	s.sentNoncesMu.Lock()
	s.sentNonces[nonce] = struct{}{}
	s.sentNoncesMu.Unlock()
}

// removeSentNonce removes a nonce after the connection is no longer needed.
func (s *Server) removeSentNonce(nonce uint64) {
	s.sentNoncesMu.Lock()
	delete(s.sentNonces, nonce)
	s.sentNoncesMu.Unlock()
}

// isSelfNonce returns true when the nonce was generated by this node.
// Legacy: main.cpp:5760-5766 checks nLocalHostNonce for self-connection.
func (s *Server) isSelfNonce(nonce uint64) bool {
	s.sentNoncesMu.RLock()
	_, ok := s.sentNonces[nonce]
	s.sentNoncesMu.RUnlock()
	return ok
}

// Public API methods

// ConnectPeer connects to a peer
func (s *Server) ConnectPeer(addr string, permanent bool) error {
	req := &connRequest{
		addr:      addr,
		permanent: permanent,
		reply:     make(chan error, 1),
	}

	select {
	case s.connRequests <- req:
		return <-req.reply
	case <-s.quit:
		return errors.New("server shutting down")
	case <-time.After(30 * time.Second):
		return errors.New("connection request timeout")
	}
}

// DisconnectPeer disconnects a peer
func (s *Server) DisconnectPeer(addr string) error {
	if peerValue, exists := s.peers.Load(addr); exists {
		peer := peerValue.(*Peer)
		select {
		case s.donePeers <- peer:
			return nil
		case <-s.quit:
			return errors.New("server shutting down")
		}
	}
	return errors.New("peer not found")
}

// BanPeer bans a peer
func (s *Server) BanPeer(addr string) error {
	if peerValue, exists := s.peers.Load(addr); exists {
		peer := peerValue.(*Peer)
		select {
		case s.banPeers <- banRequest{peer: peer, reason: "manually added"}:
			return nil
		case <-s.quit:
			return errors.New("server shutting down")
		}
	}
	return errors.New("peer not found")
}

// BroadcastMessage broadcasts a message to all connected peers
func (s *Server) BroadcastMessage(msg *Message) {
	var sentCount int32
	s.peers.Range(func(key, value interface{}) bool {
		peer := value.(*Peer)
		if peer.IsConnected() && peer.IsHandshakeComplete() {
			if err := peer.SendMessage(msg); err == nil {
				sentCount++
			}
		}
		return true
	})

	s.logger.WithFields(logrus.Fields{
		"command": msg.GetCommand(),
		"peers":   sentCount,
	}).Debug("Broadcasted message")
}

// GetPeerInfo returns information about all connected peers
func (s *Server) GetPeerInfo() []*PeerStats {
	var peers []*PeerStats
	s.peers.Range(func(key, value interface{}) bool {
		peer := value.(*Peer)
		peers = append(peers, peer.GetStats())
		return true
	})
	return peers
}

// GetStats returns server statistics
func (s *Server) GetStats() *ServerStats {
	// Aggregate bytes sent from all connected peers
	var totalBytesSent uint64
	s.peers.Range(func(key, value interface{}) bool {
		peer := value.(*Peer)
		totalBytesSent += peer.bytesSent.Load()
		return true
	})

	stats := &ServerStats{
		Listening:        s.listening.Load(),
		PeerCount:        s.peerCount.Load(),
		InboundCount:     s.inbounds.Load(),
		OutboundCount:    s.outbounds.Load(),
		TotalConnections: s.totalConnections.Load(),
		TotalMessages:    s.totalMessages.Load(),
		BytesReceived:    s.bytesReceived.Load(),
		BytesSent:        totalBytesSent,
		Services:         s.services,
	}

	if s.localAddr != nil {
		stats.LocalAddress = s.localAddr.String()
		stats.ListenPort = s.localAddr.Port
	}

	if extIP := s.getExternalIP(); extIP != nil {
		stats.ExternalIP = extIP.String()
	}

	return stats
}

// SetEventHandlers sets event handlers for server events
func (s *Server) SetEventHandlers(onNewPeer, onPeerDone func(*Peer), onNewMessage func(*PeerMessage)) {
	s.onNewPeer = onNewPeer
	s.onPeerDone = onPeerDone
	s.onNewMessage = onNewMessage
}

// IsListening returns whether the server is listening
func (s *Server) IsListening() bool {
	return s.listening.Load()
}

// IsStarted returns whether the server is started
func (s *Server) IsStarted() bool {
	return s.started.Load()
}

// GetPeerCount returns the current peer count
func (s *Server) GetPeerCount() int32 {
	return s.peerCount.Load()
}

// GetLocalAddress returns the server's local address
func (s *Server) GetLocalAddress() *NetAddress {
	return s.localAddr
}

// SetExternalIP sets the server's external IP address (thread-safe).
func (s *Server) SetExternalIP(ip net.IP) {
	s.externalIP.Store(&ip)
}

// getExternalIP returns the server's external IP or nil (thread-safe).
func (s *Server) getExternalIP() net.IP {
	if p := s.externalIP.Load(); p != nil {
		return *p
	}
	return nil
}

// getDialTimeout returns the configured connection timeout from config, falling back to 30s.
func (s *Server) getDialTimeout() time.Duration {
	if s.config.Network.Timeout > 0 {
		return time.Duration(s.config.Network.Timeout) * time.Second
	}
	return 30 * time.Second
}

// getPingInterval returns the configured keep-alive interval from config, falling back to protocol default.
func (s *Server) getPingInterval() time.Duration {
	if s.config.Network.KeepAlive > 0 {
		return time.Duration(s.config.Network.KeepAlive) * time.Second
	}
	return PingInterval
}

// applyConfigToPeer sets configurable timeouts on a peer from the server's config.
func (s *Server) applyConfigToPeer(peer *Peer) {
	peer.pingInterval = s.getPingInterval()
	peer.dialTimeout = s.getDialTimeout()
}

// SetBlockchain sets the blockchain instance for block validation and storage
func (s *Server) SetBlockchain(bc blockchain.Blockchain) {
	s.blockchain = bc
	s.logger.Debug("Blockchain configured for P2P")
}

// SetMempool sets the mempool instance for transaction handling
func (s *Server) SetMempool(mp mempool.Mempool) {
	s.mempool = mp
	s.logger.Debug("Mempool configured for P2P")
}

// BroadcastTransaction adds a transaction to mempool and broadcasts it to peers
func (s *Server) BroadcastTransaction(tx *types.Transaction) error {
	if s.mempool == nil {
		return errors.New("mempool not configured")
	}

	// Add to mempool first (validates the transaction)
	if err := s.mempool.AddTransaction(tx); err != nil {
		// Legacy-compatible idempotence: duplicate mempool tx still allowed to relay.
		if mErr, ok := err.(*mempool.MempoolError); !ok || mErr.Code != mempool.RejectDuplicate {
			return fmt.Errorf("failed to add transaction to mempool: %w", err)
		}
	}

	// Relay to all connected peers
	s.relayTransaction(tx, nil)

	s.logger.WithField("hash", tx.Hash().String()).Debug("Transaction broadcast to network")
	return nil
}

// RelayTransaction relays an already-accepted transaction to peers.
// Use this for RPC paths that already called mempool.AddTransaction.
func (s *Server) RelayTransaction(tx *types.Transaction) error {
	if tx == nil {
		return errors.New("transaction is nil")
	}
	s.relayTransaction(tx, nil)
	return nil
}

// GetMempoolTransactions returns transactions from mempool for block creation
func (s *Server) GetMempoolTransactions(maxSize uint32, maxCount int) []*types.Transaction {
	if s.mempool == nil {
		return nil
	}
	return s.mempool.GetTransactionsForBlock(maxSize, maxCount)
}

// SetSyncer sets the blockchain syncer instance for block synchronization
func (s *Server) SetSyncer(syncer *BlockchainSyncer) {
	s.syncer = syncer
	s.logger.Debug("Blockchain syncer configured for P2P")
}

// SetMasternodeManager sets the masternode manager instance for masternode operations
func (s *Server) SetMasternodeManager(mnMgr MasternodeManager) {
	s.mnManager = mnMgr
	s.logger.Debug("Masternode manager configured for P2P")
}

// SetDebugCollector sets the debug event collector for masternode activity tracing.
func (s *Server) SetDebugCollector(collector *debug.Collector) {
	s.debugCollector.Store(collector)
	s.logger.Debug("Debug collector configured for P2P")
}

// SetPaymentValidator sets the masternode payment validator
func (s *Server) SetPaymentValidator(pv *consensus.MasternodePaymentValidator) {
	s.paymentValidator = pv
	s.logger.Debug("Payment validator configured for P2P")
}

// SetPeerTimeHandler sets the handler for peer time data (for network time adjustment)
func (s *Server) SetPeerTimeHandler(handler func(peerID string, timestamp uint32)) {
	s.onPeerTime = handler
	s.logger.Debug("Peer time handler configured for network time adjustment")
}

// SetExternalSporkHandler sets the handler for forwarding received sporks to the global spork manager
func (s *Server) SetExternalSporkHandler(handler func(sporkID int32, value int64, timestamp int64, signature []byte)) {
	s.externalSporkHandler = handler
	s.logger.Debug("External spork handler configured")
}

// BroadcastMasternodeWinner broadcasts a masternode winner vote to all connected peers.
// This implements the P2P relay for legacy CMasternodePaymentWinner::Relay() which calls
// RelayInv(MSG_MASTERNODE_WINNER, hash) followed by sending the actual "mnw" message.
// The vote is serialized using legacy wire format for network compatibility.
func (s *Server) BroadcastMasternodeWinner(vote *masternode.MasternodeWinnerVote) error {
	if vote == nil {
		return errors.New("vote is nil")
	}

	// Serialize the vote to wire format
	payload, err := vote.Serialize()
	if err != nil {
		return fmt.Errorf("failed to serialize winner vote: %w", err)
	}

	// Create the P2P message with "mnw" command
	msg := NewMessage(MsgMasternodeWinner, payload, s.params.NetMagicBytes)

	// Broadcast to all connected peers
	var sentCount int32
	s.peers.Range(func(key, value interface{}) bool {
		peer := value.(*Peer)
		if peer.IsConnected() && peer.IsHandshakeComplete() {
			if err := peer.SendMessage(msg); err == nil {
				sentCount++
			}
		}
		return true
	})

	s.logger.WithFields(logrus.Fields{
		"block_height": vote.BlockHeight,
		"voter":        vote.VoterOutpoint.String(),
		"peers":        sentCount,
	}).Debug("Broadcast masternode winner vote")

	return nil
}

// BroadcastMasternodeBroadcast broadcasts a masternode announcement to all connected peers.
// This implements the P2P relay for legacy CMasternodeBroadcast::Relay() which sends
// the "mnb" message to propagate masternode announcements across the network.
func (s *Server) BroadcastMasternodeBroadcast(mnb *masternode.MasternodeBroadcast, excludeAddr string) error {
	if mnb == nil {
		return errors.New("broadcast is nil")
	}

	// Serialize the broadcast to wire format
	payload, err := SerializeMasternodeBroadcast(mnb)
	if err != nil {
		return fmt.Errorf("failed to serialize masternode broadcast: %w", err)
	}

	// Create the P2P message with "mnb" command
	msg := NewMessage(MsgMasternode, payload, s.params.NetMagicBytes)

	// Broadcast to all connected peers, excluding the origin peer to avoid relay-bounce
	var sentCount int32
	s.peers.Range(func(key, value interface{}) bool {
		peer := value.(*Peer)
		// Skip origin peer - they already have this broadcast
		if excludeAddr != "" && peer.GetAddress().String() == excludeAddr {
			return true
		}
		if peer.IsConnected() && peer.IsHandshakeComplete() {
			if err := peer.SendMessage(msg); err == nil {
				sentCount++
			}
		}
		return true
	})

	s.logger.WithFields(logrus.Fields{
		"outpoint": mnb.OutPoint.String(),
		"addr":     mnb.Addr.String(),
		"peers":    sentCount,
	}).Debug("Broadcast masternode announcement")

	return nil
}

// BroadcastMasternodePing broadcasts masternode ping inventory to all connected peers.
// Legacy CMasternodePing::Relay() announces only INV(MSG_MASTERNODE_PING, hash).
// Peers fetch full ping payload via getdata, served by handleMasternodePingRequest.
func (s *Server) BroadcastMasternodePing(ping *masternode.MasternodePing) error {
	if ping == nil {
		return errors.New("ping is nil")
	}

	pingHash := ping.GetHash()

	// Broadcast INV announcement to all connected peers.
	// Full ping payload is transferred on demand via getdata.
	var sentCount int32
	s.peers.Range(func(key, value interface{}) bool {
		peer := value.(*Peer)
		if peer.IsConnected() && peer.IsHandshakeComplete() {
			invPayload := s.buildInvMessage(InvTypeMasternodePing, []types.Hash{pingHash})
			invMsg := NewMessage(MsgInv, invPayload, s.params.NetMagicBytes)
			if err := peer.SendMessage(invMsg); err == nil {
				sentCount++
			}
		}
		return true
	})

	s.logger.WithFields(logrus.Fields{
		"outpoint": ping.OutPoint.String(),
		"hash":     pingHash.String(),
		"sigtime":  ping.SigTime,
		"peers":    sentCount,
	}).Debug("Broadcast masternode ping inventory")

	return nil
}

// GetUserAgent returns the server's user agent string
func (s *Server) GetUserAgent() string {
	return s.userAgent
}

// GetServices returns the server's service flags
func (s *Server) GetServices() ServiceFlag {
	return s.services
}

// GetSyncer returns the blockchain syncer (for RPC access)
func (s *Server) GetSyncer() *BlockchainSyncer {
	return s.syncer
}

// GetHealthTracker returns the shared health tracker (for RPC access)
func (s *Server) GetHealthTracker() *PeerHealthTracker {
	return s.healthTracker
}

// GetBootstrap returns the bootstrap manager (for RPC access)
func (s *Server) GetBootstrap() *BootstrapManager {
	return s.bootstrap
}

// requestAddressesFromPeers sends getaddr messages to connected peers
func (s *Server) requestAddressesFromPeers() {
	count := 0
	maxRequests := 5 // Limit to 5 peers per request cycle to avoid overwhelming network

	s.peers.Range(func(key, value interface{}) bool {
		if count >= maxRequests {
			return false // Stop iteration
		}

		peer := value.(*Peer)

		// Send getaddr message to request peer addresses
		getAddrMsg := NewMessage(MsgGetAddr, []byte{}, s.params.NetMagicBytes)
		if err := peer.SendMessage(getAddrMsg); err != nil {
			s.logger.WithError(err).WithField("peer", peer.GetAddress().String()).
				Debug("Failed to send getaddr message")
		} else {
			peer.fGetAddr.Store(true) // Suppress relay of solicited addr responses
			s.logger.WithField("peer", peer.GetAddress().String()).
				Debug("Sent getaddr request to peer")
			count++
		}

		return true // Continue iteration
	})

	if count > 0 {
		s.logger.WithField("peers", count).Debug("Requested addresses from peers")
	}
}

// SyncPeerRequester interface implementation
// These methods are used by the masternode SyncManager to request sync data from peers

// RequestSporks sends getsporks message to connected peers
// Implements masternode.SyncPeerRequester interface
func (s *Server) RequestSporks() error {
	if s.peerCount.Load() == 0 {
		s.logger.Debug("No connected peers, cannot request sporks")
		return fmt.Errorf("no connected peers")
	}

	msg := NewMessage(MsgGetSporks, []byte{}, s.params.NetMagicBytes)
	var sentCount int32

	s.peers.Range(func(key, value interface{}) bool {
		peer := value.(*Peer)
		if peer.IsConnected() && peer.IsHandshakeComplete() {
			if err := peer.SendMessage(msg); err == nil {
				sentCount++
			}
		}
		return true
	})

	if sentCount == 0 {
		s.logger.Debug("No peers available to request sporks")
		return fmt.Errorf("no peers available")
	}

	s.logger.WithField("peers", sentCount).Debug("Requested sporks from peers")
	return nil
}

// RequestMasternodeList sends dseg message to connected peers to request masternode list
// Implements masternode.SyncPeerRequester interface
// Legacy: CMasternodeMan::DsegUpdate() in masternodeman.cpp
// Updated: Per-peer fulfilled request tracking like legacy pnode->HasFulfilledRequest("mnsync")
func (s *Server) RequestMasternodeList() (int, int, error) {
	if s.peerCount.Load() == 0 {
		s.logger.Debug("No connected peers, cannot request masternode list")
		return 0, 0, fmt.Errorf("no connected peers")
	}

	// dseg message payload is a serialized empty CTxIn (request full list)
	// Legacy format: CTxIn() serializes to 41 bytes:
	// - prevout.hash: 32 zero bytes
	// - prevout.n: 0xFFFFFFFF (4 bytes)
	// - scriptSig: empty (varint 0x00)
	// - nSequence: 0xFFFFFFFF (4 bytes)
	emptyTxIn := make([]byte, 41)
	// Set prevout.n to 0xFFFFFFFF (bytes 32-35)
	emptyTxIn[32] = 0xFF
	emptyTxIn[33] = 0xFF
	emptyTxIn[34] = 0xFF
	emptyTxIn[35] = 0xFF
	// scriptSig length is 0 (byte 36 is already 0)
	// Set nSequence to 0xFFFFFFFF (bytes 37-40)
	emptyTxIn[37] = 0xFF
	emptyTxIn[38] = 0xFF
	emptyTxIn[39] = 0xFF
	emptyTxIn[40] = 0xFF
	msg := NewMessage(MsgDSEG, emptyTxIn, s.params.NetMagicBytes)
	var sentCount int32
	var skippedCount int32

	s.peers.Range(func(key, value interface{}) bool {
		defer func() {
			if r := recover(); r != nil {
				s.logger.WithField("panic", r).Error("Panic in peer loop")
			}
		}()

		peer := value.(*Peer)
		peerAddr := peer.GetAddress().String()

		if !peer.IsConnected() || !peer.IsHandshakeComplete() {
			return true
		}

		// Check if this peer has already been asked (per-peer tracking like legacy)
		// Legacy: if (pnode->HasFulfilledRequest("mnsync")) continue;
		if s.mnManager != nil && s.mnManager.HasFulfilledRequest(peerAddr, "mnsync") {
			skippedCount++
			return true
		}

		if err := peer.SendMessage(msg); err == nil {
			sentCount++
			// Mark this peer as asked (like legacy pnode->FulfilledRequest("mnsync"))
			if s.mnManager != nil {
				s.mnManager.FulfilledRequest(peerAddr, "mnsync")
			}
		}
		return true
	})

	if sentCount == 0 && skippedCount == 0 {
		s.logger.Debug("No peers available to request masternode list")
		return 0, 0, fmt.Errorf("no peers available")
	}

	s.logger.WithFields(logrus.Fields{
		"sent":    sentCount,
		"skipped": skippedCount,
	}).Debug("Requested masternode list (dseg) from peers")
	return int(sentCount), int(skippedCount), nil
}

// RequestMasternodeWinners sends mnget message to connected peers to request winners
// Implements masternode.SyncPeerRequester interface
// Legacy: pnode->PushMessage("mnget", nMnCount) in masternode-sync.cpp:361
// Updated: Per-peer fulfilled request tracking like legacy pnode->HasFulfilledRequest("mnwsync")
func (s *Server) RequestMasternodeWinners(mnCount int) (int, int, error) {
	if s.peerCount.Load() == 0 {
		s.logger.Debug("No connected peers, cannot request masternode winners")
		return 0, 0, fmt.Errorf("no connected peers")
	}

	// mnget payload is the masternode count (4 bytes, little-endian int32)
	payload := make([]byte, 4)
	binary.LittleEndian.PutUint32(payload, uint32(mnCount))

	msg := NewMessage(MsgMNGet, payload, s.params.NetMagicBytes)
	var sentCount int32
	var skippedCount int32

	s.peers.Range(func(key, value interface{}) bool {
		peer := value.(*Peer)
		if !peer.IsConnected() || !peer.IsHandshakeComplete() {
			return true
		}

		peerAddr := peer.GetAddress().String()

		// Check if this peer has already been asked (per-peer tracking like legacy)
		// Legacy: if (pnode->HasFulfilledRequest("mnwsync")) continue;
		if s.mnManager != nil && s.mnManager.HasFulfilledRequest(peerAddr, "mnwsync") {
			skippedCount++
			return true
		}

		if err := peer.SendMessage(msg); err == nil {
			sentCount++
			// Mark this peer as asked (like legacy pnode->FulfilledRequest("mnwsync"))
			if s.mnManager != nil {
				s.mnManager.FulfilledRequest(peerAddr, "mnwsync")
			}
		}
		return true
	})

	if sentCount == 0 && skippedCount == 0 {
		s.logger.Debug("No peers available to request masternode winners")
		return 0, 0, fmt.Errorf("no peers available")
	}

	s.logger.WithFields(logrus.Fields{
		"sent":    sentCount,
		"skipped": skippedCount,
		"mnCount": mnCount,
	}).Debug("Requested masternode winners (mnget) from peers")
	return int(sentCount), int(skippedCount), nil
}

// GetConnectedPeerCount returns the number of connected peers
// Implements masternode.SyncPeerRequester interface
func (s *Server) GetConnectedPeerCount() int {
	return int(s.peerCount.Load())
}

// Orphan block handling methods

// AddOrphanBlock stores a block whose parent is not yet available
func (s *Server) AddOrphanBlock(block *types.Block) {
	blockHash := block.Hash()
	parentHash := block.Header.PrevBlockHash

	s.orphanBlocksMu.Lock()
	defer s.orphanBlocksMu.Unlock()

	// Check if already stored
	if _, exists := s.orphanBlocks[blockHash]; exists {
		return
	}

	// Limit orphan pool size to prevent memory exhaustion
	const maxOrphans = 100
	if len(s.orphanBlocks) >= maxOrphans {
		// Remove oldest orphan (simple FIFO - could be improved with LRU)
		for hash := range s.orphanBlocks {
			s.removeOrphanLocked(hash)
			break
		}
	}

	// Store the orphan
	s.orphanBlocks[blockHash] = block

	// Index by parent hash for quick lookup when parent arrives
	s.orphanBlocksByParent[parentHash] = append(s.orphanBlocksByParent[parentHash], blockHash)
}

// removeOrphanLocked removes an orphan from the pool (must hold lock)
func (s *Server) removeOrphanLocked(hash types.Hash) {
	block, exists := s.orphanBlocks[hash]
	if !exists {
		return
	}

	// Remove from main map
	delete(s.orphanBlocks, hash)

	// Remove from parent index
	parentHash := block.Header.PrevBlockHash
	orphans := s.orphanBlocksByParent[parentHash]
	for i, h := range orphans {
		if h == hash {
			s.orphanBlocksByParent[parentHash] = append(orphans[:i], orphans[i+1:]...)
			break
		}
	}
	// Clean up empty parent entry
	if len(s.orphanBlocksByParent[parentHash]) == 0 {
		delete(s.orphanBlocksByParent, parentHash)
	}
}

// GetOrphansForParent returns all orphan blocks waiting for the given parent
func (s *Server) GetOrphansForParent(parentHash types.Hash) []*types.Block {
	s.orphanBlocksMu.RLock()
	defer s.orphanBlocksMu.RUnlock()

	orphanHashes := s.orphanBlocksByParent[parentHash]
	if len(orphanHashes) == 0 {
		return nil
	}

	result := make([]*types.Block, 0, len(orphanHashes))
	for _, hash := range orphanHashes {
		if block, exists := s.orphanBlocks[hash]; exists {
			result = append(result, block)
		}
	}
	return result
}

// RemoveOrphan removes an orphan block from the pool
func (s *Server) RemoveOrphan(hash types.Hash) {
	s.orphanBlocksMu.Lock()
	defer s.orphanBlocksMu.Unlock()
	s.removeOrphanLocked(hash)
}

// RequestBlockFromPeers sends getdata request for a block to connected peers
func (s *Server) RequestBlockFromPeers(hash types.Hash) {
	// Check deduplication with stale retry (allow re-request after 30s)
	s.pendingBlockRequestsMu.Lock()
	if reqTime, exists := s.pendingBlockRequests[hash]; exists && time.Since(reqTime) < 30*time.Second {
		s.pendingBlockRequestsMu.Unlock()
		return
	}
	s.pendingBlockRequests[hash] = time.Now()
	s.pendingBlockRequestsMu.Unlock()

	// Build getdata message
	invList := []InventoryVector{{Type: InvTypeBlock, Hash: hash}}
	getDataMsg := &GetDataMessage{InvList: invList}
	payload, err := s.serializeGetDataMessage(getDataMsg)
	if err != nil {
		s.logger.WithError(err).Error("Failed to serialize getdata for orphan parent")
		return
	}
	msg := NewMessage(MsgGetData, payload, s.params.NetMagicBytes)

	// Send to first available peer (could be improved to request from multiple)
	sent := false
	s.peers.Range(func(key, value interface{}) bool {
		peer := value.(*Peer)
		if peer.IsConnected() && peer.IsHandshakeComplete() {
			if err := peer.SendMessage(msg); err == nil {
				s.logger.WithFields(logrus.Fields{
					"peer": peer.GetAddress().String(),
					"hash": hash.String(),
				}).Debug("Requested orphan parent from peer")
				sent = true
				return false // Stop after first successful send
			}
		}
		return true
	})

	if !sent {
		s.logger.WithField("hash", hash.String()).
			Warn("No peers available to request orphan parent")
		// Remove from pending since we couldn't send
		s.pendingBlockRequestsMu.Lock()
		delete(s.pendingBlockRequests, hash)
		s.pendingBlockRequestsMu.Unlock()
	}
}

// CleanupStaleRequests removes pending block requests older than timeout
func (s *Server) CleanupStaleRequests(timeout time.Duration) {
	s.pendingBlockRequestsMu.Lock()
	defer s.pendingBlockRequestsMu.Unlock()

	now := time.Now()
	for hash, requestTime := range s.pendingBlockRequests {
		if now.Sub(requestTime) > timeout {
			delete(s.pendingBlockRequests, hash)
		}
	}
}

// Fork detection constants
const (
	MaxAutoForkDepth     = 10               // Maximum depth for automatic fork recovery
	ForkDetectTimeout    = 10 * time.Second // Timeout waiting for peer responses
	MinPeersForConsensus = 3                // Minimum peers needed for fork consensus
)

// CheckForFork checks if we're on a fork by comparing orphan's parent with our chain
// Returns: isFork, forkDepth, forkPointHeight
func (s *Server) CheckForFork(orphanParentHash types.Hash, orphanHeight uint32) (bool, uint32, uint32) {
	if s.blockchain == nil {
		return false, 0, 0
	}

	// Expected height of the orphan's parent
	expectedParentHeight := orphanHeight - 1

	// Get what block we have at this height
	ourBlockHash, err := s.blockchain.GetBlockHash(expectedParentHeight)
	if err != nil {
		// We don't have a block at this height - not a fork, just missing blocks
		return false, 0, 0
	}

	// Compare hashes
	if ourBlockHash == orphanParentHash {
		// Same block - not a fork
		return false, 0, 0
	}

	// Different blocks at same height - potential fork!
	// Calculate fork depth (how many blocks we'd need to roll back)
	ourTipHeight, err := s.blockchain.GetBestHeight()
	if err != nil {
		return false, 0, 0
	}

	// Fork point is the parent of the conflicting block (one height before)
	forkPointHeight := expectedParentHeight - 1
	forkDepth := ourTipHeight - forkPointHeight

	return true, forkDepth, forkPointHeight
}

// RequestBlockFromAllPeers requests a block from all connected peers and counts responses
// Returns the number of peers that have this block
func (s *Server) RequestBlockFromAllPeers(hash types.Hash) int {
	// Build getdata message
	invList := []InventoryVector{{Type: InvTypeBlock, Hash: hash}}
	getDataMsg := &GetDataMessage{InvList: invList}
	payload, err := s.serializeGetDataMessage(getDataMsg)
	if err != nil {
		s.logger.WithError(err).Error("Failed to serialize getdata for fork check")
		return 0
	}
	msg := NewMessage(MsgGetData, payload, s.params.NetMagicBytes)

	// Send to all peers in parallel
	var sentCount int
	s.peers.Range(func(key, value interface{}) bool {
		peer := value.(*Peer)
		if peer.IsConnected() && peer.IsHandshakeComplete() {
			if err := peer.SendMessage(msg); err == nil {
				sentCount++
			}
		}
		return true
	})

	return sentCount
}

// TriggerForkRecovery initiates fork recovery by pruning to fork point
func (s *Server) TriggerForkRecovery(forkPointHeight uint32, forkDepth uint32) error {
	// Fork recovery should ONLY run when fully synced with network consensus.
	// Before reaching StateSynced, orphan blocks are normal - we're still catching up.
	// Check state machine first (authoritative sync state)
	if s.syncer != nil && s.syncer.GetStateMachine() != nil {
		state := s.syncer.GetStateMachine().GetState()
		if state != StateSynced {
			s.logger.WithFields(logrus.Fields{
				"fork_point": forkPointHeight,
				"fork_depth": forkDepth,
				"state":      state.String(),
			}).Debug("Skipping fork recovery - not synced with consensus")
			return nil
		}
	} else if s.blockchain != nil && s.blockchain.IsInitialBlockDownload() {
		// Fallback when syncer/state machine not available
		s.logger.WithFields(logrus.Fields{
			"fork_point": forkPointHeight,
			"fork_depth": forkDepth,
		}).Debug("Skipping fork detection during IBD - treating as orphan")
		return nil
	}

	if forkDepth > MaxAutoForkDepth {
		err := fmt.Errorf("fork depth %d exceeds maximum auto-recovery depth %d - manual intervention required",
			forkDepth, MaxAutoForkDepth)
		s.logger.WithFields(logrus.Fields{
			"fork_depth":     forkDepth,
			"max_auto_depth": MaxAutoForkDepth,
			"fork_point":     forkPointHeight,
		}).Error("NODE IS ON A FORK - MANUAL INTERVENTION REQUIRED")
		return err
	}

	s.logger.WithFields(logrus.Fields{
		"fork_point": forkPointHeight,
		"fork_depth": forkDepth,
	}).Warn("Fork detected, initiating automatic recovery")

	// Use blockchain's recovery mechanism
	if s.blockchain == nil {
		return fmt.Errorf("blockchain not available for fork recovery")
	}

	// Trigger recovery to fork point (need type assertion for concrete type)
	bc, ok := s.blockchain.(*blockchain.BlockChain)
	if !ok {
		return fmt.Errorf("blockchain type assertion failed for recovery")
	}

	forkErr := fmt.Errorf("fork detected at height %d", forkPointHeight)
	if err := bc.TriggerRecovery(forkPointHeight, forkErr); err != nil {
		s.logger.WithError(err).Error("Fork recovery failed")
		return err
	}

	// Clear orphan pool - they'll be re-requested during sync
	s.orphanBlocksMu.Lock()
	s.orphanBlocks = make(map[types.Hash]*types.Block)
	s.orphanBlocksByParent = make(map[types.Hash][]types.Hash)
	s.orphanBlocksMu.Unlock()

	s.logger.WithField("fork_point", forkPointHeight).Info("Fork recovery completed, resync will begin")
	return nil
}

// InjectMasternodeCachePeers extracts addresses from the masternode manager's
// loaded cache and injects them into peer discovery as priority bootstrap peers.
// Must be called after SetMasternodeManager and after the masternode cache is loaded.
func (s *Server) InjectMasternodeCachePeers() {
	if s.mnManager == nil {
		return
	}

	addrs := s.mnManager.GetPeerAddresses()
	if len(addrs) == 0 {
		return
	}

	added := s.discovery.AddMasternodeAddresses(addrs)
	s.logger.WithFields(logrus.Fields{
		"total":    len(addrs),
		"injected": added,
	}).Info("Injected masternode cache addresses as priority bootstrap peers")
}

// UpdatePeerHeights pushes the current chain height to all connected 70928+ peers.
// Called after a new block is accepted so peers get continuous height updates via pings.
func (s *Server) UpdatePeerHeights(height uint32) {
	s.peers.Range(func(key, value interface{}) bool {
		peer := value.(*Peer)
		if peer.IsHandshakeComplete() && peer.IsConnected() {
			peer.SetCurrentHeight(height)
		}
		return true
	})
}

// InvalidateChainStateCache clears the cached chainstate response.
// Called when a new block is accepted so the next getchainstate request rebuilds it.
func (s *Server) InvalidateChainStateCache() {
	s.chainStateCacheMu.Lock()
	s.chainStateCache = nil
	s.chainStateCacheMu.Unlock()
}

// getOrBuildChainState returns the cached chainstate or builds a new one.
// Uses double-checked locking to avoid TOCTOU race between read and write.
func (s *Server) getOrBuildChainState() (*ChainStateMessage, error) {
	s.chainStateCacheMu.RLock()
	cached := s.chainStateCache
	s.chainStateCacheMu.RUnlock()
	if cached != nil {
		return cached, nil
	}

	// Acquire write lock and re-check (double-checked locking)
	s.chainStateCacheMu.Lock()
	defer s.chainStateCacheMu.Unlock()

	if s.chainStateCache != nil {
		return s.chainStateCache, nil
	}

	// Build new chainstate
	bestHeight, err := s.blockchain.GetBestHeight()
	if err != nil {
		return nil, fmt.Errorf("failed to get best height: %w", err)
	}
	bestHash, err := s.blockchain.GetBestBlockHash()
	if err != nil {
		return nil, fmt.Errorf("failed to get best block hash: %w", err)
	}

	// Build block locator via the syncer
	var locator []types.Hash
	if s.syncer != nil {
		locator = s.syncer.GetBlockLocator()
	}

	cs := &ChainStateMessage{
		Version:   ProtocolVersion,
		TipHeight: bestHeight,
		TipHash:   bestHash,
		Locator:   locator,
	}

	s.chainStateCache = cs
	return cs, nil
}
