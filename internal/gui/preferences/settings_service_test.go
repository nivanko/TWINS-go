package preferences

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestNewSettingsService tests service initialization
func TestNewSettingsService(t *testing.T) {
	// Create temp directory for test
	tmpDir := t.TempDir()
	origFunc := getSettingsPath
	getSettingsPath = func() string {
		return filepath.Join(tmpDir, "settings.json")
	}
	defer func() { getSettingsPath = origFunc }()

	service, err := NewSettingsService()
	if err != nil {
		t.Fatalf("NewSettingsService failed: %v", err)
	}

	if service == nil {
		t.Fatal("Expected non-nil service")
	}

	// Should have default settings
	settings := service.GetAll()
	if settings == nil {
		t.Fatal("Expected non-nil settings")
	}

	// Verify some defaults
	if settings.DisplayUnit != DisplayUnitTWINS {
		t.Errorf("Expected DisplayUnit=%d, got %d", DisplayUnitTWINS, settings.DisplayUnit)
	}
	if settings.Digits != 8 {
		t.Errorf("Expected Digits=8, got %d", settings.Digits)
	}
}

// TestSettingsServiceLoadSave tests save and load operations
func TestSettingsServiceLoadSave(t *testing.T) {
	tmpDir := t.TempDir()
	settingsPath := filepath.Join(tmpDir, "settings.json")

	origFunc := getSettingsPath
	getSettingsPath = func() string { return settingsPath }
	defer func() { getSettingsPath = origFunc }()

	// Create service and modify settings
	service, err := NewSettingsService()
	if err != nil {
		t.Fatalf("NewSettingsService failed: %v", err)
	}

	// Modify some settings
	if err := service.SetBool("fMinimizeToTray", true); err != nil {
		t.Fatalf("SetBool failed: %v", err)
	}
	if err := service.SetInt("nDisplayUnit", 2); err != nil {
		t.Fatalf("SetInt failed: %v", err)
	}
	if err := service.SetString("theme", "dark"); err != nil {
		t.Fatalf("SetString failed: %v", err)
	}
	if err := service.SetInt64("nTransactionFee", 50000); err != nil {
		t.Fatalf("SetInt64 failed: %v", err)
	}

	// Verify file was created
	if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
		t.Fatal("Settings file was not created")
	}

	// Create new service to load saved settings
	service2, err := NewSettingsService()
	if err != nil {
		t.Fatalf("NewSettingsService (reload) failed: %v", err)
	}

	// Verify loaded values
	if !service2.GetBool("fMinimizeToTray") {
		t.Error("fMinimizeToTray not persisted")
	}
	if service2.GetInt("nDisplayUnit") != 2 {
		t.Errorf("nDisplayUnit: expected 2, got %d", service2.GetInt("nDisplayUnit"))
	}
	if service2.GetString("theme") != "dark" {
		t.Errorf("theme: expected 'dark', got '%s'", service2.GetString("theme"))
	}
	if service2.GetInt64("nTransactionFee") != 50000 {
		t.Errorf("nTransactionFee: expected 50000, got %d", service2.GetInt64("nTransactionFee"))
	}
}

// TestSettingsServiceReset tests reset functionality
func TestSettingsServiceReset(t *testing.T) {
	tmpDir := t.TempDir()
	origFunc := getSettingsPath
	getSettingsPath = func() string { return filepath.Join(tmpDir, "settings.json") }
	defer func() { getSettingsPath = origFunc }()

	service, _ := NewSettingsService()

	// Modify settings
	service.SetBool("fMinimizeToTray", true)
	service.SetInt("digits", 6)
	service.SetString("theme", "custom")

	// Reset
	if err := service.Reset(); err != nil {
		t.Fatalf("Reset failed: %v", err)
	}

	// Verify defaults restored
	if service.GetBool("fMinimizeToTray") != false {
		t.Error("fMinimizeToTray not reset to default")
	}
	if service.GetInt("digits") != 8 {
		t.Errorf("digits: expected 8, got %d", service.GetInt("digits"))
	}
	if service.GetString("theme") != "dark" {
		t.Errorf("theme: expected 'dark', got '%s'", service.GetString("theme"))
	}
}

// TestSettingsServiceWindowGeometry tests window state persistence
func TestSettingsServiceWindowGeometry(t *testing.T) {
	tmpDir := t.TempDir()
	origFunc := getSettingsPath
	getSettingsPath = func() string { return filepath.Join(tmpDir, "settings.json") }
	defer func() { getSettingsPath = origFunc }()

	service, _ := NewSettingsService()

	// Save window state
	state := WindowState{X: 100, Y: 200, Width: 800, Height: 600, Maximized: false}
	if err := service.SetWindowState("MainWindow", state); err != nil {
		t.Fatalf("SetWindowState failed: %v", err)
	}

	// Retrieve and verify
	retrieved := service.GetWindowState("MainWindow")
	if retrieved.X != 100 || retrieved.Y != 200 {
		t.Errorf("Position mismatch: got (%d,%d), expected (100,200)", retrieved.X, retrieved.Y)
	}
	if retrieved.Width != 800 || retrieved.Height != 600 {
		t.Errorf("Size mismatch: got %dx%d, expected 800x600", retrieved.Width, retrieved.Height)
	}

	// Test non-existent window returns zero state
	empty := service.GetWindowState("NonExistent")
	if empty.Width != 0 || empty.Height != 0 {
		t.Error("Expected zero WindowState for non-existent window")
	}
}

