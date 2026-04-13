package config

// Port constants for TWINS network ports.
const (
	MainnetP2PPort = 37817
	MainnetRPCPort = 37818
	TestnetP2PPort = 37819
	TestnetRPCPort = 37820
	RegtestP2PPort = 37821
	RegtestRPCPort = 37822
)

// DefaultConfig returns the default configuration.
// Network-specific settings (ports, seeds, etc.) should be set in the config file.
func DefaultConfig() *Config {
	config := &Config{
		Network: NetworkConfig{
			// Basic settings
			Port: MainnetP2PPort, // TWINS mainnet default port
			Seeds: GetDefaultSeeds(NetworkMainnet),
			MaxPeers:         125,
			MaxOutboundPeers: 16, // Default max outbound connections
			TestNet:          false,
			ListenAddr:       "0.0.0.0",
			ExternalIP:       "",
			Timeout:          5,   // Legacy: DEFAULT_CONNECT_TIMEOUT 5000ms
			KeepAlive:        120, // Legacy: PING_INTERVAL 120 seconds
			MaxBandwidth:     0,   // Unlimited

			// Core Connection Settings (Legacy C++ Compatible)
			Listen:   true,
			DNS:      true,
			DNSSeed:  true,
			Discover: true,

			// Peer Management (Legacy C++ Compatible)
			AddNodes:    []string{},
			SeedNodes:   []string{},
			ConnectOnly: []string{},

			// Ban Settings (Legacy C++ Compatible)
			BanScore: 100,   // Legacy: DEFAULT_BANSCORE_THRESHOLD
			BanTime:  86400, // Legacy: DEFAULT_MISBEHAVING_BANTIME (24 hours)

			// Proxy/Tor Settings (Legacy C++ Compatible)
			Proxy:          "",
			OnionProxy:     "",
			TorControl:     "127.0.0.1:9051",
			TorPassword:    "",
			ListenOnion:    true,
			ProxyRandomize: true,

			// UPnP Settings (Legacy C++ Compatible)
			UPnP: true,

			// Buffer Settings (Legacy C++ Compatible)
			MaxReceiveBuffer: 5000, // Legacy: DEFAULT_MAXRECEIVEBUFFER (x1000 bytes = 5MB)
			MaxSendBuffer:    1000, // Legacy: DEFAULT_MAXSENDBUFFER (x1000 bytes = 1MB)

			// Network Filtering (Legacy C++ Compatible)
			BindAddresses: []string{},
			WhiteBind:     []string{},
			Whitelist:     []string{},
			OnlyNet:       "",
		},
		RPC: RPCConfig{
			Enabled:    true,
			Port:       MainnetRPCPort, // TWINS mainnet RPC port
			Host:       "127.0.0.1",
			Username:   "",
			Password:   "",
			MaxClients: 100,
			AllowedIPs: []string{"127.0.0.1", "::1"},
			RateLimit:  100,
			Timeout:    30,
		},
		Masternode: MasternodeConfig{
			Enabled:       false,
			PrivateKey:    "",
			ServiceAddr:   "",
			MnConf:        "masternode.conf",
			MnConfLock:    true,
			Debug:         false,
			DebugMaxMB:    50,
			DebugMaxFiles: 3,
		},
		Logging: LoggingConfig{
			Level:  "error",
			Format: "text",
			Output: "./twins.log",
		},
		Wallet: WalletConfig{
			Enabled:             true,      // Wallet enabled by default (legacy: not -disablewallet)
			PayTxFee:            0,         // Dynamic fee by default (legacy: -paytxfee)
			MinTxFee:            10000,     // 0.0001 TWINS per KB (legacy: -mintxfee default)
			MaxTxFee:            100000000, // 1 TWINS maximum (legacy: -maxtxfee default)
			TxConfirmTarget:     1,         // 1 block target (legacy: -txconfirmtarget default)
			Keypool:             1000,      // Pre-generate 1000 keys (legacy: -keypool default)
			SpendZeroConfChange: false,     // Disabled by default — matches legacy C++ -spendzeroconfchange=false; intentional deviation from old GUI default (true)
			CreateWalletBackups: 10,        // Keep 10 backups (legacy: -createwalletbackups default)
			AutoCombineTarget:   100000,    // 100,000 TWINS default consolidation target
			AutoCombineCooldown: 600,       // 10 minutes between consolidation cycles
		},
		Staking: StakingConfig{
			Enabled:             false,          // Staking disabled by default
			ReserveBalance:      0,              // No reserve by default
			StakeSplitThreshold: 200000, // 200,000 TWINS
		},
		Sync: SyncConfig{
			BootstrapMinPeers:         4,        // Minimum peers before starting sync
			BootstrapMaxWait:          120,      // Maximum wait for peer connections (seconds)
			IBDThreshold:              5000,     // Blocks behind to trigger IBD mode
			ConsensusStrategy:         "outbound_only", // Peer consensus calculation strategy
			MaxSyncPeers:              20,       // Maximum peers in sync rotation
			ErrorScoreCooldown:        10.0,     // Error points to trigger cooldown
			CooldownDuration:          300,      // Cooldown duration in seconds (5 minutes)
			HealthDecayTime:           3600,     // Time before health resets (1 hour)
			HealthThreshold:           30.0,     // Minimum score to be considered healthy
			ErrorWeightInvalidBlock:   5.0,      // Malicious/incompatible block penalty
			ErrorWeightTimeout:        2.0,      // Timeout penalty
			ErrorWeightConnectionDrop: 1.0,      // Connection drop (not peer's fault)
			ErrorWeightSlowResponse:   0.5,      // Slow response penalty
			ErrorWeightSendFailed:     1.0,      // Send failure penalty
			BatchTimeout:              60,       // Rotate to next peer if no response (seconds)
			RoundsBeforeRebuild:       5,        // Rebuild rotation list every N rounds
			ReorgWindow:               3600,     // Time window for counting reorgs (seconds)
			MaxAutoReorgs:             1,        // Maximum automatic reorgs before pausing
			ProgressLogInterval:       10,       // Seconds between sync progress logs
		},
	}

	return config
}


