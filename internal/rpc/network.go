// Copyright (c) 2025 The TWINS Core developers
// Distributed under the MIT software license

package rpc

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/twins-dev/twins-core/internal/cli"
)

// getConnectionCountHandler handles the getconnectioncount RPC command
func (s *Server) getConnectionCountHandler(req *Request) *Response {
	if s.p2pServer == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(CodeNetworkError, "P2P networking is disabled", nil),
			ID:      req.ID,
		}
	}

	stats := s.p2pServer.GetStats()
	return &Response{
		JSONRPC: "2.0",
		Result:  stats.PeerCount,
		ID:      req.ID,
	}
}

// pingHandler handles the ping RPC command
func (s *Server) pingHandler(req *Request) *Response {
	if s.p2pServer == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(CodeNetworkError, "P2P networking is disabled", nil),
			ID:      req.ID,
		}
	}

	// Queue ping for all peers
	s.p2pServer.PingAllPeers()
	return &Response{
		JSONRPC: "2.0",
		Result:  nil,
		ID:      req.ID,
	}
}

// getPeerInfoHandler handles the getpeerinfo RPC command
func (s *Server) getPeerInfoHandler(req *Request) *Response {
	if s.p2pServer == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(CodeNetworkError, "P2P networking is disabled", nil),
			ID:      req.ID,
		}
	}

	peers := s.p2pServer.GetPeers()
	result := make([]map[string]interface{}, 0, len(peers))

	for _, peer := range peers {
		lastHeaderUpdate := int64(0)
		if !peer.LastHeaderUpdateTime.IsZero() {
			lastHeaderUpdate = peer.LastHeaderUpdateTime.Unix()
		}

		peerInfo := map[string]interface{}{
			"id":                 peer.ID,
			"addr":               peer.Address,
			"services":           fmt.Sprintf("%016x", peer.Services),
			"lastsend":           peer.LastSend.Unix(),
			"lastrecv":           peer.LastRecv.Unix(),
			"bytessent":          peer.BytesSent,
			"bytesrecv":          peer.BytesReceived,
			"conntime":           peer.TimeConnected.Unix(),
			"timeoffset":         peer.TimeOffset,
			"pingtime":           peer.PingTime,
			"version":            peer.ProtocolVersion,
			"subver":             peer.UserAgent,
			"inbound":            peer.Inbound,
			"startingheight":     peer.StartHeight,
			"banscore":           peer.BanScore,
			"synced_headers":     peer.SyncedHeaders,
			"synced_blocks":      peer.SyncedBlocks,
			"synced_height":      peer.SyncedHeight,
			"last_header_update": lastHeaderUpdate,
			"whitelisted":        peer.Whitelisted,
		}

		// Add optional fields
		if peer.AddrLocal != "" {
			peerInfo["addrlocal"] = peer.AddrLocal
		}
		if peer.PingWait > 0 {
			peerInfo["pingwait"] = peer.PingWait
		}
		if len(peer.Inflight) > 0 {
			peerInfo["inflight"] = peer.Inflight
		} else {
			// Empty array if no inflight blocks
			peerInfo["inflight"] = []int32{}
		}

		result = append(result, peerInfo)
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  result,
		ID:      req.ID,
	}
}

// addNodeHandler handles the addnode RPC command
func (s *Server) addNodeHandler(req *Request) *Response {
	if s.p2pServer == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(CodeNetworkError, "P2P networking is disabled", nil),
			ID:      req.ID,
		}
	}

	if len(req.Params) < 2 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("addnode requires 2 parameters: node and command"),
			ID:      req.ID,
		}
	}

	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("invalid parameters"),
			ID:      req.ID,
		}
	}

	node, ok := params[0].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("node must be a string"),
			ID:      req.ID,
		}
	}

	command, ok := params[1].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("command must be a string"),
			ID:      req.ID,
		}
	}

	// Validate command
	if command != "add" && command != "remove" && command != "onetry" {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("command must be 'add', 'remove', or 'onetry'"),
			ID:      req.ID,
		}
	}

	// Validate node address format
	if !isValidNodeAddress(node) {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("invalid node address format"),
			ID:      req.ID,
		}
	}

	switch command {
	case "add":
		if err := s.p2pServer.AddNode(node, true); err != nil {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewError(CodeNetworkError, err.Error(), nil),
				ID:      req.ID,
			}
		}
	case "remove":
		if err := s.p2pServer.RemoveNode(node); err != nil {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewError(CodeNetworkError, err.Error(), nil),
				ID:      req.ID,
			}
		}
	case "onetry":
		if err := s.p2pServer.ConnectNode(node); err != nil {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewError(CodeNetworkError, err.Error(), nil),
				ID:      req.ID,
			}
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  nil,
		ID:      req.ID,
	}
}

