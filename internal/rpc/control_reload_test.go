package rpc

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
)

func TestHandleReloadRPCCerts_Disabled_NoTLSManager(t *testing.T) {
	s := &Server{
		handlers: make(map[string]Handler),
		logger:   logrus.NewEntry(logrus.New()),
		shutdown: make(chan struct{}),
		// tlsManager is nil
	}

	req := &Request{
		JSONRPC:    "2.0",
		Method:     "reloadrpccerts",
		Params:     json.RawMessage(`["some-passphrase"]`),
		ID:         1,
		RemoteAddr: "127.0.0.1:12345",
	}

	resp := s.handleReloadRPCCerts(req)
	if resp.Error == nil {
		t.Fatal("expected error when TLS manager is nil")
	}
	if resp.Error.Code != -1 {
		t.Errorf("error code = %d, want -1", resp.Error.Code)
	}
	if msg := resp.Error.Message; msg != "reloadrpccerts is disabled (no reload passphrase configured)" {
		t.Errorf("unexpected error message: %s", msg)
	}
}

func TestHandleReloadRPCCerts_Disabled_NoPassHash(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)

	tm, err := NewTLSManager(TLSConfig{
		Enabled:  true,
		CertFile: certPath,
		KeyFile:  keyPath,
		// No ReloadPassphraseFile
	}, logrus.NewEntry(logrus.New()))
	if err != nil {
		t.Fatalf("NewTLSManager: %v", err)
	}
	defer tm.Close()

	s := &Server{
		handlers:   make(map[string]Handler),
		logger:     logrus.NewEntry(logrus.New()),
		shutdown:   make(chan struct{}),
		tlsManager: tm,
	}

	req := &Request{
		JSONRPC:    "2.0",
		Params:     json.RawMessage(`["some-passphrase"]`),
		ID:         1,
		RemoteAddr: "127.0.0.1:12345",
	}

	resp := s.handleReloadRPCCerts(req)
	if resp.Error == nil {
		t.Fatal("expected error when no passphrase hash configured")
	}
	if resp.Error.Message != "reloadrpccerts is disabled (no reload passphrase configured)" {
		t.Errorf("unexpected error: %s", resp.Error.Message)
	}
}

func TestHandleReloadRPCCerts_CorrectPassphrase(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)
	password := "correct-reload-pass"
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

	s := &Server{
		handlers:   make(map[string]Handler),
		logger:     logrus.NewEntry(logrus.New()),
		shutdown:   make(chan struct{}),
		tlsManager: tm,
	}

	params, _ := json.Marshal([]string{password})
	req := &Request{
		JSONRPC:    "2.0",
		Params:     params,
		ID:         1,
		RemoteAddr: "192.168.1.10:5555",
	}

	resp := s.handleReloadRPCCerts(req)
	if resp.Error != nil {
		t.Fatalf("expected success, got error: %s", resp.Error.Message)
	}
	if resp.Result != "TLS certificates reloaded" {
		t.Errorf("result = %v, want 'TLS certificates reloaded'", resp.Result)
	}
}

func TestHandleReloadRPCCerts_WrongPassphrase(t *testing.T) {
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

	s := &Server{
		handlers:   make(map[string]Handler),
		logger:     logrus.NewEntry(logrus.New()),
		shutdown:   make(chan struct{}),
		tlsManager: tm,
	}

	params, _ := json.Marshal([]string{"wrong-password"})
	req := &Request{
		JSONRPC:    "2.0",
		Params:     params,
		ID:         1,
		RemoteAddr: "10.0.0.5:9999",
	}

	resp := s.handleReloadRPCCerts(req)
	if resp.Error == nil {
		t.Fatal("expected error for wrong passphrase")
	}
	if resp.Error.Message != "incorrect passphrase" {
		t.Errorf("error = %s, want 'incorrect passphrase'", resp.Error.Message)
	}
}

func TestHandleReloadRPCCerts_BackoffEnforced(t *testing.T) {
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

	s := &Server{
		handlers:   make(map[string]Handler),
		logger:     logrus.NewEntry(logrus.New()),
		shutdown:   make(chan struct{}),
		tlsManager: tm,
	}

	ip := "10.0.0.99:1234"
	wrongParams, _ := json.Marshal([]string{"wrong"})

	// First wrong attempt — should get "incorrect passphrase"
	req := &Request{
		JSONRPC:    "2.0",
		Params:     wrongParams,
		ID:         1,
		RemoteAddr: ip,
	}
	resp := s.handleReloadRPCCerts(req)
	if resp.Error == nil || resp.Error.Message != "incorrect passphrase" {
		t.Fatalf("first attempt: expected 'incorrect passphrase', got %v", resp.Error)
	}

	// Second attempt immediately — should get rate limited
	req.ID = 2
	resp = s.handleReloadRPCCerts(req)
	if resp.Error == nil {
		t.Fatal("expected rate limit error on second immediate attempt")
	}
	if msg := resp.Error.Message; len(msg) < 12 || msg[:12] != "rate limited" {
		t.Errorf("expected rate limit message, got: %s", msg)
	}
}

func TestHandleReloadRPCCerts_MissingParams(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)
	hashPath := writeHashFile(t, dir, generatePHCHash("password"))

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

	s := &Server{
		handlers:   make(map[string]Handler),
		logger:     logrus.NewEntry(logrus.New()),
		shutdown:   make(chan struct{}),
		tlsManager: tm,
	}

	tests := []struct {
		name   string
		params json.RawMessage
	}{
		{"empty params", json.RawMessage(`[]`)},
		{"no params", json.RawMessage(`null`)},
		{"non-string param", json.RawMessage(`[123]`)},
		{"empty string", json.RawMessage(`[""]`)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &Request{
				JSONRPC:    "2.0",
				Params:     tt.params,
				ID:         1,
				RemoteAddr: "127.0.0.1:1234",
			}
			resp := s.handleReloadRPCCerts(req)
			if resp.Error == nil {
				t.Error("expected error for invalid params")
			}
		})
	}
}

// TestHandleReloadRPCCerts_HashFilePermissionWarning verifies the config
// template comment warning about reload_passphrase != wallet passphrase.
// This is a documentation/config-level requirement verified by checking the
// help text contains the warning.
func TestHandleReloadRPCCerts_HelpTextWarning(t *testing.T) {
	helpText := GetCommandHelp("reloadrpccerts")
	if helpText == "(No detailed help available)" {
		t.Fatal("reloadrpccerts help text not registered")
	}

	// Verify the warning about wallet passphrase is present
	if !strings.Contains(helpText, "wallet passphrase") {
		t.Error("help text should warn that reload_passphrase must not equal wallet passphrase")
	}
}

// Ensure we don't break existing tests by verifying the hash file
// permission check works at the NewTLSManager level.
func TestNewTLSManager_BadHashFilePermissions(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateSelfSignedCert(t, dir)

	// Write hash file with wrong permissions
	hashPath := writeHashFile(t, dir, generatePHCHash("password"))
	if err := os.Chmod(hashPath, 0644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	_, err := NewTLSManager(TLSConfig{
		Enabled:              true,
		CertFile:             certPath,
		KeyFile:              keyPath,
		ReloadPassphraseFile: hashPath,
	}, logrus.NewEntry(logrus.New()))
	if err == nil {
		t.Fatal("expected error for hash file with wrong permissions")
	}
}
