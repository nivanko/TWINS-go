package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/twins-dev/twins-core/internal/blockchain"
	"github.com/twins-dev/twins-core/internal/cli"
	"github.com/twins-dev/twins-core/internal/config"
	"github.com/twins-dev/twins-core/internal/daemon"
	"github.com/twins-dev/twins-core/internal/startup"
	configservice "github.com/twins-dev/twins-core/internal/gui/config"
	"github.com/twins-dev/twins-core/internal/gui/core"
	"github.com/twins-dev/twins-core/internal/gui/preferences"
	"github.com/twins-dev/twins-core/internal/gui/shutdown"
	"github.com/twins-dev/twins-core/internal/gui/tests/mocks"
	"github.com/twins-dev/twins-core/internal/gui/window"
	"github.com/twins-dev/twins-core/internal/gui/initialization"
	"github.com/twins-dev/twins-core/internal/masternode"
	"github.com/twins-dev/twins-core/internal/p2p"
	"github.com/twins-dev/twins-core/internal/rpc"
	"github.com/twins-dev/twins-core/internal/wallet"
	"github.com/twins-dev/twins-core/pkg/types"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	defaultP2PListenAddr = "0.0.0.0:37817" // Standard TWINS P2P port
	defaultP2PMaxPeers   = 125             // Max P2P connections
	defaultRPCPort       = types.DefaultRPCPort // Standard TWINS RPC port
)

// initPhaseDescriptions maps startup phase names to user-friendly splash screen messages.
var initPhaseDescriptions = map[string]string{
	"core":       "Initializing core components...",
	"storage":    "Opening blockchain database...",
	"genesis":    "Verifying genesis block...",
	"consensus":  "Initializing consensus engine...",
	"blockchain": "Loading blockchain state...",
	"mempool":    "Initializing transaction pool...",
	"spork":      "Loading network parameters...",
	"masternode": "Initializing masternode manager...",
	"validation": "Validating chain integrity...",
	"wallet":     "Loading wallet...",
	"mncache":    "Loading masternode cache...",
	"parallel":   "Starting mempool and P2P...",
	"mnconf":        "Loading masternode configuration...",
	"coreservices":  "Starting core services...",
}

// initErrorCategory defines error classification for the initialization error dialog.
type initErrorCategory struct {
	title    string
	keywords []string
	template string
}

var initErrorCategories = []initErrorCategory{
	{
		title:    "Database Error",
		keywords: []string{"storage", "database"},
		template: "Failed to open blockchain database.\n\nThis may be caused by:\n- Insufficient disk space\n- File permission issues\n- Database corruption\n\nError: %v\n\nTry:\n1. Check disk space in data directory\n2. Verify file permissions\n3. Delete blockchain.db and resync",
	},
	{
		title:    "Genesis Block Error",
		keywords: []string{"genesis"},
		template: "Failed to verify genesis block.\n\nThis may indicate:\n- Wrong network selected\n- Corrupted blockchain data\n\nError: %v\n\nTry deleting the data directory and restarting.",
	},
	{
		title:    "Chain Validation Error",
		keywords: []string{"validation", "chain"},
		template: "Blockchain validation failed.\n\nThe blockchain data may be corrupted or on a wrong fork.\n\nError: %v\n\nTry:\n1. Restart the application (may auto-recover)\n2. Delete blockchain.db and resync from network",
	},
	{
		title:    "Consensus Engine Error",
		keywords: []string{"consensus"},
		template: "Failed to start consensus engine.\n\nError: %v\n\nPlease report this issue.",
	},
}

// App struct with services
type App struct {
	ctx             context.Context
	coreClient      core.CoreClient        // Core blockchain client (Mock or Real)
	coreComponents  *daemon.CoreComponents // Full daemon components (nil if using mock) - legacy bridge
	node            *daemon.Node           // Unified node lifecycle (replaces coreComponents for init)
	wallet          *wallet.Wallet         // HD wallet for RPC and staking
	masternodeConf  *masternode.MasternodeConfFile // Masternode config file manager
	componentsMu    sync.RWMutex           // Mutex for thread-safe component access
	initStarting    atomic.Bool            // Prevents concurrent initializeFullDaemon calls
	initCompleted   atomic.Bool            // True after successful daemon initialization
	initError       string                 // Non-empty if initialization failed (protected by componentsMu)
	p2pStarting     atomic.Bool            // Prevents concurrent StartP2P calls
	monitorStarted  atomic.Bool            // Prevents multiple monitor goroutines
	rpcServer       *rpc.Server            // RPC server for twins-cli compatibility
	rpcStarted      atomic.Bool            // Prevents multiple RPC server starts
	initService     *initialization.Service
	configService   *configservice.Service
	configManager   *config.ConfigManager  // Unified daemon config (twinsd.yml)
	windowManager   *window.Manager
	prefsService    *preferences.Service
	settingsService *preferences.SettingsService // GUI settings (57+ options)
	shutdownManager *shutdown.Manager
	dataDir            string         // Current data directory
	guiConfig          *cli.GUIConfig // Parsed command-line flags
	trafficCollector   *TrafficCollector // Background network traffic sampler
	pendingRepair      *RepairPending // Repair action detected on startup (nil if none)
	contactsStore      *ContactsStore // Sending address book contacts
	trayManager        *TrayManager   // System tray icon manager
	trayQuitRequested  atomic.Bool    // Skips minimize behaviors in OnBeforeClose when Quit chosen from tray
}

// NewApp creates a new App application struct with parsed CLI config
func NewApp(guiConfig *cli.GUIConfig) *App {
	return &App{
		guiConfig: guiConfig,
	}
}

// GetGUIConfig returns the GUI configuration (exposed to frontend)
func (a *App) GetGUIConfig() map[string]interface{} {
	if a.guiConfig == nil {
		return nil
	}
	return a.guiConfig.ToMap()
}

// DataDirectoryInfo contains the current data directory and its source
type DataDirectoryInfo struct {
	Path   string `json:"path"`
	Source string `json:"source"` // "cli", "preference", or "default"
}

// GetDataDirectoryInfo returns the current data directory path and its source
func (a *App) GetDataDirectoryInfo() *DataDirectoryInfo {
	// Thread-safe read of all relevant fields
	a.componentsMu.RLock()
	dataDir := a.dataDir
	guiConfig := a.guiConfig
	prefsService := a.prefsService
	a.componentsMu.RUnlock()

	// Determine source based on priority: CLI > preference > default
	var source string
	if guiConfig != nil && guiConfig.DataDir != "" {
		source = "cli"
	} else if prefsService != nil && prefsService.GetDataDirectory() != "" {
		source = "preference"
	} else {
		source = "default"
	}

	return &DataDirectoryInfo{
		Path:   dataDir,
		Source: source,
	}
}