// disconnectNodeHandler handles the disconnectnode RPC command
func (s *Server) disconnectNodeHandler(req *Request) *Response {
	if s.p2pServer == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(CodeNetworkError, "P2P networking is disabled", nil),
			ID:      req.ID,
		}
	}

	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 1 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("disconnectnode requires node parameter"),
			ID:      req.ID,
		}
	}

	node, ok := params[0].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("node must be a string"),
			ID:      req.ID,
		}
	}

	if err := s.p2pServer.DisconnectNode(node); err != nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(CodeNodeNotConnected, "Node not found in connected nodes", nil),
			ID:      req.ID,
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  nil,
		ID:      req.ID,
	}
}

// getAddedNodeInfoHandler handles the getaddednodeinfo RPC command
func (s *Server) getAddedNodeInfoHandler(req *Request) *Response {
	if s.p2pServer == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(CodeNetworkError, "P2P networking is disabled", nil),
			ID:      req.ID,
		}
	}

	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 1 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("getaddednodeinfo requires dns parameter"),
			ID:      req.ID,
		}
	}

	dns, ok := params[0].(bool)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("dns must be a boolean"),
			ID:      req.ID,
		}
	}

	// Optional node filter
	var nodeFilter string
	if len(params) >= 2 {
		if nf, ok := params[1].(string); ok {
			nodeFilter = nf
		}
	}

	addedNodes := s.p2pServer.GetAddedNodes()

	// Filter if specified
	if nodeFilter != "" {
		found := false
		for _, node := range addedNodes {
			if node == nodeFilter {
				found = true
				break
			}
		}
		if !found {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewError(CodeNetworkError, "Node has not been added", nil),
				ID:      req.ID,
			}
		}
		addedNodes = []string{nodeFilter}
	}

	result := make([]map[string]interface{}, 0, len(addedNodes))

	for _, node := range addedNodes {
		nodeInfo := map[string]interface{}{
			"addednode": node,
		}

		if dns {
			// Check if node is connected
			peers := s.p2pServer.GetPeers()
			connected := false
			addresses := make([]map[string]interface{}, 0)

			// Extract host and port
			host, portStr, err := net.SplitHostPort(node)
			if err != nil {
				// Try without port - use default
				host = node
				portStr = "37817" // Default TWINS port
			}

			// Try to resolve
			ips, err := net.LookupIP(host)
			if err == nil {
				for _, ip := range ips {
					addrStr := net.JoinHostPort(ip.String(), portStr)
					addrInfo := map[string]interface{}{
						"address": addrStr,
					}

					// Check if this address is connected
					for _, peer := range peers {
						if strings.HasPrefix(peer.Address, ip.String()+":") {
							connected = true
							if peer.Inbound {
								addrInfo["connected"] = "inbound"
							} else {
								addrInfo["connected"] = "outbound"
							}
							break
						}
					}

					if _, ok := addrInfo["connected"]; !ok {
						addrInfo["connected"] = "false"
					}

					addresses = append(addresses, addrInfo)
				}
			}

			nodeInfo["connected"] = connected
			nodeInfo["addresses"] = addresses
		}

		result = append(result, nodeInfo)
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  result,
		ID:      req.ID,
	}
}

// getNetTotalsHandler handles the getnettotals RPC command
func (s *Server) getNetTotalsHandler(req *Request) *Response {
	if s.p2pServer == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(CodeNetworkError, "P2P networking is disabled", nil),
			ID:      req.ID,
		}
	}

	stats := s.p2pServer.GetStats()

	result := map[string]interface{}{
		"totalbytesrecv": stats.BytesReceived,
		"totalbytessent": stats.BytesSent,
		"timemillis":     time.Now().UnixMilli(),
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  result,
		ID:      req.ID,
	}
}

