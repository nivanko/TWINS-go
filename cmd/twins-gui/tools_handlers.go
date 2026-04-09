package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/twins-dev/twins-core/internal/cli"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/twins-dev/twins-core/internal/p2p"
	"github.com/twins-dev/twins-core/internal/rpc"
)

// ToolsInfo contains system, network, and blockchain information for the Information tab.
type ToolsInfo struct {
	// Client info
	ClientName      string `json:"clientName"`
	ClientVersion   string `json:"clientVersion"`
	GoVersion       string `json:"goVersion"`
	Platform        string `json:"platform"`
	BuildDate       string `json:"buildDate"`
	DatabaseVersion string `json:"databaseVersion"`
	StartupTime     int64  `json:"startupTime"`
	DataDir         string `json:"dataDir"`

	// Network info
	NetworkName string `json:"networkName"`
	Connections int32  `json:"connections"`
	InPeers     int32  `json:"inPeers"`
	OutPeers    int32  `json:"outPeers"`

	// Blockchain info
	BlockCount    uint32 `json:"blockCount"`
	LastBlockTime int64  `json:"lastBlockTime"`

	// Masternode info
	MasternodeCount int `json:"masternodeCount"`
}

// RPCCommandResult contains the result of executing an RPC command.
type RPCCommandResult struct {
	Result interface{} `json:"result,omitempty"`
	Error  string      `json:"error,omitempty"`
	Time   string      `json:"time"`
}

// PeerDetail contains detailed peer information for the Peers tab.
type PeerDetail struct {
	ID              int     `json:"id"`
	Address         string  `json:"address"`
	Alias           string  `json:"alias"`
	Services        string  `json:"services"`
	LastSend        int64   `json:"lastSend"`
	LastRecv        int64   `json:"lastRecv"`
	BytesSent       uint64  `json:"bytesSent"`
	BytesReceived   uint64  `json:"bytesReceived"`
	ConnTime        int64   `json:"connTime"`
	TimeOffset      int64   `json:"timeOffset"`
	PingTime        float64 `json:"pingTime"`
	PingWait        float64 `json:"pingWait"`
	ProtocolVersion int32   `json:"protocolVersion"`
	UserAgent       string  `json:"userAgent"`
	Inbound         bool    `json:"inbound"`
	StartHeight     int32   `json:"startHeight"`
	BanScore        int     `json:"banScore"`
	SyncedHeaders   int32   `json:"syncedHeaders"`
	SyncedBlocks    int32   `json:"syncedBlocks"`
	SyncedHeight    int32   `json:"syncedHeight"`
	Whitelisted     bool    `json:"whitelisted"`
}

// BannedPeerInfo contains information about a banned peer.
type BannedPeerInfo struct {
	Address     string `json:"address"`
	Alias       string `json:"alias"`
	BannedUntil int64  `json:"bannedUntil"`
	BanCreated  int64  `json:"banCreated"`
	Reason      string `json:"reason"`
}

// TrafficInfo contains network traffic statistics.
type TrafficInfo struct {
	TotalBytesRecv uint64 `json:"totalBytesRecv"`
	TotalBytesSent uint64 `json:"totalBytesSent"`
	PeerCount      int32  `json:"peerCount"`
}

// startupTime records when the application started (set in startup).
var startupTime = time.Now()

// ExecuteRPCCommand parses and executes an RPC command string via the internal
// RPC server handler dispatch (no HTTP roundtrip).
func (a *App) ExecuteRPCCommand(command string) *RPCCommandResult {
	now := time.Now().Format("15:04:05")

	command = strings.TrimSpace(command)
	if command == "" {
		return &RPCCommandResult{
			Error: "Empty command",
			Time:  now,
		}
	}

	// Parse command string into method and params
	// Supports: method param1 param2 "param with spaces"
	method, params := parseCommand(command)

	// Get RPC server
	a.componentsMu.RLock()
	rpcServer := a.rpcServer
	a.componentsMu.RUnlock()

	if rpcServer == nil {
		return &RPCCommandResult{
			Error: "RPC server not initialized. Wait for P2P to start.",
			Time:  now,
		}
	}

	// Execute via internal dispatch
	response := rpcServer.ExecuteCommand(method, params)

	if response.Error != nil {
		return &RPCCommandResult{
			Error: response.Error.Message,
			Time:  now,
		}
	}

	return &RPCCommandResult{
		Result: response.Result,
		Time:   now,
	}
}

