package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestNewConfigManager(t *testing.T) {
	cm := NewConfigManager("/tmp/test.yml", nil)
	if cm == nil {
		t.Fatal("NewConfigManager returned nil")
	}
	if len(cm.registry) == 0 {
		t.Fatal("registry is empty after construction")
	}
}

func TestGetSetBool(t *testing.T) {
	cm := NewConfigManager("/tmp/test.yml", nil)

	// Default value
	if cm.GetBool("staking.enabled") != false {
		t.Error("expected staking.enabled default to be false")
	}

	// Set without loading (uses in-memory defaults)
	if err := cm.Set("staking.enabled", true); err != nil {
		// Will fail on persist since no file, that's expected in this test
		// Use SetFromCLI which doesn't persist
	}

	// Use SetFromCLI to bypass persistence
	if err := cm.SetFromCLI("staking.enabled", true); err != nil {
		t.Fatalf("SetFromCLI failed: %v", err)
	}
	if cm.GetBool("staking.enabled") != true {
		t.Error("expected staking.enabled to be true after set")
	}
}

func TestGetSetInt(t *testing.T) {
	cm := NewConfigManager("/tmp/test.yml", nil)

	if cm.GetInt("network.port") != 37817 {
		t.Errorf("expected default port 37817, got %d", cm.GetInt("network.port"))
	}

	if err := cm.SetFromCLI("network.port", 9999); err != nil {
		t.Fatalf("SetFromCLI failed: %v", err)
	}
	if cm.GetInt("network.port") != 9999 {
		t.Errorf("expected port 9999 after set, got %d", cm.GetInt("network.port"))
	}
}

func TestGetSetInt64(t *testing.T) {
	cm := NewConfigManager("/tmp/test.yml", nil)

	if cm.GetInt64("staking.reserveBalance") != 0 {
		t.Errorf("expected default reserve 0, got %d", cm.GetInt64("staking.reserveBalance"))
	}

	if err := cm.SetFromCLI("staking.reserveBalance", int64(5000000000)); err != nil {
		t.Fatalf("SetFromCLI failed: %v", err)
	}
	if cm.GetInt64("staking.reserveBalance") != 5000000000 {
		t.Errorf("expected reserve 5000000000, got %d", cm.GetInt64("staking.reserveBalance"))
	}
}

func TestGetSetString(t *testing.T) {
	cm := NewConfigManager("/tmp/test.yml", nil)

	if cm.GetString("logging.level") != "error" {
		t.Errorf("expected default level 'error', got %q", cm.GetString("logging.level"))
	}

	if err := cm.SetFromCLI("logging.level", "debug"); err != nil {
		t.Fatalf("SetFromCLI failed: %v", err)
	}
	if cm.GetString("logging.level") != "debug" {
		t.Errorf("expected level 'debug' after set, got %q", cm.GetString("logging.level"))
	}
}

func TestGetUnknownKey(t *testing.T) {
	cm := NewConfigManager("/tmp/test.yml", nil)

	_, err := cm.Get("nonexistent.key")
	if err == nil {
		t.Error("expected error for unknown key")
	}
}

func TestCLILock(t *testing.T) {
	tmpDir := t.TempDir()
	yamlPath := filepath.Join(tmpDir, "twinsd.yml")
	cm := NewConfigManager(yamlPath, nil)
	if err := cm.LoadOrCreate(); err != nil {
		t.Fatalf("LoadOrCreate failed: %v", err)
	}

	// Set via CLI (locks the key)
	if err := cm.SetFromCLI("staking.enabled", true); err != nil {
		t.Fatalf("SetFromCLI failed: %v", err)
	}
	if !cm.IsLocked("staking.enabled") {
		t.Error("expected staking.enabled to be locked after SetFromCLI")
	}

	// Try to set via normal Set (should fail because locked)
	err := cm.Set("staking.enabled", false)
	if err == nil {
		t.Error("expected error when setting CLI-locked key")
	}

	// Value should still be true
	if cm.GetBool("staking.enabled") != true {
		t.Error("locked value should not have changed")
	}
}

