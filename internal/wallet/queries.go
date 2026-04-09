package wallet

import (
	"fmt"
	"sort"

	"github.com/twins-dev/twins-core/pkg/types"
)

// ReceivedByAddress represents received amount per address
type ReceivedByAddress struct {
	Address       string   `json:"address"`
	Account       string   `json:"account"`
	Amount        int64    `json:"amount"`
	Confirmations int32    `json:"confirmations"`
	TxIDs         []string `json:"txids"`
}

// ReceivedByAccount represents received amount per account
type ReceivedByAccount struct {
	Account       string `json:"account"`
	Amount        int64  `json:"amount"`
	Confirmations int32  `json:"confirmations"`
}

// AddressGrouping represents a group of addresses with common ownership
type AddressGrouping struct {
	Address string `json:"address"`
	Amount  int64  `json:"amount"`
	Account string `json:"account,omitempty"`
}

// ListReceivedByAddress lists amounts received by each address
func (w *Wallet) ListReceivedByAddress(minConf int, includeEmpty bool, includeWatchOnly bool) ([]ReceivedByAddress, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	// Build map of address -> received info
	receivedMap := make(map[string]*ReceivedByAddress)

	// Get best height for confirmations calculation
	bestHeight, err := w.blockchain.GetBestHeight()
	if err != nil {
		return nil, fmt.Errorf("failed to get best height: %w", err)
	}

	// Iterate through all transactions
	for _, wtx := range w.transactions {
		// Calculate confirmations
		confs := int32(0)
		if wtx.BlockHeight > 0 {
			confs = int32(bestHeight) - wtx.BlockHeight + 1
		}

		// Skip if below minimum confirmations
		if int(confs) < minConf {
			continue
		}

		// Skip watch-only if not included
		if !includeWatchOnly && wtx.WatchOnly {
			continue
		}

		// Process transaction outputs to our addresses
		if wtx.Category == TxCategoryReceive || wtx.Category == TxCategoryGenerate {
			addr := wtx.Address
			if addr == "" {
				continue
			}

			// Get or create entry
			if receivedMap[addr] == nil {
				// Find account for this address
				account := ""
				if walletAddr, exists := w.addresses[addr]; exists {
					if acct, ok := w.accounts[walletAddr.Account]; ok {
						account = acct.Name
					}
				}

				receivedMap[addr] = &ReceivedByAddress{
					Address:       addr,
					Account:       account,
					Amount:        0,
					Confirmations: confs,
					TxIDs:         []string{},
				}
			}

			// Add amount and txid
			receivedMap[addr].Amount += wtx.Amount
			receivedMap[addr].TxIDs = append(receivedMap[addr].TxIDs, wtx.Hash.String())

			// Update confirmations to minimum
			if confs < receivedMap[addr].Confirmations {
				receivedMap[addr].Confirmations = confs
			}
		}
	}

	// Convert map to slice
	result := make([]ReceivedByAddress, 0, len(receivedMap))
	for _, received := range receivedMap {
		// Skip empty addresses if not included
		if !includeEmpty && received.Amount == 0 {
			continue
		}
		result = append(result, *received)
	}

	return result, nil
}

// ListReceivedByAccount lists amounts received by each account
func (w *Wallet) ListReceivedByAccount(minConf int, includeEmpty bool, includeWatchOnly bool) ([]ReceivedByAccount, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	// Build map of account -> received info
	receivedMap := make(map[string]*ReceivedByAccount)

	// Get best height for confirmations calculation
	bestHeight, err := w.blockchain.GetBestHeight()
	if err != nil {
		return nil, fmt.Errorf("failed to get best height: %w", err)
	}

	// Iterate through all transactions
	for _, wtx := range w.transactions {
		// Calculate confirmations
		confs := int32(0)
		if wtx.BlockHeight > 0 {
			confs = int32(bestHeight) - wtx.BlockHeight + 1
		}

		// Skip if below minimum confirmations
		if int(confs) < minConf {
			continue
		}

		// Process received transactions
		if wtx.Category == TxCategoryReceive || wtx.Category == TxCategoryGenerate {
			account := wtx.Account
			if account == "" {
				account = "default"
			}

			// Get or create entry
			if receivedMap[account] == nil {
				receivedMap[account] = &ReceivedByAccount{
					Account:       account,
					Amount:        0,
					Confirmations: confs,
				}
			}

			// Add amount
			receivedMap[account].Amount += wtx.Amount

			// Update confirmations to minimum
			if confs < receivedMap[account].Confirmations {
				receivedMap[account].Confirmations = confs
			}
		}
	}

	// Convert map to slice
	result := make([]ReceivedByAccount, 0, len(receivedMap))
	for _, received := range receivedMap {
		// Skip empty accounts if not included
		if !includeEmpty && received.Amount == 0 {
			continue
		}
		result = append(result, *received)
	}

	return result, nil
}

