package wallet

import (
	"fmt"
	"time"

	"github.com/twins-dev/twins-core/pkg/crypto"
	"github.com/twins-dev/twins-core/pkg/types"
)

// NetworkType represents the blockchain network type
type NetworkType int

const (
	MainNet NetworkType = iota
	TestNet
	RegTest
)

func (n NetworkType) String() string {
	switch n {
	case MainNet:
		return "mainnet"
	case TestNet:
		return "testnet"
	case RegTest:
		return "regtest"
	default:
		return "unknown"
	}
}

// ScriptType represents the type of script
type ScriptType int

const (
	ScriptTypeP2PKH      ScriptType = iota // Pay to Public Key Hash
	ScriptTypeP2SH                         // Pay to Script Hash
	ScriptTypeMultiSig                     // Multi-signature
	ScriptTypeNullData                     // OP_RETURN
	ScriptTypeCoinStake                    // Coinstake
)

func (s ScriptType) String() string {
	switch s {
	case ScriptTypeP2PKH:
		return "p2pkh"
	case ScriptTypeP2SH:
		return "p2sh"
	case ScriptTypeMultiSig:
		return "multisig"
	case ScriptTypeNullData:
		return "nulldata"
	case ScriptTypeCoinStake:
		return "coinstake"
	default:
		return "unknown"
	}
}

// TxCategory represents transaction category
type TxCategory string

const (
	TxCategorySend       TxCategory = "send"
	TxCategoryReceive    TxCategory = "receive"
	TxCategoryCoinStake  TxCategory = "stake"
	TxCategoryCoinBase   TxCategory = "coinbase"
	TxCategoryMasternode TxCategory = "masternode"
	TxCategoryGenerate   TxCategory = "generate"
	TxCategoryToSelf     TxCategory = "send_to_self"
)

// Account represents a wallet account
type Account struct {
	ID            uint32
	Name          string
	ExtendedKey   *HDKey
	ExternalChain *AddressChain
	InternalChain *AddressChain // Change addresses
	Balance       *Balance
}

// Address represents a wallet address
type Address struct {
	Address    string
	PubKey     *crypto.PublicKey
	PrivKey    *crypto.PrivateKey
	ScriptType ScriptType
	Account    uint32
	Index      uint32
	Internal   bool      // Change address
	Used       bool
	Label      string
	WatchOnly  bool      // Watch-only address (no private key)
	CreatedAt  time.Time // Timestamp when address was generated
}

// MultisigAddress represents a multisig P2SH address
type MultisigAddress struct {
	Address      string    // P2SH address
	RedeemScript string    // Hex-encoded redeem script
	NRequired    int       // Number of required signatures
	Keys         []string  // Public key hashes or addresses
	Account      string    // Associated account name
	CreatedAt    time.Time // Creation timestamp
}

// MultiSendEntry represents a multisend recipient configuration
type MultiSendEntry struct {
	Address string // Recipient address
	Percent uint32 // Percentage to send (1-100)
}

// Balance represents account balance
type Balance struct {
	Confirmed   int64
	Unconfirmed int64
	Immature    int64 // Coinbase/coinstake not yet mature
}

// txKey is the composite map key for w.transactions: Hash + Vout.
// This allows a single transaction to have multiple wallet entries (e.g. a
// combined staker+MN coinstake produces one entry for the MN reward at Vout=0
// and a second entry for the staking reward at Vout=1).
type txKey struct {
	Hash types.Hash
	Vout int32
}

// WalletTransaction represents a wallet transaction
// Matches Qt's TransactionRecord in transactionrecord.h
type WalletTransaction struct {
	Tx            *types.Transaction
	Hash          types.Hash
	BlockHash     types.Hash
	BlockHeight   int32
	Confirmations int32
	Time          time.Time
	SeqNum        int64 // Sequence number for chronological ordering (oldest first = 1)
	Category      TxCategory
	Amount        int64
	Fee           int64
	Account       string
	Address       string
	Label         string
	Comment       string
	WatchOnly     bool // Transaction involves watch-only address

	// Vout is the output index for receive transactions
	// Matches Qt's TransactionRecord::idx
	Vout int32

	// FromAddress is the sender's address (if known from inputs)
	// Usually empty for privacy reasons unless explicitly set
	FromAddress string

	// IsConflicted indicates if this transaction conflicts with another
	IsConflicted bool
}

