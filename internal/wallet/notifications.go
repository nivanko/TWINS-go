package wallet

import (
	"fmt"
	"time"

	binarystorage "github.com/twins-dev/twins-core/internal/storage/binary"
	"github.com/twins-dev/twins-core/pkg/crypto"
	"github.com/twins-dev/twins-core/pkg/types"
)

// NotifyBlocks processes new blocks to update wallet transaction and UTXO state.
// This is called during blockchain sync after blocks are committed to storage.
func (w *Wallet) NotifyBlocks(blocks []*types.Block) error {
	if w == nil {
		return nil
	}

	var newHeight uint32
	var heightValid bool

	w.mu.Lock()

	// Process each block
	for _, block := range blocks {
		if err := w.processBlock(block); err != nil {
			w.logger.WithError(err).WithField("hash", block.Hash().String()).
				Warn("Failed to process block for wallet")
			// Continue processing other blocks even if one fails
		}
	}

	// Capture height to update outside mu (avoids heightMu-under-mu lock ordering violation)
	if len(blocks) > 0 {
		lastHeight, err := w.storage.GetBlockHeight(blocks[len(blocks)-1].Hash())
		if err == nil {
			newHeight = lastHeight
			heightValid = true
		}
	}

	w.mu.Unlock()

	// Update cached chain height OUTSIDE mu to follow lock ordering: heightMu → mu
	if heightValid {
		w.heightMu.Lock()
		w.cachedChainHeight = newHeight
		w.heightMu.Unlock()
	}

	// Trigger autocombine worker (non-blocking).
	// Read worker pointer under mu.RLock to avoid race with Start/StopAutoCombine.
	w.mu.RLock()
	acWorker := w.autoCombineWorker
	w.mu.RUnlock()
	if acWorker != nil {
		acWorker.NotifyBlock()
	}

	return nil
}

