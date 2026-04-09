package preferences

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// SettingsService manages GUI settings persistence
type SettingsService struct {
	mu       sync.RWMutex
	settings *GUISettings
	path     string
}

// NewSettingsService creates a new settings service and loads existing settings
func NewSettingsService() (*SettingsService, error) {
	settingsPath := getSettingsPath()

	// Ensure the settings directory exists
	settingsDir := filepath.Dir(settingsPath)
	if err := os.MkdirAll(settingsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create settings directory: %w", err)
	}

	service := &SettingsService{
		path: settingsPath,
	}

	// Load existing settings or create defaults
	if err := service.Load(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to load settings: %w", err)
	}

	return service, nil
}

// getSettingsPath returns the path to the settings file
// This is a variable to allow test overrides
var getSettingsPath = func() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "."
	}
	// Clean path to prevent directory traversal
	homeDir = filepath.Clean(homeDir)

	switch runtime.GOOS {
	case "windows":
		return filepath.Join(homeDir, "AppData", "Local", "TWINS-Wallet", "settings.json")
	case "darwin":
		return filepath.Join(homeDir, "Library", "Preferences", "com.twins.wallet", "settings.json")
	default: // Linux and others
		configDir := os.Getenv("XDG_CONFIG_HOME")
		if configDir == "" {
			configDir = filepath.Join(homeDir, ".config")
		}
		return filepath.Join(configDir, "twins-wallet", "settings.json")
	}
}

// Load reads settings from disk
func (s *SettingsService) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			// No settings file yet, use defaults
			s.settings = NewDefaultSettings()
			return nil
		}
		return err
	}

	var settings GUISettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return fmt.Errorf("failed to parse settings: %w", err)
	}

	// Ensure WindowGeometry map is initialized
	if settings.WindowGeometry == nil {
		settings.WindowGeometry = make(map[string]WindowState)
	}

	s.settings = &settings
	return nil
}

// Save writes settings to disk
func (s *SettingsService) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.saveLocked()
}

// saveLocked writes settings to disk (caller must hold lock)
func (s *SettingsService) saveLocked() error {
	if s.settings == nil {
		return fmt.Errorf("no settings to save")
	}

	// Update last modified timestamp
	s.settings.LastModified = time.Now().UTC().Format(time.RFC3339)

	data, err := json.MarshalIndent(s.settings, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal settings: %w", err)
	}

	// Ensure directory exists
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create settings directory: %w", err)
	}

	// Write atomically using temp file
	tempPath := s.path + ".tmp"
	if err := os.WriteFile(tempPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write settings: %w", err)
	}

	if err := os.Rename(tempPath, s.path); err != nil {
		os.Remove(tempPath) // Clean up temp file
		return fmt.Errorf("failed to save settings: %w", err)
	}

	return nil
}

// Reset restores all settings to defaults
func (s *SettingsService) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.settings = NewDefaultSettings()
	return s.saveLocked()
}

// GetAll returns a copy of all settings
func (s *SettingsService) GetAll() *GUISettings {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.settings == nil {
		return NewDefaultSettings()
	}

	// Return a copy to prevent external modification
	copy := *s.settings
	if s.settings.WindowGeometry != nil {
		copy.WindowGeometry = make(map[string]WindowState)
		for k, v := range s.settings.WindowGeometry {
			copy.WindowGeometry[k] = v
		}
	}
	return &copy
}

// SetAll replaces all settings
func (s *SettingsService) SetAll(settings *GUISettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if settings == nil {
		return fmt.Errorf("settings cannot be nil")
	}

	s.settings = settings
	return s.saveLocked()
}

// === Type-safe Getters ===

