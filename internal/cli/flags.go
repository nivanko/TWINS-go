package cli

import (
	"runtime"
	"time"

	"github.com/urfave/cli/v2"
)

// cpuAwareDefault returns lowCPU value on single-core machines, normal otherwise.
func cpuAwareDefault(normal, lowCPU int) int {
	if runtime.NumCPU() <= 1 {
		return lowCPU
	}
	return normal
}

// CommonDaemonFlags returns flags common to daemon applications
func CommonDaemonFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:    "bind",
			Aliases: []string{"b"},
			Value:   "127.0.0.1:37818",
			Usage:   "Bind address for RPC server",
			EnvVars: []string{"TWINS_RPC_BIND"},
		},
		&cli.IntFlag{
			Name:    "rpc-port",
			Value:   37818,
			Usage:   "RPC server port",
			EnvVars: []string{"TWINS_RPC_PORT"},
		},
		&cli.StringFlag{
			Name:    "rpc-user",
			Usage:   "RPC username for authentication",
			EnvVars: []string{"TWINS_RPC_USER"},
		},
		&cli.StringFlag{
			Name:    "rpc-password",
			Usage:   "RPC password for authentication",
			EnvVars: []string{"TWINS_RPC_PASSWORD"},
		},
		&cli.StringFlag{
			Name:    "p2p-bind",
			Value:   "0.0.0.0:37817",
			Usage:   "P2P network bind address",
			EnvVars: []string{"TWINS_P2P_BIND"},
		},
		&cli.IntFlag{
			Name:    "p2p-port",
			Value:   37817,
			Usage:   "P2P network port",
			EnvVars: []string{"TWINS_P2P_PORT"},
		},
		&cli.StringSliceFlag{
			Name:    "peers",
			Aliases: []string{"p"},
			Usage:   "Connect to specific peers (can be used multiple times)",
			EnvVars: []string{"TWINS_PEERS"},
		},
		&cli.BoolFlag{
			Name:    "daemon",
			Usage:   "Run as daemon (background process)",
			EnvVars: []string{"TWINS_DAEMON"},
		},
		&cli.BoolFlag{
			Name:    "staking",
			Value:   false,
			Usage:   "Enable staking (Proof-of-Stake mining)",
			EnvVars: []string{"TWINS_STAKING"},
		},
		&cli.Int64Flag{
			Name:    "reservebalance",
			Value:   0,
			Usage:   "Keep the specified amount (in satoshis) available for spending, not used for staking",
			EnvVars: []string{"TWINS_RESERVE_BALANCE"},
		},
		&cli.BoolFlag{
			Name:    "masternode",
			Value:   false,
			Usage:   "Enable masternode functionality",
			EnvVars: []string{"TWINS_MASTERNODE"},
		},
		&cli.StringFlag{
			Name:    "masternode-key",
			Usage:   "Masternode private key (for masternode operation)",
			EnvVars: []string{"TWINS_MASTERNODE_KEY"},
		},
		&cli.BoolFlag{
			Name:    "disablewallet",
			Value:   false,
			Usage:   "Disable wallet functionality (for pure relay/validation nodes)",
			EnvVars: []string{"TWINS_DISABLE_WALLET"},
		},

		// === RPC TLS Flags ===
		&cli.BoolFlag{
			Name:    "rpc-tls-enabled",
			Value:   false,
			Usage:   "Enable TLS encryption on the RPC listener",
			EnvVars: []string{"TWINS_RPC_TLS_ENABLED"},
		},
		&cli.StringFlag{
			Name:    "rpc-tls-cert",
			Usage:   "Path to TLS certificate file for RPC server",
			EnvVars: []string{"TWINS_RPC_TLS_CERT"},
		},
		&cli.StringFlag{
			Name:    "rpc-tls-key",
			Usage:   "Path to TLS private key file for RPC server",
			EnvVars: []string{"TWINS_RPC_TLS_KEY"},
		},
		&cli.IntFlag{
			Name:    "rpc-tls-expiry-warn-days",
			Value:   30,
			Usage:   "Days before certificate expiry to start warnings",
			EnvVars: []string{"TWINS_RPC_TLS_EXPIRY_WARN_DAYS"},
		},
		&cli.StringFlag{
			Name:    "rpc-tls-reload-passphrase-file",
			Usage:   "Path to argon2id hash file for reloadrpccerts RPC",
			EnvVars: []string{"TWINS_RPC_TLS_RELOAD_PASSPHRASE_FILE"},
		},
		&cli.BoolFlag{
			Name:    "rpc-tls-mtls-enabled",
			Value:   false,
			Usage:   "Require client certificates for RPC connections (mTLS)",
			EnvVars: []string{"TWINS_RPC_TLS_MTLS_ENABLED"},
		},
		&cli.StringFlag{
			Name:    "rpc-tls-mtls-client-ca",
			Usage:   "Path to client CA bundle for mTLS verification",
			EnvVars: []string{"TWINS_RPC_TLS_MTLS_CLIENT_CA"},
		},
		&cli.BoolFlag{
			Name:    "rpc-allow-plaintext-public",
			Value:   false,
			Usage:   "Allow unencrypted RPC on non-loopback (requires rpc.allowPlaintextPublic in YAML too)",
			EnvVars: []string{"TWINS_RPC_ALLOW_PLAINTEXT_PUBLIC"},
		},

		// === Legacy RPC SSL flags (rejected at startup) ===
		// Hidden so they don't appear in --help but c.IsSet() detects them.
		&cli.BoolFlag{Name: "rpcssl", Hidden: true},
		&cli.StringFlag{Name: "rpcsslcertificatechainfile", Hidden: true},
		&cli.StringFlag{Name: "rpcsslprivatekeyfile", Hidden: true},
		&cli.StringFlag{Name: "rpcsslciphers", Hidden: true},
	}
}

