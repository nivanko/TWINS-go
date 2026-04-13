package wallet

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/twins-dev/twins-core/internal/storage"
	walletcrypto "github.com/twins-dev/twins-core/internal/wallet/crypto"
	"github.com/twins-dev/twins-core/internal/wallet/legacy"
	"github.com/twins-dev/twins-core/pkg/crypto"
	"github.com/twins-dev/twins-core/pkg/types"
)

// ErrWalletLocked is returned when an operation requires an unlocked wallet.
// Matches legacy C++ EnsureWalletIsUnlocked() error message.
var ErrWalletLocked = errors.New("please enter the wallet passphrase with walletpassphrase first")

// BlockchainInterface defines the blockchain operations needed by wallet
type BlockchainInterface interface {
	GetUTXO(outpoint types.Outpoint) (*types.UTXO, error)
	GetBestHeight() (uint32, error)
	GetBestBlockHash() (types.Hash, error)
}

// MempoolInterface defines the mempool operations needed by wallet
type MempoolInterface interface {
	AddTransaction(tx *types.Transaction) error
}

// BroadcasterInterface defines the transaction broadcast operations needed by wallet
// This should be implemented by the P2P server to broadcast transactions to the network
type BroadcasterInterface interface {
	BroadcastTransaction(tx *types.Transaction) error
}

// MasternodeCollateralChecker defines the interface for checking if a UTXO is masternode collateral.
// This is used to completely hide collateral UTXOs from wallet transaction operations.
// Legacy: Equivalent to CMasternodeConfig integration in C++ CWallet for UTXO locking (mnconflock)
type MasternodeCollateralChecker interface {
	// IsCollateralOutpoint checks if the given outpoint is a masternode collateral from masternode.conf
	IsCollateralOutpoint(outpoint types.Outpoint) bool
}

// Wallet represents the main wallet structure
type Wallet struct {
	// Storage and configuration
	storage storage.Storage
	config  *Config
	logger  *logrus.Entry
	wdb     *WalletDB // wallet.dat database

	// Key management
	masterKey       *HDKey
	accounts        map[uint32]*Account
	addresses       map[string]*Address   // Base58 address -> Address (for API)
	addressesBinary map[[21]byte]*Address // Binary address -> Address (for fast lookup)
	addrMgr         *AddressManager
	multisigAddrs   map[string]*MultisigAddress // P2SH address -> Multisig info

	// Transaction tracking
	transactions map[txKey]*WalletTransaction
	utxos        map[types.Outpoint]*UTXO
	balances     map[string]*Balance

	// Transaction creation dependencies
	blockchain  BlockchainInterface  // For UTXO queries and chain height
	mempool     MempoolInterface     // For local mempool (validation only)
	broadcaster BroadcasterInterface // For P2P network broadcast

	// Masternode collateral checking
	masternodeManager MasternodeCollateralChecker // For hiding collateral UTXOs from transaction flow

	// Pending transaction tracking (mempool-aware state for GUI and UTXO selection)
	// In-memory only, resets on restart. Follows lockedCoins pattern with separate mutex.
	// Lock ordering: heightMu → mu → pendingMu → lockedCoinsMu
	pendingTxs   map[types.Hash]*WalletTransaction // Pending wallet transactions (not yet in a block)
	pendingUTXOs map[types.Outpoint]*UTXO          // Change UTXOs from pending transactions
	pendingSpent map[types.Outpoint]types.Hash     // Outpoints spent by pending txs → spending tx hash
	pendingMu    sync.RWMutex

	// Coin locking (user-controlled UTXO locks via lockunspent RPC/GUI)
	// In-memory only, resets on restart — matches legacy C++ CWallet::setLockedCoins
	lockedCoins   map[types.Outpoint]struct{}
	lockedCoinsMu sync.RWMutex

	// Synchronization
	mu                sync.RWMutex
	syncHeight        int32
	syncing           bool
	cachedChainHeight uint32       // Cached chain height for performance
	heightMu          sync.RWMutex // Separate mutex for height cache

	// Encryption
	encrypted           bool
	passphrase          []byte
	unlocked            bool
	unlockTime          time.Time
	unlockedStakingOnly bool               // When true, wallet is unlocked only for staking (sending blocked)
	autoLockCancel      context.CancelFunc // Cancels previous auto-lock goroutine on re-unlock
	onLockCallback      func()             // Called after wallet locks (e.g., to stop staking); invoked outside mutex
	onUnlockCallback    func()             // Called after wallet unlocks (e.g., to start staking); invoked outside mutex

	// Lifecycle
	started bool

	// Fee configuration
	// txFeePerKB mirrors config.FeePerKB and must be kept in sync via SetTransactionFee.
	// All transaction builders read config.FeePerKB; GetTransactionFee reads txFeePerKB.
	// Never update txFeePerKB directly — always use SetTransactionFee to keep both fields consistent.
	txFeePerKB int64 // Transaction fee per kilobyte in satoshis

	// Transaction cache
	nextSeqNum int64 // Next sequence number for chronological ordering

	// Wallet rebroadcast scheduler (legacy-like periodic rebroadcast)
	rebroadcastCancel context.CancelFunc
	lastRebroadcast   map[types.Hash]time.Time // Per-tx cooldown state

	// Sent transaction comments — in-memory map of tx hash → comment for locally-sent transactions.
	// Populated by SendMany/SendManyWithOptions before broadcast. Read by processBlock/OnMempoolTransaction
	// to set WalletTransaction.Comment. Entries cleaned up after confirmation.
	// Protected by mu (written under Lock in send paths, read under RLock/Lock in notification paths).
	sentTxComments map[types.Hash]string

	// Auto-combine inputs (UTXO consolidation)
	autoCombineWorker       *AutoCombineWorker
	autoCombineEnabled      bool
	autoCombineTarget       int64
	autoCombineCooldown     int
	onConsolidationCallback func(txCount int, totalAmount int64) // Called after consolidation cycle; invoked outside mutex
	syncChecker             func() bool                          // Returns true when node is fully synced (consensus confidence 100%); nil = skip check
}

// Config contains wallet configuration
type Config struct {
	DataDir          string
	Network          NetworkType
	AccountLookahead int    // Number of addresses to generate ahead (legacy: -keypool)
	MinConfirmations int    // Minimum confirmations for spends
	FeePerKB         int64  // Default fee per kilobyte (legacy: -paytxfee)
	EncryptWallet    bool   // Encrypt wallet on creation
	CoinbaseMaturity   uint32 // Blocks until coinbase/coinstake outputs can be spent
	GenesisTimestamp   uint32 // Genesis block timestamp (from ChainParams, used for block time estimation)

	// === Fee Configuration (Legacy C++ Compatible) ===
	MinTxFee        int64 // Minimum fee threshold in satoshis (legacy: -mintxfee)
	MaxTxFee        int64 // Maximum total fee allowed in satoshis (legacy: -maxtxfee)
	TxConfirmTarget int   // Target confirmations for fee estimation (legacy: -txconfirmtarget)

	// === Wallet Management (Legacy C++ Compatible) ===
	SpendZeroConfChange bool   // Allow spending unconfirmed change (legacy: -spendzeroconfchange)
	CreateWalletBackups int    // Auto-backup count, 0 to disable (legacy: -createwalletbackups)
	BackupPath          string // Custom backup directory (legacy: -backuppath)

	// === HD Wallet Creation (Legacy C++ Compatible) ===
	Mnemonic           string // BIP39 mnemonic for wallet creation (legacy: -mnemonic)
	MnemonicPassphrase string // Optional mnemonic passphrase (legacy: -mnemonicpassphrase)
	HDSeed             string // Direct seed specification in hex (legacy: -hdseed)

	// === Staking Configuration (Legacy C++ Compatible) ===
	ReserveBalance int64 // Amount in satoshis to keep available for spending, not used for staking (legacy: -reservebalance)
	MinStakeAmount int64 // Minimum UTXO value for staking in satoshis (legacy: nStakeMinInput from chainparams)
}

// DefaultConfig returns default wallet configuration
func DefaultConfig() *Config {
	return &Config{
		DataDir:             "./wallet",
		Network:             MainNet,
		AccountLookahead:    1000, // Legacy default: -keypool=1000
		MinConfirmations:    6,
		FeePerKB:            10000, // 0.0001 TWINS per KB (legacy: -paytxfee default)
		EncryptWallet:       false,
		CoinbaseMaturity:    60,         // Default to mainnet value (must be set from ChainParams)
		GenesisTimestamp:    1546300800,  // Default to mainnet genesis (must be set from ChainParams)
		MinTxFee:            10000,     // Legacy default: -mintxfee=10000
		MaxTxFee:            100000000, // 1 TWINS (legacy: -maxtxfee default)
		TxConfirmTarget:     1,         // Legacy default: -txconfirmtarget=1
		SpendZeroConfChange: false,     // Legacy default: false
		CreateWalletBackups: 10,        // Legacy default: -createwalletbackups=10
		// LEGACY COMPLIANCE: MinStakeAmount must match Params().StakingMinInput()
		// Legacy: chainparams.cpp line 243: nStakeMinInput = 12000 * COIN
		MinStakeAmount: 1200000000000, // 12000 TWINS in satoshis (12000 * 100000000)
	}
}


// NewWallet creates a new wallet instance
func NewWallet(config *Config, storage storage.Storage, logger *logrus.Logger) (*Wallet, error) {
	if config == nil {
		config = DefaultConfig()
	}

	if storage == nil {
		return nil, fmt.Errorf("storage is required")
	}

	if logger == nil {
		logger = logrus.New()
	}

	w := &Wallet{
		storage:         storage,
		config:          config,
		logger:          logger.WithField("component", "wallet"),
		accounts:        make(map[uint32]*Account),
		addresses:       make(map[string]*Address),
		addressesBinary: make(map[[21]byte]*Address),
		transactions:    make(map[txKey]*WalletTransaction),
		utxos:           make(map[types.Outpoint]*UTXO),
		balances:        make(map[string]*Balance),
		multisigAddrs:   make(map[string]*MultisigAddress),
		pendingTxs:      make(map[types.Hash]*WalletTransaction),
		pendingUTXOs:    make(map[types.Outpoint]*UTXO),
		pendingSpent:    make(map[types.Outpoint]types.Hash),
		lastRebroadcast: make(map[types.Hash]time.Time),
		lockedCoins:     make(map[types.Outpoint]struct{}),
		encrypted:       false,
		unlocked:        true,
	}

	// Initialize address manager
	w.addrMgr = NewAddressManager(w)

	return w, nil
}

// hasTransactionByHash checks if any wallet transaction entry exists for the given hash.
// Caller must hold w.mu (read or write).
func (w *Wallet) hasTransactionByHash(hash types.Hash) bool {
	if _, ok := w.transactions[txKey{hash, 0}]; ok {
		return true
	}
	if _, ok := w.transactions[txKey{hash, 1}]; ok {
		return true
	}
	return false
}

// getTransactionByHash returns a wallet transaction by hash, checking vout=0 first then vout=1.
// Caller must hold w.mu (read or write).
func (w *Wallet) getTransactionByHash(hash types.Hash) (*WalletTransaction, bool) {
	if tx, ok := w.transactions[txKey{hash, 0}]; ok {
		return tx, true
	}
	if tx, ok := w.transactions[txKey{hash, 1}]; ok {
		return tx, true
	}
	return nil, false
}