// GetRPCCommandList returns sorted list of all registered RPC commands
// for auto-completion in the console. Falls back to static list when
// the RPC server is not yet initialized.
func (a *App) GetRPCCommandList() []string {
	a.componentsMu.RLock()
	rpcServer := a.rpcServer
	a.componentsMu.RUnlock()

	if rpcServer != nil {
		return rpcServer.GetRegisteredCommands()
	}

	// Fall back to static list from category map so autocomplete works
	// before the RPC server is ready.
	return a.getStaticCommandList()
}

// getStaticCommandList returns a sorted list of all commands from the
// static category mapping, used as fallback when RPC server is unavailable.
func (a *App) getStaticCommandList() []string {
	cats := a.getStaticCategories()
	var all []string
	for _, cmds := range cats {
		all = append(all, cmds...)
	}
	sort.Strings(all)
	return all
}

// GetRPCCommandHelp returns detailed help text for a specific RPC command.
func (a *App) GetRPCCommandHelp(command string) string {
	return rpc.GetCommandHelp(command)
}

// GetRPCCommandDescriptions returns brief one-line descriptions for RPC
// commands that have help text available.
func (a *App) GetRPCCommandDescriptions() map[string]string {
	return rpc.GetCommandBriefDescriptions()
}

// GetRPCCategoryOrder returns the canonical display order for RPC command
// categories. This is the single source of truth used by the frontend.
// Note: "Other" is not defined in getStaticCategories(); it is populated
// dynamically by GetRPCCommandCategories when the RPC server is live and
// registers commands not present in the static mapping. The frontend
// skips empty categories, so including it here is safe.
func (a *App) GetRPCCategoryOrder() []string {
	return []string{
		"Blockchain", "Control", "Masternode", "Mempool", "Mining",
		"Network", "Raw Transactions", "Transactions", "Utility", "Wallet", "Other",
	}
}

