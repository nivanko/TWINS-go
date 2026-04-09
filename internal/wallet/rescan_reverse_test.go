package wallet

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twins-dev/twins-core/pkg/crypto"
	"github.com/twins-dev/twins-core/pkg/types"
)

// TestRescanReverseReconcilePhantomUnspent verifies that rescan marks a
// wallet-owned UTXO as spent when:
//   - the UTXO exists in storage with SpendingHeight == 0 (phantom unspent)
//   - an on-main-chain transaction in the wallet's address history consumes it
//     as an input
//
// This is the reverse counterpart of TestRescanSelfHealStaleAddressIndex:
// the forward self-heal unsticks UTXOs spent by nonexistent transactions,
// and the reverse reconciliation marks UTXOs that the chain has spent but
// storage never recorded as spent.
func TestRescanReverseReconcilePhantomUnspent(t *testing.T) {
	w := createTestWallet(t)

	address, err := w.GetNewAddress("reverse")
	require.NoError(t, err)

	addr, err := crypto.DecodeAddress(address)
	require.NoError(t, err)

	addressBinary := make([]byte, 21)
	addressBinary[0] = addr.NetID()
	copy(addressBinary[1:], addr.Hash160())

	// Build a P2PKH script that decodes to the wallet's address so that
	// isOurScriptLocked recognizes the prev UTXO as wallet-owned.
	script := make([]byte, 25)
	script[0] = 0x76 // OP_DUP
	script[1] = 0xa9 // OP_HASH160
	script[2] = 0x14 // PUSH20
	copy(script[3:23], addr.Hash160())
	script[23] = 0x88 // OP_EQUALVERIFY
	script[24] = 0xac // OP_CHECKSIG

	// Seed a wallet-owned UTXO in storage that is currently unspent.
	prevOutpoint := types.Outpoint{Hash: types.Hash{0xba, 0x5e, 0x11}, Index: 0}
	prevOutput := &types.TxOutput{Value: 10_000_000_000, ScriptPubKey: script}
	require.NoError(t, w.storage.StoreUTXO(prevOutpoint, prevOutput, 100, false))

	// Build an on-chain transaction that spends the wallet-owned UTXO.
	spenderTx := &types.Transaction{
		Version: 1,
		Inputs: []*types.TxInput{
			{
				PreviousOutput: prevOutpoint,
				ScriptSig:      []byte{0x01},
				Sequence:       0xffffffff,
			},
		},
		Outputs: []*types.TxOutput{
			{Value: 9_999_999_000, ScriptPubKey: []byte{0x6a}}, // OP_RETURN sink
		},
		LockTime: 0,
	}
	spenderHash := spenderTx.Hash()

	// Store the spending transaction and its block header/index so rescan
	// can find it via the address index and validate it as on-main-chain.
	spenderBlock := &types.Block{
		Header: &types.BlockHeader{
			Version:       1,
			PrevBlockHash: types.Hash{0xaa},
			MerkleRoot:    spenderHash,
			Timestamp:     1_600_000_000,
			Bits:          0x1d00ffff,
			Nonce:         42,
		},
		Transactions: []*types.Transaction{spenderTx},
	}
	spenderBlockHash := spenderBlock.Hash()
	spenderHeight := uint32(200)

	batch := w.storage.NewBatch()
	require.NoError(t, batch.StoreBlockIndex(spenderBlockHash, spenderHeight))
	require.NoError(t, batch.StoreBlockWithHeight(spenderBlock, spenderHeight))
	// Store the tx index explicitly (so GetTransactionData returns it).
	require.NoError(t, batch.StoreTransaction(spenderTx))
	// Index the tx under the wallet address as an input (isInput=true)
	// so that GetTransactionsByAddress returns it during rescan.
	require.NoError(t, batch.IndexTransactionByAddress(
		addressBinary, spenderHash, spenderHeight, 0, prevOutput.Value, true, spenderBlockHash,
	))
	require.NoError(t, batch.Commit())

	// Sanity-check the phantom-unspent state before rescan.
	before, err := w.storage.GetUTXO(prevOutpoint)
	require.NoError(t, err)
	require.NotNil(t, before)
	assert.Equal(t, uint32(0), before.SpendingHeight, "UTXO should be unspent before rescan")

	// Run the rescan. Reverse reconciliation should detect the phantom
	// unspent state and mark the UTXO as spent by the on-chain tx.
	require.NoError(t, w.RescanAllAddresses())

	after, err := w.storage.GetUTXO(prevOutpoint)
	require.NoError(t, err)
	require.NotNil(t, after)
	assert.Equal(t, spenderHeight, after.SpendingHeight, "UTXO SpendingHeight should match spender block height")
	assert.Equal(t, spenderHash, after.SpendingTxHash, "UTXO SpendingTxHash should match spender tx hash")

	// The UTXO must NOT appear in the in-memory wallet UTXO map either.
	_, stillInWallet := w.utxos[prevOutpoint]
	assert.False(t, stillInWallet, "reconciled UTXO should not remain in w.utxos")
}

