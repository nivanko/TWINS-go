package rpc

import (
	"errors"
	"net"
	"testing"

	"github.com/sirupsen/logrus"
)

// mockAddr implements net.Addr for testing.
type mockAddr struct {
	addr string
}

func (a *mockAddr) Network() string { return "tcp" }
func (a *mockAddr) String() string  { return a.addr }

// mockConn implements the net.Conn methods needed by rateLimitedListener.
type mockConn struct {
	net.Conn // embed for unused methods
	remote   net.Addr
	closed   bool
}

func (c *mockConn) RemoteAddr() net.Addr { return c.remote }
func (c *mockConn) Close() error         { c.closed = true; return nil }

// mockListener implements net.Listener, returning pre-configured connections.
type mockListener struct {
	conns []*mockConn
	idx   int
}

func (m *mockListener) Accept() (net.Conn, error) {
	if m.idx >= len(m.conns) {
		return nil, errors.New("listener closed")
	}
	conn := m.conns[m.idx]
	m.idx++
	return conn, nil
}

func (m *mockListener) Close() error   { return nil }
func (m *mockListener) Addr() net.Addr { return &mockAddr{addr: "127.0.0.1:8080"} }

func TestRateLimitedListenerAccept(t *testing.T) {
	logger := logrus.NewEntry(logrus.New())
	limiter := NewRateLimiter(2, logger) // 2 connections/min/IP
	defer limiter.Stop()

	// 4 connections from the same IP, only first 2 should be accepted
	conns := []*mockConn{
		{remote: &mockAddr{addr: "10.0.0.1:1111"}},
		{remote: &mockAddr{addr: "10.0.0.1:2222"}},
		{remote: &mockAddr{addr: "10.0.0.1:3333"}}, // should be rejected
		{remote: &mockAddr{addr: "10.0.0.2:4444"}}, // different IP, should pass
	}

	ml := &mockListener{conns: conns}
	ln := newRateLimitedListener(ml, limiter, logger)

	// First connection: allowed
	conn1, err := ln.Accept()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conn1.(*mockConn).closed {
		t.Fatal("first connection should not be closed")
	}

	// Second connection: allowed
	conn2, err := ln.Accept()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conn2.(*mockConn).closed {
		t.Fatal("second connection should not be closed")
	}

	// Third connection from same IP: rejected (closed), fourth from different IP: accepted
	conn4, err := ln.Accept()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The third conn should have been closed (rate limited)
	if !conns[2].closed {
		t.Fatal("third connection should have been closed (rate limited)")
	}

	// The returned connection should be the fourth (different IP)
	if conn4.RemoteAddr().String() != "10.0.0.2:4444" {
		t.Fatalf("expected connection from 10.0.0.2:4444, got %s", conn4.RemoteAddr().String())
	}
}

func TestRateLimitedListenerDisabled(t *testing.T) {
	logger := logrus.NewEntry(logrus.New())
	limiter := NewRateLimiter(0, logger) // disabled
	defer limiter.Stop()

	conns := []*mockConn{
		{remote: &mockAddr{addr: "10.0.0.1:1111"}},
		{remote: &mockAddr{addr: "10.0.0.1:2222"}},
		{remote: &mockAddr{addr: "10.0.0.1:3333"}},
	}

	ml := &mockListener{conns: conns}
	ln := newRateLimitedListener(ml, limiter, logger)

	// All connections should pass through when rate limiting is disabled
	for i := 0; i < 3; i++ {
		conn, err := ln.Accept()
		if err != nil {
			t.Fatalf("connection %d: unexpected error: %v", i, err)
		}
		if conn.(*mockConn).closed {
			t.Fatalf("connection %d should not be closed when rate limiting is disabled", i)
		}
	}
}

func TestRateLimitedListenerIPv6SamePrefix(t *testing.T) {
	logger := logrus.NewEntry(logrus.New())
	limiter := NewRateLimiter(1, logger) // 1 connection/min/IP
	defer limiter.Stop()

	// Two IPv6 addresses in the same /64 — should share a bucket
	conns := []*mockConn{
		{remote: &mockAddr{addr: "[2001:db8::1]:1111"}},
		{remote: &mockAddr{addr: "[2001:db8::2]:2222"}},   // same /64, should be rejected
		{remote: &mockAddr{addr: "[2001:db8:1::1]:3333"}}, // different /64, should pass
	}

	ml := &mockListener{conns: conns}
	ln := newRateLimitedListener(ml, limiter, logger)

	// First connection: allowed
	conn1, err := ln.Accept()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conn1.(*mockConn).closed {
		t.Fatal("first connection should not be closed")
	}

	// Second connection (same /64): rejected, third (different /64): accepted
	conn3, err := ln.Accept()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !conns[1].closed {
		t.Fatal("second connection should have been closed (same /64 bucket)")
	}
	if conn3.RemoteAddr().String() != "[2001:db8:1::1]:3333" {
		t.Fatalf("expected connection from [2001:db8:1::1]:3333, got %s", conn3.RemoteAddr().String())
	}
}

func TestRateLimitedListenerPropagatesError(t *testing.T) {
	logger := logrus.NewEntry(logrus.New())
	limiter := NewRateLimiter(10, logger)
	defer limiter.Stop()

	// Empty listener — Accept returns error immediately
	ml := &mockListener{conns: nil}
	ln := newRateLimitedListener(ml, limiter, logger)

	_, err := ln.Accept()
	if err == nil {
		t.Fatal("expected error from empty listener")
	}
}
