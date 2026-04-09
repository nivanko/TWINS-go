package rpc

import (
	"encoding/hex"
	"fmt"
	"time"

	"github.com/twins-dev/twins-core/internal/wallet"
	"github.com/twins-dev/twins-core/pkg/types"
)

// WalletAdapter adapts wallet.Wallet to the RPC WalletInterface
type WalletAdapter struct {
	w *wallet.Wallet
}

// NewWalletAdapter creates a new wallet adapter for RPC
func NewWalletAdapter(w *wallet.Wallet) *WalletAdapter {
	return &WalletAdapter{w: w}
}

// GetNewAddress generates a new receiving address
func (a *WalletAdapter) GetNewAddress(label string) (string, error) {
	return a.w.GetNewAddress(label)
}

// GetChangeAddress generates a new change address
func (a *WalletAdapter) GetChangeAddress() (string, error) {
	return a.w.GetChangeAddress()
}

// GetAddressInfo returns detailed information about an address
func (a *WalletAdapter) GetAddressInfo(address string) (*AddressInfo, error) {
	addrInfo, err := a.w.GetAddressInfo(address)
	if err != nil {
		return nil, err
	}

	// Determine if it's a script based on ScriptType
	isScript := addrInfo.ScriptType == wallet.ScriptTypeP2SH

	// Convert wallet.AddressInfo to rpc.AddressInfo
	return &AddressInfo{
		Address:     addrInfo.Address,
		IsMine:      addrInfo.IsMine,
		IsScript:    isScript,
		IsWatchOnly: addrInfo.IsWatchOnly,
		Label:       addrInfo.Label,
		PubKey:      addrInfo.PubKey,
		HDKeyPath:   addrInfo.HDKeyPath,
	}, nil
}

// ValidateAddress validates an address
func (a *WalletAdapter) ValidateAddress(address string) (*AddressValidation, error) {
	walletValidation, err := a.w.ValidateAddress(address)
	if err != nil {
		return nil, err
	}

	// Convert wallet.AddressValidation to rpc.AddressValidation
	// Note: wallet.AddressValidation doesn't have IsWatchOnly, default to false
	rpcValidation := &AddressValidation{
		IsValid:     walletValidation.IsValid,
		Address:     walletValidation.Address,
		IsScript:    walletValidation.IsScript,
		IsWatchOnly: false, // Wallet doesn't track watch-only addresses yet
	}

	return rpcValidation, nil
}

// ListAddresses returns all addresses
func (a *WalletAdapter) ListAddresses() ([]*Address, error) {
	addresses, err := a.w.ListAddresses()
	if err != nil {
		return nil, err
	}

	result := make([]*Address, len(addresses))
	for i, addr := range addresses {
		result[i] = &Address{
			Address: addr.Address,
			Label:   addr.Label,
		}
	}

	return result, nil
}

// SetLabel sets a label for an address
func (a *WalletAdapter) SetLabel(address string, label string) error {
	return a.w.SetLabel(address, label)
}

// GetBalance returns the wallet balance
func (a *WalletAdapter) GetBalance() *Balance {
	balance := a.w.GetBalance()
	if balance == nil {
		return &Balance{
			Confirmed:   0,
			Unconfirmed: 0,
			Immature:    0,
		}
	}
	return &Balance{
		Confirmed:   balance.Confirmed,
		Unconfirmed: balance.Unconfirmed,
		Immature:    balance.Immature,
	}
}

