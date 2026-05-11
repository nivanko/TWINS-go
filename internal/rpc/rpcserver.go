package rpc

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/twins-dev/twins-core/pkg/types"
)

// Server represents the RPC server
type Server struct {
	config     *Config
	httpServer *http.Server
	handlers   map[string]Handler
	logger     *logrus.Entry

	// Core components
	blockchain       BlockchainInterface
	consensus        ConsensusInterface
	mempool          MempoolInterface
	p2pServer        P2PServer
	wallet           WalletInterface
	masternode       MasternodeInterface
	activeMasternode ActiveMasternodeInterface
	masternodeConf   MasternodeConfInterface
	sporkManager     SporkManagerInterface
	chainParams      *types.ChainParams

	// Middleware components
	ipFilter    *IPFilter
	connLimiter *ConnectionLimiter
	rateLimiter *RateLimiter

	// TLS
	tlsManager *TLSManager
	listener   net.Listener

	// TCP-level rate limiter (separate from HTTP-level rateLimiter)
	tcpRateLimiter *RateLimiter

	// Configuration persistence (optional, set by daemon for RPC→twinsd.yml persistence)
	configSetter ConfigSetter

	// Synchronization
	mu           sync.RWMutex
	shutdown     chan struct{}
	wg           sync.WaitGroup
	shutdownFunc ShutdownFunc // Callback for daemon shutdown
	started      atomic.Bool  // Prevents race condition with SetShutdownFunc

}

// NewServer creates a new RPC server
func NewServer(config *Config, logger *logrus.Entry) *Server {
	if logger == nil {
		logger = logrus.NewEntry(logrus.New())
	}

	// Initialize IP filter middleware
	ipFilter := NewIPFilter(config.AllowedIPs, logger.WithField("middleware", "ip_filter"))

	// Initialize connection limiter middleware
	connLimiter := NewConnectionLimiter(config.MaxClients, logger.WithField("middleware", "conn_limiter"))

	// Initialize rate limiter middleware
	rateLimiter := NewRateLimiter(config.RateLimit, logger.WithField("middleware", "rate_limiter"))

	// Log middleware configuration
	ips, nets := ipFilter.AllowedCount()
	logger.WithFields(logrus.Fields{
		"allowed_ips":      ips,
		"allowed_networks": nets,
		"max_connections":  config.MaxClients,
		"rate_limit":       config.RateLimit,
	}).Debug("RPC middleware initialized")

	return &Server{
		config:      config,
		handlers:    make(map[string]Handler),
		logger:      logger,
		shutdown:    make(chan struct{}),
		ipFilter:    ipFilter,
		connLimiter: connLimiter,
		rateLimiter: rateLimiter,
	}
}

// SetBlockchain sets the blockchain interface
func (s *Server) SetBlockchain(bc BlockchainInterface) {
	s.blockchain = bc
}

// SetConsensus sets the consensus interface
func (s *Server) SetConsensus(c ConsensusInterface) {
	s.consensus = c
}

// SetMempool sets the mempool interface
func (s *Server) SetMempool(m MempoolInterface) {
	s.mempool = m
}

// SetP2P sets the p2p interface
func (s *Server) SetP2P(p P2PServer) {
	s.p2pServer = p
}

// SetChainParams sets the chain parameters
func (s *Server) SetChainParams(params *types.ChainParams) {
	s.chainParams = params
}

// SetWallet sets the wallet interface
func (s *Server) SetWallet(w WalletInterface) {
	s.wallet = w
}

// SetConfigSetter sets the config persistence interface for RPC→twinsd.yml writes
func (s *Server) SetConfigSetter(cs ConfigSetter) {
	s.configSetter = cs
}

// SetMasternode sets the masternode interface
func (s *Server) SetMasternode(m MasternodeInterface) {
	s.masternode = m
}

// SetActiveMasternode sets the active masternode interface
func (s *Server) SetActiveMasternode(am ActiveMasternodeInterface) {
	s.activeMasternode = am
}

// SetMasternodeConf sets the masternode.conf interface
func (s *Server) SetMasternodeConf(mc MasternodeConfInterface) {
	s.masternodeConf = mc
}

// SetSporkManager sets the spork manager for network parameter queries
func (s *Server) SetSporkManager(sm SporkManagerInterface) {
	s.sporkManager = sm
}

// RegisterHandler registers an RPC method handler
func (s *Server) RegisterHandler(method string, handler Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = handler
}

