package core

import "time"

// Balance represents all wallet balance types
// This mirrors the balance information provided by the C++ wallet
// Equivalent to the balance information shown in overviewpage.cpp
type Balance struct {
	// ==========================================
	// TWINS Balance Fields
	// ==========================================

	// Total is the total TWINS balance (Available + Pending + Immature)
	Total float64 `json:"total"`

	// Available is the spendable balance (Spendable - Locked)
	// This is what the user can actually spend right now
	Available float64 `json:"available"`

	// Spendable is the confirmed spendable balance (before subtracting locked)
	Spendable float64 `json:"spendable"`

	// Pending is the balance from unconfirmed transactions
	// Equivalent to unconfirmedBalance in overviewpage.cpp
	Pending float64 `json:"pending"`

	// Immature is the balance from recent staking/mining that hasn't matured yet
	// Requires 120 confirmations to become spendable
	Immature float64 `json:"immature"`

	// Locked is the balance locked as masternode collateral
	// Calculated from UTXOs that match masternode.conf entries
	// Used by GUI to show "Available" vs "Locked" distinction
	Locked float64 `json:"locked"`
}

// TransactionType represents the type of wallet transaction
// Equivalent to TransactionRecord::Type enum in transactionrecord.h
type TransactionType string

const (
	// TxTypeOther represents an unknown or other transaction type
	TxTypeOther TransactionType = "other"

	// TxTypeGenerated represents a generated (mined) block reward
	TxTypeGenerated TransactionType = "generated"

	// TxTypeStakeMint represents a Proof-of-Stake reward
	TxTypeStakeMint TransactionType = "stake"

	// TxTypeSendToAddress represents sending to an address
	TxTypeSendToAddress TransactionType = "send"

	// TxTypeSendToOther represents sending to other (no label)
	TxTypeSendToOther TransactionType = "send_to_other"

	// TxTypeRecvWithAddress represents receiving with an address
	TxTypeRecvWithAddress TransactionType = "receive"

	// TxTypeMNReward represents a masternode reward payment
	TxTypeMNReward TransactionType = "masternode"

	// TxTypeRecvFromOther represents receiving from other (no label)
	TxTypeRecvFromOther TransactionType = "receive_from_other"

	// TxTypeSendToSelf represents an internal transfer to self
	TxTypeSendToSelf TransactionType = "send_to_self"

	// TxTypeConsolidation represents a UTXO consolidation (autocombine) transaction
	TxTypeConsolidation TransactionType = "consolidation"
)

// Transaction represents a wallet transaction
// Equivalent to CWalletTx in the C++ code and TransactionRecord in transactionrecord.h
type Transaction struct {
	// TxID is the transaction ID (hash)
	TxID string `json:"txid"`

	// Vout is the output index within the transaction (for received outputs)
	// Matches Qt's TransactionRecord::idx field
	Vout int `json:"vout"`

	// Amount is the transaction amount (positive for receive, negative for send)
	Amount float64 `json:"amount"`

	// Fee is the transaction fee (always positive)
	Fee float64 `json:"fee"`

	// Confirmations is the number of confirmations
	Confirmations int `json:"confirmations"`

	// BlockHash is the hash of the block containing this transaction
	BlockHash string `json:"block_hash"`

	// BlockHeight is the height of the block containing this transaction
	BlockHeight int64 `json:"block_height"`

	// Time is the transaction timestamp
	Time time.Time `json:"time"`

	// Type is the transaction type using the TransactionType enum
	Type TransactionType `json:"type"`

	// Address is the primary address involved in the transaction
	// For receive: this is YOUR receiving address
	// For send: this is the recipient's address
	Address string `json:"address"`

	// FromAddress is the sender's address (for receive transactions)
	// May be empty if sender is unknown (privacy feature)
	// Populated from mapValue["from"] in legacy C++ code
	FromAddress string `json:"from_address"`

	// Label is the optional label for the transaction (from address book)
	Label string `json:"label"`

	// Comment is the optional comment for the transaction
	Comment string `json:"comment"`

	// Category provides additional categorization (send, receive, generate, immature)
	Category string `json:"category"`

	// IsWatchOnly indicates if this is a watch-only transaction
	IsWatchOnly bool `json:"is_watch_only"`

	// IsLocked indicates if this transaction is InstantX/SwiftTX locked
	IsLocked bool `json:"is_locked"`

	// IsConflicted indicates if this transaction conflicts with another transaction
	IsConflicted bool `json:"is_conflicted"`

	// IsCoinbase indicates if this is a coinbase (mined) transaction
	IsCoinbase bool `json:"is_coinbase"`

	// IsCoinstake indicates if this is a coinstake (staking reward) transaction
	IsCoinstake bool `json:"is_coinstake"`

	// MaturesIn is the number of blocks until coinbase/coinstake can be spent
	// Only relevant when IsCoinbase or IsCoinstake is true and confirmations < maturity
	// 0 means already mature or not a coinbase/coinstake
	MaturesIn int `json:"matures_in"`

	// Debit is the debit amount (amount leaving the wallet)
	Debit float64 `json:"debit"`

	// Credit is the credit amount (amount entering the wallet)
	Credit float64 `json:"credit"`
}

