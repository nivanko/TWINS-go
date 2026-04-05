// Copyright (c) 2025 The TWINS Core developers
// Distributed under the MIT software license

package p2p

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// TestErrorScoring tests weighted error accumulation and cooldown trigger
func TestErrorScoring(t *testing.T) {
	_ = logrus.New().WithField("test", "health")
	tracker := NewPeerHealthTracker()

	addr := "peer1"
	tracker.RecordPeerDiscovered(addr, 1000, false, TierBronze, false)

	// Test weighted error accumulation
	// Actual weights: InvalidBlock=2.0, Timeout=1.0, ConnectionDrop=0.5
	tests := []struct {
		name           string
		errorType      ErrorType
		expectedScore  float64
		expectCooldown bool
	}{
		{"First timeout (weight 1.0)", ErrorTypeTimeout, 1.0, false},
		{"Second timeout", ErrorTypeTimeout, 2.0, false},
		{"Invalid block (weight 2.0)", ErrorTypeInvalidBlock, 4.0, false},
		{"Connection drop (weight 0.5)", ErrorTypeConnectionDrop, 4.5, false},
		{"Another invalid block - triggers cooldown", ErrorTypeInvalidBlock, 6.5, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker.RecordError(addr, tt.errorType)

			tracker.mu.RLock()
			stats, exists := tracker.peers[addr]
			tracker.mu.RUnlock()

			if !exists {
				t.Fatal("peer not found")
			}

			if stats.ErrorScore != tt.expectedScore {
				t.Errorf("expected error score %.1f, got %.1f", tt.expectedScore, stats.ErrorScore)
			}

			if tt.expectCooldown && time.Now().Before(stats.CooldownUntil) {
				// Cooldown should be active
			} else if tt.expectCooldown {
				t.Error("expected peer to be on cooldown")
			}
		})
	}
}

// TestHealthCalculation tests health score calculation with bonuses/penalties
func TestHealthCalculation(t *testing.T) {
	_ = logrus.New().WithField("test", "health")
	tracker := NewPeerHealthTracker()

	// Test regular peer
	tracker.RecordPeerDiscovered("regular", 1000, false, TierNone, false)
	regularHealth := tracker.GetHealthScore("regular")
	if regularHealth != 50.0 {
		t.Errorf("expected regular peer health 50.0, got %.1f", regularHealth)
	}

	// Test masternode bonuses (Bronze=5, Silver=10, Gold=20, Platinum=30)
	tracker.RecordPeerDiscovered("bronze", 1000, true, TierBronze, false)
	bronzeHealth := tracker.GetHealthScore("bronze")
	if bronzeHealth != 55.0 {
		t.Errorf("expected bronze health 55.0, got %.1f", bronzeHealth)
	}

	tracker.RecordPeerDiscovered("silver", 1000, true, TierSilver, false)
	silverHealth := tracker.GetHealthScore("silver")
	if silverHealth != 60.0 {
		t.Errorf("expected silver health 60.0, got %.1f", silverHealth)
	}

	tracker.RecordPeerDiscovered("gold", 1000, true, TierGold, false)
	goldHealth := tracker.GetHealthScore("gold")
	if goldHealth != 70.0 {
		t.Errorf("expected gold health 70.0, got %.1f", goldHealth)
	}

	tracker.RecordPeerDiscovered("platinum", 1000, true, TierPlatinum, false)
	platinumHealth := tracker.GetHealthScore("platinum")
	if platinumHealth != 80.0 {
		t.Errorf("expected platinum health 80.0, got %.1f", platinumHealth)
	}

	// Test error penalty (ErrorScore * 5)
	// Record actual errors to accumulate error score
	tracker.RecordPeerDiscovered("error_peer", 1000, false, TierNone, false)
	for i := 0; i < 5; i++ {
		tracker.RecordError("error_peer", ErrorTypeTimeout) // 5 * 1.0 = 5.0 error score
	}

	errorHealth := tracker.GetHealthScore("error_peer")
	expectedHealth := 50.0 - (5.0 * 5) // 25.0
	if errorHealth != expectedHealth {
		t.Errorf("expected error peer health %.1f, got %.1f", expectedHealth, errorHealth)
	}

	// Test floor at 0 (need 10+ error score to hit floor)
	tracker.RecordPeerDiscovered("maxerror", 1000, false, TierNone, false)
	for i := 0; i < 20; i++ {
		tracker.RecordError("maxerror", ErrorTypeInvalidBlock) // 20 * 2.0 = 40.0 error score
	}

	maxErrorHealth := tracker.GetHealthScore("maxerror")
	if maxErrorHealth != 0 {
		t.Errorf("expected max error health 0, got %.1f", maxErrorHealth)
	}
}

