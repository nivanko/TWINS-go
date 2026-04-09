package wallet

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/twins-dev/twins-core/internal/wallet/crypto"
	"github.com/twins-dev/twins-core/internal/wallet/legacy"
	"github.com/twins-dev/twins-core/internal/wallet/serialization"
	walletstorage "github.com/twins-dev/twins-core/internal/wallet/storage"
	pkgcrypto "github.com/twins-dev/twins-core/pkg/crypto"
	"go.etcd.io/bbolt"
)

// Bucket names for wallet.dat
var (
	bucketWallet = []byte("wallet")
	bucketKeys   = []byte("keys")
	bucketMeta   = []byte("meta")
)

// Key prefixes matching legacy format
var (
	keyPrefixMasterKey = []byte("mkey")
	keyPrefixKey       = []byte("key")
	keyPrefixCKey      = []byte("ckey")
	keyPrefixKeymeta   = []byte("keymeta")
	keyPrefixHDChain   = []byte("hdchain")
	keyPrefixCHDChain  = []byte("chdchain") // Encrypted HD chain
	keyPrefixHDPubKey  = []byte("hdpubkey") // HD wallet public key with derivation path
	keyPrefixName      = []byte("name")     // Address label
	keyPrefixPurpose   = []byte("purpose")  // Address purpose
	keyPrefixVersion   = []byte("version")
	keyPrefixMultisig  = []byte("multisig")  // Multisig P2SH address
	keyPrefixMultiSend = []byte("msend")     // MultiSend entries
	keyPrefixDestData  = []byte("destdata")  // Address destdata (payment requests, etc.)
)

// WalletDB provides legacy-compatible wallet.dat persistence
type WalletDB struct {
	db            *bbolt.DB
	path          string
	masterKeys    map[uint32]*crypto.MasterKey
	encryptionKey []byte
	encrypted     bool
	mu            sync.RWMutex // Protects encryptionKey and encrypted state
}

