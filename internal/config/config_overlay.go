package config

// ConfigOverlay is the YAML parsing layer with pointer fields.
// This allows distinguishing between "not set" and "explicitly set to zero value".
// After parsing YAML into this struct, use MergeFromOverlay to apply values to Config.
type ConfigOverlay struct {
	Network    *NetworkConfigOverlay    `yaml:"network"`
	RPC        *RPCConfigOverlay        `yaml:"rpc"`
	Staking    *StakingConfigOverlay    `yaml:"staking"`
	Masternode *MasternodeConfigOverlay `yaml:"masternode"`
	Wallet     *WalletConfigOverlay     `yaml:"wallet"`
	Logging    *LoggingConfigOverlay    `yaml:"logging"`
	Sync       *SyncConfigOverlay       `yaml:"sync"`
}

// NetworkConfigOverlay is the overlay for NetworkConfig
type NetworkConfigOverlay struct {
	// Basic Network Settings
	Port             *int     `yaml:"port"`
	Seeds            []string `yaml:"seeds"`
	MaxPeers         *int     `yaml:"maxPeers"`
	MaxOutboundPeers *int     `yaml:"maxOutboundPeers"`
	TestNet          *bool    `yaml:"testNet"`
	ListenAddr       *string  `yaml:"listenAddr"`
	ExternalIP       *string  `yaml:"externalIP"`
	Timeout          *int     `yaml:"timeout"`
	KeepAlive        *int     `yaml:"keepAlive"`
	MaxBandwidth     *int64   `yaml:"maxBandwidth"`

	// Core Connection Settings
	Listen   *bool `yaml:"listen"`
	DNS      *bool `yaml:"dns"`
	DNSSeed  *bool `yaml:"dnsSeed"`
	Discover *bool `yaml:"discover"`

	// Peer Management
	AddNodes    []string `yaml:"addNodes"`
	SeedNodes   []string `yaml:"seedNodes"`
	ConnectOnly []string `yaml:"connectOnly"`

	// Ban Settings
	BanScore *int `yaml:"banScore"`
	BanTime  *int `yaml:"banTime"`

	// Proxy/Tor Settings
	Proxy          *string `yaml:"proxy"`
	OnionProxy     *string `yaml:"onionProxy"`
	TorControl     *string `yaml:"torControl"`
	TorPassword    *string `yaml:"torPassword"`
	ListenOnion    *bool   `yaml:"listenOnion"`
	ProxyRandomize *bool   `yaml:"proxyRandomize"`

	// UPnP Settings
	UPnP *bool `yaml:"upnp"`

	// Buffer Settings
	MaxReceiveBuffer *int `yaml:"maxReceiveBuffer"`
	MaxSendBuffer    *int `yaml:"maxSendBuffer"`

	// Network Filtering
	BindAddresses []string `yaml:"bindAddresses"`
	WhiteBind     []string `yaml:"whiteBind"`
	Whitelist     []string `yaml:"whitelist"`
	OnlyNet       *string  `yaml:"onlyNet"`

	// Write Queue Configuration
	PeerWriteQueueSize     *int `yaml:"peerWriteQueueSize"`
	SyncPeerWriteQueueSize *int `yaml:"syncPeerWriteQueueSize"`
	WriteQueueTimeout      *int `yaml:"writeQueueTimeout"`

	// Rate Limiting
	MaxBlocksPerSecond   *int   `yaml:"maxBlocksPerSecond"`
	BlockResponseDelayMs *int   `yaml:"blockResponseDelayMs"`
	MaxGetDataBatchSize  *int   `yaml:"maxGetDataBatchSize"`
	GetDataBatchAsync    *bool  `yaml:"getDataBatchAsync"`
	SlowPeerThreshold    *int   `yaml:"slowPeerThreshold"`
	MaxUploadBytesPerSec *int64 `yaml:"maxUploadBytesPerSec"`
}

// RPCConfigOverlay is the overlay for RPCConfig
type RPCConfigOverlay struct {
	Enabled    *bool    `yaml:"enabled"`
	Port       *int     `yaml:"port"`
	Host       *string  `yaml:"host"`
	Username   *string  `yaml:"username"`
	Password   *string  `yaml:"password"`
	MaxClients *int     `yaml:"maxClients"`
	AllowedIPs []string `yaml:"allowedIPs"`
	RateLimit  *int     `yaml:"rateLimit"`
	Timeout    *int     `yaml:"timeout"`
}

