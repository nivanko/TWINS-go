package rpc

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func TestRateLimiterAllow(t *testing.T) {
	rl := NewRateLimiter(5, logrus.NewEntry(logrus.New()))

	// First 5 requests should be allowed
	for i := 0; i < 5; i++ {
		if !rl.Allow("192.168.1.1") {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}

	// 6th request should be rejected
	if rl.Allow("192.168.1.1") {
		t.Fatal("6th request should be rejected")
	}
}

func TestRateLimiterPerIP(t *testing.T) {
	rl := NewRateLimiter(2, logrus.NewEntry(logrus.New()))

	// 2 requests from IP A
	if !rl.Allow("10.0.0.1") {
		t.Fatal("first request from IP A should be allowed")
	}
	if !rl.Allow("10.0.0.1") {
		t.Fatal("second request from IP A should be allowed")
	}

	// IP A exhausted
	if rl.Allow("10.0.0.1") {
		t.Fatal("third request from IP A should be rejected")
	}

	// IP B should still work independently
	if !rl.Allow("10.0.0.2") {
		t.Fatal("first request from IP B should be allowed")
	}
	if !rl.Allow("10.0.0.2") {
		t.Fatal("second request from IP B should be allowed")
	}
}

func TestRateLimiterDisabled(t *testing.T) {
	rl := NewRateLimiter(0, logrus.NewEntry(logrus.New()))

	// All requests should be allowed when disabled
	for i := 0; i < 100; i++ {
		if !rl.Allow("192.168.1.1") {
			t.Fatalf("request %d should be allowed when rate limiting is disabled", i+1)
		}
	}
}

func TestRateLimiterNegativeDisabled(t *testing.T) {
	rl := NewRateLimiter(-1, logrus.NewEntry(logrus.New()))

	for i := 0; i < 10; i++ {
		if !rl.Allow("192.168.1.1") {
			t.Fatalf("request %d should be allowed when rate limiting is disabled", i+1)
		}
	}
}

func TestRateLimiterWindowExpiry(t *testing.T) {
	rl := NewRateLimiter(2, logrus.NewEntry(logrus.New()))

	// Manually inject old timestamps to simulate time passing
	rl.mu.Lock()
	cw := &clientWindow{
		timestamps: []time.Time{
			time.Now().Add(-2 * time.Minute), // Expired
			time.Now().Add(-2 * time.Minute), // Expired
		},
	}
	rl.clients["192.168.1.1"] = cw
	rl.mu.Unlock()

	// Old timestamps should be expired, so new requests should be allowed
	if !rl.Allow("192.168.1.1") {
		t.Fatal("request should be allowed after window expires")
	}
	if !rl.Allow("192.168.1.1") {
		t.Fatal("second request should be allowed after window expires")
	}

	// Now exhausted again
	if rl.Allow("192.168.1.1") {
		t.Fatal("third request should be rejected")
	}
}

func TestRateLimiterMiddleware(t *testing.T) {
	logger := logrus.NewEntry(logrus.New())
	rl := NewRateLimiter(2, logger)

	handlerCalled := 0
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled++
		w.WriteHeader(http.StatusOK)
	})

	handler := rl.Middleware(inner)

	// First 2 requests succeed
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, w.Code)
		}
	}

	// 3rd request should get 429
	req := httptest.NewRequest("POST", "/", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", w.Code)
	}

	if handlerCalled != 2 {
		t.Fatalf("expected inner handler called 2 times, got %d", handlerCalled)
	}
}

func TestRateLimiterMiddlewareDisabled(t *testing.T) {
	rl := NewRateLimiter(0, logrus.NewEntry(logrus.New()))

	handlerCalled := 0
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled++
		w.WriteHeader(http.StatusOK)
	})

	handler := rl.Middleware(inner)

	// All requests should pass through when disabled
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest("POST", "/", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, w.Code)
		}
	}

	if handlerCalled != 10 {
		t.Fatalf("expected 10 calls, got %d", handlerCalled)
	}
}

func TestRateLimiterStats(t *testing.T) {
	rl := NewRateLimiter(2, logrus.NewEntry(logrus.New()))

	rl.Allow("10.0.0.1") // allowed - but Allow doesn't track stats

	// Use middleware to test stats
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := rl.Middleware(inner)

	// Reset with fresh limiter for stats test
	rl2 := NewRateLimiter(2, logrus.NewEntry(logrus.New()))
	handler2 := rl2.Middleware(inner)

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("POST", "/", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		w := httptest.NewRecorder()
		handler2.ServeHTTP(w, req)
	}

	allowed, rejected := rl2.Stats()
	if allowed != 2 {
		t.Fatalf("expected 2 allowed, got %d", allowed)
	}
	if rejected != 1 {
		t.Fatalf("expected 1 rejected, got %d", rejected)
	}

	// Suppress unused variable warnings
	_ = handler
}