// OpenWalletDB opens or creates a wallet.dat database
func OpenWalletDB(dataDir string) (*WalletDB, error) {
	// Ensure data directory exists
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	walletPath := filepath.Join(dataDir, "wallet.dat")

	// Check if legacy wallet exists and migrate if needed
	if _, err := os.Stat(walletPath); err == nil {
		// File exists, check if it needs migration
		if err := walletstorage.AutoMigrateWallet(dataDir); err != nil {
			return nil, fmt.Errorf("wallet migration failed: %w", err)
		}
	}

	// Open bbolt database
	db, err := bbolt.Open(walletPath, 0600, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open wallet database: %w", err)
	}

	// Create buckets if they don't exist
	if err := db.Update(func(tx *bbolt.Tx) error {
		for _, bucket := range [][]byte{bucketWallet, bucketKeys, bucketMeta} {
			if _, err := tx.CreateBucketIfNotExists(bucket); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create buckets: %w", err)
	}

	wdb := &WalletDB{
		db:         db,
		path:       walletPath,
		masterKeys: make(map[uint32]*crypto.MasterKey),
		encrypted:  false,
	}

	// Load master keys if wallet is encrypted
	if err := wdb.loadMasterKeys(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to load master keys: %w", err)
	}

	return wdb, nil
}

// Close closes the wallet database
func (wdb *WalletDB) Close() error {
	wdb.mu.Lock()
	defer wdb.mu.Unlock()

	// Zero encryption key even if database close fails
	if wdb.encryptionKey != nil {
		for i := range wdb.encryptionKey {
			wdb.encryptionKey[i] = 0
		}
		wdb.encryptionKey = nil
	}

	if wdb.db != nil {
		return wdb.db.Close()
	}
	return nil
}

// IsEncrypted returns true if the wallet is encrypted
func (wdb *WalletDB) IsEncrypted() bool {
	wdb.mu.RLock()
	defer wdb.mu.RUnlock()
	return wdb.encrypted
}

// Unlock unlocks the wallet with a passphrase
func (wdb *WalletDB) Unlock(passphrase []byte) error {
	wdb.mu.Lock()
	defer wdb.mu.Unlock()

	if !wdb.encrypted {
		return fmt.Errorf("wallet is not encrypted")
	}

	// Try to unlock all master keys
	for _, mk := range wdb.masterKeys {
		key, err := mk.Unlock(passphrase)
		if err == nil {
			// Successfully unlocked
			wdb.encryptionKey = key
			return nil
		}
	}

	return fmt.Errorf("incorrect passphrase")
}

// Lock locks the wallet
func (wdb *WalletDB) Lock() {
	wdb.mu.Lock()
	defer wdb.mu.Unlock()

	// Securely zero the encryption key before clearing
	if wdb.encryptionKey != nil {
		for i := range wdb.encryptionKey {
			wdb.encryptionKey[i] = 0
		}
	}
	wdb.encryptionKey = nil
}

// ChangePassphrase changes the wallet encryption passphrase by re-wrapping
// the existing encryption key with a new passphrase. Private keys and HD seed
// remain encrypted with the same encryption key — only the master key wrapping changes.
// NOTE: Caller must hold appropriate locks (e.g., Wallet.mu)
func (wdb *WalletDB) ChangePassphrase(oldPassphrase, newPassphrase []byte) error {
	// NOTE: No wdb.mu lock here - caller (Wallet.ChangePassphrase) already holds Wallet.mu

	if !wdb.encrypted {
		return fmt.Errorf("wallet is not encrypted")
	}

	// Try to unlock a master key with the old passphrase to get the encryption key
	var encryptionKey []byte
	var unlockedID uint32
	for id, mk := range wdb.masterKeys {
		key, err := mk.Unlock(oldPassphrase)
		if err == nil {
			encryptionKey = key
			unlockedID = id
			break
		}
	}
	if encryptionKey == nil {
		return fmt.Errorf("incorrect old passphrase")
	}

	// Zero the encryption key when done
	defer func() {
		for i := range encryptionKey {
			encryptionKey[i] = 0
		}
	}()

	// Wrap the same encryption key with the new passphrase
	newMK, err := crypto.WrapEncryptionKeyScrypt(
		encryptionKey, newPassphrase,
		crypto.DefaultScryptN, crypto.DefaultScryptR, crypto.DefaultScryptP,
	)
	if err != nil {
		return fmt.Errorf("failed to wrap encryption key with new passphrase: %w", err)
	}

	// Write updated master key to database
	if err := wdb.WriteMasterKey(unlockedID, newMK); err != nil {
		return fmt.Errorf("failed to write updated master key: %w", err)
	}

	// Update in-memory master key
	wdb.masterKeys[unlockedID] = newMK

	return nil
}

// Encrypt encrypts an unencrypted wallet with a passphrase
// NOTE: Caller must hold appropriate locks (e.g., Wallet.mu)
func (wdb *WalletDB) Encrypt(passphrase []byte) error {
	// NOTE: No wdb.mu lock here - caller (Wallet.EncryptWallet) already holds Wallet.mu
	// Adding a lock here causes deadlock when ReadKey() tries to acquire wdb.mu.RLock()

	// Check if wallet is already encrypted
	if wdb.encrypted {
		return fmt.Errorf("wallet is already encrypted")
	}

	if len(passphrase) == 0 {
		return fmt.Errorf("passphrase cannot be empty")
	}

	// Create a new master key using scrypt derivation (recommended over EVP_sha512)
	mk, encryptionKey, err := crypto.NewMasterKeyScrypt(
		passphrase,
		crypto.DefaultScryptN,
		crypto.DefaultScryptR,
		crypto.DefaultScryptP,
	)
	if err != nil {
		return fmt.Errorf("failed to create master key: %w", err)
	}

	// Make a copy for wdb.encryptionKey — the local encryptionKey will be zeroed on return.
	// Ownership of the copy transfers to wdb.encryptionKey on success.
	encryptionKeyCopy := make([]byte, len(encryptionKey))
	copy(encryptionKeyCopy, encryptionKey)

	// Zero the local encryption key after use (the copy is stored in wdb on success)
	defer func() {
		for i := range encryptionKey {
			encryptionKey[i] = 0
		}
	}()

	// Write master key to database with ID 0
	if err := wdb.WriteMasterKey(0, mk); err != nil {
		return fmt.Errorf("failed to write master key: %w", err)
	}

	// Get all existing public keys
	pubkeys, err := wdb.GetAllKeys()
	if err != nil {
		return fmt.Errorf("failed to get keys for encryption: %w", err)
	}

	// Re-encrypt all existing private keys
	for _, pubkey := range pubkeys {
		// Read unencrypted private key
		privkey, err := wdb.ReadKey(pubkey)
		if err != nil {
			continue // Skip keys that can't be read
		}

		// Generate IV from public key
		iv := crypto.GenerateIV(pubkey)

		// Encrypt the private key
		encryptedPrivkey, err := crypto.EncryptAES256CBC(encryptionKey, iv, privkey)
		if err != nil {
			return fmt.Errorf("failed to encrypt private key: %w", err)
		}

		// Store encrypted key with ckey prefix
		var keyBuf bytes.Buffer
		serialization.WriteVarBytes(&keyBuf, keyPrefixCKey)
		serialization.WriteVarBytes(&keyBuf, pubkey)

		var valueBuf bytes.Buffer
		serialization.WriteVarBytes(&valueBuf, encryptedPrivkey)

		// Write encrypted key to database
		if err := wdb.db.Update(func(tx *bbolt.Tx) error {
			bucket := tx.Bucket(bucketWallet)
			if err := bucket.Put(keyBuf.Bytes(), valueBuf.Bytes()); err != nil {
				return err
			}

			// Delete unencrypted key
			var unencKeyBuf bytes.Buffer
			serialization.WriteVarBytes(&unencKeyBuf, keyPrefixKey)
			serialization.WriteVarBytes(&unencKeyBuf, pubkey)
			return bucket.Delete(unencKeyBuf.Bytes())
		}); err != nil {
			return fmt.Errorf("failed to write encrypted key: %w", err)
		}

		// Zero the unencrypted private key from memory
		for i := range privkey {
			privkey[i] = 0
		}
	}

	// Encrypt HD chain seed if present and unencrypted
	hdChain, isEncrypted, err := wdb.ReadHDChain()
	if err == nil && !isEncrypted && hdChain != nil && len(hdChain.Seed) > 0 {
		// Compute chain ID as DoubleHash256 of seed (matches C++ Hash() = SHA256d)
		chainID := pkgcrypto.DoubleHash256(hdChain.Seed)

		if len(chainID) < 16 {
			return fmt.Errorf("corrupted wallet: chain ID too short for IV derivation (%d bytes, expected 32 from SHA256)", len(chainID))
		}

		// Use first 16 bytes of chain ID as IV (matches legacy EncryptSecret behavior)
		iv := chainID[:16]

		// Encrypt the seed using the master encryption key
		encryptedSeed, err := crypto.EncryptSecret(encryptionKey, hdChain.Seed, iv)
		if err != nil {
			return fmt.Errorf("failed to encrypt HD chain seed: %w", err)
		}

		// Encrypt mnemonic and mnemonic passphrase if present
		// (matches C++ EncryptHDChain which encrypts all three with same IV)
		encryptedMnemonic := hdChain.Mnemonic
		if len(hdChain.Mnemonic) > 0 {
			encryptedMnemonic, err = crypto.EncryptSecret(encryptionKey, hdChain.Mnemonic, iv)
			if err != nil {
				return fmt.Errorf("failed to encrypt HD chain mnemonic: %w", err)
			}
		}
		encryptedMnemonicPass := hdChain.MnemonicPass
		if len(hdChain.MnemonicPass) > 0 {
			encryptedMnemonicPass, err = crypto.EncryptSecret(encryptionKey, hdChain.MnemonicPass, iv)
			if err != nil {
				return fmt.Errorf("failed to encrypt HD chain mnemonic passphrase: %w", err)
			}
		}

		// Build encrypted HD chain
		encryptedHDChain := &legacy.CHDChain{
			Version:         hdChain.Version,
			ChainID:         chainID,
			Crypted:         true,
			Seed:            encryptedSeed,
			Mnemonic:        encryptedMnemonic,
			MnemonicPass:    encryptedMnemonicPass,
			ExternalCounter: hdChain.ExternalCounter,
			InternalCounter: hdChain.InternalCounter,
		}

		// Write encrypted HD chain to database
		if err := wdb.WriteCryptedHDChain(encryptedHDChain); err != nil {
			return fmt.Errorf("failed to write encrypted HD chain: %w", err)
		}

		// Securely zero the original seed
		for i := range hdChain.Seed {
			hdChain.Seed[i] = 0
		}
	}

	// Update master keys map and encryption state
	wdb.masterKeys[0] = mk
	wdb.encryptionKey = encryptionKeyCopy
	wdb.encrypted = true

	return nil
}

// loadMasterKeys loads all master keys from the database
func (wdb *WalletDB) loadMasterKeys() error {
	return wdb.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWallet)
		if bucket == nil {
			return nil
		}

		// Iterate through all keys looking for master keys
		c := bucket.Cursor()
		prefix := keyPrefixMasterKey
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			// Extract master key ID from key
			if len(k) < len(prefix)+4 {
				continue
			}

			// Deserialize master key
			mk := &legacy.CMasterKey{}
			if err := legacy.DeserializeFromBytes(v, mk); err != nil {
				continue
			}

			// Convert to crypto.MasterKey
			masterKey := &crypto.MasterKey{
				EncryptedKey:              mk.EncryptedKey,
				Salt:                      mk.Salt,
				DerivationMethod:          mk.DerivationMethod,
				DeriveIterations:          mk.DeriveIterations,
				OtherDerivationParameters: mk.OtherDerivationParameters,
			}

			// Extract ID
			id := uint32(k[len(prefix)]) | uint32(k[len(prefix)+1])<<8 |
				uint32(k[len(prefix)+2])<<16 | uint32(k[len(prefix)+3])<<24

			wdb.masterKeys[id] = masterKey
			wdb.encrypted = true
		}

		return nil
	})
}