// TestRescanReverseReconcileCrossAddress verifies that reverse reconciliation
// works across wallet addresses: the spender transaction is stored in the
// address history of one wallet address (the change-output recipient), while
// the consumed input UTXO belongs to a different wallet address. The fix
// must not require the consumed UTXO's address to match the current rescan
// address — it only requires that isOurScriptLocked recognizes the script
// as wallet-owned. This reproduces the scenario observed on the affected
// node where 71746 txs were walked with zero reconciliations because the
// guard previously required same-address match.
func TestRescanReverseReconcileCrossAddress(t *testing.T) {
	w := createTestWallet(t)

	// Address A: owns the phantom-unspent UTXO (the consumed input).
	addrA, err := w.GetNewAddress("source")
	require.NoError(t, err)
	decodedA, err := crypto.DecodeAddress(addrA)
	require.NoError(t, err)

	// Address B: receives the change output (spender tx lives in B's history).
	addrB, err := w.GetNewAddress("change")
	require.NoError(t, err)
	decodedB, err := crypto.DecodeAddress(addrB)
	require.NoError(t, err)

	addrBinaryB := make([]byte, 21)
	addrBinaryB[0] = decodedB.NetID()
	copy(addrBinaryB[1:], decodedB.Hash160())

	buildP2PKH := func(hash160 []byte) []byte {
		script := make([]byte, 25)
		script[0] = 0x76 // OP_DUP
		script[1] = 0xa9 // OP_HASH160
		script[2] = 0x14 // PUSH20
		copy(script[3:23], hash160)
		script[23] = 0x88 // OP_EQUALVERIFY
		script[24] = 0xac // OP_CHECKSIG
		return script
	}
	scriptA := buildP2PKH(decodedA.Hash160())
	scriptB := buildP2PKH(decodedB.Hash160())

	// Seed phantom-unspent UTXO on address A.
	prevOutpoint := types.Outpoint{Hash: types.Hash{0xca, 0xfe, 0x01}, Index: 0}
	prevOutput := &types.TxOutput{Value: 12_500_000_000, ScriptPubKey: scriptA}
	require.NoError(t, w.storage.StoreUTXO(prevOutpoint, prevOutput, 100, false))

	// Build a spender tx that consumes the A-owned UTXO and emits a change
	// output to address B. The spender tx is indexed ONLY under B (as
	// output), matching the cross-address phantom scenario.
	spenderTx := &types.Transaction{
		Version: 1,
		Inputs: []*types.TxInput{
			{
				PreviousOutput: prevOutpoint,
				ScriptSig:      []byte{0x01},
				Sequence:       0xffffffff,
			},
		},
		Outputs: []*types.TxOutput{
			{Value: 12_499_999_000, ScriptPubKey: scriptB},
		},
		LockTime: 0,
	}
	spenderHash := spenderTx.Hash()

	spenderBlock := &types.Block{
		Header: &types.BlockHeader{
			Version:       1,
			PrevBlockHash: types.Hash{0xaa},
			MerkleRoot:    spenderHash,
			Timestamp:     1_600_000_000,
			Bits:          0x1d00ffff,
			Nonce:         42,
		},
		Transactions: []*types.Transaction{spenderTx},
	}
	spenderBlockHash := spenderBlock.Hash()
	spenderHeight := uint32(200)

	batch := w.storage.NewBatch()
	require.NoError(t, batch.StoreBlockIndex(spenderBlockHash, spenderHeight))
	require.NoError(t, batch.StoreBlockWithHeight(spenderBlock, spenderHeight))
	require.NoError(t, batch.StoreTransaction(spenderTx))
	// Index only under address B as output — simulating the cross-address
	// case where address A has no address history entry for this tx.
	require.NoError(t, batch.IndexTransactionByAddress(
		addrBinaryB, spenderHash, spenderHeight, 0, spenderTx.Outputs[0].Value, false, spenderBlockHash,
	))
	require.NoError(t, batch.Commit())

	// Sanity: UTXO is unspent before rescan.
	before, err := w.storage.GetUTXO(prevOutpoint)
	require.NoError(t, err)
	require.NotNil(t, before)
	assert.Equal(t, uint32(0), before.SpendingHeight)

	// Run rescan. When rescanning address B we walk the spender tx, iterate
	// inputs, discover the A-owned phantom UTXO, and mark it spent — even
	// though the current rescan address is B, not A.
	require.NoError(t, w.RescanAllAddresses())

	after, err := w.storage.GetUTXO(prevOutpoint)
	require.NoError(t, err)
	require.NotNil(t, after)
	assert.Equal(t, spenderHeight, after.SpendingHeight,
		"cross-address reverse reconcile must mark the A-owned UTXO as spent")
	assert.Equal(t, spenderHash, after.SpendingTxHash)
}

