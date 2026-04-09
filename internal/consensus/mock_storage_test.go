package consensus

import (
	"errors"
	"fmt"

	"github.com/twins-dev/twins-core/internal/storage"
	"github.com/twins-dev/twins-core/pkg/types"
)

// MockStorage implements storage.Storage interface for testing
type MockStorage struct {
	blocks          map[types.Hash]*types.Block
	blocksByHeight  map[uint32]*types.Block
	transactions    map[types.Hash]*types.Transaction
	txBlocks        map[types.Hash]*types.Block // Maps transaction hash to containing block
	utxos           map[types.Outpoint]*types.UTXO
	chainTip        *types.Block
	stakeModifiers  map[types.Hash]uint64 // Stake modifier storage for integration tests
}

// NewMockStorage creates a new mock storage instance
func NewMockStorage() *MockStorage {
	return &MockStorage{
		blocks:         make(map[types.Hash]*types.Block),
		blocksByHeight: make(map[uint32]*types.Block),
		transactions:   make(map[types.Hash]*types.Transaction),
		txBlocks:       make(map[types.Hash]*types.Block),
		utxos:          make(map[types.Outpoint]*types.UTXO),
		stakeModifiers: make(map[types.Hash]uint64),
	}
}

// GetBlock retrieves a block by hash
func (ms *MockStorage) GetBlock(hash types.Hash) (*types.Block, error) {
	if block, exists := ms.blocks[hash]; exists {
		return block, nil
	}
	return nil, fmt.Errorf("block not found: %s", hash.String())
}

// GetBlockByHash retrieves a block by hash (alias for GetBlock)
func (ms *MockStorage) GetBlockByHash(hash types.Hash) (*types.Block, error) {
	return ms.GetBlock(hash)
}

// GetBlockByHeight retrieves a block by height
func (ms *MockStorage) GetBlockByHeight(height uint32) (*types.Block, error) {
	if block, exists := ms.blocksByHeight[height]; exists {
		return block, nil
	}
	return nil, fmt.Errorf("block not found at height %d", height)
}

// StoreBlock stores a block
func (ms *MockStorage) StoreBlock(block *types.Block) error {
	if block == nil {
		return errors.New("block is nil")
	}

	hash := block.Header.Hash()
	ms.blocks[hash] = block
	// Height mapping managed externally for testing

	// Store all transactions in the block
	for _, tx := range block.Transactions {
		txHash := tx.Hash()
		ms.transactions[txHash] = tx
		ms.txBlocks[txHash] = block
	}

	// Update chain tip if this is the highest block
	// Simplified chain tip management
	ms.chainTip = block

	return nil
}

// GetTransaction retrieves a transaction by hash
func (ms *MockStorage) GetTransaction(hash types.Hash) (*types.Transaction, error) {
	if tx, exists := ms.transactions[hash]; exists {
		return tx, nil
	}
	return nil, fmt.Errorf("transaction not found: %s", hash.String())
}

// GetBlockContainingTx retrieves the block containing a transaction
func (ms *MockStorage) GetBlockContainingTx(txHash types.Hash) (*types.Block, error) {
	if block, exists := ms.txBlocks[txHash]; exists {
		return block, nil
	}
	return nil, fmt.Errorf("block containing transaction not found: %s", txHash.String())
}

// GetChainTip returns the current chain tip hash
func (ms *MockStorage) GetChainTip() (types.Hash, error) {
	if ms.chainTip == nil {
		return types.Hash{}, errors.New("no chain tip available")
	}
	return ms.chainTip.Header.Hash(), nil
}

// GetChainHeight returns the current chain height
func (ms *MockStorage) GetChainHeight() (uint32, error) {
	if ms.chainTip == nil {
		return 0, errors.New("no chain tip available")
	}
	// Find the height of the chain tip from blocksByHeight
	for height, block := range ms.blocksByHeight {
		if block.Hash() == ms.chainTip.Hash() {
			return height, nil
		}
	}
	// If not found in blocksByHeight, return 0 as fallback
	return 0, nil
}

// HasBlock checks if a block exists
func (ms *MockStorage) HasBlock(hash types.Hash) (bool, error) {
	_, exists := ms.blocks[hash]
	return exists, nil
}