// startup is called when the app starts
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	fmt.Println("TWINS Wallet starting...")

	// Initialize preferences service first (bootstrap prefs like data directory)
	prefsService, err := preferences.NewService()
	if err != nil {
		fmt.Printf("Failed to initialize preferences service: %v\n", err)
	}
	a.prefsService = prefsService

	// Initialize settings service (full GUI settings - 57+ options)
	settingsService, err := preferences.NewSettingsService()
	if err != nil {
		fmt.Printf("Failed to initialize settings service: %v\n", err)
	}
	a.settingsService = settingsService

	// Log settings and CLI config if -dev-fulllogs flag or TWINS_DEV_FULL_LOGS env var is set
	showFullLogs := (a.guiConfig != nil && a.guiConfig.DevFullLogs) || os.Getenv("TWINS_DEV_FULL_LOGS") == "1"
	a.logGUIConfig(showFullLogs)
	a.logSettings(showFullLogs)

	// Resolve data directory: CLI flag > preferences > OS default
	var prefsDataDir string
	if a.prefsService != nil {
		prefsDataDir = a.prefsService.GetDataDirectory()
	}
	cliDataDir := ""
	if a.guiConfig != nil {
		cliDataDir = a.guiConfig.DataDir
	}
	a.dataDir = cli.ResolveDataDir(cliDataDir, prefsDataDir)

	// Initialize contacts store (sending address book) - no wallet dependency
	a.contactsStore = NewContactsStore(a.dataDir)
	if err := a.contactsStore.Load(); err != nil {
		fmt.Printf("Warning: failed to load contacts: %v\n", err)
	}

	// Initialize services first (needed for checking first run)
	a.initService = initialization.NewService()

	// Initialize window manager
	a.windowManager = window.NewManager(ctx)

	// On Windows, Wails window dimensions include the title bar, so the content
	// area is smaller than the requested height. Add extra pixels to compensate.
	if extra := splashHeightExtra(); extra > 0 {
		a.windowManager.SetSplashHeightExtra(extra)
	}

	// Set custom window title if provided via -windowtitle flag
	if a.windowManager != nil && a.guiConfig != nil && a.guiConfig.WindowTitle != "" {
		a.windowManager.SetCustomTitle(a.guiConfig.GetWindowTitle())
	}

	// Initialize shutdown manager
	a.shutdownManager = shutdown.NewManager(ctx)

	// Prepare window size based on first run and CLI flags
	result := a.CheckFirstRun()
	isFirstRun, ok := result["isFirstRun"].(bool)
	if !ok {
		fmt.Printf("Warning: isFirstRun type assertion failed, defaulting to false\n")
		isFirstRun = false
	}

	// Check CLI flags for -choosedatadir (force intro even if not first run)
	forceChooseDataDir := a.guiConfig != nil && a.guiConfig.ChooseDataDir

	if isFirstRun || forceChooseDataDir {
		// First run or -choosedatadir flag - prepare intro window size (but keep hidden)
		if forceChooseDataDir {
			fmt.Println("-choosedatadir flag set - forcing data directory selection")
		} else {
			fmt.Println("First run detected - preparing intro window")
		}
		if err := a.windowManager.PrepareWindowState(window.StateIntro); err != nil {
			fmt.Printf("Error preparing intro window: %v\n", err)
		}
	} else {
		// Check CLI flags for -nosplash
		showSplash := a.guiConfig == nil || a.guiConfig.ShowSplash

		if showSplash {
			// Not first run - prepare splash window size (but keep hidden)
			fmt.Println("Returning user - preparing splash window")
			if err := a.windowManager.PrepareWindowState(window.StateSplash); err != nil {
				fmt.Printf("Error preparing splash window: %v\n", err)
			}
		} else {
			// -nosplash flag - skip splash, go directly to main window
			fmt.Println("-nosplash flag set - skipping splash screen")
			if err := a.windowManager.PrepareWindowState(window.StateMain); err != nil {
				fmt.Printf("Error preparing main window: %v\n", err)
			}
		}
	}

	// Handle -min flag (start minimized)
	if a.guiConfig != nil && a.guiConfig.StartMinimized {
		fmt.Println("-min flag set - window will start minimized")
		// Note: Actual minimization happens after window is shown
		// Frontend should handle this via GetGUIConfig()
	}
}

// handleCoreEvents listens to core events and forwards them to the frontend
// This runs in a goroutine and continues until the core stops
func (a *App) handleCoreEvents() {
	fmt.Println("Starting core event listener...")

	events := a.coreClient.Events()
	for event := range events {
		eventType := event.EventType()

		// Log event for debugging
		fmt.Printf("Core Event: %s at %s\n", eventType, event.Timestamp().Format("15:04:05"))

		// Forward event to frontend based on type
		switch e := event.(type) {
		case *core.BalanceChangedEvent:
			// Forward balance changes to frontend
			runtime.EventsEmit(a.ctx, "balance:changed", e.Balance)

		case *core.TransactionReceivedEvent:
			// Forward new transactions to frontend
			runtime.EventsEmit(a.ctx, "transaction:received", map[string]interface{}{
				"txid":          e.TxID,
				"amount":        e.Amount,
				"confirmations": e.Confirmations,
			})

		case *core.TransactionConfirmedEvent:
			// Forward transaction confirmations to frontend
			runtime.EventsEmit(a.ctx, "transaction:confirmed", map[string]interface{}{
				"txid":          e.TxID,
				"confirmations": e.Confirmations,
			})

		case *core.BlockConnectedEvent:
			// Forward new blocks to frontend
			runtime.EventsEmit(a.ctx, "block:connected", map[string]interface{}{
				"hash":   e.Hash,
				"height": e.Height,
				"size":   e.Size,
			})

		case *core.ChainSyncUpdateEvent:
			// Forward sync updates to frontend
			runtime.EventsEmit(a.ctx, "chain:sync", map[string]interface{}{
				"currentHeight": e.CurrentHeight,
				"targetHeight":  e.TargetHeight,
				"progress":      e.Progress,
			})

		case *core.ConnectionCountChangedEvent:
			// Forward connection count changes
			runtime.EventsEmit(a.ctx, "network:connections", e.Count)

		case *core.StakeRewardEvent:
			// Forward staking rewards
			runtime.EventsEmit(a.ctx, "stake:reward", map[string]interface{}{
				"txid":   e.TxID,
				"amount": e.Amount,
				"height": e.Height,
			})

		case *core.MasternodePaymentReceivedEvent:
			// Forward masternode payments
			runtime.EventsEmit(a.ctx, "masternode:payment", map[string]interface{}{
				"alias":  e.Alias,
				"txid":   e.TxID,
				"amount": e.Amount,
			})

		case *core.ErrorEvent:
			// Forward errors to frontend
			runtime.EventsEmit(a.ctx, "error", map[string]interface{}{
				"error":   e.Error,
				"details": e.Details,
			})

		case *core.WarningEvent:
			// Forward warnings to frontend
			runtime.EventsEmit(a.ctx, "warning", e.Warning)
		}
	}

	fmt.Println("Core event listener stopped")
}

// logGUIConfig logs CLI configuration flags when showFullLogs is enabled.
// This replaces inline CLI logging with a dedicated function.
func (a *App) logGUIConfig(showFullLogs bool) {
	if !showFullLogs {
		return
	}
	if a.guiConfig == nil {
		return
	}

	cfg := a.guiConfig
	fmt.Println()
	fmt.Println()
	fmt.Println("GUI Configuration:")
	fmt.Printf("  DataDir:        %s\n", cfg.DataDir)
	fmt.Printf("  Network:        %s\n", cfg.Network)
	fmt.Printf("  StartMinimized: %v\n", cfg.StartMinimized)
	fmt.Printf("  ShowSplash:     %v\n", cfg.ShowSplash)
	fmt.Printf("  ChooseDataDir:  %v\n", cfg.ChooseDataDir)
	fmt.Printf("  Language:       %s\n", cfg.Language)
	fmt.Printf("  WindowTitle:    %s\n", cfg.WindowTitle)
	fmt.Printf("  ResetSettings:  %v\n", cfg.ResetSettings)
	fmt.Println()
	fmt.Println()
}

// logSettings logs all GUI settings when showFullLogs is enabled.
// This replaces inline settings logging with a dedicated function.
func (a *App) logSettings(showFullLogs bool) {
	if !showFullLogs {
		return
	}
	if a.settingsService == nil {
		return
	}

	settings := a.settingsService.GetAll()
	fmt.Println()
	fmt.Println()
	// Sanitize path to hide username
	settingsPath := a.settingsService.GetSettingsPath()
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(settingsPath, home) {
		settingsPath = "~" + settingsPath[len(home):]
	}
	fmt.Printf("GUI Settings (from: %s):\n", settingsPath)
	fmt.Printf("  MinimizeToTray:       %v\n", settings.MinimizeToTray)
	fmt.Printf("  MinimizeOnClose:      %v\n", settings.MinimizeOnClose)
	fmt.Printf("  HideTrayIcon:         %v\n", settings.HideTrayIcon)
	fmt.Printf("  DisplayUnit:          %d\n", settings.DisplayUnit)
	fmt.Printf("  Theme:                %s\n", settings.Theme)
	fmt.Printf("  Digits:               %d\n", settings.Digits)
	fmt.Printf("  Language:             %s\n", settings.Language)
	fmt.Printf("  ShowMasternodesTab:   %v\n", settings.ShowMasternodesTab)
	fmt.Printf("  ThirdPartyTxUrls:     %s\n", settings.ThirdPartyTxUrls)
	fmt.Printf("  StakeSplitThreshold:  %d\n", settings.StakeSplitThreshold)
	fmt.Printf("  AutoCombineRewards:   %v\n", settings.AutoCombineRewards)
	fmt.Printf("  CoinControlFeatures:  %v\n", settings.CoinControlFeatures)
	fmt.Printf("  CoinControlMode:      %d\n", settings.CoinControlMode)
	fmt.Printf("  CoinControlSortCol:   %d\n", settings.CoinControlSortColumn)
	fmt.Printf("  CoinControlSortOrder: %d\n", settings.CoinControlSortOrder)
	fmt.Printf("  TransactionDate:      %d\n", settings.TransactionDate)
	fmt.Printf("  TransactionType:      %d\n", settings.TransactionType)
	fmt.Printf("  TransactionMinAmount: %d\n", settings.TransactionMinAmount)
	fmt.Printf("  HideOrphans:          %v\n", settings.HideOrphans)
	fmt.Printf("  HideZeroBalances:     %v\n", settings.HideZeroBalances)
	fmt.Printf("  FeeSectionMinimized:  %v\n", settings.FeeSectionMinimized)
	fmt.Printf("  FeeRadio:             %d\n", settings.FeeRadio)
	fmt.Printf("  CustomFeeRadio:       %d\n", settings.CustomFeeRadio)
	fmt.Printf("  SmartFeeSliderPos:    %d\n", settings.SmartFeeSliderPos)
	fmt.Printf("  TransactionFee:       %d\n", settings.TransactionFee)
	fmt.Printf("  PayOnlyMinFee:        %v\n", settings.PayOnlyMinFee)
	fmt.Printf("  SendFreeTransactions: %v\n", settings.SendFreeTransactions)
	fmt.Printf("  SubtractFeeFromAmt:   %v\n", settings.SubtractFeeFromAmt)
	fmt.Printf("  CurrentReceiveAddr:   %s\n", settings.CurrentReceiveAddress)
	fmt.Printf("  RestartRequired:      %v\n", settings.RestartRequired)
	fmt.Printf("  DataDir:              %s\n", settings.DataDir)
	fmt.Printf("  Version:              %d\n", settings.Version)
	fmt.Printf("  LastModified:         %s\n", settings.LastModified)
	fmt.Printf("  WindowGeometry:       %d entries\n", len(settings.WindowGeometry))
	for name, state := range settings.WindowGeometry {
		fmt.Printf("    %s: pos(%d,%d) size(%dx%d) maximized=%v\n",
			name, state.X, state.Y, state.Width, state.Height, state.Maximized)
	}
	fmt.Println()
	fmt.Println()
}

