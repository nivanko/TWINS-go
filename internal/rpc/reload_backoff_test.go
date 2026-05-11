package rpc

import (
	"testing"
	"time"
)

func TestReloadBackoff_NoDelayInitially(t *testing.T) {
	b := NewReloadBackoff()
	defer b.Stop()

	if d := b.Check("192.168.1.1"); d != 0 {
		t.Errorf("expected 0 delay for unknown IP, got %v", d)
	}
}

func TestReloadBackoff_ExponentialGrowth(t *testing.T) {
	b := NewReloadBackoff()
	defer b.Stop()

	ip := "10.0.0.1"

	// First failure: 1s
	b.RecordFailure(ip)
	d := b.Check(ip)
	if d < 900*time.Millisecond || d > backoffInitial+100*time.Millisecond {
		t.Errorf("after 1 failure: expected ~1s delay, got %v", d)
	}

	// Simulate time passing (clear the delay window to test next doubling)
	b.mu.Lock()
	b.entries[ip].lastFailure = time.Now().Add(-2 * time.Second)
	b.mu.Unlock()

	// Second failure: 2s
	b.RecordFailure(ip)
	b.mu.Lock()
	delay := b.entries[ip].delay
	b.mu.Unlock()
	if delay != 2*time.Second {
		t.Errorf("after 2 failures: expected 2s delay, got %v", delay)
	}

	// Third failure: 4s
	b.mu.Lock()
	b.entries[ip].lastFailure = time.Now().Add(-3 * time.Second)
	b.mu.Unlock()
	b.RecordFailure(ip)
	b.mu.Lock()
	delay = b.entries[ip].delay
	b.mu.Unlock()
	if delay != 4*time.Second {
		t.Errorf("after 3 failures: expected 4s delay, got %v", delay)
	}

	// Test cap at 60s — set delay just below cap, then fail again
	b.mu.Lock()
	b.entries[ip].delay = 32 * time.Second
	b.entries[ip].lastFailure = time.Now().Add(-33 * time.Second)
	b.mu.Unlock()
	b.RecordFailure(ip)
	b.mu.Lock()
	delay = b.entries[ip].delay
	b.mu.Unlock()
	if delay != backoffMax {
		t.Errorf("expected cap at %v, got %v", backoffMax, delay)
	}

	// One more failure should stay at cap
	b.mu.Lock()
	b.entries[ip].lastFailure = time.Now().Add(-61 * time.Second)
	b.mu.Unlock()
	b.RecordFailure(ip)
	b.mu.Lock()
	delay = b.entries[ip].delay
	b.mu.Unlock()
	if delay != backoffMax {
		t.Errorf("expected delay to stay at cap %v, got %v", backoffMax, delay)
	}
}

func TestReloadBackoff_ResetOnSuccess(t *testing.T) {
	b := NewReloadBackoff()
	defer b.Stop()

	ip := "10.0.0.2"
	b.RecordFailure(ip)
	if d := b.Check(ip); d == 0 {
		t.Error("expected non-zero delay after failure")
	}

	b.RecordSuccess(ip)
	if d := b.Check(ip); d != 0 {
		t.Errorf("expected 0 delay after success, got %v", d)
	}
}

func TestReloadBackoff_PerIPIsolation(t *testing.T) {
	b := NewReloadBackoff()
	defer b.Stop()

	b.RecordFailure("1.1.1.1")

	// Different IP should have no delay
	if d := b.Check("2.2.2.2"); d != 0 {
		t.Errorf("expected 0 delay for unrelated IP, got %v", d)
	}

	// Original IP should have delay
	if d := b.Check("1.1.1.1"); d == 0 {
		t.Error("expected non-zero delay for failed IP")
	}
}

func TestReloadBackoff_Cleanup(t *testing.T) {
	b := newReloadBackoffWithInterval(50 * time.Millisecond)
	defer b.Stop()

	ip := "10.0.0.3"
	b.RecordFailure(ip)

	// Artificially age the entry past expiry
	b.mu.Lock()
	b.entries[ip].lastFailure = time.Now().Add(-backoffExpiry - time.Second)
	b.mu.Unlock()

	// Wait for cleanup to run
	time.Sleep(100 * time.Millisecond)

	b.mu.Lock()
	_, exists := b.entries[ip]
	b.mu.Unlock()

	if exists {
		t.Error("expected expired entry to be cleaned up")
	}
}
