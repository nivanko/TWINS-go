package storage

import (
	"fmt"
	"sync/atomic"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/sirupsen/logrus"
	"github.com/twins-dev/twins-core/pkg/types"
)

// CacheMetrics tracks cache performance metrics
type CacheMetrics struct {
	Hits       atomic.Uint64
	Misses     atomic.Uint64
	Evictions  atomic.Uint64
	Writes     atomic.Uint64
	TotalBytes atomic.Uint64
}

// HitRate returns the cache hit rate as a percentage
func (m *CacheMetrics) HitRate() float64 {
	hits := m.Hits.Load()
	misses := m.Misses.Load()
	total := hits + misses
	if total == 0 {
		return 0
	}
	return float64(hits) / float64(total) * 100
}

// CachedStorage wraps a Storage implementation with UTXO caching
type CachedStorage struct {
	Storage // Embedded interface for delegation

	// UTXO cache with LRU eviction (hashicorp/golang-lru/v2 is internally thread-safe)
	utxoCache *lru.Cache[string, *types.UTXO]

	// Cache configuration
	maxCacheSize int // Maximum cache entries

	// Metrics
	metrics *CacheMetrics
	logger  *logrus.Entry
}

// CachedStorageConfig contains configuration for cached storage
type CachedStorageConfig struct {
	MaxUTXOCacheEntries int // Maximum number of UTXO entries to cache (default: 100000)
}

// DefaultCachedStorageConfig returns default cache configuration
func DefaultCachedStorageConfig() *CachedStorageConfig {
	return &CachedStorageConfig{
		MaxUTXOCacheEntries: 100000, // ~100MB for typical UTXOs
	}
}

