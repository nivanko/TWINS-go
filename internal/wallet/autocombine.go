package wallet

import (
	"sync"
	"time"

	"github.com/twins-dev/twins-core/pkg/types"
)

const (
	// autoCombineMinInputs is the minimum number of UTXOs on an address to trigger consolidation
	autoCombineMinInputs = 10

	// autoCombineMaxInputs is the maximum number of inputs per consolidation transaction.
	// Derivation: floor((MaxStandardTxSize - bytesPerOutput - txBaseOverhead) / bytesPerInput) = 525.
	// 480 provides ~91% utilization with an 8.7KB safety margin for signature variance.
	autoCombineMaxInputs = 480

	// autoCombineMaxTxsPerCycle is the maximum number of consolidation transactions per cooldown cycle
	autoCombineMaxTxsPerCycle = 4

	// autoCombineFeeGuardPercent is the max fee as percentage of per-UTXO value.
	// UTXOs where their proportional fee share exceeds this percentage are skipped.
	autoCombineFeeGuardPercent = 5
)

// AutoCombineWorker consolidates small UTXOs on wallet addresses.
// Triggered from NotifyBlocks via non-blocking channel send.
// Runs as a dedicated goroutine with its own cooldown timer.
type AutoCombineWorker struct {
	wallet      *Wallet
	blockEvents chan struct{}
	stopCh      chan struct{}
	doneCh      chan struct{}
	lastRun     time.Time
	mu          sync.Mutex // protects lastRun
}

// newAutoCombineWorker creates a new autocombine worker.
func newAutoCombineWorker(w *Wallet) *AutoCombineWorker {
	return &AutoCombineWorker{
		wallet:      w,
		blockEvents: make(chan struct{}, 1), // buffered to avoid blocking NotifyBlocks
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
	}
}

// Start launches the autocombine event loop goroutine.
func (acw *AutoCombineWorker) Start() {
	go acw.run()
}

// Stop signals the worker to stop and waits for it to finish.
func (acw *AutoCombineWorker) Stop() {
	close(acw.stopCh)
	<-acw.doneCh
}

// NotifyBlock signals a new block was connected. Non-blocking.
func (acw *AutoCombineWorker) NotifyBlock() {
	select {
	case acw.blockEvents <- struct{}{}:
	default:
		// Already signaled, skip
	}
}

// run is the main event loop.
func (acw *AutoCombineWorker) run() {
	defer close(acw.doneCh)

	for {
		select {
		case <-acw.blockEvents:
			acw.tryConsolidate()
		case <-acw.stopCh:
			return
		}
	}
}

// tryConsolidate checks cooldown and runs a consolidation cycle if ready.
func (acw *AutoCombineWorker) tryConsolidate() {
	w := acw.wallet

	// Read autocombine config from wallet fields
	w.mu.RLock()
	enabledFlag := w.autoCombineEnabled
	target := w.autoCombineTarget
	cooldownSecs := w.autoCombineCooldown
	w.mu.RUnlock()

	if !enabledFlag || target <= 0 {
		return
	}

	// Check sync status — only consolidate when fully synced (confidence 100%).
	// Read callback under RLock, invoke outside lock (callback may acquire its own locks).
	w.mu.RLock()
	syncFn := w.syncChecker
	w.mu.RUnlock()
	if syncFn != nil && !syncFn() {
		w.logger.Debug("autocombine: skipping, node not fully synced")
		return
	}

	// Check cooldown
	acw.mu.Lock()
	cooldown := time.Duration(cooldownSecs) * time.Second
	if time.Since(acw.lastRun) < cooldown {
		acw.mu.Unlock()
		return
	}
	acw.mu.Unlock()

	// Check wallet lock state
	w.mu.RLock()
	locked := w.isLockedForSendingLocked()
	w.mu.RUnlock()
	if locked {
		w.logger.Debug("autocombine: skipping, wallet is locked")
		return
	}

	// Check masternode manager is wired (nil-guard for collateral protection)
	w.mu.RLock()
	hasMN := w.masternodeManager != nil
	w.mu.RUnlock()
	if !hasMN {
		w.logger.Debug("autocombine: skipping, masternode manager not initialized")
		return
	}

	// Run consolidation — only update cooldown if at least one tx was submitted
	if acw.consolidateAll(target) {
		acw.mu.Lock()
		acw.lastRun = time.Now()
		acw.mu.Unlock()
	}
}

