package rpc

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/twins-dev/twins-core/internal/p2p"
	"github.com/twins-dev/twins-core/pkg/types"
)

// --- Test helpers ---

// generateSelfSignedCert creates a self-signed CA certificate and server certificate
// in the given directory. Returns paths to cert.pem and key.pem.
func generateSelfSignedCert(t *testing.T, dir string) (certPath, keyPath string) {
	t.Helper()

	// Generate CA key
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}

	// Create self-signed CA cert
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "Test CA"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		IsCA:         true,
		BasicConstraintsValid: true,
	}

	caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}

	caCert, err := x509.ParseCertificate(caCertDER)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}

	// Generate server key
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate server key: %v", err)
	}

	// Create server cert signed by CA
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		DNSNames:     []string{"localhost"},
	}

	serverCertDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create server cert: %v", err)
	}

	// Write cert PEM
	certPath = filepath.Join(dir, "cert.pem")
	certFile, err := os.Create(certPath)
	if err != nil {
		t.Fatalf("create cert file: %v", err)
	}
	pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: serverCertDER})
	certFile.Close()

	// Write key PEM with 0600 permissions
	keyPath = filepath.Join(dir, "key.pem")
	keyFile, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatalf("create key file: %v", err)
	}
	keyBytes, err := x509.MarshalECPrivateKey(serverKey)
	if err != nil {
		t.Fatalf("marshal server key: %v", err)
	}
	pem.Encode(keyFile, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	keyFile.Close()

	// Also write CA cert for client verification
	caPath := filepath.Join(dir, "ca.pem")
	caFile, err := os.Create(caPath)
	if err != nil {
		t.Fatalf("create CA file: %v", err)
	}
	pem.Encode(caFile, &pem.Block{Type: "CERTIFICATE", Bytes: caCertDER})
	caFile.Close()

	return certPath, keyPath
}

func testLogger() *logrus.Entry {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)
	logger.SetOutput(os.Stderr)
	return logrus.NewEntry(logger)
}

// --- Permission check tests ---

func TestCheckKeyFilePermissions_TooPermissive(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(keyPath, []byte("fake-key"), 0644); err != nil {
		t.Fatal(err)
	}

	err := checkKeyFilePermissions(keyPath)
	if err == nil {
		t.Fatal("expected error for mode 0644, got nil")
	}
	if testing.Verbose() {
		t.Logf("got expected error: %v", err)
	}
}

func TestCheckKeyFilePermissions_Exact0600(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(keyPath, []byte("fake-key"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := checkKeyFilePermissions(keyPath); err != nil {
		t.Fatalf("mode 0600 should be allowed: %v", err)
	}
}

func TestCheckKeyFilePermissions_Stricter0400(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(keyPath, []byte("fake-key"), 0400); err != nil {
		t.Fatal(err)
	}

	if err := checkKeyFilePermissions(keyPath); err != nil {
		t.Fatalf("mode 0400 should be allowed: %v", err)
	}
}

func TestCheckKeyFilePermissions_GroupReadable(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key.pem")
	// Mode 0640: owner rw, group read — group bits set, must be rejected
	if err := os.WriteFile(keyPath, []byte("fake-key"), 0640); err != nil {
		t.Fatal(err)
	}

	err := checkKeyFilePermissions(keyPath)
	if err == nil {
		t.Fatal("expected error for mode 0640 (group-readable), got nil")
	}
}

func TestCheckKeyFilePermissions_NonExistent(t *testing.T) {
	err := checkKeyFilePermissions("/nonexistent/path/key.pem")
	if err == nil {
		t.Fatal("expected error for non-existent file, got nil")
	}
}

// --- TLSManager tests ---

func TestNewTLSManager_LoadsCert(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)

	tm, err := NewTLSManager(TLSConfig{
		Enabled:  true,
		CertFile: certPath,
		KeyFile:  keyPath,
	}, testLogger())
	if err != nil {
		t.Fatalf("NewTLSManager failed: %v", err)
	}

	status := tm.Status()
	if !status.Active {
		t.Error("expected Active=true")
	}
	if status.NotAfter.IsZero() {
		t.Error("expected non-zero NotAfter")
	}
	if status.MTLSActive {
		t.Error("expected MTLSActive=false when mTLS not configured")
	}
}

func TestNewTLSManager_MissingCertFile(t *testing.T) {
	_, err := NewTLSManager(TLSConfig{
		Enabled:  true,
		CertFile: "",
		KeyFile:  "/some/key.pem",
	}, testLogger())
	if err == nil {
		t.Fatal("expected error for missing cert file path")
	}
}

func TestNewTLSManager_MissingKeyFile(t *testing.T) {
	_, err := NewTLSManager(TLSConfig{
		Enabled:  true,
		CertFile: "/some/cert.pem",
		KeyFile:  "",
	}, testLogger())
	if err == nil {
		t.Fatal("expected error for missing key file path")
	}
}

func TestNewTLSManager_BadPermissions(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)

	// Make key world-readable
	if err := os.Chmod(keyPath, 0644); err != nil {
		t.Fatal(err)
	}

	_, err := NewTLSManager(TLSConfig{
		Enabled:  true,
		CertFile: certPath,
		KeyFile:  keyPath,
	}, testLogger())
	if err == nil {
		t.Fatal("expected error for key with mode 0644")
	}
}

