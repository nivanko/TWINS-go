package rpc

import (
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/twins-dev/twins-core/internal/wallet"
	"github.com/twins-dev/twins-core/pkg/crypto"
	"github.com/twins-dev/twins-core/pkg/types"
)

// registerWalletHandlers registers wallet-related RPC handlers
func (s *Server) registerWalletHandlers() {
	s.RegisterHandler("getnewaddress", s.handleGetNewAddress)
	s.RegisterHandler("getaccountaddress", s.handleGetAccountAddress)
	s.RegisterHandler("getrawchangeaddress", s.handleGetRawChangeAddress)
	s.RegisterHandler("getbalance", s.handleGetBalance)
	s.RegisterHandler("getunconfirmedbalance", s.handleGetUnconfirmedBalance)
	s.RegisterHandler("sendtoaddress", s.handleSendToAddress)
	s.RegisterHandler("sendfrom", s.handleSendFrom)
	s.RegisterHandler("sendmany", s.handleSendMany)
	s.RegisterHandler("setaccount", s.handleSetAccount)
	s.RegisterHandler("getaccount", s.handleGetAccount)
	s.RegisterHandler("getaddressesbyaccount", s.handleGetAddressesByAccount)
	s.RegisterHandler("getreceivedbyaddress", s.handleGetReceivedByAddress)
	s.RegisterHandler("getreceivedbyaccount", s.handleGetReceivedByAccount)
	s.RegisterHandler("listreceivedbyaddress", s.handleListReceivedByAddress)
	s.RegisterHandler("listreceivedbyaccount", s.handleListReceivedByAccount)
	s.RegisterHandler("listunspent", s.handleListUnspent)
	s.RegisterHandler("lockunspent", s.handleLockUnspent)
	s.RegisterHandler("listlockunspent", s.handleListLockUnspent)
	s.RegisterHandler("listtransactions", s.handleListTransactions)
	s.RegisterHandler("gettransaction", s.handleGetTransaction)
	s.RegisterHandler("listsinceblock", s.handleListSinceBlock)
	s.RegisterHandler("listaccounts", s.handleListAccounts)
	s.RegisterHandler("listaddressgroupings", s.handleListAddressGroupings)
	s.RegisterHandler("getaddressinfo", s.handleGetAddressInfo)
	s.RegisterHandler("validateaddress", s.handleValidateAddress)
	s.RegisterHandler("dumpprivkey", s.handleDumpPrivKey)
	s.RegisterHandler("importprivkey", s.handleImportPrivKey)
	s.RegisterHandler("importaddress", s.handleImportAddress)
	s.RegisterHandler("dumpwallet", s.handleDumpWallet)
	s.RegisterHandler("importwallet", s.handleImportWallet)
	s.RegisterHandler("dumphdinfo", s.handleDumpHDInfo)
	s.RegisterHandler("encryptwallet", s.handleEncryptWallet)
	s.RegisterHandler("walletpassphrase", s.handleWalletPassphrase)
	s.RegisterHandler("walletlock", s.handleWalletLock)
	s.RegisterHandler("walletpassphrasechange", s.handleWalletPassphraseChange)
	s.RegisterHandler("listaddresses", s.handleListAddresses)
	s.RegisterHandler("signmessage", s.handleSignMessage)
	s.RegisterHandler("verifymessage", s.handleVerifyMessage)
	s.RegisterHandler("getwalletinfo", s.handleGetWalletInfo)
	s.RegisterHandler("backupwallet", s.handleBackupWallet)
	s.RegisterHandler("keypoolrefill", s.handleKeypoolRefill)
	s.RegisterHandler("reservebalance", s.handleReserveBalance)
	s.RegisterHandler("setstakesplitthreshold", s.handleSetStakeSplitThreshold)
	s.RegisterHandler("getstakesplitthreshold", s.handleGetStakeSplitThreshold)
	s.RegisterHandler("setautocombine", s.handleSetAutoCombine)
	s.RegisterHandler("getautocombine", s.handleGetAutoCombine)
	s.RegisterHandler("multisend", s.handleMultiSend)
	s.RegisterHandler("addmultisigaddress", s.handleAddMultisigAddress)
	s.RegisterHandler("createmultisig", s.handleCreateMultisig)
}

// handleGetNewAddress generates a new receiving address
func (s *Server) handleGetNewAddress(req *Request) *Response {
	var params []interface{}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewInvalidParamsError("invalid parameters"),
				ID:      req.ID,
			}
		}
	}

	label := ""
	if len(params) > 0 {
		if l, ok := params[0].(string); ok {
			label = l
		}
	}

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	address, err := s.wallet.GetNewAddress(label)
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  address,
		ID:      req.ID,
	}
}

// handleGetAccountAddress returns the current TWINS address for receiving payments to an account
func (s *Server) handleGetAccountAddress(req *Request) *Response {
	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("invalid parameters"),
			ID:      req.ID,
		}
	}

	// Account parameter is required
	if len(params) < 1 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("getaccountaddress requires account parameter"),
			ID:      req.ID,
		}
	}

	account, ok := params[0].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("account must be a string"),
			ID:      req.ID,
		}
	}

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	// Get or create address for account
	// In the legacy implementation, this returns the current receiving address for the account
	// If no address exists for the account, it creates a new one
	// Note: This RPC is deprecated (removed from Bitcoin Core v0.17.0 in 2018).
	// Modern wallets use label-based addressing instead of account-based.
	// Current implementation creates a new address with the account as a label,
	// which provides reasonable compatibility for legacy clients.
	address, err := s.wallet.GetNewAddress(account)
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError("failed to get account address: " + err.Error()),
			ID:      req.ID,
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  address,
		ID:      req.ID,
	}
}

// handleGetBalance returns the wallet balance
func (s *Server) handleGetBalance(req *Request) *Response {
	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	balance := s.wallet.GetBalance()

	// Convert satoshis to TWINS (divide by 1e8)
	balanceTWINS := float64(balance.Confirmed) / 1e8

	return &Response{
		JSONRPC: "2.0",
		Result:  balanceTWINS,
		ID:      req.ID,
	}
}

// handleSendToAddress sends TWINS to an address
func (s *Server) handleSendToAddress(req *Request) *Response {
	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("invalid parameters"),
			ID:      req.ID,
		}
	}

	if len(params) < 2 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("sendtoaddress requires address and amount"),
			ID:      req.ID,
		}
	}

	address, ok := params[0].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("address must be a string"),
			ID:      req.ID,
		}
	}

	amount, ok := params[1].(float64)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("amount must be a number"),
			ID:      req.ID,
		}
	}

	comment := ""
	if len(params) > 2 {
		if c, ok := params[2].(string); ok {
			comment = c
		}
	}

	commentTo := ""
	if len(params) > 3 {
		if ct, ok := params[3].(string); ok {
			commentTo = ct
		}
	}

	subtractFee := false
	if len(params) > 4 {
		if sf, ok := params[4].(bool); ok {
			subtractFee = sf
		}
	}

	// Validate amount range FIRST (before wallet lock/address checks)
	// This prevents information leakage about wallet state
	if amount <= 0 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("Invalid amount: must be positive"),
			ID:      req.ID,
		}
	}

	const MAX_MONEY = 21000000000.0 // 21 billion TWINS
	if amount > MAX_MONEY {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("Invalid amount: exceeds maximum"),
			ID:      req.ID,
		}
	}

	// Convert TWINS amount to satoshis with precision check
	amountSatoshis := int64(amount * 1e8)
	// Verify conversion is reversible (no precision loss)
	if float64(amountSatoshis)/1e8 != amount {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("Invalid amount: too many decimal places (max 8)"),
			ID:      req.ID,
		}
	}

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-4, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	// Check wallet is unlocked for sending
	if s.wallet.IsLocked() {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-13, "Error: Please enter the wallet passphrase with walletpassphrase first.", nil),
			ID:      req.ID,
		}
	}

	// Validate address
	validation, err := s.wallet.ValidateAddress(address)
	if err != nil || !validation.IsValid {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("Invalid TWINS address"),
			ID:      req.ID,
		}
	}

	// Use commentTo as the comment field (Bitcoin Core style)
	fullComment := comment
	if commentTo != "" {
		fullComment = comment + " to " + commentTo
	}

	txHash, err := s.wallet.SendToAddress(address, amountSatoshis, fullComment, subtractFee)
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  txHash,
		ID:      req.ID,
	}
}

