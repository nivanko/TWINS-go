package config

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"sync"

	"github.com/sirupsen/logrus"
)

// SettingType represents the data type of a configuration setting.
type SettingType int

const (
	TypeBool SettingType = iota
	TypeInt
	TypeInt64
	TypeUint32
	TypeFloat64
	TypeString
	TypeStringSlice
)

var settingTypeNames = map[SettingType]string{
	TypeBool:        "bool",
	TypeInt:         "int",
	TypeInt64:       "int64",
	TypeUint32:      "uint32",
	TypeFloat64:     "float64",
	TypeString:      "string",
	TypeStringSlice: "[]string",
}

func (t SettingType) String() string {
	if name, ok := settingTypeNames[t]; ok {
		return name
	}
	return "unknown"
}

func (t SettingType) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.String())
}

func (t *SettingType) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	for k, v := range settingTypeNames {
		if v == s {
			*t = k
			return nil
		}
	}
	return fmt.Errorf("unknown setting type: %q", s)
}

// Validation defines constraints for a setting value.
type Validation struct {
	Min     *float64 `json:"min,omitempty"`
	Max     *float64 `json:"max,omitempty"`
	Options []string `json:"options,omitempty"` // valid string values (enum)
}

// SettingMeta describes a single configuration setting for both the daemon and GUI.
type SettingMeta struct {
	Key         string      `json:"key"`
	Type        SettingType `json:"type"`
	Default     interface{} `json:"default"`
	Category    string      `json:"category"`
	Label       string      `json:"label"`
	Description string      `json:"description"`
	HotReload   bool        `json:"hotReload"`
	Validation  *Validation `json:"validation,omitempty"`
	CLIFlag     string      `json:"cliFlag,omitempty"`
	EnvVar      string      `json:"envVar,omitempty"`
	Units       string      `json:"units,omitempty"`
	Deprecated  bool        `json:"deprecated,omitempty"`
}

// settingDef is the internal representation that includes accessor functions.
type settingDef struct {
	SettingMeta
	getter func(*Config) interface{}
	setter func(*Config, interface{}) error
}

// ChangeHandler is called when a setting value changes.
type ChangeHandler func(key string, oldValue, newValue interface{})

// ConfigManager is the single authority for all daemon configuration.
// It owns twinsd.yml, supports CLI overrides, and notifies subscribers of changes.
type ConfigManager struct {
	mu             sync.RWMutex
	config         *Config
	registry       map[string]*settingDef
	registryOrder  []string // preserves registration order for GUI display
	categoryOrder  []string
	subscribers    map[string][]ChangeHandler
	cliLocks       map[string]bool
	pendingRestart map[string]bool
	yamlPath       string
	logger         *logrus.Entry
}

// NewConfigManager creates a new ConfigManager for the given YAML path.
func NewConfigManager(yamlPath string, logger *logrus.Entry) *ConfigManager {
	cm := &ConfigManager{
		config:         DefaultConfig(),
		registry:       make(map[string]*settingDef),
		subscribers:    make(map[string][]ChangeHandler),
		cliLocks:       make(map[string]bool),
		pendingRestart: make(map[string]bool),
		yamlPath:       yamlPath,
		logger:         logger,
	}
	registerAllSettings(cm)
	return cm
}

// LoadOrCreate loads twinsd.yml if it exists, or creates a default one with comments.
func (cm *ConfigManager) LoadOrCreate() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if _, err := os.Stat(cm.yamlPath); os.IsNotExist(err) {
		// Create default config file with comments
		cm.config = DefaultConfig()
		if err := cm.generateCommentedYAML(); err != nil {
			return fmt.Errorf("failed to create default config: %w", err)
		}
		if cm.logger != nil {
			cm.logger.Infof("Created default configuration: %s", cm.yamlPath)
		}
		return nil
	}

	// Load existing YAML using the overlay pattern
	cfg, err := LoadConfig(cm.yamlPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	cm.config = cfg

	// Apply environment variable overrides and lock the affected settings so
	// the GUI shows them as read-only (same behaviour as CLI-flag overrides).
	envOverrides := LoadEnvironmentOverrides()
	if len(envOverrides) > 0 {
		if err := applyOverrides(cm.config, envOverrides); err != nil {
			return fmt.Errorf("failed to apply env overrides: %w", err)
		}
		for key := range envOverrides {
			if _, inRegistry := cm.registry[key]; inRegistry {
				cm.cliLocks[key] = true
			}
		}
	}

	return nil
}