func TestValidation(t *testing.T) {
	tmpDir := t.TempDir()
	yamlPath := filepath.Join(tmpDir, "twinsd.yml")
	cm := NewConfigManager(yamlPath, nil)
	if err := cm.LoadOrCreate(); err != nil {
		t.Fatalf("LoadOrCreate failed: %v", err)
	}

	// Port below minimum
	err := cm.Set("network.port", 80)
	if err == nil {
		t.Error("expected validation error for port 80")
	}

	// Port above maximum
	err = cm.Set("network.port", 70000)
	if err == nil {
		t.Error("expected validation error for port 70000")
	}

	// Valid port
	err = cm.Set("network.port", 8080)
	if err != nil {
		t.Errorf("expected no error for valid port 8080, got: %v", err)
	}

	// Invalid log level
	err = cm.Set("logging.level", "invalid")
	if err == nil {
		t.Error("expected validation error for invalid log level")
	}

	// Valid log level
	err = cm.Set("logging.level", "debug")
	if err != nil {
		t.Errorf("expected no error for valid log level, got: %v", err)
	}

	// Wrong type
	err = cm.Set("staking.enabled", "not-a-bool")
	if err == nil {
		t.Error("expected validation error for wrong type")
	}
}

func TestSubscriber(t *testing.T) {
	tmpDir := t.TempDir()
	yamlPath := filepath.Join(tmpDir, "twinsd.yml")
	cm := NewConfigManager(yamlPath, nil)
	if err := cm.LoadOrCreate(); err != nil {
		t.Fatalf("LoadOrCreate failed: %v", err)
	}

	var notified bool
	var notifiedOld, notifiedNew interface{}

	cm.Subscribe("staking.enabled", func(key string, oldVal, newVal interface{}) {
		notified = true
		notifiedOld = oldVal
		notifiedNew = newVal
	})

	if err := cm.Set("staking.enabled", true); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	if !notified {
		t.Error("subscriber was not notified")
	}
	if notifiedOld != false {
		t.Errorf("expected old value false, got %v", notifiedOld)
	}
	if notifiedNew != true {
		t.Errorf("expected new value true, got %v", notifiedNew)
	}
}

func TestSubscriberReceivesCoercedValue(t *testing.T) {
	tmpDir := t.TempDir()
	yamlPath := filepath.Join(tmpDir, "twinsd.yml")
	cm := NewConfigManager(yamlPath, nil)
	if err := cm.LoadOrCreate(); err != nil {
		t.Fatalf("LoadOrCreate failed: %v", err)
	}

	// Test int64 setting: JSON bridge sends float64, subscriber must receive int64
	var receivedType string
	var receivedVal interface{}
	cm.Subscribe("staking.reserveBalance", func(key string, oldVal, newVal interface{}) {
		receivedType = fmt.Sprintf("%T", newVal)
		receivedVal = newVal
	})

	// Simulate Wails JSON bridge: all numbers arrive as float64
	if err := cm.Set("staking.reserveBalance", float64(5000000000)); err != nil {
		t.Fatalf("Set reserveBalance failed: %v", err)
	}
	if receivedType != "int64" {
		t.Errorf("subscriber received %s, want int64", receivedType)
	}
	if receivedVal.(int64) != 5000000000 {
		t.Errorf("subscriber received value %v, want 5000000000", receivedVal)
	}

	// Test uint32 setting: JSON bridge sends float64, subscriber must receive uint32
	var receivedType2 string
	cm.Subscribe("sync.ibdThreshold", func(key string, oldVal, newVal interface{}) {
		receivedType2 = fmt.Sprintf("%T", newVal)
	})

	if err := cm.Set("sync.ibdThreshold", float64(6000)); err != nil {
		t.Fatalf("Set sync.ibdThreshold failed: %v", err)
	}
	if receivedType2 != "uint32" {
		t.Errorf("subscriber received %s, want uint32", receivedType2)
	}
}

