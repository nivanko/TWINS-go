package main

// SettingMetadata contains metadata about a setting
type SettingMetadata struct {
	Key             string      `json:"key"`
	Tab             string      `json:"tab"`             // "main", "wallet", "network", "window", "display"
	RequiresRestart bool        `json:"requiresRestart"` // True if changing requires app restart
	OverriddenByCLI bool        `json:"overriddenByCLI"` // True if setting was set via CLI flag
	CLIFlagName     string      `json:"cliFlagName,omitempty"`
	DefaultValue    interface{} `json:"defaultValue,omitempty"`
	MinValue        interface{} `json:"minValue,omitempty"`
	MaxValue        interface{} `json:"maxValue,omitempty"`
	Deprecated      bool        `json:"deprecated,omitempty"`
	DeprecatedMsg   string      `json:"deprecatedMsg,omitempty"`
}

// settingsMetadataMap contains metadata for all settings
// Key is the JSON field name from GUISettings struct
var settingsMetadataMap = map[string]SettingMetadata{
	// === Main Tab ===
	// NOTE: nDatabaseCache and nThreadsScriptVerif moved to ConfigManager (twinsd.yml)
	"language": {
		Key:             "language",
		Tab:             "main",
		RequiresRestart: false, // Hot reload supported in frontend
		DefaultValue:    "",
	},
	"theme": {
		Key:             "theme",
		Tab:             "main",
		RequiresRestart: true,
		DefaultValue:    "dark",
	},

	// === Wallet Tab ===
	"nStakeSplitThreshold": {
		Key:             "nStakeSplitThreshold",
		Tab:             "wallet",
		RequiresRestart: false,
		DefaultValue:    int64(200000 * 100000000), // 200000 TWINS in satoshis
		MinValue:        int64(0),
		MaxValue:        int64(999999 * 100000000),
	},
	"fCoinControlFeatures": {
		Key:             "fCoinControlFeatures",
		Tab:             "wallet",
		RequiresRestart: false,
		DefaultValue:    false,
	},
	"fShowMasternodesTab": {
		Key:             "fShowMasternodesTab",
		Tab:             "wallet",
		RequiresRestart: true,
		DefaultValue:    true,
	},

	// === Window Tab ===
	"fMinimizeToTray": {
		Key:             "fMinimizeToTray",
		Tab:             "window",
		RequiresRestart: false,
		DefaultValue:    false,
	},
	"fMinimizeOnClose": {
		Key:             "fMinimizeOnClose",
		Tab:             "window",
		RequiresRestart: false,
		DefaultValue:    false,
	},
	"fHideTrayIcon": {
		Key:             "fHideTrayIcon",
		Tab:             "window",
		RequiresRestart: false,
		DefaultValue:    false,
	},

	// === Display Tab ===
	"nDisplayUnit": {
		Key:             "nDisplayUnit",
		Tab:             "display",
		RequiresRestart: false,
		DefaultValue:    0, // 0=TWINS, 1=mTWINS, 2=uTWINS
		MinValue:        0,
		MaxValue:        2,
	},
	"digits": {
		Key:             "digits",
		Tab:             "display",
		RequiresRestart: false,
		DefaultValue:    8,
		MinValue:        2,
		MaxValue:        8,
	},
	"strThirdPartyTxUrls": {
		Key:             "strThirdPartyTxUrls",
		Tab:             "display",
		RequiresRestart: false,
		DefaultValue:    "",
	},
	"fHideOrphans": {
		Key:             "fHideOrphans",
		Tab:             "display",
		RequiresRestart: false,
		DefaultValue:    false,
	},
	"fHideZeroBalances": {
		Key:             "fHideZeroBalances",
		Tab:             "display",
		RequiresRestart: false,
		DefaultValue:    false,
	},
}

// GetSettingMetadata returns metadata for a single setting
func (a *App) GetSettingMetadata(key string) *SettingMetadata {
	if metadata, ok := settingsMetadataMap[key]; ok {
		// Check if overridden by CLI
		metadata.OverriddenByCLI = a.isSettingOverriddenByCLI(key)
		if metadata.OverriddenByCLI {
			metadata.CLIFlagName = getClieFlagName(key)
		}
		return &metadata
	}
	return nil
}

// GetAllSettingsMetadata returns metadata for all settings
func (a *App) GetAllSettingsMetadata() map[string]SettingMetadata {
	result := make(map[string]SettingMetadata, len(settingsMetadataMap))
	for key, metadata := range settingsMetadataMap {
		metadata.OverriddenByCLI = a.isSettingOverriddenByCLI(key)
		if metadata.OverriddenByCLI {
			metadata.CLIFlagName = getClieFlagName(key)
		}
		result[key] = metadata
	}
	return result
}

// isSettingOverriddenByCLI checks if a setting was overridden by CLI flags
func (a *App) isSettingOverriddenByCLI(key string) bool {
	if a.guiConfig == nil {
		return false
	}

	// Map settings to CLI flag names and check if they were explicitly set
	switch key {
	case "language":
		return a.guiConfig.Language != ""
	// Add more mappings as CLI flags are added
	// For now, most settings are not overridable via CLI in the Go version
	default:
		return false
	}
}

// getClieFlagName returns the CLI flag name for a setting
func getClieFlagName(key string) string {
	flagMap := map[string]string{
		"language":   "-lang",
		"strDataDir": "-datadir",
	}
	if flag, ok := flagMap[key]; ok {
		return flag
	}
	return ""
}

// GetRestartRequiredSettings returns a list of settings that require restart
func (a *App) GetRestartRequiredSettings() []string {
	var settings []string
	for key, metadata := range settingsMetadataMap {
		if metadata.RequiresRestart {
			settings = append(settings, key)
		}
	}
	return settings
}
