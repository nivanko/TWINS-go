package config

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Network name constants
const (
	NetworkMainnet = "mainnet"
	NetworkTestnet = "testnet"
	NetworkRegtest = "regtest"
)

// Config represents the complete TWINS node configuration
type Config struct {
	// Data directory for all node data (blockchain, wallet, peers, logs)
	DataDir string

	Network    NetworkConfig
	RPC        RPCConfig
	Staking    StakingConfig
	Masternode MasternodeConfig
	Wallet     WalletConfig
	Logging    LoggingConfig
	Sync       SyncConfig
}

// NetworkConfig contains P2P network settings
type NetworkConfig struct {
	// === Basic Network Settings ===
	Port             int      // TWINS mainnet P2P port (legacy: nDefaultPort)
	Seeds            []string // DNS seed nodes for peer discovery
	MaxPeers         int      // Maximum peer connections (legacy: -maxconnections)
	MaxOutboundPeers int      // Maximum outbound connections (default: 16)
	TestNet          bool     // Run on testnet
	ListenAddr       string   // Address to listen for P2P (legacy: -bind)
	ExternalIP       string   // External IP for peer advertising (legacy: -externalip)
	Timeout          int      // Connection timeout in seconds (legacy: DEFAULT_CONNECT_TIMEOUT 5000ms)
	KeepAlive        int      // Keep-alive/ping interval in seconds (legacy: PING_INTERVAL)
	MaxBandwidth     int64    // Max bandwidth in KB/s, 0 = unlimited

	// === Core Connection Settings (Legacy C++ Compatible) ===
	Listen   bool // Accept inbound connections (legacy: -listen)
	DNS      bool // Allow DNS lookups for -addnode etc (legacy: -dns)
	DNSSeed  bool // Query DNS seeds for peers (legacy: -dnsseed)
	Discover bool // Discover own IP address (legacy: -discover)

	// === Peer Management (Legacy C++ Compatible) ===
	AddNodes    []string // Nodes to connect to and keep open (legacy: -addnode)
	SeedNodes   []string // Connect to retrieve peer addresses only (legacy: -seednode)
	ConnectOnly []string // Connect ONLY to these nodes (legacy: -connect)

	// === Ban Settings (Legacy C++ Compatible) ===
	BanScore int // Misbehavior threshold for disconnect (legacy: -banscore)
	BanTime  int // Ban duration in seconds, 24h default (legacy: -bantime)

	// === Proxy/Tor Settings (Legacy C++ Compatible) ===
	Proxy          string // SOCKS5 proxy for all connections (legacy: -proxy)
	OnionProxy     string // Separate proxy for Tor (legacy: -onion)
	TorControl     string // Tor control port (legacy: -torcontrol)
	TorPassword    string // Tor control password (legacy: -torpassword)
	ListenOnion    bool   // Create Tor hidden service (legacy: -listenonion)
	ProxyRandomize bool   // Randomize proxy credentials (legacy: -proxyrandomize)

	// === UPnP Settings (Legacy C++ Compatible) ===
	UPnP bool // Use UPnP port mapping (legacy: -upnp)

	// === Buffer Settings (Legacy C++ Compatible) ===
	MaxReceiveBuffer int // Per-connection receive buffer x1000 bytes (legacy: -maxreceivebuffer)
	MaxSendBuffer    int // Per-connection send buffer x1000 bytes (legacy: -maxsendbuffer)

	// === Network Filtering (Legacy C++ Compatible) ===
	BindAddresses []string // Addresses to bind and listen (legacy: -bind)
	WhiteBind     []string // Bind and whitelist peers (legacy: -whitebind)
	Whitelist     []string // Whitelist peers by IP/netmask (legacy: -whitelist)
	OnlyNet       string   // Only connect via network: ipv4/ipv6/onion (legacy: -onlynet)

	// === Write Queue Configuration (Go-specific) ===
	PeerWriteQueueSize     int // Regular peer queue buffer size
	SyncPeerWriteQueueSize int // Syncing peer queue buffer size
	WriteQueueTimeout      int // Timeout in seconds before dropping message

	// === Rate Limiting for Block Responses (Go-specific) ===
	MaxBlocksPerSecond   int   // Max blocks sent per second per peer
	BlockResponseDelayMs int   // Delay between block responses in milliseconds
	MaxGetDataBatchSize  int   // Max items in single getdata request
	GetDataBatchAsync    bool  // Process large batches asynchronously
	SlowPeerThreshold    int   // Queue utilization % to consider peer slow
	MaxUploadBytesPerSec int64 // 10 MB/s default upload limit
}

