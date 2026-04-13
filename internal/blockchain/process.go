package blockchain

import (
	"errors"
	"fmt"

	"github.com/twins-dev/twins-core/internal/storage"
	"github.com/twins-dev/twins-core/pkg/types"
)

// hasBlockFast checks if a block exists in the in-memory index (fast path, no DB I/O)
func (bc *BlockChain) hasBlockFast(hash types.Hash) bool {
	bc.indexMu.RLock()
	_, exists := bc.blockIndex[hash]
	bc.indexMu.RUnlock()
	return exists
}

// hasBlock checks if a block exists (in-memory first, then storage)
// Assumes bc.mu is already held by caller for atomic check-then-act operations
func (bc *BlockChain) hasBlock(hash types.Hash) (bool, error) {
	// Fast path: check in-memory index first (O(1), no DB I/O)
	// This is critical for performance during sequential sync - avoids ~2000 DB reads per 500 blocks
	if bc.hasBlockFast(hash) {
		return true, nil
	}

	// Slow path: check storage for blocks not yet in memory index
	return bc.storage.HasBlock(hash)
}

// ProcessBlock processes a new block
func (bc *BlockChain) ProcessBlock(block *types.Block) error {
	if block == nil {
		return fmt.Errorf("block is nil")
	}

	blockHash := block.Hash()
	bc.logger.WithField("block", blockHash.String()).Debug("Processing block")

	// Process the block using unified batch processor
	// All validation checks (existence, parent, orphan, reorg) are done inside
	// processBatchUnified under a single continuous lock to prevent TOCTOU race conditions
	if err := bc.processBatchUnified([]*types.Block{block}); err != nil {
		// Check if this is a parent not found error - need to handle orphans
		if errors.Is(err, ErrParentNotFound) {
			bc.logger.WithFields(map[string]interface{}{
				"block":  blockHash.String(),
				"error":  err.Error(),
			}).Debug("Block parent not found, may need orphan handling")
		}
		return err
	}

	bc.logger.WithField("block", blockHash.String()).Debug("Block processed successfully")

	// Try to process any orphans that may now connect
	// This runs outside the main processing lock
	bc.processOrphans(blockHash)

	return nil
}

// ConnectBlock connects a validated block to the chain
// This is the public API that uses the unified batch processor
func (bc *BlockChain) ConnectBlock(block *types.Block) error {
	// Use unified batch processor for consistent processing
	return bc.processBatchUnified([]*types.Block{block})
}


// DisconnectBlock disconnects a block from the chain.
// Acquires processingMu — must NOT be called while the lock is already held.
func (bc *BlockChain) DisconnectBlock(block *types.Block) error {
	bc.processingMu.Lock()
	defer bc.processingMu.Unlock()
	return bc.disconnectBlock(block)
}

