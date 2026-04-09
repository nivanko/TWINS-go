package wallet

import (
	"encoding/binary"
	"fmt"

	"github.com/twins-dev/twins-core/pkg/crypto"
	"github.com/twins-dev/twins-core/pkg/types"
)

// StakeableUTXO represents a UTXO eligible for staking.
// Mirrors consensus.StakeableUTXO to avoid import cycle.
type StakeableUTXO struct {
	Outpoint      types.Outpoint
	Amount        int64
	Address       string
	BlockHeight   uint32
	BlockTime     uint32
	Confirmations uint32
	CoinAge       int64
	ScriptPubKey  []byte
}

// MinStakeConfirmations is the minimum confirmations for staking.
// Legacy: wallet.cpp:2413 - uses 10 for regular UTXOs (coinbase/coinstake use maturity)
const MinStakeConfirmations = 10

// MinStakeSplitThresholdSatoshis is the hard floor for stake splitting.
// A coinstake is only split into two outputs when totalReward >= this value.
// Set to 100000 TWINS so each half is at least 50000 TWINS, safely above the
// legacy StakingMinInput of 12000 TWINS (chainparams.cpp:243, main.cpp:3978).
// This prevents the wallet from producing split coinstakes whose vout[1] would
// be rejected by legacy C++ nodes with "CheckBlock() : stake under min. stake value".
const MinStakeSplitThresholdSatoshis int64 = 100000 * 100000000

// GetStakeableUTXOs returns all wallet UTXOs eligible for staking.
// Filtering criteria:
// - Minimum 10 confirmations (MinStakeConfirmations) - legacy wallet.cpp:2413
// - Minimum coin age >= StakeMinAge (3 hours) - legacy nStakeMinAge
// - Coinbase/coinstake outputs need CoinbaseMaturity confirmations
// - Not locked for other purposes
// - Respects ReserveBalance: keeps enough balance available for spending
func (w *Wallet) GetStakeableUTXOs(chainHeight uint32, chainTime uint32) ([]*StakeableUTXO, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.utxos == nil {
		return nil, fmt.Errorf("wallet not initialized")
	}

	// Snapshot pending-spent outpoints to exclude UTXOs already spent by pending transactions.
	// Prevents staking from selecting UTXOs that conflict with pending mempool transactions.
	// Snapshot taken BEFORE totalSpendable calculation so reserve balance check is accurate.
	w.pendingMu.RLock()
	pendingSpentSnapshot := make(map[types.Outpoint]struct{}, len(w.pendingSpent))
	for k := range w.pendingSpent {
		pendingSpentSnapshot[k] = struct{}{}
	}
	w.pendingMu.RUnlock()

	// Calculate total spendable balance for reserve balance check.
	// Excludes pending-spent UTXOs to avoid inflating maxStakeableAmount.
	var totalSpendable int64
	for outpoint, utxo := range w.utxos {
		if utxo.Spendable && utxo.Output != nil {
			if _, spent := pendingSpentSnapshot[outpoint]; !spent {
				totalSpendable += utxo.Output.Value
			}
		}
	}

	// Get reserve balance from config (amount to keep available for spending)
	reserveBalance := w.config.ReserveBalance

	// Calculate maximum amount that can be used for staking
	maxStakeableAmount := totalSpendable - reserveBalance
	if maxStakeableAmount < 0 {
		maxStakeableAmount = 0
	}

	var stakeableUTXOs []*StakeableUTXO
	var totalStakeableValue int64

	for outpoint, utxo := range w.utxos {
		// Skip non-spendable UTXOs
		if !utxo.Spendable {
			continue
		}

		// Skip UTXOs already spent by pending mempool transactions
		if _, spent := pendingSpentSnapshot[outpoint]; spent {
			continue
		}

		// Skip if not enough confirmations (using int32 from UTXO)
		utxoHeight := uint32(utxo.BlockHeight)
		if utxo.BlockHeight < 0 || utxoHeight > chainHeight {
			continue // UTXO is in a block ahead of us or unconfirmed
		}
		confirmations := chainHeight - utxoHeight
		if confirmations < MinStakeConfirmations {
			continue
		}

		// Check coinbase/coinstake maturity
		if utxo.IsCoinbase || utxo.IsStake {
			if confirmations < w.config.CoinbaseMaturity {
				continue
			}
		}

		// Use actual block time from UTXO for coin age calculation (legacy compliance)
		// BlockTime is populated from block header when UTXO is created/loaded
		blockTime := utxo.BlockTime
		if blockTime == 0 {
			// Fallback: estimate block time if not available (genesis time + height * 60s)
			blockTime = w.config.GenesisTimestamp + utxoHeight*60
		}

		// Calculate coin age
		coinAge := int64(0)
		if chainTime > blockTime {
			coinAge = int64(chainTime - blockTime)
		}

		// Skip if coin age is too low (3 hours = 10800 seconds)
		// Legacy: nStakeMinAge = 3 * 60 * 60 (kernel.cpp)
		const stakeMinAgeSeconds = 3 * 60 * 60 // 3 hours - legacy compliance
		if coinAge < stakeMinAgeSeconds {
			continue
		}

		// Skip locked UTXOs (used for other purposes like masternode collateral)
		if w.isUTXOLocked(outpoint) {
			continue
		}

		// Get address for this UTXO
		address := utxo.Address
		if address == "" {
			continue // Can't determine owner address
		}

		// Get the script from the output
		scriptPubKey := []byte{}
		if utxo.Output != nil {
			scriptPubKey = utxo.Output.ScriptPubKey
		}

		// Get the value from the output
		value := int64(0)
		if utxo.Output != nil {
			value = utxo.Output.Value
		}

		// Skip if value is below minimum stake amount
		// Legacy: wallet.cpp:2395-2399 - pwalletMain->nStakeMinInput check
		if w.config.MinStakeAmount > 0 && value < w.config.MinStakeAmount {
			continue
		}

		// Check if adding this UTXO would exceed maxStakeableAmount (reserve balance check)
		if reserveBalance > 0 && totalStakeableValue+value > maxStakeableAmount {
			continue // Skip this UTXO to preserve reserve balance
		}

		totalStakeableValue += value

		stakeableUTXOs = append(stakeableUTXOs, &StakeableUTXO{
			Outpoint:      outpoint,
			Amount:        value,
			Address:       address,
			BlockHeight:   utxoHeight,
			BlockTime:     blockTime, // Use actual block time (legacy compliance)
			Confirmations: confirmations,
			CoinAge:       coinAge,
			ScriptPubKey:  scriptPubKey,
		})
	}

	w.logger.WithFields(map[string]interface{}{
		"count":          len(stakeableUTXOs),
		"totalStakeable": totalStakeableValue,
		"reserveBalance": reserveBalance,
	}).Debug("Found stakeable UTXOs")
	return stakeableUTXOs, nil
}