// StakingConfigOverlay is the overlay for StakingConfig
type StakingConfigOverlay struct {
	Enabled             *bool  `yaml:"enabled"`
	ReserveBalance      *int64 `yaml:"reserveBalance"`
	StakeSplitThreshold *int64 `yaml:"stakeSplitThreshold"`
}

// MasternodeConfigOverlay is the overlay for MasternodeConfig
type MasternodeConfigOverlay struct {
	Enabled     *bool   `yaml:"enabled"`
	PrivateKey  *string `yaml:"privateKey"`
	ServiceAddr *string `yaml:"serviceAddr"`
	MnConf      *string `yaml:"mnConf"`
	MnConfLock  *bool   `yaml:"mnConfLock"`

	// Debug event collection
	Debug         *bool `yaml:"debug"`
	DebugMaxMB    *int  `yaml:"debugMaxMB"`
	DebugMaxFiles *int  `yaml:"debugMaxFiles"`
}

// WalletConfigOverlay is the overlay for WalletConfig
type WalletConfigOverlay struct {
	Enabled             *bool   `yaml:"enabled"`
	PayTxFee            *int64  `yaml:"payTxFee"`
	MinTxFee            *int64  `yaml:"minTxFee"`
	MaxTxFee            *int64  `yaml:"maxTxFee"`
	TxConfirmTarget     *int    `yaml:"txConfirmTarget"`
	Keypool             *int    `yaml:"keypool"`
	SpendZeroConfChange *bool   `yaml:"spendZeroConfChange"`
	CreateWalletBackups *int    `yaml:"createWalletBackups"`
	BackupPath          *string `yaml:"backupPath"`
	AutoCombine         *bool   `yaml:"autoCombine"`
	AutoCombineTarget   *int64  `yaml:"autoCombineTarget"`
	AutoCombineCooldown *int    `yaml:"autoCombineCooldown"`
	Mnemonic            *string `yaml:"mnemonic,omitempty"`
	MnemonicPassphrase  *string `yaml:"mnemonicPassphrase,omitempty"`
	HDSeed              *string `yaml:"hdSeed,omitempty"`
}

// LoggingConfigOverlay is the overlay for LoggingConfig
type LoggingConfigOverlay struct {
	Level  *string `yaml:"level"`
	Format *string `yaml:"format"`
	Output *string `yaml:"output"`
}

// SyncConfigOverlay is the overlay for SyncConfig
type SyncConfigOverlay struct {
	BootstrapMinPeers *int `yaml:"bootstrapMinPeers"`
	BootstrapMinWait  *int `yaml:"bootstrapMinWait"`
	BootstrapMaxWait  *int `yaml:"bootstrapMaxWait"`

	IBDThreshold *uint32 `yaml:"ibdThreshold"`

	MaxSyncPeers *int `yaml:"maxSyncPeers"`

	ErrorScoreCooldown *float64 `yaml:"errorScoreCooldown"`
	CooldownDuration   *int     `yaml:"cooldownDuration"`
	HealthDecayTime    *int     `yaml:"healthDecayTime"`
	HealthThreshold    *float64 `yaml:"healthThreshold"`

	ErrorWeightInvalidBlock   *float64 `yaml:"errorWeightInvalidBlock"`
	ErrorWeightTimeout        *float64 `yaml:"errorWeightTimeout"`
	ErrorWeightConnectionDrop *float64 `yaml:"errorWeightConnectionDrop"`
	ErrorWeightSlowResponse   *float64 `yaml:"errorWeightSlowResponse"`
	ErrorWeightSendFailed     *float64 `yaml:"errorWeightSendFailed"`

	BatchTimeout        *int `yaml:"batchTimeout"`
	RoundsBeforeRebuild *int `yaml:"roundsBeforeRebuild"`

	ReorgWindow   *int `yaml:"reorgWindow"`
	MaxAutoReorgs *int `yaml:"maxAutoReorgs"`

	ProgressLogInterval *int `yaml:"progressLogInterval"`

	ConsensusStrategy         *string `yaml:"consensusStrategy"`
	ConsensusOutboundPriority *bool   `yaml:"consensusOutboundPriority"`
	ConsensusHeightMaxDiff    *uint32 `yaml:"consensusHeightMaxDiff"`
	ConsensusPruneOutdated    *bool   `yaml:"consensusPruneOutdated"`
	ConsensusPruneMaxDiff     *uint32 `yaml:"consensusPruneMaxDiff"`
	ConsensusMinOutboundPeers *int    `yaml:"consensusMinOutboundPeers"`
}