// handleListUnspent lists unspent transaction outputs
func (s *Server) handleListUnspent(req *Request) *Response {
	var params []interface{}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewInvalidParamsError("invalid parameters"),
				ID:      req.ID,
			}
		}
	}

	minConf := 1
	if len(params) > 0 {
		if mc, ok := params[0].(float64); ok {
			minConf = int(mc)
		}
	}

	maxConf := 9999999
	if len(params) > 1 {
		if mc, ok := params[1].(float64); ok {
			maxConf = int(mc)
		}
	}

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	// Parse addresses filter (optional 3rd param)
	var addresses []string
	if len(params) > 2 {
		if addrArray, ok := params[2].([]interface{}); ok {
			for _, addr := range addrArray {
				if addrStr, ok := addr.(string); ok {
					addresses = append(addresses, addrStr)
				}
			}
		}
	}

	utxos, err := s.wallet.ListUnspent(minConf, maxConf, addresses)
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  utxos,
		ID:      req.ID,
	}
	//
	// result := make([]map[string]interface{}, 0, len(utxos))
	// for _, utxo := range utxos {
	// 	if utxo.Confirmations < int32(minConf) || utxo.Confirmations > int32(maxConf) {
	// 		continue
	// 	}
	//
	// 	result = append(result, map[string]interface{}{
	// 		"txid":          utxo.TxHash.String(),
	// 		"vout":          utxo.Index,
	// 		"address":       utxo.Address,
	// 		"scriptPubKey":  hex.EncodeToString(utxo.ScriptPubKey),
	// 		"amount":        float64(utxo.Amount) / 1e8,
	// 		"confirmations": utxo.Confirmations,
	// 		"spendable":     utxo.Spendable,
	// 	})
	// }
	//
	// return &Response{
	// 	JSONRPC: "2.0",
	// 	Result:  result,
	// 	ID:      req.ID,
	// }
}

// handleLockUnspent locks or unlocks unspent outputs via the unified wallet lock store.
// Legacy: Delegates to CWallet::LockCoin/UnlockCoin/UnlockAllCoins — shared by both GUI and RPC.
func (s *Server) handleLockUnspent(req *Request) *Response {
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("invalid parameters"),
			ID:      req.ID,
		}
	}

	if len(params) < 1 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("missing unlock parameter"),
			ID:      req.ID,
		}
	}

	unlock, ok := params[0].(bool)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("unlock must be a boolean"),
			ID:      req.ID,
		}
	}

	// Parse transactions array (optional)
	var transactions []map[string]interface{}
	if len(params) > 1 && params[1] != nil {
		txArray, ok := params[1].([]interface{})
		if !ok {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewInvalidParamsError("transactions must be an array"),
				ID:      req.ID,
			}
		}

		for _, txRaw := range txArray {
			txMap, ok := txRaw.(map[string]interface{})
			if !ok {
				return &Response{
					JSONRPC: "2.0",
					Error:   NewInvalidParamsError("invalid transaction object"),
					ID:      req.ID,
				}
			}
			transactions = append(transactions, txMap)
		}
	}

	// If no transactions specified:
	// - unlock=true: unlock all coins (legacy CWallet::UnlockAllCoins())
	// - unlock=false: no-op, returns true (legacy behavior, nothing to lock)
	if len(transactions) == 0 {
		if unlock {
			s.wallet.UnlockAllCoins()
		}
		return &Response{
			JSONRPC: "2.0",
			Result:  true,
			ID:      req.ID,
		}
	}

	// Process each transaction via the wallet's unified lock store
	for _, txMap := range transactions {
		txidStr, ok := txMap["txid"].(string)
		if !ok {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewInvalidParamsError("missing or invalid txid"),
				ID:      req.ID,
			}
		}

		voutFloat, ok := txMap["vout"].(float64)
		if !ok {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewInvalidParamsError("missing or invalid vout"),
				ID:      req.ID,
			}
		}

		txHash, err := types.NewHashFromString(txidStr)
		if err != nil {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewInvalidParamsError("invalid transaction hash"),
				ID:      req.ID,
			}
		}

		outpoint := types.Outpoint{
			Hash:  txHash,
			Index: uint32(voutFloat),
		}

		if unlock {
			s.wallet.UnlockCoin(outpoint)
		} else {
			s.wallet.LockCoin(outpoint)
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  true,
		ID:      req.ID,
	}
}

// handleListLockUnspent returns list of locked unspent outputs from the unified wallet lock store.
// Legacy: Delegates to CWallet::ListLockedCoins — shared by both GUI and RPC.
func (s *Server) handleListLockUnspent(req *Request) *Response {
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	locked := s.wallet.ListLockedCoins()

	result := make([]map[string]interface{}, 0, len(locked))
	for _, outpoint := range locked {
		result = append(result, map[string]interface{}{
			"txid": outpoint.Hash.String(),
			"vout": outpoint.Index,
		})
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  result,
		ID:      req.ID,
	}
}

// handleListTransactions lists recent transactions
func (s *Server) handleListTransactions(req *Request) *Response {
	var params []interface{}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewInvalidParamsError("invalid parameters"),
				ID:      req.ID,
			}
		}
	}

	// Parse parameters - support both legacy and new formats
	// Legacy: [account, count, skip]
	// New: [count, skip]
	paramOffset := 0

	// Check if first param is account (string) - legacy format
	if len(params) > 0 {
		if _, ok := params[0].(string); ok {
			paramOffset = 1 // Skip account parameter
		}
	}

	count := 10
	if len(params) > paramOffset {
		if c, ok := params[paramOffset].(float64); ok {
			count = int(c)
		}
	}

	skip := 0
	if len(params) > paramOffset+1 {
		if s, ok := params[paramOffset+1].(float64); ok {
			skip = int(s)
		}
	}

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	txs, err := s.wallet.ListTransactions(count, skip)
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	// Convert amounts from satoshi to TWINS
	result := make([]map[string]interface{}, len(txs))
	for i, tx := range txs {
		result[i] = map[string]interface{}{
			"txid":          tx.TxID,
			"amount":        float64(tx.Amount) / 1e8,
			"fee":           float64(tx.Fee) / 1e8,
			"confirmations": tx.Confirmations,
			"blockhash":     tx.BlockHash,
			"blockheight":   tx.BlockHeight,
			"blocktime":     tx.BlockTime,
			"time":          tx.Time,
			"timereceived":  tx.TimeReceived,
			"comment":       tx.Comment,
			"label":         tx.Label,
			"address":       tx.Address,
			"category":      tx.Category,
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  result,
		ID:      req.ID,
	}
}

// Note: handleGetTransaction is defined in transactions.go and handles both
// blockchain and wallet transactions

// handleGetAddressInfo returns information about an address
func (s *Server) handleGetAddressInfo(req *Request) *Response {
	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("invalid parameters"),
			ID:      req.ID,
		}
	}

	if len(params) < 1 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("getaddressinfo requires address parameter"),
			ID:      req.ID,
		}
	}

	address, ok := params[0].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("address must be a string"),
			ID:      req.ID,
		}
	}

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	info, err := s.wallet.GetAddressInfo(address)
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  info,
		ID:      req.ID,
	}
}

// handleValidateAddress validates an address
func (s *Server) handleValidateAddress(req *Request) *Response {
	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("invalid parameters"),
			ID:      req.ID,
		}
	}

	if len(params) < 1 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("validateaddress requires address parameter"),
			ID:      req.ID,
		}
	}

	address, ok := params[0].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("address must be a string"),
			ID:      req.ID,
		}
	}

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	validation, err := s.wallet.ValidateAddress(address)
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  validation,
		ID:      req.ID,
	}
}

// handleDumpPrivKey dumps private key for an address
func (s *Server) handleDumpPrivKey(req *Request) *Response {
	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("invalid parameters"),
			ID:      req.ID,
		}
	}

	if len(params) < 1 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("dumpprivkey requires address parameter"),
			ID:      req.ID,
		}
	}

	address, ok := params[0].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("address must be a string"),
			ID:      req.ID,
		}
	}

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	privKey, err := s.wallet.DumpPrivKey(address)
	if err != nil {
		s.logger.WithError(err).WithField("address", address).Error("DumpPrivKey failed")
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  privKey,
		ID:      req.ID,
	}
}