// LoadMultisigAddresses loads all multisig addresses from the wallet database
// This should be called after opening the wallet database
func (w *Wallet) LoadMultisigAddresses() error {
	if w.wdb == nil {
		return nil // No database, nothing to load
	}

	multisigAddrs, err := w.wdb.GetAllMultisigAddresses()
	if err != nil {
		return fmt.Errorf("failed to load multisig addresses: %w", err)
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// Load all addresses into memory
	for addr, ma := range multisigAddrs {
		w.multisigAddrs[addr] = ma
	}

	w.logger.Debugf("Loaded %d multisig addresses from database", len(multisigAddrs))
	return nil
}

// CreateWallet creates a new wallet with a seed
func (w *Wallet) CreateWallet(seed []byte, passphrase []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.masterKey != nil {
		return fmt.Errorf("wallet already exists")
	}

	// Create master key from seed
	masterKey, err := NewHDKeyFromSeed(seed, w.config.Network)
	if err != nil {
		return fmt.Errorf("failed to create master key: %w", err)
	}

	w.masterKey = masterKey

	// Initialize wallet database
	wdb, err := OpenWalletDB(w.config.DataDir)
	if err != nil {
		return fmt.Errorf("failed to create wallet database: %w", err)
	}
	w.wdb = wdb

	// Create default account (account 0)
	_, err = w.addrMgr.CreateAccount(0, "Default")
	if err != nil {
		return fmt.Errorf("failed to create default account: %w", err)
	}

	// Fill address pool
	err = w.addrMgr.FillAddressPool(0)
	if err != nil {
		return fmt.Errorf("failed to fill address pool: %w", err)
	}

	// Encrypt if requested
	if w.config.EncryptWallet && len(passphrase) > 0 {
		// Defensive copy: encryptWalletLocked zeros its argument,
		// but we need the passphrase again for wdb.Unlock() below.
		passphraseCopy := make([]byte, len(passphrase))
		copy(passphraseCopy, passphrase)

		if err := w.encryptWalletLocked(passphraseCopy); err != nil {
			return fmt.Errorf("failed to encrypt wallet: %w", err)
		}

		// Unlock the wallet database so it's usable immediately after creation.
		// We call wdb.Unlock directly (not w.Unlock) because:
		// 1. We already hold w.mu
		// 2. w.Unlock zeros the passphrase, which the caller may still need
		// 3. We don't need the full Unlock flow (HD seed decryption, auto-lock timer)
		//    since we just encrypted — the master key is still in memory from CreateWallet
		if err := w.wdb.Unlock(passphrase); err != nil {
			return fmt.Errorf("failed to unlock wallet after encryption: %w", err)
		}
		w.unlocked = true
		w.passphrase = make([]byte, len(passphrase))
		copy(w.passphrase, passphrase)
	}

	// Save wallet to storage
	if err := w.save(); err != nil {
		return fmt.Errorf("failed to save wallet: %w", err)
	}

	// Initialize cached chain height for GetBalance performance
	currentHeight, err := w.storage.GetChainHeight()
	if err == nil {
		w.heightMu.Lock()
		w.cachedChainHeight = currentHeight
		w.heightMu.Unlock()
	}

	w.logger.Info("Wallet created successfully")
	return nil
}

// LoadWallet loads an existing wallet from storage using wallet.dat
func (w *Wallet) LoadWallet() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Open wallet database (with automatic migration if needed)
	wdb, err := OpenWalletDB(w.config.DataDir)
	if err != nil {
		return fmt.Errorf("failed to open wallet database: %w", err)
	}
	w.wdb = wdb

	// Check if wallet is encrypted
	w.encrypted = wdb.IsEncrypted()
	if w.encrypted {
		w.unlocked = false
		w.logger.Info("Wallet is encrypted, unlock required")
	} else {
		w.unlocked = true
		w.logger.Debug("Wallet is not encrypted")
	}

	// Load HD chain state
	hdChain, isEncrypted, err := wdb.ReadHDChain()
	if err == nil {
		w.logger.WithField("external_counter", hdChain.ExternalCounter).
			WithField("internal_counter", hdChain.InternalCounter).
			WithField("encrypted", isEncrypted).
			WithField("crypted_flag", hdChain.Crypted).
			Debug("Loaded HD chain state")

		// Only restore master key if seed is not encrypted
		// Check both the prefix detection (isEncrypted) and the Crypted flag
		if len(hdChain.Seed) > 0 && !isEncrypted && !hdChain.Crypted {
			// Unencrypted seed - recreate master key immediately
			masterKey, err := NewHDKeyFromSeed(hdChain.Seed, w.config.Network)
			if err != nil {
				w.logger.WithError(err).Warn("Failed to restore master key from HD chain")
			} else {
				w.masterKey = masterKey
				w.logger.Debug("Master key restored from unencrypted HD chain")
			}
		} else if isEncrypted || hdChain.Crypted {
			w.logger.Debug("HD chain seed is encrypted, will be decrypted on wallet unlock")
			// Store encrypted seed for later decryption
			// Seed will be decrypted in the Unlock() method
		}

		// Restore account counters
		if w.addrMgr != nil {
			for accountID, account := range w.addrMgr.accounts {
				if account.ExternalChain != nil {
					account.ExternalChain.nextIndex = hdChain.ExternalCounter
				}
				if account.InternalChain != nil {
					account.InternalChain.nextIndex = hdChain.InternalCounter
				}
				w.logger.WithField("account", accountID).
					Debug("Restored address counters from HD chain")
			}
		}
	}

	// Load all keys from wallet.dat
	pubkeys, err := wdb.GetAllKeys()
	if err != nil {
		w.logger.WithError(err).Warn("Failed to load keys from wallet.dat")
	} else {
		w.logger.WithField("key_count", len(pubkeys)).Debug("Loading keys from wallet.dat")

		// Load each key and reconstruct address
		for _, pubkeyBytes := range pubkeys {
			// Parse public key
			pubKey, err := crypto.ParsePublicKeyFromBytes(pubkeyBytes)
			if err != nil {
				w.logger.WithError(err).Warn("Failed to parse public key")
				continue
			}

			// Calculate address from public key
			pubKeyHash := crypto.Hash160(pubkeyBytes)
			var version byte
			switch w.config.Network {
			case MainNet:
				version = crypto.MainNetPubKeyHashAddrID // 0x49 - TWINS W... addresses
			case TestNet, RegTest:
				version = crypto.TestNetPubKeyHashAddrID // 0x6f
			}

			payload := append([]byte{version}, pubKeyHash...)
			checksum := crypto.DoubleHash256(payload)[:4]
			fullPayload := append(payload, checksum...)
			address := crypto.Base58Encode(fullPayload)

			// Validate the generated address
			if address == "" || address == "0" || len(address) < 20 {
				w.logger.WithField("pubkey", fmt.Sprintf("%x", pubkeyBytes[:min(len(pubkeyBytes), 8)])).
					Warn("Invalid address generated from public key, skipping")
				continue
			}

			// Validate address format by trying to decode it
			if _, err := crypto.Base58CheckDecode(address); err != nil {
				w.logger.WithError(err).WithField("address", address).
					Warn("Generated address failed validation, skipping")
				continue
			}

			// Read label and purpose
			label, _ := wdb.ReadName(address)
			purpose, _ := wdb.ReadPurpose(address)

			// Read private key if wallet is unlocked
			var privKey *crypto.PrivateKey
			if !w.encrypted || w.unlocked {
				privKeyBytes, err := wdb.ReadKey(pubkeyBytes)
				if err == nil {
					privKey, err = crypto.ParsePrivateKeyFromBytes(privKeyBytes)
					if err != nil {
						w.logger.WithError(err).WithField("address", address).
							Warn("Failed to parse private key")
					}
					// Securely zero private key bytes from memory after use
					for i := range privKeyBytes {
						privKeyBytes[i] = 0
					}
				}
			}

			// Create Address object
			addr := &Address{
				Address:    address,
				PubKey:     pubKey,
				PrivKey:    privKey,
				ScriptType: ScriptTypeP2PKH,
				Account:    0, // Legacy wallets use string-based account names, not HD account IDs
				Label:      label,
			}

			// Determine if this is a change address from purpose
			if purpose == "change" {
				addr.Internal = true
			}

			w.addresses[address] = addr

			// Also add to binary address map for fast script matching
			if binaryKey, ok := w.addressToBinaryKey(address); ok {
				w.addressesBinary[binaryKey] = addr
			}
		}

		w.logger.WithField("address_count", len(w.addresses)).
			Info("Loaded addresses from wallet.dat")

		// Populate address pool with loaded addresses for GetReceivingAddresses()
		if w.addrMgr != nil && w.addrMgr.pool != nil {
			w.addrMgr.pool.mu.Lock()
			for _, addr := range w.addresses {
				if !addr.Internal {
					// External (receiving) address
					w.addrMgr.pool.external = append(w.addrMgr.pool.external, addr)
				} else {
					// Internal (change) address
					w.addrMgr.pool.internal = append(w.addrMgr.pool.internal, addr)
				}
			}
			w.addrMgr.pool.mu.Unlock()
			w.logger.WithField("external_count", len(w.addrMgr.pool.external)).
				WithField("internal_count", len(w.addrMgr.pool.internal)).
				Debug("Populated address pool from loaded addresses")
		}
	}

	// Create default account(s) for legacy wallets
	// Legacy wallets use string-based account names, not HD account IDs
	// We create a default account with empty string name (Bitcoin Core compatible)
	if len(w.accounts) == 0 && len(w.addresses) > 0 {
		w.logger.Debug("Creating default account for legacy wallet")

		// Create default account with empty string name (Bitcoin Core default)
		defaultAccount := &Account{
			ID:   0,
			Name: "", // Empty string is the default account name in Bitcoin Core
			Balance: &Balance{
				Confirmed:   0,
				Unconfirmed: 0,
			},
		}

		// Initialize address chains for HD wallet support
		// Use hdchain counters if available (for migrated HD wallets)
		var externalCounter, internalCounter uint32
		hdChain, _, err := wdb.ReadHDChain()
		if err == nil && hdChain != nil {
			externalCounter = hdChain.ExternalCounter
			internalCounter = hdChain.InternalCounter
			w.logger.WithField("external_counter", externalCounter).
				WithField("internal_counter", internalCounter).
				Debug("Restoring HD chain counters for default account")
		}

		defaultAccount.ExternalChain = &AddressChain{
			account:   defaultAccount,
			internal:  false,
			nextIndex: externalCounter,
			addresses: make([]*Address, 0),
			gap:       0,
		}
		defaultAccount.InternalChain = &AddressChain{
			account:   defaultAccount,
			internal:  true,
			nextIndex: internalCounter,
			addresses: make([]*Address, 0),
			gap:       0,
		}

		w.accounts[0] = defaultAccount

		// Also register in address manager if available
		if w.addrMgr != nil {
			w.addrMgr.accounts[0] = defaultAccount
		}

		w.logger.Debug("Created default account \"\" for legacy wallet")
	}

	// Initialize cached chain height for GetBalance performance
	currentHeight, err := w.storage.GetChainHeight()
	if err == nil {
		w.heightMu.Lock()
		w.cachedChainHeight = currentHeight
		w.heightMu.Unlock()
	}

	w.logger.Debug("Wallet loaded successfully from wallet.dat")
	return nil
}

// Close saves and closes the wallet
// StartAutoCombine starts the autocombine worker if not already running.
func (w *Wallet) StartAutoCombine() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.autoCombineWorker != nil {
		return
	}
	worker := newAutoCombineWorker(w)
	w.autoCombineWorker = worker
	worker.Start()
	w.logger.Info("autocombine: worker started")
}

// StopAutoCombine stops the autocombine worker if running.
func (w *Wallet) StopAutoCombine() {
	w.mu.Lock()
	worker := w.autoCombineWorker
	w.autoCombineWorker = nil
	w.mu.Unlock()
	if worker != nil {
		worker.Stop()
		w.logger.Info("autocombine: worker stopped")
	}
}

// SetAutoCombineConfig updates autocombine configuration.
// Starts or stops the worker based on the enabled flag.
func (w *Wallet) SetAutoCombineConfig(enabled bool, target int64, cooldown int) {
	w.mu.Lock()
	w.autoCombineEnabled = enabled
	w.autoCombineTarget = target
	w.autoCombineCooldown = cooldown
	w.mu.Unlock()

	if enabled && target > 0 {
		w.StartAutoCombine()
	} else {
		w.StopAutoCombine()
	}
}

// GetAutoCombineConfig returns the current autocombine configuration.
func (w *Wallet) GetAutoCombineConfig() (enabled bool, target int64, cooldown int) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.autoCombineEnabled, w.autoCombineTarget, w.autoCombineCooldown
}

// SetOnConsolidationCallback registers a callback invoked after each consolidation cycle.
// The callback receives the number of transactions submitted and the total amount consolidated.
func (w *Wallet) SetOnConsolidationCallback(fn func(txCount int, totalAmount int64)) {
	w.mu.Lock()
	w.onConsolidationCallback = fn
	w.mu.Unlock()
}

// SetSyncChecker registers a function that returns true when the node is fully
// synced with the network. Used by autocombine to skip consolidation during IBD
// and partial sync. When nil, autocombine runs unconditionally (legacy behavior).
func (w *Wallet) SetSyncChecker(fn func() bool) {
	w.mu.Lock()
	w.syncChecker = fn
	w.mu.Unlock()
}

func (w *Wallet) Close() error {
	// Stop autocombine worker before acquiring lock
	w.StopAutoCombine()

	w.mu.Lock()
	defer w.mu.Unlock()

	// Sync and close wallet database
	if w.wdb != nil {
		if err := w.wdb.Sync(); err != nil {
			w.logger.WithError(err).Error("Failed to sync wallet database")
		}
		if err := w.wdb.Close(); err != nil {
			return fmt.Errorf("failed to close wallet database: %w", err)
		}
	}

	// Clear sensitive data from memory (use internal method to avoid mutex deadlock)
	if w.unlocked && w.encrypted {
		w.clearSecretsLocked()
	}

	w.started = false
	w.logger.Info("Wallet closed")
	return nil
}

// IsEncrypted returns whether the wallet is encrypted
func (w *Wallet) IsEncrypted() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.encrypted
}

// IsLocked returns whether the wallet is locked
func (w *Wallet) IsLocked() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.isLockedLocked()
}

// isLockedLocked is an internal version that assumes caller holds w.mu
func (w *Wallet) isLockedLocked() bool {
	return w.encrypted && !w.unlocked
}

// isLockedForSendingLocked checks if the wallet is locked for non-staking operations.
// Returns true if the wallet is locked OR if it's unlocked for staking only.
// Must be called with w.mu held. Matches legacy C++ EnsureWalletIsUnlocked(false).
func (w *Wallet) isLockedForSendingLocked() bool {
	return w.isLockedLocked() || w.unlockedStakingOnly
}

// IsUnlockedForStakingOnly returns true if the wallet is unlocked but only for staking.
// When in this mode, sending funds is blocked but staking operations are allowed.
func (w *Wallet) IsUnlockedForStakingOnly() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.unlocked && w.unlockedStakingOnly
}

// UnlockTime returns when the wallet will auto-lock.
// Returns zero time if the wallet is locked, indefinitely unlocked, or in staking-only mode.
func (w *Wallet) UnlockTime() time.Time {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.unlockTime
}

// SetOnLockCallback registers a callback invoked after the wallet locks.
// Used to stop staking when private keys are cleared.
// Must be called during initialization before concurrent use.
func (w *Wallet) SetOnLockCallback(fn func()) {
	w.onLockCallback = fn
}

// SetOnUnlockCallback registers a callback invoked after the wallet unlocks.
// Used to start staking when wallet becomes available for signing.
// Must be called during initialization before concurrent use.
func (w *Wallet) SetOnUnlockCallback(fn func()) {
	w.onUnlockCallback = fn
}

// EncryptWallet encrypts an unencrypted wallet with a passphrase.
// The wallet is locked after encryption — call Unlock() to use it.
func (w *Wallet) EncryptWallet(passphrase []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.encryptWalletLocked(passphrase)
}

// encryptWalletLocked encrypts an unencrypted wallet with a passphrase.
// Caller must hold w.mu. The passphrase is zeroed after use.
func (w *Wallet) encryptWalletLocked(passphrase []byte) error {
	// Zero passphrase from memory after use (security)
	defer func() {
		for i := range passphrase {
			passphrase[i] = 0
		}
	}()

	if w.encrypted {
		return fmt.Errorf("wallet is already encrypted")
	}

	if len(passphrase) == 0 {
		return fmt.Errorf("passphrase cannot be empty")
	}

	if w.wdb == nil {
		return fmt.Errorf("wallet database not initialized")
	}

	// Encrypt wallet database with passphrase (encrypts private keys + HD chain seed)
	if err := w.wdb.Encrypt(passphrase); err != nil {
		return fmt.Errorf("failed to encrypt wallet database: %w", err)
	}

	// Mark wallet as encrypted and locked
	w.encrypted = true
	w.unlocked = false

	// Clear master key from memory (wallet is now locked)
	w.masterKey = nil

	// Clear private keys from memory
	for _, addr := range w.addresses {
		if addr.PrivKey != nil {
			// Zero out private key bytes
			privKeyBytes := addr.PrivKey.Bytes()
			for i := range privKeyBytes {
				privKeyBytes[i] = 0
			}
			addr.PrivKey = nil
		}
	}

	w.logger.Info("Wallet encrypted successfully - wallet is now locked")
	return nil
}

