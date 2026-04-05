// Copyright (c) 2025 The TWINS Core developers
// Distributed under the MIT software license

package p2p

import (
	"fmt"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// TestStateTransitions tests all valid state transitions
func TestStateTransitions(t *testing.T) {
	logger := logrus.New().WithField("test", "statemachine")
	healthTracker := NewPeerHealthTracker()
	peerList := NewSyncPeerList(healthTracker)
	consensus := NewConsensusValidator(healthTracker)
	sm := NewSyncStateMachine(peerList, healthTracker, consensus, logger.WithField("component", "statemachine"))

	tests := []struct {
		name        string
		fromState   SyncState
		toState     SyncState
		shouldError bool
	}{
		{"BOOTSTRAP → SYNC_DECISION", StateBootstrap, StateSyncDecision, false},
		{"SYNC_DECISION → IBD", StateSyncDecision, StateIBD, false},
		{"SYNC_DECISION → REGULAR", StateSyncDecision, StateRegularSync, false},
		{"SYNC_DECISION → SYNCED", StateSyncDecision, StateSynced, false},
		{"IBD → REGULAR", StateIBD, StateRegularSync, false},
		{"REGULAR → SYNCED", StateRegularSync, StateSynced, false},
		{"SYNCED → SYNC_DECISION", StateSynced, StateSyncDecision, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Manually set initial state
			sm.mu.Lock()
			sm.currentState = tt.fromState
			sm.mu.Unlock()

			err := sm.Transition(tt.toState)

			if tt.shouldError && err == nil {
				t.Errorf("expected error but got none")
			}
			if !tt.shouldError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if err == nil {
				currentState := sm.GetState()
				if currentState != tt.toState {
					t.Errorf("expected state %s, got %s", tt.toState.String(), currentState.String())
				}
			}
		})
	}
}

// TestEvaluateSyncNeeded tests sync mode selection based on gap
func TestEvaluateSyncNeeded(t *testing.T) {
	logger := logrus.New().WithField("test", "statemachine")
	healthTracker := NewPeerHealthTracker()
	peerList := NewSyncPeerList(healthTracker)
	consensus := NewConsensusValidator(healthTracker)
	sm := NewSyncStateMachine(peerList, healthTracker, consensus, logger.WithField("component", "statemachine"))

	tests := []struct {
		name            string
		currentHeight   uint32
		consensusHeight uint32
		expectedState   SyncState
	}{
		// gap < 3 (regularSyncThreshold): StateSynced - broadcast mechanism handles it
		{"fully synced", 10000, 10000, StateSynced},
		{"1 block behind", 9999, 10000, StateSynced},
		{"2 blocks behind (boundary)", 9998, 10000, StateSynced},
		// gap >= 3 and < 5000 (ibdThreshold): StateRegularSync
		{"3 blocks behind (boundary)", 9997, 10000, StateRegularSync},
		{"9 blocks behind", 9991, 10000, StateRegularSync},
		{"10 blocks behind", 9990, 10000, StateRegularSync},
		{"100 blocks behind", 9900, 10000, StateRegularSync},
		{"4999 blocks behind", 5001, 10000, StateRegularSync},
		// gap >= 5000: StateIBD
		{"IBD threshold blocks behind", 5000, 10000, StateIBD},
		{"10000 blocks behind", 0, 10000, StateIBD},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, err := sm.EvaluateSyncNeeded(tt.currentHeight, tt.consensusHeight)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if state != tt.expectedState {
				t.Errorf("expected state %s, got %s", tt.expectedState.String(), state.String())
			}
		})
	}
}

// TestHandleReorg tests reorg handling logic
func TestHandleReorg(t *testing.T) {
	logger := logrus.New().WithField("test", "statemachine")
	healthTracker := NewPeerHealthTracker()
	peerList := NewSyncPeerList(healthTracker)
	consensus := NewConsensusValidator(healthTracker)
	sm := NewSyncStateMachine(peerList, healthTracker, consensus, logger.WithField("component", "statemachine"))

	// First reorg should auto-execute
	executed, err := sm.HandleReorg()
	if err != nil {
		t.Errorf("first reorg should not error: %v", err)
	}
	if !executed {
		t.Error("first reorg should be executed")
	}

	if sm.reorgCount != 1 {
		t.Errorf("expected reorg count 1, got %d", sm.reorgCount)
	}

	// Second reorg within window should pause
	executed, err = sm.HandleReorg()
	if err == nil {
		t.Error("second reorg should return error (paused)")
	}
	if executed {
		t.Error("second reorg should not be executed")
	}

	if !sm.IsReorgPaused() {
		t.Error("sync should be paused after second reorg")
	}

	// Resume should clear pause
	sm.ResumeAfterReorg()
	if sm.IsReorgPaused() {
		t.Error("sync should not be paused after resume")
	}
}

