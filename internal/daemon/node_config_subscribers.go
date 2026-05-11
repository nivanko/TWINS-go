package daemon

import (
	"fmt"

	"github.com/sirupsen/logrus"

	"github.com/twins-dev/twins-core/internal/masternode/debug"
	"github.com/twins-dev/twins-core/internal/wallet"
)

// WireConfigSubscribers registers change handlers on the ConfigManager so that
// hot-reloadable settings take effect at runtime without a daemon restart.
// Safe to call when ConfigManager is nil (no-op).
func (n *Node) WireConfigSubscribers() {
	if n.ConfigManager == nil {
		return
	}

	n.logger.Debug("Wiring ConfigManager subscribers for hot-reload")

	// staking.enabled — toggle staking on/off at runtime
	n.ConfigManager.Subscribe("staking.enabled", func(_ string, _, newValue interface{}) {
		enabled, ok := newValue.(bool)
		if !ok {
			return
		}
		if n.Consensus == nil {
			n.logger.Warn("Cannot update staking state: consensus engine not initialized")
			return
		}
		if enabled {
			// If wallet is locked, defer staking to the onUnlockCallback registered in InitWallet.
			if n.Wallet != nil && n.Wallet.IsEncrypted() && n.Wallet.IsLocked() {
				n.logger.Debug("Staking enabled via config change (wallet locked, will start on unlock)")
			} else if err := n.StartStaking(); err != nil {
				n.logger.WithError(err).Warn("Failed to start staking via config change")
			} else {
				n.logger.Info("Staking enabled via config change")
			}
		} else {
			n.StopStaking()
			n.logger.Info("Staking disabled via config change")
		}
	})

	// staking.reserveBalance — update wallet reserve balance
	n.ConfigManager.Subscribe("staking.reserveBalance", func(_ string, _, newValue interface{}) {
		amount, ok := newValue.(int64)
		if !ok {
			n.logger.WithField("type", fmt.Sprintf("%T", newValue)).Warn("staking.reserveBalance: unexpected value type from ConfigManager")
			return
		}
		if n.Wallet == nil {
			n.logger.Warn("Cannot update reserve balance: wallet not initialized")
			return
		}
		enabled := amount > 0
		if err := n.Wallet.SetReserveBalance(enabled, amount); err != nil {
			n.logger.WithError(err).Warn("Failed to update reserve balance via config change")
		} else {
			n.logger.WithField("amount", amount).Info("Reserve balance updated via config change")
		}
	})

	// wallet.payTxFee — update transaction fee
	n.ConfigManager.Subscribe("wallet.payTxFee", func(_ string, _, newValue interface{}) {
		fee, ok := newValue.(int64)
		if !ok {
			n.logger.WithField("type", fmt.Sprintf("%T", newValue)).Warn("wallet.payTxFee: unexpected value type from ConfigManager")
			return
		}
		if n.Wallet == nil {
			n.logger.Warn("Cannot update transaction fee: wallet not initialized")
			return
		}
		// 0 means "reset to dynamic fee" (legacy -paytxfee=0 semantic). Rather than
		// skipping the call (which would leave a previously-set fee in place), reset to
		// wallet.DefaultFeePerKB so the GUI can revert to the default after setting a
		// custom fee. Calling SetTransactionFee(0) would produce zero-fee transactions.
		if fee == 0 {
			fee = wallet.DefaultFeePerKB
		}
		if err := n.Wallet.SetTransactionFee(fee); err != nil {
			n.logger.WithError(err).Warn("Failed to update transaction fee via config change")
		} else {
			n.logger.WithField("feePerKB", fee).Info("Transaction fee updated via config change")
		}
	})

	// logging.level — update global log level at runtime
	n.ConfigManager.Subscribe("logging.level", func(_ string, _, newValue interface{}) {
		levelStr, ok := newValue.(string)
		if !ok {
			return
		}
		level, err := logrus.ParseLevel(levelStr)
		if err != nil {
			n.logger.WithField("level", levelStr).Warn("Invalid log level in config change, ignoring")
			return
		}
		logrus.SetLevel(level)
		n.logger.WithField("level", level.String()).Info("Log level updated via config change")
	})

	// logging.format — switch between text and JSON formatter at runtime
	n.ConfigManager.Subscribe("logging.format", func(_ string, _, newValue interface{}) {
		format, ok := newValue.(string)
		if !ok {
			return
		}
		switch format {
		case "json":
			logrus.SetFormatter(&logrus.JSONFormatter{TimestampFormat: "2006-01-02T15:04:05.000Z"})
		default: // "text" or unrecognised
			logrus.SetFormatter(&logrus.TextFormatter{FullTimestamp: true, TimestampFormat: "2006-01-02 15:04:05"})
		}
		n.logger.WithField("format", format).Info("Logging format updated via config change")
	})

	// wallet.minTxFee — update minimum transaction fee threshold
	n.ConfigManager.Subscribe("wallet.minTxFee", func(_ string, _, newValue interface{}) {
		fee, ok := newValue.(int64)
		if !ok {
			n.logger.WithField("type", fmt.Sprintf("%T", newValue)).Warn("wallet.minTxFee: unexpected value type from ConfigManager")
			return
		}
		if n.Wallet == nil {
			n.logger.Warn("Cannot update minTxFee: wallet not initialized")
			return
		}
		if err := n.Wallet.SetMinTxFee(fee); err != nil {
			n.logger.WithError(err).Warn("Failed to update minTxFee via config change")
		} else {
			n.logger.WithField("minTxFee", fee).Info("MinTxFee updated via config change")
		}
	})

	// wallet.maxTxFee — update maximum transaction fee cap
	n.ConfigManager.Subscribe("wallet.maxTxFee", func(_ string, _, newValue interface{}) {
		fee, ok := newValue.(int64)
		if !ok {
			n.logger.WithField("type", fmt.Sprintf("%T", newValue)).Warn("wallet.maxTxFee: unexpected value type from ConfigManager")
			return
		}
		if n.Wallet == nil {
			n.logger.Warn("Cannot update maxTxFee: wallet not initialized")
			return
		}
		if err := n.Wallet.SetMaxTxFee(fee); err != nil {
			n.logger.WithError(err).Warn("Failed to update maxTxFee via config change")
		} else {
			n.logger.WithField("maxTxFee", fee).Info("MaxTxFee updated via config change")
		}
	})

	// wallet.txConfirmTarget — update confirmation target for fee estimation
	n.ConfigManager.Subscribe("wallet.txConfirmTarget", func(_ string, _, newValue interface{}) {
		target, ok := newValue.(int)
		if !ok {
			n.logger.WithField("type", fmt.Sprintf("%T", newValue)).Warn("wallet.txConfirmTarget: unexpected value type from ConfigManager")
			return
		}
		if n.Wallet == nil {
			n.logger.Warn("Cannot update txConfirmTarget: wallet not initialized")
			return
		}
		if err := n.Wallet.SetTxConfirmTarget(target); err != nil {
			n.logger.WithError(err).Warn("Failed to update txConfirmTarget via config change")
		} else {
			n.logger.WithField("txConfirmTarget", target).Info("TxConfirmTarget updated via config change")
		}
	})

	// wallet.spendZeroConfChange — control spending of unconfirmed change outputs
	n.ConfigManager.Subscribe("wallet.spendZeroConfChange", func(_ string, _, newValue interface{}) {
		v, ok := newValue.(bool)
		if !ok {
			return
		}
		if n.Wallet == nil {
			n.logger.Warn("Cannot update spendZeroConfChange: wallet not initialized")
			return
		}
		if err := n.Wallet.SetSpendZeroConfChange(v); err != nil {
			n.logger.WithError(err).Warn("Failed to update spendZeroConfChange via config change")
		} else {
			n.logger.WithField("spendZeroConfChange", v).Info("SpendZeroConfChange updated via config change")
		}
	})

	// wallet.createWalletBackups — update auto-backup count (0 to disable)
	n.ConfigManager.Subscribe("wallet.createWalletBackups", func(_ string, _, newValue interface{}) {
		count, ok := newValue.(int)
		if !ok {
			n.logger.WithField("type", fmt.Sprintf("%T", newValue)).Warn("wallet.createWalletBackups: unexpected value type from ConfigManager")
			return
		}
		if n.Wallet == nil {
			n.logger.Warn("Cannot update createWalletBackups: wallet not initialized")
			return
		}
		if err := n.Wallet.SetCreateWalletBackups(count); err != nil {
			n.logger.WithError(err).Warn("Failed to update createWalletBackups via config change")
		} else {
			n.logger.WithField("createWalletBackups", count).Info("CreateWalletBackups updated via config change")
		}
	})

	// wallet.backupPath — update backup directory path
	n.ConfigManager.Subscribe("wallet.backupPath", func(_ string, _, newValue interface{}) {
		path, ok := newValue.(string)
		if !ok {
			return
		}
		if n.Wallet == nil {
			n.logger.Warn("Cannot update backupPath: wallet not initialized")
			return
		}
		if err := n.Wallet.SetBackupPath(path); err != nil {
			n.logger.WithError(err).Warn("Failed to update backupPath via config change")
		} else {
			n.logger.WithField("backupPath", path).Info("BackupPath updated via config change")
		}
	})

	// wallet.autoCombine — toggle auto-combine on/off
	n.ConfigManager.Subscribe("wallet.autoCombine", func(_ string, _, newValue interface{}) {
		enabled, ok := newValue.(bool)
		if !ok {
			return
		}
		if n.Wallet == nil {
			return
		}
		_, target, cooldown := n.Wallet.GetAutoCombineConfig()
		n.Wallet.SetAutoCombineConfig(enabled, target, cooldown)
		n.logger.WithField("enabled", enabled).Info("Auto-combine updated via config change")
	})

	// wallet.autoCombineTarget — update auto-combine target (config stores TWINS, wallet uses satoshis)
	n.ConfigManager.Subscribe("wallet.autoCombineTarget", func(_ string, _, newValue interface{}) {
		targetTWINS, ok := newValue.(int64)
		if !ok {
			return
		}
		if n.Wallet == nil {
			return
		}
		targetSatoshis := targetTWINS * 100_000_000
		enabled, _, cooldown := n.Wallet.GetAutoCombineConfig()
		n.Wallet.SetAutoCombineConfig(enabled, targetSatoshis, cooldown)
		n.logger.WithField("target_twins", targetTWINS).Info("Auto-combine target updated via config change")
	})

	// wallet.autoCombineCooldown — update auto-combine cooldown
	n.ConfigManager.Subscribe("wallet.autoCombineCooldown", func(_ string, _, newValue interface{}) {
		cooldown, ok := newValue.(int)
		if !ok {
			return
		}
		if n.Wallet == nil {
			return
		}
		enabled, target, _ := n.Wallet.GetAutoCombineConfig()
		n.Wallet.SetAutoCombineConfig(enabled, target, cooldown)
		n.logger.WithField("cooldown", cooldown).Info("Auto-combine cooldown updated via config change")
	})

	// staking.stakeSplitThreshold — update stake split threshold (config stores TWINS, wallet uses satoshis)
	n.ConfigManager.Subscribe("staking.stakeSplitThreshold", func(_ string, _, newValue interface{}) {
		thresholdTWINS, ok := newValue.(int64)
		if !ok {
			return
		}
		if n.Wallet == nil {
			return
		}
		thresholdSatoshis := thresholdTWINS * 100_000_000
		if err := n.Wallet.SetStakeSplitThreshold(thresholdSatoshis); err != nil {
			n.logger.WithError(err).Warn("Failed to update stake split threshold via config change")
		} else {
			n.logger.WithField("threshold_twins", thresholdTWINS).Info("Stake split threshold updated via config change")
		}
	})

	// masternode.debug — start or stop the debug event collector at runtime.
	// On enable: construct a fresh *debug.Collector from the live max-MB / max-files
	// config values, Enable() it, and wire it onto the masternode Manager (which
	// internally fans out to SyncManager + ActiveMasternode) and the P2P server.
	// On disable: detach the collector from both consumers (atomic.Pointer swap to nil)
	// and Close() the old instance so its file handle is released.
	n.ConfigManager.Subscribe("masternode.debug", func(_ string, _, newValue interface{}) {
		enabled, ok := newValue.(bool)
		if !ok {
			n.logger.WithField("type", fmt.Sprintf("%T", newValue)).Warn("masternode.debug: unexpected value type from ConfigManager")
			return
		}
		if n.shuttingDown.Load() {
			// Shutdown is in progress; doShutdown's Phase 4.5 owns the existing collector.
			// Do nothing — neither create a new one (would leak past storage close) nor
			// attempt to close the one shutdown is already closing.
			return
		}
		if n.Masternode == nil {
			n.logger.Warn("Cannot toggle masternode debug: masternode manager not initialized")
			return
		}

		n.mu.Lock()
		// TOCTOU re-check: shutdown may have started between the early Load above
		// and the lock acquisition. doShutdown sets shuttingDown.Store(true) before
		// any phase, then later does an atomic Swap(nil) on DebugCollector — if
		// shuttingDown is set under our lock we abort, leaving the close path to
		// shutdown's Swap.
		if n.shuttingDown.Load() {
			n.mu.Unlock()
			return
		}
		// Snapshot P2PServer under the same lock used by InitP2P / Shutdown so the
		// read does not race with the write at node_p2p.go:95 (Go race detector
		// flags an unsynchronised read otherwise — see Manager pattern at
		// node_wallet.go:84). The collector wiring then runs unlocked since
		// SetDebugCollector itself uses atomic.Pointer swap.
		p2pServer := n.P2PServer
		if enabled {
			maxMB := 50
			maxFiles := 3
			if v := n.ConfigManager.GetInt("masternode.debugMaxMB"); v > 0 {
				maxMB = v
			}
			if v := n.ConfigManager.GetInt("masternode.debugMaxFiles"); v > 0 {
				maxFiles = v
			}
			c := debug.NewCollector(n.Config.DataDir, maxMB, maxFiles)
			if err := c.Enable(); err != nil {
				n.mu.Unlock()
				n.logger.WithError(err).Warn("Failed to enable masternode debug collector via config change")
				return
			}
			old := n.DebugCollector.Swap(c)
			n.mu.Unlock()
			n.Masternode.SetDebugCollector(c)
			if p2pServer != nil {
				p2pServer.SetDebugCollector(c)
			}
			// Defensive: ConfigManager.Set() may notify subscribers when the same
			// value is re-applied (e.g. user clicks Apply twice in the Options
			// dialog without changing anything). Close the previous collector so
			// the file handle does not leak. The new collector has already been
			// swapped onto Manager + P2PServer, so any in-flight Emit() call has
			// switched targets via the atomic.Pointer load before we close the
			// old one.
			if old != nil {
				old.Close()
			}
			n.logger.Info("Masternode debug collector started via config change")
		} else {
			old := n.DebugCollector.Swap(nil)
			n.mu.Unlock()
			n.Masternode.SetDebugCollector(nil)
			if p2pServer != nil {
				p2pServer.SetDebugCollector(nil)
			}
			if old != nil {
				old.Close()
			}
			n.logger.Info("Masternode debug collector stopped via config change")
		}
	})

	// Reconcile masternode.debug runtime state with the persisted ConfigManager
	// value. NewNode took a snapshot via cfg.MasternodeDebug — if anything wrote
	// to twinsd.yml in the brief window between LoadOrCreate and this call (an
	// external YAML editor, an early Set before subscribers were attached, etc.),
	// runtime state is stale until the user toggles the setting again. Firing
	// Set with the persisted value re-runs the subscriber above and re-aligns
	// runtime with truth. Skip when state already matches to avoid a wasted
	// collector swap on the common path.
	persistedDebug := n.ConfigManager.GetBool("masternode.debug")
	if persistedDebug != (n.DebugCollector.Load() != nil) {
		if err := n.ConfigManager.Set("masternode.debug", persistedDebug); err != nil {
			n.logger.WithError(err).Warn("Failed to reconcile masternode.debug runtime state with config")
		}
	}
}