func TestRateLimiterCleanup(t *testing.T) {
	rl := NewRateLimiter(10, logrus.NewEntry(logrus.New()))

	// Add entries with expired timestamps
	rl.mu.Lock()
	rl.clients["expired-ip"] = &clientWindow{
		timestamps: []time.Time{time.Now().Add(-2 * time.Minute)},
	}
	rl.clients["active-ip"] = &clientWindow{
		timestamps: []time.Time{time.Now()},
	}
	rl.mu.Unlock()

	rl.cleanup()

	rl.mu.Lock()
	defer rl.mu.Unlock()

	if _, exists := rl.clients["expired-ip"]; exists {
		t.Fatal("expired-ip should have been cleaned up")
	}
	if _, exists := rl.clients["active-ip"]; !exists {
		t.Fatal("active-ip should still exist")
	}
}

func TestExtractIP(t *testing.T) {
	tests := []struct {
		remoteAddr string
		expected   string
	}{
		{"192.168.1.1:12345", "192.168.1.1"},
		{"10.0.0.1:80", "10.0.0.1"},
		{"[::1]:8080", "::"},                                                  // IPv6 loopback masked to /64
		{"[2001:db8::1]:8080", "2001:db8::"},                                  // IPv6 masked to /64
		{"[2001:db8:abcd:1234:5678:9abc:def0:1]:443", "2001:db8:abcd:1234::"}, // Full IPv6 masked to /64
		{"2001:db8::1", "2001:db8::"},           // Bare IPv6 without port (SplitHostPort fails, normalizeIP still masks to /64)
		{"invalid-no-port", "invalid-no-port"},
	}

	for _, tt := range tests {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tt.remoteAddr
		got := extractIP(r)
		if got != tt.expected {
			t.Errorf("extractIP(%q) = %q, want %q", tt.remoteAddr, got, tt.expected)
		}
	}
}

func TestNormalizeIP(t *testing.T) {
	tests := []struct {
		name     string
		ip       string
		expected string
	}{
		// IPv4: returned as-is
		{"ipv4", "192.168.1.1", "192.168.1.1"},
		{"ipv4 loopback", "127.0.0.1", "127.0.0.1"},

		// IPv6: masked to /64
		{"ipv6 loopback", "::1", "::"},
		{"ipv6 simple", "2001:db8::1", "2001:db8::"},
		{"ipv6 full", "2001:db8:abcd:1234:5678:9abc:def0:1", "2001:db8:abcd:1234::"},

		// Same /64 prefix must produce same key
		{"ipv6 /64 peer A", "2001:db8::1", "2001:db8::"},
		{"ipv6 /64 peer B", "2001:db8::2", "2001:db8::"},
		{"ipv6 /64 peer C", "2001:db8::ffff", "2001:db8::"},

		// Different /64 prefixes produce different keys
		{"ipv6 different /64", "2001:db8:1::1", "2001:db8:1::"},

		// Invalid: returned as-is
		{"invalid", "not-an-ip", "not-an-ip"},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeIP(tt.ip)
			if got != tt.expected {
				t.Errorf("normalizeIP(%q) = %q, want %q", tt.ip, got, tt.expected)
			}
		})
	}

	// Verify same-bucket property: two addresses in the same /64 produce identical keys
	a := normalizeIP("2001:db8::1")
	b := normalizeIP("2001:db8::2")
	if a != b {
		t.Errorf("2001:db8::1 and 2001:db8::2 should share same bucket, got %q vs %q", a, b)
	}
}

func TestRateLimiterIPv6SameBucket(t *testing.T) {
	rl := NewRateLimiter(2, logrus.NewEntry(logrus.New()))

	// Use normalized IPs (as extractIP would produce)
	ip := normalizeIP("2001:db8::1")

	// Exhaust the bucket from "address 1"
	if !rl.Allow(ip) {
		t.Fatal("first request should be allowed")
	}
	if !rl.Allow(ip) {
		t.Fatal("second request should be allowed")
	}

	// "Address 2" in the same /64 should be blocked (same bucket)
	ip2 := normalizeIP("2001:db8::2")
	if ip != ip2 {
		t.Fatalf("expected same normalized IP, got %q vs %q", ip, ip2)
	}
	if rl.Allow(ip2) {
		t.Fatal("third request from same /64 should be rejected")
	}
}