// ListSinceBlock lists all transactions since a given block
func (w *Wallet) ListSinceBlock(blockHash *types.Hash, targetConf int, includeWatchOnly bool) ([]WalletTransaction, types.Hash, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	var sinceHeight int32 = 0
	if blockHash != nil {
		// Look up block height from blockchain storage
		height, err := w.storage.GetBlockHeight(*blockHash)
		if err != nil {
			return nil, types.Hash{}, fmt.Errorf("failed to get block height for hash %s: %w", blockHash.String(), err)
		}
		sinceHeight = int32(height)
	}

	// Get best height
	bestHeight, err := w.blockchain.GetBestHeight()
	if err != nil {
		return nil, types.Hash{}, fmt.Errorf("failed to get best height: %w", err)
	}

	// Collect transactions since the block
	result := make([]WalletTransaction, 0)
	for _, wtx := range w.transactions {
		// Skip transactions before the specified block
		if wtx.BlockHeight > 0 && wtx.BlockHeight <= sinceHeight {
			continue
		}

		// Calculate confirmations
		confs := int32(0)
		if wtx.BlockHeight > 0 {
			confs = int32(bestHeight) - wtx.BlockHeight + 1
		}

		// Update confirmations
		wtxCopy := *wtx
		wtxCopy.Confirmations = confs

		result = append(result, wtxCopy)
	}

	// Sort by block height (desc) first, then by time (desc)
	sort.Slice(result, func(i, j int) bool {
		// Primary sort: block height (descending - newest blocks first)
		if result[i].BlockHeight != result[j].BlockHeight {
			return result[i].BlockHeight > result[j].BlockHeight
		}
		// Secondary sort: time (descending - newest first)
		return result[i].Time.After(result[j].Time)
	})

	// Return with current best block hash
	lastBlock, err := w.blockchain.GetBestBlockHash()
	if err != nil {
		return nil, types.Hash{}, fmt.Errorf("failed to get best block hash: %w", err)
	}

	return result, lastBlock, nil
}

// ListAccounts returns balances by account
func (w *Wallet) ListAccounts(minConf int, includeWatchOnly bool) (map[string]int64, error) {
	// Get cached chain height BEFORE acquiring wallet lock
	w.heightMu.RLock()
	currentHeight := w.cachedChainHeight
	w.heightMu.RUnlock()

	w.mu.RLock()
	defer w.mu.RUnlock()

	result := make(map[string]int64)

	// Initialize all accounts with 0 balance
	for _, acct := range w.accounts {
		result[acct.Name] = 0
	}

	// Calculate balances from UTXOs, grouped by account
	for _, utxo := range w.utxos {
		if !utxo.Spendable {
			continue // Skip unspendable UTXOs
		}

		// Calculate current confirmations dynamically
		var confirmations int32
		if currentHeight >= uint32(utxo.BlockHeight) {
			confirmations = int32(currentHeight) - utxo.BlockHeight + 1
		}

		// Check if meets minConf requirement
		if int(confirmations) < minConf {
			continue
		}

		// Find account for this address
		accountName := ""
		if walletAddr, exists := w.addresses[utxo.Address]; exists {
			if acct, ok := w.accounts[walletAddr.Account]; ok {
				accountName = acct.Name
			}
		}

		// Add to account balance
		result[accountName] += utxo.Output.Value
	}

	return result, nil
}

// ListAddressGroupings lists groups of addresses with common ownership
func (w *Wallet) ListAddressGroupings() ([][][]interface{}, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	// Build address -> balance map from UTXOs (much faster than iterating transactions)
	addressBalances := make(map[string]int64)
	for _, utxo := range w.utxos {
		addressBalances[utxo.Address] += utxo.Output.Value
	}

	// Group addresses by account
	accountGroups := make(map[string][][]interface{})

	for addr, walletAddr := range w.addresses {
		// Skip unused addresses (keypool/lookahead) - matches legacy C++ behavior
		// Only show addresses that have received transactions
		if !walletAddr.Used {
			continue
		}

		// Get account name
		accountName := ""
		if acct, ok := w.accounts[walletAddr.Account]; ok {
			accountName = acct.Name
		}

		// Get balance from UTXO map
		balance := addressBalances[addr]

		// Create grouping entry: [address, amount, label]
		entry := []interface{}{addr, balance}
		if walletAddr.Label != "" {
			entry = append(entry, walletAddr.Label)
		}

		accountGroups[accountName] = append(accountGroups[accountName], entry)
	}

	// Convert to nested array format and sort each group by amount (descending)
	result := make([][][]interface{}, 0, len(accountGroups))
	for _, group := range accountGroups {
		// Sort group by amount (descending)
		sort.Slice(group, func(i, j int) bool {
			amountI := group[i][1].(int64)
			amountJ := group[j][1].(int64)
			return amountI > amountJ
		})
		result = append(result, group)
	}

	return result, nil
}

// GetTotalBalancesByAddress returns the total balance for each address in satoshis.
// Includes all unspent UTXOs regardless of lock state, maturity, or pending-spent status.
// Excludes already-spent UTXOs (Spendable=false in the UTXO map means the output was consumed).
func (w *Wallet) GetTotalBalancesByAddress() map[string]int64 {
	w.mu.RLock()
	defer w.mu.RUnlock()

	balances := make(map[string]int64)
	for _, utxo := range w.utxos {
		if !utxo.Spendable {
			continue
		}
		balances[utxo.Address] += utxo.Output.Value
	}
	return balances
}
