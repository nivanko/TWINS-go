package storage

import (
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/twins-dev/twins-core/pkg/types"
)

// AddressTransaction represents a transaction involving a specific address
type AddressTransaction struct {
	TxHash  types.Hash
	Height  uint32
	TxIndex uint32 // Index of transaction within the block
}

// TransactionData represents transaction data with block location metadata
type TransactionData struct {
	BlockHash types.Hash
	Height    uint32
	TxIndex   uint32
	TxData    *types.Transaction
}

// SpenderInfo identifies the transaction that spent a given outpoint.
// Returned by FindAndMarkSpendersForOutpoints so callers can update their
// in-memory state (e.g. wallet dropping the UTXO from w.utxos) without
// re-reading storage.
type SpenderInfo struct {
	SpenderTxHash types.Hash
	SpenderHeight uint32
}

// Storage defines the main interface for blockchain data storage
type Storage interface {
	// Block operations
	StoreBlock(block *types.Block) error
	GetBlock(hash types.Hash) (*types.Block, error)
	GetBlockByHeight(height uint32) (*types.Block, error)
	HasBlock(hash types.Hash) (bool, error)
	DeleteBlock(hash types.Hash) error
	DeleteBlockData(hash types.Hash) error                                                    // Deletes only block data key (prefix 0x01), no indexes or transactions
	DeleteBlockIndex(hash types.Hash) error                                                   // Deletes only hash→height and height→hash index entries (for orphaned index cleanup)
	DeleteCorruptBlock(hash types.Hash, height uint32) (unspent int, deleted int, err error) // Deletes a corrupt block (missing transactions) with UTXO rollback via height-based scan
	GetBlockParentHash(hash types.Hash) (types.Hash, error)                                  // Gets parent hash from compact block without loading transactions

	// Transaction operations
	StoreTransaction(tx *types.Transaction) error
	GetTransaction(hash types.Hash) (*types.Transaction, error)
	GetTransactionData(hash types.Hash) (*TransactionData, error) // Returns transaction with block location metadata
	HasTransaction(hash types.Hash) (bool, error)
	GetBlockContainingTx(txHash types.Hash) (*types.Block, error)

	// Height operations
	GetBlockHeight(hash types.Hash) (uint32, error)
	GetBlockHashByHeight(height uint32) (types.Hash, error)
	StoreBlockIndex(hash types.Hash, height uint32) error

	// UTXO operations
	StoreUTXO(outpoint types.Outpoint, output *types.TxOutput, height uint32, isCoinbase bool) error
	GetUTXO(outpoint types.Outpoint) (*types.UTXO, error)
	DeleteUTXOWithData(outpoint types.Outpoint, utxo *types.UTXO) error
	GetUTXOsByAddress(address string) ([]*types.UTXO, error)

	// UTXO spending validation (for mark-as-spent model)
	// Returns (isSpent, spendingTxHash, error) - checks if UTXO is already spent
	ValidateUTXOSpend(outpoint types.Outpoint) (isSpent bool, spendingTxHash types.Hash, err error)

	// Address index operations (for wallet rescan and transaction lookups)
	// addressBinary is the decoded address (netID + hash160 = 21 bytes)
	IndexTransactionByAddress(addressBinary []byte, txHash types.Hash, height uint32, txIndex uint32, value int64, isInput bool, blockHash types.Hash) error
	GetTransactionsByAddress(addressBinary []byte) ([]AddressTransaction, error)
	DeleteAddressIndex(addressBinary []byte, txHash types.Hash) error

	// Chain state operations
	GetChainHeight() (uint32, error)
	GetChainTip() (types.Hash, error)
	SetChainState(height uint32, hash types.Hash) error

	// Money supply tracking (incremental per block)
	GetMoneySupply(height uint32) (int64, error)
	StoreMoneySupply(height uint32, supply int64) error

	// Invalid block tracking
	MarkBlockInvalid(hash types.Hash) error
	RemoveBlockInvalid(hash types.Hash) error
	IsBlockInvalid(hash types.Hash) (bool, error)
	GetInvalidBlocks() ([]types.Hash, error)

	// Dynamic checkpoint management
	AddDynamicCheckpoint(height uint32, hash types.Hash) error
	RemoveDynamicCheckpoint(height uint32) error
	GetDynamicCheckpoint(height uint32) (types.Hash, error)
	GetAllDynamicCheckpoints() (map[uint32]types.Hash, error)

	// Stake modifier storage (for PoS consensus)
	StoreStakeModifier(blockHash types.Hash, modifier uint64) error
	GetStakeModifier(blockHash types.Hash) (uint64, error)
	HasStakeModifier(blockHash types.Hash) (bool, error)
	DeleteStakeModifier(blockHash types.Hash) error

	// PoS checksum chain storage (for stake modifier checksum validation)
	// Stores hashProofOfStake (kernel hash) and stakeModifierChecksum for chaining
	StoreBlockPoSMetadata(blockHash types.Hash, checksum uint32, proofHash types.Hash) error
	GetBlockPoSMetadata(blockHash types.Hash) (checksum uint32, proofHash types.Hash, err error)
	HasBlockPoSMetadata(blockHash types.Hash) (bool, error)

	// Index consistency operations
	// IterateHashToHeight iterates all hash→height index entries.
	// Callback receives (blockHash, height). Return false to stop iteration.
	IterateHashToHeight(fn func(hash types.Hash, height uint32) bool) error

	// CleanOrphanedBlocks removes all blocks whose hash→height entry points to a
	// height above maxValidHeight. Performs full cleanup: block data, transaction
	// indexes, stake modifiers, PoS metadata, and both index entries. The height→hash
	// entry is only deleted if it points to the orphaned block (not the correct block
	// at that height). Returns the number of orphaned entries removed.
	CleanOrphanedBlocks(maxValidHeight uint32) (int, error)

	// UnspendUTXOsBySpendingTx iterates the UTXO set and resets the spending
	// reference (SpendingHeight and SpendingTxHash) on any UTXO whose
	// SpendingTxHash is present in txHashes. Affected UTXOs are re-added to the
	// address UTXO index so balance queries reflect them again. Returns the
	// number of UTXOs unspent. Used to reconcile stuck-spent UTXOs whose
	// spending transaction no longer exists in storage (stale references after
	// incomplete orphan cleanup or corrupt block recovery).
	UnspendUTXOsBySpendingTx(txHashes map[types.Hash]struct{}) (int, error)

	// FindAndMarkSpendersForOutpoints performs a full PrefixTransaction scan
	// searching for transactions whose inputs consume any of the given
	// outpoints. For every match, validates the spender transaction is on
	// the active main chain, confirms the UTXO is still unspent, and marks
	// it as spent via a batched write (reducing the address UTXO index
	// entry and updating SpendingHeight/SpendingTxHash). Returns a map of
	// outpoint → spender info so callers can update their in-memory state
	// (e.g. wallet dropping the UTXO from w.utxos).
	//
	// This is the recovery path for "phantom-unspent" UTXOs whose spending
	// transaction was persisted in storage but whose mark-spent AND
	// address-index input-side writes were skipped by an interrupted batch
	// commit. When both index writes are missing, address-history-based
	// reconciliation cannot find the spender, and a full transaction scan
	// is the only way to recover. Used as the final sweep of
	// RescanAllAddresses; expensive (O(all transactions in storage)) so
	// callers should invoke it at most once per startup.
	FindAndMarkSpendersForOutpoints(outpoints map[types.Outpoint]struct{}) (map[types.Outpoint]SpenderInfo, error)

	// Maintenance operations
	Compact() error
	Close() error
	Sync() error

	// Batch operations
	NewBatch() Batch

	// Statistics and diagnostics
	GetStats() (*DatabaseStats, error)
	GetSize() (int64, error)
}