// consolidateAll scans all wallet addresses and consolidates eligible UTXOs.
// Submits up to autoCombineMaxTxsPerCycle transactions sequentially.
// Returns true if at least one transaction was submitted.
func (acw *AutoCombineWorker) consolidateAll(target int64) bool {
	w := acw.wallet

	w.heightMu.RLock()
	chainHeight := w.cachedChainHeight
	w.heightMu.RUnlock()

	w.mu.RLock()

	// Snapshot pending spent
	w.pendingMu.RLock()
	pendingSpentSnapshot := make(map[types.Outpoint]struct{}, len(w.pendingSpent))
	for op := range w.pendingSpent {
		pendingSpentSnapshot[op] = struct{}{}
	}
	w.pendingMu.RUnlock()

	// Snapshot locked coins
	w.lockedCoinsMu.RLock()
	lockedSnapshot := make(map[types.Outpoint]struct{}, len(w.lockedCoins))
	for op := range w.lockedCoins {
		lockedSnapshot[op] = struct{}{}
	}
	w.lockedCoinsMu.RUnlock()

	// Group eligible UTXOs by address
	groups := make(map[string][]types.Outpoint)
	for outpoint, utxo := range w.utxos {
		// Skip non-spendable
		if !utxo.Spendable {
			continue
		}

		// Skip UTXOs >= target (only consolidate small ones)
		if utxo.Output.Value >= target {
			continue
		}

		// Skip pending spent
		if _, spent := pendingSpentSnapshot[outpoint]; spent {
			continue
		}

		// Skip user-locked
		if _, isLocked := lockedSnapshot[outpoint]; isLocked {
			continue
		}

		// Skip masternode collateral
		if w.isCollateralUTXOLocked(outpoint) {
			continue
		}

		// Skip immature coinbase/coinstake
		if utxo.IsCoinbase || utxo.IsStake {
			maturity := uint32(w.config.CoinbaseMaturity) + 1
			if utxo.BlockHeight > 0 && chainHeight < uint32(utxo.BlockHeight)+maturity {
				continue
			}
		}

		// Skip unconfirmed
		if utxo.BlockHeight <= 0 {
			continue
		}

		groups[utxo.Address] = append(groups[utxo.Address], outpoint)
	}
	w.mu.RUnlock()

	// Process groups — max autoCombineMaxTxsPerCycle transactions, sequential dispatch
	txCount := 0
	var totalConsolidated int64
	for addr, outpoints := range groups {
		if txCount >= autoCombineMaxTxsPerCycle {
			break
		}

		if len(outpoints) < autoCombineMinInputs {
			continue
		}

		// Cap inputs
		if len(outpoints) > autoCombineMaxInputs {
			outpoints = outpoints[:autoCombineMaxInputs]
		}

		// Apply fee guard: estimate fee and filter out UTXOs where fee > 5% of value
		outpoints = acw.applyFeeGuard(outpoints)
		if len(outpoints) < autoCombineMinInputs {
			continue
		}

		// Cap total value at target * 1.1 — consolidate to target-sized chunks, not everything
		maxAmount := target + target/10 // target * 1.1
		outpoints = acw.capByAmount(outpoints, maxAmount)
		if len(outpoints) < autoCombineMinInputs {
			continue
		}

		// Calculate total value and estimate fee
		totalValue := acw.sumOutpoints(outpoints)
		if totalValue <= 0 {
			continue
		}

		fee := acw.estimateFee(len(outpoints))
		sendAmount := totalValue - fee
		if sendAmount <= 0 {
			continue
		}

		// Use SendManyWithOptions with coin control
		// Send (total - fee) to same address; any residual change also goes to same address
		opts := &SendOptions{
			SelectedUTXOs: outpoints,
			ChangeAddress: addr,
		}

		txHash, err := w.SendManyWithOptions(
			map[string]int64{addr: sendAmount},
			"autocombine",
			opts,
		)
		if err != nil {
			w.logger.WithError(err).WithField("address", addr).
				Warn("autocombine: failed to consolidate address")
			continue
		}

		w.logger.WithFields(map[string]interface{}{
			"address": addr,
			"inputs":  len(outpoints),
			"fee":     fee,
			"txHash":  txHash,
		}).Info("autocombine: consolidated UTXOs")

		totalConsolidated += sendAmount
		txCount++
	}

	if txCount > 0 {
		w.logger.WithField("txs", txCount).Info("autocombine: consolidation cycle complete")

		// Invoke consolidation callback outside mutex (Lock-Copy-Invoke)
		w.mu.RLock()
		cb := w.onConsolidationCallback
		w.mu.RUnlock()
		if cb != nil {
			cb(txCount, totalConsolidated)
		}
	}

	return txCount > 0
}