// isUTXOLocked checks if a UTXO is locked for other purposes.
// MUST be called with w.mu held (RLock or Lock) - reads masternodeManager directly
// to avoid nested RLock deadlock risk (same pattern as GetLockedCollateralInfo).
func (w *Wallet) isUTXOLocked(outpoint types.Outpoint) bool {
	if w.masternodeManager == nil {
		return false
	}
	return w.masternodeManager.IsCollateralOutpoint(outpoint)
}

// getAddressForScript extracts the address from a scriptPubKey.
func (w *Wallet) getAddressForScript(script []byte) string {
	// P2PKH: OP_DUP OP_HASH160 <20 bytes> OP_EQUALVERIFY OP_CHECKSIG
	if len(script) == 25 && script[0] == 0x76 && script[1] == 0xa9 && script[2] == 0x14 && script[23] == 0x88 && script[24] == 0xac {
		pubKeyHash := script[3:23]
		netID := byte(crypto.MainNetPubKeyHashAddrID)
		if w.config.Network != MainNet {
			netID = byte(crypto.TestNetPubKeyHashAddrID)
		}
		addr, err := crypto.NewAddressFromHash(pubKeyHash, netID)
		if err != nil {
			return ""
		}
		return addr.String()
	}

	// P2PK: <33/65 bytes pubkey> OP_CHECKSIG
	if len(script) == 35 && script[0] == 0x21 && script[34] == 0xac {
		// Compressed pubkey
		pubKey := script[1:34]
		pubKeyHash := crypto.Hash160(pubKey)
		netID := byte(crypto.MainNetPubKeyHashAddrID)
		if w.config.Network != MainNet {
			netID = byte(crypto.TestNetPubKeyHashAddrID)
		}
		addr, err := crypto.NewAddressFromHash(pubKeyHash, netID)
		if err != nil {
			return ""
		}
		return addr.String()
	}
	if len(script) == 67 && script[0] == 0x41 && script[66] == 0xac {
		// Uncompressed pubkey
		pubKey := script[1:66]
		pubKeyHash := crypto.Hash160(pubKey)
		netID := byte(crypto.MainNetPubKeyHashAddrID)
		if w.config.Network != MainNet {
			netID = byte(crypto.TestNetPubKeyHashAddrID)
		}
		addr, err := crypto.NewAddressFromHash(pubKeyHash, netID)
		if err != nil {
			return ""
		}
		return addr.String()
	}

	return ""
}

