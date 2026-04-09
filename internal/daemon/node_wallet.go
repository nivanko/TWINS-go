package daemon

import (
	"crypto/rand"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/pbkdf2"

	"github.com/twins-dev/twins-core/internal/config"
	"github.com/twins-dev/twins-core/internal/consensus"
	"github.com/twins-dev/twins-core/internal/wallet"
	"github.com/twins-dev/twins-core/pkg/types"
)

// WalletConfig provides configuration for wallet initialization.
type WalletConfig struct {
	// Seed source (priority: HDSeed > Mnemonic > random)
	HDSeed              string // Hex-encoded 32-byte seed
	Mnemonic            string // BIP39 mnemonic phrase
	MnemonicPassphrase  string // Optional BIP39 passphrase

	// Config file settings (optional, from twinsd.yml)
	FullConfig     *config.Config
	ReserveBalance int64
	ReserveBalanceSet bool

	// Cache behaviour
	UseTxCache bool // Try loading txcache.dat before rescanning (both daemon and GUI set true)
}

// InitWallet initializes the wallet: loads existing or creates new, wires to
// blockchain/mempool/consensus, and either loads tx cache or rescans.
// This replaces:
//   - cmd/twinsd/startup_improved.go:startWalletRescan()
//   - cmd/twins-gui/app.go:initializeWallet()
func (n *Node) InitWallet(cfg WalletConfig) error {
	if n.Config.OnProgress != nil {
		n.Config.OnProgress("wallet", 0)
	}

	// Build wallet config
	walletConfig := buildWalletConfig(n.Config.DataDir, n.Config.Network, n.ChainParams, cfg)

	// Create wallet instance
	w, err := wallet.NewWallet(walletConfig, n.Storage, logrus.StandardLogger())
	if err != nil {
		return fmt.Errorf("failed to create wallet: %w", err)
	}

	// Load or create
	walletPath := filepath.Join(walletConfig.DataDir, "wallet.dat")
	if _, err := os.Stat(walletPath); err == nil {
		if err := loadExistingWallet(w, n.logger); err != nil {
			return err
		}
	} else {
		if err := createNewWallet(w, walletConfig, walletPath, n.logger); err != nil {
			return err
		}
	}

	if n.Config.OnProgress != nil {
		n.Config.OnProgress("wallet", 50)
	}

	// Wire wallet to blockchain and mempool
	w.SetBlockchain(n.Blockchain)
	w.SetMempool(n.Mempool)

	// Wire mempool → wallet notification for pending transaction tracking.
	// When a transaction is accepted into the mempool (from P2P or local broadcast),
	// the wallet processes it immediately for GUI visibility and UTXO selection.
	n.Mempool.SetOnTransaction(func(tx *types.Transaction) {
		w.OnMempoolTransaction(tx)
	})

	// Wire mempool → wallet eviction for pending transaction cleanup.
	// When a transaction is removed from the mempool (expiry, conflict, or explicit removal),
	// the wallet evicts its pending state so stale unconfirmed entries don't linger.
	n.Mempool.SetOnRemoveTransaction(func(txHash types.Hash) {
		w.EvictPendingTx(txHash)
	})

	// Connect blockchain to wallet for block notifications during sync
	n.Blockchain.SetWallet(w)

	// Wire wallet to consensus engine for staking
	n.Consensus.SetWallet(consensus.NewStakingWalletAdapter(w))

	// Stop staking when wallet locks (auto-lock timeout or manual walletlock).
	// Private keys are cleared on lock, so staking cannot succeed.
	w.SetOnLockCallback(func() {
		if posEngine, ok := n.Consensus.(*consensus.ProofOfStake); ok {
			if err := posEngine.StopStaking(); err != nil {
				// "staking is not active" is expected when wallet locks without staking
				n.logger.WithError(err).Debug("StopStaking on wallet lock")
			} else {
				n.logger.Info("Staking stopped due to wallet lock")
			}
		}
	})

	// Start staking when wallet unlocks (walletpassphrase RPC).
	// In C++ staking is a permanent thread that checks lock state each iteration;
	// in Go staking is explicit start/stop, so we need this callback.
	// StartStaking has atomic CAS guard — harmless if already active.
	// Guard against staking.enabled=false: if the user unlocks the wallet to
	// send a transaction but has staking disabled, we must not start staking.
	// When ConfigManager is nil (GUI before Phase 3), also skip — staking is
	// started explicitly by the GUI's toggle, not by unlock.
	w.SetOnUnlockCallback(func() {
		if n.ConfigManager == nil {
			n.logger.Debug("Wallet unlocked; ConfigManager not set, skipping StartStaking (GUI manages staking)")
			return
		}
		if !n.ConfigManager.GetBool("staking.enabled") {
			n.logger.Info("Wallet unlocked but staking.enabled=false, skipping StartStaking")
			return
		}
		if posEngine, ok := n.Consensus.(*consensus.ProofOfStake); ok {
			if err := posEngine.StartStaking(); err != nil {
				// "staking is already active" is expected on re-unlock
				n.logger.WithError(err).Debug("StartStaking on wallet unlock")
			} else {
				n.logger.Info("Staking started after wallet unlock")
			}
		}
	})

	// Load transaction cache or rescan
	if cfg.UseTxCache {
		if err := w.LoadTransactionCache(); err != nil {
			switch {
			case errors.Is(err, wallet.ErrCacheNotFound):
				n.logger.Debug("Transaction cache not found, rescanning blockchain")
			case errors.Is(err, wallet.ErrCacheStale):
				n.logger.Debug("Transaction cache stale, rescanning blockchain")
			default:
				n.logger.WithError(err).Warn("Transaction cache load failed, rescanning blockchain")
			}
			if rescanErr := w.RescanAllAddresses(); rescanErr != nil {
				n.logger.WithError(rescanErr).Warn("Blockchain rescan failed")
			}
		}
	} else {
		// Fallback: always rescan when cache is explicitly disabled
		if err := w.RescanAllAddresses(); err != nil {
			n.logger.WithError(err).Warn("Wallet rescan failed")
		}
	}

	// Store wallet reference
	n.mu.Lock()
	n.Wallet = w
	n.mu.Unlock()

	// Wire autocombine from config if available (config stores TWINS, wallet uses satoshis)
	if n.ConfigManager != nil {
		acEnabled := n.ConfigManager.GetBool("wallet.autoCombine")
		acTargetTWINS := n.ConfigManager.GetInt64("wallet.autoCombineTarget")
		acTargetSatoshis := acTargetTWINS * 100_000_000
		acCooldown := n.ConfigManager.GetInt("wallet.autoCombineCooldown")
		if acCooldown <= 0 {
			acCooldown = 600 // default 10 minutes
		}
		w.SetAutoCombineConfig(acEnabled, acTargetSatoshis, acCooldown)
	}

	if n.Config.OnProgress != nil {
		n.Config.OnProgress("wallet", 100)
	}
	n.logger.Info("Wallet initialized")
	n.logger.Info("autocombinerewards RPC removed; use setautocombine/getautocombine")
	return nil
}

