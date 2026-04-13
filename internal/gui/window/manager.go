package window

import (
	"context"
	"fmt"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// State represents the window state
type State string

const (
	// Window states
	// StateIntro is used internally via PrepareWindowState() for first-run window sizing.
	// No public handler - frontend shows IntroDialog component in this window size.
	StateIntro    State = "intro"    // Initial setup dialog (674x363)
	StateSplash   State = "splash"   // Splash screen during loading (480x550)
	StateMain     State = "main"     // Main application window (1024x768)
	StateShutdown State = "shutdown" // Shutdown dialog (480x550)
)

// WindowPreset defines window dimensions and properties
type WindowPreset struct {
	Width      int  `json:"width"`
	Height     int  `json:"height"`
	MinWidth   int  `json:"minWidth"`
	MinHeight  int  `json:"minHeight"`
	MaxWidth   int  `json:"maxWidth"`
	MaxHeight  int  `json:"maxHeight"`
	Resizable  bool `json:"resizable"`
	Centered   bool `json:"centered"`
	AlwaysOnTop bool `json:"alwaysOnTop"`
}

// Manager handles window state management
type Manager struct {
	mu            sync.RWMutex
	ctx           context.Context
	currentState  State
	previousState State
	presets       map[State]WindowPreset
	customTitle   string // Custom window title from -windowtitle flag
	isFullscreen  bool
	isMaximized   bool
	isMinimized   bool
}

// NewManager creates a new window manager
func NewManager(ctx context.Context) *Manager {
	return &Manager{
		ctx:          ctx,
		currentState: "", // Start with no state - let frontend set it
		presets:      getDefaultPresets(),
	}
}

// SetSplashHeightExtra adds extra pixels to the splash and shutdown window heights.
// Used on Windows where Wails window dimensions include the title bar,
// reducing the content area below the designed 550px.
// Both windows use the same 480x550 splash layout and need identical compensation.
func (m *Manager) SetSplashHeightExtra(extra int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, state := range []State{StateSplash, StateShutdown} {
		if preset, ok := m.presets[state]; ok {
			preset.Height += extra
			preset.MinHeight += extra
			preset.MaxHeight += extra
			m.presets[state] = preset
		}
	}
}

// getDefaultPresets returns the default window presets for each state
func getDefaultPresets() map[State]WindowPreset {
	return map[State]WindowPreset{
		StateIntro: {
			Width:       674,
			Height:      363,
			MinWidth:    674,
			MinHeight:   363,
			MaxWidth:    674,
			MaxHeight:   363,
			Resizable:   false,
			Centered:    true,
			AlwaysOnTop: false,
		},
		StateSplash: {
			Width:       480,
			Height:      550,
			MinWidth:    480,
			MinHeight:   550,
			MaxWidth:    480,
			MaxHeight:   550,
			Resizable:   false,
			Centered:    true,
			AlwaysOnTop: true,
		},
		StateMain: {
			Width:       1024,
			Height:      768,
			MinWidth:    800,
			MinHeight:   600,
			MaxWidth:    0, // No maximum
			MaxHeight:   0, // No maximum
			Resizable:   true,
			Centered:    true,
			AlwaysOnTop: false,
		},
		StateShutdown: {
			Width:       480,
			Height:      550,
			MinWidth:    480,
			MinHeight:   550,
			MaxWidth:    480,
			MaxHeight:   550,
			Resizable:   false,
			Centered:    true,
			AlwaysOnTop: true,
		},
	}
}

// TransitionTo transitions the window to a new state
func (m *Manager) TransitionTo(newState State) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.currentState == newState {
		return nil // Already in this state
	}

	preset, exists := m.presets[newState]
	if !exists {
		return fmt.Errorf("unknown window state: %s", newState)
	}

	// Apply the new preset
	if err := m.applyPreset(preset); err != nil {
		return fmt.Errorf("failed to apply preset for %s: %w", newState, err)
	}

	// Update state
	m.previousState = m.currentState
	m.currentState = newState

	// Set window title (uses custom title if set)
	runtime.WindowSetTitle(m.ctx, m.getTitle(newState))

	return nil
}