// CreateCoinstakeTx creates a coinstake transaction for staking.
// Structure:
// - Input[0]: stake UTXO being spent (with signature)
// - Output[0]: empty marker (value=0, EMPTY script) - required for IsCoinStake()
// - Output[1]: stake return + reward (P2PK script with staker's pubkey)
// - Output[2]: (optional) second stake output if split threshold exceeded
//
// LEGACY COMPLIANCE: Coinstake outputs MUST use P2PK (pay-to-pubkey) format,
// NOT P2PKH (pay-to-pubkey-hash). Legacy C++ converts P2PKH to P2PK in
// stakeinput.cpp:207-217 CreateTxOuts(). This is required for proper validation.
//
// STAKE SPLITTING: If totalReward/2 > StakeSplitThreshold, the reward is split
// into two outputs for better UTXO distribution. Legacy: stakeinput.cpp:221-223
func (w *Wallet) CreateCoinstakeTx(
	stakeUTXO *StakeableUTXO,
	blockReward int64,
	blockTime uint32,
) (*types.Transaction, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.IsLocked() {
		return nil, fmt.Errorf("wallet is locked")
	}

	// Get the private key for signing
	privKey, err := w.getPrivateKeyForAddressLocked(stakeUTXO.Address)
	if err != nil {
		return nil, fmt.Errorf("failed to get private key: %w", err)
	}

	// Get the public key for creating P2PK output script
	// Legacy: stakeinput.cpp:207-217 - converts P2PKH to P2PK using wallet key lookup
	pubKey := privKey.PublicKey()
	pubKeyBytes := pubKey.SerializeCompressed()

	// Create P2PK script: <pubkey_len> <pubkey> OP_CHECKSIG
	// Legacy: scriptPubKey << key.GetPubKey() << OP_CHECKSIG
	outputScript := createP2PKScript(pubKeyBytes)

	// Create the coinstake transaction
	tx := &types.Transaction{
		Version:  1,
		LockTime: 0,
	}

	// Input: the stake UTXO
	tx.Inputs = []*types.TxInput{
		{
			PreviousOutput: stakeUTXO.Outpoint,
			Sequence:       0xffffffff,
			// ScriptSig will be filled after signing
		},
	}

	// Output 0: Empty marker (value=0, EMPTY script)
	// Legacy: wallet.cpp:3251-3252 - scriptEmpty.clear(); txNew.vout.push_back(CTxOut(0, scriptEmpty))
	// CRITICAL: Script must be EMPTY (nil/empty slice), NOT a copy of input script!
	// This is the standard coinstake marker that IsCoinStake() checks for.
	tx.Outputs = []*types.TxOutput{
		{
			Value:        0,
			ScriptPubKey: []byte{}, // EMPTY script per legacy C++ implementation
		},
	}

	// Calculate total reward
	totalReward := stakeUTXO.Amount + blockReward

	// Check if we should split the stake output
	// Legacy: stakeinput.cpp:221-223
	// if (nTotal / 2 > (CAmount)(pwallet->nStakeSplitThreshold * COIN))
	//     vout.emplace_back(CTxOut(0, scriptPubKey)); // Add second output
	// Hard floor: never split a coinstake whose total is below the minimum
	// split threshold, regardless of the user-configured stakeSplitThreshold.
	// This guarantees each post-split output is comfortably above legacy's
	// StakingMinInput (12000 TWINS) so blocks are accepted by legacy C++ nodes.
	stakeSplitThreshold, _ := w.GetStakeSplitThreshold()
	shouldSplit := stakeSplitThreshold > 0 &&
		totalReward >= MinStakeSplitThresholdSatoshis &&
		(totalReward/2) > stakeSplitThreshold

	if shouldSplit {
		// Split into two outputs for better UTXO distribution
		// Legacy: wallet.cpp:3324-3327
		// txNew.vout[1].nValue = ((nCredit - nMinFee) / 2 / CENT) * CENT;
		// txNew.vout[2].nValue = nCredit - nMinFee - txNew.vout[1].nValue;
		const CENT int64 = 1000000 // 0.01 TWINS in satoshis
		firstOutputValue := ((totalReward / 2) / CENT) * CENT
		secondOutputValue := totalReward - firstOutputValue

		tx.Outputs = append(tx.Outputs,
			&types.TxOutput{
				Value:        firstOutputValue,
				ScriptPubKey: outputScript, // P2PK script (legacy compliance)
			},
			&types.TxOutput{
				Value:        secondOutputValue,
				ScriptPubKey: outputScript, // P2PK script (legacy compliance)
			},
		)
	} else {
		// Single output with full reward
		// Legacy: wallet.cpp:3328 - txNew.vout[1].nValue = nCredit - nMinFee;
		tx.Outputs = append(tx.Outputs, &types.TxOutput{
			Value:        totalReward,
			ScriptPubKey: outputScript, // P2PK script (legacy compliance)
		})
	}

	// Note: Additional outputs for masternode payments, dev fund, etc.
	// will be added by FillBlockPayment BEFORE signing.
	// Do NOT sign here - SignCoinstakeTx must be called after all outputs are added.
	// Legacy: wallet.cpp:3337 FillBlockPayee(), then 3341 CreateTxIn() signs.

	return tx, nil
}

