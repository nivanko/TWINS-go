// Copyright (c) 2025 The TWINS Core developers
// Distributed under the MIT software license

package rpc

import (
	"net"

	"github.com/twins-dev/twins-core/internal/p2p"
	"github.com/twins-dev/twins-core/pkg/crypto"
	"github.com/twins-dev/twins-core/pkg/types"
)

// ConfigSetter allows RPC handlers to persist settings to the daemon config file (twinsd.yml).
// Implemented by config.ConfigManager.
type ConfigSetter interface {
	Set(key string, value interface{}) error
}

// P2PServer interface for network control operations
type P2PServer interface {
	// Peer management
	GetPeers() []p2p.PeerInfo
	GetStats() *p2p.ServerStats
	PingAllPeers()

	// Node management
	AddNode(addr string, permanent bool) error
	RemoveNode(addr string) error
	ConnectNode(addr string) error
	DisconnectNode(addr string) error
	GetAddedNodes() []string

	// Ban management
	BanSubnet(subnet string, banTime int64, absolute bool, reason string) error
	UnbanSubnet(subnet string) error
	GetBannedList() []p2p.BanInfo
	ClearBannedList()

	// Network control
	SetNetworkActive(active bool)

	// Block relay (for submitblock RPC)
	RelayBlock(block *types.Block)
	RelayTransaction(tx *types.Transaction) error

	// Enhanced sync access (for getsyncstatus RPC)
	GetSyncer() *p2p.BlockchainSyncer
	GetHealthTracker() *p2p.PeerHealthTracker
	GetBootstrap() *p2p.BootstrapManager
}

// WalletInterface defines the wallet operations needed by RPC handlers
type WalletInterface interface {
	// Address management
	GetNewAddress(label string) (string, error)
	GetChangeAddress() (string, error)
	GetAddressInfo(address string) (*AddressInfo, error)
	ValidateAddress(address string) (*AddressValidation, error)
	ListAddresses() ([]*Address, error)
	SetLabel(address string, label string) error

	// Balance and UTXOs
	GetBalance() *Balance
	ListUnspent(minConf, maxConf int, addresses []string) (interface{}, error)

	// Transactions
	SendToAddress(address string, amount int64, comment string, subtractFee bool) (string, error)
	SendMany(recipients map[string]int64, comment string) (string, error)
	ListTransactions(count, skip int) ([]*WalletTransaction, error)
	GetTransaction(txid string) (*WalletTransaction, error)
	ListReceivedByAddress(minConf int, includeEmpty bool, includeWatchOnly bool) ([]interface{}, error)
	ListReceivedByAccount(minConf int, includeEmpty bool, includeWatchOnly bool) ([]interface{}, error)
	ListSinceBlock(blockHash *types.Hash, targetConf int, includeWatchOnly bool) ([]WalletTransaction, types.Hash, error)
	ListAccounts(minConf int, includeWatchOnly bool) (map[string]int64, error)
	ListAddressGroupings() ([][][]interface{}, error)

	// Key management
	DumpPrivKey(address string) (string, error)
	ImportPrivKey(privKey string, label string, rescan bool) error
	DumpHDInfo() (seed string, mnemonic string, mnemonicPass string, error error)
	DumpWallet(filename string) error
	ImportWallet(filename string) error
	ImportAddress(address string, label string, rescan bool) error

	// Encryption
	EncryptWallet(passphrase []byte) error
	WalletPassphrase(passphrase []byte, timeout int64, stakingOnly bool) error
	WalletLock() error
	WalletPassphraseChange(oldPass, newPass []byte) error
	IsLocked() bool

	// Message signing
	SignMessage(address string, message string) (string, error)
	VerifyMessage(address string, signature string, message string) (bool, error)

	// Wallet management
	GetWalletInfo() (*WalletInfo, error)
	BackupWallet(destination string) error
	KeypoolRefill(newsize int) error
	GetKeypoolOldest() int64
	GetKeypoolSize() int
	GetReserveBalance() (bool, int64, error)
	SetReserveBalance(enabled bool, amount int64) error
	GetStakeSplitThreshold() (int64, error)
	SetStakeSplitThreshold(threshold int64) error
	GetAutoCombineConfig() (enabled bool, target int64, cooldown int)
	SetAutoCombineConfig(enabled bool, target int64, cooldown int)
	SetTransactionFee(feePerKB int64) error
	AddMultisigAddress(nrequired int, keys []string, account string) (string, error)
	GetMultiSend() (interface{}, error)
	SetMultiSend(entries interface{}) error
	GetMultiSendSettings() (stakeEnabled bool, masternodeEnabled bool, disabledAddrs []string, err error)
	SetMultiSendSettings(stakeEnabled bool, masternodeEnabled bool, disabledAddrs []string) error

	// Coin locking (shared store for GUI and RPC)
	LockCoin(outpoint types.Outpoint)
	UnlockCoin(outpoint types.Outpoint)
	UnlockAllCoins()
	IsLockedCoin(outpoint types.Outpoint) bool
	ListLockedCoins() []types.Outpoint

	// Utility methods
	ExtractAddress(scriptPubKey []byte) string
	IsOurAddress(address string) bool
}