// Unlock unlocks the wallet with a passphrase for a duration.
// If stakingOnly is true, the wallet is unlocked only for staking operations
// (sending funds will be blocked).
// If duration is 0, the wallet stays unlocked until explicitly locked or daemon shutdown.
func (w *Wallet) Unlock(passphrase []byte, duration time.Duration, stakingOnly bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Zero input passphrase from memory after use (security)
	defer func() {
		for i := range passphrase {
			passphrase[i] = 0
		}
	}()

	if !w.encrypted {
		return fmt.Errorf("wallet is not encrypted")
	}

	// Allow re-unlock to change staking-only mode or extend/reset timeout
	// Legacy C++ behavior: re-unlock with same mode is rejected, but mode change is allowed
	if w.unlocked && w.unlockedStakingOnly && stakingOnly {
		return fmt.Errorf("wallet is already unlocked for staking only")
	}

	// Cancel any pending auto-lock before proceeding (prevents goroutine leak on re-unlock)
	if w.autoLockCancel != nil {
		w.autoLockCancel()
		w.autoLockCancel = nil
	}

	if w.wdb == nil {
		return fmt.Errorf("wallet database not initialized")
	}

	// Success flag for passphrase cleanup on error
	success := false

	// Guard to zero w.passphrase if unlock fails partway through
	defer func() {
		if !success && w.passphrase != nil {
			// Zero stored passphrase memory on failure
			for i := range w.passphrase {
				w.passphrase[i] = 0
			}
			w.passphrase = nil
		}
	}()

	// Unlock wallet database
	if err := w.wdb.Unlock(passphrase); err != nil {
		return fmt.Errorf("incorrect passphrase: %w", err)
	}

	// Mark as unlocked
	w.unlocked = true
	w.unlockedStakingOnly = stakingOnly
	w.passphrase = make([]byte, len(passphrase))
	copy(w.passphrase, passphrase)
	if duration > 0 {
		w.unlockTime = time.Now().Add(duration)
	} else {
		w.unlockTime = time.Time{} // Zero time means no auto-lock
	}

	// Decrypt HD chain seed if wallet was encrypted
	hdChain, isEncrypted, err := w.wdb.ReadHDChain()
	if err == nil && isEncrypted && hdChain.Crypted && len(hdChain.Seed) > 0 && len(hdChain.ChainID) > 0 {
		// Get encryption key from wallet database
		w.wdb.mu.RLock()
		encryptionKey := w.wdb.encryptionKey
		w.wdb.mu.RUnlock()

		if encryptionKey != nil {
			// Use first 16 bytes of chain ID as IV (matches legacy EncryptSecret behavior)
			// Chain ID is SHA256(seed), so it's deterministic and unique per wallet
			// Note: validateChainID already checked in ReadHDChain(), but we need at least 16 bytes for IV
			if len(hdChain.ChainID) < 16 {
				return fmt.Errorf("corrupted wallet: chain ID too short for IV derivation (%d bytes)", len(hdChain.ChainID))
			}

			iv := hdChain.ChainID[:16]

			// Decrypt the HD chain seed
			decryptedSeed, err := walletcrypto.DecryptSecret(encryptionKey, hdChain.Seed, iv)
			if err != nil {
				w.logger.WithError(err).Warn("Failed to decrypt HD chain seed")
			} else {
				// Validate seed length (BIP32 seeds are 16-64 bytes)
				if len(decryptedSeed) < 16 || len(decryptedSeed) > 64 {
					w.logger.WithField("length", len(decryptedSeed)).
						Warn("Decrypted HD seed has invalid length, expected 16-64 bytes")
				} else {
					// Verify decrypted seed matches chain ID (validation check)
					// ChainID = DoubleHash256(seed) = SHA256(SHA256(seed)) - Bitcoin standard
					expectedChainID := crypto.DoubleHash256(decryptedSeed)

					if bytes.Equal(expectedChainID, hdChain.ChainID) {
						// Recreate master key from decrypted seed
						masterKey, err := NewHDKeyFromSeed(decryptedSeed, w.config.Network)
						if err != nil {
							w.logger.WithError(err).Warn("Failed to restore master key from decrypted seed")
						} else {
							w.masterKey = masterKey
							w.logger.Debug("Master key restored and validated from encrypted HD chain")
						}
					} else {
						w.logger.Warn("Decrypted seed chain ID does not match stored chain ID")
					}
				}

				// Securely zero decrypted seed
				for i := range decryptedSeed {
					decryptedSeed[i] = 0
				}
			}
		}
	}

	// Load private keys for existing addresses now that wallet is unlocked
	// This is needed for legacy (non-HD) wallets where keys are stored in DB
	for address, addr := range w.addresses {
		if addr.PrivKey == nil && addr.PubKey != nil {
			// Try to load private key from database
			privKeyBytes, err := w.wdb.ReadKey(addr.PubKey.SerializeCompressed())
			if err == nil {
				privKey, err := crypto.ParsePrivateKeyFromBytes(privKeyBytes)
				if err == nil {
					addr.PrivKey = privKey
					w.logger.WithField("address", address).Debug("Loaded private key after unlock")
				}
				// Securely zero private key bytes from memory after use
				for i := range privKeyBytes {
					privKeyBytes[i] = 0
				}
			}
		}
	}

	if stakingOnly {
		w.logger.Info("Wallet unlocked for staking only")
	} else {
		w.logger.Info("Wallet unlocked")
	}

	// Schedule auto-lock (only if duration > 0; duration=0 means unlock until closed)
	if duration > 0 {
		ctx, cancel := context.WithCancel(context.Background())
		w.autoLockCancel = cancel
		go func() {
			select {
			case <-time.After(duration):
				w.Lock()
			case <-ctx.Done():
				// Cancelled by re-unlock or explicit lock, don't auto-lock
				return
			}
		}()
	}

	// Copy unlock callback (Lock-Copy-Invoke pattern)
	cb := w.onUnlockCallback

	// Mark success - passphrase should be kept in memory
	success = true

	// Invoke callback in goroutine to avoid deadlock under w.mu
	// (StartStaking calls wallet.IsLocked which needs RLock;
	//  StartStaking has atomic CAS guard so concurrent calls are safe)
	if cb != nil {
		go cb()
	}

	return nil
}

// RestoreToStakingOnly transitions the wallet from fully unlocked back to staking-only mode.
// The passphrase remains in memory; the wallet stays unlocked for staking operations.
// Any pending auto-lock timer is cancelled — staking-only mode is indefinite.
// Called after a temporary full-unlock for a specific operation (send, masternode start).
func (w *Wallet) RestoreToStakingOnly() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.encrypted {
		return fmt.Errorf("wallet is not encrypted")
	}
	if !w.unlocked {
		return fmt.Errorf("wallet is not unlocked")
	}
	if w.unlockedStakingOnly {
		return fmt.Errorf("wallet is already in staking-only mode")
	}

	// Cancel the temporary full-unlock auto-lock timer
	if w.autoLockCancel != nil {
		w.autoLockCancel()
		w.autoLockCancel = nil
	}

	w.unlockedStakingOnly = true
	w.unlockTime = time.Time{} // staking-only is indefinite
	w.logger.Info("Wallet restored to staking-only mode")
	return nil
}

// Lock locks the wallet
func (w *Wallet) Lock() error {
	w.mu.Lock()

	if !w.encrypted {
		w.mu.Unlock()
		return fmt.Errorf("wallet is not encrypted")
	}

	if !w.unlocked {
		w.mu.Unlock()
		return fmt.Errorf("wallet is already locked")
	}

	w.clearSecretsLocked()
	cb := w.onLockCallback
	w.logger.Info("Wallet locked")
	w.mu.Unlock()

	// Invoke callback outside mutex to avoid deadlock with staking worker
	// (StopStaking waits for worker goroutine which calls wallet.IsLocked via RLock)
	if cb != nil {
		cb()
	}

	return nil
}

// clearSecretsLocked clears private keys, passphrase, and resets lock state.
// Caller must hold w.mu.
func (w *Wallet) clearSecretsLocked() {
	// Lock wallet database
	if w.wdb != nil {
		w.wdb.Lock()
	}

	// Clear private keys from memory for security
	// Legacy wallets load keys on unlock, so we clear them on lock
	for _, addr := range w.addresses {
		if addr.PrivKey != nil {
			// Zero out private key bytes
			privKeyBytes := addr.PrivKey.Bytes()
			for i := range privKeyBytes {
				privKeyBytes[i] = 0
			}
			addr.PrivKey = nil
		}
	}

	// Clear passphrase from memory
	if w.passphrase != nil {
		for i := range w.passphrase {
			w.passphrase[i] = 0
		}
		w.passphrase = nil
	}

	// Cancel any pending auto-lock goroutine (prevents race if Lock called before timer)
	if w.autoLockCancel != nil {
		w.autoLockCancel()
		w.autoLockCancel = nil
	}

	w.unlocked = false
	w.unlockedStakingOnly = false
	w.unlockTime = time.Time{} // Clear stale unlock time (matches C++ LockWallet nWalletUnlockTime=0)
}

// ChangePassphrase changes the wallet encryption passphrase
func (w *Wallet) ChangePassphrase(oldPassphrase, newPassphrase []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Zero both passphrases from memory after use (security)
	defer func() {
		for i := range oldPassphrase {
			oldPassphrase[i] = 0
		}
		for i := range newPassphrase {
			newPassphrase[i] = 0
		}
	}()

	if !w.encrypted {
		return fmt.Errorf("wallet is not encrypted")
	}

	if len(newPassphrase) == 0 {
		return fmt.Errorf("new passphrase cannot be empty")
	}

	// Remember lock state to restore after passphrase change (matches C++ behavior)
	wasLocked := !w.unlocked

	// Lock wallet before changing passphrase (clears in-memory secrets)
	w.unlocked = false
	w.unlockedStakingOnly = false
	if w.wdb.encryptionKey != nil {
		for i := range w.wdb.encryptionKey {
			w.wdb.encryptionKey[i] = 0
		}
		w.wdb.encryptionKey = nil
	}
	if w.passphrase != nil {
		for i := range w.passphrase {
			w.passphrase[i] = 0
		}
		w.passphrase = nil
	}

	// Delegate to WalletDB: unlocks master key with old passphrase,
	// re-wraps same encryption key with new passphrase, writes to DB
	if err := w.wdb.ChangePassphrase(oldPassphrase, newPassphrase); err != nil {
		return err
	}

	// Re-lock if wallet was locked before the change
	if wasLocked {
		w.wdb.Lock()
	}

	w.logger.Info("Wallet passphrase changed successfully")
	return nil
}

// GetNewAddress generates a new receiving address
func (w *Wallet) GetNewAddress(label string) (string, error) {
	// Check if wallet is locked (only for encrypted wallets)
	if w.IsLocked() {
		return "", fmt.Errorf("wallet is locked")
	}

	return w.addrMgr.GetNewAddress(0, label)
}

// GetChangeAddress gets a change address
func (w *Wallet) GetChangeAddress() (string, error) {
	// Check if wallet is locked (only for encrypted wallets)
	if w.IsLocked() {
		return "", fmt.Errorf("wallet is locked")
	}

	return w.addrMgr.GetChangeAddress(0)
}

// GetChainHeight returns the cached chain height for confirmation calculations
func (w *Wallet) GetChainHeight() uint32 {
	w.heightMu.RLock()
	defer w.heightMu.RUnlock()
	return w.cachedChainHeight
}

// GetBalance returns the wallet balance by summing UTXOs
func (w *Wallet) GetBalance() *Balance {
	// Get cached chain height BEFORE acquiring wallet lock (avoid lock ordering issues)
	w.heightMu.RLock()
	currentHeight := w.cachedChainHeight
	w.heightMu.RUnlock()

	w.mu.RLock()
	defer w.mu.RUnlock()

	balance := &Balance{}

	// Note: account-level balances (w.accounts[*].Balance) are NOT summed here.
	// The UTXO iteration below is the single authoritative source of truth for balance
	// calculation, preventing double-counting between account tracking and UTXO state.

	// Snapshot pending-spent outpoints to exclude from confirmed balance
	w.pendingMu.RLock()
	pendingSpentSet := make(map[types.Outpoint]struct{}, len(w.pendingSpent))
	for k := range w.pendingSpent {
		pendingSpentSet[k] = struct{}{}
	}
	// Include pending UTXO values (our change + received outputs) in Unconfirmed
	for _, putxo := range w.pendingUTXOs {
		balance.Unconfirmed += putxo.Output.Value
	}
	w.pendingMu.RUnlock()

	// Calculate balance from confirmed UTXOs
	for _, utxo := range w.utxos {
		if !utxo.Spendable {
			continue // Skip unspendable UTXOs
		}

		// Skip UTXOs being spent by pending transactions —
		// their value moves to pending UTXOs (change) counted above
		if _, isSpent := pendingSpentSet[utxo.Outpoint]; isSpent {
			continue
		}

		// Calculate current confirmations dynamically
		var confirmations int32
		if currentHeight >= uint32(utxo.BlockHeight) {
			confirmations = int32(currentHeight) - utxo.BlockHeight + 1
		} else {
			// Block height is in future - possible during reorg or clock skew
			w.logger.WithFields(logrus.Fields{
				"utxo_height":    utxo.BlockHeight,
				"current_height": currentHeight,
				"outpoint":       utxo.Outpoint.String(),
			}).Debug("UTXO block height exceeds current chain height")
			confirmations = 0
		}

		// Check if immature (coinbase/coinstake with < CoinbaseMaturity confirmations)
		if (utxo.IsCoinbase || utxo.IsStake) && confirmations < int32(w.config.CoinbaseMaturity) {
			balance.Immature += utxo.Output.Value
		} else if confirmations >= int32(w.config.MinConfirmations) {
			balance.Confirmed += utxo.Output.Value
		} else {
			balance.Unconfirmed += utxo.Output.Value
		}
	}

	return balance
}

// ListTransactions returns recent transactions
func (w *Wallet) ListTransactions(count int, skip int) ([]*WalletTransaction, error) {
	// Get cached chain height BEFORE acquiring wallet lock (avoid lock ordering issues)
	w.heightMu.RLock()
	currentHeight := w.cachedChainHeight
	w.heightMu.RUnlock()

	w.mu.RLock()
	defer w.mu.RUnlock()

	// Collect all confirmed transactions with recalculated confirmations.
	// Filter out entries from orphaned blocks — during active staking, locally staked
	// blocks may be accepted locally but rejected by the network. Their entries persist
	// in w.transactions until fork recovery runs disconnectBlock. We detect these by
	// verifying that the block hash at the stored height matches the entry's BlockHash.
	txs := make([]*WalletTransaction, 0, len(w.transactions))
	for _, tx := range w.transactions {
		txCopy := *tx
		if txCopy.BlockHeight > 0 && currentHeight >= uint32(txCopy.BlockHeight) {
			// Verify this entry is from a canonical block (not an orphaned staking attempt)
			if canonicalHash, err := w.storage.GetBlockHashByHeight(uint32(txCopy.BlockHeight)); err == nil && canonicalHash != txCopy.BlockHash {
				continue // Orphaned block — skip
			}
			txCopy.Confirmations = int32(currentHeight) - txCopy.BlockHeight + 1
		} else if txCopy.BlockHeight == 0 {
			txCopy.Confirmations = 0
		}
		txs = append(txs, &txCopy)
	}

	// Append pending transactions (0 confirmations).
	// Skip entries whose hash already exists in w.transactions (dedup for evicted txs
	// that re-entered the mempool — present in both maps simultaneously).
	w.pendingMu.RLock()
	for _, ptx := range w.pendingTxs {
		if inConfirmed := w.hasTransactionByHash(ptx.Hash); inConfirmed {
			continue // Already included from w.transactions above
		}
		txCopy := *ptx
		txCopy.Confirmations = 0
		txs = append(txs, &txCopy)
	}
	w.pendingMu.RUnlock()

	// Sort by time (desc) with deterministic tiebreaker.
	// All transactions (confirmed, pending, evicted) sort uniformly by time
	// so that old evicted staking attempts don't displace recent confirmed entries.
	sort.Slice(txs, func(i, j int) bool {
		// Primary sort: time (descending - newest first)
		if !txs[i].Time.Equal(txs[j].Time) {
			return txs[i].Time.After(txs[j].Time)
		}
		// Deterministic tiebreaker: SeqNum then TxID to ensure stable order
		// for transactions with identical time (e.g. same block).
		if txs[i].SeqNum != txs[j].SeqNum {
			return txs[i].SeqNum > txs[j].SeqNum
		}
		return txs[i].Hash.CompareTo(txs[j].Hash) > 0
	})

	// Apply skip and count
	// count <= 0 means return all transactions (unlimited)
	start := skip
	if start >= len(txs) {
		return []*WalletTransaction{}, nil
	}

	end := len(txs) // Default to all remaining transactions
	if count > 0 {
		end = start + count
		if end > len(txs) {
			end = len(txs)
		}
	}

	return txs[start:end], nil
}

// TransactionFilterParams contains filter/sort/pagination parameters for ListTransactionsFiltered.
type TransactionFilterParams struct {
	Page     int
	PageSize int

	DateFilter    string // "all","today","week","month","lastMonth","year","range"
	DateRangeFrom string // ISO date for "range"
	DateRangeTo   string // ISO date for "range"
	TypeFilter    string // "all","mostCommon","received","sent","toYourself","mined","minted","masternode", etc.
	SearchText    string
	MinAmount     float64 // minimum absolute amount in satoshis

	WatchOnlyFilter  string // "all","yes","no"
	HideOrphanStakes bool

	SortColumn    string // "date","type","address","amount"
	SortDirection string // "asc","desc"
}

// TransactionFilterResult holds paginated results from ListTransactionsFiltered.
type TransactionFilterResult struct {
	Transactions []*WalletTransaction
	Total        int // count after filtering
	TotalAll     int // count before filtering
}