// processBlock scans a single block for wallet-relevant transactions
func (w *Wallet) processBlock(block *types.Block) error {
	// Get actual block height from storage - critical for confirmation tracking
	blockHeight, err := w.storage.GetBlockHeight(block.Hash())
	if err != nil {
		// Don't store incorrect data - fail this block processing
		return fmt.Errorf("failed to get block height for wallet processing: %w", err)
	}

	// Scan all transactions in block
	for txIdx, tx := range block.Transactions {
		// Determine transaction type (coinbase, coinstake, regular)
		isCoinbase := txIdx == 0 && len(tx.Inputs) == 1 && tx.Inputs[0].PreviousOutput.Hash.IsZero()
		isCoinstake := txIdx == 1 && len(tx.Inputs) > 0 && !tx.Inputs[0].PreviousOutput.Hash.IsZero() &&
			len(tx.Outputs) > 0 && len(tx.Outputs[0].ScriptPubKey) == 0

		// Check outputs - are any sent to our addresses?
		for i, output := range tx.Outputs {
			if addr, isOurs := w.getAddressForScriptLocked(output.ScriptPubKey); isOurs {
				// Mark address as used (matches legacy C++ behavior for listaddressgroupings)
				addr.Used = true

				// Create UTXO
				outpoint := types.Outpoint{
					Hash:  tx.Hash(),
					Index: uint32(i),
				}

				utxo := &UTXO{
					Outpoint:      outpoint,
					Output:        output,
					Address:       addr.Address,
					BlockHeight:   int32(blockHeight),     // Store actual height
					BlockTime:     block.Header.Timestamp, // Store actual block time for coin age (legacy compliance)
					Confirmations: 1,                      // Initial value, calculated dynamically in GetBalance()
					Spendable:     true,
					IsCoinbase:    isCoinbase,
					IsStake:       isCoinstake,
					IsChange:      addr.Internal, // Track if this is a change output
				}

				w.utxos[outpoint] = utxo

				// Update balance
				if w.balances[addr.Address] == nil {
					w.balances[addr.Address] = &Balance{}
				}
				w.balances[addr.Address].Confirmed += output.Value
			}
		}

		// Check inputs - are we spending any of our UTXOs?
		for _, input := range tx.Inputs {
			if utxo, exists := w.utxos[input.PreviousOutput]; exists {
				// Mark UTXO as not spendable (spent)
				utxo.Spendable = false

				// Update balance
				if w.balances[utxo.Address] != nil {
					w.balances[utxo.Address].Confirmed -= utxo.Output.Value
				}
			}
		}

		// Check inputs callback - uses in-memory UTXO map for fast lookup during sync
		checkInput := func(outpoint types.Outpoint) (int64, string, bool) {
			if utxo, exists := w.utxos[outpoint]; exists {
				return utxo.Output.Value, utxo.Address, true
			}
			return 0, "", false
		}

		// Use unified categorization logic to determine if transaction is relevant
		// Use locked version since processBlock holds w.mu
		category, netAmount, address, extra := w.categorizeTransactionLocked(tx, txIdx, checkInput)

		// If transaction is relevant (has category and address), add to wallet
		if address != "" {
			// Extract sender address only for receive transactions (optimization)
			// Avoids unnecessary storage lookups for send/stake transactions.
			// TxCategoryMasternode intentionally produces an empty fromAddress:
			// the guard below skips extractSenderAddressLocked for all non-receive
			// categories, including masternode rewards whose inputs belong to the
			// block producer, not the masternode recipient.
			var fromAddress string
			if category == TxCategoryReceive {
				fromAddress = w.extractSenderAddressLocked(tx, txIdx)
			}

			w.nextSeqNum++
			// For send_to_self: netAmount = receivedAmount - spentAmount = -(fee),
			// so the fee equals -netAmount. Populate the Fee field so the details
			// dialog can display it. If fee is zero (e.g. a miner-included free tx),
			// txFee stays 0 and the UI's "fee > 0" guard suppresses the fee row —
			// this is correct: zero-fee self-sends are not reachable on mainnet.
			txFee := int64(0)
			if category == TxCategoryToSelf {
				txFee = -netAmount
			}
			txHash := tx.Hash()
			blockHash := block.Hash()
			txTime := time.Unix(int64(block.Header.Timestamp), 0)
			// Look up comment from locally-sent transactions (e.g. "autocombine")
			txComment := w.sentTxComments[txHash]
			walletTx := &WalletTransaction{
				Tx:            tx,
				Hash:          txHash,
				BlockHash:     blockHash,
				BlockHeight:   int32(blockHeight),
				Confirmations: 1, // Initial value, calculated dynamically
				Time:          txTime,
				SeqNum:        w.nextSeqNum,
				Category:      category,
				Amount:        netAmount,
				Fee:           txFee,
				Address:       address,
				FromAddress:   fromAddress,
				Comment:       txComment,
			}

			w.transactions[txKey{txHash, 0}] = walletTx

			// When the wallet is simultaneously the block staker AND a masternode
			// recipient in the same coinstake, create a second entry for the staking
			// reward. The real tx hash is preserved in WalletTransaction.Hash so the
			// GUI can still link to the explorer.
			if extra != nil {
				w.nextSeqNum++
				w.transactions[txKey{txHash, 1}] = &WalletTransaction{
					Tx:            tx,
					Hash:          txHash, // real hash for explorer linking
					BlockHash:     blockHash,
					BlockHeight:   int32(blockHeight),
					Confirmations: 1,
					Time:          txTime,
					SeqNum:        w.nextSeqNum,
					Category:      extra.Category,
					Amount:        extra.NetAmount,
					Address:       extra.Address,
					Vout:          1,
				}
			}
		}
	}

	// Promote any pending transactions that were confirmed in this block
	w.promotePendingTxsFromBlock(block)

	return nil
}

// NotifyBlockDisconnected reverses the wallet effects of a block that has been
// disconnected during a chain reorganization or rollback.
// This is the inverse of processBlock: it removes UTXOs created by the block,
// restores UTXOs spent by the block, removes wallet transactions, and updates balances.
func (w *Wallet) NotifyBlockDisconnected(block *types.Block) error {
	if w == nil {
		return nil
	}

	blockHash := block.Hash()

	w.mu.Lock()

	if err := w.disconnectBlock(block); err != nil {
		w.mu.Unlock()
		w.logger.WithError(err).WithField("hash", blockHash.String()).
			Warn("Failed to disconnect block from wallet")
		return err
	}

	w.mu.Unlock()

	// Update cached chain height OUTSIDE mu to follow lock ordering: heightMu → mu
	w.heightMu.Lock()
	if w.cachedChainHeight > 0 {
		w.cachedChainHeight--
	}
	w.heightMu.Unlock()

	return nil
}