// SignCoinstakeTx signs an unsigned coinstake transaction.
// Must be called AFTER all outputs are added (masternode, dev fund, etc.)
// Legacy: wallet.cpp:3341 - stakeInput->CreateTxIn(this, in, hashTxOut)
func (w *Wallet) SignCoinstakeTx(tx *types.Transaction, stakeUTXO *StakeableUTXO) error {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.IsLocked() {
		return fmt.Errorf("wallet is locked")
	}

	// Get the private key for signing
	privKey, err := w.getPrivateKeyForAddressLocked(stakeUTXO.Address)
	if err != nil {
		return fmt.Errorf("failed to get private key: %w", err)
	}

	// Sign the transaction (modifies tx.Inputs[0].ScriptSig in place)
	_, err = w.signCoinstakeTransaction(tx, stakeUTXO, privKey)
	if err != nil {
		return fmt.Errorf("failed to sign coinstake: %w", err)
	}

	return nil
}

// createP2PKScript creates a P2PK (pay-to-pubkey) script.
// Format: <pubkey_len> <pubkey> OP_CHECKSIG
// Legacy: stakeinput.cpp:215 - scriptPubKey << key.GetPubKey() << OP_CHECKSIG
func createP2PKScript(pubKeyBytes []byte) []byte {
	// P2PK format: <len> <pubkey> OP_CHECKSIG(0xac)
	script := make([]byte, 0, len(pubKeyBytes)+2)
	script = append(script, byte(len(pubKeyBytes))) // Push pubkey length
	script = append(script, pubKeyBytes...)         // Push pubkey bytes
	script = append(script, 0xac)                   // OP_CHECKSIG
	return script
}

// signCoinstakeTransaction signs a coinstake transaction.
func (w *Wallet) signCoinstakeTransaction(
	tx *types.Transaction,
	stakeUTXO *StakeableUTXO,
	privKey *crypto.PrivateKey,
) (*types.Transaction, error) {
	// Clone the transaction for signing
	txCopy := cloneTransaction(tx)

	// For P2PKH, we need to sign with the scriptPubKey of the input
	txCopy.Inputs[0].ScriptSig = stakeUTXO.ScriptPubKey

	// Serialize and hash
	serialized := serializeTransactionForSigning(txCopy, 0)

	// Double SHA256
	hash := crypto.DoubleHash256(serialized)

	// Sign
	signature, err := privKey.Sign(hash)
	if err != nil {
		return nil, fmt.Errorf("signing failed: %w", err)
	}

	// Get signature bytes and add SIGHASH_ALL
	sigBytes := signature.Bytes()
	sigWithHashType := append(sigBytes, byte(0x01)) // SIGHASH_ALL

	// Build scriptSig based on script type
	// P2PK: <signature> only (pubkey is in scriptPubKey)
	// P2PKH: <signature> <pubkey>
	var scriptSig []byte
	if isP2PKScript(stakeUTXO.ScriptPubKey) {
		// P2PK: only signature needed
		scriptSig = make([]byte, 0, len(sigWithHashType)+1)
		scriptSig = append(scriptSig, byte(len(sigWithHashType)))
		scriptSig = append(scriptSig, sigWithHashType...)
	} else {
		// P2PKH: signature + pubkey
		pubKey := privKey.PublicKey()
		pubKeyBytes := pubKey.SerializeCompressed()
		scriptSig = make([]byte, 0, len(sigWithHashType)+len(pubKeyBytes)+2)
		scriptSig = append(scriptSig, byte(len(sigWithHashType)))
		scriptSig = append(scriptSig, sigWithHashType...)
		scriptSig = append(scriptSig, byte(len(pubKeyBytes)))
		scriptSig = append(scriptSig, pubKeyBytes...)
	}

	tx.Inputs[0].ScriptSig = scriptSig

	return tx, nil
}

