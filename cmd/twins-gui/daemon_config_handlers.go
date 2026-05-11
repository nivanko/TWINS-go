package main

import (
	"fmt"

	"github.com/twins-dev/twins-core/internal/config"
)

// === Daemon Configuration Handler Methods for Wails Frontend ===
//
// These methods expose the unified ConfigManager (twinsd.yml) to the frontend,
// enabling dynamic rendering of daemon settings and hot-reload of changes.
//
// configManager is written once during initializeFullDaemon (goroutine) and read
// from these Wails handlers. All reads snapshot the pointer under componentsMu
// to avoid a data race.

// getConfigManager returns a snapshot of the configManager pointer under componentsMu.
func (a *App) getConfigManager() *config.ConfigManager {
	a.componentsMu.RLock()
	cm := a.configManager
	a.componentsMu.RUnlock()
	return cm
}

// GetDaemonConfigMetadata returns metadata for all registered daemon settings,
// ordered by category. The frontend uses this to dynamically render settings tabs.
func (a *App) GetDaemonConfigMetadata() []config.SettingMeta {
	cm := a.getConfigManager()
	if cm == nil {
		return nil
	}
	return cm.GetAllMetadata()
}

// GetDaemonConfigValues returns the current value, CLI lock state, and pending restart
// flag for each daemon setting. Used to populate the settings dialog.
func (a *App) GetDaemonConfigValues() map[string]interface{} {
	cm := a.getConfigManager()
	if cm == nil {
		return nil
	}
	return cm.GetAllValues()
}

// GetDaemonConfigBool returns the current value of a boolean daemon config key
// from the unified ConfigManager (twinsd.yml). Use this — not GetSettingBool —
// for any key registered in internal/config/registry.go (e.g. masternode.debug,
// staking.enabled). GetSettingBool only reaches the GUI's local SettingsService
// store and silently returns false for daemon keys, which is a footgun.
func (a *App) GetDaemonConfigBool(key string) bool {
	cm := a.getConfigManager()
	if cm == nil {
		return false
	}
	return cm.GetBool(key)
}

// SetDaemonConfigValue sets a single daemon config value by key.
// Validates the value, persists to twinsd.yml, and triggers subscriber callbacks
// for hot-reloadable settings (staking, fees, logging).
// Returns error if the setting is locked by a CLI flag or validation fails.
func (a *App) SetDaemonConfigValue(key string, value interface{}) error {
	cm := a.getConfigManager()
	if cm == nil {
		return fmt.Errorf("configuration manager not initialized")
	}
	return cm.Set(key, value)
}

// GetDaemonConfigCategories returns the ordered list of setting category names.
// Each category maps to a tab in the settings dialog.
func (a *App) GetDaemonConfigCategories() []string {
	cm := a.getConfigManager()
	if cm == nil {
		return nil
	}
	return cm.GetCategories()
}

// HasDaemonPendingRestart returns true if any changed daemon setting requires
// an application restart to take effect.
func (a *App) HasDaemonPendingRestart() bool {
	cm := a.getConfigManager()
	if cm == nil {
		return false
	}
	return cm.HasPendingRestart()
}

// GetDaemonPendingRestartKeys returns the keys of daemon settings that were
// changed and require a restart to take effect.
func (a *App) GetDaemonPendingRestartKeys() []string {
	cm := a.getConfigManager()
	if cm == nil {
		return nil
	}
	return cm.GetPendingRestartKeys()
}