// disconnectBlock is the internal disconnect implementation
// Restores spent UTXOs by looking up original transactions from chain
func (bc *BlockChain) disconnectBlock(block *types.Block) error {
	blockHash := block.Hash()

	// Get block height
	height, err := bc.storage.GetBlockHeight(blockHash)
	if err != nil {
		return fmt.Errorf("failed to get block height: %w", err)
	}

	// Start batch operation
	batch := bc.storage.NewBatch()

	// Process transactions in reverse order
	for i := len(block.Transactions) - 1; i >= 0; i-- {
		tx := block.Transactions[i]
		txHash := tx.Hash()

		// Remove UTXOs created by this transaction
		for outIdx, output := range tx.Outputs {
			outpoint := types.Outpoint{
				Hash:  txHash,
				Index: uint32(outIdx),
			}
			// Create minimal UTXO data for deletion
			utxo := &types.UTXO{
				Outpoint: outpoint,
				Output:   output,
				Height:   height,
			}
			if err := batch.DeleteUTXOWithData(outpoint, utxo); err != nil {
				bc.logger.WithError(err).Warn("Failed to delete UTXO during disconnect")
			}
		}

		// Restore spent UTXOs using mark-as-spent model (skip for coinbase - has no real inputs)
		// Note: Coinstake DOES have real inputs (the stake) that must be restored
		// With mark-as-spent model, we just reset the spending fields instead of recreating UTXO
		if !tx.IsCoinbase() {
			for _, input := range tx.Inputs {
				prevOutpoint := input.PreviousOutput

				// Simply mark the UTXO as unspent again
				// This is 25x faster than the old method that required transaction lookups
				if err := batch.UnspendUTXO(prevOutpoint); err != nil {
					return fmt.Errorf("failed to unspend UTXO %s:%d: %w", prevOutpoint.Hash.String(), prevOutpoint.Index, err)
				}

				bc.logger.WithField("outpoint", prevOutpoint.String()).
					Debug("Restored UTXO via unspend")
			}
		}

		// Delete transaction from index (critical for reorg correctness)
		// Without this, stale transactions remain indexed after block disconnect
		if err := batch.DeleteTransaction(txHash, height); err != nil {
			bc.logger.WithError(err).WithField("tx", txHash.String()).
				Warn("Failed to delete transaction during disconnect")
		}
	}

	// Delete stake modifier to prevent stale modifiers after rollback
	if err := batch.DeleteStakeModifier(blockHash); err != nil {
		bc.logger.WithError(err).WithField("block", blockHash.String()).
			Warn("Failed to delete stake modifier during disconnect")
	}

	// Delete block data and indexes within the batch so the entire disconnect
	// (UTXO rollback + tx deletion + block removal + chain state) is atomic.
	// A crash between separate operations previously left orphaned spending
	// references — UTXOs marked as spent by transactions that no longer exist.
	if err := batch.DeleteBlockDisconnect(blockHash, height); err != nil {
		return fmt.Errorf("failed to delete block in batch: %w", err)
	}

	// Update chain tip to parent
	if err := batch.SetChainState(height-1, block.Header.PrevBlockHash); err != nil {
		return err
	}

	// Commit batch — all changes (UTXO unspend, tx deletion, block removal,
	// chain state update) are persisted atomically in a single Pebble write.
	if err := batch.Commit(); err != nil {
		return fmt.Errorf("failed to commit disconnect: %w", err)
	}

	// Update in-memory state
	parentBlock, err := bc.storage.GetBlock(block.Header.PrevBlockHash)
	if err != nil {
		return fmt.Errorf("failed to get parent block: %w", err)
	}

	bc.mu.Lock()
	bc.bestBlock = parentBlock
	bc.bestHeight.Store(height - 1)
	bc.bestHash = block.Header.PrevBlockHash
	bc.mu.Unlock()

	// Remove from in-memory block index
	bc.indexMu.Lock()
	delete(bc.blockIndex, blockHash)
	bc.indexMu.Unlock()

	// Remove from knownBlocks cache
	bc.knownBlocksMu.Lock()
	delete(bc.knownBlocks, blockHash)
	bc.knownBlocksMu.Unlock()

	// Notify wallet about block disconnect so it can reverse its in-memory state
	// (remove UTXOs created by this block, restore UTXOs spent by this block, remove wallet txs)
	if bc.wallet != nil {
		if err := bc.wallet.NotifyBlockDisconnected(block); err != nil {
			bc.logger.WithError(err).WithFields(map[string]interface{}{
				"hash":   blockHash.String(),
				"height": height,
			}).Warn("Failed to notify wallet about block disconnect")
		}
	}

	bc.logger.WithFields(map[string]interface{}{
		"hash":   blockHash.String(),
		"height": height,
	}).Debug("Block disconnected and deleted")

	return nil
}

// addOrphan adds a block to the orphan pool
func (bc *BlockChain) addOrphan(block *types.Block) error {
	bc.orphansMu.Lock()
	defer bc.orphansMu.Unlock()

	blockHash := block.Hash()
	parentHash := block.Header.PrevBlockHash

	// Check if orphan already exists (shouldn't happen with ProcessBlock check, but be safe)
	if _, exists := bc.orphans[blockHash]; exists {
		bc.logger.WithField("block", blockHash.String()).Debug("Orphan already exists, skipping")
		return nil
	}

	// Check orphan limit
	if len(bc.orphans) >= bc.config.MaxOrphans {
		// Remove oldest orphan
		// In production, would have proper eviction policy
		for hash := range bc.orphans {
			delete(bc.orphans, hash)
			break
		}
	}

	bc.orphans[blockHash] = block
	bc.logger.WithFields(map[string]interface{}{
		"orphans": len(bc.orphans),
		"parent":  parentHash.String(),
	}).Debug("Block added to orphans")

	// During IBD, don't request individual parent blocks
	// The batch sync process will handle filling in the gaps sequentially
	// Only request parent blocks when we're caught up and doing normal operation
	isInIBD := bc.IsInitialBlockDownload()

	if isInIBD {
		bc.logger.WithFields(map[string]interface{}{
			"block":  blockHash.String(),
			"parent": parentHash.String(),
		}).Debug("Orphan block during IBD - will be resolved by batch sync")
		return nil
	}

	// Request the missing parent block from P2P network (only when not in IBD)
	// Only request if we don't already have it in orphans (avoid requesting same parent multiple times)
	_, parentIsOrphan := bc.orphans[parentHash]
	if bc.onRequestBlock != nil && !parentIsOrphan {
		bc.logger.WithField("parent", parentHash.String()).Debug("Requesting missing parent block")
		go bc.onRequestBlock(parentHash)
	}

	return nil
}

