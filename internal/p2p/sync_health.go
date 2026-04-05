package p2p

import (
	"math"
	"sync"
	"time"

	"github.com/twins-dev/twins-core/pkg/types"
)

// Error type constants
type ErrorType int

const (
	ErrorTypeInvalidBlock ErrorType = iota
	ErrorTypeTimeout
	ErrorTypeConnectionDrop
	ErrorTypeSlowResponse
	ErrorTypeSendFailed
	ErrorTypeForkDetected // Peer is on a different fork
)

// Error weights for scoring (gradient system instead of instant ban)
const (
	ErrorWeightInvalidBlock   = 2.0 // Malicious or incompatible peer
	ErrorWeightTimeout        = 1.0 // Network or load issue
	ErrorWeightConnectionDrop = 0.5 // Not necessarily peer's fault
	ErrorWeightSlowResponse   = 0.5 // Performance issue
	ErrorWeightSendFailed     = 1.0 // Communication problem
	ErrorWeightForkDetected   = 1.5 // On wrong fork - serious but might recover
)

// Health constants
const (
	ErrorScoreCooldownThreshold = 5.0              // Trigger cooldown at 5 error points
	CooldownDuration            = 5 * time.Minute  // 5 minute cooldown
	HealthDecayTime             = 1 * time.Hour    // Reset to neutral after 1 hour
	HealthThreshold             = 15.0             // Minimum score to be healthy
	BaseHealthScore             = 50.0             // Neutral starting score
	StaleHeaderTimeout          = 10 * time.Minute // Disconnect outbound peers with no header updates after this duration
	StalePingHeightTimeout      = 10 * time.Minute // Disconnect 70928 peers whose ping height hasn't advanced
	StalePingHealthPenalty      = 15.0             // Health score penalty for stale ping height
)

// PeerHealthStats tracks health-specific statistics for a peer
// This extends the base PeerStats with sync health information
type PeerHealthStats struct {
	// Identity
	Address      string
	IsMasternode bool
	Tier         MasternodeTier

	// Connection direction
	IsOutbound bool // true if we connected to them (outbound)
	IsInbound  bool // true if they connected to us (inbound)

	// Performance metrics
	TipHeight       uint32
	ResponseTimeAvg time.Duration // Moving average of response times
	BlocksDelivered uint64
	BytesDelivered  uint64

	// Error tracking (weighted)
	ErrorScore    float64   // Accumulated error score (0-5 triggers cooldown)
	LastErrorTime time.Time // When last error occurred
	CooldownUntil time.Time // Set to now+5min after ErrorScore >= 5

	// Health scoring
	HealthScore     float64   // 0-100, calculated from performance and errors
	LastInteraction time.Time // Last time we interacted with this peer
	FirstSeen       time.Time // When peer was first discovered

	// Reorg tracking
	ReorgCount    int
	LastReorgTime time.Time

	// Sync state tracking (for getpeerinfo RPC)
	BestKnownHeight      uint32    // Best known block height from this peer (from headers)
	CommonHeight         uint32    // Last block height we have in common with this peer
	InFlight             []uint32  // Heights of blocks currently being requested from this peer
	LastHeaderUpdateTime time.Time // Last time BestKnownHeight was updated (for stale detection)

	// Stale ping height detection (protocol 70928+)
	LastPingHeight           uint32    // Last height reported via ping/pong (0 = no data yet)
	LastPingHeightChangeTime time.Time // When LastPingHeight last changed (zero = never received ping height)

	// Internal tracking
	responseTimeSamples []time.Duration // For moving average calculation
	mu                  sync.RWMutex
}

// blockAnnouncement tracks which peers announced a specific block
type blockAnnouncement struct {
	peers      []string  // Peer addresses that announced this block
	recordTime time.Time // When first announcement was recorded
}

