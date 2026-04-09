package consensus

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"

	"github.com/sirupsen/logrus"

	"github.com/twins-dev/twins-core/internal/storage"
	"github.com/twins-dev/twins-core/pkg/crypto"
	"github.com/twins-dev/twins-core/pkg/script"
	"github.com/twins-dev/twins-core/pkg/types"
)

// SporkInterface defines methods needed for spork-aware validation
type SporkInterface interface {
	IsActive(sporkID int32) bool
	GetValue(sporkID int32) int64
}

// TrxValidationStatus mirrors legacy TrxValidationStatus enum
type TrxValidationStatus int

const (
	TrxValidationInvalid       TrxValidationStatus = 0 // Transaction is invalid
	TrxValidationValid         TrxValidationStatus = 1 // Transaction is valid
	TrxValidationDoublePayment TrxValidationStatus = 2 // Double budget payment detected
	TrxValidationVoteThreshold TrxValidationStatus = 3 // Not enough votes for finalized budget
)

// BudgetInterface defines methods needed for budget/superblock validation
// CRITICAL: Must include IsTransactionValid to match legacy budget.IsTransactionValid()
// Legacy: masternode-payments.cpp:238-251 checks budget validation with enforcement
type BudgetInterface interface {
	IsBudgetPaymentBlock(height uint32) bool
	GetBudgetPaymentCycleBlocks() uint32
	// IsTransactionValid validates budget payment transaction
	// Returns TrxValidationStatus matching legacy behavior
	// Legacy: budget.IsTransactionValid(txNew, nBlockHeight) at masternode-payments.cpp:239
	IsTransactionValid(tx *types.Transaction, height uint32) TrxValidationStatus
}

// BlockValidator handles comprehensive PoS block validation
type BlockValidator struct {
	pos              *ProofOfStake
	storage          storage.Storage
	params           *types.ChainParams
	paymentValidator *MasternodePaymentValidator
	sporkManager     SporkInterface
	budgetManager    BudgetInterface
	isSynced         func() bool // Function to check if node is synced
}

// ValidationContext holds context information for block validation
type ValidationContext struct {
	Block      *types.Block
	PrevBlock  *types.Block
	PrevIndex  *BlockIndex // Index of previous block for version checks
	Height     uint32
	MedianTime uint32
	Flags      ValidationFlags
}

// Reset clears the validation context for reuse
func (vc *ValidationContext) Reset() {
	vc.Block = nil
	vc.PrevBlock = nil
	vc.PrevIndex = nil
	vc.Height = 0
	vc.MedianTime = 0
	vc.Flags = 0
}

// ValidationFlags specify which validation checks to perform
type ValidationFlags uint32

const (
	ValidatePoS          ValidationFlags = 1 << iota // Validate Proof-of-Stake
	ValidateTime                                     // Validate block timing
	ValidateTarget                                   // Validate difficulty target
	ValidateModifier                                 // Validate stake modifier
	ValidateSignature                                // Validate block signature
	ValidateTransactions                             // Validate all transactions
	SkipInputValidation                              // Skip UTXO lookup and script verification (used during batch processing)
	ValidateAll          = ValidatePoS | ValidateTime | ValidateTarget | ValidateModifier | ValidateSignature | ValidateTransactions
)

// NewBlockValidator creates a new block validator
func NewBlockValidator(pos *ProofOfStake, storage storage.Storage, params *types.ChainParams) *BlockValidator {
	return &BlockValidator{
		pos:              pos,
		storage:          storage,
		params:           params,
		paymentValidator: nil, // Set via SetPaymentValidator
	}
}

// SetPaymentValidator sets the masternode payment validator
func (bv *BlockValidator) SetPaymentValidator(validator *MasternodePaymentValidator) {
	bv.paymentValidator = validator
}

// SetSporkManager sets the spork manager for spork-aware validation
func (bv *BlockValidator) SetSporkManager(sporkManager SporkInterface) {
	bv.sporkManager = sporkManager
}

// SetBudgetManager sets the budget manager for superblock validation
func (bv *BlockValidator) SetBudgetManager(budgetManager BudgetInterface) {
	bv.budgetManager = budgetManager
}

// SetSyncedFunc sets the function to check if the node is synced
func (bv *BlockValidator) SetSyncedFunc(isSynced func() bool) {
	bv.isSynced = isSynced
}

// ValidateBlock performs comprehensive block validation according to flags
func (bv *BlockValidator) ValidateBlock(ctx *ValidationContext) error {
	if ctx == nil || ctx.Block == nil {
		return errors.New("validation context or block is nil")
	}

	block := ctx.Block

	// Basic block structure validation (always performed)
	if err := bv.validateBlockStructure(block); err != nil {
		return fmt.Errorf("block structure validation failed: %w", err)
	}

	// Header validation
	if err := bv.ValidateBlockHeader(block.Header, ctx.PrevBlock.Header, ctx.Height); err != nil {
		return fmt.Errorf("block header validation failed: %w", err)
	}

	// Version validation
	if err := ValidateBlockVersion(block, ctx.PrevIndex, bv.params); err != nil {
		return fmt.Errorf("block version validation failed: %w", err)
	}

	// Version-specific contextual validation
	if err := ValidateBlockVersionContext(block, ctx.PrevIndex, bv.params); err != nil {
		return fmt.Errorf("block version context validation failed: %w", err)
	}

	// Time validation
	if ctx.Flags&ValidateTime != 0 {
		if err := bv.ValidateBlockTime(block, ctx.MedianTime); err != nil {
			return fmt.Errorf("block time validation failed: %w", err)
		}
	}

	// Target validation
	if ctx.Flags&ValidateTarget != 0 {
		if err := bv.ValidateTarget(block, ctx.Height); err != nil {
			return fmt.Errorf("target validation failed: %w", err)
		}
	}

	// Stake modifier validation
	if ctx.Flags&ValidateModifier != 0 {
		if err := bv.ValidateStakeModifier(ctx); err != nil {
			return fmt.Errorf("stake modifier validation failed: %w", err)
		}
	}

	// PoS validation (skip for PoW blocks - height <= LastPOWBlock)
	if ctx.Flags&ValidatePoS != 0 {
		// Only validate PoS for blocks after the PoW period
		if ctx.Height > bv.params.LastPOWBlock {
			if err := bv.validateProofOfStake(block, ctx.Height); err != nil {
				return fmt.Errorf("proof of stake validation failed: %w", err)
			}
		}
	}

	// Transaction validation
	if ctx.Flags&ValidateTransactions != 0 {
		if err := bv.validateTransactions(block, ctx.Height, ctx.Flags); err != nil {
			return fmt.Errorf("transaction validation failed: %w", err)
		}
	}

	// Block signature validation (skip for PoW blocks - they don't have signatures)
	if ctx.Flags&ValidateSignature != 0 {
		// Only validate signatures for PoS blocks
		if ctx.Height > bv.params.LastPOWBlock {
			if err := bv.validateBlockSignature(block); err != nil {
				return fmt.Errorf("block signature validation failed: %w", err)
			}
		}
	}

	// Block value validation - ensure minted amount doesn't exceed expected reward
	// Skip during batch processing (SkipInputValidation) because it requires UTXO lookups
	// for coinstake inputs, which may not be in committed storage yet.
	// The block value is implicitly validated through UTXO consistency in applyBlockToBatch.
	blockReward := bv.CalculateBlockReward(ctx.Height)
	if ctx.Flags&SkipInputValidation == 0 {
		if err := bv.validateBlockValue(block, ctx.Height, blockReward); err != nil {
			return fmt.Errorf("block value validation failed: %w", err)
		}
	}

	// Dev fund validation - ensure development fund payout is present (height >= 2)
	if err := bv.validateDevFundPayment(block, ctx.Height, blockReward); err != nil {
		return fmt.Errorf("dev fund validation failed: %w", err)
	}

	// Minimum stake OUTPUT validation for PoS blocks
	// Legacy: main.cpp:3975-3979 - validates vout[1].Value >= MinStakeAmount with spork+height gate
	// CRITICAL: This is OUTPUT validation (not INPUT) - C++ checks the coinstake output, not input
	if ctx.Height > bv.params.LastPOWBlock && block.IsProofOfStake() {
		if err := bv.validateMinStakeOutput(block, ctx.Height); err != nil {
			return fmt.Errorf("min stake output validation failed: %w", err)
		}
	}

	// Masternode payment validation (if validator is set)
	// CRITICAL: Legacy validates BOTH PoW and PoS blocks once synced
	// - PoW blocks: use coinbase (vtx[0]) for payment validation
	// - PoS blocks: use coinstake (vtx[1]) for payment validation
	// Legacy: IsBlockPayeeValid at masternode-payments.cpp:225-269
	if bv.paymentValidator != nil {
		// LEGACY COMPATIBILITY: Pass isSynced state to payment validator
		// Legacy masternodeSync.IsSynced() returns true when RequestedMasternodeAssets == MASTERNODE_SYNC_FINISHED
		// During initial sync, payment validation is skipped to allow block acceptance without full MN data
		isSynced := bv.isSynced != nil && bv.isSynced()
		if err := bv.paymentValidator.ValidateBlockPayment(block, ctx.Height, blockReward, isSynced); err != nil {
			return fmt.Errorf("masternode payment validation failed: %w", err)
		}
	}

	return nil
}