// MergeFromOverlay applies non-nil values from overlay to config
func (c *Config) MergeFromOverlay(o *ConfigOverlay) {
	if o == nil {
		return
	}
	if o.Network != nil {
		c.Network.mergeFrom(o.Network)
	}
	if o.RPC != nil {
		c.RPC.mergeFrom(o.RPC)
	}
	if o.Staking != nil {
		c.Staking.mergeFrom(o.Staking)
	}
	if o.Masternode != nil {
		c.Masternode.mergeFrom(o.Masternode)
	}
	if o.Wallet != nil {
		c.Wallet.mergeFrom(o.Wallet)
	}
	if o.Logging != nil {
		c.Logging.mergeFrom(o.Logging)
	}
	if o.Sync != nil {
		c.Sync.mergeFrom(o.Sync)
	}
}

func (n *NetworkConfig) mergeFrom(o *NetworkConfigOverlay) {
	if o.Port != nil {
		n.Port = *o.Port
	}
	if len(o.Seeds) > 0 {
		n.Seeds = o.Seeds
	}
	if o.MaxPeers != nil {
		n.MaxPeers = *o.MaxPeers
	}
	if o.MaxOutboundPeers != nil {
		n.MaxOutboundPeers = *o.MaxOutboundPeers
	}
	if o.TestNet != nil {
		n.TestNet = *o.TestNet
	}
	if o.ListenAddr != nil {
		n.ListenAddr = *o.ListenAddr
	}
	if o.ExternalIP != nil {
		n.ExternalIP = *o.ExternalIP
	}
	if o.Timeout != nil {
		n.Timeout = *o.Timeout
	}
	if o.KeepAlive != nil {
		n.KeepAlive = *o.KeepAlive
	}
	if o.MaxBandwidth != nil {
		n.MaxBandwidth = *o.MaxBandwidth
	}
	if o.Listen != nil {
		n.Listen = *o.Listen
	}
	if o.DNS != nil {
		n.DNS = *o.DNS
	}
	if o.DNSSeed != nil {
		n.DNSSeed = *o.DNSSeed
	}
	if o.Discover != nil {
		n.Discover = *o.Discover
	}
	if len(o.AddNodes) > 0 {
		n.AddNodes = o.AddNodes
	}
	if len(o.SeedNodes) > 0 {
		n.SeedNodes = o.SeedNodes
	}
	if len(o.ConnectOnly) > 0 {
		n.ConnectOnly = o.ConnectOnly
	}
	if o.BanScore != nil {
		n.BanScore = *o.BanScore
	}
	if o.BanTime != nil {
		n.BanTime = *o.BanTime
	}
	if o.Proxy != nil {
		n.Proxy = *o.Proxy
	}
	if o.OnionProxy != nil {
		n.OnionProxy = *o.OnionProxy
	}
	if o.TorControl != nil {
		n.TorControl = *o.TorControl
	}
	if o.TorPassword != nil {
		n.TorPassword = *o.TorPassword
	}
	if o.ListenOnion != nil {
		n.ListenOnion = *o.ListenOnion
	}
	if o.ProxyRandomize != nil {
		n.ProxyRandomize = *o.ProxyRandomize
	}
	if o.UPnP != nil {
		n.UPnP = *o.UPnP
	}
	if o.MaxReceiveBuffer != nil {
		n.MaxReceiveBuffer = *o.MaxReceiveBuffer
	}
	if o.MaxSendBuffer != nil {
		n.MaxSendBuffer = *o.MaxSendBuffer
	}
	if len(o.BindAddresses) > 0 {
		n.BindAddresses = o.BindAddresses
	}
	if len(o.WhiteBind) > 0 {
		n.WhiteBind = o.WhiteBind
	}
	if len(o.Whitelist) > 0 {
		n.Whitelist = o.Whitelist
	}
	if o.OnlyNet != nil {
		n.OnlyNet = *o.OnlyNet
	}
	if o.PeerWriteQueueSize != nil {
		n.PeerWriteQueueSize = *o.PeerWriteQueueSize
	}
	if o.SyncPeerWriteQueueSize != nil {
		n.SyncPeerWriteQueueSize = *o.SyncPeerWriteQueueSize
	}
	if o.WriteQueueTimeout != nil {
		n.WriteQueueTimeout = *o.WriteQueueTimeout
	}
	if o.MaxBlocksPerSecond != nil {
		n.MaxBlocksPerSecond = *o.MaxBlocksPerSecond
	}
	if o.BlockResponseDelayMs != nil {
		n.BlockResponseDelayMs = *o.BlockResponseDelayMs
	}
	if o.MaxGetDataBatchSize != nil {
		n.MaxGetDataBatchSize = *o.MaxGetDataBatchSize
	}
	if o.GetDataBatchAsync != nil {
		n.GetDataBatchAsync = *o.GetDataBatchAsync
	}
	if o.SlowPeerThreshold != nil {
		n.SlowPeerThreshold = *o.SlowPeerThreshold
	}
	if o.MaxUploadBytesPerSec != nil {
		n.MaxUploadBytesPerSec = *o.MaxUploadBytesPerSec
	}
}