// disconnectBlock reverses the effects of processBlock for a single block.
// Caller MUST hold w.mu.Lock().
func (w *Wallet) disconnectBlock(block *types.Block) error {
	blockHash := block.Hash()

	// Process transactions in reverse order (mirrors disconnectBlock in blockchain layer)
	for i := len(block.Transactions) - 1; i >= 0; i-- {
		tx := block.Transactions[i]
		txHash := tx.Hash()
		isCoinbase := i == 0 && len(tx.Inputs) == 1 && tx.Inputs[0].PreviousOutput.Hash.IsZero()

		// Step 1: Restore spent UTXOs (reverse of marking as spent in processBlock)
		// Skip coinbase — it has no real inputs
		if !isCoinbase {
			for _, input := range tx.Inputs {
				if utxo, exists := w.utxos[input.PreviousOutput]; exists {
					// Restore UTXO as spendable
					utxo.Spendable = true

					// Restore balance
					if w.balances[utxo.Address] != nil {
						w.balances[utxo.Address].Confirmed += utxo.Output.Value
					}
				}
			}
		}

		// Step 2: Remove UTXOs created by this transaction (reverse of creation in processBlock)
		for outIdx := range tx.Outputs {
			outpoint := types.Outpoint{
				Hash:  txHash,
				Index: uint32(outIdx),
			}

			if utxo, exists := w.utxos[outpoint]; exists {
				// Subtract from balance before removing
				if w.balances[utxo.Address] != nil {
					w.balances[utxo.Address].Confirmed -= utxo.Output.Value
				}
				delete(w.utxos, outpoint)
			}
		}

		// Step 3: Remove wallet transaction (and secondary entry if present)
		delete(w.transactions, txKey{txHash, 0})
		delete(w.transactions, txKey{txHash, 1})
	}

	// Step 4: Clean pending state — remove any pending references related to this block
	w.demotePendingFromDisconnectedBlock(block)

	w.logger.WithField("hash", blockHash.String()).
		Debug("Block disconnected from wallet")

	return nil
}

// demotePendingFromDisconnectedBlock cleans up pending transaction state when a block
// is disconnected. Transactions from the disconnected block that were previously promoted
// from pending (via promotePendingTxsFromBlock) are already gone from pendingTxs.
// This method ensures no stale pending references remain.
// Caller MUST hold w.mu.Lock().
func (w *Wallet) demotePendingFromDisconnectedBlock(block *types.Block) {
	w.pendingMu.Lock()
	defer w.pendingMu.Unlock()

	for _, tx := range block.Transactions {
		txHash := tx.Hash()

		// Remove any pending tracking for transactions in this block
		if _, exists := w.pendingTxs[txHash]; exists {
			delete(w.pendingTxs, txHash)
			delete(w.lastRebroadcast, txHash)

			for _, input := range tx.Inputs {
				delete(w.pendingSpent, input.PreviousOutput)
			}

			for outIdx := range tx.Outputs {
				outpoint := types.Outpoint{Hash: txHash, Index: uint32(outIdx)}
				delete(w.pendingUTXOs, outpoint)
			}
		}
	}
}

// ExtractAddress extracts the address from a scriptPubKey using AnalyzeScript
// Supports P2PKH, P2SH, and P2PK script types (Public wrapper for RPC layer)
func (w *Wallet) ExtractAddress(scriptPubKey []byte) string {
	return w.extractAddress(scriptPubKey)
}

// extractAddress extracts the address from a scriptPubKey using AnalyzeScript
// Supports P2PKH, P2SH, and P2PK script types
func (w *Wallet) extractAddress(scriptPubKey []byte) string {
	// Use existing AnalyzeScript function (supports P2PKH, P2SH, and P2PK)
	scriptType, scriptHash := binarystorage.AnalyzeScript(scriptPubKey)

	if scriptType == binarystorage.ScriptTypeUnknown {
		return ""
	}

	// Determine version byte based on script type and network
	var version byte
	switch scriptType {
	case binarystorage.ScriptTypeP2PKH, binarystorage.ScriptTypeP2PK:
		// P2PK uses same address format as P2PKH (both are pubkey-based)
		switch w.config.Network {
		case MainNet:
			version = crypto.MainNetPubKeyHashAddrID // 0x49
		case TestNet, RegTest:
			version = crypto.TestNetPubKeyHashAddrID // 0x6f
		default:
			return ""
		}
	case binarystorage.ScriptTypeP2SH:
		switch w.config.Network {
		case MainNet:
			version = crypto.MainNetScriptHashAddrID // 0x53
		case TestNet, RegTest:
			version = crypto.TestNetScriptHashAddrID // 0xC4
		default:
			return ""
		}
	default:
		return ""
	}

	// Create Base58 address: version + scriptHash + checksum
	payload := append([]byte{version}, scriptHash[:]...)
	checksum := crypto.DoubleHash256(payload)[:4]
	fullPayload := append(payload, checksum...)
	return crypto.Base58Encode(fullPayload)
}