// WriteMasterKey writes a master key to the database
func (wdb *WalletDB) WriteMasterKey(id uint32, mk *crypto.MasterKey) error {
	// Convert to legacy format
	legacyMK := &legacy.CMasterKey{
		EncryptedKey:              mk.EncryptedKey,
		Salt:                      mk.Salt,
		DerivationMethod:          mk.DerivationMethod,
		DeriveIterations:          mk.DeriveIterations,
		OtherDerivationParameters: mk.OtherDerivationParameters,
	}

	// Serialize
	data, err := legacy.SerializeToBytes(legacyMK)
	if err != nil {
		return err
	}

	// Create key: mkey || id (little-endian)
	key := make([]byte, len(keyPrefixMasterKey)+4)
	copy(key, keyPrefixMasterKey)
	key[len(keyPrefixMasterKey)] = byte(id)
	key[len(keyPrefixMasterKey)+1] = byte(id >> 8)
	key[len(keyPrefixMasterKey)+2] = byte(id >> 16)
	key[len(keyPrefixMasterKey)+3] = byte(id >> 24)

	// Write to database
	return wdb.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWallet)
		return bucket.Put(key, data)
	})
}

// WriteKey writes a private key to the database (encrypted or unencrypted)
func (wdb *WalletDB) WriteKey(pubkey []byte, privkey []byte, metadata *legacy.CKeyMetadata) error {
	var keyData []byte
	var keyPrefix []byte

	// Use full lock to prevent race condition during encryption
	wdb.mu.Lock()
	shouldEncrypt := wdb.encrypted && wdb.encryptionKey != nil

	if shouldEncrypt {
		// Copy encryption key for use during encryption
		encKey := make([]byte, len(wdb.encryptionKey))
		copy(encKey, wdb.encryptionKey)

		// Perform encryption while holding lock to prevent TOCTOU race
		// This ensures encryption state doesn't change mid-operation
		iv := crypto.GenerateIV(pubkey)
		encrypted, err := crypto.EncryptAES256CBC(encKey, iv, privkey)

		// Zero the copied key
		for i := range encKey {
			encKey[i] = 0
		}

		wdb.mu.Unlock()

		if err != nil {
			return err
		}

		// Use ckey prefix for encrypted keys
		keyPrefix = keyPrefixCKey
		keyData = encrypted
	} else {
		wdb.mu.Unlock()

		// Use key prefix for unencrypted keys
		keyPrefix = keyPrefixKey
		keyData = privkey
	}

	// Create compound key: prefix || pubkey
	var keyBuf bytes.Buffer
	serialization.WriteVarBytes(&keyBuf, keyPrefix)
	serialization.WriteVarBytes(&keyBuf, pubkey)

	// Serialize value
	var valueBuf bytes.Buffer
	serialization.WriteVarBytes(&valueBuf, keyData)

	// Write to database
	return wdb.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWallet)
		if err := bucket.Put(keyBuf.Bytes(), valueBuf.Bytes()); err != nil {
			return err
		}

		// Write key metadata if provided
		if metadata != nil {
			metaKey := append(keyPrefixKeymeta, pubkey...)
			metaData, err := legacy.SerializeToBytes(metadata)
			if err != nil {
				return err
			}
			return bucket.Put(metaKey, metaData)
		}

		return nil
	})
}