// Register adds a setting definition to the registry.
func (cm *ConfigManager) Register(def *settingDef) {
	cm.registry[def.Key] = def
	cm.registryOrder = append(cm.registryOrder, def.Key)
	// Track category order (preserve insertion order, deduplicate)
	for _, cat := range cm.categoryOrder {
		if cat == def.Category {
			return
		}
	}
	cm.categoryOrder = append(cm.categoryOrder, def.Category)
}

// Get returns the current value of a setting by key.
func (cm *ConfigManager) Get(key string) (interface{}, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	def, ok := cm.registry[key]
	if !ok {
		return nil, fmt.Errorf("unknown setting: %s", key)
	}
	return def.getter(cm.config), nil
}

// GetBool returns a boolean setting value. Returns false if key is unknown.
func (cm *ConfigManager) GetBool(key string) bool {
	val, err := cm.Get(key)
	if err != nil {
		return false
	}
	if b, ok := val.(bool); ok {
		return b
	}
	return false
}

// GetInt returns an integer setting value.
func (cm *ConfigManager) GetInt(key string) int {
	val, err := cm.Get(key)
	if err != nil {
		return 0
	}
	if i, ok := val.(int); ok {
		return i
	}
	return 0
}

// GetInt64 returns an int64 setting value.
func (cm *ConfigManager) GetInt64(key string) int64 {
	val, err := cm.Get(key)
	if err != nil {
		return 0
	}
	if i, ok := val.(int64); ok {
		return i
	}
	return 0
}

// GetUint32 returns a uint32 setting value.
func (cm *ConfigManager) GetUint32(key string) uint32 {
	val, err := cm.Get(key)
	if err != nil {
		return 0
	}
	if i, ok := val.(uint32); ok {
		return i
	}
	return 0
}

// GetFloat64 returns a float64 setting value.
func (cm *ConfigManager) GetFloat64(key string) float64 {
	val, err := cm.Get(key)
	if err != nil {
		return 0
	}
	if f, ok := val.(float64); ok {
		return f
	}
	return 0
}

// GetString returns a string setting value.
func (cm *ConfigManager) GetString(key string) string {
	val, err := cm.Get(key)
	if err != nil {
		return ""
	}
	if s, ok := val.(string); ok {
		return s
	}
	return ""
}

// GetStringSlice returns a copy of a string slice setting value.
// A copy is returned so callers cannot mutate the manager's internal config state.
func (cm *ConfigManager) GetStringSlice(key string) []string {
	val, err := cm.Get(key)
	if err != nil {
		return nil
	}
	if s, ok := val.([]string); ok {
		cp := make([]string, len(s))
		copy(cp, s)
		return cp
	}
	return nil
}

// Set updates a setting value with validation and persistence.
// Returns an error if the key is unknown, the value fails validation,
// or the setting is locked by a CLI flag.
func (cm *ConfigManager) Set(key string, value interface{}) error {
	// Lock-Copy-Invoke pattern: hold lock for state mutation, release before callbacks.
	// This prevents double-panic if a subscriber callback panics (see masternode.go:2383).
	handlers, oldValue, coerced, err := cm.setLocked(key, value)
	if err != nil {
		return err
	}

	// Notify subscribers outside the lock (panic-safe).
	// Pass the coerced value (e.g. int64) not the raw JSON value (float64),
	// so subscriber type assertions match the stored type.
	for _, handler := range handlers {
		handler(key, oldValue, coerced)
	}
	return nil
}

