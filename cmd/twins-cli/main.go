package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	"gopkg.in/yaml.v3"

	twinslib "github.com/twins-dev/twins-core/internal/cli"
	"github.com/twins-dev/twins-core/pkg/types"
)

func main() {
	app := twinslib.CreateBaseApp("twins-cli", "TWINS cryptocurrency RPC client", twinslib.String())

	// Add RPC client flags
	app.Flags = append(app.Flags, twinslib.CommonRPCClientFlags()...)

	// Blockchain commands
	app.Commands = []*cli.Command{
		{
			Name:     "getinfo",
			Usage:    "Get general information about the node",
			Category: "Blockchain",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "getinfo", nil)
			},
		},
		{
			Name:     "getblockchaininfo",
			Usage:    "Get blockchain information",
			Category: "Blockchain",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "getblockchaininfo", nil)
			},
		},
		{
			Name:      "getblock",
			Usage:     "Get block information",
			Category:  "Blockchain",
			ArgsUsage: "<blockhash> [verbosity]",
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("block hash required")
				}

				args := []interface{}{c.Args().Get(0)}
				if c.NArg() > 1 {
					if verbosity, err := strconv.Atoi(c.Args().Get(1)); err == nil {
						args = append(args, verbosity)
					}
				}

				return executeRPC(c, "getblock", args)
			},
		},
		{
			Name:      "getblockhash",
			Usage:     "Get block hash by height",
			Category:  "Blockchain",
			ArgsUsage: "<height>",
			Action: func(c *cli.Context) error {
				if c.NArg() != 1 {
					return fmt.Errorf("block height required")
				}

				height, err := strconv.Atoi(c.Args().First())
				if err != nil {
					return fmt.Errorf("invalid block height: %v", err)
				}

				return executeRPC(c, "getblockhash", []interface{}{height})
			},
		},
		{
			Name:     "getblockcount",
			Usage:    "Get the current block height",
			Category: "Blockchain",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "getblockcount", nil)
			},
		},
		{
			Name:     "getbestblockhash",
			Usage:    "Get the hash of the best block",
			Category: "Blockchain",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "getbestblockhash", nil)
			},
		},
		{
			Name:      "verifychain",
			Usage:     "Verify blockchain database integrity",
			Category:  "Blockchain",
			ArgsUsage: "[checklevel] [numblocks]",
			Action: func(c *cli.Context) error {
				args := []interface{}{}
				if c.NArg() > 0 {
					if level, err := strconv.Atoi(c.Args().Get(0)); err == nil {
						args = append(args, level)
					}
				}
				if c.NArg() > 1 {
					if blocks, err := strconv.Atoi(c.Args().Get(1)); err == nil {
						args = append(args, blocks)
					}
				}
				return executeRPC(c, "verifychain", args)
			},
		},
		{
			Name:     "getchaintips",
			Usage:    "Return information about all known chain tips",
			Category: "Blockchain",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "getchaintips", nil)
			},
		},
		{
			Name:      "invalidateblock",
			Usage:     "Mark a block as invalid (test/development only)",
			Category:  "Blockchain",
			ArgsUsage: "<blockhash>",
			Action: func(c *cli.Context) error {
				if c.NArg() != 1 {
					return fmt.Errorf("block hash required")
				}
				return executeRPC(c, "invalidateblock", []interface{}{c.Args().First()})
			},
		},
		{
			Name:      "reconsiderblock",
			Usage:     "Remove invalid status from a block",
			Category:  "Blockchain",
			ArgsUsage: "<blockhash>",
			Action: func(c *cli.Context) error {
				if c.NArg() != 1 {
					return fmt.Errorf("block hash required")
				}
				return executeRPC(c, "reconsiderblock", []interface{}{c.Args().First()})
			},
		},
		{
			Name:      "addcheckpoint",
			Usage:     "Add a checkpoint to the blockchain",
			Category:  "Blockchain",
			ArgsUsage: "<height> <hash>",
			Action: func(c *cli.Context) error {
				if c.NArg() != 2 {
					return fmt.Errorf("height and hash required")
				}
				height, err := strconv.Atoi(c.Args().Get(0))
				if err != nil {
					return fmt.Errorf("invalid height: %v", err)
				}
				return executeRPC(c, "addcheckpoint", []interface{}{height, c.Args().Get(1)})
			},
		},
		{
			Name:      "getfeeinfo",
			Usage:     "Get network fee information",
			Category:  "Blockchain",
			ArgsUsage: "[blocks]",
			Action: func(c *cli.Context) error {
				args := []interface{}{}
				if c.NArg() > 0 {
					if blocks, err := strconv.Atoi(c.Args().First()); err == nil {
						args = append(args, blocks)
					}
				}
				return executeRPC(c, "getfeeinfo", args)
			},
		},
		{
			Name:     "getdifficulty",
			Usage:    "Get the current difficulty",
			Category: "Blockchain",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "getdifficulty", nil)
			},
		},
		{
			Name:      "getblockheader",
			Usage:     "Get block header information",
			Category:  "Blockchain",
			ArgsUsage: "<blockhash> [verbose]",
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("block hash required")
				}

				args := []interface{}{c.Args().Get(0)}
				if c.NArg() > 1 {
					verbose := strings.ToLower(c.Args().Get(1)) == "true" || c.Args().Get(1) == "1"
					args = append(args, verbose)
				} else {
					args = append(args, true) // Default to verbose
				}

				return executeRPC(c, "getblockheader", args)
			},
		},
		{
			Name:     "gettxoutsetinfo",
			Usage:    "Get statistics about the UTXO set",
			Category: "Blockchain",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "gettxoutsetinfo", nil)
			},
		},

		// Transaction commands
		{
			Name:      "getrawtransaction",
			Usage:     "Get raw transaction data",
			Category:  "Transactions",
			ArgsUsage: "<txid> [verbose]",
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("transaction ID required")
				}

				args := []interface{}{c.Args().Get(0)}
				if c.NArg() > 1 {
					// Parse verbose parameter as integer (0 or 1)
					verboseStr := c.Args().Get(1)
					verbose := 0
					if strings.ToLower(verboseStr) == "true" || verboseStr == "1" {
						verbose = 1
					}
					args = append(args, verbose)
				}

				return executeRPC(c, "getrawtransaction", args)
			},
		},
		{
			Name:      "sendrawtransaction",
			Usage:     "Submit a raw transaction to the network",
			Category:  "Transactions",
			ArgsUsage: "<hexstring>",
			Action: func(c *cli.Context) error {
				if c.NArg() != 1 {
					return fmt.Errorf("raw transaction hex string required")
				}

				return executeRPC(c, "sendrawtransaction", []interface{}{c.Args().First()})
			},
		},
		{
			Name:     "getmempoolinfo",
			Usage:    "Get mempool information",
			Category: "Transactions",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "getmempoolinfo", nil)
			},
		},
		{
			Name:      "getrawmempool",
			Usage:     "Get all transaction IDs in mempool",
			Category:  "Transactions",
			ArgsUsage: "[verbose]",
			Action: func(c *cli.Context) error {
				args := []interface{}{}
				if c.NArg() > 0 {
					verbose := strings.ToLower(c.Args().Get(0)) == "true" || c.Args().Get(0) == "1"
					args = append(args, verbose)
				}
				return executeRPC(c, "getrawmempool", args)
			},
		},
		{
			Name:      "gettxout",
			Usage:     "Get details about an unspent transaction output",
			Category:  "Transactions",
			ArgsUsage: "<txid> <n> [includemempool]",
			Action: func(c *cli.Context) error {
				if c.NArg() < 2 {
					return fmt.Errorf("transaction ID and output index required")
				}

				txid := c.Args().Get(0)
				n, err := strconv.Atoi(c.Args().Get(1))
				if err != nil {
					return fmt.Errorf("invalid output index: %v", err)
				}

				args := []interface{}{txid, n}
				if c.NArg() > 2 {
					includemempool := strings.ToLower(c.Args().Get(2)) == "true" || c.Args().Get(2) == "1"
					args = append(args, includemempool)
				}

				return executeRPC(c, "gettxout", args)
			},
		},

		// Network commands
		{
			Name:     "getnetworkinfo",
			Usage:    "Get network information",
			Category: "Network",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "getnetworkinfo", nil)
			},
		},
		{
			Name:     "getpeerinfo",
			Usage:    "Get information about connected peers",
			Category: "Network",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "getpeerinfo", nil)
			},
		},
		{
			Name:     "getconnectioncount",
			Usage:    "Get the number of connections to the network",
			Category: "Network",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "getconnectioncount", nil)
			},
		},
		{
			Name:      "addnode",
			Usage:     "Add a node to the connection list",
			Category:  "Network",
			ArgsUsage: "<node> <add|remove|onetry>",
			Action: func(c *cli.Context) error {
				if c.NArg() != 2 {
					return fmt.Errorf("node address and command required")
				}

				return executeRPC(c, "addnode", []interface{}{c.Args().Get(0), c.Args().Get(1)})
			},
		},
		{
			Name:      "disconnectnode",
			Usage:     "Disconnect from a peer",
			Category:  "Network",
			ArgsUsage: "<address>",
			Action: func(c *cli.Context) error {
				if c.NArg() != 1 {
					return fmt.Errorf("node address required")
				}

				return executeRPC(c, "disconnectnode", []interface{}{c.Args().First()})
			},
		},
		{
			Name:     "getnettotals",
			Usage:    "Get network traffic statistics",
			Category: "Network",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "getnettotals", nil)
			},
		},
		{
			Name:     "getsyncstatus",
			Usage:    "Get detailed blockchain synchronization status",
			Category: "Network",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "getsyncstatus", nil)
			},
		},
		{
			Name:     "ping",
			Usage:    "Send a ping to all peers",
			Category: "Network",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "ping", nil)
			},
		},
		{
			Name:      "getaddednodeinfo",
			Usage:     "Get information about added nodes",
			Category:  "Network",
			ArgsUsage: "<dns> [node]",
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("dns parameter required (true/false)")
				}

				dns := strings.ToLower(c.Args().Get(0)) == "true"
				args := []interface{}{dns}

				if c.NArg() > 1 {
					args = append(args, c.Args().Get(1))
				}

				return executeRPC(c, "getaddednodeinfo", args)
			},
		},
		{
			Name:      "setban",
			Usage:     "Add or remove an IP/Subnet from the banned list",
			Category:  "Network",
			ArgsUsage: "<subnet> <add|remove> [bantime] [absolute]",
			Action: func(c *cli.Context) error {
				if c.NArg() < 2 {
					return fmt.Errorf("subnet and command required")
				}

				subnet := c.Args().Get(0)
				command := c.Args().Get(1)
				args := []interface{}{subnet, command}

				if c.NArg() > 2 {
					if bantime, err := strconv.ParseInt(c.Args().Get(2), 10, 64); err == nil {
						args = append(args, bantime)
					}
				}

				if c.NArg() > 3 {
					absolute := strings.ToLower(c.Args().Get(3)) == "true"
					args = append(args, absolute)
				}

				return executeRPC(c, "setban", args)
			},
		},
		{
			Name:     "listbanned",
			Usage:    "List all banned IPs/Subnets",
			Category: "Network",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "listbanned", nil)
			},
		},
		{
			Name:     "clearbanned",
			Usage:    "Clear all banned IPs",
			Category: "Network",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "clearbanned", nil)
			},
		},

		// Staking/Mining commands
		{
			Name:     "getstakinginfo",
			Usage:    "Get staking information",
			Category: "Staking",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "getstakinginfo", nil)
			},
		},
		{
			Name:      "setstaking",
			Usage:     "Enable or disable staking",
			Category:  "Staking",
			ArgsUsage: "<true|false>",
			Action: func(c *cli.Context) error {
				if c.NArg() != 1 {
					return fmt.Errorf("staking status required (true/false)")
				}

				enabled := strings.ToLower(c.Args().First()) == "true"
				return executeRPC(c, "setstaking", []interface{}{enabled})
			},
		},
		{
			Name:     "getmininginfo",
			Usage:    "Get mining/staking information",
			Category: "Staking",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "getmininginfo", nil)
			},
		},
		{
			Name:     "getstakingstatus",
			Usage:    "Get current staking status",
			Category: "Staking",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "getstakingstatus", nil)
			},
		},
		{
			Name:      "getnetworkhashps",
			Usage:     "Get estimated network hashes per second",
			Category:  "Staking",
			ArgsUsage: "[blocks] [height]",
			Action: func(c *cli.Context) error {
				args := []interface{}{}
				if c.NArg() > 0 {
					if blocks, err := strconv.Atoi(c.Args().Get(0)); err == nil {
						args = append(args, blocks)
					}
				}
				if c.NArg() > 1 {
					if height, err := strconv.Atoi(c.Args().Get(1)); err == nil {
						args = append(args, height)
					}
				}
				return executeRPC(c, "getnetworkhashps", args)
			},
		},
		{
			Name:     "getrewardrates",
			Usage:    "Get block reward distribution rates for masternodes",
			Category: "Staking",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "getrewardrates", nil)
			},
		},
		{
			Name:      "submitblock",
			Usage:     "Submit a new block to the network",
			Category:  "Staking",
			ArgsUsage: "<hexdata>",
			Action: func(c *cli.Context) error {
				if c.NArg() != 1 {
					return fmt.Errorf("block hex data required")
				}

				return executeRPC(c, "submitblock", []interface{}{c.Args().First()})
			},
		},
		{
			Name:      "getblocktemplate",
			Usage:     "Get data needed to construct a block",
			Category:  "Staking",
			ArgsUsage: "[jsonrequestobject]",
			Action: func(c *cli.Context) error {
				args := []interface{}{}
				if c.NArg() > 0 {
					var jsonreq map[string]interface{}
					if err := json.Unmarshal([]byte(c.Args().Get(0)), &jsonreq); err != nil {
						return fmt.Errorf("invalid JSON request object: %v", err)
					}
					args = append(args, jsonreq)
				}
				return executeRPC(c, "getblocktemplate", args)
			},
		},
		{
			Name:      "prioritisetransaction",
			Usage:     "Accept transaction into blocks at higher priority",
			Category:  "Staking",
			ArgsUsage: "<txid> <priority_delta> <fee_delta>",
			Action: func(c *cli.Context) error {
				if c.NArg() != 3 {
					return fmt.Errorf("txid, priority_delta and fee_delta required")
				}

				txid := c.Args().Get(0)
				priorityDelta, err := strconv.ParseFloat(c.Args().Get(1), 64)
				if err != nil {
					return fmt.Errorf("invalid priority_delta: %v", err)
				}
				feeDelta, err := strconv.ParseInt(c.Args().Get(2), 10, 64)
				if err != nil {
					return fmt.Errorf("invalid fee_delta: %v", err)
				}

				return executeRPC(c, "prioritisetransaction", []interface{}{txid, priorityDelta, feeDelta})
			},
		},
		{
			Name:      "estimatefee",
			Usage:     "Estimate fee per kB for confirmation",
			Category:  "Staking",
			ArgsUsage: "<nblocks>",
			Action: func(c *cli.Context) error {
				if c.NArg() != 1 {
					return fmt.Errorf("nblocks required")
				}

				nblocks, err := strconv.Atoi(c.Args().First())
				if err != nil {
					return fmt.Errorf("invalid nblocks: %v", err)
				}

				return executeRPC(c, "estimatefee", []interface{}{nblocks})
			},
		},
		{
			Name:      "estimatepriority",
			Usage:     "Estimate priority for zero-fee confirmation",
			Category:  "Staking",
			ArgsUsage: "<nblocks>",
			Action: func(c *cli.Context) error {
				if c.NArg() != 1 {
					return fmt.Errorf("nblocks required")
				}

				nblocks, err := strconv.Atoi(c.Args().First())
				if err != nil {
					return fmt.Errorf("invalid nblocks: %v", err)
				}

				return executeRPC(c, "estimatepriority", []interface{}{nblocks})
			},
		},
		{
			Name:      "setgenerate",
			Usage:     "Enable or disable PoS staking (legacy command)",
			Category:  "Staking",
			ArgsUsage: "<generate> [genproclimit]",
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("generate parameter required (true/false)")
				}

				generate := strings.ToLower(c.Args().Get(0)) == "true"
				args := []interface{}{generate}

				if c.NArg() > 1 {
					if genproclimit, err := strconv.Atoi(c.Args().Get(1)); err == nil {
						args = append(args, genproclimit)
					}
				}

				return executeRPC(c, "setgenerate", args)
			},
		},
		{
			Name:     "getgenerate",
			Usage:    "Check if staking is enabled (legacy command)",
			Category: "Staking",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "getgenerate", nil)
			},
		},
		{
			Name:     "gethashespersec",
			Usage:    "Get hash rate (deprecated, always returns 0)",
			Category: "Staking",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "gethashespersec", nil)
			},
		},

		// Wallet commands
		{
			Name:     "getnewaddress",
			Usage:    "Generate a new receiving address",
			Category: "Wallet",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:  "label",
					Usage: "Optional label for the address",
				},
			},
			Action: func(c *cli.Context) error {
				args := []interface{}{}
				if c.IsSet("label") {
					args = append(args, c.String("label"))
				}
				return executeRPC(c, "getnewaddress", args)
			},
		},
		{
			Name:      "getaccountaddress",
			Usage:     "Get the current address for an account",
			Category:  "Wallet",
			ArgsUsage: "<account>",
			Action: func(c *cli.Context) error {
				if c.NArg() != 1 {
					return fmt.Errorf("account name required")
				}
				return executeRPC(c, "getaccountaddress", []interface{}{c.Args().First()})
			},
		},
		{
			Name:     "getbalance",
			Usage:    "Get wallet balance",
			Category: "Wallet",
			Flags: []cli.Flag{
				&cli.IntFlag{
					Name:  "minconf",
					Usage: "Minimum confirmations",
					Value: 1,
				},
			},
			Action: func(c *cli.Context) error {
				args := []interface{}{}
				if c.IsSet("minconf") {
					args = append(args, c.Int("minconf"))
				}
				return executeRPC(c, "getbalance", args)
			},
		},
		{
			Name:      "sendtoaddress",
			Usage:     "Send amount to a given address",
			Category:  "Wallet",
			ArgsUsage: "<address> <amount> [comment] [comment-to]",
			Action: func(c *cli.Context) error {
				if c.NArg() < 2 {
					return fmt.Errorf("address and amount required")
				}

				address := c.Args().Get(0)
				amountStr := c.Args().Get(1)
				amount, err := strconv.ParseFloat(amountStr, 64)
				if err != nil {
					return fmt.Errorf("invalid amount: %v", err)
				}

				args := []interface{}{address, amount}
				if c.NArg() > 2 {
					args = append(args, c.Args().Get(2)) // comment
				}
				if c.NArg() > 3 {
					args = append(args, c.Args().Get(3)) // comment-to
				}

				return executeRPC(c, "sendtoaddress", args)
			},
		},
		{
			Name:     "listtransactions",
			Usage:    "List recent transactions",
			Category: "Wallet",
			Flags: []cli.Flag{
				&cli.IntFlag{
					Name:  "count",
					Usage: "Number of transactions to list",
					Value: 10,
				},
				&cli.IntFlag{
					Name:  "skip",
					Usage: "Number of transactions to skip",
					Value: 0,
				},
			},
			Action: func(c *cli.Context) error {
				args := []interface{}{"*", c.Int("count"), c.Int("skip")}
				return executeRPC(c, "listtransactions", args)
			},
		},
		{
			Name:      "gettransaction",
			Usage:     "Get detailed information about a wallet transaction",
			Category:  "Wallet",
			ArgsUsage: "<txid>",
			Action: func(c *cli.Context) error {
				if c.NArg() != 1 {
					return fmt.Errorf("transaction ID required")
				}
				return executeRPC(c, "gettransaction", []interface{}{c.Args().First()})
			},
		},
		{
			Name:     "listaddressgroupings",
			Usage:    "List groups of addresses with balances",
			Category: "Wallet",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "listaddressgroupings", nil)
			},
		},
		{
			Name:      "dumpprivkey",
			Usage:     "Reveal private key for an address",
			Category:  "Wallet",
			ArgsUsage: "<address>",
			Action: func(c *cli.Context) error {
				if c.NArg() != 1 {
					return fmt.Errorf("address required")
				}
				return executeRPC(c, "dumpprivkey", []interface{}{c.Args().First()})
			},
		},
		{
			Name:      "importprivkey",
			Usage:     "Import a private key",
			Category:  "Wallet",
			ArgsUsage: "<privkey> [label] [rescan]",
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("private key required")
				}

				args := []interface{}{c.Args().Get(0)}
				if c.NArg() > 1 {
					args = append(args, c.Args().Get(1)) // label
				}
				if c.NArg() > 2 {
					rescan := strings.ToLower(c.Args().Get(2)) == "true"
					args = append(args, rescan)
				}

				return executeRPC(c, "importprivkey", args)
			},
		},
		{
			Name:     "walletlock",
			Usage:    "Lock the wallet",
			Category: "Wallet",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "walletlock", nil)
			},
		},
		{
			Name:      "walletpassphrase",
			Usage:     "Unlock the wallet for a time period",
			Category:  "Wallet",
			ArgsUsage: "<passphrase> <timeout> [stakingonly]",
			Action: func(c *cli.Context) error {
				if c.NArg() < 2 {
					return fmt.Errorf("passphrase and timeout required")
				}

				passphrase := c.Args().Get(0)
				timeout, err := strconv.Atoi(c.Args().Get(1))
				if err != nil {
					return fmt.Errorf("invalid timeout: %v", err)
				}

				// Optional stakingonly parameter (default false)
				params := []interface{}{passphrase, timeout}
				if c.NArg() >= 3 {
					stakingOnly := c.Args().Get(2) == "true" || c.Args().Get(2) == "1"
					params = append(params, stakingOnly)
				}

				return executeRPC(c, "walletpassphrase", params)
			},
		},
		{
			Name:      "walletpassphrasechange",
			Usage:     "Change wallet passphrase",
			Category:  "Wallet",
			ArgsUsage: "<oldpassphrase> <newpassphrase>",
			Action: func(c *cli.Context) error {
				if c.NArg() != 2 {
					return fmt.Errorf("old and new passphrase required")
				}

				return executeRPC(c, "walletpassphrasechange", []interface{}{c.Args().Get(0), c.Args().Get(1)})
			},
		},
		{
			Name:      "encryptwallet",
			Usage:     "Encrypt the wallet with a passphrase",
			Category:  "Wallet",
			ArgsUsage: "<passphrase>",
			Action: func(c *cli.Context) error {
				if c.NArg() != 1 {
					return fmt.Errorf("passphrase required")
				}

				return executeRPC(c, "encryptwallet", []interface{}{c.Args().First()})
			},
		},
		{
			Name:      "backupwallet",
			Usage:     "Backup wallet to a file",
			Category:  "Wallet",
			ArgsUsage: "<destination>",
			Action: func(c *cli.Context) error {
				if c.NArg() != 1 {
					return fmt.Errorf("destination path required")
				}

				return executeRPC(c, "backupwallet", []interface{}{c.Args().First()})
			},
		},
		{
			Name:     "getwalletinfo",
			Usage:    "Get wallet state information",
			Category: "Wallet",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "getwalletinfo", nil)
			},
		},
		{
			Name:      "keypoolrefill",
			Usage:     "Refill the keypool",
			Category:  "Wallet",
			ArgsUsage: "[newsize]",
			Action: func(c *cli.Context) error {
				args := []interface{}{}
				if c.NArg() > 0 {
					if newsize, err := strconv.Atoi(c.Args().First()); err == nil {
						args = append(args, newsize)
					}
				}
				return executeRPC(c, "keypoolrefill", args)
			},
		},
		{
			Name:      "reservebalance",
			Usage:     "Show or set the reserve balance amount",
			Category:  "Wallet",
			ArgsUsage: "[reserve] [amount]",
			Action: func(c *cli.Context) error {
				args := []interface{}{}
				if c.NArg() > 0 {
					// Parse reserve as boolean
					reserve := c.Args().Get(0) == "true" || c.Args().Get(0) == "1"
					args = append(args, reserve)
				}
				if c.NArg() > 1 {
					if amount, err := strconv.ParseFloat(c.Args().Get(1), 64); err == nil {
						args = append(args, amount)
					}
				}
				return executeRPC(c, "reservebalance", args)
			},
		},
		{
			Name:      "setstakesplitthreshold",
			Usage:     "Set the stake split threshold",
			Category:  "Wallet",
			ArgsUsage: "<threshold>",
			Action: func(c *cli.Context) error {
				if c.NArg() != 1 {
					return fmt.Errorf("threshold value required")
				}

				threshold, err := strconv.ParseFloat(c.Args().First(), 64)
				if err != nil {
					return fmt.Errorf("invalid threshold value: %v", err)
				}

				return executeRPC(c, "setstakesplitthreshold", []interface{}{threshold})
			},
		},
		{
			Name:     "getstakesplitthreshold",
			Usage:    "Get the current stake split threshold",
			Category: "Wallet",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "getstakesplitthreshold", nil)
			},
		},
		{
			Name:      "setautocombine",
			Usage:     "Configure automatic UTXO consolidation",
			Category:  "Wallet",
			ArgsUsage: "<true|false> [target_amount]",
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("enabled parameter required (true/false)")
				}

				enabled := strings.ToLower(c.Args().Get(0)) == "true"
				args := []interface{}{enabled}

				if c.NArg() > 1 {
					if target, err := strconv.ParseFloat(c.Args().Get(1), 64); err == nil {
						args = append(args, target)
					} else {
						return fmt.Errorf("invalid target amount: %s", c.Args().Get(1))
					}
				}

				return executeRPC(c, "setautocombine", args)
			},
		},
		{
			Name:     "getautocombine",
			Usage:    "Get current auto-combine configuration",
			Category: "Wallet",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "getautocombine", nil)
			},
		},
		{
			Name:      "multisend",
			Usage:     "Manage multi-send configuration",
			Category:  "Wallet",
			ArgsUsage: "<command> [args...]",
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("command required (print, clear, enable, disable, etc.)")
				}

				args := make([]interface{}, c.NArg())
				for i := 0; i < c.NArg(); i++ {
					args[i] = c.Args().Get(i)
				}

				return executeRPC(c, "multisend", args)
			},
		},
		{
			Name:     "dumphdinfo",
			Usage:    "Dump HD wallet information",
			Category: "Wallet",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "dumphdinfo", nil)
			},
		},
		{
			Name:      "dumpwallet",
			Usage:     "Dump wallet private keys to a file",
			Category:  "Wallet",
			ArgsUsage: "<filename>",
			Action: func(c *cli.Context) error {
				if c.NArg() != 1 {
					return fmt.Errorf("filename required")
				}

				return executeRPC(c, "dumpwallet", []interface{}{c.Args().First()})
			},
		},
		{
			Name:      "importaddress",
			Usage:     "Import a watch-only address",
			Category:  "Wallet",
			ArgsUsage: "<address> [label] [rescan]",
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("address required")
				}

				args := []interface{}{c.Args().Get(0)}

				if c.NArg() > 1 {
					args = append(args, c.Args().Get(1)) // label
				}

				if c.NArg() > 2 {
					rescan := strings.ToLower(c.Args().Get(2)) == "true"
					args = append(args, rescan)
				}

				return executeRPC(c, "importaddress", args)
			},
		},
		{
			Name:      "importwallet",
			Usage:     "Import wallet from a dump file",
			Category:  "Wallet",
			ArgsUsage: "<filename>",
			Action: func(c *cli.Context) error {
				if c.NArg() != 1 {
					return fmt.Errorf("filename required")
				}

				return executeRPC(c, "importwallet", []interface{}{c.Args().First()})
			},
		},
		{
			Name:      "addmultisigaddress",
			Usage:     "Add a multisignature address to the wallet",
			Category:  "Wallet",
			ArgsUsage: "<nrequired> <keys-json> [account]",
			Action: func(c *cli.Context) error {
				if c.NArg() < 2 {
					return fmt.Errorf("nrequired and keys array required")
				}

				nrequired, err := strconv.Atoi(c.Args().Get(0))
				if err != nil {
					return fmt.Errorf("invalid nrequired: %v", err)
				}

				// Parse keys JSON array
				var keys []string
				if err := json.Unmarshal([]byte(c.Args().Get(1)), &keys); err != nil {
					return fmt.Errorf("invalid keys JSON: %v", err)
				}

				args := []interface{}{nrequired, keys}

				// Add optional account
				if c.NArg() > 2 {
					args = append(args, c.Args().Get(2))
				}

				return executeRPC(c, "addmultisigaddress", args)
			},
		},
		{
			Name:      "signmessage",
			Usage:     "Sign a message with the private key of an address",
			Category:  "Wallet",
			ArgsUsage: "<address> <message>",
			Action: func(c *cli.Context) error {
				if c.NArg() != 2 {
					return fmt.Errorf("address and message required")
				}

				return executeRPC(c, "signmessage", []interface{}{c.Args().Get(0), c.Args().Get(1)})
			},
		},
		{
			Name:      "sendmany",
			Usage:     "Send to multiple addresses in one transaction",
			Category:  "Wallet",
			ArgsUsage: "<account> <recipients-json> [minconf] [comment]",
			Action: func(c *cli.Context) error {
				if c.NArg() < 2 {
					return fmt.Errorf("account and recipients required")
				}

				// Parse recipients JSON
				var recipients map[string]interface{}
				if err := json.Unmarshal([]byte(c.Args().Get(1)), &recipients); err != nil {
					return fmt.Errorf("invalid recipients JSON: %v", err)
				}

				args := []interface{}{c.Args().Get(0), recipients}

				// Add optional minconf
				if c.NArg() > 2 {
					if minconf, err := strconv.Atoi(c.Args().Get(2)); err == nil {
						args = append(args, minconf)
					}
				}

				// Add optional comment
				if c.NArg() > 3 {
					args = append(args, c.Args().Get(3))
				}

				return executeRPC(c, "sendmany", args)
			},
		},
		{
			Name:      "move",
			Usage:     "Move amount from one account to another",
			Category:  "Wallet",
			ArgsUsage: "<fromaccount> <toaccount> <amount> [minconf] [comment]",
			Action: func(c *cli.Context) error {
				if c.NArg() < 3 {
					return fmt.Errorf("fromaccount, toaccount, and amount required")
				}

				amount, err := strconv.ParseFloat(c.Args().Get(2), 64)
				if err != nil {
					return fmt.Errorf("invalid amount: %v", err)
				}

				args := []interface{}{c.Args().Get(0), c.Args().Get(1), amount}

				// Add optional minconf
				if c.NArg() > 3 {
					if minconf, err := strconv.Atoi(c.Args().Get(3)); err == nil {
						args = append(args, minconf)
					}
				}

				// Add optional comment
				if c.NArg() > 4 {
					args = append(args, c.Args().Get(4))
				}

				return executeRPC(c, "move", args)
			},
		},
		{
			Name:     "listreceivedbyaddress",
			Usage:    "List amounts received by each address",
			Category: "Wallet",
			Flags: []cli.Flag{
				&cli.IntFlag{
					Name:  "minconf",
					Usage: "Minimum confirmations",
					Value: 1,
				},
				&cli.BoolFlag{
					Name:  "includeempty",
					Usage: "Include addresses with no transactions",
				},
				&cli.BoolFlag{
					Name:  "includewatchonly",
					Usage: "Include watch-only addresses",
				},
			},
			Action: func(c *cli.Context) error {
				args := []interface{}{
					c.Int("minconf"),
					c.Bool("includeempty"),
					c.Bool("includewatchonly"),
				}
				return executeRPC(c, "listreceivedbyaddress", args)
			},
		},
		{
			Name:     "listreceivedbyaccount",
			Usage:    "List amounts received by each account",
			Category: "Wallet",
			Flags: []cli.Flag{
				&cli.IntFlag{
					Name:  "minconf",
					Usage: "Minimum confirmations",
					Value: 1,
				},
				&cli.BoolFlag{
					Name:  "includeempty",
					Usage: "Include accounts with no transactions",
				},
				&cli.BoolFlag{
					Name:  "includewatchonly",
					Usage: "Include watch-only",
				},
			},
			Action: func(c *cli.Context) error {
				args := []interface{}{
					c.Int("minconf"),
					c.Bool("includeempty"),
					c.Bool("includewatchonly"),
				}
				return executeRPC(c, "listreceivedbyaccount", args)
			},
		},
		{
			Name:      "listsinceblock",
			Usage:     "Get all transactions since a given block",
			Category:  "Wallet",
			ArgsUsage: "[blockhash] [target-confirmations] [includewatchonly]",
			Action: func(c *cli.Context) error {
				args := []interface{}{}

				if c.NArg() > 0 {
					args = append(args, c.Args().Get(0))
				}

				if c.NArg() > 1 {
					if tc, err := strconv.Atoi(c.Args().Get(1)); err == nil {
						args = append(args, tc)
					}
				}

				if c.NArg() > 2 {
					args = append(args, strings.ToLower(c.Args().Get(2)) == "true")
				}

				return executeRPC(c, "listsinceblock", args)
			},
		},
		{
			Name:     "listaccounts",
			Usage:    "List balances by account",
			Category: "Wallet",
			Flags: []cli.Flag{
				&cli.IntFlag{
					Name:  "minconf",
					Usage: "Minimum confirmations",
					Value: 1,
				},
				&cli.BoolFlag{
					Name:  "includewatchonly",
					Usage: "Include watch-only",
				},
			},
			Action: func(c *cli.Context) error {
				args := []interface{}{
					c.Int("minconf"),
					c.Bool("includewatchonly"),
				}
				return executeRPC(c, "listaccounts", args)
			},
		},
		// Control commands
		{
			Name:     "stop",
			Usage:    "Stop the TWINS daemon",
			Category: "Control",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "stop", nil)
			},
		},
		{
			Name:      "setloglevel",
			Usage:     "Change the daemon log level at runtime",
			Category:  "Control",
			ArgsUsage: "<level>",
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("log level required (trace, debug, info, warn, error, fatal)")
				}
				return executeRPC(c, "setloglevel", []interface{}{c.Args().Get(0)})
			},
		},
		// Raw transaction commands
		{
			Name:      "createrawtransaction",
			Usage:     "Create a raw transaction",
			Category:  "Transactions",
			ArgsUsage: "<inputs> <outputs> [locktime]",
			Action: func(c *cli.Context) error {
				if c.NArg() < 2 {
					return fmt.Errorf("inputs and outputs required")
				}

				// Parse inputs JSON
				var inputs []interface{}
				if err := json.Unmarshal([]byte(c.Args().Get(0)), &inputs); err != nil {
					return fmt.Errorf("invalid inputs JSON: %v", err)
				}

				// Parse outputs JSON
				var outputs map[string]interface{}
				if err := json.Unmarshal([]byte(c.Args().Get(1)), &outputs); err != nil {
					return fmt.Errorf("invalid outputs JSON: %v", err)
				}

				args := []interface{}{inputs, outputs}

				// Optional locktime
				if c.NArg() > 2 {
					if locktime, err := strconv.Atoi(c.Args().Get(2)); err == nil {
						args = append(args, locktime)
					}
				}

				return executeRPC(c, "createrawtransaction", args)
			},
		},
		{
			Name:      "signrawtransaction",
			Usage:     "Sign a raw transaction",
			Category:  "Transactions",
			ArgsUsage: "<hex> [prevtxs] [privatekeys] [sighashtype]",
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("transaction hex required")
				}

				args := []interface{}{c.Args().Get(0)}

				// Optional prevtxs
				if c.NArg() > 1 && c.Args().Get(1) != "" {
					var prevtxs []interface{}
					if err := json.Unmarshal([]byte(c.Args().Get(1)), &prevtxs); err != nil {
						return fmt.Errorf("invalid prevtxs JSON: %v", err)
					}
					args = append(args, prevtxs)
				}

				// Optional privatekeys
				if c.NArg() > 2 && c.Args().Get(2) != "" {
					var privatekeys []interface{}
					if err := json.Unmarshal([]byte(c.Args().Get(2)), &privatekeys); err != nil {
						return fmt.Errorf("invalid privatekeys JSON: %v", err)
					}
					args = append(args, privatekeys)
				}

				// Optional sighashtype
				if c.NArg() > 3 {
					args = append(args, c.Args().Get(3))
				}

				return executeRPC(c, "signrawtransaction", args)
			},
		},
		{
			Name:      "decoderawtransaction",
			Usage:     "Decode a raw transaction",
			Category:  "Transactions",
			ArgsUsage: "<hex>",
			Action: func(c *cli.Context) error {
				if c.NArg() != 1 {
					return fmt.Errorf("transaction hex required")
				}

				return executeRPC(c, "decoderawtransaction", []interface{}{c.Args().First()})
			},
		},
		{
			Name:      "decodescript",
			Usage:     "Decode a hex-encoded script",
			Category:  "Transactions",
			ArgsUsage: "<hex>",
			Action: func(c *cli.Context) error {
				if c.NArg() != 1 {
					return fmt.Errorf("script hex required")
				}

				return executeRPC(c, "decodescript", []interface{}{c.Args().First()})
			},
		},
		{
			Name:      "listunspent",
			Usage:     "List unspent transaction outputs",
			Category:  "Transactions",
			ArgsUsage: "[minconf] [maxconf] [addresses]",
			Action: func(c *cli.Context) error {
				args := []interface{}{}

				// Optional minconf (default: 1)
				if c.NArg() > 0 {
					if minconf, err := strconv.Atoi(c.Args().Get(0)); err == nil {
						args = append(args, minconf)
					}
				}

				// Optional maxconf (default: 9999999)
				if c.NArg() > 1 {
					if maxconf, err := strconv.Atoi(c.Args().Get(1)); err == nil {
						args = append(args, maxconf)
					}
				}

				// Optional addresses filter
				if c.NArg() > 2 {
					var addresses []interface{}
					if err := json.Unmarshal([]byte(c.Args().Get(2)), &addresses); err != nil {
						return fmt.Errorf("invalid addresses JSON: %v", err)
					}
					args = append(args, addresses)
				}

				return executeRPC(c, "listunspent", args)
			},
		},
		{
			Name:      "lockunspent",
			Usage:     "Lock or unlock unspent transaction outputs",
			Category:  "Transactions",
			ArgsUsage: "<unlock> <outputs>",
			Action: func(c *cli.Context) error {
				if c.NArg() < 2 {
					return fmt.Errorf("unlock flag and outputs required")
				}

				// Parse unlock flag
				unlock := strings.ToLower(c.Args().Get(0)) == "true" || c.Args().Get(0) == "1"

				// Parse outputs JSON
				var outputs []interface{}
				if err := json.Unmarshal([]byte(c.Args().Get(1)), &outputs); err != nil {
					return fmt.Errorf("invalid outputs JSON: %v", err)
				}

				return executeRPC(c, "lockunspent", []interface{}{unlock, outputs})
			},
		},
		{
			Name:     "listlockunspent",
			Usage:    "List temporarily unspendable outputs",
			Category: "Transactions",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "listlockunspent", nil)
			},
		},

		// Masternode commands
		{
			Name:      "masternode",
			Usage:     "Execute masternode command",
			Category:  "Masternodes",
			ArgsUsage: "<command> [args...]",
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("masternode command required (list, count, status, etc.)")
				}

				// Pass all arguments to the masternode RPC
				args := make([]interface{}, c.NArg())
				for i := 0; i < c.NArg(); i++ {
					args[i] = c.Args().Get(i)
				}

				return executeRPC(c, "masternode", args)
			},
		},
		{
			Name:     "getmasternodecount",
			Usage:    "Get masternode count by status",
			Category: "Masternodes",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "getmasternodecount", nil)
			},
		},
		{
			Name:     "listmasternodes",
			Usage:    "Get list of masternodes",
			Category: "Masternodes",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:  "status",
					Usage: "Filter by status (enabled, expired, etc.)",
				},
				&cli.StringFlag{
					Name:  "tier",
					Usage: "Filter by tier (bronze, silver, gold, platinum)",
				},
			},
			Action: func(c *cli.Context) error {
				args := []interface{}{}
				if c.IsSet("status") {
					args = append(args, c.String("status"))
				}
				if c.IsSet("tier") {
					args = append(args, c.String("tier"))
				}

				return executeRPC(c, "listmasternodes", args)
			},
		},
		{
			Name:      "startmasternode",
			Usage:     "Start masternode",
			Category:  "Masternodes",
			ArgsUsage: "<mode> [alias] - mode: local|alias|all|many|missing|disabled",
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("startmasternode requires mode parameter (local|alias|all|many|missing|disabled)")
				}
				args := make([]interface{}, c.NArg())
				for i := 0; i < c.NArg(); i++ {
					args[i] = c.Args().Get(i)
				}
				return executeRPC(c, "startmasternode", args)
			},
		},
		{
			Name:     "getmasternodestatus",
			Usage:    "Get masternode status",
			Category: "Masternodes",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "getmasternodestatus", nil)
			},
		},
		{
			Name:     "masternodecurrent",
			Usage:    "Get current masternode winner",
			Category: "Masternodes",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "masternodecurrent", nil)
			},
		},
		{
			Name:      "getmasternodewinners",
			Usage:     "Get masternode winners for recent blocks",
			Category:  "Masternodes",
			ArgsUsage: "[blocks]",
			Action: func(c *cli.Context) error {
				args := []interface{}{}
				if c.NArg() > 0 {
					// Parse blocks parameter as integer
					blocks, err := strconv.Atoi(c.Args().Get(0))
					if err != nil {
						return fmt.Errorf("invalid blocks parameter: %v", err)
					}
					args = append(args, blocks)
				}
				return executeRPC(c, "getmasternodewinners", args)
			},
		},
		{
			Name:      "getmasternodescores",
			Usage:     "Get masternode scores for next payment",
			Category:  "Masternodes",
			ArgsUsage: "[blocks]",
			Action: func(c *cli.Context) error {
				args := []interface{}{}
				if c.NArg() > 0 {
					// Parse blocks parameter as integer
					blocks, err := strconv.Atoi(c.Args().First())
					if err != nil {
						return fmt.Errorf("invalid blocks parameter: %v", err)
					}
					args = append(args, blocks)
				}
				return executeRPC(c, "getmasternodescores", args)
			},
		},
		{
			Name:     "createmasternodekey",
			Usage:    "Create a new masternode private key",
			Category: "Masternodes",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "createmasternodekey", nil)
			},
		},
		{
			Name:     "getmasternodeoutputs",
			Usage:    "Print masternode transaction outputs",
			Category: "Masternodes",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "getmasternodeoutputs", nil)
			},
		},
		{
			Name:      "listmasternodeconf",
			Usage:     "Print masternode.conf in JSON format",
			Category:  "Masternodes",
			ArgsUsage: "[filter]",
			Action: func(c *cli.Context) error {
				args := []interface{}{}
				if c.NArg() > 0 {
					args = append(args, c.Args().First())
				}
				return executeRPC(c, "listmasternodeconf", args)
			},
		},
		{
			Name:     "getpoolinfo",
			Usage:    "Get masternode pool information",
			Category: "Masternodes",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "getpoolinfo", nil)
			},
		},
		{
			Name:      "createmasternodebroadcast",
			Usage:     "Create masternode broadcast message",
			Category:  "Masternodes",
			ArgsUsage: "<command> [alias]",
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("createmasternodebroadcast requires a command parameter (alias or all)")
				}
				args := make([]interface{}, c.NArg())
				for i := 0; i < c.NArg(); i++ {
					args[i] = c.Args().Get(i)
				}
				return executeRPC(c, "createmasternodebroadcast", args)
			},
		},
		{
			Name:      "decodemasternodebroadcast",
			Usage:     "Decode masternode broadcast message",
			Category:  "Masternodes",
			ArgsUsage: "<hex>",
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("decodemasternodebroadcast requires hex parameter")
				}
				hexStr := c.Args().First()
				if !isValidHex(hexStr) {
					return fmt.Errorf("invalid hex string: must be valid hexadecimal with even length")
				}
				args := []interface{}{hexStr}
				return executeRPC(c, "decodemasternodebroadcast", args)
			},
		},
		{
			Name:      "relaymasternodebroadcast",
			Usage:     "Relay masternode broadcast message to network",
			Category:  "Masternodes",
			ArgsUsage: "<hex>",
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("relaymasternodebroadcast requires hex parameter")
				}
				hexStr := c.Args().First()
				if !isValidHex(hexStr) {
					return fmt.Errorf("invalid hex string: must be valid hexadecimal with even length")
				}
				args := []interface{}{hexStr}
				return executeRPC(c, "relaymasternodebroadcast", args)
			},
		},
		{
			Name:      "masternodeconnect",
			Usage:     "Connect to a masternode",
			Category:  "Masternodes",
			ArgsUsage: "<address>",
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("masternodeconnect requires address parameter")
				}
				args := []interface{}{c.Args().First()}
				return executeRPC(c, "masternodeconnect", args)
			},
		},
		{
			Name:     "masternodedebug",
			Usage:    "Print masternode debug information",
			Category: "Masternodes",
			Action: func(c *cli.Context) error {
				return executeRPC(c, "masternodedebug", nil)
			},
		},

		// NOTE: Budget/Governance commands removed
		// The budget system (inherited from PIVX/Dash) is permanently disabled in TWINS
		// via SPORK_13_ENABLE_SUPERBLOCKS set to year 2099. Commands removed:
		// - preparebudget, submitbudget, mnbudgetvote, mnbudget
		// - mnbudgetrawvote, mnfinalbudget
		// - getbudgetvotes, getnextsuperblock, getbudgetprojection, getbudgetinfo, checkbudgets

		// Utility commands
		{
			Name:      "validateaddress",
			Usage:     "Validate a TWINS address",
			Category:  "Utility",
			ArgsUsage: "<address>",
			Action: func(c *cli.Context) error {
				if c.NArg() != 1 {
					return fmt.Errorf("address required")
				}
				return executeRPC(c, "validateaddress", []interface{}{c.Args().First()})
			},
		},
		{
			Name:      "createmultisig",
			Usage:     "Create a multisig address",
			Category:  "Utility",
			ArgsUsage: "<nrequired> <keys>",
			Action: func(c *cli.Context) error {
				if c.NArg() != 2 {
					return fmt.Errorf("nrequired and keys array required")
				}
				nrequired, err := strconv.Atoi(c.Args().Get(0))
				if err != nil {
					return fmt.Errorf("invalid nrequired: %v", err)
				}
				var keys []string
				if err := json.Unmarshal([]byte(c.Args().Get(1)), &keys); err != nil {
					return fmt.Errorf("invalid keys array: %v", err)
				}
				return executeRPC(c, "createmultisig", []interface{}{nrequired, keys})
			},
		},
		{
			Name:      "verifymessage",
			Usage:     "Verify a signed message",
			Category:  "Utility",
			ArgsUsage: "<address> <signature> <message>",
			Action: func(c *cli.Context) error {
				if c.NArg() != 3 {
					return fmt.Errorf("address, signature, and message required")
				}
				return executeRPC(c, "verifymessage", []interface{}{
					c.Args().Get(0), // address
					c.Args().Get(1), // signature
					c.Args().Get(2), // message
				})
			},
		},
		{
			Name:      "setmocktime",
			Usage:     "Set mock time (regtest only)",
			Category:  "Utility",
			ArgsUsage: "<timestamp>",
			Action: func(c *cli.Context) error {
				if c.NArg() != 1 {
					return fmt.Errorf("timestamp required")
				}
				timestamp, err := strconv.ParseInt(c.Args().First(), 10, 64)
				if err != nil {
					return fmt.Errorf("invalid timestamp: %v", err)
				}
				return executeRPC(c, "setmocktime", []interface{}{timestamp})
			},
		},
		{
			Name:      "mnsync",
			Usage:     "Get masternode sync status or reset",
			Category:  "Utility",
			ArgsUsage: "<status|reset>",
			Action: func(c *cli.Context) error {
				if c.NArg() != 1 {
					return fmt.Errorf("mode required (status or reset)")
				}
				mode := c.Args().First()
				if mode != "status" && mode != "reset" {
					return fmt.Errorf("invalid mode (expected 'status' or 'reset')")
				}
				return executeRPC(c, "mnsync", []interface{}{mode})
			},
		},
		{
			Name:      "spork",
			Usage:     "Show or update spork values",
			Category:  "Utility",
			ArgsUsage: "<show|active|sporkname> [value]",
			Action: func(c *cli.Context) error {
				if c.NArg() < 1 {
					return fmt.Errorf("command required")
				}
				var args []interface{}
				args = append(args, c.Args().Get(0))
				if c.NArg() > 1 {
					value, err := strconv.ParseInt(c.Args().Get(1), 10, 64)
					if err != nil {
						return fmt.Errorf("invalid spork value: %v", err)
					}
					args = append(args, value)
				}
				return executeRPC(c, "spork", args)
			},
		},
		{
			Name:      "settxfee",
			Usage:     "Set transaction fee per kilobyte",
			Category:  "Utility",
			ArgsUsage: "<amount>",
			Action: func(c *cli.Context) error {
				if c.NArg() != 1 {
					return fmt.Errorf("amount required")
				}
				amount, err := strconv.ParseFloat(c.Args().First(), 64)
				if err != nil {
					return fmt.Errorf("invalid amount: %v", err)
				}
				return executeRPC(c, "settxfee", []interface{}{amount})
			},
		},
		{
			Name:      "rpchelp",
			Usage:     "Get RPC command help from daemon",
			Category:  "Utility",
			ArgsUsage: "[command]",
			Action: func(c *cli.Context) error {
				var args []interface{}
				if c.NArg() > 0 {
					args = []interface{}{c.Args().First()}
				}
				return executeRPC(c, "help", args)
			},
		},
		{
			Name:  "version",
			Usage: "Show version information",
			Action: func(c *cli.Context) error {
				twinslib.PrintVersion()
				return nil
			},
		},
		{
			Name:      "help",
			Usage:     "Show help for a command",
			ArgsUsage: "[command]",
			Action: func(c *cli.Context) error {
				if c.NArg() == 0 {
					cli.ShowAppHelpAndExit(c, 0)
				}
				cli.ShowCommandHelpAndExit(c, c.Args().First(), 0)
				return nil // This will never be reached due to Exit calls above
			},
		},
	}

	// Propagate app-level flags (--config, --datadir, --rpc-host, etc.) to all
	// subcommands so they work regardless of position. Without this, urfave/cli
	// rejects app flags placed after the subcommand name (e.g.,
	// "twins-cli getsyncstatus --config=/path" would fail).
	twinslib.PropagateAppFlags(app)

	if err := app.Run(os.Args); err != nil {
		logrus.WithError(err).Fatal("RPC client failed")
	}
}