// ReadKey reads a private key from the database
func (wdb *WalletDB) ReadKey(pubkey []byte) ([]byte, error) {
	var privkey []byte

	// Check encryption state and copy key under lock
	wdb.mu.RLock()
	isEncrypted := wdb.encrypted
	var encKey []byte
	if wdb.encryptionKey != nil {
		encKey = make([]byte, len(wdb.encryptionKey))
		copy(encKey, wdb.encryptionKey)
	}
	wdb.mu.RUnlock()

	err := wdb.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWallet)

		// Helper to build VarBytes format key
		buildVarBytesKey := func(prefix, pk []byte) []byte {
			var buf bytes.Buffer
			serialization.WriteVarBytes(&buf, prefix)
			serialization.WriteVarBytes(&buf, pk)
			return buf.Bytes()
		}

		// Helper to build legacy length-prefix format key
		// Format: [prefix_len][prefix][pubkey_len][pubkey]
		buildLegacyKey := func(prefix, pk []byte) []byte {
			key := make([]byte, 1+len(prefix)+1+len(pk))
			key[0] = byte(len(prefix))
			copy(key[1:], prefix)
			key[1+len(prefix)] = byte(len(pk))
			copy(key[2+len(prefix):], pk)
			return key
		}

		// Try encrypted keys (ckey prefix) in both formats
		// 1. VarBytes format (new Go code)
		if data := bucket.Get(buildVarBytesKey(keyPrefixCKey, pubkey)); data != nil {
			return wdb.decryptKeyData(data, pubkey, isEncrypted, encKey, &privkey)
		}
		// 2. Legacy length-prefix format (migrated BerkeleyDB data)
		if data := bucket.Get(buildLegacyKey(keyPrefixCKey, pubkey)); data != nil {
			return wdb.decryptKeyData(data, pubkey, isEncrypted, encKey, &privkey)
		}

		// Try unencrypted keys (key prefix) in both formats
		// 1. VarBytes format
		if data := bucket.Get(buildVarBytesKey(keyPrefixKey, pubkey)); data != nil {
			return wdb.decodeUnencryptedKey(data, &privkey)
		}
		// 2. Legacy length-prefix format
		if data := bucket.Get(buildLegacyKey(keyPrefixKey, pubkey)); data != nil {
			return wdb.decodeUnencryptedKey(data, &privkey)
		}

		return fmt.Errorf("key not found")
	})

	return privkey, err
}

// decryptKeyData decrypts an encrypted key from the database
func (wdb *WalletDB) decryptKeyData(data, pubkey []byte, isEncrypted bool, encKey []byte, privkey *[]byte) error {
	if !isEncrypted || encKey == nil {
		return fmt.Errorf("wallet is locked")
	}

	// Try to decode VarBytes wrapper first
	var encryptedKey []byte
	reader := bytes.NewReader(data)
	decoded, err := serialization.ReadVarBytes(reader)
	if err == nil {
		encryptedKey = decoded
	} else {
		// Fallback: use data as-is (legacy format)
		encryptedKey = data
	}

	// Decrypt
	iv := crypto.GenerateIV(pubkey)
	decrypted, err := crypto.DecryptAES256CBC(encKey, iv, encryptedKey)
	if err != nil {
		return fmt.Errorf("failed to decrypt key: %w", err)
	}

	// Extract 32-byte private key from decrypted data
	// Decrypted data might be a DER structure, look for 0x04 0x20 pattern
	if len(decrypted) == 32 {
		// Already 32 bytes - use as is
		*privkey = decrypted
	} else {
		// Look for private key marker 0x04 0x20 (OCTET STRING of 32 bytes)
		privKeyMarker := []byte{0x04, 0x20}
		privKeyPos := bytes.Index(decrypted, privKeyMarker)
		if privKeyPos >= 0 && privKeyPos+2+32 <= len(decrypted) {
			// Extract 32-byte private key after marker
			*privkey = decrypted[privKeyPos+2 : privKeyPos+2+32]
		} else {
			// No marker found - return error
			return fmt.Errorf("failed to extract private key from decrypted data (length: %d)", len(decrypted))
		}
	}
	return nil
}

// decodeUnencryptedKey decodes an unencrypted key from the database
func (wdb *WalletDB) decodeUnencryptedKey(data []byte, privkey *[]byte) error {
	// Try to decode VarBytes wrapper first
	reader := bytes.NewReader(data)
	decoded, err := serialization.ReadVarBytes(reader)
	if err == nil {
		*privkey = decoded
	} else {
		// Fallback: use data as-is (legacy format might store raw key)
		// Check if it's a valid private key length
		if len(data) == 32 {
			*privkey = data
		} else {
			// Try to extract from DER format
			privKeyMarker := []byte{0x04, 0x20}
			privKeyPos := bytes.Index(data, privKeyMarker)
			if privKeyPos >= 0 && privKeyPos+2+32 <= len(data) {
				*privkey = data[privKeyPos+2 : privKeyPos+2+32]
			} else {
				return fmt.Errorf("failed to decode private key: %w", err)
			}
		}
	}
	return nil
}

// WriteHDChain writes HD chain state to the database
func (wdb *WalletDB) WriteHDChain(chain *legacy.CHDChain) error {
	// Serialize HD chain
	data, err := legacy.SerializeToBytes(chain)
	if err != nil {
		return err
	}

	// Write to database
	return wdb.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWallet)
		return bucket.Put(keyPrefixHDChain, data)
	})
}

// WriteCryptedHDChain writes encrypted HD chain state to the database
// and deletes the unencrypted HD chain key atomically.
// Matches C++ CWalletDB::WriteCryptedHDChain which calls Erase("hdchain").
func (wdb *WalletDB) WriteCryptedHDChain(chain *legacy.CHDChain) error {
	// Serialize HD chain
	data, err := legacy.SerializeToBytes(chain)
	if err != nil {
		return err
	}

	// Write encrypted chain and delete unencrypted chain atomically
	return wdb.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWallet)
		if err := bucket.Put(keyPrefixCHDChain, data); err != nil {
			return err
		}
		// Delete unencrypted HD chain to prevent plaintext seed leak
		return bucket.Delete(keyPrefixHDChain)
	})
}