// setLocked performs the guarded portion of Set: validation, mutation, persistence.
// Returns the handlers to invoke, the old value, the coerced new value, or an error.
func (cm *ConfigManager) setLocked(key string, value interface{}) ([]ChangeHandler, interface{}, interface{}, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	def, ok := cm.registry[key]
	if !ok {
		return nil, nil, nil, fmt.Errorf("unknown setting: %s", key)
	}

	if cm.cliLocks[key] {
		if def.CLIFlag != "" {
			return nil, nil, nil, fmt.Errorf("setting %q is locked by CLI flag --%s", key, def.CLIFlag)
		}
		if def.EnvVar != "" {
			return nil, nil, nil, fmt.Errorf("setting %q is locked by environment variable %s", key, def.EnvVar)
		}
		return nil, nil, nil, fmt.Errorf("setting %q is locked", key)
	}

	if def.Deprecated {
		return nil, nil, nil, fmt.Errorf("setting %q is deprecated", key)
	}

	// Coerce JSON types (e.g. []interface{} → []string from frontend)
	value = coerceValue(def.Type, value)

	// Validate the value
	if err := cm.validateValue(def, value); err != nil {
		return nil, nil, nil, fmt.Errorf("validation failed for %s: %w", key, err)
	}

	// Get old value for notification
	oldValue := def.getter(cm.config)

	// Apply the new value (recover from type-assertion panics in setters)
	if err := func() (setErr error) {
		defer func() {
			if r := recover(); r != nil {
				setErr = fmt.Errorf("setter panic for %s: %v (value type: %T)", key, r, value)
			}
		}()
		return def.setter(cm.config, value)
	}(); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to set %s: %w", key, err)
	}

	// Persist to YAML
	if err := cm.persistYAML(); err != nil {
		// Rollback on persist failure
		_ = def.setter(cm.config, oldValue)
		return nil, nil, nil, fmt.Errorf("failed to persist config: %w", err)
	}

	// Track restart requirements
	if !def.HotReload {
		cm.pendingRestart[key] = true
	}

	// Copy handlers for invocation outside the lock
	handlers := make([]ChangeHandler, len(cm.subscribers[key]))
	copy(handlers, cm.subscribers[key])

	return handlers, oldValue, value, nil
}

// SetFromCLI sets a value and marks it as locked (overridden by CLI flag).
// CLI-locked settings are read-only in the GUI. The value is coerced and
// validated through the same pipeline as Set() to catch type mismatches early.
func (cm *ConfigManager) SetFromCLI(key string, value interface{}) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	def, ok := cm.registry[key]
	if !ok {
		return fmt.Errorf("unknown setting: %s", key)
	}

	value = coerceValue(def.Type, value)

	if err := cm.validateValue(def, value); err != nil {
		return fmt.Errorf("CLI validation failed for %s: %w", key, err)
	}

	if err := def.setter(cm.config, value); err != nil {
		return fmt.Errorf("failed to set %s from CLI: %w", key, err)
	}

	cm.cliLocks[key] = true
	return nil
}

// IsLocked returns true if the setting is locked by a CLI flag.
func (cm *ConfigManager) IsLocked(key string) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.cliLocks[key]
}

// HasPendingRestart returns true if any changed settings require a restart.
func (cm *ConfigManager) HasPendingRestart() bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return len(cm.pendingRestart) > 0
}

// GetPendingRestartKeys returns the keys of settings changed that need a restart.
func (cm *ConfigManager) GetPendingRestartKeys() []string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	keys := make([]string, 0, len(cm.pendingRestart))
	for k := range cm.pendingRestart {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ClearPendingRestart clears the pending restart flags (called after actual restart).
func (cm *ConfigManager) ClearPendingRestart() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.pendingRestart = make(map[string]bool)
}

// Subscribe registers a handler for changes to a specific setting key.
func (cm *ConfigManager) Subscribe(key string, handler ChangeHandler) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.subscribers[key] = append(cm.subscribers[key], handler)
}