// ListUnspent returns unspent transaction outputs
func (a *WalletAdapter) ListUnspent(minConf, maxConf int, addresses []string) (interface{}, error) {
	includeUnconfirmed := minConf == 0
	utxos, err := a.w.GetUTXOs(includeUnconfirmed)
	if err != nil {
		return nil, err
	}

	// Get current chain height for dynamic confirmation calculation
	currentHeight := a.w.GetChainHeight()

	result := make([]map[string]interface{}, 0)
	for _, utxo := range utxos {
		// Calculate current confirmations dynamically
		var confirmations int32
		if currentHeight >= uint32(utxo.BlockHeight) {
			confirmations = int32(currentHeight) - utxo.BlockHeight + 1
		}

		// Filter zero-value UTXOs (matches legacy AvailableCoins behavior)
		if utxo.Output.Value <= 0 {
			continue
		}

		// Filter by confirmations
		if int(confirmations) < minConf {
			continue
		}
		if maxConf > 0 && int(confirmations) > maxConf {
			continue
		}

		// Filter by addresses if specified
		if len(addresses) > 0 {
			found := false
			for _, addr := range addresses {
				if utxo.Address == addr {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		// Return amount in TWINS (not satoshis) for RPC compatibility
		result = append(result, map[string]interface{}{
			"txid":          utxo.Outpoint.Hash.String(),
			"vout":          utxo.Outpoint.Index,
			"address":       utxo.Address,
			"scriptPubKey":  hex.EncodeToString(utxo.Output.ScriptPubKey),
			"amount":        float64(utxo.Output.Value) / 1e8,
			"confirmations": confirmations,
			"spendable":     utxo.Spendable,
		})
	}

	return result, nil
}

// SendToAddress sends coins to an address
func (a *WalletAdapter) SendToAddress(address string, amount int64, comment string, subtractFee bool) (string, error) {
	return a.w.SendToAddress(address, amount, comment, subtractFee)
}

// SendMany sends coins to multiple addresses
func (a *WalletAdapter) SendMany(recipients map[string]int64, comment string) (string, error) {
	return a.w.SendMany(recipients, comment)
}

// ListTransactions returns recent transactions
func (a *WalletAdapter) ListTransactions(count, skip int) ([]*WalletTransaction, error) {
	txs, err := a.w.ListTransactions(count, skip)
	if err != nil {
		return nil, err
	}

	// Get current chain height for dynamic confirmation calculation
	currentHeight := a.w.GetChainHeight()

	result := make([]*WalletTransaction, len(txs))
	for i, tx := range txs {
		// Calculate current confirmations dynamically
		var confirmations int32
		if currentHeight >= uint32(tx.BlockHeight) {
			confirmations = int32(currentHeight) - tx.BlockHeight + 1
		}

		result[i] = &WalletTransaction{
			TxID:          tx.Hash.String(),
			Category:      string(tx.Category),
			Amount:        tx.Amount,
			Fee:           tx.Fee,
			Confirmations: confirmations,
			BlockHash:     tx.BlockHash.String(),
			BlockHeight:   tx.BlockHeight,
			Time:          tx.Time.Unix(),
			TimeReceived:  tx.Time.Unix(), // wallet.WalletTransaction doesn't have TimeReceived, use Time
			Comment:       tx.Comment,
			Label:         tx.Label,
			Address:       tx.Address,
		}
	}

	return result, nil
}

// GetTransaction returns a transaction by ID
func (a *WalletAdapter) GetTransaction(txid string) (*WalletTransaction, error) {
	// Parse hash from string
	hash, err := types.NewHashFromString(txid)
	if err != nil {
		return nil, fmt.Errorf("invalid transaction ID: %w", err)
	}

	tx, err := a.w.GetTransaction(hash)
	if err != nil {
		return nil, err
	}

	return &WalletTransaction{
		TxID:          tx.Hash.String(),
		Category:      string(tx.Category),
		Amount:        tx.Amount,
		Fee:           tx.Fee,
		Confirmations: tx.Confirmations,
		BlockHash:     tx.BlockHash.String(),
		BlockHeight:   tx.BlockHeight,
		Time:          tx.Time.Unix(),
		TimeReceived:  tx.Time.Unix(), // wallet.WalletTransaction doesn't have TimeReceived, use Time
		Comment:       tx.Comment,
		Label:         tx.Label,
	}, nil
}

// DumpPrivKey exports the private key for an address
func (a *WalletAdapter) DumpPrivKey(address string) (string, error) {
	return a.w.DumpPrivKey(address)
}

// ImportPrivKey imports a private key
func (a *WalletAdapter) ImportPrivKey(privKey string, label string, rescan bool) error {
	return a.w.ImportPrivateKey(privKey, label, rescan)
}

// DumpHDInfo dumps HD wallet information
func (a *WalletAdapter) DumpHDInfo() (seed string, mnemonic string, mnemonicPass string, error error) {
	return a.w.DumpHDInfo()
}

// DumpWallet exports all private keys to a file
func (a *WalletAdapter) DumpWallet(filename string) error {
	return a.w.DumpWallet(filename)
}

// ImportWallet imports private keys from a wallet dump file
func (a *WalletAdapter) ImportWallet(filename string) error {
	return a.w.ImportWallet(filename)
}

// ImportAddress adds a watch-only address
func (a *WalletAdapter) ImportAddress(address string, label string, rescan bool) error {
	return a.w.ImportAddress(address, label, rescan)
}

// EncryptWallet encrypts the wallet
func (a *WalletAdapter) EncryptWallet(passphrase []byte) error {
	// Passphrase will be zeroed by wallet layer after use
	return a.w.EncryptWallet(passphrase)
}

// WalletPassphrase unlocks the wallet.
// If stakingOnly is true, the wallet is unlocked only for staking operations.
// If timeout is 0, the wallet stays unlocked until explicitly locked or daemon shutdown.
func (a *WalletAdapter) WalletPassphrase(passphrase []byte, timeout int64, stakingOnly bool) error {
	// Passphrase will be zeroed by wallet layer after use
	duration := time.Duration(timeout) * time.Second
	return a.w.Unlock(passphrase, duration, stakingOnly)
}

// WalletLock locks the wallet
func (a *WalletAdapter) WalletLock() error {
	return a.w.Lock()
}

// WalletPassphraseChange changes the wallet passphrase
func (a *WalletAdapter) WalletPassphraseChange(oldPass, newPass []byte) error {
	// Passphrases will be zeroed by wallet layer after use
	return a.w.ChangePassphrase(oldPass, newPass)
}

// IsLocked returns whether the wallet is locked
func (a *WalletAdapter) IsLocked() bool {
	return a.w.IsLocked()
}

// SignMessage signs a message with an address's private key
func (a *WalletAdapter) SignMessage(address string, message string) (string, error) {
	return a.w.SignMessage(address, message)
}

// VerifyMessage verifies a message signature
func (a *WalletAdapter) VerifyMessage(address string, signature string, message string) (bool, error) {
	return a.w.VerifyMessage(address, signature, message)
}

// ListReceivedByAddress lists amounts received by each address
func (a *WalletAdapter) ListReceivedByAddress(minConf int, includeEmpty bool, includeWatchOnly bool) ([]interface{}, error) {
	received, err := a.w.ListReceivedByAddress(minConf, includeEmpty, includeWatchOnly)
	if err != nil {
		return nil, err
	}

	// Convert to []interface{} with amount in TWINS for RPC compatibility
	result := make([]interface{}, len(received))
	for i, r := range received {
		result[i] = map[string]interface{}{
			"address":       r.Address,
			"account":       r.Account,
			"amount":        float64(r.Amount) / 1e8,
			"confirmations": r.Confirmations,
			"txids":         r.TxIDs,
		}
	}
	return result, nil
}

// ListReceivedByAccount lists amounts received by each account
func (a *WalletAdapter) ListReceivedByAccount(minConf int, includeEmpty bool, includeWatchOnly bool) ([]interface{}, error) {
	received, err := a.w.ListReceivedByAccount(minConf, includeEmpty, includeWatchOnly)
	if err != nil {
		return nil, err
	}

	// Convert to []interface{} with amount in TWINS for RPC compatibility
	result := make([]interface{}, len(received))
	for i, r := range received {
		result[i] = map[string]interface{}{
			"account":       r.Account,
			"amount":        float64(r.Amount) / 1e8,
			"confirmations": r.Confirmations,
		}
	}
	return result, nil
}

// ListSinceBlock lists all transactions since a given block
func (a *WalletAdapter) ListSinceBlock(blockHash *types.Hash, targetConf int, includeWatchOnly bool) ([]WalletTransaction, types.Hash, error) {
	txs, lastBlock, err := a.w.ListSinceBlock(blockHash, targetConf, includeWatchOnly)
	if err != nil {
		return nil, types.Hash{}, err
	}

	// Convert wallet.WalletTransaction to rpc.WalletTransaction
	result := make([]WalletTransaction, len(txs))
	for i, tx := range txs {
		result[i] = WalletTransaction{
			TxID:          tx.Hash.String(),
			Category:      string(tx.Category),
			Amount:        tx.Amount,
			Fee:           tx.Fee,
			Confirmations: tx.Confirmations,
			BlockHash:     tx.BlockHash.String(),
			BlockHeight:   tx.BlockHeight,
			Time:          tx.Time.Unix(),
			TimeReceived:  tx.Time.Unix(),
			Comment:       tx.Comment,
			Label:         tx.Label,
			Address:       tx.Address,
		}
	}

	return result, lastBlock, nil
}

// ListAccounts returns balances by account
func (a *WalletAdapter) ListAccounts(minConf int, includeWatchOnly bool) (map[string]int64, error) {
	return a.w.ListAccounts(minConf, includeWatchOnly)
}

// ListAddressGroupings lists groups of addresses with common ownership
func (a *WalletAdapter) ListAddressGroupings() ([][][]interface{}, error) {
	return a.w.ListAddressGroupings()
}

// ExtractAddress extracts the address from a scriptPubKey
func (a *WalletAdapter) ExtractAddress(scriptPubKey []byte) string {
	return a.w.ExtractAddress(scriptPubKey)
}

// IsOurAddress checks if an address belongs to the wallet
func (a *WalletAdapter) IsOurAddress(address string) bool {
	return a.w.IsOurAddress(address)
}

// GetWalletInfo returns wallet state information
func (a *WalletAdapter) GetWalletInfo() (*WalletInfo, error) {
	balance := a.w.GetBalance()

	// Calculate unlocked_until timestamp
	var unlockedUntil int64
	if unlockTime := a.w.UnlockTime(); !unlockTime.IsZero() {
		unlockedUntil = unlockTime.Unix()
	}

	// Determine encryption status
	encryptionStatus := "Unencrypted"
	if a.w.IsEncrypted() {
		if a.w.IsLocked() {
			encryptionStatus = "Locked"
		} else if a.w.IsUnlockedForStakingOnly() {
			encryptionStatus = "Unlocked (staking only)"
		} else {
			encryptionStatus = "Unlocked"
		}
	}

	return &WalletInfo{
		WalletVersion:    1, // TWINS wallet version
		Balance:          float64(balance.Confirmed) / 1e8,
		DelegatedBalance: 0,
		TxCount:          a.w.GetTransactionCount(),
		KeypoolOldest:    a.w.GetKeypoolOldest(),
		KeypoolSize:      a.w.GetKeypoolSize(),
		UnlockedUntil:    unlockedUntil,
		EncryptionStatus: encryptionStatus,
	}, nil
}

// BackupWallet safely copies wallet.dat to destination
func (a *WalletAdapter) BackupWallet(destination string) error {
	return a.w.BackupWallet(destination)
}

// KeypoolRefill refills the keypool
func (a *WalletAdapter) KeypoolRefill(newsize int) error {
	return a.w.KeypoolRefill(newsize)
}

// GetKeypoolOldest returns the timestamp of the oldest key in the keypool
func (a *WalletAdapter) GetKeypoolOldest() int64 {
	return a.w.GetKeypoolOldest()
}

// GetKeypoolSize returns the number of keys in the keypool
func (a *WalletAdapter) GetKeypoolSize() int {
	return a.w.GetKeypoolSize()
}

// GetReserveBalance returns the reserve balance setting
func (a *WalletAdapter) GetReserveBalance() (bool, int64, error) {
	enabled, amount, err := a.w.GetReserveBalance()
	if err != nil {
		return false, 0, err
	}
	return enabled, amount, nil
}

// SetReserveBalance sets the reserve balance
func (a *WalletAdapter) SetReserveBalance(enabled bool, amount int64) error {
	return a.w.SetReserveBalance(enabled, amount)
}

// GetStakeSplitThreshold returns the stake split threshold
func (a *WalletAdapter) GetStakeSplitThreshold() (int64, error) {
	return a.w.GetStakeSplitThreshold()
}

// SetStakeSplitThreshold sets the stake split threshold
func (a *WalletAdapter) SetStakeSplitThreshold(threshold int64) error {
	return a.w.SetStakeSplitThreshold(threshold)
}

// GetAutoCombineConfig returns the current autocombine configuration
func (a *WalletAdapter) GetAutoCombineConfig() (enabled bool, target int64, cooldown int) {
	return a.w.GetAutoCombineConfig()
}

// SetAutoCombineConfig updates the autocombine configuration
func (a *WalletAdapter) SetAutoCombineConfig(enabled bool, target int64, cooldown int) {
	a.w.SetAutoCombineConfig(enabled, target, cooldown)
}

// SetTransactionFee sets the transaction fee per kilobyte
func (a *WalletAdapter) SetTransactionFee(feePerKB int64) error {
	return a.w.SetTransactionFee(feePerKB)
}

// AddMultisigAddress adds a multisignature address to the wallet
func (a *WalletAdapter) AddMultisigAddress(nrequired int, keys []string, account string) (string, error) {
	return a.w.AddMultisigAddress(nrequired, keys, account)
}

// GetMultiSend returns the current multisend configuration
func (a *WalletAdapter) GetMultiSend() (interface{}, error) {
	return a.w.GetMultiSend()
}

// SetMultiSend sets the multisend configuration
func (a *WalletAdapter) SetMultiSend(entries interface{}) error {
	// Convert interface{} to []wallet.MultiSendEntry
	if entries == nil {
		return a.w.SetMultiSend(nil)
	}

	// Type assertion based on what we expect from RPC layer
	switch v := entries.(type) {
	case []wallet.MultiSendEntry:
		return a.w.SetMultiSend(v)
	default:
		return fmt.Errorf("invalid multisend entries type")
	}
}

// GetMultiSendSettings returns the multisend settings
func (a *WalletAdapter) GetMultiSendSettings() (stakeEnabled bool, masternodeEnabled bool, disabledAddrs []string, err error) {
	return a.w.GetMultiSendSettings()
}

// SetMultiSendSettings sets the multisend settings
func (a *WalletAdapter) SetMultiSendSettings(stakeEnabled bool, masternodeEnabled bool, disabledAddrs []string) error {
	return a.w.SetMultiSendSettings(stakeEnabled, masternodeEnabled, disabledAddrs)
}

// LockCoin locks a UTXO to prevent it from being selected for spending.
func (a *WalletAdapter) LockCoin(outpoint types.Outpoint) {
	a.w.LockCoin(outpoint)
}

// UnlockCoin unlocks a previously locked UTXO.
func (a *WalletAdapter) UnlockCoin(outpoint types.Outpoint) {
	a.w.UnlockCoin(outpoint)
}

// UnlockAllCoins removes all user-set coin locks.
func (a *WalletAdapter) UnlockAllCoins() {
	a.w.UnlockAllCoins()
}

// IsLockedCoin checks if a UTXO is locked by the user.
func (a *WalletAdapter) IsLockedCoin(outpoint types.Outpoint) bool {
	return a.w.IsLockedCoin(outpoint)
}

// ListLockedCoins returns all currently locked outpoints.
func (a *WalletAdapter) ListLockedCoins() []types.Outpoint {
	return a.w.ListLockedCoins()
}