// PrepareWindowState prepares the window size and title for a state without showing it
// This is used during startup to set the correct size before the window is shown
func (m *Manager) PrepareWindowState(newState State) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.currentState == newState {
		return nil // Already prepared for this state
	}

	// Get the preset for the new state
	preset, exists := m.presets[newState]
	if !exists {
		return fmt.Errorf("no preset found for state: %s", newState)
	}

	// Set window size (but don't show)
	runtime.WindowSetSize(m.ctx, preset.Width, preset.Height)

	// Center window if requested
	if preset.Centered {
		runtime.WindowCenter(m.ctx)
	}

	// Set window title (uses custom title if set)
	runtime.WindowSetTitle(m.ctx, m.getTitle(newState))

	// Update current state (but don't show window or emit events yet)
	m.currentState = newState

	return nil
}

// applyPreset applies window preset settings
func (m *Manager) applyPreset(preset WindowPreset) error {
	// First, remove all size constraints to allow resizing
	runtime.WindowSetMinSize(m.ctx, 1, 1)
	runtime.WindowSetMaxSize(m.ctx, 9999, 9999)

	// Set the window size
	runtime.WindowSetSize(m.ctx, preset.Width, preset.Height)

	// Center window if requested (do this BEFORE constraints)
	if preset.Centered {
		runtime.WindowCenter(m.ctx)
	}

	// Verify the size was applied
	actualWidth, actualHeight := runtime.WindowGetSize(m.ctx)

	// Apply the constraints after the window has been resized and positioned
	// For fixed-size windows (where min == max == desired size), only set if size is correct
	if preset.MinWidth == preset.MaxWidth && preset.MinHeight == preset.MaxHeight {
		// Fixed size window - only set constraints if we got the right size
		if actualWidth == preset.Width && actualHeight == preset.Height {
			runtime.WindowSetMinSize(m.ctx, preset.MinWidth, preset.MinHeight)
			runtime.WindowSetMaxSize(m.ctx, preset.MaxWidth, preset.MaxHeight)
		}
	} else {
		// Resizable window - set min/max normally
		if preset.MinWidth > 0 && preset.MinHeight > 0 {
			runtime.WindowSetMinSize(m.ctx, preset.MinWidth, preset.MinHeight)
		}
		if preset.MaxWidth > 0 && preset.MaxHeight > 0 {
			runtime.WindowSetMaxSize(m.ctx, preset.MaxWidth, preset.MaxHeight)
		}
	}

	// Set always on top
	runtime.WindowSetAlwaysOnTop(m.ctx, preset.AlwaysOnTop)

	// Ensure window is shown after applying all settings
	runtime.WindowShow(m.ctx)

	return nil
}

// GetCurrentState returns the current window state
func (m *Manager) GetCurrentState() State {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.currentState
}

// GetPreviousState returns the previous window state
func (m *Manager) GetPreviousState() State {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.previousState
}

// SetFullscreen sets the window to fullscreen mode
func (m *Manager) SetFullscreen(fullscreen bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if fullscreen {
		runtime.WindowFullscreen(m.ctx)
		m.isFullscreen = true
	} else {
		runtime.WindowUnfullscreen(m.ctx)
		m.isFullscreen = false
	}
}

// ToggleFullscreen toggles fullscreen mode
func (m *Manager) ToggleFullscreen() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.isFullscreen {
		runtime.WindowUnfullscreen(m.ctx)
		m.isFullscreen = false
	} else {
		runtime.WindowFullscreen(m.ctx)
		m.isFullscreen = true
	}
}

// Maximize maximizes the window
func (m *Manager) Maximize() {
	m.mu.Lock()
	defer m.mu.Unlock()

	runtime.WindowMaximise(m.ctx)
	m.isMaximized = true
	m.isMinimized = false
}