// Batch defines the interface for atomic database operations
type Batch interface {
	StoreBlock(block *types.Block) error
	StoreBlockWithHeight(block *types.Block, height uint32) error // For batch processing where height is known
	StoreTransaction(tx *types.Transaction) error
	DeleteTransaction(txHash types.Hash, height uint32) error          // Delete transaction and its address indexes during block disconnect
	DeleteBlockDisconnect(hash types.Hash, height uint32) error        // Delete block data + indexes atomically within the batch (for disconnect)
	StoreUTXO(outpoint types.Outpoint, output *types.TxOutput, height uint32, isCoinbase bool) error
	DeleteUTXOWithData(outpoint types.Outpoint, utxo *types.UTXO) error // Delete UTXO with known data for proper address index cleanup
	SetChainState(height uint32, hash types.Hash) error
	StoreBlockIndex(hash types.Hash, height uint32) error
	StoreStakeModifier(blockHash types.Hash, modifier uint64) error
	DeleteStakeModifier(blockHash types.Hash) error
	StoreBlockPoSMetadata(blockHash types.Hash, checksum uint32, proofHash types.Hash) error
	StoreMoneySupply(height uint32, supply int64) error
	// addressBinary is the decoded address (netID + hash160 = 21 bytes)
	IndexTransactionByAddress(addressBinary []byte, txHash types.Hash, height uint32, txIndex uint32, value int64, isInput bool, blockHash types.Hash) error

	// UTXO spending operations (mark-as-spent model)
	// MarkUTXOSpent marks a UTXO as spent without deleting it
	// Returns the UTXO data for validation and address index updates
	MarkUTXOSpent(outpoint types.Outpoint, spendingHeight uint32, spendingTxHash types.Hash) (*types.UTXO, error)
	// UnspendUTXO marks a spent UTXO as unspent again (for block disconnect)
	// Resets SpendingHeight to 0 and SpendingTxHash to empty
	UnspendUTXO(outpoint types.Outpoint) error

	Commit() error
	Rollback() error
	Size() int
	Reset()
}

