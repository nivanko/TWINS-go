package rpc

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// ShutdownFunc is a callback function for initiating daemon shutdown
type ShutdownFunc func()

// registerControlHandlers registers daemon control RPC handlers
func (s *Server) registerControlHandlers() {
	s.RegisterHandler("stop", s.handleStop)
	s.RegisterHandler("help", s.handleHelp)
	s.RegisterHandler("setloglevel", s.handleSetLogLevel)
}

// SetShutdownFunc sets the shutdown callback function
// Must be called during initialization before Start() to avoid race conditions
func (s *Server) SetShutdownFunc(fn ShutdownFunc) {
	// Check if server has already started
	if s.started.Load() {
		panic("SetShutdownFunc called after server started - this is a race condition")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.shutdownFunc = fn
}

// handleHelp returns help text for RPC commands
func (s *Server) handleHelp(req *Request) *Response {
	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		// If params are invalid, just show general help
		params = []interface{}{}
	}

	// If command specified, show help for that command
	if len(params) > 0 {
		if command, ok := params[0].(string); ok {
			// Check if handler exists
			s.mu.RLock()
			_, exists := s.handlers[command]
			s.mu.RUnlock()

			if !exists {
				return &Response{
					JSONRPC: "2.0",
					Error:   NewError(-1, "help: unknown command: "+command, nil),
					ID:      req.ID,
				}
			}

			// Return help text for specific command
			helpText := s.getCommandHelp(command)
			return &Response{
				JSONRPC: "2.0",
				Result:  helpText,
				ID:      req.ID,
			}
		}
	}

	// No command specified - list all available commands
	s.mu.RLock()
	commands := make([]string, 0, len(s.handlers))
	for cmd := range s.handlers {
		commands = append(commands, cmd)
	}
	s.mu.RUnlock()

	// Sort commands alphabetically
	sort.Strings(commands)

	helpText := "Available RPC commands:\n"
	for _, cmd := range commands {
		helpText += cmd + "\n"
	}
	helpText += "\nUse 'help <command>' for detailed help on a specific command"

	return &Response{
		JSONRPC: "2.0",
		Result:  helpText,
		ID:      req.ID,
	}
}