// buildWalletConfig constructs wallet configuration.
func buildWalletConfig(dataDir, network string, chainParams *types.ChainParams, cfg WalletConfig) *wallet.Config {
	wc := wallet.DefaultConfig()
	wc.DataDir = dataDir

	switch network {
	case "mainnet":
		wc.Network = wallet.MainNet
	case "testnet":
		wc.Network = wallet.TestNet
	case "regtest":
		wc.Network = wallet.RegTest
	default:
		wc.Network = wallet.MainNet
	}

	wc.CoinbaseMaturity = chainParams.CoinbaseMaturity
	wc.MinStakeAmount = chainParams.MinStakeAmount

	// Apply config file values
	if cfg.FullConfig != nil {
		applyConfigFileSettings(wc, cfg.FullConfig)
	}

	// Apply seed source from config
	if cfg.HDSeed != "" {
		wc.HDSeed = cfg.HDSeed
	}
	if cfg.Mnemonic != "" {
		wc.Mnemonic = cfg.Mnemonic
		wc.MnemonicPassphrase = cfg.MnemonicPassphrase
	}

	// Apply CLI reserve balance if explicitly set
	if cfg.ReserveBalanceSet {
		wc.ReserveBalance = cfg.ReserveBalance
	}

	return wc
}

// applyConfigFileSettings applies wallet settings from the config file.
func applyConfigFileSettings(wc *wallet.Config, fullConfig *config.Config) {
	fc := fullConfig.Wallet

	if fc.PayTxFee > 0 {
		wc.FeePerKB = fc.PayTxFee
	}
	// MinTxFee and MaxTxFee are applied unconditionally because the ConfigManager
	// always provides the correct value (default or user-set). Unlike PayTxFee where
	// 0 means "use wallet's dynamic fee default", 0 is a valid explicit value for
	// these fields (meaning "no minimum fee" / "no max fee cap").
	wc.MinTxFee = fc.MinTxFee
	wc.MaxTxFee = fc.MaxTxFee
	if fc.TxConfirmTarget > 0 {
		wc.TxConfirmTarget = fc.TxConfirmTarget
	}
	if fc.Keypool > 0 {
		wc.AccountLookahead = fc.Keypool
	}
	wc.SpendZeroConfChange = fc.SpendZeroConfChange
	if fc.CreateWalletBackups >= 0 {
		wc.CreateWalletBackups = fc.CreateWalletBackups
	}
	if fc.BackupPath != "" {
		wc.BackupPath = fc.BackupPath
	}
	wc.Mnemonic = fc.Mnemonic
	wc.MnemonicPassphrase = fc.MnemonicPassphrase
	wc.HDSeed = fc.HDSeed

	if fullConfig.Staking.ReserveBalance > 0 {
		wc.ReserveBalance = fullConfig.Staking.ReserveBalance
	}
}