// PeerHealthTracker manages health statistics for all peers
type PeerHealthTracker struct {
	peers map[string]*PeerHealthStats // key: "ip:port"
	mu    sync.RWMutex

	// Block announcement cache: hash -> announcement record
	// Used to update heights for all peers who announced a block when it's saved
	blockAnnouncements map[types.Hash]*blockAnnouncement
	announcementsMu    sync.RWMutex

	// Cleanup goroutine control
	stopCleanup chan struct{}
}

// NewPeerHealthTracker creates a new peer health tracker
func NewPeerHealthTracker() *PeerHealthTracker {
	pht := &PeerHealthTracker{
		peers:              make(map[string]*PeerHealthStats),
		blockAnnouncements: make(map[types.Hash]*blockAnnouncement),
		stopCleanup:        make(chan struct{}),
	}

	// Start cleanup goroutine for expired announcements
	go pht.announcementCleanupLoop()

	return pht
}

// RecordPeerDiscovered registers a newly discovered peer
func (pht *PeerHealthTracker) RecordPeerDiscovered(address string, tipHeight uint32, isMasternode bool, tier MasternodeTier, isOutbound bool) {
	pht.mu.Lock()
	defer pht.mu.Unlock()

	if _, exists := pht.peers[address]; !exists {
		now := time.Now()
		pht.peers[address] = &PeerHealthStats{
			Address:              address,
			IsMasternode:         isMasternode,
			Tier:                 tier,
			IsOutbound:           isOutbound,
			IsInbound:            !isOutbound,
			TipHeight:            tipHeight,
			HealthScore:          BaseHealthScore,
			FirstSeen:            now,
			LastInteraction:      now,
			LastHeaderUpdateTime: now, // Grace period: new peers get StaleHeaderTimeout before first check
			responseTimeSamples:  make([]time.Duration, 0, 10),
		}
	} else {
		// Update existing peer info
		stats := pht.peers[address]
		stats.mu.Lock()
		stats.TipHeight = tipHeight
		stats.IsMasternode = isMasternode
		stats.Tier = tier
		// Update direction if it changed (shouldn't happen normally)
		stats.IsOutbound = isOutbound
		stats.IsInbound = !isOutbound
		stats.mu.Unlock()
	}
}

// RecordSuccess records a successful interaction with a peer
func (pht *PeerHealthTracker) RecordSuccess(address string, blocksReceived uint64, bytesReceived uint64, duration time.Duration) {
	pht.mu.RLock()
	stats, exists := pht.peers[address]
	pht.mu.RUnlock()

	if !exists {
		return
	}

	stats.mu.Lock()
	defer stats.mu.Unlock()

	stats.BlocksDelivered += blocksReceived
	stats.BytesDelivered += bytesReceived
	stats.LastInteraction = time.Now()

	// Successful interactions gradually recover from errors
	if stats.ErrorScore > 0 {
		recovery := 0.7 // Base recovery per success
		if blocksReceived > 0 {
			recovery += math.Min(float64(blocksReceived)/100.0, 2.0)
		}
		if bytesReceived > 0 {
			recovery += math.Min(float64(bytesReceived)/1_000_000.0, 1.0)
		}

		stats.ErrorScore -= recovery
		if stats.ErrorScore < 0 {
			stats.ErrorScore = 0
		}

		// Clear cooldown once we're below the threshold
		if stats.ErrorScore < ErrorScoreCooldownThreshold {
			stats.CooldownUntil = time.Time{}
		}
	}

	// Update response time moving average
	stats.responseTimeSamples = append(stats.responseTimeSamples, duration)
	if len(stats.responseTimeSamples) > 10 {
		stats.responseTimeSamples = stats.responseTimeSamples[1:] // Keep last 10 samples
	}

	// Calculate moving average
	var total time.Duration
	for _, sample := range stats.responseTimeSamples {
		total += sample
	}
	stats.ResponseTimeAvg = total / time.Duration(len(stats.responseTimeSamples))

	// Recalculate health score
	stats.HealthScore = stats.calculateHealthLocked()
}

