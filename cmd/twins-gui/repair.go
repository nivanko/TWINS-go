package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const repairFlagFile = "repair-pending.json"

// RepairPending represents a queued repair action persisted to disk.
type RepairPending struct {
	Action    string `json:"action"`
	Timestamp string `json:"timestamp"`
}

// RepairResult is emitted to the frontend after a repair action completes on startup.
type RepairResult struct {
	Action  string `json:"action"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// writeRepairFlag persists the repair action to repair-pending.json in the data directory.
func writeRepairFlag(dataDir, action string) error {
	pending := RepairPending{
		Action:    action,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	data, err := json.MarshalIndent(pending, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal repair flag: %w", err)
	}

	flagPath := filepath.Join(dataDir, repairFlagFile)
	if err := os.WriteFile(flagPath, data, 0644); err != nil {
		return fmt.Errorf("write repair flag: %w", err)
	}

	fmt.Printf("Repair flag written: action=%s path=%s\n", action, flagPath)
	return nil
}

// readAndClearRepairFlag reads and deletes the repair-pending.json file.
// Returns nil if no flag file exists. The file is always deleted after reading,
// even if parsing fails, to prevent infinite restart loops.
func readAndClearRepairFlag(dataDir string) *RepairPending {
	flagPath := filepath.Join(dataDir, repairFlagFile)

	data, err := os.ReadFile(flagPath)
	if err != nil {
		// File doesn't exist — no pending repair
		return nil
	}

	// Delete the file immediately before parsing to prevent infinite restart loops
	if removeErr := os.Remove(flagPath); removeErr != nil {
		fmt.Printf("Warning: failed to remove repair flag file: %v\n", removeErr)
	}

	var pending RepairPending
	if err := json.Unmarshal(data, &pending); err != nil {
		fmt.Printf("Warning: failed to parse repair flag file: %v\n", err)
		return nil
	}

	fmt.Printf("Repair flag detected: action=%s timestamp=%s\n", pending.Action, pending.Timestamp)
	return &pending
}

// GetPendingRepairResult returns and clears the repair result from the last restart.
// Called by the frontend on mount to check if a repair action was completed.
// This is pull-based to avoid timing issues with event emission during splash-to-main transition.
func (a *App) GetPendingRepairResult() *RepairResult {
	a.componentsMu.Lock()
	defer a.componentsMu.Unlock()
	if a.pendingRepair == nil {
		return nil
	}
	result := &RepairResult{
		Action:  a.pendingRepair.Action,
		Success: true,
	}
	a.pendingRepair = nil
	return result
}

// RestartApp is a Wails binding that restarts the application without any repair action.
// Used by the Options dialog to apply settings that require a restart.
func (a *App) RestartApp() error {
	return a.restartApp()
}

// restartApp shuts down the daemon, spawns a new instance, and quits the Wails app.
// This follows the legacy C++ pattern: Interrupt() + PrepareShutdown() → startDetached → quit.
// The node is shut down first to release the DB lock, P2P port, and RPC port before
// the child process starts, preventing resource conflicts.
func (a *App) restartApp() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}

	// Shut down the daemon node first to release DB lock, P2P port (37817), and RPC port (11771).
	// node.Shutdown() is idempotent (sync.Once), so app.shutdown() calling it again is safe.
	if a.node != nil {
		fmt.Println("Repair restart: shutting down daemon node before respawn...")
		a.node.Shutdown()
		fmt.Println("Repair restart: daemon node shutdown complete")
	}

	// Spawn a fully detached child process (no inherited stdio)
	cmd := exec.Command(exePath, os.Args[1:]...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start new process: %w", err)
	}

	fmt.Printf("Spawned new process (PID %d), quitting current instance...\n", cmd.Process.Pid)

	// Quit the current Wails application — triggers OnShutdown -> app.shutdown()
	wailsRuntime.Quit(a.ctx)
	return nil
}
