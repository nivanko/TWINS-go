package rpc

import (
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
)

// RateLimiter enforces per-IP request rate limits for the RPC server.
// Uses a sliding window approach: tracks request timestamps per IP and
// rejects requests that exceed the configured maximum per minute.
type RateLimiter struct {
	maxPerMinute  int
	mu            sync.Mutex
	clients       map[string]*clientWindow
	totalAllowed  atomic.Uint64
	totalRejected atomic.Uint64
	logger        *logrus.Entry
	done          chan struct{}
}

// clientWindow tracks request timestamps for a single client IP.
type clientWindow struct {
	timestamps []time.Time
}

// NewRateLimiter creates a new per-IP rate limiter.
// If maxPerMinute is 0 or negative, no rate limiting is enforced.
func NewRateLimiter(maxPerMinute int, logger *logrus.Entry) *RateLimiter {
	rl := &RateLimiter{
		maxPerMinute: maxPerMinute,
		clients:      make(map[string]*clientWindow),
		logger:       logger,
		done:         make(chan struct{}),
	}

	if maxPerMinute > 0 {
		go rl.cleanupLoop()
	}

	return rl
}

// Allow checks whether a request from the given IP should be allowed.
func (rl *RateLimiter) Allow(ip string) bool {
	if rl.maxPerMinute <= 0 {
		return true
	}

	now := time.Now()
	windowStart := now.Add(-time.Minute)

	rl.mu.Lock()
	defer rl.mu.Unlock()

	cw, exists := rl.clients[ip]
	if !exists {
		cw = &clientWindow{}
		rl.clients[ip] = cw
	}

	// Remove expired timestamps
	valid := 0
	for _, ts := range cw.timestamps {
		if ts.After(windowStart) {
			cw.timestamps[valid] = ts
			valid++
		}
	}
	cw.timestamps = cw.timestamps[:valid]

	if len(cw.timestamps) >= rl.maxPerMinute {
		return false
	}

	cw.timestamps = append(cw.timestamps, now)
	return true
}

// Middleware returns an HTTP middleware that enforces per-IP rate limiting.
// Rejected requests receive HTTP 429 Too Many Requests.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rl.maxPerMinute <= 0 {
			next.ServeHTTP(w, r)
			return
		}

		ip := extractIP(r)
		if !rl.Allow(ip) {
			rl.totalRejected.Add(1)
			if rl.logger != nil {
				rl.logger.WithFields(logrus.Fields{
					"ip":    ip,
					"limit": rl.maxPerMinute,
				}).Warn("RPC request rejected: rate limit exceeded")
			}
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}
		rl.totalAllowed.Add(1)

		next.ServeHTTP(w, r)
	})
}

// Stats returns rate limiter statistics.
func (rl *RateLimiter) Stats() (allowed, rejected uint64) {
	return rl.totalAllowed.Load(), rl.totalRejected.Load()
}

// Stop shuts down the rate limiter's background cleanup goroutine.
func (rl *RateLimiter) Stop() {
	close(rl.done)
}

// cleanupLoop periodically removes stale client entries to prevent memory leaks.
func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rl.cleanup()
		case <-rl.done:
			return
		}
	}
}

// cleanup removes client entries with no recent requests.
func (rl *RateLimiter) cleanup() {
	now := time.Now()
	windowStart := now.Add(-time.Minute)

	rl.mu.Lock()
	defer rl.mu.Unlock()

	for ip, cw := range rl.clients {
		// Remove expired timestamps
		valid := 0
		for _, ts := range cw.timestamps {
			if ts.After(windowStart) {
				cw.timestamps[valid] = ts
				valid++
			}
		}
		cw.timestamps = cw.timestamps[:valid]

		// Remove client entry if no recent requests
		if len(cw.timestamps) == 0 {
			delete(rl.clients, ip)
		}
	}
}

// extractIP extracts the client IP address from an HTTP request,
// normalizing IPv6 addresses to /64 prefix for rate limiting.
func extractIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return normalizeIP(r.RemoteAddr)
	}
	return normalizeIP(host)
}

// normalizeIP masks IPv6 addresses to /64 prefix for rate limiting.
// An attacker with a single /64 allocation can rotate through 2^64 addresses;
// keying on the full /128 would let them trivially bypass per-IP limits.
// IPv4 addresses are returned as-is.
func normalizeIP(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ip
	}
	if parsed.To4() != nil {
		return ip
	}
	// IPv6: mask to /64 so all addresses in the same allocation share a bucket
	return parsed.Mask(net.CIDRMask(64, 128)).String()
}
