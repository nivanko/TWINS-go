package wallet

import (
	"encoding/hex"
	"fmt"

	"github.com/twins-dev/twins-core/pkg/crypto"
	"github.com/twins-dev/twins-core/pkg/types"
)

const (
	// DefaultFeePerKB is the default fee per kilobyte
	DefaultFeePerKB = 10000

	// MinRelayFee is the minimum fee to relay transaction (1000 satoshis = 0.00001 TWINS)
	MinRelayFee = 1000

	// MaxReasonableFeeRate is the maximum reasonable fee (10% of total output value)
	// This prevents accidentally overpaying due to bugs
	MaxReasonableFeePercent = 10

	// MaxStandardTxSize is the maximum size of a standard transaction in bytes.
	// Legacy: MAX_STANDARD_TX_SIZE = 100000 (main.h:70)
	MaxStandardTxSize = 100_000

	// CentThreshold is 0.01 TWINS in satoshis, used by knapsack for change avoidance.
	// Legacy: CENT = 1000000 (util.h)
	CentThreshold int64 = 1_000_000

	// MaxPendingTransactions caps the number of pending (mempool) wallet transactions
	// tracked in memory. Prevents resource exhaustion from P2P dust-spam attacks
	// against a known wallet address. Bounded by mempool's own 50k cap, but this
	// provides a tighter wallet-level limit.
	MaxPendingTransactions = 1000

	// TX size estimation constants for P2PKH transactions.
	// Used in fee estimation, pre-sign size checks, and UTXO selection.
	bytesPerInput  = 190 // Estimated serialized size of one signed P2PKH input (DER sig + pubkey + opcodes)
	bytesPerOutput = 34  // Estimated serialized size of one P2PKH output
	txBaseOverhead = 10  // Fixed overhead for version, locktime, and varint counts
)

// SendOptions contains options for sending transactions with advanced features
type SendOptions struct {
	// SelectedUTXOs specifies which UTXOs to use (coin control)
	// If nil or empty, UTXOs will be automatically selected
	SelectedUTXOs []types.Outpoint

	// ChangeAddress overrides the default change address
	// If empty, a new change address will be generated
	ChangeAddress string

	// SplitCount splits the output into multiple UTXOs of equal value
	// Useful for staking optimization. Value of 1 or 0 means no split.
	SplitCount int

	// FeeRate is the user-selected fee rate in satoshis per kilobyte
	// If 0, uses wallet's default fee rate (w.config.FeePerKB)
	FeeRate int64
}

// SendToAddress sends TWINS to a single address
func (w *Wallet) SendToAddress(address string, amount int64, comment string, subtractFee bool) (string, error) {
	if w.IsLocked() {
		return "", fmt.Errorf("wallet is locked")
	}
	if w.IsUnlockedForStakingOnly() {
		return "", fmt.Errorf("wallet is unlocked for staking only")
	}

	if w.blockchain == nil {
		return "", fmt.Errorf("blockchain not set - call SetBlockchain first")
	}

	if w.mempool == nil {
		return "", fmt.Errorf("mempool not set - call SetMempool first")
	}

	// Validate address
	validation, err := w.ValidateAddress(address)
	if err != nil || !validation.IsValid {
		return "", fmt.Errorf("invalid address: %s", address)
	}

	// Validate amount
	if amount <= 0 {
		return "", fmt.Errorf("invalid amount: %d", amount)
	}

	// Create single-recipient map
	recipients := map[string]int64{
		address: amount,
	}

	// Use SendMany for the actual transaction creation
	return w.SendMany(recipients, comment)
}

// SendMany sends TWINS to multiple addresses in one transaction
func (w *Wallet) SendMany(recipients map[string]int64, comment string) (string, error) {
	if w.IsLocked() {
		return "", fmt.Errorf("wallet is locked")
	}
	if w.IsUnlockedForStakingOnly() {
		return "", fmt.Errorf("wallet is unlocked for staking only")
	}

	if w.blockchain == nil {
		return "", fmt.Errorf("blockchain not set - call SetBlockchain first")
	}

	if w.mempool == nil {
		return "", fmt.Errorf("mempool not set - call SetMempool first")
	}

	// Validate recipients
	if len(recipients) == 0 {
		return "", fmt.Errorf("no recipients specified")
	}

	totalAmount := int64(0)
	for addr, amount := range recipients {
		validation, err := w.ValidateAddress(addr)
		if err != nil || !validation.IsValid {
			return "", fmt.Errorf("invalid address: %s", addr)
		}
		if amount <= 0 {
			return "", fmt.Errorf("invalid amount for address %s: %d", addr, amount)
		}
		totalAmount += amount
	}

	// Estimate fee (will be refined during transaction building)
	estimatedSize := bytesPerInput*3 + bytesPerOutput*len(recipients) + bytesPerOutput // Rough estimate
	estimatedFee := int64(estimatedSize) * w.config.FeePerKB / 1000

	// Select UTXOs
	targetAmount := totalAmount + estimatedFee
	selectedUTXOs, err := w.SelectUTXOs(targetAmount, w.config.MinConfirmations, len(recipients)+1)
	if err != nil {
		w.logger.WithError(err).WithField("target", targetAmount).Error("SendMany: UTXO selection failed")
		return "", fmt.Errorf("failed to select UTXOs: %w", err)
	}

	// Build transaction
	tx, actualFee, err := w.BuildTransaction(selectedUTXOs, recipients)
	if err != nil {
		w.logger.WithError(err).WithField("inputs", len(selectedUTXOs)).Error("SendMany: build failed")
		return "", fmt.Errorf("failed to build transaction: %w", err)
	}

	// Sign transaction
	signedTx, err := w.SignTransaction(tx)
	if err != nil {
		w.logger.WithError(err).Error("SendMany: sign failed")
		return "", fmt.Errorf("failed to sign transaction: %w", err)
	}

	// Post-sign size check: verify actual serialized size after signatures are added.
	// Legacy: MAX_STANDARD_TX_SIZE = 100000 (main.h:70)
	actualSize := signedTx.SerializeSize()
	if actualSize > MaxStandardTxSize {
		w.logger.WithFields(map[string]interface{}{
			"size": actualSize, "max": MaxStandardTxSize,
		}).Error("SendMany: signed TX exceeds size limit")
		return "", fmt.Errorf("signed transaction size %d bytes exceeds maximum %d", actualSize, MaxStandardTxSize)
	}

	// Store comment before broadcast so OnMempoolTransaction can read it
	if comment != "" {
		txHash := signedTx.Hash()
		w.mu.Lock()
		if w.sentTxComments == nil {
			w.sentTxComments = make(map[types.Hash]string)
		}
		w.sentTxComments[txHash] = comment
		w.mu.Unlock()
	}

	// Broadcast transaction to the network
	broadcasted, err := w.broadcastTransaction(signedTx)
	if err != nil {
		return "", err
	}

	txHash := signedTx.Hash()
	w.logger.WithFields(map[string]interface{}{
		"tx_hash":     txHash.String(),
		"recipients":  len(recipients),
		"amount":      totalAmount,
		"fee":         actualFee,
		"broadcasted": broadcasted,
	}).Info("Transaction sent successfully")

	return txHash.String(), nil
}

