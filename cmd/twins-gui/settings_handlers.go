package main

import (
	"fmt"
	"runtime"
	"math"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/twins-dev/twins-core/internal/gui/preferences"
)

// === Settings Handler Methods for Wails Frontend ===

// GetPlatform returns the operating system identifier (e.g., "darwin", "linux", "windows").
// Used by the frontend to disable platform-specific features that are not yet implemented.
func (a *App) GetPlatform() string {
	return runtime.GOOS
}

// GetSettings returns all GUI settings
func (a *App) GetSettings() *preferences.GUISettings {
	if a.settingsService == nil {
		return preferences.NewDefaultSettings()
	}
	return a.settingsService.GetAll()
}

// UpdateSetting updates a single setting by key
// Automatically detects the value type and calls the appropriate setter
// Validates against metadata constraints (min/max, deprecated status)
func (a *App) UpdateSetting(key string, value interface{}) error {
	if a.settingsService == nil {
		return fmt.Errorf("settings service not initialized")
	}

	// Check if setting exists in metadata
	metadata, ok := settingsMetadataMap[key]
	if !ok {
		return fmt.Errorf("unknown setting key: %s", key)
	}

	// Reject updates to deprecated settings
	if metadata.Deprecated {
		return fmt.Errorf("setting %s is deprecated: %s", key, metadata.DeprecatedMsg)
	}

	var saveErr error
	switch v := value.(type) {
	case bool:
		saveErr = a.settingsService.SetBool(key, v)
	case int:
		// Validate against min/max constraints
		if err := validateNumericRange(key, int64(v), metadata); err != nil {
			return err
		}
		saveErr = a.settingsService.SetInt(key, v)
	case int64:
		if err := validateNumericRange(key, v, metadata); err != nil {
			return err
		}
		saveErr = a.settingsService.SetInt64(key, v)
	case float64:
		// JSON numbers come as float64, convert to int64 with overflow check
		if v > float64(math.MaxInt64) || v < float64(math.MinInt64) {
			return fmt.Errorf("value %f out of range for int64", v)
		}
		intVal := int64(v)
		if err := validateNumericRange(key, intVal, metadata); err != nil {
			return err
		}
		saveErr = a.settingsService.SetInt64(key, intVal)
	case string:
		// Validate specific string settings
		if err := validateStringSetting(key, v); err != nil {
			return err
		}
		saveErr = a.settingsService.SetString(key, v)
	default:
		return fmt.Errorf("unsupported value type: %T", value)
	}

	if saveErr != nil {
		return saveErr
	}

	// Push wallet-related settings to the wallet layer so they take effect immediately.
	a.applyWalletSetting(key)

	return nil
}

// validateStringSetting validates string settings that require format checking
func validateStringSetting(key, value string) error {
	switch key {
	case "strThirdPartyTxUrls":
		return validateThirdPartyURLs(value)
	default:
		return nil // No special validation for other strings
	}
}

// validateThirdPartyURLs validates third-party transaction URL templates
func validateThirdPartyURLs(urls string) error {
	if urls == "" {
		return nil // Empty is valid
	}

	// Split by pipe separator
	parts := strings.Split(urls, "|")
	for _, urlTemplate := range parts {
		urlTemplate = strings.TrimSpace(urlTemplate)
		if urlTemplate == "" {
			continue
		}

		// Check for required placeholder
		if !strings.Contains(urlTemplate, "%s") {
			return fmt.Errorf("URL template must contain %%s placeholder for transaction ID: %s", urlTemplate)
		}

		// Validate scheme (must be http or https)
		if !strings.HasPrefix(urlTemplate, "http://") && !strings.HasPrefix(urlTemplate, "https://") {
			return fmt.Errorf("URL must use http or https scheme: %s", urlTemplate)
		}
	}

	return nil
}

// validateNumericRange checks if a numeric value is within metadata-defined constraints
func validateNumericRange(key string, value int64, metadata SettingMetadata) error {
	if metadata.MinValue != nil {
		var minVal int64
		switch m := metadata.MinValue.(type) {
		case int:
			minVal = int64(m)
		case int64:
			minVal = m
		case float64:
			minVal = int64(m)
		}
		if value < minVal {
			return fmt.Errorf("value %d below minimum %d for %s", value, minVal, key)
		}
	}
	if metadata.MaxValue != nil {
		var maxVal int64
		switch m := metadata.MaxValue.(type) {
		case int:
			maxVal = int64(m)
		case int64:
			maxVal = m
		case float64:
			maxVal = int64(m)
		}
		if value > maxVal {
			return fmt.Errorf("value %d exceeds maximum %d for %s", value, maxVal, key)
		}
	}
	return nil
}

// UpdateSettings updates multiple settings at once
// Uses two-phase commit: validates all updates first, then applies them
// This prevents partial updates when one validation fails
func (a *App) UpdateSettings(updates map[string]interface{}) error {
	if a.settingsService == nil {
		return fmt.Errorf("settings service not initialized")
	}

	// Phase 1: Validate all updates before applying any
	for key, value := range updates {
		if err := a.validateSettingUpdate(key, value); err != nil {
			return fmt.Errorf("validation failed for %s: %w", key, err)
		}
	}

	// Phase 2: Apply all updates (validation passed)
	for key, value := range updates {
		if err := a.UpdateSetting(key, value); err != nil {
			// This should not happen if validation passed, but handle gracefully
			return fmt.Errorf("failed to update %s: %w", key, err)
		}
	}
	return nil
}

