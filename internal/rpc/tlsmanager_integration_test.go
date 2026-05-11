//go:build integration

package rpc

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestTLSListener_RealConnection starts a real TLS-enabled RPC server and verifies
// an HTTPS client with CA trust can complete the TLS 1.3 handshake.
// Equivalent to: curl --cacert ca.pem https://127.0.0.1:<port>/
func TestTLSListener_RealConnection(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)
	caPath := filepath.Join(dir, "ca.pem")

	cfg := DefaultConfig()
	cfg.Host = "127.0.0.1"
	cfg.Port = 0 // Let OS assign
	cfg.TLS.Enabled = true
	cfg.TLS.CertFile = certPath
	cfg.TLS.KeyFile = keyPath
	cfg.Username = "testuser"
	cfg.Password = "testpass"

	s := NewServer(cfg, testLogger())
	err := s.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	// Get the actual address the server is listening on
	addr := s.listener.Addr().String()

	// Load CA cert for client verification
	caCert, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatalf("read CA cert: %v", err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caCert) {
		t.Fatal("failed to add CA cert to pool")
	}

	// Create HTTPS client with CA trust
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    caPool,
				MinVersion: tls.VersionTLS13,
			},
		},
		Timeout: 5 * time.Second,
	}

	// Make a request — should get a valid HTTPS response (auth will fail
	// without credentials, but the TLS handshake should succeed)
	resp, err := client.Get("https://" + addr + "/")
	if err != nil {
		t.Fatalf("HTTPS request failed: %v", err)
	}
	defer resp.Body.Close()

	// We expect 401 Unauthorized (no auth header) — not a TLS error
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
	if resp.TLS == nil {
		t.Error("expected TLS connection info")
	}
	if resp.TLS != nil && resp.TLS.Version != tls.VersionTLS13 {
		t.Errorf("expected TLS 1.3, got version %d", resp.TLS.Version)
	}
}

// TestTLSListener_PlaintextClientRejected verifies that a plaintext HTTP client
// cannot successfully communicate with the TLS listener.
func TestTLSListener_PlaintextClientRejected(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)

	cfg := DefaultConfig()
	cfg.Host = "127.0.0.1"
	cfg.Port = 0
	cfg.TLS.Enabled = true
	cfg.TLS.CertFile = certPath
	cfg.TLS.KeyFile = keyPath

	s := NewServer(cfg, testLogger())
	err := s.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	addr := s.listener.Addr().String()

	// Try plaintext HTTP against a TLS listener — should fail or return error status.
	// Go's TLS server sends an HTTP 400 "Client sent an HTTP request to an HTTPS server"
	// to plaintext clients, so the client may get a response rather than a connection error.
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + addr + "/")
	if err != nil {
		// Connection-level error — expected
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for plaintext to TLS, got %d", resp.StatusCode)
	}
}