// SendManyWithOptions sends TWINS to multiple addresses with advanced options
// Supports coin control (specific UTXO selection), custom change address, and UTXO splitting
func (w *Wallet) SendManyWithOptions(recipients map[string]int64, comment string, opts *SendOptions) (string, error) {
	if w.IsLocked() {
		return "", fmt.Errorf("wallet is locked")
	}
	if w.IsUnlockedForStakingOnly() {
		return "", fmt.Errorf("wallet is unlocked for staking only")
	}

	if w.blockchain == nil {
		return "", fmt.Errorf("blockchain not set - call SetBlockchain first")
	}

	if w.mempool == nil {
		return "", fmt.Errorf("mempool not set - call SetMempool first")
	}

	// Validate recipients
	if len(recipients) == 0 {
		return "", fmt.Errorf("no recipients specified")
	}

	// Handle UTXO split: multiply outputs to same address
	finalRecipients := recipients
	if opts != nil && opts.SplitCount > 1 {
		finalRecipients = make(map[string]int64)
		for addr, amount := range recipients {
			// Split the amount into equal parts
			splitAmount := amount / int64(opts.SplitCount)
			dustThreshold := types.GetDustThreshold(types.DefaultMinRelayTxFee)
			if splitAmount < dustThreshold {
				return "", fmt.Errorf("split amount %d is below dust threshold %d", splitAmount, dustThreshold)
			}
			// For split, we need to create multiple outputs to the same address
			// Since map can't have duplicate keys, we'll handle this in BuildTransactionWithOptions
			finalRecipients[addr] = amount
		}
	}

	totalAmount := int64(0)
	for addr, amount := range finalRecipients {
		validation, err := w.ValidateAddress(addr)
		if err != nil || !validation.IsValid {
			return "", fmt.Errorf("invalid address: %s", addr)
		}
		if amount <= 0 {
			return "", fmt.Errorf("invalid amount for address %s: %d", addr, amount)
		}
		totalAmount += amount
	}

	// Select UTXOs - either from options (coin control) or automatically
	var selectedUTXOs []*UTXO
	var err error

	if opts != nil && len(opts.SelectedUTXOs) > 0 {
		// Coin control: use specified UTXOs
		selectedUTXOs, err = w.GetUTXOsByOutpoints(opts.SelectedUTXOs)
		if err != nil {
			return "", fmt.Errorf("failed to get selected UTXOs: %w", err)
		}

		// Verify total value of selected UTXOs
		totalInput := int64(0)
		for _, utxo := range selectedUTXOs {
			totalInput += utxo.Output.Value
		}

		// Rough fee estimate using same formula as BuildTransactionWithOptions:
		// bytesPerInput/input + bytesPerOutput/output + change + txBaseOverhead
		preCheckFeeRate := w.config.FeePerKB
		if opts.FeeRate > 0 {
			preCheckFeeRate = opts.FeeRate
		}
		estimatedSize := bytesPerInput*len(selectedUTXOs) + bytesPerOutput*(len(finalRecipients)+1) + txBaseOverhead
		estimatedFee := int64(estimatedSize) * preCheckFeeRate / 1000
		minFee := max(w.config.MinTxFee, MinRelayFee)
		estimatedFee = max(estimatedFee, minFee)

		if totalInput < totalAmount+estimatedFee {
			return "", fmt.Errorf("selected UTXOs insufficient: need %d, have %d",
				totalAmount+estimatedFee, totalInput)
		}
	} else {
		// Automatic UTXO selection
		estimatedSize := bytesPerInput*3 + bytesPerOutput*len(finalRecipients) + bytesPerOutput
		estimatedFee := int64(estimatedSize) * w.config.FeePerKB / 1000
		targetAmount := totalAmount + estimatedFee

		selectedUTXOs, err = w.SelectUTXOs(targetAmount, w.config.MinConfirmations, len(finalRecipients)+1)
		if err != nil {
			w.logger.WithError(err).WithField("target", targetAmount).Error("SendManyWithOptions: UTXO selection failed")
			return "", fmt.Errorf("failed to select UTXOs: %w", err)
		}
	}

	// Build transaction with options
	tx, actualFee, err := w.BuildTransactionWithOptions(selectedUTXOs, finalRecipients, opts)
	if err != nil {
		w.logger.WithError(err).WithField("inputs", len(selectedUTXOs)).Error("SendManyWithOptions: build failed")
		return "", fmt.Errorf("failed to build transaction: %w", err)
	}

	// Sign transaction
	signedTx, err := w.SignTransaction(tx)
	if err != nil {
		w.logger.WithError(err).Error("SendManyWithOptions: sign failed")
		return "", fmt.Errorf("failed to sign transaction: %w", err)
	}

	// Post-sign size check: verify actual serialized size after signatures are added.
	// Legacy: MAX_STANDARD_TX_SIZE = 100000 (main.h:70)
	actualSize := signedTx.SerializeSize()
	if actualSize > MaxStandardTxSize {
		w.logger.WithFields(map[string]interface{}{
			"size": actualSize, "max": MaxStandardTxSize,
		}).Error("SendManyWithOptions: signed TX exceeds size limit")
		return "", fmt.Errorf("signed transaction size %d bytes exceeds maximum %d", actualSize, MaxStandardTxSize)
	}

	// Store comment before broadcast so OnMempoolTransaction can read it
	if comment != "" {
		txHash := signedTx.Hash()
		w.mu.Lock()
		if w.sentTxComments == nil {
			w.sentTxComments = make(map[types.Hash]string)
		}
		w.sentTxComments[txHash] = comment
		w.mu.Unlock()
	}

	// Broadcast transaction to the network
	broadcasted, err := w.broadcastTransaction(signedTx)
	if err != nil {
		return "", err
	}

	txHash := signedTx.Hash()
	splitCount := 0
	if opts != nil {
		splitCount = opts.SplitCount
	}
	w.logger.WithFields(map[string]interface{}{
		"tx_hash":       txHash.String(),
		"broadcasted":   broadcasted,
		"recipients":    len(recipients),
		"amount":        totalAmount,
		"fee":           actualFee,
		"coin_control":  opts != nil && len(opts.SelectedUTXOs) > 0,
		"custom_change": opts != nil && opts.ChangeAddress != "",
		"split_count":   splitCount,
	}).Info("Transaction sent successfully with options")

	return txHash.String(), nil
}