// handleImportPrivKey imports a private key
func (s *Server) handleImportPrivKey(req *Request) *Response {
	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("invalid parameters"),
			ID:      req.ID,
		}
	}

	if len(params) < 1 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("importprivkey requires privkey parameter"),
			ID:      req.ID,
		}
	}

	privkey, ok := params[0].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("privkey must be a string"),
			ID:      req.ID,
		}
	}

	label := ""
	if len(params) > 1 {
		if l, ok := params[1].(string); ok {
			label = l
		}
	}

	rescan := true
	if len(params) > 2 {
		if r, ok := params[2].(bool); ok {
			rescan = r
		}
	}

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	if err := s.wallet.ImportPrivKey(privkey, label, rescan); err != nil {
		s.logger.WithError(err).WithField("label", label).Error("ImportPrivKey failed")
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  nil,
		ID:      req.ID,
	}
}

// handleEncryptWallet encrypts the wallet with a passphrase
func (s *Server) handleEncryptWallet(req *Request) *Response {
	// Security check: Ensure RPC authentication is enabled before allowing wallet encryption
	// This is a defense-in-depth measure - even though the server won't start without
	// authentication (checked in startup_improved.go:425-435), we verify it again here for
	// this critical security operation.
	if s.config.Username == "" || s.config.Password == "" {
		return &Response{
			JSONRPC: "2.0",
			Error: &Error{
				Code:    CodeWalletError,
				Message: "encryptwallet requires RPC authentication to be enabled",
			},
			ID: req.ID,
		}
	}

	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("invalid parameters"),
			ID:      req.ID,
		}
	}

	if len(params) < 1 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("encryptwallet requires passphrase parameter"),
			ID:      req.ID,
		}
	}

	passphraseStr, ok := params[0].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("passphrase must be a string"),
			ID:      req.ID,
		}
	}

	// Convert string to []byte for secure handling
	passphrase := []byte(passphraseStr)
	defer func() {
		// Zero passphrase from memory after use
		for i := range passphrase {
			passphrase[i] = 0
		}
	}()

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	if err := s.wallet.EncryptWallet(passphrase); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	// Note: Unlike legacy C++ which required restart due to BerkeleyDB slack space issues,
	// Go's BBolt database uses atomic writes and doesn't leave plaintext in slack space.
	// Hot encryption is safe - no restart required.

	return &Response{
		JSONRPC: "2.0",
		Result:  "wallet encrypted successfully",
		ID:      req.ID,
	}
}

// handleWalletPassphrase unlocks the wallet
func (s *Server) handleWalletPassphrase(req *Request) *Response {
	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("invalid parameters"),
			ID:      req.ID,
		}
	}

	if len(params) < 2 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("walletpassphrase requires passphrase and timeout"),
			ID:      req.ID,
		}
	}

	passphraseStr, ok := params[0].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("passphrase must be a string"),
			ID:      req.ID,
		}
	}

	// Convert string to []byte for secure handling
	passphrase := []byte(passphraseStr)
	defer func() {
		// Zero passphrase from memory after use
		for i := range passphrase {
			passphrase[i] = 0
		}
	}()

	timeout, ok := params[1].(float64)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("timeout must be a number"),
			ID:      req.ID,
		}
	}

	// Parse optional stakingonly parameter (3rd param)
	stakingOnly := false
	if len(params) >= 3 {
		if so, ok := params[2].(bool); ok {
			stakingOnly = so
		}
	}

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	if err := s.wallet.WalletPassphrase(passphrase, int64(timeout), stakingOnly); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  nil,
		ID:      req.ID,
	}
}

// handleWalletLock locks the wallet
func (s *Server) handleWalletLock(req *Request) *Response {
	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	if err := s.wallet.WalletLock(); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  nil,
		ID:      req.ID,
	}
}

// handleWalletPassphraseChange changes wallet passphrase
func (s *Server) handleWalletPassphraseChange(req *Request) *Response {
	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("invalid parameters"),
			ID:      req.ID,
		}
	}

	if len(params) < 2 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("walletpassphrasechange requires old and new passphrase"),
			ID:      req.ID,
		}
	}

	oldPassphraseStr, ok := params[0].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("old passphrase must be a string"),
			ID:      req.ID,
		}
	}

	newPassphraseStr, ok := params[1].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("new passphrase must be a string"),
			ID:      req.ID,
		}
	}

	// Convert strings to []byte for secure handling
	oldPassphrase := []byte(oldPassphraseStr)
	newPassphrase := []byte(newPassphraseStr)
	defer func() {
		// Zero both passphrases from memory after use
		for i := range oldPassphrase {
			oldPassphrase[i] = 0
		}
		for i := range newPassphrase {
			newPassphrase[i] = 0
		}
	}()

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	if err := s.wallet.WalletPassphraseChange(oldPassphrase, newPassphrase); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  nil,
		ID:      req.ID,
	}
}

// handleListAddresses lists all wallet addresses
func (s *Server) handleListAddresses(req *Request) *Response {
	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	addresses, err := s.wallet.ListAddresses()
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  addresses,
		ID:      req.ID,
	}
}

// handleSignMessage signs a message with the private key of an address
func (s *Server) handleSignMessage(req *Request) *Response {
	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("invalid parameters"),
			ID:      req.ID,
		}
	}

	if len(params) < 2 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("missing address or message"),
			ID:      req.ID,
		}
	}

	address, ok := params[0].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("address must be a string"),
			ID:      req.ID,
		}
	}

	message, ok := params[1].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("message must be a string"),
			ID:      req.ID,
		}
	}

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	// Sign message
	signature, err := s.wallet.SignMessage(address, message)
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  signature,
		ID:      req.ID,
	}
}

// handleVerifyMessage verifies a signed message
func (s *Server) handleVerifyMessage(req *Request) *Response {
	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("invalid parameters"),
			ID:      req.ID,
		}
	}

	if len(params) < 3 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("missing address, signature, or message"),
			ID:      req.ID,
		}
	}

	address, ok := params[0].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("address must be a string"),
			ID:      req.ID,
		}
	}

	signature, ok := params[1].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("signature must be a string"),
			ID:      req.ID,
		}
	}

	message, ok := params[2].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("message must be a string"),
			ID:      req.ID,
		}
	}

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	// Verify message
	valid, err := s.wallet.VerifyMessage(address, signature, message)
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  valid,
		ID:      req.ID,
	}
}

// handleGetRawChangeAddress returns a new TWINS address for receiving change
func (s *Server) handleGetRawChangeAddress(req *Request) *Response {
	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	address, err := s.wallet.GetChangeAddress()
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  address,
		ID:      req.ID,
	}
}

// handleSetAccount sets the account associated with the given address
func (s *Server) handleSetAccount(req *Request) *Response {
	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("invalid parameters"),
			ID:      req.ID,
		}
	}

	if len(params) < 2 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("setaccount requires address and account parameters"),
			ID:      req.ID,
		}
	}

	address, ok := params[0].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("address must be a string"),
			ID:      req.ID,
		}
	}

	account, ok := params[1].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("account must be a string"),
			ID:      req.ID,
		}
	}

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-4, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	// Validate address
	validation, err := s.wallet.ValidateAddress(address)
	if err != nil || !validation.IsValid {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("Invalid TWINS address"),
			ID:      req.ID,
		}
	}

	// Set label (account is just a label in modern wallet implementation)
	if err := s.wallet.SetLabel(address, account); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  nil,
		ID:      req.ID,
	}
}

// handleGetAccount returns the account associated with the given address
func (s *Server) handleGetAccount(req *Request) *Response {
	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("invalid parameters"),
			ID:      req.ID,
		}
	}

	if len(params) < 1 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("getaccount requires address parameter"),
			ID:      req.ID,
		}
	}

	address, ok := params[0].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("address must be a string"),
			ID:      req.ID,
		}
	}

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	// Validate address first (consistency with setaccount)
	validation, err := s.wallet.ValidateAddress(address)
	if err != nil || !validation.IsValid {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("Invalid TWINS address"),
			ID:      req.ID,
		}
	}

	// Get address label (account)
	info, err := s.wallet.GetAddressInfo(address)
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	// Return empty string if no label set
	label := ""
	if info != nil {
		label = info.Label
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  label,
		ID:      req.ID,
	}
}