func TestNewTLSManager_NonExistentCert(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(keyPath, []byte("fake"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := NewTLSManager(TLSConfig{
		Enabled:  true,
		CertFile: filepath.Join(dir, "nonexistent.pem"),
		KeyFile:  keyPath,
	}, testLogger())
	if err == nil {
		t.Fatal("expected error for non-existent cert file")
	}
}

// --- mTLS tests ---

func TestNewTLSManager_MTLS(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)
	caPath := filepath.Join(dir, "ca.pem") // Written by generateSelfSignedCert

	tm, err := NewTLSManager(TLSConfig{
		Enabled:  true,
		CertFile: certPath,
		KeyFile:  keyPath,
		MTLS: MTLSConfig{
			Enabled:      true,
			ClientCAFile: caPath,
		},
	}, testLogger())
	if err != nil {
		t.Fatalf("NewTLSManager with mTLS failed: %v", err)
	}

	status := tm.Status()
	if !status.MTLSActive {
		t.Error("expected MTLSActive=true")
	}

	tlsCfg := tm.TLSConfig()
	if tlsCfg.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Errorf("expected RequireAndVerifyClientCert, got %v", tlsCfg.ClientAuth)
	}
	if !tlsCfg.SessionTicketsDisabled {
		t.Error("expected SessionTicketsDisabled=true for mTLS")
	}
	if tlsCfg.ClientCAs == nil {
		t.Error("expected non-nil ClientCAs pool")
	}
}

func TestNewTLSManager_MTLS_NoCAFile(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)

	_, err := NewTLSManager(TLSConfig{
		Enabled:  true,
		CertFile: certPath,
		KeyFile:  keyPath,
		MTLS: MTLSConfig{
			Enabled:      true,
			ClientCAFile: "",
		},
	}, testLogger())
	if err == nil {
		t.Fatal("expected error for mTLS with empty ClientCAFile")
	}
}

func TestNewTLSManager_MTLS_InvalidCAFile(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)

	badCA := filepath.Join(dir, "bad-ca.pem")
	if err := os.WriteFile(badCA, []byte("not a certificate"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := NewTLSManager(TLSConfig{
		Enabled:  true,
		CertFile: certPath,
		KeyFile:  keyPath,
		MTLS: MTLSConfig{
			Enabled:      true,
			ClientCAFile: badCA,
		},
	}, testLogger())
	if err == nil {
		t.Fatal("expected error for invalid CA file content")
	}
}

// --- TLSConfig builder tests ---

func TestTLSConfig_MinVersion(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)

	tm, err := NewTLSManager(TLSConfig{
		Enabled:  true,
		CertFile: certPath,
		KeyFile:  keyPath,
	}, testLogger())
	if err != nil {
		t.Fatal(err)
	}

	tlsCfg := tm.TLSConfig()
	if tlsCfg.MinVersion != tls.VersionTLS13 {
		t.Errorf("expected MinVersion TLS 1.3 (%d), got %d", tls.VersionTLS13, tlsCfg.MinVersion)
	}
}