// domReady is called after the front-end resources have been loaded
func (a *App) domReady(ctx context.Context) {
	useMockMode := a.guiConfig != nil && a.guiConfig.DevMockMode
	if useMockMode {
		// Mock mode - create mock client (no fake initialization)
		fmt.Println("Initializing Core Client (DEV MOCK MODE)...")
		a.coreClient = mocks.NewMockCoreClient()
		if err := a.coreClient.Start(a.ctx); err != nil {
			fmt.Printf("Failed to start mock core client: %v\n", err)
		} else {
			fmt.Println("Mock core client started successfully")
		}
	} else {
		// Real mode - initialization triggered by StartInitialization() from frontend
		fmt.Println("Real daemon mode - waiting for frontend StartInitialization()")
	}

	// On Windows, explicitly set ICON_BIG for the taskbar icon.
	// Wails v2 only sets ICON_SMALL (title bar); this fills in ICON_BIG from .exe resources.
	setTaskbarIcon()

	// Initialize system tray icon (requires Wails event loop to be running).
	// On Windows, the tray is completely disabled (shouldStartTray returns false).
	// On macOS/Linux, always start the tray so it can be shown later via SetVisible(true).
	// If fHideTrayIcon is set, immediately swap to a transparent icon.
	a.trayManager = NewTrayManager(a)
	if shouldStartTray() {
		a.trayManager.Start(appIcon)
		if a.settingsService != nil && a.settingsService.GetBool("fHideTrayIcon") {
			a.trayManager.SetVisible(false)
			fmt.Println("System tray icon started (hidden via fHideTrayIcon)")
		} else {
			fmt.Println("System tray icon started")
		}
	} else {
		fmt.Println("System tray icon disabled on this platform")
	}
}