// HasTransaction checks if a transaction exists
func (ms *MockStorage) HasTransaction(hash types.Hash) (bool, error) {
	_, exists := ms.transactions[hash]
	return exists, nil
}

// GetBlockLocator returns a block locator for sync
func (ms *MockStorage) GetBlockLocator() ([]types.Hash, error) {
	var locator []types.Hash

	// Add the chain tip
	if ms.chainTip != nil {
		locator = append(locator, ms.chainTip.Header.Hash())
	}

	// Add some previous blocks (simplified)
	height := uint32(0)
	if ms.chainTip != nil {
		height = uint32(1) // Simplified for testing
	}

	step := uint32(1)
	for height > 0 && len(locator) < 10 {
		if height >= step {
			height -= step
		} else {
			height = 0
		}

		if block, exists := ms.blocksByHeight[height]; exists {
			locator = append(locator, block.Header.Hash())
		}

		if step < 10 {
			step *= 2
		}
	}

	return locator, nil
}

// Close closes the storage (no-op for mock)
func (ms *MockStorage) Close() error {
	return nil
}

// Additional helper methods for testing

// AddTestBlock adds a test block to the storage
func (ms *MockStorage) AddTestBlock(block *types.Block) {
	if block == nil {
		return
	}

	hash := block.Header.Hash()
	ms.blocks[hash] = block
	// Height mapping managed externally for testing

	// Add transactions
	for _, tx := range block.Transactions {
		txHash := tx.Hash()
		ms.transactions[txHash] = tx
		ms.txBlocks[txHash] = block
	}

	// Update chain tip
	// Simplified chain tip management
	ms.chainTip = block
}

// AddTestTransaction adds a test transaction
func (ms *MockStorage) AddTestTransaction(tx *types.Transaction, containingBlock *types.Block) {
	if tx == nil {
		return
	}

	txHash := tx.Hash()
	ms.transactions[txHash] = tx

	if containingBlock != nil {
		ms.txBlocks[txHash] = containingBlock
	}
}

// SetChainTip sets the chain tip hash (implements Storage interface)
func (ms *MockStorage) SetChainTip(hash types.Hash) error {
	// Find the block with this hash and set it as chain tip
	if block, exists := ms.blocks[hash]; exists {
		ms.chainTip = block
		return nil
	}
	return fmt.Errorf("block not found: %s", hash.String())
}

// SetChainTipBlock manually sets the chain tip (test helper)
func (ms *MockStorage) SetChainTipBlock(block *types.Block) {
	ms.chainTip = block
	if block != nil {
		ms.blocks[block.Hash()] = block
	}
}

// Clear clears all storage data
func (ms *MockStorage) Clear() {
	ms.blocks = make(map[types.Hash]*types.Block)
	ms.blocksByHeight = make(map[uint32]*types.Block)
	ms.transactions = make(map[types.Hash]*types.Transaction)
	ms.txBlocks = make(map[types.Hash]*types.Block)
	ms.chainTip = nil
}

// GetBlockCount returns the number of stored blocks
func (ms *MockStorage) GetBlockCount() int {
	return len(ms.blocks)
}

// GetTransactionCount returns the number of stored transactions
func (ms *MockStorage) GetTransactionCount() int {
	return len(ms.transactions)
}

// GetAllBlocks returns all stored blocks
func (ms *MockStorage) GetAllBlocks() []*types.Block {
	blocks := make([]*types.Block, 0, len(ms.blocks))
	for _, block := range ms.blocks {
		blocks = append(blocks, block)
	}
	return blocks
}

// GetBlocksInRange returns blocks within a height range
func (ms *MockStorage) GetBlocksInRange(startHeight, endHeight uint32) ([]*types.Block, error) {
	var blocks []*types.Block

	for height := startHeight; height <= endHeight; height++ {
		if block, exists := ms.blocksByHeight[height]; exists {
			blocks = append(blocks, block)
		}
	}

	return blocks, nil
}