// ValidateBlockHeader validates the block header against the previous header
func (bv *BlockValidator) ValidateBlockHeader(header, prevHeader *types.BlockHeader, height uint32) error {
	if header == nil {
		return errors.New("header is nil")
	}

	// Genesis block has no previous header
	if height == 0 {
		return bv.validateGenesisHeader(header)
	}

	if prevHeader == nil {
		return errors.New("previous header is nil for non-genesis block")
	}

	// Height continuity is validated at a higher level through the ValidationContext

	// Previous block hash must match
	// For genesis block (height 0), use canonical hash from chainparams (Quark hash)
	var expectedPrevHash types.Hash
	if height == 1 {
		expectedPrevHash = bv.params.GenesisHash
	} else {
		expectedPrevHash = prevHeader.Hash()
	}

	if header.PrevBlockHash != expectedPrevHash {
		return fmt.Errorf("previous block hash mismatch: got %s, expected %s",
			header.PrevBlockHash.String(), expectedPrevHash.String())
	}

	// Timestamp validation against previous block
	// Legacy TWINS allows timestamps to be slightly out of order due to clock drift
	// and P2P network delays. We only enforce median time past in ValidateBlockTime.
	// This relaxed check allows historical blocks with minor timestamp inversions.
	// Note: Median time past (checked later) is the primary protection against timestamp manipulation.

	// Version validation
	if err := bv.validateVersion(header); err != nil {
		return fmt.Errorf("version validation failed: %w", err)
	}

	// PoW hash validation for PoW blocks (height <= LastPOWBlock)
	// Legacy: CheckBlockHeader() calls CheckProofOfWork() for PoW blocks (main.cpp:3890-3892)
	if height <= bv.params.LastPOWBlock {
		blockHash := header.Hash()
		if err := CheckProofOfWork(blockHash, header.Bits, bv.params.PowLimitBig); err != nil {
			return fmt.Errorf("proof of work validation failed: %w", err)
		}
	}

	return nil
}

// ValidateBlockTime validates block timing rules
func (bv *BlockValidator) ValidateBlockTime(block *types.Block, medianTime uint32) error {
	header := block.Header

	// Block time must be after median time of last 11 blocks
	if header.Timestamp <= medianTime {
		return fmt.Errorf("block time %d must be after median time %d",
			header.Timestamp, medianTime)
	}

	// Check future time limit - matches legacy main.cpp:3921
	// PoS blocks: 3 minutes, PoW blocks: 2 hours
	now := GetAdjustedTime() // Use network-adjusted time, not system time
	var maxFutureTime uint32
	if block.IsProofOfStake() {
		maxFutureTime = now + 180 // 3 minutes for PoS (legacy: 180 seconds)
	} else {
		maxFutureTime = now + 7200 // 2 hours for PoW (legacy: 7200 seconds)
	}

	if header.Timestamp > maxFutureTime {
		return fmt.Errorf("block time %d is too far in future (max: %d)",
			header.Timestamp, maxFutureTime)
	}

	// Validate minimum time between blocks (if applicable)
	if bv.params.MinBlockInterval > 0 {
		minTime := medianTime + uint32(bv.params.MinBlockInterval.Seconds())
		if header.Timestamp < minTime {
			return fmt.Errorf("block time %d is too early (min: %d)",
				header.Timestamp, minTime)
		}
	}

	return nil
}

// ValidateTarget validates the difficulty target
func (bv *BlockValidator) ValidateTarget(block *types.Block, height uint32) error {
	header := block.Header

	// Validate target is within limits (this is critical)
	target := GetTargetFromBits(header.Bits)
	maxTarget := bv.params.PowLimitBig
	if target.Cmp(maxTarget) > 0 {
		return fmt.Errorf("target exceeds maximum allowed")
	}

	// Validate expected target bits against block header
	expectedBits, err := bv.pos.CalculateNextWorkRequired(header, height)
	if err != nil {
		// With batch-aware blockchain lookups, this should only fail for
		// truly missing blocks (e.g., first blocks during IBD from genesis).
		// Log warning so we can track if this happens unexpectedly.
		bv.pos.logger.WithError(err).WithFields(logrus.Fields{
			"height": height,
			"hash":   header.Hash().String(),
			"bits":   fmt.Sprintf("0x%08x", header.Bits),
		}).Warn("Cannot verify block difficulty - previous block unavailable")
		return nil
	}

	// Legacy allows up to 50% tolerance for blocks before height 68000
	// This handles the historical DigiShield rounding quirk (legacy/src/main.cpp:4068-4086)
	const toleranceHeight = 68000
	if height < toleranceHeight {
		// Calculate 50% tolerance
		actualTarget := GetTargetFromBits(header.Bits)
		expectedTarget := GetTargetFromBits(expectedBits)

		// Calculate difference as percentage
		// Allow if actual is within 50%-150% of expected
		diff := new(big.Int)
		if actualTarget.Cmp(expectedTarget) >= 0 {
			diff.Sub(actualTarget, expectedTarget)
		} else {
			diff.Sub(expectedTarget, actualTarget)
		}

		// Calculate 50% of expected target
		halfExpected := new(big.Int)
		halfExpected.Div(expectedTarget, big.NewInt(2))

		// If difference is <= 50% of expected, accept
		if diff.Cmp(halfExpected) <= 0 {
			return nil
		}

		// Fall through to exact check if tolerance exceeded
	}

	// For blocks >= 68000 or if tolerance exceeded, require exact match
	if header.Bits != expectedBits {
		return fmt.Errorf("difficulty target mismatch at height %d: got %x, expected %x",
			height, header.Bits, expectedBits)
	}

	return nil
}