// initializeFullDaemon runs full daemon initialization including P2P in background.
// Progress events are emitted to frontend via Wails events using InitProgress format.
// Called by StartInitialization() when SplashScreenWithEvents mounts.
// Replaces initializeDaemonAsync() and incorporates P2P init from StartP2P().
func (a *App) initializeFullDaemon() {
	// Atomic guard: prevent concurrent calls (React StrictMode double-mounts in dev)
	if !a.initStarting.CompareAndSwap(false, true) {
		fmt.Println("Daemon initialization already in progress, skipping duplicate call")
		return
	}

	fmt.Println("Starting full daemon initialization...")

	phases := initPhaseDescriptions
	totalSteps := len(phases) + 1 // +1 for "complete" event
	currentStep := 0
	lastPhase := ""

	// Helper to emit progress events in InitProgress format.
	// Deduplicates by phase: internal OnProgress callbacks fire multiple times per phase
	// (e.g. storage 0%, 30%, 100%) but we only increment the step counter once per phase.
	emitProgress := func(step, description string) {
		if step != lastPhase {
			currentStep++
			lastPhase = step
		}
		progress := int(float64(currentStep) / float64(totalSteps) * 100)
		runtime.EventsEmit(a.ctx, "initialization:progress", initialization.InitProgress{
			Step:        step,
			Description: description,
			Progress:    progress,
			TotalSteps:  totalSteps,
			CurrentStep: currentStep,
			IsComplete:  false,
		})
	}

	// Determine network
	network := "mainnet"
	if a.guiConfig != nil {
		network = a.guiConfig.Network
	}

	// Create ConfigManager — unified daemon config authority (twinsd.yml).
	// Loads defaults → YAML → env var overrides.
	yamlPath := filepath.Join(a.dataDir, "twinsd.yml")
	cm := config.NewConfigManager(yamlPath, logrus.NewEntry(logrus.StandardLogger()))
	if err := cm.LoadOrCreate(); err != nil {
		a.emitInitFatal(fmt.Sprintf("Failed to load configuration: %v", err), err)
		return
	}

	// One-time migration: daemon settings from settings.json → twinsd.yml
	if a.settingsService != nil {
		migrateDaemonSettings(a.settingsService, cm, a.dataDir)
	}

	a.componentsMu.Lock()
	a.configManager = cm
	a.componentsMu.Unlock()

	// Wire ConfigManager to configService if already initialized (early LoadConfiguration call).
	a.componentsMu.RLock()
	cs := a.configService
	a.componentsMu.RUnlock()
	if cs != nil {
		cs.SetConfigManager(cm)
	}

	// Apply logging level from ConfigManager.
	// Guard against TWINS_LOG_LEVEL: main.go reads that env var before ConfigManager
	// exists and applies it directly to logrus. Preserve that user-supplied value here.
	// (ConfigManager uses TWINS_LOGGING_LEVEL, which it locks internally via cliLocks.)
	if os.Getenv("TWINS_LOG_LEVEL") == "" {
		if lvl := cm.GetString("logging.level"); lvl != "" {
			if level, err := logrus.ParseLevel(lvl); err == nil {
				logrus.SetLevel(level)
			}
		}
	}

	// Apply logging format from ConfigManager (text or json)
	if logFormat := cm.GetString("logging.format"); logFormat != "" {
		switch logFormat {
		case "json":
			logrus.SetFormatter(&logrus.JSONFormatter{TimestampFormat: "2006-01-02T15:04:05.000Z"})
		default: // "text" or unrecognised
			logrus.SetFormatter(&logrus.TextFormatter{FullTimestamp: true, TimestampFormat: "2006-01-02 15:04:05"})
		}
	}

	// Apply log output from ConfigManager.
	// When a file path is configured, redirect logrus to that file instead of stdout.
	// f is a process-lifetime handle; the OS reclaims it on exit — acceptable for a GUI app.
	if output := cm.GetString("logging.output"); output != "" && output != "stdout" {
		switch output {
		case "stderr":
			logrus.SetOutput(os.Stderr)
		default:
			// Resolve relative paths against data directory.
			logPath := output
			if !filepath.IsAbs(logPath) {
				logPath = filepath.Join(a.dataDir, logPath)
			}
			if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600); err == nil {
				logrus.SetOutput(f)
			} else {
				fmt.Fprintf(os.Stderr, "WARNING: failed to open log file %q from config: %v (continuing with stdout)\n", logPath, err)
			}
		}
	}

	// Build P2P config from ConfigManager
	p2pCfg := daemon.P2PConfig{
		ListenAddr: fmt.Sprintf("%s:%d", cm.GetString("network.listenAddr"), cm.GetInt("network.port")),
		Seeds:      cm.GetStringSlice("network.seeds"),
		MaxPeers:   cm.GetInt("network.maxPeers"),
		TestNet:    network == "testnet",
		Listen:     cm.GetBool("network.listen"),
	}

	// Read staking preference from ConfigManager
	staking := cm.GetBool("staking.enabled")

	// Check for pending repair action from a previous restart
	pendingRepair := readAndClearRepairFlag(a.dataDir)
	if pendingRepair != nil {
		emitProgress("repair", fmt.Sprintf("Executing repair: %s...", pendingRepair.Action))
		switch pendingRepair.Action {
		case "resync":
			// Delete blockchain database and all derived caches to force full re-sync from peers
			filesToDelete := []struct {
				path string
				name string
			}{
				{daemon.DBPath(a.dataDir), "blockchain.db"},
				{filepath.Join(a.dataDir, "txcache.dat"), "txcache.dat"},
				{filepath.Join(a.dataDir, "mncache.dat"), "mncache.dat"},
				{filepath.Join(a.dataDir, "mnpayments.dat"), "mnpayments.dat"},
			}
			for _, f := range filesToDelete {
				if err := os.RemoveAll(f.path); err != nil && !os.IsNotExist(err) {
					fmt.Printf("Warning: failed to remove %s for resync: %v\n", f.name, err)
				} else {
					fmt.Printf("Removed %s for resync\n", f.name)
				}
			}
		case "rescan":
			// Delete only the transaction cache to force wallet rescan from blockchain indexes
			txcachePath := filepath.Join(a.dataDir, "txcache.dat")
			if err := os.Remove(txcachePath); err != nil && !os.IsNotExist(err) {
				fmt.Printf("Warning: failed to remove txcache.dat for rescan: %v\n", err)
			} else {
				fmt.Printf("Removed txcache.dat for rescan\n")
			}
		default:
			fmt.Printf("Warning: repair action '%s' not yet handled, skipping\n", pendingRepair.Action)
			pendingRepair = nil
		}
		a.componentsMu.Lock()
		a.pendingRepair = pendingRepair
		a.componentsMu.Unlock()
	}

	// Wire masternode debug config from ConfigManager snapshot
	configSnapshot := cm.Snapshot()
	mnDebug := configSnapshot.Masternode.Debug
	mnDebugMaxMB := configSnapshot.Masternode.DebugMaxMB
	mnDebugMaxFiles := configSnapshot.Masternode.DebugMaxFiles

	// Run unified startup sequence (NewNode → ValidateChain → Wallet → MNCache → Mempool+P2P → MNConf → Staking → RPC)
	node, err := startup.Start(a.ctx, startup.Config{
		Network: network,
		DataDir: a.dataDir,
		Logger:  logrus.NewEntry(logrus.StandardLogger()),
		OnProgress: func(phase string, pct float64) {
			desc, ok := phases[phase]
			if !ok {
				desc = fmt.Sprintf("Initializing %s...", phase)
			}
			emitProgress(phase, desc)
		},
		MasternodeDebug:         mnDebug,
		MasternodeDebugMaxMB:    mnDebugMaxMB,
		MasternodeDebugMaxFiles: mnDebugMaxFiles,
		WalletConfig: daemon.WalletConfig{
			FullConfig: configSnapshot, // applies wallet settings from twinsd.yml (fees, keypool, reserveBalance, etc.)
			UseTxCache: true,
		},
		P2PConfig:               p2pCfg,
		Staking:                 staking,
		ConfigManager:           cm,
		RPCConfig: &daemon.RPCConfig{
			ShutdownFunc:          func() { runtime.Quit(a.ctx) },
			FullConfig:            configSnapshot,
			MasternodeEnabled:     configSnapshot.Masternode.Enabled,
			MasternodePrivateKey:  configSnapshot.Masternode.PrivateKey,
			MasternodeServiceAddr: configSnapshot.Masternode.ServiceAddr,
		},
	})
	if err != nil {
		a.emitInitFatal(fmt.Sprintf("Daemon initialization failed during %s phase: %v", lastPhase, err), err)
		return
	}

	// --- GUI Post-hooks: wire components to frontend ---

	// Store node and build legacy CoreComponents bridge (includes P2P components)
	a.componentsMu.Lock()
	a.node = node
	a.coreComponents = &daemon.CoreComponents{
		Storage:    node.Storage,
		Blockchain: node.Blockchain,
		Consensus:  node.Consensus,
		Mempool:    node.Mempool,
		Masternode: node.Masternode,
		Spork:      node.Spork,
		P2PServer:  node.P2PServer,
		Syncer:     node.Syncer,
	}
	a.wallet = node.Wallet
	a.masternodeConf = node.MasternodeConf
	a.componentsMu.Unlock()

	// Apply GUI wallet settings to the wallet layer.
	// These settings are stored in GUI preferences but must be pushed to the wallet DB
	// so the staking engine uses the user's configured values.
	if node.Wallet != nil && a.settingsService != nil {
		guiSettings := a.settingsService.GetAll()
		if err := node.Wallet.SetStakeSplitThreshold(guiSettings.StakeSplitThreshold); err != nil {
			logrus.WithError(err).Warn("Failed to apply stake split threshold from GUI settings")
		}
		if err := node.Wallet.SetAutoCombineRewards(guiSettings.AutoCombineRewards, guiSettings.StakeSplitThreshold); err != nil {
			logrus.WithError(err).Warn("Failed to apply auto-combine rewards from GUI settings")
		}
	}

	// Wire wallet lock callback to emit Wails event for frontend state sync.
	// The daemon's InitWallet already set a callback that stops staking;
	// we replace it with one that does both (stop staking + emit event).
	// This ensures auto-lock timeout emits wallet:locked to the frontend.
	// Safe to call here: wallet is still encrypted+locked at this point
	// (no user interaction possible before initialization:complete event).
	if node.Wallet != nil {
		cons := node.Consensus // may be nil
		appCtx := a.ctx
		logger := logrus.WithField("component", "wallet-lock-callback")
		node.Wallet.SetOnLockCallback(func() {
			// Stop staking (equivalent to daemon's original callback in node_wallet.go:86-95)
			if cons != nil {
				if err := cons.StopStaking(); err != nil {
					logger.WithError(err).Debug("StopStaking on wallet lock")
				} else {
					logger.Info("Staking stopped due to wallet lock")
				}
			}
			// Emit wallet:locked event so frontend updates lock icon immediately
			runtime.EventsEmit(appCtx, "wallet:locked", nil)
		})
	}

	// Mark P2P as started only if it actually succeeded (allows StartP2P retry on failure)
	if node.P2PServer != nil {
		a.p2pStarting.Store(true)
	}

	// Start background traffic collector (samples P2P bytes every 1s, retains 24h)
	if node.P2PServer != nil {
		a.trafficCollector = NewTrafficCollector(func() (uint64, uint64) {
			stats := node.P2PServer.GetStats()
			if stats == nil {
				return 0, 0
			}
			return stats.BytesReceived, stats.BytesSent
		})
		a.trafficCollector.Start()
	}

	// Create real GoCoreClient with full daemon components
	emitProgress("coreservices", "Starting core services...")
	a.coreClient = core.NewGoCoreClientWithComponents(a.coreComponents)

	// Wire wallet, consensus, syncer, P2P to core client
	if goCoreClient, ok := a.coreClient.(*core.GoCoreClient); ok {
		if node.Wallet != nil {
			goCoreClient.SetWallet(node.Wallet)
		}
		if node.Consensus != nil {
			goCoreClient.SetConsensus(node.Consensus)
		}
		if node.Syncer != nil {
			goCoreClient.SetSyncer(node.Syncer)
		}
		if node.P2PServer != nil {
			goCoreClient.SetP2PServer(node.P2PServer)
		}
		if node.PaymentTracker != nil {
			goCoreClient.SetPaymentTracker(node.PaymentTracker)
		}
		// Set initial staking enabled state from ConfigManager
		// Use local cm (already set under componentsMu lock above)
		goCoreClient.SetStakingEnabled(cm.GetBool("staking.enabled"))

		// Subscribe to staking.enabled changes so the GoCoreClient cache stays in sync
		// when the Options dialog (or any ConfigManager.Set path) toggles staking.
		// Also emits a "staking:changed" event so the frontend refreshes immediately
		// instead of waiting for the next 10-second polling cycle.
		gcc := goCoreClient // capture for closure
		appCtx := a.ctx     // capture for closure
		cm.Subscribe("staking.enabled", func(_ string, _, newValue interface{}) {
			if enabled, ok := newValue.(bool); ok {
				gcc.SetStakingEnabled(enabled)
				runtime.EventsEmit(appCtx, "staking:changed", map[string]interface{}{
					"enabled": enabled,
				})
			}
		})
	}

	// Start the core client
	if err := a.coreClient.Start(a.ctx); err != nil {
		if a.trafficCollector != nil {
			a.trafficCollector.Stop()
		}
		node.Shutdown()
		a.componentsMu.Lock()
		a.coreComponents = nil
		a.node = nil
		a.componentsMu.Unlock()

		a.emitInitFatal(fmt.Sprintf("Failed to start core client: %v", err), err)
		return
	}

	// Start listening to core events
	go a.handleCoreEvents()

	// Wire masternode manager to wallet for collateral UTXO filtering
	a.wireMasternodeCollaterals()

	fmt.Println("P2P networking started successfully")

	// Start background goroutine to monitor P2P status and emit events
	if a.monitorStarted.CompareAndSwap(false, true) {
		go a.monitorP2PStatus()
	}

	// RPC and staking were started by startup.Start() — store RPC reference
	if node.RPCServer != nil {
		a.componentsMu.Lock()
		a.rpcServer = node.RPCServer
		a.rpcStarted.Store(true)
		a.componentsMu.Unlock()
	}

	fmt.Println("Full daemon initialization completed")
	a.initCompleted.Store(true)

	// If a repair action was pending, log success. The result is kept on a.pendingRepair
	// and retrieved by the frontend via GetPendingRepairResult() (pull-based, avoids
	// timing issues with event emission during splash-to-main transition).
	a.componentsMu.RLock()
	if a.pendingRepair != nil {
		fmt.Printf("Repair action '%s' completed successfully\n", a.pendingRepair.Action)
	}
	a.componentsMu.RUnlock()

	// Emit completion — splash screen transitions to main UI
	runtime.EventsEmit(a.ctx, "initialization:complete", initialization.InitProgress{
		Step:        "complete",
		Description: "Wallet initialized successfully!",
		Progress:    100,
		TotalSteps:  totalSteps,
		CurrentStep: totalSteps,
		IsComplete:  true,
	})
}