// commandHelpTexts is the package-level map of RPC command help texts.
// Exported via GetCommandHelp() and GetCommandBriefDescriptions().
var commandHelpTexts = map[string]string{
		"setloglevel": "setloglevel \"level\"\n\n" +
			"Immediately change the log level of the running daemon.\n\n" +
			"Arguments:\n" +
			"1. \"level\"     (string, required) The new log level. One of: trace, debug, info, warn, error, fatal\n\n" +
			"Result:\n" +
			"\"text\"           (string) Confirmation of the new log level\n\n" +
			"Examples:\n" +
			"> twins-cli setloglevel debug\n" +
			"> twins-cli setloglevel error\n" +
			"> curl --user myusername --data-binary '{\"jsonrpc\": \"1.0\", \"id\":\"curltest\", \"method\": \"setloglevel\", \"params\": [\"debug\"] }' -H 'content-type: text/plain;' http://127.0.0.1:37818/",

		"stop": "stop\n\n" +
			"Stop TWINS server.\n\n" +
			"Result:\n" +
			"\"TWINS server stopping\"    (string) Confirmation message\n\n" +
			"Examples:\n" +
			"> twins-cli stop\n" +
			"> curl --user myusername --data-binary '{\"jsonrpc\": \"1.0\", \"id\":\"curltest\", \"method\": \"stop\", \"params\": [] }' -H 'content-type: text/plain;' http://127.0.0.1:37818/",

		"help": "help ( \"command\" )\n\n" +
			"List all commands, or get help for a specified command.\n\n" +
			"Arguments:\n" +
			"1. \"command\"     (string, optional) The command to get help on\n\n" +
			"Result:\n" +
			"\"text\"           (string) The help text\n\n" +
			"Examples:\n" +
			"> twins-cli help\n" +
			"> twins-cli help getinfo",

		"getinfo": "getinfo\n\n" +
			"Returns an object containing various state info.\n\n" +
			"Result:\n" +
			"{\n" +
			"  \"version\": xxxxx,           (numeric) the server version\n" +
			"  \"protocolversion\": xxxxx,   (numeric) the protocol version\n" +
			"  \"walletversion\": xxxxx,     (numeric) the wallet version\n" +
			"  \"balance\": xxxxxxx,         (numeric) the total TWINS balance of the wallet\n" +
			"  \"blocks\": xxxxxx,           (numeric) the current number of blocks processed in the server\n" +
			"  \"timeoffset\": xxxxx,        (numeric) the time offset\n" +
			"  \"connections\": xxxxx,       (numeric) the number of connections\n" +
			"  \"proxy\": \"host:port\",       (string, optional) the proxy used by the server\n" +
			"  \"difficulty\": xxxxxx,       (numeric) the current difficulty\n" +
			"  \"testnet\": true|false,      (boolean) if the server is using testnet or not\n" +
			"  \"moneysupply\": xxxxx,       (numeric) the money supply\n" +
			"  \"keypoololdest\": xxxxxx,    (numeric) the timestamp (seconds since GMT epoch) of the oldest pre-generated key in the key pool\n" +
			"  \"keypoolsize\": xxxx,        (numeric) how many new keys are pre-generated\n" +
			"  \"unlocked_until\": ttt,      (numeric) the timestamp in seconds since epoch (midnight Jan 1 1970 GMT) that the wallet is unlocked for transfers, or 0 if the wallet is locked\n" +
			"  \"paytxfee\": x.xxxx,         (numeric) the transaction fee set in TWINS/kB\n" +
			"  \"relayfee\": x.xxxx,         (numeric) minimum relay fee for non-free transactions in TWINS/kB\n" +
			"  \"staking status\": true|false, (boolean) if the wallet is staking or not\n" +
			"  \"errors\": \"...\"             (string) any error messages\n" +
			"}\n\n" +
			"Examples:\n" +
			"> twins-cli getinfo",

		"getblockcount": "getblockcount\n\n" +
			"Returns the number of blocks in the longest blockchain.\n\n" +
			"Result:\n" +
			"n    (numeric) The current block count\n\n" +
			"Examples:\n" +
			"> twins-cli getblockcount",

		"getbestblockhash": "getbestblockhash\n\n" +
			"Returns the hash of the best (tip) block in the longest blockchain.\n\n" +
			"Result:\n" +
			"\"hash\"    (string) The block hash hex-encoded\n\n" +
			"Examples:\n" +
			"> twins-cli getbestblockhash",

		"getblock": "getblock \"hash\" ( verbose )\n\n" +
			"Returns information about the block with the given hash.\n\n" +
			"Arguments:\n" +
			"1. \"hash\"          (string, required) The block hash\n" +
			"2. verbose         (boolean, optional, default=true) true for a json object, false for the hex encoded data\n\n" +
			"Result (for verbose = true):\n" +
			"{\n" +
			"  \"hash\" : \"hash\",       (string) the block hash (same as provided)\n" +
			"  \"confirmations\" : n,   (numeric) The number of confirmations, or -1 if the block is not on the main chain\n" +
			"  \"size\" : n,            (numeric) The block size\n" +
			"  \"height\" : n,          (numeric) The block height or index\n" +
			"  \"version\" : n,         (numeric) The block version\n" +
			"  \"merkleroot\" : \"xxxx\", (string) The merkle root\n" +
			"  \"tx\" : [               (array of string) The transaction ids\n" +
			"     \"transactionid\"     (string) The transaction id\n" +
			"     ,...\n" +
			"  ],\n" +
			"  \"time\" : ttt,          (numeric) The block time in seconds since epoch (Jan 1 1970 GMT)\n" +
			"  \"nonce\" : n,           (numeric) The nonce\n" +
			"  \"bits\" : \"1d00ffff\",   (string) The bits\n" +
			"  \"difficulty\" : x.xxx,  (numeric) The difficulty\n" +
			"  \"previousblockhash\" : \"hash\",  (string) The hash of the previous block\n" +
			"  \"nextblockhash\" : \"hash\"       (string) The hash of the next block\n" +
			"}\n\n" +
			"Result (for verbose=false):\n" +
			"\"data\"             (string) A string that is serialized, hex-encoded data for block 'hash'.\n\n" +
			"Examples:\n" +
			"> twins-cli getblock \"00000000c937983704a73af28acdec37b049d214adbda81d7e2a3dd146f6ed09\"\n" +
			"> twins-cli getblock \"00000000c937983704a73af28acdec37b049d214adbda81d7e2a3dd146f6ed09\" false",

		"listmasternodes": "listmasternodes ( \"filter\" )\n\n" +
			"Get a ranked list of masternodes.\n\n" +
			"Arguments:\n" +
			"1. \"filter\"    (string, optional) Filter by masternode address, status, or tier\n\n" +
			"Result:\n" +
			"[\n" +
			"  {\n" +
			"    \"rank\": n,           (numeric) Masternode rank\n" +
			"    \"txhash\": \"hash\",    (string) Collateral transaction hash\n" +
			"    \"outidx\": n,         (numeric) Collateral output index\n" +
			"    \"status\": \"status\",  (string) Status (ENABLED, EXPIRED, etc.)\n" +
			"    \"addr\": \"addr\",      (string) Masternode TWINS address\n" +
			"    \"version\": n,        (numeric) Masternode protocol version\n" +
			"    \"lastseen\": ttt,     (numeric) Last seen timestamp\n" +
			"    \"activetime\": ttt,   (numeric) Active time in seconds\n" +
			"    \"lastpaid\": ttt,     (numeric) Last payment timestamp\n" +
			"    \"tier\": \"tier\"       (string) Tier (Bronze, Silver, Gold, Platinum)\n" +
			"  }\n" +
			"  ,...\n" +
			"]\n\n" +
			"Examples:\n" +
			"> twins-cli listmasternodes\n" +
			"> twins-cli listmasternodes \"ENABLED\"",

		"getmasternodecount": "getmasternodecount\n\n" +
			"Get masternode count values by tier.\n\n" +
			"Result:\n" +
			"{\n" +
			"  \"total\": n,        (numeric) Total number of masternodes\n" +
			"  \"stable\": n,       (numeric) Stable masternodes (ENABLED)\n" +
			"  \"enabled\": n,      (numeric) Enabled masternodes\n" +
			"  \"inqueue\": n,      (numeric) Masternodes in payment queue\n" +
			"  \"bronze\": n,       (numeric) Bronze tier (1M TWINS)\n" +
			"  \"silver\": n,       (numeric) Silver tier (5M TWINS)\n" +
			"  \"gold\": n,         (numeric) Gold tier (20M TWINS)\n" +
			"  \"platinum\": n      (numeric) Platinum tier (100M TWINS)\n" +
			"}\n\n" +
			"Examples:\n" +
			"> twins-cli getmasternodecount",

		"masternodecurrent": "masternodecurrent\n\n" +
			"Get current masternode winner for next block.\n\n" +
			"Result:\n" +
			"{\n" +
			"  \"protocol\": n,       (numeric) Protocol version\n" +
			"  \"txhash\": \"hash\",    (string) Collateral transaction hash\n" +
			"  \"pubkey\": \"key\",     (string) MN Public key\n" +
			"  \"lastseen\": ttt,     (numeric) Last seen timestamp\n" +
			"  \"activeseconds\": n,  (numeric) Seconds MN has been active\n" +
			"  \"tier\": \"tier\"       (string) Tier (Bronze, Silver, Gold, Platinum)\n" +
			"}\n\n" +
			"Examples:\n" +
			"> twins-cli masternodecurrent",

		"getmasternodestatus": "getmasternodestatus\n\n" +
			"Print masternode status for this node.\n\n" +
			"Result:\n" +
			"{\n" +
			"  \"txhash\": \"hash\",      (string) Collateral transaction hash\n" +
			"  \"outputidx\": n,        (numeric) Collateral transaction output index\n" +
			"  \"netaddr\": \"addr\",     (string) Masternode network address\n" +
			"  \"addr\": \"addr\",        (string) TWINS address for masternode payments\n" +
			"  \"status\": \"status\",    (string) Masternode status\n" +
			"  \"message\": \"msg\",      (string) Status message\n" +
			"  \"tier\": \"tier\"         (string) Tier (Bronze, Silver, Gold, Platinum)\n" +
			"}\n\n" +
			"Examples:\n" +
			"> twins-cli getmasternodestatus",

		"getmasternodewinners": "getmasternodewinners ( blocks \"filter\" )\n\n" +
			"Print the masternode winners for the last n blocks.\n\n" +
			"Arguments:\n" +
			"1. blocks      (numeric, optional) Number of last blocks to check (default: 10)\n" +
			"2. \"filter\"    (string, optional) Filter by address or tier\n\n" +
			"Result:\n" +
			"[\n" +
			"  {\n" +
			"    \"height\": n,        (numeric) Block height\n" +
			"    \"winner\": {\n" +
			"      \"address\": \"addr\", (string) Winning masternode address\n" +
			"      \"nVotes\": n,       (numeric) Number of votes\n" +
			"      \"tier\": \"tier\"     (string) Tier (Bronze, Silver, Gold, Platinum)\n" +
			"    }\n" +
			"  }\n" +
			"  ,...\n" +
			"]\n\n" +
			"Examples:\n" +
			"> twins-cli getmasternodewinners\n" +
			"> twins-cli getmasternodewinners 20",

		"getmasternodescores": "getmasternodescores ( blocks )\n\n" +
			"Print list of winning masternode by score.\n\n" +
			"Arguments:\n" +
			"1. blocks    (numeric, optional) Number of blocks to check (default: 10)\n\n" +
			"Result:\n" +
			"[\n" +
			"  {\n" +
			"    \"rank\": n,           (numeric) Masternode rank\n" +
			"    \"score\": n,          (numeric) Masternode score\n" +
			"    \"address\": \"addr\",   (string) Masternode address\n" +
			"    \"tier\": \"tier\"       (string) Tier (Bronze, Silver, Gold, Platinum)\n" +
			"  }\n" +
			"  ,...\n" +
			"]\n\n" +
			"Examples:\n" +
			"> twins-cli getmasternodescores\n" +
			"> twins-cli getmasternodescores 20",

		"createmasternodekey": "createmasternodekey\n\n" +
			"Create a new masternode private key.\n\n" +
			"Result:\n" +
			"\"key\"    (string) Masternode private key\n\n" +
			"Use this key in masternode.conf and set masternodeprivkey= in twins.conf\n\n" +
			"Examples:\n" +
			"> twins-cli createmasternodekey",

		"getmasternodeoutputs": "getmasternodeoutputs\n\n" +
			"Print all masternode transaction outputs from wallet.\n\n" +
			"Result:\n" +
			"[\n" +
			"  {\n" +
			"    \"txhash\": \"hash\",   (string) Transaction hash\n" +
			"    \"outputidx\": n,     (numeric) Output index\n" +
			"    \"amount\": n,        (numeric) Output amount in TWINS\n" +
			"    \"tier\": \"tier\"      (string) Tier this output qualifies for\n" +
			"  }\n" +
			"  ,...\n" +
			"]\n\n" +
			"Valid masternode collateral amounts:\n" +
			"  Bronze: 1,000,000 TWINS\n" +
			"  Silver: 5,000,000 TWINS\n" +
			"  Gold: 20,000,000 TWINS\n" +
			"  Platinum: 100,000,000 TWINS\n\n" +
			"Examples:\n" +
			"> twins-cli getmasternodeoutputs",

		"listmasternodeconf": "listmasternodeconf ( \"filter\" )\n\n" +
			"Print masternode.conf in JSON format.\n\n" +
			"Arguments:\n" +
			"1. \"filter\"    (string, optional) Filter by alias\n\n" +
			"Result:\n" +
			"[\n" +
			"  {\n" +
			"    \"alias\": \"name\",      (string) Masternode alias\n" +
			"    \"address\": \"addr\",    (string) Masternode IP:port\n" +
			"    \"privateKey\": \"key\",  (string) Masternode private key\n" +
			"    \"txHash\": \"hash\",     (string) Collateral transaction hash\n" +
			"    \"outputIndex\": n,     (numeric) Collateral output index\n" +
			"    \"status\": \"status\"    (string) Configuration status\n" +
			"  }\n" +
			"  ,...\n" +
			"]\n\n" +
			"Examples:\n" +
			"> twins-cli listmasternodeconf\n" +
			"> twins-cli listmasternodeconf \"mn1\"",

		// === Additional Blockchain Commands ===
		"getblockhash": "getblockhash index\n\n" +
			"Returns hash of block in best-block-chain at index provided.\n\n" +
			"Arguments:\n" +
			"1. index         (numeric, required) The block index\n\n" +
			"Result:\n" +
			"\"hash\"         (string) The block hash\n\n" +
			"Examples:\n" +
			"> twins-cli getblockhash 1000",

		"getblockchaininfo": "getblockchaininfo\n\n" +
			"Returns an object containing various state info regarding block chain processing.\n\n" +
			"Result:\n" +
			"{\n" +
			"  \"chain\": \"xxxx\",        (string) current network name (main, test, regtest)\n" +
			"  \"blocks\": xxxxxx,         (numeric) the current number of blocks processed\n" +
			"  \"headers\": xxxxxx,        (numeric) the current number of headers we have validated\n" +
			"  \"bestblockhash\": \"...\", (string) the hash of the currently best block\n" +
			"  \"difficulty\": xxxxxx,     (numeric) the current difficulty\n" +
			"  \"verificationprogress\": xxxx, (numeric) estimate of verification progress [0..1]\n" +
			"  \"chainwork\": \"xxxx\"     (string) total amount of work in active chain, in hexadecimal\n" +
			"}\n\n" +
			"Examples:\n" +
			"> twins-cli getblockchaininfo",

		"getdifficulty": "getdifficulty\n\n" +
			"Returns the proof-of-work difficulty as a multiple of the minimum difficulty.\n\n" +
			"Result:\n" +
			"n.nnn       (numeric) the proof-of-work difficulty as a multiple of the minimum difficulty.\n\n" +
			"Examples:\n" +
			"> twins-cli getdifficulty",

		"getblockheader": "getblockheader \"hash\" ( verbose )\n\n" +
			"Returns information about blockheader <hash>.\n\n" +
			"Arguments:\n" +
			"1. \"hash\"          (string, required) The block hash\n" +
			"2. verbose         (boolean, optional, default=true) true for a json object, false for the hex encoded data\n\n" +
			"Result (for verbose = true):\n" +
			"{\n" +
			"  \"hash\" : \"hash\",     (string) the block hash (same as provided)\n" +
			"  \"confirmations\" : n,   (numeric) The number of confirmations\n" +
			"  \"height\" : n,          (numeric) The block height or index\n" +
			"  \"version\" : n,         (numeric) The block version\n" +
			"  \"merkleroot\" : \"xxxx\", (string) The merkle root\n" +
			"  \"time\" : ttt,          (numeric) The block time in seconds since epoch\n" +
			"  \"nonce\" : n,           (numeric) The nonce\n" +
			"  \"bits\" : \"1d00ffff\", (string) The bits\n" +
			"  \"difficulty\" : x.xxx,  (numeric) The difficulty\n" +
			"  \"previousblockhash\" : \"hash\",  (string) The hash of the previous block\n" +
			"  \"nextblockhash\" : \"hash\"       (string) The hash of the next block\n" +
			"}\n\n" +
			"Examples:\n" +
			"> twins-cli getblockheader \"00000000c937983704a73af28acdec37b049d214adbda81d7e2a3dd146f6ed09\"",

		// === Transaction Commands ===
		"getrawtransaction": "getrawtransaction \"txid\" ( verbose )\n\n" +
			"Return the raw transaction data.\n\n" +
			"Arguments:\n" +
			"1. \"txid\"      (string, required) The transaction id\n" +
			"2. verbose       (numeric, optional, default=0) If 0, return a string, other return a json object\n\n" +
			"Result (if verbose is not set or set to 0):\n" +
			"\"data\"      (string) The serialized, hex-encoded data for 'txid'\n\n" +
			"Result (if verbose > 0):\n" +
			"{\n" +
			"  \"hex\" : \"data\",       (string) The serialized, hex-encoded data for 'txid'\n" +
			"  \"txid\" : \"id\",        (string) The transaction id\n" +
			"  \"size\" : n,             (numeric) The transaction size\n" +
			"  \"version\" : n,          (numeric) The version\n" +
			"  \"locktime\" : ttt,       (numeric) The lock time\n" +
			"  \"vin\" : [               (array of json objects)\n" +
			"     {\n" +
			"       \"txid\": \"id\",    (string) The transaction id\n" +
			"       \"vout\": n,         (numeric) The output number\n" +
			"       \"scriptSig\": {     (json object) The script\n" +
			"         \"asm\": \"asm\",  (string) asm\n" +
			"         \"hex\": \"hex\"   (string) hex\n" +
			"       },\n" +
			"       \"sequence\": n     (numeric) The script sequence number\n" +
			"     }\n" +
			"     ,...\n" +
			"  ],\n" +
			"  \"vout\" : [              (array of json objects)\n" +
			"     {\n" +
			"       \"value\" : x.xxx,            (numeric) The value in TWINS\n" +
			"       \"n\" : n,                    (numeric) index\n" +
			"       \"scriptPubKey\" : {          (json object)\n" +
			"         \"asm\" : \"asm\",          (string) the asm\n" +
			"         \"hex\" : \"hex\",          (string) the hex\n" +
			"         \"reqSigs\" : n,            (numeric) The required sigs\n" +
			"         \"type\" : \"pubkeyhash\",  (string) The type, eg 'pubkeyhash'\n" +
			"         \"addresses\" : [           (json array of string)\n" +
			"           \"twinsaddress\"   (string) TWINS address\n" +
			"           ,...\n" +
			"         ]\n" +
			"       }\n" +
			"     }\n" +
			"     ,...\n" +
			"  ],\n" +
			"  \"blockhash\" : \"hash\",   (string) the block hash\n" +
			"  \"confirmations\" : n,      (numeric) The confirmations\n" +
			"  \"time\" : ttt,             (numeric) The transaction time in seconds since epoch\n" +
			"  \"blocktime\" : ttt         (numeric) The block time in seconds since epoch\n" +
			"}\n\n" +
			"Examples:\n" +
			"> twins-cli getrawtransaction \"mytxid\"\n" +
			"> twins-cli getrawtransaction \"mytxid\" 1",

		"sendrawtransaction": "sendrawtransaction \"hexstring\" ( allowhighfees )\n\n" +
			"Submits raw transaction (serialized, hex-encoded) to local node and network.\n\n" +
			"Arguments:\n" +
			"1. \"hexstring\"    (string, required) The hex string of the raw transaction)\n" +
			"2. allowhighfees    (boolean, optional, default=false) Allow high fees\n\n" +
			"Result:\n" +
			"\"hex\"             (string) The transaction hash in hex\n\n" +
			"Examples:\n" +
			"> twins-cli sendrawtransaction \"signedhex\"",

		"decoderawtransaction": "decoderawtransaction \"hexstring\"\n\n" +
			"Return a JSON object representing the serialized, hex-encoded transaction.\n\n" +
			"Arguments:\n" +
			"1. \"hexstring\"      (string, required) The transaction hex string\n\n" +
			"Result:\n" +
			"{\n" +
			"  \"txid\" : \"id\",        (string) The transaction id\n" +
			"  \"size\" : n,             (numeric) The transaction size\n" +
			"  \"version\" : n,          (numeric) The version\n" +
			"  \"locktime\" : ttt,       (numeric) The lock time\n" +
			"  \"vin\" : [               (array of json objects)\n" +
			"     ...\n" +
			"  ],\n" +
			"  \"vout\" : [              (array of json objects)\n" +
			"     ...\n" +
			"  ]\n" +
			"}\n\n" +
			"Examples:\n" +
			"> twins-cli decoderawtransaction \"hexstring\"",

		"createrawtransaction": "createrawtransaction [{\"txid\":\"id\",\"vout\":n},...] {\"address\":amount,...}\n\n" +
			"Create a transaction spending the given inputs and sending to the given addresses.\n\n" +
			"Arguments:\n" +
			"1. \"transactions\"        (string, required) A json array of json objects\n" +
			"     [\n" +
			"       {\n" +
			"         \"txid\":\"id\",    (string, required) The transaction id\n" +
			"         \"vout\":n        (numeric, required) The output number\n" +
			"       }\n" +
			"       ,...\n" +
			"     ]\n" +
			"2. \"addresses\"           (string, required) a json object with addresses as keys and amounts as values\n" +
			"    {\n" +
			"      \"address\": x.xxx   (numeric, required) The key is the TWINS address, the value is the TWINS amount\n" +
			"      ,...\n" +
			"    }\n\n" +
			"Result:\n" +
			"\"transaction\"            (string) hex string of the transaction\n\n" +
			"Examples:\n" +
			"> twins-cli createrawtransaction \"[{\\\"txid\\\":\\\"myid\\\",\\\"vout\\\":0}]\" \"{\\\"address\\\":0.01}\"",

		"signrawtransaction": "signrawtransaction \"hexstring\" ( [{\"txid\":\"id\",\"vout\":n,\"scriptPubKey\":\"hex\",\"redeemScript\":\"hex\"},...] [\"privatekey1\",...] sighashtype )\n\n" +
			"Sign inputs for raw transaction (serialized, hex-encoded).\n\n" +
			"Arguments:\n" +
			"1. \"hexstring\"     (string, required) The transaction hex string\n" +
			"2. \"prevtxs\"       (string, optional) An json array of previous dependent transaction outputs\n" +
			"3. \"privkeys\"      (string, optional) A json array of base58-encoded private keys for signing\n" +
			"4. \"sighashtype\"   (string, optional, default=ALL) The signature hash type\n\n" +
			"Result:\n" +
			"{\n" +
			"  \"hex\": \"value\",   (string) The raw transaction with signature(s) (hex-encoded string)\n" +
			"  \"complete\": n       (numeric) if transaction has a complete set of signature (0 if not)\n" +
			"}\n\n" +
			"Examples:\n" +
			"> twins-cli signrawtransaction \"myhex\"",

		"decodescript": "decodescript \"hex\"\n\n" +
			"Decode a hex-encoded script.\n\n" +
			"Arguments:\n" +
			"1. \"hex\"     (string) the hex encoded script\n\n" +
			"Result:\n" +
			"{\n" +
			"  \"asm\":\"asm\",   (string) Script public key\n" +
			"  \"hex\":\"hex\",   (string) hex encoded public key\n" +
			"  \"type\":\"type\", (string) The output type\n" +
			"  \"reqSigs\": n,    (numeric) The required signatures\n" +
			"  \"addresses\": [   (json array of string)\n" +
			"     \"address\"     (string) TWINS address\n" +
			"     ,...\n" +
			"  ],\n" +
			"  \"p2sh\",\"address\" (string) script address\n" +
			"}\n\n" +
			"Examples:\n" +
			"> twins-cli decodescript \"hexstring\"",

		// === Wallet Commands ===
		"getnewaddress": "getnewaddress ( \"account\" )\n\n" +
			"Returns a new TWINS address for receiving payments.\n\n" +
			"Arguments:\n" +
			"1. \"account\"        (string, optional) The account name for the address to be linked to. if not provided, the default account \"\" is used.\n\n" +
			"Result:\n" +
			"\"twinsaddress\"    (string) The new TWINS address\n\n" +
			"Examples:\n" +
			"> twins-cli getnewaddress\n" +
			"> twins-cli getnewaddress \"myaccount\"",

		"getbalance": "getbalance ( \"account\" minconf includeWatchonly )\n\n" +
			"Returns the server's total available balance.\n\n" +
			"Arguments:\n" +
			"1. \"account\"      (string, optional) The selected account, or \"*\" for entire wallet. Default is \"*\".\n" +
			"2. minconf          (numeric, optional, default=1) Only include transactions confirmed at least this many times.\n" +
			"3. includeWatchonly (bool, optional, default=false) Also include balance in watchonly addresses\n\n" +
			"Result:\n" +
			"amount              (numeric) The total amount in TWINS received for this account.\n\n" +
			"Examples:\n" +
			"> twins-cli getbalance\n" +
			"> twins-cli getbalance \"*\" 6",

		"sendtoaddress": "sendtoaddress \"twinsaddress\" amount ( \"comment\" \"comment-to\" )\n\n" +
			"Send an amount to a given address.\n\n" +
			"Arguments:\n" +
			"1. \"twinsaddress\"  (string, required) The TWINS address to send to.\n" +
			"2. \"amount\"      (numeric, required) The amount in TWINS to send. eg 0.1\n" +
			"3. \"comment\"     (string, optional) A comment used to store what the transaction is for.\n" +
			"4. \"comment-to\"  (string, optional) A comment to store the name of the person or organization to which you're sending the transaction.\n\n" +
			"Result:\n" +
			"\"transactionid\"  (string) The transaction id.\n\n" +
			"Examples:\n" +
			"> twins-cli sendtoaddress \"DMJRSsuU9zfyrvxVaAEFQqK4MxZg6vgeS6\" 0.1\n" +
			"> twins-cli sendtoaddress \"DMJRSsuU9zfyrvxVaAEFQqK4MxZg6vgeS6\" 0.1 \"donation\" \"seans outpost\"",

		"listunspent": "listunspent ( minconf maxconf  [\"address\",...] )\n\n" +
			"Returns array of unspent transaction outputs with between minconf and maxconf confirmations.\n\n" +
			"Arguments:\n" +
			"1. minconf          (numeric, optional, default=1) The minimum confirmations to filter\n" +
			"2. maxconf          (numeric, optional, default=9999999) The maximum confirmations to filter\n" +
			"3. \"addresses\"    (string) A json array of TWINS addresses to filter\n\n" +
			"Result:\n" +
			"[                   (array of json object)\n" +
			"  {\n" +
			"    \"txid\" : \"txid\",        (string) the transaction id\n" +
			"    \"vout\" : n,               (numeric) the vout value\n" +
			"    \"address\" : \"address\",  (string) the TWINS address\n" +
			"    \"account\" : \"account\",  (string) The associated account, or \"\" for the default account\n" +
			"    \"scriptPubKey\" : \"key\", (string) the script key\n" +
			"    \"amount\" : x.xxx,         (numeric) the transaction amount in TWINS\n" +
			"    \"confirmations\" : n       (numeric) The number of confirmations\n" +
			"  }\n" +
			"  ,...\n" +
			"]\n\n" +
			"Examples:\n" +
			"> twins-cli listunspent\n" +
			"> twins-cli listunspent 6 9999999 \"[\\\"DMJRSsuU9zfyrvxVaAEFQqK4MxZg6vgeS6\\\",\\\"DAD3Y6ivr8nPQLT1NEPX84DxGCw9jz9Jvg\\\"]\"",

		"lockunspent": "lockunspent unlock [{\"txid\":\"txid\",\"vout\":n},...]\n\n" +
			"Updates list of temporarily unspendable outputs.\n\n" +
			"Arguments:\n" +
			"1. unlock            (boolean, required) Whether to unlock (true) or lock (false) the specified transactions\n" +
			"2. \"transactions\"  (string) A json array of json objects. Each object has txid (string) vout (numeric)\n" +
			"     [\n" +
			"       {\n" +
			"         \"txid\":\"id\",    (string) The transaction id\n" +
			"         \"vout\": n        (numeric) The output number\n" +
			"       }\n" +
			"       ,...\n" +
			"     ]\n\n" +
			"Result:\n" +
			"true|false    (boolean) Whether the command was successful or not\n\n" +
			"Examples:\n" +
			"> twins-cli lockunspent false \"[{\\\"txid\\\":\\\"a08e6907dbbd3d809776dbfc5d82e371b764ed838b5655e72f463568df1aadf0\\\",\\\"vout\\\":1}]\"",

		"listlockunspent": "listlockunspent\n\n" +
			"Returns list of temporarily unspendable outputs.\n\n" +
			"Result:\n" +
			"[\n" +
			"  {\n" +
			"    \"txid\" : \"transactionid\",     (string) The transaction id locked\n" +
			"    \"vout\" : n                      (numeric) The vout value\n" +
			"  }\n" +
			"  ,...\n" +
			"]\n\n" +
			"Examples:\n" +
			"> twins-cli listlockunspent",

		"encryptwallet": "encryptwallet \"passphrase\"\n\n" +
			"Encrypts the wallet with 'passphrase'.\n\n" +
			"Arguments:\n" +
			"1. \"passphrase\"    (string) The pass phrase to encrypt the wallet with. Must be at least 1 character.\n\n" +
			"Result:\n" +
			"\"result\"           (string) A string describing the result\n\n" +
			"IMPORTANT: Any previous backups you have made of this wallet are now invalid!\n" +
			"Make new backups using the backupwallet command.\n\n" +
			"Examples:\n" +
			"> twins-cli encryptwallet \"my pass phrase\"",

		"walletpassphrase": "walletpassphrase \"passphrase\" timeout ( stakingonly )\n\n" +
			"Stores the wallet decryption key in memory for 'timeout' seconds.\n\n" +
			"Arguments:\n" +
			"1. \"passphrase\"     (string, required) The wallet passphrase\n" +
			"2. timeout            (numeric, required) The time to keep the decryption key in seconds.\n" +
			"3. stakingonly        (boolean, optional, default=false) If true, the wallet will only be unlocked for staking\n\n" +
			"Examples:\n" +
			"> twins-cli walletpassphrase \"my pass phrase\" 60\n" +
			"> twins-cli walletpassphrase \"my pass phrase\" 3600 true",

		"walletlock": "walletlock\n\n" +
			"Removes the wallet encryption key from memory, locking the wallet.\n" +
			"After calling this method, you will need to call walletpassphrase again before being able to call methods which require the wallet to be unlocked.\n\n" +
			"Examples:\n" +
			"> twins-cli walletlock",

		"dumpprivkey": "dumpprivkey \"twinsaddress\"\n\n" +
			"Reveals the private key corresponding to 'twinsaddress'.\n\n" +
			"Arguments:\n" +
			"1. \"twinsaddress\"   (string, required) The TWINS address for the private key\n\n" +
			"Result:\n" +
			"\"key\"                (string) The private key\n\n" +
			"Examples:\n" +
			"> twins-cli dumpprivkey \"myaddress\"",

		"importprivkey": "importprivkey \"twinsprivkey\" ( \"label\" rescan )\n\n" +
			"Adds a private key (as returned by dumpprivkey) to your wallet.\n\n" +
			"Arguments:\n" +
			"1. \"twinsprivkey\"   (string, required) The private key (see dumpprivkey)\n" +
			"2. \"label\"            (string, optional, default=\"\") An optional label\n" +
			"3. rescan               (boolean, optional, default=true) Rescan the wallet for transactions\n\n" +
			"Examples:\n" +
			"> twins-cli importprivkey \"mykey\"\n" +
			"> twins-cli importprivkey \"mykey\" \"testing\" false",

		"validateaddress": "validateaddress \"twinsaddress\"\n\n" +
			"Return information about the given TWINS address.\n\n" +
			"Arguments:\n" +
			"1. \"twinsaddress\"     (string, required) The TWINS address to validate\n\n" +
			"Result:\n" +
			"{\n" +
			"  \"isvalid\" : true|false,       (boolean) If the address is valid or not.\n" +
			"  \"address\" : \"twinsaddress\", (string) The TWINS address validated\n" +
			"  \"ismine\" : true|false,        (boolean) If the address is yours or not\n" +
			"  \"isscript\" : true|false,      (boolean) If the key is a script\n" +
			"  \"pubkey\" : \"publickeyhex\",  (string) The hex value of the raw public key\n" +
			"  \"iscompressed\" : true|false,  (boolean) If the address is compressed\n" +
			"  \"account\" : \"account\"       (string) The account associated with the address\n" +
			"}\n\n" +
			"Examples:\n" +
			"> twins-cli validateaddress \"DMJRSsuU9zfyrvxVaAEFQqK4MxZg6vgeS6\"",

		// === Network Commands ===
		"getconnectioncount": "getconnectioncount\n\n" +
			"Returns the number of connections to other nodes.\n\n" +
			"Result:\n" +
			"n          (numeric) The connection count\n\n" +
			"Examples:\n" +
			"> twins-cli getconnectioncount",

		"getpeerinfo": "getpeerinfo\n\n" +
			"Returns data about each connected network node as a json array of objects.\n\n" +
			"Result:\n" +
			"[\n" +
			"  {\n" +
			"    \"id\": n,                   (numeric) Peer index\n" +
			"    \"addr\":\"host:port\",      (string) The ip address and port of the peer\n" +
			"    \"addrlocal\":\"ip:port\",   (string) local address\n" +
			"    \"services\":\"xxxxxxxxxxxxxxxx\", (string) The services offered\n" +
			"    \"lastsend\": ttt,           (numeric) The time in seconds since epoch (Jan 1 1970 GMT) of the last send\n" +
			"    \"lastrecv\": ttt,           (numeric) The time in seconds since epoch (Jan 1 1970 GMT) of the last receive\n" +
			"    \"bytessent\": n,            (numeric) The total bytes sent\n" +
			"    \"bytesrecv\": n,            (numeric) The total bytes received\n" +
			"    \"conntime\": ttt,           (numeric) The connection time in seconds since epoch (Jan 1 1970 GMT)\n" +
			"    \"pingtime\": n,             (numeric) ping time\n" +
			"    \"version\": v,              (numeric) The peer version\n" +
			"    \"subver\": \"/Satoshi:x.x.x/\",  (string) The string version\n" +
			"    \"inbound\": true|false,     (boolean) Inbound (true) or Outbound (false)\n" +
			"    \"startingheight\": n,       (numeric) The starting height (block) of the peer\n" +
			"    \"banscore\": n,             (numeric) The ban score\n" +
			"    \"synced_headers\": n,       (numeric) The last header we have in common with this peer\n" +
			"    \"synced_blocks\": n,        (numeric) The last block we have in common with this peer\n" +
			"  }\n" +
			"  ,...\n" +
			"]\n\n" +
			"Examples:\n" +
			"> twins-cli getpeerinfo",

		"addnode": "addnode \"node\" \"add|remove|onetry\"\n\n" +
			"Attempts add or remove a node from the addnode list or try a connection to a node once.\n\n" +
			"Arguments:\n" +
			"1. \"node\"     (string, required) The node (see getpeerinfo for nodes)\n" +
			"2. \"command\"  (string, required) 'add' to add a node to the list, 'remove' to remove a node from the list, 'onetry' to try a connection to the node once\n\n" +
			"Examples:\n" +
			"> twins-cli addnode \"192.168.0.6:37817\" \"onetry\"\n" +
			"> twins-cli addnode \"192.168.0.6:37817\" \"add\"",

		"disconnectnode": "disconnectnode \"node\"\n\n" +
			"Immediately disconnects from the specified node.\n\n" +
			"Arguments:\n" +
			"1. \"node\"     (string, required) The node (see getpeerinfo for nodes)\n\n" +
			"Examples:\n" +
			"> twins-cli disconnectnode \"192.168.0.6:37817\"",

		"getnetworkinfo": "getnetworkinfo\n\n" +
			"Returns an object containing various state info regarding P2P networking.\n\n" +
			"Result:\n" +
			"{\n" +
			"  \"version\": xxxxx,                      (numeric) the server version\n" +
			"  \"subversion\": \"/Satoshi:x.x.x/\",     (string) the server subversion string\n" +
			"  \"protocolversion\": xxxxx,              (numeric) the protocol version\n" +
			"  \"localservices\": \"xxxxxxxxxxxxxxxx\", (string) the services we offer to the network\n" +
			"  \"timeoffset\": xxxxx,                   (numeric) the time offset\n" +
			"  \"connections\": xxxxx,                  (numeric) the number of connections\n" +
			"  \"networks\": [                          (array) information per network\n" +
			"  {\n" +
			"    \"name\": \"xxx\",                     (string) network (ipv4, ipv6 or onion)\n" +
			"    \"limited\": true|false,               (boolean) is the network limited using -onlynet?\n" +
			"    \"reachable\": true|false,             (boolean) is the network reachable?\n" +
			"    \"proxy\": \"host:port\"               (string) the proxy that is used for this network, or empty if none\n" +
			"  }\n" +
			"  ,...\n" +
			"  ],\n" +
			"  \"relayfee\": x.xxxxxxxx,                (numeric) minimum relay fee for non-free transactions in TWINS/kB\n" +
			"  \"localaddresses\": [                    (array) list of local addresses\n" +
			"  {\n" +
			"    \"address\": \"xxxx\",                 (string) network address\n" +
			"    \"port\": xxx,                         (numeric) network port\n" +
			"    \"score\": xxx                         (numeric) relative score\n" +
			"  }\n" +
			"  ,...\n" +
			"  ]\n" +
			"}\n\n" +
			"Examples:\n" +
			"> twins-cli getnetworkinfo",

	// === Mempool Commands ===
	"getrawmempool": "getrawmempool ( verbose )\n\n" +
		"Returns all transaction ids in memory pool as a json array of string transaction ids.\n\n" +
		"Arguments:\n" +
		"1. verbose           (boolean, optional, default=false) true for a json object, false for array of transaction ids\n\n" +
		"Result: (for verbose = false):\n" +
		"[                     (json array of string)\n" +
		"  \"transactionid\"     (string) The transaction id\n" +
		"  ,...\n" +
		"]\n\n" +
		"Result: (for verbose = true):\n" +
		"{                           (json object)\n" +
		"  \"transactionid\" : {       (json object)\n" +
		"    \"size\" : n,             (numeric) transaction size in bytes\n" +
		"    \"fee\" : n,              (numeric) transaction fee in TWINS\n" +
		"    \"time\" : n,             (numeric) local time transaction entered pool in seconds since 1 Jan 1970 GMT\n" +
		"    \"height\" : n,           (numeric) block height when transaction entered pool\n" +
		"    \"startingpriority\" : n, (numeric) priority when transaction entered pool\n" +
		"    \"currentpriority\" : n,  (numeric) transaction priority now\n" +
		"    \"depends\" : [           (array) unconfirmed transactions used as inputs for this transaction\n" +
		"        \"transactionid\",    (string) parent transaction id\n" +
		"       ,...\n" +
		"    ]\n" +
		"  }, ...\n" +
		"}\n\n" +
		"Examples:\n" +
		"> twins-cli getrawmempool\n" +
		"> twins-cli getrawmempool true",

	"getmempoolinfo": "getmempoolinfo\n\n" +
		"Returns details on the active state of the TX memory pool.\n\n" +
		"Result:\n" +
		"{\n" +
		"  \"size\": xxxxx         (numeric) Current tx count\n" +
		"  \"bytes\": xxxxx        (numeric) Sum of all tx sizes\n" +
		"  \"usage\": xxxxx        (numeric) Total memory usage for the mempool\n" +
		"}\n\n" +
		"Examples:\n" +
		"> twins-cli getmempoolinfo",

	"getmempoolentry": "getmempoolentry \"txid\"\n\n" +
		"Returns mempool data for given transaction.\n\n" +
		"Arguments:\n" +
		"1. \"txid\"         (string, required) The transaction id (must be in mempool)\n\n" +
		"Result:\n" +
		"{                           (json object)\n" +
		"    \"size\" : n,             (numeric) transaction size in bytes\n" +
		"    \"fee\" : n,              (numeric) transaction fee in TWINS\n" +
		"    \"time\" : n,             (numeric) local time transaction entered pool\n" +
		"    \"height\" : n,           (numeric) block height when transaction entered pool\n" +
		"    \"depends\" : [           (array) unconfirmed transactions used as inputs\n" +
		"        \"transactionid\",    (string) parent transaction id\n" +
		"       ,...\n" +
		"    ]\n" +
		"}\n\n" +
		"Examples:\n" +
		"> twins-cli getmempoolentry \"mytxid\"",

	"getmempoolancestors": "getmempoolancestors \"txid\" ( verbose )\n\n" +
		"If txid is in the mempool, returns all in-mempool ancestors.\n\n" +
		"Arguments:\n" +
		"1. \"txid\"           (string, required) The transaction id (must be in mempool)\n" +
		"2. verbose             (boolean, optional, default=false) true for a json object, false for array of txids\n\n" +
		"Result (for verbose=false):\n" +
		"[                       (json array of strings)\n" +
		"  \"transactionid\"       (string) The transaction id of an in-mempool ancestor transaction\n" +
		"  ,...\n" +
		"]\n\n" +
		"Result (for verbose=true):\n" +
		"{                           (json object)\n" +
		"  \"transactionid\" : {       (json object)\n" +
		"    \"size\" : n,             (numeric) transaction size in bytes\n" +
		"    \"fee\" : n,              (numeric) transaction fee in TWINS\n" +
		"    \"time\" : n,             (numeric) local time transaction entered pool\n" +
		"    \"height\" : n,           (numeric) block height when transaction entered pool\n" +
		"    \"depends\" : [           (array) unconfirmed transactions used as inputs\n" +
		"        \"transactionid\",    (string) parent transaction id\n" +
		"       ,...\n" +
		"    ]\n" +
		"  }, ...\n" +
		"}\n\n" +
		"Examples:\n" +
		"> twins-cli getmempoolancestors \"mytxid\"",

	"getmempooldescendants": "getmempooldescendants \"txid\" ( verbose )\n\n" +
		"If txid is in the mempool, returns all in-mempool descendants.\n\n" +
		"Arguments:\n" +
		"1. \"txid\"           (string, required) The transaction id (must be in mempool)\n" +
		"2. verbose             (boolean, optional, default=false) true for a json object, false for array of txids\n\n" +
		"Result (for verbose=false):\n" +
		"[                       (json array of strings)\n" +
		"  \"transactionid\"       (string) The transaction id of an in-mempool descendant transaction\n" +
		"  ,...\n" +
		"]\n\n" +
		"Result (for verbose=true):\n" +
		"{                           (json object)\n" +
		"  \"transactionid\" : {       (json object)\n" +
		"    \"size\" : n,             (numeric) transaction size in bytes\n" +
		"    \"fee\" : n,              (numeric) transaction fee in TWINS\n" +
		"    \"time\" : n,             (numeric) local time transaction entered pool\n" +
		"    \"height\" : n,           (numeric) block height when transaction entered pool\n" +
		"    \"depends\" : [           (array) unconfirmed transactions used as inputs\n" +
		"        \"transactionid\",    (string) parent transaction id\n" +
		"       ,...\n" +
		"    ]\n" +
		"  }, ...\n" +
		"}\n\n" +
		"Examples:\n" +
		"> twins-cli getmempooldescendants \"mytxid\"",

	// === Additional Masternode Commands ===
	"masternode": "masternode \"command\"...\n\n" +
		"Set of commands to execute masternode related actions.\n\n" +
		"Arguments:\n" +
		"1. \"command\"        (string, required) The command to execute\n\n" +
		"Available commands:\n" +
		"  count        - Print count information of all known masternodes\n" +
		"  current      - Print info on current masternode winner\n" +
		"  debug        - Print masternode status\n" +
		"  genkey        - Generate new masternodeprivkey\n" +
		"  outputs       - Print masternode compatible outputs\n" +
		"  start-alias   - Start single remote masternode by assigned alias\n" +
		"  start-all     - Start all remote masternodes\n" +
		"  start-many    - Start all remote masternodes (deprecated, use start-all)\n" +
		"  start-missing - Start all remote masternodes that are missing (not in masternode list)\n" +
		"  start-disabled - Start all remote masternodes that are disabled\n" +
		"  list          - Print list of all known masternodes (see listmasternodes for more info)\n" +
		"  list-conf     - Print masternode.conf in JSON format\n" +
		"  status        - Print masternode status information\n" +
		"  winners       - Print list of masternode winners\n\n" +
		"Examples:\n" +
		"> twins-cli masternode count\n" +
		"> twins-cli masternode list",

	"masternodedebug": "masternodedebug\n\n" +
		"Print masternode status.\n\n" +
		"Result:\n" +
		"\"status\"     (string) Masternode status message\n\n" +
		"Examples:\n" +
		"> twins-cli masternodedebug",

	"startmasternode": "startmasternode \"set\" \"lockwallet\" ( \"alias\" )\n\n" +
		"Attempts to start one or more masternode(s).\n\n" +
		"Arguments:\n" +
		"1. \"set\"         (string, required) Specify which set of masternode(s) to start.\n" +
		"                   Options: \"alias\", \"all\", \"many\", \"missing\", \"disabled\"\n" +
		"2. \"lockwallet\"  (string, required) Lock wallet after completion (\"true\" or \"false\")\n" +
		"3. \"alias\"       (string, required for set=\"alias\") The alias of the masternode to start\n\n" +
		"Result:\n" +
		"{\n" +
		"  \"overall\": \"xxxx\",     (string) Overall status message\n" +
		"  \"detail\": [             (array) Status detail for each masternode\n" +
		"    {\n" +
		"      \"alias\": \"xxxx\",   (string) Masternode alias\n" +
		"      \"result\": \"xxxx\",  (string) Start result (\"successful\" or error message)\n" +
		"    }\n" +
		"    ,...\n" +
		"  ]\n" +
		"}\n\n" +
		"Examples:\n" +
		"> twins-cli startmasternode \"alias\" \"false\" \"my_mn\"\n" +
		"> twins-cli startmasternode \"all\" \"true\"",

	"createmasternodebroadcast": "createmasternodebroadcast \"command\" ( \"alias\" )\n\n" +
		"Creates a masternode broadcast message for one or all masternodes configured in masternode.conf.\n\n" +
		"Arguments:\n" +
		"1. \"command\"      (string, required) \"alias\" for single masternode, \"all\" for all masternodes\n" +
		"2. \"alias\"        (string, required for command=\"alias\") The alias of the masternode\n\n" +
		"Result:\n" +
		"{\n" +
		"  \"overall\": \"xxxx\",        (string) Overall status\n" +
		"  \"detail\": [                (array) Status for each masternode\n" +
		"    {\n" +
		"      \"alias\": \"xxxx\",      (string) Masternode alias\n" +
		"      \"success\": true|false, (boolean) Success status\n" +
		"      \"hex\": \"xxxx\"         (string) Broadcast data in hex (if successful)\n" +
		"    }\n" +
		"    ,...\n" +
		"  ]\n" +
		"}\n\n" +
		"Examples:\n" +
		"> twins-cli createmasternodebroadcast \"alias\" \"my_mn\"\n" +
		"> twins-cli createmasternodebroadcast \"all\"",

	"decodemasternodebroadcast": "decodemasternodebroadcast \"hexstring\"\n\n" +
		"Decode a masternode broadcast message from hex.\n\n" +
		"Arguments:\n" +
		"1. \"hexstring\"    (string, required) The hex encoded masternode broadcast message\n\n" +
		"Result:\n" +
		"{\n" +
		"  \"vin\": \"xxxx\",                (string) The unspent output which is holding the masternode collateral\n" +
		"  \"addr\": \"xxxx\",               (string) IP address and port\n" +
		"  \"pubkeycollateral\": \"xxxx\",   (string) Collateral address public key\n" +
		"  \"pubkeymasternode\": \"xxxx\",   (string) Masternode public key\n" +
		"  \"vchsig\": \"xxxx\",            (string) Broadcast signature (Base64)\n" +
		"  \"sigtime\": \"nnn\",            (numeric) Signature time\n" +
		"  \"protocolversion\": \"nnn\",    (numeric) Masternode protocol version\n" +
		"  \"lastping\" : {                (object) Last masternode ping\n" +
		"    \"vin\": \"xxxx\",             (string) Masternode collateral output\n" +
		"    \"blockhash\": \"xxxx\",       (string) Block hash of last known block\n" +
		"    \"sigtime\": \"nnn\",          (numeric) Ping signature time\n" +
		"    \"vchsig\": \"xxxx\"           (string) Ping signature (Base64)\n" +
		"  }\n" +
		"}\n\n" +
		"Examples:\n" +
		"> twins-cli decodemasternodebroadcast \"hexstring\"",

	"relaymasternodebroadcast": "relaymasternodebroadcast \"hexstring\"\n\n" +
		"Relay a masternode broadcast message to the network.\n\n" +
		"Arguments:\n" +
		"1. \"hexstring\"    (string, required) The hex encoded masternode broadcast message\n\n" +
		"Result:\n" +
		"true|false    (boolean) Whether the broadcast was successfully relayed\n\n" +
		"Examples:\n" +
		"> twins-cli relaymasternodebroadcast \"hexstring\"",

	"masternodeconnect": "masternodeconnect \"address\"\n\n" +
		"Attempts to connect to specified masternode address.\n\n" +
		"Arguments:\n" +
		"1. \"address\"     (string, required) IP address and port of the masternode (e.g. \"192.168.0.6:37817\")\n\n" +
		"Result:\n" +
		"\"status\"         (string) Connection attempt result\n\n" +
		"Examples:\n" +
		"> twins-cli masternodeconnect \"192.168.0.6:37817\"",

	"getpoolinfo": "getpoolinfo\n\n" +
		"Returns anonymous pool-related information.\n\n" +
		"Result:\n" +
		"{\n" +
		"  \"current\": \"addr\",    (string) TWINS address of current masternode\n" +
		"  \"state\": xxxx,        (string) Current state of the pool\n" +
		"  \"entries\": xxxx,      (numeric) Number of entries in the pool\n" +
		"  \"accepted\": xxxx,     (numeric) Number of accepted entries\n" +
		"}\n\n" +
		"Examples:\n" +
		"> twins-cli getpoolinfo",

	"stopmasternode": "stopmasternode \"set\" ( \"alias\" )\n\n" +
		"Attempts to stop one or all masternode(s).\n\n" +
		"Arguments:\n" +
		"1. \"set\"         (string, required) Specify which masternode(s) to stop.\n" +
		"                   Options: \"alias\", \"all\"\n" +
		"2. \"alias\"       (string, required for set=\"alias\") The alias of the masternode to stop\n\n" +
		"Result:\n" +
		"{\n" +
		"  \"overall\": \"xxxx\",     (string) Overall status message\n" +
		"  \"detail\": [             (array) Status detail for each masternode\n" +
		"    {\n" +
		"      \"alias\": \"xxxx\",   (string) Masternode alias\n" +
		"      \"result\": \"xxxx\"   (string) Stop result\n" +
		"    }\n" +
		"    ,...\n" +
		"  ]\n" +
		"}\n\n" +
		"Examples:\n" +
		"> twins-cli stopmasternode \"alias\" \"my_mn\"\n" +
		"> twins-cli stopmasternode \"all\"",

	// === Utility Commands ===
	"setmocktime": "setmocktime timestamp\n\n" +
		"Set the local time to given timestamp (-regtest only).\n\n" +
		"Arguments:\n" +
		"1. timestamp  (integer, required) Unix seconds-since-epoch timestamp\n" +
		"   Pass 0 to go back to using the system time.\n\n" +
		"Examples:\n" +
		"> twins-cli setmocktime 1503840000",

	"mnsync": "mnsync \"command\"\n\n" +
		"Returns the sync status or resets sync.\n\n" +
		"Arguments:\n" +
		"1. \"command\"    (string, required) The command to execute\n\n" +
		"Available commands:\n" +
		"  status    - Print sync status\n" +
		"  reset     - Reset masternode sync\n\n" +
		"Result (for status):\n" +
		"{\n" +
		"  \"IsBlockchainSynced\": true|false,    (boolean) Blockchain sync status\n" +
		"  \"lastMasternodeList\": xxxx,           (numeric) Timestamp of last MN list sync\n" +
		"  \"lastMasternodeWinner\": xxxx,         (numeric) Timestamp of last MN winner sync\n" +
		"  \"lastFailure\": xxxx,                  (numeric) Timestamp of last failure\n" +
		"  \"nCountFailures\": n,                  (numeric) Number of failed syncs\n" +
		"  \"sumMasternodeList\": n,               (numeric) Number of MN list items synced\n" +
		"  \"sumMasternodeWinner\": n,             (numeric) Number of MN winners synced\n" +
		"  \"countMasternodeList\": n,             (numeric) Count of MN list syncs\n" +
		"  \"countMasternodeWinner\": n,           (numeric) Count of MN winner syncs\n" +
		"  \"RequestedMasternodeAssets\": n,       (numeric) Current sync phase\n" +
		"  \"RequestedMasternodeAttempt\": n       (numeric) Sync attempt number\n" +
		"}\n\n" +
		"Examples:\n" +
		"> twins-cli mnsync status\n" +
		"> twins-cli mnsync reset",

	"spork": "spork \"command\" ( value )\n\n" +
		"Shows information about current state of sporks or sets a spork value.\n\n" +
		"Arguments:\n" +
		"1. \"command\"     (string, required) \"show\" to show all current spork values, \"active\" to show active sporks,\n" +
		"                   or spork name to set (requires spork private key)\n" +
		"2. value           (numeric, required for set) The new spork value\n\n" +
		"Result (for show):\n" +
		"{\n" +
		"  \"spork_name\": nnn      (key/value) Spork name and value\n" +
		"}\n\n" +
		"Examples:\n" +
		"> twins-cli spork show\n" +
		"> twins-cli spork active",

	"settxfee": "settxfee amount\n\n" +
		"Set the transaction fee per kB.\n\n" +
		"Arguments:\n" +
		"1. amount         (numeric, required) The transaction fee in TWINS/kB rounded to the nearest 0.00000001\n\n" +
		"Result:\n" +
		"true|false        (boolean) Returns true if successful\n\n" +
		"Examples:\n" +
		"> twins-cli settxfee 0.00001",

	// === Additional Wallet Commands ===
	"getaccountaddress": "getaccountaddress \"account\"\n\n" +
		"Returns the current TWINS address for receiving payments to this account.\n\n" +
		"Arguments:\n" +
		"1. \"account\"       (string, required) The account name. It can also be set to the empty string \"\" to represent the default account.\n\n" +
		"Result:\n" +
		"\"twinsaddress\"   (string) The account TWINS address\n\n" +
		"Examples:\n" +
		"> twins-cli getaccountaddress \"\"\n" +
		"> twins-cli getaccountaddress \"myaccount\"",

	"getrawchangeaddress": "getrawchangeaddress\n\n" +
		"Returns a new TWINS address for receiving change. This is for use with raw transactions, NOT normal use.\n\n" +
		"Result:\n" +
		"\"address\"    (string) The address\n\n" +
		"Examples:\n" +
		"> twins-cli getrawchangeaddress",

	"getunconfirmedbalance": "getunconfirmedbalance\n\n" +
		"Returns the server's total unconfirmed balance.\n\n" +
		"Result:\n" +
		"n            (numeric) The total unconfirmed balance in TWINS\n\n" +
		"Examples:\n" +
		"> twins-cli getunconfirmedbalance",

	"sendfrom": "sendfrom \"fromaccount\" \"totwinsaddress\" amount ( minconf \"comment\" \"comment-to\" )\n\n" +
		"Send an amount from an account to a TWINS address.\n" +
		"The amount is a real and is rounded to the nearest 0.00000001.\n\n" +
		"Arguments:\n" +
		"1. \"fromaccount\"       (string, required) The name of the account to send funds from. Default account is \"\".\n" +
		"2. \"totwinsaddress\"  (string, required) The TWINS address to send funds to.\n" +
		"3. amount                (numeric, required) The amount in TWINS (transaction fee is added on top).\n" +
		"4. minconf               (numeric, optional, default=1) Only use funds with at least this many confirmations.\n" +
		"5. \"comment\"           (string, optional) A comment used to store what the transaction is for.\n" +
		"6. \"comment-to\"       (string, optional) An optional comment to store the name of the person or organization.\n\n" +
		"Result:\n" +
		"\"transactionid\"        (string) The transaction id.\n\n" +
		"Examples:\n" +
		"> twins-cli sendfrom \"\" \"DMJRSsuU9zfyrvxVaAEFQqK4MxZg6vgeS6\" 0.01\n" +
		"> twins-cli sendfrom \"tabby\" \"DMJRSsuU9zfyrvxVaAEFQqK4MxZg6vgeS6\" 0.01 6 \"donation\" \"seans outpost\"",

	"sendmany": "sendmany \"fromaccount\" {\"address\":amount,...} ( minconf \"comment\" )\n\n" +
		"Send multiple times. Amounts are double-precision floating point numbers.\n\n" +
		"Arguments:\n" +
		"1. \"fromaccount\"         (string, required) The account to send the funds from. Should be \"\" for the default account.\n" +
		"2. \"amounts\"             (string, required) A json object with addresses and amounts\n" +
		"    {\n" +
		"      \"address\":amount   (numeric) The TWINS address is the key, the numeric amount in TWINS is the value\n" +
		"      ,...\n" +
		"    }\n" +
		"3. minconf                 (numeric, optional, default=1) Only use the balance confirmed at least this many times.\n" +
		"4. \"comment\"             (string, optional) A comment\n\n" +
		"Result:\n" +
		"\"transactionid\"          (string) The transaction id for the send.\n\n" +
		"Examples:\n" +
		"> twins-cli sendmany \"\" \"{\\\"DMJRSsuU9zfyrvxVaAEFQqK4MxZg6vgeS6\\\":0.01,\\\"DAD3Y6ivr8nPQLT1NEPX84DxGCw9jz9Jvg\\\":0.02}\"",

	"setaccount": "setaccount \"twinsaddress\" \"account\"\n\n" +
		"Sets the account associated with the given address.\n\n" +
		"Arguments:\n" +
		"1. \"twinsaddress\"  (string, required) The TWINS address to be associated with an account.\n" +
		"2. \"account\"         (string, required) The account to assign the address to.\n\n" +
		"Examples:\n" +
		"> twins-cli setaccount \"DMJRSsuU9zfyrvxVaAEFQqK4MxZg6vgeS6\" \"tabby\"",

	"getaccount": "getaccount \"twinsaddress\"\n\n" +
		"Returns the account associated with the given address.\n\n" +
		"Arguments:\n" +
		"1. \"twinsaddress\"  (string, required) The TWINS address for account lookup.\n\n" +
		"Result:\n" +
		"\"accountname\"        (string) the account address belongs to\n\n" +
		"Examples:\n" +
		"> twins-cli getaccount \"DMJRSsuU9zfyrvxVaAEFQqK4MxZg6vgeS6\"",

	"getaddressesbyaccount": "getaddressesbyaccount \"account\"\n\n" +
		"Returns the list of addresses for the given account.\n\n" +
		"Arguments:\n" +
		"1. \"account\"        (string, required) The account name.\n\n" +
		"Result:\n" +
		"[                     (json array of string)\n" +
		"  \"twinsaddress\"  (string) a TWINS address associated with the given account\n" +
		"  ,...\n" +
		"]\n\n" +
		"Examples:\n" +
		"> twins-cli getaddressesbyaccount \"tabby\"",

	"getreceivedbyaddress": "getreceivedbyaddress \"twinsaddress\" ( minconf )\n\n" +
		"Returns the total amount received by the given TWINS address in transactions with at least minconf confirmations.\n\n" +
		"Arguments:\n" +
		"1. \"twinsaddress\"  (string, required) The TWINS address for transactions.\n" +
		"2. minconf             (numeric, optional, default=1) Only include transactions confirmed at least this many times.\n\n" +
		"Result:\n" +
		"amount   (numeric) The total amount in TWINS received at this address.\n\n" +
		"Examples:\n" +
		"> twins-cli getreceivedbyaddress \"DMJRSsuU9zfyrvxVaAEFQqK4MxZg6vgeS6\"\n" +
		"> twins-cli getreceivedbyaddress \"DMJRSsuU9zfyrvxVaAEFQqK4MxZg6vgeS6\" 6",

	"getreceivedbyaccount": "getreceivedbyaccount \"account\" ( minconf )\n\n" +
		"Returns the total amount received by addresses with account in transactions with at least minconf confirmations.\n\n" +
		"Arguments:\n" +
		"1. \"account\"      (string, required) The selected account, may be the default account using \"\".\n" +
		"2. minconf          (numeric, optional, default=1) Only include transactions confirmed at least this many times.\n\n" +
		"Result:\n" +
		"amount              (numeric) The total amount in TWINS received for this account.\n\n" +
		"Examples:\n" +
		"> twins-cli getreceivedbyaccount \"\"\n" +
		"> twins-cli getreceivedbyaccount \"tabby\" 6",

	"listreceivedbyaddress": "listreceivedbyaddress ( minconf includeempty includeWatchonly )\n\n" +
		"List balances by receiving address.\n\n" +
		"Arguments:\n" +
		"1. minconf       (numeric, optional, default=1) The minimum number of confirmations before payments are included.\n" +
		"2. includeempty  (boolean, optional, default=false) Whether to include addresses that haven't received any payments.\n" +
		"3. includeWatchonly (bool, optional, default=false) Whether to include watchonly addresses.\n\n" +
		"Result:\n" +
		"[\n" +
		"  {\n" +
		"    \"involvesWatchonly\": true,   (bool) Only returned if imported addresses were involved in transaction\n" +
		"    \"address\" : \"receivingaddress\",  (string) The receiving address\n" +
		"    \"account\" : \"accountname\",  (string) The account of the receiving address.\n" +
		"    \"amount\" : x.xxx,                  (numeric) The total amount in TWINS received by the address\n" +
		"    \"confirmations\" : n                 (numeric) The number of confirmations of the most recent transaction included\n" +
		"  }\n" +
		"  ,...\n" +
		"]\n\n" +
		"Examples:\n" +
		"> twins-cli listreceivedbyaddress\n" +
		"> twins-cli listreceivedbyaddress 6 true",

	"listreceivedbyaccount": "listreceivedbyaccount ( minconf includeempty includeWatchonly )\n\n" +
		"List balances by account.\n\n" +
		"Arguments:\n" +
		"1. minconf       (numeric, optional, default=1) The minimum number of confirmations before payments are included.\n" +
		"2. includeempty  (boolean, optional, default=false) Whether to include accounts that haven't received any payments.\n" +
		"3. includeWatchonly (bool, optional, default=false) Whether to include watchonly addresses.\n\n" +
		"Result:\n" +
		"[\n" +
		"  {\n" +
		"    \"involvesWatchonly\": true,   (bool) Only returned if imported addresses were involved in transaction\n" +
		"    \"account\" : \"accountname\",  (string) The account name of the receiving account\n" +
		"    \"amount\" : x.xxx,            (numeric) The total amount received by addresses with this account\n" +
		"    \"confirmations\" : n          (numeric) The number of confirmations of the most recent transaction included\n" +
		"  }\n" +
		"  ,...\n" +
		"]\n\n" +
		"Examples:\n" +
		"> twins-cli listreceivedbyaccount\n" +
		"> twins-cli listreceivedbyaccount 6 true",

	"listtransactions": "listtransactions ( \"account\" count from includeWatchonly )\n\n" +
		"Returns up to 'count' most recent transactions skipping the first 'from' transactions for account 'account'.\n\n" +
		"Arguments:\n" +
		"1. \"account\"    (string, optional) The account name. If not included, it will list all transactions for all accounts.\n" +
		"                                     If \"\" is set, it will list transactions for the default account.\n" +
		"2. count          (numeric, optional, default=10) The number of transactions to return\n" +
		"3. from           (numeric, optional, default=0) The number of transactions to skip\n" +
		"4. includeWatchonly (bool, optional, default=false) Include transactions to watchonly addresses\n\n" +
		"Result:\n" +
		"[\n" +
		"  {\n" +
		"    \"account\":\"accountname\",       (string) The account name associated with the transaction.\n" +
		"    \"address\":\"twinsaddress\",    (string) The TWINS address of the transaction.\n" +
		"    \"category\":\"send|receive|stake\", (string) The transaction category.\n" +
		"    \"amount\": x.xxx,                 (numeric) The amount in TWINS.\n" +
		"    \"fee\": x.xxx,                    (numeric) The amount of the fee in TWINS. This is negative and only available for the 'send' category.\n" +
		"    \"confirmations\": n,              (numeric) The number of confirmations for the transaction.\n" +
		"    \"blockhash\": \"hashvalue\",     (string) The block hash containing the transaction.\n" +
		"    \"blockindex\": n,                 (numeric) The block index containing the transaction.\n" +
		"    \"txid\": \"transactionid\",      (string) The transaction id.\n" +
		"    \"time\": xxx,                     (numeric) The transaction time in seconds since epoch (midnight Jan 1 1970 GMT).\n" +
		"    \"timereceived\": xxx,             (numeric) The time received in seconds since epoch.\n" +
		"    \"comment\": \"...\",             (string) If a comment is associated with the transaction.\n" +
		"    \"to\": \"...\",                  (string) If a comment to is associated with the transaction.\n" +
		"  }\n" +
		"  ,...\n" +
		"]\n\n" +
		"Examples:\n" +
		"> twins-cli listtransactions\n" +
		"> twins-cli listtransactions \"*\" 20 100",

	"gettransaction": "gettransaction \"txid\" ( includeWatchonly )\n\n" +
		"Get detailed information about in-wallet transaction <txid>.\n\n" +
		"Arguments:\n" +
		"1. \"txid\"    (string, required) The transaction id\n" +
		"2. \"includeWatchonly\"    (bool, optional, default=false) Whether to include watchonly addresses in balance calculation and details[]\n\n" +
		"Result:\n" +
		"{\n" +
		"  \"amount\" : x.xxx,        (numeric) The transaction amount in TWINS\n" +
		"  \"confirmations\" : n,     (numeric) The number of confirmations\n" +
		"  \"blockhash\" : \"hash\",  (string) The block hash\n" +
		"  \"blockindex\" : xx,       (numeric) The block index\n" +
		"  \"blocktime\" : ttt,       (numeric) The time in seconds since epoch (1 Jan 1970 GMT)\n" +
		"  \"txid\" : \"transactionid\",   (string) The transaction id.\n" +
		"  \"time\" : ttt,            (numeric) The transaction time in seconds since epoch (1 Jan 1970 GMT)\n" +
		"  \"timereceived\" : ttt,    (numeric) The time received in seconds since epoch (1 Jan 1970 GMT)\n" +
		"  \"details\" : [\n" +
		"    {\n" +
		"      \"account\" : \"accountname\",  (string) The account name involved in the transaction\n" +
		"      \"address\" : \"twinsaddress\",   (string) The TWINS address involved in the transaction\n" +
		"      \"category\" : \"send|receive\",    (string) The category, either 'send' or 'receive'\n" +
		"      \"amount\" : x.xxx                  (numeric) The amount in TWINS\n" +
		"    }\n" +
		"    ,...\n" +
		"  ],\n" +
		"  \"hex\" : \"data\"         (string) Raw data for transaction\n" +
		"}\n\n" +
		"Examples:\n" +
		"> twins-cli gettransaction \"1075db55d416d3ca199f55b6084e2115b9345e16c5cf302fc80e9d5fbf5d48d\"",

	"listsinceblock": "listsinceblock ( \"blockhash\" target-confirmations includeWatchonly )\n\n" +
		"Get all transactions in blocks since block [blockhash], or all transactions if omitted.\n\n" +
		"Arguments:\n" +
		"1. \"blockhash\"   (string, optional) The block hash to list transactions since\n" +
		"2. target-confirmations:    (numeric, optional) The confirmations required, must be 1 or more\n" +
		"3. includeWatchonly:        (bool, optional, default=false) Include transactions to watchonly addresses\n\n" +
		"Result:\n" +
		"{\n" +
		"  \"transactions\": [\n" +
		"    \"account\":\"accountname\",       (string) The account name associated with the transaction.\n" +
		"    \"address\":\"twinsaddress\",    (string) The TWINS address of the transaction.\n" +
		"    \"category\":\"send|receive\",     (string) The transaction category.\n" +
		"    \"amount\": x.xxx,                 (numeric) The amount in TWINS.\n" +
		"    \"fee\": x.xxx,                    (numeric) The amount of the fee in TWINS.\n" +
		"    \"confirmations\": n,              (numeric) The number of confirmations for the transaction.\n" +
		"    \"blockhash\": \"hashvalue\",     (string) The block hash containing the transaction.\n" +
		"    \"blockindex\": n,                 (numeric) The block index containing the transaction.\n" +
		"    \"blocktime\": xxx,                (numeric) The block time in seconds since epoch.\n" +
		"    \"txid\": \"transactionid\",      (string) The transaction id.\n" +
		"    \"time\": xxx,                     (numeric) The transaction time.\n" +
		"    \"timereceived\": xxx,             (numeric) The time received.\n" +
		"    \"comment\": \"...\",             (string) If a comment is associated with the transaction.\n" +
		"    \"to\": \"...\"                   (string) If a comment to is associated with the transaction.\n" +
		"  ],\n" +
		"  \"lastblock\": \"lastblockhash\"     (string) The hash of the last block\n" +
		"}\n\n" +
		"Examples:\n" +
		"> twins-cli listsinceblock\n" +
		"> twins-cli listsinceblock \"000000000000000bacf66f7497b7dc45ef753ee9a7d38571037cdb1a57f663ad\" 6",

	"listaccounts": "listaccounts ( minconf includeWatchonly )\n\n" +
		"Returns Object that has account names as keys, account balances as values.\n\n" +
		"Arguments:\n" +
		"1. minconf          (numeric, optional, default=1) Only include transactions with at least this many confirmations\n" +
		"2. includeWatchonly (bool, optional, default=false) Include balances in watchonly addresses\n\n" +
		"Result:\n" +
		"{                      (json object where keys are account names, and values are numeric balances\n" +
		"  \"account\": x.xxx,  (numeric) The property name is the account name, and the value is the total balance for the account.\n" +
		"  ...\n" +
		"}\n\n" +
		"Examples:\n" +
		"> twins-cli listaccounts\n" +
		"> twins-cli listaccounts 6",

	"listaddressgroupings": "listaddressgroupings\n\n" +
		"Lists groups of addresses which have had their common ownership made public by common use as inputs or as the resulting change in past transactions.\n\n" +
		"Result:\n" +
		"[\n" +
		"  [\n" +
		"    [\n" +
		"      \"twinsaddress\",     (string) The TWINS address\n" +
		"      amount,                 (numeric) The amount in TWINS\n" +
		"      \"account\"             (string, optional) The account\n" +
		"    ]\n" +
		"    ,...\n" +
		"  ]\n" +
		"  ,...\n" +
		"]\n\n" +
		"Examples:\n" +
		"> twins-cli listaddressgroupings",

	"getaddressinfo": "getaddressinfo \"address\"\n\n" +
		"Return information about the given TWINS address. Some information requires the address to be in the wallet.\n\n" +
		"Arguments:\n" +
		"1. \"address\"                    (string, required) The TWINS address to get the information of.\n\n" +
		"Result:\n" +
		"{\n" +
		"  \"address\" : \"address\",        (string) The TWINS address validated\n" +
		"  \"scriptPubKey\" : \"hex\",       (string) The hex encoded scriptPubKey generated by the address\n" +
		"  \"ismine\" : true|false,          (boolean) If the address is yours or not\n" +
		"  \"iswatchonly\" : true|false,     (boolean) If the address is watchonly\n" +
		"  \"isscript\" : true|false,        (boolean) If the key is a script\n" +
		"  \"pubkey\" : \"publickeyhex\",    (string) The hex value of the raw public key\n" +
		"  \"iscompressed\" : true|false,    (boolean) If the address is compressed\n" +
		"  \"account\" : \"account\"         (string) The account associated with the address\n" +
		"}\n\n" +
		"Examples:\n" +
		"> twins-cli getaddressinfo \"DMJRSsuU9zfyrvxVaAEFQqK4MxZg6vgeS6\"",

	"importaddress": "importaddress \"address\" ( \"label\" rescan )\n\n" +
		"Adds an address or script (in hex) that can be watched as if it were in your wallet but cannot be used to spend.\n\n" +
		"Arguments:\n" +
		"1. \"address\"          (string, required) The address\n" +
		"2. \"label\"            (string, optional, default=\"\") An optional label\n" +
		"3. rescan               (boolean, optional, default=true) Rescan the wallet for transactions\n\n" +
		"Note: This call can take a long time to complete if rescan is true.\n\n" +
		"Examples:\n" +
		"> twins-cli importaddress \"myaddress\"\n" +
		"> twins-cli importaddress \"myaddress\" \"testing\" false",

	"dumpwallet": "dumpwallet \"filename\"\n\n" +
		"Dumps all wallet keys in a human-readable format.\n\n" +
		"Arguments:\n" +
		"1. \"filename\"    (string, required) The filename\n\n" +
		"Examples:\n" +
		"> twins-cli dumpwallet \"test\"",

	"importwallet": "importwallet \"filename\"\n\n" +
		"Imports keys from a wallet dump file (see dumpwallet).\n\n" +
		"Arguments:\n" +
		"1. \"filename\"    (string, required) The wallet file\n\n" +
		"Examples:\n" +
		"> twins-cli importwallet \"test\"",

	"dumphdinfo": "dumphdinfo\n\n" +
		"Returns an object containing sensitive HD wallet information.\n\n" +
		"Result:\n" +
		"{\n" +
		"  \"hdseed\" : \"seed\",    (string) The HD seed (sensitive data!)\n" +
		"  \"mnemonic\" : \"words\", (string) The mnemonic for the HD seed (sensitive data!)\n" +
		"  \"mnemonicpassphrase\" : \"phrase\" (string) The mnemonic passphrase (sensitive data!)\n" +
		"}\n\n" +
		"Examples:\n" +
		"> twins-cli dumphdinfo",

	"walletpassphrasechange": "walletpassphrasechange \"oldpassphrase\" \"newpassphrase\"\n\n" +
		"Changes the wallet passphrase from 'oldpassphrase' to 'newpassphrase'.\n\n" +
		"Arguments:\n" +
		"1. \"oldpassphrase\"      (string) The current passphrase\n" +
		"2. \"newpassphrase\"      (string) The new passphrase\n\n" +
		"Examples:\n" +
		"> twins-cli walletpassphrasechange \"old one\" \"new one\"",

	"listaddresses": "listaddresses\n\n" +
		"Returns all addresses in the wallet with their labels.\n\n" +
		"Result:\n" +
		"[\n" +
		"  {\n" +
		"    \"address\": \"addr\",  (string) The TWINS address\n" +
		"    \"label\": \"label\",   (string) The label/account associated with the address\n" +
		"    \"ismine\": true|false, (boolean) If the key is yours\n" +
		"  }\n" +
		"  ,...\n" +
		"]\n\n" +
		"Examples:\n" +
		"> twins-cli listaddresses",

	"signmessage": "signmessage \"twinsaddress\" \"message\"\n\n" +
		"Sign a message with the private key of an address.\n\n" +
		"Arguments:\n" +
		"1. \"twinsaddress\"  (string, required) The TWINS address to use for the private key.\n" +
		"2. \"message\"         (string, required) The message to create a signature of.\n\n" +
		"Result:\n" +
		"\"signature\"          (string) The signature of the message encoded in base 64\n\n" +
		"Requires wallet passphrase to be set with walletpassphrase call.\n\n" +
		"Examples:\n" +
		"> twins-cli signmessage \"DMJRSsuU9zfyrvxVaAEFQqK4MxZg6vgeS6\" \"my message\"",

	"verifymessage": "verifymessage \"twinsaddress\" \"signature\" \"message\"\n\n" +
		"Verify a signed message.\n\n" +
		"Arguments:\n" +
		"1. \"twinsaddress\"  (string, required) The TWINS address to use for the signature.\n" +
		"2. \"signature\"       (string, required) The signature provided by the signer in base 64 encoding.\n" +
		"3. \"message\"         (string, required) The message that was signed.\n\n" +
		"Result:\n" +
		"true|false   (boolean) If the signature is verified or not.\n\n" +
		"Examples:\n" +
		"> twins-cli verifymessage \"DMJRSsuU9zfyrvxVaAEFQqK4MxZg6vgeS6\" \"signature\" \"my message\"",

	"getwalletinfo": "getwalletinfo\n\n" +
		"Returns an object containing various wallet state info.\n\n" +
		"Result:\n" +
		"{\n" +
		"  \"walletversion\": xxxxx,     (numeric) the wallet version\n" +
		"  \"balance\": xxxxxxx,         (numeric) the total confirmed TWINS balance of the wallet\n" +
		"  \"unconfirmed_balance\": xxx, (numeric) the total unconfirmed balance of the wallet in TWINS\n" +
		"  \"immature_balance\": xxxxxx, (numeric) the total immature balance of the wallet in TWINS\n" +
		"  \"txcount\": xxxxxxx,         (numeric) the total number of transactions in the wallet\n" +
		"  \"keypoololdest\": xxxxxx,    (numeric) the timestamp (seconds since GMT epoch) of the oldest pre-generated key in the key pool\n" +
		"  \"keypoolsize\": xxxx,        (numeric) how many new keys are pre-generated\n" +
		"  \"unlocked_until\": ttt,      (numeric) the timestamp in seconds since epoch (midnight Jan 1 1970 GMT) that the wallet is unlocked for transfers, or 0 if the wallet is locked\n" +
		"  \"paytxfee\": x.xxxx,         (numeric) the transaction fee configuration, set in TWINS/kB\n" +
		"}\n\n" +
		"Examples:\n" +
		"> twins-cli getwalletinfo",

	"backupwallet": "backupwallet \"destination\"\n\n" +
		"Safely copies wallet.dat to destination, which can be a directory or a path with filename.\n\n" +
		"Arguments:\n" +
		"1. \"destination\"   (string) The destination directory or file\n\n" +
		"Examples:\n" +
		"> twins-cli backupwallet \"backup.dat\"",

	"keypoolrefill": "keypoolrefill ( newsize )\n\n" +
		"Fills the keypool. Requires wallet passphrase to be set with walletpassphrase call.\n\n" +
		"Arguments:\n" +
		"1. newsize     (numeric, optional, default=100) The new keypool size\n\n" +
		"Examples:\n" +
		"> twins-cli keypoolrefill\n" +
		"> twins-cli keypoolrefill 100",

	"reservebalance": "reservebalance ( reserve amount )\n\n" +
		"Show or set the reserve amount not participating in network protection.\n" +
		"If no parameters provided, current setting is printed.\n\n" +
		"Arguments:\n" +
		"1. reserve       (boolean, optional) is true or false to turn balance reserve on or off.\n" +
		"2. amount        (numeric, optional) is a real and rounded to cent.\n\n" +
		"Result:\n" +
		"{\n" +
		"  \"reserve\": true|false, (boolean) Status of the reserve balance\n" +
		"  \"amount\": x.xxxx       (numeric) Amount reserved\n" +
		"}\n\n" +
		"Examples:\n" +
		"> twins-cli reservebalance true 25",

	"setstakesplitthreshold": "setstakesplitthreshold value\n\n" +
		"This will set the output size of your stakes to never be below this number.\n\n" +
		"Arguments:\n" +
		"1. value   (numeric, required) Threshold value between 1 and 999999\n\n" +
		"Result:\n" +
		"{\n" +
		"  \"threshold\": n,       (numeric) Threshold value set\n" +
		"  \"saved\": true|false   (boolean) If threshold was saved\n" +
		"}\n\n" +
		"Examples:\n" +
		"> twins-cli setstakesplitthreshold 5000",

	"getstakesplitthreshold": "getstakesplitthreshold\n\n" +
		"Returns the threshold for stake splitting.\n\n" +
		"Result:\n" +
		"n      (numeric) Threshold value\n\n" +
		"Examples:\n" +
		"> twins-cli getstakesplitthreshold",

	"setautocombine": "setautocombine true|false ( target_amount )\n\n" +
		"Configure automatic UTXO consolidation. When enabled, the wallet periodically\n" +
		"combines small UTXOs below the target amount into larger ones.\n\n" +
		"Note: Consolidation links UTXOs on the same address on-chain (privacy consideration).\n\n" +
		"Arguments:\n" +
		"1. true|false      (boolean, required) Enable/disable auto-combine.\n" +
		"2. target_amount    (numeric, optional) Target amount in TWINS (required when enabling)\n\n" +
		"Result:\n" +
		"{\n" +
		"  \"enabled\": true|false,\n" +
		"  \"target\": n,\n" +
		"  \"cooldown\": n\n" +
		"}\n\n" +
		"Examples:\n" +
		"> twins-cli setautocombine true 200000\n" +
		"> twins-cli setautocombine false",

	"getautocombine": "getautocombine\n\n" +
		"Returns the current auto-combine configuration.\n\n" +
		"Result:\n" +
		"{\n" +
		"  \"enabled\": true|false,\n" +
		"  \"target\": n,        (numeric) Target amount in TWINS\n" +
		"  \"cooldown\": n       (numeric) Cooldown between cycles in seconds\n" +
		"}\n\n" +
		"Examples:\n" +
		"> twins-cli getautocombine",

	"multisend": "multisend \"command\" ( \"parameter\" )\n\n" +
		"MultiSend allows the automatic sending of a percentage of your stake reward to given addresses.\n\n" +
		"Arguments:\n" +
		"1. \"command\"      (string, required) The command to execute\n\n" +
		"Available commands:\n" +
		"  print       - Displays the current MultiSend vector\n" +
		"  clear       - Deletes the current MultiSend vector\n" +
		"  enablestake - Activates the current MultiSend vector to be activated on stake rewards\n" +
		"  disablestake - Disables the current MultiSend vector\n" +
		"  delete <Address #> - Deletes specific entry from MultiSend vector (0-indexed)\n" +
		"  <address> <percent> - Adds an entry to the MultiSend vector\n\n" +
		"Examples:\n" +
		"> twins-cli multisend print\n" +
		"> twins-cli multisend \"DMJRSsuU9zfyrvxVaAEFQqK4MxZg6vgeS6\" 50",

	"addmultisigaddress": "addmultisigaddress nrequired [\"key\",...] ( \"account\" )\n\n" +
		"Add a nrequired-to-sign multisignature address to the wallet.\n" +
		"Each key is a TWINS address or hex-encoded public key.\n\n" +
		"Arguments:\n" +
		"1. nrequired        (numeric, required) The number of required signatures out of the n keys or addresses.\n" +
		"2. \"keysobject\"   (string, required) A json array of TWINS addresses or hex-encoded public keys\n" +
		"     [\n" +
		"       \"address\"  (string) TWINS address or hex-encoded public key\n" +
		"       ...,\n" +
		"     ]\n" +
		"3. \"account\"      (string, optional) An account to assign the addresses to.\n\n" +
		"Result:\n" +
		"\"twinsaddress\"  (string) A TWINS address associated with the keys.\n\n" +
		"Examples:\n" +
		"> twins-cli addmultisigaddress 2 \"[\\\"DMJRSsuU9zfyrvxVaAEFQqK4MxZg6vgeS6\\\",\\\"DAD3Y6ivr8nPQLT1NEPX84DxGCw9jz9Jvg\\\"]\"",

	"createmultisig": "createmultisig nrequired [\"key\",...]\n\n" +
		"Creates a multi-signature address with n signature of m keys required.\n\n" +
		"Arguments:\n" +
		"1. nrequired      (numeric, required) The number of required signatures out of the n keys or addresses.\n" +
		"2. \"keys\"       (string, required) A json array of keys which are TWINS addresses or hex-encoded public keys\n" +
		"     [\n" +
		"       \"key\"    (string) TWINS address or hex-encoded public key\n" +
		"       ,...\n" +
		"     ]\n\n" +
		"Result:\n" +
		"{\n" +
		"  \"address\":\"multisigaddress\",  (string) The value of the new multisig address.\n" +
		"  \"redeemScript\":\"script\"       (string) The string value of the hex-encoded redemption script.\n" +
		"}\n\n" +
		"Examples:\n" +
		"> twins-cli createmultisig 2 \"[\\\"DMJRSsuU9zfyrvxVaAEFQqK4MxZg6vgeS6\\\",\\\"DAD3Y6ivr8nPQLT1NEPX84DxGCw9jz9Jvg\\\"]\"",

	// === Additional Transaction Commands ===
	"gettxout": "gettxout \"txid\" n ( includemempool )\n\n" +
		"Returns details about an unspent transaction output.\n\n" +
		"Arguments:\n" +
		"1. \"txid\"       (string, required) The transaction id\n" +
		"2. n              (numeric, required) vout value\n" +
		"3. includemempool  (boolean, optional) Whether to include the mempool\n\n" +
		"Result:\n" +
		"{\n" +
		"  \"bestblock\" : \"hash\",    (string) the block hash\n" +
		"  \"confirmations\" : n,       (numeric) The number of confirmations\n" +
		"  \"value\" : x.xxx,           (numeric) The transaction value in TWINS\n" +
		"  \"scriptPubKey\" : {         (json object)\n" +
		"     \"asm\" : \"code\",       (string)\n" +
		"     \"hex\" : \"hex\",        (string)\n" +
		"     \"reqSigs\" : n,          (numeric) Number of required signatures\n" +
		"     \"type\" : \"pubkeyhash\", (string) The type, eg pubkeyhash\n" +
		"     \"addresses\" : [          (array of string) array of TWINS addresses\n" +
		"        \"twinsaddress\"     (string)\n" +
		"     ]\n" +
		"  },\n" +
		"  \"version\" : n,            (numeric) The version\n" +
		"  \"coinbase\" : true|false   (boolean) Coinbase or not\n" +
		"}\n\n" +
		"Examples:\n" +
		"> twins-cli gettxout \"txid\" 1",

	// === Additional Network Commands ===
	"ping": "ping\n\n" +
		"Requests that a ping be sent to all other nodes, to measure ping time.\n" +
		"Results provided in getpeerinfo, pingtime and pingwait fields are decimal seconds.\n" +
		"Ping command is handled in queue with all other commands, so it measures processing backlog, not just network ping.\n\n" +
		"Examples:\n" +
		"> twins-cli ping",

	"getaddednodeinfo": "getaddednodeinfo dns ( \"node\" )\n\n" +
		"Returns information about the given added node, or all added nodes.\n\n" +
		"Arguments:\n" +
		"1. dns        (boolean, required) If false, only a list of added nodes will be provided, otherwise connected information will also be available.\n" +
		"2. \"node\"   (string, optional) If provided, return information about this specific node, otherwise all nodes are returned.\n\n" +
		"Result:\n" +
		"[\n" +
		"  {\n" +
		"    \"addednode\" : \"192.168.0.201\",   (string) The node ip address\n" +
		"    \"connected\" : true|false,          (boolean) If connected\n" +
		"    \"addresses\" : [\n" +
		"       {\n" +
		"         \"address\" : \"192.168.0.201:37817\",  (string) The TWINS server host and port\n" +
		"         \"connected\" : \"outbound\"           (string) connection, inbound or outbound\n" +
		"       }\n" +
		"       ,...\n" +
		"    ]\n" +
		"  }\n" +
		"  ,...\n" +
		"]\n\n" +
		"Examples:\n" +
		"> twins-cli getaddednodeinfo true\n" +
		"> twins-cli getaddednodeinfo true \"192.168.0.201\"",

	"getnettotals": "getnettotals\n\n" +
		"Returns information about network traffic, including bytes in, bytes out, and current time.\n\n" +
		"Result:\n" +
		"{\n" +
		"  \"totalbytesrecv\": n,   (numeric) Total bytes received\n" +
		"  \"totalbytessent\": n,   (numeric) Total bytes sent\n" +
		"  \"timemillis\": t        (numeric) Total cpu time\n" +
		"}\n\n" +
		"Examples:\n" +
		"> twins-cli getnettotals",

	"setban": "setban \"ip(/netmask)\" \"add|remove\" (bantime) (absolute)\n\n" +
		"Attempts to add or remove an IP/Subnet from the banned list.\n\n" +
		"Arguments:\n" +
		"1. \"ip(/netmask)\" (string, required) The IP/Subnet (see getpeerinfo for nodes ip) with an optional netmask (default is /32 = single ip)\n" +
		"2. \"command\"      (string, required) 'add' to add an IP/Subnet to the list, 'remove' to remove an IP/Subnet from the list\n" +
		"3. \"bantime\"      (numeric, optional) time in seconds how long (or until when if [absolute] is set) the ip is banned (0 or empty means using the default time of 24h which can also be overwritten by the -bantime startup argument)\n" +
		"4. \"absolute\"     (boolean, optional) If set, the bantime must be an absolute timestamp in seconds since epoch (Jan 1 1970 GMT)\n\n" +
		"Examples:\n" +
		"> twins-cli setban \"192.168.0.6\" \"add\" 86400\n" +
		"> twins-cli setban \"192.168.0.0/24\" \"add\"",

	"listbanned": "listbanned\n\n" +
		"List all banned IPs/Subnets.\n\n" +
		"Result:\n" +
		"[\n" +
		"  {\n" +
		"    \"address\": \"xxxx\",      (string) The banned address\n" +
		"    \"banned_until\": ttt,      (numeric) The timestamp when the ban expires\n" +
		"    \"ban_created\": ttt,       (numeric) The timestamp when the ban was created\n" +
		"    \"ban_reason\": \"xxxx\"    (string) The ban reason\n" +
		"  }\n" +
		"  ,...\n" +
		"]\n\n" +
		"Examples:\n" +
		"> twins-cli listbanned",

	"clearbanned": "clearbanned\n\n" +
		"Clear all banned IPs.\n\n" +
		"Examples:\n" +
		"> twins-cli clearbanned",

	"setnetworkactive": "setnetworkactive true|false\n\n" +
		"Disable/enable all P2P network activity.\n\n" +
		"Arguments:\n" +
		"1. \"state\"        (boolean, required) true to enable networking, false to disable\n\n" +
		"Examples:\n" +
		"> twins-cli setnetworkactive true\n" +
		"> twins-cli setnetworkactive false",

	"getsyncstatus": "getsyncstatus\n\n" +
		"Returns the sync status of the node.\n\n" +
		"Result:\n" +
		"{\n" +
		"  \"IsBlockchainSynced\": true|false, (boolean) Whether blockchain sync is complete\n" +
		"  \"lastMasternodeList\": ttt,        (numeric) Timestamp of last masternode list sync\n" +
		"  \"lastMasternodeWinner\": ttt,      (numeric) Timestamp of last masternode winner sync\n" +
		"  \"lastFailure\": ttt,               (numeric) Timestamp of last sync failure\n" +
		"  \"nCountFailures\": n,              (numeric) Number of sync failures\n" +
		"  \"sumMasternodeList\": n,           (numeric) Number of masternode list entries synced\n" +
		"  \"sumMasternodeWinner\": n,         (numeric) Number of masternode winner entries synced\n" +
		"  \"countMasternodeList\": n,         (numeric) Masternode list sync count\n" +
		"  \"countMasternodeWinner\": n        (numeric) Masternode winner sync count\n" +
		"}\n\n" +
		"Examples:\n" +
		"> twins-cli getsyncstatus",

	// === Additional Blockchain Commands ===
	"getchaintips": "getchaintips\n\n" +
		"Return information about all known tips in the block tree, including the main chain as well as orphaned branches.\n\n" +
		"Result:\n" +
		"[\n" +
		"  {\n" +
		"    \"height\": xxxx,         (numeric) height of the chain tip\n" +
		"    \"hash\": \"xxxx\",       (string) block hash of the tip\n" +
		"    \"branchlen\": 0          (numeric) zero for main chain\n" +
		"    \"status\": \"active\"    (string) \"active\" for the main chain\n" +
		"  },\n" +
		"  {\n" +
		"    \"height\": xxxx,\n" +
		"    \"hash\": \"xxxx\",\n" +
		"    \"branchlen\": 1          (numeric) length of branch connecting the tip to the main chain\n" +
		"    \"status\": \"xxxx\"      (string) status of the chain (active, valid-fork, valid-headers, headers-only, invalid)\n" +
		"  }\n" +
		"]\n\n" +
		"Examples:\n" +
		"> twins-cli getchaintips",

	"gettxoutsetinfo": "gettxoutsetinfo\n\n" +
		"Returns statistics about the unspent transaction output set.\n" +
		"Note this call may take some time.\n\n" +
		"Result:\n" +
		"{\n" +
		"  \"height\":n,     (numeric) The current block height (index)\n" +
		"  \"bestblock\": \"hex\",   (string) the best block hash hex\n" +
		"  \"transactions\": n,      (numeric) The number of transactions\n" +
		"  \"txouts\": n,            (numeric) The number of output transactions\n" +
		"  \"bytes_serialized\": n,  (numeric) The serialized size\n" +
		"  \"hash_serialized\": \"hash\",   (string) The serialized hash\n" +
		"  \"total_amount\": x.xxx          (numeric) The total amount\n" +
		"}\n\n" +
		"Examples:\n" +
		"> twins-cli gettxoutsetinfo",

	"verifychain": "verifychain ( checklevel numblocks )\n\n" +
		"Verifies blockchain database.\n\n" +
		"Arguments:\n" +
		"1. checklevel   (numeric, optional, 0-4, default=3) How thorough the block verification is.\n" +
		"2. numblocks    (numeric, optional, default=288, 0=all) The number of blocks to check.\n\n" +
		"Result:\n" +
		"true|false       (boolean) Verified or not\n\n" +
		"Examples:\n" +
		"> twins-cli verifychain",

	"invalidateblock": "invalidateblock \"hash\"\n\n" +
		"Permanently marks a block as invalid, as if it violated a consensus rule.\n\n" +
		"Arguments:\n" +
		"1. hash   (string, required) the hash of the block to mark as invalid\n\n" +
		"Result:\n" +
		"None\n\n" +
		"Examples:\n" +
		"> twins-cli invalidateblock \"blockhash\"",

	"reconsiderblock": "reconsiderblock \"hash\"\n\n" +
		"Removes invalidity status of a block and its descendants, reconsider them for activation.\n" +
		"This can be used to undo the effects of invalidateblock.\n\n" +
		"Arguments:\n" +
		"1. hash   (string, required) the hash of the block to reconsider\n\n" +
		"Result:\n" +
		"None\n\n" +
		"Examples:\n" +
		"> twins-cli reconsiderblock \"blockhash\"",

	"addcheckpoint": "addcheckpoint height hash\n\n" +
		"Adds a manual checkpoint to the chain.\n\n" +
		"Arguments:\n" +
		"1. height  (numeric, required) The block height\n" +
		"2. hash    (string, required) The block hash at the given height\n\n" +
		"Result:\n" +
		"true|false  (boolean) Whether the checkpoint was added successfully\n\n" +
		"Examples:\n" +
		"> twins-cli addcheckpoint 100000 \"0000000000000a1b2c3d4e5f...\"",

	"getfeeinfo": "getfeeinfo blocks\n\n" +
		"Returns the fee information for the last n blocks.\n\n" +
		"Arguments:\n" +
		"1. blocks     (numeric, required) the number of blocks to get fee info for\n\n" +
		"Result:\n" +
		"{\n" +
		"  \"txcount\": xxxxx,         (numeric) Number of transactions\n" +
		"  \"txbytes\": xxxxx,         (numeric) Total size of transactions\n" +
		"  \"ttlfee\": xxxxx,          (numeric) Sum of all fees in TWINS\n" +
		"  \"feeperkb\": xxxxx,        (numeric) Average fee per kilobyte\n" +
		"  \"rec_highpriorityfee_perkb\": xxxxx  (numeric) Recommended fee for high priority per kB\n" +
		"}\n\n" +
		"Examples:\n" +
		"> twins-cli getfeeinfo 5",

	"getrewardrates": "getrewardrates ( height )\n\n" +
		"Returns the current reward distribution rates for PoS and masternodes at the given height.\n\n" +
		"Arguments:\n" +
		"1. height     (numeric, optional) Block height for reward calculation. Default is current chain tip.\n\n" +
		"Result:\n" +
		"{\n" +
		"  \"height\": n,             (numeric) Block height\n" +
		"  \"blockreward\": x.xxx,   (numeric) Total block reward in TWINS\n" +
		"  \"stakereward\": x.xxx,   (numeric) PoS staker reward in TWINS\n" +
		"  \"mnreward\": x.xxx,      (numeric) Masternode reward in TWINS\n" +
		"  \"stakepercentage\": n,   (numeric) PoS reward percentage\n" +
		"  \"mnpercentage\": n       (numeric) Masternode reward percentage\n" +
		"}\n\n" +
		"Examples:\n" +
		"> twins-cli getrewardrates\n" +
		"> twins-cli getrewardrates 1000000",

	// === Mining/Staking Commands ===
	"getnetworkhashps": "getnetworkhashps ( blocks height )\n\n" +
		"Returns the estimated network hashes per second based on the last n blocks.\n\n" +
		"Arguments:\n" +
		"1. blocks     (numeric, optional, default=120) The number of blocks, or -1 for blocks since last difficulty change.\n" +
		"2. height     (numeric, optional, default=-1) To estimate at the time of the given height.\n\n" +
		"Result:\n" +
		"x             (numeric) Hashes per second estimated\n\n" +
		"Examples:\n" +
		"> twins-cli getnetworkhashps",

	"getmininginfo": "getmininginfo\n\n" +
		"Returns a json object containing mining-related information.\n\n" +
		"Result:\n" +
		"{\n" +
		"  \"blocks\": nnn,             (numeric) The current block\n" +
		"  \"currentblocksize\": nnn,   (numeric) The last block size\n" +
		"  \"currentblocktx\": nnn,     (numeric) The last block transaction\n" +
		"  \"difficulty\": xxx.xxxxx    (numeric) The current difficulty\n" +
		"  \"errors\": \"...\"          (string) Current errors\n" +
		"  \"generate\": true|false     (boolean) If the generation is on or off (see getgenerate or setgenerate calls)\n" +
		"  \"genproclimit\": n          (numeric) The processor limit for generation. -1 if no generation.\n" +
		"  \"hashespersec\": n          (numeric) The hashes per second of the generation, or 0 if no generation.\n" +
		"  \"pooledtx\": n              (numeric) The size of the mem pool\n" +
		"  \"testnet\": true|false      (boolean) If using testnet or not\n" +
		"  \"chain\": \"xxxx\"          (string) current network name (main, test, regtest)\n" +
		"  \"staking status\": true|false (boolean) If the wallet is staking or not\n" +
		"}\n\n" +
		"Examples:\n" +
		"> twins-cli getmininginfo",

	"getstakinginfo": "getstakinginfo\n\n" +
		"Alias for getstakingstatus. Returns an object containing various staking information.\n\n" +
		"See getstakingstatus for full details.",

	"getstakingstatus": "getstakingstatus\n\n" +
		"Returns an object containing various staking information.\n\n" +
		"Result:\n" +
		"{\n" +
		"  \"validtime\": true|false,          (boolean) if the chain tip is within staking phases\n" +
		"  \"haveconnections\": true|false,    (boolean) if network connections are present\n" +
		"  \"walletunlocked\": true|false,     (boolean) if the wallet is unlocked\n" +
		"  \"mintablecoins\": true|false,      (boolean) if the wallet has mintable coins\n" +
		"  \"enoughcoins\": true|false,        (boolean) if available coins are greater than reserve balance\n" +
		"  \"mnsync\": true|false,             (boolean) if masternode data is synced\n" +
		"  \"staking status\": true|false,     (boolean) if the wallet is staking or not\n" +
		"}\n\n" +
		"Examples:\n" +
		"> twins-cli getstakingstatus",

	"submitblock": "submitblock \"hexdata\" ( \"jsonparametersobject\" )\n\n" +
		"Attempts to submit new block to network.\n\n" +
		"Arguments:\n" +
		"1. \"hexdata\"    (string, required) the hex-encoded block data to submit\n" +
		"2. \"jsonparametersobject\"     (string, optional) object of optional parameters\n" +
		"     {\n" +
		"       \"workid\" : \"id\"    (string, optional) if the server provided a workid, it MUST be included with submissions\n" +
		"     }\n\n" +
		"Result:\n" +
		"None (returns error if block was not accepted)\n\n" +
		"Examples:\n" +
		"> twins-cli submitblock \"mydata\"",

	"getblocktemplate": "getblocktemplate ( \"jsonrequestobject\" )\n\n" +
		"Returns data needed to construct a block to work on.\n\n" +
		"Arguments:\n" +
		"1. \"jsonrequestobject\"       (string, optional) A json object in the following spec\n" +
		"     {\n" +
		"       \"mode\":\"template\"    (string, optional) This must be set to \"template\" or omitted\n" +
		"       \"capabilities\":[       (array, optional) A list of strings\n" +
		"           \"support\"          (string) client side supported feature, 'longpoll', 'coinbasetxn', 'coinbasevalue', 'proposal', 'serverlist', 'workid'\n" +
		"           ,...\n" +
		"       ]\n" +
		"     }\n\n" +
		"Result:\n" +
		"{\n" +
		"  \"version\" : n,                    (numeric) The block version\n" +
		"  \"previousblockhash\" : \"xxxx\",    (string) The hash of current highest block\n" +
		"  \"transactions\" : [                (array) contents of non-coinbase transactions that should be included in the next block\n" +
		"      {\n" +
		"         \"data\" : \"xxxx\",          (string) transaction data encoded in hexadecimal (byte-for-byte)\n" +
		"         \"hash\" : \"xxxx\",          (string) hash/id encoded in little-endian hexadecimal\n" +
		"         \"depends\" : [              (array) array of numbers\n" +
		"             n                        (numeric) transactions before this one (by 1-based index in 'transactions' list) that must be present in the final block if this one is\n" +
		"             ,...\n" +
		"         ],\n" +
		"         \"fee\": n,                   (numeric) difference in value between transaction inputs and outputs (in satoshis)\n" +
		"         \"sigops\" : n,               (numeric) total number of SigOps\n" +
		"         \"required\" : true|false     (boolean) if provided and true, this transaction must be in the final block\n" +
		"      }\n" +
		"      ,...\n" +
		"  ],\n" +
		"  \"coinbaseaux\" : {                 (json object) data that should be included in the coinbase's scriptSig content\n" +
		"      \"flags\" : \"flags\"            (string)\n" +
		"  },\n" +
		"  \"coinbasevalue\" : n,               (numeric) maximum allowable input to coinbase transaction, including the generation award and transaction fees (in satoshis)\n" +
		"  \"target\" : \"xxxx\",               (string) The hash target\n" +
		"  \"mintime\" : xxx,                   (numeric) The minimum timestamp appropriate for next block time in seconds since epoch\n" +
		"  \"mutable\" : [                      (array of string) list of ways the block template may be changed\n" +
		"     \"value\"                          (string) A way the block template may be changed, e.g. 'time', 'transactions', 'prevblock'\n" +
		"     ,...\n" +
		"  ],\n" +
		"  \"noncerange\" : \"00000000ffffffff\",   (string) A range of valid nonces\n" +
		"  \"sigoplimit\" : n,                 (numeric) limit of sigops in blocks\n" +
		"  \"sizelimit\" : n,                  (numeric) limit of block size\n" +
		"  \"curtime\" : ttt,                  (numeric) current timestamp in seconds since epoch\n" +
		"  \"bits\" : \"xxx\",                  (string) compressed target of next block\n" +
		"  \"height\" : n                      (numeric) The height of the next block\n" +
		"}\n\n" +
		"Examples:\n" +
		"> twins-cli getblocktemplate",

	"prioritisetransaction": "prioritisetransaction \"txid\" priority_delta fee_delta\n\n" +
		"Accepts the transaction into mined blocks at a higher (or lower) priority.\n\n" +
		"Arguments:\n" +
		"1. \"txid\"       (string, required) The transaction id.\n" +
		"2. priority_delta (numeric, required) The priority to add or subtract.\n" +
		"3. fee_delta      (numeric, required) The fee value (in satoshis) to add (or subtract, if negative).\n\n" +
		"Result:\n" +
		"true              (boolean) Returns true\n\n" +
		"Examples:\n" +
		"> twins-cli prioritisetransaction \"txid\" 0.0 10000",

	"estimatefee": "estimatefee nblocks\n\n" +
		"Estimates the approximate fee per kilobyte needed for a transaction to begin confirmation within nblocks blocks.\n\n" +
		"Arguments:\n" +
		"1. nblocks     (numeric) The number of blocks for target confirmation\n\n" +
		"Result:\n" +
		"n :    (numeric) estimated fee-per-kilobyte\n" +
		"-1.0 is returned if not enough transactions and blocks have been observed to make an estimate.\n\n" +
		"Examples:\n" +
		"> twins-cli estimatefee 6",

	"estimatepriority": "estimatepriority nblocks\n\n" +
		"Estimates the approximate priority a zero-fee transaction needs to begin confirmation within nblocks blocks.\n\n" +
		"Arguments:\n" +
		"1. nblocks     (numeric) The number of blocks for target confirmation\n\n" +
		"Result:\n" +
		"n :    (numeric) estimated priority\n" +
		"-1.0 is returned if not enough transactions and blocks have been observed to make an estimate.\n\n" +
		"Examples:\n" +
		"> twins-cli estimatepriority 6",

	"setgenerate": "setgenerate generate ( genproclimit )\n\n" +
		"Set 'generate' true or false to turn generation on or off.\n" +
		"Generation is limited to 'genproclimit' processors, -1 is unlimited.\n\n" +
		"Arguments:\n" +
		"1. generate         (boolean, required) Set to true to turn on generation, false to turn off.\n" +
		"2. genproclimit     (numeric, optional) Set the processor limit for when generation is on. Can be -1 for unlimited.\n\n" +
		"Examples:\n" +
		"> twins-cli setgenerate true 1",

	"getgenerate": "getgenerate\n\n" +
		"Return if the server is set to generate coins or not. The default is false.\n" +
		"It is set with the command line argument -gen (or twins.conf setting gen).\n" +
		"It can also be set with the setgenerate call.\n\n" +
		"Result:\n" +
		"true|false      (boolean) If the server is set to generate coins or not\n\n" +
		"Examples:\n" +
		"> twins-cli getgenerate",

	"gethashespersec": "gethashespersec\n\n" +
		"Returns a recent hashes per second performance measurement while generating.\n\n" +
		"Result:\n" +
		"n           (numeric) The recent hashes per second when generation is on (will return 0 if generation is off)\n\n" +
		"Examples:\n" +
		"> twins-cli gethashespersec",
}

