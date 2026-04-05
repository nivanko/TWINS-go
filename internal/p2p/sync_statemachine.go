// Copyright (c) 2025 The TWINS Core developers
// Distributed under the MIT software license

package p2p

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/twins-dev/twins-core/internal/blockchain"
)

// SyncState represents the current state of blockchain synchronization
type SyncState int

const (
	StateBootstrap    SyncState = iota // Discovering and evaluating peers
	StateSyncDecision                  // Calculating consensus and deciding sync mode
	StateIBD                           // Initial Block Download (aggressive mode, ≥IBDThreshold blocks behind)
	StateRegularSync                   // Regular sync mode (<IBDThreshold blocks behind)
	StateSynced                        // Fully synced and up-to-date
)

// String returns the string representation of a sync state
func (s SyncState) String() string {
	switch s {
	case StateBootstrap:
		return "BOOTSTRAP"
	case StateSyncDecision:
		return "SYNC_DECISION"
	case StateIBD:
		return "IBD"
	case StateRegularSync:
		return "REGULAR_SYNC"
	case StateSynced:
		return "SYNCED"
	default:
		return "UNKNOWN"
	}
}

// SyncStateMachine orchestrates state transitions and mode switching for blockchain sync
type SyncStateMachine struct {
	// State
	currentState SyncState
	previousState SyncState
	stateStartTime time.Time

	// Components
	peerList      *SyncPeerList
	healthTracker *PeerHealthTracker
	consensus     *ConsensusValidator
	logger        *logrus.Entry

	// Thresholds
	ibdThreshold         uint32 // Blocks behind to trigger IBD mode (default: blockchain.IBDThreshold)
	regularSyncThreshold uint32 // Blocks behind to trigger regular sync (default: 3, below this broadcast handles it)

	// Reorg tracking
	reorgCount    int
	lastReorgTime time.Time
	reorgWindow   time.Duration // Time window for counting reorgs (default: 1 hour)
	maxAutoReorgs int           // Maximum automatic reorgs (default: 1)
	reorgPaused   bool          // Sync paused due to repeated reorgs

	// Height tracking
	currentHeight   uint32
	consensusHeight uint32
	lastHeightEval  time.Time

	// Synchronization
	mu sync.RWMutex

	// Callbacks
	onStateChange    func(oldState, newState SyncState)
	onMempoolControl func(enabled bool)
}

// NewSyncStateMachine creates a new sync state machine
func NewSyncStateMachine(peerList *SyncPeerList, healthTracker *PeerHealthTracker,
	consensus *ConsensusValidator, logger *logrus.Entry) *SyncStateMachine {

	return &SyncStateMachine{
		currentState:   StateBootstrap,
		previousState:  StateBootstrap,
		stateStartTime: time.Now(),
		peerList:       peerList,
		healthTracker:  healthTracker,
		consensus:      consensus,
		logger:         logger,
		ibdThreshold:         blockchain.IBDThreshold,
		regularSyncThreshold: 3, // Below this, broadcast mechanism handles block reception
		reorgWindow:          1 * time.Hour,
		maxAutoReorgs:        1,
	}
}

// GetState returns the current sync state
func (sm *SyncStateMachine) GetState() SyncState {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.currentState
}

// Transition moves the state machine to a new state
func (sm *SyncStateMachine) Transition(newState SyncState) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.currentState == newState {
		return nil // Already in this state
	}

	oldState := sm.currentState
	duration := time.Since(sm.stateStartTime)

	sm.logger.WithFields(logrus.Fields{
		"from":     oldState.String(),
		"to":       newState.String(),
		"duration": duration,
	}).Debug("State transition")

	// Execute transition cleanup/setup
	if err := sm.handleTransition(oldState, newState); err != nil {
		return fmt.Errorf("transition failed: %w", err)
	}

	sm.previousState = oldState
	sm.currentState = newState
	sm.stateStartTime = time.Now()

	// Release lock before callback to prevent deadlock if callback queries state
	sm.mu.Unlock()

	// Trigger callback if set
	if sm.onStateChange != nil {
		sm.onStateChange(oldState, newState)
	}

	sm.mu.Lock() // Re-acquire for defer unlock

	return nil
}