// getStaticCategories returns the static category mapping used as a fallback
// when the RPC server is not yet initialized.
func (a *App) getStaticCategories() map[string][]string {
	return map[string][]string{
		"Blockchain": {
			"addcheckpoint", "getbestblockhash", "getblock", "getblockchaininfo",
			"getblockcount", "getblockhash", "getblockheader", "getchaintips",
			"getdifficulty", "getfeeinfo", "getinfo", "getrewardrates",
			"gettxoutsetinfo", "invalidateblock", "reconsiderblock", "verifychain",
		},
		"Control": {
			"help", "stop",
		},
		"Masternode": {
			"createmasternodebroadcast", "createmasternodekey",
			"decodemasternodebroadcast", "getmasternodecount",
			"getmasternodeoutputs", "getmasternodescores", "getmasternodestatus",
			"getmasternodewinners", "getpoolinfo", "listmasternodeconf",
			"listmasternodes", "masternode", "masternodeconnect",
			"masternodecurrent", "masternodedebug", "relaymasternodebroadcast",
			"startmasternode", "stopmasternode",
		},
		"Mempool": {
			"getmempoolancestors", "getmempooldescendants", "getmempoolentry",
			"getmempoolinfo", "getrawmempool",
		},
		"Mining": {
			"estimatefee", "estimatepriority", "getblocktemplate",
			"getgenerate", "gethashespersec", "getmininginfo",
			"getnetworkhashps", "getstakingstatus", "prioritisetransaction",
			"setgenerate", "submitblock",
		},
		"Network": {
			"addnode", "clearbanned", "disconnectnode", "getaddednodeinfo",
			"getconnectioncount", "getnettotals", "getnetworkinfo",
			"getpeerinfo", "getsyncstatus", "listbanned", "ping",
			"setban", "setnetworkactive",
		},
		"Raw Transactions": {
			"createrawtransaction", "decodescript", "signrawtransaction",
		},
		"Transactions": {
			"decoderawtransaction", "getrawtransaction", "gettxout",
			"sendrawtransaction",
		},
		"Utility": {
			"mnsync", "setmocktime", "settxfee", "spork",
		},
		"Wallet": {
			"addmultisigaddress", "setautocombine", "getautocombine", "backupwallet",
			"createmultisig", "dumpprivkey", "dumphdinfo", "dumpwallet",
			"encryptwallet", "getaccount", "getaccountaddress",
			"getaddressesbyaccount", "getaddressinfo", "getbalance",
			"getnewaddress", "getrawchangeaddress", "getreceivedbyaccount",
			"getreceivedbyaddress", "getstakesplitthreshold", "gettransaction",
			"getunconfirmedbalance", "getwalletinfo", "importaddress",
			"importprivkey", "importwallet", "keypoolrefill", "listaccounts",
			"listaddresses", "listaddressgroupings", "listlockunspent",
			"listreceivedbyaccount", "listreceivedbyaddress", "listsinceblock",
			"listtransactions", "listunspent", "lockunspent", "multisend",
			"reservebalance", "sendfrom", "sendmany", "sendtoaddress",
			"setaccount", "setstakesplitthreshold", "signmessage",
			"validateaddress", "verifymessage", "walletlock",
			"walletpassphrase", "walletpassphrasechange",
		},
	}
}

// GetRPCCommandCategories returns RPC commands grouped by category
// for the categorized autocomplete popup in the console.
// Falls back to the static mapping when the RPC server is not yet ready.
func (a *App) GetRPCCommandCategories() map[string][]string {
	categoryMap := a.getStaticCategories()

	// Get actually registered commands to filter the static mapping
	a.componentsMu.RLock()
	rpcServer := a.rpcServer
	a.componentsMu.RUnlock()

	// If RPC server not ready, return static categories unfiltered
	// so autocomplete and help work before daemon is fully initialized.
	if rpcServer == nil {
		result := make(map[string][]string)
		for category, commands := range categoryMap {
			sorted := make([]string, len(commands))
			copy(sorted, commands)
			sort.Strings(sorted)
			result[category] = sorted
		}
		return result
	}

	registered := make(map[string]bool)
	for _, cmd := range rpcServer.GetRegisteredCommands() {
		registered[cmd] = true
	}

	// Filter: only include commands that are actually registered
	result := make(map[string][]string)
	for category, commands := range categoryMap {
		var filtered []string
		for _, cmd := range commands {
			if registered[cmd] {
				filtered = append(filtered, cmd)
			}
		}
		if len(filtered) > 0 {
			sort.Strings(filtered)
			result[category] = filtered
		}
	}

	// Catch any registered commands not in our category mapping
	categorized := make(map[string]bool)
	for _, commands := range categoryMap {
		for _, cmd := range commands {
			categorized[cmd] = true
		}
	}
	var uncategorized []string
	for cmd := range registered {
		if !categorized[cmd] {
			uncategorized = append(uncategorized, cmd)
		}
	}
	if len(uncategorized) > 0 {
		sort.Strings(uncategorized)
		result["Other"] = uncategorized
	}

	return result
}