// getNetworkInfoHandler handles the getnetworkinfo RPC command
func (s *Server) getNetworkInfoHandler(req *Request) *Response {
	if s.p2pServer == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(CodeNetworkError, "P2P networking is disabled", nil),
			ID:      req.ID,
		}
	}

	stats := s.p2pServer.GetStats()

	// Build network information
	networks := []map[string]interface{}{
		{
			"name":      "ipv4",
			"limited":   false,
			"reachable": true,
			"proxy":     "",
		},
		{
			"name":      "ipv6",
			"limited":   false,
			"reachable": true,
			"proxy":     "",
		},
	}

	// Calculate time offset from peers
	timeOffset := s.calculateNetworkTimeOffset()

	result := map[string]interface{}{
		"version":         70103, // Protocol version
		"subversion":      fmt.Sprintf("/TWINS-Go:%s/", cli.Version),
		"protocolversion": 70103,
		"localservices":   fmt.Sprintf("%016x", stats.Services),
		"timeoffset":      timeOffset,
		"connections":     stats.PeerCount,
		"networks":        networks,
		"relayfee":        0.0001, // Default relay fee
		"localaddresses":  []interface{}{},
	}

	// Add local address if available
	if stats.LocalAddress != "" {
		localAddr := map[string]interface{}{
			"address": stats.LocalAddress,
			"port":    stats.ListenPort,
			"score":   1,
		}
		result["localaddresses"] = []interface{}{localAddr}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  result,
		ID:      req.ID,
	}
}

// setBanHandler handles the setban RPC command
func (s *Server) setBanHandler(req *Request) *Response {
	if s.p2pServer == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(CodeNetworkError, "P2P networking is disabled", nil),
			ID:      req.ID,
		}
	}

	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 2 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("setban requires subnet and command parameters"),
			ID:      req.ID,
		}
	}

	subnet, ok := params[0].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("subnet must be a string"),
			ID:      req.ID,
		}
	}

	command, ok := params[1].(string)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("command must be a string"),
			ID:      req.ID,
		}
	}

	if command != "add" && command != "remove" {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("command must be 'add' or 'remove'"),
			ID:      req.ID,
		}
	}

	// Parse ban time (optional)
	var banTime int64 = 24 * 60 * 60 // Default 24 hours
	if len(params) >= 3 {
		if bt, ok := params[2].(float64); ok {
			banTime = int64(bt)
		}
	}

	// Parse absolute flag (optional)
	absolute := false
	if len(params) >= 4 {
		if abs, ok := params[3].(bool); ok {
			absolute = abs
		}
	}

	// Validate subnet
	_, _, err := net.ParseCIDR(subnet)
	if err != nil {
		// Try as single IP
		ip := net.ParseIP(subnet)
		if ip == nil {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewInvalidParamsError("Invalid IP/Subnet"),
				ID:      req.ID,
			}
		}
		// Convert single IP to /32 or /128 CIDR
		if ip.To4() != nil {
			subnet = subnet + "/32"
		} else {
			subnet = subnet + "/128"
		}
	}

	switch command {
	case "add":
		if err := s.p2pServer.BanSubnet(subnet, banTime, absolute, "manually added"); err != nil {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewError(CodeNetworkError, err.Error(), nil),
				ID:      req.ID,
			}
		}
	case "remove":
		if err := s.p2pServer.UnbanSubnet(subnet); err != nil {
			return &Response{
				JSONRPC: "2.0",
				Error:   NewError(CodeNetworkError, err.Error(), nil),
				ID:      req.ID,
			}
		}
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  nil,
		ID:      req.ID,
	}
}

// listBannedHandler handles the listbanned RPC command
func (s *Server) listBannedHandler(req *Request) *Response {
	if s.p2pServer == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(CodeNetworkError, "P2P networking is disabled", nil),
			ID:      req.ID,
		}
	}

	banned := s.p2pServer.GetBannedList()
	result := make([]map[string]interface{}, 0, len(banned))

	for _, ban := range banned {
		banInfo := map[string]interface{}{
			"address":      ban.Subnet,
			"banned_until": ban.BannedUntil,
			"ban_created":  ban.BanCreated,
			"ban_reason":   ban.Reason,
		}
		result = append(result, banInfo)
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  result,
		ID:      req.ID,
	}
}

// clearBannedHandler handles the clearbanned RPC command
func (s *Server) clearBannedHandler(req *Request) *Response {
	if s.p2pServer == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(CodeNetworkError, "P2P networking is disabled", nil),
			ID:      req.ID,
		}
	}

	s.p2pServer.ClearBannedList()
	return &Response{
		JSONRPC: "2.0",
		Result:  nil,
		ID:      req.ID,
	}
}

// setNetworkActiveHandler handles the setnetworkactive RPC command
func (s *Server) setNetworkActiveHandler(req *Request) *Response {
	if s.p2pServer == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(CodeNetworkError, "P2P networking is disabled", nil),
			ID:      req.ID,
		}
	}

	var params []interface{}
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 1 {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("setnetworkactive requires state parameter"),
			ID:      req.ID,
		}
	}

	state, ok := params[0].(bool)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewInvalidParamsError("state must be a boolean"),
			ID:      req.ID,
		}
	}

	s.p2pServer.SetNetworkActive(state)
	return &Response{
		JSONRPC: "2.0",
		Result:  state,
		ID:      req.ID,
	}
}