// TestRescanFullScanPhantomRecovery verifies that RescanAllAddresses picks
// up phantom-unspent UTXOs via the final FindAndMarkSpendersForOutpoints
// sweep when the spender transaction exists in storage but has NO
// corresponding entry in any wallet address's history. This simulates the
// real-world corruption class where both mark-spent and address-index
// input-side writes were skipped by the same interrupted batch commit.
func TestRescanFullScanPhantomRecovery(t *testing.T) {
	w := createTestWallet(t)

	// Wallet address A — owns the phantom-unspent UTXO.
	addrA, err := w.GetNewAddress("source")
	require.NoError(t, err)
	decodedA, err := crypto.DecodeAddress(addrA)
	require.NoError(t, err)

	// P2PKH script belonging to the wallet.
	scriptA := make([]byte, 25)
	scriptA[0] = 0x76
	scriptA[1] = 0xa9
	scriptA[2] = 0x14
	copy(scriptA[3:23], decodedA.Hash160())
	scriptA[23] = 0x88
	scriptA[24] = 0xac

	// Build a legitimate parent transaction whose output 0 will become
	// the phantom-unspent UTXO. The parent tx must exist in storage and
	// its block must be on main chain so the phantom-CREATED recovery
	// sweep (which runs first) does not delete the UTXO before the
	// phantom-UNSPENT full scan can reach it.
	parentOutput := &types.TxOutput{Value: 9_000_000_000, ScriptPubKey: scriptA}
	parentTx := &types.Transaction{
		Version: 1,
		Inputs: []*types.TxInput{
			{
				PreviousOutput: types.Outpoint{Hash: types.Hash{0xa1, 0xb2}, Index: 0},
				ScriptSig:      []byte{0x10},
				Sequence:       0xffffffff,
			},
		},
		Outputs:  []*types.TxOutput{parentOutput},
		LockTime: 0,
	}
	parentTxHash := parentTx.Hash()
	parentBlock := &types.Block{
		Header: &types.BlockHeader{
			Version:       1,
			PrevBlockHash: types.Hash{0x10},
			MerkleRoot:    parentTxHash,
			Timestamp:     1_599_999_000,
			Bits:          0x1d00ffff,
			Nonce:         1,
		},
		Transactions: []*types.Transaction{parentTx},
	}
	parentBlockHash := parentBlock.Hash()
	parentHeight := uint32(80)
	{
		parentBatch := w.storage.NewBatch()
		require.NoError(t, parentBatch.StoreBlockIndex(parentBlockHash, parentHeight))
		require.NoError(t, parentBatch.StoreBlockWithHeight(parentBlock, parentHeight))
		require.NoError(t, parentBatch.StoreTransaction(parentTx))
		require.NoError(t, parentBatch.StoreUTXO(
			types.Outpoint{Hash: parentTxHash, Index: 0}, parentOutput, parentHeight, false,
		))
		require.NoError(t, parentBatch.Commit())
	}
	prevOutpoint := types.Outpoint{Hash: parentTxHash, Index: 0}

	// Build a spender tx that consumes the phantom UTXO and emits an
	// external output (not wallet-owned).
	externalScript := []byte{
		0x76, 0xa9, 0x14,
		0xde, 0xad, 0xbe, 0xef, 0xde, 0xad, 0xbe, 0xef,
		0xde, 0xad, 0xbe, 0xef, 0xde, 0xad, 0xbe, 0xef,
		0xde, 0xad, 0xbe, 0xef,
		0x88, 0xac,
	}
	spenderTx := &types.Transaction{
		Version: 1,
		Inputs: []*types.TxInput{
			{
				PreviousOutput: prevOutpoint,
				ScriptSig:      []byte{0x01},
				Sequence:       0xffffffff,
			},
		},
		Outputs: []*types.TxOutput{
			{Value: 8_999_999_000, ScriptPubKey: externalScript},
		},
	}
	spenderHash := spenderTx.Hash()
	spenderBlock := &types.Block{
		Header: &types.BlockHeader{
			Version:       1,
			PrevBlockHash: types.Hash{0xab},
			MerkleRoot:    spenderHash,
			Timestamp:     1_600_001_000,
			Bits:          0x1d00ffff,
			Nonce:         7,
		},
		Transactions: []*types.Transaction{spenderTx},
	}
	spenderBlockHash := spenderBlock.Hash()
	spenderHeight := uint32(130)

	// Store the spender tx and register its block, but DELIBERATELY skip
	// IndexTransactionByAddress for both input and output — this is the
	// corruption scenario where neither wallet address history nor the
	// external address history contains the tx, so reverse reconciliation
	// via address history cannot discover it. Only the full-scan recovery
	// can find it.
	batch := w.storage.NewBatch()
	require.NoError(t, batch.StoreBlockIndex(spenderBlockHash, spenderHeight))
	require.NoError(t, batch.StoreBlockWithHeight(spenderBlock, spenderHeight))
	require.NoError(t, batch.StoreTransaction(spenderTx))
	require.NoError(t, batch.Commit())

	// Before rescan: UTXO is unspent.
	before, err := w.storage.GetUTXO(prevOutpoint)
	require.NoError(t, err)
	assert.Equal(t, uint32(0), before.SpendingHeight)

	// Run rescan. The full-scan sweep must find the spender tx in the
	// PrefixTransaction index and mark the phantom UTXO as spent.
	require.NoError(t, w.RescanAllAddresses())

	after, err := w.storage.GetUTXO(prevOutpoint)
	require.NoError(t, err)
	require.NotNil(t, after)
	assert.Equal(t, spenderHeight, after.SpendingHeight,
		"full-scan recovery must mark phantom UTXO as spent")
	assert.Equal(t, spenderHash, after.SpendingTxHash)

	// The reconciled UTXO must be dropped from the in-memory wallet.
	_, stillInWallet := w.utxos[prevOutpoint]
	assert.False(t, stillInWallet, "reconciled UTXO should not remain in w.utxos")
}