// TestReorgWindowReset tests reorg counter reset after time window
func TestReorgWindowReset(t *testing.T) {
	logger := logrus.New().WithField("test", "statemachine")
	healthTracker := NewPeerHealthTracker()
	peerList := NewSyncPeerList(healthTracker)
	consensus := NewConsensusValidator(healthTracker)
	sm := NewSyncStateMachine(peerList, healthTracker, consensus, logger.WithField("component", "statemachine"))

	// Set short reorg window for testing
	sm.SetReorgWindow(100 * time.Millisecond)

	// First reorg
	executed, err := sm.HandleReorg()
	if err != nil || !executed {
		t.Fatal("first reorg should execute")
	}

	// Wait for window to expire
	time.Sleep(150 * time.Millisecond)

	// Second reorg after window should reset counter and execute
	executed, err = sm.HandleReorg()
	if err != nil {
		t.Errorf("reorg after window should not error: %v", err)
	}
	if !executed {
		t.Error("reorg after window should execute")
	}

	if sm.reorgCount != 1 {
		t.Errorf("reorg count should be reset to 1, got %d", sm.reorgCount)
	}
}

// TestMempoolControl tests mempool enable/disable during state transitions
func TestMempoolControl(t *testing.T) {
	logger := logrus.New().WithField("test", "statemachine")
	healthTracker := NewPeerHealthTracker()
	peerList := NewSyncPeerList(healthTracker)
	consensus := NewConsensusValidator(healthTracker)
	sm := NewSyncStateMachine(peerList, healthTracker, consensus, logger.WithField("component", "statemachine"))

	mempoolEnabled := true
	mempoolCallCount := 0

	// Set callback to track mempool control
	sm.SetMempoolControlCallback(func(enabled bool) {
		mempoolEnabled = enabled
		mempoolCallCount++
	})

	// Transition to IBD should disable mempool
	sm.mu.Lock()
	sm.currentState = StateSyncDecision
	sm.mu.Unlock()

	err := sm.Transition(StateIBD)
	if err != nil {
		t.Fatalf("transition failed: %v", err)
	}

	if mempoolEnabled {
		t.Error("mempool should be disabled in IBD mode")
	}
	if mempoolCallCount != 1 {
		t.Errorf("expected 1 mempool callback, got %d", mempoolCallCount)
	}

	// Transition to REGULAR should enable mempool (called twice: exit IBD + enter REGULAR)
	err = sm.Transition(StateRegularSync)
	if err != nil {
		t.Fatalf("transition failed: %v", err)
	}

	if !mempoolEnabled {
		t.Error("mempool should be enabled in regular sync mode")
	}
	if mempoolCallCount != 3 {
		t.Errorf("expected 3 mempool callbacks (1 disable + 2 enable), got %d", mempoolCallCount)
	}

	// Transition to SYNCED should keep mempool enabled
	err = sm.Transition(StateSynced)
	if err != nil {
		t.Fatalf("transition failed: %v", err)
	}

	if !mempoolEnabled {
		t.Error("mempool should be enabled in synced state")
	}
}

// TestStateChangeCallback tests state change notifications
func TestStateChangeCallback(t *testing.T) {
	logger := logrus.New().WithField("test", "statemachine")
	healthTracker := NewPeerHealthTracker()
	peerList := NewSyncPeerList(healthTracker)
	consensus := NewConsensusValidator(healthTracker)
	sm := NewSyncStateMachine(peerList, healthTracker, consensus, logger.WithField("component", "statemachine"))

	var lastOldState, lastNewState SyncState
	callCount := 0

	sm.SetStateChangeCallback(func(oldState, newState SyncState) {
		lastOldState = oldState
		lastNewState = newState
		callCount++
	})

	// Transition should trigger callback
	err := sm.Transition(StateSyncDecision)
	if err != nil {
		t.Fatalf("transition failed: %v", err)
	}

	if callCount != 1 {
		t.Errorf("expected 1 callback, got %d", callCount)
	}

	if lastOldState != StateBootstrap {
		t.Errorf("expected old state BOOTSTRAP, got %s", lastOldState.String())
	}

	if lastNewState != StateSyncDecision {
		t.Errorf("expected new state SYNC_DECISION, got %s", lastNewState.String())
	}
}