// handleGetAddressesByAccount returns the list of addresses for the given account
func (s *Server) handleGetAddressesByAccount(req *Request) *Response {
	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("invalid parameters"),
			ID:      req.ID,
		}
	}

	if len(params) < 1 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("getaddressesbyaccount requires account parameter"),
			ID:      req.ID,
		}
	}

	account, ok := params[0].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("account must be a string"),
			ID:      req.ID,
		}
	}

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	// List all addresses
	addresses, err := s.wallet.ListAddresses()
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	// Filter by account (label)
	var result []string
	for _, addr := range addresses {
		if addr.Label == account {
			result = append(result, addr.Address)
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  result,
		ID:      req.ID,
	}
}

// handleGetUnconfirmedBalance returns the server's total unconfirmed balance
func (s *Server) handleGetUnconfirmedBalance(req *Request) *Response {
	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	balance := s.wallet.GetBalance()

	// Convert satoshis to TWINS (divide by 1e8)
	balanceTWINS := float64(balance.Unconfirmed) / 1e8

	return &Response{
		JSONRPC: "2.0",
		Result:  balanceTWINS,
		ID:      req.ID,
	}
}

// handleGetReceivedByAddress returns the total amount received by the given address
func (s *Server) handleGetReceivedByAddress(req *Request) *Response {
	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("invalid parameters"),
			ID:      req.ID,
		}
	}

	if len(params) < 1 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("getreceivedbyaddress requires address parameter"),
			ID:      req.ID,
		}
	}

	address, ok := params[0].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("address must be a string"),
			ID:      req.ID,
		}
	}

	minConf := 1
	if len(params) > 1 {
		if mc, ok := params[1].(float64); ok {
			minConf = int(mc)
		}
	}

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	// Get all transactions
	txs, err := s.wallet.ListTransactions(999999, 0)
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	// Sum received amounts for this address with sufficient confirmations
	var totalReceived int64
	for _, tx := range txs {
		if tx.Category == "receive" && tx.Address == address && tx.Confirmations >= int32(minConf) {
			totalReceived += tx.Amount
		}
	}

	// Convert satoshis to TWINS
	amountTWINS := float64(totalReceived) / 1e8

	return &Response{
		JSONRPC: "2.0",
		Result:  amountTWINS,
		ID:      req.ID,
	}
}

// handleGetReceivedByAccount returns the total amount received by addresses in an account
func (s *Server) handleGetReceivedByAccount(req *Request) *Response {
	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("invalid parameters"),
			ID:      req.ID,
		}
	}

	if len(params) < 1 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("getreceivedbyaccount requires account parameter"),
			ID:      req.ID,
		}
	}

	account, ok := params[0].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("account must be a string"),
			ID:      req.ID,
		}
	}

	minConf := 1
	if len(params) > 1 {
		if mc, ok := params[1].(float64); ok {
			minConf = int(mc)
		}
	}

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	// Get all addresses for this account
	addresses, err := s.wallet.ListAddresses()
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	// Build set of addresses in this account
	accountAddresses := make(map[string]bool)
	for _, addr := range addresses {
		if addr.Label == account {
			accountAddresses[addr.Address] = true
		}
	}

	// Get all transactions
	txs, err := s.wallet.ListTransactions(999999, 0)
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	// Sum received amounts for addresses in this account
	var totalReceived int64
	for _, tx := range txs {
		if tx.Category == "receive" && accountAddresses[tx.Address] && tx.Confirmations >= int32(minConf) {
			totalReceived += tx.Amount
		}
	}

	// Convert satoshis to TWINS
	amountTWINS := float64(totalReceived) / 1e8

	return &Response{
		JSONRPC: "2.0",
		Result:  amountTWINS,
		ID:      req.ID,
	}
}

// handleSendFrom sends an amount from an account to a TWINS address
func (s *Server) handleSendFrom(req *Request) *Response {
	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("invalid parameters"),
			ID:      req.ID,
		}
	}

	if len(params) < 3 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("sendfrom requires account, address, and amount"),
			ID:      req.ID,
		}
	}

	account, ok := params[0].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("account must be a string"),
			ID:      req.ID,
		}
	}

	address, ok := params[1].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("address must be a string"),
			ID:      req.ID,
		}
	}

	amount, ok := params[2].(float64)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("amount must be a number"),
			ID:      req.ID,
		}
	}

	minConf := 1
	if len(params) > 3 {
		if mc, ok := params[3].(float64); ok {
			minConf = int(mc)
		}
	}

	comment := ""
	if len(params) > 4 {
		if c, ok := params[4].(string); ok {
			comment = c
		}
	}

	commentTo := ""
	if len(params) > 5 {
		if ct, ok := params[5].(string); ok {
			commentTo = ct
		}
	}

	// Validate amount range FIRST (before wallet lock/address checks)
	// This prevents information leakage about wallet state
	if amount <= 0 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("Invalid amount: must be positive"),
			ID:      req.ID,
		}
	}

	const MAX_MONEY = 21000000000.0 // 21 billion TWINS
	if amount > MAX_MONEY {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("Invalid amount: exceeds maximum"),
			ID:      req.ID,
		}
	}

	// Convert TWINS amount to satoshis with precision check
	amountSatoshis := int64(amount * 1e8)
	// Verify conversion is reversible (no precision loss)
	if float64(amountSatoshis)/1e8 != amount {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("Invalid amount: too many decimal places (max 8)"),
			ID:      req.ID,
		}
	}

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-4, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	// Check wallet is unlocked for sending
	if s.wallet.IsLocked() {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-13, "Error: Please enter the wallet passphrase with walletpassphrase first.", nil),
			ID:      req.ID,
		}
	}

	// Validate address
	validation, err := s.wallet.ValidateAddress(address)
	if err != nil || !validation.IsValid {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("Invalid TWINS address"),
			ID:      req.ID,
		}
	}

	// Build full comment
	fullComment := comment
	if commentTo != "" {
		fullComment = comment + " to " + commentTo
	}

	// Note: This RPC is deprecated (removed from Bitcoin Core v0.17.0 in 2018).
	// In the legacy implementation, sendfrom would only use UTXOs from addresses
	// with the specified account label. Modern implementation sends from any available UTXO
	// and records the account in the comment. Full account-based UTXO selection would require
	// significant wallet changes and is not necessary for reasonable legacy compatibility.
	_ = account  // Mark as used
	_ = minConf  // Mark as used

	txHash, err := s.wallet.SendToAddress(address, amountSatoshis, fullComment, false)
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  txHash,
		ID:      req.ID,
	}
}

// handleSendMany sends TWINS to multiple addresses in one transaction
func (s *Server) handleSendMany(req *Request) *Response {
	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("invalid parameters"),
			ID:      req.ID,
		}
	}

	if len(params) < 2 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("sendmany requires account and amounts"),
			ID:      req.ID,
		}
	}

	account, ok := params[0].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("account must be a string"),
			ID:      req.ID,
		}
	}

	// Parse amounts object (address -> amount mapping)
	amountsObj, ok := params[1].(map[string]interface{})
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("amounts must be an object"),
			ID:      req.ID,
		}
	}

	minConf := 1
	if len(params) > 2 {
		if mc, ok := params[2].(float64); ok {
			minConf = int(mc)
		}
	}

	comment := ""
	if len(params) > 3 {
		if c, ok := params[3].(string); ok {
			comment = c
		}
	}

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-4, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	// Check wallet is unlocked for sending
	if s.wallet.IsLocked() {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-13, "Error: Please enter the wallet passphrase with walletpassphrase first.", nil),
			ID:      req.ID,
		}
	}

	// Build recipients map and validate amounts
	const MAX_MONEY = 21000000000.0 // 21 billion TWINS
	recipients := make(map[string]int64)
	var totalAmount int64
	addressSet := make(map[string]bool)

	for address, amountVal := range amountsObj {
		// Validate address
		validation, err := s.wallet.ValidateAddress(address)
		if err != nil || !validation.IsValid {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewInvalidParamsError("Invalid TWINS address: " + address),
				ID:      req.ID,
			}
		}

		// Check for duplicate addresses
		if addressSet[address] {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewInvalidParamsError("Invalid parameter, duplicated address: " + address),
				ID:      req.ID,
			}
		}
		addressSet[address] = true

		// Parse and validate amount
		amount, ok := amountVal.(float64)
		if !ok {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewInvalidParamsError("Invalid amount for address " + address),
				ID:      req.ID,
			}
		}

		if amount <= 0 {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewInvalidParamsError("Invalid amount: must be positive"),
				ID:      req.ID,
			}
		}

		if amount > MAX_MONEY {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewInvalidParamsError("Invalid amount: exceeds maximum"),
				ID:      req.ID,
			}
		}

		// Convert TWINS amount to satoshis with precision check
		amountSatoshis := int64(amount * 1e8)
		if float64(amountSatoshis)/1e8 != amount {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewInvalidParamsError("Invalid amount: too many decimal places (max 8)"),
				ID:      req.ID,
			}
		}

		recipients[address] = amountSatoshis
		totalAmount += amountSatoshis
	}

	// Validate total amount
	if totalAmount <= 0 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("Invalid total amount"),
			ID:      req.ID,
		}
	}

	// Note: This RPC is deprecated (removed from Bitcoin Core v0.17.0 in 2018).
	// In the legacy implementation, sendmany would only use UTXOs from addresses
	// with the specified account label. Modern implementation sends from any available UTXO.
	// Full account-based UTXO selection would require significant wallet changes and is not
	// necessary for reasonable legacy compatibility.
	_ = account
	_ = minConf

	// Send to multiple addresses
	txHash, err := s.wallet.SendMany(recipients, comment)
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  txHash,
		ID:      req.ID,
	}
}