// TestSettingsServiceGetAllDefensiveCopy tests that GetAll returns a copy
func TestSettingsServiceGetAllDefensiveCopy(t *testing.T) {
	tmpDir := t.TempDir()
	origFunc := getSettingsPath
	getSettingsPath = func() string { return filepath.Join(tmpDir, "settings.json") }
	defer func() { getSettingsPath = origFunc }()

	service, _ := NewSettingsService()

	// Get settings and modify the returned copy
	settings1 := service.GetAll()
	settings1.Theme = "modified"
	settings1.Digits = 9999

	// Get again - should not reflect modifications
	settings2 := service.GetAll()
	if settings2.Theme == "modified" {
		t.Error("GetAll did not return defensive copy - Theme was modified")
	}
	if settings2.Digits == 9999 {
		t.Error("GetAll did not return defensive copy - Digits was modified")
	}
}

// TestSettingsServiceUnknownKeys tests handling of unknown keys
func TestSettingsServiceUnknownKeys(t *testing.T) {
	tmpDir := t.TempDir()
	origFunc := getSettingsPath
	getSettingsPath = func() string { return filepath.Join(tmpDir, "settings.json") }
	defer func() { getSettingsPath = origFunc }()

	service, _ := NewSettingsService()

	// Unknown keys should return error on set
	if err := service.SetBool("unknownKey", true); err == nil {
		t.Error("Expected error for unknown bool key")
	}
	if err := service.SetInt("unknownInt", 123); err == nil {
		t.Error("Expected error for unknown int key")
	}
	if err := service.SetString("unknownStr", "test"); err == nil {
		t.Error("Expected error for unknown string key")
	}

	// Unknown keys should return zero values on get (not error)
	if service.GetBool("unknownKey") != false {
		t.Error("Expected false for unknown bool key")
	}
	if service.GetInt("unknownInt") != 0 {
		t.Error("Expected 0 for unknown int key")
	}
	if service.GetString("unknownStr") != "" {
		t.Error("Expected empty string for unknown string key")
	}
}

// TestSettingsServiceHasKey tests key existence check
func TestSettingsServiceHasKey(t *testing.T) {
	tmpDir := t.TempDir()
	origFunc := getSettingsPath
	getSettingsPath = func() string { return filepath.Join(tmpDir, "settings.json") }
	defer func() { getSettingsPath = origFunc }()

	service, _ := NewSettingsService()

	// Known keys
	knownKeys := []string{"fMinimizeToTray", "nDisplayUnit", "theme", "nTransactionFee"}
	for _, key := range knownKeys {
		if !service.HasKey(key) {
			t.Errorf("HasKey(%s) should return true", key)
		}
	}

	// Unknown keys
	if service.HasKey("unknownKey") {
		t.Error("HasKey should return false for unknown key")
	}
}

// TestSettingsServiceConcurrency tests basic thread safety
func TestSettingsServiceConcurrency(t *testing.T) {
	tmpDir := t.TempDir()
	origFunc := getSettingsPath
	getSettingsPath = func() string { return filepath.Join(tmpDir, "settings.json") }
	defer func() { getSettingsPath = origFunc }()

	service, _ := NewSettingsService()

	var wg sync.WaitGroup
	iterations := 100

	// Concurrent writers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				service.SetInt("digits", id+j%8)
			}
		}(i)
	}

	// Concurrent readers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = service.GetInt("digits")
				_ = service.GetAll()
			}
		}()
	}

	wg.Wait()
	// If we get here without panic/race, basic concurrency works
}

// TestSettingsServiceJSONRoundTrip tests JSON serialization
func TestSettingsServiceJSONRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	settingsPath := filepath.Join(tmpDir, "settings.json")
	origFunc := getSettingsPath
	getSettingsPath = func() string { return settingsPath }
	defer func() { getSettingsPath = origFunc }()

	service, _ := NewSettingsService()

	// Set various types
	service.SetBool("fMinimizeToTray", true)
	service.SetInt("nDisplayUnit", 1)
	service.SetInt64("nStakeSplitThreshold", 500000000000)
	service.SetString("language", "de")
	service.SetWindowState("TestWindow", WindowState{X: 50, Y: 75, Width: 1024, Height: 768, Maximized: true})

	// Read raw JSON
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("Failed to read settings file: %v", err)
	}

	// Verify JSON structure
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Invalid JSON: %v", err)
	}

	// Check specific fields in JSON
	if parsed["fMinimizeToTray"] != true {
		t.Error("fMinimizeToTray not correctly serialized")
	}
	if parsed["language"] != "de" {
		t.Error("language not correctly serialized")
	}

	// Verify WindowGeometry is a map
	geom, ok := parsed["windowGeometry"].(map[string]interface{})
	if !ok {
		t.Fatal("windowGeometry not serialized as object")
	}
	if _, exists := geom["TestWindow"]; !exists {
		t.Error("TestWindow not in windowGeometry")
	}
}

// TestNewDefaultSettings tests default values
func TestNewDefaultSettings(t *testing.T) {
	defaults := NewDefaultSettings()

	// Verify critical defaults match C++ legacy
	if defaults.StakeSplitThreshold != 200000000000 {
		t.Errorf("StakeSplitThreshold: expected 200000000000, got %d", defaults.StakeSplitThreshold)
	}
	if defaults.TransactionFee != 10000 {
		t.Errorf("TransactionFee: expected 10000, got %d", defaults.TransactionFee)
	}
	if defaults.Theme != "dark" {
		t.Errorf("Theme: expected 'dark', got '%s'", defaults.Theme)
	}
	if defaults.Digits != 8 {
		t.Errorf("Digits: expected 8, got %d", defaults.Digits)
	}
	if defaults.WindowGeometry == nil {
		t.Error("WindowGeometry should be initialized map, not nil")
	}
}