// AddressValidation represents address validation result
// Equivalent to validateaddress RPC response
type AddressValidation struct {
	// IsValid indicates if the address is valid
	IsValid bool `json:"isvalid"`

	// Address is the validated address
	Address string `json:"address"`

	// IsMine indicates if this address belongs to the wallet
	IsMine bool `json:"ismine"`

	// IsWatchOnly indicates if this is a watch-only address
	IsWatchOnly bool `json:"iswatchonly"`

	// IsScript indicates if this is a script address
	IsScript bool `json:"isscript"`

	// PubKey is the public key for the address (if available)
	PubKey string `json:"pubkey"`

	// Account is the account name (deprecated but kept for compatibility)
	Account string `json:"account"`

	// HDKeyPath is the HD wallet derivation path (if applicable)
	HDKeyPath string `json:"hdkeypath"`

	// HDMasterKeyID is the master key ID for HD wallets
	HDMasterKeyID string `json:"hdmasterkeyid"`
}

// WalletInfo represents wallet state information
// Equivalent to getwalletinfo RPC response
type WalletInfo struct {
	// Version is the wallet version
	Version int `json:"version"`

	// Balance is the total wallet balance
	Balance float64 `json:"balance"`

	// UnconfirmedBalance is the unconfirmed balance
	UnconfirmedBalance float64 `json:"unconfirmed_balance"`

	// ImmatureBalance is the immature balance
	ImmatureBalance float64 `json:"immature_balance"`

	// TxCount is the number of transactions in the wallet
	TxCount int `json:"txcount"`

	// KeyPoolSize is the size of the key pool
	KeyPoolSize int `json:"keypoolsize"`

	// KeyPoolOldest is the timestamp of the oldest key in the pool
	KeyPoolOldest time.Time `json:"keypoololdest"`

	// Unlocked indicates if the wallet is unlocked
	Unlocked bool `json:"unlocked"`

	// UnlockedUntil is the timestamp when the wallet will auto-lock
	UnlockedUntil time.Time `json:"unlocked_until"`

	// Encrypted indicates if the wallet is encrypted
	Encrypted bool `json:"encrypted"`

	// PayTxFee is the transaction fee per kilobyte
	PayTxFee float64 `json:"paytxfee"`

	// HDMasterKeyID is the master key ID for HD wallets
	HDMasterKeyID string `json:"hdmasterkeyid"`
}