// shutdown is called when the app is closing.
// Uses node.Shutdown() for unified ordered shutdown.
func (a *App) shutdown(ctx context.Context) {
	fmt.Println("TWINS Wallet shutting down...")

	// Stop core client before node shutdown
	if a.coreClient != nil {
		fmt.Println("Stopping core client...")
		if err := a.coreClient.Stop(); err != nil {
			fmt.Printf("Error stopping core client: %v\n", err)
		} else {
			fmt.Println("Core client stopped successfully")
		}
	}

	// Stop system tray before node shutdown
	if a.trayManager != nil {
		a.trayManager.Stop()
	}

	// Stop traffic collector before node shutdown
	if a.trafficCollector != nil {
		a.trafficCollector.Stop()
	}

	// Unified node shutdown handles: RPC, P2P, masternode save, consensus,
	// wallet tx cache save, and storage close — in proper order.
	// Do NOT call wallet.Close() separately as it would close the DB before
	// node.Shutdown() can save the transaction cache.
	if a.node != nil {
		fmt.Println("Shutting down daemon node...")
		a.node.Shutdown()
		fmt.Println("Daemon node shutdown complete")
	}

	// Clean up RPC cookie file after node shutdown
	a.componentsMu.RLock()
	dataDir := a.dataDir
	a.componentsMu.RUnlock()

	if dataDir != "" {
		if err := rpc.DeleteCookieFile(dataDir); err != nil {
			fmt.Printf("Warning: failed to delete cookie file: %v\n", err)
		}
	}

	// GUI-specific cleanup
	if a.shutdownManager != nil && a.shutdownManager.IsShuttingDown() {
		fmt.Println("App: Waiting for shutdown to complete...")
	}
	if a.configService != nil {
		if err := a.configService.Close(); err != nil {
			fmt.Printf("Error closing config service: %v\n", err)
		}
	}
}

// categorizeInitError matches an error string against initErrorCategories and returns
// a user-friendly title and message. Returns defaults if no category matches.
func categorizeInitError(errStr string, err error) (title, message string) {
	for _, cat := range initErrorCategories {
		for _, kw := range cat.keywords {
			if strings.Contains(errStr, kw) {
				return cat.title, fmt.Sprintf(cat.template, err)
			}
		}
	}
	return "Initialization Failed",
		fmt.Sprintf("Failed to initialize blockchain.\n\nError: %v\n\nPlease check the logs for more details.", err)
}

// showInitializationError displays an error dialog for initialization failures.
// Error is categorized using initErrorCategories for guidance.
func (a *App) showInitializationError(err error) {
	title, message := categorizeInitError(err.Error(), err)
	runtime.MessageDialog(a.ctx, runtime.MessageDialogOptions{
		Type:    runtime.ErrorDialog,
		Title:   title,
		Message: message,
	})
}

// emitInitFatal logs an initialization error, shows a dialog, and emits a fatal event.
func (a *App) emitInitFatal(errMsg string, err error) {
	fmt.Println(errMsg)

	// Store error so late-connecting clients (browser dev mode) can be notified
	a.componentsMu.Lock()
	a.initError = errMsg
	a.componentsMu.Unlock()

	a.showInitializationError(err)
	runtime.EventsEmit(a.ctx, "initialization:fatal", map[string]interface{}{
		"error":      errMsg,
		"shouldExit": true,
	})
}

// IsDevMockMode returns whether the app is running in development mock mode
func (a *App) IsDevMockMode() bool {
	return a.guiConfig != nil && a.guiConfig.DevMockMode
}

// StartP2P initializes P2P networking and starts blockchain synchronization.
// This should be called when the main window appears after splash screen.
// Returns immediately after starting P2P - sync progress is reported via events.
// Uses node.InitP2P() for unified initialization (fixes 4 GUI bugs).
func (a *App) StartP2P() error {
	// Prevent concurrent StartP2P calls - atomic check-and-set
	if !a.p2pStarting.CompareAndSwap(false, true) {
		fmt.Println("P2P initialization already in progress")
		return nil
	}
	// Note: We don't reset p2pStarting on success - P2P should only start once

	// Check context before starting
	select {
	case <-a.ctx.Done():
		a.p2pStarting.Store(false)
		return fmt.Errorf("application shutting down")
	default:
	}

	// Use RLock for read-only checks
	a.componentsMu.RLock()
	nodeNil := a.node == nil
	p2pAlreadyStarted := !nodeNil && a.node.P2PServer != nil
	a.componentsMu.RUnlock()

	if nodeNil {
		a.p2pStarting.Store(false)
		return fmt.Errorf("node not initialized")
	}

	if p2pAlreadyStarted {
		fmt.Println("P2P already started")
		return nil
	}

	fmt.Println("Starting P2P networking...")

	// Emit connecting event
	runtime.EventsEmit(a.ctx, "p2p:connecting", map[string]interface{}{
		"status": "connecting",
	})

	// Get seed nodes for the network
	network := "mainnet"
	if a.guiConfig != nil && a.guiConfig.Network != "" {
		network = a.guiConfig.Network
	}
	seeds := config.GetDefaultSeeds(network)

	// Use node.InitP2P for unified initialization (handles all wiring + 4 bug fixes)
	if err := a.node.InitP2P(a.ctx, daemon.P2PConfig{
		ListenAddr: defaultP2PListenAddr,
		Seeds:      seeds,
		MaxPeers:   defaultP2PMaxPeers,
		TestNet:    network == "testnet",
		Listen:     true,
	}); err != nil {
		a.p2pStarting.Store(false)
		return fmt.Errorf("failed to initialize P2P: %w", err)
	}

	// Update legacy bridge with P2P components
	a.componentsMu.Lock()
	a.coreComponents.P2PServer = a.node.P2PServer
	a.coreComponents.Syncer = a.node.Syncer
	a.componentsMu.Unlock()

	// Wire syncer and P2P server to core client for status info
	if goCoreClient, ok := a.coreClient.(*core.GoCoreClient); ok {
		goCoreClient.SetSyncer(a.node.Syncer)
		goCoreClient.SetP2PServer(a.node.P2PServer)
	}

	// Wire masternode manager to wallet for collateral UTXO filtering
	// and lock any already-known collateral UTXOs to prevent accidental spending.
	// Collateral existence verification is deferred to post-sync (see monitorP2PStatus).
	a.wireMasternodeCollaterals()

	fmt.Println("P2P networking started successfully")

	// Start background goroutine to monitor P2P status and emit events (only once)
	if a.monitorStarted.CompareAndSwap(false, true) {
		go a.monitorP2PStatus()
	}

	// Start RPC server for twins-cli compatibility (non-blocking)
	go func() {
		if err := a.StartRPCServer(); err != nil {
			fmt.Printf("Warning: failed to start RPC server: %v\n", err)
		}
	}()

	// Start staking if enabled in settings (non-blocking)
	go func() {
		if err := a.StartStaking(); err != nil {
			fmt.Printf("Warning: failed to start staking: %v\n", err)
		}
	}()

	return nil
}

