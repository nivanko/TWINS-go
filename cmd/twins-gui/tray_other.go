//go:build !darwin && !windows

package main

// dispatchSystrayToMainThread calls startFn directly on Linux.
// systray does not require main thread dispatch on Linux.
func dispatchSystrayToMainThread(startFn func()) {
	startFn()
}

// hideDockIcon is a no-op on Linux.
func hideDockIcon() {}

// showDockIcon is a no-op on Linux.
func showDockIcon() {}

// shouldStartTray returns true on Linux where tray is supported.
func shouldStartTray() bool { return true }

// splashHeightExtra returns 0 on Linux — Wails uses content dimensions.
func splashHeightExtra() int { return 0 }

// setTaskbarIcon is a no-op on Linux — icon set via Linux.Icon option.
func setTaskbarIcon() {}

// configureWindowsTaskbarIdentity is a no-op on Linux.
func configureWindowsTaskbarIdentity() {}
