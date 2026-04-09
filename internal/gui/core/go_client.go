package core

import (
	"context"
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/twins-dev/twins-core/internal/blockchain"
	"github.com/twins-dev/twins-core/internal/daemon"
	"github.com/twins-dev/twins-core/internal/masternode"
	"github.com/twins-dev/twins-core/internal/spork"
	"github.com/twins-dev/twins-core/internal/storage"
	"github.com/twins-dev/twins-core/internal/storage/binary"
	"github.com/twins-dev/twins-core/internal/wallet"
	"github.com/twins-dev/twins-core/pkg/crypto"
	"github.com/twins-dev/twins-core/pkg/types"
)

// satoshisPerTWINS is the conversion factor between satoshis and TWINS.
const satoshisPerTWINS = 100_000_000.0

// SyncerInterface defines the methods needed from the P2P syncer
// to provide sync status information without circular imports.
type SyncerInterface interface {
	// IsSyncing returns whether we're currently syncing
	IsSyncing() bool
	// IsSynced returns whether the node is synced with the network
	IsSynced() bool
	// GetSyncProgress returns current height, target height, and sync peer address
	GetSyncProgress() (current, target uint32, peer string)
	// GetNetworkHeight returns the best known network height from peer consensus
	GetNetworkHeight() uint32
}

// P2PServerInterface defines the methods needed from the P2P server
// to provide network status information without circular imports.
type P2PServerInterface interface {
	// GetPeerCount returns the current peer count
	GetPeerCount() int32
	// IsStarted returns whether the server is started
	IsStarted() bool
}

// ConsensusInterface defines the methods needed from the consensus engine
// to provide staking status information without circular imports.
type ConsensusInterface interface {
	// IsStaking returns whether the consensus engine is actively staking
	IsStaking() bool
}

// GoCoreClient implements CoreClient with direct storage access.
// This is the production implementation that reads blockchain data
// directly from the Pebble storage layer.
type GoCoreClient struct {
	storage storage.Storage

	// Full daemon components (optional, for full functionality)
	wallet         *wallet.Wallet
	masternode     *masternode.Manager
	spork          *spork.Manager
	paymentTracker *masternode.PaymentTracker // Payment stats for LastPaid (optional)
	syncer         SyncerInterface            // P2P syncer for sync status (optional)
	p2pServer      P2PServerInterface         // P2P server for network info (optional)
	consensus      ConsensusInterface         // Consensus engine for staking info (optional)

	// Staking configuration
	stakingEnabled bool // Whether staking is enabled in settings

	// State
	running bool
	mu      sync.RWMutex

	// Event system
	events     chan CoreEvent
	eventsDone chan struct{}

	// Context for cancellation
	ctx    context.Context
	cancel context.CancelFunc
}

// NewGoCoreClient creates a new GoCoreClient with the given storage.
// This is the basic constructor for backward compatibility.
func NewGoCoreClient(store storage.Storage) *GoCoreClient {
	return &GoCoreClient{
		storage:    store,
		events:     make(chan CoreEvent, 100),
		eventsDone: make(chan struct{}),
	}
}

// NewGoCoreClientWithComponents creates a new GoCoreClient with full daemon components.
// This constructor provides access to masternode and spork functionality.
// Note: Wallet is not yet implemented (requires legacy.CMasterKey types).
func NewGoCoreClientWithComponents(components *daemon.CoreComponents) *GoCoreClient {
	client := &GoCoreClient{
		storage:    components.Storage,
		masternode: components.Masternode,
		spork:      components.Spork,
		events:     make(chan CoreEvent, 100),
		eventsDone: make(chan struct{}),
	}
	return client
}

// SetWallet sets the wallet instance for transaction operations.
func (c *GoCoreClient) SetWallet(w *wallet.Wallet) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wallet = w
}

// SetSyncer sets the P2P syncer for sync status information.
func (c *GoCoreClient) SetSyncer(s SyncerInterface) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.syncer = s
}

// SetP2PServer sets the P2P server for network status information.
func (c *GoCoreClient) SetP2PServer(p P2PServerInterface) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.p2pServer = p
}

// SetPaymentTracker sets the payment tracker for masternode LastPaid lookups.
func (c *GoCoreClient) SetPaymentTracker(tracker *masternode.PaymentTracker) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.paymentTracker = tracker
}

// SetConsensus sets the consensus engine for staking status information.
func (c *GoCoreClient) SetConsensus(cons ConsensusInterface) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.consensus = cons
}

// SetStakingEnabled sets whether staking is enabled in GUI settings.
func (c *GoCoreClient) SetStakingEnabled(enabled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stakingEnabled = enabled
}

// Start initializes the core client.
func (c *GoCoreClient) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.running {
		return fmt.Errorf("core client already running")
	}

	c.ctx, c.cancel = context.WithCancel(ctx)
	c.running = true

	return nil
}

// Stop gracefully shuts down the core client.
func (c *GoCoreClient) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.running {
		return nil
	}

	c.cancel()
	close(c.events)
	c.running = false

	return nil
}

// IsRunning returns true if the core is running.
func (c *GoCoreClient) IsRunning() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.running
}

// Events returns a channel for core events.
func (c *GoCoreClient) Events() <-chan CoreEvent {
	return c.events
}

// ==========================================
// Explorer Operations (Real Implementation)
// ==========================================

// GetLatestBlocks returns the most recent blocks.
func (c *GoCoreClient) GetLatestBlocks(limit, offset int) ([]BlockSummary, error) {
	if limit <= 0 {
		limit = 25
	}
	if limit > 100 {
		limit = 100
	}

	height, err := c.storage.GetChainHeight()
	if err != nil {
		return nil, fmt.Errorf("failed to get chain height: %w", err)
	}

	blocks := make([]BlockSummary, 0, limit)
	startHeight := int64(height) - int64(offset)

	for i := 0; i < limit && startHeight-int64(i) >= 0; i++ {
		h := uint32(startHeight - int64(i))
		block, err := c.storage.GetBlockByHeight(h)
		if err != nil {
			continue // Skip missing blocks
		}

		summary := c.blockToSummary(block, h)
		blocks = append(blocks, summary)
	}

	return blocks, nil
}

// GetExplorerBlock returns detailed block information.
func (c *GoCoreClient) GetExplorerBlock(query string) (BlockDetail, error) {
	var block *types.Block
	var height uint32
	var err error

	// Try parsing as height first
	if h, parseErr := strconv.ParseUint(query, 10, 32); parseErr == nil {
		height = uint32(h)
		block, err = c.storage.GetBlockByHeight(height)
	} else {
		// Parse as hash (display format is reversed)
		hashBytes, decodeErr := hex.DecodeString(query)
		if decodeErr != nil || len(hashBytes) != 32 {
			return BlockDetail{}, fmt.Errorf("invalid block hash or height: %s", query)
		}
		// Reverse bytes from display format to internal format
		var hash types.Hash
		for i := 0; i < 32; i++ {
			hash[i] = hashBytes[31-i]
		}
		block, err = c.storage.GetBlock(hash)
		if err == nil {
			height, err = c.storage.GetBlockHeight(hash)
		}
	}

	if err != nil {
		return BlockDetail{}, fmt.Errorf("block not found: %w", err)
	}

	return c.blockToDetail(block, height)
}

// GetExplorerTransaction returns detailed transaction information.
func (c *GoCoreClient) GetExplorerTransaction(txid string) (ExplorerTransaction, error) {
	hashBytes, err := hex.DecodeString(txid)
	if err != nil || len(hashBytes) != 32 {
		return ExplorerTransaction{}, fmt.Errorf("invalid transaction id: %s", txid)
	}

	// Reverse bytes from display format to internal format
	var hash types.Hash
	for i := 0; i < 32; i++ {
		hash[i] = hashBytes[31-i]
	}

	txData, err := c.storage.GetTransactionData(hash)
	if err != nil {
		return ExplorerTransaction{}, fmt.Errorf("transaction not found: %w", err)
	}

	return c.txToExplorerTx(txData)
}

