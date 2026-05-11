package rpcclient

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	rpc "github.com/twins-dev/twins-core/internal/rpc"
)

// generateTestCert creates a self-signed EC P-256 certificate for testing.
// Returns the x509 cert, tls cert, and PEM-encoded certificate bytes.
func generateTestCert(t *testing.T) (*x509.Certificate, tls.Certificate, []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost", "127.0.0.1"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}

	x509Cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		t.Fatalf("failed to parse certificate: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("failed to marshal key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("failed to create TLS key pair: %v", err)
	}

	return x509Cert, tlsCert, certPEM
}

func TestSPKIFingerprint(t *testing.T) {
	x509Cert, _, _ := generateTestCert(t)

	fingerprint := SPKIFingerprint(x509Cert)

	// Verify manually: base64(sha256(RawSubjectPublicKeyInfo))
	sum := sha256.Sum256(x509Cert.RawSubjectPublicKeyInfo)
	expected := base64.StdEncoding.EncodeToString(sum[:])

	if fingerprint != expected {
		t.Errorf("SPKIFingerprint mismatch: got %q, want %q", fingerprint, expected)
	}

	// Verify it's non-empty and looks like base64
	if len(fingerprint) == 0 {
		t.Error("SPKIFingerprint returned empty string")
	}
}

func TestSPKIFingerprint_DifferentKeys(t *testing.T) {
	cert1, _, _ := generateTestCert(t)
	cert2, _, _ := generateTestCert(t)

	fp1 := SPKIFingerprint(cert1)
	fp2 := SPKIFingerprint(cert2)

	if fp1 == fp2 {
		t.Error("different keys should produce different SPKI fingerprints")
	}
}

func TestNewTransport_Disabled(t *testing.T) {
	transport, err := NewTransport(false, rpc.ClientTLSConfig{}, 30*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if transport.TLSClientConfig != nil {
		t.Error("TLS should not be configured when disabled")
	}
	if transport.MaxIdleConns != 10 {
		t.Errorf("MaxIdleConns = %d, want 10", transport.MaxIdleConns)
	}
	if transport.ResponseHeaderTimeout != 30*time.Second {
		t.Errorf("ResponseHeaderTimeout = %v, want 30s", transport.ResponseHeaderTimeout)
	}
}

func TestNewTransport_SystemCA(t *testing.T) {
	transport, err := NewTransport(true, rpc.ClientTLSConfig{}, 30*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if transport.TLSClientConfig == nil {
		t.Fatal("TLS config should be set when enabled")
	}
	if transport.TLSClientConfig.RootCAs != nil {
		t.Error("RootCAs should be nil (system roots) when no CAFile specified")
	}
	if transport.TLSClientConfig.MinVersion != tls.VersionTLS13 {
		t.Errorf("MinVersion = %d, want TLS 1.3 (%d)", transport.TLSClientConfig.MinVersion, tls.VersionTLS13)
	}
}

func TestNewTransport_CustomCA(t *testing.T) {
	_, _, certPEM := generateTestCert(t)

	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, certPEM, 0644); err != nil {
		t.Fatalf("failed to write CA file: %v", err)
	}

	transport, err := NewTransport(true, rpc.ClientTLSConfig{CAFile: caFile}, 30*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if transport.TLSClientConfig == nil {
		t.Fatal("TLS config should be set")
	}
	if transport.TLSClientConfig.RootCAs == nil {
		t.Error("RootCAs should be set when CAFile specified")
	}
}

func TestNewTransport_CustomCA_BadFile(t *testing.T) {
	_, err := NewTransport(true, rpc.ClientTLSConfig{CAFile: "/nonexistent/ca.pem"}, 30*time.Second)
	if err == nil {
		t.Fatal("expected error for nonexistent CA file")
	}
}

func TestNewTransport_CustomCA_BadPEM(t *testing.T) {
	badFile := filepath.Join(t.TempDir(), "bad.pem")
	if err := os.WriteFile(badFile, []byte("not a PEM file"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	_, err := NewTransport(true, rpc.ClientTLSConfig{CAFile: badFile}, 30*time.Second)
	if err == nil {
		t.Fatal("expected error for invalid PEM content")
	}
}

func TestNewTransport_SPKIPin_Match(t *testing.T) {
	// Start a TLS test server
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Extract the server's leaf cert SPKI fingerprint
	serverCert := server.TLS.Certificates[0]
	leaf, err := x509.ParseCertificate(serverCert.Certificate[0])
	if err != nil {
		t.Fatalf("failed to parse server cert: %v", err)
	}
	pin := SPKIFingerprint(leaf)

	// Write the server's CA cert to a temp file for chain verification
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.TLS.Certificates[0].Certificate[0]})
	if err := os.WriteFile(caFile, caPEM, 0644); err != nil {
		t.Fatalf("failed to write CA file: %v", err)
	}

	transport, err := NewTransport(true, rpc.ClientTLSConfig{
		CAFile:    caFile,
		PinSHA256: pin,
	}, 30*time.Second)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}

	client := &http.Client{Transport: transport}
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("request with matching pin should succeed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestNewTransport_SPKIPin_Mismatch(t *testing.T) {
	// Start a TLS test server
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Write the server's cert as CA for chain verification
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.TLS.Certificates[0].Certificate[0]})
	if err := os.WriteFile(caFile, caPEM, 0644); err != nil {
		t.Fatalf("failed to write CA file: %v", err)
	}

	// Use a wrong pin
	transport, err := NewTransport(true, rpc.ClientTLSConfig{
		CAFile:    caFile,
		PinSHA256: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
	}, 30*time.Second)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}

	client := &http.Client{Transport: transport}
	_, err = client.Get(server.URL)
	if err == nil {
		t.Fatal("request with mismatched pin should fail")
	}

	// Verify the error message mentions the helper command
	errMsg := err.Error()
	if !strings.Contains(errMsg, "SPKI hash mismatch") {
		t.Errorf("error should mention SPKI hash mismatch, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "rpc-cert-fingerprint") {
		t.Errorf("error should mention rpc-cert-fingerprint helper, got: %s", errMsg)
	}
}