// BlockchainInfo represents blockchain state information
// Equivalent to getblockchaininfo RPC response
type BlockchainInfo struct {
	// Chain is the network name (main, test, regtest)
	Chain string `json:"chain"`

	// Blocks is the current number of blocks
	Blocks int64 `json:"blocks"`

	// Headers is the current number of headers
	Headers int64 `json:"headers"`

	// BestBlockHash is the hash of the best (tip) block
	BestBlockHash string `json:"bestblockhash"`

	// Difficulty is the current proof-of-work/proof-of-stake difficulty
	Difficulty float64 `json:"difficulty"`

	// MedianTime is the median time of the past 11 blocks
	MedianTime time.Time `json:"mediantime"`

	// VerificationProgress is the estimate of verification progress (0.0 to 1.0)
	VerificationProgress float64 `json:"verificationprogress"`

	// ChainWork is the total work in the active chain
	ChainWork string `json:"chainwork"`

	// Pruned indicates if the blockchain is pruned
	Pruned bool `json:"pruned"`

	// PruneHeight is the lowest height block stored (if pruned)
	PruneHeight int64 `json:"pruneheight"`

	// InitialBlockDownload indicates if in initial block download
	InitialBlockDownload bool `json:"initialblockdownload"`

	// SizeOnDisk is the estimated size of the blockchain on disk
	SizeOnDisk uint64 `json:"size_on_disk"`

	// ==========================================
	// Sync Status Fields
	// ==========================================
	// These fields provide user-friendly sync status information
	// for display in the GUI (like "out of sync", "32 weeks behind")

	// IsSyncing indicates if the blockchain is currently synchronizing
	IsSyncing bool `json:"is_syncing"`

	// IsOutOfSync indicates if the blockchain is behind the network
	// True if behindBlocks > 0 or behindTime > threshold
	IsOutOfSync bool `json:"is_out_of_sync"`

	// BehindBlocks is the number of blocks behind the network
	// Calculated as: network_height - local_height
	BehindBlocks int64 `json:"behind_blocks"`

	// BehindTime is a human-readable string describing how far behind
	// Examples: "5 minutes behind", "32 weeks behind", "up to date"
	BehindTime string `json:"behind_time"`

	// SyncPercentage is the sync progress (0-100)
	// Calculated from VerificationProgress
	SyncPercentage float64 `json:"sync_percentage"`

	// CurrentBlockScan is the current block being scanned/validated
	// Example: "Scanning block 1545297"
	CurrentBlockScan int64 `json:"current_block_scan"`

	// PeerCount is the number of connected peers (for sync status determination)
	PeerCount int `json:"peer_count"`

	// IsConnecting indicates insufficient peers for reliable consensus (peer_count < MinSyncPeers)
	IsConnecting bool `json:"is_connecting"`
}

// NetworkInfo represents network state information
// Equivalent to getnetworkinfo RPC response
type NetworkInfo struct {
	// Version is the server version
	Version int `json:"version"`

	// Subversion is the server subversion string
	Subversion string `json:"subversion"`

	// ProtocolVersion is the protocol version
	ProtocolVersion int `json:"protocolversion"`

	// LocalServices is the services offered by this node
	LocalServices string `json:"localservices"`

	// LocalRelay indicates if transaction relay is enabled
	LocalRelay bool `json:"localrelay"`

	// TimeOffset is the time offset from system clock
	TimeOffset int `json:"timeoffset"`

	// Connections is the number of connections
	Connections int `json:"connections"`

	// NetworkActive indicates if networking is enabled
	NetworkActive bool `json:"networkactive"`

	// Networks is the list of available networks
	Networks []NetworkType `json:"networks"`

	// RelayFee is the minimum relay fee
	RelayFee float64 `json:"relayfee"`

	// LocalAddresses is the list of local addresses
	LocalAddresses []LocalAddress `json:"localaddresses"`

	// Warnings contains any network warnings
	Warnings string `json:"warnings"`

	// NetworkHeight is the best known block height from network peer consensus.
	// 0 means unknown (not enough peers or no consensus yet).
	NetworkHeight int64 `json:"network_height"`
}

// NetworkType represents a network type (ipv4, ipv6, onion)
type NetworkType struct {
	Name      string `json:"name"`
	Limited   bool   `json:"limited"`
	Reachable bool   `json:"reachable"`
	Proxy     string `json:"proxy"`
}

// LocalAddress represents a local address
type LocalAddress struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
	Score   int    `json:"score"`
}