// GetToolsInfo returns aggregated system, network, and blockchain information
// for the Information tab of the Tools Window.
func (a *App) GetToolsInfo() *ToolsInfo {
	info := &ToolsInfo{
		ClientName:      "TWINS Core",
		ClientVersion:   cli.Version,
		GoVersion:       runtime.Version(),
		Platform:        fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		BuildDate:       cli.BuildDate,
		DatabaseVersion: cli.DatabaseVersion,
		StartupTime:     startupTime.Unix(),
	}

	// Data directory and config (read under lock for consistency)
	a.componentsMu.RLock()
	info.DataDir = a.dataDir
	components := a.coreComponents
	guiConfig := a.guiConfig
	a.componentsMu.RUnlock()

	// Network name
	if guiConfig != nil && guiConfig.Network != "" {
		info.NetworkName = guiConfig.Network
	} else {
		info.NetworkName = "mainnet"
	}

	// Connection info from P2P
	if components != nil && components.P2PServer != nil {
		stats := components.P2PServer.GetStats()
		if stats != nil {
			info.Connections = stats.PeerCount
			info.InPeers = stats.InboundCount
			info.OutPeers = stats.OutboundCount
		}
	}

	// Blockchain info
	if components != nil && components.Blockchain != nil {
		height, _ := components.Blockchain.GetBestHeight()
		info.BlockCount = height

		// Get last block time from best block header
		block, err := components.Blockchain.GetBestBlock()
		if err == nil && block != nil {
			info.LastBlockTime = int64(block.Header.Timestamp)
		}
	}

	// Masternode count (protocol version 0 = count all)
	if components != nil && components.Masternode != nil {
		info.MasternodeCount = components.Masternode.CountEnabled(0)
	}

	return info
}

// GetPeerList returns detailed information about all connected peers.
func (a *App) GetPeerList() []PeerDetail {
	a.componentsMu.RLock()
	components := a.coreComponents
	a.componentsMu.RUnlock()

	if components == nil || components.P2PServer == nil {
		return []PeerDetail{}
	}

	peers := components.P2PServer.GetPeers()
	aliases := components.P2PServer.GetPeerAliases()
	result := make([]PeerDetail, 0, len(peers))

	for _, p := range peers {
		result = append(result, PeerDetail{
			ID:              p.ID,
			Address:         p.Address,
			Alias:           aliases[p.Address],
			Services:        p2p.ServiceFlag(p.Services).String(),
			LastSend:        p.LastSend.Unix(),
			LastRecv:        p.LastRecv.Unix(),
			BytesSent:       p.BytesSent,
			BytesReceived:   p.BytesReceived,
			ConnTime:        p.TimeConnected.Unix(),
			TimeOffset:      p.TimeOffset,
			PingTime:        p.PingTime,
			PingWait:        p.PingWait,
			ProtocolVersion: p.ProtocolVersion,
			UserAgent:       p.UserAgent,
			Inbound:         p.Inbound,
			StartHeight:     p.StartHeight,
			BanScore:        p.BanScore,
			SyncedHeaders:   p.SyncedHeaders,
			SyncedBlocks:    p.SyncedBlocks,
			SyncedHeight:    p.SyncedHeight,
			Whitelisted:     p.Whitelisted,
		})
	}

	return result
}

// GetBannedPeers returns list of banned peer addresses.
func (a *App) GetBannedPeers() []BannedPeerInfo {
	a.componentsMu.RLock()
	components := a.coreComponents
	a.componentsMu.RUnlock()

	if components == nil || components.P2PServer == nil {
		return []BannedPeerInfo{}
	}

	banned := components.P2PServer.GetBannedList()
	aliases := components.P2PServer.GetPeerAliases()
	result := make([]BannedPeerInfo, 0, len(banned))

	for _, b := range banned {
		result = append(result, BannedPeerInfo{
			Address:     b.Subnet,
			Alias:       findAliasForCIDR(b.Subnet, aliases),
			BannedUntil: b.BannedUntil,
			BanCreated:  b.BanCreated,
			Reason:      b.Reason,
		})
	}

	return result
}

