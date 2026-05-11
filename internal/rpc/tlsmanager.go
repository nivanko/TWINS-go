package rpc

import (
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/argon2"
)

// TLSStatus represents the current TLS state for observability.
type TLSStatus struct {
	Active     bool      `json:"active"`
	CertFile   string    `json:"cert_file"`
	NotAfter   time.Time `json:"not_after"`
	MTLSActive bool      `json:"mtls_active"`
}

// TLSManager owns the TLS certificate lifecycle for the RPC server.
// It uses atomic.Pointer for the certificate to support zero-downtime
// reload via SIGHUP.
type TLSManager struct {
	cert     atomic.Pointer[tls.Certificate]
	certFile string
	keyFile  string

	// mTLS
	clientCAs   *x509.CertPool
	mtlsEnabled bool

	// Reload passphrase (reloadrpccerts RPC handler)
	reloadPassHash    []byte // argon2id hash from PHC string; nil = disabled
	reloadPassSalt    []byte
	reloadPassTime    uint32
	reloadPassMemory  uint32
	reloadPassThreads uint8
	reloadPassKeyLen  uint32
	hashFile          string          // Path to hash file; empty = no hash file
	mu                sync.Mutex      // Serializes VerifyReloadPassphrase (CPU DoS mitigation)
	backoff           *ReloadBackoff  // Per-IP exponential backoff tracker

	// Monitoring
	warnDays       int           // Days before expiry to start warning (default 30)
	done           chan struct{} // Closed by Close() to stop tickers
	lastExpiryWarn atomic.Int64  // Unix timestamp of last expiry warning (daily cadence)
	nowFunc        func() time.Time // For testable time injection (default: time.Now)

	logger *logrus.Entry
}

// NewTLSManager creates a TLSManager, validates key-file permissions and
// ownership, and loads the initial certificate from PEM files.
func NewTLSManager(cfg TLSConfig, logger *logrus.Entry) (*TLSManager, error) {
	if cfg.CertFile == "" {
		return nil, fmt.Errorf("TLS enabled but no certificate file specified (rpc.tls.cert_file)")
	}
	if cfg.KeyFile == "" {
		return nil, fmt.Errorf("TLS enabled but no key file specified (rpc.tls.key_file)")
	}

	// Validate key file permissions before loading
	if err := checkKeyFilePermissions(cfg.KeyFile); err != nil {
		return nil, err
	}
	if err := checkKeyFileOwnership(cfg.KeyFile); err != nil {
		return nil, err
	}

	// Load initial certificate
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load TLS certificate: %w", err)
	}

	warnDays := cfg.ExpiryWarnDays
	if warnDays <= 0 {
		warnDays = 30
	}

	tm := &TLSManager{
		certFile: cfg.CertFile,
		keyFile:  cfg.KeyFile,
		hashFile: cfg.ReloadPassphraseFile,
		warnDays: warnDays,
		done:     make(chan struct{}),
		nowFunc:  time.Now,
		logger:   logger,
	}
	tm.cert.Store(&cert)

	// Load reload passphrase hash if configured
	if cfg.ReloadPassphraseFile != "" {
		if err := tm.loadReloadPassHash(cfg.ReloadPassphraseFile); err != nil {
			return nil, fmt.Errorf("failed to load reload passphrase hash: %w", err)
		}
		tm.backoff = NewReloadBackoff()
		logger.Info("reloadrpccerts passphrase hash loaded")
	}

	// Parse cert for metadata logging
	if len(cert.Certificate) > 0 {
		x509Cert, err := x509.ParseCertificate(cert.Certificate[0])
		if err == nil {
			logger.WithFields(logrus.Fields{
				"subject":   x509Cert.Subject.CommonName,
				"not_after": x509Cert.NotAfter.Format(time.RFC3339),
				"issuer":    x509Cert.Issuer.CommonName,
			}).Info("TLS certificate loaded")
		}
	}

	// mTLS setup
	if cfg.MTLS.Enabled {
		if cfg.MTLS.ClientCAFile == "" {
			return nil, fmt.Errorf("mTLS enabled but no client CA file specified (rpc.tls.mtls.client_ca_file)")
		}
		caCert, err := os.ReadFile(cfg.MTLS.ClientCAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read client CA file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse client CA certificates from %s", cfg.MTLS.ClientCAFile)
		}
		tm.clientCAs = pool
		tm.mtlsEnabled = true
		logger.Info("mTLS enabled with client certificate verification")
	}

	return tm, nil
}

