package preferences

// NewDefaultSettings creates a GUISettings with all default values
// These defaults match the legacy C++ Qt OptionsModel defaults
func NewDefaultSettings() *GUISettings {
	return &GUISettings{
		// === Window/UI Settings ===
		MinimizeToTray:     false,
		MinimizeOnClose:    false,
		DisplayUnit:        DisplayUnitTWINS, // Full TWINS
		Theme:              "dark",           // Dark theme (default)
		Digits:             8,                // Full precision
		Language:           "",               // System default
		HideTrayIcon:       false,
		ShowMasternodesTab: true,
		ThirdPartyTxUrls:   "",

		// === Window Geometry ===
		WindowGeometry: make(map[string]WindowState),

		// === Wallet Settings ===
		StakeSplitThreshold: 200000000000, // 2000 TWINS in satoshis
		AutoCombineRewards:  true,

		// === Coin Control Settings ===
		CoinControlFeatures:   false,
		CoinControlMode:       CoinControlModeTree,
		CoinControlSortColumn: 0,
		CoinControlSortOrder:  SortOrderAscending,

		// === Transaction View Settings ===
		TransactionDate:      0, // All dates
		TransactionType:      0, // All types
		TransactionMinAmount: 0,
		HideOrphans:          true,
		HideZeroBalances:     true,

		// === Send Coins Dialog Settings ===
		FeeSectionMinimized:  true,
		FeeRadio:             FeeModeRecommended,
		CustomFeeRadio:       CustomFeePerKB,
		SmartFeeSliderPos:    0,
		TransactionFee:       10000, // 0.0001 TWINS default fee
		PayOnlyMinFee:        false,
		SendFreeTransactions: false,
		SubtractFeeFromAmt:   false,

		// === Receive Coins Dialog ===
		CurrentReceiveAddress: "",

		// === Misc Settings ===
		RestartRequired: false,
		DataDir:         "", // Use system default

		// === Internal Metadata ===
		Version:      1,
		LastModified: "",
	}
}

// DefaultDatabaseCache returns the default database cache size in MB
func DefaultDatabaseCache() int {
	return 1024
}

// DefaultTransactionFee returns the default transaction fee in satoshis
func DefaultTransactionFee() int64 {
	return 10000
}

// DefaultStakeSplitThreshold returns the default stake split threshold in satoshis
func DefaultStakeSplitThreshold() int64 {
	return 200000000000 // 2000 TWINS
}