// CommonRPCClientFlags returns flags common to RPC client applications
func CommonRPCClientFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:    "rpc-host",
			Value:   "127.0.0.1",
			Usage:   "RPC server host",
			EnvVars: []string{"TWINS_RPC_HOST"},
		},
		&cli.IntFlag{
			Name:    "rpc-port",
			Value:   37818,
			Usage:   "RPC server port",
			EnvVars: []string{"TWINS_RPC_PORT"},
		},
		&cli.StringFlag{
			Name:    "rpc-user",
			Usage:   "RPC username for authentication",
			EnvVars: []string{"TWINS_RPC_USER"},
		},
		&cli.StringFlag{
			Name:    "rpc-password",
			Usage:   "RPC password for authentication",
			EnvVars: []string{"TWINS_RPC_PASSWORD"},
		},
		&cli.DurationFlag{
			Name:    "rpc-timeout",
			Value:   30 * time.Second,
			Usage:   "RPC request timeout",
			EnvVars: []string{"TWINS_RPC_TIMEOUT"},
		},
		&cli.BoolFlag{
			Name:    "rpc-tls",
			Value:   false,
			Usage:   "Use TLS for RPC connections",
			EnvVars: []string{"TWINS_RPC_TLS"},
		},
		&cli.StringFlag{
			Name:    "rpc-tls-ca",
			Usage:   "Path to custom CA bundle for RPC server verification",
			EnvVars: []string{"TWINS_RPC_TLS_CA"},
		},
		&cli.StringFlag{
			Name:    "rpc-tls-pin",
			Usage:   "SPKI SHA-256 hash pin for certificate pinning (use rpc-cert-fingerprint to compute)",
			EnvVars: []string{"TWINS_RPC_TLS_PIN"},
		},
	}
}

// CommonWalletFlags returns flags common to wallet applications
func CommonWalletFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:    "wallet-dir",
			Usage:   "Wallet directory path",
			EnvVars: []string{"TWINS_WALLET_DIR"},
		},
		&cli.StringFlag{
			Name:    "wallet-name",
			Aliases: []string{"w"},
			Value:   "default",
			Usage:   "Wallet name to use",
			EnvVars: []string{"TWINS_WALLET_NAME"},
		},
		&cli.BoolFlag{
			Name:    "wallet-create",
			Usage:   "Create wallet if it doesn't exist",
			EnvVars: []string{"TWINS_WALLET_CREATE"},
		},
		&cli.StringFlag{
			Name:    "wallet-passphrase",
			Usage:   "Wallet encryption passphrase",
			EnvVars: []string{"TWINS_WALLET_PASSPHRASE"},
		},
		&cli.BoolFlag{
			Name:    "wallet-testnet",
			Value:   false,
			Usage:   "Use testnet address format",
			EnvVars: []string{"TWINS_WALLET_TESTNET"},
		},
		// === Fee Configuration (Legacy C++ Compatible) ===
		&cli.Int64Flag{
			Name:    "paytxfee",
			Value:   0,
			Usage:   "Fee per kB to add to transactions (in satoshis)",
			EnvVars: []string{"TWINS_PAYTXFEE"},
		},
		&cli.Int64Flag{
			Name:    "mintxfee",
			Value:   10000,
			Usage:   "Minimum transaction fee threshold (in satoshis)",
			EnvVars: []string{"TWINS_MINTXFEE"},
		},
		&cli.Int64Flag{
			Name:    "maxtxfee",
			Value:   100000000,
			Usage:   "Maximum total transaction fee (in satoshis, default 1 TWINS)",
			EnvVars: []string{"TWINS_MAXTXFEE"},
		},
		&cli.IntFlag{
			Name:    "txconfirmtarget",
			Value:   1,
			Usage:   "Target confirmations for fee estimation",
			EnvVars: []string{"TWINS_TXCONFIRMTARGET"},
		},
		// === Wallet Management (Legacy C++ Compatible) ===
		&cli.IntFlag{
			Name:    "keypool",
			Value:   1000,
			Usage:   "Set key pool size",
			EnvVars: []string{"TWINS_KEYPOOL"},
		},
		&cli.BoolFlag{
			Name:    "spendzeroconfchange",
			Value:   false,
			Usage:   "Spend unconfirmed change when sending transactions",
			EnvVars: []string{"TWINS_SPENDZEROCONFCHANGE"},
		},
		&cli.IntFlag{
			Name:    "createwalletbackups",
			Value:   10,
			Usage:   "Number of automatic wallet backups (0 to disable)",
			EnvVars: []string{"TWINS_CREATEWALLETBACKUPS"},
		},
		&cli.StringFlag{
			Name:    "backuppath",
			Usage:   "Custom wallet backup directory",
			EnvVars: []string{"TWINS_BACKUPPATH"},
		},
		// === HD Wallet Creation (Legacy C++ Compatible) ===
		&cli.StringFlag{
			Name:    "mnemonic",
			Usage:   "BIP39 mnemonic phrase for wallet creation",
			EnvVars: []string{"TWINS_MNEMONIC"},
		},
		&cli.StringFlag{
			Name:    "mnemonicpassphrase",
			Usage:   "Optional passphrase for mnemonic",
			EnvVars: []string{"TWINS_MNEMONICPASSPHRASE"},
		},
		&cli.StringFlag{
			Name:    "hdseed",
			Usage:   "HD seed in hex format for wallet creation",
			EnvVars: []string{"TWINS_HDSEED"},
		},
	}
}

