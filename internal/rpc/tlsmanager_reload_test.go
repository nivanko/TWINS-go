package rpc

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/argon2"
)

// generatePHCHash creates a PHC-format argon2id hash for testing.
func generatePHCHash(password string) string {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		panic(err)
	}
	hash := argon2.IDKey([]byte(password), salt, 3, 65536, 4, 32)
	return fmt.Sprintf("$argon2id$v=19$m=65536,t=3,p=4$%s$%s",
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash))
}

// writeHashFile creates a hash file with proper permissions for testing.
func writeHashFile(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "rpcreload.hash")
	if err := os.WriteFile(path, []byte(content+"\n"), 0600); err != nil {
		t.Fatalf("write hash file: %v", err)
	}
	return path
}

// --- PHC Parser Tests ---

func TestParsePHCArgon2id_Valid(t *testing.T) {
	password := "test-passphrase"
	phc := generatePHCHash(password)

	salt, hash, timeCost, memory, threads, keyLen, err := parsePHCArgon2id(phc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(salt) != 16 {
		t.Errorf("salt length = %d, want 16", len(salt))
	}
	if len(hash) != 32 {
		t.Errorf("hash length = %d, want 32", len(hash))
	}
	if timeCost != 3 {
		t.Errorf("time = %d, want 3", timeCost)
	}
	if memory != 65536 {
		t.Errorf("memory = %d, want 65536", memory)
	}
	if threads != 4 {
		t.Errorf("threads = %d, want 4", threads)
	}
	if keyLen != 32 {
		t.Errorf("keyLen = %d, want 32", keyLen)
	}

	// Verify the parsed hash matches recomputation
	recomputed := argon2.IDKey([]byte(password), salt, timeCost, memory, uint8(threads), keyLen)
	if string(recomputed) != string(hash) {
		t.Error("recomputed hash does not match parsed hash")
	}
}

func TestParsePHCArgon2id_InvalidPrefix(t *testing.T) {
	tests := []struct {
		name string
		phc  string
	}{
		{"wrong algorithm", "$argon2i$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA"},
		{"no dollar prefix", "argon2id$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA"},
		{"empty string", ""},
		{"random text", "not-a-phc-string"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, _, _, _, err := parsePHCArgon2id(tt.phc)
			if err == nil {
				t.Error("expected error for invalid prefix")
			}
		})
	}
}

func TestParsePHCArgon2id_MalformedParams(t *testing.T) {
	tests := []struct {
		name string
		phc  string
	}{
		{"missing params", "$argon2id$v=19$$c2FsdA$aGFzaA"},
		{"bad memory", "$argon2id$v=19$m=abc,t=3,p=4$c2FsdA$aGFzaA"},
		{"bad time", "$argon2id$v=19$m=65536,t=abc,p=4$c2FsdA$aGFzaA"},
		{"bad threads", "$argon2id$v=19$m=65536,t=3,p=abc$c2FsdA$aGFzaA"},
		{"missing fields", "$argon2id$v=19$m=65536,t=3$c2FsdA$aGFzaA"},
		{"bad base64 salt", "$argon2id$v=19$m=65536,t=3,p=4$!!!invalid!!!$aGFzaA"},
		{"bad base64 hash", "$argon2id$v=19$m=65536,t=3,p=4$c2FsdA$!!!invalid!!!"},
		{"too few segments", "$argon2id$v=19$m=65536,t=3,p=4$c2FsdA"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, _, _, _, err := parsePHCArgon2id(tt.phc)
			if err == nil {
				t.Error("expected error for malformed params")
			}
		})
	}
}

// --- VerifyReloadPassphrase Tests ---