// RPCRequest represents a JSON-RPC 2.0 request
type RPCRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params,omitempty"`
	ID      int           `json:"id"`
}

// RPCResponse represents a JSON-RPC 2.0 response
type RPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	Result  interface{} `json:"result"`
	Error   *RPCError   `json:"error"`
	ID      int         `json:"id"`
}

// RPCError represents a JSON-RPC error
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("RPC error %d: %s", e.Code, e.Message)
}

// twinsdYAMLConfig is a minimal struct to parse RPC settings from twinsd.yml
type twinsdYAMLConfig struct {
	RPC struct {
		Username string `yaml:"username"`
		Password string `yaml:"password"`
		Host     string `yaml:"host"`
		Port     int    `yaml:"port"`
	} `yaml:"rpc"`
}

// parseTwinsdYAML reads all RPC settings from a YAML config file in one pass.
func parseTwinsdYAML(yamlPath string) (*twinsdYAMLConfig, error) {
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		return nil, err
	}

	var cfg twinsdYAMLConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// parseYAMLConfig reads RPC credentials from a YAML config file
func parseYAMLConfig(yamlPath string) (username, password string, err error) {
	cfg, err := parseTwinsdYAML(yamlPath)
	if err != nil {
		return "", "", err
	}
	return cfg.RPC.Username, cfg.RPC.Password, nil
}