// stakeModifierCheckpoints contains hardcoded stake modifier checksum checkpoints
// Legacy kernel.cpp:34-35: mapStakeModifierCheckpoints = { (0, 0xfd11f4e7u) }
//
// These checkpoints verify the integrity of the stake modifier chain.
// The checksum at height N depends on:
// - Previous block's checksum (height N-1)
// - Previous block's hashProofOfStake (kernel hash)
// - Previous block's stake modifier
// - Previous block's flags (PoW=0, PoS=1)
//
// Additional checkpoints can be generated from a synced mainnet node by logging
// the checksum values at specific heights (e.g., every 100,000 blocks).
// TODO: Add checkpoints for heights 100000, 200000, etc. from mainnet
var stakeModifierCheckpoints = map[uint32]uint32{
	0: 0xfd11f4e7, // Genesis block checksum
}

// ValidateStakeModifier validates the stake modifier and its checksum
// After successful validation, stores the modifier and checksum in the current block
// for use in subsequent block validation (checksum chaining)
func (bv *BlockValidator) ValidateStakeModifier(ctx *ValidationContext) error {
	header := ctx.Block.Header

	// Get expected modifier
	expectedModifier, isNew, err := bv.pos.modifierCache.ComputeNextStakeModifier(header, ctx.Height)
	if err != nil {
		return fmt.Errorf("failed to compute expected modifier: %w", err)
	}

	// Store the stake modifier in the current block
	// This is equivalent to C++ pindexNew->SetStakeModifier(nStakeModifier, fGeneratedStakeModifier)
	ctx.Block.SetStakeModifier(expectedModifier, isNew)

	// Compute stake modifier checksum (legacy kernel.cpp:428-439)
	// Hash previous checksum with CURRENT block's flags, hashProofOfStake and nStakeModifier
	// C++ uses: ss << pindex->pprev->nStakeModifierChecksum; (previous checksum)
	//           ss << pindex->nFlags << pindex->hashProofOfStake << pindex->nStakeModifier; (CURRENT block!)
	// NOTE: For genesis block, there is no previous checksum
	checksum := computeStakeModifierChecksum(ctx.Height, ctx.Block, ctx.PrevBlock)

	// Store the checksum in the current block for chaining
	// This is equivalent to C++ pindexNew->nStakeModifierChecksum = GetStakeModifierChecksum(pindexNew)
	ctx.Block.SetStakeModifierChecksum(checksum)

	// Verify against hardcoded checkpoints (legacy kernel.cpp:442-448)
	if expectedChecksum, hasCheckpoint := stakeModifierCheckpoints[ctx.Height]; hasCheckpoint {
		if checksum != expectedChecksum {
			return fmt.Errorf("stake modifier checksum mismatch at height %d: got 0x%x, expected 0x%x",
				ctx.Height, checksum, expectedChecksum)
		}
	}

	return nil
}

// computeStakeModifierChecksum computes the stake modifier checksum
// Legacy kernel.cpp:428-439: GetStakeModifierChecksum
// Hash: prevChecksum || flags || hashProofOfStake || modifier
// Then take top 32 bits (hashChecksum >>= (256 - 32))
//
// CRITICAL FIX: The C++ code uses:
//   - pindex->pprev->nStakeModifierChecksum (PREVIOUS block's checksum)
//   - pindex->nFlags (CURRENT block's flags)
//   - pindex->hashProofOfStake (CURRENT block's proof hash)
//   - pindex->nStakeModifier (CURRENT block's modifier)
//
// This function takes both currentBlock (for flags/hashProofOfStake/modifier) and
// prevBlock (for the previous checksum only).
func computeStakeModifierChecksum(height uint32, currentBlock *types.Block, prevBlock *types.Block) uint32 {
	// For genesis block, use initial checksum
	if height == 0 || prevBlock == nil {
		return stakeModifierCheckpoints[0]
	}

	// Legacy C++ serializes: prevChecksum, nFlags, hashProofOfStake, nStakeModifier
	// prevChecksum comes from PREVIOUS block, everything else from CURRENT block

	data := make([]byte, 0, 48) // 4 + 4 + 32 + 8 = 48 bytes

	// Previous checksum (4 bytes, little-endian) - from PREVIOUS block
	prevChecksum := prevBlock.GetStakeModifierChecksum()
	data = append(data, byte(prevChecksum), byte(prevChecksum>>8), byte(prevChecksum>>16), byte(prevChecksum>>24))

	// Flags (4 bytes, little-endian) - from CURRENT block
	// PoS blocks have flag=1 (BLOCK_PROOF_OF_STAKE)
	flags := uint32(0)
	if currentBlock.IsProofOfStake() {
		flags = 1 // BLOCK_PROOF_OF_STAKE flag
	}
	data = append(data, byte(flags), byte(flags>>8), byte(flags>>16), byte(flags>>24))

	// hashProofOfStake (32 bytes) - from CURRENT block
	// Zero for PoW, kernel hash for PoS
	hashProofOfStake := currentBlock.GetHashProofOfStake()
	data = append(data, hashProofOfStake[:]...)

	// Modifier (8 bytes, little-endian) - from CURRENT block
	currentModifier := currentBlock.GetStakeModifier()
	data = append(data,
		byte(currentModifier), byte(currentModifier>>8), byte(currentModifier>>16), byte(currentModifier>>24),
		byte(currentModifier>>32), byte(currentModifier>>40), byte(currentModifier>>48), byte(currentModifier>>56))

	// Hash using double SHA256 (legacy Hash() function) and take top 32 bits
	// Legacy: hashChecksum >>= (256 - 32) which extracts the top 32 bits
	hash := crypto.DoubleHash256(data)
	// Top 32 bits are at the END of the hash (big-endian interpretation after shift)
	// After right-shift by 224, we have the top 32 bits in the least significant position
	// The hash is in little-endian byte order, so top 32 bits are at bytes [28:32]
	checksum := uint32(hash[28]) | uint32(hash[29])<<8 | uint32(hash[30])<<16 | uint32(hash[31])<<24

	return checksum
}