// Block represents a block in the blockchain
// Equivalent to getblock RPC response
type Block struct {
	// Hash is the block hash
	Hash string `json:"hash"`

	// Confirmations is the number of confirmations
	Confirmations int `json:"confirmations"`

	// Size is the block size in bytes
	Size int `json:"size"`

	// Height is the block height
	Height int64 `json:"height"`

	// Version is the block version
	Version int `json:"version"`

	// MerkleRoot is the merkle root
	MerkleRoot string `json:"merkleroot"`

	// Transactions is the list of transactions (can be TxIDs or full Transaction objects)
	Transactions []Transaction `json:"tx"`

	// Time is the block time
	Time time.Time `json:"time"`

	// MedianTime is the median time of past blocks
	MedianTime time.Time `json:"mediantime"`

	// Nonce is the block nonce
	Nonce uint32 `json:"nonce"`

	// Bits is the difficulty bits
	Bits string `json:"bits"`

	// Difficulty is the proof-of-work difficulty
	Difficulty float64 `json:"difficulty"`

	// ChainWork is the total work in the chain up to this block
	ChainWork string `json:"chainwork"`

	// PreviousBlockHash is the hash of the previous block
	PreviousBlockHash string `json:"previousblockhash"`

	// NextBlockHash is the hash of the next block
	NextBlockHash string `json:"nextblockhash"`

	// Flags is block flags (proof-of-work, proof-of-stake)
	Flags string `json:"flags"`

	// ProofHash is the proof hash (for PoS blocks)
	ProofHash string `json:"proofhash"`

	// Modifier is the stake modifier (for PoS)
	Modifier string `json:"modifier"`
}

// PeerInfo represents information about a connected peer
// Equivalent to getpeerinfo RPC response
type PeerInfo struct {
	// ID is the peer ID
	ID int `json:"id"`

	// Address is the peer address (IP:port)
	Address string `json:"addr"`

	// AddressLocal is the local address
	AddressLocal string `json:"addrlocal"`

	// Services is the services offered by the peer
	Services string `json:"services"`

	// LastSend is the timestamp of last send
	LastSend time.Time `json:"lastsend"`

	// LastRecv is the timestamp of last receive
	LastRecv time.Time `json:"lastrecv"`

	// BytesSent is the total bytes sent
	BytesSent uint64 `json:"bytessent"`

	// BytesRecv is the total bytes received
	BytesRecv uint64 `json:"bytesrecv"`

	// ConnTime is the connection time
	ConnTime time.Time `json:"conntime"`

	// TimeOffset is the time offset
	TimeOffset int `json:"timeoffset"`

	// PingTime is the ping time in seconds
	PingTime float64 `json:"pingtime"`

	// MinPing is the minimum ping time observed
	MinPing float64 `json:"minping"`

	// Version is the peer version
	Version int `json:"version"`

	// SubVer is the peer subversion string
	SubVer string `json:"subver"`

	// Inbound indicates if this is an inbound connection
	Inbound bool `json:"inbound"`

	// StartingHeight is the peer's starting block height
	StartingHeight int64 `json:"startingheight"`

	// BanScore is the ban score
	BanScore int `json:"banscore"`

	// SyncedHeaders is the last header synced from this peer
	SyncedHeaders int64 `json:"synced_headers"`

	// SyncedBlocks is the last block synced from this peer
	SyncedBlocks int64 `json:"synced_blocks"`

	// LastHeaderUpdate is the unix timestamp of when synced_headers was last updated
	LastHeaderUpdate int64 `json:"last_header_update"`

	// WhiteListed indicates if the peer is whitelisted
	WhiteListed bool `json:"whitelisted"`
}

// MasternodeInfo represents masternode information
// Equivalent to masternode list RPC response
type MasternodeInfo struct {
	// Rank is the masternode rank in the payment queue
	Rank int `json:"rank"`

	// Txhash is the collateral transaction hash
	Txhash string `json:"txhash"`

	// Outidx is the collateral output index
	Outidx int `json:"outidx"`

	// Status is the masternode status (ENABLED, EXPIRED, etc.)
	Status string `json:"status"`

	// Address is the masternode IP:port
	Address string `json:"addr"`

	// Version is the masternode version
	Version int `json:"version"`

	// LastSeen is the timestamp when the masternode was last seen
	LastSeen time.Time `json:"lastseen"`

	// ActiveTime is the total active time in seconds
	ActiveTime int64 `json:"activetime"`

	// LastPaid is the timestamp of the last payment
	LastPaid time.Time `json:"lastpaid"`

	// Tier is the masternode tier (1M, 5M, 20M, 100M)
	Tier string `json:"tier"`

	// PaymentAddress is the address receiving rewards
	PaymentAddress string `json:"paymentaddress"`

	// PubKey is the masternode public key
	PubKey string `json:"pubkey"`

	// PubKeyOperator is the operator public key
	PubKeyOperator string `json:"pubkey_operator"`
}