// GetDefaultSeeds returns the default seed nodes for a network
// These match the legacy C++ implementation in chainparams.cpp
func GetDefaultSeeds(network string) []string {
	switch network {
	case NetworkMainnet, "main":
		return []string{
			"159.65.195.97:37817",
			"134.209.146.52:37817",
			"46.101.113.6:37817",
			"138.68.154.249:37817",
			"137.184.217.142:37817",
			"165.22.149.70:37817",
			"170.64.157.157:37817",
			"134.122.38.24:37817",
			"45.77.64.171:37817",
			"45.32.36.145:37817",
			"45.77.206.161:37817",
			"207.148.67.25:37817",
		}
	case NetworkTestnet, "test":
		return []string{
			"46.19.210.197:37847",  // Germany
			"46.19.214.68:37847",   // Singapore
			"142.93.145.197:37847", // Toronto
			"159.65.84.118:37847",  // London
		}
	case NetworkRegtest, "reg":
		return []string{} // No seeds for regression testing
	default:
		return []string{}
	}
}

// GetDefaultPorts returns the default ports for a network
func GetDefaultPorts(network string) (p2pPort, rpcPort int) {
	switch network {
	case NetworkMainnet, "main":
		return MainnetP2PPort, MainnetRPCPort
	case NetworkTestnet, "test":
		return TestnetP2PPort, TestnetRPCPort
	case NetworkRegtest, "reg":
		return RegtestP2PPort, RegtestRPCPort
	default:
		return MainnetP2PPort, MainnetRPCPort // Default to mainnet
	}
}

// GetDefaultDataDir returns the default data directory for a network
// Note: This returns relative paths for use with --datadir flag override.
// The actual default data directory is ~/.twins (set in internal/cli/app.go)
func GetDefaultDataDir(network string) string {
	switch network {
	case "mainnet", "main":
		return "./mainnet"
	case "testnet", "test":
		return "./testnet"
	case "regtest", "reg":
		return "./regtest"
	default:
		return "."
	}
}