// GetAddressInfo returns basic information about an address (balance, UTXOs).
// Transactions are loaded separately via GetAddressTransactions for progressive loading.
func (c *GoCoreClient) GetAddressInfo(address string, _ int) (AddressInfo, error) {
	// Decode address to binary format (netID + hash160 = 21 bytes)
	addr, err := crypto.DecodeAddress(address)
	if err != nil {
		return AddressInfo{}, fmt.Errorf("invalid address: %w", err)
	}
	// Build 21-byte address binary: [netID:1][hash160:20]
	addressBinary := make([]byte, 21)
	addressBinary[0] = addr.NetID()
	copy(addressBinary[1:], addr.Hash160())

	// Get UTXOs for balance calculation
	utxos, err := c.storage.GetUTXOsByAddress(address)
	if err != nil && !storage.IsNotFoundError(err) {
		return AddressInfo{}, fmt.Errorf("failed to get UTXOs: %w", err)
	}

	var balance, totalReceived int64
	addressUTXOs := make([]AddressUTXO, 0)

	chainHeight, _ := c.storage.GetChainHeight()

	for _, utxo := range utxos {
		balance += utxo.Output.Value
		totalReceived += utxo.Output.Value

		confirmations := int(chainHeight) - int(utxo.Height) + 1
		if confirmations < 0 {
			confirmations = 0
		}

		addressUTXOs = append(addressUTXOs, AddressUTXO{
			TxID:          utxo.Outpoint.Hash.String(),
			Vout:          utxo.Outpoint.Index,
			Amount:        float64(utxo.Output.Value) / satoshisPerTWINS,
			Confirmations: confirmations,
			BlockHeight:   int64(utxo.Height),
		})
	}

	// Get transaction count only (for display)
	addrTxs, _ := c.storage.GetTransactionsByAddress(addressBinary)
	txCount := len(addrTxs)

	return AddressInfo{
		Address:            address,
		Balance:            float64(balance) / satoshisPerTWINS,
		TotalReceived:      float64(totalReceived) / satoshisPerTWINS,
		TotalSent:          0, // Calculated when transactions are loaded
		TxCount:            txCount,
		UnconfirmedBalance: 0,
		Transactions:       nil, // Loaded separately
		UTXOs:              addressUTXOs,
	}, nil
}

// AddressTxPage represents a page of address transactions
type AddressTxPage struct {
	Transactions []AddressTx `json:"transactions"`
	Total        int         `json:"total"`
	HasMore      bool        `json:"has_more"`
}

// GetAddressTransactions returns a page of transactions for an address.
// limit: number of transactions per page (1-10000)
// offset: starting position (0-based, from most recent)
func (c *GoCoreClient) GetAddressTransactions(address string, limit, offset int) (AddressTxPage, error) {
	// Input validation to prevent DoS and memory issues
	if limit <= 0 {
		limit = 50
	}
	if limit > 10000 {
		return AddressTxPage{}, fmt.Errorf("invalid limit: %d (must be 1-10000)", limit)
	}
	if offset < 0 {
		return AddressTxPage{}, fmt.Errorf("invalid offset: %d (must be >= 0)", offset)
	}

	// Decode address
	addr, err := crypto.DecodeAddress(address)
	if err != nil {
		return AddressTxPage{}, fmt.Errorf("invalid address: %w", err)
	}
	addressBinary := make([]byte, 21)
	addressBinary[0] = addr.NetID()
	copy(addressBinary[1:], addr.Hash160())

	// Get all transaction references
	addrTxs, err := c.storage.GetTransactionsByAddress(addressBinary)
	if err != nil && !storage.IsNotFoundError(err) {
		return AddressTxPage{}, fmt.Errorf("failed to get address transactions: %w", err)
	}

	total := len(addrTxs)
	transactions := make([]AddressTx, 0, limit)
	chainHeight, _ := c.storage.GetChainHeight()

	// Process transactions from most recent (reverse order)
	// Start at (total - 1 - offset) and go backwards
	startIdx := total - 1 - offset
	endIdx := startIdx - limit
	if endIdx < -1 {
		endIdx = -1
	}

	var failedCount int
	for i := startIdx; i > endIdx && i >= 0; i-- {
		addrTx := addrTxs[i]

		txData, err := c.storage.GetTransactionData(addrTx.TxHash)
		if err != nil {
			failedCount++
			log.Warnf("Failed to load transaction %s for address %s: %v",
				addrTx.TxHash.String(), address, err)
			continue
		}

		// Calculate net amount for this address
		var netAmount int64

		// Add outputs to this address
		for _, output := range txData.TxData.Outputs {
			outAddr := c.extractAddressFromScript(output.ScriptPubKey)
			if outAddr == address {
				netAmount += output.Value
			}
		}

		// Subtract inputs from this address
		for _, input := range txData.TxData.Inputs {
			// Skip coinbase inputs
			if input.PreviousOutput.Hash.IsZero() {
				continue
			}
			// Get the previous transaction to find the input value
			prevTxData, err := c.storage.GetTransactionData(input.PreviousOutput.Hash)
			if err != nil {
				continue
			}
			if int(input.PreviousOutput.Index) < len(prevTxData.TxData.Outputs) {
				prevOutput := prevTxData.TxData.Outputs[input.PreviousOutput.Index]
				prevAddr := c.extractAddressFromScript(prevOutput.ScriptPubKey)
				if prevAddr == address {
					netAmount -= prevOutput.Value
				}
			}
		}

		confirmations := int(chainHeight) - int(txData.Height) + 1
		if confirmations < 0 {
			confirmations = 0
		}

		// Get block for timestamp
		block, _ := c.storage.GetBlockByHeight(txData.Height)
		var txTime time.Time
		if block != nil {
			txTime = time.Unix(int64(block.Header.Timestamp), 0)
		}

		transactions = append(transactions, AddressTx{
			TxID:          addrTx.TxHash.String(),
			BlockHeight:   int64(txData.Height),
			Time:          txTime,
			Amount:        float64(netAmount) / satoshisPerTWINS,
			Confirmations: confirmations,
		})
	}

	if failedCount > 0 {
		log.Warnf("Failed to load %d/%d transactions for address %s",
			failedCount, total, address)
	}

	hasMore := offset+len(transactions) < total

	return AddressTxPage{
		Transactions: transactions,
		Total:        total,
		HasMore:      hasMore,
	}, nil
}

// SearchExplorer searches for a block, transaction, or address.
func (c *GoCoreClient) SearchExplorer(query string) (SearchResult, error) {
	result := SearchResult{Query: query}

	// Try block height
	if height, err := strconv.ParseUint(query, 10, 32); err == nil {
		block, err := c.GetExplorerBlock(fmt.Sprintf("%d", height))
		if err == nil {
			result.Type = "block"
			result.Block = &block
			return result, nil
		}
	}

	// Try block hash (64 hex chars)
	if len(query) == 64 && isHex(query) {
		block, err := c.GetExplorerBlock(query)
		if err == nil {
			result.Type = "block"
			result.Block = &block
			return result, nil
		}

		// Also try as transaction hash
		tx, err := c.GetExplorerTransaction(query)
		if err == nil {
			result.Type = "transaction"
			result.Transaction = &tx
			return result, nil
		}
	}

	// Try address (D prefix for mainnet)
	if len(query) >= 26 && len(query) <= 35 {
		if _, err := crypto.DecodeAddress(query); err == nil {
			addr, err := c.GetAddressInfo(query, 25)
			if err == nil {
				result.Type = "address"
				result.Address = &addr
				return result, nil
			}
		}
	}

	result.Type = "not_found"
	result.Error = fmt.Sprintf("no results found for: %s", query)
	return result, nil
}

// ==========================================
// Helper Functions
// ==========================================

func (c *GoCoreClient) blockToSummary(block *types.Block, height uint32) BlockSummary {
	isPoS := len(block.Transactions) > 1 && block.Transactions[1].IsCoinStake()

	var reward float64
	if len(block.Transactions) > 0 {
		for _, out := range block.Transactions[0].Outputs {
			reward += float64(out.Value) / satoshisPerTWINS
		}
	}

	return BlockSummary{
		Height:  int64(height),
		Hash:    block.Header.Hash().String(),
		Time:    time.Unix(int64(block.Header.Timestamp), 0),
		TxCount: len(block.Transactions),
		Size:    block.SerializeSize(),
		IsPoS:   isPoS,
		Reward:  reward,
	}
}