// SpentOutput represents a UTXO that was spent (used for address indexing)
type SpentOutput struct {
	Outpoint   types.Outpoint
	Output     *types.TxOutput
	Height     uint32
	IsCoinbase bool
}

// DatabaseStats contains statistics about the database
type DatabaseStats struct {
	Size           int64   `json:"size"`
	Keys           int64   `json:"keys"`
	Blocks         int64   `json:"blocks"`
	Transactions   int64   `json:"transactions"`
	UTXOs          int64   `json:"utxos"`
	CacheHitRate   float64 `json:"cache_hit_rate"`
	CacheHits      uint64  `json:"cache_hits"`
	CacheMisses    uint64  `json:"cache_misses"`
	CompactionTime int64   `json:"compaction_time_ms"`
	LastCompaction time.Time `json:"last_compaction"`
	WriteAmplification float64 `json:"write_amplification"`
}

// StorageConfig contains configuration parameters for storage
type StorageConfig struct {
	// Basic configuration
	Path string `yaml:"path" json:"path"`

	// Cache settings (in MB)
	CacheSize      int64 `yaml:"cache_size" json:"cache_size"`           // In-memory cache size
	BlockCacheSize int64 `yaml:"block_cache_size" json:"block_cache_size"` // Pebble block cache

	// Write buffer settings (in MB)
	WriteBufferSize int64 `yaml:"write_buffer_size" json:"write_buffer_size"` // Write buffer size
	WriteBuffer     int   `yaml:"write_buffer" json:"write_buffer"`           // Number of write buffers

	// File handling
	MaxOpenFiles int `yaml:"max_open_files" json:"max_open_files"`

	// Compression and filtering
	CompressionType string `yaml:"compression_type" json:"compression_type"` // "snappy", "zstd", "none"
	BloomFilterBits int    `yaml:"bloom_filter_bits" json:"bloom_filter_bits"`

	// Compaction settings
	MaxConcurrentCompactions int `yaml:"max_concurrent_compactions" json:"max_concurrent_compactions"`
	CompactionConcurrency    int `yaml:"compaction_concurrency" json:"compaction_concurrency"`

	// Performance tuning
	SyncWrites           bool          `yaml:"sync_writes" json:"sync_writes"`
	DisableWAL           bool          `yaml:"disable_wal" json:"disable_wal"`
	WALDir               string        `yaml:"wal_dir" json:"wal_dir"`
	MemTableStopWrites   int64         `yaml:"memtable_stop_writes" json:"memtable_stop_writes"`
	MemTableSize         int64         `yaml:"memtable_size" json:"memtable_size"`
	MaxBackgroundFlushes int           `yaml:"max_background_flushes" json:"max_background_flushes"`
	FlushDelayDelete     time.Duration `yaml:"flush_delay_delete" json:"flush_delay_delete"`

	// Advanced options
	ReadOnly                bool  `yaml:"read_only" json:"read_only"`
	CreateIfMissing         bool  `yaml:"create_if_missing" json:"create_if_missing"`
	ErrorIfExists           bool  `yaml:"error_if_exists" json:"error_if_exists"`
	ParanoidChecks          bool  `yaml:"paranoid_checks" json:"paranoid_checks"`
	DeleteRangeFlushDelay   int64 `yaml:"delete_range_flush_delay" json:"delete_range_flush_delay"`
	ForceNoFsync            bool  `yaml:"force_no_fsync" json:"force_no_fsync"`
}