// TestHealthDecay tests 1 hour inactivity reset
func TestHealthDecay(t *testing.T) {
	_ = logrus.New().WithField("test", "health")
	tracker := NewPeerHealthTracker()

	addr := "peer1"
	tracker.RecordPeerDiscovered(addr, 1000, false, TierBronze, false)

	// Add some errors
	for i := 0; i < 5; i++ {
		tracker.RecordError(addr, ErrorTypeTimeout)
	}

	// Verify error score is accumulated
	tracker.mu.RLock()
	stats := tracker.peers[addr]
	tracker.mu.RUnlock()

	if stats.ErrorScore != 5 {
		t.Errorf("expected error score 5, got %.1f", stats.ErrorScore)
	}

	// Manually set last interaction to >1 hour ago
	tracker.mu.Lock()
	stats = tracker.peers[addr]
	stats.LastInteraction = time.Now().Add(-2 * time.Hour)
	tracker.peers[addr] = stats
	tracker.mu.Unlock()

	// Request health score - should trigger decay
	health := tracker.GetHealthScore(addr)

	// After decay, health should be back to base score (50) + masternode bonus (10) = 60
	if health < 50 || health > 65 {
		t.Errorf("expected health near 50-65 after decay, got %.1f", health)
	}

	// Verify error score was reset
	tracker.mu.RLock()
	stats = tracker.peers[addr]
	tracker.mu.RUnlock()

	if stats.ErrorScore != 0 {
		t.Errorf("expected error score reset to 0 after decay, got %.1f", stats.ErrorScore)
	}
}

// TestCooldownManagement tests 5min timeout and error reset
func TestCooldownManagement(t *testing.T) {
	_ = logrus.New().WithField("test", "health")
	tracker := NewPeerHealthTracker()

	addr := "peer1"
	tracker.RecordPeerDiscovered(addr, 1000, false, TierBronze, false)

	// Add errors to trigger cooldown (5+ score, weight 2.0 each)
	for i := 0; i < 3; i++ {
		tracker.RecordError(addr, ErrorTypeInvalidBlock) // 2.0 points each = 6.0 total
	}

	// Verify cooldown is active (not healthy)
	if tracker.IsHealthy(addr) {
		t.Error("peer should not be healthy when on cooldown")
	}

	// Test that cooldown expires after 5 minutes
	tracker.mu.Lock()
	stats := tracker.peers[addr]
	stats.CooldownUntil = time.Now().Add(-1 * time.Minute) // Expired
	tracker.peers[addr] = stats
	tracker.mu.Unlock()

	if !tracker.IsHealthy(addr) {
		t.Error("peer should be healthy after cooldown expiry")
	}

	// Trigger cooldown again
	for i := 0; i < 3; i++ {
		tracker.RecordError(addr, ErrorTypeInvalidBlock)
	}

	if tracker.IsHealthy(addr) {
		t.Error("peer should not be healthy when on cooldown again")
	}

	// Record success should reduce error score
	tracker.RecordSuccess(addr, 10, 1000, time.Second)

	// Still on cooldown (needs time to expire)
	tracker.mu.RLock()
	stats = tracker.peers[addr]
	tracker.mu.RUnlock()

	if time.Now().After(stats.CooldownUntil) {
		t.Error("cooldown should not have expired yet")
	}

	// Multiple successes should eventually clear errors
	for i := 0; i < 20; i++ {
		tracker.RecordSuccess(addr, 10, 1000, time.Second)
	}

	// Wait for cooldown to expire
	tracker.mu.Lock()
	stats = tracker.peers[addr]
	stats.CooldownUntil = time.Now().Add(-1 * time.Minute)
	tracker.peers[addr] = stats
	tracker.mu.Unlock()

	if !tracker.IsHealthy(addr) {
		t.Error("peer should be healthy after many successes and time expiry")
	}
}