func TestTLSConfig_NoCipherSuites(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)

	tm, err := NewTLSManager(TLSConfig{
		Enabled:  true,
		CertFile: certPath,
		KeyFile:  keyPath,
	}, testLogger())
	if err != nil {
		t.Fatal(err)
	}

	tlsCfg := tm.TLSConfig()
	if tlsCfg.CipherSuites != nil {
		t.Errorf("expected nil CipherSuites for TLS 1.3, got %v", tlsCfg.CipherSuites)
	}
}

func TestTLSConfig_NoMTLS(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)

	tm, err := NewTLSManager(TLSConfig{
		Enabled:  true,
		CertFile: certPath,
		KeyFile:  keyPath,
	}, testLogger())
	if err != nil {
		t.Fatal(err)
	}

	tlsCfg := tm.TLSConfig()
	if tlsCfg.ClientAuth != tls.NoClientCert {
		t.Errorf("expected NoClientCert when mTLS disabled, got %v", tlsCfg.ClientAuth)
	}
	if tlsCfg.SessionTicketsDisabled {
		t.Error("SessionTicketsDisabled should be false when mTLS is off")
	}
}

// --- GetCertificate atomic swap test ---

func TestGetCertificate_AtomicSwap(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)

	tm, err := NewTLSManager(TLSConfig{
		Enabled:  true,
		CertFile: certPath,
		KeyFile:  keyPath,
	}, testLogger())
	if err != nil {
		t.Fatal(err)
	}

	// Get initial cert
	cert1, err := tm.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate failed: %v", err)
	}
	if cert1 == nil {
		t.Fatal("expected non-nil certificate")
	}

	// Simulate atomic swap by storing a new cert
	newCert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	tm.cert.Store(&newCert)

	// Verify GetCertificate returns the new cert
	cert2, err := tm.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate after swap failed: %v", err)
	}
	if cert2 == cert1 {
		t.Error("expected different cert pointer after atomic swap")
	}
}

// --- isLoopback tests ---

func TestIsLoopback(t *testing.T) {
	tests := []struct {
		host     string
		expected bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"localhost", true},
		{"0.0.0.0", false},
		{"192.168.1.1", false},
		{"10.0.0.1", false},
		{"example.com", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			got := isLoopback(tt.host)
			if got != tt.expected {
				t.Errorf("isLoopback(%q) = %v, want %v", tt.host, got, tt.expected)
			}
		})
	}
}

// --- Fail-safe tests ---

func TestFailSafe_NonLoopbackNoTLS(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Host = "192.168.1.1"
	cfg.TLS.Enabled = false
	cfg.AllowPlaintextPublic = false

	s := NewServer(cfg, testLogger())
	err := s.Start()
	if err == nil {
		s.Stop()
		t.Fatal("expected fail-safe error for non-loopback without TLS")
	}
	if testing.Verbose() {
		t.Logf("got expected error: %v", err)
	}
}

func TestFailSafe_LoopbackNoTLS(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Host = "127.0.0.1"
	cfg.Port = 0 // Let OS pick a port
	cfg.TLS.Enabled = false
	cfg.AllowPlaintextPublic = false

	s := NewServer(cfg, testLogger())
	err := s.Start()
	if err != nil {
		t.Fatalf("loopback without TLS should succeed: %v", err)
	}
	s.Stop()
}

func TestFailSafe_NonLoopbackPlaintextAllowed(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Host = "0.0.0.0"
	cfg.Port = 0
	cfg.TLS.Enabled = false
	cfg.AllowPlaintextPublic = true

	s := NewServer(cfg, testLogger())
	err := s.Start()
	if err != nil {
		t.Fatalf("non-loopback with AllowPlaintextPublic should succeed: %v", err)
	}
	s.Stop()
}

func TestFailSafe_NonLoopbackWithTLS(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)

	cfg := DefaultConfig()
	cfg.Host = "127.0.0.1" // Use loopback to avoid bind issues; TLS enabled bypasses fail-safe
	cfg.Port = 0
	cfg.TLS.Enabled = true
	cfg.TLS.CertFile = certPath
	cfg.TLS.KeyFile = keyPath
	cfg.AllowPlaintextPublic = false

	s := NewServer(cfg, testLogger())
	err := s.Start()
	if err != nil {
		t.Fatalf("TLS enabled should not trigger fail-safe: %v", err)
	}
	s.Stop()
}