// TestOnListRebuild tests height re-evaluation on peer list rebuild
func TestOnListRebuild(t *testing.T) {
	logger := logrus.New().WithField("test", "statemachine")
	healthTracker := NewPeerHealthTracker()
	peerList := NewSyncPeerList(healthTracker)

	// Add peers with heights
	for i := 0; i < 5; i++ {
		addr := "peer" + string(rune('0'+i))
		healthTracker.RecordPeerDiscovered(addr, 1000, false, TierBronze, true)
	}

	consensus := NewConsensusValidator(healthTracker)
	sm := NewSyncStateMachine(peerList, healthTracker, consensus, logger.WithField("component", "statemachine"))

	// Initial evaluation
	_, err := sm.EvaluateSyncNeeded(900, 1000)
	if err != nil {
		t.Fatalf("evaluation failed: %v", err)
	}

	// Update peer heights
	for i := 0; i < 5; i++ {
		addr := "peer" + string(rune('0'+i))
		healthTracker.UpdateTipHeight(addr, 1100)
	}

	// Rebuild should recalculate consensus
	err = sm.OnListRebuild()
	if err != nil {
		t.Errorf("list rebuild failed: %v", err)
	}

	// Check that consensus height was updated
	height, _, err := sm.GetConsensusHeight()
	if err != nil {
		t.Fatalf("failed to get consensus height: %v", err)
	}

	if height != 1100 {
		t.Errorf("expected consensus height 1100 after rebuild, got %d", height)
	}
}

// TestGetStats tests statistics retrieval
func TestGetStats(t *testing.T) {
	logger := logrus.New().WithField("test", "statemachine")
	healthTracker := NewPeerHealthTracker()
	peerList := NewSyncPeerList(healthTracker)
	consensus := NewConsensusValidator(healthTracker)
	sm := NewSyncStateMachine(peerList, healthTracker, consensus, logger.WithField("component", "statemachine"))

	// Set some state
	sm.Transition(StateIBD)
	sm.EvaluateSyncNeeded(5000, 10000)

	stats := sm.GetStats()

	if stats.CurrentState != StateIBD {
		t.Errorf("expected current state IBD, got %s", stats.CurrentState.String())
	}

	if stats.CurrentHeight != 5000 {
		t.Errorf("expected current height 5000, got %d", stats.CurrentHeight)
	}

	if stats.ConsensusHeight != 10000 {
		t.Errorf("expected consensus height 10000, got %d", stats.ConsensusHeight)
	}

	if stats.BlocksBehind != 5000 {
		t.Errorf("expected 5000 blocks behind, got %d", stats.BlocksBehind)
	}
}

// TestCustomThresholds tests custom threshold configuration
func TestCustomThresholds(t *testing.T) {
	logger := logrus.New().WithField("test", "statemachine")
	healthTracker := NewPeerHealthTracker()
	peerList := NewSyncPeerList(healthTracker)
	consensus := NewConsensusValidator(healthTracker)
	sm := NewSyncStateMachine(peerList, healthTracker, consensus, logger.WithField("component", "statemachine"))

	// Set custom IBD threshold
	sm.SetIBDThreshold(10000)

	// 9999 blocks behind should be REGULAR
	state, _ := sm.EvaluateSyncNeeded(1, 10000)
	if state != StateRegularSync {
		t.Errorf("expected REGULAR_SYNC with custom threshold, got %s", state.String())
	}

	// 10000 blocks behind should be IBD
	state, _ = sm.EvaluateSyncNeeded(0, 10000)
	if state != StateIBD {
		t.Errorf("expected IBD with custom threshold, got %s", state.String())
	}
}