// getSyncStatusHandler handles the getsyncstatus RPC command
// Uses enhanced sync components for detailed peer health and sync state information
func (s *Server) getSyncStatusHandler(req *Request) *Response {
	if s.p2pServer == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(CodeNetworkError, "P2P networking is disabled", nil),
			ID:      req.ID,
		}
	}

	// Get syncer and enhanced components
	syncer := s.p2pServer.GetSyncer()
	if syncer == nil {
		return &Response{
			JSONRPC: "2.0",
			Error:   NewError(CodeInternalError, "Blockchain syncer not initialized", nil),
			ID:      req.ID,
		}
	}

	healthTracker := syncer.GetHealthTracker()
	peerList := syncer.GetPeerList()
	stateMachine := syncer.GetStateMachine()

	// Get current blockchain height
	currentHeight := syncer.GetCurrentHeight()

	// Get consensus height and confidence from state machine.
	// Uses outbound-only strategy first, falls back to all peers if no healthy outbound.
	consensusHeight, confidence, _, err := stateMachine.GetConsensusHeightWithFallback()
	if err != nil {
		// Fallback to current height if consensus can't be determined at all
		consensusHeight = currentHeight
		confidence = 0.0
	}

	// Calculate blocks behind
	blocksBehind := uint32(0)
	if consensusHeight > currentHeight {
		blocksBehind = consensusHeight - currentHeight
	}

	// Get sync state from state machine
	state := stateMachine.GetState()
	stateString := state.String()

	// Calculate progress percentage
	progress := 100.0
	if consensusHeight > 0 {
		progress = (float64(currentHeight) / float64(consensusHeight)) * 100.0
		if progress > 100.0 {
			progress = 100.0
		}
	}

	// Get current sync peer
	currentPeer := syncer.GetCurrentPeer()

	// Get round count from peer list
	roundCount := peerList.GetRoundCount()

	// Build detailed peer list with health information
	peerAddrs := peerList.GetAllPeers()
	detailedPeers := make([]map[string]interface{}, 0, len(peerAddrs))

	for _, addr := range peerAddrs {
		stats := healthTracker.GetStats(addr)

		peerInfo := map[string]interface{}{
			"address":           stats.Address,
			"height":            stats.TipHeight,
			"health_score":      fmt.Sprintf("%.1f", stats.HealthScore),
			"error_score":       fmt.Sprintf("%.1f", stats.ErrorScore),
			"blocks_delivered":  stats.BlocksDelivered,
			"bytes_delivered":   stats.BytesDelivered,
			"avg_batch_time_ms": int64(stats.ResponseTimeAvg.Milliseconds()),
			"masternode":        stats.IsMasternode,
		}

		// Add masternode tier if applicable
		if stats.IsMasternode {
			tierName := "unknown"
			switch stats.Tier {
			case 0:
				tierName = "none"
			case 1:
				tierName = "bronze"
			case 2:
				tierName = "silver"
			case 3:
				tierName = "gold"
			case 4:
				tierName = "platinum"
			}
			peerInfo["masternode_tier"] = tierName
		}

		// Add cooldown status
		if time.Now().Before(stats.CooldownUntil) {
			peerInfo["on_cooldown"] = true
			peerInfo["cooldown_remaining"] = int(time.Until(stats.CooldownUntil).Seconds())
		} else {
			peerInfo["on_cooldown"] = false
		}

		// Indicate if this is the current sync peer
		if addr == currentPeer {
			peerInfo["current_sync_peer"] = true
		}

		detailedPeers = append(detailedPeers, peerInfo)
	}

	// Get reorg status
	reorgPaused := stateMachine.IsReorgPaused()

	// Calculate ETA if syncing
	eta := int64(0)
	blocksPerSec := 0.0
	if blocksBehind > 0 {
		// Use spike-resistant blocks per second calculation
		blocksPerSec = syncer.GetBlocksPerSec()

		// Fallback to conservative estimate if no data available
		if blocksPerSec <= 0 {
			blocksPerSec = 0.5
		}

		eta = int64(float64(blocksBehind) / blocksPerSec)
	}

	// Build result
	result := map[string]interface{}{
		// State information
		"state":            stateString,
		"current_height":   currentHeight,
		"consensus_height": consensusHeight,
		"blocks_behind":    blocksBehind,
		"confidence":       fmt.Sprintf("%.1f%%", confidence*100),
		"progress":         fmt.Sprintf("%.2f%%", progress),

		// Sync information
		"current_peer":     currentPeer,
		"rounds_completed": roundCount,
		"reorg_paused":     reorgPaused,

		// Peer statistics
		"total_peers":      len(peerAddrs),
		"healthy_peers":    countHealthyPeers(detailedPeers),
		"masternode_peers": countMasternodes(detailedPeers),
		"peers":            detailedPeers,

		// ETA information
		"eta_seconds":    eta,
		"eta_string":     formatDuration(time.Duration(eta) * time.Second),
		"blocks_per_sec": math.Round(blocksPerSec*100) / 100,
	}

	return &Response{
		JSONRPC: "2.0",
		Result:  result,
		ID:      req.ID,
	}
}