// --- Log capture helper for expiry/reload tests ---

type logHook struct {
	mu      sync.Mutex
	entries []capturedEntry
}

type capturedEntry struct {
	Level   logrus.Level
	Message string
	Data    logrus.Fields
}

func (h *logHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (h *logHook) Fire(entry *logrus.Entry) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	data := make(logrus.Fields, len(entry.Data))
	for k, v := range entry.Data {
		data[k] = v
	}
	h.entries = append(h.entries, capturedEntry{
		Level:   entry.Level,
		Message: entry.Message,
		Data:    data,
	})
	return nil
}

func (h *logHook) getEntries() []capturedEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	copied := make([]capturedEntry, len(h.entries))
	copy(copied, h.entries)
	return copied
}

func testLoggerWithHook() (*logrus.Entry, *logHook) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)
	logger.SetOutput(io.Discard)
	hook := &logHook{}
	logger.AddHook(hook)
	return logrus.NewEntry(logger), hook
}

// getCertNotAfter parses the NotAfter time from a TLSManager's loaded certificate.
func getCertNotAfter(t *testing.T, tm *TLSManager) time.Time {
	t.Helper()
	cert := tm.cert.Load()
	if cert == nil || len(cert.Certificate) == 0 {
		t.Fatal("no certificate loaded")
	}
	x509Cert, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	return x509Cert.NotAfter
}

// --- Reload tests ---

func TestReload_Success(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)

	tm, err := NewTLSManager(TLSConfig{
		Enabled:  true,
		CertFile: certPath,
		KeyFile:  keyPath,
	}, testLogger())
	if err != nil {
		t.Fatal(err)
	}

	cert1, err := tm.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := tm.Reload(); err != nil {
		t.Fatalf("Reload failed: %v", err)
	}

	cert2, err := tm.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cert2 == cert1 {
		t.Error("expected different cert pointer after reload")
	}
}

func TestReload_BadPermissions(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)

	tm, err := NewTLSManager(TLSConfig{
		Enabled:  true,
		CertFile: certPath,
		KeyFile:  keyPath,
	}, testLogger())
	if err != nil {
		t.Fatal(err)
	}

	cert1, _ := tm.GetCertificate(nil)

	// Make key world-readable — triggers permission check failure
	if err := os.Chmod(keyPath, 0644); err != nil {
		t.Fatal(err)
	}

	err = tm.Reload()
	if err == nil {
		t.Fatal("expected error for bad key permissions during reload")
	}
	if !strings.Contains(err.Error(), "permission") {
		t.Errorf("expected permission-related error, got: %v", err)
	}

	// All-or-nothing: old cert must remain active
	cert2, _ := tm.GetCertificate(nil)
	if cert2 != cert1 {
		t.Error("cert should be unchanged after failed reload")
	}
}

func TestReload_MissingCertFile(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)

	tm, err := NewTLSManager(TLSConfig{
		Enabled:  true,
		CertFile: certPath,
		KeyFile:  keyPath,
	}, testLogger())
	if err != nil {
		t.Fatal(err)
	}

	cert1, _ := tm.GetCertificate(nil)

	// Remove cert file after initial load
	os.Remove(certPath)

	err = tm.Reload()
	if err == nil {
		t.Fatal("expected error for missing cert file during reload")
	}

	// All-or-nothing: old cert must remain active
	cert2, _ := tm.GetCertificate(nil)
	if cert2 != cert1 {
		t.Error("cert should be unchanged after failed reload")
	}
}

// --- Certificate expiry cadence tests ---