// GetBool returns a boolean setting value
func (s *SettingsService) GetBool(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.settings == nil {
		return false
	}

	switch key {
	case "fMinimizeToTray":
		return s.settings.MinimizeToTray
	case "fMinimizeOnClose":
		return s.settings.MinimizeOnClose
	case "fHideTrayIcon":
		return s.settings.HideTrayIcon
	case "fShowMasternodesTab":
		return s.settings.ShowMasternodesTab
	case "fCoinControlFeatures":
		return s.settings.CoinControlFeatures
	case "fHideOrphans":
		return s.settings.HideOrphans
	case "fHideZeroBalances":
		return s.settings.HideZeroBalances
	case "fFeeSectionMinimized":
		return s.settings.FeeSectionMinimized
	case "fPayOnlyMinFee":
		return s.settings.PayOnlyMinFee
	case "fSendFreeTransactions":
		return s.settings.SendFreeTransactions
	case "fSubtractFeeFromAmount":
		return s.settings.SubtractFeeFromAmt
	case "fRestartRequired":
		return s.settings.RestartRequired
	default:
		return false
	}
}

// GetInt returns an integer setting value
func (s *SettingsService) GetInt(key string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.settings == nil {
		return 0
	}

	switch key {
	case "nDisplayUnit":
		return s.settings.DisplayUnit
	case "digits":
		return s.settings.Digits
	case "nCoinControlMode":
		return s.settings.CoinControlMode
	case "nCoinControlSortColumn":
		return s.settings.CoinControlSortColumn
	case "nCoinControlSortOrder":
		return s.settings.CoinControlSortOrder
	case "transactionDate":
		return s.settings.TransactionDate
	case "transactionType":
		return s.settings.TransactionType
	case "nFeeRadio":
		return s.settings.FeeRadio
	case "nCustomFeeRadio":
		return s.settings.CustomFeeRadio
	case "nSmartFeeSliderPosition":
		return s.settings.SmartFeeSliderPos
	default:
		return 0
	}
}

// GetInt64 returns a 64-bit integer setting value
func (s *SettingsService) GetInt64(key string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.settings == nil {
		return 0
	}

	switch key {
	case "nStakeSplitThreshold":
		return s.settings.StakeSplitThreshold
	case "nTransactionFee":
		return s.settings.TransactionFee
	case "transactionMinAmount":
		return s.settings.TransactionMinAmount
	default:
		return 0
	}
}

// GetString returns a string setting value
func (s *SettingsService) GetString(key string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.settings == nil {
		return ""
	}

	switch key {
	case "theme":
		return s.settings.Theme
	case "language":
		return s.settings.Language
	case "strThirdPartyTxUrls":
		return s.settings.ThirdPartyTxUrls
	case "current_receive_address":
		return s.settings.CurrentReceiveAddress
	case "strDataDir":
		return s.settings.DataDir
	default:
		return ""
	}
}

// GetWindowState returns the saved state for a window
func (s *SettingsService) GetWindowState(windowName string) WindowState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.settings == nil || s.settings.WindowGeometry == nil {
		return WindowState{}
	}

	return s.settings.WindowGeometry[windowName]
}

// === Type-safe Setters ===

// SetBool sets a boolean setting value and saves
func (s *SettingsService) SetBool(key string, value bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.settings == nil {
		s.settings = NewDefaultSettings()
	}

	switch key {
	case "fMinimizeToTray":
		s.settings.MinimizeToTray = value
	case "fMinimizeOnClose":
		s.settings.MinimizeOnClose = value
	case "fHideTrayIcon":
		s.settings.HideTrayIcon = value
	case "fShowMasternodesTab":
		s.settings.ShowMasternodesTab = value
	case "fCoinControlFeatures":
		s.settings.CoinControlFeatures = value
	case "fHideOrphans":
		s.settings.HideOrphans = value
	case "fHideZeroBalances":
		s.settings.HideZeroBalances = value
	case "fFeeSectionMinimized":
		s.settings.FeeSectionMinimized = value
	case "fPayOnlyMinFee":
		s.settings.PayOnlyMinFee = value
	case "fSendFreeTransactions":
		s.settings.SendFreeTransactions = value
	case "fSubtractFeeFromAmount":
		s.settings.SubtractFeeFromAmt = value
	case "fRestartRequired":
		s.settings.RestartRequired = value
	default:
		return fmt.Errorf("unknown boolean setting: %s", key)
	}

	return s.saveLocked()
}