// estimateFee returns estimated fee in satoshis for a consolidation tx with the given input count.
func (acw *AutoCombineWorker) estimateFee(inputCount int) int64 {
	w := acw.wallet

	feePerKB := w.config.FeePerKB
	if feePerKB <= 0 {
		feePerKB = DefaultFeePerKB
	}

	// Account for 2 outputs (recipient + potential change) and add 10% safety margin
	estimatedSize := int64(bytesPerInput*inputCount + bytesPerOutput*2 + txBaseOverhead)
	estimatedSize = estimatedSize * 110 / 100 // 10% safety margin for signature variance
	fee := (estimatedSize * feePerKB) / 1000
	if fee < MinRelayFee {
		fee = MinRelayFee
	}
	return fee
}

// capByAmount selects outpoints until the running total reaches maxAmount.
// Returns a subset of outpoints whose sum is <= maxAmount.
// Returns nil if even the minimum number of inputs exceeds maxAmount (UTXOs too large for target).
func (acw *AutoCombineWorker) capByAmount(outpoints []types.Outpoint, maxAmount int64) []types.Outpoint {
	w := acw.wallet
	w.mu.RLock()
	defer w.mu.RUnlock()

	var total int64
	result := make([]types.Outpoint, 0, len(outpoints))
	for _, op := range outpoints {
		utxo, exists := w.utxos[op]
		if !exists {
			continue
		}
		if total+utxo.Output.Value > maxAmount && len(result) >= autoCombineMinInputs {
			break // Enough to reach target
		}
		total += utxo.Output.Value
		result = append(result, op)
	}

	// If the minimum set already exceeds the target, skip this address —
	// individual UTXOs are too large relative to the target
	if len(result) <= autoCombineMinInputs && total > maxAmount {
		return nil
	}

	return result
}

// applyFeeGuard filters out UTXOs where the proportional fee share exceeds autoCombineFeeGuardPercent.
func (acw *AutoCombineWorker) applyFeeGuard(outpoints []types.Outpoint) []types.Outpoint {
	w := acw.wallet

	fee := acw.estimateFee(len(outpoints))
	feePerInput := fee / int64(len(outpoints))

	w.mu.RLock()
	defer w.mu.RUnlock()

	filtered := make([]types.Outpoint, 0, len(outpoints))
	for _, op := range outpoints {
		utxo, exists := w.utxos[op]
		if !exists {
			continue
		}
		value := utxo.Output.Value
		if value > 0 && (feePerInput*100/value) > autoCombineFeeGuardPercent {
			continue // Skip: fee would exceed 5% of UTXO value
		}
		filtered = append(filtered, op)
	}
	return filtered
}

// sumOutpoints returns the total value of the given outpoints.
func (acw *AutoCombineWorker) sumOutpoints(outpoints []types.Outpoint) int64 {
	w := acw.wallet
	w.mu.RLock()
	defer w.mu.RUnlock()

	var total int64
	for _, op := range outpoints {
		if utxo, exists := w.utxos[op]; exists {
			total += utxo.Output.Value
		}
	}
	return total
}