// GetCertificate is a tls.Config callback that returns the current certificate.
// The atomic pointer allows zero-downtime cert reload by swapping the stored cert.
func (tm *TLSManager) GetCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	cert := tm.cert.Load()
	if cert == nil {
		return nil, fmt.Errorf("no TLS certificate loaded")
	}
	return cert, nil
}

// TLSConfig returns a *tls.Config for use with the HTTP server or tls.NewListener.
func (tm *TLSManager) TLSConfig() *tls.Config {
	tlsCfg := &tls.Config{
		GetCertificate: tm.GetCertificate,
		// TLS 1.3 only — provides mandatory AEAD ciphers + forward secrecy + 1-RTT.
		// No CipherSuites configuration: TLS 1.3 uses a fixed set of AEAD suites
		// managed automatically by Go's crypto/tls.
		// Note: Android API <29 does not support TLS 1.3. Operators needing older
		// Android clients should use a reverse proxy with TLS 1.2 termination.
		MinVersion: tls.VersionTLS13,
	}

	if tm.mtlsEnabled && tm.clientCAs != nil {
		tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert
		tlsCfg.ClientCAs = tm.clientCAs
		// Disable session tickets under mTLS to ensure client certificate
		// verification on every connection. Session resumption could bypass
		// cert verification, which is unacceptable for mTLS security.
		tlsCfg.SessionTicketsDisabled = true
	}

	return tlsCfg
}

// Status returns the current TLS status for observability (e.g., getnetworkinfo).
func (tm *TLSManager) Status() TLSStatus {
	status := TLSStatus{
		Active:     true,
		CertFile:   tm.certFile,
		MTLSActive: tm.mtlsEnabled,
	}

	cert := tm.cert.Load()
	if cert != nil && len(cert.Certificate) > 0 {
		x509Cert, err := x509.ParseCertificate(cert.Certificate[0])
		if err == nil {
			status.NotAfter = x509Cert.NotAfter
		}
	}

	return status
}

// Reload re-reads the cert and key files (and hash file if configured),
// validates permissions and ownership, and atomically swaps both the
// certificate and passphrase hash. All-or-nothing: any failure returns an
// error and the existing certificate AND hash remain active.
func (tm *TLSManager) Reload() error {
	// --- Phase 1: Load everything into temporaries (no state mutation) ---

	// Verify key file permissions before loading
	if err := checkKeyFilePermissions(tm.keyFile); err != nil {
		return fmt.Errorf("key file permission check failed during reload: %w", err)
	}
	if err := checkKeyFileOwnership(tm.keyFile); err != nil {
		return fmt.Errorf("key file ownership check failed during reload: %w", err)
	}

	// Load new certificate into temporary
	newCert, err := tls.LoadX509KeyPair(tm.certFile, tm.keyFile)
	if err != nil {
		return fmt.Errorf("failed to load TLS certificate during reload: %w", err)
	}

	// Load new hash into temporaries (if configured)
	var newHash, newSalt []byte
	var newTime, newMemory, newKeyLen uint32
	var newThreads uint8
	if tm.hashFile != "" {
		if err := checkKeyFilePermissions(tm.hashFile); err != nil {
			return fmt.Errorf("hash file permission check failed during reload: %w", err)
		}
		if err := checkKeyFileOwnership(tm.hashFile); err != nil {
			return fmt.Errorf("hash file ownership check failed during reload: %w", err)
		}
		data, err := os.ReadFile(tm.hashFile)
		if err != nil {
			return fmt.Errorf("failed to read hash file during reload: %w", err)
		}
		phc := strings.TrimSpace(string(data))
		newSalt, newHash, newTime, newMemory, newThreads, newKeyLen, err = parsePHCArgon2id(phc)
		if err != nil {
			return fmt.Errorf("failed to parse hash file during reload: %w", err)
		}
	}

	// --- Phase 2: All loads succeeded — commit both atomically ---

	// Swap certificate
	tm.cert.Store(&newCert)

	// Swap hash fields under mutex (coordinates with VerifyReloadPassphrase)
	if tm.hashFile != "" {
		tm.mu.Lock()
		tm.reloadPassHash = newHash
		tm.reloadPassSalt = newSalt
		tm.reloadPassTime = newTime
		tm.reloadPassMemory = newMemory
		tm.reloadPassThreads = newThreads
		tm.reloadPassKeyLen = newKeyLen
		tm.mu.Unlock()
	}

	// Parse for metadata logging
	if len(newCert.Certificate) > 0 {
		x509Cert, err := x509.ParseCertificate(newCert.Certificate[0])
		if err == nil {
			tm.logger.WithFields(logrus.Fields{
				"subject":   x509Cert.Subject.CommonName,
				"not_after": x509Cert.NotAfter.Format(time.RFC3339),
				"issuer":    x509Cert.Issuer.CommonName,
			}).Info("TLS certificate reloaded")
		}
	}

	return nil
}