// cloneTransaction creates a deep copy of a transaction for signing.
func cloneTransaction(tx *types.Transaction) *types.Transaction {
	clone := &types.Transaction{
		Version:  tx.Version,
		LockTime: tx.LockTime,
	}

	// Clone inputs
	clone.Inputs = make([]*types.TxInput, len(tx.Inputs))
	for i, input := range tx.Inputs {
		clone.Inputs[i] = &types.TxInput{
			PreviousOutput: input.PreviousOutput,
			Sequence:       input.Sequence,
		}
		if input.ScriptSig != nil {
			clone.Inputs[i].ScriptSig = make([]byte, len(input.ScriptSig))
			copy(clone.Inputs[i].ScriptSig, input.ScriptSig)
		}
	}

	// Clone outputs
	clone.Outputs = make([]*types.TxOutput, len(tx.Outputs))
	for i, output := range tx.Outputs {
		clone.Outputs[i] = &types.TxOutput{
			Value: output.Value,
		}
		if output.ScriptPubKey != nil {
			clone.Outputs[i].ScriptPubKey = make([]byte, len(output.ScriptPubKey))
			copy(clone.Outputs[i].ScriptPubKey, output.ScriptPubKey)
		}
	}

	return clone
}

// serializeTransactionForSigning serializes transaction for signature hash.
func serializeTransactionForSigning(tx *types.Transaction, inputIndex int) []byte {
	var buf []byte

	// Version (4 bytes, little endian)
	versionBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(versionBytes, tx.Version)
	buf = append(buf, versionBytes...)

	// Number of inputs (varint)
	buf = append(buf, encodeVarInt(uint64(len(tx.Inputs)))...)

	// Inputs
	for i, input := range tx.Inputs {
		// Previous output hash (32 bytes)
		buf = append(buf, input.PreviousOutput.Hash[:]...)

		// Previous output index (4 bytes, little endian)
		indexBytes := make([]byte, 4)
		binary.LittleEndian.PutUint32(indexBytes, input.PreviousOutput.Index)
		buf = append(buf, indexBytes...)

		// Script (only for the input being signed)
		if i == inputIndex {
			buf = append(buf, encodeVarInt(uint64(len(input.ScriptSig)))...)
			buf = append(buf, input.ScriptSig...)
		} else {
			buf = append(buf, 0x00) // Empty script
		}

		// Sequence (4 bytes, little endian)
		seqBytes := make([]byte, 4)
		binary.LittleEndian.PutUint32(seqBytes, input.Sequence)
		buf = append(buf, seqBytes...)
	}

	// Number of outputs (varint)
	buf = append(buf, encodeVarInt(uint64(len(tx.Outputs)))...)

	// Outputs
	for _, output := range tx.Outputs {
		// Value (8 bytes, little endian)
		valueBytes := make([]byte, 8)
		binary.LittleEndian.PutUint64(valueBytes, uint64(output.Value))
		buf = append(buf, valueBytes...)

		// Script
		buf = append(buf, encodeVarInt(uint64(len(output.ScriptPubKey)))...)
		buf = append(buf, output.ScriptPubKey...)
	}

	// Lock time (4 bytes, little endian)
	lockTimeBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(lockTimeBytes, tx.LockTime)
	buf = append(buf, lockTimeBytes...)

	// SIGHASH_ALL (4 bytes, little endian)
	hashTypeBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(hashTypeBytes, 0x01) // SIGHASH_ALL
	buf = append(buf, hashTypeBytes...)

	return buf
}