func (c *GoCoreClient) blockToDetail(block *types.Block, height uint32) (BlockDetail, error) {
	chainHeight, _ := c.storage.GetChainHeight()
	confirmations := int(chainHeight) - int(height) + 1

	isPoS := len(block.Transactions) > 1 && block.Transactions[1].IsCoinStake()

	var stakeReward, masternodeReward, totalReward float64
	var stakerAddr, masternodeAddr string

	if isPoS && len(block.Transactions) > 1 {
		coinstake := block.Transactions[1]

		// Calculate total inputs by looking up the original transactions
		// (UTXOs are already spent, so we need to fetch from the source tx)
		var totalInputs int64
		for _, in := range coinstake.Inputs {
			// Get the transaction that contains this output
			txData, err := c.storage.GetTransactionData(in.PreviousOutput.Hash)
			if err == nil && txData != nil && int(in.PreviousOutput.Index) < len(txData.TxData.Outputs) {
				totalInputs += txData.TxData.Outputs[in.PreviousOutput.Index].Value
			}
		}

		// Calculate total outputs
		var totalOutputs int64
		for _, out := range coinstake.Outputs {
			totalOutputs += out.Value
		}

		// Total reward = outputs - inputs (the newly created coins)
		totalReward = float64(totalOutputs-totalInputs) / satoshisPerTWINS

		// Parse outputs: typically output[0] is empty, output[1] is staker, output[2] is masternode
		if len(coinstake.Outputs) > 1 {
			// Staker gets stake back + stake reward (output 1)
			stakerAddr = c.extractAddressFromScript(coinstake.Outputs[1].ScriptPubKey)
			// Stake reward = staker output - original stake input
			stakeReward = float64(coinstake.Outputs[1].Value-totalInputs) / satoshisPerTWINS
		}
		if len(coinstake.Outputs) > 2 {
			// Masternode reward (output 2)
			masternodeAddr = c.extractAddressFromScript(coinstake.Outputs[2].ScriptPubKey)
			masternodeReward = float64(coinstake.Outputs[2].Value) / satoshisPerTWINS
		}
	}

	txids := make([]string, len(block.Transactions))
	for i, tx := range block.Transactions {
		txids[i] = tx.Hash().String()
	}

	// Get previous/next block hashes
	var prevHash, nextHash string
	prevHash = block.Header.PrevBlockHash.String()

	if height < chainHeight {
		nextBlock, err := c.storage.GetBlockByHeight(height + 1)
		if err == nil {
			nextHash = nextBlock.Header.Hash().String()
		}
	}

	return BlockDetail{
		Block: Block{
			Hash:              block.Header.Hash().String(),
			Height:            int64(height),
			Confirmations:     confirmations,
			Size:              block.SerializeSize(),
			Time:              time.Unix(int64(block.Header.Timestamp), 0),
			PreviousBlockHash: prevHash,
			NextBlockHash:     nextHash,
			Difficulty:        float64(block.Header.Bits), // Simplified
			Bits:              fmt.Sprintf("%08x", block.Header.Bits),
			Nonce:             block.Header.Nonce,
			MerkleRoot:        block.Header.MerkleRoot.String(),
		},
		TxIDs:             txids,
		IsPoS:             isPoS,
		StakeReward:       stakeReward,
		MasternodeReward:  masternodeReward,
		StakerAddress:     stakerAddr,
		MasternodeAddress: masternodeAddr,
		TotalReward:       totalReward,
	}, nil
}

func (c *GoCoreClient) txToExplorerTx(txData *storage.TransactionData) (ExplorerTransaction, error) {
	tx := txData.TxData
	chainHeight, _ := c.storage.GetChainHeight()
	confirmations := int(chainHeight) - int(txData.Height) + 1

	// Get block for timestamp
	block, _ := c.storage.GetBlockByHeight(txData.Height)
	var txTime time.Time
	if block != nil {
		txTime = time.Unix(int64(block.Header.Timestamp), 0)
	}

	isCoinbase := tx.IsCoinbase()
	inputs := make([]TxInput, len(tx.Inputs))
	var totalInput int64

	for i, in := range tx.Inputs {
		// For coinbase, the first input is special (no previous output)
		isCoinbaseInput := isCoinbase && i == 0
		inputs[i] = TxInput{
			TxID:       in.PreviousOutput.Hash.String(),
			Vout:       in.PreviousOutput.Index,
			IsCoinbase: isCoinbaseInput,
		}

		if !isCoinbaseInput {
			// Get previous output from the source transaction
			// (UTXOs may already be spent, so we need to fetch from the source tx)
			prevTxData, err := c.storage.GetTransactionData(in.PreviousOutput.Hash)
			if err == nil && prevTxData != nil && int(in.PreviousOutput.Index) < len(prevTxData.TxData.Outputs) {
				prevOutput := prevTxData.TxData.Outputs[in.PreviousOutput.Index]
				inputs[i].Amount = float64(prevOutput.Value) / satoshisPerTWINS
				inputs[i].Address = c.extractAddressFromScript(prevOutput.ScriptPubKey)
				totalInput += prevOutput.Value
			}
		}
	}

	outputs := make([]TxOutput, len(tx.Outputs))
	var totalOutput int64

	for i, out := range tx.Outputs {
		outputs[i] = TxOutput{
			Index:      uint32(i),
			Address:    c.extractAddressFromScript(out.ScriptPubKey),
			Amount:     float64(out.Value) / satoshisPerTWINS,
			ScriptType: c.getScriptType(out.ScriptPubKey),
			IsSpent:    false, // Would need spent check
		}
		totalOutput += out.Value
	}

	fee := totalInput - totalOutput
	if fee < 0 {
		fee = 0 // Coinbase/coinstake
	}

	return ExplorerTransaction{
		TxID:          tx.Hash().String(),
		BlockHash:     txData.BlockHash.String(),
		BlockHeight:   int64(txData.Height),
		Confirmations: confirmations,
		Time:          txTime,
		Size:          tx.SerializeSize(),
		Fee:           float64(fee) / satoshisPerTWINS,
		IsCoinbase:    isCoinbase,
		IsCoinStake:   tx.IsCoinStake(),
		Inputs:        inputs,
		Outputs:       outputs,
		TotalInput:    float64(totalInput) / satoshisPerTWINS,
		TotalOutput:   float64(totalOutput) / satoshisPerTWINS,
	}, nil
}

func (c *GoCoreClient) extractAddressFromScript(scriptBytes []byte) string {
	scriptType, scriptHash := binary.AnalyzeScript(scriptBytes)

	var netID byte
	switch scriptType {
	case binary.ScriptTypeP2PKH, binary.ScriptTypeP2PK:
		netID = crypto.MainNetPubKeyHashAddrID
	case binary.ScriptTypeP2SH:
		netID = crypto.MainNetScriptHashAddrID
	default:
		return ""
	}

	addr, err := crypto.NewAddressFromHash(scriptHash[:], netID)
	if err != nil {
		return ""
	}
	return addr.String()
}

func (c *GoCoreClient) getScriptType(script []byte) string {
	if len(script) == 25 && script[0] == 0x76 && script[1] == 0xa9 {
		return "pubkeyhash"
	}
	if len(script) == 23 && script[0] == 0xa9 {
		return "scripthash"
	}
	if len(script) > 0 && script[0] == 0x6a {
		return "nulldata"
	}
	return "nonstandard"
}

func isHex(s string) bool {
	matched, _ := regexp.MatchString("^[0-9a-fA-F]+$", s)
	return matched
}

// ==========================================
// Stub implementations for other methods
// These return errors until fully implemented
// ==========================================

func (c *GoCoreClient) GetBalance() (Balance, error) {
	c.mu.RLock()
	w := c.wallet
	c.mu.RUnlock()

	if w == nil {
		return Balance{}, fmt.Errorf("wallet not initialized")
	}

	// Get balance from wallet (values are in satoshis)
	walletBalance := w.GetBalance()
	if walletBalance == nil {
		return Balance{}, fmt.Errorf("failed to get wallet balance")
	}

	// Convert satoshis to TWINS (1 TWINS = 100,000,000 satoshis)


	confirmed := float64(walletBalance.Confirmed) / satoshisPerTWINS
	unconfirmed := float64(walletBalance.Unconfirmed) / satoshisPerTWINS
	immature := float64(walletBalance.Immature) / satoshisPerTWINS

	// Calculate totals
	// Available = Confirmed (spendable balance)
	// Pending = Unconfirmed
	// Total = Available + Pending + Immature
	total := confirmed + unconfirmed + immature

	// Calculate locked balance from masternode UTXOs using atomic wallet method
	// This ensures consistent results by holding the wallet lock during iteration
	lockedSatoshis, _ := w.GetLockedCollateralInfo()
	locked := float64(lockedSatoshis) / satoshisPerTWINS

	// Available = Confirmed balance minus locked
	// This is what the user can actually spend
	available := confirmed - locked

	// Spendable = Available (after subtracting locked)
	spendable := available

	return Balance{
		Total:     total,
		Available: available,
		Spendable: spendable,
		Pending:   unconfirmed,
		Immature:  immature,
		Locked:    locked,
	}, nil
}

func (c *GoCoreClient) GetNewAddress(label string) (string, error) {
	// Wallet not yet implemented - requires legacy.CMasterKey types
	return "", fmt.Errorf("wallet not implemented")
}