// WalletInfo represents wallet state information
type WalletInfo struct {
	WalletVersion    int     `json:"walletversion"`
	Balance          float64 `json:"balance"`
	DelegatedBalance float64 `json:"delegated_balance"`
	TxCount          int     `json:"txcount"`
	KeypoolOldest    int64   `json:"keypoololdest"`
	KeypoolSize      int     `json:"keypoolsize"`
	UnlockedUntil    int64   `json:"unlocked_until"`
	EncryptionStatus string  `json:"encryptionstatus"`
}

// Address represents a wallet address (simplified version for RPC)
type Address struct {
	Address string
	Label   string
}

// Balance represents wallet balance
type Balance struct {
	Confirmed   int64
	Unconfirmed int64
	Immature    int64
}

// AddressInfo contains detailed information about an address
type AddressInfo struct {
	Address     string `json:"address"`
	IsMine      bool   `json:"ismine"`
	IsScript    bool   `json:"isscript"`
	IsWatchOnly bool   `json:"iswatchonly"`
	Label       string `json:"label,omitempty"`
	PubKey      string `json:"pubkey,omitempty"`
	HDKeyPath   string `json:"hdkeypath,omitempty"`
}

// AddressValidation contains validation result for an address
type AddressValidation struct {
	IsValid     bool   `json:"isvalid"`
	Address     string `json:"address,omitempty"`
	IsScript    bool   `json:"isscript,omitempty"`
	IsWatchOnly bool   `json:"iswatchonly,omitempty"`
}

// UTXO represents an unspent transaction output
type UTXO struct {
	TxID          string `json:"txid"`
	Vout          uint32 `json:"vout"`
	Address       string `json:"address"`
	Label         string `json:"label,omitempty"`
	ScriptPubKey  string `json:"scriptPubKey"`
	Amount        int64  `json:"amount"`
	Confirmations int32  `json:"confirmations"`
	Spendable     bool   `json:"spendable"`
}

// WalletTransaction represents a wallet transaction
type WalletTransaction struct {
	TxID          string              `json:"txid"`
	Amount        int64               `json:"amount"`
	Fee           int64               `json:"fee,omitempty"`
	Confirmations int32               `json:"confirmations"`
	BlockHash     string              `json:"blockhash,omitempty"`
	BlockIndex    int32               `json:"blockindex,omitempty"`
	BlockHeight   int32               `json:"blockheight,omitempty"`
	BlockTime     int64               `json:"blocktime,omitempty"`
	Time          int64               `json:"time"`
	TimeReceived  int64               `json:"timereceived"`
	Comment       string              `json:"comment,omitempty"`
	Label         string              `json:"label,omitempty"`
	Address       string              `json:"address,omitempty"`
	Category      string              `json:"category"` // "send", "receive", "generate", "immature"
	Details       []TransactionDetail `json:"details,omitempty"`
	Hex           string              `json:"hex,omitempty"`
}

// TransactionDetail represents a single detail entry in a wallet transaction
type TransactionDetail struct {
	Account  string `json:"account"`
	Address  string `json:"address"`
	Category string `json:"category"`
	Amount   int64  `json:"amount"`
	Vout     uint32 `json:"vout"`
	Fee      int64  `json:"fee,omitempty"`
}

// ActiveMasternodeInterface defines the active masternode operations needed by RPC handlers
type ActiveMasternodeInterface interface {
	// Status and state
	GetStatus() string
	IsStarted() bool
	GetVin() types.Outpoint
	GetServiceAddr() net.Addr
	GetPubKeyMasternode() *crypto.PublicKey
	IsAutoManagementRunning() bool

	// Lifecycle management
	Initialize(privKeyWIF string, serviceAddr string) error
	Start(collateralTx types.Hash, collateralIdx uint32, collateralKey interface{}) error
	Stop()
	ManageStatus() error

	// Dependencies
	SetSyncChecker(fn func() bool)
	SetBalanceGetter(fn func() int64)
}

// MasternodeConfInterface defines operations for masternode.conf file
type MasternodeConfInterface interface {
	// Read reads and parses the masternode.conf file
	Read() error
	// GetEntries returns all masternode entries
	GetEntries() []*MasternodeConfEntry
	// GetEntry returns a masternode entry by alias
	GetEntry(alias string) *MasternodeConfEntry
	// GetCount returns the number of entries
	GetCount() int
}

// MasternodeConfEntry represents a single entry from masternode.conf
type MasternodeConfEntry struct {
	Alias           string
	IP              string // host:port
	PrivKey         string
	TxHash          types.Hash
	OutputIndex     uint32
	DonationAddress string
	DonationPercent int
}

// SporkManagerInterface defines operations for network parameter management
type SporkManagerInterface interface {
	// GetValue returns the current value of a spork by ID
	GetValue(sporkID int32) int64
	// IsActive checks if a spork is currently active (timestamp <= now)
	IsActive(sporkID int32) bool
}