// broadcastTransaction broadcasts a signed transaction to the network
// Returns (broadcasted bool, error) where broadcasted indicates if P2P broadcast was used
// Thread-safe: copies broadcaster reference under lock to prevent race conditions
func (w *Wallet) broadcastTransaction(tx *types.Transaction) (bool, error) {
	w.mu.RLock()
	broadcaster := w.broadcaster
	w.mu.RUnlock()

	if broadcaster != nil {
		if err := broadcaster.BroadcastTransaction(tx); err != nil {
			w.logger.WithError(err).WithField("tx", tx.Hash().String()).Error("broadcastTransaction: broadcast failed")
			return false, fmt.Errorf("failed to broadcast transaction to network: %w", err)
		}
		// Track as pending immediately (mempool callback may also fire, OnMempoolTransaction deduplicates).
		// Called synchronously so pending state is visible before broadcastTransaction returns,
		// enabling immediate transaction chaining without a race window.
		// Note: if the tx is later evicted from the mempool (conflict, expiry via MaxTransactionAge),
		// the onRemoveTransaction callback fires EvictPendingTx to clean up wallet pending state.
		w.OnMempoolTransaction(tx)
		return true, nil
	}

	// WARNING: This only adds to local mempool - transaction will NOT be relayed to peers!
	w.logger.Warn("No P2P broadcaster configured - transaction will NOT be relayed to network!")
	if err := w.mempool.AddTransaction(tx); err != nil {
		return false, fmt.Errorf("failed to add transaction to mempool: %w", err)
	}
	// Note: mempool.AddTransaction triggers the onTransaction callback which calls OnMempoolTransaction
	return false, nil
}

// GetUTXOsByOutpoints retrieves specific UTXOs by their outpoints (for coin control)
func (w *Wallet) GetUTXOsByOutpoints(outpoints []types.Outpoint) ([]*UTXO, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	result := make([]*UTXO, 0, len(outpoints))
	for _, op := range outpoints {
		utxo, exists := w.utxos[op]
		if !exists {
			return nil, fmt.Errorf("UTXO not found: %s:%d", op.Hash.String(), op.Index)
		}
		if !utxo.Spendable {
			return nil, fmt.Errorf("UTXO not spendable: %s:%d", op.Hash.String(), op.Index)
		}
		result = append(result, utxo)
	}

	return result, nil
}

// HasUTXO checks if a spendable UTXO exists for the given outpoint.
// Unlike ListUnspent, this does NOT filter out masternode collateral UTXOs.
func (w *Wallet) HasUTXO(outpoint types.Outpoint) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	utxo, exists := w.utxos[outpoint]
	return exists && utxo.Spendable
}

// ListUnspent returns unspent transaction outputs
func (w *Wallet) ListUnspent(minConf, maxConf int, addresses []string) (interface{}, error) {
	// Get cached chain height BEFORE acquiring wallet lock (avoid lock ordering issues)
	w.heightMu.RLock()
	currentHeight := w.cachedChainHeight
	w.heightMu.RUnlock()

	w.mu.RLock()
	defer w.mu.RUnlock()

	// Filter addresses if specified
	addressFilter := make(map[string]bool)
	if len(addresses) > 0 {
		for _, addr := range addresses {
			addressFilter[addr] = true
		}
	}

	// Snapshot locked coins for consistent checking within this call
	w.lockedCoinsMu.RLock()
	lockedSnapshot := make(map[types.Outpoint]struct{}, len(w.lockedCoins))
	for k := range w.lockedCoins {
		lockedSnapshot[k] = struct{}{}
	}
	w.lockedCoinsMu.RUnlock()

	// Snapshot pending-spent outpoints to exclude UTXOs already spent by pending transactions
	w.pendingMu.RLock()
	pendingSpentSnapshot := make(map[types.Outpoint]struct{}, len(w.pendingSpent))
	for k := range w.pendingSpent {
		pendingSpentSnapshot[k] = struct{}{}
	}
	w.pendingMu.RUnlock()

	// Collect UTXOs
	result := make([]*UnspentOutput, 0)
	for _, utxo := range w.utxos {
		// Skip if not spendable
		if !utxo.Spendable {
			continue
		}

		// Filter by address if specified
		if len(addressFilter) > 0 && !addressFilter[utxo.Address] {
			continue
		}

		// Skip UTXOs already spent by pending mempool transactions
		if _, spent := pendingSpentSnapshot[utxo.Outpoint]; spent {
			continue
		}

		// Calculate confirmations
		confirmations := int32(0)
		if currentHeight >= uint32(utxo.BlockHeight) {
			confirmations = int32(currentHeight) - utxo.BlockHeight + 1
		}

		// Check maturity for coinbase/stake
		if (utxo.IsCoinbase || utxo.IsStake) && confirmations < int32(w.config.CoinbaseMaturity) {
			continue // Immature
		}

		// Filter by confirmations
		if confirmations < int32(minConf) {
			continue
		}
		if maxConf > 0 && confirmations > int32(maxConf) {
			continue
		}

		// Determine lock state: masternode collateral or user-locked
		// Legacy: CoinControlDialog shows collateral as locked/disabled, not hidden
		isCollateral := w.isCollateralUTXOLocked(utxo.Outpoint)
		_, isUserLocked := lockedSnapshot[utxo.Outpoint]
		locked := isCollateral || isUserLocked

		result = append(result, &UnspentOutput{
			TxID:          utxo.Outpoint.Hash.String(),
			Vout:          utxo.Outpoint.Index,
			Address:       utxo.Address,
			Account:       fmt.Sprintf("%d", utxo.Account),
			ScriptPubKey:  fmt.Sprintf("%x", utxo.Output.ScriptPubKey),
			Amount:        float64(utxo.Output.Value) / 100000000, // Convert to TWINS
			Confirmations: confirmations,
			Spendable:     utxo.Spendable && !locked,
			Locked:        locked,
			BlockHeight:   utxo.BlockHeight,
			BlockTime:     utxo.BlockTime,
		})
	}

	return result, nil
}