// processOrphans tries to connect orphan blocks
func (bc *BlockChain) processOrphans(parentHash types.Hash) {
	bc.processOrphansWithDepth(parentHash, 0)
}

// processOrphansWithDepth tries to connect orphan blocks with recursion depth limit
func (bc *BlockChain) processOrphansWithDepth(parentHash types.Hash, depth int) {
	// Limit recursion depth to prevent infinite loops
	if depth > MaxOrphanProcessingDepth {
		bc.logger.WithField("depth", depth).Warn("Max orphan processing depth reached")
		return
	}

	bc.orphansMu.Lock()
	defer bc.orphansMu.Unlock()

	// Find orphans that connect to this parent
	connected := make([]types.Hash, 0)

	for hash, orphan := range bc.orphans {
		if orphan.Header.PrevBlockHash == parentHash {
			bc.logger.WithFields(map[string]interface{}{
				"orphan": hash.String(),
				"depth":  depth,
			}).Debug("Processing orphan block")

			// Unlock before processing to avoid deadlock
			bc.orphansMu.Unlock()
			err := bc.processBatchUnified([]*types.Block{orphan})
			bc.orphansMu.Lock()

			if err != nil {
				bc.logger.WithError(err).WithField("orphan", hash.String()).
					Error("Failed to process orphan")
			} else {
				connected = append(connected, hash)
			}
		}
	}

	// Remove connected orphans
	for _, hash := range connected {
		delete(bc.orphans, hash)
	}

	// Release lock before recursive calls
	bc.orphansMu.Unlock()

	// Recursively process children of connected orphans
	for _, hash := range connected {
		bc.processOrphansWithDepth(hash, depth+1)
	}

	// Reacquire lock to satisfy defer
	bc.orphansMu.Lock()
}

// updateBlockIndex updates the block index
func (bc *BlockChain) updateBlockIndex(block *types.Block, height uint32, status BlockStatus) {
	bc.indexMu.Lock()
	defer bc.indexMu.Unlock()

	blockHash := block.Hash()

	// Get parent node
	var parent *BlockNode
	if !block.Header.PrevBlockHash.IsZero() {
		parent = bc.blockIndex[block.Header.PrevBlockHash]
	}

	// Calculate work (simplified)
	work := types.NewBigInt(int64(height))
	if parent != nil && parent.Work != nil {
		work = types.NewBigInt(parent.Work.Int64() + 1)
	}

	node := &BlockNode{
		Hash:      blockHash,
		Height:    height,
		Work:      work,
		Parent:    parent,
		Block:     nil, // Don't store full block - causes memory leak (1GB+ on 200K blocks)
		Status:    status,
		Timestamp: block.Header.Timestamp,
	}

	bc.blockIndex[blockHash] = node

	// Update knownBlocks cache for O(1) HasBlock/GetBlockHeight
	bc.knownBlocksMu.Lock()
	bc.knownBlocks[blockHash] = height
	bc.knownBlocksMu.Unlock()
}

// updateStats updates blockchain statistics
func (bc *BlockChain) updateStats(block *types.Block) {
	bc.statsMu.Lock()
	defer bc.statsMu.Unlock()

	bc.stats.Height = bc.bestHeight.Load()
	bc.stats.BestBlockHash = bc.bestHash
	bc.stats.Blocks = bc.bestHeight.Load() + 1
	bc.stats.MedianTime = block.Header.Timestamp

	// Update transaction count
	bc.stats.Transactions += uint64(len(block.Transactions))

	// Log cache statistics periodically
	if bc.config.EnableUTXOCache && bc.stats.Blocks%1000 == 0 {
		if cachedStorage, ok := bc.storage.(*storage.CachedStorage); ok {
			cachedStorage.LogCacheStats()
		}
	}

	// Update orphan count
	bc.orphansMu.RLock()
	bc.stats.OrphanBlocks = len(bc.orphans)
	bc.orphansMu.RUnlock()
}

// GetStats returns blockchain statistics
func (bc *BlockChain) GetStats() BlockchainStats {
	bc.statsMu.RLock()
	defer bc.statsMu.RUnlock()

	// Create copy
	stats := bc.stats
	return stats
}