func TestCheckCertExpiry_Expired(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)

	logger, hook := testLoggerWithHook()
	tm, err := NewTLSManager(TLSConfig{
		Enabled:  true,
		CertFile: certPath,
		KeyFile:  keyPath,
	}, logger)
	if err != nil {
		t.Fatal(err)
	}

	notAfter := getCertNotAfter(t, tm)
	tm.nowFunc = func() time.Time { return notAfter.Add(1 * time.Hour) }

	tm.checkCertExpiry()

	entries := hook.getEntries()
	found := false
	for _, e := range entries {
		if e.Level == logrus.ErrorLevel && strings.Contains(e.Message, "EXPIRED") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected ERROR-level log with 'EXPIRED' for expired cert")
	}
}

func TestCheckCertExpiry_Within24Hours(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)

	logger, hook := testLoggerWithHook()
	tm, err := NewTLSManager(TLSConfig{
		Enabled:  true,
		CertFile: certPath,
		KeyFile:  keyPath,
	}, logger)
	if err != nil {
		t.Fatal(err)
	}

	notAfter := getCertNotAfter(t, tm)
	// 12 hours before expiry — within 24h tier, hourly WARN
	tm.nowFunc = func() time.Time { return notAfter.Add(-12 * time.Hour) }

	tm.checkCertExpiry()

	entries := hook.getEntries()
	found := false
	for _, e := range entries {
		if e.Level == logrus.WarnLevel && strings.Contains(e.Message, "expires within 24 hours") {
			found = true
			if hoursLeft, ok := e.Data["hours_left"]; ok {
				if h, ok := hoursLeft.(int); ok && h != 12 {
					t.Errorf("expected hours_left=12, got %d", h)
				}
			}
			break
		}
	}
	if !found {
		t.Error("expected WARN with 'expires within 24 hours'")
	}
}

func TestCheckCertExpiry_Within7Days(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)

	logger, hook := testLoggerWithHook()
	tm, err := NewTLSManager(TLSConfig{
		Enabled:        true,
		CertFile:       certPath,
		KeyFile:        keyPath,
		ExpiryWarnDays: 30,
	}, logger)
	if err != nil {
		t.Fatal(err)
	}

	notAfter := getCertNotAfter(t, tm)
	// 3 days before expiry — within 7d tier, daily WARN
	tm.nowFunc = func() time.Time { return notAfter.Add(-3 * 24 * time.Hour) }

	tm.checkCertExpiry()

	entries := hook.getEntries()
	found := false
	for _, e := range entries {
		if e.Level == logrus.WarnLevel && strings.Contains(e.Message, "expires within 7 days") {
			found = true
			if daysLeft, ok := e.Data["days_left"]; ok {
				if d, ok := daysLeft.(int); ok && d != 3 {
					t.Errorf("expected days_left=3, got %d", d)
				}
			}
			break
		}
	}
	if !found {
		t.Error("expected WARN with 'expires within 7 days'")
	}
}

func TestCheckCertExpiry_WithinWarnThreshold(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)

	logger, hook := testLoggerWithHook()
	tm, err := NewTLSManager(TLSConfig{
		Enabled:        true,
		CertFile:       certPath,
		KeyFile:        keyPath,
		ExpiryWarnDays: 30,
	}, logger)
	if err != nil {
		t.Fatal(err)
	}

	notAfter := getCertNotAfter(t, tm)
	// 15 days before expiry — within 30d threshold but outside 7d
	tm.nowFunc = func() time.Time { return notAfter.Add(-15 * 24 * time.Hour) }

	tm.checkCertExpiry()

	entries := hook.getEntries()
	found := false
	for _, e := range entries {
		if e.Level == logrus.WarnLevel && strings.Contains(e.Message, "approaching expiry") {
			found = true
			if daysLeft, ok := e.Data["days_left"]; ok {
				if d, ok := daysLeft.(int); ok && d != 15 {
					t.Errorf("expected days_left=15, got %d", d)
				}
			}
			break
		}
	}
	if !found {
		t.Error("expected WARN with 'approaching expiry'")
	}
}