// TestRecordSuccess tests success recording and error reduction
func TestRecordSuccess(t *testing.T) {
	_ = logrus.New().WithField("test", "health")
	tracker := NewPeerHealthTracker()

	addr := "peer1"
	tracker.RecordPeerDiscovered(addr, 1000, false, TierBronze, false)

	// Add some errors
	for i := 0; i < 10; i++ {
		tracker.RecordError(addr, ErrorTypeTimeout)
	}

	tracker.mu.RLock()
	initialScore := tracker.peers[addr].ErrorScore
	tracker.mu.RUnlock()

	if initialScore != 10.0 {
		t.Errorf("expected error score 10.0, got %.1f", initialScore)
	}

	// Record success should reduce error score
	tracker.RecordSuccess(addr, 10, 1000, time.Second)

	tracker.mu.RLock()
	newScore := tracker.peers[addr].ErrorScore
	tracker.mu.RUnlock()

	if newScore >= initialScore {
		t.Errorf("expected error score to decrease after success, got %.1f", newScore)
	}

	// Multiple successes should reduce errors further
	for i := 0; i < 15; i++ {
		tracker.RecordSuccess(addr, 10, 1000, time.Second)
	}

	tracker.mu.RLock()
	finalScore := tracker.peers[addr].ErrorScore
	tracker.mu.RUnlock()

	if finalScore > 0.1 {
		t.Errorf("expected error score near 0 after many successes, got %.1f", finalScore)
	}
}

// TestGetHealthyPeers tests filtering of healthy vs unhealthy peers
func TestGetHealthyPeers(t *testing.T) {
	_ = logrus.New().WithField("test", "health")
	tracker := NewPeerHealthTracker()

	// Add healthy peers
	for i := 0; i < 5; i++ {
		addr := "healthy" + string(rune('0'+i))
		tracker.RecordPeerDiscovered(addr, 1000, false, TierBronze, false)
	}

	// Add unhealthy peer (on cooldown)
	tracker.RecordPeerDiscovered("unhealthy1", 1000, false, TierBronze, false)
	for i := 0; i < 4; i++ {
		tracker.RecordError("unhealthy1", ErrorTypeInvalidBlock)
	}

	// Verify healthy peer count using HasHealthyPeers and IsHealthy
	healthyCount := 0
	tracker.mu.RLock()
	for addr := range tracker.peers {
		tracker.mu.RUnlock()
		if tracker.IsHealthy(addr) {
			healthyCount++
		}
		tracker.mu.RLock()
	}
	tracker.mu.RUnlock()

	// Should have 5 healthy peers (unhealthy1 is on cooldown)
	if healthyCount != 5 {
		t.Errorf("expected 5 healthy peers, got %d", healthyCount)
	}

	// Verify unhealthy peer is not healthy
	if tracker.IsHealthy("unhealthy1") {
		t.Error("unhealthy peer should not be marked as healthy")
	}
}

// TestUpdateTipHeight tests height updates and tracking
func TestUpdateTipHeight(t *testing.T) {
	_ = logrus.New().WithField("test", "health")
	tracker := NewPeerHealthTracker()

	addr := "peer1"
	tracker.RecordPeerDiscovered(addr, 1000, false, TierBronze, false)

	// Update to higher height
	tracker.UpdateTipHeight(addr, 1500)

	tracker.mu.RLock()
	stats, exists := tracker.peers[addr]
	tracker.mu.RUnlock()

	if !exists {
		t.Fatal("peer not found")
	}

	if stats.TipHeight != 1500 {
		t.Errorf("expected tip height 1500, got %d", stats.TipHeight)
	}

	// Update to lower height (should still update)
	tracker.UpdateTipHeight(addr, 1200)

	tracker.mu.RLock()
	stats = tracker.peers[addr]
	tracker.mu.RUnlock()

	if stats.TipHeight != 1200 {
		t.Errorf("expected tip height 1200, got %d", stats.TipHeight)
	}
}