// handleTransition executes state-specific setup/cleanup during transitions
func (sm *SyncStateMachine) handleTransition(oldState, newState SyncState) error {
	// Handle state exit cleanup
	switch oldState {
	case StateIBD:
		// Exiting IBD: enable mempool
		sm.logger.Debug("Exiting IBD mode, enabling mempool")
		if sm.onMempoolControl != nil {
			sm.onMempoolControl(true)
		}
	}

	// Handle state entry setup
	switch newState {
	case StateIBD:
		// Entering IBD: disable mempool for faster sync
		sm.logger.Info("Entering IBD mode, disabling mempool")
		if sm.onMempoolControl != nil {
			sm.onMempoolControl(false)
		}

	case StateRegularSync:
		// Entering regular sync: ensure mempool is enabled
		sm.logger.Debug("Entering regular sync mode, ensuring mempool enabled")
		if sm.onMempoolControl != nil {
			sm.onMempoolControl(true)
		}

	case StateSynced:
		// Entering synced state: ensure mempool is enabled
		sm.logger.Info("Entering synced state")
		if sm.onMempoolControl != nil {
			sm.onMempoolControl(true)
		}
	}

	return nil
}

// EvaluateSyncNeeded determines which sync mode to use based on gap
func (sm *SyncStateMachine) EvaluateSyncNeeded(currentHeight, consensusHeight uint32) (SyncState, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.currentHeight = currentHeight
	sm.consensusHeight = consensusHeight
	sm.lastHeightEval = time.Now()

	// Calculate gap
	var gap uint32
	if consensusHeight > currentHeight {
		gap = consensusHeight - currentHeight
	}

	sm.logger.WithFields(logrus.Fields{
		"current_height":   currentHeight,
		"consensus_height": consensusHeight,
		"gap":              gap,
	}).Debug("Evaluating sync mode")

	// Determine appropriate state
	// gap < regularSyncThreshold: broadcast mechanism handles it (StateSynced)
	// gap >= regularSyncThreshold && < ibdThreshold: regular sync needed
	// gap >= ibdThreshold: IBD mode (minimal validation)
	if gap < sm.regularSyncThreshold {
		return StateSynced, nil
	} else if gap >= sm.ibdThreshold {
		return StateIBD, nil
	} else {
		return StateRegularSync, nil
	}
}

// HandleReorg handles blockchain reorganization
// Returns true if reorg was executed, false if paused for user confirmation
func (sm *SyncStateMachine) HandleReorg() (bool, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now()

	// Check if we're within the reorg window
	if !sm.lastReorgTime.IsZero() && now.Sub(sm.lastReorgTime) < sm.reorgWindow {
		sm.reorgCount++

		if sm.reorgCount > sm.maxAutoReorgs {
			// Too many reorgs in short time - pause for safety
			sm.reorgPaused = true
			sm.logger.WithFields(logrus.Fields{
				"reorg_count": sm.reorgCount,
				"window":      sm.reorgWindow,
			}).Warn("Multiple reorgs detected within window - pausing sync for manual review")
			return false, fmt.Errorf("sync paused: %d reorgs within %v (max %d)",
				sm.reorgCount, sm.reorgWindow, sm.maxAutoReorgs)
		}
	} else {
		// Outside window, reset counter
		sm.reorgCount = 1
	}

	sm.lastReorgTime = now

	sm.logger.WithFields(logrus.Fields{
		"reorg_count": sm.reorgCount,
		"auto":        true,
	}).Debug("Executing automatic reorg")

	return true, nil
}

// OnListRebuild is called when the peer list is rebuilt (every 3 rounds)
// Re-evaluates consensus height and checks if state transition is needed
func (sm *SyncStateMachine) OnListRebuild() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.logger.Debug("Peer list rebuilt, re-evaluating consensus height")

	// Calculate new consensus
	result, err := sm.consensus.CalculateConsensusHeight()
	if err != nil {
		sm.logger.WithError(err).Warn("Failed to calculate consensus on list rebuild")
		return err
	}

	oldConsensus := sm.consensusHeight
	sm.consensusHeight = result.Height
	sm.lastHeightEval = time.Now()

	if oldConsensus != sm.consensusHeight {
		sm.logger.WithFields(logrus.Fields{
			"old_consensus": oldConsensus,
			"new_consensus": sm.consensusHeight,
			"difference":    int64(sm.consensusHeight) - int64(oldConsensus),
			"confidence":    fmt.Sprintf("%.1f%%", result.Confidence*100),
		}).Debug("Consensus height updated")

		// Check if we need to change sync mode
		targetState, err := sm.evaluateSyncModeUnlocked(sm.currentHeight, sm.consensusHeight)
		if err != nil {
			return err
		}

		if targetState != sm.currentState {
			sm.logger.WithFields(logrus.Fields{
				"current_state": sm.currentState.String(),
				"target_state":  targetState.String(),
			}).Debug("Sync mode change needed after height re-evaluation")
		}
	}

	return nil
}