// ExecuteCommand dispatches an RPC command internally without HTTP.
// Used by the GUI debug console to execute commands directly.
func (s *Server) ExecuteCommand(method string, params json.RawMessage) *Response {
	s.mu.RLock()
	handler, exists := s.handlers[method]
	s.mu.RUnlock()

	if !exists {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(CodeMethodNotFound, fmt.Sprintf("Method not found: %s", method), nil),
		}
	}

	req := &Request{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      1,
	}

	return handler(req)
}

// GetRegisteredCommands returns a sorted list of all registered RPC command names.
// Used by the GUI debug console for auto-completion.
func (s *Server) GetRegisteredCommands() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	commands := make([]string, 0, len(s.handlers))
	for cmd := range s.handlers {
		commands = append(commands, cmd)
	}
	sort.Strings(commands)
	return commands
}

// Start starts the RPC server
func (s *Server) Start() error {
	// Mark server as started to prevent race conditions with SetShutdownFunc
	s.started.Store(true)

	addr := fmt.Sprintf("%s:%d", s.config.Host, s.config.Port)

	// Fail-safe: refuse to serve plaintext RPC on a non-loopback address.
	// Override requires BOTH rpc.allow_plaintext_public in twinsd.yml AND
	// --rpc-allow-plaintext-public on the CLI (double-gate pattern).
	if !isLoopback(s.config.Host) && !s.config.TLS.Enabled && !s.config.AllowPlaintextPublic {
		return fmt.Errorf("refusing to start: RPC bound to non-loopback address %s without TLS; "+
			"either enable TLS (rpc.tls.enabled: true + --rpc-tls-enabled) or set BOTH "+
			"rpc.allow_plaintext_public: true in twinsd.yml AND --rpc-allow-plaintext-public on the command line",
			s.config.Host)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRequest)

	// Build middleware chain: IP Filter -> Rate Limiter -> Connection Limiter -> Handler
	// Order matters: IP filter runs first to reject unauthorized IPs early,
	// then rate limiter checks per-IP request rates, then connection limiter caps concurrency
	var handler http.Handler = mux
	handler = s.connLimiter.Middleware(handler)
	handler = s.rateLimiter.Middleware(handler)
	handler = s.ipFilter.Middleware(handler)

	s.httpServer = &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadTimeout:       s.config.ReadTimeout,
		ReadHeaderTimeout: s.config.ReadHeaderTimeout, // Slowloris defense: cap header-read phase independently of ReadTimeout
		WriteTimeout:      s.config.WriteTimeout,
		IdleTimeout:       s.config.IdleTimeout,
	}

	// Initialize TLS if enabled
	if s.config.TLS.Enabled {
		tm, err := NewTLSManager(s.config.TLS, s.logger.WithField("component", "tls"))
		if err != nil {
			return fmt.Errorf("TLS initialization failed: %w", err)
		}
		s.tlsManager = tm
		s.httpServer.TLSConfig = tm.TLSConfig()
	}

	s.logger.WithField("address", addr).Info("Starting RPC server")

	// Register all handlers
	s.registerHandlers()

	// Create explicit TCP listener. Using net.Listen + optional tls.NewListener
	// (instead of ListenAndServeTLS) gives control over the underlying TCP listener.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	// TCP-level per-IP rate limiting — applied BEFORE TLS handshake to defend
	// against handshake DoS (each TLS handshake is expensive crypto work).
	if s.config.RateLimit > 0 {
		s.tcpRateLimiter = NewRateLimiter(s.config.RateLimit, s.logger.WithField("middleware", "tcp_rate_limiter"))
		ln = newRateLimitedListener(ln, s.tcpRateLimiter, s.logger.WithField("component", "tcp_rate_limiter"))
		s.logger.WithField("rate_limit", s.config.RateLimit).Debug("TCP rate limiting enabled")
	}

	if s.tlsManager != nil {
		ln = tls.NewListener(ln, s.httpServer.TLSConfig)
		s.tlsManager.StartMonitoring()
		s.logger.Info("RPC server TLS enabled (TLS 1.3)")
	}
	s.listener = ln

	// Start plaintext-public warning ticker when the escape hatch is active
	// (TLS off + non-loopback + AllowPlaintextPublic). Stopped via s.shutdown.
	if !s.config.TLS.Enabled && s.config.AllowPlaintextPublic && !isLoopback(s.config.Host) {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.plaintextWarnLoop()
		}()
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := s.httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.logger.WithError(err).Error("RPC server error")
		}
	}()

	return nil
}