// Close stops all background monitoring goroutines (expiry ticker, backoff cleanup).
// Must be called AFTER http.Server.Shutdown() to ensure in-flight TLS
// connections can complete their responses before monitoring stops.
func (tm *TLSManager) Close() {
	close(tm.done)
	if tm.backoff != nil {
		tm.backoff.Stop()
	}
}

// StartMonitoring starts the certificate expiry warning goroutine.
// Call after the TLS listener is active.
func (tm *TLSManager) StartMonitoring() {
	go tm.expiryWarnLoop()
}

// expiryWarnLoop checks certificate expiry on an hourly ticker and logs
// warnings at tiered cadence: daily when within warnDays, daily within 7 days,
// hourly within 24 hours, ERROR when expired.
func (tm *TLSManager) expiryWarnLoop() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	// Check immediately on start
	tm.checkCertExpiry()

	for {
		select {
		case <-ticker.C:
			tm.checkCertExpiry()
		case <-tm.done:
			return
		}
	}
}

// checkCertExpiry evaluates the current certificate's expiry and logs at the
// appropriate level and cadence.
func (tm *TLSManager) checkCertExpiry() {
	cert := tm.cert.Load()
	if cert == nil || len(cert.Certificate) == 0 {
		return
	}

	x509Cert, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return
	}

	now := tm.nowFunc()
	timeLeft := x509Cert.NotAfter.Sub(now)
	warnThreshold := time.Duration(tm.warnDays) * 24 * time.Hour

	fields := logrus.Fields{
		"cert_file": tm.certFile,
		"not_after": x509Cert.NotAfter.Format(time.RFC3339),
	}

	switch {
	case timeLeft <= 0:
		// Expired — ERROR every tick (hourly)
		tm.logger.WithFields(fields).Errorf(
			"RPC TLS certificate EXPIRED at %s — reload required",
			x509Cert.NotAfter.Format(time.RFC3339))

	case timeLeft <= 24*time.Hour:
		// Last 24 hours — WARN every tick (hourly)
		tm.logger.WithFields(fields).WithField("hours_left", int(timeLeft.Hours())).
			Warn("RPC TLS certificate expires within 24 hours")

	case timeLeft <= 7*24*time.Hour:
		// Last 7 days — WARN daily
		if tm.shouldWarnDaily() {
			tm.logger.WithFields(fields).WithField("days_left", int(timeLeft.Hours()/24)).
				Warn("RPC TLS certificate expires within 7 days")
		}

	case timeLeft <= warnThreshold:
		// Within warn threshold — WARN daily
		if tm.shouldWarnDaily() {
			tm.logger.WithFields(fields).WithField("days_left", int(timeLeft.Hours()/24)).
				Warn("RPC TLS certificate approaching expiry")
		}
	}
}

// shouldWarnDaily returns true if at least 24 hours have elapsed since the last
// expiry warning. Uses atomic.Int64 for lock-free tracking.
func (tm *TLSManager) shouldWarnDaily() bool {
	now := tm.nowFunc().Unix()
	last := tm.lastExpiryWarn.Load()
	if now-last >= 86400 {
		tm.lastExpiryWarn.Store(now)
		return true
	}
	return false
}

// --- Reload passphrase (reloadrpccerts RPC handler) ---