// isOurScript checks if a scriptPubKey belongs to our wallet
// Returns address string and whether it's ours
func (w *Wallet) isOurScript(scriptPubKey []byte) (string, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.isOurScriptLocked(scriptPubKey)
}

// isOurScriptLocked is an internal version that assumes caller holds w.mu
func (w *Wallet) isOurScriptLocked(scriptPubKey []byte) (string, bool) {
	addr, exists := w.getAddressForScriptLocked(scriptPubKey)
	if !exists {
		return "", false
	}
	return addr.Address, true
}

// getAddressForScriptLocked returns the full Address struct for a script if it belongs to our wallet
// This allows callers to access the Internal flag to determine if it's a change address
func (w *Wallet) getAddressForScriptLocked(scriptPubKey []byte) (*Address, bool) {
	scriptType, scriptHash := binarystorage.AnalyzeScript(scriptPubKey)
	if scriptType == binarystorage.ScriptTypeUnknown {
		return nil, false
	}

	isTestNet := w.config.Network == TestNet
	addressBinary := binarystorage.ScriptHashToAddressBinary(scriptType, scriptHash, isTestNet)
	if addressBinary == nil || len(addressBinary) != 21 {
		return nil, false
	}

	var key [21]byte
	copy(key[:], addressBinary)

	addr, exists := w.addressesBinary[key]
	if !exists {
		return nil, false
	}

	return addr, true
}

// categorizationExtra carries optional secondary categorization data for transactions
// where the wallet receives two reward types from the same coinstake (e.g., acting
// as both block staker and masternode recipient). When non-nil, callers should create
// a second WalletTransaction entry stored under txKey{txHash, 1}.
type categorizationExtra struct {
	Category  TxCategory
	NetAmount int64
	Address   string
}

// categorizeTransaction determines transaction category, net amount, and primary address
// This function provides unified transaction categorization logic used during both
// blockchain sync (processBlock) and wallet rescan (RescanBlockchain)
//
// checkInput is a callback that checks if an input outpoint belongs to our wallet
// and returns its value, address, and ownership status
func (w *Wallet) categorizeTransaction(
	tx *types.Transaction,
	txIdx int,
	checkInput func(outpoint types.Outpoint) (value int64, address string, isOurs bool),
) (category TxCategory, netAmount int64, address string, extra *categorizationExtra) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.categorizeTransactionLocked(tx, txIdx, checkInput)
}