func (c *GoCoreClient) SendToAddress(address string, amount float64, comment string) (string, error) {
	c.mu.RLock()
	w := c.wallet
	c.mu.RUnlock()

	if w == nil {
		return "", fmt.Errorf("wallet not initialized")
	}

	// Convert amount from TWINS to satoshis (1 TWINS = 100,000,000 satoshis)
	// Use math.Round to avoid floating-point precision errors

	amountSatoshis := int64(math.Round(amount * satoshisPerTWINS))

	if amountSatoshis <= 0 {
		return "", fmt.Errorf("invalid amount: must be positive")
	}

	// Call wallet's SendToAddress
	// subtractFee=false means fee is added on top of the amount
	txid, err := w.SendToAddress(address, amountSatoshis, comment, false)
	if err != nil {
		return "", fmt.Errorf("send failed: %w", err)
	}

	return txid, nil
}

// SendOptions contains options for advanced transaction sending
type SendOptions struct {
	// SelectedUTXOs are the specific UTXOs to use (coin control)
	// Format: ["txid:vout", ...]
	SelectedUTXOs []string `json:"selectedUtxos"`

	// ChangeAddress overrides the default change address
	ChangeAddress string `json:"changeAddress"`

	// SplitCount splits each output into multiple UTXOs
	SplitCount int `json:"splitCount"`

	// FeeRate is the user-selected fee rate in TWINS/kB
	// If 0 or omitted, uses wallet default fee rate
	FeeRate float64 `json:"feeRate"`
}

// SendToAddressWithOptions sends TWINS with advanced options (coin control, custom change, split)
func (c *GoCoreClient) SendToAddressWithOptions(address string, amount float64, comment string, opts *SendOptions) (string, error) {
	c.mu.RLock()
	w := c.wallet
	c.mu.RUnlock()

	if w == nil {
		return "", fmt.Errorf("wallet not initialized")
	}

	// Convert amount from TWINS to satoshis
	// Use math.Round to avoid floating-point precision errors

	amountSatoshis := int64(math.Round(amount * satoshisPerTWINS))

	if amountSatoshis <= 0 {
		return "", fmt.Errorf("invalid amount: must be positive")
	}

	// Build wallet options
	var walletOpts *wallet.SendOptions
	if opts != nil {
		walletOpts = &wallet.SendOptions{
			ChangeAddress: opts.ChangeAddress,
			SplitCount:    opts.SplitCount,
		}

		// Convert fee rate from TWINS/kB to satoshis/kB
		if opts.FeeRate > 0 {
			walletOpts.FeeRate = int64(math.Round(opts.FeeRate * satoshisPerTWINS))
		}

		// Parse selected UTXOs from strings "txid:vout"
		if len(opts.SelectedUTXOs) > 0 {
			outpoints, err := parseUTXOStrings(opts.SelectedUTXOs)
			if err != nil {
				return "", err
			}
			walletOpts.SelectedUTXOs = outpoints
		}
	}

	// Create recipients map
	recipients := map[string]int64{
		address: amountSatoshis,
	}

	// Call wallet's SendManyWithOptions
	txid, err := w.SendManyWithOptions(recipients, comment, walletOpts)
	if err != nil {
		return "", fmt.Errorf("send failed: %w", err)
	}

	return txid, nil
}

// splitUTXOString splits a UTXO string by the last colon
func splitUTXOString(s string) []string {
	lastColon := -1
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			lastColon = i
			break
		}
	}
	if lastColon == -1 {
		return []string{s}
	}
	return []string{s[:lastColon], s[lastColon+1:]}
}

// parseUTXOStrings parses a slice of UTXO strings in "txid:vout" format
// into a slice of types.Outpoint. Returns an error if any UTXO is malformed.
func parseUTXOStrings(utxoStrings []string) ([]types.Outpoint, error) {
	if len(utxoStrings) == 0 {
		return nil, nil
	}

	outpoints := make([]types.Outpoint, 0, len(utxoStrings))
	for _, utxoStr := range utxoStrings {
		var txid string
		var vout uint32

		// Parse "txid:vout" format using splitUTXOString (handles txids with colons)
		parts := splitUTXOString(utxoStr)
		if len(parts) != 2 || parts[1] == "" {
			return nil, fmt.Errorf("invalid UTXO format: %s (expected txid:vout)", utxoStr)
		}

		txid = parts[0]
		var voutInt int
		_, err := fmt.Sscanf(parts[1], "%d", &voutInt)
		if err != nil {
			return nil, fmt.Errorf("invalid UTXO vout: %s", parts[1])
		}
		vout = uint32(voutInt)

		hash, err := types.NewHashFromString(txid)
		if err != nil {
			return nil, fmt.Errorf("invalid UTXO txid: %s", txid)
		}

		outpoints = append(outpoints, types.Outpoint{
			Hash:  hash,
			Index: vout,
		})
	}

	return outpoints, nil
}

// SendMany sends coins to multiple recipients in a single transaction.
// recipients: map of address → amount in TWINS
// Supports all options from SendOptions (coin control, custom change, UTXO split).
func (c *GoCoreClient) SendMany(recipients map[string]float64, comment string, opts *SendOptions) (string, error) {
	c.mu.RLock()
	w := c.wallet
	c.mu.RUnlock()

	if w == nil {
		return "", fmt.Errorf("wallet not set - call SetWallet first")
	}

	if len(recipients) == 0 {
		return "", fmt.Errorf("no recipients specified")
	}

	// Convert recipients to satoshis
	recipientsSatoshis := make(map[string]int64, len(recipients))
	for addr, amount := range recipients {
		if amount <= 0 {
			return "", fmt.Errorf("invalid amount for address %s: must be positive", addr)
		}
		// Convert TWINS to satoshis (1 TWINS = 100,000,000 satoshis)
		// Use math.Round to avoid floating-point precision errors
		amountSatoshis := int64(math.Round(amount * satoshisPerTWINS))
		recipientsSatoshis[addr] = amountSatoshis
	}

	// Build wallet options
	walletOpts := &wallet.SendOptions{}

	if opts != nil {
		walletOpts.ChangeAddress = opts.ChangeAddress
		walletOpts.SplitCount = opts.SplitCount

		// Convert fee rate from TWINS/kB to satoshis/kB
		if opts.FeeRate > 0 {
			walletOpts.FeeRate = int64(math.Round(opts.FeeRate * satoshisPerTWINS))
		}

		// Parse selected UTXOs using shared helper function
		if len(opts.SelectedUTXOs) > 0 {
			outpoints, err := parseUTXOStrings(opts.SelectedUTXOs)
			if err != nil {
				return "", err
			}
			walletOpts.SelectedUTXOs = outpoints
		}
	}

	// Call wallet's SendManyWithOptions
	txid, err := w.SendManyWithOptions(recipientsSatoshis, comment, walletOpts)
	if err != nil {
		return "", fmt.Errorf("send failed: %w", err)
	}

	return txid, nil
}

// FeeEstimateResult contains detailed fee estimation for GUI display
type FeeEstimateResult struct {
	Fee        float64 `json:"fee"`        // Estimated fee in TWINS
	InputCount int     `json:"inputCount"` // Number of inputs that would be used
	TxSize     int     `json:"txSize"`     // Estimated transaction size in bytes
}

// EstimateTransactionFee estimates the transaction fee based on recipients and options
// This method works even when the wallet is locked (no signing required)
// Parameters:
//   - recipients: map of address → amount in TWINS
//   - opts: optional SendOptions (coin control UTXOs, fee rate, split count)
//
// Returns FeeEstimateResult with fee (in TWINS), input count, and transaction size
func (c *GoCoreClient) EstimateTransactionFee(recipients map[string]float64, opts *SendOptions) (*FeeEstimateResult, error) {
	c.mu.RLock()
	w := c.wallet
	c.mu.RUnlock()

	if w == nil {
		return nil, fmt.Errorf("wallet not initialized")
	}

	if len(recipients) == 0 {
		return nil, fmt.Errorf("no recipients specified")
	}

	// Convert recipients to satoshis
	// Max safe amount before int64 overflow: 92,233,720,368 TWINS (int64_max / satoshisPerTWINS)
	// Use 21 billion as practical limit (well below overflow threshold)
	const maxAmountTWINS = 21_000_000_000.0
	recipientsSatoshis := make(map[string]int64, len(recipients))
	for addr, amount := range recipients {
		if amount <= 0 {
			return nil, fmt.Errorf("invalid amount for address %s: must be positive", addr)
		}
		if amount > maxAmountTWINS {
			return nil, fmt.Errorf("invalid amount for address %s: exceeds maximum (%g TWINS)", addr, maxAmountTWINS)
		}
		// Convert TWINS to satoshis (1 TWINS = 100,000,000 satoshis)
		amountSatoshis := int64(math.Round(amount * satoshisPerTWINS))
		recipientsSatoshis[addr] = amountSatoshis
	}

	// Parse options
	var selectedUTXOs []types.Outpoint
	var feeRate int64
	var splitCount int

	if opts != nil {
		// Convert fee rate from TWINS/kB to satoshis/kB
		if opts.FeeRate > 0 {
			feeRate = int64(math.Round(opts.FeeRate * satoshisPerTWINS))
		}

		splitCount = opts.SplitCount

		// Parse selected UTXOs
		if len(opts.SelectedUTXOs) > 0 {
			var err error
			selectedUTXOs, err = parseUTXOStrings(opts.SelectedUTXOs)
			if err != nil {
				return nil, err
			}
		}
	}

	// Call wallet's EstimateFee
	result, err := w.EstimateFee(recipientsSatoshis, selectedUTXOs, feeRate, splitCount)
	if err != nil {
		return nil, fmt.Errorf("fee estimation failed: %w", err)
	}

	// Convert fee from satoshis to TWINS
	feeInTWINS := float64(result.Fee) / satoshisPerTWINS

	return &FeeEstimateResult{
		Fee:        feeInTWINS,
		InputCount: result.InputCount,
		TxSize:     result.TxSize,
	}, nil
}