// ListTransactionsFiltered returns a filtered, sorted, paginated slice of wallet transactions.
func (w *Wallet) ListTransactionsFiltered(params TransactionFilterParams) (TransactionFilterResult, error) {
	// Get cached chain height BEFORE acquiring wallet lock (avoid lock ordering issues)
	w.heightMu.RLock()
	currentHeight := w.cachedChainHeight
	w.heightMu.RUnlock()

	w.mu.RLock()
	defer w.mu.RUnlock()

	// Collect all confirmed transactions with recalculated confirmations.
	// Filter out entries from orphaned blocks (same logic as ListTransactions).
	all := make([]*WalletTransaction, 0, len(w.transactions))
	for _, tx := range w.transactions {
		txCopy := *tx
		if txCopy.BlockHeight > 0 && currentHeight >= uint32(txCopy.BlockHeight) {
			if canonicalHash, err := w.storage.GetBlockHashByHeight(uint32(txCopy.BlockHeight)); err == nil && canonicalHash != txCopy.BlockHash {
				continue // Orphaned block — skip
			}
			txCopy.Confirmations = int32(currentHeight) - txCopy.BlockHeight + 1
		} else if txCopy.BlockHeight == 0 {
			txCopy.Confirmations = 0
		}
		all = append(all, &txCopy)
	}

	// Append pending transactions (0 confirmations).
	// Skip entries whose hash already exists in w.transactions (dedup for evicted txs
	// that re-entered the mempool — present in both maps simultaneously).
	w.pendingMu.RLock()
	for _, ptx := range w.pendingTxs {
		if inConfirmed := w.hasTransactionByHash(ptx.Hash); inConfirmed {
			continue // Already included from w.transactions above
		}
		txCopy := *ptx
		txCopy.Confirmations = 0
		all = append(all, &txCopy)
	}
	w.pendingMu.RUnlock()

	totalAll := len(all)

	// Apply filters
	filtered := make([]*WalletTransaction, 0, len(all))
	for _, tx := range all {
		if params.HideOrphanStakes && isOrphanStake(tx) {
			continue
		}
		if !matchesDateFilter(tx.Time, params.DateFilter, params.DateRangeFrom, params.DateRangeTo) {
			continue
		}
		if !matchesTypeFilterWithComment(string(tx.Category), tx.Comment, params.TypeFilter) {
			continue
		}
		if !matchesWatchOnlyFilter(tx.WatchOnly, params.WatchOnlyFilter) {
			continue
		}
		if !matchesSearchText(tx.Address, tx.Label, params.SearchText) {
			continue
		}
		if params.MinAmount > 0 && math.Abs(float64(tx.Amount)) < params.MinAmount {
			continue
		}
		filtered = append(filtered, tx)
	}

	total := len(filtered)

	// Sort
	sortTransactions(filtered, params.SortColumn, params.SortDirection)

	// Paginate
	// PageSize <= 0 means return all filtered results (no pagination)
	pageSize := params.PageSize
	var pageSlice []*WalletTransaction
	if pageSize <= 0 {
		pageSlice = filtered
	} else {
		page := params.Page
		if page < 1 {
			page = 1
		}

		start := (page - 1) * pageSize
		if start >= total {
			// Clamp to last page
			if total > 0 {
				start = ((total - 1) / pageSize) * pageSize
			} else {
				start = 0
			}
		}
		end := start + pageSize
		if end > total {
			end = total
		}

		if total > 0 {
			pageSlice = filtered[start:end]
		}
	}

	return TransactionFilterResult{
		Transactions: pageSlice,
		Total:        total,
		TotalAll:     totalAll,
	}, nil
}

// --- filter helper functions ---

func isOrphanStake(tx *WalletTransaction) bool {
	return (tx.Category == TxCategoryCoinStake || tx.Category == TxCategoryCoinBase || tx.Category == TxCategoryGenerate) &&
		tx.Confirmations < 1
}

func matchesDateFilter(txTime time.Time, filter, rangeFrom, rangeTo string) bool {
	if filter == "" || filter == "all" {
		return true
	}

	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	switch filter {
	case "today":
		return !txTime.Before(startOfDay)
	case "week":
		startOfWeek := startOfDay.AddDate(0, 0, -int(startOfDay.Weekday()))
		return !txTime.Before(startOfWeek)
	case "month":
		startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		return !txTime.Before(startOfMonth)
	case "lastMonth":
		startOfLastMonth := time.Date(now.Year(), now.Month()-1, 1, 0, 0, 0, 0, now.Location())
		endOfLastMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Add(-time.Nanosecond)
		return !txTime.Before(startOfLastMonth) && !txTime.After(endOfLastMonth)
	case "year":
		startOfYear := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, now.Location())
		return !txTime.Before(startOfYear)
	case "range":
		if rangeFrom == "" || rangeTo == "" {
			return true
		}
		fromDate, err1 := time.Parse("2006-01-02", rangeFrom)
		toDate, err2 := time.Parse("2006-01-02", rangeTo)
		if err1 != nil || err2 != nil {
			return true
		}
		toDate = toDate.Add(24*time.Hour - time.Nanosecond) // include entire "to" day
		return !txTime.Before(fromDate) && !txTime.After(toDate)
	default:
		return true
	}
}

func matchesTypeFilter(category string, filter string) bool {
	if filter == "" || filter == "all" {
		return true
	}

	if filter == "mostCommon" {
		return true
	}

	switch filter {
	case "received":
		return category == "receive"
	case "sent":
		return category == "send"
	case "toYourself":
		return category == "send_to_self"
	case "mined":
		return category == "generate"
	case "minted":
		return category == "stake"
	case "masternode":
		return category == "masternode"
	case "other":
		known := map[string]bool{
			"receive": true, "send": true, "send_to_self": true,
			"generate": true, "stake": true, "masternode": true,
			"coinbase": true,
		}
		return !known[category]
	default:
		return true
	}
}

// matchesTypeFilterWithComment extends matchesTypeFilter with comment-based filtering.
// Used for types like "consolidation" that are distinguished by comment, not category.
func matchesTypeFilterWithComment(category, comment, filter string) bool {
	if filter == "consolidation" {
		return category == "send_to_self" && comment == "autocombine"
	}
	if filter == "toYourself" {
		// Exclude autocombine transactions from "to yourself" filter
		return category == "send_to_self" && comment != "autocombine"
	}
	return matchesTypeFilter(category, filter)
}

func matchesWatchOnlyFilter(isWatchOnly bool, filter string) bool {
	switch filter {
	case "yes":
		return isWatchOnly
	case "no":
		return !isWatchOnly
	default:
		return true
	}
}

func matchesSearchText(address, label, search string) bool {
	if search == "" {
		return true
	}
	s := strings.ToLower(search)
	return strings.Contains(strings.ToLower(address), s) ||
		strings.Contains(strings.ToLower(label), s)
}

func sortTransactions(txs []*WalletTransaction, column, direction string) {
	sort.Slice(txs, func(i, j int) bool {
		var cmp int
		switch column {
		case "type":
			cmp = strings.Compare(string(txs[i].Category), string(txs[j].Category))
		case "address":
			cmp = strings.Compare(txs[i].Address, txs[j].Address)
		case "amount":
			if txs[i].Amount < txs[j].Amount {
				cmp = -1
			} else if txs[i].Amount > txs[j].Amount {
				cmp = 1
			}
		default: // "date" or empty — sort by time uniformly
			if txs[i].Time.Before(txs[j].Time) {
				cmp = -1
			} else if txs[i].Time.After(txs[j].Time) {
				cmp = 1
			}
		}

		// Deterministic tiebreaker: SeqNum then TxID to ensure stable order
		// for transactions with identical primary sort keys (e.g. same block).
		if cmp == 0 {
			if txs[i].SeqNum != txs[j].SeqNum {
				if txs[i].SeqNum < txs[j].SeqNum {
					cmp = -1
				} else {
					cmp = 1
				}
			} else {
				cmp = txs[i].Hash.CompareTo(txs[j].Hash)
			}
		}

		if direction == "asc" {
			return cmp < 0
		}
		return cmp > 0
	})
}

// GetTransaction returns a transaction by hash
func (w *Wallet) GetTransaction(hash types.Hash) (*WalletTransaction, error) {
	// Get cached chain height BEFORE acquiring wallet lock (avoid lock ordering issues)
	w.heightMu.RLock()
	currentHeight := w.cachedChainHeight
	w.heightMu.RUnlock()

	w.mu.RLock()
	defer w.mu.RUnlock()

	tx, exists := w.getTransactionByHash(hash)
	if !exists {
		// Check pending transactions
		w.pendingMu.RLock()
		ptx, pExists := w.pendingTxs[hash]
		w.pendingMu.RUnlock()
		if !pExists {
			return nil, fmt.Errorf("transaction not found")
		}
		txCopy := *ptx
		txCopy.Confirmations = 0
		return &txCopy, nil
	}

	// Return a copy with dynamically recalculated confirmations
	// (matches the pattern in ListTransactions and ListTransactionsFiltered)
	txCopy := *tx
	if txCopy.BlockHeight > 0 && currentHeight >= uint32(txCopy.BlockHeight) {
		txCopy.Confirmations = int32(currentHeight) - txCopy.BlockHeight + 1
	} else if txCopy.BlockHeight == 0 {
		txCopy.Confirmations = 0
	}

	return &txCopy, nil
}

// GetUTXOs returns all unspent transaction outputs
func (w *Wallet) GetUTXOs(includeUnconfirmed bool) ([]*UTXO, error) {
	return w.getUTXOsInternal(includeUnconfirmed, false)
}

// GetUTXOsForMasternode returns UTXOs including masternode-locked collaterals.
// This is used for masternode operations where locked collaterals need to be visible.
// Equivalent to legacy C++ AvailableCoins with ONLY_10000 mode.
func (w *Wallet) GetUTXOsForMasternode(includeUnconfirmed bool) ([]*UTXO, error) {
	return w.getUTXOsInternal(includeUnconfirmed, true)
}

// getUTXOsInternal is the internal implementation for UTXO retrieval.
// If includeMasternodeLocked is true, masternode collateral UTXOs are included.
func (w *Wallet) getUTXOsInternal(includeUnconfirmed bool, includeMasternodeLocked bool) ([]*UTXO, error) {
	// Get current chain height BEFORE acquiring wallet lock (avoid lock ordering issues)
	w.heightMu.RLock()
	currentHeight := w.cachedChainHeight
	w.heightMu.RUnlock()

	w.mu.RLock()
	defer w.mu.RUnlock()

	utxos := make([]*UTXO, 0)
	for _, utxo := range w.utxos {
		if !utxo.Spendable {
			continue
		}

		// HIDE masternode collateral UTXOs from listing (unless explicitly included)
		if !includeMasternodeLocked && w.isCollateralUTXOLocked(utxo.Outpoint) {
			continue
		}

		// Calculate current confirmations dynamically
		var confirmations int32
		if currentHeight >= uint32(utxo.BlockHeight) {
			confirmations = int32(currentHeight) - utxo.BlockHeight + 1
		}

		// Check maturity for coinbase/stake outputs (need 60 confirmations)
		if (utxo.IsCoinbase || utxo.IsStake) && confirmations < 60 {
			continue // Immature, skip
		}

		// Filter by minimum confirmations
		if !includeUnconfirmed && confirmations < int32(w.config.MinConfirmations) {
			continue
		}

		utxos = append(utxos, utxo)
	}

	return utxos, nil
}

// GetAddressInfo returns information about an address
func (w *Wallet) GetAddressInfo(address string) (*AddressInfo, error) {
	return w.addrMgr.GetAddressInfo(address)
}

// ListAddresses returns all addresses
func (w *Wallet) ListAddresses() ([]*Address, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	addresses := make([]*Address, 0, len(w.addresses))
	for _, addr := range w.addresses {
		addresses = append(addresses, addr)
	}

	return addresses, nil
}

// ImportPrivateKey imports a private key
func (w *Wallet) ImportPrivateKey(keyWIF string, label string, rescan bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.isLockedForSendingLocked() {
		return ErrWalletLocked
	}

	// Decode WIF private key
	privKey, compressed, err := crypto.DecodePrivateKeyWIF(keyWIF)
	if err != nil {
		return fmt.Errorf("invalid WIF private key: %w", err)
	}

	// Generate address from private key
	pubKey := privKey.PublicKey()
	var pubKeyBytes []byte
	if compressed {
		pubKeyBytes = pubKey.CompressedBytes()
	} else {
		pubKeyBytes = pubKey.Bytes()
	}
	pubKeyHash := crypto.Hash160(pubKeyBytes)

	// Add version byte
	var version byte
	switch w.config.Network {
	case MainNet:
		version = crypto.MainNetPubKeyHashAddrID // 0x49 - TWINS W... addresses
	case TestNet, RegTest:
		version = crypto.TestNetPubKeyHashAddrID // 0x6f
	}

	payload := append([]byte{version}, pubKeyHash...)
	checksum := crypto.DoubleHash256(payload)[:4]
	fullPayload := append(payload, checksum...)
	address := crypto.Base58Encode(fullPayload)

	// Check if address already exists
	if _, exists := w.addresses[address]; exists {
		return fmt.Errorf("address already in wallet")
	}

	// Add to wallet
	addr := &Address{
		Address:    address,
		PubKey:     pubKey,
		PrivKey:    privKey,
		ScriptType: ScriptTypeP2PKH,
		Account:    0,
		Label:      label,
	}

	w.addresses[address] = addr

	// Also add to binary address map for fast script matching
	if binaryKey, ok := w.addressToBinaryKey(address); ok {
		w.addressesBinary[binaryKey] = addr
	}

	// Save imported key to wallet.dat if WalletDB is initialized
	if w.wdb != nil {
		// Get master key fingerprint (if wallet has HD master key)
		var masterKeyID []byte
		if w.masterKey != nil {
			masterKeyID = w.masterKey.Fingerprint()
		}

		// Create key metadata for imported key
		metadata := &legacy.CKeyMetadata{
			Version:       1,
			CreateTime:    time.Now().Unix(),
			HDKeyPath:     "", // Imported keys don't have HD path
			HDMasterKeyID: masterKeyID,
		}

		// Write key to database
		pubKeyBytes := pubKey.CompressedBytes()
		privKeyBytes := privKey.Bytes()
		if err := w.wdb.WriteKey(pubKeyBytes, privKeyBytes, metadata); err != nil {
			return fmt.Errorf("failed to save imported key to wallet.dat: %w", err)
		}

		// Write address label to wallet.dat (matches legacy SetAddressBook behavior)
		if label != "" {
			if err := w.wdb.WriteName(address, label); err != nil {
				w.logger.WithError(err).Warn("Failed to save address label to wallet.dat")
			}
		}

		// Write address purpose to wallet.dat (imported keys are "receive" addresses)
		if err := w.wdb.WritePurpose(address, "receive"); err != nil {
			w.logger.WithError(err).Warn("Failed to save address purpose to wallet.dat")
		}
	}

	w.logger.WithField("address", address).Debug("Private key imported")

	// Perform blockchain rescan if requested
	if rescan {
		// Use internal locked version to avoid mutex deadlock
		err := w.rescanBlockchainLocked(address)
		if err != nil {
			w.logger.WithError(err).WithField("address", address).Warn("Blockchain rescan failed")
			// Don't fail the import - key is already added
			// User can manually rescan later if needed
		}
	}

	return nil
}

// DumpPrivKey exports the private key for a given address in WIF format
func (w *Wallet) DumpPrivKey(address string) (string, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return w.dumpPrivKeyLocked(address)
}