// DefaultStorageConfig returns optimized default configuration for Go 1.25.
// On single-CPU machines (NumCPU <= 1), cache and buffer sizes are reduced
// to avoid excessive memory pressure on constrained VPS instances.
func DefaultStorageConfig() *StorageConfig {
	cacheSize := int64(512)
	blockCacheSize := int64(256)
	writeBufferSize := int64(64)
	writeBuffer := 4
	maxOpenFiles := 1000

	if runtime.NumCPU() <= 1 {
		cacheSize = 256
		blockCacheSize = 128
		writeBufferSize = 32
		writeBuffer = 2 // Pebble requires >= 2
		maxOpenFiles = 500
	}

	return &StorageConfig{
		// Basic configuration
		Path: "./data",

		// Cache settings (optimized for Go 1.25 GC)
		CacheSize:      cacheSize,
		BlockCacheSize: blockCacheSize,

		// Write buffer settings
		WriteBufferSize: writeBufferSize,
		WriteBuffer:     writeBuffer,

		// File handling
		MaxOpenFiles: maxOpenFiles,

		// Compression and filtering
		CompressionType: "snappy", // Fast compression
		BloomFilterBits: 10,       // Good balance between space and false positives

		// Compaction settings (leverages Go 1.25 container awareness)
		MaxConcurrentCompactions: 0, // Auto-detect based on GOMAXPROCS
		CompactionConcurrency:    0, // Auto-detect

		// Performance tuning
		SyncWrites:           true,                // Ensure durability
		DisableWAL:           false,               // Keep WAL for crash recovery
		WALDir:               "",                  // Same as data directory
		MemTableStopWrites:   64 * 1024 * 1024,   // 64MB
		MemTableSize:         32 * 1024 * 1024,   // 32MB
		MaxBackgroundFlushes: 1,                  // Number of background flush threads
		FlushDelayDelete:     10 * time.Second,   // Delay before deleting flushed files

		// Advanced options
		ReadOnly:              false,
		CreateIfMissing:       true,
		ErrorIfExists:         false,
		ParanoidChecks:        false, // Disable for better performance
		DeleteRangeFlushDelay: 0,     // No delay
		ForceNoFsync:          false, // Keep fsync for durability
	}
}

// TestStorageConfig returns configuration optimized for testing.
// Each call creates a unique temp directory to avoid Pebble DB lock conflicts
// between parallel tests.
func TestStorageConfig() *StorageConfig {
	config := DefaultStorageConfig()
	dir, err := os.MkdirTemp("", "twins-test-db-*")
	if err != nil {
		panic(fmt.Sprintf("TestStorageConfig: failed to create temp dir: %v", err))
	}
	config.Path = dir
	config.CacheSize = 16     // Smaller cache for tests
	config.BlockCacheSize = 8
	config.WriteBufferSize = 4
	config.WriteBuffer = 2 // Pebble requires MemTableStopWritesThreshold >= 2
	config.MaxOpenFiles = 100
	config.SyncWrites = false // Faster for tests
	return config
}

// BenchmarkStorageConfig returns configuration optimized for benchmarking
func BenchmarkStorageConfig() *StorageConfig {
	config := DefaultStorageConfig()
	config.Path = ":memory:"
	config.CacheSize = 1024    // Larger cache for benchmarks
	config.BlockCacheSize = 512
	config.WriteBufferSize = 128
	config.WriteBuffer = 8
	config.MaxOpenFiles = 2000
	config.BloomFilterBits = 15 // More accurate bloom filters
	config.SyncWrites = false   // Faster for benchmarks
	config.ParanoidChecks = false
	return config
}