// RecordError records an error for a peer with weighted scoring
func (pht *PeerHealthTracker) RecordError(address string, errorType ErrorType) {
	pht.mu.RLock()
	stats, exists := pht.peers[address]
	pht.mu.RUnlock()

	if !exists {
		return
	}

	stats.mu.Lock()
	defer stats.mu.Unlock()

	// Add weighted error score
	weight := pht.getErrorWeight(errorType)
	stats.ErrorScore += weight
	stats.LastErrorTime = time.Now()
	stats.LastInteraction = time.Now()

	// Check if we should trigger cooldown
	if stats.ErrorScore >= ErrorScoreCooldownThreshold {
		stats.CooldownUntil = time.Now().Add(CooldownDuration)
		// Note: Error score will be reset when cooldown expires (checked in IsHealthy)
	}

	// Recalculate health score
	stats.HealthScore = stats.calculateHealthLocked()
}

// getErrorWeight returns the weight for an error type
func (pht *PeerHealthTracker) getErrorWeight(errorType ErrorType) float64 {
	switch errorType {
	case ErrorTypeInvalidBlock:
		return ErrorWeightInvalidBlock
	case ErrorTypeTimeout:
		return ErrorWeightTimeout
	case ErrorTypeConnectionDrop:
		return ErrorWeightConnectionDrop
	case ErrorTypeSlowResponse:
		return ErrorWeightSlowResponse
	case ErrorTypeSendFailed:
		return ErrorWeightSendFailed
	case ErrorTypeForkDetected:
		return ErrorWeightForkDetected
	default:
		return 1.0
	}
}

// IsHealthy checks if a peer is healthy and available for sync
func (pht *PeerHealthTracker) IsHealthy(address string) bool {
	pht.mu.RLock()
	stats, exists := pht.peers[address]
	pht.mu.RUnlock()

	if !exists {
		return false
	}

	stats.mu.RLock()
	cooldownUntil := stats.CooldownUntil
	stats.mu.RUnlock()

	if time.Now().Before(cooldownUntil) {
		return false
	}

	score := pht.GetHealthScore(address)
	return score >= HealthThreshold
}

// RemovePeer removes a peer from the health tracker when they disconnect
func (pht *PeerHealthTracker) RemovePeer(address string) {
	pht.mu.Lock()
	defer pht.mu.Unlock()
	delete(pht.peers, address)
}

// PruneDisconnectedPeers removes health tracker entries for peers that are no
// longer in the connected set. connectedAddrs should contain the addresses of
// all currently connected peers. Returns the number of stale entries removed.
func (pht *PeerHealthTracker) PruneDisconnectedPeers(connectedAddrs map[string]struct{}) int {
	pht.mu.Lock()
	defer pht.mu.Unlock()

	pruned := 0
	for addr := range pht.peers {
		if _, connected := connectedAddrs[addr]; !connected {
			delete(pht.peers, addr)
			pruned++
		}
	}
	return pruned
}

// GetHealthScore returns the current health score for a peer
func (pht *PeerHealthTracker) GetHealthScore(address string) float64 {
	pht.mu.RLock()
	stats, exists := pht.peers[address]
	pht.mu.RUnlock()

	if !exists {
		return 0
	}

	stats.mu.Lock()
	defer stats.mu.Unlock()

	now := time.Now()

	// Reset error score if cooldown expired
	if stats.ErrorScore >= ErrorScoreCooldownThreshold && !now.Before(stats.CooldownUntil) {
		stats.ErrorScore = 0
		stats.CooldownUntil = time.Time{} // Clear cooldown
	}

	return stats.calculateHealthLocked()
}