// dumpPrivKeyLocked is the lock-free implementation of DumpPrivKey.
// Caller must hold w.mu (RLock or Lock).
func (w *Wallet) dumpPrivKeyLocked(address string) (string, error) {
	// Note: masterKey is only needed for HD wallets, legacy wallets have keys in DB
	// So we don't check for masterKey here - legacy wallets work without it

	// Check if wallet is locked or staking-only
	if w.isLockedForSendingLocked() {
		return "", ErrWalletLocked
	}

	// Get address from wallet
	addr, exists := w.addresses[address]
	if !exists {
		return "", fmt.Errorf("address %s not found in wallet", address)
	}

	// Get private key - either from memory, database, or derive from HD chain
	var privKey *crypto.PrivateKey

	if addr.PrivKey != nil {
		privKey = addr.PrivKey
	} else {
		// Watch-only addresses have no public key — cannot derive private key
		if addr.PubKey == nil {
			return "", fmt.Errorf("no public key for address (watch-only): %s", address)
		}

		// Key not in memory - try to read from database (for encrypted wallets)
		if w.wdb == nil {
			return "", fmt.Errorf("wallet database not initialized")
		}

		// Try compressed pubkey first (33 bytes), then uncompressed (65 bytes)
		// Legacy wallets may store keys under either format
		compressedPubKey := addr.PubKey.CompressedBytes()
		uncompressedPubKey := addr.PubKey.Bytes() // Returns uncompressed (65 bytes)

		privKeyBytes, err := w.wdb.ReadKey(compressedPubKey)
		if err != nil {
			privKeyBytes, err = w.wdb.ReadKey(uncompressedPubKey)
		}

		if err == nil {
			privKey, err = crypto.ParsePrivateKeyFromBytes(privKeyBytes)
			// Securely zero private key bytes from memory after use
			for i := range privKeyBytes {
				privKeyBytes[i] = 0
			}
			if err != nil {
				return "", fmt.Errorf("failed to parse private key: %w", err)
			}
		} else {
			// Key not in database - try HD wallet derivation
			// HD wallets don't store private keys, they derive them from seed
			privKey, err = w.deriveHDPrivKeyLocked(compressedPubKey)
			if err != nil {
				return "", fmt.Errorf("private key not available for address %s: %w", address, err)
			}
		}
	}

	// Determine network ID for WIF encoding
	var netID byte
	switch w.config.Network {
	case MainNet:
		netID = crypto.PrivateKeyID // 0x42 for TWINS MainNet
	case TestNet, RegTest:
		netID = crypto.PrivateKeyID // Same for testnet (use same WIF prefix)
	default:
		netID = crypto.PrivateKeyID
	}

	// Convert to WIF format (compressed = true for modern wallets)
	wif := crypto.EncodePrivateKeyWIF(privKey, true, netID)

	w.logger.WithField("address", address).Debug("Private key exported")
	return wif, nil
}

// deriveHDPrivKeyLocked derives a private key from HD chain seed for a given public key
// Assumes caller holds w.mu.RLock or w.mu.Lock
func (w *Wallet) deriveHDPrivKeyLocked(pubkey []byte) (*crypto.PrivateKey, error) {
	// Read HD pubkey entry to get derivation path
	hdPubKey, err := w.wdb.ReadHDPubKey(pubkey)
	if err != nil {
		return nil, fmt.Errorf("not an HD wallet key: %w", err)
	}

	// Use existing master key if available (already decrypted on wallet unlock)
	var masterKey *HDKey
	if w.masterKey != nil {
		masterKey = w.masterKey
	} else {
		// Need to derive master key from seed
		hdChain, isEncrypted, err := w.wdb.ReadHDChain()
		if err != nil {
			return nil, fmt.Errorf("failed to read HD chain: %w", err)
		}

		var seed []byte
		var needsZeroing bool // Track if we need to zero the seed after use
		if isEncrypted || hdChain.Crypted {
			// Need to decrypt seed - get encryption key from wdb
			w.wdb.mu.RLock()
			encryptionKey := w.wdb.encryptionKey
			w.wdb.mu.RUnlock()

			if encryptionKey == nil {
				return nil, fmt.Errorf("wallet is locked, cannot decrypt HD seed")
			}

			// Use first 16 bytes of chain ID as IV (matches legacy behavior)
			if len(hdChain.ChainID) < 16 {
				return nil, fmt.Errorf("corrupted wallet: chain ID too short")
			}
			iv := hdChain.ChainID[:16]

			// Decrypt the seed
			decryptedSeed, err := walletcrypto.DecryptSecret(encryptionKey, hdChain.Seed, iv)
			if err != nil {
				return nil, fmt.Errorf("failed to decrypt HD seed: %w", err)
			}
			seed = decryptedSeed
			needsZeroing = true // Mark for secure cleanup
		} else {
			seed = hdChain.Seed
		}

		// Securely zero decrypted seed after use
		if needsZeroing {
			defer func() {
				for i := range seed {
					seed[i] = 0
				}
			}()
		}

		if len(seed) == 0 {
			return nil, fmt.Errorf("HD chain seed is empty")
		}

		// Create master key from seed
		masterKey, err = NewHDKeyFromSeed(seed, w.config.Network)
		if err != nil {
			return nil, fmt.Errorf("failed to create master key from seed: %w", err)
		}
	}

	// Derive child key using path from CHDPubKey
	// Path: m/44'/{coinType}'/{account}'/{change}/{child}
	// TWINS coin type is 970
	path := &KeyPath{
		CoinType: 970, // TWINS ExtCoinType
		Account:  hdPubKey.AccountIndex,
		Change:   hdPubKey.ChangeIndex,
		Index:    hdPubKey.ExtPubKey.Child,
	}

	derivedKey, err := masterKey.DerivePath(path)
	if err != nil {
		return nil, fmt.Errorf("failed to derive key at path %s: %w", path.String(), err)
	}

	return derivedKey.PrivateKey(), nil
}

// RescanBlockchain scans the blockchain for transactions involving a specific address
// Uses GetUTXOsByAddress to get current unspent outputs and transaction history
func (w *Wallet) RescanBlockchain(address string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.rescanBlockchainLocked(address)
}

// RescanAllAddresses rescans the blockchain for all wallet addresses
// This is useful after the wallet is connected to an already-synced blockchain
func (w *Wallet) RescanAllAddresses() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if len(w.addresses) == 0 {
		w.logger.Debug("RescanAllAddresses: No addresses to rescan")
		return nil
	}

	w.logger.WithField("address_count", len(w.addresses)).Debug("Rescanning all wallet addresses")

	// Update cached chain height before rescan
	currentHeight, err := w.storage.GetChainHeight()
	if err == nil {
		w.heightMu.Lock()
		w.cachedChainHeight = currentHeight
		w.heightMu.Unlock()
	}

	for address := range w.addresses {
		if err := w.rescanBlockchainLocked(address); err != nil {
			w.logger.WithError(err).WithField("address", address).Warn("Failed to rescan address")
		}
	}

	// Recovery sweep: phantom-unspent UTXOs whose spending tx is in
	// storage but was never indexed under any of our addresses (both the
	// mark-spent AND address-index input-side writes were skipped by an
	// interrupted batch commit). The per-address reverse reconcile above
	// cannot find these, so we fall back to a full PrefixTransaction scan
	// across the currently-tracked unspent UTXOs. This is O(all tx in
	// storage) — expensive but only runs once per rescan pass.
	if err := w.phantomUnspentFullScanRecoveryLocked(); err != nil {
		w.logger.WithError(err).Warn("Phantom-unspent full scan recovery failed")
	}

	w.logger.WithFields(logrus.Fields{
		"utxo_count": len(w.utxos),
		"tx_count":   len(w.transactions),
	}).Debug("RescanAllAddresses complete")

	return nil
}

// phantomUnspentFullScanRecoveryLocked performs a one-shot full scan of
// PrefixTransaction to find spender transactions for any currently
// unspent wallet UTXOs whose spend was missed during block processing.
// Caller must hold w.mu (write lock). Called at the end of
// RescanAllAddresses after per-address reconciliation has run.
func (w *Wallet) phantomUnspentFullScanRecoveryLocked() error {
	if len(w.utxos) == 0 {
		return nil
	}

	outpoints := make(map[types.Outpoint]struct{}, len(w.utxos))
	for op := range w.utxos {
		outpoints[op] = struct{}{}
	}

	results, err := w.storage.FindAndMarkSpendersForOutpoints(outpoints)
	if err != nil {
		return fmt.Errorf("full-scan phantom-unspent recovery: %w", err)
	}
	if len(results) == 0 {
		w.logger.WithField("utxo_count", len(outpoints)).
			Debug("Phantom-unspent full scan: no unreconciled UTXOs found")
		return nil
	}

	// Update in-memory wallet state to match the new storage state:
	// drop each reconciled outpoint from w.utxos and subtract its value
	// from the owning address's confirmed balance.
	for op, info := range results {
		existing, exists := w.utxos[op]
		if !exists {
			continue
		}
		if bal := w.balances[existing.Address]; bal != nil {
			bal.Confirmed -= existing.Output.Value
		}
		delete(w.utxos, op)
		w.logger.WithFields(logrus.Fields{
			"outpoint": op.String(),
			"address":  existing.Address,
			"value":    existing.Output.Value,
			"spender":  info.SpenderTxHash.String(),
			"height":   info.SpenderHeight,
		}).Info("Phantom-unspent full scan: reconciled UTXO as spent")
	}

	w.logger.WithFields(logrus.Fields{
		"scanned_utxos": len(outpoints),
		"reconciled":    len(results),
	}).Info("Phantom-unspent full scan complete")

	return nil
}

