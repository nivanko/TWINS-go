package daemon

import (
	"path/filepath"
	"testing"

	"github.com/sirupsen/logrus"

	"github.com/twins-dev/twins-core/internal/config"
)

func TestWireConfigSubscribers_NilManager(t *testing.T) {
	// WireConfigSubscribers should not panic when ConfigManager is nil.
	n := &Node{
		logger: logrus.NewEntry(logrus.StandardLogger()),
	}
	// Should be a no-op, no panic.
	n.WireConfigSubscribers()
}

func TestWireConfigSubscribers_LoggingLevel(t *testing.T) {
	// Create a temp ConfigManager with a temp YAML path.
	tmpDir := t.TempDir()
	yamlPath := filepath.Join(tmpDir, "twinsd.yml")
	logger := logrus.NewEntry(logrus.StandardLogger())

	cm := config.NewConfigManager(yamlPath, logger)
	if err := cm.LoadOrCreate(); err != nil {
		t.Fatalf("LoadOrCreate failed: %v", err)
	}

	n := &Node{
		ConfigManager: cm,
		logger:        logger,
	}
	n.WireConfigSubscribers()

	// Set log level to debug via ConfigManager — subscriber should apply it.
	originalLevel := logrus.GetLevel()
	defer logrus.SetLevel(originalLevel)

	if err := cm.Set("logging.level", "debug"); err != nil {
		t.Fatalf("Set logging.level failed: %v", err)
	}

	if logrus.GetLevel() != logrus.DebugLevel {
		t.Errorf("expected log level debug, got %s", logrus.GetLevel())
	}

	// Change to warn level.
	if err := cm.Set("logging.level", "warn"); err != nil {
		t.Fatalf("Set logging.level failed: %v", err)
	}

	if logrus.GetLevel() != logrus.WarnLevel {
		t.Errorf("expected log level warn, got %s", logrus.GetLevel())
	}
}

func TestWireConfigSubscribers_StakingEnabled(t *testing.T) {
	// Verify the subscriber is registered and fires on staking.enabled change.
	// We can't fully test StartStaking/StopStaking without a consensus engine,
	// but we verify the subscriber doesn't panic when Node has no consensus.
	tmpDir := t.TempDir()
	yamlPath := filepath.Join(tmpDir, "twinsd.yml")
	logger := logrus.NewEntry(logrus.StandardLogger())

	cm := config.NewConfigManager(yamlPath, logger)
	if err := cm.LoadOrCreate(); err != nil {
		t.Fatalf("LoadOrCreate failed: %v", err)
	}

	n := &Node{
		ConfigManager: cm,
		logger:        logger,
	}
	n.WireConfigSubscribers()

	// Setting staking.enabled to true without consensus engine should log a warning, not panic.
	err := cm.Set("staking.enabled", true)
	if err != nil {
		t.Fatalf("Set staking.enabled failed: %v", err)
	}

	// Setting it back to false should also not panic.
	err = cm.Set("staking.enabled", false)
	if err != nil {
		t.Fatalf("Set staking.enabled=false failed: %v", err)
	}
}

func TestWireConfigSubscribers_MasternodeDebug_NilManager(t *testing.T) {
	// Verify the masternode.debug subscriber gracefully no-ops when Node.Masternode
	// is nil instead of panicking. This is the path the GUI hits during early
	// startup if a config change races with InitWallet/LoadMasternodeCache.
	tmpDir := t.TempDir()
	yamlPath := filepath.Join(tmpDir, "twinsd.yml")
	logger := logrus.NewEntry(logrus.StandardLogger())

	cm := config.NewConfigManager(yamlPath, logger)
	if err := cm.LoadOrCreate(); err != nil {
		t.Fatalf("LoadOrCreate failed: %v", err)
	}

	n := &Node{
		ConfigManager: cm,
		logger:        logger,
		Config:        NodeConfig{DataDir: tmpDir},
	}
	n.WireConfigSubscribers()

	// Toggling either way must not panic when Masternode manager is nil.
	if err := cm.Set("masternode.debug", true); err != nil {
		t.Fatalf("Set masternode.debug=true failed: %v", err)
	}
	if err := cm.Set("masternode.debug", false); err != nil {
		t.Fatalf("Set masternode.debug=false failed: %v", err)
	}

	// DebugCollector must remain nil — the subscriber bails out before constructing
	// a collector when there is no Masternode manager to wire it into.
	if dc := n.DebugCollector.Load(); dc != nil {
		t.Errorf("expected DebugCollector to remain nil when Masternode manager is nil, got %v", dc)
	}
}

func TestMasternodeDebugSetting_IsHotReloadable(t *testing.T) {
	// Lock in the registry contract: masternode.debug must be hot-reloadable so
	// the subscriber actually fires on Set(). debugMaxMB / debugMaxFiles must
	// remain restart-only (changing the rotation policy at runtime is out of scope).
	tmpDir := t.TempDir()
	yamlPath := filepath.Join(tmpDir, "twinsd.yml")
	logger := logrus.NewEntry(logrus.StandardLogger())

	cm := config.NewConfigManager(yamlPath, logger)
	if err := cm.LoadOrCreate(); err != nil {
		t.Fatalf("LoadOrCreate failed: %v", err)
	}

	meta := cm.GetAllMetadata()
	want := map[string]bool{
		"masternode.debug":         true,
		"masternode.debugMaxMB":    false,
		"masternode.debugMaxFiles": false,
	}
	for _, m := range meta {
		if expected, tracked := want[m.Key]; tracked {
			if m.HotReload != expected {
				t.Errorf("%s: HotReload=%v, want %v", m.Key, m.HotReload, expected)
			}
			delete(want, m.Key)
		}
	}
	for k := range want {
		t.Errorf("setting %s not found in metadata", k)
	}
}