// TestMasternodeTierBonus tests masternode tier bonuses
func TestMasternodeTierBonus(t *testing.T) {
	_ = logrus.New().WithField("test", "health")
	tracker := NewPeerHealthTracker()

	tests := []struct {
		name          string
		tier          MasternodeTier
		expectedBonus float64
	}{
		{"Bronze", TierBronze, 5},
		{"Silver", TierSilver, 10},
		{"Gold", TierGold, 20},
		{"Platinum", TierPlatinum, 30},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr := "mn_" + tt.name
			tracker.RecordPeerDiscovered(addr, 1000, true, tt.tier, false)

			health := tracker.GetHealthScore(addr)

			expectedHealth := 50.0 + tt.expectedBonus
			if health != expectedHealth {
				t.Errorf("expected health %.1f for %s tier, got %.1f", expectedHealth, tt.name, health)
			}
		})
	}
}

// TestGetPeerStats tests statistics retrieval
func TestGetPeerStats(t *testing.T) {
	_ = logrus.New().WithField("test", "health")
	tracker := NewPeerHealthTracker()

	addr := "peer1"
	tracker.RecordPeerDiscovered(addr, 1000, true, TierGold, false)

	// Record some activity
	tracker.RecordSuccess(addr, 10, 1000, time.Second)
	tracker.RecordError(addr, ErrorTypeTimeout)
	tracker.UpdateTipHeight(addr, 1100)

	stats := tracker.GetStats(addr)

	if stats == nil {
		t.Fatal("expected stats, got nil")
	}

	if stats.TipHeight != 1100 {
		t.Errorf("expected tip height 1100, got %d", stats.TipHeight)
	}

	if stats.ErrorScore <= 0 {
		t.Errorf("expected positive error score, got %.1f", stats.ErrorScore)
	}

	if !stats.IsMasternode {
		t.Error("expected masternode flag to be true")
	}

	if stats.Tier != TierGold {
		t.Errorf("expected Gold tier, got %v", stats.Tier)
	}
}

// TestHasHealthyPeers tests HasHealthyPeers method
func TestHasHealthyPeers(t *testing.T) {
	_ = logrus.New().WithField("test", "health")
	tracker := NewPeerHealthTracker()

	// No peers initially
	if tracker.HasHealthyPeers() {
		t.Error("should have no healthy peers initially")
	}

	// Add a healthy peer
	tracker.RecordPeerDiscovered("peer1", 1000, false, TierBronze, false)

	if !tracker.HasHealthyPeers() {
		t.Error("should have healthy peers after adding one")
	}

	// Put peer on cooldown (need 5+ error score, InvalidBlock=2.0 each)
	for i := 0; i < 3; i++ {
		tracker.RecordError("peer1", ErrorTypeInvalidBlock) // 2.0*3 = 6.0 > 5.0 threshold
	}

	if tracker.HasHealthyPeers() {
		t.Error("should have no healthy peers when all on cooldown")
	}
}

// TestUpdatePingHeight tests ping height tracking for stale detection
func TestUpdatePingHeight(t *testing.T) {
	tracker := NewPeerHealthTracker()
	defer tracker.StopAnnouncementCleanup()

	addr := "192.168.1.1:37817"
	tracker.RecordPeerDiscovered(addr, 1000, false, TierNone, true)

	// Initially no ping height data
	stats := tracker.GetStats(addr)
	if stats.LastPingHeight != 0 {
		t.Errorf("expected initial LastPingHeight=0, got %d", stats.LastPingHeight)
	}
	if !stats.LastPingHeightChangeTime.IsZero() {
		t.Error("expected initial LastPingHeightChangeTime to be zero")
	}

	// First ping height update
	tracker.UpdatePingHeight(addr, 5000)
	stats = tracker.GetStats(addr)
	if stats.LastPingHeight != 5000 {
		t.Errorf("expected LastPingHeight=5000, got %d", stats.LastPingHeight)
	}
	if stats.LastPingHeightChangeTime.IsZero() {
		t.Error("expected LastPingHeightChangeTime to be set after first update")
	}
	firstChangeTime := stats.LastPingHeightChangeTime

	// Same height should NOT update the change time
	time.Sleep(2 * time.Millisecond)
	tracker.UpdatePingHeight(addr, 5000)
	stats = tracker.GetStats(addr)
	if stats.LastPingHeightChangeTime != firstChangeTime {
		t.Error("same height should not update LastPingHeightChangeTime")
	}

	// Different height should update the change time
	time.Sleep(2 * time.Millisecond)
	tracker.UpdatePingHeight(addr, 5001)
	stats = tracker.GetStats(addr)
	if stats.LastPingHeight != 5001 {
		t.Errorf("expected LastPingHeight=5001, got %d", stats.LastPingHeight)
	}
	if !stats.LastPingHeightChangeTime.After(firstChangeTime) {
		t.Error("new height should update LastPingHeightChangeTime")
	}

	// Zero height should be ignored
	tracker.UpdatePingHeight(addr, 0)
	stats = tracker.GetStats(addr)
	if stats.LastPingHeight != 5001 {
		t.Errorf("zero height should be ignored, got %d", stats.LastPingHeight)
	}

	// Unknown peer should be safe to call
	tracker.UpdatePingHeight("unknown:1234", 100) // Should not panic
}