func (c *GoCoreClient) GetTransaction(txid string) (Transaction, error) {
	c.mu.RLock()
	w := c.wallet
	c.mu.RUnlock()

	if w == nil {
		return Transaction{}, fmt.Errorf("wallet not initialized")
	}

	hash, err := types.NewHashFromString(txid)
	if err != nil {
		return Transaction{}, fmt.Errorf("invalid transaction ID: %w", err)
	}

	wtx, err := w.GetTransaction(hash)
	if err != nil {
		return Transaction{}, fmt.Errorf("transaction not found: %w", err)
	}

	return c.convertWalletTransaction(wtx), nil
}

func (c *GoCoreClient) ListTransactions(count int, skip int) ([]Transaction, error) {
	c.mu.RLock()
	w := c.wallet
	c.mu.RUnlock()

	if w == nil {
		return nil, fmt.Errorf("wallet not initialized")
	}

	// Get transactions from wallet
	walletTxs, err := w.ListTransactions(count, skip)
	if err != nil {
		return nil, fmt.Errorf("failed to list transactions: %w", err)
	}

	// Convert wallet transactions to core transactions
	txs := make([]Transaction, 0, len(walletTxs))
	for _, wtx := range walletTxs {
		tx := c.convertWalletTransaction(wtx)
		txs = append(txs, tx)
	}

	return txs, nil
}

// convertWalletTransaction converts a wallet.WalletTransaction to core.Transaction
func (c *GoCoreClient) convertWalletTransaction(wtx *wallet.WalletTransaction) Transaction {
	// Convert satoshis to TWINS (1 TWINS = 100,000,000 satoshis)


	// Coinbase maturity constant (must match chainparams)
	// TWINS mainnet uses 60 blocks for coinbase maturity
	const coinbaseMaturity = 60

	// Map wallet TxCategory to core TransactionType
	txType := mapCategoryToType(wtx.Category)

	// Override type for autocombine consolidation transactions
	if txType == TxTypeSendToSelf && wtx.Comment == "autocombine" {
		txType = TxTypeConsolidation
	}

	// Calculate amount as float64
	amount := float64(wtx.Amount) / satoshisPerTWINS
	fee := float64(wtx.Fee) / satoshisPerTWINS

	// Calculate debit/credit based on amount sign
	var debit, credit float64
	if amount < 0 {
		debit = -amount
	} else {
		credit = amount
	}

	// Determine if coinbase or coinstake
	isCoinbase := wtx.Category == wallet.TxCategoryCoinBase || wtx.Category == wallet.TxCategoryGenerate
	isCoinstake := wtx.Category == wallet.TxCategoryCoinStake || wtx.Category == wallet.TxCategoryMasternode

	// Calculate blocks until maturity for coinbase/coinstake
	var maturesIn int
	if (isCoinbase || isCoinstake) && int(wtx.Confirmations) < coinbaseMaturity {
		maturesIn = coinbaseMaturity - int(wtx.Confirmations)
		if maturesIn < 0 {
			maturesIn = 0
		}
	}

	// Look up label dynamically from address book for current value
	// This ensures labels always reflect the latest saved value
	label := wtx.Label
	if c.wallet != nil && wtx.Address != "" {
		if addressLabel := c.wallet.GetAddressLabel(wtx.Address); addressLabel != "" {
			label = addressLabel
		}
	}

	return Transaction{
		TxID:          wtx.Hash.String(),
		Vout:          int(wtx.Vout),
		Amount:        amount,
		Fee:           fee,
		Confirmations: int(wtx.Confirmations),
		BlockHash:     blockHashStr(wtx.BlockHash),
		BlockHeight:   int64(wtx.BlockHeight),
		Time:          wtx.Time,
		Type:          txType,
		Address:       wtx.Address,
		FromAddress:   wtx.FromAddress,
		Label:         label,
		Comment:       wtx.Comment,
		Category:      string(wtx.Category),
		IsWatchOnly:   wtx.WatchOnly,
		IsLocked:      false, // SwiftTX not implemented
		IsConflicted:  wtx.IsConflicted,
		IsCoinbase:    isCoinbase,
		IsCoinstake:   isCoinstake,
		MaturesIn:     maturesIn,
		Debit:         debit,
		Credit:        credit,
	}
}

// blockHashStr returns the block hash as a string, or empty string for zero hashes.
// This prevents the frontend from displaying "000...000" for unconfirmed transactions.
func blockHashStr(h types.Hash) string {
	if h.IsZero() {
		return ""
	}
	return h.String()
}

// mapCategoryToType maps wallet.TxCategory to core.TransactionType
func mapCategoryToType(cat wallet.TxCategory) TransactionType {
	switch cat {
	case wallet.TxCategorySend:
		return TxTypeSendToAddress
	case wallet.TxCategoryReceive:
		return TxTypeRecvWithAddress
	case wallet.TxCategoryCoinStake:
		return TxTypeStakeMint
	case wallet.TxCategoryCoinBase:
		return TxTypeGenerated
	case wallet.TxCategoryMasternode:
		return TxTypeMNReward
	case wallet.TxCategoryGenerate:
		return TxTypeGenerated
	case wallet.TxCategoryToSelf:
		return TxTypeSendToSelf
	default:
		return TxTypeOther
	}
}

func (c *GoCoreClient) ListTransactionsFiltered(filter TransactionFilter) (TransactionPage, error) {
	c.mu.RLock()
	w := c.wallet
	c.mu.RUnlock()

	if w == nil {
		return TransactionPage{}, fmt.Errorf("wallet not initialized")
	}

	// Validate page size
	validSizes := map[int]bool{25: true, 50: true, 100: true, 250: true}
	if !validSizes[filter.PageSize] {
		filter.PageSize = 25
	}
	if filter.Page < 1 {
		filter.Page = 1
	}

	// Convert MinAmount from TWINS to satoshis for wallet layer
	minAmountSat := float64(0)
	if filter.MinAmount > 0 {
		minAmountSat = filter.MinAmount * satoshisPerTWINS
	}

	params := wallet.TransactionFilterParams{
		Page:             filter.Page,
		PageSize:         filter.PageSize,
		DateFilter:       filter.DateFilter,
		DateRangeFrom:    filter.DateRangeFrom,
		DateRangeTo:      filter.DateRangeTo,
		TypeFilter:       filter.TypeFilter,
		SearchText:       filter.SearchText,
		MinAmount:        minAmountSat,
		WatchOnlyFilter:  filter.WatchOnlyFilter,
		HideOrphanStakes: filter.HideOrphanStakes,
		SortColumn:       filter.SortColumn,
		SortDirection:    filter.SortDirection,
	}

	result, err := w.ListTransactionsFiltered(params)
	if err != nil {
		return TransactionPage{}, fmt.Errorf("failed to list filtered transactions: %w", err)
	}

	// Convert wallet transactions to core transactions
	txs := make([]Transaction, 0, len(result.Transactions))
	for _, wtx := range result.Transactions {
		txs = append(txs, c.convertWalletTransaction(wtx))
	}

	totalPages := 0
	if result.Total > 0 {
		totalPages = (result.Total + filter.PageSize - 1) / filter.PageSize
	}

	// Derive actual page from the data the wallet returned rather than
	// re-clamping independently (wallet already clamps out-of-range pages).
	actualPage := filter.Page
	if totalPages > 0 {
		if actualPage > totalPages {
			actualPage = totalPages
		}
	} else {
		actualPage = 1
	}

	return TransactionPage{
		Transactions: txs,
		Total:        result.Total,
		TotalAll:     result.TotalAll,
		Page:         actualPage,
		PageSize:     filter.PageSize,
		TotalPages:   totalPages,
	}, nil
}