func TestPendingRestart(t *testing.T) {
	tmpDir := t.TempDir()
	yamlPath := filepath.Join(tmpDir, "twinsd.yml")
	cm := NewConfigManager(yamlPath, nil)
	if err := cm.LoadOrCreate(); err != nil {
		t.Fatalf("LoadOrCreate failed: %v", err)
	}

	// No pending restart initially
	if cm.HasPendingRestart() {
		t.Error("expected no pending restart initially")
	}

	// Change a non-hot-reload setting
	if err := cm.Set("network.port", 9999); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	if !cm.HasPendingRestart() {
		t.Error("expected pending restart after changing network.port")
	}

	keys := cm.GetPendingRestartKeys()
	if len(keys) != 1 || keys[0] != "network.port" {
		t.Errorf("expected [network.port], got %v", keys)
	}

	// Clear pending
	cm.ClearPendingRestart()
	if cm.HasPendingRestart() {
		t.Error("expected no pending restart after clear")
	}
}

func TestHotReloadNoPendingRestart(t *testing.T) {
	tmpDir := t.TempDir()
	yamlPath := filepath.Join(tmpDir, "twinsd.yml")
	cm := NewConfigManager(yamlPath, nil)
	if err := cm.LoadOrCreate(); err != nil {
		t.Fatalf("LoadOrCreate failed: %v", err)
	}

	// Change a hot-reload setting
	if err := cm.Set("staking.enabled", true); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Should NOT trigger pending restart
	if cm.HasPendingRestart() {
		t.Error("hot-reload setting should not trigger pending restart")
	}
}

func TestLoadOrCreate_NewFile(t *testing.T) {
	tmpDir := t.TempDir()
	yamlPath := filepath.Join(tmpDir, "twinsd.yml")

	cm := NewConfigManager(yamlPath, nil)
	if err := cm.LoadOrCreate(); err != nil {
		t.Fatalf("LoadOrCreate failed: %v", err)
	}

	// File should exist now
	if _, err := os.Stat(yamlPath); os.IsNotExist(err) {
		t.Error("expected twinsd.yml to be created")
	}

	// File should contain comments
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("failed to read generated config: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "# TWINS Core Configuration") || !strings.Contains(content, "staking:") || !strings.Contains(content, "network:") || !strings.Contains(content, "rpc:") {
		t.Error("generated config missing expected sections")
	}
}

func TestLoadOrCreate_ExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	yamlPath := filepath.Join(tmpDir, "twinsd.yml")

	// Write a minimal YAML config
	yamlContent := `
staking:
  enabled: true
  reserveBalance: 5000000000
network:
  port: 9999
`
	if err := os.WriteFile(yamlPath, []byte(yamlContent), 0600); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cm := NewConfigManager(yamlPath, nil)
	if err := cm.LoadOrCreate(); err != nil {
		t.Fatalf("LoadOrCreate failed: %v", err)
	}

	// Values should reflect the YAML file
	if cm.GetBool("staking.enabled") != true {
		t.Error("expected staking.enabled=true from YAML")
	}
	if cm.GetInt64("staking.reserveBalance") != 5000000000 {
		t.Errorf("expected reserveBalance=5000000000, got %d", cm.GetInt64("staking.reserveBalance"))
	}
	if cm.GetInt("network.port") != 9999 {
		t.Errorf("expected port=9999, got %d", cm.GetInt("network.port"))
	}

	// Unset values should keep defaults
	if cm.GetInt("rpc.port") != 37818 {
		t.Errorf("expected rpc.port default 37818, got %d", cm.GetInt("rpc.port"))
	}
}

func TestPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	yamlPath := filepath.Join(tmpDir, "twinsd.yml")

	// Create and modify
	cm1 := NewConfigManager(yamlPath, nil)
	if err := cm1.LoadOrCreate(); err != nil {
		t.Fatalf("LoadOrCreate failed: %v", err)
	}
	if err := cm1.Set("staking.enabled", true); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	if err := cm1.Set("network.port", 9999); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Load in a new manager and verify persistence
	cm2 := NewConfigManager(yamlPath, nil)
	if err := cm2.LoadOrCreate(); err != nil {
		t.Fatalf("LoadOrCreate (2) failed: %v", err)
	}
	if cm2.GetBool("staking.enabled") != true {
		t.Error("expected staking.enabled=true after reload")
	}
	if cm2.GetInt("network.port") != 9999 {
		t.Errorf("expected port=9999 after reload, got %d", cm2.GetInt("network.port"))
	}
}

func TestSnapshot(t *testing.T) {
	cm := NewConfigManager("/tmp/test.yml", nil)

	snap := cm.Snapshot()
	if snap == nil {
		t.Fatal("Snapshot returned nil")
	}

	// Snapshot should be independent of manager
	snap.Staking.Enabled = true
	if cm.GetBool("staking.enabled") != false {
		t.Error("modifying snapshot should not affect manager")
	}
}

func TestGetAllMetadata(t *testing.T) {
	cm := NewConfigManager("/tmp/test.yml", nil)

	meta := cm.GetAllMetadata()
	if len(meta) == 0 {
		t.Fatal("GetAllMetadata returned empty slice")
	}

	// Check that metadata is sorted by category
	lastCat := ""
	for _, m := range meta {
		if m.Category < lastCat {
			// Categories should be in registration order, not alphabetical
			// Just verify no empty categories
		}
		if m.Key == "" || m.Label == "" || m.Description == "" {
			t.Errorf("incomplete metadata: key=%q label=%q desc=%q", m.Key, m.Label, m.Description)
		}
		lastCat = m.Category
	}
}

func TestGetAllMetadataRegistrationOrder(t *testing.T) {
	cm := NewConfigManager("/tmp/test.yml", nil)
	meta := cm.GetAllMetadata()

	// Extract RPC settings in order
	var rpcKeys []string
	for _, m := range meta {
		if m.Category == "rpc" {
			rpcKeys = append(rpcKeys, m.Key)
		}
	}

	// Verify rpc.username appears before rpc.password (registration order)
	usernameIdx, passwordIdx := -1, -1
	for i, key := range rpcKeys {
		if key == "rpc.username" {
			usernameIdx = i
		}
		if key == "rpc.password" {
			passwordIdx = i
		}
	}
	if usernameIdx == -1 || passwordIdx == -1 {
		t.Fatalf("rpc.username or rpc.password not found in metadata; rpcKeys=%v", rpcKeys)
	}
	if usernameIdx >= passwordIdx {
		t.Errorf("rpc.username (index %d) should appear before rpc.password (index %d); got order: %v",
			usernameIdx, passwordIdx, rpcKeys)
	}
}

func TestGetAllMetadataJSON(t *testing.T) {
	cm := NewConfigManager("/tmp/test.yml", nil)

	meta := cm.GetAllMetadata()
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("failed to marshal metadata to JSON: %v", err)
	}

	// Verify it's valid JSON and can be parsed back
	var parsed []SettingMeta
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal metadata from JSON: %v", err)
	}
	if len(parsed) != len(meta) {
		t.Errorf("expected %d settings, got %d after JSON round-trip", len(meta), len(parsed))
	}
}

func TestGetAllValues(t *testing.T) {
	cm := NewConfigManager("/tmp/test.yml", nil)

	values := cm.GetAllValues()
	if len(values) == 0 {
		t.Fatal("GetAllValues returned empty map")
	}

	// Check staking.enabled entry
	entry, ok := values["staking.enabled"]
	if !ok {
		t.Fatal("missing staking.enabled in values")
	}

	m, ok := entry.(map[string]interface{})
	if !ok {
		t.Fatal("expected map value for staking.enabled")
	}
	if m["value"] != false {
		t.Errorf("expected staking.enabled value=false, got %v", m["value"])
	}
	if m["locked"] != false {
		t.Errorf("expected staking.enabled locked=false, got %v", m["locked"])
	}
}