// loadReloadPassHash reads an argon2id PHC-format hash from a file, validates
// permissions and ownership, and stores the parsed fields in TLSManager.
func (tm *TLSManager) loadReloadPassHash(path string) error {
	if err := checkKeyFilePermissions(path); err != nil {
		return fmt.Errorf("hash file %s: %w", path, err)
	}
	if err := checkKeyFileOwnership(path); err != nil {
		return fmt.Errorf("hash file %s: %w", path, err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot read hash file %s: %w", path, err)
	}

	phc := strings.TrimSpace(string(data))
	if phc == "" {
		return fmt.Errorf("hash file %s is empty", path)
	}

	salt, hash, timeCost, memory, threads, keyLen, err := parsePHCArgon2id(phc)
	if err != nil {
		return fmt.Errorf("invalid PHC hash in %s: %w", path, err)
	}

	tm.reloadPassHash = hash
	tm.reloadPassSalt = salt
	tm.reloadPassTime = timeCost
	tm.reloadPassMemory = memory
	tm.reloadPassThreads = threads
	tm.reloadPassKeyLen = keyLen
	return nil
}

// VerifyReloadPassphrase checks the provided passphrase against the in-memory
// argon2id hash. Returns false if no hash is loaded (disabled state).
// Serialized by sync.Mutex to prevent concurrent argon2id CPU DoS (each call
// uses ~64 MiB of memory with OWASP 2024 recommended parameters).
func (tm *TLSManager) VerifyReloadPassphrase(passphrase string) bool {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if tm.reloadPassHash == nil {
		return false
	}

	computed := argon2.IDKey(
		[]byte(passphrase),
		tm.reloadPassSalt,
		tm.reloadPassTime,
		tm.reloadPassMemory,
		tm.reloadPassThreads,
		tm.reloadPassKeyLen,
	)

	match := subtle.ConstantTimeCompare(computed, tm.reloadPassHash) == 1

	// Zero derived key material to reduce heap exposure window.
	// The passphrase string itself is immutable in Go and cannot be zeroed.
	for i := range computed {
		computed[i] = 0
	}

	return match
}

// HasReloadPassphrase returns true if a reload passphrase hash is loaded.
func (tm *TLSManager) HasReloadPassphrase() bool {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.reloadPassHash != nil
}

// GetBackoff returns the per-IP backoff tracker (may be nil if passphrase
// not configured).
func (tm *TLSManager) GetBackoff() *ReloadBackoff {
	return tm.backoff
}

// parsePHCArgon2id parses a PHC-format argon2id hash string:
//
//	$argon2id$v=19$m=65536,t=3,p=4$<base64-salt>$<base64-hash>
//
// Returns the decoded salt, hash, and algorithm parameters.
func parsePHCArgon2id(phc string) (salt, hash []byte, timeCost, memory uint32, threads uint8, keyLen uint32, err error) {
	// Split by '$' — expect: ["", "argon2id", "v=19", "m=...,t=...,p=...", "<salt>", "<hash>"]
	parts := strings.Split(phc, "$")
	if len(parts) != 6 {
		return nil, nil, 0, 0, 0, 0, fmt.Errorf("expected 6 $-separated segments, got %d", len(parts))
	}

	if parts[1] != "argon2id" {
		return nil, nil, 0, 0, 0, 0, fmt.Errorf("unsupported algorithm %q, expected argon2id", parts[1])
	}

	if parts[2] != "v=19" {
		return nil, nil, 0, 0, 0, 0, fmt.Errorf("unsupported version %q, expected v=19", parts[2])
	}

	// Parse params: "m=65536,t=3,p=4"
	params := strings.Split(parts[3], ",")
	if len(params) != 3 {
		return nil, nil, 0, 0, 0, 0, fmt.Errorf("expected 3 params (m,t,p), got %d", len(params))
	}

	paramMap := make(map[string]string, 3)
	for _, p := range params {
		kv := strings.SplitN(p, "=", 2)
		if len(kv) != 2 {
			return nil, nil, 0, 0, 0, 0, fmt.Errorf("malformed param %q", p)
		}
		paramMap[kv[0]] = kv[1]
	}

	mVal, err := strconv.ParseUint(paramMap["m"], 10, 32)
	if err != nil {
		return nil, nil, 0, 0, 0, 0, fmt.Errorf("invalid memory param: %w", err)
	}
	tVal, err := strconv.ParseUint(paramMap["t"], 10, 32)
	if err != nil {
		return nil, nil, 0, 0, 0, 0, fmt.Errorf("invalid time param: %w", err)
	}
	pVal, err := strconv.ParseUint(paramMap["p"], 10, 8)
	if err != nil {
		return nil, nil, 0, 0, 0, 0, fmt.Errorf("invalid parallelism param: %w", err)
	}

	salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return nil, nil, 0, 0, 0, 0, fmt.Errorf("invalid base64 salt: %w", err)
	}

	hash, err = base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return nil, nil, 0, 0, 0, 0, fmt.Errorf("invalid base64 hash: %w", err)
	}

	return salt, hash, uint32(tVal), uint32(mVal), uint8(pVal), uint32(len(hash)), nil
}