// validateChainID validates that a chain ID has the correct length
// Chain ID should be 32 bytes (SHA256 output)
func validateChainID(chainID []byte) error {
	if len(chainID) == 0 {
		// Empty chainID is valid (wallet may not have HD chain yet)
		return nil
	}
	if len(chainID) != 32 {
		return fmt.Errorf("invalid chain ID length: got %d bytes, expected 32 (SHA256 output)", len(chainID))
	}
	return nil
}

// ReadHDChain reads HD chain state from the database
// Checks both encrypted (chdchain) and unencrypted (hdchain) prefixes
func (wdb *WalletDB) ReadHDChain() (*legacy.CHDChain, bool, error) {
	var chain *legacy.CHDChain
	var encrypted bool

	err := wdb.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWallet)

		// Try encrypted HD chain first
		data := bucket.Get(keyPrefixCHDChain)
		if data != nil {
			encrypted = true
			chain = &legacy.CHDChain{}
			return legacy.DeserializeFromBytes(data, chain)
		}

		// Try unencrypted HD chain
		data = bucket.Get(keyPrefixHDChain)
		if data != nil {
			encrypted = false
			chain = &legacy.CHDChain{}
			return legacy.DeserializeFromBytes(data, chain)
		}

		return fmt.Errorf("HD chain not found")
	})

	if err != nil {
		return nil, false, err
	}

	// Validate chain ID if present
	if chain != nil && len(chain.ChainID) > 0 {
		if err := validateChainID(chain.ChainID); err != nil {
			return nil, false, fmt.Errorf("corrupted HD chain in database: %w", err)
		}
	}

	return chain, encrypted, nil
}

// GetAllKeys returns all public keys stored in the wallet
func (wdb *WalletDB) GetAllKeys() ([][]byte, error) {
	var pubkeys [][]byte

	err := wdb.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWallet)
		if bucket == nil {
			return nil
		}

		// Iterate through all keys looking for key or ckey entries
		c := bucket.Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			pubkey := tryExtractPubkey(k)
			if pubkey != nil {
				pubkeys = append(pubkeys, pubkey)
			}
		}

		return nil
	})

	return pubkeys, err
}

// tryExtractPubkey attempts to extract a public key from a database key
// Supports both VarBytes format (new Go code) and legacy length-prefix format (migrated data)
func tryExtractPubkey(key []byte) []byte {
	pubkey, _ := tryExtractPubkeyWithSource(key)
	return pubkey
}

// tryExtractPubkeyWithSource extracts pubkey and returns source type (key/ckey/hdpubkey)
func tryExtractPubkeyWithSource(key []byte) ([]byte, string) {
	// Attempt 1: VarBytes format (new Go code)
	// Format: VarBytes("key" or "ckey" or "hdpubkey") + VarBytes(pubkey)
	reader := bytes.NewReader(key)
	prefix, err := serialization.ReadVarBytes(reader)
	if err == nil {
		var source string
		if bytes.Equal(prefix, keyPrefixKey) {
			source = "key"
		} else if bytes.Equal(prefix, keyPrefixCKey) {
			source = "ckey"
		} else if bytes.Equal(prefix, keyPrefixHDPubKey) {
			source = "hdpubkey"
		}
		if source != "" {
			pubkey, err := serialization.ReadVarBytes(reader)
			if err == nil && (len(pubkey) == 33 || len(pubkey) == 65) {
				return pubkey, source + "(varbytes)"
			}
		}
	}

	// Attempt 2: Legacy length-prefix format (migrated BerkeleyDB data)
	// Format: [prefix_len][prefix][pubkey_len][pubkey]
	if len(key) < 2 {
		return nil, ""
	}

	prefixLen := int(key[0])
	if prefixLen == 0 || prefixLen > 20 || len(key) < 1+prefixLen {
		return nil, ""
	}

	prefix = key[1 : 1+prefixLen]
	var source string
	if bytes.Equal(prefix, keyPrefixKey) {
		source = "key"
	} else if bytes.Equal(prefix, keyPrefixCKey) {
		source = "ckey"
	} else if bytes.Equal(prefix, keyPrefixHDPubKey) {
		source = "hdpubkey"
	}
	if source != "" {
		dataStart := 1 + prefixLen
		if dataStart >= len(key) {
			return nil, ""
		}

		// Read pubkey length
		pubkeyLen := int(key[dataStart])
		pubkeyStart := dataStart + 1

		if pubkeyStart+pubkeyLen > len(key) {
			return nil, ""
		}

		// Extract public key
		pubkey := key[pubkeyStart : pubkeyStart+pubkeyLen]
		if len(pubkey) == 33 || len(pubkey) == 65 {
			return pubkey, source + "(legacy)"
		}
	}

	return nil, ""
}