func TestCheckCertExpiry_Safe(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)

	logger, hook := testLoggerWithHook()
	tm, err := NewTLSManager(TLSConfig{
		Enabled:        true,
		CertFile:       certPath,
		KeyFile:        keyPath,
		ExpiryWarnDays: 30,
	}, logger)
	if err != nil {
		t.Fatal(err)
	}

	notAfter := getCertNotAfter(t, tm)
	// 60 days before expiry — well outside 30d warn threshold
	tm.nowFunc = func() time.Time { return notAfter.Add(-60 * 24 * time.Hour) }

	tm.checkCertExpiry()

	entries := hook.getEntries()
	for _, e := range entries {
		// logrus: Panic=0 Fatal=1 Error=2 Warn=3; <= WarnLevel catches all severe levels
		if e.Level <= logrus.WarnLevel {
			t.Errorf("unexpected warn/error log when cert is safe: level=%v message=%q", e.Level, e.Message)
		}
	}
}

// --- shouldWarnDaily cadence tests ---

func TestShouldWarnDaily_Cadence(t *testing.T) {
	tm := &TLSManager{
		nowFunc: func() time.Time { return time.Unix(1000000, 0) },
	}

	// First call: lastExpiryWarn is 0, so now-0 >= 86400 → true
	if !tm.shouldWarnDaily() {
		t.Error("first call should return true")
	}

	// Immediate repeat: 1000000-1000000 = 0 < 86400 → false
	if tm.shouldWarnDaily() {
		t.Error("immediate second call should return false")
	}

	// Just under 24h: still false
	tm.nowFunc = func() time.Time { return time.Unix(1000000+86399, 0) }
	if tm.shouldWarnDaily() {
		t.Error("call at 86399s should return false")
	}

	// Exactly 24h: should return true
	tm.nowFunc = func() time.Time { return time.Unix(1000000+86400, 0) }
	if !tm.shouldWarnDaily() {
		t.Error("call at 86400s should return true")
	}
}

// --- Monitoring lifecycle test ---

func TestStartMonitoring_CloseStopsLoop(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)

	tm, err := NewTLSManager(TLSConfig{
		Enabled:  true,
		CertFile: certPath,
		KeyFile:  keyPath,
	}, testLogger())
	if err != nil {
		t.Fatal(err)
	}

	tm.StartMonitoring()
	// Close should stop the monitoring goroutine without panic or deadlock
	tm.Close()
}

// --- getnetworkinfo TLS field tests ---

// stubP2PServer satisfies the P2PServer interface with minimal stubs for
// getnetworkinfo testing. Only GetStats and GetPeers return useful data.
type stubP2PServer struct{}

func (s *stubP2PServer) GetPeers() []p2p.PeerInfo          { return nil }
func (s *stubP2PServer) GetStats() *p2p.ServerStats         { return &p2p.ServerStats{} }
func (s *stubP2PServer) PingAllPeers()                      {}
func (s *stubP2PServer) AddNode(string, bool) error         { return nil }
func (s *stubP2PServer) RemoveNode(string) error            { return nil }
func (s *stubP2PServer) ConnectNode(string) error           { return nil }
func (s *stubP2PServer) DisconnectNode(string) error        { return nil }
func (s *stubP2PServer) GetAddedNodes() []string            { return nil }
func (s *stubP2PServer) BanSubnet(string, int64, bool, string) error { return nil }
func (s *stubP2PServer) UnbanSubnet(string) error           { return nil }
func (s *stubP2PServer) GetBannedList() []p2p.BanInfo       { return nil }
func (s *stubP2PServer) ClearBannedList()                   {}
func (s *stubP2PServer) SetNetworkActive(bool)              {}
func (s *stubP2PServer) RelayBlock(*types.Block)            {}
func (s *stubP2PServer) RelayTransaction(*types.Transaction) error { return nil }
func (s *stubP2PServer) GetSyncer() *p2p.BlockchainSyncer       { return nil }
func (s *stubP2PServer) GetHealthTracker() *p2p.PeerHealthTracker { return nil }
func (s *stubP2PServer) GetBootstrap() *p2p.BootstrapManager     { return nil }