// loadExistingWallet loads an existing wallet and multisig addresses.
func loadExistingWallet(w *wallet.Wallet, logger *logrus.Entry) error {
	logger.Info("Loading existing wallet...")
	if err := w.LoadWallet(); err != nil {
		return fmt.Errorf("failed to load wallet: %w", err)
	}
	if err := w.LoadMultisigAddresses(); err != nil {
		logger.WithError(err).Warn("Failed to load multisig addresses")
	}
	logger.Info("Wallet loaded successfully")
	return nil
}

// createNewWallet creates a new wallet with a generated or provided seed.
func createNewWallet(w *wallet.Wallet, walletConfig *wallet.Config, walletPath string, logger *logrus.Entry) error {
	logger.Info("No wallet found, creating new wallet...")

	seed, err := generateSeed(walletConfig, logger)
	if err != nil {
		return err
	}

	if err := w.CreateWallet(seed, nil); err != nil {
		secureClearBytes(seed)
		return fmt.Errorf("failed to create wallet: %w", err)
	}
	secureClearBytes(seed)

	if err := w.LoadMultisigAddresses(); err != nil {
		logger.WithError(err).Warn("Failed to load multisig addresses")
	}

	logger.Info("New wallet created successfully")
	logger.WithField("path", walletPath).Debug("Wallet file location")
	logger.Warn("IMPORTANT: Wallet is unencrypted. Use 'encryptwallet' RPC to encrypt it.")
	logger.Warn("IMPORTANT: Back up your wallet file to prevent loss of funds.")
	return nil
}

// generateSeed generates or loads an HD seed based on wallet configuration.
func generateSeed(walletConfig *wallet.Config, logger *logrus.Entry) ([]byte, error) {
	if walletConfig.HDSeed != "" {
		return loadHDSeed(walletConfig.HDSeed, logger)
	}
	if walletConfig.Mnemonic != "" {
		return deriveSeedFromMnemonic(walletConfig.Mnemonic, walletConfig.MnemonicPassphrase, logger)
	}
	return generateRandomSeed(logger)
}

func loadHDSeed(hdSeed string, logger *logrus.Entry) ([]byte, error) {
	seed, err := hex.DecodeString(hdSeed)
	if err != nil {
		return nil, fmt.Errorf("invalid hdseed hex: %w", err)
	}
	if len(seed) != types.SeedLengthBytes {
		return nil, fmt.Errorf("hdseed must be %d bytes (64 hex chars), got %d bytes", types.SeedLengthBytes, len(seed))
	}
	if isWeakSeed(seed) {
		return nil, fmt.Errorf("hdseed is cryptographically weak")
	}
	logger.Debug("Using provided HD seed for wallet creation")
	return seed, nil
}

func deriveSeedFromMnemonic(mnemonic, passphrase string, logger *logrus.Entry) ([]byte, error) {
	mnemonic = strings.TrimSpace(mnemonic)
	salt := "mnemonic" + passphrase
	fullSeed := pbkdf2.Key([]byte(mnemonic), []byte(salt), types.BIP39Iterations, types.BIP39SeedLength, sha512.New)
	seed := make([]byte, types.SeedLengthBytes)
	copy(seed, fullSeed[:types.SeedLengthBytes])
	secureClearBytes(fullSeed)
	logger.Debug("Using BIP39 mnemonic for wallet creation")
	return seed, nil
}

func generateRandomSeed(logger *logrus.Entry) ([]byte, error) {
	seed := make([]byte, types.SeedLengthBytes)
	if _, err := io.ReadFull(rand.Reader, seed); err != nil {
		return nil, fmt.Errorf("failed to generate secure seed: %w", err)
	}
	if isWeakSeed(seed) {
		return nil, fmt.Errorf("generated seed failed entropy check")
	}
	logger.Debug("Generated new cryptographically secure random seed")
	return seed, nil
}

// secureClearBytes overwrites a byte slice with zeros.
func secureClearBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// isWeakSeed performs basic entropy validation on cryptographic seed material.
func isWeakSeed(seed []byte) bool {
	if len(seed) == 0 {
		return true
	}

	// All zeros
	allZero := true
	for _, b := range seed {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return true
	}

	// All same byte
	allSame := true
	first := seed[0]
	for _, b := range seed {
		if b != first {
			allSame = false
			break
		}
	}
	if allSame {
		return true
	}

	// Sequential
	sequential := true
	for i := 1; i < len(seed); i++ {
		if seed[i] != seed[i-1]+1 {
			sequential = false
			break
		}
	}
	if sequential {
		return true
	}

	// Reverse sequential
	reverseSeq := true
	for i := 1; i < len(seed); i++ {
		if seed[i] != seed[i-1]-1 {
			reverseSeq = false
			break
		}
	}
	if reverseSeq {
		return true
	}

	// Low unique byte count
	uniqueBytes := make(map[byte]struct{})
	for _, b := range seed {
		uniqueBytes[b] = struct{}{}
	}
	if len(seed) >= types.SeedLengthBytes && len(uniqueBytes) < types.MinUniqueByteEntropy {
		return true
	}

	return false
}