func (c *GoCoreClient) ExportFilteredTransactionsCSV(filter TransactionFilter) (string, error) {
	c.mu.RLock()
	w := c.wallet
	c.mu.RUnlock()

	if w == nil {
		return "", fmt.Errorf("wallet not initialized")
	}

	// Convert MinAmount from TWINS to satoshis for wallet layer
	minAmountSat := float64(0)
	if filter.MinAmount > 0 {
		minAmountSat = filter.MinAmount * satoshisPerTWINS
	}

	// PageSize <= 0 returns all matching results (no pagination)
	params := wallet.TransactionFilterParams{
		Page:             1,
		PageSize:         0,
		DateFilter:       filter.DateFilter,
		DateRangeFrom:    filter.DateRangeFrom,
		DateRangeTo:      filter.DateRangeTo,
		TypeFilter:       filter.TypeFilter,
		SearchText:       filter.SearchText,
		MinAmount:        minAmountSat,
		WatchOnlyFilter:  filter.WatchOnlyFilter,
		HideOrphanStakes: filter.HideOrphanStakes,
		SortColumn:       filter.SortColumn,
		SortDirection:    filter.SortDirection,
	}

	result, err := w.ListTransactionsFiltered(params)
	if err != nil {
		return "", fmt.Errorf("failed to list filtered transactions: %w", err)
	}

	// Convert and generate CSV
	var sb strings.Builder
	sb.WriteString("\"Confirmed\",\"Date\",\"Type\",\"Label\",\"Address\",\"Amount (TWINS)\",\"ID\"\n")

	for _, wtx := range result.Transactions {
		tx := c.convertWalletTransaction(wtx)
		confirmed := "false"
		if tx.Confirmations >= 6 {
			confirmed = "true"
		}
		date := tx.Time.Format("2006-01-02T15:04:05")
		typeLabel := csvEscape(mapTypeToLabel(tx.Type))
		label := csvEscape(tx.Label)
		address := csvEscape(tx.Address)
		amount := fmt.Sprintf("%.8f", tx.Amount)
		txid := tx.TxID

		sb.WriteString(fmt.Sprintf("\"%s\",\"%s\",%s,%s,%s,\"%s\",\"%s\"\n",
			confirmed, date, typeLabel, label, address, amount, txid))
	}

	return sb.String(), nil
}

// csvEscape escapes a string for CSV with formula injection protection
func csvEscape(s string) string {
	// Sanitize formula injection
	if len(s) > 0 && (s[0] == '=' || s[0] == '+' || s[0] == '-' || s[0] == '@') {
		s = "'" + s
	}
	// Replace control characters
	s = strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(s)
	return "\"" + strings.ReplaceAll(s, "\"", "\"\"") + "\""
}

// mapTypeToLabel maps a TransactionType to a human-readable label for CSV export
func mapTypeToLabel(t TransactionType) string {
	switch t {
	case TxTypeSendToAddress, TxTypeSendToOther:
		return "Sent to"
	case TxTypeRecvWithAddress, TxTypeRecvFromOther:
		return "Received with"
	case TxTypeSendToSelf:
		return "Payment to yourself"
	case TxTypeGenerated:
		return "Mined"
	case TxTypeStakeMint:
		return "Minted"
	case TxTypeMNReward:
		return "Masternode Reward"
	case TxTypeConsolidation:
		return "UTXO Consolidation"
	default:
		return string(t)
	}
}

func (c *GoCoreClient) ValidateAddress(address string) (AddressValidation, error) {
	// First validate address format
	_, err := crypto.DecodeAddress(address)
	if err != nil {
		return AddressValidation{
			IsValid: false,
			Address: address,
			IsMine:  false,
		}, nil
	}

	// Check wallet ownership - wallet must be available for IsMine check
	c.mu.RLock()
	w := c.wallet
	c.mu.RUnlock()

	if w == nil {
		// Return error so frontend can show appropriate message
		return AddressValidation{
			IsValid: true,
			Address: address,
			IsMine:  false,
		}, fmt.Errorf("wallet not initialized")
	}

	return AddressValidation{
		IsValid: true,
		Address: address,
		IsMine:  w.IsOurAddress(address),
	}, nil
}

func (c *GoCoreClient) EncryptWallet(passphrase string) error {
	return fmt.Errorf("not implemented: use wallet layer")
}

func (c *GoCoreClient) WalletLock() error {
	return fmt.Errorf("not implemented: use wallet layer")
}

func (c *GoCoreClient) WalletPassphrase(passphrase string, timeout int) error {
	return fmt.Errorf("not implemented: use wallet layer")
}

func (c *GoCoreClient) WalletPassphraseChange(oldPassphrase string, newPassphrase string) error {
	return fmt.Errorf("not implemented: use wallet layer")
}

func (c *GoCoreClient) GetWalletInfo() (WalletInfo, error) {
	c.mu.RLock()
	w := c.wallet
	c.mu.RUnlock()

	if w == nil {
		return WalletInfo{}, fmt.Errorf("wallet not initialized")
	}

	// Convert satoshis to TWINS (1 TWINS = 100,000,000 satoshis)


	balance := w.GetBalance()
	info := WalletInfo{
		Version:            1,
		Balance:            float64(balance.Confirmed) / satoshisPerTWINS,
		UnconfirmedBalance: float64(balance.Unconfirmed) / satoshisPerTWINS,
		ImmatureBalance:    float64(balance.Immature) / satoshisPerTWINS,
		Encrypted:          w.IsEncrypted(),
		Unlocked:           !w.IsLocked(),
		UnlockedUntil:      w.UnlockTime(),
		PayTxFee:           0.0001, // Default fee
	}

	return info, nil
}

func (c *GoCoreClient) BackupWallet(destination string) error {
	c.mu.RLock()
	w := c.wallet
	c.mu.RUnlock()

	if w == nil {
		return fmt.Errorf("wallet not initialized")
	}

	return w.BackupWallet(destination)
}

func (c *GoCoreClient) ListUnspent(minConf int, maxConf int) ([]UTXO, error) {
	c.mu.RLock()
	w := c.wallet
	c.mu.RUnlock()

	if w == nil {
		return nil, fmt.Errorf("wallet not initialized")
	}

	// Call wallet ListUnspent with empty address filter (all addresses)
	// The wallet now returns Locked/Spendable fields reflecting both user locks and collateral
	result, err := w.ListUnspent(minConf, maxConf, []string{})
	if err != nil {
		return nil, fmt.Errorf("failed to list unspent: %w", err)
	}

	// Type assert result to []*wallet.UnspentOutput
	unspentOutputs, ok := result.([]*wallet.UnspentOutput)
	if !ok {
		return nil, fmt.Errorf("unexpected type from wallet.ListUnspent: %T", result)
	}

	// Convert to core.UTXO type
	utxos := make([]UTXO, 0, len(unspentOutputs))
	for _, uo := range unspentOutputs {
		// Calculate priority: (amount * confirmations) / 148
		// 148 is approximate size of a typical input in bytes
		priority := uo.Amount * float64(uo.Confirmations) / 148.0

		utxos = append(utxos, UTXO{
			TxID:          uo.TxID,
			Vout:          uo.Vout,
			Address:       uo.Address,
			Label:         "", // TODO: Get from address manager when available
			ScriptPubKey:  uo.ScriptPubKey,
			Amount:        uo.Amount,
			Confirmations: int(uo.Confirmations),
			Spendable:     uo.Spendable,
			Solvable:      true, // Assume solvable for wallet UTXOs
			Locked:        uo.Locked,
			Type:          "Personal", // TODO: Detect multisig when available
			Date:          int64(uo.BlockTime),
			Priority:      priority,
		})
	}

	return utxos, nil
}

// LockUnspent locks or unlocks UTXOs via the unified wallet lock store.
// Legacy: Delegates to CWallet::LockCoin/UnlockCoin — shared by both GUI and RPC.
func (c *GoCoreClient) LockUnspent(unlock bool, outputs []OutPoint) error {
	c.mu.RLock()
	w := c.wallet
	c.mu.RUnlock()

	if w == nil {
		return fmt.Errorf("wallet not initialized")
	}

	for _, op := range outputs {
		outpoint, err := parseOutPoint(op.TxID, op.Vout)
		if err != nil {
			return fmt.Errorf("invalid outpoint %s:%d: %w", op.TxID, op.Vout, err)
		}

		if unlock {
			w.UnlockCoin(outpoint)
		} else {
			w.LockCoin(outpoint)
		}
	}

	return nil
}