// SelectUTXOs selects UTXOs to cover the target amount using knapsack algorithm
// with 3-tier confirmation fallback matching legacy C++ behavior.
// The minConf parameter is kept for API compatibility but confirmation filtering
// is handled internally by the 3-tier fallback (1,6 → 1,1 → 0,1).
// numOutputs is the number of transaction outputs (recipients + change) used for
// TX size estimation in the size-aware fallback.
func (w *Wallet) SelectUTXOs(targetAmount int64, minConf int, numOutputs int) ([]*UTXO, error) {
	// Get cached chain height BEFORE acquiring wallet lock (avoid lock ordering issues)
	w.heightMu.RLock()
	currentHeight := w.cachedChainHeight
	w.heightMu.RUnlock()

	w.mu.RLock()
	defer w.mu.RUnlock()

	// Debug logging for UTXO selection
	w.logger.WithFields(map[string]interface{}{
		"total_utxos":   len(w.utxos),
		"target_amount": targetAmount,
		"min_conf":      minConf,
		"chain_height":  currentHeight,
		"address_count": len(w.addresses),
	}).Debug("SelectUTXOs: Starting UTXO selection")

	// Snapshot locked coins for consistent checking
	w.lockedCoinsMu.RLock()
	lockedSnapshot := make(map[types.Outpoint]struct{}, len(w.lockedCoins))
	for k := range w.lockedCoins {
		lockedSnapshot[k] = struct{}{}
	}
	w.lockedCoinsMu.RUnlock()

	// Snapshot pending state for consistent checking
	w.pendingMu.RLock()
	pendingSpentSnapshot := make(map[types.Outpoint]types.Hash, len(w.pendingSpent))
	for k, v := range w.pendingSpent {
		pendingSpentSnapshot[k] = v
	}
	// Collect pending UTXOs (change outputs from our pending txs)
	pendingUTXOsSnapshot := make([]*UTXO, 0, len(w.pendingUTXOs))
	for _, utxo := range w.pendingUTXOs {
		pendingUTXOsSnapshot = append(pendingUTXOsSnapshot, utxo)
	}
	w.pendingMu.RUnlock()

	// Collect available UTXOs: filter only by spendable, collateral, user-lock, and pending-spent.
	// Confirmation and maturity filtering is handled by selectCoinsMinConf per tier,
	// matching legacy AvailableCoins() which collects all non-locked, non-collateral UTXOs.
	// Legacy: wallet.cpp:2232 (AvailableCoins)
	available := make([]*UTXO, 0)
	notSpendableCount := 0
	collateralCount := 0
	lockedCount := 0
	pendingSpentCount := 0
	var collateralAmount int64 // Track locked collateral amount for error messages

	for _, utxo := range w.utxos {
		if !utxo.Spendable {
			notSpendableCount++
			continue
		}

		// Skip masternode collateral UTXOs from automatic selection
		// Legacy: CWallet::AvailableCoins skips IsLockedCoin except ONLY_10000
		if w.isCollateralUTXOLocked(utxo.Outpoint) {
			collateralCount++
			collateralAmount += utxo.Output.Value
			continue
		}

		// Skip user-locked UTXOs from automatic selection
		// Legacy: CWallet::AvailableCoins checks IsLockedCoin() at wallet.cpp:2286
		if _, locked := lockedSnapshot[utxo.Outpoint]; locked {
			lockedCount++
			continue
		}

		// Skip UTXOs already spent by pending mempool transactions
		if _, spent := pendingSpentSnapshot[utxo.Outpoint]; spent {
			pendingSpentCount++
			continue
		}

		available = append(available, utxo)
	}

	// Include pending change UTXOs (outputs from our pending txs) for transaction chaining.
	// These have BlockHeight=-1, so confirmations=0 in coin_selection.go.
	// They are only selected at Tier 3 (minConfMine=0) when SpendZeroConfChange is enabled,
	// matching legacy C++ SpendZeroConfChange behavior (wallet.cpp:1551).
	for _, utxo := range pendingUTXOsSnapshot {
		if utxo.Spendable && utxo.IsChange {
			available = append(available, utxo)
		}
	}

	w.logger.WithFields(map[string]interface{}{
		"available":      len(available),
		"not_spendable":  notSpendableCount,
		"collateral":     collateralCount,
		"user_locked":    lockedCount,
		"pending_spent":  pendingSpentCount,
		"pending_change": len(pendingUTXOsSnapshot),
	}).Debug("SelectUTXOs: UTXO filtering results")

	if len(available) == 0 {
		// Use pre-calculated collateral amount (calculated during UTXO iteration above)
		if collateralAmount > 0 {
			lockedTWINS := float64(collateralAmount) / 1e8
			return nil, fmt.Errorf("insufficient unlocked funds. You have %.8f TWINS locked as masternode collateral in %d UTXOs (total=%d, not_spendable=%d)",
				lockedTWINS, collateralCount, len(w.utxos), notSpendableCount)
		}

		return nil, fmt.Errorf("no spendable UTXOs available (total=%d, not_spendable=%d, chain_height=%d)",
			len(w.utxos), notSpendableCount, currentHeight)
	}

	// 3-tier confirmation fallback matching legacy SelectCoinsMinConf behavior.
	// Legacy: wallet.cpp:1550-1561
	//   Tier 1: SelectCoinsMinConf(1, 6) - 1 conf for ours, 6 for others
	//   Tier 2: SelectCoinsMinConf(1, 1) - 1 conf for all
	//   Tier 3: SelectCoinsMinConf(0, 1) - allow zero-conf change via SpendZeroConfChange
	tiers := []struct {
		minConfMine int32 // minimum confirmations for our change outputs
		minConf     int32 // minimum confirmations for other outputs
	}{
		{1, 6},
		{1, 1},
		{0, 1},
	}

	for _, tier := range tiers {
		// Tier 3 (0-conf change) is only attempted when SpendZeroConfChange is enabled.
		// Legacy: wallet.cpp:1551 guards the tier-3 call with bSpendZeroConfChange.
		if tier.minConfMine == 0 && !w.config.SpendZeroConfChange {
			continue
		}

		result := selectCoinsMinConf(available, targetAmount, tier.minConf, tier.minConfMine,
			currentHeight, int32(w.config.CoinbaseMaturity), w.config.SpendZeroConfChange)
		if result != nil {
			// Size-aware fallback: if knapsack selected too many inputs that would
			// exceed MaxStandardTxSize, fall back to largest-first which picks the
			// fewest, largest coins. This is an improvement over legacy C++ which
			// would fail later at the TX build stage.
			estimatedSize := bytesPerInput*len(result.Selected) + bytesPerOutput*numOutputs + txBaseOverhead
			if estimatedSize > MaxStandardTxSize {
				w.logger.WithFields(map[string]interface{}{
					"knapsack_inputs": len(result.Selected),
					"estimated_size":  estimatedSize,
					"max_size":        MaxStandardTxSize,
					"target":          targetAmount,
				}).Debug("SelectUTXOs: Knapsack result would exceed TX size limit, using largest-first fallback")

				// Filter eligible coins for this tier (same confirmation requirements)
				eligible := filterByConfirmations(available, tier.minConf, tier.minConfMine,
					currentHeight, int32(w.config.CoinbaseMaturity), w.config.SpendZeroConfChange)
				largestFirst := selectLargestFirst(eligible, targetAmount)
				if largestFirst != nil {
					// Verify fallback result also fits within size limit
					fallbackSize := bytesPerInput*len(largestFirst.Selected) + bytesPerOutput*numOutputs + txBaseOverhead
					if fallbackSize > MaxStandardTxSize {
						w.logger.WithFields(map[string]interface{}{
							"fallback_inputs": len(largestFirst.Selected),
							"fallback_size":   fallbackSize,
							"max_size":        MaxStandardTxSize,
						}).Warn("SelectUTXOs: UTXO set too fragmented, even largest-first exceeds TX size limit")
						continue // try next tier
					}
					w.logger.WithFields(map[string]interface{}{
						"fallback_inputs": len(largestFirst.Selected),
						"fallback_total":  largestFirst.Total,
						"target":          targetAmount,
					}).Debug("SelectUTXOs: Largest-first fallback succeeded")
					return largestFirst.Selected, nil
				}
			}

			w.logger.WithFields(map[string]interface{}{
				"tier_minConfMine": tier.minConfMine,
				"tier_minConf":     tier.minConf,
				"selected_count":   len(result.Selected),
				"selected_total":   result.Total,
				"target":           targetAmount,
			}).Debug("SelectUTXOs: Coin selection succeeded")
			return result.Selected, nil
		}
	}

	// All tiers failed - compute total available for error message
	totalAvailable := int64(0)
	for _, utxo := range available {
		totalAvailable += utxo.Output.Value
	}
	return nil, fmt.Errorf("insufficient funds: need %d, have %d (available=%d UTXOs)",
		targetAmount, totalAvailable, len(available))
}