// TestGetConsensusHeightWithFallback_OutboundOnly tests that outbound-only strategy is preferred
func TestGetConsensusHeightWithFallback_OutboundOnly(t *testing.T) {
	logger := logrus.New().WithField("test", "statemachine")
	healthTracker := NewPeerHealthTracker()
	peerList := NewSyncPeerList(healthTracker)
	consensus := NewConsensusValidator(healthTracker)
	sm := NewSyncStateMachine(peerList, healthTracker, consensus, logger.WithField("component", "statemachine"))

	// Add 3 outbound peers (minClusterSize default is 3)
	for i := 0; i < 3; i++ {
		addr := "outbound" + string(rune('0'+i))
		healthTracker.RecordPeerDiscovered(addr, 1000, false, TierBronze, true) // outbound
	}

	height, confidence, strategy, err := sm.GetConsensusHeightWithFallback()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if height != 1000 {
		t.Errorf("expected height 1000, got %d", height)
	}
	if confidence <= 0 {
		t.Errorf("expected positive confidence, got %f", confidence)
	}
	if strategy != "outbound_only" {
		t.Errorf("expected strategy outbound_only, got %s", strategy)
	}
}

// TestGetConsensusHeightWithFallback_FallsBackToAll tests fallback when no outbound peers
func TestGetConsensusHeightWithFallback_FallsBackToAll(t *testing.T) {
	logger := logrus.New().WithField("test", "statemachine")
	healthTracker := NewPeerHealthTracker()
	peerList := NewSyncPeerList(healthTracker)
	consensus := NewConsensusValidator(healthTracker)
	sm := NewSyncStateMachine(peerList, healthTracker, consensus, logger.WithField("component", "statemachine"))

	// Add only inbound peers (no outbound)
	for i := 0; i < 5; i++ {
		addr := "inbound" + string(rune('0'+i))
		healthTracker.RecordPeerDiscovered(addr, 2000, false, TierBronze, false) // inbound
	}

	height, confidence, strategy, err := sm.GetConsensusHeightWithFallback()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if height != 2000 {
		t.Errorf("expected height 2000, got %d", height)
	}
	if confidence <= 0 {
		t.Errorf("expected positive confidence, got %f", confidence)
	}
	if strategy != "all_fallback" {
		t.Errorf("expected strategy all_fallback, got %s", strategy)
	}
}

// TestGetConsensusHeightWithFallback_NoPeers tests error when no peers at all
func TestGetConsensusHeightWithFallback_NoPeers(t *testing.T) {
	logger := logrus.New().WithField("test", "statemachine")
	healthTracker := NewPeerHealthTracker()
	peerList := NewSyncPeerList(healthTracker)
	consensus := NewConsensusValidator(healthTracker)
	sm := NewSyncStateMachine(peerList, healthTracker, consensus, logger.WithField("component", "statemachine"))

	_, _, _, err := sm.GetConsensusHeightWithFallback()
	if err == nil {
		t.Error("expected error with no peers, got nil")
	}
}

// TestGetConsensusHeightWithFallback_OutboundDisagree tests that fallback does NOT
// trigger when outbound peers exist but disagree (preserves Sybil resistance)
func TestGetConsensusHeightWithFallback_OutboundDisagree(t *testing.T) {
	logger := logrus.New().WithField("test", "statemachine")
	healthTracker := NewPeerHealthTracker()
	peerList := NewSyncPeerList(healthTracker)
	consensus := NewConsensusValidator(healthTracker)
	sm := NewSyncStateMachine(peerList, healthTracker, consensus, logger.WithField("component", "statemachine"))

	// Add 2 outbound peers at wildly different heights (minClusterSize=3 so no cluster forms)
	healthTracker.RecordPeerDiscovered("out1", 1000, false, TierBronze, true)
	healthTracker.RecordPeerDiscovered("out2", 50000, false, TierBronze, true)

	// Add 5 inbound peers that agree (would form consensus if fallback triggered)
	for i := 0; i < 5; i++ {
		healthTracker.RecordPeerDiscovered(fmt.Sprintf("in%d", i), 2000, false, TierBronze, false)
	}

	// Should fail — outbound peers exist but disagree, must NOT fall back to inbound
	_, _, strategy, err := sm.GetConsensusHeightWithFallback()
	if err == nil {
		t.Errorf("expected error when outbound peers disagree, but got strategy=%s", strategy)
	}
}