// GetStats returns a copy of peer health statistics
func (pht *PeerHealthTracker) GetStats(address string) *PeerHealthStats {
	pht.mu.RLock()
	stats, exists := pht.peers[address]
	pht.mu.RUnlock()

	if !exists {
		return nil
	}

	stats.mu.RLock()
	defer stats.mu.RUnlock()

	// Return a copy to avoid race conditions
	statsCopy := &PeerHealthStats{
		Address:         stats.Address,
		IsMasternode:    stats.IsMasternode,
		Tier:            stats.Tier,
		IsOutbound:      stats.IsOutbound,
		IsInbound:       stats.IsInbound,
		TipHeight:       stats.TipHeight,
		ResponseTimeAvg: stats.ResponseTimeAvg,
		BlocksDelivered: stats.BlocksDelivered,
		BytesDelivered:  stats.BytesDelivered,
		ErrorScore:      stats.ErrorScore,
		LastErrorTime:   stats.LastErrorTime,
		CooldownUntil:   stats.CooldownUntil,
		HealthScore:     stats.calculateHealthLocked(),
		LastInteraction: stats.LastInteraction,
		FirstSeen:       stats.FirstSeen,
		ReorgCount:      stats.ReorgCount,
		LastReorgTime:   stats.LastReorgTime,
		// Sync state tracking
		BestKnownHeight:      stats.BestKnownHeight,
		CommonHeight:         stats.CommonHeight,
		LastHeaderUpdateTime: stats.LastHeaderUpdateTime,
		// Stale ping height detection
		LastPingHeight:           stats.LastPingHeight,
		LastPingHeightChangeTime: stats.LastPingHeightChangeTime,
	}

	// Copy InFlight slice
	if len(stats.InFlight) > 0 {
		statsCopy.InFlight = make([]uint32, len(stats.InFlight))
		copy(statsCopy.InFlight, stats.InFlight)
	}

	return statsCopy
}

// GetAllPeers returns a map of all peer health statistics
func (pht *PeerHealthTracker) GetAllPeers() map[string]*PeerHealthStats {
	pht.mu.RLock()
	defer pht.mu.RUnlock()

	result := make(map[string]*PeerHealthStats)
	for addr, stats := range pht.peers {
		stats.mu.RLock()
		result[addr] = &PeerHealthStats{
			Address:              stats.Address,
			IsMasternode:         stats.IsMasternode,
			Tier:                 stats.Tier,
			IsOutbound:           stats.IsOutbound,
			IsInbound:            stats.IsInbound,
			TipHeight:            stats.TipHeight,
			ResponseTimeAvg:      stats.ResponseTimeAvg,
			BlocksDelivered:      stats.BlocksDelivered,
			BytesDelivered:       stats.BytesDelivered,
			ErrorScore:           stats.ErrorScore,
			LastErrorTime:        stats.LastErrorTime,
			CooldownUntil:        stats.CooldownUntil,
			HealthScore:          stats.calculateHealthLocked(),
			LastInteraction:      stats.LastInteraction,
			FirstSeen:            stats.FirstSeen,
			ReorgCount:           stats.ReorgCount,
			LastReorgTime:        stats.LastReorgTime,
			LastHeaderUpdateTime:     stats.LastHeaderUpdateTime,
			LastPingHeight:           stats.LastPingHeight,
			LastPingHeightChangeTime: stats.LastPingHeightChangeTime,
		}
		stats.mu.RUnlock()
	}
	return result
}

// GetPeerStats returns a copy of the health stats for a single peer, or nil if not found.
func (pht *PeerHealthTracker) GetPeerStats(addr string) *PeerHealthStats {
	pht.mu.RLock()
	defer pht.mu.RUnlock()

	stats, exists := pht.peers[addr]
	if !exists {
		return nil
	}

	stats.mu.RLock()
	defer stats.mu.RUnlock()

	return &PeerHealthStats{
		Address:              stats.Address,
		IsMasternode:         stats.IsMasternode,
		Tier:                 stats.Tier,
		IsOutbound:           stats.IsOutbound,
		IsInbound:            stats.IsInbound,
		TipHeight:               stats.TipHeight,
		CooldownUntil:           stats.CooldownUntil,
		LastHeaderUpdateTime:    stats.LastHeaderUpdateTime,
		LastPingHeight:           stats.LastPingHeight,
		LastPingHeightChangeTime: stats.LastPingHeightChangeTime,
	}
}