// ReadHDPubKey reads an HD public key entry for a given pubkey
// Returns the CHDPubKey data which contains derivation path info
func (wdb *WalletDB) ReadHDPubKey(pubkey []byte) (*legacy.CHDPubKey, error) {
	var hdPubKey legacy.CHDPubKey

	err := wdb.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWallet)
		if bucket == nil {
			return fmt.Errorf("wallet bucket not found")
		}

		// Try VarBytes format: VarBytes("hdpubkey") + VarBytes(pubkey)
		var keyBuf bytes.Buffer
		serialization.WriteVarBytes(&keyBuf, keyPrefixHDPubKey)
		serialization.WriteVarBytes(&keyBuf, pubkey)

		data := bucket.Get(keyBuf.Bytes())
		if data != nil {
			reader := bytes.NewReader(data)
			if err := hdPubKey.Deserialize(reader); err != nil {
				return fmt.Errorf("failed to deserialize hdpubkey: %w", err)
			}
			return nil
		}

		// Try legacy length-prefix format
		// Format: [8]["hdpubkey"][33][pubkey]
		legacyKey := make([]byte, 0, 1+8+1+len(pubkey))
		legacyKey = append(legacyKey, 8) // length of "hdpubkey"
		legacyKey = append(legacyKey, keyPrefixHDPubKey...)
		legacyKey = append(legacyKey, byte(len(pubkey)))
		legacyKey = append(legacyKey, pubkey...)

		data = bucket.Get(legacyKey)
		if data != nil {
			reader := bytes.NewReader(data)
			if err := hdPubKey.Deserialize(reader); err != nil {
				return fmt.Errorf("failed to deserialize legacy hdpubkey: %w", err)
			}
			return nil
		}

		return fmt.Errorf("hdpubkey not found")
	})

	if err != nil {
		return nil, err
	}
	return &hdPubKey, nil
}

// GetAllHDPubKeys returns all HD public keys stored in the wallet
func (wdb *WalletDB) GetAllHDPubKeys() (map[string]*legacy.CHDPubKey, error) {
	result := make(map[string]*legacy.CHDPubKey)

	err := wdb.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWallet)
		if bucket == nil {
			return nil
		}

		c := bucket.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			// Try to extract pubkey from hdpubkey entry
			pubkey := tryExtractHDPubKeyPubkey(k)
			if pubkey == nil {
				continue
			}

			var hdPubKey legacy.CHDPubKey
			reader := bytes.NewReader(v)
			if err := hdPubKey.Deserialize(reader); err != nil {
				continue // Skip malformed entries
			}

			// Use hex-encoded pubkey as map key
			result[fmt.Sprintf("%x", pubkey)] = &hdPubKey
		}

		return nil
	})

	return result, err
}

// tryExtractHDPubKeyPubkey extracts pubkey from an hdpubkey database key
func tryExtractHDPubKeyPubkey(key []byte) []byte {
	// Try VarBytes format
	reader := bytes.NewReader(key)
	prefix, err := serialization.ReadVarBytes(reader)
	if err == nil && bytes.Equal(prefix, keyPrefixHDPubKey) {
		pubkey, err := serialization.ReadVarBytes(reader)
		if err == nil && len(pubkey) == 33 {
			return pubkey
		}
	}

	// Try legacy length-prefix format
	if len(key) < 2 {
		return nil
	}
	prefixLen := int(key[0])
	if prefixLen != 8 || len(key) < 1+prefixLen {
		return nil
	}
	prefix = key[1 : 1+prefixLen]
	if !bytes.Equal(prefix, keyPrefixHDPubKey) {
		return nil
	}

	dataStart := 1 + prefixLen
	if dataStart >= len(key) {
		return nil
	}
	pubkeyLen := int(key[dataStart])
	pubkeyStart := dataStart + 1
	if pubkeyStart+pubkeyLen > len(key) || pubkeyLen != 33 {
		return nil
	}

	return key[pubkeyStart : pubkeyStart+pubkeyLen]
}

// WriteName writes an address label to the database
// Matches legacy CWalletDB::WriteName behavior
func (wdb *WalletDB) WriteName(address string, label string) error {
	// Create key: "name" + address
	key := append(keyPrefixName, []byte(address)...)

	// Write to database
	return wdb.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWallet)
		return bucket.Put(key, []byte(label))
	})
}

// WritePurpose writes an address purpose to the database
// Matches legacy CWalletDB::WritePurpose behavior
func (wdb *WalletDB) WritePurpose(address string, purpose string) error {
	// Create key: "purpose" + address
	key := append(keyPrefixPurpose, []byte(address)...)

	// Write to database
	return wdb.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWallet)
		return bucket.Put(key, []byte(purpose))
	})
}

// ReadName reads an address label from the database
func (wdb *WalletDB) ReadName(address string) (string, error) {
	var label string

	err := wdb.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWallet)
		key := append(keyPrefixName, []byte(address)...)
		data := bucket.Get(key)
		if data == nil {
			return fmt.Errorf("label not found for address")
		}
		label = string(data)
		return nil
	})

	return label, err
}

// ReadPurpose reads an address purpose from the database
func (wdb *WalletDB) ReadPurpose(address string) (string, error) {
	var purpose string

	err := wdb.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWallet)
		key := append(keyPrefixPurpose, []byte(address)...)
		data := bucket.Get(key)
		if data == nil {
			return fmt.Errorf("purpose not found for address")
		}
		purpose = string(data)
		return nil
	})

	return purpose, err
}

// WriteDestData writes address destdata to database (for payment requests, etc.)
// Matches legacy CWalletDB::WriteDestData behavior
// Key format: "destdata" + address + dataKey (e.g., "rr0" for payment request 0)
func (wdb *WalletDB) WriteDestData(address, dataKey, value string) error {
	// Create compound key: "destdata" + address + dataKey
	key := append(keyPrefixDestData, []byte(address+dataKey)...)

	// Write to database
	return wdb.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWallet)
		return bucket.Put(key, []byte(value))
	})
}

// ReadDestData reads a single destdata value for an address
func (wdb *WalletDB) ReadDestData(address, dataKey string) (string, error) {
	var value string

	err := wdb.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWallet)
		key := append(keyPrefixDestData, []byte(address+dataKey)...)
		data := bucket.Get(key)
		if data == nil {
			return fmt.Errorf("destdata not found for address %s key %s", address, dataKey)
		}
		value = string(data)
		return nil
	})

	return value, err
}

// EraseDestData deletes address destdata from database
func (wdb *WalletDB) EraseDestData(address, dataKey string) error {
	key := append(keyPrefixDestData, []byte(address+dataKey)...)

	return wdb.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWallet)
		return bucket.Delete(key)
	})
}

