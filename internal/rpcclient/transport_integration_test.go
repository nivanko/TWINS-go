//go:build integration

package rpcclient

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	rpc "github.com/twins-dev/twins-core/internal/rpc"
)

// generateCA creates a self-signed CA certificate and key for integration tests.
func generateCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey, []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate CA key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("failed to create CA cert: %v", err)
	}

	caCert, err := x509.ParseCertificate(certDER)
	if err != nil {
		t.Fatalf("failed to parse CA cert: %v", err)
	}

	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	return caCert, key, caPEM
}

// generateServerCert creates a server certificate signed by the given CA.
func generateServerCert(t *testing.T, caCert *x509.Certificate, caKey *ecdsa.PrivateKey) (tls.Certificate, *x509.Certificate) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate server key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("failed to create server cert: %v", err)
	}

	serverCert, err := x509.ParseCertificate(certDER)
	if err != nil {
		t.Fatalf("failed to parse server cert: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("failed to marshal server key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("failed to create TLS key pair: %v", err)
	}

	return tlsCert, serverCert
}

func TestIntegration_TLS13Handshake(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Write server cert as CA for verification
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.TLS.Certificates[0].Certificate[0]})
	if err := os.WriteFile(caFile, caPEM, 0644); err != nil {
		t.Fatalf("failed to write CA file: %v", err)
	}

	transport, err := NewTransport(true, rpc.ClientTLSConfig{CAFile: caFile}, 30*time.Second)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}

	client := &http.Client{Transport: transport}
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("TLS 1.3 handshake failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	if resp.TLS == nil {
		t.Fatal("expected TLS connection state")
	}
	if resp.TLS.Version < tls.VersionTLS13 {
		t.Errorf("expected TLS 1.3+, got version 0x%x", resp.TLS.Version)
	}
}

func TestIntegration_SPKIPin_RealServer(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Extract leaf cert
	leaf, err := x509.ParseCertificate(server.TLS.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatalf("failed to parse leaf cert: %v", err)
	}
	correctPin := SPKIFingerprint(leaf)

	// Write server cert as CA
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.TLS.Certificates[0].Certificate[0]})
	if err := os.WriteFile(caFile, caPEM, 0644); err != nil {
		t.Fatalf("failed to write CA file: %v", err)
	}

	// Correct pin should succeed
	transport, err := NewTransport(true, rpc.ClientTLSConfig{CAFile: caFile, PinSHA256: correctPin}, 30*time.Second)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}

	client := &http.Client{Transport: transport}
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("correct pin should succeed: %v", err)
	}
	resp.Body.Close()

	// Wrong pin should fail
	transport2, err := NewTransport(true, rpc.ClientTLSConfig{CAFile: caFile, PinSHA256: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}, 30*time.Second)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}

	client2 := &http.Client{Transport: transport2}
	_, err = client2.Get(server.URL)
	if err == nil {
		t.Fatal("wrong pin should fail")
	}
}

func TestIntegration_CustomCA(t *testing.T) {
	// Generate a custom CA and server cert signed by it
	caCert, caKey, caPEM := generateCA(t)
	serverTLSCert, _ := generateServerCert(t, caCert, caKey)

	// Start a TLS server with the CA-signed cert
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverTLSCert},
		MinVersion:   tls.VersionTLS13,
	}
	server.StartTLS()
	defer server.Close()

	// Write CA to file
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, caPEM, 0644); err != nil {
		t.Fatalf("failed to write CA file: %v", err)
	}

	// Connect with custom CA — should succeed
	transport, err := NewTransport(true, rpc.ClientTLSConfig{CAFile: caFile}, 30*time.Second)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}

	client := &http.Client{Transport: transport}
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("custom CA connection should succeed: %v", err)
	}
	resp.Body.Close()

	// Connect without CA — should fail (system roots won't have our test CA)
	transport2, err := NewTransport(true, rpc.ClientTLSConfig{}, 30*time.Second)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}

	client2 := &http.Client{Transport: transport2}
	_, err = client2.Get(server.URL)
	if err == nil {
		t.Fatal("connection without custom CA should fail for self-signed cert")
	}
}

func TestIntegration_PlaintextRejected(t *testing.T) {
	// Start a TLS-only server
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Try to connect with plaintext HTTP transport
	transport, err := NewTransport(false, rpc.ClientTLSConfig{}, 30*time.Second)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}

	// Replace https:// with http:// in the URL
	plaintextURL := "http" + server.URL[5:] // strip "https" prefix, add "http"
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	resp, err := client.Get(plaintextURL)
	if err != nil {
		// Connection-level error — plaintext correctly rejected
		return
	}
	defer resp.Body.Close()
	// httptest server returns HTTP 400 "Client sent an HTTP request to an HTTPS server"
	if resp.StatusCode == http.StatusOK {
		t.Fatal("plaintext connection to TLS server should not succeed with 200 OK")
	}
}