// NewCachedStorage creates a new cached storage wrapper
func NewCachedStorage(underlying Storage, config *CachedStorageConfig) (*CachedStorage, error) {
	if config == nil {
		config = DefaultCachedStorageConfig()
	}

	cs := &CachedStorage{
		Storage:      underlying,
		maxCacheSize: config.MaxUTXOCacheEntries,
		metrics:      &CacheMetrics{},
		logger:       logrus.WithField("component", "cached_storage"),
	}

	// Create LRU cache with eviction callback wired to metrics
	cache, err := lru.NewWithEvict(config.MaxUTXOCacheEntries, func(key string, value *types.UTXO) {
		cs.metrics.Evictions.Add(1)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create LRU cache: %w", err)
	}
	cs.utxoCache = cache

	cs.logger.WithField("cache_size", config.MaxUTXOCacheEntries).Debug("UTXO cache initialized")

	return cs, nil
}

// makeUTXOCacheKey creates a cache key from an outpoint
func makeUTXOCacheKey(outpoint types.Outpoint) string {
	return fmt.Sprintf("%s:%d", outpoint.Hash.String(), outpoint.Index)
}

// StoreUTXO stores a UTXO with write-through caching
func (cs *CachedStorage) StoreUTXO(outpoint types.Outpoint, output *types.TxOutput, height uint32, isCoinbase bool) error {
	// Write to underlying storage first
	if err := cs.Storage.StoreUTXO(outpoint, output, height, isCoinbase); err != nil {
		return err
	}

	// Update cache
	utxo := &types.UTXO{
		Outpoint:   outpoint,
		Output:     output,
		Height:     height,
		IsCoinbase: isCoinbase,
	}
	cs.utxoCache.Add(makeUTXOCacheKey(outpoint), utxo)

	cs.metrics.Writes.Add(1)

	return nil
}

// GetUTXO retrieves a UTXO with caching
func (cs *CachedStorage) GetUTXO(outpoint types.Outpoint) (*types.UTXO, error) {
	key := makeUTXOCacheKey(outpoint)

	// Try cache first
	if utxo, ok := cs.utxoCache.Get(key); ok {
		cs.metrics.Hits.Add(1)

		// Return a copy to prevent cache corruption
		return &types.UTXO{
			Outpoint:       utxo.Outpoint,
			Output:         utxo.Output,
			Height:         utxo.Height,
			IsCoinbase:     utxo.IsCoinbase,
			SpendingHeight: utxo.SpendingHeight,
			SpendingTxHash: utxo.SpendingTxHash,
		}, nil
	}

	cs.metrics.Misses.Add(1)

	// Cache miss - fetch from storage
	utxo, err := cs.Storage.GetUTXO(outpoint)
	if err != nil {
		return nil, err
	}

	// Add to cache
	cs.utxoCache.Add(key, utxo)

	return utxo, nil
}

// DeleteUTXOWithData deletes a UTXO with cache invalidation
func (cs *CachedStorage) DeleteUTXOWithData(outpoint types.Outpoint, utxo *types.UTXO) error {
	// Delete from underlying storage
	if err := cs.Storage.DeleteUTXOWithData(outpoint, utxo); err != nil {
		return err
	}

	// Remove from cache
	cs.utxoCache.Remove(makeUTXOCacheKey(outpoint))

	return nil
}

// GetUTXOsByAddress retrieves UTXOs by address (not cached for now)
func (cs *CachedStorage) GetUTXOsByAddress(address string) ([]*types.UTXO, error) {
	// Address queries are less frequent during sync, so we don't cache them
	// This avoids cache pollution with large result sets
	return cs.Storage.GetUTXOsByAddress(address)
}

// FlushCache clears the UTXO cache (useful for testing or memory pressure)
func (cs *CachedStorage) FlushCache() {
	cs.utxoCache.Purge()
	cs.logger.Debug("UTXO cache flushed")
}

// UnspendUTXOsBySpendingTx delegates to the underlying storage and flushes the
// UTXO cache on success. The cache is purged rather than selectively
// invalidated because the underlying implementation does not expose the
// affected outpoints; this operation is rare (wallet rescan reconciliation)
// so the full purge is acceptable.
func (cs *CachedStorage) UnspendUTXOsBySpendingTx(txHashes map[types.Hash]struct{}) (int, error) {
	unspent, err := cs.Storage.UnspendUTXOsBySpendingTx(txHashes)
	if unspent > 0 {
		cs.utxoCache.Purge()
		cs.logger.WithField("unspent", unspent).Debug("UTXO cache purged after stale-spend reconciliation")
	}
	return unspent, err
}

// FindAndMarkSpendersForOutpoints delegates to the underlying storage and
// selectively invalidates cache entries for the outpoints that were
// marked as spent. Unlike UnspendUTXOsBySpendingTx this path returns
// the exact outpoint set, so we can invalidate precisely instead of
// purging the entire cache.
func (cs *CachedStorage) FindAndMarkSpendersForOutpoints(outpoints map[types.Outpoint]struct{}) (map[types.Outpoint]SpenderInfo, error) {
	results, err := cs.Storage.FindAndMarkSpendersForOutpoints(outpoints)
	if err != nil {
		return results, err
	}
	for op := range results {
		cs.utxoCache.Remove(makeUTXOCacheKey(op))
	}
	if len(results) > 0 {
		cs.logger.WithField("marked_spent", len(results)).Debug("UTXO cache entries invalidated after phantom-unspent full scan")
	}
	return results, nil
}

// GetCacheMetrics returns current cache metrics
func (cs *CachedStorage) GetCacheMetrics() *CacheMetrics {
	return cs.metrics
}

// LogCacheStats logs current cache statistics
func (cs *CachedStorage) LogCacheStats() {
	cacheLen := cs.utxoCache.Len()

	cs.logger.WithFields(logrus.Fields{
		"cache_entries": cacheLen,
		"cache_hits":    cs.metrics.Hits.Load(),
		"cache_misses":  cs.metrics.Misses.Load(),
		"hit_rate":      fmt.Sprintf("%.2f%%", cs.metrics.HitRate()),
		"evictions":     cs.metrics.Evictions.Load(),
		"writes":        cs.metrics.Writes.Load(),
	}).Debug("UTXO cache statistics")
}

// Batch operations - wrap underlying batch with cache updates
type CachedBatch struct {
	Batch
	cache *CachedStorage

	// Track operations for cache update
	storedUTXOs  []utxoOp
	deletedUTXOs []types.Outpoint
	spentUTXOs   []types.Outpoint
}

type utxoOp struct {
	outpoint   types.Outpoint
	output     *types.TxOutput
	height     uint32
	isCoinbase bool
}

// NewBatch creates a new batch that updates cache on commit
func (cs *CachedStorage) NewBatch() Batch {
	return &CachedBatch{
		Batch:        cs.Storage.NewBatch(),
		cache:        cs,
		storedUTXOs:  make([]utxoOp, 0, 100),
		deletedUTXOs: make([]types.Outpoint, 0, 100),
		spentUTXOs:   make([]types.Outpoint, 0, 100),
	}
}

// StoreUTXO in batch (cache updated on commit)
func (cb *CachedBatch) StoreUTXO(outpoint types.Outpoint, output *types.TxOutput, height uint32, isCoinbase bool) error {
	// Store in underlying batch
	if err := cb.Batch.StoreUTXO(outpoint, output, height, isCoinbase); err != nil {
		return err
	}

	// Track for cache update
	cb.storedUTXOs = append(cb.storedUTXOs, utxoOp{
		outpoint:   outpoint,
		output:     output,
		height:     height,
		isCoinbase: isCoinbase,
	})

	return nil
}

// MarkUTXOSpent in batch (cache invalidated on commit to prevent stale unspent reads)
func (cb *CachedBatch) MarkUTXOSpent(outpoint types.Outpoint, spendingHeight uint32, spendingTxHash types.Hash) (*types.UTXO, error) {
	utxo, err := cb.Batch.MarkUTXOSpent(outpoint, spendingHeight, spendingTxHash)
	if err != nil {
		return utxo, err
	}

	// Track for cache invalidation — forces next GetUTXO to read from storage
	// where SpendingHeight/SpendingTxHash are now set
	cb.spentUTXOs = append(cb.spentUTXOs, outpoint)

	return utxo, nil
}

// DeleteUTXOWithData in batch (cache updated on commit)
func (cb *CachedBatch) DeleteUTXOWithData(outpoint types.Outpoint, utxo *types.UTXO) error {
	// Delete from underlying batch
	if err := cb.Batch.DeleteUTXOWithData(outpoint, utxo); err != nil {
		return err
	}

	// Track for cache update
	cb.deletedUTXOs = append(cb.deletedUTXOs, outpoint)

	return nil
}

// Commit the batch and update cache
func (cb *CachedBatch) Commit() error {
	// Commit underlying batch first
	if err := cb.Batch.Commit(); err != nil {
		return err
	}

	// Update cache after successful commit (LRU is internally thread-safe)

	// Apply deletions
	for _, outpoint := range cb.deletedUTXOs {
		cb.cache.utxoCache.Remove(makeUTXOCacheKey(outpoint))
	}

	// Invalidate spent UTXOs — forces next GetUTXO to read fresh from storage
	// where SpendingHeight/SpendingTxHash are set, enabling mempool validation
	// to correctly reject transactions spending already-confirmed UTXOs
	for _, outpoint := range cb.spentUTXOs {
		cb.cache.utxoCache.Remove(makeUTXOCacheKey(outpoint))
	}

	// Apply additions
	for _, op := range cb.storedUTXOs {
		utxo := &types.UTXO{
			Outpoint:   op.outpoint,
			Output:     op.output,
			Height:     op.height,
			IsCoinbase: op.isCoinbase,
		}
		cb.cache.utxoCache.Add(makeUTXOCacheKey(op.outpoint), utxo)
		cb.cache.metrics.Writes.Add(1)
	}

	return nil
}