// handleListReceivedByAddress lists amounts received by each address
func (s *Server) handleListReceivedByAddress(req *Request) *Response {
	var params []interface{}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewInvalidParamsError("invalid parameters"),
				ID:      req.ID,
			}
		}
	}

	// Parse optional parameters
	minConf := 1
	if len(params) > 0 {
		if mc, ok := params[0].(float64); ok {
			minConf = int(mc)
		}
	}

	includeEmpty := false
	if len(params) > 1 {
		if ie, ok := params[1].(bool); ok {
			includeEmpty = ie
		}
	}

	includeWatchOnly := false
	if len(params) > 2 {
		if iw, ok := params[2].(bool); ok {
			includeWatchOnly = iw
		}
	}

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	// List received by address (adapter already converts to TWINS)
	received, err := s.wallet.ListReceivedByAddress(minConf, includeEmpty, includeWatchOnly)
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  received,
		ID:      req.ID,
	}
}

// handleListReceivedByAccount lists amounts received by each account
func (s *Server) handleListReceivedByAccount(req *Request) *Response {
	var params []interface{}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewInvalidParamsError("invalid parameters"),
				ID:      req.ID,
			}
		}
	}

	// Parse optional parameters
	minConf := 1
	if len(params) > 0 {
		if mc, ok := params[0].(float64); ok {
			minConf = int(mc)
		}
	}

	includeEmpty := false
	if len(params) > 1 {
		if ie, ok := params[1].(bool); ok {
			includeEmpty = ie
		}
	}

	includeWatchOnly := false
	if len(params) > 2 {
		if iw, ok := params[2].(bool); ok {
			includeWatchOnly = iw
		}
	}

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	// List received by account (adapter already converts to TWINS)
	received, err := s.wallet.ListReceivedByAccount(minConf, includeEmpty, includeWatchOnly)
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  received,
		ID:      req.ID,
	}
}

// handleListSinceBlock lists all transactions since a given block
func (s *Server) handleListSinceBlock(req *Request) *Response {
	var params []interface{}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewInvalidParamsError("invalid parameters"),
				ID:      req.ID,
			}
		}
	}

	// Parse optional blockhash parameter
	var blockHash *types.Hash
	if len(params) > 0 {
		if hashStr, ok := params[0].(string); ok && hashStr != "" {
			hash, err := types.NewHashFromString(hashStr)
			if err != nil {
				return &Response{
					JSONRPC: "2.0",
					Error:   NewInvalidParamsError("invalid block hash"),
					ID:      req.ID,
				}
			}
			blockHash = &hash
		}
	}

	// Parse optional target-confirmations parameter
	targetConf := 1
	if len(params) > 1 {
		if tc, ok := params[1].(float64); ok {
			targetConf = int(tc)
		}
	}

	// Parse optional includeWatchonly parameter
	includeWatchOnly := false
	if len(params) > 2 {
		if iw, ok := params[2].(bool); ok {
			includeWatchOnly = iw
		}
	}

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	// List since block
	transactions, lastBlock, err := s.wallet.ListSinceBlock(blockHash, targetConf, includeWatchOnly)
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	// Convert amounts from satoshi to TWINS
	convertedTxs := make([]map[string]interface{}, len(transactions))
	for i, tx := range transactions {
		convertedTxs[i] = map[string]interface{}{
			"txid":          tx.TxID,
			"amount":        float64(tx.Amount) / 1e8,
			"fee":           float64(tx.Fee) / 1e8,
			"confirmations": tx.Confirmations,
			"blockhash":     tx.BlockHash,
			"blockheight":   tx.BlockHeight,
			"blocktime":     tx.BlockTime,
			"time":          tx.Time,
			"timereceived":  tx.TimeReceived,
			"comment":       tx.Comment,
			"label":         tx.Label,
			"address":       tx.Address,
			"category":      tx.Category,
		}
	}

	result := map[string]interface{}{
		"transactions": convertedTxs,
		"lastblock":    lastBlock.String(),
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  result,
		ID:      req.ID,
	}
}

// handleListAccounts returns balances by account
func (s *Server) handleListAccounts(req *Request) *Response {
	var params []interface{}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewInvalidParamsError("invalid parameters"),
				ID:      req.ID,
			}
		}
	}

	// Parse optional minconf parameter
	minConf := 1
	if len(params) > 0 {
		if mc, ok := params[0].(float64); ok {
			minConf = int(mc)
		}
	}

	// Parse optional includeWatchonly parameter
	includeWatchOnly := false
	if len(params) > 1 {
		if iw, ok := params[1].(bool); ok {
			includeWatchOnly = iw
		}
	}

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	// List accounts
	accounts, err := s.wallet.ListAccounts(minConf, includeWatchOnly)
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	// Convert satoshis to TWINS
	result := make(map[string]float64)
	for account, balance := range accounts {
		result[account] = float64(balance) / 1e8
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  result,
		ID:      req.ID,
	}
}