// Stop stops the RPC server gracefully.
// Shutdown ordering: http.Server.Shutdown first (drains in-flight requests),
// then TLSManager.Close() (stops expiry tickers). This ensures active TLS
// connections can complete their responses before monitoring stops.
func (s *Server) Stop() error {
	close(s.shutdown)
	s.rateLimiter.Stop()
	if s.tcpRateLimiter != nil {
		s.tcpRateLimiter.Stop()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var shutdownErr error
	if s.httpServer != nil {
		if err := s.httpServer.Shutdown(ctx); err != nil {
			s.logger.WithError(err).Error("Error shutting down RPC server")
			shutdownErr = err
		}
	}

	s.wg.Wait()

	// Close TLSManager AFTER http server has drained — tickers stop, cert released
	if s.tlsManager != nil {
		s.tlsManager.Close()
	}

	s.logger.Info("RPC server stopped")
	return shutdownErr
}

// plaintextWarnLoop logs a warning every 60 seconds when the RPC server is
// running without TLS on a non-loopback address (escape hatch active).
// The structured log field allows log aggregators to suppress/filter.
func (s *Server) plaintextWarnLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	// Log immediately on start
	s.logger.WithField("rpc_plaintext_public_active", true).
		Warn("RPC server running without TLS on non-loopback address")

	for {
		select {
		case <-ticker.C:
			s.logger.WithField("rpc_plaintext_public_active", true).
				Warn("RPC server running without TLS on non-loopback address")
		case <-s.shutdown:
			return
		}
	}
}

// GetTLSManager returns the TLS manager, or nil if TLS is not enabled.
// Used by the daemon to wire SIGHUP reload (h-rpc-tls-reload-sighup).
func (s *Server) GetTLSManager() *TLSManager {
	return s.tlsManager
}

// isLoopback returns true if the given host string resolves to a loopback address.
func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// handleRequest handles incoming RPC requests
func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	// Basic authentication check
	if s.config.Username != "" && s.config.Password != "" {
		username, password, ok := r.BasicAuth()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="TWINS RPC"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Use constant-time comparison to prevent timing attacks
		usernameMatch := subtle.ConstantTimeCompare([]byte(username), []byte(s.config.Username)) == 1
		passwordMatch := subtle.ConstantTimeCompare([]byte(password), []byte(s.config.Password)) == 1

		if !usernameMatch || !passwordMatch {
			w.Header().Set("WWW-Authenticate", `Basic realm="TWINS RPC"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	// Parse request
	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendError(w, nil, CodeParseError, "Parse error", nil)
		return
	}

	// Attach caller address for handlers that need it (e.g., reloadrpccerts backoff)
	req.RemoteAddr = r.RemoteAddr

	// Set default version if not specified
	if req.JSONRPC == "" {
		req.JSONRPC = "2.0"
	}

	// Handle request
	s.mu.RLock()
	handler, exists := s.handlers[req.Method]
	s.mu.RUnlock()

	if !exists {
		s.sendError(w, req.ID, CodeMethodNotFound, "Method not found", nil)
		return
	}

	// Execute handler
	response := handler(&req)
	if response == nil {
		response = &Response{
			ID:      req.ID,
			JSONRPC: req.JSONRPC,
		}
	}

	// Send response
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.WithError(err).Error("Failed to encode response")
	}
}

// sendError sends an error response
func (s *Server) sendError(w http.ResponseWriter, id interface{}, code int, message string, data interface{}) {
	response := Response{
		ID:      id,
		JSONRPC: "2.0",
		Error: &Error{
			Code:    code,
			Message: message,
			Data:    data,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// registerHandlers registers all RPC handlers
func (s *Server) registerHandlers() {
	// Register handlers
	s.registerBlockchainHandlers()
	s.registerNetworkHandlers()
	s.registerMempoolHandlers()
	s.registerMasternodeHandlers()
	s.registerMiningHandlers()
	s.registerWalletHandlers()
	s.registerTransactionHandlers()
	s.registerRawTransactionHandlers()
	s.registerUtilityHandlers()
	s.registerControlHandlers()
}

// Note: The actual registerXXXHandlers methods are defined in their respective files:
// - registerBlockchainHandlers in blockchain.go
// - registerNetworkHandlers in network.go
// - registerMempoolHandlers in mempool.go
// - registerMasternodeHandlers in masternode.go
// - registerMiningHandlers in mining.go
// - registerWalletHandlers in wallet.go
// - registerTransactionHandlers in transactions.go