// Missing methods to implement Storage interface
func (ms *MockStorage) Compact() error { return nil }
func (ms *MockStorage) Sync() error    { return nil }
func (ms *MockStorage) DeleteCorruptBlock(hash types.Hash, height uint32) (int, int, error) {
	delete(ms.blocks, hash)
	return 0, 0, nil
}
func (ms *MockStorage) DeleteBlock(hash types.Hash) error {
	delete(ms.blocks, hash)
	return nil
}
func (ms *MockStorage) DeleteBlockData(hash types.Hash) error {
	delete(ms.blocks, hash)
	return nil
}
func (ms *MockStorage) DeleteBlockIndex(hash types.Hash) error {
	return nil
}
func (ms *MockStorage) GetBlockHeight(hash types.Hash) (uint32, error) {
	// Simple implementation - find the block and iterate to find its height
	for height, block := range ms.blocksByHeight {
		if block.Hash() == hash {
			return height, nil
		}
	}
	return 0, errors.New("block not found")
}

func (ms *MockStorage) StoreBlockIndex(hash types.Hash, height uint32) error {
	ms.blocksByHeight[height] = ms.blocks[hash]
	return nil
}

func (ms *MockStorage) GetBlockHashByHeight(height uint32) (types.Hash, error) {
	if block, exists := ms.blocksByHeight[height]; exists {
		return block.Hash(), nil
	}
	return types.Hash{}, fmt.Errorf("block not found at height %d", height)
}

// UTXO operations
func (ms *MockStorage) StoreUTXO(outpoint types.Outpoint, output *types.TxOutput, height uint32, isCoinbase bool) error {
	ms.utxos[outpoint] = &types.UTXO{
		Outpoint:   outpoint,
		Output:     output,
		Height:     height,
		IsCoinbase: isCoinbase,
	}
	return nil
}

func (ms *MockStorage) GetUTXO(outpoint types.Outpoint) (*types.UTXO, error) {
	if utxo, exists := ms.utxos[outpoint]; exists {
		return utxo, nil
	}
	return nil, fmt.Errorf("UTXO not found")
}

func (ms *MockStorage) DeleteUTXO(outpoint types.Outpoint) error {
	delete(ms.utxos, outpoint)
	return nil
}

func (ms *MockStorage) GetUTXOsByAddress(address string) ([]*types.UTXO, error) {
	// Simple implementation - not needed for consensus tests
	return nil, nil
}

func (ms *MockStorage) StoreTransaction(tx *types.Transaction) error {
	ms.transactions[tx.Hash()] = tx
	return nil
}

// Batch and administrative operations
func (ms *MockStorage) NewBatch() storage.Batch                   { return nil }
func (ms *MockStorage) SetChainHeight(height uint32) error        { return nil }
func (ms *MockStorage) GetStats() (*storage.DatabaseStats, error) { return nil, nil }
func (ms *MockStorage) GetSize() (int64, error)                   { return 0, nil }
func (ms *MockStorage) AddDynamicCheckpoint(height uint32, hash types.Hash) error { return nil }
func (ms *MockStorage) SetChainState(height uint32, hash types.Hash) error        { return nil }
func (ms *MockStorage) GetChainState() (uint32, types.Hash, error)                { return 0, types.Hash{}, nil }
func (ms *MockStorage) StoreStakeModifier(hash types.Hash, modifier uint64) error {
	ms.stakeModifiers[hash] = modifier
	return nil
}
func (ms *MockStorage) GetStakeModifier(hash types.Hash) (uint64, error) {
	if modifier, exists := ms.stakeModifiers[hash]; exists {
		return modifier, nil
	}
	return 0, fmt.Errorf("stake modifier not found for block %s", hash.String())
}
func (ms *MockStorage) StoreBlockWithHeight(block *types.Block, height uint32) error {
	ms.blocks[block.Hash()] = block
	ms.blocksByHeight[height] = block
	return nil
}