// rescanBlockchainLocked is an internal version of RescanBlockchain that assumes the caller already holds w.mu
// This prevents deadlocks when called from functions that already hold the wallet lock (like LoadWallet)
func (w *Wallet) rescanBlockchainLocked(address string) error {
	w.logger.WithField("address", address).Debug("Starting blockchain rescan")

	// Get current chain height for confirmations calculation
	currentHeight, err := w.storage.GetChainHeight()
	if err != nil {
		w.logger.WithError(err).Warn("Failed to get current chain height")
		currentHeight = 0 // Fallback to 0 if unavailable
	}

	w.logger.WithFields(logrus.Fields{
		"address":      address,
		"chain_height": currentHeight,
	}).Debug("Rescan: Querying UTXOs from storage")

	// Storage already maintains the up-to-date UTXO set
	utxos, err := w.storage.GetUTXOsByAddress(address)
	if err != nil {
		w.logger.WithError(err).Warn("Failed to get UTXOs for address")
		return fmt.Errorf("failed to get UTXOs: %w", err)
	}

	w.logger.WithFields(logrus.Fields{
		"address":    address,
		"utxo_count": len(utxos),
	}).Debug("Rescan: Found UTXOs in storage")

	// Add UTXOs to wallet
	for _, utxo := range utxos {
		// Calculate confirmations
		var confirmations int32
		if currentHeight >= utxo.Height {
			confirmations = int32(currentHeight) - int32(utxo.Height) + 1
		}

		// Get actual block time for coin age calculation (legacy compliance)
		// Fallback to estimated time if block lookup fails
		var blockTime uint32
		if block, err := w.storage.GetBlockByHeight(utxo.Height); err == nil && block != nil {
			blockTime = block.Header.Timestamp
		} else {
			// Fallback: estimate block time (genesis time + height * 60s)
			blockTime = w.config.GenesisTimestamp + utxo.Height*60
		}

		// Create wallet UTXO entry
		walletUTXO := &UTXO{
			Outpoint:      utxo.Outpoint,
			Output:        utxo.Output,
			BlockHeight:   int32(utxo.Height),
			BlockTime:     blockTime,
			Confirmations: confirmations,
			IsCoinbase:    utxo.IsCoinbase,
			IsStake:       false, // Storage UTXO doesn't track this, assume false
			Spendable:     true,  // All UTXOs from storage are spendable
			Address:       address,
			Account:       0, // Imported keys go to account 0
		}
		w.utxos[utxo.Outpoint] = walletUTXO
	}

	// Get transaction history from address index for transaction list
	// Decode address to binary format (netID + hash160 = 21 bytes)
	addr, err := crypto.DecodeAddress(address)
	if err != nil {
		return fmt.Errorf("invalid address: %w", err)
	}

	// Create binary address: netID (1 byte) + hash160 (20 bytes)
	addressBinary := make([]byte, 21)
	addressBinary[0] = addr.NetID()
	copy(addressBinary[1:], addr.Hash160())

	// Get all transactions for this address from the address index
	addressTxs, err := w.storage.GetTransactionsByAddress(addressBinary)
	if err != nil {
		w.logger.WithError(err).Warn("Address index lookup failed, transactions list will be empty")
	} else {
		// Mark address as used if it has any transactions (matches legacy C++ behavior)
		if len(addressTxs) > 0 {
			if walletAddr, exists := w.addresses[address]; exists {
				walletAddr.Used = true
			}
		}

		// Track orphan tx hashes already cleaned to avoid redundant cleanup
		// (GetTransactionsByAddress can return multiple entries per tx — one per output/input)
		orphanCleaned := make(map[types.Hash]struct{})

		// Track stale txids: address index points to a transaction that no
		// longer exists in storage. These are collected and reconciled at the
		// end of this per-address rescan pass: address index entries removed
		// and any UTXO still marked as spent by one of these txids is unspent.
		staleTxids := make(map[types.Hash]struct{})

		// Process each transaction for transaction history
		for _, addrTx := range addressTxs {
			// Skip if already cleaned as orphan
			if _, cleaned := orphanCleaned[addrTx.TxHash]; cleaned {
				continue
			}
			if _, stale := staleTxids[addrTx.TxHash]; stale {
				continue
			}

			// Get transaction data with block location metadata
			// Using GetTransactionData instead of GetTransaction + GetBlock avoids
			// loading the entire block (which can fail if any tx in block is missing)
			txData, err := w.storage.GetTransactionData(addrTx.TxHash)
			if err != nil {
				// Distinguish NotFound (stale address index entry — self-heal)
				// from transient I/O errors (abort rescan to prevent damage).
				if storage.IsNotFoundError(err) {
					staleTxids[addrTx.TxHash] = struct{}{}
					w.logger.WithField("txhash", addrTx.TxHash.String()).
						Debug("Stale address index entry detected during rescan (tx not in storage)")
					continue
				}
				return fmt.Errorf("get transaction data for %s: %w", addrTx.TxHash.String(), err)
			}

			// Verify the transaction's block is on the main chain.
			// Stale address index entries from orphaned blocks (incomplete cleanup
			// during reorg) could reference blocks no longer on the main chain.
			// Without this check, orphan transactions would be indexed as confirmed,
			// and their inputs would appear spent (locked), preventing those UTXOs
			// from being used in new transactions.
			isOrphan := false
			mainChainHash, err := w.storage.GetBlockHashByHeight(txData.Height)
			if err != nil {
				// Distinguish "height not found" (orphan) from transient I/O errors.
				// Only treat as orphan if the height genuinely doesn't exist on chain.
				// Transient errors (Pebble I/O, corruption) should abort rescan to
				// prevent accidental deletion of valid transactions.
				if storage.IsNotFoundError(err) || isHeightNotFoundError(err) {
					isOrphan = true
				} else {
					return fmt.Errorf("storage error checking block at height %d: %w", txData.Height, err)
				}
			} else if mainChainHash != txData.BlockHash {
				isOrphan = true
			}

			if isOrphan {
				orphanCleaned[addrTx.TxHash] = struct{}{}

				w.logger.WithFields(logrus.Fields{
					"txhash": addrTx.TxHash.String(),
					"height": txData.Height,
				}).Warn("Orphan transaction detected during rescan, cleaning up")

				// Single atomic batch for all orphan cleanup operations:
				// delete created UTXOs + unspend consumed UTXOs + delete tx index
				batch := w.storage.NewBatch()

				// Delete UTXOs created by this orphan transaction (its outputs).
				// These may have already been loaded into w.utxos by the UTXO
				// loading phase above, so also remove them from the in-memory map.
				if txData.TxData != nil {
					for outIdx, output := range txData.TxData.Outputs {
						outpoint := types.Outpoint{Hash: addrTx.TxHash, Index: uint32(outIdx)}
						utxo := &types.UTXO{
							Outpoint: outpoint,
							Output:   output,
							Height:   txData.Height,
						}
						if err := batch.DeleteUTXOWithData(outpoint, utxo); err != nil {
							w.logger.WithError(err).WithFields(logrus.Fields{
								"txhash":   addrTx.TxHash.String(),
								"outpoint": outpoint.String(),
							}).Debug("Failed to delete orphan UTXO from storage")
						}
						// Remove from in-memory wallet UTXO set (loaded earlier in rescan)
						if existingUTXO, exists := w.utxos[outpoint]; exists {
							if w.balances[existingUTXO.Address] != nil {
								w.balances[existingUTXO.Address].Confirmed -= existingUTXO.Output.Value
							}
							delete(w.utxos, outpoint)
						}
					}
				}

				// Unspend UTXOs consumed by this orphan transaction, but only if
				// the UTXO's current spender matches this orphan tx. If a different
				// main-chain transaction already spent the UTXO, do not touch it.
				if txData.TxData != nil && !txData.TxData.IsCoinbase() {
					orphanTxHash := addrTx.TxHash
					for _, input := range txData.TxData.Inputs {
						utxo, err := w.storage.GetUTXO(input.PreviousOutput)
						if err != nil {
							continue // UTXO not found or error — skip
						}
						// Only unspend if this orphan tx is the recorded spender
						if utxo.SpendingTxHash == orphanTxHash {
							if err := batch.UnspendUTXO(input.PreviousOutput); err != nil {
								w.logger.WithError(err).WithFields(logrus.Fields{
									"txhash":   addrTx.TxHash.String(),
									"outpoint": input.PreviousOutput.String(),
								}).Warn("Failed to unspend UTXO for orphan transaction")
							}
						}
					}
				}

				// Delete orphan transaction from the transaction index and its
				// address history entries (DeleteTransaction cleans both tx index
				// and address history for all outputs/inputs of this transaction)
				if err := batch.DeleteTransaction(addrTx.TxHash, txData.Height); err != nil {
					w.logger.WithError(err).WithField("txhash", addrTx.TxHash.String()).
						Warn("Failed to delete orphan transaction from index")
				}

				if err := batch.Commit(); err != nil {
					w.logger.WithError(err).WithField("txhash", addrTx.TxHash.String()).
						Warn("Failed to commit orphan cleanup batch")
				}

				// Belt-and-suspenders: delete any remaining stale address index
				// entries for this address+tx combination that DeleteTransaction
				// might have missed (e.g. entries indexed under different addresses
				// not covered by the output/input scan in DeleteTransaction)
				if err := w.storage.DeleteAddressIndex(addressBinary, addrTx.TxHash); err != nil {
					w.logger.WithError(err).WithField("txhash", addrTx.TxHash.String()).
						Warn("Failed to delete orphan address index entries")
				}

				continue
			}

			tx := txData.TxData
			blockHash := txData.BlockHash
			txIdx := int(txData.TxIndex)

			// Reverse reconciliation (phantom-unspent fix): for every input
			// of this main-chain-validated transaction, verify that the
			// consumed UTXO is actually marked as spent in storage. Phantom-
			// unspent UTXOs arise when a prior block-processing interruption
			// persisted the transaction itself but missed the mark-spent step
			// for its inputs, leaving wallet-owned UTXOs visible as
			// spendable even though the chain has clearly consumed them.
			//
			// Safety: we only touch UTXOs that resolve to a wallet-owned
			// address via isOurScriptLocked — the codebase-wide "is this
			// ours?" predicate shared with categorizeTransactionLocked
			// and mempool notifications. The prev UTXO's owning address
			// may differ from the current rescan address (send-to-self
			// and change-output flows put the spender tx under the
			// change recipient's history while consuming an input from
			// another wallet address), so we deliberately do NOT require
			// a same-address match. Multi-wallet isolation is enforced
			// at the storage layer, not by per-address guards inside a
			// single wallet's rescan loop. The main-chain check above
			// (GetBlockHashByHeight equality) plus the re-check right
			// before batch.MarkUTXOSpent together guarantee the spender
			// tx is on the active chain when we record the spend.
			if tx != nil && !tx.IsCoinbase() {
				for _, input := range tx.Inputs {
					prev := input.PreviousOutput
					// Skip coinstake-style null prevouts (already filtered
					// by IsCoinbase above for coinbase, but coinstake marker
					// inputs use the same null sentinel).
					if prev.Hash == (types.Hash{}) && prev.Index == 0xffffffff {
						continue
					}

					prevUTXO, utxoErr := w.storage.GetUTXO(prev)
					if utxoErr != nil {
						continue // not in storage — nothing to reconcile
					}
					if prevUTXO == nil || prevUTXO.Output == nil {
						continue
					}
					// Already marked spent — no work needed. If spent by
					// a different tx we leave it alone (conflict belongs to
					// a separate investigation, not this rescan).
					if prevUTXO.SpendingHeight != 0 {
						continue
					}
					// Only reconcile wallet-owned UTXOs. Derive the owning
					// address from the script; isOurScriptLocked returns
					// isOurs==true only when the script resolves to an
					// address we control — that is the full safety guard.
					//
					// Do NOT additionally require prevAddr == current rescan
					// address: send-to-self / change-output patterns create
					// the spender tx in a DIFFERENT wallet address's history
					// (the change recipient), while the input being consumed
					// lives on yet another wallet address. A same-address
					// restriction would silently skip these common cases and
					// leave phantom-unspent UTXOs unreconciled — exactly
					// what we observed on the affected node (71746 txs
					// walked, zero reconciliations).
					prevAddr, isOurs := w.isOurScriptLocked(prevUTXO.Output.ScriptPubKey)
					if !isOurs {
						continue
					}

					// Re-validate main-chain membership immediately before
					// mutating UTXO state. The earlier check at wallet.go:2153
					// happened before we walked the inputs; in a scenario
					// where a concurrent block processor disconnects this
					// block between that check and now, writing the spend
					// here would record a spender that is no longer on the
					// active chain. This re-check closes that race window.
					canonicalHash, chkErr := w.storage.GetBlockHashByHeight(txData.Height)
					if chkErr != nil {
						if !storage.IsNotFoundError(chkErr) && !isHeightNotFoundError(chkErr) {
							return fmt.Errorf("revalidate main chain for %s: %w", addrTx.TxHash.String(), chkErr)
						}
						continue
					}
					if canonicalHash != txData.BlockHash {
						continue
					}

					reverseBatch := w.storage.NewBatch()
					if _, markErr := reverseBatch.MarkUTXOSpent(prev, txData.Height, addrTx.TxHash); markErr != nil {
						w.logger.WithError(markErr).WithFields(logrus.Fields{
							"outpoint": prev.String(),
							"spender":  addrTx.TxHash.String(),
						}).Warn("Failed to reconcile phantom-unspent UTXO (mark spent)")
						continue
					}
					if commitErr := reverseBatch.Commit(); commitErr != nil {
						w.logger.WithError(commitErr).WithFields(logrus.Fields{
							"outpoint": prev.String(),
							"spender":  addrTx.TxHash.String(),
						}).Warn("Failed to commit phantom-unspent reconciliation batch")
						continue
					}

					// Update in-memory wallet state to match the new storage
					// state: drop the UTXO from w.utxos and subtract from
					// the confirmed balance. This mirrors the forward
					// orphan-cleanup branch above (wallet.go:2198-2203).
					if existing, exists := w.utxos[prev]; exists {
						if bal := w.balances[existing.Address]; bal != nil {
							bal.Confirmed -= existing.Output.Value
						}
						delete(w.utxos, prev)
					}

					w.logger.WithFields(logrus.Fields{
						"rescan_address": address,
						"prev_address":   prevAddr,
						"outpoint":       prev.String(),
						"spender":        addrTx.TxHash.String(),
						"height":         txData.Height,
					}).Info("Reconciled phantom-unspent UTXO (marked as spent by on-chain tx)")
				}
			}

			// Get block timestamp from block header only (not full block with all transactions)
			var blockTime time.Time
			block, err := w.storage.GetBlockByHeight(txData.Height)
			if err == nil && block != nil {
				blockTime = time.Unix(int64(block.Header.Timestamp), 0)
			} else {
				// Fallback: estimate block time (genesis time + height * 60s)
				blockTime = time.Unix(int64(w.config.GenesisTimestamp)+int64(txData.Height)*60, 0)
				w.logger.WithFields(logrus.Fields{
					"tx_hash":      addrTx.TxHash.String(),
					"block_height": txData.Height,
				}).Debug("Using estimated block time for transaction")
			}

			// Calculate confirmations
			var confirmations int32
			if currentHeight >= txData.Height {
				confirmations = int32(currentHeight) - int32(txData.Height) + 1
			}

			// Check inputs callback - reads from storage for rescan
			checkInput := func(outpoint types.Outpoint) (int64, string, bool) {
				prevTx, err := w.storage.GetTransaction(outpoint.Hash)
				if err != nil || int(outpoint.Index) >= len(prevTx.Outputs) {
					return 0, "", false
				}
				output := prevTx.Outputs[outpoint.Index]
				// Use locked version since rescanBlockchainLocked holds w.mu
				if addr, isOurs := w.isOurScriptLocked(output.ScriptPubKey); isOurs {
					return output.Value, addr, true
				}
				return 0, "", false
			}

			// Use unified categorization logic - use locked version since rescanBlockchainLocked holds w.mu
			category, amount, txAddress, extra := w.categorizeTransactionLocked(tx, txIdx, checkInput)

			// Extract sender address only for receive transactions (optimization)
			// Avoids unnecessary storage lookups for send/stake transactions
			var fromAddress string
			if category == TxCategoryReceive {
				fromAddress = w.extractSenderAddressLocked(tx, txIdx)
			}

			// For send_to_self: amount = receivedAmount - spentAmount = -(fee),
			// so the fee equals -amount. Populate the Fee field so the details
			// dialog can display it.
			txFee := int64(0)
			if category == TxCategoryToSelf {
				txFee = -amount
			}
			// Add transaction to wallet
			w.nextSeqNum++
			wtx := &WalletTransaction{
				Tx:            tx,
				Hash:          addrTx.TxHash,
				BlockHash:     blockHash,
				BlockHeight:   int32(txData.Height),
				Time:          blockTime,
				Confirmations: confirmations,
				SeqNum:        w.nextSeqNum,
				Category:      category,
				Amount:        amount,
				Fee:           txFee,
				Address:       txAddress,
				FromAddress:   fromAddress,
			}

			w.transactions[txKey{addrTx.TxHash, 0}] = wtx

			// When the wallet is simultaneously the block staker AND a masternode
			// recipient in the same coinstake, create a second entry for the staking
			// reward.
			if extra != nil {
				w.nextSeqNum++
				w.transactions[txKey{addrTx.TxHash, 1}] = &WalletTransaction{
					Tx:            tx,
					Hash:          addrTx.TxHash, // real hash for explorer linking
					BlockHash:     blockHash,
					BlockHeight:   int32(txData.Height),
					Time:          blockTime,
					Confirmations: confirmations,
					SeqNum:        w.nextSeqNum,
					Category:      extra.Category,
					Amount:        extra.NetAmount,
					Address:       extra.Address,
					Vout:          1,
				}
			}
		}

		// Self-heal reconciliation: delete stale address index entries for
		// transactions that no longer exist in storage, and unspend any UTXO
		// still marked as spent by one of those stale txids. Without this,
		// the Warn loops forever and affected UTXOs stay stuck-spent.
		if len(staleTxids) > 0 {
			for staleHash := range staleTxids {
				if err := w.storage.DeleteAddressIndex(addressBinary, staleHash); err != nil {
					w.logger.WithError(err).WithField("txhash", staleHash.String()).
						Warn("Failed to delete stale address index entry")
				}
			}
			unspent, err := w.storage.UnspendUTXOsBySpendingTx(staleTxids)
			if err != nil {
				w.logger.WithError(err).Warn("Failed to reconcile stuck-spent UTXOs from stale txids")
			} else if unspent > 0 {
				w.logger.WithFields(logrus.Fields{
					"address": address,
					"stale":   len(staleTxids),
					"unspent": unspent,
				}).Info("Reconciled stale address index entries and stuck-spent UTXOs")
			}
		}
	}

	w.logger.WithField("address", address).WithFields(logrus.Fields{
		"utxos":        len(utxos),
		"transactions": len(addressTxs),
	}).Debug("Blockchain rescan completed")
	return nil
}

// isHeightNotFoundError checks if a storage error indicates a missing block height.
// GetBlockHashByHeight returns StorageError with code "HEIGHT_NOT_FOUND" which
// is not matched by storage.IsNotFoundError (which only checks "NOT_FOUND").
func isHeightNotFoundError(err error) bool {
	if storageErr, ok := err.(*storage.StorageError); ok {
		return storageErr.Code == "HEIGHT_NOT_FOUND"
	}
	return false
}

// bytesEqual compares two byte slices for equality
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// addressToBinaryKey converts a Base58 address string to binary [21]byte key
// Returns the key and true if successful, or zero key and false on error
func (w *Wallet) addressToBinaryKey(address string) ([21]byte, bool) {
	var key [21]byte

	// Decode Base58 address
	addr, err := crypto.DecodeAddress(address)
	if err != nil {
		return key, false
	}

	// Create binary key: netID (1 byte) + hash160 (20 bytes)
	key[0] = addr.NetID()
	copy(key[1:], addr.Hash160())

	return key, true
}

// save syncs wallet database to disk (legacy method kept for compatibility)
func (w *Wallet) save() error {
	if w.wdb != nil {
		return w.wdb.Sync()
	}
	return nil
}

// GetMasterKey returns the master HD key (only if unlocked)
func (w *Wallet) GetMasterKey() (*HDKey, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.masterKey == nil {
		return nil, fmt.Errorf("wallet not initialized: please create or load a wallet first")
	}

	if w.isLockedForSendingLocked() {
		return nil, ErrWalletLocked
	}

	return w.masterKey, nil
}

// GetAccount returns an account by ID
func (w *Wallet) GetAccount(accountID uint32) (*Account, error) {
	return w.addrMgr.GetAccount(accountID)
}

// CreateAccount creates a new account
func (w *Wallet) CreateAccount(name string) (uint32, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.masterKey == nil {
		return 0, fmt.Errorf("wallet not initialized: please create or load a wallet first")
	}

	if w.isLockedForSendingLocked() {
		return 0, ErrWalletLocked
	}

	// Find next available account ID
	accountID := uint32(1)
	for {
		if _, exists := w.accounts[accountID]; !exists {
			break
		}
		accountID++
	}

	_, err := w.addrMgr.CreateAccount(accountID, name)
	if err != nil {
		return 0, err
	}

	// Fill address pool
	if err := w.addrMgr.FillAddressPool(accountID); err != nil {
		return 0, fmt.Errorf("failed to fill address pool: %w", err)
	}

	w.logger.WithFields(logrus.Fields{
		"account_id": accountID,
		"name":       name,
	}).Debug("Account created")

	return accountID, nil
}

// SetLabel sets the label for an address
func (w *Wallet) SetLabel(address string, label string) error {
	return w.addrMgr.SetAddressLabel(address, label)
}

