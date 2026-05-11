package rpc

import (
	"net"

	"github.com/sirupsen/logrus"
)

// rateLimitedListener wraps a net.Listener to enforce per-IP connection
// rate limiting at the TCP accept level — before the TLS handshake.
// This defends against TLS handshake DoS where an attacker opens many
// connections to force expensive cryptographic operations.
type rateLimitedListener struct {
	net.Listener
	limiter *RateLimiter
	logger  *logrus.Entry
}

// newRateLimitedListener wraps ln with per-IP TCP connection rate limiting.
// The limiter's maxPerMinute controls how many connections a single IP
// (or IPv6 /64 prefix) may open per minute.
func newRateLimitedListener(ln net.Listener, limiter *RateLimiter, logger *logrus.Entry) net.Listener {
	return &rateLimitedListener{
		Listener: ln,
		limiter:  limiter,
		logger:   logger,
	}
}

// Accept waits for and returns the next allowed connection.
// Connections from IPs that exceed the rate limit are closed immediately
// (before any TLS handshake) and Accept loops to the next connection.
func (l *rateLimitedListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}

		host, _, err := net.SplitHostPort(conn.RemoteAddr().String())
		if err != nil {
			// Can't extract IP — allow the connection through
			return conn, nil
		}

		ip := normalizeIP(host)
		if l.limiter.Allow(ip) {
			return conn, nil
		}

		// Rate exceeded — close before TLS handshake
		if l.logger != nil {
			l.logger.WithField("ip", ip).Debug("TCP connection rejected: rate limit exceeded")
		}
		conn.Close()
	}
}