func (r *RPCConfig) mergeFrom(o *RPCConfigOverlay) {
	if o.Enabled != nil {
		r.Enabled = *o.Enabled
	}
	if o.Port != nil {
		r.Port = *o.Port
	}
	if o.Host != nil {
		r.Host = *o.Host
	}
	if o.Username != nil {
		r.Username = *o.Username
	}
	if o.Password != nil {
		r.Password = *o.Password
	}
	if o.MaxClients != nil {
		r.MaxClients = *o.MaxClients
	}
	if len(o.AllowedIPs) > 0 {
		r.AllowedIPs = o.AllowedIPs
	}
	if o.RateLimit != nil {
		r.RateLimit = *o.RateLimit
	}
	if o.Timeout != nil {
		r.Timeout = *o.Timeout
	}
}

func (s *StakingConfig) mergeFrom(o *StakingConfigOverlay) {
	if o.Enabled != nil {
		s.Enabled = *o.Enabled
	}
	if o.ReserveBalance != nil {
		s.ReserveBalance = *o.ReserveBalance
	}
	if o.StakeSplitThreshold != nil {
		s.StakeSplitThreshold = *o.StakeSplitThreshold
	}
}

func (m *MasternodeConfig) mergeFrom(o *MasternodeConfigOverlay) {
	if o.Enabled != nil {
		m.Enabled = *o.Enabled
	}
	if o.PrivateKey != nil {
		m.PrivateKey = *o.PrivateKey
	}
	if o.ServiceAddr != nil {
		m.ServiceAddr = *o.ServiceAddr
	}
	if o.MnConf != nil {
		m.MnConf = *o.MnConf
	}
	if o.MnConfLock != nil {
		m.MnConfLock = *o.MnConfLock
	}
	if o.Debug != nil {
		m.Debug = *o.Debug
	}
	if o.DebugMaxMB != nil {
		m.DebugMaxMB = *o.DebugMaxMB
	}
	if o.DebugMaxFiles != nil {
		m.DebugMaxFiles = *o.DebugMaxFiles
	}
}

func (w *WalletConfig) mergeFrom(o *WalletConfigOverlay) {
	if o.Enabled != nil {
		w.Enabled = *o.Enabled
	}
	if o.PayTxFee != nil {
		w.PayTxFee = *o.PayTxFee
	}
	if o.MinTxFee != nil {
		w.MinTxFee = *o.MinTxFee
	}
	if o.MaxTxFee != nil {
		w.MaxTxFee = *o.MaxTxFee
	}
	if o.TxConfirmTarget != nil {
		w.TxConfirmTarget = *o.TxConfirmTarget
	}
	if o.Keypool != nil {
		w.Keypool = *o.Keypool
	}
	if o.SpendZeroConfChange != nil {
		w.SpendZeroConfChange = *o.SpendZeroConfChange
	}
	if o.CreateWalletBackups != nil {
		w.CreateWalletBackups = *o.CreateWalletBackups
	}
	if o.BackupPath != nil {
		w.BackupPath = *o.BackupPath
	}
	if o.Mnemonic != nil {
		w.Mnemonic = *o.Mnemonic
	}
	if o.MnemonicPassphrase != nil {
		w.MnemonicPassphrase = *o.MnemonicPassphrase
	}
	if o.HDSeed != nil {
		w.HDSeed = *o.HDSeed
	}
	if o.AutoCombine != nil {
		w.AutoCombine = *o.AutoCombine
	}
	if o.AutoCombineTarget != nil {
		w.AutoCombineTarget = *o.AutoCombineTarget
	}
	if o.AutoCombineCooldown != nil {
		w.AutoCombineCooldown = *o.AutoCombineCooldown
	}
}