// DatabaseFlags returns database-related flags
func DatabaseFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:    "db-type",
			Value:   "pebble",
			Usage:   "Database type (pebble)",
			EnvVars: []string{"TWINS_DB_TYPE"},
		},
		&cli.StringFlag{
			Name:    "db-path",
			Usage:   "Database file path (defaults to datadir/blockchain.db)",
			EnvVars: []string{"TWINS_DB_PATH"},
		},
		&cli.IntFlag{
			Name:    "db-cache",
			Value:   cpuAwareDefault(256, 128),
			Usage:   "Database cache size in MB",
			EnvVars: []string{"TWINS_DB_CACHE"},
		},
		&cli.IntFlag{
			Name:    "db-handles",
			Value:   1024,
			Usage:   "Database file handles limit",
			EnvVars: []string{"TWINS_DB_HANDLES"},
		},
		&cli.BoolFlag{
			Name:    "db-sync",
			Value:   true,
			Usage:   "Enable database synchronous writes",
			EnvVars: []string{"TWINS_DB_SYNC"},
		},
		&cli.BoolFlag{
			Name:    "reindex",
			Value:   false,
			Usage:   "Rebuild block chain index from blk*.dat files",
			EnvVars: []string{"TWINS_REINDEX"},
		},
	}
}

// PerformanceFlags returns performance tuning flags
func PerformanceFlags() []cli.Flag {
	return []cli.Flag{
		&cli.IntFlag{
			Name:    "max-peers",
			Value:   125,
			Usage:   "Maximum number of peer connections",
			EnvVars: []string{"TWINS_MAX_PEERS"},
		},
		&cli.IntFlag{
			Name:    "workers",
			Value:   cpuAwareDefault(4, 2),
			Usage:   "Number of worker goroutines",
			EnvVars: []string{"TWINS_WORKERS"},
		},
		&cli.IntFlag{
			Name:    "validation-workers",
			Value:   cpuAwareDefault(2, 1),
			Usage:   "Number of block validation workers",
			EnvVars: []string{"TWINS_VALIDATION_WORKERS"},
		},
		&cli.IntFlag{
			Name:    "mempool-size",
			Value:   5000,
			Usage:   "Maximum transactions in mempool",
			EnvVars: []string{"TWINS_MEMPOOL_SIZE"},
		},
		&cli.DurationFlag{
			Name:    "block-time",
			Value:   60,
			Usage:   "Target block time in seconds",
			EnvVars: []string{"TWINS_BLOCK_TIME"},
		},
		&cli.BoolFlag{
			Name:    "pprof",
			Value:   false,
			Usage:   "Enable pprof HTTP server for profiling (memory, CPU, goroutines)",
			EnvVars: []string{"TWINS_PPROF"},
		},
		&cli.StringFlag{
			Name:    "pprof-addr",
			Value:   "localhost:6060",
			Usage:   "Address for pprof HTTP server",
			EnvVars: []string{"TWINS_PPROF_ADDR"},
		},
	}
}