// TestGetStalePingHeightPeers tests stale ping height detection
func TestGetStalePingHeightPeers(t *testing.T) {
	tracker := NewPeerHealthTracker()
	defer tracker.StopAnnouncementCleanup()

	// Add a 70928 outbound peer (will receive ping heights)
	addr70928 := "192.168.1.1:37817"
	tracker.RecordPeerDiscovered(addr70928, 1000, false, TierNone, true) // outbound

	// Add a 70927 outbound peer (no ping height data)
	addr70927 := "192.168.1.2:37817"
	tracker.RecordPeerDiscovered(addr70927, 1000, false, TierNone, true) // outbound

	// Add a 70928 inbound peer (should be excluded from stale check)
	addrInbound := "192.168.1.3:37817"
	tracker.RecordPeerDiscovered(addrInbound, 1000, false, TierNone, false) // inbound

	// Initially no stale peers (no one has received ping heights yet)
	stale := tracker.GetStalePingHeightPeers(10 * time.Minute)
	if len(stale) != 0 {
		t.Errorf("expected 0 stale peers initially, got %d", len(stale))
	}

	// Simulate 70928 peer receiving height update, then becoming stale
	tracker.mu.RLock()
	stats := tracker.peers[addr70928]
	tracker.mu.RUnlock()
	stats.mu.Lock()
	stats.LastPingHeight = 5000
	stats.LastPingHeightChangeTime = time.Now().Add(-15 * time.Minute) // 15 min ago
	stats.mu.Unlock()

	// 70928 peer should be stale (15 min > 10 min timeout)
	stale = tracker.GetStalePingHeightPeers(10 * time.Minute)
	if len(stale) != 1 || stale[0] != addr70928 {
		t.Errorf("expected 1 stale peer (%s), got %v", addr70928, stale)
	}

	// 70927 peer should NOT be flagged (no ping height data)
	for _, s := range stale {
		if s == addr70927 {
			t.Error("70927 peer should not be flagged as stale")
		}
	}

	// Make inbound peer stale too — should NOT be flagged (inbound excluded)
	tracker.mu.RLock()
	inboundStats := tracker.peers[addrInbound]
	tracker.mu.RUnlock()
	inboundStats.mu.Lock()
	inboundStats.LastPingHeight = 4000
	inboundStats.LastPingHeightChangeTime = time.Now().Add(-15 * time.Minute)
	inboundStats.mu.Unlock()

	stale = tracker.GetStalePingHeightPeers(10 * time.Minute)
	for _, s := range stale {
		if s == addrInbound {
			t.Error("inbound peer should not be flagged as stale")
		}
	}

	// Update 70928 peer height — should no longer be stale
	tracker.UpdatePingHeight(addr70928, 5001)
	stale = tracker.GetStalePingHeightPeers(10 * time.Minute)
	if len(stale) != 0 {
		t.Errorf("expected 0 stale peers after height update, got %d", len(stale))
	}
}