// SetInt sets an integer setting value and saves
func (s *SettingsService) SetInt(key string, value int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.settings == nil {
		s.settings = NewDefaultSettings()
	}

	switch key {
	case "nDisplayUnit":
		s.settings.DisplayUnit = value
	case "digits":
		s.settings.Digits = value
	case "nCoinControlMode":
		s.settings.CoinControlMode = value
	case "nCoinControlSortColumn":
		s.settings.CoinControlSortColumn = value
	case "nCoinControlSortOrder":
		s.settings.CoinControlSortOrder = value
	case "transactionDate":
		s.settings.TransactionDate = value
	case "transactionType":
		s.settings.TransactionType = value
	case "nFeeRadio":
		s.settings.FeeRadio = value
	case "nCustomFeeRadio":
		s.settings.CustomFeeRadio = value
	case "nSmartFeeSliderPosition":
		s.settings.SmartFeeSliderPos = value
	default:
		return fmt.Errorf("unknown integer setting: %s", key)
	}

	return s.saveLocked()
}

// SetInt64 sets a 64-bit integer setting value and saves
func (s *SettingsService) SetInt64(key string, value int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.settings == nil {
		s.settings = NewDefaultSettings()
	}

	switch key {
	case "nStakeSplitThreshold":
		s.settings.StakeSplitThreshold = value
	case "nTransactionFee":
		s.settings.TransactionFee = value
	case "transactionMinAmount":
		s.settings.TransactionMinAmount = value
	case "nDisplayUnit":
		s.settings.DisplayUnit = int(value)
	case "digits":
		s.settings.Digits = int(value)
	default:
		return fmt.Errorf("unknown int64 setting: %s", key)
	}

	return s.saveLocked()
}

// SetString sets a string setting value and saves
func (s *SettingsService) SetString(key string, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.settings == nil {
		s.settings = NewDefaultSettings()
	}

	switch key {
	case "theme":
		s.settings.Theme = value
	case "language":
		s.settings.Language = value
	case "strThirdPartyTxUrls":
		s.settings.ThirdPartyTxUrls = value
	case "current_receive_address":
		s.settings.CurrentReceiveAddress = value
	case "strDataDir":
		s.settings.DataDir = value
	default:
		return fmt.Errorf("unknown string setting: %s", key)
	}

	return s.saveLocked()
}

// SetWindowState saves the state for a window
func (s *SettingsService) SetWindowState(windowName string, state WindowState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.settings == nil {
		s.settings = NewDefaultSettings()
	}

	if s.settings.WindowGeometry == nil {
		s.settings.WindowGeometry = make(map[string]WindowState)
	}

	s.settings.WindowGeometry[windowName] = state
	return s.saveLocked()
}

// GetSettingsPath returns the path to the settings file
func (s *SettingsService) GetSettingsPath() string {
	return s.path
}

// HasKey checks if a setting key exists
func (s *SettingsService) HasKey(key string) bool {
	// Check all known keys
	knownKeys := []string{
		"fMinimizeToTray", "fMinimizeOnClose", "nDisplayUnit", "theme", "digits",
		"language", "fHideTrayIcon", "fShowMasternodesTab", "strThirdPartyTxUrls",
		"nStakeSplitThreshold",
		"fCoinControlFeatures", "nCoinControlMode", "nCoinControlSortColumn", "nCoinControlSortOrder",
		"transactionDate", "transactionType", "transactionMinAmount", "fHideOrphans", "fHideZeroBalances",
		"fFeeSectionMinimized", "nFeeRadio", "nCustomFeeRadio", "nSmartFeeSliderPosition",
		"nTransactionFee", "fPayOnlyMinFee", "fSendFreeTransactions", "fSubtractFeeFromAmount",
		"current_receive_address", "fRestartRequired", "strDataDir",
	}

	for _, k := range knownKeys {
		if k == key {
			return true
		}
	}
	return false
}