// isP2PKScript checks if script is Pay-to-PubKey (P2PK).
// P2PK compressed: <33 bytes pubkey> OP_CHECKSIG (35 bytes total)
// P2PK uncompressed: <65 bytes pubkey> OP_CHECKSIG (67 bytes total)
func isP2PKScript(script []byte) bool {
	if len(script) == 35 && script[0] == 0x21 && script[34] == 0xac {
		return true // Compressed P2PK
	}
	if len(script) == 67 && script[0] == 0x41 && script[66] == 0xac {
		return true // Uncompressed P2PK
	}
	return false
}

// encodeVarInt encodes an integer as a Bitcoin varint.
func encodeVarInt(v uint64) []byte {
	if v < 0xfd {
		return []byte{byte(v)}
	}
	if v <= 0xffff {
		buf := make([]byte, 3)
		buf[0] = 0xfd
		binary.LittleEndian.PutUint16(buf[1:], uint16(v))
		return buf
	}
	if v <= 0xffffffff {
		buf := make([]byte, 5)
		buf[0] = 0xfe
		binary.LittleEndian.PutUint32(buf[1:], uint32(v))
		return buf
	}
	buf := make([]byte, 9)
	buf[0] = 0xff
	binary.LittleEndian.PutUint64(buf[1:], v)
	return buf
}

// getPrivateKeyForAddressLocked returns the private key for a given address.
// Resolution chain: in-memory cache → BDB database → HD derivation.
// This mirrors dumpPrivKeyLocked() but returns *crypto.PrivateKey instead of WIF,
// and does NOT check isLockedForSendingLocked() — staking needs key access
// even in staking-only mode.
// MUST be called with w.mu held (RLock or Lock).
func (w *Wallet) getPrivateKeyForAddressLocked(address string) (*crypto.PrivateKey, error) {
	addr, exists := w.addresses[address]
	if !exists {
		return nil, fmt.Errorf("address not found in wallet: %s", address)
	}

	// 1. In-memory cache (keys loaded on wallet unlock or imported keys)
	if addr.PrivKey != nil {
		return addr.PrivKey, nil
	}

	// Watch-only addresses have no public key — cannot derive private key
	if addr.PubKey == nil {
		return nil, fmt.Errorf("no public key for address (watch-only): %s", address)
	}

	// 2. BDB database (encrypted wallets store keys in DB)
	if w.wdb != nil {
		compressedPubKey := addr.PubKey.CompressedBytes()
		uncompressedPubKey := addr.PubKey.Bytes()

		privKeyBytes, err := w.wdb.ReadKey(compressedPubKey)
		if err != nil {
			privKeyBytes, err = w.wdb.ReadKey(uncompressedPubKey)
		}

		if err == nil {
			privKey, parseErr := crypto.ParsePrivateKeyFromBytes(privKeyBytes)
			for i := range privKeyBytes {
				privKeyBytes[i] = 0
			}
			if parseErr != nil {
				return nil, fmt.Errorf("failed to parse private key: %w", parseErr)
			}
			return privKey, nil
		}

		// 3. HD derivation (HD wallets derive keys from seed, not stored)
		privKey, err := w.deriveHDPrivKeyLocked(compressedPubKey)
		if err == nil {
			return privKey, nil
		}
	}

	return nil, fmt.Errorf("private key not available for address: %s", address)
}

// GetPrivateKeyForAddress returns the private key for a given address.
// This is the public interface for consensus.StakingWalletInterface.
func (w *Wallet) GetPrivateKeyForAddress(address string) (*crypto.PrivateKey, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.getPrivateKeyForAddressLocked(address)
}

// SignMessageBytes signs a pre-computed hash with the key for the given address.
// Used for block signature creation where input is already a 32-byte hash.
// Legacy: blocksignature.cpp:12 - key.Sign(block.GetHash(), block.vchBlockSig)
// NOTE: This does NOT hash the input - it signs the raw 32-byte hash directly.
func (w *Wallet) SignMessageBytes(address string, hash []byte) ([]byte, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	privKey, err := w.getPrivateKeyForAddressLocked(address)
	if err != nil {
		return nil, err
	}

	// Sign the hash directly (no double-hashing)
	// Legacy: CKey::Sign takes raw 32-byte hash, secp256k1_ecdsa_sign internally
	sig, err := privKey.Sign(hash)
	if err != nil {
		return nil, err
	}

	return sig.Bytes(), nil
}