// categorizeTransactionLocked is an internal version that assumes caller holds w.mu
func (w *Wallet) categorizeTransactionLocked(
	tx *types.Transaction,
	txIdx int,
	checkInput func(outpoint types.Outpoint) (value int64, address string, isOurs bool),
) (category TxCategory, netAmount int64, address string, extra *categorizationExtra) {

	// Determine transaction type based on position and structure
	isCoinbase := txIdx == 0 && len(tx.Inputs) == 1 && tx.Inputs[0].PreviousOutput.Hash.IsZero()
	isCoinstake := txIdx == 1 && len(tx.Inputs) > 0 && !tx.Inputs[0].PreviousOutput.Hash.IsZero() &&
		len(tx.Outputs) > 0 && len(tx.Outputs[0].ScriptPubKey) == 0

	var receivedAmount int64
	var spentAmount int64
	var firstReceiveAddress string
	var firstSpendAddress string

	// Check outputs - are any sent to our addresses?
	for _, output := range tx.Outputs {
		if addr, isOurs := w.isOurScriptLocked(output.ScriptPubKey); isOurs {
			receivedAmount += output.Value
			if firstReceiveAddress == "" {
				firstReceiveAddress = addr
			}
		}
	}

	// Check inputs - are we spending any of our coins?
	// For coinstake transactions also build a set of the addresses that provided
	// inputs so we can later distinguish staking outputs from masternode payments.
	var stakingInputAddrs map[string]bool
	if isCoinstake {
		stakingInputAddrs = make(map[string]bool)
	}
	for _, input := range tx.Inputs {
		if value, addr, isOurs := checkInput(input.PreviousOutput); isOurs {
			spentAmount += value
			if firstSpendAddress == "" {
				firstSpendAddress = addr
			}
			if isCoinstake {
				stakingInputAddrs[addr] = true
			}
		}
	}

	// Determine category and net amount based on transaction type
	if isCoinbase {
		return TxCategoryCoinBase, receivedAmount, firstReceiveAddress, nil
	} else if isCoinstake {
		if spentAmount == 0 {
			// No inputs from our wallet — inferred to be a masternode reward payment.
			// This heuristic is safe in all code paths:
			//   - processBlock: outputs are indexed into w.utxos before categorize runs,
			//     so the staker's inputs are present and spentAmount > 0 for staking rewards.
			//   - rescanBlockchainLocked: checkInput uses storage.GetTransaction(), which
			//     reads the full transaction from the blockchain database regardless of
			//     UTXO spent/unspent status. Input ownership is verified against the live
			//     address set (isOurScriptLocked), not w.utxos, so the staker's inputs
			//     are found even when their UTXOs have already been spent.
			return TxCategoryMasternode, receivedAmount, firstReceiveAddress, nil
		}
		// We are the staker (spentAmount > 0). However, the same wallet may also own
		// a masternode payment address that received the MN portion of this block
		// reward. We use a two-pass detection strategy:
		//
		// Pass 1 (address-based, fast path): iterate wallet-owned outputs and accept
		// those whose address is NOT in the staking input set. This handles the
		// common case where the MN payout address differs from every staking input
		// address.
		//
		// Pass 2 (structural-position fallback): if Pass 1 finds nothing, fall back
		// to a position-based check. In TWINS PoS coinstakes the canonical output
		// layout is:
		//
		//   [output[0]=empty, stake_return..., mn_payment, dev_payment]
		//
		// The MN payment sits at structural position len-2 because TWINS always
		// pays the dev fund at the last output (see pkg/types/chainparams.go
		// DevAddress and internal/masternode/payment_tracker.go
		// extractMasternodePaymentAtHeight for the canonical extraction logic).
		// Pass 2 treats tx.Outputs[len-2] as the MN payment when it is wallet-owned
		// with non-empty script and value > 0.
		//
		// Pass 2 fires only when Pass 1 yielded mnAmount == 0 AND len(tx.Outputs)
		// >= 4 (the minimum TWINS coinstake layout: empty + stake + mn + dev). The
		// len >= 4 guard prevents false positives on hypothetical configurations
		// without a dev fund output, where a pure stake with split outputs could
		// otherwise be misclassified as having an MN payment.
		//
		// This two-pass strategy fixes a bug where coinstakes whose MN payout
		// address overlapped with a staking input address were misclassified as
		// stake rewards. The address-based Pass 1 alone cannot distinguish a
		// staker's own-address return from an MN payment on the same address; the
		// structural Pass 2 resolves that ambiguity by using the canonical TWINS
		// output position. See the `?-research-verify-payment-stats-tab` research
		// file in team-management/tasks/done/ for the full root-cause analysis.
		var mnAmount int64
		var mnAddress string
		for _, output := range tx.Outputs {
			if addr, isOurs := w.isOurScriptLocked(output.ScriptPubKey); isOurs {
				if !stakingInputAddrs[addr] {
					mnAmount += output.Value
					if mnAddress == "" {
						mnAddress = addr
					}
				}
			}
		}
		// Pass 2: structural-position fallback for the address-overlap case.
		if mnAmount == 0 && len(tx.Outputs) >= 4 {
			mnIdx := len(tx.Outputs) - 2
			candidate := tx.Outputs[mnIdx]
			if len(candidate.ScriptPubKey) > 0 && candidate.Value > 0 {
				if addr, isOurs := w.isOurScriptLocked(candidate.ScriptPubKey); isOurs {
					mnAmount = candidate.Value
					mnAddress = addr
				}
			}
		}
		if mnAmount > 0 {
			// The wallet received a masternode payment in this coinstake transaction
			// in addition to its staking reward. Return the MN payment as the primary
			// category so the transaction is shown as "Masternode Reward" in the GUI.
			// Also return secondary staking-reward data so callers can create a second
			// wallet entry for the staking portion (stored under txKey{hash, 1}).
			stakingNet := receivedAmount - mnAmount - spentAmount
			var stakeExtra *categorizationExtra
			if stakingNet > 0 {
				stakeExtra = &categorizationExtra{
					Category:  TxCategoryCoinStake,
					NetAmount: stakingNet,
					Address:   firstSpendAddress,
				}
			}
			return TxCategoryMasternode, mnAmount, mnAddress, stakeExtra
		}
		// For stake, show the net reward earned (received - spent)
		return TxCategoryCoinStake, receivedAmount - spentAmount, firstReceiveAddress, nil
	} else if spentAmount > 0 && receivedAmount > 0 {
		// Check if all value outputs belong to our wallet (send-to-self)
		allValueOutputsOurs := true
		for _, output := range tx.Outputs {
			if output.Value == 0 {
				continue // Skip OP_RETURN and other zero-value outputs
			}
			_, isOurs := w.isOurScriptLocked(output.ScriptPubKey)
			if !isOurs {
				allValueOutputsOurs = false
				break
			}
		}
		if allValueOutputsOurs {
			// All outputs go back to our wallet — net amount equals -(fee)
			return TxCategoryToSelf, receivedAmount - spentAmount, firstReceiveAddress, nil
		}
		// Both inputs and outputs but some go externally - calculate net
		netAmount = receivedAmount - spentAmount
		if netAmount < 0 {
			return TxCategorySend, netAmount, firstSpendAddress, nil
		}
		return TxCategoryReceive, netAmount, firstReceiveAddress, nil
	} else if receivedAmount > 0 {
		// Only received
		return TxCategoryReceive, receivedAmount, firstReceiveAddress, nil
	} else {
		// Only spent
		return TxCategorySend, -spentAmount, firstSpendAddress, nil
	}
}