// GetAllDestData returns all destdata for an address
// Returns map of dataKey -> value (e.g., "rr0" -> "{json...}", "rr1" -> "{json...}")
func (wdb *WalletDB) GetAllDestData(address string) (map[string]string, error) {
	result := make(map[string]string)

	err := wdb.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWallet)
		if bucket == nil {
			return nil
		}

		// Build prefix for this address: "destdata" + address
		prefix := append(keyPrefixDestData, []byte(address)...)

		// Iterate through all keys with this prefix
		c := bucket.Cursor()
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			// Extract dataKey by removing prefix
			dataKey := string(k[len(prefix):])
			result[dataKey] = string(v)
		}

		return nil
	})

	return result, err
}

// Sync flushes all pending writes to disk
func (wdb *WalletDB) Sync() error {
	return wdb.db.Sync()
}

// GetDB returns the underlying bbolt database for advanced operations
func (wdb *WalletDB) GetDB() *bbolt.DB {
	return wdb.db
}

// Backup safely copies the wallet database to destination
func (wdb *WalletDB) Backup(destination string) error {
	return wdb.db.View(func(tx *bbolt.Tx) error {
		return tx.CopyFile(destination, 0600)
	})
}

// GetReserveBalance returns the reserve balance setting
func (wdb *WalletDB) GetReserveBalance() (bool, int64, error) {
	var enabled bool
	var amount int64

	err := wdb.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWallet)

		// Read reserve enabled flag
		enabledData := bucket.Get([]byte("reservebalance_enabled"))
		if len(enabledData) >= 1 {
			enabled = enabledData[0] == 1
		}

		// Read reserve amount
		amountData := bucket.Get([]byte("reservebalance_amount"))
		if len(amountData) >= 8 {
			amount = int64(binary.LittleEndian.Uint64(amountData))
		}

		return nil
	})

	return enabled, amount, err
}

// SetReserveBalance sets the reserve balance for staking
func (wdb *WalletDB) SetReserveBalance(enabled bool, amount int64) error {
	return wdb.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWallet)

		// Write enabled flag
		enabledByte := byte(0)
		if enabled {
			enabledByte = 1
		}
		if err := bucket.Put([]byte("reservebalance_enabled"), []byte{enabledByte}); err != nil {
			return err
		}

		// Write amount
		amountBytes := make([]byte, 8)
		binary.LittleEndian.PutUint64(amountBytes, uint64(amount))
		return bucket.Put([]byte("reservebalance_amount"), amountBytes)
	})
}

// GetStakeSplitThreshold returns the stake split threshold
func (wdb *WalletDB) GetStakeSplitThreshold() (int64, error) {
	var threshold int64

	err := wdb.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWallet)
		data := bucket.Get([]byte("stakesplitthreshold"))
		if len(data) >= 8 {
			threshold = int64(binary.LittleEndian.Uint64(data))
		} else {
			// Default threshold: 20000 TWINS (legacy: wallet.h:349 nStakeSplitThreshold = 20000)
			threshold = 20000 * 1e8
		}
		return nil
	})

	return threshold, err
}

// SetStakeSplitThreshold sets the stake split threshold
func (wdb *WalletDB) SetStakeSplitThreshold(threshold int64) error {
	return wdb.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWallet)
		thresholdBytes := make([]byte, 8)
		binary.LittleEndian.PutUint64(thresholdBytes, uint64(threshold))
		return bucket.Put([]byte("stakesplitthreshold"), thresholdBytes)
	})
}


// GetMultiSend gets the multisend entries
func (wdb *WalletDB) GetMultiSend() ([]MultiSendEntry, error) {
	var entries []MultiSendEntry

	err := wdb.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWallet)
		if bucket == nil {
			return nil
		}

		// Get multisend entries
		data := bucket.Get([]byte("multisend_entries"))
		if len(data) == 0 {
			return nil
		}

		// Deserialize entries: [count:4][addr1_len:4][addr1][percent1:4]...[addrN_len:4][addrN][percentN:4]
		buf := bytes.NewReader(data)
		var count uint32
		if err := binary.Read(buf, binary.LittleEndian, &count); err != nil {
			return err
		}

		entries = make([]MultiSendEntry, count)
		for i := uint32(0); i < count; i++ {
			var addrLen uint32
			if err := binary.Read(buf, binary.LittleEndian, &addrLen); err != nil {
				return err
			}

			addrBytes := make([]byte, addrLen)
			if _, err := buf.Read(addrBytes); err != nil {
				return err
			}
			entries[i].Address = string(addrBytes)

			if err := binary.Read(buf, binary.LittleEndian, &entries[i].Percent); err != nil {
				return err
			}
		}

		return nil
	})

	return entries, err
}

// SetMultiSend sets the multisend entries
func (wdb *WalletDB) SetMultiSend(entries []MultiSendEntry) error {
	return wdb.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWallet)
		if bucket == nil {
			return fmt.Errorf("wallet bucket not found")
		}

		// Serialize entries: [count:4][addr1_len:4][addr1][percent1:4]...[addrN_len:4][addrN][percentN:4]
		buf := new(bytes.Buffer)
		count := uint32(len(entries))
		if err := binary.Write(buf, binary.LittleEndian, count); err != nil {
			return err
		}

		for _, entry := range entries {
			addrBytes := []byte(entry.Address)
			addrLen := uint32(len(addrBytes))
			if err := binary.Write(buf, binary.LittleEndian, addrLen); err != nil {
				return err
			}
			if _, err := buf.Write(addrBytes); err != nil {
				return err
			}
			if err := binary.Write(buf, binary.LittleEndian, entry.Percent); err != nil {
				return err
			}
		}

		return bucket.Put([]byte("multisend_entries"), buf.Bytes())
	})
}