// handleListAddressGroupings lists groups of addresses with common ownership
func (s *Server) handleListAddressGroupings(req *Request) *Response {
	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	// List address groupings
	groupings, err := s.wallet.ListAddressGroupings()
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	// Convert satoshis to TWINS in the groupings
	result := make([][][]interface{}, len(groupings))
	for i, group := range groupings {
		result[i] = make([][]interface{}, len(group))
		for j, entry := range group {
			// entry is [address, amount, account (optional)]
			convertedEntry := make([]interface{}, len(entry))
			for k, val := range entry {
				if k == 1 {
					// Convert amount from satoshis to TWINS
					if amount, ok := val.(int64); ok {
						convertedEntry[k] = float64(amount) / 1e8
					} else {
						convertedEntry[k] = val
					}
				} else {
					convertedEntry[k] = val
				}
			}
			result[i][j] = convertedEntry
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  result,
		ID:      req.ID,
	}
}

// handleGetTransaction returns detailed wallet transaction information
func (s *Server) handleGetTransaction(req *Request) *Response {
	// Check wallet availability
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	// Parse parameters
	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("invalid parameters"),
			ID:      req.ID,
		}
	}

	if len(params) < 1 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("missing transaction ID"),
			ID:      req.ID,
		}
	}

	txid, ok := params[0].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("transaction ID must be a string"),
			ID:      req.ID,
		}
	}

	// Get wallet transaction
	wtx, err := s.wallet.GetTransaction(txid)
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-5, "Invalid or non-wallet transaction id", txid),
			ID:      req.ID,
		}
	}

	// Get raw transaction for hex field
	txHash, err := types.NewHashFromString(txid)
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("invalid transaction ID"),
			ID:      req.ID,
		}
	}

	rawTx, err := s.blockchain.GetTransaction(txHash)
	if err != nil {
		// Transaction might be in mempool
		rawTx, ok = s.mempool.GetTransaction(txHash)
		if !ok {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewError(-5, "Transaction not found in blockchain or mempool", txid),
				ID:      req.ID,
			}
		}
	}

	// Serialize transaction to hex
	txBytes, err := rawTx.Serialize()
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError("failed to serialize transaction: " + err.Error()),
			ID:      req.ID,
		}
	}
	txHex := hex.EncodeToString(txBytes)

	// Calculate dynamic confirmations from current chain height
	currentHeight, err := s.blockchain.GetBestHeight()
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError("failed to get chain height: " + err.Error()),
			ID:      req.ID,
		}
	}

	var confirmations int32
	if wtx.BlockHeight > 0 && uint32(wtx.BlockHeight) <= currentHeight {
		confirmations = int32(currentHeight) - wtx.BlockHeight + 1
	}

	// Build details array from transaction outputs
	// Use map[string]interface{} to output amount in TWINS (not satoshis)
	details := make([]map[string]interface{}, 0)
	for vout, output := range rawTx.Outputs {
		// Extract address from scriptPubKey
		addr := s.wallet.ExtractAddress(output.ScriptPubKey)
		if addr != "" && s.wallet.IsOurAddress(addr) {
			details = append(details, map[string]interface{}{
				"account":  "",           // Account field deprecated in modern Bitcoin
				"address":  addr,
				"category": wtx.Category,
				"amount":   float64(output.Value) / 1e8, // Convert to TWINS
				"vout":     uint32(vout),
			})
		}
	}

	// If no details found, use the wallet transaction's stored values
	if len(details) == 0 && wtx.Address != "" {
		details = append(details, map[string]interface{}{
			"account":  "",
			"address":  wtx.Address,
			"category": wtx.Category,
			"amount":   float64(wtx.Amount) / 1e8, // Convert to TWINS
			"vout":     0,
		})
	}

	// Get block information if transaction is confirmed
	var blockTime int64
	if wtx.BlockHeight > 0 {
		block, err := s.blockchain.GetBlockByHeight(uint32(wtx.BlockHeight))
		if err == nil {
			blockTime = int64(block.Header.Timestamp)
		}
	}

	// Build response
	result := map[string]interface{}{
		"txid":          wtx.TxID,
		"amount":        float64(wtx.Amount) / 1e8,
		"confirmations": confirmations,
		"time":          wtx.Time,
		"timereceived":  wtx.TimeReceived,
		"details":       details,
		"hex":           txHex,
	}

	// Add optional fields
	if wtx.Fee != 0 {
		result["fee"] = float64(wtx.Fee) / 1e8
	}
	if wtx.BlockHash != "" {
		result["blockhash"] = wtx.BlockHash
	}
	if wtx.BlockHeight > 0 {
		result["blockindex"] = wtx.BlockHeight
	}
	if blockTime > 0 {
		result["blocktime"] = blockTime
	}
	if wtx.Comment != "" {
		result["comment"] = wtx.Comment
	}
	if wtx.Label != "" {
		result["label"] = wtx.Label
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  result,
		ID:      req.ID,
	}
}

// handleGetWalletInfo returns wallet state information
func (s *Server) handleGetWalletInfo(req *Request) *Response {
	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	info, err := s.wallet.GetWalletInfo()
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  info,
		ID:      req.ID,
	}
}

// handleBackupWallet safely copies wallet.dat to destination
func (s *Server) handleBackupWallet(req *Request) *Response {
	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 1 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("destination path required"),
			ID:      req.ID,
		}
	}

	destination, ok := params[0].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("destination must be a string"),
			ID:      req.ID,
		}
	}

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	if err := s.wallet.BackupWallet(destination); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  nil,
		ID:      req.ID,
	}
}

// handleKeypoolRefill refills the keypool
func (s *Server) handleKeypoolRefill(req *Request) *Response {
	var params []interface{}
	newsize := 100 // Default keypool size

	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err == nil && len(params) > 0 {
			if size, ok := params[0].(float64); ok {
				newsize = int(size)
			}
		}
	}

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	if err := s.wallet.KeypoolRefill(newsize); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  nil,
		ID:      req.ID,
	}
}

// handleReserveBalance gets or sets the reserve balance
func (s *Server) handleReserveBalance(req *Request) *Response {
	var params []interface{}

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	// If no parameters, get current reserve balance
	if len(req.Params) == 0 || string(req.Params) == "[]" {
		enabled, amount, err := s.wallet.GetReserveBalance()
		if err != nil {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewInternalError(err.Error()),
				ID:      req.ID,
			}
		}

		result := map[string]interface{}{
			"reserve":  enabled,
			"amount":   float64(amount) / 1e8,
		}

		return &Response{
			JSONRPC: "2.0",
			Result:  result,
			ID:      req.ID,
		}
	}

	// Parse parameters for setting reserve balance
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("invalid parameters"),
			ID:      req.ID,
		}
	}

	if len(params) < 1 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("enabled flag required"),
			ID:      req.ID,
		}
	}

	enabled, ok := params[0].(bool)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("enabled must be a boolean"),
			ID:      req.ID,
		}
	}

	amount := int64(0)
	if len(params) > 1 {
		if amountFloat, ok := params[1].(float64); ok {
			amount = int64(amountFloat * 1e8) // Convert TWINS to satoshis
		}
	}

	if err := s.wallet.SetReserveBalance(enabled, amount); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  true,
		ID:      req.ID,
	}
}

// handleSetStakeSplitThreshold sets the stake split threshold
func (s *Server) handleSetStakeSplitThreshold(req *Request) *Response {
	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 1 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("threshold value required"),
			ID:      req.ID,
		}
	}

	thresholdFloat, ok := params[0].(float64)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("threshold must be a number"),
			ID:      req.ID,
		}
	}

	threshold := int64(thresholdFloat * 1e8) // Convert TWINS to satoshis

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	if err := s.wallet.SetStakeSplitThreshold(threshold); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	result := map[string]interface{}{
		"threshold": thresholdFloat,
		"enabled":   true,
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  result,
		ID:      req.ID,
	}
}

// handleGetStakeSplitThreshold gets the current stake split threshold
func (s *Server) handleGetStakeSplitThreshold(req *Request) *Response {
	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	threshold, err := s.wallet.GetStakeSplitThreshold()
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	// Convert satoshis to TWINS
	thresholdFloat := float64(threshold) / 1e8

	result := map[string]interface{}{
		"threshold": thresholdFloat,
		"enabled":   threshold > 0,
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  result,
		ID:      req.ID,
	}
}

// handleDumpHDInfo dumps HD wallet information
func (s *Server) handleDumpHDInfo(req *Request) *Response {
	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	// Get HD info from wallet
	seed, mnemonic, mnemonicPass, err := s.wallet.DumpHDInfo()
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	result := map[string]interface{}{
		"hdseed":             seed,
		"mnemonic":           mnemonic,
		"mnemonicpassphrase": mnemonicPass,
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  result,
		ID:      req.ID,
	}
}

// handleSetAutoCombine configures automatic UTXO consolidation
// Usage: setautocombine <true|false> [target_amount_in_TWINS]
func (s *Server) handleSetAutoCombine(req *Request) *Response {
	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 1 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("enable flag required"),
			ID:      req.ID,
		}
	}

	enabled, ok := params[0].(bool)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("enable must be a boolean"),
			ID:      req.ID,
		}
	}

	var targetTWINS int64
	if enabled {
		if len(params) < 2 {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewInvalidParamsError("target amount required when enabling"),
				ID:      req.ID,
			}
		}

		targetFloat, ok := params[1].(float64)
		if !ok {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewInvalidParamsError("target must be a number"),
				ID:      req.ID,
			}
		}

		targetTWINS = int64(targetFloat)
		if targetTWINS <= 0 {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewInvalidParamsError("target must be positive"),
				ID:      req.ID,
			}
		}
	}

	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	// Get current cooldown (preserve it on enable/disable)
	_, _, cooldown := s.wallet.GetAutoCombineConfig()
	if cooldown <= 0 {
		cooldown = 600 // default 10 minutes
	}

	// Apply immediately (wallet uses satoshis internally)
	targetSatoshis := targetTWINS * 100_000_000
	s.wallet.SetAutoCombineConfig(enabled, targetSatoshis, cooldown)

	// Persist to twinsd.yml via ConfigManager (stores TWINS, not satoshis)
	if s.configSetter != nil {
		_ = s.configSetter.Set("wallet.autoCombine", enabled)
		_ = s.configSetter.Set("wallet.autoCombineTarget", targetTWINS)
		_ = s.configSetter.Set("wallet.autoCombineCooldown", cooldown)
	}

	result := map[string]interface{}{
		"enabled":  enabled,
		"target":   targetTWINS,
		"cooldown": cooldown,
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  result,
		ID:      req.ID,
	}
}