// findAliasForCIDR finds an alias for a banned peer's CIDR address.
// Banned peers use CIDR notation (e.g. "1.2.3.4/32") while aliases
// are keyed by IP:Port. Scan aliases for any key starting with the IP.
func findAliasForCIDR(cidr string, aliases map[string]string) string {
	if len(aliases) == 0 {
		return ""
	}
	// Check direct CIDR key match (alias set from banned peer table)
	if alias, ok := aliases[cidr]; ok {
		return alias
	}
	// Extract IP from CIDR and scan for IP:Port key match
	ip, _, err := net.ParseCIDR(cidr)
	if err != nil {
		return ""
	}
	ipStr := ip.String()
	for addr, alias := range aliases {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			continue
		}
		if host == ipStr {
			return alias
		}
	}
	return ""
}

// BanPeer bans a peer for the specified duration.
// duration: "1h", "1d", "1w", "1y"
func (a *App) BanPeer(addr string, duration string) error {
	a.componentsMu.RLock()
	components := a.coreComponents
	a.componentsMu.RUnlock()

	if components == nil || components.P2PServer == nil {
		return fmt.Errorf("P2P server not initialized")
	}

	var banSeconds int64
	switch duration {
	case "1h":
		banSeconds = 3600
	case "1d":
		banSeconds = 86400
	case "1w":
		banSeconds = 604800
	case "1y":
		banSeconds = 31536000
	default:
		banSeconds = 86400 // default 1 day
	}

	// BanSubnet expects CIDR notation; append appropriate prefix for bare IPs
	if !strings.Contains(addr, "/") {
		ip := net.ParseIP(addr)
		if ip == nil {
			return fmt.Errorf("invalid IP address: %s", addr)
		}
		if ip.To4() != nil {
			addr = addr + "/32"
		} else {
			addr = addr + "/128"
		}
	}

	return components.P2PServer.BanSubnet(addr, banSeconds, false, "manually added")
}

// UnbanPeer removes a ban for the specified address.
func (a *App) UnbanPeer(addr string) error {
	a.componentsMu.RLock()
	components := a.coreComponents
	a.componentsMu.RUnlock()

	if components == nil || components.P2PServer == nil {
		return fmt.Errorf("P2P server not initialized")
	}

	return components.P2PServer.UnbanSubnet(addr)
}

// DisconnectPeer disconnects a specific peer by address.
func (a *App) DisconnectPeer(addr string) error {
	a.componentsMu.RLock()
	components := a.coreComponents
	a.componentsMu.RUnlock()

	if components == nil || components.P2PServer == nil {
		return fmt.Errorf("P2P server not initialized")
	}

	return components.P2PServer.DisconnectNode(addr)
}

// resolveAddr parses an address string that may or may not include a port.
// Returns the validated host IP and the full IP:Port address.
func resolveAddr(addr string, defaultPort int) (host string, fullAddr string, err error) {
	// Try IP:Port format first
	h, _, splitErr := net.SplitHostPort(addr)
	if splitErr == nil {
		// Valid IP:Port — validate the IP
		if net.ParseIP(h) == nil {
			return "", "", fmt.Errorf("invalid IP address: %s", h)
		}
		return h, addr, nil
	}

	// No port — try as bare IP and append default port
	if net.ParseIP(addr) != nil {
		full := net.JoinHostPort(addr, fmt.Sprintf("%d", defaultPort))
		return addr, full, nil
	}

	return "", "", fmt.Errorf("invalid address format: expected IP or IP:Port")
}

// getDefaultPort returns the default P2P port from chainparams.
// Must be called with componentsMu RLock held.
func (a *App) getDefaultPort() int {
	if a.node != nil && a.node.ChainParams != nil {
		return a.node.ChainParams.DefaultPort
	}
	return 37817 // mainnet fallback
}