// HasHealthyPeers checks if there are any healthy peers available
func (pht *PeerHealthTracker) HasHealthyPeers() bool {
	pht.mu.RLock()
	defer pht.mu.RUnlock()

	for addr := range pht.peers {
		if pht.IsHealthy(addr) {
			return true
		}
	}
	return false
}

// GetBestPeers returns the N best peers based on health score
func (pht *PeerHealthTracker) GetBestPeers(n int) []string {
	pht.mu.RLock()
	defer pht.mu.RUnlock()

	// Collect all peers with their scores
	type peerScore struct {
		address string
		score   float64
	}

	scores := make([]peerScore, 0, len(pht.peers))
	for addr, stats := range pht.peers {
		stats.mu.RLock()
		score := stats.calculateHealthLocked()
		stats.mu.RUnlock()

		scores = append(scores, peerScore{addr, score})
	}

	// Sort by score (descending)
	for i := 0; i < len(scores); i++ {
		for j := i + 1; j < len(scores); j++ {
			if scores[j].score > scores[i].score {
				scores[i], scores[j] = scores[j], scores[i]
			}
		}
	}

	// Return top N
	result := make([]string, 0, n)
	for i := 0; i < n && i < len(scores); i++ {
		result = append(result, scores[i].address)
	}
	return result
}

// GetStaleHeaderPeers returns addresses of outbound peers whose LastHeaderUpdateTime
// is older than staleDuration. Used to detect peers that stopped sending header updates.
func (pht *PeerHealthTracker) GetStaleHeaderPeers(staleDuration time.Duration) []string {
	pht.mu.RLock()
	defer pht.mu.RUnlock()

	cutoff := time.Now().Add(-staleDuration)
	var stale []string

	for addr, stats := range pht.peers {
		stats.mu.RLock()
		isOutbound := stats.IsOutbound
		lastUpdate := stats.LastHeaderUpdateTime
		stats.mu.RUnlock()

		// Only check outbound peers
		if !isOutbound {
			continue
		}

		if lastUpdate.Before(cutoff) {
			stale = append(stale, addr)
		}
	}

	return stale
}

// GetUnhealthyPeers returns addresses of outbound peers whose health score has dropped
// below HealthThreshold. These peers are candidates for disconnection to free connection
// slots for healthier peers. Only outbound peers are returned since we control those connections.
func (pht *PeerHealthTracker) GetUnhealthyPeers() []string {
	// Phase 1: collect candidate addresses under map lock
	pht.mu.RLock()
	var candidates []string
	for addr, stats := range pht.peers {
		stats.mu.RLock()
		isOutbound := stats.IsOutbound
		cooldownUntil := stats.CooldownUntil
		stats.mu.RUnlock()

		if !isOutbound {
			continue
		}

		// Skip peers on cooldown — they're already being managed
		if time.Now().Before(cooldownUntil) {
			continue
		}

		candidates = append(candidates, addr)
	}
	pht.mu.RUnlock()

	// Phase 2: check health scores outside map lock (avoids nested RLock)
	var unhealthy []string
	for _, addr := range candidates {
		if pht.GetHealthScore(addr) < HealthThreshold {
			unhealthy = append(unhealthy, addr)
		}
	}

	return unhealthy
}

// UpdatePingHeight updates the height reported by a peer via ping/pong (protocol 70928+).
// Tracks when the height last changed for stale detection.
func (pht *PeerHealthTracker) UpdatePingHeight(address string, height uint32) {
	pht.mu.RLock()
	stats, exists := pht.peers[address]
	pht.mu.RUnlock()

	if !exists || height == 0 {
		return
	}

	stats.mu.Lock()
	defer stats.mu.Unlock()

	if height != stats.LastPingHeight {
		stats.LastPingHeight = height
		stats.LastPingHeightChangeTime = time.Now()
	}
}