// validateBlockStructure performs basic block structure validation
func (bv *BlockValidator) validateBlockStructure(block *types.Block) error {
	if block.Header == nil {
		return errors.New("block header is nil")
	}

	if len(block.Transactions) == 0 {
		return errors.New("block has no transactions")
	}

	// First transaction must always be coinbase (matches legacy: main.cpp:3966)
	if !block.Transactions[0].IsCoinbase() {
		return errors.New("first transaction must be coinbase")
	}

	// Check for duplicate coinbase (only first transaction should be coinbase)
	for i := 1; i < len(block.Transactions); i++ {
		if block.Transactions[i].IsCoinbase() {
			return fmt.Errorf("block contains second coinbase at index %d", i)
		}
	}

	// PoS-specific checks
	if block.IsProofOfStake() {
		// Second transaction must be coinstake
		if len(block.Transactions) < 2 {
			return errors.New("PoS block must have at least 2 transactions (coinbase + coinstake)")
		}
		if !bv.isCoinstake(block.Transactions[1]) {
			return errors.New("second transaction must be coinstake for PoS blocks")
		}

		// Coinbase must be empty in PoS blocks (only 1 output with 0 value)
		// Legacy: main.cpp:3955-3959
		coinbase := block.Transactions[0]
		if len(coinbase.Outputs) != 1 {
			return fmt.Errorf("PoS coinbase must have exactly 1 output, got %d", len(coinbase.Outputs))
		}
		if coinbase.Outputs[0].Value != 0 {
			return fmt.Errorf("PoS coinbase output must be empty (0 value), got %d", coinbase.Outputs[0].Value)
		}

		// No duplicate coinstake (check all transactions after second)
		// Legacy: main.cpp:3964-3967
		for i := 2; i < len(block.Transactions); i++ {
			if bv.isCoinstake(block.Transactions[i]) {
				return fmt.Errorf("PoS block contains multiple coinstakes at index %d", i)
			}
		}

		// NOTE: Minimum stake OUTPUT validation is performed in ValidateBlock() via
		// validateMinStakeOutput() which has access to the block height from ValidationContext.
		// This is deferred because validateBlockStructure() doesn't have height context.
	} else {
		// PoW-specific checks
		// No coinstake allowed in PoW blocks
		// Legacy: main.cpp:3970
		for i := 1; i < len(block.Transactions); i++ {
			if bv.isCoinstake(block.Transactions[i]) {
				return fmt.Errorf("PoW block cannot contain coinstake at index %d", i)
			}
		}
	}

	// Validate merkle root
	calculatedRoot := types.CalculateMerkleRoot(block.Transactions)
	if block.Header.MerkleRoot != calculatedRoot {
		return fmt.Errorf("merkle root mismatch: got %s, expected %s",
			block.Header.MerkleRoot.String(), calculatedRoot.String())
	}

	// Validate block size
	blockSize := bv.calculateBlockSize(block)
	if blockSize > bv.params.MaxBlockSize {
		return fmt.Errorf("block size %d exceeds maximum %d", blockSize, bv.params.MaxBlockSize)
	}

	return nil
}

// validateProofOfStake validates the PoS proof for the block
// height is passed from ValidationContext to avoid GetBlockHeight lookup on unindexed blocks
// After successful validation, stores the kernel hash (hashProofOfStake) in the block
// for use in stake modifier checksum chaining
func (bv *BlockValidator) validateProofOfStake(block *types.Block, height uint32) error {
	result, err := bv.pos.ValidateProofOfStakeWithHeight(block, height)
	if err != nil {
		return err
	}

	if !result.IsValid {
		return errors.New("proof of stake validation failed")
	}

	// Store the kernel hash in the block for checksum chaining
	// This is equivalent to C++ mapProofOfStake[hash] = hashProofOfStake
	// The kernel hash is later used in GetStakeModifierChecksum()
	block.SetHashProofOfStake(result.KernelHash)

	return nil
}

// validateTransactions validates all transactions in the block
func (bv *BlockValidator) validateTransactions(block *types.Block, height uint32, flags ValidationFlags) error {
	// Track spent outputs within this block to detect double-spends
	spentInBlock := make(map[types.Outpoint]bool)

	blockTime := block.Header.Timestamp

	for i, tx := range block.Transactions {
		// Skip coinbase and coinstake for finality check (they don't have nLockTime restrictions)
		if i > 0 && !(i == 1 && block.IsProofOfStake()) {
			// Check transaction finality (nLockTime and nSequence)
			// Legacy: main.cpp:4176-4184 ContextualCheckBlock
			if !IsFinalTx(tx, height, blockTime) {
				return fmt.Errorf("transaction %d is non-final: locktime=%d, height=%d, time=%d",
					i, tx.LockTime, height, blockTime)
			}
		}

		// Check for double-spends within the block
		for _, input := range tx.Inputs {
			if spentInBlock[input.PreviousOutput] {
				return fmt.Errorf("double-spend detected in block: %s:%d spent multiple times",
					input.PreviousOutput.Hash.String(), input.PreviousOutput.Index)
			}
			spentInBlock[input.PreviousOutput] = true
		}

		// Validate transaction
		isCoinbase := (i == 0)
		isCoinstake := (i == 1 && len(block.Transactions) > 1)
		if err := bv.validateTransaction(tx, isCoinbase || isCoinstake, height, flags); err != nil {
			return fmt.Errorf("transaction %d validation failed: %w", i, err)
		}
	}

	// Check for duplicate transactions
	if err := bv.checkDuplicateTransactions(block.Transactions); err != nil {
		return err
	}

	// Count signature operations (sigops) to prevent DoS attacks
	// Legacy: main.cpp:4056-4063 CheckBlock
	totalSigOps := script.GetBlockSigOpCount(block)

	// Add P2SH sigops and enforce per-transaction limits
	// Legacy: main.cpp:1411,1638,2882 - GetP2SHSigOpCount
	for i := 1; i < len(block.Transactions); i++ {
		tx := block.Transactions[i]

		// Per-transaction sigop limit (legacy: consensus/consensus.h)
		txSigOps := script.GetTransactionSigOpCount(tx)
		if txSigOps > script.MAX_TX_SIGOPS_COUNT {
			return fmt.Errorf("transaction %d exceeds max sigops: %d > %d",
				i, txSigOps, script.MAX_TX_SIGOPS_COUNT)
		}

		// Skip P2SH sigops during batch processing (requires UTXO lookup)
		if flags&SkipInputValidation == 0 {
			// Add P2SH sigops (requires UTXO lookup)
			p2shSigOps := script.GetP2SHSigOpCount(tx, func(op types.Outpoint) (*types.TxOutput, error) {
				// Use blockchain interface if available (for batch cache), otherwise use storage
				var utxo *types.UTXO
				var err error
				if bv.pos.blockchain != nil {
					utxo, err = bv.pos.blockchain.GetUTXO(op)
				} else {
					utxo, err = bv.storage.GetUTXO(op)
				}
				if err != nil {
					return nil, err
				}
				return utxo.Output, nil
			})
			totalSigOps += p2shSigOps
		}
	}

	// Dynamic block sigop limit based on Zerocoin activation
	// Legacy: main.cpp:4060 - nMaxBlockSigOps = fZerocoinActive ? MAX_BLOCK_SIGOPS_CURRENT : MAX_BLOCK_SIGOPS_LEGACY
	zerocoinActive := height >= bv.params.ZerocoinStartHeight
	maxBlockSigOps := script.GetMaxBlockSigOps(zerocoinActive)

	if totalSigOps > maxBlockSigOps {
		return fmt.Errorf("block exceeds max sigops: %d > %d (utilization: %.1f%%, zerocoin: %v)",
			totalSigOps, maxBlockSigOps,
			float64(totalSigOps)/float64(maxBlockSigOps)*100,
			zerocoinActive)
	}

	return nil
}