// parseConfFile reads rpcuser and rpcpassword from a legacy .conf file
func parseConfFile(confPath string) (username, password string, err error) {
	file, err := os.Open(confPath)
	if err != nil {
		return "", "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Skip comments and empty lines
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse key=value
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "rpcuser":
			username = value
		case "rpcpassword":
			password = value
		}
	}

	if err := scanner.Err(); err != nil {
		return "", "", err
	}

	return username, password, nil
}

// parseCookieFile reads RPC credentials from a cookie authentication file.
// Cookie format: __cookie__:hash
// Returns username "__cookie__" and the hash as password.
func parseCookieFile(cookiePath string) (username, password string, err error) {
	data, err := os.ReadFile(cookiePath)
	if err != nil {
		return "", "", err
	}

	content := strings.TrimSpace(string(data))
	parts := strings.SplitN(content, ":", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid cookie format: expected '__cookie__:hash'")
	}

	// Validate that username is the expected cookie identifier
	if parts[0] != "__cookie__" {
		return "", "", fmt.Errorf("invalid cookie format: unexpected username '%s', expected '__cookie__'", parts[0])
	}

	return parts[0], parts[1], nil
}

// getRPCCredentials gets RPC credentials with priority:
// 1. CLI flags (--rpc-user, --rpc-password)
// 2. Config file (twinsd.yml or twins.conf)
// 3. Cookie authentication (.cookie file in datadir)
func getRPCCredentials(c *cli.Context) (username, password string) {
	// 1. Check CLI flags first (highest priority)
	// Use GetStringFromLineage to walk parent contexts — PropagateAppFlags
	// copies flag definitions to subcommands, causing c.String() to return
	// the empty default instead of the app-level value when flags are placed
	// before the subcommand name (e.g., "twins-cli --rpc-user=x getinfo").
	username = twinslib.GetStringFromLineage(c, "rpc-user")
	password = twinslib.GetStringFromLineage(c, "rpc-password")
	if username != "" && password != "" {
		return username, password
	}

	// 2. Use shared config discovery logic (same as twinsd)
	configPath, explicitConfig := twinslib.GetEffectiveConfigPath(c)

	if configPath != "" {
		// Load credentials from config file
		lowerPath := strings.ToLower(configPath)
		if strings.HasSuffix(lowerPath, ".yml") || strings.HasSuffix(lowerPath, ".yaml") {
			if u, p, err := parseYAMLConfig(configPath); err == nil && u != "" && p != "" {
				return u, p
			}
		} else if strings.HasSuffix(lowerPath, ".conf") {
			if u, p, err := parseConfFile(configPath); err == nil && u != "" && p != "" {
				return u, p
			}
		}
	}

	// If explicit config was provided but no credentials found - don't fall back to cookie
	if explicitConfig {
		return "", ""
	}

	// 3. Try cookie authentication as final fallback
	dataDir := twinslib.GetDataDir(c)
	if dataDir != "" {
		cookiePath := filepath.Join(dataDir, ".cookie")
		if u, p, err := parseCookieFile(cookiePath); err == nil && u != "" && p != "" {
			return u, p
		}
	}

	return "", ""
}