// SignMessage signs a message with the private key associated with an address
// Returns the base64-encoded compact signature (65 bytes with recovery ID)
func (w *Wallet) SignMessage(address string, message string) (string, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.isLockedForSendingLocked() {
		return "", ErrWalletLocked
	}

	// Get private key using full resolution chain (memory → BDB → HD derivation)
	privKey, err := w.getPrivateKeyForAddressLocked(address)
	if err != nil {
		return "", err
	}

	// Sign the message using compact signature format (Bitcoin message signing)
	signature, err := crypto.SignCompact(privKey, message)
	if err != nil {
		return "", fmt.Errorf("failed to sign message: %w", err)
	}

	// Return base64-encoded signature
	return crypto.Base64Encode(signature), nil
}

// VerifyMessage verifies a message signature
// signature should be base64-encoded compact signature (65 bytes)
func (w *Wallet) VerifyMessage(address string, signature string, message string) (bool, error) {
	// Decode base64 signature
	sigBytes, err := crypto.Base64Decode(signature)
	if err != nil {
		return false, fmt.Errorf("invalid signature encoding: %w", err)
	}

	// Recover public key from signature
	pubKey, err := crypto.RecoverCompactSignature(message, sigBytes)
	if err != nil {
		return false, fmt.Errorf("failed to recover public key: %w", err)
	}

	// Determine network ID based on wallet network configuration
	var netID byte
	switch w.config.Network {
	case MainNet:
		netID = crypto.MainNetPubKeyHashAddrID // 0x49 (W... addresses)
	case TestNet, RegTest:
		netID = crypto.TestNetPubKeyHashAddrID // 0x6F (m.../n... addresses)
	default:
		return false, fmt.Errorf("unknown network type")
	}

	// Generate address from recovered public key using proper crypto function
	recoveredAddr := crypto.NewAddressFromPubKey(pubKey, netID)
	recoveredAddress := recoveredAddr.String()

	// Compare recovered address with provided address
	return recoveredAddress == address, nil
}

// serializeHDKey serializes an HDKey to bytes
func (w *Wallet) serializeHDKey(key *HDKey) []byte {
	// Serialize HDKey in a simple format:
	// version(1) + depth(1) + parentFP(4) + childIndex(4) + chainCode(32) + privateKey(32) = 74 bytes
	result := make([]byte, 74)

	result[0] = 0x01 // version
	result[1] = key.depth
	copy(result[2:6], key.parentFP[:4])
	binary.BigEndian.PutUint32(result[6:10], key.childIndex)
	copy(result[10:42], key.chainCode)

	// Serialize private key
	if key.key != nil {
		privBytes := key.key.Bytes()
		// Pad to 32 bytes if needed (big-endian)
		if len(privBytes) < 32 {
			padded := make([]byte, 32)
			copy(padded[32-len(privBytes):], privBytes)
			privBytes = padded
		} else if len(privBytes) > 32 {
			privBytes = privBytes[:32]
		}
		copy(result[42:], privBytes)
	}

	return result
}

// deserializeHDKey deserializes an HDKey from bytes
func (w *Wallet) deserializeHDKey(data []byte) (*HDKey, error) {
	if len(data) < 74 {
		return nil, fmt.Errorf("invalid HD key data length: %d", len(data))
	}

	version := data[0]
	if version != 0x01 {
		return nil, fmt.Errorf("unsupported HD key version: %d", version)
	}

	depth := data[1]
	parentFP := make([]byte, 4)
	copy(parentFP, data[2:6])
	childIndex := binary.BigEndian.Uint32(data[6:10])

	chainCode := make([]byte, 32)
	copy(chainCode, data[10:42])

	// Deserialize private key
	privKey, err := crypto.ParsePrivateKeyFromBytes(data[42:74])
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	key := &HDKey{
		key:        privKey,
		pubKey:     privKey.PublicKey(),
		chainCode:  chainCode,
		depth:      depth,
		parentFP:   parentFP,
		childIndex: childIndex,
		network:    w.config.Network,
		isPrivate:  true,
	}

	return key, nil
}

// SetBlockchain sets the blockchain interface for transaction creation
func (w *Wallet) SetBlockchain(blockchain BlockchainInterface) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.blockchain = blockchain
}

// SetMempool sets the mempool interface for local mempool operations
func (w *Wallet) SetMempool(mempool MempoolInterface) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.mempool = mempool
}

// SetBroadcaster sets the broadcaster interface for P2P network transaction broadcast
// This should be called after P2P server is initialized to enable real transaction broadcasting
func (w *Wallet) SetBroadcaster(broadcaster BroadcasterInterface) {
	w.mu.Lock()
	var stopFn context.CancelFunc
	if broadcaster == nil {
		stopFn = w.rebroadcastCancel
		w.rebroadcastCancel = nil
	}
	w.broadcaster = broadcaster
	if broadcaster != nil && w.rebroadcastCancel == nil {
		ctx, cancel := context.WithCancel(context.Background())
		w.rebroadcastCancel = cancel
		go w.rebroadcastLoop(ctx)
	}
	w.mu.Unlock()

	if stopFn != nil {
		stopFn()
	}
	w.logger.Debug("Transaction broadcaster configured")
}

// SetMasternodeManager sets the masternode manager for collateral UTXO filtering
// This enables the wallet to completely hide masternode collateral UTXOs from transaction operations.
// Legacy: Equivalent to CMasternodeConfig integration in C++ CWallet for UTXO locking (mnconflock)
func (w *Wallet) SetMasternodeManager(mgr MasternodeCollateralChecker) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.masternodeManager = mgr
	w.logger.Debug("Masternode collateral filtering configured")
}

// IsCollateralUTXO checks if an outpoint is masternode collateral from masternode.conf.
// Returns true if the UTXO is used as masternode collateral and should be treated as locked.
// Legacy: Equivalent to CMasternodeConfig::IsLocked() check in C++ wallet.
// NOTE: This method acquires w.mu.RLock — do NOT call from within a w.mu.RLock section.
// Use isCollateralUTXOLocked() instead for lock-free access.
func (w *Wallet) IsCollateralUTXO(outpoint types.Outpoint) bool {
	w.mu.RLock()
	mgr := w.masternodeManager
	w.mu.RUnlock()

	if mgr == nil {
		return false
	}

	// Delegate to masternode manager's collateral check
	// The manager handles thread safety and conf file iteration internally
	return mgr.IsCollateralOutpoint(outpoint)
}

// isCollateralUTXOLocked is a lock-free version of IsCollateralUTXO for use by
// methods that already hold w.mu.RLock (e.g., ListUnspent, SelectUTXOs).
// Go's RWMutex does not support recursive read locks — calling IsCollateralUTXO
// (which acquires w.mu.RLock) from within a w.mu.RLock section can deadlock
// if a writer is waiting.
func (w *Wallet) isCollateralUTXOLocked(outpoint types.Outpoint) bool {
	if w.masternodeManager == nil {
		return false
	}
	return w.masternodeManager.IsCollateralOutpoint(outpoint)
}

// GetLockedCollateralInfo returns the total amount and count of UTXOs locked as masternode collateral.
// This method is atomic - it holds the wallet lock while iterating UTXOs and checking each against
// the masternode configuration, ensuring consistent results.
// Returns (amountInSatoshis, count).
func (w *Wallet) GetLockedCollateralInfo() (int64, int) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.masternodeManager == nil {
		return 0, 0
	}

	var totalAmount int64
	var count int
	for _, utxo := range w.utxos {
		if utxo == nil || !utxo.Spendable {
			continue
		}
		if w.masternodeManager.IsCollateralOutpoint(utxo.Outpoint) {
			totalAmount += utxo.Output.Value
			count++
		}
	}

	return totalAmount, count
}

// LockCoin marks a UTXO as locked, preventing it from being selected for spending.
// Legacy: CWallet::LockCoin(COutPoint& output) in wallet.cpp
func (w *Wallet) LockCoin(outpoint types.Outpoint) {
	w.lockedCoinsMu.Lock()
	defer w.lockedCoinsMu.Unlock()
	w.lockedCoins[outpoint] = struct{}{}
}

// UnlockCoin removes the lock from a UTXO, allowing it to be selected for spending.
// Legacy: CWallet::UnlockCoin(COutPoint& output) in wallet.cpp
func (w *Wallet) UnlockCoin(outpoint types.Outpoint) {
	w.lockedCoinsMu.Lock()
	defer w.lockedCoinsMu.Unlock()
	delete(w.lockedCoins, outpoint)
}

// UnlockAllCoins removes all user-set coin locks.
// Legacy: CWallet::UnlockAllCoins() in wallet.cpp
func (w *Wallet) UnlockAllCoins() {
	w.lockedCoinsMu.Lock()
	defer w.lockedCoinsMu.Unlock()
	w.lockedCoins = make(map[types.Outpoint]struct{})
}

// IsLockedCoin checks if a UTXO is locked by the user.
// Legacy: CWallet::IsLockedCoin(uint256 hash, unsigned int n) in wallet.cpp
func (w *Wallet) IsLockedCoin(outpoint types.Outpoint) bool {
	w.lockedCoinsMu.RLock()
	defer w.lockedCoinsMu.RUnlock()
	_, locked := w.lockedCoins[outpoint]
	return locked
}

// ListLockedCoins returns all currently locked outpoints.
// Legacy: CWallet::ListLockedCoins(std::vector<COutPoint>& vOutpts) in wallet.cpp
func (w *Wallet) ListLockedCoins() []types.Outpoint {
	w.lockedCoinsMu.RLock()
	defer w.lockedCoinsMu.RUnlock()
	result := make([]types.Outpoint, 0, len(w.lockedCoins))
	for outpoint := range w.lockedCoins {
		result = append(result, outpoint)
	}
	return result
}

// IsOurAddress checks if an address belongs to this wallet
func (w *Wallet) IsOurAddress(address string) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.isOurAddressLocked(address)
}

// isOurAddressLocked is an internal version that assumes caller holds w.mu
func (w *Wallet) isOurAddressLocked(address string) bool {
	_, exists := w.addresses[address]
	return exists
}

// GetTransactionCount returns the number of transactions in the wallet
func (w *Wallet) GetTransactionCount() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return len(w.transactions)
}

// BackupWallet safely copies wallet.dat to destination
func (w *Wallet) BackupWallet(destination string) error {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.wdb == nil {
		return fmt.Errorf("wallet database not initialized")
	}

	return w.wdb.Backup(destination)
}

// GetPublicKeyForAddress retrieves the public key for a given address
func (w *Wallet) GetPublicKeyForAddress(address string) ([]byte, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	// Look up address directly in wallet's address map
	addr, exists := w.addresses[address]
	if !exists {
		return nil, fmt.Errorf("address not found in wallet: %s", address)
	}

	if addr.PubKey == nil {
		return nil, fmt.Errorf("no public key available for address %s", address)
	}

	// Serialize public key as compressed bytes (33 bytes)
	return addr.PubKey.SerializeCompressed(), nil
}

// KeypoolRefill refills the keypool with new keys
func (w *Wallet) KeypoolRefill(newsize int) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Check if wallet is locked (direct field access to avoid deadlock)
	if w.encrypted && !w.unlocked {
		return fmt.Errorf("wallet is locked")
	}

	if w.addrMgr == nil {
		return fmt.Errorf("address manager not initialized")
	}

	// Refill keypool for default account (account 0)
	return w.addrMgr.RefillKeypool(0, newsize)
}

// GetReserveBalance returns the reserve balance setting
func (w *Wallet) GetReserveBalance() (bool, int64, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.wdb == nil {
		return false, 0, fmt.Errorf("wallet database not initialized")
	}

	return w.wdb.GetReserveBalance()
}

// SetReserveBalance sets the reserve balance for staking
func (w *Wallet) SetReserveBalance(enabled bool, amount int64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.wdb == nil {
		return fmt.Errorf("wallet database not initialized")
	}

	// Update in-memory config so GetStakeableUTXOs picks up the change immediately.
	// Without this, only the BDB layer is updated and the running staking session
	// continues using the stale w.config.ReserveBalance value.
	w.config.ReserveBalance = amount

	return w.wdb.SetReserveBalance(enabled, amount)
}

// GetStakeSplitThreshold returns the stake split threshold
func (w *Wallet) GetStakeSplitThreshold() (int64, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.wdb == nil {
		return 0, fmt.Errorf("wallet database not initialized")
	}

	return w.wdb.GetStakeSplitThreshold()
}

// SetStakeSplitThreshold sets the stake split threshold
func (w *Wallet) SetStakeSplitThreshold(threshold int64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.wdb == nil {
		return fmt.Errorf("wallet database not initialized")
	}

	if threshold < 0 {
		return fmt.Errorf("threshold must be non-negative")
	}

	// Clamp to the hard floor MinStakeSplitThresholdSatoshis. A user who sets a
	// lower value would otherwise cause CreateCoinstakeTx to produce split
	// coinstakes whose vout[1] falls below legacy's StakingMinInput (12000
	// TWINS), which legacy nodes reject at CheckBlock. Threshold 0 remains a
	// valid "disable splitting" sentinel and is not clamped.
	if threshold > 0 && threshold < MinStakeSplitThresholdSatoshis {
		if w.logger != nil {
			w.logger.Warnf("stake split threshold %d below minimum %d; clamping",
				threshold, MinStakeSplitThresholdSatoshis)
		}
		threshold = MinStakeSplitThresholdSatoshis
	}

	return w.wdb.SetStakeSplitThreshold(threshold)
}


// GetTransactionFee returns the current transaction fee per kilobyte in satoshis
func (w *Wallet) GetTransactionFee() int64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.txFeePerKB
}

// SetTransactionFee sets the transaction fee per kilobyte in satoshis
func (w *Wallet) SetTransactionFee(feePerKB int64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if feePerKB < 0 {
		return fmt.Errorf("fee must be non-negative")
	}

	// Maximum fee: 1 TWINS per KB (100,000,000 satoshis)
	const maxFee int64 = 100000000
	if feePerKB > maxFee {
		return fmt.Errorf("fee exceeds maximum allowed (1 TWINS/kB)")
	}

	w.txFeePerKB = feePerKB
	w.config.FeePerKB = feePerKB
	w.logger.WithField("fee_per_kb", feePerKB).Debug("Transaction fee updated")
	return nil
}

// SetMinTxFee updates the minimum transaction fee threshold at runtime.
func (w *Wallet) SetMinTxFee(fee int64) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if fee < 0 {
		return fmt.Errorf("minTxFee must be non-negative")
	}
	w.config.MinTxFee = fee
	return nil
}

// SetMaxTxFee updates the maximum transaction fee cap at runtime.
func (w *Wallet) SetMaxTxFee(fee int64) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if fee < 0 {
		return fmt.Errorf("maxTxFee must be non-negative")
	}
	const maxAllowed int64 = 1_000_000_000 // 10 TWINS — matches registry validation upper bound
	if fee > maxAllowed {
		return fmt.Errorf("maxTxFee %d exceeds ceiling %d satoshis", fee, maxAllowed)
	}
	w.config.MaxTxFee = fee
	return nil
}

// SetTxConfirmTarget updates the target confirmation count for fee estimation at runtime.
func (w *Wallet) SetTxConfirmTarget(target int) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if target < 1 {
		return fmt.Errorf("txConfirmTarget must be at least 1")
	}
	w.config.TxConfirmTarget = target
	return nil
}

// SetSpendZeroConfChange updates the spend-zero-conf-change setting at runtime.
func (w *Wallet) SetSpendZeroConfChange(v bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.config.SpendZeroConfChange = v
	return nil
}