// GetCommandHelp returns detailed help text for a specific RPC command.
// This is a package-level function callable without a Server instance.
func GetCommandHelp(command string) string {
	if help, exists := commandHelpTexts[command]; exists {
		return help
	}
	return command + "\n\n(No detailed help available)"
}

// GetCommandBriefDescriptions returns a map of command name to brief description
// extracted from the first description line of each help text.
func GetCommandBriefDescriptions() map[string]string {
	descriptions := make(map[string]string, len(commandHelpTexts))
	for cmd, help := range commandHelpTexts {
		// Help format: "command (args)\n\nDescription.\n\n..."
		parts := strings.SplitN(help, "\n\n", 3)
		if len(parts) >= 2 {
			// Take first line of the description paragraph
			desc := strings.SplitN(parts[1], "\n", 2)[0]
			// Remove trailing period for consistency
			desc = strings.TrimSuffix(desc, ".")
			descriptions[cmd] = desc
		}
	}
	return descriptions
}

// getCommandHelp is the Server method that delegates to the package function.
func (s *Server) getCommandHelp(command string) string {
	return GetCommandHelp(command)
}

// handleStop handles the stop RPC command to gracefully shutdown the daemon
func (s *Server) handleStop(req *Request) *Response {
	s.logger.Info("Received stop command via RPC")

	// Schedule shutdown after response is sent
	go func() {
		// Calculate delay based on WriteTimeout to ensure response is sent
		// Use 10% of WriteTimeout or minimum 100ms, whichever is larger
		delay := s.config.WriteTimeout / 10
		minDelay := 100 * time.Millisecond
		if delay < minDelay {
			delay = minDelay
		}

		// Give time for the response to be sent
		time.Sleep(delay)

		// Trigger shutdown if callback is set
		s.mu.RLock()
		shutdownFn := s.shutdownFunc
		s.mu.RUnlock()

		if shutdownFn != nil {
			s.logger.Info("Initiating daemon shutdown")
			shutdownFn()
		} else {
			s.logger.Warn("No shutdown function registered, attempting server stop")
			// Try to stop the RPC server itself
			s.Stop()
		}
	}()

	// Return success message
	return &Response{
		JSONRPC: "2.0",
		Result:  "TWINS server stopping",
		ID:      req.ID,
	}
}

// handleSetLogLevel changes the global log level at runtime
func (s *Server) handleSetLogLevel(req *Request) *Response {
	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 1 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(CodeInvalidParams, "Missing required parameter: level (trace, debug, info, warn, error, fatal)", nil),
			ID:      req.ID,
		}
	}

	levelStr, ok := params[0].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(CodeInvalidParams, "Level must be a string", nil),
			ID:      req.ID,
		}
	}

	level, err := logrus.ParseLevel(levelStr)
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(CodeInvalidParams, "Invalid log level: "+levelStr+". Use: trace, debug, info, warn, error, fatal", nil),
			ID:      req.ID,
		}
	}

	prev := logrus.GetLevel()
	s.logger.WithFields(logrus.Fields{
		"previous": prev.String(),
		"current":  level.String(),
	}).Info("Log level changed via RPC")
	logrus.SetLevel(level)

	return &Response{
		JSONRPC: "2.0",
		Result:  "Log level set to " + level.String(),
		ID:      req.ID,
	}
}