// evaluateSyncModeUnlocked determines sync mode without locking (caller must hold lock)
func (sm *SyncStateMachine) evaluateSyncModeUnlocked(currentHeight, consensusHeight uint32) (SyncState, error) {
	var gap uint32
	if consensusHeight > currentHeight {
		gap = consensusHeight - currentHeight
	}

	// gap < regularSyncThreshold: broadcast mechanism handles it
	if gap < sm.regularSyncThreshold {
		return StateSynced, nil
	} else if gap >= sm.ibdThreshold {
		return StateIBD, nil
	} else {
		return StateRegularSync, nil
	}
}

// ResumeAfterReorg resumes sync after user confirms reorg is acceptable
func (sm *SyncStateMachine) ResumeAfterReorg() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.reorgPaused = false
	sm.logger.Debug("Sync resumed after reorg confirmation")
}

// IsReorgPaused returns true if sync is paused due to repeated reorgs
func (sm *SyncStateMachine) IsReorgPaused() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.reorgPaused
}

// GetConsensusHeight returns the current consensus height
func (sm *SyncStateMachine) GetConsensusHeight() (uint32, float64, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	result, err := sm.consensus.CalculateConsensusHeight()
	if err != nil {
		return 0, 0.0, err
	}

	return result.Height, result.Confidence, nil
}

// GetConsensusHeightWithFallback tries StrategyOutboundOnly first, then falls
// back to StrategyAll only when no outbound peers are available.
// If outbound peers exist but disagree ("insufficient peer agreement"), the
// error is returned without fallback to preserve Sybil resistance.
// Returns (height, confidence, strategy_used, error).
func (sm *SyncStateMachine) GetConsensusHeightWithFallback() (uint32, float64, string, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	// Try outbound-only first (more Sybil-resistant)
	result, err := sm.consensus.CalculateConsensusHeightWithStrategy(StrategyOutboundOnly)
	if err == nil {
		return result.Height, result.Confidence, "outbound_only", nil
	}

	// Only fall back to StrategyAll when outbound peers are unavailable.
	// If outbound peers exist but disagree, preserve the error to maintain
	// Sybil resistance — don't let inbound peers override outbound consensus.
	if !strings.Contains(err.Error(), "no peers available") {
		return 0, 0.0, "", err
	}

	// Fallback to all peers when no outbound peers exist
	result, err = sm.consensus.CalculateConsensusHeightWithStrategy(StrategyAll)
	if err != nil {
		return 0, 0.0, "", err
	}

	return result.Height, result.Confidence, "all_fallback", nil
}

// GetBlocksBehind returns how many blocks we are behind consensus
func (sm *SyncStateMachine) GetBlocksBehind() uint32 {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if sm.consensusHeight > sm.currentHeight {
		return sm.consensusHeight - sm.currentHeight
	}
	return 0
}

// SetStateChangeCallback sets a callback for state transitions
func (sm *SyncStateMachine) SetStateChangeCallback(callback func(oldState, newState SyncState)) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.onStateChange = callback
}

// SetMempoolControlCallback sets a callback for mempool enable/disable
func (sm *SyncStateMachine) SetMempoolControlCallback(callback func(enabled bool)) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.onMempoolControl = callback
}

// GetStats returns state machine statistics
func (sm *SyncStateMachine) GetStats() StateMachineStats {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	return StateMachineStats{
		CurrentState:    sm.currentState,
		PreviousState:   sm.previousState,
		StateStartTime:  sm.stateStartTime,
		StateDuration:   time.Since(sm.stateStartTime),
		CurrentHeight:   sm.currentHeight,
		ConsensusHeight: sm.consensusHeight,
		BlocksBehind:    sm.GetBlocksBehind(),
		ReorgCount:      sm.reorgCount,
		ReorgPaused:     sm.reorgPaused,
		LastHeightEval:  sm.lastHeightEval,
	}
}

// StateMachineStats contains state machine statistics
type StateMachineStats struct {
	CurrentState    SyncState
	PreviousState   SyncState
	StateStartTime  time.Time
	StateDuration   time.Duration
	CurrentHeight   uint32
	ConsensusHeight uint32
	BlocksBehind    uint32
	ReorgCount      int
	ReorgPaused     bool
	LastHeightEval  time.Time
}

// SetIBDThreshold sets the threshold for entering IBD mode
func (sm *SyncStateMachine) SetIBDThreshold(blocks uint32) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.ibdThreshold = blocks
}

// SetReorgWindow sets the time window for counting reorgs
func (sm *SyncStateMachine) SetReorgWindow(window time.Duration) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.reorgWindow = window
}

// SetMaxAutoReorgs sets the maximum number of automatic reorgs
func (sm *SyncStateMachine) SetMaxAutoReorgs(max int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.maxAutoReorgs = max
}
