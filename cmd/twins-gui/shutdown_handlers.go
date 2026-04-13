package main

import (
	"fmt"
	"os"

	"github.com/twins-dev/twins-core/internal/gui/window"
)

// ==========================================
// Shutdown Management Methods
// ==========================================

// InitiateShutdown starts the graceful shutdown process
func (a *App) InitiateShutdown() error {
	fmt.Println("App: Initiating graceful shutdown...")

	// Transition to shutdown window
	if a.windowManager != nil {
		if err := a.windowManager.TransitionTo(window.StateShutdown); err != nil {
			fmt.Printf("App: Failed to transition to shutdown window: %v\n", err)
		}
	}

	// Register cleanup callbacks
	if a.shutdownManager != nil {
		// Register config cleanup
		a.shutdownManager.RegisterCallback(func() error {
			fmt.Println("App: Closing configuration service...")
			if a.configService != nil {
				return a.configService.Close()
			}
			return nil
		})

		// Start the shutdown sequence
		return a.shutdownManager.StartShutdown()
	}

	return fmt.Errorf("shutdown manager not initialized")
}

// ForceShutdown performs an immediate shutdown
func (a *App) ForceShutdown() {
	fmt.Println("App: Force shutdown requested")

	if a.shutdownManager != nil {
		a.shutdownManager.ForceShutdown()
	} else {
		// If shutdown manager not available, quit directly
		fmt.Println("App: Emergency exit")
		os.Exit(1)
	}
}

// GetShutdownProgress returns the current shutdown progress
func (a *App) GetShutdownProgress() map[string]interface{} {
	if a.shutdownManager == nil || !a.shutdownManager.IsShuttingDown() {
		return map[string]interface{}{
			"isShuttingDown": false,
			"progress":       nil,
		}
	}

	progress := a.shutdownManager.GetProgress()
	return map[string]interface{}{
		"isShuttingDown": true,
		"progress": map[string]interface{}{
			"message":    progress.Message,
			"percentage": progress.Percentage,
			"stage":      string(progress.Stage),
		},
	}
}

// IsShuttingDown returns whether shutdown is in progress
func (a *App) IsShuttingDown() bool {
	if a.shutdownManager == nil {
		return false
	}
	return a.shutdownManager.IsShuttingDown()
}

// IsShutdownComplete returns whether shutdown has completed
func (a *App) IsShutdownComplete() bool {
	if a.shutdownManager == nil {
		return false
	}
	return a.shutdownManager.IsShutdownComplete()
}