// countHealthyPeers counts peers not on cooldown
func countHealthyPeers(peers []map[string]interface{}) int {
	count := 0
	for _, peer := range peers {
		if cooldown, ok := peer["on_cooldown"].(bool); ok && !cooldown {
			count++
		}
	}
	return count
}

// countMasternodes counts the number of masternodes in the peer list
func countMasternodes(peers []map[string]interface{}) int {
	count := 0
	for _, peer := range peers {
		if mn, ok := peer["masternode"].(bool); ok && mn {
			count++
		}
	}
	return count
}

// formatDuration formats a duration into a human-readable string
func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}

	seconds := int(d.Seconds())
	minutes := seconds / 60
	hours := minutes / 60
	days := hours / 24

	if days > 0 {
		return fmt.Sprintf("%dd %dh", days, hours%24)
	} else if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes%60)
	} else if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds%60)
	}
	return fmt.Sprintf("%ds", seconds)
}

// registerNetworkHandlers registers network-related RPC handlers
func (s *Server) registerNetworkHandlers() {
	s.RegisterHandler("getconnectioncount", s.getConnectionCountHandler)
	s.RegisterHandler("ping", s.pingHandler)
	s.RegisterHandler("getpeerinfo", s.getPeerInfoHandler)
	s.RegisterHandler("addnode", s.addNodeHandler)
	s.RegisterHandler("disconnectnode", s.disconnectNodeHandler)
	s.RegisterHandler("getaddednodeinfo", s.getAddedNodeInfoHandler)
	s.RegisterHandler("getnettotals", s.getNetTotalsHandler)
	s.RegisterHandler("getnetworkinfo", s.getNetworkInfoHandler)
	s.RegisterHandler("setban", s.setBanHandler)
	s.RegisterHandler("listbanned", s.listBannedHandler)
	s.RegisterHandler("clearbanned", s.clearBannedHandler)
	s.RegisterHandler("setnetworkactive", s.setNetworkActiveHandler)
	s.RegisterHandler("getsyncstatus", s.getSyncStatusHandler)
}

// Helper function to validate node address format
func isValidNodeAddress(addr string) bool {
	// Must be in format "host:port" or just "host"
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		// No port specified, check if it's a valid hostname/IP
		host = addr
	}

	// Validate host
	if net.ParseIP(host) != nil {
		return true
	}

	// Check if it's a valid hostname
	if len(host) > 0 && len(host) <= 255 {
		// If port was specified, validate it
		if port != "" {
			if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
				return false
			}
		}
		return true
	}

	return false
}

// calculateNetworkTimeOffset calculates the median time offset from connected peers
// Returns the offset in seconds (positive means our clock is ahead, negative means behind)
func (s *Server) calculateNetworkTimeOffset() int64 {
	if s.p2pServer == nil {
		return 0
	}

	// Get peer information
	peers := s.p2pServer.GetPeers()
	if len(peers) == 0 {
		return 0
	}

	// Collect time offsets from all peers
	// Time offset = peer_time - our_time
	// If positive, our clock is behind; if negative, our clock is ahead
	offsets := make([]int64, 0, len(peers))

	for _, peer := range peers {
		// Use TimeOffset calculated during handshake
		// TimeOffset is already computed in GetPeers() from peer version message
		offsets = append(offsets, peer.TimeOffset)
	}

	// If we don't have peer timestamps yet, return 0
	if len(offsets) == 0 {
		return 0
	}

	// Calculate median offset to avoid outliers
	// Sort offsets using standard library (O(n log n) vs O(n²) bubble sort)
	sort.Slice(offsets, func(i, j int) bool {
		return offsets[i] < offsets[j]
	})

	// Return median
	median := offsets[len(offsets)/2]
	if len(offsets)%2 == 0 {
		median = (offsets[len(offsets)/2-1] + offsets[len(offsets)/2]) / 2
	}

	// Negate the offset so positive means our clock is ahead
	// (matching Bitcoin Core convention)
	return -median
}