// monitorP2PStatus monitors P2P status and emits events to frontend
func (a *App) monitorP2PStatus() {
	fmt.Println("Starting P2P status monitor...")

	var lastPeerCount int
	lastSyncing := false
	wasConnected := false
	wasSynced := false

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// Helper to safely emit events (checks context first)
	safeEmit := func(event string, data map[string]interface{}) bool {
		select {
		case <-a.ctx.Done():
			return false
		default:
			runtime.EventsEmit(a.ctx, event, data)
			return true
		}
	}

	for {
		select {
		case <-a.ctx.Done():
			fmt.Println("P2P status monitor stopped")
			return
		case <-ticker.C:
			// Get components with mutex protection
			a.componentsMu.RLock()
			components := a.coreComponents
			var p2pServer *p2p.Server
			var syncer *p2p.BlockchainSyncer
			if components != nil {
				p2pServer = components.P2PServer
				syncer = components.Syncer
			}
			a.componentsMu.RUnlock()

			if p2pServer == nil {
				continue
			}

			peerCount := int(p2pServer.GetPeerCount())
			connected := peerCount > 0

			// Emit peer count change event
			if peerCount != lastPeerCount {
				fmt.Printf("[P2P Event] p2p:peer_count peers=%d\n", peerCount)
				if !safeEmit("p2p:peer_count", map[string]interface{}{
					"peers": peerCount,
				}) {
					return
				}
				lastPeerCount = peerCount
			}

			// Emit connected event when first peer connects
			if connected && !wasConnected {
				fmt.Printf("[P2P Event] p2p:connected peers=%d\n", peerCount)
				if !safeEmit("p2p:connected", map[string]interface{}{
					"peers": peerCount,
				}) {
					return
				}
				wasConnected = true
			}

			// Check sync status
			if syncer != nil {
				syncing := syncer.IsSyncing()
				currentHeight, targetHeight, _ := syncer.GetSyncProgress()

				// Emit syncing start event
				if syncing && !lastSyncing {
					progress := float64(0)
					if targetHeight > 0 {
						progress = float64(currentHeight) / float64(targetHeight) * 100
					}
					fmt.Printf("[P2P Event] p2p:syncing height=%d/%d progress=%.2f%%\n", currentHeight, targetHeight, progress)
					if !safeEmit("p2p:syncing", map[string]interface{}{
						"currentHeight": currentHeight,
						"networkHeight": targetHeight,
						"progress":      progress,
					}) {
						return
					}
				}

				// Emit sync progress periodically while syncing
				if syncing && targetHeight > 0 {
					progress := float64(currentHeight) / float64(targetHeight) * 100
					fmt.Printf("[P2P Event] chain:sync height=%d/%d progress=%.2f%%\n", currentHeight, targetHeight, progress)
					if !safeEmit("chain:sync", map[string]interface{}{
						"currentHeight": currentHeight,
						"targetHeight":  targetHeight,
						"progress":      progress,
					}) {
						return
					}
				}

				// Emit synced event when sync completes OR when already synced and connected
				// This handles the case where blockchain was already synced at startup
				if !syncing && !wasSynced && connected {
					fmt.Printf("[P2P Event] p2p:synced height=%d\n", currentHeight)
					if !safeEmit("p2p:synced", map[string]interface{}{
						"height": currentHeight,
					}) {
						return
					}
					wasSynced = true

					// Verify masternode collateral UTXOs now that chain sync is complete.
					// This ensures the wallet UTXO set is fully populated before checking.
					// Runs in a separate goroutine to avoid blocking the monitor ticker.
					go a.verifyMasternodeCollaterals()
				}

				lastSyncing = syncing
			}
		}
	}
}

// GetP2PStatus returns the current P2P network status
func (a *App) GetP2PStatus() map[string]interface{} {
	// Get components with mutex protection
	a.componentsMu.RLock()
	components := a.coreComponents
	var p2pServer *p2p.Server
	var syncer *p2p.BlockchainSyncer
	var blockchain blockchain.Blockchain
	if components != nil {
		p2pServer = components.P2PServer
		syncer = components.Syncer
		blockchain = components.Blockchain
	}
	a.componentsMu.RUnlock()

	if p2pServer == nil {
		return map[string]interface{}{
			"connected":     false,
			"peers":         0,
			"syncing":       false,
			"initialized":   false,
			"currentHeight": uint32(0),
			"networkHeight": uint32(0),
			"progress":      float64(0),
			"message":       "P2P not initialized",
		}
	}

	peerCount := p2pServer.GetPeerCount()
	connected := peerCount > 0

	// Get current blockchain height
	currentHeight := uint32(0)
	if blockchain != nil {
		currentHeight, _ = blockchain.GetBestHeight()
	}

	// Get sync status from syncer
	syncing := false
	networkHeight := uint32(0)
	progress := float64(100)
	message := "Synced"

	if syncer != nil {
		syncing = syncer.IsSyncing()
		_, targetHeight, _ := syncer.GetSyncProgress()
		networkHeight = targetHeight

		if syncing && networkHeight > 0 {
			progress = float64(currentHeight) / float64(networkHeight) * 100
			if progress > 100 {
				progress = 100
			}
			message = fmt.Sprintf("Syncing: %d / %d blocks", currentHeight, networkHeight)
		} else if !connected {
			message = "Connecting..."
			progress = 0
		}
	}

	return map[string]interface{}{
		"connected":     connected,
		"peers":         peerCount,
		"syncing":       syncing,
		"initialized":   true,
		"currentHeight": currentHeight,
		"networkHeight": networkHeight,
		"progress":      progress,
		"message":       message,
	}
}

// wireMasternodeCollaterals wires the masternode manager to the wallet for collateral
// UTXO filtering and locks any collateral UTXOs already known in the wallet.
// This runs immediately during StartP2P() to protect collateral from accidental spending.
// The collateral existence check is deferred to verifyMasternodeCollaterals() which runs post-sync.
func (a *App) wireMasternodeCollaterals() {
	a.componentsMu.RLock()
	confFile := a.masternodeConf
	w := a.wallet
	mnManager := a.coreComponents.Masternode
	a.componentsMu.RUnlock()

	// Wire masternode.conf to masternode manager for collateral checking
	if mnManager != nil && confFile != nil {
		mnManager.SetConfFile(confFile)
	}

	// Wire masternode manager to wallet for collateral UTXO filtering
	if w != nil && mnManager != nil {
		w.SetMasternodeManager(mnManager)
		fmt.Println("Wallet integrated with masternode manager for collateral filtering")
	} else {
		// Log warning when integration fails
		if w == nil {
			fmt.Println("Warning: Wallet not available - masternode collateral filtering disabled")
		}
		if mnManager == nil {
			fmt.Println("Warning: Masternode manager not available - collateral filtering disabled")
		}
	}

	// Lock any collateral UTXOs already known in the wallet (pre-sync)
	if confFile == nil || w == nil {
		return
	}

	entries := confFile.GetEntries()
	if len(entries) == 0 {
		return
	}

	for _, entry := range entries {
		if w.HasUTXO(entry.GetOutpoint()) {
			outpoint := core.OutPoint{
				TxID: entry.TxHash.String(),
				Vout: entry.OutputIndex,
			}
			if a.coreClient != nil {
				if err := a.coreClient.LockUnspent(false, []core.OutPoint{outpoint}); err != nil {
					fmt.Printf("Warning: failed to lock collateral for %s: %v\n", entry.Alias, err)
				} else {
					fmt.Printf("Locked collateral UTXO for masternode '%s'\n", entry.Alias)
				}
			}
		}
	}
}

// verifyMasternodeCollaterals checks all masternode collateral UTXOs after chain sync
// completes. Uses HasUTXO which does NOT filter out collateral UTXOs (unlike ListUnspent).
// Only emits masternode:invalid_collateral events for genuinely missing/spent collaterals.
// Respects context cancellation for clean shutdown.
func (a *App) verifyMasternodeCollaterals() {
	a.componentsMu.RLock()
	confFile := a.masternodeConf
	w := a.wallet
	ctx := a.ctx
	a.componentsMu.RUnlock()

	if confFile == nil || w == nil {
		return
	}

	entries := confFile.GetEntries()
	if len(entries) == 0 {
		return
	}

	fmt.Printf("Verifying %d masternode collateral UTXOs (post-sync)...\n", len(entries))

	for _, entry := range entries {
		// Check for shutdown/context cancellation before each entry
		select {
		case <-ctx.Done():
			fmt.Println("Masternode collateral verification cancelled - shutting down")
			return
		default:
		}

		if w.HasUTXO(entry.GetOutpoint()) {
			// UTXO exists - ensure it's locked
			outpoint := core.OutPoint{
				TxID: entry.TxHash.String(),
				Vout: entry.OutputIndex,
			}
			if a.coreClient != nil {
				if err := a.coreClient.LockUnspent(false, []core.OutPoint{outpoint}); err != nil {
					fmt.Printf("Warning: failed to lock collateral for %s: %v\n", entry.Alias, err)
				} else {
					fmt.Printf("Locked collateral UTXO for masternode '%s'\n", entry.Alias)
				}
			}
		} else {
			// UTXO genuinely missing after full sync - emit event
			fmt.Printf("Warning: collateral UTXO for masternode '%s' not found (may have been spent)\n", entry.Alias)
			runtime.EventsEmit(ctx, "masternode:invalid_collateral", map[string]interface{}{
				"alias":  entry.Alias,
				"txHash": entry.TxHash.String(),
				"vout":   entry.OutputIndex,
			})
		}
	}

	fmt.Println("Masternode collateral verification complete")
}

