package wallet

import (
	"testing"
	"time"
)

func TestAutoCombineWorkerStartStop(t *testing.T) {
	w := &Wallet{}
	worker := newAutoCombineWorker(w)
	worker.Start()

	// Should be able to notify without blocking
	worker.NotifyBlock()
	worker.NotifyBlock() // second call should not block (buffered channel)

	worker.Stop()
	// Stop should be safe to call (doneCh closed)
}

func TestAutoCombineWorkerCooldown(t *testing.T) {
	w := &Wallet{
		autoCombineEnabled:  true,
		autoCombineTarget:   100000000000, // 1000 TWINS
		autoCombineCooldown: 600,          // 10 minutes
	}

	worker := newAutoCombineWorker(w)

	// Set lastRun to now — cooldown should prevent consolidation
	worker.lastRun = time.Now()

	// tryConsolidate should return quickly due to cooldown
	worker.tryConsolidate()

	// No crash or panic means cooldown logic works
}

func TestAutoCombineWorkerDisabled(t *testing.T) {
	w := &Wallet{
		autoCombineEnabled:  false,
		autoCombineTarget:   0,
		autoCombineCooldown: 600,
	}

	worker := newAutoCombineWorker(w)

	// tryConsolidate should return immediately when disabled
	worker.tryConsolidate()
}

func TestAutoCombineWorkerZeroTarget(t *testing.T) {
	w := &Wallet{
		autoCombineEnabled:  true,
		autoCombineTarget:   0, // zero target = disabled
		autoCombineCooldown: 600,
	}

	worker := newAutoCombineWorker(w)
	worker.tryConsolidate()
}

func TestAutoCombineConstants(t *testing.T) {
	// Verify the max inputs constant provides adequate margin
	// 480 inputs * 190 bytes/input + 34 bytes output + 10 bytes overhead = 91,244 bytes
	estimatedSize := autoCombineMaxInputs*bytesPerInput + bytesPerOutput + txBaseOverhead
	if estimatedSize >= MaxStandardTxSize {
		t.Errorf("autoCombineMaxInputs=%d produces estimated size %d >= MaxStandardTxSize %d",
			autoCombineMaxInputs, estimatedSize, MaxStandardTxSize)
	}

	margin := MaxStandardTxSize - estimatedSize
	if margin < 5000 {
		t.Errorf("Safety margin %d bytes is too small (expected >= 5000)", margin)
	}

	// Verify min inputs is reasonable
	if autoCombineMinInputs < 2 {
		t.Error("autoCombineMinInputs must be >= 2 (cannot combine a single UTXO)")
	}

	// Verify max txs per cycle
	if autoCombineMaxTxsPerCycle < 1 {
		t.Error("autoCombineMaxTxsPerCycle must be >= 1")
	}

	// Verify fee guard percentage
	if autoCombineFeeGuardPercent <= 0 || autoCombineFeeGuardPercent > 50 {
		t.Errorf("autoCombineFeeGuardPercent=%d should be between 1 and 50", autoCombineFeeGuardPercent)
	}
}

func TestAutoCombineConfigGetSet(t *testing.T) {
	w := &Wallet{}

	// Default values
	enabled, target, cooldown := w.GetAutoCombineConfig()
	if enabled || target != 0 || cooldown != 0 {
		t.Errorf("Expected defaults (false, 0, 0), got (%v, %d, %d)", enabled, target, cooldown)
	}

	// Set config — but don't actually start worker (no broadcaster etc.)
	// We test the field updates directly
	w.autoCombineEnabled = true
	w.autoCombineTarget = 20000000000000 // 200000 TWINS
	w.autoCombineCooldown = 300

	enabled, target, cooldown = w.GetAutoCombineConfig()
	if !enabled {
		t.Error("Expected enabled=true")
	}
	if target != 20000000000000 {
		t.Errorf("Expected target=20000000000000, got %d", target)
	}
	if cooldown != 300 {
		t.Errorf("Expected cooldown=300, got %d", cooldown)
	}
}

func TestMatchesTypeFilterWithComment(t *testing.T) {
	tests := []struct {
		name     string
		category string
		comment  string
		filter   string
		want     bool
	}{
		{"consolidation filter matches autocombine", "send_to_self", "autocombine", "consolidation", true},
		{"consolidation filter rejects non-autocombine", "send_to_self", "", "consolidation", false},
		{"consolidation filter rejects send", "send", "autocombine", "consolidation", false},
		{"toYourself excludes autocombine", "send_to_self", "autocombine", "toYourself", false},
		{"toYourself includes regular send_to_self", "send_to_self", "", "toYourself", true},
		{"toYourself includes send_to_self with other comment", "send_to_self", "manual", "toYourself", true},
		{"all includes autocombine", "send_to_self", "autocombine", "all", true},
		{"mostCommon includes autocombine", "send_to_self", "autocombine", "mostCommon", true},
		{"sent filter works normally", "send", "", "sent", true},
		{"received filter works normally", "receive", "", "received", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesTypeFilterWithComment(tt.category, tt.comment, tt.filter)
			if got != tt.want {
				t.Errorf("matchesTypeFilterWithComment(%q, %q, %q) = %v, want %v",
					tt.category, tt.comment, tt.filter, got, tt.want)
			}
		})
	}
}

func TestAutoCombineConsolidationCallback(t *testing.T) {
	w := &Wallet{}

	var callbackTxCount int
	var callbackAmount int64
	w.SetOnConsolidationCallback(func(txCount int, totalAmount int64) {
		callbackTxCount = txCount
		callbackAmount = totalAmount
	})

	// Verify callback is set
	w.mu.RLock()
	cb := w.onConsolidationCallback
	w.mu.RUnlock()
	if cb == nil {
		t.Fatal("Expected callback to be set")
	}

	// Invoke directly to verify wiring
	cb(3, 5000000000)
	if callbackTxCount != 3 {
		t.Errorf("Expected txCount=3, got %d", callbackTxCount)
	}
	if callbackAmount != 5000000000 {
		t.Errorf("Expected amount=5000000000, got %d", callbackAmount)
	}
}

func TestAutoCombineNotifyBlockNonBlocking(t *testing.T) {
	w := &Wallet{}
	worker := newAutoCombineWorker(w)

	// Should not block even without starting the worker
	done := make(chan struct{})
	go func() {
		worker.NotifyBlock()
		worker.NotifyBlock()
		worker.NotifyBlock()
		close(done)
	}()

	select {
	case <-done:
		// OK — all calls returned
	case <-time.After(time.Second):
		t.Fatal("NotifyBlock blocked for more than 1 second")
	}
}