// GetStalePingHeightPeers returns addresses of peers whose ping-reported height
// hasn't changed within staleDuration. Only considers peers that have received
// at least one ping height update (protocol 70928+ peers).
func (pht *PeerHealthTracker) GetStalePingHeightPeers(staleDuration time.Duration) []string {
	pht.mu.RLock()
	defer pht.mu.RUnlock()

	cutoff := time.Now().Add(-staleDuration)
	var stale []string

	for addr, stats := range pht.peers {
		stats.mu.RLock()
		changeTime := stats.LastPingHeightChangeTime
		isOutbound := stats.IsOutbound
		stats.mu.RUnlock()

		// Only check outbound peers (inbound peers may be syncing from us)
		if !isOutbound {
			continue
		}

		// Skip peers that never received a ping height (70927 or newly connected)
		if changeTime.IsZero() {
			continue
		}

		if changeTime.Before(cutoff) {
			stale = append(stale, addr)
		}
	}

	return stale
}

// UpdateTipHeight updates the tip height for a peer
func (pht *PeerHealthTracker) UpdateTipHeight(address string, height uint32) {
	pht.mu.RLock()
	stats, exists := pht.peers[address]
	pht.mu.RUnlock()

	if !exists {
		return
	}

	stats.mu.Lock()
	defer stats.mu.Unlock()

	stats.TipHeight = height
	// Also update BestKnownHeight if this is higher (synced_headers in getpeerinfo)
	if height > stats.BestKnownHeight {
		stats.BestKnownHeight = height
		stats.LastHeaderUpdateTime = time.Now()
	}
	stats.LastInteraction = time.Now()
}

// UpdateBestKnownHeight updates the best known block height from a peer (from headers)
// This also updates TipHeight since BestKnownHeight >= TipHeight by definition
func (pht *PeerHealthTracker) UpdateBestKnownHeight(address string, height uint32) {
	pht.mu.RLock()
	stats, exists := pht.peers[address]
	pht.mu.RUnlock()

	if !exists {
		return
	}

	stats.mu.Lock()
	defer stats.mu.Unlock()

	// Only update if higher (peer learned about more blocks)
	if height > stats.BestKnownHeight {
		stats.BestKnownHeight = height
		stats.LastHeaderUpdateTime = time.Now()
	}
	// Also update TipHeight - if peer sent headers up to this height,
	// their chain tip is at least this high. This ensures consensus
	// calculation uses the most recent peer height information.
	if height > stats.TipHeight {
		stats.TipHeight = height
	}
	stats.LastInteraction = time.Now()
}

// GetBestKnownHeight returns the best known block height for a peer (from headers/inv).
// Returns 0 if the peer is not tracked.
func (pht *PeerHealthTracker) GetBestKnownHeight(address string) uint32 {
	pht.mu.RLock()
	stats, exists := pht.peers[address]
	pht.mu.RUnlock()

	if !exists {
		return 0
	}

	stats.mu.RLock()
	h := stats.BestKnownHeight
	stats.mu.RUnlock()
	return h
}

// UpdateCommonHeight updates the last common block height with a peer
func (pht *PeerHealthTracker) UpdateCommonHeight(address string, height uint32) {
	pht.mu.RLock()
	stats, exists := pht.peers[address]
	pht.mu.RUnlock()

	if !exists {
		return
	}

	stats.mu.Lock()
	defer stats.mu.Unlock()

	stats.CommonHeight = height
	stats.LastInteraction = time.Now()
}

// AddInFlight adds a block height to the in-flight list for a peer
func (pht *PeerHealthTracker) AddInFlight(address string, height uint32) {
	pht.mu.RLock()
	stats, exists := pht.peers[address]
	pht.mu.RUnlock()

	if !exists {
		return
	}

	stats.mu.Lock()
	defer stats.mu.Unlock()

	// Check if already in list
	for _, h := range stats.InFlight {
		if h == height {
			return
		}
	}
	stats.InFlight = append(stats.InFlight, height)
}

