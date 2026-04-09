package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/twins-dev/twins-core/internal/gui/constants"
	"github.com/twins-dev/twins-core/internal/gui/utils"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// formatDisplayVersion returns a clean version string for GUI display (e.g. "4.0.12" or "4.0.0-beta.1")
func formatDisplayVersion(v *utils.Version) string {
	base := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Prerelease != "" {
		return base + "-" + v.Prerelease
	}
	return base
}

// ==========================================
// Utility Methods
// ==========================================

// CopyToClipboard copies text to the system clipboard using Wails runtime
func (a *App) CopyToClipboard(text string) error {
	if a.ctx == nil {
		return fmt.Errorf("application context not initialized")
	}
	runtime.ClipboardSetText(a.ctx, text)
	return nil
}

// GetWalletVersion returns the wallet version information
func (a *App) GetWalletVersion() map[string]string {
	network := "mainnet"
	if a.configService != nil {
		network = a.configService.GetNetwork()
	}

	// Load version from internal/cli/version.go
	version := utils.GetVersionOrDefault()

	return map[string]string{
		"version":  formatDisplayVersion(version),
		"build":    version.Build,
		"protocol": constants.ProtocolVersion,
		"network":  network,
		"codename": version.Codename,
	}
}

// BackupWallet opens a native save dialog and copies the wallet file to the selected location
func (a *App) BackupWallet() (bool, error) {
	if a.ctx == nil {
		return false, fmt.Errorf("application context not initialized")
	}

	a.componentsMu.RLock()
	w := a.wallet
	a.componentsMu.RUnlock()

	if w == nil {
		return false, fmt.Errorf("wallet not initialized")
	}

	defaultFilename := fmt.Sprintf("wallet-backup-%s.dat", time.Now().Format("2006-01-02"))

	filePath, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		DefaultFilename: defaultFilename,
		Title:           "Backup Wallet",
		Filters: []runtime.FileFilter{
			{
				DisplayName: "Wallet Data (*.dat)",
				Pattern:     "*.dat",
			},
		},
	})
	if err != nil {
		return false, fmt.Errorf("failed to open save dialog: %w", err)
	}

	// User cancelled the dialog
	if filePath == "" {
		return false, nil
	}

	filePath = filepath.Clean(filePath)

	// Ensure .dat extension
	if filepath.Ext(filePath) != ".dat" {
		filePath += ".dat"
	}

	if err := w.BackupWallet(filePath); err != nil {
		return false, fmt.Errorf("wallet backup failed: %w", err)
	}

	return true, nil
}

// SaveCSVFile opens a save file dialog and writes CSV content to the selected file
func (a *App) SaveCSVFile(content string, defaultFilename string, title string) (bool, error) {
	if a.ctx == nil {
		return false, fmt.Errorf("application context not initialized")
	}

	// Use default title if not provided
	if title == "" {
		title = "Export CSV"
	}

	// Open save file dialog
	filePath, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		DefaultFilename: defaultFilename,
		Title:           title,
		Filters: []runtime.FileFilter{
			{
				DisplayName: "CSV Files (*.csv)",
				Pattern:     "*.csv",
			},
		},
	})
	if err != nil {
		return false, fmt.Errorf("failed to open save dialog: %w", err)
	}

	// User cancelled the dialog
	if filePath == "" {
		return false, nil
	}

	// Normalize path for security
	filePath = filepath.Clean(filePath)

	// Ensure .csv extension
	if filepath.Ext(filePath) != ".csv" {
		filePath += ".csv"
	}

	// Write the content to file with restrictive permissions (owner read/write only)
	// CSV exports may contain sensitive wallet data (addresses, amounts, labels)
	err = os.WriteFile(filePath, []byte(content), 0600)
	if err != nil {
		return false, fmt.Errorf("failed to write file: %w", err)
	}

	return true, nil
}

// SaveQRImage opens a native save dialog and writes a PNG decoded from
// base64 data to the chosen path. Accepts either a raw base64 string or a
// full `data:image/png;base64,...` data URL.
//
// Returns (true, nil) if the file was saved, (false, nil) if the user
// cancelled the dialog, and (false, error) on any failure.
func (a *App) SaveQRImage(pngBase64 string, defaultFilename string) (bool, error) {
	if a.ctx == nil {
		return false, fmt.Errorf("application context not initialized")
	}

	// Strip the data URL prefix if present (e.g. "data:image/png;base64,")
	if strings.HasPrefix(pngBase64, "data:") {
		if idx := strings.Index(pngBase64, ","); idx != -1 {
			pngBase64 = pngBase64[idx+1:]
		}
	}

	pngData, err := base64.StdEncoding.DecodeString(pngBase64)
	if err != nil {
		return false, fmt.Errorf("invalid base64 PNG data: %w", err)
	}

	if defaultFilename == "" {
		defaultFilename = fmt.Sprintf("twins-qr-%s.png", time.Now().Format("2006-01-02"))
	}

	filePath, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		DefaultFilename: defaultFilename,
		Title:           "Save QR Code Image",
		Filters: []runtime.FileFilter{
			{
				DisplayName: "PNG Images (*.png)",
				Pattern:     "*.png",
			},
		},
	})
	if err != nil {
		return false, fmt.Errorf("failed to open save dialog: %w", err)
	}

	// User cancelled the dialog
	if filePath == "" {
		return false, nil
	}

	filePath = filepath.Clean(filePath)
	if filepath.Ext(filePath) != ".png" {
		filePath += ".png"
	}

	// PNG images are not sensitive (they encode the same address/URI already
	// displayed in the UI), so use standard file permissions.
	if err := os.WriteFile(filePath, pngData, 0644); err != nil {
		return false, fmt.Errorf("failed to write PNG file: %w", err)
	}

	return true, nil
}