// GetAllMetadata returns metadata for all registered settings, ordered by category.
// This is the JSON-serializable API consumed by the GUI settings dialog.
func (cm *ConfigManager) GetAllMetadata() []SettingMeta {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	// Build a registration-order index so we can preserve insertion order
	// within each category instead of sorting alphabetically by key.
	regIndex := make(map[string]int, len(cm.registryOrder))
	for i, key := range cm.registryOrder {
		regIndex[key] = i
	}

	result := make([]SettingMeta, 0, len(cm.registry))
	for _, key := range cm.registryOrder {
		if def, ok := cm.registry[key]; ok {
			result = append(result, def.SettingMeta)
		}
	}

	// Sort by category order, preserving registration order within each category
	sort.SliceStable(result, func(i, j int) bool {
		ci := cm.categoryIndex(result[i].Category)
		cj := cm.categoryIndex(result[j].Category)
		if ci != cj {
			return ci < cj
		}
		return regIndex[result[i].Key] < regIndex[result[j].Key]
	})

	return result
}

// GetAllValues returns current values and lock states for all settings.
// Used by the GUI to populate the settings dialog.
//
// Sensitive fields (RPC credentials, masternode key) are returned unmasked.
// This is safe because the GUI runs in the same process via Wails (not over
// a network API), and the values already exist in the user's twinsd.yml on
// disk. The frontend handles display-level security via type="password"
// inputs with eye-icon reveal toggles.
func (cm *ConfigManager) GetAllValues() map[string]interface{} {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	values := make(map[string]interface{}, len(cm.registry))
	for key, def := range cm.registry {
		val := def.getter(cm.config)
		values[key] = map[string]interface{}{
			"value":          val,
			"locked":         cm.cliLocks[key],
			"pendingRestart": cm.pendingRestart[key],
		}
	}
	return values
}

// GetCategories returns the ordered list of setting categories (tab names for GUI).
func (cm *ConfigManager) GetCategories() []string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	cats := make([]string, len(cm.categoryOrder))
	copy(cats, cm.categoryOrder)
	return cats
}

// Snapshot returns a deep copy of the current Config for initialization.
// The returned copy is detached from the manager and safe for concurrent use.
func (cm *ConfigManager) Snapshot() *Config {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.config.Clone()
}

// GetConfig returns a reference to the live config.
//
// Deprecated: This returns a pointer that can be read after the lock is released,
// creating a potential race if the config is mutated concurrently. Use Snapshot()
// for a safe, detached deep copy. GetConfig is retained only for initialization
// code that needs to pass *Config to components before any concurrent mutation.
func (cm *ConfigManager) GetConfig() *Config {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.config
}

// YAMLPath returns the path to the configuration file.
func (cm *ConfigManager) YAMLPath() string {
	return cm.yamlPath
}

// categoryIndex returns the sort index for a category.
func (cm *ConfigManager) categoryIndex(cat string) int {
	for i, c := range cm.categoryOrder {
		if c == cat {
			return i
		}
	}
	return len(cm.categoryOrder)
}