// Additional methods to fully implement storage.Storage interface
func (ms *MockStorage) GetTransactionData(hash types.Hash) (*storage.TransactionData, error) {
	tx, err := ms.GetTransaction(hash)
	if err != nil {
		return nil, err
	}
	return &storage.TransactionData{TxData: tx}, nil
}
func (ms *MockStorage) DeleteUTXOWithData(outpoint types.Outpoint, utxo *types.UTXO) error {
	delete(ms.utxos, outpoint)
	return nil
}
func (ms *MockStorage) IndexTransactionByAddress(addressBinary []byte, txHash types.Hash, height uint32, txIndex uint32, value int64, isInput bool, blockHash types.Hash) error {
	return nil
}
func (ms *MockStorage) GetTransactionsByAddress(addressBinary []byte) ([]storage.AddressTransaction, error) {
	return nil, nil
}
func (ms *MockStorage) DeleteAddressIndex(addressBinary []byte, txHash types.Hash) error {
	return nil
}
func (ms *MockStorage) MarkBlockInvalid(hash types.Hash) error     { return nil }
func (ms *MockStorage) RemoveBlockInvalid(hash types.Hash) error   { return nil }
func (ms *MockStorage) IsBlockInvalid(hash types.Hash) (bool, error) { return false, nil }
func (ms *MockStorage) GetInvalidBlocks() ([]types.Hash, error)    { return nil, nil }
func (ms *MockStorage) RemoveDynamicCheckpoint(height uint32) error { return nil }
func (ms *MockStorage) GetDynamicCheckpoint(height uint32) (types.Hash, error) { return types.Hash{}, nil }
func (ms *MockStorage) GetAllDynamicCheckpoints() (map[uint32]types.Hash, error) { return nil, nil }
func (ms *MockStorage) HasStakeModifier(hash types.Hash) (bool, error) {
	_, exists := ms.stakeModifiers[hash]
	return exists, nil
}
func (ms *MockStorage) DeleteStakeModifier(hash types.Hash) error {
	delete(ms.stakeModifiers, hash)
	return nil
}

// PoS metadata storage for stake modifier checksum chaining
func (ms *MockStorage) StoreBlockPoSMetadata(blockHash types.Hash, checksum uint32, proofHash types.Hash) error {
	return nil // Mock implementation
}
func (ms *MockStorage) GetBlockPoSMetadata(blockHash types.Hash) (uint32, types.Hash, error) {
	return 0, types.ZeroHash, nil // Mock implementation returns zeros
}
func (ms *MockStorage) HasBlockPoSMetadata(blockHash types.Hash) (bool, error) {
	return false, nil
}
func (ms *MockStorage) GetBlockParentHash(hash types.Hash) (types.Hash, error) {
	if block, exists := ms.blocks[hash]; exists {
		return block.Header.PrevBlockHash, nil
	}
	return types.Hash{}, fmt.Errorf("block not found: %s", hash.String())
}
func (ms *MockStorage) ValidateUTXOSpend(outpoint types.Outpoint) (bool, types.Hash, error) {
	return false, types.Hash{}, nil
}
func (ms *MockStorage) GetMoneySupply(height uint32) (int64, error)      { return 0, nil }
func (ms *MockStorage) StoreMoneySupply(height uint32, supply int64) error { return nil }
func (ms *MockStorage) IterateHashToHeight(fn func(hash types.Hash, height uint32) bool) error {
	return nil
}
func (ms *MockStorage) CleanOrphanedBlocks(maxValidHeight uint32) (int, error) {
	return 0, nil
}
func (ms *MockStorage) UnspendUTXOsBySpendingTx(txHashes map[types.Hash]struct{}) (int, error) {
	return 0, nil
}
func (ms *MockStorage) FindAndMarkSpendersForOutpoints(outpoints map[types.Outpoint]struct{}) (map[types.Outpoint]storage.SpenderInfo, error) {
	return nil, nil
}

// MockBlockchain implements BlockchainInterface for consensus integration tests
type MockBlockchain struct {
	storage        *MockStorage
	blocks         map[types.Hash]*types.Block
	blocksByHeight map[uint32]*types.Block
	stakeModifiers map[types.Hash]uint64
	ibd            bool
}

// NewMockBlockchain creates a new mock blockchain for testing
func NewMockBlockchain(storage *MockStorage) *MockBlockchain {
	return &MockBlockchain{
		storage:        storage,
		blocks:         make(map[types.Hash]*types.Block),
		blocksByHeight: make(map[uint32]*types.Block),
		stakeModifiers: make(map[types.Hash]uint64),
		ibd:            false,
	}
}

