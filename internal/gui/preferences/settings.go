package preferences

// GUISettings holds all GUI preferences (57+ settings)
// JSON tags match Qt QSettings keys for documentation reference
type GUISettings struct {
	// === Window/UI Settings ===
	MinimizeToTray     bool   `json:"fMinimizeToTray"`
	MinimizeOnClose    bool   `json:"fMinimizeOnClose"`
	DisplayUnit        int    `json:"nDisplayUnit"`        // 0=TWINS, 1=mTWINS, 2=uTWINS
	Theme              string `json:"theme"`               // "light", "dark", "system"
	Digits             int    `json:"digits"`              // Decimal places to display (default: 8)
	Language           string `json:"language"`            // Language code (e.g., "en", "de", "")
	HideTrayIcon       bool   `json:"fHideTrayIcon"`       // Hide system tray icon
	ShowMasternodesTab bool   `json:"fShowMasternodesTab"` // Display masternodes tab
	ThirdPartyTxUrls   string `json:"strThirdPartyTxUrls"` // Third-party transaction explorer URLs

	// === Window Geometry ===
	// Stored as nested map: windowName -> WindowState
	WindowGeometry map[string]WindowState `json:"windowGeometry"`

	// === Wallet Settings ===
	StakeSplitThreshold int64 `json:"nStakeSplitThreshold"` // Stake split threshold in satoshis

	// === Coin Control Settings ===
	CoinControlFeatures   bool `json:"fCoinControlFeatures"`   // Enable coin control dialog
	CoinControlMode       int  `json:"nCoinControlMode"`       // 0=tree view, 1=list view
	CoinControlSortColumn int  `json:"nCoinControlSortColumn"` // Sort column index
	CoinControlSortOrder  int  `json:"nCoinControlSortOrder"`  // 0=ascending, 1=descending

	// === Transaction View Settings ===
	TransactionDate     int  `json:"transactionDate"`     // Transaction filter: date range
	TransactionType     int  `json:"transactionType"`     // Transaction filter: type
	TransactionMinAmount int64 `json:"transactionMinAmount"` // Minimum amount filter
	HideOrphans         bool `json:"fHideOrphans"`        // Hide orphaned transactions
	HideZeroBalances    bool `json:"fHideZeroBalances"`   // Hide zero balance accounts

	// === Send Coins Dialog Settings ===
	FeeSectionMinimized  bool  `json:"fFeeSectionMinimized"`  // Collapse fee section
	FeeRadio             int   `json:"nFeeRadio"`             // Fee mode: 0=recommended, 1=custom
	CustomFeeRadio       int   `json:"nCustomFeeRadio"`       // Custom fee: 0=per kB, 1=total
	SmartFeeSliderPos    int   `json:"nSmartFeeSliderPosition"` // Smart fee slider position
	TransactionFee       int64 `json:"nTransactionFee"`       // Transaction fee in satoshis
	PayOnlyMinFee        bool  `json:"fPayOnlyMinFee"`        // Pay only minimum fee
	SendFreeTransactions bool  `json:"fSendFreeTransactions"` // Attempt free transactions
	SubtractFeeFromAmt   bool  `json:"fSubtractFeeFromAmount"` // Subtract fee from amount

	// === Receive Coins Dialog ===
	CurrentReceiveAddress string `json:"current_receive_address"` // Last used receiving address

	// === Misc Settings ===
	RestartRequired bool   `json:"fRestartRequired"` // Flag indicating restart needed
	DataDir         string `json:"strDataDir"`       // Data directory path (override)

	// === Internal Metadata ===
	Version      int    `json:"_version"`      // Settings schema version
	LastModified string `json:"_lastModified"` // ISO timestamp of last modification
}

// WindowState stores position and size for a window
type WindowState struct {
	X         int  `json:"x"`
	Y         int  `json:"y"`
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	Maximized bool `json:"maximized"`
}

// DisplayUnit constants matching Qt BitcoinUnits enum
const (
	DisplayUnitTWINS  = 0 // Full TWINS (8 decimals)
	DisplayUnitMTWINS = 1 // Milli-TWINS (5 decimals)
	DisplayUnitUTWINS = 2 // Micro-TWINS (2 decimals)
)

// CoinControlMode constants
const (
	CoinControlModeTree = 0
	CoinControlModeList = 1
)

// FeeMode constants
const (
	FeeModeRecommended = 0
	FeeModeCustom      = 1
)

// CustomFeeMode constants
const (
	CustomFeePerKB  = 0
	CustomFeeTotal  = 1
)

// SortOrder constants matching Qt::SortOrder
const (
	SortOrderAscending  = 0
	SortOrderDescending = 1
)
