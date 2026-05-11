package rpc

import (
	"sync"
	"time"
)

const (
	backoffInitial         = 1 * time.Second
	backoffMax             = 60 * time.Second
	backoffCleanupInterval = 5 * time.Minute
	backoffExpiry          = 10 * time.Minute
)

// backoffEntry tracks per-IP exponential backoff state for failed
// reloadrpccerts passphrase attempts.
type backoffEntry struct {
	lastFailure time.Time
	delay       time.Duration
}

// ReloadBackoff implements per-IP exponential backoff for the reloadrpccerts
// RPC handler. No hard lockout — every attempt is still verified after the
// delay expires. This prevents admin-DoS via shared NAT while still rate-
// limiting brute-force attempts.
type ReloadBackoff struct {
	entries map[string]*backoffEntry
	mu      sync.Mutex
	done    chan struct{}
}

// NewReloadBackoff creates a ReloadBackoff with default cleanup interval.
func NewReloadBackoff() *ReloadBackoff {
	return newReloadBackoffWithInterval(backoffCleanupInterval)
}

// newReloadBackoffWithInterval creates a ReloadBackoff with a configurable
// cleanup interval (for testing).
func newReloadBackoffWithInterval(cleanupInterval time.Duration) *ReloadBackoff {
	rb := &ReloadBackoff{
		entries: make(map[string]*backoffEntry),
		done:    make(chan struct{}),
	}
	go rb.cleanupLoop(cleanupInterval)
	return rb
}

// Check returns the remaining wait time for the given IP. Returns 0 if no
// delay is active (either no prior failure or the delay has elapsed).
func (rb *ReloadBackoff) Check(ip string) time.Duration {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	entry, ok := rb.entries[ip]
	if !ok {
		return 0
	}

	elapsed := time.Since(entry.lastFailure)
	if elapsed >= entry.delay {
		return 0
	}
	return entry.delay - elapsed
}

// RecordFailure records a failed passphrase attempt. Sets the initial delay
// on first failure, doubles on subsequent failures, capped at backoffMax.
func (rb *ReloadBackoff) RecordFailure(ip string) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	entry, ok := rb.entries[ip]
	if !ok {
		rb.entries[ip] = &backoffEntry{
			lastFailure: time.Now(),
			delay:       backoffInitial,
		}
		return
	}

	entry.delay *= 2
	if entry.delay > backoffMax {
		entry.delay = backoffMax
	}
	entry.lastFailure = time.Now()
}

// RecordSuccess removes the backoff entry for the given IP (reset on
// correct passphrase).
func (rb *ReloadBackoff) RecordSuccess(ip string) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	delete(rb.entries, ip)
}

// Stop stops the background cleanup goroutine.
func (rb *ReloadBackoff) Stop() {
	close(rb.done)
}

// cleanupLoop periodically removes expired backoff entries to prevent
// unbounded memory growth from many unique IPs.
func (rb *ReloadBackoff) cleanupLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rb.cleanup()
		case <-rb.done:
			return
		}
	}
}

func (rb *ReloadBackoff) cleanup() {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	now := time.Now()
	for ip, entry := range rb.entries {
		if now.Sub(entry.lastFailure) >= backoffExpiry {
			delete(rb.entries, ip)
		}
	}
}