// StartRPCServer initializes and starts the RPC server for twins-cli compatibility.
// Uses node.InitRPC() for unified initialization.
// Should be called after P2P is initialized.
func (a *App) StartRPCServer() error {
	// Prevent concurrent RPC server starts
	if !a.rpcStarted.CompareAndSwap(false, true) {
		fmt.Println("RPC server already started or starting")
		return nil
	}

	// Check context before starting
	select {
	case <-a.ctx.Done():
		a.rpcStarted.Store(false)
		return fmt.Errorf("application shutting down")
	default:
	}

	a.componentsMu.RLock()
	node := a.node
	a.componentsMu.RUnlock()

	if node == nil {
		a.rpcStarted.Store(false)
		return fmt.Errorf("node not initialized")
	}

	fmt.Println("Starting RPC server...")

	// Build RPCConfig with masternode settings from ConfigManager
	rpcCfg := daemon.RPCConfig{}
	if node.ConfigManager != nil {
		rpcCfg.FullConfig = node.ConfigManager.Snapshot()
		if node.ConfigManager.GetBool("masternode.enabled") {
			rpcCfg.MasternodeEnabled = true
			rpcCfg.MasternodePrivateKey = node.ConfigManager.GetString("masternode.privateKey")
			rpcCfg.MasternodeServiceAddr = node.ConfigManager.GetString("masternode.serviceAddr")
		}
	}
	if err := node.InitRPC(rpcCfg); err != nil {
		a.rpcStarted.Store(false)
		return fmt.Errorf("failed to start RPC server: %w", err)
	}

	// Store reference for shutdown and status queries
	a.componentsMu.Lock()
	a.rpcServer = node.RPCServer
	a.componentsMu.Unlock()

	fmt.Println("RPC server started successfully")
	return nil
}

// StartStaking enables staking based on ConfigManager settings.
// Should be called after P2P is initialized and synced.
func (a *App) StartStaking() error {
	// Check if staking is enabled via ConfigManager
	cm := a.getConfigManager()
	if cm == nil {
		fmt.Println("Staking not enabled: ConfigManager not initialized")
		return nil
	}

	if !cm.GetBool("staking.enabled") {
		fmt.Println("Staking not enabled in settings")
		return nil
	}

	// Get components and wallet
	a.componentsMu.RLock()
	components := a.coreComponents
	wallet := a.wallet
	a.componentsMu.RUnlock()

	if components == nil || components.Consensus == nil {
		return fmt.Errorf("consensus engine not initialized")
	}

	// Check if wallet is initialized and wired to consensus
	// This can fail if StartStaking is called before initializeWallet completes
	if wallet == nil {
		return fmt.Errorf("wallet not initialized - staking will be enabled after wallet loads")
	}

	fmt.Println("Starting staking based on ConfigManager settings...")

	// Start staking
	// Note: Staking requires wallet to be unlocked - this will be checked by StartStaking
	if err := components.Consensus.StartStaking(); err != nil {
		return fmt.Errorf("failed to start staking: %w", err)
	}

	fmt.Println("Staking started successfully")
	return nil
}

// GetRPCStatus returns the current RPC server status.
func (a *App) GetRPCStatus() map[string]interface{} {
	a.componentsMu.RLock()
	rpcServer := a.rpcServer
	a.componentsMu.RUnlock()

	if rpcServer == nil {
		return map[string]interface{}{
			"running": false,
			"host":    "",
			"port":    0,
		}
	}

	host := "127.0.0.1"
	port := defaultRPCPort
	if cm := a.getConfigManager(); cm != nil {
		host = cm.GetString("rpc.host")
		port = cm.GetInt("rpc.port")
	}
	return map[string]interface{}{
		"running": true,
		"host":    host,
		"port":    port,
	}
}

// GetStakingStatus returns the current staking status for the status bar.
// This bypasses GoCoreClient and accesses components directly for real-time status.
func (a *App) GetStakingStatus() map[string]interface{} {
	a.componentsMu.RLock()
	components := a.coreComponents
	w := a.wallet
	a.componentsMu.RUnlock()

	// Default response when components not ready
	if components == nil || components.Consensus == nil {
		return map[string]interface{}{
			"staking":        false,
			"enabled":        false,
			"walletUnlocked": false,
			"initialized":    false,
		}
	}

	// Get staking status from consensus engine
	isStaking := components.Consensus.IsStaking()

	// Check if staking is enabled via ConfigManager
	stakingEnabled := false
	if cm := a.getConfigManager(); cm != nil {
		stakingEnabled = cm.GetBool("staking.enabled")
	}

	// Check wallet lock status (staking requires unlocked wallet)
	walletUnlocked := false
	if w != nil {
		walletUnlocked = !w.IsLocked()
	}

	return map[string]interface{}{
		"staking":        isStaking,
		"enabled":        stakingEnabled,
		"walletUnlocked": walletUnlocked,
		"initialized":    true,
	}
}

// ToggleStaking enables or disables staking at runtime.
// Persists to twinsd.yml via ConfigManager and triggers the staking subscriber
// which starts/stops the consensus engine. Also updates GoCoreClient cache
// for the Overview page.
func (a *App) ToggleStaking(enabled bool) map[string]interface{} {
	// Prefer ConfigManager (unified path)
	if cm := a.getConfigManager(); cm != nil {
		// ConfigManager.Set persists to twinsd.yml + triggers subscriber (start/stop staking)
		if err := cm.Set("staking.enabled", enabled); err != nil {
			return map[string]interface{}{
				"staking":        false,
				"enabled":        enabled,
				"walletUnlocked": false,
				"initialized":    true,
				"error":          err.Error(),
			}
		}

		// GoCoreClient cache is updated automatically by the ConfigManager
		// subscriber wired in initializeFullDaemon (staking.enabled → SetStakingEnabled).

		return a.GetStakingStatus()
	}

	// Fallback: no ConfigManager (should not happen after init, but defensive).
	// Note: this path does NOT persist the staking preference because there is
	// no ConfigManager to write twinsd.yml. Log a warning so we notice if it
	// ever fires in production.
	logrus.Warn("ToggleStaking: configManager is nil, staking preference will not be persisted")
	a.componentsMu.RLock()
	components := a.coreComponents
	w := a.wallet
	a.componentsMu.RUnlock()

	if components == nil || components.Consensus == nil {
		return map[string]interface{}{
			"staking":        false,
			"enabled":        enabled,
			"walletUnlocked": false,
			"initialized":    false,
			"error":          "consensus engine not initialized",
		}
	}

	// Update GoCoreClient cache for Overview page
	if goCoreClient, ok := a.coreClient.(*core.GoCoreClient); ok {
		goCoreClient.SetStakingEnabled(enabled)
	}

	if enabled {
		// Check wallet is available
		if w == nil {
			return map[string]interface{}{
				"staking":        false,
				"enabled":        false,
				"walletUnlocked": false,
				"initialized":    true,
				"error":          "wallet not initialized",
			}
		}

		// Persist setting and update cache regardless of wallet lock state.
		// If wallet is locked, the wallet's onUnlockCallback (wired in
		// daemon/node_wallet.go) will auto-start staking on unlock.
		if goCoreClient, ok := a.coreClient.(*core.GoCoreClient); ok {
			goCoreClient.SetStakingEnabled(true)
		}

		if w.IsLocked() {
			fmt.Println("Staking enabled via GUI toggle (wallet locked, will auto-start on unlock)")
		} else {
			if err := components.Consensus.StartStaking(); err != nil {
				logrus.WithError(err).Warn("ToggleStaking fallback: failed to start staking")
			}
		}
	} else {
		components.Consensus.StopStaking()
	}

	return a.GetStakingStatus()
}

// GetWalletEncryptionStatus returns wallet encryption state for the status bar.
// This bypasses GoCoreClient wallet stubs and accesses wallet directly.
// Returns encryption status matching legacy Qt wallet 4-state model:
// - "unencrypted": Wallet has no encryption
// - "locked": Wallet is encrypted and locked
// - "unlocked": Wallet is encrypted and fully unlocked
// - "unlocked_staking": Wallet is encrypted and unlocked for staking only
func (a *App) GetWalletEncryptionStatus() map[string]interface{} {
	a.componentsMu.RLock()
	w := a.wallet
	a.componentsMu.RUnlock()

	// Default response when wallet not ready
	if w == nil {
		return map[string]interface{}{
			"status":      "unknown",
			"encrypted":   false,
			"locked":      false,
			"initialized": false,
		}
	}

	isEncrypted := w.IsEncrypted()
	isLocked := w.IsLocked()

	// Determine status string matching legacy Qt behavior (4 states)
	var status string
	if !isEncrypted {
		status = "unencrypted"
	} else if isLocked {
		status = "locked"
	} else if w.IsUnlockedForStakingOnly() {
		// Encrypted and unlocked for staking only (matches legacy UnlockedForAnonymizationOnly)
		status = "unlocked_staking"
	} else {
		// Encrypted and fully unlocked
		status = "unlocked"
	}

	return map[string]interface{}{
		"status":      status,
		"encrypted":   isEncrypted,
		"locked":      isLocked,
		"initialized": true,
	}
}