func TestNewTransport_SPKIPin_NoCA(t *testing.T) {
	// Pin-only mode: self-signed cert, no CAFile.
	// The pin should be the sole trust anchor.
	x509Cert, tlsCert, _ := generateTestCert(t)
	pin := SPKIFingerprint(x509Cert)

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		MinVersion:   tls.VersionTLS13,
	}
	server.StartTLS()
	defer server.Close()

	// Correct pin, no CA — should succeed
	transport, err := NewTransport(true, rpc.ClientTLSConfig{PinSHA256: pin}, 30*time.Second)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}
	client := &http.Client{Transport: transport}
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("pin-only with correct pin should succeed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	// Wrong pin, no CA — should fail
	transport2, err := NewTransport(true, rpc.ClientTLSConfig{PinSHA256: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}, 30*time.Second)
	if err != nil {
		t.Fatalf("failed to create transport: %v", err)
	}
	client2 := &http.Client{Transport: transport2}
	_, err = client2.Get(server.URL)
	if err == nil {
		t.Fatal("pin-only with wrong pin should fail")
	}
}

func TestNewTransport_SPKIPin_ConfiguresVerifyConnection(t *testing.T) {
	transport, err := NewTransport(true, rpc.ClientTLSConfig{PinSHA256: "somepin"}, 30*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if transport.TLSClientConfig == nil {
		t.Fatal("TLS config should be set")
	}
	if !transport.TLSClientConfig.InsecureSkipVerify {
		t.Error("InsecureSkipVerify should be true when pin is set (chain verified in VerifyConnection)")
	}
	if transport.TLSClientConfig.VerifyConnection == nil {
		t.Error("VerifyConnection callback should be set when pin is configured")
	}
}