// RemoveInFlight removes a block height from the in-flight list for a peer
func (pht *PeerHealthTracker) RemoveInFlight(address string, height uint32) {
	pht.mu.RLock()
	stats, exists := pht.peers[address]
	pht.mu.RUnlock()

	if !exists {
		return
	}

	stats.mu.Lock()
	defer stats.mu.Unlock()

	for i, h := range stats.InFlight {
		if h == height {
			stats.InFlight = append(stats.InFlight[:i], stats.InFlight[i+1:]...)
			return
		}
	}
}

// ClearInFlight clears all in-flight blocks for a peer
func (pht *PeerHealthTracker) ClearInFlight(address string) {
	pht.mu.RLock()
	stats, exists := pht.peers[address]
	pht.mu.RUnlock()

	if !exists {
		return
	}

	stats.mu.Lock()
	defer stats.mu.Unlock()

	stats.InFlight = nil
}

// GetInFlight returns a copy of the in-flight block heights for a peer
func (pht *PeerHealthTracker) GetInFlight(address string) []uint32 {
	pht.mu.RLock()
	stats, exists := pht.peers[address]
	pht.mu.RUnlock()

	if !exists {
		return nil
	}

	stats.mu.RLock()
	defer stats.mu.RUnlock()

	if len(stats.InFlight) == 0 {
		return nil
	}

	// Return a copy to avoid race conditions
	result := make([]uint32, len(stats.InFlight))
	copy(result, stats.InFlight)
	return result
}

// RecordReorg records a reorg for tracking
func (pht *PeerHealthTracker) RecordReorg(address string) {
	pht.mu.RLock()
	stats, exists := pht.peers[address]
	pht.mu.RUnlock()

	if !exists {
		return
	}

	stats.mu.Lock()
	defer stats.mu.Unlock()

	stats.ReorgCount++
	stats.LastReorgTime = time.Now()
}

// calculateHealthLocked calculates the health score (must hold stats.mu)
func (stats *PeerHealthStats) calculateHealthLocked() float64 {
	now := time.Now()

	// Check for health decay (reset to neutral after 1 hour of inactivity)
	timeSinceInteraction := now.Sub(stats.LastInteraction)
	if timeSinceInteraction > HealthDecayTime {
		stats.ErrorScore = 0
		stats.CooldownUntil = time.Time{}
		return BaseHealthScore
	}

	score := BaseHealthScore

	// Bonus for block delivery
	deliveryBonus := math.Min(float64(stats.BlocksDelivered)/1000.0*20, 20)
	score += deliveryBonus

	// Penalty for errors
	errorPenalty := stats.ErrorScore * 5
	score -= errorPenalty

	// Bonus/penalty for response time
	if stats.ResponseTimeAvg > 0 {
		if stats.ResponseTimeAvg < 1*time.Second {
			score += 10 // Fast response
		} else if stats.ResponseTimeAvg < 3*time.Second {
			score += 5 // Good response
		} else if stats.ResponseTimeAvg > 10*time.Second {
			score -= 10 // Slow response
		}
	}

	// Penalty for stale ping height (70928 peers whose height stopped advancing)
	if !stats.LastPingHeightChangeTime.IsZero() {
		staleDuration := now.Sub(stats.LastPingHeightChangeTime)
		if staleDuration > StalePingHeightTimeout {
			score -= StalePingHealthPenalty
		}
	}

	// Bonus for masternodes (tier-based)
	if stats.IsMasternode {
		switch stats.Tier {
		case TierPlatinum:
			score += 30
		case TierGold:
			score += 20
		case TierSilver:
			score += 10
		case TierBronze:
			score += 5
		}
	}

	// Clamp to 0-100 range
	score = math.Max(0, math.Min(100, score))

	return score
}

// ============================================================================
// Block Announcement Tracking
// These methods implement blockchain.BlockAnnouncementNotifier interface
// to update peer heights when blocks are saved to chain
// ============================================================================

const (
	// AnnouncementCacheTTL is how long to keep block announcements in cache
	AnnouncementCacheTTL = 5 * time.Minute
	// AnnouncementCleanupInterval is how often to run cleanup
	AnnouncementCleanupInterval = 1 * time.Minute
)