// getRPCEndpoint returns RPC host and port with priority:
// 1. CLI flags (if explicitly set)
// 2. Config file (twinsd.yml or twins.conf)
// 3. Default values (127.0.0.1:37818)
func getRPCEndpoint(c *cli.Context) (host string, port int) {
	// Default values
	host = "127.0.0.1"
	port = types.DefaultRPCPort

	// Try config file first (lowest priority, will be overridden by CLI)
	configPath, _ := twinslib.GetEffectiveConfigPath(c)
	if configPath != "" {
		lowerPath := strings.ToLower(configPath)
		if strings.HasSuffix(lowerPath, ".yml") || strings.HasSuffix(lowerPath, ".yaml") {
			if h, p, err := parseYAMLConfigRPC(configPath); err == nil {
				if h != "" {
					// Server bind "0.0.0.0" means "all interfaces" - client should use localhost
					if h == "0.0.0.0" {
						host = "127.0.0.1"
					} else {
						host = h
					}
				}
				if p > 0 {
					port = p
				}
			}
		}
	}

	// CLI flags override config (highest priority)
	// Use lineage-aware IsSet so flags before subcommand name are detected.
	if twinslib.IsSetInLineage(c, "rpc-host") {
		host = twinslib.GetStringFromLineage(c, "rpc-host")
	}
	if twinslib.IsSetInLineage(c, "rpc-port") {
		port = twinslib.GetIntFromLineage(c, "rpc-port")
	}

	return host, port
}