// MasternodeStatus represents local masternode status
// Equivalent to masternode status RPC response
type MasternodeStatus struct {
	// Status is the status string
	Status string `json:"status"`

	// Message is the status message
	Message string `json:"message"`

	// Txhash is the collateral transaction hash
	Txhash string `json:"txhash"`

	// Outidx is the collateral output index
	Outidx int `json:"outidx"`

	// NetAddr is the network address
	NetAddr string `json:"netaddr"`

	// Addr is the payment address
	Addr string `json:"addr"`

	// PubKey is the masternode public key
	PubKey string `json:"pubkey"`
}

// MasternodeCount represents masternode count statistics
// Equivalent to masternode count RPC response
type MasternodeCount struct {
	// Total is the total number of masternodes
	Total int `json:"total"`

	// Enabled is the number of enabled masternodes
	Enabled int `json:"enabled"`

	// InQueue is the number of masternodes in the payment queue
	InQueue int `json:"inqueue"`

	// Ipv4 is the number of IPv4 masternodes
	Ipv4 int `json:"ipv4"`

	// Ipv6 is the number of IPv6 masternodes
	Ipv6 int `json:"ipv6"`

	// Onion is the number of Tor masternodes
	Onion int `json:"onion"`

	// Tier1M is the number of 1M TWINS tier masternodes
	Tier1M int `json:"tier_1m"`

	// Tier5M is the number of 5M TWINS tier masternodes
	Tier5M int `json:"tier_5m"`

	// Tier20M is the number of 20M TWINS tier masternodes
	Tier20M int `json:"tier_20m"`

	// Tier100M is the number of 100M TWINS tier masternodes
	Tier100M int `json:"tier_100m"`
}

// MyMasternode represents a user's configured masternode for the UI table
// This matches the columns shown in the Qt wallet's masternodelist.cpp
// Data comes from masternode.conf entries combined with network status
type MyMasternode struct {
	// Alias is the user-defined name for this masternode
	Alias string `json:"alias"`

	// Address is the masternode IP:port (e.g., "45.123.45.67:9340")
	Address string `json:"address"`

	// Protocol is the masternode protocol version (e.g., 70922)
	Protocol int `json:"protocol"`

	// Status is the masternode status (ENABLED, MISSING, EXPIRED, etc.)
	Status string `json:"status"`

	// ActiveSeconds is the time in seconds since the masternode was activated
	// Displayed as "Xd Xh Xm Xs" in the Qt wallet
	ActiveSeconds int64 `json:"active_seconds"`

	// LastSeen is the timestamp when the masternode was last seen
	LastSeen time.Time `json:"last_seen"`

	// CollateralAddress is the TWINS address derived from the collateral public key
	CollateralAddress string `json:"collateral_address"`

	// Collateral transaction info (for internal use)
	TxHash     string `json:"tx_hash"`
	OutputIndex int    `json:"output_index"`
}

// StakingInfo represents staking status and statistics
// Equivalent to getstakinginfo RPC response
type StakingInfo struct {
	// Enabled indicates if staking is enabled
	Enabled bool `json:"enabled"`

	// Staking indicates if actively staking
	Staking bool `json:"staking"`

	// Errors contains any staking errors
	Errors string `json:"errors"`

	// CurrentBlockSize is the current block size
	CurrentBlockSize int64 `json:"currentblocksize"`

	// CurrentBlockTx is the number of transactions in current block
	CurrentBlockTx int `json:"currentblocktx"`

	// PooledTx is the number of transactions in mempool
	PooledTx int `json:"pooledtx"`

	// Difficulty is the current staking difficulty
	Difficulty float64 `json:"difficulty"`

	// SearchInterval is the stake search interval
	SearchInterval int `json:"search-interval"`

	// WalletUnlocked indicates if wallet is unlocked (required for staking)
	WalletUnlocked bool `json:"walletunlocked"`

	// ExpectedStakeTime is the estimated seconds until the next stake.
	// 0 means the value could not be computed (wallet locked, no UTXOs, etc.)
	ExpectedStakeTime int64 `json:"expectedstaketime"`
}

// UTXO represents an unspent transaction output
// OutPoint identifies a specific transaction output
// Corresponds to COutPoint in the C++ code
type OutPoint struct {
	// TxID is the transaction hash
	TxID string `json:"txid"`

	// Vout is the output index
	Vout uint32 `json:"vout"`
}