// RPCConfig contains RPC server settings
type RPCConfig struct {
	Enabled    bool
	Port       int // TWINS RPC port (not Bitcoin 8332)
	Host       string
	Username   string
	Password   string
	MaxClients int
	AllowedIPs []string
	RateLimit  int // Requests per minute
	Timeout    int // Request timeout in seconds
}

// StakingConfig contains staking-specific settings (legacy C++ compatible)
type StakingConfig struct {
	// Enabled controls whether staking is active (legacy: -staking, default: 1 in C++, false in Go)
	Enabled bool

	// ReserveBalance is the amount to keep available for spending, not used for staking
	// Value is in satoshis (1 TWINS = 100,000,000 satoshis)
	// Legacy: -reservebalance=<amt> (default: 0)
	ReserveBalance int64

	// StakeSplitThreshold is the amount above which staking outputs are split into two
	// Value is in TWINS (whole coins, e.g. 20000)
	// Legacy: was in wallet.dat, now in twinsd.yml
	StakeSplitThreshold int64
}

// MasternodeConfig contains masternode-specific settings
// Legacy C++ compatible: -masternode, -mnconf, -mnconflock, -masternodeprivkey, -masternodeaddr
type MasternodeConfig struct {
	Enabled     bool
	PrivateKey  string // Masternode private key (legacy: -masternodeprivkey)
	ServiceAddr string // External IP:port (legacy: -masternodeaddr)
	MnConf      string // Config file for remote MN entries (legacy: -mnconf)
	MnConfLock  bool   // Lock UTXOs from conf file (legacy: -mnconflock)

	// Debug event collection (disabled by default, zero-cost when off)
	Debug         bool `yaml:"debug"`         // Enable debug event collection to JSONL
	DebugMaxMB    int  `yaml:"debugMaxMB"`    // Max JSONL file size before rotation (default 50)
	DebugMaxFiles int  `yaml:"debugMaxFiles"` // Max rotated files to keep (default 3)
}

// WalletConfig contains wallet-specific settings (legacy C++ compatible)
type WalletConfig struct {
	// === Wallet Enable/Disable (Legacy C++ Compatible) ===
	Enabled bool // Enable wallet (legacy: not -disablewallet)

	// === Fee Configuration (Legacy C++ Compatible) ===
	PayTxFee        int64 // Fee per kB for transactions in satoshis (legacy: -paytxfee)
	MinTxFee        int64 // Minimum fee threshold in satoshis (legacy: -mintxfee)
	MaxTxFee        int64 // Maximum total fee allowed in satoshis, 1 TWINS (legacy: -maxtxfee)
	TxConfirmTarget int   // Target confirmations for fee estimation (legacy: -txconfirmtarget)

	// === Wallet Management (Legacy C++ Compatible) ===
	Keypool             int    // Pre-generated key pool size (legacy: -keypool)
	SpendZeroConfChange bool   // Allow spending unconfirmed change (legacy: -spendzeroconfchange)
	CreateWalletBackups int    // Auto-backup count, 0 to disable (legacy: -createwalletbackups)
	BackupPath          string // Custom backup directory (legacy: -backuppath)

	// === Auto-Combine Inputs (UTXO Consolidation) ===
	AutoCombine         bool  // Enable automatic UTXO consolidation
	AutoCombineTarget   int64 // Target amount in TWINS (whole coins, e.g. 200000)
	AutoCombineCooldown int   // Minimum seconds between consolidation cycles (default 600)

	// === HD Wallet Creation (Legacy C++ Compatible) ===
	// SECURITY WARNING: These fields are for CLI-only use during wallet creation.
	// NEVER store mnemonic, passphrase, or seed in config files - they will be exposed!
	// Use CLI flags (--mnemonic, --hdseed) which are only used once at wallet creation.
	Mnemonic           string // BIP39 mnemonic - CLI ONLY, DO NOT STORE IN CONFIG FILES
	MnemonicPassphrase string // Mnemonic passphrase - CLI ONLY, DO NOT STORE IN CONFIG FILES
	HDSeed             string // HD seed in hex - CLI ONLY, DO NOT STORE IN CONFIG FILES
}