// RecordBlockAnnouncement records that a peer announced a block hash via INV
// Called from handleInvMessage when processing block inventory
func (pht *PeerHealthTracker) RecordBlockAnnouncement(peerAddr string, blockHash types.Hash) {
	pht.announcementsMu.Lock()
	defer pht.announcementsMu.Unlock()

	announcement, exists := pht.blockAnnouncements[blockHash]
	if !exists {
		announcement = &blockAnnouncement{
			peers:      make([]string, 0, 4), // Pre-allocate for typical case
			recordTime: time.Now(),
		}
		pht.blockAnnouncements[blockHash] = announcement
	}

	// Avoid duplicates (same peer announcing same block twice)
	for _, p := range announcement.peers {
		if p == peerAddr {
			return
		}
	}

	announcement.peers = append(announcement.peers, peerAddr)
}

// NotifyBlocksProcessed updates heights for all peers who announced these blocks
// Implements blockchain.BlockAnnouncementNotifier interface
// Called from unified_processor.go after batch.Commit()
func (pht *PeerHealthTracker) NotifyBlocksProcessed(blocks []*types.Block, heights map[types.Hash]uint32) {
	if len(blocks) == 0 {
		return
	}

	// Collect all peers to update (under lock)
	pht.announcementsMu.Lock()
	type peerUpdate struct {
		addr   string
		height uint32
	}
	var updates []peerUpdate

	for _, block := range blocks {
		blockHash := block.Hash()
		height, hasHeight := heights[blockHash]
		if !hasHeight {
			continue
		}

		announcement, exists := pht.blockAnnouncements[blockHash]
		if !exists {
			continue
		}

		// Collect updates for all announcing peers
		for _, peerAddr := range announcement.peers {
			updates = append(updates, peerUpdate{addr: peerAddr, height: height})
		}

		// Remove from cache (block is now on chain)
		delete(pht.blockAnnouncements, blockHash)
	}
	pht.announcementsMu.Unlock()

	// Apply updates outside lock (UpdateBestKnownHeight has its own locking)
	for _, update := range updates {
		pht.UpdateBestKnownHeight(update.addr, update.height)
	}
}

// announcementCleanupLoop periodically removes expired cache entries
func (pht *PeerHealthTracker) announcementCleanupLoop() {
	ticker := time.NewTicker(AnnouncementCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			pht.cleanupExpiredAnnouncements()
		case <-pht.stopCleanup:
			return
		}
	}
}

// cleanupExpiredAnnouncements removes cache entries older than TTL
func (pht *PeerHealthTracker) cleanupExpiredAnnouncements() {
	pht.announcementsMu.Lock()
	defer pht.announcementsMu.Unlock()

	cutoff := time.Now().Add(-AnnouncementCacheTTL)
	for blockHash, announcement := range pht.blockAnnouncements {
		if announcement.recordTime.Before(cutoff) {
			delete(pht.blockAnnouncements, blockHash)
		}
	}
}

// StopAnnouncementCleanup stops the cleanup goroutine (call on shutdown)
func (pht *PeerHealthTracker) StopAnnouncementCleanup() {
	select {
	case <-pht.stopCleanup:
		// Already closed
	default:
		close(pht.stopCleanup)
	}
}

// GetAnnouncementCacheStats returns cache statistics for monitoring
func (pht *PeerHealthTracker) GetAnnouncementCacheStats() (entries int, oldestAge time.Duration) {
	pht.announcementsMu.RLock()
	defer pht.announcementsMu.RUnlock()

	entries = len(pht.blockAnnouncements)

	var oldest time.Time
	for _, announcement := range pht.blockAnnouncements {
		if oldest.IsZero() || announcement.recordTime.Before(oldest) {
			oldest = announcement.recordTime
		}
	}

	if !oldest.IsZero() {
		oldestAge = time.Since(oldest)
	}

	return
}