// BuildTransaction builds an unsigned transaction from selected UTXOs and recipients
func (w *Wallet) BuildTransaction(utxos []*UTXO, recipients map[string]int64) (*types.Transaction, int64, error) {
	// Calculate total input value
	totalInput := int64(0)
	for _, utxo := range utxos {
		totalInput += utxo.Output.Value
	}

	// Calculate total output value
	totalOutput := int64(0)
	for _, amount := range recipients {
		totalOutput += amount
	}

	// Estimate transaction size: bytesPerInput/input + bytesPerOutput/output + overhead
	estimatedSize := bytesPerInput*len(utxos) + bytesPerOutput*len(recipients) + bytesPerOutput + txBaseOverhead // +bytesPerOutput for potential change

	// Pre-estimate TX size check: fail fast before expensive signing.
	// Legacy: MAX_STANDARD_TX_SIZE = 100000 (main.h:70)
	if estimatedSize > MaxStandardTxSize {
		return nil, 0, fmt.Errorf("estimated transaction size %d bytes exceeds maximum %d (inputs: %d, outputs: %d)",
			estimatedSize, MaxStandardTxSize, len(utxos), len(recipients))
	}

	estimatedFee := int64(estimatedSize) * w.config.FeePerKB / 1000

	// Validate fee bounds against config (legacy C++ compatible)
	// Apply minimum fee threshold from config (legacy: -mintxfee)
	minFee := max(w.config.MinTxFee, MinRelayFee) // Never go below network relay minimum
	estimatedFee = max(estimatedFee, minFee)      // Bump up to minimum

	// Check against maximum fee from config (legacy: -maxtxfee)
	if w.config.MaxTxFee > 0 && estimatedFee > w.config.MaxTxFee {
		return nil, 0, fmt.Errorf("fee %d satoshis exceeds configured maximum fee %d (--maxtxfee)",
			estimatedFee, w.config.MaxTxFee)
	}

	// Also check fee doesn't exceed 10% of total output (safety check, prevents overpaying)
	// Only applies if user hasn't explicitly set a higher MaxTxFee
	maxReasonableFee := totalOutput * MaxReasonableFeePercent / 100
	if estimatedFee > maxReasonableFee {
		// If MaxTxFee is set and fee is under it, user explicitly allowed this - just warn
		if w.config.MaxTxFee > 0 && estimatedFee <= w.config.MaxTxFee {
			// Fee is high but within user-configured limit, allow it (user knows what they're doing)
		} else if w.config.MaxTxFee == 0 {
			// No MaxTxFee configured, apply 10% sanity check
			return nil, 0, fmt.Errorf("fee %d satoshis exceeds maximum reasonable fee %d (10%% of output)",
				estimatedFee, maxReasonableFee)
		}
	}

	// Calculate change
	change := totalInput - totalOutput - estimatedFee

	// Check if we have enough for fee
	if change < 0 {
		return nil, 0, fmt.Errorf("insufficient funds for fee: need %d more satoshis", -change)
	}

	// Build transaction
	tx := &types.Transaction{
		Version:  1,
		LockTime: 0,
		Inputs:   make([]*types.TxInput, 0),
		Outputs:  make([]*types.TxOutput, 0),
	}

	// Add inputs
	for _, utxo := range utxos {
		tx.Inputs = append(tx.Inputs, &types.TxInput{
			PreviousOutput: types.Outpoint{
				Hash:  utxo.Outpoint.Hash,
				Index: utxo.Outpoint.Index,
			},
			Sequence:  0xFFFFFFFF,
			ScriptSig: []byte{}, // Will be filled during signing
		})
	}

	// Add recipient outputs
	for address, amount := range recipients {
		// Decode address to get pubkey hash
		addr, err := crypto.DecodeAddress(address)
		if err != nil {
			return nil, 0, fmt.Errorf("invalid address %s: %w", address, err)
		}

		// Create P2PKH script: OP_DUP OP_HASH160 <pubKeyHash> OP_EQUALVERIFY OP_CHECKSIG
		scriptPubKey := make([]byte, 25)
		scriptPubKey[0] = 0x76 // OP_DUP
		scriptPubKey[1] = 0xa9 // OP_HASH160
		scriptPubKey[2] = 0x14 // Push 20 bytes
		copy(scriptPubKey[3:23], addr.Hash160())
		scriptPubKey[23] = 0x88 // OP_EQUALVERIFY
		scriptPubKey[24] = 0xac // OP_CHECKSIG

		tx.Outputs = append(tx.Outputs, &types.TxOutput{
			Value:        amount,
			ScriptPubKey: scriptPubKey,
		})
	}

	// Add change output if above dust threshold
	dustThreshold := types.GetDustThreshold(types.DefaultMinRelayTxFee)
	if change > dustThreshold {
		changeAddr, err := w.GetChangeAddress()
		if err != nil {
			return nil, 0, fmt.Errorf("failed to get change address: %w", err)
		}

		// Decode change address
		addr, err := crypto.DecodeAddress(changeAddr)
		if err != nil {
			return nil, 0, fmt.Errorf("invalid change address: %w", err)
		}

		// Create P2PKH script for change
		scriptPubKey := make([]byte, 25)
		scriptPubKey[0] = 0x76 // OP_DUP
		scriptPubKey[1] = 0xa9 // OP_HASH160
		scriptPubKey[2] = 0x14 // Push 20 bytes
		copy(scriptPubKey[3:23], addr.Hash160())
		scriptPubKey[23] = 0x88 // OP_EQUALVERIFY
		scriptPubKey[24] = 0xac // OP_CHECKSIG

		tx.Outputs = append(tx.Outputs, &types.TxOutput{
			Value:        change,
			ScriptPubKey: scriptPubKey,
		})
	}

	return tx, estimatedFee, nil
}