// handleGetAutoCombine returns the current auto-combine configuration
func (s *Server) handleGetAutoCombine(req *Request) *Response {
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	enabled, targetSatoshis, cooldown := s.wallet.GetAutoCombineConfig()

	result := map[string]interface{}{
		"enabled":  enabled,
		"target":   targetSatoshis / 100_000_000, // Convert satoshis to TWINS
		"cooldown": cooldown,
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  result,
		ID:      req.ID,
	}
}

// handleCreateMultisig creates a multisignature address
func (s *Server) handleCreateMultisig(req *Request) *Response {
	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 2 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("nrequired and keys array required"),
			ID:      req.ID,
		}
	}

	nrequiredFloat, ok := params[0].(float64)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("nrequired must be a number"),
			ID:      req.ID,
		}
	}
	nrequired := int(nrequiredFloat)

	keysInterface, ok := params[1].([]interface{})
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("keys must be an array"),
			ID:      req.ID,
		}
	}

	keys := make([]string, len(keysInterface))
	for i, k := range keysInterface {
		key, ok := k.(string)
		if !ok {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewInvalidParamsError("each key must be a string"),
				ID:      req.ID,
			}
		}
		keys[i] = key
	}

	if nrequired < 1 || nrequired > len(keys) {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError(fmt.Sprintf("nrequired must be between 1 and %d", len(keys))),
			ID:      req.ID,
		}
	}

	if len(keys) < 2 || len(keys) > 15 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("keys array must contain between 2 and 15 keys"),
			ID:      req.ID,
		}
	}

	// Determine network ID from chain parameters
	var networkName string
	if s.chainParams != nil {
		networkName = s.chainParams.Name
	} else {
		networkName = "mainnet"
	}
	netID := crypto.GetScriptHashNetworkID(networkName)

	// Convert keys: accept both addresses and hex public keys
	// Legacy RPC accepts "Array of keys (addresses or hex public keys)"
	pubKeys := make([]string, len(keys))
	for i, key := range keys {
		// Check if it's a hex public key (66 chars for compressed, 130 chars for uncompressed)
		if len(key) == 66 || len(key) == 130 {
			// Validate it's valid hex
			if _, err := hex.DecodeString(key); err == nil {
				pubKeys[i] = key
				continue
			}
		}

		// Assume it's an address, need to look up public key from wallet
		if s.wallet == nil {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewError(-1, fmt.Sprintf("address '%s' requires wallet to lookup public key", key), nil),
				ID:      req.ID,
			}
		}

		// Get address info from wallet to find public key
		addrInfo, err := s.wallet.GetAddressInfo(key)
		if err != nil {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewError(-5, fmt.Sprintf("invalid address or public key: %s", key), nil),
				ID:      req.ID,
			}
		}

		if addrInfo.PubKey == "" {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewError(-5, fmt.Sprintf("no public key found for address: %s (is it in your wallet?)", key), nil),
				ID:      req.ID,
			}
		}

		// Use the public key hex string from wallet
		pubKeys[i] = addrInfo.PubKey
	}

	// Create multisig address using correct network ID
	multisigInfo, err := crypto.CreateMultisigAddress(nrequired, pubKeys, netID)
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	// Return address and redeem script
	result := map[string]interface{}{
		"address":      multisigInfo.Address,
		"redeemScript": multisigInfo.RedeemScript,
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  result,
		ID:      req.ID,
	}
}

// handleMultiSend configures automatic sending to multiple addresses
func (s *Server) handleMultiSend(req *Request) *Response {
	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("invalid parameters"),
			ID:      req.ID,
		}
	}

	// Single parameter commands
	if len(params) == 1 {
		cmd, ok := params[0].(string)
		if !ok {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewInvalidParamsError("command must be a string"),
				ID:      req.ID,
			}
		}

		switch cmd {
		case "print":
			return s.handleMultiSendPrint(req)
		case "printaddress", "printaddresses":
			return s.handleMultiSendPrintAddresses(req)
		case "clear":
			return s.handleMultiSendClear(req)
		case "enablestake", "activatestake":
			return s.handleMultiSendEnableStake(req)
		case "enablemasternode", "activatemasternode":
			return s.handleMultiSendEnableMasternode(req)
		case "disable", "deactivate":
			return s.handleMultiSendDisable(req)
		case "enableall":
			return s.handleMultiSendEnableAll(req)
		default:
			return &Response{
				JSONRPC: "2.0",
				Error:   NewInvalidParamsError("unknown multisend command: " + cmd),
				ID:      req.ID,
			}
		}
	}

	// Two parameter commands
	if len(params) == 2 {
		cmd, ok := params[0].(string)
		if !ok {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewInvalidParamsError("first parameter must be a string"),
				ID:      req.ID,
			}
		}

		if cmd == "delete" {
			return s.handleMultiSendDelete(req, params[1])
		} else if cmd == "disable" {
			return s.handleMultiSendDisableAddress(req, params[1])
		} else {
			// Assume it's <address> <percent>
			return s.handleMultiSendAdd(req, params[0], params[1])
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Error:   NewInvalidParamsError("invalid number of parameters"),
		ID:      req.ID,
	}
}

// handleMultiSendPrint displays current multisend configuration
func (s *Server) handleMultiSendPrint(req *Request) *Response {
	entries, err := s.wallet.GetMultiSend()
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "failed to get multisend entries: "+err.Error(), nil),
			ID:      req.ID,
		}
	}

	stakeEnabled, mnEnabled, disabledAddrs, err := s.wallet.GetMultiSendSettings()
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "failed to get multisend settings: "+err.Error(), nil),
			ID:      req.ID,
		}
	}

	result := map[string]interface{}{
		"entries":              entries,
		"stake_enabled":        stakeEnabled,
		"masternode_enabled":   mnEnabled,
		"disabled_addresses":   disabledAddrs,
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  result,
		ID:      req.ID,
	}
}

// handleMultiSendPrintAddresses displays all wallet addresses
func (s *Server) handleMultiSendPrintAddresses(req *Request) *Response {
	addresses, err := s.wallet.ListAddresses()
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "failed to list addresses: "+err.Error(), nil),
			ID:      req.ID,
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  addresses,
		ID:      req.ID,
	}
}

// handleMultiSendClear clears all multisend entries
func (s *Server) handleMultiSendClear(req *Request) *Response {
	if err := s.wallet.SetMultiSend(nil); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "failed to clear multisend: "+err.Error(), nil),
			ID:      req.ID,
		}
	}

	// Disable multisend
	if err := s.wallet.SetMultiSendSettings(false, false, nil); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "failed to disable multisend: "+err.Error(), nil),
			ID:      req.ID,
		}
	}

	result := map[string]interface{}{
		"cleared": true,
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  result,
		ID:      req.ID,
	}
}

// handleMultiSendEnableStake enables multisend for staking rewards
func (s *Server) handleMultiSendEnableStake(req *Request) *Response {
	entries, err := s.wallet.GetMultiSend()
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "failed to get multisend entries: "+err.Error(), nil),
			ID:      req.ID,
		}
	}

	entriesSlice, ok := entries.([]wallet.MultiSendEntry)
	if !ok || len(entriesSlice) == 0 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "multisend vector is empty, add entries first", nil),
			ID:      req.ID,
		}
	}

	_, mnEnabled, disabledAddrs, err := s.wallet.GetMultiSendSettings()
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "failed to get settings: "+err.Error(), nil),
			ID:      req.ID,
		}
	}

	if err := s.wallet.SetMultiSendSettings(true, mnEnabled, disabledAddrs); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "failed to enable stake multisend: "+err.Error(), nil),
			ID:      req.ID,
		}
	}

	return s.handleMultiSendPrint(req)
}

// handleMultiSendEnableMasternode enables multisend for masternode rewards
func (s *Server) handleMultiSendEnableMasternode(req *Request) *Response {
	entries, err := s.wallet.GetMultiSend()
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "failed to get multisend entries: "+err.Error(), nil),
			ID:      req.ID,
		}
	}

	entriesSlice, ok := entries.([]wallet.MultiSendEntry)
	if !ok || len(entriesSlice) == 0 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "multisend vector is empty, add entries first", nil),
			ID:      req.ID,
		}
	}

	stakeEnabled, _, disabledAddrs, err := s.wallet.GetMultiSendSettings()
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "failed to get settings: "+err.Error(), nil),
			ID:      req.ID,
		}
	}

	if err := s.wallet.SetMultiSendSettings(stakeEnabled, true, disabledAddrs); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "failed to enable masternode multisend: "+err.Error(), nil),
			ID:      req.ID,
		}
	}

	return s.handleMultiSendPrint(req)
}

