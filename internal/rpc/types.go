package rpc

import (
	"encoding/json"
	"fmt"
)

// JSON-RPC 2.0 specification types

// Request represents a JSON-RPC 2.0 request
type Request struct {
	JSONRPC    string          `json:"jsonrpc"`
	Method     string          `json:"method"`
	Params     json.RawMessage `json:"params,omitempty"`
	ID         interface{}     `json:"id"`
	RemoteAddr string          `json:"-"` // Set by handleRequest from http.Request.RemoteAddr
}

// Response represents a JSON-RPC 2.0 response
type Response struct {
	JSONRPC string      `json:"jsonrpc"`
	Result  interface{} `json:"result,omitempty"`
	Error   *Error      `json:"error,omitempty"`
	ID      interface{} `json:"id"`
}

// BatchRequest represents multiple JSON-RPC requests
type BatchRequest []*Request

// BatchResponse represents multiple JSON-RPC responses
type BatchResponse []*Response

// Error represents a JSON-RPC 2.0 error
type Error struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// Error implements the error interface
func (e *Error) Error() string {
	return fmt.Sprintf("JSON-RPC error %d: %s", e.Code, e.Message)
}

// Standard JSON-RPC error codes
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// Application-specific error codes
const (
	// General errors
	CodeUnknownError = -1

	// Blockchain errors (0-99)
	CodeBlockNotFound       = -5 // Bitcoin Core: RPC_INVALID_ADDRESS_OR_KEY
	CodeBlockHeightNotFound = -8

	// Transaction errors (100-199)
	CodeTransactionNotFound = -5 // Bitcoin Core: RPC_INVALID_ADDRESS_OR_KEY
	CodeInvalidTransaction  = -25
	CodeTransactionRejected = -26

	// Wallet errors (200-299)
	CodeWalletError          = -4
	CodeInsufficientFunds    = -6
	CodeInvalidAddress       = -5 // Bitcoin Core: RPC_INVALID_ADDRESS_OR_KEY (shared with block/tx not found)
	CodeInvalidAmount        = -3

	// Network errors (300-399)
	CodeNetworkError              = -20
	CodeNodeNotConnected          = -29 // RPC_CLIENT_NODE_NOT_CONNECTED
	CodeNodeAlreadyAdded          = -23 // RPC_CLIENT_NODE_ALREADY_ADDED
	CodeNodeNotAdded              = -24 // RPC_CLIENT_NODE_NOT_ADDED

	// Masternode errors (400-499)
	CodeMasternodeNotFound = -30
)

// NewError creates a new JSON-RPC error
func NewError(code int, message string, data interface{}) *Error {
	return &Error{
		Code:    code,
		Message: message,
		Data:    data,
	}
}

// NewParseError creates a parse error
func NewParseError(data interface{}) *Error {
	return &Error{
		Code:    CodeParseError,
		Message: "Parse error",
		Data:    data,
	}
}

// NewInvalidRequestError creates an invalid request error
func NewInvalidRequestError(data interface{}) *Error {
	return &Error{
		Code:    CodeInvalidRequest,
		Message: "Invalid Request",
		Data:    data,
	}
}

// NewMethodNotFoundError creates a method not found error
func NewMethodNotFoundError(method string) *Error {
	return &Error{
		Code:    CodeMethodNotFound,
		Message: "Method not found",
		Data:    method,
	}
}

// NewInvalidParamsError creates an invalid params error
func NewInvalidParamsError(data interface{}) *Error {
	return &Error{
		Code:    CodeInvalidParams,
		Message: "Invalid params",
		Data:    data,
	}
}

// NewInternalError creates an internal error
func NewInternalError(data interface{}) *Error {
	return &Error{
		Code:    CodeInternalError,
		Message: "Internal error",
		Data:    data,
	}
}

// Handler function type for RPC method handlers
type Handler func(*Request) *Response

// Middleware function type for wrapping handlers
type Middleware func(Handler) Handler

// Context holds request context information
type Context struct {
	Request    *Request
	RemoteAddr string
	User       string
	StartTime  int64
}

// Method represents an RPC method definition
type Method struct {
	Name        string
	Handler     Handler
	Description string
	Params      []ParamSpec
	Returns     string
	Examples    []string
}

// ParamSpec describes a parameter
type ParamSpec struct {
	Name        string
	Type        string
	Description string
	Required    bool
	Default     interface{}
}

// ServiceInfo contains service metadata
type ServiceInfo struct {
	Version     string   `json:"version"`
	ProtocolVersion int  `json:"protocolversion"`
	Methods     []string `json:"methods"`
	Uptime      int64    `json:"uptime"`
}

// BlockInfo represents block information
type BlockInfo struct {
	Hash              string   `json:"hash"`
	Confirmations     int64    `json:"confirmations"`
	Size              int      `json:"size"`
	Height            int64    `json:"height"`
	Version           int      `json:"version"`
	MerkleRoot        string   `json:"merkleroot"`
	Tx                []string `json:"tx"`
	Time              int64    `json:"time"`
	MedianTime        int64    `json:"mediantime"`
	Nonce             uint32   `json:"nonce"`
	Bits              string   `json:"bits"`
	Difficulty        float64  `json:"difficulty"`
	PreviousBlockHash string   `json:"previousblockhash,omitempty"`
	NextBlockHash     string   `json:"nextblockhash,omitempty"`
	MoneySupply       float64  `json:"moneysupply"`
	StakeModifier     string   `json:"stakemodifier,omitempty"`
}