// validateSettingUpdate validates a setting update without applying it
func (a *App) validateSettingUpdate(key string, value interface{}) error {
	// Check if setting exists in metadata
	metadata, ok := settingsMetadataMap[key]
	if !ok {
		return fmt.Errorf("unknown setting key: %s", key)
	}

	// Reject updates to deprecated settings
	if metadata.Deprecated {
		return fmt.Errorf("setting is deprecated: %s", metadata.DeprecatedMsg)
	}

	// Validate by type
	switch v := value.(type) {
	case int:
		return validateNumericRange(key, int64(v), metadata)
	case int64:
		return validateNumericRange(key, v, metadata)
	case float64:
		if v > float64(math.MaxInt64) || v < float64(math.MinInt64) {
			return fmt.Errorf("value %f out of range for int64", v)
		}
		return validateNumericRange(key, int64(v), metadata)
	case bool:
		return nil // Boolean values don't need validation
	case string:
		return validateStringSetting(key, v)
	default:
		return fmt.Errorf("unsupported value type: %T", value)
	}
}

// ResetSettings resets all settings to defaults
func (a *App) ResetSettings() error {
	if a.settingsService == nil {
		return fmt.Errorf("settings service not initialized")
	}
	return a.settingsService.Reset()
}

// SaveWindowGeometry saves the current window position and size
func (a *App) SaveWindowGeometry(windowName string, x, y, width, height int, maximized bool) error {
	if a.settingsService == nil {
		return fmt.Errorf("settings service not initialized")
	}

	state := preferences.WindowState{
		X:         x,
		Y:         y,
		Width:     width,
		Height:    height,
		Maximized: maximized,
	}
	return a.settingsService.SetWindowState(windowName, state)
}

// GetWindowGeometry retrieves saved window position and size
func (a *App) GetWindowGeometry(windowName string) map[string]interface{} {
	if a.settingsService == nil {
		return nil
	}

	state := a.settingsService.GetWindowState(windowName)
	if state.Width == 0 && state.Height == 0 {
		return nil // No saved state
	}

	return map[string]interface{}{
		"x":         state.X,
		"y":         state.Y,
		"width":     state.Width,
		"height":    state.Height,
		"maximized": state.Maximized,
	}
}

// GetSettingBool returns a boolean setting
func (a *App) GetSettingBool(key string) bool {
	if a.settingsService == nil {
		return false
	}
	return a.settingsService.GetBool(key)
}

// GetSettingInt returns an integer setting
func (a *App) GetSettingInt(key string) int {
	if a.settingsService == nil {
		return 0
	}
	return a.settingsService.GetInt(key)
}

// GetSettingInt64 returns a 64-bit integer setting
func (a *App) GetSettingInt64(key string) int64 {
	if a.settingsService == nil {
		return 0
	}
	return a.settingsService.GetInt64(key)
}

// GetSettingString returns a string setting
func (a *App) GetSettingString(key string) string {
	if a.settingsService == nil {
		return ""
	}
	return a.settingsService.GetString(key)
}

// SetSettingBool sets a boolean setting
func (a *App) SetSettingBool(key string, value bool) error {
	if a.settingsService == nil {
		return fmt.Errorf("settings service not initialized")
	}
	return a.settingsService.SetBool(key, value)
}

// SetSettingInt sets an integer setting
func (a *App) SetSettingInt(key string, value int) error {
	if a.settingsService == nil {
		return fmt.Errorf("settings service not initialized")
	}
	return a.settingsService.SetInt(key, value)
}

// SetSettingInt64 sets a 64-bit integer setting
func (a *App) SetSettingInt64(key string, value int64) error {
	if a.settingsService == nil {
		return fmt.Errorf("settings service not initialized")
	}
	return a.settingsService.SetInt64(key, value)
}

// SetSettingString sets a string setting
func (a *App) SetSettingString(key string, value string) error {
	if a.settingsService == nil {
		return fmt.Errorf("settings service not initialized")
	}
	return a.settingsService.SetString(key, value)
}

// GetSettingsPath returns the path to the settings file
func (a *App) GetSettingsPath() string {
	if a.settingsService == nil {
		return ""
	}
	return a.settingsService.GetSettingsPath()
}

// HasSetting checks if a setting key exists
func (a *App) HasSetting(key string) bool {
	if a.settingsService == nil {
		return false
	}
	return a.settingsService.HasKey(key)
}

// applyWalletSetting pushes a wallet-related GUI setting to the wallet layer
// so it takes effect immediately without requiring a restart.
func (a *App) applyWalletSetting(key string) {
	a.componentsMu.RLock()
	w := a.wallet
	a.componentsMu.RUnlock()
	if w == nil || a.settingsService == nil {
		return
	}

	switch key {
	case "nStakeSplitThreshold":
		threshold := a.settingsService.GetInt64(key)
		if err := w.SetStakeSplitThreshold(threshold); err != nil {
			logrus.WithError(err).Warn("Failed to apply stake split threshold to wallet")
		}
	}
}