// AddPeer attempts to connect to a peer at the given address with an optional alias.
// Accepts bare IP (defaults to network P2P port) or IP:Port.
func (a *App) AddPeer(addr string, alias string) error {
	addr = strings.TrimSpace(addr)
	alias = strings.TrimSpace(alias)
	if addr == "" {
		return fmt.Errorf("address cannot be empty")
	}

	a.componentsMu.RLock()
	components := a.coreComponents
	defaultPort := a.getDefaultPort()
	a.componentsMu.RUnlock()

	if components == nil || components.P2PServer == nil {
		return fmt.Errorf("P2P server not initialized")
	}

	// Resolve address with optional default port
	_, fullAddr, err := resolveAddr(addr, defaultPort)
	if err != nil {
		return err
	}

	if err := components.P2PServer.ConnectNode(fullAddr); err != nil {
		return err
	}

	// Set alias if provided
	if alias != "" {
		if err := components.P2PServer.SetPeerAlias(fullAddr, alias); err != nil {
			// Log but don't fail the connection
			fmt.Printf("Warning: failed to set peer alias: %v\n", err)
		}
	}

	return nil
}

// AddPeerResult represents the result of adding a single peer in batch mode.
type AddPeerResult struct {
	Line    string `json:"line"`
	Address string `json:"address"`
	Alias   string `json:"alias"`
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

// AddPeers adds multiple peers from a multi-line input string.
// Each line has format: IP[:port] [alias]
// Port and alias are optional. Blank lines are skipped.
func (a *App) AddPeers(input string) []AddPeerResult {
	a.componentsMu.RLock()
	components := a.coreComponents
	defaultPort := a.getDefaultPort()
	a.componentsMu.RUnlock()

	lines := strings.Split(input, "\n")
	var results []AddPeerResult

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		result := AddPeerResult{Line: line}

		// Split into address and alias parts.
		// First token is the address, everything after the first space is the alias.
		parts := strings.SplitN(line, " ", 2)
		rawAddr := parts[0]
		if len(parts) > 1 {
			result.Alias = strings.TrimSpace(parts[1])
		}

		// Resolve address
		_, fullAddr, err := resolveAddr(rawAddr, defaultPort)
		if err != nil {
			result.Error = err.Error()
			results = append(results, result)
			continue
		}
		result.Address = fullAddr

		// Check P2P server
		if components == nil || components.P2PServer == nil {
			result.Error = "P2P server not initialized"
			results = append(results, result)
			continue
		}

		// Connect
		if err := components.P2PServer.ConnectNode(fullAddr); err != nil {
			result.Error = err.Error()
			results = append(results, result)
			continue
		}

		// Set alias if provided
		if result.Alias != "" {
			if err := components.P2PServer.SetPeerAlias(fullAddr, result.Alias); err != nil {
				fmt.Printf("Warning: failed to set peer alias for %s: %v\n", fullAddr, err)
			}
		}

		result.Success = true
		results = append(results, result)
	}

	return results
}

// SetPeerAlias sets or updates a friendly alias for a peer address.
func (a *App) SetPeerAlias(addr string, alias string) error {
	a.componentsMu.RLock()
	components := a.coreComponents
	a.componentsMu.RUnlock()

	if components == nil || components.P2PServer == nil {
		return fmt.Errorf("P2P server not initialized")
	}

	return components.P2PServer.SetPeerAlias(addr, alias)
}

// RemovePeerAlias removes the alias for a peer address.
func (a *App) RemovePeerAlias(addr string) error {
	a.componentsMu.RLock()
	components := a.coreComponents
	a.componentsMu.RUnlock()

	if components == nil || components.P2PServer == nil {
		return fmt.Errorf("P2P server not initialized")
	}

	return components.P2PServer.RemovePeerAlias(addr)
}