// handleMultiSendDisable disables multisend
func (s *Server) handleMultiSendDisable(req *Request) *Response {
	_, _, disabledAddrs, err := s.wallet.GetMultiSendSettings()
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "failed to get settings: "+err.Error(), nil),
			ID:      req.ID,
		}
	}

	if err := s.wallet.SetMultiSendSettings(false, false, disabledAddrs); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "failed to disable multisend: "+err.Error(), nil),
			ID:      req.ID,
		}
	}

	return s.handleMultiSendPrint(req)
}

// handleMultiSendEnableAll enables all addresses
func (s *Server) handleMultiSendEnableAll(req *Request) *Response {
	stakeEnabled, mnEnabled, _, err := s.wallet.GetMultiSendSettings()
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "failed to get settings: "+err.Error(), nil),
			ID:      req.ID,
		}
	}

	// Clear disabled addresses list
	if err := s.wallet.SetMultiSendSettings(stakeEnabled, mnEnabled, nil); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "failed to enable all addresses: "+err.Error(), nil),
			ID:      req.ID,
		}
	}

	return s.handleMultiSendPrint(req)
}

// handleMultiSendDelete deletes an entry by index
func (s *Server) handleMultiSendDelete(req *Request, indexParam interface{}) *Response {
	indexFloat, ok := indexParam.(float64)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("index must be a number"),
			ID:      req.ID,
		}
	}
	index := int(indexFloat)

	entries, err := s.wallet.GetMultiSend()
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "failed to get multisend entries: "+err.Error(), nil),
			ID:      req.ID,
		}
	}

	entriesSlice, ok := entries.([]wallet.MultiSendEntry)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "invalid multisend entries", nil),
			ID:      req.ID,
		}
	}

	if index < 0 || index >= len(entriesSlice) {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("invalid index"),
			ID:      req.ID,
		}
	}

	// Remove entry
	newEntries := append(entriesSlice[:index], entriesSlice[index+1:]...)
	if err := s.wallet.SetMultiSend(newEntries); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "failed to delete entry: "+err.Error(), nil),
			ID:      req.ID,
		}
	}

	return s.handleMultiSendPrint(req)
}

// handleMultiSendDisableAddress disables a specific address
func (s *Server) handleMultiSendDisableAddress(req *Request, addrParam interface{}) *Response {
	address, ok := addrParam.(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("address must be a string"),
			ID:      req.ID,
		}
	}

	// Validate address
	validation, err := s.wallet.ValidateAddress(address)
	if err != nil || !validation.IsValid {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-5, "invalid TWINS address", nil),
			ID:      req.ID,
		}
	}

	stakeEnabled, mnEnabled, disabledAddrs, err := s.wallet.GetMultiSendSettings()
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "failed to get settings: "+err.Error(), nil),
			ID:      req.ID,
		}
	}

	// Add to disabled list if not already there
	found := false
	for _, addr := range disabledAddrs {
		if addr == address {
			found = true
			break
		}
	}

	if !found {
		disabledAddrs = append(disabledAddrs, address)
	}

	if err := s.wallet.SetMultiSendSettings(stakeEnabled, mnEnabled, disabledAddrs); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "failed to disable address: "+err.Error(), nil),
			ID:      req.ID,
		}
	}

	return s.handleMultiSendPrint(req)
}

// handleMultiSendAdd adds a new multisend entry
func (s *Server) handleMultiSendAdd(req *Request, addrParam interface{}, percentParam interface{}) *Response {
	address, ok := addrParam.(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("address must be a string"),
			ID:      req.ID,
		}
	}

	percentFloat, ok := percentParam.(float64)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("percent must be a number"),
			ID:      req.ID,
		}
	}
	percent := uint32(percentFloat)

	// Validate address
	validation, err := s.wallet.ValidateAddress(address)
	if err != nil || !validation.IsValid {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-5, "invalid TWINS address", nil),
			ID:      req.ID,
		}
	}

	// Validate percent
	if percent == 0 || percent > 100 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("percent must be between 1 and 100"),
			ID:      req.ID,
		}
	}

	// Get existing entries
	entries, err := s.wallet.GetMultiSend()
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "failed to get multisend entries: "+err.Error(), nil),
			ID:      req.ID,
		}
	}

	var entriesSlice []wallet.MultiSendEntry
	if entries != nil {
		var ok bool
		entriesSlice, ok = entries.([]wallet.MultiSendEntry)
		if !ok {
			entriesSlice = []wallet.MultiSendEntry{}
		}
	}

	// Check if address already exists
	for _, entry := range entriesSlice {
		if entry.Address == address {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewError(-1, "address already in multisend vector", nil),
				ID:      req.ID,
			}
		}
	}

	// Calculate total percent
	totalPercent := percent
	for _, entry := range entriesSlice {
		totalPercent += entry.Percent
	}

	if totalPercent > 100 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "total multisend percentage exceeds 100%", nil),
			ID:      req.ID,
		}
	}

	// Add new entry
	newEntry := wallet.MultiSendEntry{
		Address: address,
		Percent: percent,
	}
	entriesSlice = append(entriesSlice, newEntry)

	if err := s.wallet.SetMultiSend(entriesSlice); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "failed to add multisend entry: "+err.Error(), nil),
			ID:      req.ID,
		}
	}

	return s.handleMultiSendPrint(req)
}

// handleImportAddress adds a watch-only address
func (s *Server) handleImportAddress(req *Request) *Response {
	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 1 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("address required"),
			ID:      req.ID,
		}
	}

	address, ok := params[0].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("address must be a string"),
			ID:      req.ID,
		}
	}

	label := ""
	if len(params) >= 2 {
		if l, ok := params[1].(string); ok {
			label = l
		}
	}

	rescan := true
	if len(params) >= 3 {
		if r, ok := params[2].(bool); ok {
			rescan = r
		}
	}

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	if err := s.wallet.ImportAddress(address, label, rescan); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  nil,
		ID:      req.ID,
	}
}

// handleDumpWallet exports all wallet keys to a file
func (s *Server) handleDumpWallet(req *Request) *Response {
	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 1 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("filename required"),
			ID:      req.ID,
		}
	}

	filename, ok := params[0].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("filename must be a string"),
			ID:      req.ID,
		}
	}

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	if err := s.wallet.DumpWallet(filename); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  filename,
		ID:      req.ID,
	}
}

// handleImportWallet imports keys from a wallet dump file
func (s *Server) handleImportWallet(req *Request) *Response {
	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 1 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("filename required"),
			ID:      req.ID,
		}
	}

	filename, ok := params[0].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("filename must be a string"),
			ID:      req.ID,
		}
	}

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	if err := s.wallet.ImportWallet(filename); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  nil,
		ID:      req.ID,
	}
}

// handleAddMultisigAddress adds a multisignature address to the wallet
func (s *Server) handleAddMultisigAddress(req *Request) *Response {
	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 2 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("nrequired and keys array required"),
			ID:      req.ID,
		}
	}

	nrequiredFloat, ok := params[0].(float64)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("nrequired must be a number"),
			ID:      req.ID,
		}
	}
	nrequired := int(nrequiredFloat)

	keysInterface, ok := params[1].([]interface{})
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("keys must be an array"),
			ID:      req.ID,
		}
	}

	keys := make([]string, len(keysInterface))
	for i, k := range keysInterface {
		key, ok := k.(string)
		if !ok {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewInvalidParamsError("all keys must be strings"),
				ID:      req.ID,
			}
		}
		keys[i] = key
	}

	account := ""
	if len(params) > 2 {
		if acc, ok := params[2].(string); ok {
			account = acc
		}
	}

	// Get wallet instance
	if s.wallet == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(-1, "wallet not available", nil),
			ID:      req.ID,
		}
	}

	address, err := s.wallet.AddMultisigAddress(nrequired, keys, account)
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInternalError(err.Error()),
			ID:      req.ID,
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  address,
		ID:      req.ID,
	}
}