// SetCreateWalletBackups updates the auto-backup count at runtime (0 to disable).
func (w *Wallet) SetCreateWalletBackups(n int) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if n < 0 {
		return fmt.Errorf("createWalletBackups must be non-negative")
	}
	w.config.CreateWalletBackups = n
	return nil
}

// SetBackupPath updates the wallet backup directory at runtime.
func (w *Wallet) SetBackupPath(path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if path != "" {
		path = filepath.Clean(path)
	}
	w.config.BackupPath = path
	return nil
}

// DumpWallet exports all private keys to a file
func (w *Wallet) DumpWallet(filename string) error {
	w.mu.RLock()
	defer w.mu.RUnlock()

	// Check wallet is fully unlocked (not staking-only)
	if w.isLockedForSendingLocked() {
		return ErrWalletLocked
	}

	if w.wdb == nil {
		return fmt.Errorf("wallet database not initialized")
	}

	// Create file
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create dump file: %w", err)
	}
	defer file.Close()

	// Write header
	dumpTime := time.Now()
	fmt.Fprintf(file, "# Wallet dump created by TWINS %s\n", dumpTime.Format(time.RFC3339))
	fmt.Fprintf(file, "# * Created on %s\n", dumpTime.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(file, "# * Best block at time of backup was %d\n", w.cachedChainHeight)
	fmt.Fprintf(file, "# * File: %s\n\n", filename)

	// Export all addresses with their private keys
	for _, addr := range w.addresses {
		// Get private key (use lock-free variant since we already hold w.mu.RLock)
		privKey, err := w.dumpPrivKeyLocked(addr.Address)
		if err != nil {
			w.logger.WithError(err).WithField("address", addr.Address).Warn("Failed to dump private key")
			continue
		}

		// Write to file: privkey timestamp label address
		label := addr.Label
		if label == "" {
			label = "imported"
		}
		fmt.Fprintf(file, "%s %d %s # addr=%s\n", privKey, time.Now().Unix(), label, addr.Address)
	}

	// Write footer
	fmt.Fprintf(file, "\n# End of dump\n")

	return nil
}

// ImportWallet imports private keys from a wallet dump file
func (w *Wallet) ImportWallet(filename string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.wdb == nil {
		return fmt.Errorf("wallet database not initialized")
	}

	// Check wallet is fully unlocked (not staking-only)
	if w.isLockedForSendingLocked() {
		return ErrWalletLocked
	}

	// Open file
	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("failed to open dump file: %w", err)
	}
	defer file.Close()

	// Read file line by line
	scanner := bufio.NewScanner(file)
	imported := 0
	for scanner.Scan() {
		line := scanner.Text()

		// Skip comments and empty lines
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}

		// Parse line: privkey timestamp label # addr=address
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		privKey := parts[0]
		label := ""
		if len(parts) >= 3 {
			label = parts[2]
		}

		// Import the private key (without rescan to be faster)
		err := w.ImportPrivateKey(privKey, label, false)
		if err != nil {
			w.logger.WithError(err).WithField("privkey", privKey[:10]+"...").Warn("Failed to import private key")
			continue
		}

		imported++
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading dump file: %w", err)
	}

	w.logger.WithField("count", imported).Debug("Imported private keys from wallet dump")

	return nil
}

// ImportAddress adds a watch-only address to the wallet
func (w *Wallet) ImportAddress(address string, label string, rescan bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.wdb == nil {
		return fmt.Errorf("wallet database not initialized")
	}

	// Validate the address (use locked version to avoid deadlock)
	validation := w.validateAddressLocked(address)
	if !validation.IsValid {
		return fmt.Errorf("invalid TWINS address")
	}

	// Check if we already own this address (use locked version to avoid deadlock)
	if w.isOurAddressLocked(address) {
		return fmt.Errorf("the wallet already contains the private key for this address")
	}

	// For now, just add it to the addresses map without private key
	// This is a simplified implementation - full watch-only support would require
	// additional database fields and transaction scanning logic
	addr := &Address{
		Address:   address,
		Label:     label,
		Account:   0,
		CreatedAt: time.Now(),
	}

	w.addresses[address] = addr

	// Rescan blockchain for transactions to this address if requested
	if rescan {
		w.logger.WithField("address", address).Debug("Starting blockchain rescan for imported address")
		if err := w.rescanBlockchainLocked(address); err != nil {
			w.logger.WithError(err).Warn("Rescan failed for imported address")
			// Don't fail the import, just log the warning
		}
	}

	w.logger.WithField("address", address).Debug("Added watch-only address")

	return nil
}

// DumpHDInfo returns HD wallet information (seed, mnemonic, passphrase)
// Requires wallet to be unlocked
func (w *Wallet) DumpHDInfo() (seed string, mnemonic string, mnemonicPass string, err error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	// Check wallet is fully unlocked (not staking-only)
	if w.isLockedForSendingLocked() {
		return "", "", "", ErrWalletLocked
	}

	if w.wdb == nil {
		return "", "", "", fmt.Errorf("wallet database not initialized")
	}

	// Read HD chain from database
	hdChain, isEncrypted, err := w.wdb.ReadHDChain()
	if err != nil {
		return "", "", "", fmt.Errorf("failed to read HD chain: %w", err)
	}

	if hdChain == nil {
		return "", "", "", fmt.Errorf("no HD chain found in wallet")
	}

	var seedBytes []byte

	// If encrypted, decrypt the seed
	if isEncrypted && hdChain.Crypted && len(hdChain.Seed) > 0 {
		// Get encryption key from wallet database (set during Unlock)
		w.wdb.mu.RLock()
		encryptionKey := w.wdb.encryptionKey
		w.wdb.mu.RUnlock()

		if encryptionKey == nil {
			return "", "", "", fmt.Errorf("wallet is locked, cannot decrypt HD seed")
		}

		// Use chain ID as IV (first 16 bytes) - matches legacy behavior
		if len(hdChain.ChainID) < 16 {
			return "", "", "", fmt.Errorf("corrupted wallet: chain ID too short for IV derivation")
		}
		iv := hdChain.ChainID[:16]

		// Decrypt the seed using the wallet's encryption key
		decryptedSeed, err := walletcrypto.DecryptSecret(encryptionKey, hdChain.Seed, iv)
		if err != nil {
			return "", "", "", fmt.Errorf("failed to decrypt HD seed: %w", err)
		}

		seedBytes = decryptedSeed
	} else {
		// Unencrypted seed
		seedBytes = hdChain.Seed
	}

	// Convert seed to hex
	seedHex := hex.EncodeToString(seedBytes)

	// Decrypt mnemonic and passphrase if wallet is encrypted
	var mnemonicBytes, mnemonicPassBytes []byte
	if isEncrypted && hdChain.Crypted {
		// Get encryption key from wallet database
		w.wdb.mu.RLock()
		encryptionKey := w.wdb.encryptionKey
		w.wdb.mu.RUnlock()

		if encryptionKey != nil {
			iv := hdChain.ChainID[:16]

			// Decrypt mnemonic if present
			if len(hdChain.Mnemonic) > 0 {
				decryptedMnemonic, err := walletcrypto.DecryptSecret(encryptionKey, hdChain.Mnemonic, iv)
				if err != nil {
					w.logger.WithError(err).Debug("Failed to decrypt mnemonic, may be empty or corrupted")
				} else {
					mnemonicBytes = decryptedMnemonic
				}
			}

			// Decrypt mnemonic passphrase if present
			if len(hdChain.MnemonicPass) > 0 {
				decryptedMnemonicPass, err := walletcrypto.DecryptSecret(encryptionKey, hdChain.MnemonicPass, iv)
				if err != nil {
					w.logger.WithError(err).Debug("Failed to decrypt mnemonic passphrase, may be empty or corrupted")
				} else {
					mnemonicPassBytes = decryptedMnemonicPass
				}
			}
		}
	} else {
		// Unencrypted mnemonic and passphrase - make copies to zero later
		mnemonicBytes = make([]byte, len(hdChain.Mnemonic))
		copy(mnemonicBytes, hdChain.Mnemonic)
		mnemonicPassBytes = make([]byte, len(hdChain.MnemonicPass))
		copy(mnemonicPassBytes, hdChain.MnemonicPass)
	}

	mnemonicStr := string(mnemonicBytes)
	mnemonicPassStr := string(mnemonicPassBytes)

	// Securely zero all sensitive data (both encrypted and unencrypted)
	for i := range seedBytes {
		seedBytes[i] = 0
	}
	for i := range mnemonicBytes {
		mnemonicBytes[i] = 0
	}
	for i := range mnemonicPassBytes {
		mnemonicPassBytes[i] = 0
	}

	return seedHex, mnemonicStr, mnemonicPassStr, nil
}

// GetKeypoolOldest returns the timestamp of the oldest address in the keypool
func (w *Wallet) GetKeypoolOldest() int64 {
	w.mu.RLock()
	defer w.mu.RUnlock()

	var oldest time.Time
	first := true

	// Find oldest unused address in the keypool (both external and internal)
	for _, addr := range w.addresses {
		if !addr.Used && !addr.CreatedAt.IsZero() {
			if first || addr.CreatedAt.Before(oldest) {
				oldest = addr.CreatedAt
				first = false
			}
		}
	}

	if first {
		// No unused addresses, return current time
		return time.Now().Unix()
	}

	return oldest.Unix()
}

// GetKeypoolSize returns the number of unused addresses in the keypool
func (w *Wallet) GetKeypoolSize() int {
	w.mu.RLock()
	defer w.mu.RUnlock()

	count := 0
	for _, addr := range w.addresses {
		if !addr.Used {
			count++
		}
	}

	return count
}

// AddMultisigAddress creates and adds a multisig address to the wallet
func (w *Wallet) AddMultisigAddress(nrequired int, keys []string, account string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Check if wallet is locked (direct field access to avoid deadlock)
	if w.encrypted && !w.unlocked {
		return "", fmt.Errorf("wallet is locked")
	}

	if nrequired < 1 || nrequired > len(keys) {
		return "", fmt.Errorf("invalid nrequired: must be between 1 and %d", len(keys))
	}

	if len(keys) < 2 || len(keys) > 15 {
		return "", fmt.Errorf("invalid number of keys: must be between 2 and 15")
	}

	// Convert addresses to public keys, or validate hex public keys
	pubKeys := make([]string, 0, len(keys))
	for i, key := range keys {
		// Try to decode as address first
		addr, err := crypto.DecodeAddress(key)
		if err == nil {
			// It's an address - look up public key from wallet
			if addr.IsScript() {
				return "", fmt.Errorf("key %d: cannot use P2SH address in multisig", i)
			}
			walletAddr, exists := w.addresses[addr.String()]
			if !exists {
				return "", fmt.Errorf("key %d: address %s not found in wallet", i, addr.String())
			}
			if walletAddr.PubKey == nil {
				return "", fmt.Errorf("key %d: no public key available for address %s", i, addr.String())
			}
			// Convert to hex (compressed format, 33 bytes)
			pubKeys = append(pubKeys, fmt.Sprintf("%x", walletAddr.PubKey.SerializeCompressed()))
		} else {
			// Assume it's a hex-encoded public key - validate format
			pubKeyBytes, hexErr := hex.DecodeString(key)
			if hexErr != nil {
				return "", fmt.Errorf("key %d: invalid address or hex public key: %v", i, hexErr)
			}
			// Validate public key length
			if len(pubKeyBytes) != 33 && len(pubKeyBytes) != 65 {
				return "", fmt.Errorf("key %d: invalid public key length %d (expected 33 or 65)", i, len(pubKeyBytes))
			}
			pubKeys = append(pubKeys, key)
		}
	}

	// Determine network ID from wallet configuration
	var networkName string
	if w.config != nil {
		networkName = w.config.Network.String()
	} else {
		networkName = "mainnet"
	}
	netID := crypto.GetScriptHashNetworkID(networkName)

	// Create multisig address using public keys and correct network ID
	multisigInfo, err := crypto.CreateMultisigAddress(nrequired, pubKeys, netID)
	if err != nil {
		return "", fmt.Errorf("failed to create multisig address: %v", err)
	}

	// Create multisig address struct
	ma := &MultisigAddress{
		Address:      multisigInfo.Address,
		RedeemScript: multisigInfo.RedeemScript,
		NRequired:    nrequired,
		Keys:         keys,
		Account:      account,
		CreatedAt:    time.Now(),
	}

	// Store in memory
	w.multisigAddrs[multisigInfo.Address] = ma

	// Persist to database if wallet database is available
	if w.wdb != nil {
		if err := w.wdb.WriteMultisigAddress(multisigInfo.Address, ma); err != nil {
			w.logger.WithError(err).Warn("Failed to persist multisig address to database")
			// Don't fail the operation - address is still stored in memory
		}
	}

	return multisigInfo.Address, nil
}

// GetMultiSend returns the current multisend configuration
func (w *Wallet) GetMultiSend() ([]MultiSendEntry, error) {
	if w.wdb == nil {
		return nil, fmt.Errorf("wallet database not available")
	}
	return w.wdb.GetMultiSend()
}

// SetMultiSend sets the multisend configuration
func (w *Wallet) SetMultiSend(entries []MultiSendEntry) error {
	if w.wdb == nil {
		return fmt.Errorf("wallet database not available")
	}
	return w.wdb.SetMultiSend(entries)
}

// GetMultiSendSettings returns the multisend settings
func (w *Wallet) GetMultiSendSettings() (stakeEnabled bool, masternodeEnabled bool, disabledAddrs []string, err error) {
	if w.wdb == nil {
		err = fmt.Errorf("wallet database not available")
		return
	}
	return w.wdb.GetMultiSendSettings()
}

// SetMultiSendSettings sets the multisend settings
func (w *Wallet) SetMultiSendSettings(stakeEnabled bool, masternodeEnabled bool, disabledAddrs []string) error {
	if w.wdb == nil {
		return fmt.Errorf("wallet database not available")
	}
	return w.wdb.SetMultiSendSettings(stakeEnabled, masternodeEnabled, disabledAddrs)
}

// ==========================================
// Receive Address Methods
// ==========================================

// GetReceivingAddress returns a receiving address for the default account
// If no unused address exists, generates a new one
func (w *Wallet) GetReceivingAddress(label string) (string, error) {
	if w.addrMgr == nil {
		return "", fmt.Errorf("address manager not initialized")
	}
	return w.addrMgr.GetNewAddress(0, label)
}

// GetReceivingAddresses returns all receiving (external) addresses
func (w *Wallet) GetReceivingAddresses() []*Address {
	if w.addrMgr == nil {
		return nil
	}
	return w.addrMgr.GetReceivingAddresses()
}

// GetAllReceivingAddresses returns all receiving addresses including keypool
func (w *Wallet) GetAllReceivingAddresses() []*Address {
	if w.addrMgr == nil {
		return nil
	}
	return w.addrMgr.GetAllReceivingAddresses()
}

// IsAddressUsed checks if an address has received any transactions
func (w *Wallet) IsAddressUsed(address string) bool {
	if w.addrMgr == nil {
		return false
	}
	return w.addrMgr.IsAddressUsed(address)
}

// GetAddressLabel returns the label for an address from the address book
func (w *Wallet) GetAddressLabel(address string) string {
	if w.wdb == nil {
		return ""
	}
	label, err := w.wdb.ReadName(address)
	if err != nil {
		return ""
	}
	return label
}

// SetAddressLabel sets the label for an address in the address book
func (w *Wallet) SetAddressLabel(address, label string) error {
	if w.wdb == nil {
		return fmt.Errorf("wallet database not available")
	}
	return w.wdb.WriteName(address, label)
}