// UnlockWalletRequest contains parameters for unlocking the wallet.
type UnlockWalletRequest struct {
	Passphrase  string `json:"passphrase"`
	Timeout     int    `json:"timeout"`      // Duration in seconds (0 = indefinite)
	StakingOnly bool   `json:"stakingOnly"`  // If true, only staking allowed, no sends
}

// UnlockWalletResult contains the result of an unlock operation.
type UnlockWalletResult struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// UnlockWallet unlocks an encrypted wallet with the given passphrase.
// The timeout specifies how long (in seconds) the wallet stays unlocked.
// If stakingOnly is true, the wallet is unlocked for staking only (cannot send).
func (a *App) UnlockWallet(req UnlockWalletRequest) *UnlockWalletResult {
	a.componentsMu.RLock()
	w := a.wallet
	a.componentsMu.RUnlock()

	if w == nil {
		return &UnlockWalletResult{
			Success: false,
			Error:   "Wallet is not initialized",
		}
	}

	if !w.IsEncrypted() {
		return &UnlockWalletResult{
			Success: false,
			Error:   "Wallet is not encrypted",
		}
	}

	if !w.IsLocked() && !w.IsUnlockedForStakingOnly() {
		// Already fully unlocked - this is fine, return success
		return &UnlockWalletResult{
			Success: true,
		}
	}

	// Convert passphrase to bytes
	// SECURITY NOTE: Go strings are immutable and cannot be securely zeroed from memory.
	// The req.Passphrase string will persist in memory until garbage collected.
	// The wallet.Unlock() method zeros the byte slice copy after use (wallet.go:646-650),
	// which mitigates most risk. This is a known Go limitation also present in RPC methods.
	passphrase := []byte(req.Passphrase)

	// Convert timeout to duration (0 means indefinite)
	var duration time.Duration
	if req.Timeout > 0 {
		duration = time.Duration(req.Timeout) * time.Second
	}

	// Attempt to unlock
	err := w.Unlock(passphrase, duration, req.StakingOnly)
	if err != nil {
		return &UnlockWalletResult{
			Success: false,
			Error:   err.Error(),
		}
	}

	// Emit wallet state change event
	runtime.EventsEmit(a.ctx, "wallet:unlocked", map[string]interface{}{
		"stakingOnly": req.StakingOnly,
		"timeout":     req.Timeout,
	})

	// Note: staking auto-start on unlock is handled by wallet.onUnlockCallback
	// wired in daemon/node_wallet.go — no GUI-specific staking logic needed here.

	return &UnlockWalletResult{
		Success: true,
	}
}

// LockWalletResult contains the result of a lock operation.
type LockWalletResult struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// LockWallet locks an encrypted wallet.
// This clears all private keys and passphrase from memory.
func (a *App) LockWallet() *LockWalletResult {
	a.componentsMu.RLock()
	w := a.wallet
	a.componentsMu.RUnlock()

	if w == nil {
		return &LockWalletResult{
			Success: false,
			Error:   "Wallet is not initialized",
		}
	}

	if !w.IsEncrypted() {
		return &LockWalletResult{
			Success: false,
			Error:   "Wallet is not encrypted",
		}
	}

	if w.IsLocked() {
		// Already locked - this is fine, return success
		return &LockWalletResult{
			Success: true,
		}
	}

	// Lock the wallet (onLockCallback emits wallet:locked event to frontend)
	err := w.Lock()
	if err != nil {
		return &LockWalletResult{
			Success: false,
			Error:   err.Error(),
		}
	}

	return &LockWalletResult{
		Success: true,
	}
}

// RestoreToStakingOnlyModeResult contains the result of a restore-to-staking-only operation.
type RestoreToStakingOnlyModeResult struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// RestoreToStakingOnlyMode transitions the wallet from fully unlocked back to staking-only mode.
// Called after a temporary full-unlock for a specific operation (send, masternode start).
// The wallet passphrase stays in memory so staking continues uninterrupted.
func (a *App) RestoreToStakingOnlyMode() *RestoreToStakingOnlyModeResult {
	a.componentsMu.RLock()
	w := a.wallet
	a.componentsMu.RUnlock()

	if w == nil {
		return &RestoreToStakingOnlyModeResult{
			Success: false,
			Error:   "Wallet is not initialized",
		}
	}

	err := w.RestoreToStakingOnly()
	if err != nil {
		return &RestoreToStakingOnlyModeResult{
			Success: false,
			Error:   err.Error(),
		}
	}

	// Emit wallet:unlocked with stakingOnly=true so the status bar updates immediately
	runtime.EventsEmit(a.ctx, "wallet:unlocked", map[string]interface{}{
		"stakingOnly": true,
		"timeout":     0,
	})

	return &RestoreToStakingOnlyModeResult{
		Success: true,
	}
}

// EncryptWalletRequest contains parameters for encrypting the wallet.
type EncryptWalletRequest struct {
	Passphrase string `json:"passphrase"`
}

// EncryptWalletResult contains the result of an encrypt operation.
type EncryptWalletResult struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// EncryptWallet encrypts an unencrypted wallet with the given passphrase.
// Unlike legacy C++ which required restart, Go's BBolt database uses atomic writes
// so hot encryption is safe without restart.
func (a *App) EncryptWallet(req EncryptWalletRequest) *EncryptWalletResult {
	a.componentsMu.RLock()
	w := a.wallet
	a.componentsMu.RUnlock()

	if w == nil {
		return &EncryptWalletResult{
			Success: false,
			Error:   "Wallet is not initialized",
		}
	}

	if w.IsEncrypted() {
		return &EncryptWalletResult{
			Success: false,
			Error:   "Wallet is already encrypted",
		}
	}

	if req.Passphrase == "" {
		return &EncryptWalletResult{
			Success: false,
			Error:   "Passphrase cannot be empty",
		}
	}

	// Convert passphrase to bytes
	passphrase := []byte(req.Passphrase)
	defer func() {
		// Zero passphrase from memory after use
		for i := range passphrase {
			passphrase[i] = 0
		}
	}()

	// Encrypt the wallet
	err := w.EncryptWallet(passphrase)
	if err != nil {
		return &EncryptWalletResult{
			Success: false,
			Error:   err.Error(),
		}
	}

	// Emit wallet state change event
	runtime.EventsEmit(a.ctx, "wallet:encrypted", nil)

	return &EncryptWalletResult{
		Success: true,
	}
}

// ChangePassphraseRequest contains parameters for changing wallet passphrase.
type ChangePassphraseRequest struct {
	OldPassphrase string `json:"oldPassphrase"`
	NewPassphrase string `json:"newPassphrase"`
}

// ChangePassphraseResult contains the result of a passphrase change operation.
type ChangePassphraseResult struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// ChangeWalletPassphrase changes the wallet passphrase.
// The wallet must be encrypted for this operation.
func (a *App) ChangeWalletPassphrase(req ChangePassphraseRequest) *ChangePassphraseResult {
	a.componentsMu.RLock()
	w := a.wallet
	a.componentsMu.RUnlock()

	if w == nil {
		return &ChangePassphraseResult{
			Success: false,
			Error:   "Wallet is not initialized",
		}
	}

	if !w.IsEncrypted() {
		return &ChangePassphraseResult{
			Success: false,
			Error:   "Wallet is not encrypted",
		}
	}

	if req.OldPassphrase == "" {
		return &ChangePassphraseResult{
			Success: false,
			Error:   "Old passphrase cannot be empty",
		}
	}

	if req.NewPassphrase == "" {
		return &ChangePassphraseResult{
			Success: false,
			Error:   "New passphrase cannot be empty",
		}
	}

	// Convert passphrases to bytes
	oldPassphrase := []byte(req.OldPassphrase)
	newPassphrase := []byte(req.NewPassphrase)
	defer func() {
		// Zero passphrases from memory after use
		for i := range oldPassphrase {
			oldPassphrase[i] = 0
		}
		for i := range newPassphrase {
			newPassphrase[i] = 0
		}
	}()

	// Change the passphrase
	err := w.ChangePassphrase(oldPassphrase, newPassphrase)
	if err != nil {
		return &ChangePassphraseResult{
			Success: false,
			Error:   err.Error(),
		}
	}

	// Emit wallet state change event
	runtime.EventsEmit(a.ctx, "wallet:passphrase_changed", nil)

	return &ChangePassphraseResult{
		Success: true,
	}
}

// GetTorStatus returns Tor network status for the status bar.
// Returns whether Tor is enabled and the onion address if available.
// Note: Tor integration is not yet wired to P2P server, returns disabled for now.
func (a *App) GetTorStatus() map[string]interface{} {
	// TODO: Wire TorController to P2P server and expose IsTorEnabled/GetOnionAddress
	// For now, Tor is not supported in the GUI - return disabled state
	return map[string]interface{}{
		"enabled":      false,
		"onionAddress": "",
		"initialized":  true,
	}
}

// fileExists returns true if the path exists and is a regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