type UTXO struct {
	// TxID is the transaction ID
	TxID string `json:"txid"`

	// Vout is the output index
	Vout uint32 `json:"vout"`

	// Address is the address
	Address string `json:"address"`

	// Label is the address label (optional)
	Label string `json:"label,omitempty"`

	// ScriptPubKey is the script public key
	ScriptPubKey string `json:"scriptPubKey"`

	// Amount is the output amount
	Amount float64 `json:"amount"`

	// Confirmations is the number of confirmations
	Confirmations int `json:"confirmations"`

	// Spendable indicates if this UTXO is spendable
	Spendable bool `json:"spendable"`

	// Solvable indicates if this UTXO is solvable
	Solvable bool `json:"solvable"`

	// Locked indicates if this UTXO is locked from spending
	Locked bool `json:"locked"`

	// Type indicates the UTXO type ("Personal" or "MultiSig")
	Type string `json:"type"`

	// Date is the transaction timestamp
	Date int64 `json:"date"`

	// Priority is calculated as (amount * confirmations)
	Priority float64 `json:"priority"`
}

// SendToAddressParams contains parameters for sending a transaction
type SendToAddressParams struct {
	Address  string
	Amount   float64
	Comment  string
	CommentTo string
	SubtractFeeFromAmount bool
}

// ReceivingAddress represents a wallet receiving address
// Used for the receive page address management
type ReceivingAddress struct {
	// Address is the TWINS receiving address
	Address string `json:"address"`

	// Label is the optional label for this address
	Label string `json:"label"`

	// Created is the timestamp when this address was generated
	Created time.Time `json:"created"`
}

// PaymentRequest represents a payment request for receiving funds
// Stores the details needed to generate a payment URI and QR code
type PaymentRequest struct {
	// ID is the unique identifier for this payment request
	ID int64 `json:"id"`

	// Date is when the payment request was created
	Date time.Time `json:"date"`

	// Label is an optional label for the payment request
	Label string `json:"label"`

	// Address is the TWINS receiving address
	Address string `json:"address"`

	// Message is an optional message to include in the payment request
	Message string `json:"message"`

	// Amount is the requested amount in TWINS (0 means any amount)
	Amount float64 `json:"amount"`
}

// ==========================================
// Explorer Types
// ==========================================

// BlockSummary represents a compact block summary for list views
// Used in explorer block list to show minimal block information
type BlockSummary struct {
	// Height is the block height
	Height int64 `json:"height"`

	// Hash is the block hash
	Hash string `json:"hash"`

	// Time is the block timestamp
	Time time.Time `json:"time"`

	// TxCount is the number of transactions in the block
	TxCount int `json:"tx_count"`

	// Size is the block size in bytes
	Size int `json:"size"`

	// IsPoS indicates if this is a Proof-of-Stake block
	IsPoS bool `json:"is_pos"`

	// Reward is the total block reward (stake + masternode)
	Reward float64 `json:"reward"`
}

// BlockDetail represents detailed block information for explorer
// Extends Block with PoS reward details
type BlockDetail struct {
	// Embed the base Block type
	Block

	// TxIDs is the list of transaction IDs in the block
	TxIDs []string `json:"txids"`

	// IsPoS indicates if this is a Proof-of-Stake block
	IsPoS bool `json:"is_pos"`

	// StakeReward is the staking reward amount (for PoS blocks)
	StakeReward float64 `json:"stake_reward"`

	// MasternodeReward is the masternode reward amount
	MasternodeReward float64 `json:"masternode_reward"`

	// StakerAddress is the address that staked this block (for PoS)
	StakerAddress string `json:"staker_address"`

	// MasternodeAddress is the masternode payment address
	MasternodeAddress string `json:"masternode_address"`

	// TotalReward is the sum of stake + masternode rewards
	TotalReward float64 `json:"total_reward"`
}