// BuildTransactionWithOptions builds a transaction with advanced options
// Supports custom change address and UTXO splitting
func (w *Wallet) BuildTransactionWithOptions(utxos []*UTXO, recipients map[string]int64, opts *SendOptions) (*types.Transaction, int64, error) {
	// Calculate total input value
	totalInput := int64(0)
	for _, utxo := range utxos {
		totalInput += utxo.Output.Value
	}

	// Calculate total output value
	totalOutput := int64(0)
	for _, amount := range recipients {
		totalOutput += amount
	}

	// Determine split count
	splitCount := 1
	if opts != nil && opts.SplitCount > 1 {
		splitCount = opts.SplitCount
	}

	// Calculate number of outputs (recipients * split + potential change)
	numOutputs := len(recipients) * splitCount

	// Determine fee rate: use custom rate if provided, else wallet default
	feePerKB := w.config.FeePerKB
	if opts != nil && opts.FeeRate > 0 {
		feePerKB = opts.FeeRate
	}

	// Estimate transaction size: bytesPerInput/input + bytesPerOutput/output + overhead
	estimatedSize := bytesPerInput*len(utxos) + bytesPerOutput*numOutputs + bytesPerOutput + txBaseOverhead

	// Pre-estimate TX size check: fail fast before expensive signing.
	// Legacy: MAX_STANDARD_TX_SIZE = 100000 (main.h:70)
	if estimatedSize > MaxStandardTxSize {
		return nil, 0, fmt.Errorf("estimated transaction size %d bytes exceeds maximum %d (inputs: %d, outputs: %d)",
			estimatedSize, MaxStandardTxSize, len(utxos), numOutputs)
	}

	estimatedFee := int64(estimatedSize) * feePerKB / 1000

	// Apply minimum fee threshold
	minFee := max(w.config.MinTxFee, MinRelayFee) // Never go below network relay minimum
	estimatedFee = max(estimatedFee, minFee)      // Bump up to minimum

	// Check against maximum fee
	if w.config.MaxTxFee > 0 && estimatedFee > w.config.MaxTxFee {
		return nil, 0, fmt.Errorf("fee %d satoshis exceeds configured maximum fee %d",
			estimatedFee, w.config.MaxTxFee)
	}

	// Calculate change
	change := totalInput - totalOutput - estimatedFee

	if change < 0 {
		return nil, 0, fmt.Errorf("insufficient funds for fee: need %d more satoshis", -change)
	}

	// Build transaction
	tx := &types.Transaction{
		Version:  1,
		LockTime: 0,
		Inputs:   make([]*types.TxInput, 0),
		Outputs:  make([]*types.TxOutput, 0),
	}

	// Add inputs
	for _, utxo := range utxos {
		tx.Inputs = append(tx.Inputs, &types.TxInput{
			PreviousOutput: types.Outpoint{
				Hash:  utxo.Outpoint.Hash,
				Index: utxo.Outpoint.Index,
			},
			Sequence:  0xFFFFFFFF,
			ScriptSig: []byte{},
		})
	}

	// Add recipient outputs (with optional split)
	for address, amount := range recipients {
		addr, err := crypto.DecodeAddress(address)
		if err != nil {
			return nil, 0, fmt.Errorf("invalid address %s: %w", address, err)
		}

		// Create P2PKH script
		scriptPubKey := make([]byte, 25)
		scriptPubKey[0] = 0x76 // OP_DUP
		scriptPubKey[1] = 0xa9 // OP_HASH160
		scriptPubKey[2] = 0x14 // Push 20 bytes
		copy(scriptPubKey[3:23], addr.Hash160())
		scriptPubKey[23] = 0x88 // OP_EQUALVERIFY
		scriptPubKey[24] = 0xac // OP_CHECKSIG

		if splitCount > 1 {
			// Split output into multiple UTXOs
			splitAmount := amount / int64(splitCount)
			remainder := amount % int64(splitCount)

			for i := 0; i < splitCount; i++ {
				outputAmount := splitAmount
				// Add remainder to last output
				if i == splitCount-1 {
					outputAmount += remainder
				}

				tx.Outputs = append(tx.Outputs, &types.TxOutput{
					Value:        outputAmount,
					ScriptPubKey: scriptPubKey,
				})
			}
		} else {
			tx.Outputs = append(tx.Outputs, &types.TxOutput{
				Value:        amount,
				ScriptPubKey: scriptPubKey,
			})
		}
	}

	// Add change output if above dust threshold
	dustThreshold := types.GetDustThreshold(types.DefaultMinRelayTxFee)
	if change > dustThreshold {
		var changeAddr string
		var err error

		// Use custom change address if provided
		if opts != nil && opts.ChangeAddress != "" {
			changeAddr = opts.ChangeAddress
			// Validate custom change address
			validation, err := w.ValidateAddress(changeAddr)
			if err != nil || !validation.IsValid {
				return nil, 0, fmt.Errorf("invalid custom change address: %s", changeAddr)
			}
		} else {
			changeAddr, err = w.GetChangeAddress()
			if err != nil {
				return nil, 0, fmt.Errorf("failed to get change address: %w", err)
			}
		}

		addr, err := crypto.DecodeAddress(changeAddr)
		if err != nil {
			return nil, 0, fmt.Errorf("invalid change address: %w", err)
		}

		scriptPubKey := make([]byte, 25)
		scriptPubKey[0] = 0x76 // OP_DUP
		scriptPubKey[1] = 0xa9 // OP_HASH160
		scriptPubKey[2] = 0x14 // Push 20 bytes
		copy(scriptPubKey[3:23], addr.Hash160())
		scriptPubKey[23] = 0x88 // OP_EQUALVERIFY
		scriptPubKey[24] = 0xac // OP_CHECKSIG

		tx.Outputs = append(tx.Outputs, &types.TxOutput{
			Value:        change,
			ScriptPubKey: scriptPubKey,
		})
	}

	return tx, estimatedFee, nil
}