// extractSenderAddress attempts to extract the sender's address from transaction inputs.
// For receive transactions, this looks up the previous outputs being spent to find
// addresses that don't belong to our wallet (i.e., the sender).
//
// Returns:
//   - The sender's address if found (first non-wallet input address)
//   - Empty string for coinbase/coinstake transactions (no external sender)
//   - Empty string if sender cannot be determined (missing tx data)
//
// Note: If a transaction has multiple inputs from different external addresses,
// this returns the first one found. The Qt wallet behaves similarly.
func (w *Wallet) extractSenderAddress(tx *types.Transaction, txIdx int) string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.extractSenderAddressLocked(tx, txIdx)
}

// extractSenderAddressLocked is the internal version that assumes caller holds w.mu
func (w *Wallet) extractSenderAddressLocked(tx *types.Transaction, txIdx int) string {
	// Coinbase transactions have no sender (newly minted coins)
	isCoinbase := txIdx == 0 && len(tx.Inputs) == 1 && tx.Inputs[0].PreviousOutput.Hash.IsZero()
	if isCoinbase {
		return ""
	}

	// Coinstake transactions - the "sender" is the staker themselves, not useful to show
	isCoinstake := txIdx == 1 && len(tx.Inputs) > 0 && !tx.Inputs[0].PreviousOutput.Hash.IsZero() &&
		len(tx.Outputs) > 0 && len(tx.Outputs[0].ScriptPubKey) == 0
	if isCoinstake {
		return ""
	}

	// Look through inputs to find addresses that don't belong to us (the sender)
	for _, input := range tx.Inputs {
		// Skip if this input has no previous output reference
		if input.PreviousOutput.Hash.IsZero() {
			continue
		}

		// Look up the previous transaction
		prevTx, err := w.storage.GetTransaction(input.PreviousOutput.Hash)
		if err != nil || prevTx == nil {
			// Transaction not in storage, can't determine sender
			continue
		}

		// Get the output being spent
		if int(input.PreviousOutput.Index) >= len(prevTx.Outputs) {
			continue
		}
		prevOutput := prevTx.Outputs[input.PreviousOutput.Index]

		// Check if this output belongs to us using consistent pattern
		// isOurScriptLocked uses binary address map (established pattern)
		_, isOurs := w.isOurScriptLocked(prevOutput.ScriptPubKey)

		// If it's not ours, extract the address and return as sender
		if !isOurs {
			// Extract the sender's address from the script
			senderAddr := w.extractAddress(prevOutput.ScriptPubKey)
			if senderAddr != "" {
				// Return the first sender address found
				// Could be enhanced to detect "Multiple Addresses" case
				return senderAddr
			}
		}
		// If isOurs, this input is from our wallet - continue to check other inputs
	}

	// If all inputs were from our wallet, there's no external sender to show
	return ""
}