// ExplorerTransaction represents a transaction for explorer view
// Similar to Transaction but with additional explorer-specific fields
type ExplorerTransaction struct {
	// TxID is the transaction hash
	TxID string `json:"txid"`

	// BlockHash is the hash of the containing block
	BlockHash string `json:"block_hash"`

	// BlockHeight is the height of the containing block
	BlockHeight int64 `json:"block_height"`

	// Confirmations is the number of confirmations
	Confirmations int `json:"confirmations"`

	// Time is the transaction timestamp
	Time time.Time `json:"time"`

	// Size is the transaction size in bytes
	Size int `json:"size"`

	// Fee is the transaction fee
	Fee float64 `json:"fee"`

	// IsCoinbase indicates if this is a coinbase transaction
	IsCoinbase bool `json:"is_coinbase"`

	// IsCoinStake indicates if this is a coinstake (PoS) transaction
	IsCoinStake bool `json:"is_coinstake"`

	// Inputs is the list of transaction inputs
	Inputs []TxInput `json:"inputs"`

	// Outputs is the list of transaction outputs
	Outputs []TxOutput `json:"outputs"`

	// TotalInput is the sum of all input values
	TotalInput float64 `json:"total_input"`

	// TotalOutput is the sum of all output values
	TotalOutput float64 `json:"total_output"`

	// RawHex is the raw transaction hex (optional, for advanced view)
	RawHex string `json:"raw_hex,omitempty"`
}

// TxInput represents a transaction input for explorer
type TxInput struct {
	// TxID is the previous transaction hash
	TxID string `json:"txid"`

	// Vout is the previous output index
	Vout uint32 `json:"vout"`

	// Address is the input address (if available)
	Address string `json:"address"`

	// Amount is the input amount
	Amount float64 `json:"amount"`

	// IsCoinbase indicates if this is a coinbase input
	IsCoinbase bool `json:"is_coinbase"`
}

// TxOutput represents a transaction output for explorer
type TxOutput struct {
	// Index is the output index
	Index uint32 `json:"index"`

	// Address is the output address
	Address string `json:"address"`

	// Amount is the output amount
	Amount float64 `json:"amount"`

	// ScriptType is the script type (pubkeyhash, scripthash, etc.)
	ScriptType string `json:"script_type"`

	// IsSpent indicates if this output has been spent
	IsSpent bool `json:"is_spent"`
}

// AddressInfo represents address information for explorer
type AddressInfo struct {
	// Address is the TWINS address
	Address string `json:"address"`

	// Balance is the current address balance
	Balance float64 `json:"balance"`

	// TotalReceived is the total amount received by this address
	TotalReceived float64 `json:"total_received"`

	// TotalSent is the total amount sent from this address
	TotalSent float64 `json:"total_sent"`

	// TxCount is the total number of transactions
	TxCount int `json:"tx_count"`

	// UnconfirmedBalance is the unconfirmed balance
	UnconfirmedBalance float64 `json:"unconfirmed_balance"`

	// Transactions is a list of recent transactions (limited)
	Transactions []AddressTx `json:"transactions"`

	// UTXOs is the list of unspent outputs (optional)
	UTXOs []AddressUTXO `json:"utxos,omitempty"`
}

// AddressTx represents a transaction in address history
type AddressTx struct {
	// TxID is the transaction hash
	TxID string `json:"txid"`

	// BlockHeight is the block height
	BlockHeight int64 `json:"block_height"`

	// Time is the transaction timestamp
	Time time.Time `json:"time"`

	// Amount is the net amount change for this address (+ received, - sent)
	Amount float64 `json:"amount"`

	// Confirmations is the number of confirmations
	Confirmations int `json:"confirmations"`
}

// AddressUTXO represents an unspent output for an address
type AddressUTXO struct {
	// TxID is the transaction hash
	TxID string `json:"txid"`

	// Vout is the output index
	Vout uint32 `json:"vout"`

	// Amount is the output amount
	Amount float64 `json:"amount"`

	// Confirmations is the number of confirmations
	Confirmations int `json:"confirmations"`

	// BlockHeight is the block height where this UTXO was created
	BlockHeight int64 `json:"block_height"`
}

// SearchResultType represents the type of search result
type SearchResultType string

const (
	// SearchResultBlock indicates the result is a block
	SearchResultBlock SearchResultType = "block"

	// SearchResultTransaction indicates the result is a transaction
	SearchResultTransaction SearchResultType = "transaction"

	// SearchResultAddress indicates the result is an address
	SearchResultAddress SearchResultType = "address"

	// SearchResultNotFound indicates no result was found
	SearchResultNotFound SearchResultType = "not_found"
)

