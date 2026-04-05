package main

import (
	"context"
	"embed"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/twins-dev/twins-core/internal/cli"
	"github.com/twins-dev/twins-core/internal/gui/constants"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Force shutdown tracking - double close within this duration triggers force exit
const forceShutdownWindow = 3 * time.Second

var (
	lastCloseAttempt   time.Time
	lastCloseAttemptMu sync.Mutex
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var appIcon []byte

func main() {
	// Set default log level to error (matching twinsd behavior).
	// Must run before any daemon components are initialized.
	logLevel := logrus.ErrorLevel
	if envLevel := os.Getenv("TWINS_LOG_LEVEL"); envLevel != "" {
		if parsed, err := logrus.ParseLevel(envLevel); err == nil {
			logLevel = parsed
		}
	}
	logrus.SetLevel(logLevel)
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05",
	})

	// Parse command-line flags BEFORE Wails initializes
	// This allows -help and -version to work without creating a window
	guiConfig, shouldExit, err := cli.ParseGUIArgs(os.Args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if shouldExit {
		os.Exit(0)
	}

	// Create an instance of the app structure with parsed config
	app := NewApp(guiConfig)

	// Get window title (with network suffix if applicable)
	windowTitle := guiConfig.GetWindowTitle()

	// Windows taskbar grouping/pinning uses AppUserModel identity in addition
	// to the window icon state. Set it before any UI is created.
	configureWindowsTaskbarIdentity()

	// Create application with options
	err = wails.Run(&options.App{
		// Window will be shown by startup function after determining intro vs splash
		Title:       windowTitle,
		Width:       constants.SplashWindowWidth,
		Height:      constants.SplashWindowHeight + splashHeightExtra(),
		MinWidth:    constants.IntroWindowWidth,
		MinHeight:   363,
		StartHidden: true, // Hide window until we determine the correct initial state
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 58, G: 58, B: 58, A: 1}, // Match Qt #3a3a3a
		Menu:             app.CreateApplicationMenu(),              // Native menu bar
		OnStartup:        app.startup,
		OnDomReady:       app.domReady,
		OnShutdown:       app.shutdown,
		OnBeforeClose: func(ctx context.Context) (prevent bool) {
			// If shutdown already completed (e.g. runtime.Quit from shutdown manager),
			// allow close immediately so Wails calls OnShutdown for cleanup.
			// This MUST run before double-close detection to avoid false positives
			// when runtime.Quit() triggers OnBeforeClose within the force window.
			if app.shutdownManager != nil && app.shutdownManager.IsShutdownComplete() {
				fmt.Println("Shutdown complete - allowing window close")
				return false
			}

			// Check for double close (force shutdown) - like daemon's double Ctrl+C
			lastCloseAttemptMu.Lock()
			now := time.Now()
			timeSinceLastClose := now.Sub(lastCloseAttempt)
			lastCloseAttempt = now
			lastCloseAttemptMu.Unlock()

			if timeSinceLastClose < forceShutdownWindow && timeSinceLastClose > 0 {
				// Double close detected - force shutdown immediately
				fmt.Println("Double close detected - forcing immediate shutdown")
				if app.shutdownManager != nil {
					app.shutdownManager.ForceShutdown()
				} else {
					os.Exit(0)
				}
				return false // Allow close (though os.Exit will happen first)
			}

			// Get current window state
			currentState := ""
			if app.windowManager != nil {
				currentState = string(app.windowManager.GetCurrentState())
			}

			// Prevent closing during splash screen (initialization)
			if currentState == "splash" {
				fmt.Println("Window close prevented - application is initializing (close again to force)")
				return true // Prevent close during splash
			}

			// Minimize on close: minimize window instead of quitting when enabled.
			// Matches legacy C++ BitcoinGUI::closeEvent() behavior.
			// Only applies after initialization (past splash screen) and when not already shutting down.
			// Skip minimize behaviors when Quit was explicitly chosen from tray menu.
			if !app.trayQuitRequested.Load() && app.settingsService != nil && app.settingsService.GetBool("fMinimizeOnClose") {
				if app.shutdownManager == nil || !app.shutdownManager.IsShuttingDown() {
					// If minimize-to-tray is also enabled, hide to tray instead of taskbar
					if app.settingsService.GetBool("fMinimizeToTray") && app.trayManager != nil && app.trayManager.IsStarted() {
						app.trayManager.HideWindow()
					} else {
						runtime.WindowMinimise(ctx)
					}
					return true // Prevent close
				}
			}
			app.trayQuitRequested.Store(false) // Reset flag

			// Check shutdown manager state
			if app.shutdownManager != nil {
				// If shutdown is in progress, inform user they can force close
				if app.shutdownManager.IsShuttingDown() {
					fmt.Println("Shutdown in progress - close again within 3s to force exit")
					return true
				}

				// If we haven't started shutdown yet, initiate it
				fmt.Println("Window close requested - initiating graceful shutdown (close again to force)")
				// Emit event to trigger shutdown dialog in frontend
				runtime.EventsEmit(ctx, "app:shutdown")
				// Prevent immediate close - let shutdown process complete
				return true
			}

			// Allow close if no shutdown manager
			return false
		},
		Bind: []interface{}{
			app,
		},
		// Platform specific options
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			DisableWindowIcon:    false,
		},
		Mac: &mac.Options{
			TitleBar: &mac.TitleBar{
				TitlebarAppearsTransparent: false, // Use native macOS titlebar
				HideTitle:                  false,
				HideTitleBar:               false,
				FullSizeContent:            false,
				UseToolbar:                 false,
				HideToolbarSeparator:       true,
			},
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			About: &mac.AboutInfo{
				Title:   "TWINS Core - Wallet",
				Message: "© 2024 TWINS Development Team",
				Icon:    appIcon,
			},
		},
		Linux: &linux.Options{
			Icon:                appIcon,
			WindowIsTranslucent: false,
		},
	})

	if err != nil {
		fmt.Printf("Error: %v\n", err)
	}
}