func (l *LoggingConfig) mergeFrom(o *LoggingConfigOverlay) {
	if o.Level != nil {
		l.Level = *o.Level
	}
	if o.Format != nil {
		l.Format = *o.Format
	}
	if o.Output != nil {
		l.Output = *o.Output
	}
}

func (s *SyncConfig) mergeFrom(o *SyncConfigOverlay) {
	if o.BootstrapMinPeers != nil {
		s.BootstrapMinPeers = *o.BootstrapMinPeers
	}
	if o.BootstrapMinWait != nil {
		s.BootstrapMinWait = *o.BootstrapMinWait
	}
	if o.BootstrapMaxWait != nil {
		s.BootstrapMaxWait = *o.BootstrapMaxWait
	}
	if o.IBDThreshold != nil {
		s.IBDThreshold = *o.IBDThreshold
	}
	if o.MaxSyncPeers != nil {
		s.MaxSyncPeers = *o.MaxSyncPeers
	}
	if o.ErrorScoreCooldown != nil {
		s.ErrorScoreCooldown = *o.ErrorScoreCooldown
	}
	if o.CooldownDuration != nil {
		s.CooldownDuration = *o.CooldownDuration
	}
	if o.HealthDecayTime != nil {
		s.HealthDecayTime = *o.HealthDecayTime
	}
	if o.HealthThreshold != nil {
		s.HealthThreshold = *o.HealthThreshold
	}
	if o.ErrorWeightInvalidBlock != nil {
		s.ErrorWeightInvalidBlock = *o.ErrorWeightInvalidBlock
	}
	if o.ErrorWeightTimeout != nil {
		s.ErrorWeightTimeout = *o.ErrorWeightTimeout
	}
	if o.ErrorWeightConnectionDrop != nil {
		s.ErrorWeightConnectionDrop = *o.ErrorWeightConnectionDrop
	}
	if o.ErrorWeightSlowResponse != nil {
		s.ErrorWeightSlowResponse = *o.ErrorWeightSlowResponse
	}
	if o.ErrorWeightSendFailed != nil {
		s.ErrorWeightSendFailed = *o.ErrorWeightSendFailed
	}
	if o.BatchTimeout != nil {
		s.BatchTimeout = *o.BatchTimeout
	}
	if o.RoundsBeforeRebuild != nil {
		s.RoundsBeforeRebuild = *o.RoundsBeforeRebuild
	}
	if o.ReorgWindow != nil {
		s.ReorgWindow = *o.ReorgWindow
	}
	if o.MaxAutoReorgs != nil {
		s.MaxAutoReorgs = *o.MaxAutoReorgs
	}
	if o.ProgressLogInterval != nil {
		s.ProgressLogInterval = *o.ProgressLogInterval
	}
	if o.ConsensusStrategy != nil {
		s.ConsensusStrategy = *o.ConsensusStrategy
	}
	if o.ConsensusOutboundPriority != nil {
		s.ConsensusOutboundPriority = *o.ConsensusOutboundPriority
	}
	if o.ConsensusHeightMaxDiff != nil {
		s.ConsensusHeightMaxDiff = *o.ConsensusHeightMaxDiff
	}
	if o.ConsensusPruneOutdated != nil {
		s.ConsensusPruneOutdated = *o.ConsensusPruneOutdated
	}
	if o.ConsensusPruneMaxDiff != nil {
		s.ConsensusPruneMaxDiff = *o.ConsensusPruneMaxDiff
	}
	if o.ConsensusMinOutboundPeers != nil {
		s.ConsensusMinOutboundPeers = *o.ConsensusMinOutboundPeers
	}
}