func TestGetNetworkInfo_TLSFields(t *testing.T) {
	t.Run("TLS_active", func(t *testing.T) {
		dir := t.TempDir()
		certPath, keyPath := generateSelfSignedCert(t, dir)

		cfg := DefaultConfig()
		cfg.Host = "127.0.0.1"
		s := NewServer(cfg, testLogger())
		s.p2pServer = &stubP2PServer{}

		// Create a real TLSManager to set on the server
		tm, err := NewTLSManager(TLSConfig{
			Enabled:  true,
			CertFile: certPath,
			KeyFile:  keyPath,
		}, testLogger())
		if err != nil {
			t.Fatal(err)
		}
		s.tlsManager = tm

		s.registerNetworkHandlers()

		resp := s.ExecuteCommand("getnetworkinfo", nil)
		if resp.Error != nil {
			t.Fatalf("getnetworkinfo error: %v", resp.Error)
		}

		resultBytes, err := json.Marshal(resp.Result)
		if err != nil {
			t.Fatal(err)
		}
		var result map[string]interface{}
		if err := json.Unmarshal(resultBytes, &result); err != nil {
			t.Fatal(err)
		}

		if result["rpc_tls_active"] != true {
			t.Errorf("expected rpc_tls_active=true, got %v", result["rpc_tls_active"])
		}
		if result["rpc_min_tls_version"] != "TLS1.3" {
			t.Errorf("expected rpc_min_tls_version=TLS1.3, got %v", result["rpc_min_tls_version"])
		}
		// Plaintext field should be absent on loopback
		if _, exists := result["rpc_plaintext_public"]; exists {
			t.Error("rpc_plaintext_public should be absent on loopback")
		}
	})

	t.Run("TLS_inactive", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "127.0.0.1"
		s := NewServer(cfg, testLogger())
		s.p2pServer = &stubP2PServer{}
		// No TLS manager set (default nil)

		s.registerNetworkHandlers()

		resp := s.ExecuteCommand("getnetworkinfo", nil)
		if resp.Error != nil {
			t.Fatalf("getnetworkinfo error: %v", resp.Error)
		}

		resultBytes, err := json.Marshal(resp.Result)
		if err != nil {
			t.Fatal(err)
		}
		var result map[string]interface{}
		if err := json.Unmarshal(resultBytes, &result); err != nil {
			t.Fatal(err)
		}

		// TLS fields should be absent
		if _, exists := result["rpc_tls_active"]; exists {
			t.Error("rpc_tls_active should be absent when TLS inactive")
		}
		if _, exists := result["rpc_min_tls_version"]; exists {
			t.Error("rpc_min_tls_version should be absent when TLS inactive")
		}
	})

	t.Run("plaintext_public_non_loopback", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "0.0.0.0"
		cfg.TLS.Enabled = false
		cfg.AllowPlaintextPublic = true
		s := NewServer(cfg, testLogger())
		s.p2pServer = &stubP2PServer{}

		s.registerNetworkHandlers()

		resp := s.ExecuteCommand("getnetworkinfo", nil)
		if resp.Error != nil {
			t.Fatalf("getnetworkinfo error: %v", resp.Error)
		}

		resultBytes, err := json.Marshal(resp.Result)
		if err != nil {
			t.Fatal(err)
		}
		var result map[string]interface{}
		if err := json.Unmarshal(resultBytes, &result); err != nil {
			t.Fatal(err)
		}

		if result["rpc_plaintext_public"] != true {
			t.Errorf("expected rpc_plaintext_public=true for non-loopback with AllowPlaintextPublic, got %v", result["rpc_plaintext_public"])
		}
	})

	t.Run("plaintext_public_suppressed_when_TLS_active", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "0.0.0.0"
		cfg.TLS.Enabled = true
		cfg.AllowPlaintextPublic = true
		s := NewServer(cfg, testLogger())
		s.p2pServer = &stubP2PServer{}

		s.registerNetworkHandlers()

		resp := s.ExecuteCommand("getnetworkinfo", nil)
		if resp.Error != nil {
			t.Fatalf("getnetworkinfo error: %v", resp.Error)
		}

		resultBytes, err := json.Marshal(resp.Result)
		if err != nil {
			t.Fatal(err)
		}
		var result map[string]interface{}
		if err := json.Unmarshal(resultBytes, &result); err != nil {
			t.Fatal(err)
		}

		// rpc_plaintext_public must NOT appear when TLS is enabled
		if _, exists := result["rpc_plaintext_public"]; exists {
			t.Error("rpc_plaintext_public should be absent when TLS is enabled")
		}
	})
}

