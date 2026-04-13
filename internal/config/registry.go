package config

import (
	"fmt"
	"path/filepath"
)

// registerAllSettings populates the ConfigManager with metadata for every daemon setting.
// Each registration includes typed getter/setter lambdas that access Config struct fields directly.
func registerAllSettings(cm *ConfigManager) {
	// ==================== Staking ====================
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "staking.enabled", Type: TypeBool, Default: false,
			Category: "staking", Label: "Enable Staking",
			Description: "Stake coins to earn Proof-of-Stake rewards",
			HotReload:   true, CLIFlag: "staking", EnvVar: "TWINS_STAKING",
		},
		getter: func(c *Config) interface{} { return c.Staking.Enabled },
		setter: func(c *Config, v interface{}) error { c.Staking.Enabled = v.(bool); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "staking.reserveBalance", Type: TypeInt64, Default: int64(0),
			Category: "staking", Label: "Reserve Balance",
			Description: "Amount in satoshis to keep available for spending, not used for staking",
			HotReload:   true, CLIFlag: "reservebalance", EnvVar: "TWINS_RESERVE_BALANCE",
			Units: "satoshis", Validation: minOnly(0),
		},
		getter: func(c *Config) interface{} { return c.Staking.ReserveBalance },
		setter: func(c *Config, v interface{}) error { c.Staking.ReserveBalance = v.(int64); return nil },
	})

	// ==================== Wallet ====================
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "wallet.enabled", Type: TypeBool, Default: true,
			Category: "wallet", Label: "Enable Wallet",
			Description: "Enable wallet functionality (disable for relay-only nodes)",
			HotReload:   false, CLIFlag: "disablewallet",
		},
		getter: func(c *Config) interface{} { return c.Wallet.Enabled },
		setter: func(c *Config, v interface{}) error { c.Wallet.Enabled = v.(bool); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "wallet.payTxFee", Type: TypeInt64, Default: int64(0),
			Category: "wallet", Label: "Transaction Fee",
			Description: "Fee per kB for transactions in satoshis (0 = use wallet default of 10000 sat/kB)",
			HotReload:   true, CLIFlag: "paytxfee",
			Units: "satoshis/kB", Validation: minOnly(0),
		},
		getter: func(c *Config) interface{} { return c.Wallet.PayTxFee },
		setter: func(c *Config, v interface{}) error { c.Wallet.PayTxFee = v.(int64); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "wallet.minTxFee", Type: TypeInt64, Default: int64(10000),
			Category: "wallet", Label: "Minimum Transaction Fee",
			Description: "Minimum fee threshold in satoshis per kB",
			HotReload:   true, CLIFlag: "mintxfee",
			Units: "satoshis/kB", Validation: minmax(0, 100000000),
		},
		getter: func(c *Config) interface{} { return c.Wallet.MinTxFee },
		setter: func(c *Config, v interface{}) error { c.Wallet.MinTxFee = v.(int64); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "wallet.maxTxFee", Type: TypeInt64, Default: int64(100000000),
			Category: "wallet", Label: "Maximum Transaction Fee",
			Description: "Maximum total fee allowed in satoshis (1 TWINS = 100,000,000)",
			HotReload:   true, CLIFlag: "maxtxfee",
			Units: "satoshis", Validation: minmax(0, 1000000000),
		},
		getter: func(c *Config) interface{} { return c.Wallet.MaxTxFee },
		setter: func(c *Config, v interface{}) error { c.Wallet.MaxTxFee = v.(int64); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "wallet.txConfirmTarget", Type: TypeInt, Default: 1,
			Category: "wallet", Label: "Confirmation Target",
			Description: "Target number of confirmations for fee estimation",
			HotReload:   true, CLIFlag: "txconfirmtarget",
			Units: "blocks", Validation: minmax(1, 25),
		},
		getter: func(c *Config) interface{} { return c.Wallet.TxConfirmTarget },
		setter: func(c *Config, v interface{}) error { c.Wallet.TxConfirmTarget = v.(int); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "wallet.keypool", Type: TypeInt, Default: 1000,
			Category: "wallet", Label: "Key Pool Size",
			Description: "Number of keys to pre-generate for the address pool",
			HotReload:   false, CLIFlag: "keypool",
			Validation: minmax(1, 100000),
		},
		getter: func(c *Config) interface{} { return c.Wallet.Keypool },
		setter: func(c *Config, v interface{}) error { c.Wallet.Keypool = v.(int); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "wallet.spendZeroConfChange", Type: TypeBool, Default: false,
			Category: "wallet", Label: "Spend Unconfirmed Change",
			Description: "Allow spending unconfirmed change outputs",
			HotReload:   true, CLIFlag: "spendzeroconfchange",
		},
		getter: func(c *Config) interface{} { return c.Wallet.SpendZeroConfChange },
		setter: func(c *Config, v interface{}) error { c.Wallet.SpendZeroConfChange = v.(bool); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "wallet.createWalletBackups", Type: TypeInt, Default: 10,
			Category: "wallet", Label: "Wallet Backups",
			Description: "Number of automatic wallet backups to keep (0 = disable)",
			HotReload:   true, CLIFlag: "createwalletbackups",
			Validation: minmax(0, 100),
		},
		getter: func(c *Config) interface{} { return c.Wallet.CreateWalletBackups },
		setter: func(c *Config, v interface{}) error { c.Wallet.CreateWalletBackups = v.(int); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "wallet.backupPath", Type: TypeString, Default: "",
			Category: "wallet", Label: "Backup Path",
			Description: "Custom directory for wallet backups (empty = default)",
			HotReload:   true, CLIFlag: "backuppath",
		},
		getter: func(c *Config) interface{} { return c.Wallet.BackupPath },
		setter: func(c *Config, v interface{}) error {
			p := v.(string)
			if p != "" && !filepath.IsAbs(p) {
				return fmt.Errorf("backupPath must be an absolute path or empty, got %q", p)
			}
			c.Wallet.BackupPath = p
			return nil
		},
	})

	// ==================== Auto-Combine Inputs ====================
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "wallet.autoCombine", Type: TypeBool, Default: false,
			Category: "wallet", Label: "Auto-Combine Inputs",
			Description: "Automatically consolidate small UTXOs into larger ones",
			HotReload:   true, CLIFlag: "autocombine",
		},
		getter: func(c *Config) interface{} { return c.Wallet.AutoCombine },
		setter: func(c *Config, v interface{}) error { c.Wallet.AutoCombine = v.(bool); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "wallet.autoCombineTarget", Type: TypeInt64, Default: int64(100000),
			Category: "wallet", Label: "Auto-Combine Target",
			Description: "Target amount in TWINS for UTXO consolidation (0 = disabled)",
			HotReload:   true, CLIFlag: "autocombine-target",
			Units: "TWINS", Validation: minOnly(0),
		},
		getter: func(c *Config) interface{} { return c.Wallet.AutoCombineTarget },
		setter: func(c *Config, v interface{}) error { c.Wallet.AutoCombineTarget = v.(int64); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "wallet.autoCombineCooldown", Type: TypeInt, Default: 600,
			Category: "wallet", Label: "Auto-Combine Cooldown",
			Description: "Minimum seconds between consolidation cycles (60-3600)",
			HotReload:   true, CLIFlag: "autocombine-cooldown",
			Units: "seconds", Validation: minmax(60, 3600),
		},
		getter: func(c *Config) interface{} { return c.Wallet.AutoCombineCooldown },
		setter: func(c *Config, v interface{}) error { c.Wallet.AutoCombineCooldown = v.(int); return nil },
	})

	// ==================== Stake Split Threshold ====================
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "staking.stakeSplitThreshold", Type: TypeInt64, Default: int64(200000),
			Category: "staking", Label: "Stake Split Threshold",
			Description: "Split staking outputs when reward/2 exceeds this amount in TWINS (default: 200000)",
			HotReload:   true, CLIFlag: "stakesplitthreshold",
			Units: "TWINS", Validation: minOnly(0),
		},
		getter: func(c *Config) interface{} { return c.Staking.StakeSplitThreshold },
		setter: func(c *Config, v interface{}) error { c.Staking.StakeSplitThreshold = v.(int64); return nil },
	})

	// ==================== Network ====================
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "network.port", Type: TypeInt, Default: MainnetP2PPort,
			Category: "network", Label: "P2P Port",
			Description: "Port for peer-to-peer connections",
			HotReload:   false, CLIFlag: "p2p-port", EnvVar: "TWINS_NETWORK_PORT",
			Validation: minmax(1024, 65535),
		},
		getter: func(c *Config) interface{} { return c.Network.Port },
		setter: func(c *Config, v interface{}) error { c.Network.Port = v.(int); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "network.listenAddr", Type: TypeString, Default: "0.0.0.0",
			Category: "network", Label: "Listen Address",
			Description: "IP address to bind for P2P connections",
			HotReload:   false, CLIFlag: "p2p-bind", EnvVar: "TWINS_LISTEN_ADDR",
		},
		getter: func(c *Config) interface{} { return c.Network.ListenAddr },
		setter: func(c *Config, v interface{}) error { c.Network.ListenAddr = v.(string); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "network.listen", Type: TypeBool, Default: true,
			Category: "network", Label: "Accept Connections",
			Description: "Listen for incoming P2P connections",
			HotReload:   false, EnvVar: "TWINS_NETWORK_LISTEN",
		},
		getter: func(c *Config) interface{} { return c.Network.Listen },
		setter: func(c *Config, v interface{}) error { c.Network.Listen = v.(bool); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "network.maxPeers", Type: TypeInt, Default: 125,
			Category: "network", Label: "Max Peers",
			Description: "Maximum number of peer connections",
			HotReload:   false, EnvVar: "TWINS_NETWORK_MAX_PEERS",
			Validation: minmax(1, 10000),
		},
		getter: func(c *Config) interface{} { return c.Network.MaxPeers },
		setter: func(c *Config, v interface{}) error { c.Network.MaxPeers = v.(int); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "network.maxOutboundPeers", Type: TypeInt, Default: 16,
			Category: "network", Label: "Max Outbound Peers",
			Description: "Maximum number of outbound connections",
			HotReload:   false,
			Validation:  minmax(1, 1000),
		},
		getter: func(c *Config) interface{} { return c.Network.MaxOutboundPeers },
		setter: func(c *Config, v interface{}) error { c.Network.MaxOutboundPeers = v.(int); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "network.upnp", Type: TypeBool, Default: true,
			Category: "network", Label: "UPnP",
			Description: "Use UPnP to map the listening port",
			HotReload:   false, EnvVar: "TWINS_NETWORK_UPNP",
		},
		getter: func(c *Config) interface{} { return c.Network.UPnP },
		setter: func(c *Config, v interface{}) error { c.Network.UPnP = v.(bool); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "network.proxy", Type: TypeString, Default: "",
			Category: "network", Label: "SOCKS5 Proxy",
			Description: "SOCKS5 proxy for all connections (host:port)",
			HotReload:   false, EnvVar: "TWINS_NETWORK_PROXY",
		},
		getter: func(c *Config) interface{} { return c.Network.Proxy },
		setter: func(c *Config, v interface{}) error { c.Network.Proxy = v.(string); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "network.externalIP", Type: TypeString, Default: "",
			Category: "network", Label: "External IP",
			Description: "External IP address for peer advertising",
			HotReload:   false, EnvVar: "TWINS_EXTERNAL_IP",
		},
		getter: func(c *Config) interface{} { return c.Network.ExternalIP },
		setter: func(c *Config, v interface{}) error { c.Network.ExternalIP = v.(string); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "network.seeds", Type: TypeStringSlice,
			Default:  GetDefaultSeeds(NetworkMainnet),
			Category: "network", Label: "Seed Nodes",
			Description: "DNS seed nodes for peer discovery",
			HotReload:   false, EnvVar: "TWINS_NETWORK_SEEDS",
		},
		getter: func(c *Config) interface{} { return c.Network.Seeds },
		setter: func(c *Config, v interface{}) error { c.Network.Seeds = v.([]string); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "network.addNodes", Type: TypeStringSlice, Default: []string{},
			Category: "network", Label: "Add Nodes",
			Description: "Additional nodes to connect to and keep connections open",
			HotReload:   false, EnvVar: "TWINS_NETWORK_ADD_NODES",
		},
		getter: func(c *Config) interface{} { return c.Network.AddNodes },
		setter: func(c *Config, v interface{}) error { c.Network.AddNodes = v.([]string); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "network.connectOnly", Type: TypeStringSlice, Default: []string{},
			Category: "network", Label: "Connect Only",
			Description: "Connect ONLY to these nodes (disables peer discovery)",
			HotReload:   false,
		},
		getter: func(c *Config) interface{} { return c.Network.ConnectOnly },
		setter: func(c *Config, v interface{}) error { c.Network.ConnectOnly = v.([]string); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "network.banScore", Type: TypeInt, Default: 100,
			Category: "network", Label: "Ban Score Threshold",
			Description: "Misbehavior score threshold before banning a peer",
			HotReload:   false, EnvVar: "TWINS_NETWORK_BAN_SCORE",
			Validation: minmax(1, 10000),
		},
		getter: func(c *Config) interface{} { return c.Network.BanScore },
		setter: func(c *Config, v interface{}) error { c.Network.BanScore = v.(int); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "network.banTime", Type: TypeInt, Default: 86400,
			Category: "network", Label: "Ban Duration",
			Description: "Duration of peer bans in seconds (default: 24 hours)",
			HotReload:   false, EnvVar: "TWINS_NETWORK_BAN_TIME",
			Units: "seconds", Validation: minmax(60, 31536000),
		},
		getter: func(c *Config) interface{} { return c.Network.BanTime },
		setter: func(c *Config, v interface{}) error { c.Network.BanTime = v.(int); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "network.timeout", Type: TypeInt, Default: 5,
			Category: "network", Label: "Connection Timeout",
			Description: "Connection timeout in seconds",
			HotReload:   false,
			Units:       "seconds", Validation: minOnly(1),
		},
		getter: func(c *Config) interface{} { return c.Network.Timeout },
		setter: func(c *Config, v interface{}) error { c.Network.Timeout = v.(int); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "network.keepAlive", Type: TypeInt, Default: 120,
			Category: "network", Label: "Keep-Alive Interval",
			Description: "Ping interval in seconds to keep connections alive",
			HotReload:   false,
			Units:       "seconds", Validation: minOnly(1),
		},
		getter: func(c *Config) interface{} { return c.Network.KeepAlive },
		setter: func(c *Config, v interface{}) error { c.Network.KeepAlive = v.(int); return nil },
	})

	// ==================== RPC ====================
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "rpc.enabled", Type: TypeBool, Default: true,
			Category: "rpc", Label: "Enable RPC Server",
			Description: "Enable the JSON-RPC server for remote control",
			HotReload:   false, EnvVar: "TWINS_RPC_ENABLED",
		},
		getter: func(c *Config) interface{} { return c.RPC.Enabled },
		setter: func(c *Config, v interface{}) error { c.RPC.Enabled = v.(bool); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "rpc.host", Type: TypeString, Default: "127.0.0.1",
			Category: "rpc", Label: "RPC Host",
			Description: "IP address to bind the RPC server",
			HotReload:   false, CLIFlag: "bind", EnvVar: "TWINS_RPC_HOST",
		},
		getter: func(c *Config) interface{} { return c.RPC.Host },
		setter: func(c *Config, v interface{}) error { c.RPC.Host = v.(string); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "rpc.port", Type: TypeInt, Default: MainnetRPCPort,
			Category: "rpc", Label: "RPC Port",
			Description: "Port for the JSON-RPC server",
			HotReload:   false, CLIFlag: "rpc-port", EnvVar: "TWINS_RPC_PORT",
			Validation: minmax(1024, 65535),
		},
		getter: func(c *Config) interface{} { return c.RPC.Port },
		setter: func(c *Config, v interface{}) error { c.RPC.Port = v.(int); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "rpc.username", Type: TypeString, Default: "",
			Category: "rpc", Label: "RPC Username",
			Description: "Username for RPC authentication (empty = cookie auth)",
			HotReload:   false, CLIFlag: "rpc-user", EnvVar: "TWINS_RPC_USERNAME",
		},
		getter: func(c *Config) interface{} { return c.RPC.Username },
		setter: func(c *Config, v interface{}) error { c.RPC.Username = v.(string); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "rpc.password", Type: TypeString, Default: "",
			Category: "rpc", Label: "RPC Password",
			Description: "Password for RPC authentication (empty = cookie auth)",
			HotReload:   false, CLIFlag: "rpc-password", EnvVar: "TWINS_RPC_PASSWORD",
		},
		getter: func(c *Config) interface{} { return c.RPC.Password },
		setter: func(c *Config, v interface{}) error { c.RPC.Password = v.(string); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "rpc.maxClients", Type: TypeInt, Default: 100,
			Category: "rpc", Label: "Max RPC Clients",
			Description: "Maximum concurrent RPC client connections",
			HotReload:   false, EnvVar: "TWINS_RPC_MAX_CLIENTS",
			Validation: minmax(1, 10000),
		},
		getter: func(c *Config) interface{} { return c.RPC.MaxClients },
		setter: func(c *Config, v interface{}) error { c.RPC.MaxClients = v.(int); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "rpc.rateLimit", Type: TypeInt, Default: 100,
			Category: "rpc", Label: "Rate Limit",
			Description: "Maximum RPC requests per minute per IP (0 = no limit)",
			HotReload:   false,
			Validation:  minmax(0, 10000),
		},
		getter: func(c *Config) interface{} { return c.RPC.RateLimit },
		setter: func(c *Config, v interface{}) error { c.RPC.RateLimit = v.(int); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "rpc.timeout", Type: TypeInt, Default: 30,
			Category: "rpc", Label: "Request Timeout",
			Description: "Timeout for individual RPC requests in seconds",
			HotReload:   false,
			Units:       "seconds", Validation: minmax(1, 300),
		},
		getter: func(c *Config) interface{} { return c.RPC.Timeout },
		setter: func(c *Config, v interface{}) error { c.RPC.Timeout = v.(int); return nil },
	})

	// ==================== Masternode ====================
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "masternode.enabled", Type: TypeBool, Default: false,
			Category: "masternode", Label: "Enable Masternode",
			Description: "Run this node as a masternode",
			HotReload:   false, CLIFlag: "masternode", EnvVar: "TWINS_MASTERNODE_ENABLED",
		},
		getter: func(c *Config) interface{} { return c.Masternode.Enabled },
		setter: func(c *Config, v interface{}) error { c.Masternode.Enabled = v.(bool); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "masternode.privateKey", Type: TypeString, Default: "",
			Category: "masternode", Label: "Private Key",
			Description: "Masternode private key for signing",
			HotReload:   false, EnvVar: "TWINS_MASTERNODE_PRIVATE_KEY",
		},
		getter: func(c *Config) interface{} { return c.Masternode.PrivateKey },
		setter: func(c *Config, v interface{}) error { c.Masternode.PrivateKey = v.(string); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "masternode.serviceAddr", Type: TypeString, Default: "",
			Category: "masternode", Label: "Service Address",
			Description: "External IP:port for masternode (e.g. 1.2.3.4:37817)",
			HotReload:   false, EnvVar: "TWINS_MASTERNODE_SERVICE_ADDR",
		},
		getter: func(c *Config) interface{} { return c.Masternode.ServiceAddr },
		setter: func(c *Config, v interface{}) error { c.Masternode.ServiceAddr = v.(string); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "masternode.mnConf", Type: TypeString, Default: "masternode.conf",
			Category: "masternode", Label: "Config File",
			Description: "Path to masternode.conf for remote masternode entries",
			HotReload:   false, EnvVar: "TWINS_MASTERNODE_MNCONF",
		},
		getter: func(c *Config) interface{} { return c.Masternode.MnConf },
		setter: func(c *Config, v interface{}) error { c.Masternode.MnConf = v.(string); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "masternode.mnConfLock", Type: TypeBool, Default: true,
			Category: "masternode", Label: "Lock Collateral UTXOs",
			Description: "Lock UTXOs used as masternode collateral",
			HotReload:   false, EnvVar: "TWINS_MASTERNODE_MNCONFLOCK",
		},
		getter: func(c *Config) interface{} { return c.Masternode.MnConfLock },
		setter: func(c *Config, v interface{}) error { c.Masternode.MnConfLock = v.(bool); return nil },
	})
	// Note: masternode.debug* entries intentionally have no CLIFlag.
	// Debug settings are config-file-only; they map to daemon.NodeConfig fields
	// (MasternodeDebug, MasternodeDebugMaxMB, MasternodeDebugMaxFiles) read once
	// at startup. No CLI flag equivalents exist or are planned.
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "masternode.debug", Type: TypeBool, Default: false,
			Category: "masternode", Label: "Debug Events",
			Description: "Enable masternode debug event collection to JSONL file",
			HotReload:   false, EnvVar: "TWINS_MASTERNODE_DEBUG",
		},
		getter: func(c *Config) interface{} { return c.Masternode.Debug },
		setter: func(c *Config, v interface{}) error { c.Masternode.Debug = v.(bool); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "masternode.debugMaxMB", Type: TypeInt, Default: 50,
			Category: "masternode", Label: "Debug Max File Size",
			Description: "Maximum JSONL debug file size before rotation",
			HotReload:   false, Units: "MB", Validation: minmax(1, 10000),
			EnvVar: "TWINS_MASTERNODE_DEBUG_MAX_MB",
		},
		getter: func(c *Config) interface{} { return c.Masternode.DebugMaxMB },
		setter: func(c *Config, v interface{}) error { c.Masternode.DebugMaxMB = v.(int); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "masternode.debugMaxFiles", Type: TypeInt, Default: 3,
			Category: "masternode", Label: "Debug Max Files",
			Description: "Maximum number of rotated debug files to keep",
			HotReload:   false, Validation: minmax(1, 100),
			EnvVar: "TWINS_MASTERNODE_DEBUG_MAX_FILES",
		},
		getter: func(c *Config) interface{} { return c.Masternode.DebugMaxFiles },
		setter: func(c *Config, v interface{}) error { c.Masternode.DebugMaxFiles = v.(int); return nil },
	})

	// ==================== Logging ====================
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "logging.level", Type: TypeString, Default: "error",
			Category: "logging", Label: "Log Level",
			Description: "Logging verbosity level",
			HotReload:   true, CLIFlag: "log-level", EnvVar: "TWINS_LOGGING_LEVEL",
			Validation: options("trace", "debug", "info", "warn", "error", "fatal"),
		},
		getter: func(c *Config) interface{} { return c.Logging.Level },
		setter: func(c *Config, v interface{}) error { c.Logging.Level = v.(string); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "logging.format", Type: TypeString, Default: "text",
			Category: "logging", Label: "Log Format",
			Description: "Log output format",
			HotReload:   true, EnvVar: "TWINS_LOGGING_FORMAT",
			Validation: options("text", "json"),
		},
		getter: func(c *Config) interface{} { return c.Logging.Format },
		setter: func(c *Config, v interface{}) error { c.Logging.Format = v.(string); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "logging.output", Type: TypeString, Default: "./twins.log",
			Category: "logging", Label: "Log Output",
			Description: "Log destination (stdout, stderr, or file path)",
			HotReload:   false, EnvVar: "TWINS_LOGGING_OUTPUT",
		},
		getter: func(c *Config) interface{} { return c.Logging.Output },
		setter: func(c *Config, v interface{}) error { c.Logging.Output = v.(string); return nil },
	})

	// ==================== Sync ====================
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "sync.bootstrapMinPeers", Type: TypeInt, Default: 4,
			Category: "sync", Label: "Bootstrap Min Peers",
			Description: "Minimum peers to connect before starting sync",
			HotReload:   false,
			Validation:  minmax(1, 100),
		},
		getter: func(c *Config) interface{} { return c.Sync.BootstrapMinPeers },
		setter: func(c *Config, v interface{}) error { c.Sync.BootstrapMinPeers = v.(int); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "sync.bootstrapMaxWait", Type: TypeInt, Default: 120,
			Category: "sync", Label: "Bootstrap Max Wait",
			Description: "Maximum wait time for peer connections before starting sync",
			HotReload:   false,
			Units:       "seconds", Validation: minmax(10, 600),
		},
		getter: func(c *Config) interface{} { return c.Sync.BootstrapMaxWait },
		setter: func(c *Config, v interface{}) error { c.Sync.BootstrapMaxWait = v.(int); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "sync.ibdThreshold", Type: TypeUint32, Default: uint32(5000),
			Category: "sync", Label: "IBD Threshold",
			Description: "Blocks behind to trigger Initial Block Download mode",
			HotReload:   false,
			Units:       "blocks", Validation: minmax(100, 1000000),
		},
		getter: func(c *Config) interface{} { return c.Sync.IBDThreshold },
		setter: func(c *Config, v interface{}) error { c.Sync.IBDThreshold = v.(uint32); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "sync.consensusStrategy", Type: TypeString, Default: "outbound_only",
			Category: "sync", Label: "Consensus Strategy",
			Description: "Peer consensus calculation strategy",
			HotReload:   false,
			Validation:  options("outbound_only", "all"),
		},
		getter: func(c *Config) interface{} { return c.Sync.ConsensusStrategy },
		setter: func(c *Config, v interface{}) error { c.Sync.ConsensusStrategy = v.(string); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "sync.maxSyncPeers", Type: TypeInt, Default: 20,
			Category: "sync", Label: "Max Sync Peers",
			Description: "Maximum peers in the sync rotation list",
			HotReload:   false,
			Validation:  minmax(5, 100),
		},
		getter: func(c *Config) interface{} { return c.Sync.MaxSyncPeers },
		setter: func(c *Config, v interface{}) error { c.Sync.MaxSyncPeers = v.(int); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "sync.batchTimeout", Type: TypeInt, Default: 60,
			Category: "sync", Label: "Batch Timeout",
			Description: "Rotate to next peer if no response within this duration",
			HotReload:   false,
			Units:       "seconds", Validation: minmax(10, 600),
		},
		getter: func(c *Config) interface{} { return c.Sync.BatchTimeout },
		setter: func(c *Config, v interface{}) error { c.Sync.BatchTimeout = v.(int); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "sync.reorgWindow", Type: TypeInt, Default: 3600,
			Category: "sync", Label: "Reorg Window",
			Description: "Time window for counting chain reorganizations",
			HotReload:   false,
			Units:       "seconds", Validation: minmax(60, 86400),
		},
		getter: func(c *Config) interface{} { return c.Sync.ReorgWindow },
		setter: func(c *Config, v interface{}) error { c.Sync.ReorgWindow = v.(int); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "sync.maxAutoReorgs", Type: TypeInt, Default: 1,
			Category: "sync", Label: "Max Auto Reorgs",
			Description: "Maximum automatic chain reorganizations before pausing",
			HotReload:   false,
			Validation:  minmax(0, 10),
		},
		getter: func(c *Config) interface{} { return c.Sync.MaxAutoReorgs },
		setter: func(c *Config, v interface{}) error { c.Sync.MaxAutoReorgs = v.(int); return nil },
	})
	cm.Register(&settingDef{
		SettingMeta: SettingMeta{
			Key: "sync.progressLogInterval", Type: TypeInt, Default: 10,
			Category: "sync", Label: "Progress Log Interval",
			Description: "Seconds between sync progress log messages",
			HotReload:   false,
			Units:       "seconds", Validation: minmax(1, 300),
		},
		getter: func(c *Config) interface{} { return c.Sync.ProgressLogInterval },
		setter: func(c *Config, v interface{}) error { c.Sync.ProgressLogInterval = v.(int); return nil },
	})
}