// SearchResult represents the result of an explorer search
type SearchResult struct {
	// Type is the type of result found
	Type SearchResultType `json:"type"`

	// Query is the original search query
	Query string `json:"query"`

	// Block is populated if Type is SearchResultBlock
	Block *BlockDetail `json:"block,omitempty"`

	// Transaction is populated if Type is SearchResultTransaction
	Transaction *ExplorerTransaction `json:"transaction,omitempty"`

	// Address is populated if Type is SearchResultAddress
	Address *AddressInfo `json:"address,omitempty"`

	// Error is populated if the search failed
	Error string `json:"error,omitempty"`
}

// TransactionFilter contains all filter/sort/pagination parameters for server-side
// transaction listing. Sent from frontend to backend for each page request.
type TransactionFilter struct {
	Page     int `json:"page"`      // 1-based page number
	PageSize int `json:"page_size"` // 25, 50, 100, or 250

	DateFilter    string  `json:"date_filter"`     // "all","today","week","month","lastMonth","year","range"
	DateRangeFrom string  `json:"date_range_from"` // ISO date for "range" filter
	DateRangeTo   string  `json:"date_range_to"`   // ISO date for "range" filter
	TypeFilter    string  `json:"type_filter"`     // "all","mostCommon","received","sent","toYourself","mined","minted","masternode", etc.
	SearchText    string  `json:"search_text"`     // address/label substring search
	MinAmount     float64 `json:"min_amount"`      // minimum absolute amount in TWINS

	WatchOnlyFilter  string `json:"watch_only_filter"`  // "all","yes","no"
	HideOrphanStakes bool   `json:"hide_orphan_stakes"` // hide orphan/conflicted stakes

	SortColumn    string `json:"sort_column"`    // "date","type","address","amount"
	SortDirection string `json:"sort_direction"` // "asc","desc"
}

// TransactionPage is a paginated response for wallet transactions.
type TransactionPage struct {
	Transactions []Transaction `json:"transactions"`
	Total        int           `json:"total"`       // total matching filter
	TotalAll     int           `json:"total_all"`   // total in wallet (unfiltered)
	Page         int           `json:"page"`        // current page (1-based)
	PageSize     int           `json:"page_size"`   // items per page
	TotalPages   int           `json:"total_pages"` // ceil(Total / PageSize)
}

// ReceivingAddressFilter contains all filter/sort/pagination parameters for
// server-side receiving address listing. Sent from frontend to backend for each
// page request. Mirrors the TransactionFilter pattern.
//
// The enumeration always returns every wallet receiving address (labeled,
// used, and external keypool entries). The only optional filter is
// HideZeroBalance; all other refinement is done via SearchText.
type ReceivingAddressFilter struct {
	Page     int `json:"page"`      // 1-based page number
	PageSize int `json:"page_size"` // 25, 50, 100, or 250

	// HideZeroBalance: when true, addresses whose balance is exactly zero are
	// excluded. When false (default), no balance filter is applied.
	HideZeroBalance bool `json:"hide_zero_balance"`

	// SearchText: case-insensitive substring match against label OR address.
	SearchText string `json:"search_text"`

	SortColumn    string `json:"sort_column"`    // "label" | "balance"
	SortDirection string `json:"sort_direction"` // "asc" | "desc"
}

// ReceivingAddressRow is one row in the paginated receiving address list.
// Used by the GUI Receiving Addresses dialog.
type ReceivingAddressRow struct {
	Address           string    `json:"address"`
	Label             string    `json:"label"`
	Balance           float64   `json:"balance"`             // TWINS
	HasPaymentRequest bool      `json:"has_payment_request"` // true if any payment request targets this address
	Created           time.Time `json:"created"`
}

// ReceivingAddressPage is a paginated response for wallet receiving addresses.
type ReceivingAddressPage struct {
	Addresses   []ReceivingAddressRow `json:"addresses"`
	Total       int                   `json:"total"`        // total matching filter
	TotalAll    int                   `json:"total_all"`    // total in wallet (unfiltered)
	Page        int                   `json:"page"`         // current page (1-based)
	PageSize    int                   `json:"page_size"`    // items per page
	TotalPages  int                   `json:"total_pages"`  // ceil(Total / PageSize)
}