// validateTransaction validates a single transaction
// Matches legacy CheckInputs() from main.cpp:2192
func (bv *BlockValidator) validateTransaction(tx *types.Transaction, isCoinstake bool, blockHeight uint32, flags ValidationFlags) error {
	if tx == nil {
		return errors.New("transaction is nil")
	}

	// Check if this is a coinbase transaction
	isCoinbase := tx.IsCoinbase()

	// Basic structure validation
	if len(tx.Inputs) == 0 && !isCoinstake && !isCoinbase {
		return errors.New("non-coinstake/non-coinbase transaction has no inputs")
	}

	if len(tx.Outputs) == 0 {
		return errors.New("transaction has no outputs")
	}

	// Skip UTXO lookup and script verification during batch processing
	// MarkUTXOSpent in applyBlockToBatch will verify UTXO existence
	if flags&SkipInputValidation != 0 {
		// Only validate output values
		for _, output := range tx.Outputs {
			if output.Value < 0 {
				return errors.New("transaction output value is negative")
			}
		}
		return nil
	}

	// Validate input/output values and UTXO rules
	inputSum := int64(0)
	outputSum := int64(0)

	// Coinbase transactions don't spend inputs - they mint new coins
	// Skip input validation for coinbase transactions (they have special null inputs)
	if !isCoinbase {
		for i, input := range tx.Inputs {
			// Check if UTXO exists and is unspent (legacy: inputs.HaveInputs)
			// Use blockchain interface if available (for batch cache), otherwise use storage
			var utxo *types.UTXO
			var err error
			if bv.pos.blockchain != nil {
				utxo, err = bv.pos.blockchain.GetUTXO(input.PreviousOutput)
			} else {
				utxo, err = bv.storage.GetUTXO(input.PreviousOutput)
			}
			if err != nil {
				return fmt.Errorf("inputs unavailable for tx %s input %d: UTXO %s:%d - %w",
					tx.Hash().String(), i, input.PreviousOutput.Hash.String(), input.PreviousOutput.Index, err)
			}
			if utxo == nil {
				return fmt.Errorf("UTXO not found (already spent or never existed): tx %s input %d references %s:%d",
					tx.Hash().String(), i, input.PreviousOutput.Hash.String(), input.PreviousOutput.Index)
			}

			// Enforce coinbase/coinstake maturity (legacy: main.cpp:2215-2220)
			// Coinbase and coinstake outputs require maturity blocks before spending
			if utxo.IsCoinbase {
				maturity := bv.params.CoinbaseMaturity
				depth := blockHeight - utxo.Height
				if depth < maturity {
					return fmt.Errorf("tried to spend coinbase/coinstake at depth %d (required: %d)",
						depth, maturity)
				}
			}

			// Skip script checks before last checkpoint (legacy: main.cpp:2716-2783)
			// This significantly speeds up initial sync
			// Note: Checkpoint validation is now handled by blockchain layer
			// For now, we'll validate all scripts (safer but slightly slower)

			// Verify script/signature (legacy: main.cpp:2252-2285 CheckInputs)
			// Legacy C++ verifies ALL inputs for ALL transactions including coinstake.
			// The loop in main.cpp:2253 iterates: for (unsigned int i = 0; i < tx.vin.size(); i++)
			// This is critical for consensus - multi-input coinstake with invalid signatures
			// on inputs 1+ would be accepted by Go but rejected by C++.
			scriptSig := input.ScriptSig
			scriptPubKey := utxo.Output.ScriptPubKey

			// Verify script execution for ALL inputs (legacy compliance)
			if err := script.VerifyScript(scriptSig, scriptPubKey, tx, i, script.StandardScriptVerifyFlags); err != nil {
				if isCoinstake {
					return fmt.Errorf("coinstake input %d signature verification failed: %w", i, err)
				}
				return fmt.Errorf("script verification failed for input %d: %w", i, err)
			}

			// Validate input value (legacy: main.cpp:2222-2226)
			if utxo.Output.Value < 0 {
				return errors.New("input value out of range (negative)")
			}
			inputSum += utxo.Output.Value
			if inputSum < 0 {
				return errors.New("input values overflow")
			}
		}
	} // end if !isCoinbase

	// Validate output values
	for _, output := range tx.Outputs {
		if output.Value < 0 {
			return errors.New("negative output value")
		}
		outputSum += output.Value
		if outputSum < 0 {
			return errors.New("output values overflow")
		}
	}

	// For non-coinstake and non-coinbase transactions, inputs must cover outputs + fees
	// (legacy: main.cpp:2229-2243)
	// Coinbase and coinstake transactions can mint new coins, so skip this check
	if !isCoinstake && !isCoinbase {
		if inputSum < outputSum {
			return fmt.Errorf("value in (%d) < value out (%d)", inputSum, outputSum)
		}

		// Validate transaction fee
		txFee := inputSum - outputSum
		if txFee < 0 {
			return errors.New("transaction fee is negative")
		}
		// Note: Could add max fee check here if needed
	}

	return nil
}