// validateValue validates a value against a setting's type and constraints.
func (cm *ConfigManager) validateValue(def *settingDef, value interface{}) error {
	switch def.Type {
	case TypeBool:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("expected bool, got %T", value)
		}
	case TypeInt:
		v, ok := value.(int)
		if !ok {
			// Accept float64 from JSON
			if f, ok := value.(float64); ok {
				v = int(f)
			} else {
				return fmt.Errorf("expected int, got %T", value)
			}
		}
		if def.Validation != nil {
			if def.Validation.Min != nil && float64(v) < *def.Validation.Min {
				return fmt.Errorf("value %d below minimum %.0f", v, *def.Validation.Min)
			}
			if def.Validation.Max != nil && float64(v) > *def.Validation.Max {
				return fmt.Errorf("value %d above maximum %.0f", v, *def.Validation.Max)
			}
		}
	case TypeInt64:
		v, ok := value.(int64)
		if !ok {
			if f, ok := value.(float64); ok {
				v = int64(f)
			} else {
				return fmt.Errorf("expected int64, got %T", value)
			}
		}
		if def.Validation != nil {
			if def.Validation.Min != nil && float64(v) < *def.Validation.Min {
				return fmt.Errorf("value %d below minimum %.0f", v, *def.Validation.Min)
			}
			if def.Validation.Max != nil && float64(v) > *def.Validation.Max {
				return fmt.Errorf("value %d above maximum %.0f", v, *def.Validation.Max)
			}
		}
	case TypeUint32:
		v, ok := value.(uint32)
		if !ok {
			if f, ok := value.(float64); ok {
				v = uint32(f)
			} else {
				return fmt.Errorf("expected uint32, got %T", value)
			}
		}
		if def.Validation != nil {
			if def.Validation.Min != nil && float64(v) < *def.Validation.Min {
				return fmt.Errorf("value %d below minimum %.0f", v, *def.Validation.Min)
			}
			if def.Validation.Max != nil && float64(v) > *def.Validation.Max {
				return fmt.Errorf("value %d above maximum %.0f", v, *def.Validation.Max)
			}
		}
	case TypeFloat64:
		v, ok := value.(float64)
		if !ok {
			return fmt.Errorf("expected float64, got %T", value)
		}
		if def.Validation != nil {
			if def.Validation.Min != nil && v < *def.Validation.Min {
				return fmt.Errorf("value %f below minimum %f", v, *def.Validation.Min)
			}
			if def.Validation.Max != nil && v > *def.Validation.Max {
				return fmt.Errorf("value %f above maximum %f", v, *def.Validation.Max)
			}
		}
	case TypeString:
		s, ok := value.(string)
		if !ok {
			return fmt.Errorf("expected string, got %T", value)
		}
		if def.Validation != nil && len(def.Validation.Options) > 0 {
			found := false
			for _, opt := range def.Validation.Options {
				if s == opt {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("invalid value %q, must be one of: %v", s, def.Validation.Options)
			}
		}
	case TypeStringSlice:
		if _, ok := value.([]string); !ok {
			return fmt.Errorf("expected []string, got %T", value)
		}
	}
	return nil
}

// coerceValue converts JSON-deserialized types to the Go types expected by setters.
// JSON numbers arrive as float64; JSON arrays arrive as []interface{}.
// This function converts them to the Go types that registry setters expect.
func coerceValue(t SettingType, value interface{}) interface{} {
	switch t {
	case TypeInt:
		if f, ok := value.(float64); ok {
			return int(f)
		}
	case TypeInt64:
		if f, ok := value.(float64); ok {
			return int64(f)
		}
	case TypeUint32:
		if f, ok := value.(float64); ok {
			if f < 0 || f > math.MaxUint32 {
				return value // let validateValue report the error
			}
			return uint32(f)
		}
	case TypeFloat64:
		// float64 is already the correct type from JSON
	case TypeStringSlice:
		if _, ok := value.([]string); ok {
			return value // already correct type
		}
		if arr, ok := value.([]interface{}); ok {
			ss := make([]string, 0, len(arr))
			for _, v := range arr {
				if s, ok := v.(string); ok {
					ss = append(ss, s)
				} else {
					return value // let validateValue report the error
				}
			}
			return ss
		}
	}
	return value
}

// persistYAML writes the current config to the YAML file atomically.
// Uses the same commented format as the initial generation so that
// the file stays consistent regardless of which process writes it.
//
// Design note: persistYAML is called on every Set() intentionally.
// GUI settings are changed one-at-a-time via toggle/input, and the user
// expects changes to survive a crash. Batching would risk data loss.
// The atomic write (via generateCommentedYAML) keeps I/O safe.
//
// Caller must hold cm.mu.
func (cm *ConfigManager) persistYAML() error {
	return cm.generateCommentedYAML()
}

// minmax returns validation pointers for min/max constraints.
func minmax(min, max float64) *Validation {
	return &Validation{Min: &min, Max: &max}
}

// minOnly returns validation with only a minimum constraint.
func minOnly(min float64) *Validation {
	return &Validation{Min: &min}
}

// options returns validation for enum-like string settings.
func options(opts ...string) *Validation {
	return &Validation{Options: opts}
}