func TestVerifyReloadPassphrase_CorrectPassword(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)
	password := "correct-horse-battery-staple"
	hashPath := writeHashFile(t, dir, generatePHCHash(password))

	tm, err := NewTLSManager(TLSConfig{
		Enabled:              true,
		CertFile:             certPath,
		KeyFile:              keyPath,
		ReloadPassphraseFile: hashPath,
	}, logrus.NewEntry(logrus.New()))
	if err != nil {
		t.Fatalf("NewTLSManager: %v", err)
	}
	defer tm.Close()

	if !tm.VerifyReloadPassphrase(password) {
		t.Error("expected true for correct password")
	}
}

func TestVerifyReloadPassphrase_WrongPassword(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)
	hashPath := writeHashFile(t, dir, generatePHCHash("correct-password"))

	tm, err := NewTLSManager(TLSConfig{
		Enabled:              true,
		CertFile:             certPath,
		KeyFile:              keyPath,
		ReloadPassphraseFile: hashPath,
	}, logrus.NewEntry(logrus.New()))
	if err != nil {
		t.Fatalf("NewTLSManager: %v", err)
	}
	defer tm.Close()

	if tm.VerifyReloadPassphrase("wrong-password") {
		t.Error("expected false for wrong password")
	}
}

func TestVerifyReloadPassphrase_NilHash(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)

	// No ReloadPassphraseFile configured
	tm, err := NewTLSManager(TLSConfig{
		Enabled:  true,
		CertFile: certPath,
		KeyFile:  keyPath,
	}, logrus.NewEntry(logrus.New()))
	if err != nil {
		t.Fatalf("NewTLSManager: %v", err)
	}
	defer tm.Close()

	if tm.HasReloadPassphrase() {
		t.Error("expected HasReloadPassphrase() == false when no hash file configured")
	}
	if tm.VerifyReloadPassphrase("anything") {
		t.Error("expected false when no hash loaded")
	}
}

func TestVerifyReloadPassphrase_MutexSerialization(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)
	password := "serialize-test"
	hashPath := writeHashFile(t, dir, generatePHCHash(password))

	tm, err := NewTLSManager(TLSConfig{
		Enabled:              true,
		CertFile:             certPath,
		KeyFile:              keyPath,
		ReloadPassphraseFile: hashPath,
	}, logrus.NewEntry(logrus.New()))
	if err != nil {
		t.Fatalf("NewTLSManager: %v", err)
	}
	defer tm.Close()

	// Measure single-call duration as baseline
	singleStart := time.Now()
	tm.VerifyReloadPassphrase(password)
	singleDuration := time.Since(singleStart)

	// Launch N concurrent calls and measure total wall time.
	// If serialized by mutex, total ≈ N × singleDuration.
	// If fully concurrent, total ≈ singleDuration.
	const n = 4
	var wg sync.WaitGroup
	start := time.Now()
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tm.VerifyReloadPassphrase(password)
		}()
	}
	wg.Wait()
	totalDuration := time.Since(start)

	// Serialized: expect total >= (N-1) × singleDuration (allow scheduling slack).
	// A fully concurrent run would finish in ~1× singleDuration.
	minExpected := time.Duration(n-1) * singleDuration
	if totalDuration < minExpected {
		t.Errorf("concurrent verification too fast: %v < %v (expected serialized: %d × %v)",
			totalDuration, minExpected, n, singleDuration)
	}
}

// --- Reload Atomicity Tests ---

func TestReload_CertAndHashBothSucceed(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)
	password := "reload-test"
	hashPath := writeHashFile(t, dir, generatePHCHash(password))

	tm, err := NewTLSManager(TLSConfig{
		Enabled:              true,
		CertFile:             certPath,
		KeyFile:              keyPath,
		ReloadPassphraseFile: hashPath,
	}, logrus.NewEntry(logrus.New()))
	if err != nil {
		t.Fatalf("NewTLSManager: %v", err)
	}
	defer tm.Close()

	// Generate new cert and new hash
	newCertPath, newKeyPath := generateSelfSignedCertWithCN(t, dir, "reloaded-cert", "cert2.pem", "key2.pem")
	newPassword := "new-password"
	// Overwrite the hash file with new password hash
	if err := os.WriteFile(hashPath, []byte(generatePHCHash(newPassword)+"\n"), 0600); err != nil {
		t.Fatalf("overwrite hash file: %v", err)
	}

	// Point TLSManager to new cert files
	tm.certFile = newCertPath
	tm.keyFile = newKeyPath

	if err := tm.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	// New password should work
	if !tm.VerifyReloadPassphrase(newPassword) {
		t.Error("new password should verify after reload")
	}
	// Old password should fail
	if tm.VerifyReloadPassphrase(password) {
		t.Error("old password should not verify after reload")
	}
}