// parseYAMLConfigRPC parses YAML config for RPC host/port
func parseYAMLConfigRPC(path string) (host string, port int, err error) {
	cfg, err := parseTwinsdYAML(path)
	if err != nil {
		return "", 0, err
	}
	return cfg.RPC.Host, cfg.RPC.Port, nil
}

// isValidHex checks if a string is valid hexadecimal using standard library
func isValidHex(s string) bool {
	if len(s) == 0 || len(s)%2 != 0 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// executeRPC executes an RPC call and prints the result
func executeRPC(c *cli.Context, method string, params []interface{}) error {
	// Build JSON-RPC 2.0 request
	req := RPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      1,
	}

	// Marshal request
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	// Build URL
	scheme := "http"
	if twinslib.GetBoolFromLineage(c, "rpc-tls") {
		scheme = "https"
	}
	host, port := getRPCEndpoint(c)
	url := fmt.Sprintf("%s://%s:%d", scheme, host, port)

	// Create HTTP request (without context for simplicity)
	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(reqJSON))
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set headers
	httpReq.Header.Set("Content-Type", "application/json")

	// Get credentials with priority: CLI flags > --config > auto-discover (twinsd.yml > twins.conf)
	username, password := getRPCCredentials(c)

	// Add Basic Auth if credentials available
	if username != "" && password != "" {
		httpReq.SetBasicAuth(username, password)
	}

	// Configure HTTP client with timeout
	timeout := twinslib.GetDurationFromLineage(c, "rpc-timeout")
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	client := &http.Client{
		Timeout: timeout,
	}

	// Configure transport with timeouts
	transport := &http.Transport{
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: timeout,
	}

	// Configure TLS if enabled
	if twinslib.GetBoolFromLineage(c, "rpc-tls") {
		tlsConfig := &tls.Config{}

		// Load custom certificate if provided
		certPath := twinslib.GetStringFromLineage(c, "rpc-cert")
		if certPath != "" {
			certPool := x509.NewCertPool()
			certPEM, err := os.ReadFile(certPath)
			if err != nil {
				return fmt.Errorf("failed to read certificate: %w", err)
			}
			if !certPool.AppendCertsFromPEM(certPEM) {
				return fmt.Errorf("failed to append certificate")
			}
			tlsConfig.RootCAs = certPool
		}

		transport.TLSClientConfig = tlsConfig
	}

	client.Transport = transport

	// Execute request
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("RPC request failed: %w", err)
	}
	defer httpResp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	// Check HTTP status
	if httpResp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP error %d: %s", httpResp.StatusCode, string(respBody))
	}

	// Parse JSON-RPC response
	var rpcResp RPCResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	// Check for RPC error
	if rpcResp.Error != nil {
		return rpcResp.Error
	}

	// Handle null result (like legacy C++ client - print nothing)
	if rpcResp.Result == nil {
		return nil
	}

	// Handle simple string results without JSON encoding
	if strResult, ok := rpcResp.Result.(string); ok {
		fmt.Println(strResult)
		return nil
	}

	// Pretty print result for complex objects
	resultJSON, err := json.MarshalIndent(rpcResp.Result, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to format result: %w", err)
	}

	fmt.Println(string(resultJSON))
	return nil
}