// TestStalePingHeightHealthPenalty tests that stale ping height reduces health score
func TestStalePingHeightHealthPenalty(t *testing.T) {
	tracker := NewPeerHealthTracker()
	defer tracker.StopAnnouncementCleanup()

	addr := "192.168.1.1:37817"
	tracker.RecordPeerDiscovered(addr, 1000, false, TierNone, true)

	// Baseline health score
	baseScore := tracker.GetHealthScore(addr)

	// Set stale ping height (beyond StalePingHeightTimeout)
	tracker.mu.RLock()
	stats := tracker.peers[addr]
	tracker.mu.RUnlock()
	stats.mu.Lock()
	stats.LastPingHeight = 5000
	stats.LastPingHeightChangeTime = time.Now().Add(-15 * time.Minute)
	stats.mu.Unlock()

	// Health score should be penalized
	penalizedScore := tracker.GetHealthScore(addr)
	if penalizedScore >= baseScore {
		t.Errorf("expected penalized score < base score, got penalized=%.1f base=%.1f", penalizedScore, baseScore)
	}
	expectedPenalty := StalePingHealthPenalty
	actualPenalty := baseScore - penalizedScore
	if actualPenalty < expectedPenalty-0.1 || actualPenalty > expectedPenalty+0.1 {
		t.Errorf("expected penalty ~%.1f, got %.1f", expectedPenalty, actualPenalty)
	}
}

// TestPruneDisconnectedPeers tests removal of stale entries for disconnected peers
func TestPruneDisconnectedPeers(t *testing.T) {
	tracker := NewPeerHealthTracker()

	// Register 5 peers
	tracker.RecordPeerDiscovered("peer1:37817", 1000, false, TierNone, true)
	tracker.RecordPeerDiscovered("peer2:37817", 1000, false, TierNone, true)
	tracker.RecordPeerDiscovered("peer3:37817", 1000, false, TierNone, false)
	tracker.RecordPeerDiscovered("peer4:12345", 1000, false, TierNone, false)
	tracker.RecordPeerDiscovered("peer5:54321", 1000, false, TierNone, true)

	allPeers := tracker.GetAllPeers()
	if len(allPeers) != 5 {
		t.Fatalf("expected 5 peers, got %d", len(allPeers))
	}

	// Only peer1, peer3, peer5 are still connected
	connected := map[string]struct{}{
		"peer1:37817": {},
		"peer3:37817": {},
		"peer5:54321": {},
	}

	pruned := tracker.PruneDisconnectedPeers(connected)
	if pruned != 2 {
		t.Errorf("expected 2 pruned, got %d", pruned)
	}

	allPeers = tracker.GetAllPeers()
	if len(allPeers) != 3 {
		t.Errorf("expected 3 peers after prune, got %d", len(allPeers))
	}

	// Verify correct peers remain
	for _, addr := range []string{"peer1:37817", "peer3:37817", "peer5:54321"} {
		if _, exists := allPeers[addr]; !exists {
			t.Errorf("expected peer %s to remain after prune", addr)
		}
	}
	for _, addr := range []string{"peer2:37817", "peer4:12345"} {
		if _, exists := allPeers[addr]; exists {
			t.Errorf("expected peer %s to be pruned", addr)
		}
	}
}

// TestPruneDisconnectedPeersEmpty tests pruning with empty connected set removes all
func TestPruneDisconnectedPeersEmpty(t *testing.T) {
	tracker := NewPeerHealthTracker()

	tracker.RecordPeerDiscovered("peer1", 1000, false, TierNone, true)
	tracker.RecordPeerDiscovered("peer2", 1000, false, TierNone, false)

	pruned := tracker.PruneDisconnectedPeers(map[string]struct{}{})
	if pruned != 2 {
		t.Errorf("expected 2 pruned, got %d", pruned)
	}

	allPeers := tracker.GetAllPeers()
	if len(allPeers) != 0 {
		t.Errorf("expected 0 peers after prune, got %d", len(allPeers))
	}
}

// TestPruneDisconnectedPeersNoOp tests pruning when all peers are connected
func TestPruneDisconnectedPeersNoOp(t *testing.T) {
	tracker := NewPeerHealthTracker()

	tracker.RecordPeerDiscovered("peer1", 1000, false, TierNone, true)
	tracker.RecordPeerDiscovered("peer2", 1000, false, TierNone, false)

	connected := map[string]struct{}{
		"peer1": {},
		"peer2": {},
	}

	pruned := tracker.PruneDisconnectedPeers(connected)
	if pruned != 0 {
		t.Errorf("expected 0 pruned, got %d", pruned)
	}

	allPeers := tracker.GetAllPeers()
	if len(allPeers) != 2 {
		t.Errorf("expected 2 peers after no-op prune, got %d", len(allPeers))
	}
}