// OnMempoolTransaction processes a transaction accepted into the mempool,
// creating pending wallet state for immediate GUI visibility and UTXO selection.
// Called from the mempool notification callback (Lock-Copy-Invoke pattern, no mempool lock held).
func (w *Wallet) OnMempoolTransaction(tx *types.Transaction) {
	if tx == nil {
		return
	}

	txHash := tx.Hash()

	w.mu.RLock()
	// Already confirmed — skip. But allow re-tracking if the entry is an evicted
	// unconfirmed tx (BlockHeight == -1) that re-entered the mempool.
	if existingTx, exists := w.getTransactionByHash(txHash); exists {
		if existingTx.BlockHeight >= 0 {
			// Truly confirmed — skip
			w.mu.RUnlock()
			return
		}
		// BlockHeight == -1: evicted unconfirmed tx re-entering mempool.
		// Fall through to re-track as pending. The stale entry in w.transactions
		// is left in place (harmless); it will be overwritten by processBlock()
		// when the transaction is confirmed in a block (under mu.Lock).
		// ListTransactions deduplicates entries present in both maps.
	}
	w.mu.RUnlock()

	w.pendingMu.RLock()
	// Already pending — skip
	if _, exists := w.pendingTxs[txHash]; exists {
		w.pendingMu.RUnlock()
		return
	}
	w.pendingMu.RUnlock()

	// Snapshot pending UTXOs once (avoids per-input pendingMu.RLock cycling in checkInput)
	w.pendingMu.RLock()
	pendingUTXOSnap := make(map[types.Outpoint]*UTXO, len(w.pendingUTXOs))
	for k, v := range w.pendingUTXOs {
		pendingUTXOSnap[k] = v
	}
	w.pendingMu.RUnlock()

	// Categorize outside pendingMu but under mu.RLock (categorize reads addresses)
	w.mu.RLock()

	// checkInput uses confirmed UTXOs first, then pending UTXOs snapshot
	checkInput := func(outpoint types.Outpoint) (int64, string, bool) {
		if utxo, exists := w.utxos[outpoint]; exists {
			return utxo.Output.Value, utxo.Address, true
		}
		if utxo, exists := pendingUTXOSnap[outpoint]; exists {
			return utxo.Output.Value, utxo.Address, true
		}
		return 0, "", false
	}

	// txIdx -1 means "not in a block" — categorize as regular tx
	// Coinstake transactions never enter the mempool, so extra is always nil here.
	category, netAmount, address, _ := w.categorizeTransactionLocked(tx, -1, checkInput)
	if address == "" {
		// Not relevant to our wallet
		w.mu.RUnlock()
		return
	}

	// Extract sender for receive transactions
	var fromAddress string
	if category == TxCategoryReceive {
		fromAddress = w.extractSenderAddressLocked(tx, -1)
	}

	// Build pending UTXO entries for our outputs (change and receives)
	var newPendingUTXOs []*UTXO
	for i, output := range tx.Outputs {
		if addr, isOurs := w.getAddressForScriptLocked(output.ScriptPubKey); isOurs {
			outpoint := types.Outpoint{Hash: txHash, Index: uint32(i)}
			utxo := &UTXO{
				Outpoint:    outpoint,
				Output:      output,
				Address:     addr.Address,
				BlockHeight: -1, // Not in a block
				Spendable:   true,
				IsChange:    addr.Internal,
			}
			newPendingUTXOs = append(newPendingUTXOs, utxo)
		}
	}

	// Acquire pendingMu.Lock while still holding mu.RLock (follows lock ordering: mu → pendingMu).
	// This closes the race window where processBlock could confirm the tx between
	// releasing mu and acquiring pendingMu.
	w.pendingMu.Lock()

	// Re-check confirmed under mu.RLock (processBlock needs mu.Lock, so can't run concurrently).
	// Allow re-tracking if the entry is an evicted unconfirmed tx (BlockHeight == -1).
	if existingTx, exists := w.getTransactionByHash(txHash); exists && existingTx.BlockHeight >= 0 {
		w.pendingMu.Unlock()
		w.mu.RUnlock()
		return
	}

	// Double-check not already added (race between parallel callbacks)
	if _, exists := w.pendingTxs[txHash]; exists {
		w.pendingMu.Unlock()
		w.mu.RUnlock()
		return
	}

	// Enforce pending transaction cap to prevent resource exhaustion from dust spam
	if len(w.pendingTxs) >= MaxPendingTransactions {
		w.pendingMu.Unlock()
		w.mu.RUnlock()
		w.logger.WithFields(map[string]interface{}{
			"tx":    txHash.String(),
			"limit": MaxPendingTransactions,
		}).Debug("Pending transaction cap reached, skipping")
		return
	}

	// For send_to_self: netAmount = receivedAmount - spentAmount = -(fee),
	// so the fee equals -netAmount. Populate the Fee field so the details
	// dialog can display it even before the transaction is confirmed.
	pendingTxFee := int64(0)
	if category == TxCategoryToSelf {
		pendingTxFee = -netAmount
	}
	// Look up comment from locally-sent transactions (e.g. "autocombine")
	pendingComment := w.sentTxComments[txHash]
	walletTx := &WalletTransaction{
		Tx:          tx,
		Hash:        txHash,
		BlockHeight: -1, // Not in a block
		Time:        time.Now(),
		Category:    category,
		Amount:      netAmount,
		Fee:         pendingTxFee,
		Address:     address,
		FromAddress: fromAddress,
		Comment:     pendingComment,
	}
	w.pendingTxs[txHash] = walletTx

	// Record spent outpoints
	for _, input := range tx.Inputs {
		w.pendingSpent[input.PreviousOutput] = txHash
	}

	// Record pending UTXOs (our outputs)
	for _, utxo := range newPendingUTXOs {
		w.pendingUTXOs[utxo.Outpoint] = utxo
	}

	w.pendingMu.Unlock()
	w.mu.RUnlock()

	w.logger.WithFields(map[string]interface{}{
		"tx":       txHash.String(),
		"category": category,
		"amount":   netAmount,
	}).Debug("Pending transaction tracked from mempool")
}