// GetMultiSendSettings gets the multisend settings
func (wdb *WalletDB) GetMultiSendSettings() (stakeEnabled bool, masternodeEnabled bool, disabledAddrs []string, err error) {
	err = wdb.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWallet)
		if bucket == nil {
			return nil
		}

		// Get stake enabled flag
		stakeData := bucket.Get([]byte("multisend_stake"))
		if len(stakeData) >= 1 {
			stakeEnabled = stakeData[0] != 0
		}

		// Get masternode enabled flag
		mnData := bucket.Get([]byte("multisend_masternode"))
		if len(mnData) >= 1 {
			masternodeEnabled = mnData[0] != 0
		}

		// Get disabled addresses
		disabledData := bucket.Get([]byte("multisend_disabled"))
		if len(disabledData) > 0 {
			buf := bytes.NewReader(disabledData)
			var count uint32
			if err := binary.Read(buf, binary.LittleEndian, &count); err != nil {
				return err
			}

			disabledAddrs = make([]string, count)
			for i := uint32(0); i < count; i++ {
				var addrLen uint32
				if err := binary.Read(buf, binary.LittleEndian, &addrLen); err != nil {
					return err
				}

				addrBytes := make([]byte, addrLen)
				if _, err := buf.Read(addrBytes); err != nil {
					return err
				}
				disabledAddrs[i] = string(addrBytes)
			}
		}

		return nil
	})

	return
}

// SetMultiSendSettings sets the multisend settings
func (wdb *WalletDB) SetMultiSendSettings(stakeEnabled bool, masternodeEnabled bool, disabledAddrs []string) error {
	return wdb.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWallet)
		if bucket == nil {
			return fmt.Errorf("wallet bucket not found")
		}

		// Store stake enabled flag
		stakeByte := byte(0)
		if stakeEnabled {
			stakeByte = 1
		}
		if err := bucket.Put([]byte("multisend_stake"), []byte{stakeByte}); err != nil {
			return err
		}

		// Store masternode enabled flag
		mnByte := byte(0)
		if masternodeEnabled {
			mnByte = 1
		}
		if err := bucket.Put([]byte("multisend_masternode"), []byte{mnByte}); err != nil {
			return err
		}

		// Store disabled addresses
		buf := new(bytes.Buffer)
		count := uint32(len(disabledAddrs))
		if err := binary.Write(buf, binary.LittleEndian, count); err != nil {
			return err
		}

		for _, addr := range disabledAddrs {
			addrBytes := []byte(addr)
			addrLen := uint32(len(addrBytes))
			if err := binary.Write(buf, binary.LittleEndian, addrLen); err != nil {
				return err
			}
			if _, err := buf.Write(addrBytes); err != nil {
				return err
			}
		}

		return bucket.Put([]byte("multisend_disabled"), buf.Bytes())
	})
}

// WriteMultisigAddress writes a multisig P2SH address to the database
func (wdb *WalletDB) WriteMultisigAddress(address string, ma *MultisigAddress) error {
	// Convert to legacy format for serialization
	legacyMA := &legacy.CMultisigAddress{
		Address:      ma.Address,
		RedeemScript: ma.RedeemScript,
		NRequired:    int32(ma.NRequired),
		Keys:         ma.Keys,
		Account:      ma.Account,
		CreatedAt:    ma.CreatedAt.Unix(),
	}

	// Serialize
	var buf bytes.Buffer
	if err := legacyMA.Serialize(&buf); err != nil {
		return fmt.Errorf("failed to serialize multisig address: %w", err)
	}

	// Create key: "multisig" + address
	key := append(keyPrefixMultisig, []byte(address)...)

	// Write to database
	return wdb.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWallet)
		return bucket.Put(key, buf.Bytes())
	})
}

// ReadMultisigAddress reads a multisig P2SH address from the database
func (wdb *WalletDB) ReadMultisigAddress(address string) (*MultisigAddress, error) {
	var ma *MultisigAddress

	err := wdb.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWallet)
		key := append(keyPrefixMultisig, []byte(address)...)
		data := bucket.Get(key)
		if data == nil {
			return fmt.Errorf("multisig address not found")
		}

		// Deserialize
		legacyMA := &legacy.CMultisigAddress{}
		reader := bytes.NewReader(data)
		if err := legacyMA.Deserialize(reader); err != nil {
			return fmt.Errorf("failed to deserialize multisig address: %w", err)
		}

		// Convert to wallet format
		ma = &MultisigAddress{
			Address:      legacyMA.Address,
			RedeemScript: legacyMA.RedeemScript,
			NRequired:    int(legacyMA.NRequired),
			Keys:         legacyMA.Keys,
			Account:      legacyMA.Account,
			CreatedAt:    time.Unix(legacyMA.CreatedAt, 0),
		}

		return nil
	})

	return ma, err
}

// GetAllMultisigAddresses returns all multisig addresses stored in the wallet
func (wdb *WalletDB) GetAllMultisigAddresses() (map[string]*MultisigAddress, error) {
	multisigAddrs := make(map[string]*MultisigAddress)

	err := wdb.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketWallet)
		if bucket == nil {
			return nil
		}

		// Iterate through all keys looking for multisig entries
		c := bucket.Cursor()
		prefix := keyPrefixMultisig
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			// Extract address from key
			address := string(k[len(prefix):])

			// Deserialize
			legacyMA := &legacy.CMultisigAddress{}
			reader := bytes.NewReader(v)
			if err := legacyMA.Deserialize(reader); err != nil {
				continue // Skip corrupted entries
			}

			// Convert to wallet format
			ma := &MultisigAddress{
				Address:      legacyMA.Address,
				RedeemScript: legacyMA.RedeemScript,
				NRequired:    int(legacyMA.NRequired),
				Keys:         legacyMA.Keys,
				Account:      legacyMA.Account,
				CreatedAt:    time.Unix(legacyMA.CreatedAt, 0),
			}

			multisigAddrs[address] = ma
		}

		return nil
	})

	return multisigAddrs, err
}