// Minimize minimizes the window
func (m *Manager) Minimize() {
	m.mu.Lock()
	defer m.mu.Unlock()

	runtime.WindowMinimise(m.ctx)
	m.isMinimized = true
	m.isMaximized = false
}

// Restore restores the window from minimized or maximized state
func (m *Manager) Restore() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.isMinimized {
		runtime.WindowUnminimise(m.ctx)
		m.isMinimized = false
	}

	if m.isMaximized {
		runtime.WindowUnmaximise(m.ctx)
		m.isMaximized = false
	}
}

// Center centers the window on the screen
func (m *Manager) Center() {
	runtime.WindowCenter(m.ctx)
}

// SetSize sets the window size
func (m *Manager) SetSize(width, height int) {
	runtime.WindowSetSize(m.ctx, width, height)
}

// SetPosition sets the window position
func (m *Manager) SetPosition(x, y int) {
	runtime.WindowSetPosition(m.ctx, x, y)
}

// SetTitle sets the window title
func (m *Manager) SetTitle(title string) {
	runtime.WindowSetTitle(m.ctx, title)
}

// SetCustomTitle sets a custom window title that overrides default titles
func (m *Manager) SetCustomTitle(title string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.customTitle = title
}

// getTitle returns the appropriate title for the given state,
// using customTitle if set, otherwise using default titles
func (m *Manager) getTitle(state State) string {
	if m.customTitle != "" {
		return m.customTitle
	}
	switch state {
	case StateIntro:
		return "Welcome"
	case StateSplash:
		return "TWINS Core"
	case StateMain:
		return "TWINS Core - Wallet"
	case StateShutdown:
		return "TWINS Core - Wallet"
	default:
		return "TWINS Core"
	}
}

// Hide hides the window
func (m *Manager) Hide() {
	runtime.WindowHide(m.ctx)
}

// Show shows the window
func (m *Manager) Show() {
	runtime.WindowShow(m.ctx)
}

// IsFullscreen returns whether the window is in fullscreen mode
func (m *Manager) IsFullscreen() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.isFullscreen
}

// IsMaximized returns whether the window is maximized
func (m *Manager) IsMaximized() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.isMaximized
}

// IsMinimized returns whether the window is minimized
func (m *Manager) IsMinimized() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.isMinimized
}

// GetPreset returns the preset for a given state
func (m *Manager) GetPreset(state State) (WindowPreset, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	preset, exists := m.presets[state]
	return preset, exists
}

// UpdatePreset updates the preset for a given state
func (m *Manager) UpdatePreset(state State, preset WindowPreset) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.presets[state] = preset
}

// GetWindowInfo returns current window information
func (m *Manager) GetWindowInfo() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	info := map[string]interface{}{
		"state":        m.currentState,
		"isFullscreen": m.isFullscreen,
		"isMaximized":  m.isMaximized,
		"isMinimized":  m.isMinimized,
	}

	// Get current size
	w, h := runtime.WindowGetSize(m.ctx)
	info["width"] = w
	info["height"] = h

	// Get current position
	x, y := runtime.WindowGetPosition(m.ctx)
	info["x"] = x
	info["y"] = y

	return info
}

// SetAlwaysOnTop sets whether the window should always be on top
func (m *Manager) SetAlwaysOnTop(onTop bool) {
	runtime.WindowSetAlwaysOnTop(m.ctx, onTop)
}

// SetResizable sets whether the window can be resized
func (m *Manager) SetResizable(resizable bool) {
	// Note: Wails doesn't directly support changing resizable at runtime
	// This would need to be handled at window creation
	// For now, we'll just track the state
	m.mu.Lock()
	defer m.mu.Unlock()

	if preset, exists := m.presets[m.currentState]; exists {
		preset.Resizable = resizable
		m.presets[m.currentState] = preset
	}
}