// LoggingConfig contains logging settings
type LoggingConfig struct {
	Level  string // Log level (debug, info, warn, error)
	Format string // Log format (text, json)
	Output string // Log output file path
}

// SyncConfig contains blockchain synchronization settings
type SyncConfig struct {
	// Bootstrap settings
	BootstrapMinPeers int // Target peer count for bootstrap
	BootstrapMinWait  int // Minimum wait in seconds even if target reached
	BootstrapMaxWait  int // Maximum wait timeout in seconds

	// Sync mode thresholds
	IBDThreshold uint32 // Blocks behind to trigger IBD mode

	// Peer management
	MaxSyncPeers int // Cap on rotation list size

	// Health & error tracking
	ErrorScoreCooldown float64 // Error points to trigger cooldown
	CooldownDuration   int     // Cooldown duration in seconds (5 minutes)
	HealthDecayTime    int     // Time before health resets to 50 (1 hour)
	HealthThreshold    float64 // Minimum score to be considered healthy

	// Error weights
	ErrorWeightInvalidBlock   float64 // Malicious/incompatible
	ErrorWeightTimeout        float64 // Network issue
	ErrorWeightConnectionDrop float64 // Not peer's fault
	ErrorWeightSlowResponse   float64 // Performance issue
	ErrorWeightSendFailed     float64 // Communication problem

	// Rotation timing
	BatchTimeout        int // Rotate peer if no response (seconds)
	RoundsBeforeRebuild int // Rebuild list every N rounds

	// Reorg protection
	ReorgWindow   int // Time window for reorg counting (1 hour)
	MaxAutoReorgs int // Auto-reorg once, pause on second

	// Logging
	ProgressLogInterval int // User-facing progress updates (seconds)

	// Consensus filtering (NEW)
	ConsensusStrategy         string // outbound_only (default), all
	ConsensusOutboundPriority bool   // Prefer outbound peers for consensus
	ConsensusHeightMaxDiff    uint32 // Max blocks difference for outlier filtering
	ConsensusPruneOutdated    bool   // Auto-disconnect outdated outbound peers
	ConsensusPruneMaxDiff     uint32 // Max blocks before pruning outbound peers
	ConsensusMinOutboundPeers int    // Min outbound peers before fallback
}

// LoadConfig loads configuration from a YAML file using overlay pattern.
// This correctly handles the distinction between "not set" and "explicitly set to zero value".
func LoadConfig(path string) (*Config, error) {
	// Start with defaults
	config := DefaultConfig()

	// If path is empty, return default configuration
	if path == "" {
		return config, nil
	}

	// Check if file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("configuration file not found: %s", path)
	}

	// Read configuration file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %v", err)
	}

	// Parse YAML into overlay (pointer fields)
	var overlay ConfigOverlay
	if err := yaml.Unmarshal(data, &overlay); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %v", err)
	}

	// Merge overlay onto defaults - only non-nil fields are applied
	config.MergeFromOverlay(&overlay)

	return config, nil
}