// GetNetworkTraffic returns current network traffic statistics.
func (a *App) GetNetworkTraffic() *TrafficInfo {
	a.componentsMu.RLock()
	components := a.coreComponents
	a.componentsMu.RUnlock()

	if components == nil || components.P2PServer == nil {
		return &TrafficInfo{}
	}

	stats := components.P2PServer.GetStats()
	if stats == nil {
		return &TrafficInfo{}
	}

	return &TrafficInfo{
		TotalBytesRecv: stats.BytesReceived,
		TotalBytesSent: stats.BytesSent,
		PeerCount:      stats.PeerCount,
	}
}

// GetTrafficHistory returns historical network traffic samples for the requested
// time range, downsampled to at most maxSamples entries.
func (a *App) GetTrafficHistory(rangeMinutes int, maxSamples int) []TrafficSample {
	if a.trafficCollector == nil {
		return []TrafficSample{}
	}
	return a.trafficCollector.GetHistory(rangeMinutes, maxSamples)
}

// WalletRepair queues a repair action and restarts the application.
// On restart, initializeFullDaemon detects the flag and executes the repair.
func (a *App) WalletRepair(action string) error {
	validActions := map[string]bool{
		"resync": true,
		"rescan": true,
	}

	if !validActions[action] {
		return fmt.Errorf("unknown repair action: %s", action)
	}

	// Resolve data directory for flag file
	a.componentsMu.RLock()
	dataDir := a.dataDir
	a.componentsMu.RUnlock()

	if dataDir == "" {
		return fmt.Errorf("data directory not initialized")
	}

	// Persist the repair flag
	if err := writeRepairFlag(dataDir, action); err != nil {
		return fmt.Errorf("failed to queue repair: %w", err)
	}

	// Restart the application in a goroutine so this method can return success to the frontend
	go func() {
		// Small delay to allow the Wails response to reach the frontend.
		// Context-aware: if the app is already shutting down, abort the restart.
		select {
		case <-time.After(500 * time.Millisecond):
		case <-a.ctx.Done():
			return
		}
		if err := a.restartApp(); err != nil {
			fmt.Printf("Error restarting application: %v\n", err)
			// Clean up the flag file since restart failed — prevents surprise repair on next normal startup
			flagPath := filepath.Join(dataDir, repairFlagFile)
			os.Remove(flagPath)
			wailsRuntime.EventsEmit(a.ctx, "repair:error", RepairResult{
				Action:  action,
				Success: false,
				Error:   fmt.Sprintf("Failed to restart: %v", err),
			})
		}
	}()

	return nil
}

// parseCommand splits a command string into method and JSON-encoded params.
// Handles quoted strings: getblock "hash" true → method="getblock", params=["hash", true]
func parseCommand(cmd string) (string, json.RawMessage) {
	parts := tokenize(cmd)
	if len(parts) == 0 {
		return "", nil
	}

	method := parts[0]
	if len(parts) == 1 {
		return method, json.RawMessage("[]")
	}

	// Convert remaining tokens to JSON array
	params := make([]interface{}, 0, len(parts)-1)
	for _, p := range parts[1:] {
		// Try to parse as JSON value (number, bool, null, object, array)
		var val interface{}
		if err := json.Unmarshal([]byte(p), &val); err == nil {
			params = append(params, val)
		} else {
			// Treat as string
			params = append(params, p)
		}
	}

	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return method, json.RawMessage("[]")
	}

	return method, json.RawMessage(paramsJSON)
}

// tokenize splits a command string respecting quoted substrings.
func tokenize(cmd string) []string {
	var tokens []string
	var current strings.Builder
	inQuote := false
	quoteChar := byte(0)

	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]

		if inQuote {
			if ch == quoteChar {
				inQuote = false
			} else {
				current.WriteByte(ch)
			}
		} else {
			if ch == '"' || ch == '\'' {
				inQuote = true
				quoteChar = ch
			} else if ch == ' ' || ch == '\t' {
				if current.Len() > 0 {
					tokens = append(tokens, current.String())
					current.Reset()
				}
			} else {
				current.WriteByte(ch)
			}
		}
	}

	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}

	return tokens
}
