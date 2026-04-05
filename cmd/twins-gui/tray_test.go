package main

import (
	"testing"
)

func TestNewTrayManager(t *testing.T) {
	tm := NewTrayManager(nil)
	if tm == nil {
		t.Fatal("NewTrayManager returned nil")
	}
	if tm.IsStarted() {
		t.Error("new TrayManager should not be started")
	}
	if tm.IsWindowHidden() {
		t.Error("new TrayManager should have window visible")
	}
}

func TestTrayManagerWindowHiddenState(t *testing.T) {
	tm := NewTrayManager(nil)

	// Initial state: window visible
	if tm.IsWindowHidden() {
		t.Error("expected window visible initially")
	}

	// Manually set hidden state (simulating HideWindow without Wails context)
	tm.windowHidden.Store(true)
	if !tm.IsWindowHidden() {
		t.Error("expected window hidden after setting flag")
	}

	// Reset
	tm.windowHidden.Store(false)
	if tm.IsWindowHidden() {
		t.Error("expected window visible after clearing flag")
	}
}

func TestTrayManagerStartedState(t *testing.T) {
	tm := NewTrayManager(nil)

	if tm.IsStarted() {
		t.Error("expected not started initially")
	}

	// Simulate started state (without actually calling Start which needs a display)
	tm.started.Store(true)
	if !tm.IsStarted() {
		t.Error("expected started after setting flag")
	}
}

func TestTrayManagerStopIdempotent(t *testing.T) {
	tm := NewTrayManager(nil)

	// Stop on unstarted manager should not panic
	tm.Stop()
	tm.Stop() // Second call should also be safe (sync.Once)
}

func TestTrayManagerToggleWindowLogic(t *testing.T) {
	// Test the toggle logic without Wails context (methods will early-return on nil ctx)
	tm := NewTrayManager(&App{})

	// ToggleWindow when visible: would hide (but early-returns due to nil ctx)
	tm.ToggleWindow()

	// ToggleWindow when hidden: would show (but early-returns due to nil ctx)
	tm.windowHidden.Store(true)
	tm.ToggleWindow()
}

func TestTrayManagerSetVisibleNotStarted(t *testing.T) {
	tm := NewTrayManager(&App{})

	// SetVisible on unstarted manager should not panic
	tm.SetVisible(true)
	tm.SetVisible(false)
	if tm.IsIconHidden() {
		t.Error("iconHidden should be false on unstarted manager")
	}
}

func TestTrayManagerSetVisibleHideSetsFlag(t *testing.T) {
	tm := NewTrayManager(&App{})
	tm.started.Store(true)
	tm.icon = []byte{1, 2, 3}

	// SetVisible(false) should set iconHidden flag
	// (actual systray.SetIcon call skipped since systray not truly initialized)
	tm.SetVisible(false)
	if !tm.IsIconHidden() {
		t.Error("expected iconHidden=true after SetVisible(false)")
	}
	// started should remain true (we don't tear down systray)
	if !tm.IsStarted() {
		t.Error("expected started=true (transparent icon, not stopped)")
	}
}

func TestTrayManagerSetVisibleShowRestoresFlag(t *testing.T) {
	tm := NewTrayManager(&App{})
	tm.started.Store(true)
	tm.icon = []byte{1, 2, 3}
	tm.iconHidden.Store(true)

	// SetVisible(true) should clear iconHidden flag
	tm.SetVisible(true)
	if tm.IsIconHidden() {
		t.Error("expected iconHidden=false after SetVisible(true)")
	}
}

func TestTrayManagerSetVisibleShowNoop(t *testing.T) {
	tm := NewTrayManager(&App{})
	tm.started.Store(true)

	// SetVisible(true) when icon already visible should be a no-op
	tm.SetVisible(true)
	if tm.IsIconHidden() {
		t.Error("SetVisible(true) on visible icon should remain visible")
	}
}

func TestTrayManagerSetVisibleHideNoop(t *testing.T) {
	tm := NewTrayManager(&App{})
	tm.started.Store(true)
	tm.iconHidden.Store(true)

	// SetVisible(false) when icon already hidden should be a no-op
	tm.SetVisible(false)
	if !tm.IsIconHidden() {
		t.Error("SetVisible(false) on hidden icon should remain hidden")
	}
}

func TestShouldStartTray(t *testing.T) {
	// shouldStartTray is platform-specific:
	// - macOS/Linux: true (tray supported)
	// - Windows: false (tray disabled)
	// On the current platform (macOS or Linux in CI), it should return true.
	result := shouldStartTray()
	if !result {
		t.Error("shouldStartTray() should return true on macOS/Linux")
	}
}