// ListLockUnspent returns all currently locked UTXOs from the unified wallet lock store.
func (c *GoCoreClient) ListLockUnspent() ([]OutPoint, error) {
	c.mu.RLock()
	w := c.wallet
	c.mu.RUnlock()

	if w == nil {
		return nil, fmt.Errorf("wallet not initialized")
	}

	locked := w.ListLockedCoins()
	result := make([]OutPoint, 0, len(locked))
	for _, op := range locked {
		result = append(result, OutPoint{
			TxID: op.Hash.String(),
			Vout: op.Index,
		})
	}

	return result, nil
}

// parseOutPoint converts a txid string and vout to a types.Outpoint.
func parseOutPoint(txid string, vout uint32) (types.Outpoint, error) {
	hash, err := types.NewHashFromString(txid)
	if err != nil {
		return types.Outpoint{}, fmt.Errorf("invalid txid: %w", err)
	}
	return types.Outpoint{Hash: hash, Index: vout}, nil
}

// EstimateFee estimates the fee rate (in TWINS/kB) for confirmation within the specified number of blocks.
// For TWINS, this returns a fee rate based on desired confirmation time:
// - 1-2 blocks (fast): Higher fee rate for priority
// - 3-6 blocks (normal): Standard fee rate
// - 7+ blocks (economy): Minimum relay fee
func (c *GoCoreClient) EstimateFee(blocks int) (float64, error) {
	// TWINS default fee is 0.0001 TWINS/kB (10000 satoshis/kB)
	const (
		defaultFeePerKB  = 0.0001  // Standard fee (10000 satoshis/kB)
		priorityFeePerKB = 0.001   // Priority fee (100000 satoshis/kB)
		minFeePerKB      = 0.00001 // Minimum relay fee (1000 satoshis/kB)
	)

	// Check if wallet is available to get configured fee
	c.mu.RLock()
	w := c.wallet
	c.mu.RUnlock()

	var configuredFee float64
	if w != nil {
		// Get fee from wallet configuration (in satoshis/kB)
		feePerKB := w.GetTransactionFee()
		if feePerKB > 0 {
			configuredFee = float64(feePerKB) / satoshisPerTWINS // Convert satoshis to TWINS
		}
	}

	// Return fee based on confirmation urgency
	switch {
	case blocks <= 2:
		// Fast confirmation - use priority fee or 10x configured
		if configuredFee > 0 {
			return configuredFee * 10, nil
		}
		return priorityFeePerKB, nil
	case blocks <= 6:
		// Normal confirmation - use configured or default fee
		if configuredFee > 0 {
			return configuredFee, nil
		}
		return defaultFeePerKB, nil
	default:
		// Economy - use minimum fee
		return minFeePerKB, nil
	}
}

func (c *GoCoreClient) GetBlockchainInfo() (BlockchainInfo, error) {
	height, err := c.storage.GetChainHeight()
	if err != nil {
		return BlockchainInfo{}, err
	}
	tip, _ := c.storage.GetChainTip()

	info := BlockchainInfo{
		Blocks:        int64(height),
		BestBlockHash: tip.String(),
		Chain:         "main",
	}

	// Populate sync status fields from syncer and p2p server if available
	c.mu.RLock()
	syncer := c.syncer
	p2p := c.p2pServer
	c.mu.RUnlock()

	// Populate peer count and connecting state for frontend sync status determination
	if p2p != nil {
		info.PeerCount = int(p2p.GetPeerCount())
		info.IsConnecting = info.PeerCount < blockchain.MinSyncPeers
	}

	if syncer != nil {
		current, target, _ := syncer.GetSyncProgress()
		isSyncing := syncer.IsSyncing()
		isSynced := syncer.IsSynced()
		networkHeight := syncer.GetNetworkHeight()

		// If network consensus height is 0, we have no reliable consensus
		// and cannot claim to be synced regardless of state machine
		if networkHeight == 0 {
			isSynced = false
		}

		info.IsSyncing = isSyncing

		// Calculate behind blocks (only if target > current)
		if target > current {
			behindBlocks := int64(target - current)
			info.BehindBlocks = behindBlocks
			info.IsOutOfSync = true

			// Calculate sync percentage
			if target > 0 {
				info.SyncPercentage = (float64(current) / float64(target)) * 100
			}

			// Calculate behind time (TWINS has ~60 second block time)
			info.BehindTime = formatBehindTime(behindBlocks)
		} else if !isSynced {
			// Not synced but target <= current (e.g., no consensus, too few peers)
			info.IsOutOfSync = true
			info.SyncPercentage = 0
			info.BehindTime = ""
		} else {
			// We're synced
			info.IsOutOfSync = false
			info.SyncPercentage = 100.0
			info.BehindTime = "up to date"
		}

		// Current block being processed during sync
		if isSyncing {
			info.CurrentBlockScan = int64(current)
		}
	}

	return info, nil
}

// formatBehindTime converts blocks behind into a human-readable time string.
// TWINS has approximately 60 second block times.
func formatBehindTime(blocks int64) string {
	if blocks <= 0 {
		return "up to date"
	}

	// TWINS block time is ~60 seconds
	totalSeconds := blocks * 60

	minutes := totalSeconds / 60
	hours := minutes / 60
	days := hours / 24
	weeks := days / 7

	if weeks > 0 {
		return fmt.Sprintf("%d weeks behind", weeks)
	}
	if days > 0 {
		return fmt.Sprintf("%d days behind", days)
	}
	if hours > 0 {
		return fmt.Sprintf("%d hours behind", hours)
	}
	if minutes > 0 {
		return fmt.Sprintf("%d minutes behind", minutes)
	}
	return fmt.Sprintf("%d blocks behind", blocks)
}

func (c *GoCoreClient) GetNetworkInfo() (NetworkInfo, error) {
	c.mu.RLock()
	p2pServer := c.p2pServer
	syncer := c.syncer
	c.mu.RUnlock()

	info := NetworkInfo{
		Version:         70928, // Protocol version
		Subversion:      "/TWINS Core:2.0.0/",
		ProtocolVersion: 70928,
		LocalRelay:      true,
		RelayFee:        0.0001,
	}

	if p2pServer != nil {
		info.Connections = int(p2pServer.GetPeerCount())
		info.NetworkActive = p2pServer.IsStarted()
	} else {
		// P2P not initialized yet
		info.Connections = 0
		info.NetworkActive = false
	}

	// Populate network consensus height from syncer
	if syncer != nil {
		info.NetworkHeight = int64(syncer.GetNetworkHeight())
	}

	return info, nil
}

func (c *GoCoreClient) GetBlock(hash string) (Block, error) {
	return Block{}, fmt.Errorf("not implemented: use GetExplorerBlock")
}

func (c *GoCoreClient) GetBlockHash(height int64) (string, error) {
	hash, err := c.storage.GetBlockHashByHeight(uint32(height))
	if err != nil {
		return "", err
	}
	return hash.String(), nil
}

func (c *GoCoreClient) GetBlockCount() (int64, error) {
	height, err := c.storage.GetChainHeight()
	return int64(height), err
}

func (c *GoCoreClient) GetPeerInfo() ([]PeerInfo, error) {
	return nil, fmt.Errorf("not implemented: use p2p layer")
}

func (c *GoCoreClient) GetConnectionCount() (int, error) {
	return 0, fmt.Errorf("not implemented: use p2p layer")
}