// UTXO represents an unspent transaction output
type UTXO struct {
	Outpoint      types.Outpoint
	Output        *types.TxOutput
	BlockHeight   int32
	BlockTime     uint32 // Block timestamp for coin age calculation (legacy compliance)
	Confirmations int32
	IsCoinbase    bool
	IsStake       bool
	IsChange      bool // True if this is a change output from our own transaction
	Spendable     bool
	Address       string
	Account       uint32
}

// AddressInfo provides detailed address information
type AddressInfo struct {
	Address      string     `json:"address"`
	Account      string     `json:"account"`
	Label        string     `json:"label"`
	ScriptType   ScriptType `json:"scripttype"`
	IsCompressed bool       `json:"iscompressed"`
	IsWatchOnly  bool       `json:"iswatchonly"`
	IsMine       bool       `json:"ismine"`
	IsValid      bool       `json:"isvalid"`
	PubKey       string     `json:"pubkey,omitempty"`
	HDKeyPath    string     `json:"hdkeypath,omitempty"`
}

// KeyPath represents a BIP44 derivation path
type KeyPath struct {
	Purpose  uint32 // BIP44: 44'
	CoinType uint32 // TWINS coin type
	Account  uint32 // Account index
	Change   uint32 // 0 = external, 1 = internal
	Index    uint32 // Address index
}

// String returns the string representation of key path
func (kp *KeyPath) String() string {
	return fmt.Sprintf("m/44'/%d'/%d'/%d/%d", kp.CoinType, kp.Account, kp.Change, kp.Index)
}

// AddressChain manages a chain of derived addresses
type AddressChain struct {
	account   *Account
	internal  bool   // true for change addresses
	nextIndex uint32
	addresses []*Address
	gap       int // Current gap in used addresses
	maxGap    int // Maximum gap before stopping generation
}

// StakeInput represents a stakeable input
type StakeInput struct {
	UTXO          *UTXO
	Age           int64
	Weight        int64
	Eligible      bool
	NextStakeTime time.Time
}

// StakeCandidate represents a potential staking transaction
type StakeCandidate struct {
	Inputs      []*StakeInput
	TotalWeight int64
	Address     string
	Key         *crypto.PrivateKey
}

// StakingInfo provides staking statistics
type StakingInfo struct {
	Enabled       bool          `json:"enabled"`
	Staking       bool          `json:"staking"`
	Weight        int64         `json:"weight"`
	NetworkWeight int64         `json:"networkweight"`
	ExpectedTime  time.Duration `json:"expectedtime"`
	Difficulty    float64       `json:"difficulty"`
}

// SyncProgress tracks synchronization progress
type SyncProgress struct {
	StartHeight   int32     `json:"start_height"`
	CurrentHeight int32     `json:"current_height"`
	TargetHeight  int32     `json:"target_height"`
	Percentage    float64   `json:"percentage"`
	StartTime     time.Time `json:"start_time"`
	EstimatedTime time.Time `json:"estimated_time"`
	BlocksPerSec  float64   `json:"blocks_per_sec"`
}

// EncryptedData represents encrypted wallet data
type EncryptedData struct {
	Salt       []byte `json:"salt"`
	IV         []byte `json:"iv"`
	CipherText []byte `json:"ciphertext"`
	MAC        []byte `json:"mac"`
}

// CryptoParams defines encryption parameters
type CryptoParams struct {
	Iterations int
	KeyLength  int
	SaltLength int
	IVLength   int
}

// DefaultCryptoParams returns default encryption parameters
func DefaultCryptoParams() *CryptoParams {
	return &CryptoParams{
		Iterations: 10000,
		KeyLength:  32,
		SaltLength: 32,
		IVLength:   16,
	}
}

// WalletBackup represents a complete wallet backup
type WalletBackup struct {
	Version      int                        `json:"version"`
	CreatedAt    time.Time                  `json:"created_at"`
	Network      NetworkType                `json:"network"`
	MasterSeed   []byte                     `json:"master_seed"`
	Accounts     map[uint32]*Account        `json:"accounts"`
	Addresses    []*Address                 `json:"addresses"`
	Transactions []*WalletTransaction       `json:"transactions"`
	Labels       map[string]string          `json:"labels"`
	Settings     map[string]interface{}     `json:"settings"`
}