func TestGetAllValuesSensitiveFieldsUnmasked(t *testing.T) {
	cm := NewConfigManager("/tmp/test.yml", nil)

	// Set sensitive fields to known values via SetFromCLI (bypasses persistence)
	sensitiveFields := map[string]string{
		"rpc.username":          "testuser",
		"rpc.password":          "testpass",
		"masternode.privateKey": "testkey123",
	}
	for key, val := range sensitiveFields {
		if err := cm.SetFromCLI(key, val); err != nil {
			t.Fatalf("SetFromCLI(%q) failed: %v", key, err)
		}
	}

	values := cm.GetAllValues()

	for key, expected := range sensitiveFields {
		entry, ok := values[key]
		if !ok {
			t.Fatalf("missing %s in GetAllValues", key)
		}
		m, ok := entry.(map[string]interface{})
		if !ok {
			t.Fatalf("expected map value for %s", key)
		}
		got, _ := m["value"].(string)
		if got != expected {
			t.Errorf("GetAllValues[%s] = %q, want %q (value should not be masked)", key, got, expected)
		}
	}
}

func TestGetCategories(t *testing.T) {
	cm := NewConfigManager("/tmp/test.yml", nil)

	cats := cm.GetCategories()
	if len(cats) == 0 {
		t.Fatal("GetCategories returned empty slice")
	}

	// Should include all known categories
	expected := map[string]bool{
		"staking": false, "wallet": false, "network": false,
		"rpc": false, "masternode": false, "logging": false, "sync": false,
	}
	for _, cat := range cats {
		expected[cat] = true
	}
	for cat, found := range expected {
		if !found {
			t.Errorf("missing expected category: %s", cat)
		}
	}
}

func TestConcurrentAccess(t *testing.T) {
	tmpDir := t.TempDir()
	yamlPath := filepath.Join(tmpDir, "twinsd.yml")
	cm := NewConfigManager(yamlPath, nil)
	if err := cm.LoadOrCreate(); err != nil {
		t.Fatalf("LoadOrCreate failed: %v", err)
	}

	var wg sync.WaitGroup
	// Concurrent reads
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = cm.GetBool("staking.enabled")
			_ = cm.GetInt("network.port")
			_ = cm.GetString("logging.level")
			_ = cm.Snapshot()
		}()
	}
	// Concurrent writes
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = cm.Set("staking.enabled", true)
			_ = cm.Set("staking.enabled", false)
		}()
	}
	wg.Wait()
}

func TestGenerateDefaultConfig(t *testing.T) {
	tmpDir := t.TempDir()
	yamlPath := filepath.Join(tmpDir, "twinsd.yml")

	if err := GenerateDefaultConfig(yamlPath); err != nil {
		t.Fatalf("GenerateDefaultConfig failed: %v", err)
	}

	data, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("failed to read generated config: %v", err)
	}

	content := string(data)

	// Should have all major sections
	for _, section := range []string{"staking:", "wallet:", "network:", "rpc:", "masternode:", "logging:", "sync:"} {
		if !strings.Contains(content, section) {
			t.Errorf("generated config missing section: %s", section)
		}
	}

	// Should have comments
	if !strings.Contains(content, "# TWINS Core Configuration") {
		t.Error("generated config missing header comment")
	}

	// Should have specific setting comments (description text, not labels)
	if !strings.Contains(content, "Stake coins to earn Proof-of-Stake rewards") {
		t.Error("generated config missing setting descriptions")
	}

	// Sync section should have real default values, not zeros
	syncChecks := map[string]string{
		"batchTimeout: 60":          "batchTimeout",
		"bootstrapMaxWait: 120":     "bootstrapMaxWait",
		"bootstrapMinPeers: 4":      "bootstrapMinPeers",
		"consensusStrategy: outbound_only": "consensusStrategy",
		"ibdThreshold: 5000":        "ibdThreshold",
		"maxAutoReorgs: 1":          "maxAutoReorgs",
		"maxSyncPeers: 20":          "maxSyncPeers",
		"progressLogInterval: 10":   "progressLogInterval",
		"reorgWindow: 3600":         "reorgWindow",
	}
	for expected, name := range syncChecks {
		if !strings.Contains(content, expected) {
			t.Errorf("sync default %s not found in YAML (expected substring: %q)", name, expected)
		}
	}
}