// validateBlockSignature validates the block signature
// Matches legacy: blocksignature.cpp:58 CheckBlockSignature()
func (bv *BlockValidator) validateBlockSignature(block *types.Block) error {
	// PoW blocks must have empty signature (legacy: blocksignature.cpp:60-61)
	if len(block.Transactions) == 1 {
		if len(block.Signature) != 0 {
			return errors.New("PoW block must have empty signature")
		}
		return nil
	}

	// PoS blocks must have signature (legacy: blocksignature.cpp:63-64)
	if len(block.Signature) == 0 {
		return errors.New("PoS block signature is missing")
	}

	// For PoS blocks, second transaction is coinstake (we validated this in validateBlockStructure)
	coinstake := block.Transactions[1]

	// Extract public key from coinstake vout[1].scriptPubKey
	// Legacy: blocksignature.cpp:76-84
	if len(coinstake.Outputs) < 2 {
		return errors.New("coinstake must have at least 2 outputs")
	}

	scriptPubKey := coinstake.Outputs[1].ScriptPubKey

	// Parse the scriptPubKey to extract public key
	// Simplified script parsing (full Bitcoin script interpreter would be Task 16)
	// We support two standard types:
	// 1. TX_PUBKEY: <pubkey> OP_CHECKSIG (pubkey at start)
	// 2. TX_PUBKEYHASH: OP_DUP OP_HASH160 <pubkeyhash> OP_EQUALVERIFY OP_CHECKSIG

	var pubKeyBytes []byte
	if len(scriptPubKey) == 35 && scriptPubKey[0] == 33 {
		// TX_PUBKEY compressed (33-byte pubkey + OP_CHECKSIG)
		pubKeyBytes = scriptPubKey[1:34]
	} else if len(scriptPubKey) == 67 && scriptPubKey[0] == 65 {
		// TX_PUBKEY uncompressed (65-byte pubkey + OP_CHECKSIG)
		pubKeyBytes = scriptPubKey[1:66]
	} else if script.IsP2PKH(scriptPubKey) {
		// LEGACY COMPLIANCE: P2PKH coinstake outputs are NOT supported by C++ nodes
		// Legacy blocksignature.cpp:76-84 calls Solver() which returns pubkeyhash (20 bytes)
		// for TX_PUBKEYHASH type, then CPubKey(vchPubKey) with 20-byte hash creates
		// invalid pubkey → pubkey.IsValid() = false → block rejected.
		// Go nodes MUST reject P2PKH coinstake outputs to maintain consensus.
		// Coinstake outputs MUST use P2PK (pay-to-pubkey) format.
		return errors.New("P2PKH coinstake outputs not supported - must use P2PK (legacy compliance)")
	} else {
		return fmt.Errorf("unsupported scriptPubKey format (len=%d)", len(scriptPubKey))
	}

	// Parse public key
	pubKey, err := crypto.ParsePublicKeyFromBytes(pubKeyBytes)
	if err != nil {
		return fmt.Errorf("failed to parse public key: %w", err)
	}

	// Verify signature against block hash
	// Legacy: blocksignature.cpp:90 - pubkey.Verify(block.GetHash(), block.vchBlockSig)
	blockHash := block.Header.Hash()

	// Verify signature using strict DER format (legacy compatibility)
	// CRITICAL: Must use VerifyDERSignature, not VerifySignature!
	// Legacy C++ only accepts DER format, so Go nodes must also reject
	// 64-byte R||S signatures to maintain consensus compatibility.
	if !crypto.VerifyDERSignature(pubKey, blockHash[:], block.Signature) {
		return errors.New("invalid block signature (must be DER format)")
	}

	return nil
}

// validateGenesisHeader validates the genesis block header
func (bv *BlockValidator) validateGenesisHeader(header *types.BlockHeader) error {
	// Genesis block specific validations
	// Height is already validated at the caller level

	if !header.PrevBlockHash.IsZero() {
		return errors.New("genesis block must have zero previous block hash")
	}

	// Validate genesis timestamp
	if header.Timestamp != bv.params.GenesisTimestamp {
		return fmt.Errorf("invalid genesis timestamp: got %d, expected %d",
			header.Timestamp, bv.params.GenesisTimestamp)
	}

	return nil
}

// validateVersion validates the block version
func (bv *BlockValidator) validateVersion(header *types.BlockHeader) error {
	// Validate version based on height and network parameters
	if header.Version < bv.params.MinBlockVersion {
		return fmt.Errorf("block version %d below minimum %d",
			header.Version, bv.params.MinBlockVersion)
	}

	return nil
}

// isCoinstake checks if a transaction is a coinstake transaction
func (bv *BlockValidator) isCoinstake(tx *types.Transaction) bool {
	// Coinstake has special characteristics:
	// 1. First input spends a regular UTXO (for stake)
	// 2. First output is empty (value = 0)
	// 3. Subsequent outputs contain stake reward

	if len(tx.Inputs) == 0 || len(tx.Outputs) == 0 {
		return false
	}

	// First output should be empty for coinstake
	return tx.Outputs[0].Value == 0
}

// calculateBlockSize calculates the serialized size of a block
func (bv *BlockValidator) calculateBlockSize(block *types.Block) uint32 {
	// Simplified calculation - would use proper serialization in full implementation
	size := uint32(0)

	const serializedHeaderSize = 80
	const serializedTxCountSize = 4

	size += serializedHeaderSize
	size += serializedTxCountSize

	// Transactions
	for _, tx := range block.Transactions {
		size += bv.calculateTransactionSize(tx)
	}

	return size
}

// calculateTransactionSize calculates the serialized size of a transaction
func (bv *BlockValidator) calculateTransactionSize(tx *types.Transaction) uint32 {
	// Simplified calculation
	size := uint32(0)

	const (
		serializedTxBaseSize     = 8  // version(4) + locktime(4)
		serializedCountSize      = 4  // varint for input/output count
		serializedInputBaseSize  = 36 // outpoint hash(32) + index(4) (simplified estimate)
		serializedOutputBaseSize = 8  // value(8)
	)

	size += serializedTxBaseSize

	// Input count + inputs
	size += serializedCountSize
	size += uint32(len(tx.Inputs)) * serializedInputBaseSize

	// Output count + outputs
	size += serializedCountSize
	size += uint32(len(tx.Outputs)) * serializedOutputBaseSize

	// Add script sizes (estimated)
	for _, input := range tx.Inputs {
		size += uint32(len(input.ScriptSig))
	}
	for _, output := range tx.Outputs {
		size += uint32(len(output.ScriptPubKey))
	}

	return size
}

// checkDuplicateTransactions checks for duplicate transactions in a block
func (bv *BlockValidator) checkDuplicateTransactions(transactions []*types.Transaction) error {
	seen := make(map[types.Hash]bool)

	for _, tx := range transactions {
		hash := tx.Hash()
		if seen[hash] {
			return fmt.Errorf("duplicate transaction: %s", hash.String())
		}
		seen[hash] = true
	}

	return nil
}


// CalculateBlockReward calculates the block reward for a given height.
// This implements the TWINS block reward schedule matching legacy GetBlockValue()
func (bv *BlockValidator) CalculateBlockReward(height uint32) int64 {
	return GetBlockValue(height)
}