// promotePendingTxsFromBlock removes pending transactions that have been confirmed in a block.
// Called at the end of processBlock() while holding w.mu.Lock().
func (w *Wallet) promotePendingTxsFromBlock(block *types.Block) {
	w.pendingMu.Lock()
	defer w.pendingMu.Unlock()

	if len(w.pendingTxs) == 0 {
		return
	}

	for _, tx := range block.Transactions {
		txHash := tx.Hash()
		pendingTx, exists := w.pendingTxs[txHash]
		if !exists {
			continue
		}

		// Remove from pending txs
		delete(w.pendingTxs, txHash)
		delete(w.lastRebroadcast, txHash)

		// Remove spent outpoints tracked by this pending tx
		for _, input := range pendingTx.Tx.Inputs {
			delete(w.pendingSpent, input.PreviousOutput)
		}

		// Remove pending UTXOs created by this tx
		for i := range pendingTx.Tx.Outputs {
			outpoint := types.Outpoint{Hash: txHash, Index: uint32(i)}
			delete(w.pendingUTXOs, outpoint)
		}
	}
}

// EvictPendingTx removes a specific pending transaction (e.g., when evicted from mempool).
// Also cascade-evicts any dependent pending transactions whose inputs reference outputs
// of the evicted transaction, preventing phantom balance from orphaned chains.
// Evicted transactions are preserved in w.transactions so they remain visible in the
// transaction list (with 0 confirmations) rather than silently disappearing.
// Lock ordering: mu → pendingMu (matches established wallet lock ordering).
func (w *Wallet) EvictPendingTx(txHash types.Hash) {
	w.mu.Lock()
	w.pendingMu.Lock()

	evicted := w.evictPendingTxCollectLocked(txHash)

	// Preserve evicted transactions in w.transactions so they remain visible
	// in the transaction list. If the tx is later confirmed in a block,
	// processBlock will overwrite this entry with the confirmed version.
	for _, wtx := range evicted {
		if _, exists := w.transactions[txKey{wtx.Hash, wtx.Vout}]; !exists {
			w.nextSeqNum++
			wtx.SeqNum = w.nextSeqNum
			w.transactions[txKey{wtx.Hash, wtx.Vout}] = wtx
		}
	}

	w.pendingMu.Unlock()
	w.mu.Unlock()
}

// evictPendingTxCollectLocked removes a pending transaction and cascade-evicts dependents.
// Returns the evicted WalletTransaction entries for preservation in w.transactions.
// Uses iterative BFS to avoid unbounded recursion depth on long tx chains.
// Bounded by MaxPendingTransactions (each tx visited at most once via delete-before-enqueue).
// Caller must hold mu.Lock() and pendingMu.Lock().
func (w *Wallet) evictPendingTxCollectLocked(txHash types.Hash) []*WalletTransaction {
	queue := []types.Hash{txHash}
	var evicted []*WalletTransaction

	for len(queue) > 0 {
		hash := queue[0]
		queue = queue[1:]

		pendingTx, exists := w.pendingTxs[hash]
		if !exists {
			continue
		}

		evicted = append(evicted, pendingTx)
		delete(w.pendingTxs, hash)
		delete(w.lastRebroadcast, hash)

		for _, input := range pendingTx.Tx.Inputs {
			delete(w.pendingSpent, input.PreviousOutput)
		}

		for i := range pendingTx.Tx.Outputs {
			outpoint := types.Outpoint{Hash: hash, Index: uint32(i)}
			delete(w.pendingUTXOs, outpoint)

			// Enqueue any pending tx that spends this output (cascade dependency)
			if spendingTxHash, spent := w.pendingSpent[outpoint]; spent {
				queue = append(queue, spendingTxHash)
			}
		}
	}

	return evicted
}