// LoadConfigWithOverrides loads configuration with custom overrides
func LoadConfigWithOverrides(path string, overrides map[string]interface{}) (*Config, error) {
	config, err := LoadConfig(path)
	if err != nil {
		return nil, err
	}

	// Apply overrides
	if err := applyOverrides(config, overrides); err != nil {
		return nil, fmt.Errorf("failed to apply overrides: %v", err)
	}

	return config, nil
}

// Clone creates a deep copy of the configuration
func (c *Config) Clone() *Config {
	clone := *c // shallow copy of all value types

	// Deep copy Network slices
	clone.Network.Seeds = append([]string{}, c.Network.Seeds...)
	clone.Network.AddNodes = append([]string{}, c.Network.AddNodes...)
	clone.Network.SeedNodes = append([]string{}, c.Network.SeedNodes...)
	clone.Network.ConnectOnly = append([]string{}, c.Network.ConnectOnly...)
	clone.Network.BindAddresses = append([]string{}, c.Network.BindAddresses...)
	clone.Network.WhiteBind = append([]string{}, c.Network.WhiteBind...)
	clone.Network.Whitelist = append([]string{}, c.Network.Whitelist...)

	// Deep copy RPC slices
	clone.RPC.AllowedIPs = append([]string{}, c.RPC.AllowedIPs...)

	return &clone
}

// GetNetworkName returns the network name based on configuration
func (c *Config) GetNetworkName() string {
	if c.Network.TestNet {
		return NetworkTestnet
	}
	return NetworkMainnet
}

// IsMainNet returns true if running on mainnet
func (c *Config) IsMainNet() bool {
	return !c.Network.TestNet
}

// IsTestNet returns true if running on testnet
func (c *Config) IsTestNet() bool {
	return c.Network.TestNet
}

// IsMasternodeEnabled returns true if masternode is enabled
func (c *Config) IsMasternodeEnabled() bool {
	return c.Masternode.Enabled && c.Masternode.PrivateKey != ""
}

// GetListenAddress returns the full listen address for P2P
func (c *Config) GetListenAddress() string {
	if c.Network.ListenAddr == "" || !c.Network.Listen {
		return "" // Don't listen for inbound connections
	}
	return fmt.Sprintf("%s:%d", c.Network.ListenAddr, c.Network.Port)
}

// ApplyParameterInteractions applies legacy C++ parameter interactions
// This implements the same logic as the C++ InitParameterInteraction() function
// where certain flags force other flags to specific values.
//
// ORDER MATTERS: The C++ code applies these in a specific order where
// -bind/-whitebind are processed FIRST (they force -listen=1),
// then -connect/-proxy can override listen to false.
func (c *Config) ApplyParameterInteractions() {
	// FIRST: -bind or -whitebind forces -listen=1 (legacy: InitParameterInteraction)
	// This must come first so that -connect/-proxy can override if needed
	if len(c.Network.BindAddresses) > 0 || len(c.Network.WhiteBind) > 0 {
		c.Network.Listen = true
	}

	// -connect forces -listen=0, -dnsseed=0 (legacy: InitParameterInteraction)
	if len(c.Network.ConnectOnly) > 0 {
		c.Network.Listen = false
		c.Network.DNSSeed = false
	}

	// -proxy forces -listen=0, -upnp=0, -discover=0 (legacy: InitParameterInteraction)
	if c.Network.Proxy != "" {
		c.Network.Listen = false
		c.Network.UPnP = false
		c.Network.Discover = false
	}

	// -listen=0 forces -upnp=0, -discover=0, -listenonion=0 (legacy: InitParameterInteraction)
	if !c.Network.Listen {
		c.Network.UPnP = false
		c.Network.Discover = false
		c.Network.ListenOnion = false
	}

	// -externalip forces -discover=0 (legacy: InitParameterInteraction)
	if c.Network.ExternalIP != "" {
		c.Network.Discover = false
	}
}

// GetRPCAddress returns the full RPC server address
func (c *Config) GetRPCAddress() string {
	return fmt.Sprintf("%s:%d", c.RPC.Host, c.RPC.Port)
}