// GetBlockValue returns the block reward for a given height (standalone utility)
// This implements the legacy TWINS reward schedule from legacy/src/main.cpp:1889-1970
func GetBlockValue(height uint32) int64 {
	const COIN = 100000000 // 1 TWINS = 100000000 satoshis

	// First block with initial pre-mine (from legacy/src/main.cpp:1899-1900)
	if height == 1 {
		return 6000000 * COIN // 6 million TWINS premine
	}

	// Legacy TWINS reward schedule (from legacy/src/main.cpp:1902-1905)
	// Release 15220.70 TWINS as reward until block 711111
	if height < 711111 {
		return int64(15220.70 * COIN)
	}

	// Phased reduction schedule
	switch {
	case height < 716666:
		return 8000 * COIN
	case height < 722222:
		return 4000 * COIN
	case height < 727777:
		return 2000 * COIN
	case height < 733333:
		return 1000 * COIN
	case height < 738888:
		return 500 * COIN
	case height < 744444:
		return 250 * COIN
	case height < 750000:
		return 125 * COIN
	case height < 755555:
		return 60 * COIN
	case height < 761111:
		return 30 * COIN
	case height < 766666:
		return 15 * COIN
	case height < 772222:
		return 8 * COIN
	case height < 777777:
		return 4 * COIN
	case height < 910000:
		return 2 * COIN
	case height < 6569605:
		return 100 * COIN
	default:
		return 0 // No more rewards after block 6569605
	}
}

// validateBlockValue validates that the minted value doesn't exceed expected reward
// Implements legacy IsBlockValueValid logic (from legacy/src/masternode-payments.cpp:176-216)
// with spork-aware superblock support
func (bv *BlockValidator) validateBlockValue(block *types.Block, height uint32, expectedValue int64) error {
	if len(block.Transactions) == 0 {
		return fmt.Errorf("block has no transactions")
	}

	// Calculate total minted value in coinbase/coinstake
	// For PoS, nMint = (sum outputs) - (sum inputs) matching legacy main.cpp:2926
	var mintedValue int64
	var totalFees int64

	if block.IsProofOfWork() {
		// PoW: First transaction is coinbase
		for _, output := range block.Transactions[0].Outputs {
			mintedValue += output.Value
		}

		// Calculate transaction fees from non-coinbase transactions
		// Legacy: nFees += view.GetValueIn(tx) - tx.GetValueOut() (main.cpp:2791-2795)
		for i := 1; i < len(block.Transactions); i++ {
			tx := block.Transactions[i]

			// Calculate input sum
			var inputSum int64
			for _, input := range tx.Inputs {
				// Use blockchain interface if available (for batch cache), otherwise use storage
				var utxo *types.UTXO
				var err error
				if bv.pos.blockchain != nil {
					utxo, err = bv.pos.blockchain.GetUTXO(input.PreviousOutput)
				} else {
					utxo, err = bv.storage.GetUTXO(input.PreviousOutput)
				}
				if err != nil {
					return fmt.Errorf("failed to get input UTXO for fee calculation: %w", err)
				}
				inputSum += utxo.Output.Value
			}

			// Calculate output sum
			var outputSum int64
			for _, output := range tx.Outputs {
				outputSum += output.Value
			}

			// Fee = inputs - outputs
			totalFees += (inputSum - outputSum)
		}
	} else {
		// PoS: Second transaction is coinstake (first is empty coinbase)
		if len(block.Transactions) < 2 {
			return fmt.Errorf("PoS block must have at least 2 transactions")
		}

		coinstake := block.Transactions[1]

		// Sum outputs
		var outputSum int64
		for _, output := range coinstake.Outputs {
			outputSum += output.Value
		}

		// Sum inputs - need to get input values from UTXO set
		var inputSum int64
		for _, input := range coinstake.Inputs {
			// Look up the UTXO being spent
			// Use blockchain interface if available (for batch cache), otherwise use storage
			var utxo *types.UTXO
			var err error
			if bv.pos.blockchain != nil {
				utxo, err = bv.pos.blockchain.GetUTXO(input.PreviousOutput)
			} else {
				utxo, err = bv.storage.GetUTXO(input.PreviousOutput)
			}
			if err != nil {
				return fmt.Errorf("failed to get coinstake input UTXO: %w", err)
			}
			inputSum += utxo.Output.Value
		}

		// nMint = outputs - inputs (fees are destroyed in PoS)
		mintedValue = outputSum - inputSum
	}

	// Spork-aware superblock handling (matching legacy IsBlockValueValid)
	isSynced := bv.isSynced != nil && bv.isSynced()

	if !isSynced {
		// During sync, allow superblocks in the first 100 blocks of each budget cycle
		if bv.budgetManager != nil {
			cycleBlocks := bv.budgetManager.GetBudgetPaymentCycleBlocks()
			if cycleBlocks > 0 && (height%cycleBlocks) < 100 {
				return nil // Allow any value for potential superblocks during sync
			}
		}
		// Not in superblock window - enforce limit
		// For PoW blocks, allow base reward + fees (legacy: main.cpp:2791-2795)
		maxAllowed := expectedValue
		if block.IsProofOfWork() {
			maxAllowed = expectedValue + totalFees
		}
		if mintedValue > maxAllowed {
			if block.IsProofOfWork() && totalFees > 0 {
				return fmt.Errorf("block mints too much: got %d satoshis, expected max %d satoshis (base %d + fees %d, height %d)",
					mintedValue, maxAllowed, expectedValue, totalFees, height)
			}
			return fmt.Errorf("block mints too much: got %d satoshis, expected max %d satoshis (height %d)",
				mintedValue, maxAllowed, height)
		}
	} else {
		// Node is synced - check budget schedule with spork support
		// SPORK_13_ENABLE_SUPERBLOCKS controls whether superblocks are active
		if bv.sporkManager != nil && bv.sporkManager.IsActive(SporkEnableSuperblocks) {
			// Superblocks are enabled - check if this is a budget payment block
			if bv.budgetManager != nil && bv.budgetManager.IsBudgetPaymentBlock(height) {
				// Budget payment block - value is validated in CheckBlock (budget manager)
				return nil
			}
		}

		// Not a superblock (or superblocks disabled) - enforce expected value limit
		// For PoW blocks, allow base reward + fees (legacy: main.cpp:2791-2795)
		maxAllowed := expectedValue
		if block.IsProofOfWork() {
			maxAllowed = expectedValue + totalFees
		}
		if mintedValue > maxAllowed {
			if block.IsProofOfWork() && totalFees > 0 {
				return fmt.Errorf("block mints too much: got %d satoshis, expected max %d satoshis (base %d + fees %d, height %d)",
					mintedValue, maxAllowed, expectedValue, totalFees, height)
			}
			return fmt.Errorf("block mints too much: got %d satoshis, expected max %d satoshis (height %d)",
				mintedValue, maxAllowed, height)
		}
	}

	return nil
}