// TransactionInfo represents transaction information
type TransactionInfo struct {
	Hex           string     `json:"hex,omitempty"`
	TxID          string     `json:"txid"`
	Hash          string     `json:"hash"`
	Version       int        `json:"version"`
	Size          int        `json:"size"`
	VSize         int        `json:"vsize"`
	LockTime      uint32     `json:"locktime"`
	Vin           []VinInfo  `json:"vin"`
	Vout          []VoutInfo `json:"vout"`
	BlockHash     string     `json:"blockhash,omitempty"`
	Confirmations int64      `json:"confirmations,omitempty"`
	Time          int64      `json:"time,omitempty"`
	BlockTime     int64      `json:"blocktime,omitempty"`
}

// VinInfo represents transaction input information
type VinInfo struct {
	TxID      string            `json:"txid,omitempty"`
	Vout      uint32            `json:"vout,omitempty"`
	ScriptSig *ScriptSigInfo    `json:"scriptSig,omitempty"`
	Sequence  uint32            `json:"sequence"`
	Coinbase  string            `json:"coinbase,omitempty"`
}

// VoutInfo represents transaction output information
type VoutInfo struct {
	Value        float64          `json:"value"`
	N            int              `json:"n"`
	ScriptPubKey *ScriptPubKeyInfo `json:"scriptPubKey"`
}

// ScriptSigInfo represents script signature information
type ScriptSigInfo struct {
	Asm string `json:"asm"`
	Hex string `json:"hex"`
}

// ScriptPubKeyInfo represents script public key information
type ScriptPubKeyInfo struct {
	Asm       string   `json:"asm"`
	Hex       string   `json:"hex"`
	ReqSigs   int      `json:"reqSigs,omitempty"`
	Type      string   `json:"type"`
	Addresses []string `json:"addresses,omitempty"`
}

// MempoolInfo represents mempool information
type MempoolInfo struct {
	Size      int     `json:"size"`
	Bytes     uint64  `json:"bytes"`
	Usage     uint64  `json:"usage"`
	MaxMempool uint64  `json:"maxmempool"`
	MempoolMinFee float64 `json:"mempoolminfee"`
}

// PeerInfo represents peer node information
type PeerInfo struct {
	ID             int      `json:"id"`
	Addr           string   `json:"addr"`
	AddrLocal      string   `json:"addrlocal,omitempty"`
	Services       string   `json:"services"`
	LastSend       int64    `json:"lastsend"`
	LastRecv       int64    `json:"lastrecv"`
	BytesSent      uint64   `json:"bytessent"`
	BytesRecv      uint64   `json:"bytesrecv"`
	ConnTime       int64    `json:"conntime"`
	TimeOffset     int      `json:"timeoffset"`
	PingTime       float64  `json:"pingtime,omitempty"`
	Version        int      `json:"version"`
	SubVer         string   `json:"subver"`
	Inbound        bool     `json:"inbound"`
	StartingHeight int64    `json:"startingheight"`
	BanScore       int      `json:"banscore"`
	SyncedHeaders    int64 `json:"synced_headers"`
	SyncedBlocks     int64 `json:"synced_blocks"`
	LastHeaderUpdate int64 `json:"last_header_update"`
}

// NetworkInfo represents network information
type NetworkInfo struct {
	Version         int      `json:"version"`
	SubVersion      string   `json:"subversion"`
	ProtocolVersion int      `json:"protocolversion"`
	LocalServices   string   `json:"localservices"`
	LocalRelay      bool     `json:"localrelay"`
	TimeOffset      int      `json:"timeoffset"`
	Connections     int      `json:"connections"`
	NetworkActive   bool     `json:"networkactive"`
	Networks        []string `json:"networks"`
	RelayFee        float64  `json:"relayfee"`
}

// ChainInfo represents blockchain information
type ChainInfo struct {
	Chain                string  `json:"chain"`
	Blocks               int64   `json:"blocks"`
	Headers              int64   `json:"headers"`
	BestBlockHash        string  `json:"bestblockhash"`
	Difficulty           float64 `json:"difficulty"`
	MedianTime           int64   `json:"mediantime"`
	VerificationProgress float64 `json:"verificationprogress"`
	ChainWork            string  `json:"chainwork"`
	Pruned               bool    `json:"pruned"`
}

// MasternodeInfo represents masternode information
type MasternodeInfo struct {
	Rank            int     `json:"rank"`
	TxHash          string  `json:"txhash"`
	OutputIndex     int     `json:"outputidx"`
	Status          string  `json:"status"`
	Addr            string  `json:"addr"`
	Version         int     `json:"version"`
	LastSeen        int64   `json:"lastseen"`
	ActiveSeconds   int64   `json:"activeseconds"`
	LastPaidTime    int64   `json:"lastpaidtime"`
	LastPaidBlock   int64   `json:"lastpaidblock"`
	IP              string  `json:"ip"`
	Payee           string  `json:"payee"`
	Protocol        int     `json:"protocol"`
	Tier            string  `json:"tier"`
	CollateralAmount float64 `json:"collateral"`
}