func (c *GoCoreClient) MasternodeList(filter string) ([]MasternodeInfo, error) {
	if c.masternode == nil {
		return nil, fmt.Errorf("masternode manager not initialized")
	}

	// Get current block height for rank calculation
	var blockHeight uint32
	if c.storage != nil {
		if height, err := c.storage.GetChainHeight(); err == nil && height > masternode.ScoreBlockDepth {
			blockHeight = height - masternode.ScoreBlockDepth
		}
	}

	// Get masternodes with calculated ranks
	// GetMasternodeRanks returns masternodes sorted by rank with proper score calculation
	rankedMns := c.masternode.GetMasternodeRanks(blockHeight, masternode.ActiveProtocolVersion)

	// Build lookup map for ranks by outpoint
	rankMap := make(map[string]int)
	for _, entry := range rankedMns {
		outpointKey := entry.Masternode.OutPoint.Hash.String() + ":" + fmt.Sprintf("%d", entry.Masternode.OutPoint.Index)
		rankMap[outpointKey] = entry.Rank
	}

	// Get all masternodes and populate with ranks
	mns := c.masternode.GetMasternodes()
	result := make([]MasternodeInfo, 0, len(mns))
	currentTime := time.Now()
	expireTime := time.Duration(masternode.ExpirationSeconds) * time.Second

	for outpoint, mn := range mns {
		// Refresh status before reading to match legacy mn.Check() behavior.
		// Without this, masternodes stay stuck at PRE_ENABLED in the GUI
		// because UpdateStatus() is what transitions PRE_ENABLED -> ENABLED.
		// See: masternode_adapter.go:54 (RPC path has same fix)
		mn.UpdateStatus(currentTime, expireTime)

		// Get address string from net.Addr
		addrStr := ""
		if mn.Addr != nil {
			addrStr = mn.Addr.String()
		}
		// Get public keys as hex strings
		pubKey := ""
		if mn.PubKeyCollateral != nil {
			pubKey = mn.PubKeyCollateral.CompressedHex()
		}
		pubKeyOperator := ""
		if mn.PubKey != nil {
			pubKeyOperator = mn.PubKey.CompressedHex()
		}

		// Look up calculated rank
		outpointKey := outpoint.Hash.String() + ":" + fmt.Sprintf("%d", outpoint.Index)
		rank := rankMap[outpointKey] // 0 if not found

		// Calculate active time as live-incrementing duration since activation
		activeTime := int64(0)
		if !mn.ActiveSince.IsZero() {
			activeTime = time.Now().Unix() - mn.ActiveSince.Unix()
			if activeTime < 0 {
				activeTime = 0 // Guard against clock skew
			}
		}

		// Use payment tracker for LastPaid if available, fall back to mn.LastPaid
		// Normalize: zero time or Unix epoch both mean "never paid"
		lastPaid := mn.LastPaid
		if c.paymentTracker != nil {
			if stats := c.paymentTracker.GetStatsByScript(mn.GetPayeeScript()); stats != nil {
				lastPaid = stats.LastPaid
			}
		}
		if lastPaid.Unix() <= 0 {
			lastPaid = time.Time{}
		}

		info := MasternodeInfo{
			Rank:           rank,
			Address:        addrStr,
			Status:         mn.Status.String(),
			ActiveTime:     activeTime,
			LastSeen:       mn.LastSeen,
			LastPaid:       lastPaid,
			Txhash:         outpoint.Hash.String(),
			Outidx:         int(outpoint.Index),
			Tier:           mn.Tier.String(),
			Version:        int(mn.Protocol),
			PubKey:         pubKey,
			PubKeyOperator: pubKeyOperator,
			PaymentAddress: mn.GetPayee(), // Get payment address from collateral pubkey
		}
		// Apply filter if provided
		if filter == "" || filter == "all" ||
			(filter == "enabled" && mn.Status == masternode.StatusEnabled) {
			result = append(result, info)
		}
	}
	return result, nil
}

func (c *GoCoreClient) MasternodeStart(alias string) error {
	return fmt.Errorf("not implemented: use masternode layer")
}

func (c *GoCoreClient) MasternodeStartAll() error {
	return fmt.Errorf("not implemented: use masternode layer")
}

func (c *GoCoreClient) MasternodeStatus() (MasternodeStatus, error) {
	return MasternodeStatus{}, fmt.Errorf("not implemented: use masternode layer")
}

func (c *GoCoreClient) GetMasternodeCount() (MasternodeCount, error) {
	if c.masternode == nil {
		return MasternodeCount{}, fmt.Errorf("masternode manager not initialized")
	}
	total := c.masternode.GetMasternodeCount()
	enabled := c.masternode.GetActiveCount()
	return MasternodeCount{
		Total:   total,
		Enabled: enabled,
	}, nil
}

func (c *GoCoreClient) MasternodeCurrentWinner() (MasternodeInfo, error) {
	return MasternodeInfo{}, fmt.Errorf("not implemented: use masternode layer")
}

func (c *GoCoreClient) GetMyMasternodes() ([]MyMasternode, error) {
	return nil, fmt.Errorf("not implemented: use masternode layer")
}

func (c *GoCoreClient) MasternodeStartMissing() (int, error) {
	return 0, fmt.Errorf("not implemented: use masternode layer")
}

func (c *GoCoreClient) GetStakingInfo() (StakingInfo, error) {
	c.mu.RLock()
	consensus := c.consensus
	w := c.wallet
	stakingEnabled := c.stakingEnabled
	store := c.storage
	c.mu.RUnlock()

	info := StakingInfo{
		Enabled: stakingEnabled,
	}

	// Get staking status from consensus engine
	if consensus != nil {
		info.Staking = consensus.IsStaking()
	}

	// Get wallet lock status
	if w != nil {
		info.WalletUnlocked = !w.IsLocked()
	}

	// Compute expected time to next stake from chain data and wallet UTXOs.
	//
	// The TWINS PoS kernel runs once per second per UTXO; a hit occurs when:
	//   hash(kernelData) < target × (utxo.Amount/100)
	// where target = CompactToBig(block.Bits) is the per-second-per-coin-unit difficulty.
	//
	// Probability of wallet finding a block in any given second:
	//   P = target × walletWeight / 2^256   (walletWeight = Σ utxo.Amount/100)
	// Expected seconds until next stake:
	//   E[t] = 1/P = 2^256 / (target × walletWeight)
	//
	// This is equivalent to (networkWeight / walletWeight) × target_spacing because the
	// difficulty adjustment sets target = 2^256 / (networkWeight × target_spacing), so
	// 2^256 / (target × walletWeight) = target_spacing × networkWeight / walletWeight.
	// No additional target_spacing factor is needed here.
	if store != nil && w != nil {
		if chainHeight, err := store.GetChainHeight(); err == nil {
			if bestBlock, err := store.GetBlockByHeight(chainHeight); err == nil && bestBlock != nil {
				chainTime := bestBlock.Header.Timestamp
				if utxos, err := w.GetStakeableUTXOs(chainHeight, chainTime); err == nil && len(utxos) > 0 {
					walletWeightBig := new(big.Int)
					for _, utxo := range utxos {
						walletWeightBig.Add(walletWeightBig, big.NewInt(utxo.Amount/100))
					}
					if walletWeightBig.Sign() > 0 {
						target := types.CompactToBig(bestBlock.Header.Bits)
						if target.Sign() > 0 {
							two256 := new(big.Int).Lsh(big.NewInt(1), 256)
							effectiveTarget := new(big.Int).Mul(target, walletWeightBig)
							if effectiveTarget.Cmp(two256) >= 0 {
								// Theoretical overflow guard: effectiveTarget >= 2^256 would
								// produce a nonsensical result. In practice this cannot occur
								// (walletWeight in satoshis/100 is far below 2^256/target), but
								// we guard defensively and display N/A.
								info.ExpectedStakeTime = 0
							} else {
								expected := new(big.Int).Div(two256, effectiveTarget)
								const maxExpected = int64(10 * 365 * 24 * 3600)
								if expected.IsInt64() && expected.Int64() < maxExpected {
									info.ExpectedStakeTime = expected.Int64()
								} else {
									info.ExpectedStakeTime = maxExpected
								}
							}
						}
					}
				}
			}
		}
	}

	return info, nil
}

func (c *GoCoreClient) SetStaking(enabled bool) error {
	return fmt.Errorf("not implemented: use staking layer")
}

func (c *GoCoreClient) GetStakingStatus() (bool, error) {
	return false, fmt.Errorf("not implemented: use staking layer")
}

func (c *GoCoreClient) SignMessage(address string, message string) (string, error) {
	return "", fmt.Errorf("not implemented: use wallet layer")
}

func (c *GoCoreClient) VerifyMessage(address string, signature string, message string) (bool, error) {
	return false, fmt.Errorf("not implemented")
}

func (c *GoCoreClient) GetInfo() (map[string]interface{}, error) {
	height, _ := c.storage.GetChainHeight()
	return map[string]interface{}{
		"blocks": height,
	}, nil
}

func (c *GoCoreClient) AddNode(node string, command string) error {
	return fmt.Errorf("not implemented: use p2p layer")
}

func (c *GoCoreClient) DisconnectNode(address string) error {
	return fmt.Errorf("not implemented: use p2p layer")
}

func (c *GoCoreClient) GetAddedNodeInfo(node string) ([]interface{}, error) {
	return nil, fmt.Errorf("not implemented: use p2p layer")
}

func (c *GoCoreClient) SetNetworkActive(active bool) error {
	return fmt.Errorf("not implemented: use p2p layer")
}

func (c *GoCoreClient) InvalidateBlock(hash string) error {
	return fmt.Errorf("not implemented: use blockchain layer")
}

func (c *GoCoreClient) ReconsiderBlock(hash string) error {
	return fmt.Errorf("not implemented: use blockchain layer")
}

func (c *GoCoreClient) VerifyChain(checkLevel int, numBlocks int) (bool, error) {
	return false, fmt.Errorf("not implemented: use blockchain layer")
}