// Validate validates the entire configuration.
// Delegates to ValidateConfig for the canonical validation logic.
func (c *Config) Validate() error {
	return ValidateConfig(c)
}

// applyOverrides applies configuration overrides
func applyOverrides(config *Config, overrides map[string]interface{}) error {
	for key, value := range overrides {
		if err := setConfigValue(config, key, value); err != nil {
			return fmt.Errorf("failed to apply override %s: %v", key, err)
		}
	}
	return nil
}

// setConfigValue sets a configuration value using dot notation (e.g., "network.port")
func setConfigValue(config *Config, key string, value interface{}) error {
	parts := strings.Split(key, ".")
	if len(parts) < 2 {
		return fmt.Errorf("invalid key format: %s", key)
	}

	v := reflect.ValueOf(config).Elem()

	// Navigate to the target field
	for i, part := range parts[:len(parts)-1] {
		// Handle special struct name mappings
		var fieldName string
		switch part {
		case "network":
			fieldName = "Network"
		case "rpc":
			fieldName = "RPC"
		case "masternode":
			fieldName = "Masternode"
		case "logging":
			fieldName = "Logging"
		case "sync":
			fieldName = "Sync"
		default:
			// Capitalize first letter for other fields
			fieldName = strings.ToUpper(part[:1]) + part[1:]
		}

		field := v.FieldByName(fieldName)
		if !field.IsValid() {
			return fmt.Errorf("invalid field path at %s: %s", strings.Join(parts[:i+1], "."), part)
		}
		v = field
	}

	// Set the final field
	finalField := parts[len(parts)-1]
	// Handle special field name mappings
	fieldName := finalField
	switch finalField {
	case "port":
		fieldName = "Port"
	case "enabled":
		fieldName = "Enabled"
	case "host":
		fieldName = "Host"
	case "maxPeers":
		fieldName = "MaxPeers"
	case "testNet":
		fieldName = "TestNet"
	case "listenAddr":
		fieldName = "ListenAddr"
	case "externalIP":
		fieldName = "ExternalIP"
	default:
		// Capitalize first letter for other fields
		if len(finalField) > 0 {
			fieldName = strings.ToUpper(finalField[:1]) + finalField[1:]
		}
	}
	field := v.FieldByName(fieldName)
	if !field.IsValid() || !field.CanSet() {
		return fmt.Errorf("cannot set field: %s", finalField)
	}

	// Convert and set value
	return setReflectValue(field, value)
}

// setReflectValue sets a reflect.Value with proper type conversion
func setReflectValue(field reflect.Value, value interface{}) error {
	valueType := reflect.TypeOf(value)
	fieldType := field.Type()

	// Direct assignment if types match
	if valueType == fieldType {
		field.Set(reflect.ValueOf(value))
		return nil
	}

	// Convert value to string first for parsing
	str := fmt.Sprintf("%v", value)

	switch field.Kind() {
	case reflect.String:
		field.SetString(str)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if val, err := strconv.ParseInt(str, 10, 64); err != nil {
			return err
		} else {
			field.SetInt(val)
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if val, err := strconv.ParseUint(str, 10, 64); err != nil {
			return err
		} else {
			field.SetUint(val)
		}
	case reflect.Float32, reflect.Float64:
		if val, err := strconv.ParseFloat(str, 64); err != nil {
			return err
		} else {
			field.SetFloat(val)
		}
	case reflect.Bool:
		if val, err := strconv.ParseBool(str); err != nil {
			return err
		} else {
			field.SetBool(val)
		}
	case reflect.Slice:
		// Handle string slices
		if field.Type().Elem().Kind() == reflect.String {
			if strSlice, ok := value.([]string); ok {
				field.Set(reflect.ValueOf(strSlice))
			} else {
				return fmt.Errorf("cannot convert to string slice")
			}
		}
	default:
		return fmt.Errorf("unsupported field type: %s", field.Kind())
	}

	return nil
}