// Storage errors
type StorageError struct {
	Code    string
	Message string
	Cause   error
}

func (e *StorageError) Error() string {
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}

func (e *StorageError) Unwrap() error {
	return e.Cause
}

// Common storage errors
var (
	ErrNotFound           = &StorageError{Code: "NOT_FOUND", Message: "key not found"}
	ErrCorruptedData      = &StorageError{Code: "CORRUPTED_DATA", Message: "data is corrupted"}
	ErrInvalidStorageType = &StorageError{Code: "INVALID_STORAGE_TYPE", Message: "invalid storage type"}
	ErrDatabaseClosed     = &StorageError{Code: "DATABASE_CLOSED", Message: "database is closed"}
	ErrBatchCommitted     = &StorageError{Code: "BATCH_COMMITTED", Message: "batch already committed"}
	ErrBatchRolledBack    = &StorageError{Code: "BATCH_ROLLED_BACK", Message: "batch already rolled back"}
	ErrInvalidKey         = &StorageError{Code: "INVALID_KEY", Message: "invalid key format"}
	ErrReadOnlyMode       = &StorageError{Code: "READ_ONLY_MODE", Message: "database is in read-only mode"}
)

// NewStorageError creates a new storage error with cause
func NewStorageError(code, message string, cause error) *StorageError {
	return &StorageError{
		Code:    code,
		Message: message,
		Cause:   cause,
	}
}

// Validation functions

// ValidateStorageConfig validates storage configuration
func ValidateStorageConfig(config *StorageConfig) error {
	if config == nil {
		return NewStorageError("INVALID_CONFIG", "config cannot be nil", nil)
	}

	if config.Path == "" {
		return NewStorageError("INVALID_CONFIG", "path cannot be empty", nil)
	}

	if config.CacheSize < 0 {
		return NewStorageError("INVALID_CONFIG", "cache size cannot be negative", nil)
	}

	if config.WriteBufferSize < 0 {
		return NewStorageError("INVALID_CONFIG", "write buffer size cannot be negative", nil)
	}

	if config.WriteBuffer < 1 {
		return NewStorageError("INVALID_CONFIG", "write buffer count must be at least 1", nil)
	}

	if config.MaxOpenFiles < 10 {
		return NewStorageError("INVALID_CONFIG", "max open files must be at least 10", nil)
	}

	validCompressionTypes := map[string]bool{
		"none":   true,
		"snappy": true,
		"zstd":   true,
		"lz4":    true,
	}

	if !validCompressionTypes[config.CompressionType] {
		return NewStorageError("INVALID_CONFIG", "invalid compression type: "+config.CompressionType, nil)
	}

	if config.BloomFilterBits < 0 || config.BloomFilterBits > 20 {
		return NewStorageError("INVALID_CONFIG", "bloom filter bits must be between 0 and 20", nil)
	}

	return nil
}

// IsNotFoundError checks if an error is a "not found" error. Matches the
// generic NOT_FOUND code as well as domain-specific variants (e.g.
// TX_NOT_FOUND returned by transaction lookups, HEIGHT_NOT_FOUND returned
// by height lookups) so callers can uniformly distinguish missing records
// from transient I/O errors.
func IsNotFoundError(err error) bool {
	if storageErr, ok := err.(*StorageError); ok {
		switch storageErr.Code {
		case "NOT_FOUND", "TX_NOT_FOUND", "HEIGHT_NOT_FOUND":
			return true
		}
	}
	return false
}

// IsCorruptedDataError checks if an error indicates corrupted data
func IsCorruptedDataError(err error) bool {
	if storageErr, ok := err.(*StorageError); ok {
		return storageErr.Code == "CORRUPTED_DATA"
	}
	return false
}

// IsDatabaseClosedError checks if an error indicates the database is closed
func IsDatabaseClosedError(err error) bool {
	if storageErr, ok := err.(*StorageError); ok {
		return storageErr.Code == "DATABASE_CLOSED"
	}
	return false
}