// validateDevFundPayment validates the development fund payout
// Implements legacy dev fund validation (from legacy/src/main.cpp:2948-2966)
func (bv *BlockValidator) validateDevFundPayment(block *types.Block, height uint32, blockReward int64) error {
	// Dev fund starts at height 2 (legacy: pindex->pprev->nHeight >= 2, which means current height >= 2)
	if height < 2 {
		return nil // No dev fund required before height 2
	}

	// LEGACY COMPATIBILITY: Dev fund only paid on PoS blocks
	// Legacy FillBlockPayee() at masternode-payments.cpp:325-362 only adds dev output
	// in the "if (fProofOfStake)" branch (lines 336-341). The PoW else branch (357-362)
	// does NOT add any dev reward output.
	// VERIFIED: PoW blocks (heights 1-400 on mainnet) do not have dev fund payments.
	// This is correct legacy behavior - dev fund was introduced with PoS.
	if block.IsProofOfWork() {
		return nil // Skip dev fund validation for PoW blocks - legacy never pays dev on PoW
	}

	// Calculate dev reward: 10% of block reward (legacy line 2950)
	devReward := (blockReward * 10) / 100

	if devReward <= 0 {
		return nil // No dev reward if blockReward is 0
	}

	// Get dev fund scriptPubKey from chain params
	if len(bv.params.DevAddress) == 0 {
		return fmt.Errorf("dev fund address not configured in chain params")
	}
	devScript := bv.params.DevAddress

	// For PoS blocks, check coinstake transaction
	if len(block.Transactions) < 2 {
		return fmt.Errorf("PoS block must have at least 2 transactions")
	}
	txToCheck := block.Transactions[1] // Coinstake

	// Check that dev fund output exists with correct amount and scriptPubKey (legacy lines 2956-2960)
	devPaid := false
	for _, output := range txToCheck.Outputs {
		if output.Value == devReward && bytes.Equal(output.ScriptPubKey, devScript) {
			devPaid = true
			break
		}
	}

	if !devPaid {
		return fmt.Errorf("no dev reward: expected %d satoshis to dev fund script at height %d",
			devReward, height)
	}

	return nil
}


// validateMinStakeOutput validates minimum stake OUTPUT amount with spork+height gate
// Legacy: main.cpp:3975-3979 - CheckBlock min stake validation
// CRITICAL: This validates the OUTPUT (vout[1].Value), NOT the input amount
// C++ only validates output amount at consensus level; input amount is wallet-level only
func (bv *BlockValidator) validateMinStakeOutput(block *types.Block, height uint32) error {
	// Spork ID for minimum stake amount enforcement
	const SPORK_TWINS_02_MIN_STAKE_AMOUNT = int32(20190002)

	// Height thresholds from legacy main.cpp:3976
	// Mainnet: 333500, Testnet: 192500
	const minStakeHeightMainnet = uint32(333500)
	const minStakeHeightTestnet = uint32(192500)

	// LEGACY COMPLIANCE: Legacy C++ gates this rule on
	// IsSporkActive(SPORK_TWINS_02_MIN_STAKE_AMOUNT). On mainnet the spork was
	// activated via a signed broadcast long ago and every legacy mainnet node
	// enforces the rule; a fresh Go node may never have received that
	// broadcast, so the spork stays at its OFF default and the check is
	// silently skipped -- letting invalid stakes through (see block
	// fbf23a39... accepted by Go, rejected by legacy).
	//
	// Fix: on mainnet, enforce the rule unconditionally past the legacy
	// height gate. This cannot diverge from legacy mainnet because legacy
	// mainnet already enforces it. On testnet/regtest we keep the spork gate
	// because we do not know whether those networks ever activated the
	// spork; forcing enforcement there could fork testnet.
	if bv.params.Name != "mainnet" {
		sporkActive := false
		if bv.sporkManager != nil {
			sporkActive = bv.sporkManager.IsActive(SPORK_TWINS_02_MIN_STAKE_AMOUNT)
		}
		if !sporkActive {
			return nil
		}
	}

	// Check height threshold based on network
	// Legacy: nHeight >= (Params().NetworkID() == CBaseChainParams::MAIN ? 333500 : 192500)
	var minStakeHeight uint32
	if bv.params.Name == "mainnet" {
		minStakeHeight = minStakeHeightMainnet
	} else {
		minStakeHeight = minStakeHeightTestnet
	}

	if height < minStakeHeight {
		return nil
	}

	// Validate coinstake exists and has at least 2 outputs
	if len(block.Transactions) < 2 {
		return errors.New("PoS block missing coinstake transaction")
	}
	coinstake := block.Transactions[1]
	if len(coinstake.Outputs) < 2 {
		return errors.New("coinstake has insufficient outputs for min stake validation")
	}

	// Legacy: block.vtx[1].vout[1].nValue < Params().StakingMinInput()
	// Note: Despite the name "StakingMinInput", this is checking the OUTPUT value
	stakeOutputValue := coinstake.Outputs[1].Value
	minStakeAmount := bv.params.MinStakeAmount

	if stakeOutputValue < minStakeAmount {
		return fmt.Errorf("stake output value %d below minimum %d at height %d",
			stakeOutputValue, minStakeAmount, height)
	}

	return nil
}

// extractPubKeyFromScriptSig extracts the public key from a P2PKH scriptSig
// P2PKH scriptSig format: <sig> <pubkey>
// where <sig> is DER-encoded signature and <pubkey> is compressed (33) or uncompressed (65) pubkey
func extractPubKeyFromScriptSig(scriptSig []byte) ([]byte, error) {
	if len(scriptSig) == 0 {
		return nil, fmt.Errorf("empty scriptSig")
	}

	pc := 0
	var pubKeyBytes []byte

	// Parse scriptSig opcodes to find the pubkey
	// First element is signature, second is pubkey
	elementCount := 0

	for pc < len(scriptSig) {
		opcode := scriptSig[pc]
		pc++

		// Handle data push opcodes
		var dataLen int
		if opcode >= 0x01 && opcode <= 0x4b {
			// Direct push of N bytes
			dataLen = int(opcode)
		} else if opcode == script.OP_PUSHDATA1 {
			if pc >= len(scriptSig) {
				return nil, fmt.Errorf("OP_PUSHDATA1: missing length byte")
			}
			dataLen = int(scriptSig[pc])
			pc++
		} else if opcode == script.OP_PUSHDATA2 {
			if pc+1 >= len(scriptSig) {
				return nil, fmt.Errorf("OP_PUSHDATA2: missing length bytes")
			}
			dataLen = int(scriptSig[pc]) | (int(scriptSig[pc+1]) << 8)
			pc += 2
		} else {
			// Skip non-push opcodes
			continue
		}

		if pc+dataLen > len(scriptSig) {
			return nil, fmt.Errorf("data exceeds scriptSig length")
		}

		data := scriptSig[pc : pc+dataLen]
		pc += dataLen

		elementCount++

		// Second element is the pubkey
		if elementCount == 2 {
			// Verify it looks like a pubkey (33 or 65 bytes)
			if len(data) == 33 || len(data) == 65 {
				pubKeyBytes = data
				break
			} else {
				return nil, fmt.Errorf("second element is not a valid pubkey (len=%d)", len(data))
			}
		}
	}

	if pubKeyBytes == nil {
		return nil, fmt.Errorf("pubkey not found in scriptSig (found %d elements)", elementCount)
	}

	return pubKeyBytes, nil
}