// GetBlockHeight returns the height of a block by hash
func (mb *MockBlockchain) GetBlockHeight(hash types.Hash) (uint32, error) {
	for height, block := range mb.blocksByHeight {
		if block.Hash() == hash {
			return height, nil
		}
	}
	// Fallback to storage
	if mb.storage != nil {
		return mb.storage.GetBlockHeight(hash)
	}
	return 0, fmt.Errorf("block not found: %s", hash.String())
}

// GetBlock returns a block by hash
func (mb *MockBlockchain) GetBlock(hash types.Hash) (*types.Block, error) {
	if block, exists := mb.blocks[hash]; exists {
		return block, nil
	}
	// Fallback to storage
	if mb.storage != nil {
		return mb.storage.GetBlock(hash)
	}
	return nil, fmt.Errorf("block not found: %s", hash.String())
}

// GetBlockByHeight returns a block by height
func (mb *MockBlockchain) GetBlockByHeight(height uint32) (*types.Block, error) {
	if block, exists := mb.blocksByHeight[height]; exists {
		return block, nil
	}
	// Fallback to storage
	if mb.storage != nil {
		return mb.storage.GetBlockByHeight(height)
	}
	return nil, fmt.Errorf("block not found at height %d", height)
}

// GetUTXO returns a UTXO by outpoint
func (mb *MockBlockchain) GetUTXO(outpoint types.Outpoint) (*types.UTXO, error) {
	if mb.storage != nil {
		return mb.storage.GetUTXO(outpoint)
	}
	return nil, fmt.Errorf("UTXO not found")
}

// GetStakeModifier returns the stake modifier for a block
func (mb *MockBlockchain) GetStakeModifier(blockHash types.Hash) (uint64, error) {
	if modifier, exists := mb.stakeModifiers[blockHash]; exists {
		return modifier, nil
	}
	// Fallback to storage
	if mb.storage != nil {
		return mb.storage.GetStakeModifier(blockHash)
	}
	return 0, fmt.Errorf("stake modifier not found for block %s", blockHash.String())
}

// IsInitialBlockDownload returns whether we're in IBD mode
func (mb *MockBlockchain) IsInitialBlockDownload() bool {
	return mb.ibd
}

// GetBestHeight returns the current best height
func (mb *MockBlockchain) GetBestHeight() (uint32, error) {
	var best uint32
	for h := range mb.blocksByHeight {
		if h > best {
			best = h
		}
	}
	return best, nil
}

// GetCheckpointManager returns nil (no checkpoint manager in mock)
func (mb *MockBlockchain) GetCheckpointManager() types.CheckpointManager {
	return nil
}

// GetTransaction returns a transaction by hash
func (mb *MockBlockchain) GetTransaction(hash types.Hash) (*types.Transaction, error) {
	if mb.storage != nil {
		return mb.storage.GetTransaction(hash)
	}
	return nil, fmt.Errorf("transaction not found: %s", hash.String())
}

// GetTransactionBlock returns the block containing a transaction
func (mb *MockBlockchain) GetTransactionBlock(hash types.Hash) (*types.Block, error) {
	if mb.storage != nil {
		return mb.storage.GetBlockContainingTx(hash)
	}
	return nil, fmt.Errorf("transaction block not found: %s", hash.String())
}

// AddBlock adds a block to the mock blockchain at a specific height
func (mb *MockBlockchain) AddBlock(block *types.Block, height uint32) {
	hash := block.Hash()
	mb.blocks[hash] = block
	mb.blocksByHeight[height] = block
	if mb.storage != nil {
		mb.storage.blocks[hash] = block
		mb.storage.blocksByHeight[height] = block
	}
}

// SetStakeModifier sets the stake modifier for a block
func (mb *MockBlockchain) SetStakeModifier(blockHash types.Hash, modifier uint64) {
	mb.stakeModifiers[blockHash] = modifier
	if mb.storage != nil {
		mb.storage.stakeModifiers[blockHash] = modifier
	}
}

// GetBlockWithPoSMetadata returns a block with PoS metadata populated
func (mb *MockBlockchain) GetBlockWithPoSMetadata(hash types.Hash) (*types.Block, error) {
	// For mock, just return the block without additional metadata
	return mb.GetBlock(hash)
}

// ProcessBlock validates and adds a block to the chain (mock no-op)
func (mb *MockBlockchain) ProcessBlock(block *types.Block) error {
	return nil
}
