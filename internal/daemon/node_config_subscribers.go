package daemon

import (
	"fmt"

	"github.com/sirupsen/logrus"

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
}