// SignTransaction signs a transaction with wallet private keys
func (w *Wallet) SignTransaction(tx *types.Transaction) (*types.Transaction, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	// Check wallet lock status AFTER acquiring mutex (prevents TOCTOU race)
	if w.encrypted && !w.unlocked {
		return nil, fmt.Errorf("wallet is locked")
	}

	// Create a copy to sign
	signedTx := &types.Transaction{
		Version:  tx.Version,
		LockTime: tx.LockTime,
		Inputs:   make([]*types.TxInput, len(tx.Inputs)),
		Outputs:  make([]*types.TxOutput, len(tx.Outputs)),
	}

	// Copy outputs
	for i, out := range tx.Outputs {
		signedTx.Outputs[i] = &types.TxOutput{
			Value:        out.Value,
			ScriptPubKey: make([]byte, len(out.ScriptPubKey)),
		}
		copy(signedTx.Outputs[i].ScriptPubKey, out.ScriptPubKey)
	}

	// Sign each input
	for i, input := range tx.Inputs {
		// Copy input
		signedTx.Inputs[i] = &types.TxInput{
			PreviousOutput: input.PreviousOutput,
			Sequence:       input.Sequence,
		}

		// Get the UTXO being spent (check confirmed first, then pending for tx chaining)
		utxo, exists := w.utxos[input.PreviousOutput]
		isPendingInput := false
		if !exists {
			w.pendingMu.RLock()
			utxo, exists = w.pendingUTXOs[input.PreviousOutput]
			w.pendingMu.RUnlock()
			if !exists {
				return nil, fmt.Errorf("UTXO not found for input %d: %s:%d", i, input.PreviousOutput.Hash.String(), input.PreviousOutput.Index)
			}
			isPendingInput = true
		}

		// Validate confirmed UTXOs still exist in blockchain (prevents double-spend).
		// Skip for pending inputs — their parent tx is still in the mempool.
		if !isPendingInput && w.blockchain != nil {
			chainUtxo, err := w.blockchain.GetUTXO(input.PreviousOutput)
			if err != nil {
				return nil, fmt.Errorf("UTXO not found in blockchain for input %d: %s:%d (may have been spent)",
					i, input.PreviousOutput.Hash.String(), input.PreviousOutput.Index)
			}
			// Verify value matches to detect UTXO tampering
			if chainUtxo.Output.Value != utxo.Output.Value {
				return nil, fmt.Errorf("UTXO value mismatch for input %d: wallet=%d blockchain=%d",
					i, utxo.Output.Value, chainUtxo.Output.Value)
			}
		}

		// Get the address that owns this UTXO
		addr, exists := w.addresses[utxo.Address]
		if !exists {
			return nil, fmt.Errorf("address not found in wallet: %s", utxo.Address)
		}

		// Resolve private key using 3-step chain: in-memory → BDB → HD derivation
		// (addr.PrivKey may be nil even when wallet is unlocked for BDB/HD keys)
		privKey, err := w.getPrivateKeyForAddressLocked(utxo.Address)
		if err != nil {
			return nil, fmt.Errorf("private key not available for address %s: %w", utxo.Address, err)
		}

		// Calculate signature hash
		sigHash, err := w.calculateSignatureHash(tx, i, utxo.Output.ScriptPubKey)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate signature hash for input %d: %w", i, err)
		}

		// Create ECDSA signature
		signature, err := privKey.Sign(sigHash)
		if err != nil {
			return nil, fmt.Errorf("failed to sign input %d: %w", i, err)
		}

		// Detect script type to build correct scriptSig
		// P2PKH: OP_DUP OP_HASH160 <20 bytes> OP_EQUALVERIFY OP_CHECKSIG = 76a914...88ac
		// P2PK (compressed): <33 bytes pubkey> OP_CHECKSIG = 21...ac
		// P2PK (uncompressed): <65 bytes pubkey> OP_CHECKSIG = 41...ac
		scriptPubKey := utxo.Output.ScriptPubKey
		isP2PK := false
		isP2PKH := false

		// Check for P2PK: starts with push opcode (0x21 for compressed, 0x41 for uncompressed)
		// and ends with OP_CHECKSIG (0xac)
		if len(scriptPubKey) == 35 && scriptPubKey[0] == 0x21 && scriptPubKey[34] == 0xac {
			// P2PK with compressed pubkey (33 bytes)
			isP2PK = true
		} else if len(scriptPubKey) == 67 && scriptPubKey[0] == 0x41 && scriptPubKey[66] == 0xac {
			// P2PK with uncompressed pubkey (65 bytes)
			isP2PK = true
		} else if len(scriptPubKey) == 25 && scriptPubKey[0] == 0x76 && scriptPubKey[1] == 0xa9 &&
			scriptPubKey[2] == 0x14 && scriptPubKey[23] == 0x88 && scriptPubKey[24] == 0xac {
			// P2PKH
			isP2PKH = true
		}

		if !isP2PK && !isP2PKH {
			return nil, fmt.Errorf("unsupported script type for input %d (script length=%d)", i, len(scriptPubKey))
		}

		pubKeyBytes := addr.PubKey.SerializeCompressed()
		sigBytes := signature.Bytes()
		sigWithHashType := append(sigBytes, byte(0x01)) // SIGHASH_ALL = 0x01

		// Build scriptSig based on script type
		var scriptSig []byte
		if isP2PK {
			// P2PK: scriptSig = <signature> only (pubkey is in scriptPubKey)
			scriptSig = make([]byte, 0, 1+len(sigWithHashType))
			scriptSig = append(scriptSig, byte(len(sigWithHashType))) // Push signature
			scriptSig = append(scriptSig, sigWithHashType...)
		} else if isP2PKH {
			// P2PKH: scriptSig = <signature> <pubkey>
			scriptSig = make([]byte, 0, 1+len(sigWithHashType)+1+len(pubKeyBytes))
			scriptSig = append(scriptSig, byte(len(sigWithHashType))) // Push signature
			scriptSig = append(scriptSig, sigWithHashType...)
			scriptSig = append(scriptSig, byte(len(pubKeyBytes))) // Push pubkey
			scriptSig = append(scriptSig, pubKeyBytes...)
		}

		signedTx.Inputs[i].ScriptSig = scriptSig
	}

	return signedTx, nil
}

// calculateSignatureHash calculates the signature hash for a transaction input
// Uses the built-in SignatureHash method from types.Transaction for Bitcoin compatibility
func (w *Wallet) calculateSignatureHash(tx *types.Transaction, inputIndex int, scriptPubKey []byte) ([]byte, error) {
	// Use the transaction's built-in SignatureHash method with SIGHASH_ALL
	// This ensures Bitcoin/TWINS protocol compatibility with proper:
	// - SIGHASH type handling
	// - OP_CODESEPARATOR removal
	// - Sequence number handling
	sigHash := tx.SignatureHash(inputIndex, scriptPubKey, types.SigHashAll)
	return sigHash[:], nil
}