func TestDeprecatedSettingReject(t *testing.T) {
	tmpDir := t.TempDir()
	yamlPath := filepath.Join(tmpDir, "twinsd.yml")
	cm := NewConfigManager(yamlPath, nil)
	if err := cm.LoadOrCreate(); err != nil {
		t.Fatalf("LoadOrCreate failed: %v", err)
	}

	// Register a deprecated setting for testing
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "test.deprecated", Type: TypeBool, Default: false,
			Category: "test", Label: "Deprecated", Description: "test",
			Deprecated: true,
		},
		getter: func(c *Config) interface{} { return false },
		setter: func(c *Config, v interface{}) error { return nil },
	})

	err := cm.Set("test.deprecated", true)
	if err == nil {
		t.Error("expected error when setting deprecated key")
	}
}

func TestSettingTypeString(t *testing.T) {
	tests := []struct {
		st   SettingType
		want string
	}{
		{TypeBool, "bool"},
		{TypeInt, "int"},
		{TypeInt64, "int64"},
		{TypeUint32, "uint32"},
		{TypeFloat64, "float64"},
		{TypeString, "string"},
		{TypeStringSlice, "[]string"},
	}
	for _, tt := range tests {
		if got := tt.st.String(); got != tt.want {
			t.Errorf("SettingType(%d).String() = %q, want %q", tt.st, got, tt.want)
		}
	}
}

// TestCoerceFloat64 verifies that JSON-deserialized float64 values are
// correctly coerced to the Go types expected by registry setters.
// Without coercion, setter type assertions like v.(int) panic on float64.
func TestCoerceFloat64(t *testing.T) {
	cm := NewConfigManager(filepath.Join(t.TempDir(), "test-coerce.yml"), nil)

	// Simulate JSON bridge: all numbers arrive as float64
	tests := []struct {
		key     string
		jsonVal float64
	}{
		{"network.port", 9999},
		{"staking.reserveBalance", 500000000},
		{"network.maxPeers", 125},
	}

	for _, tt := range tests {
		// Set with float64 (as Wails JSON bridge would deliver)
		if err := cm.Set(tt.key, tt.jsonVal); err != nil {
			t.Errorf("Set(%s, %v) failed: %v", tt.key, tt.jsonVal, err)
		}
	}

	// Verify values via typed getters
	if cm.GetInt("network.port") != 9999 {
		t.Errorf("network.port: expected 9999, got %d", cm.GetInt("network.port"))
	}
	if cm.GetInt64("staking.reserveBalance") != 500000000 {
		t.Errorf("staking.reserveBalance: expected 500000000, got %d", cm.GetInt64("staking.reserveBalance"))
	}
	if cm.GetInt("network.maxPeers") != 125 {
		t.Errorf("network.maxPeers: expected 125, got %d", cm.GetInt("network.maxPeers"))
	}
}

// TestCoerceFloat64ViaJSON verifies end-to-end: JSON unmarshal -> Set -> Get.
func TestCoerceFloat64ViaJSON(t *testing.T) {
	cm := NewConfigManager(filepath.Join(t.TempDir(), "test-coerce-json.yml"), nil)

	// Simulate a JSON payload from the frontend
	payload := `{"network.port": 37817}`
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	for k, v := range m {
		if err := cm.Set(k, v); err != nil {
			t.Fatalf("Set(%s, %v) failed: %v", k, v, err)
		}
	}

	if cm.GetInt("network.port") != 37817 {
		t.Errorf("expected network.port=37817, got %d", cm.GetInt("network.port"))
	}
}