func TestReload_CertSucceeds_HashFails(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)
	password := "original-password"
	hashPath := writeHashFile(t, dir, generatePHCHash(password))

	tm, err := NewTLSManager(TLSConfig{
		Enabled:              true,
		CertFile:             certPath,
		KeyFile:              keyPath,
		ReloadPassphraseFile: hashPath,
	}, logrus.NewEntry(logrus.New()))
	if err != nil {
		t.Fatalf("NewTLSManager: %v", err)
	}
	defer tm.Close()

	// Get original cert serial for comparison
	origCert := tm.cert.Load()
	origX509, _ := x509.ParseCertificate(origCert.Certificate[0])
	origSerial := origX509.SerialNumber

	// Make hash file unreadable (bad permissions)
	if err := os.Chmod(hashPath, 0644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	// Reload should fail (hash file has wrong perms)
	if err := tm.Reload(); err == nil {
		t.Fatal("expected Reload to fail when hash file has wrong permissions")
	}

	// Cert should NOT have changed (rollback)
	currentCert := tm.cert.Load()
	currentX509, _ := x509.ParseCertificate(currentCert.Certificate[0])
	if currentX509.SerialNumber.Cmp(origSerial) != 0 {
		t.Error("cert was swapped despite hash reload failure — atomicity violated")
	}

	// Original password should still work
	if !tm.VerifyReloadPassphrase(password) {
		t.Error("original password should still verify after failed reload")
	}
}

func TestReload_CertFails_HashNotAttempted(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)
	password := "keep-me"
	hashPath := writeHashFile(t, dir, generatePHCHash(password))

	tm, err := NewTLSManager(TLSConfig{
		Enabled:              true,
		CertFile:             certPath,
		KeyFile:              keyPath,
		ReloadPassphraseFile: hashPath,
	}, logrus.NewEntry(logrus.New()))
	if err != nil {
		t.Fatalf("NewTLSManager: %v", err)
	}
	defer tm.Close()

	// Point cert to non-existent file
	tm.certFile = filepath.Join(dir, "nonexistent.pem")

	if err := tm.Reload(); err == nil {
		t.Fatal("expected Reload to fail with nonexistent cert file")
	}

	// Password should still work (nothing changed)
	if !tm.VerifyReloadPassphrase(password) {
		t.Error("password should still verify when cert reload fails")
	}
}

func TestReload_NoHashFile(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)

	tm, err := NewTLSManager(TLSConfig{
		Enabled:  true,
		CertFile: certPath,
		KeyFile:  keyPath,
	}, logrus.NewEntry(logrus.New()))
	if err != nil {
		t.Fatalf("NewTLSManager: %v", err)
	}
	defer tm.Close()

	// Reload should succeed (just cert, no hash)
	if err := tm.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	// Still no passphrase configured
	if tm.HasReloadPassphrase() {
		t.Error("expected HasReloadPassphrase() == false")
	}
}

// --- Test helper: generate cert with custom CN and filenames ---

func generateSelfSignedCertWithCN(t *testing.T, dir, cn, certFile, keyFile string) (string, string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}

	certPath := filepath.Join(dir, certFile)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	if err := os.WriteFile(certPath, certPEM, 0644); err != nil {
		t.Fatalf("write cert: %v", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPath := filepath.Join(dir, keyFile)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	return certPath, keyPath
}