// UnspentOutput represents an unspent transaction output
type UnspentOutput struct {
	TxID          string  `json:"txid"`
	Vout          uint32  `json:"vout"`
	Address       string  `json:"address"`
	Account       string  `json:"account"`
	ScriptPubKey  string  `json:"scriptPubKey"`
	Amount        float64 `json:"amount"`
	Confirmations int32   `json:"confirmations"`
	Spendable     bool    `json:"spendable"`
	Locked        bool    `json:"locked"`
	BlockHeight   int32   `json:"blockheight"`
	BlockTime     uint32  `json:"blocktime"`
}

// ValidateAddress validates a TWINS address and returns information about it
func (w *Wallet) ValidateAddress(address string) (*AddressValidation, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.validateAddressLocked(address), nil
}

// validateAddressLocked is an internal version that assumes caller holds w.mu
func (w *Wallet) validateAddressLocked(address string) *AddressValidation {
	// First validate address format
	decodedAddr, err := crypto.DecodeAddress(address)
	if err != nil {
		// Invalid address format
		return &AddressValidation{
			IsValid:      false,
			Address:      address,
			IsMine:       false,
			IsScript:     false,
			PubKey:       "",
			IsCompressed: true,
			Account:      "",
		}
	}

	// Address format is valid, now check if we own it
	addr, exists := w.addresses[address]

	// Get public key hex if we own this address
	pubKeyHex := ""
	if exists && addr != nil && addr.PubKey != nil {
		pubKeyHex = hex.EncodeToString(addr.PubKey.CompressedBytes())
	}

	return &AddressValidation{
		IsValid:      true, // Format is valid
		Address:      address,
		IsMine:       exists,
		IsScript:     decodedAddr.IsScript(),
		PubKey:       pubKeyHex,
		IsCompressed: true,
		Account: func() string {
			if exists && addr != nil {
				return addr.Label
			}
			return ""
		}(),
	}
}

// AddressValidation contains address validation result
type AddressValidation struct {
	IsValid      bool   `json:"isvalid"`
	Address      string `json:"address"`
	IsMine       bool   `json:"ismine"`
	IsScript     bool   `json:"isscript"`
	PubKey       string `json:"pubkey"`
	IsCompressed bool   `json:"iscompressed"`
	Account      string `json:"account"`
}

// EstimateFeeResult contains fee estimation details for GUI display
type EstimateFeeResult struct {
	Fee        int64 `json:"fee"`        // Estimated fee in satoshis
	InputCount int   `json:"inputCount"` // Number of inputs that would be used
	TxSize     int   `json:"txSize"`     // Estimated transaction size in bytes
}

// EstimateFee estimates transaction fee based on recipients and UTXO selection
// This method works even when the wallet is locked (no signing required)
// Parameters:
//   - recipients: map of address -> amount in satoshis
//   - selectedUTXOs: specific UTXOs to use (coin control), or nil for automatic selection
//   - feeRate: fee rate in satoshis per KB, or 0 for wallet default
//   - splitCount: number of outputs to split into (1 = no split)
//
// Returns EstimateFeeResult with fee, input count, and transaction size
func (w *Wallet) EstimateFee(recipients map[string]int64, selectedUTXOs []types.Outpoint, feeRate int64, splitCount int) (*EstimateFeeResult, error) {
	// Validate recipients
	if len(recipients) == 0 {
		return nil, fmt.Errorf("no recipients specified")
	}

	// Calculate total output value
	totalOutput := int64(0)
	for _, amount := range recipients {
		if amount <= 0 {
			return nil, fmt.Errorf("invalid amount: %d", amount)
		}
		totalOutput += amount
	}

	// Determine split count (affects number of outputs)
	if splitCount < 1 {
		splitCount = 1
	}

	// Calculate number of recipient outputs (change output added in size calculation below)
	numOutputs := len(recipients) * splitCount

	// Determine fee rate: use provided rate or wallet default
	effectiveFeeRate := feeRate
	if effectiveFeeRate <= 0 {
		effectiveFeeRate = w.config.FeePerKB
	}

	// Select UTXOs - either from provided list (coin control) or automatically
	var utxos []*UTXO
	var err error

	if len(selectedUTXOs) > 0 {
		// Coin control: use specified UTXOs
		utxos, err = w.GetUTXOsByOutpoints(selectedUTXOs)
		if err != nil {
			return nil, fmt.Errorf("failed to get selected UTXOs: %w", err)
		}
		if len(utxos) == 0 {
			return nil, fmt.Errorf("no UTXOs resolved from coin control selection")
		}
	} else {
		// Automatic UTXO selection
		// First estimate size with 2 inputs to get rough fee
		roughSize := bytesPerInput*2 + bytesPerOutput*(numOutputs+1) + txBaseOverhead
		roughFee := int64(roughSize) * effectiveFeeRate / 1000
		targetAmount := totalOutput + roughFee

		utxos, err = w.SelectUTXOs(targetAmount, w.config.MinConfirmations, numOutputs+1)
		if err != nil {
			return nil, fmt.Errorf("failed to select UTXOs: %w", err)
		}
	}

	// Calculate total input value
	totalInput := int64(0)
	for _, utxo := range utxos {
		totalInput += utxo.Output.Value
	}

	// Calculate transaction size using the same formula as BuildTransactionWithOptions:
	// bytesPerInput/input + bytesPerOutput/output + bytesPerOutput for change + txBaseOverhead
	// Note: We add 1 for change output since change is likely
	txSize := bytesPerInput*len(utxos) + bytesPerOutput*(numOutputs+1) + txBaseOverhead

	// Calculate fee in satoshis: (size in bytes * satoshis per KB) / 1000
	fee := int64(txSize) * effectiveFeeRate / 1000

	// Apply minimum fee threshold (never go below network relay minimum)
	minFee := max(w.config.MinTxFee, MinRelayFee)
	fee = max(fee, minFee)

	// For automatic UTXO selection, verify inputs can cover outputs + fee.
	// For coin control (specific UTXOs), skip this check — EstimateFee is an
	// estimation API; the frontend calls it to show the correct fee before the
	// user adjusts the recipient amount (e.g. via "Max"). Blocking here causes
	// the frontend to fall back to a 2-input estimate, showing the wrong fee
	// and leading to "selected UTXOs insufficient" on the actual send.
	// Insufficiency is validated in SendManyWithOptions when the real send occurs.
	if len(selectedUTXOs) == 0 && totalInput < totalOutput+fee {
		return nil, fmt.Errorf("insufficient funds: need %d, have %d (outputs=%d, fee=%d)",
			totalOutput+fee, totalInput, totalOutput, fee)
	}

	return &EstimateFeeResult{
		Fee:        fee,
		InputCount: len(utxos),
		TxSize:     txSize,
	}, nil
}
