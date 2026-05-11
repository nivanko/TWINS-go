package rpc

import "time"

// Config holds RPC server configuration
type Config struct {
	// Network settings
	Host string
	Port int

	// Authentication
	Username      string
	Password      string
	UseCookieAuth bool
	DataDir       string

	// Connection settings
	MaxClients        int
	ReadTimeout       time.Duration
	ReadHeaderTimeout time.Duration // Slowloris defense: max time to read request headers (independent of ReadTimeout)
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxRequestSize    int64
	// RateLimit is the maximum requests per minute per IP (0 = no limit).
	// Applied at two independent layers: (1) TCP accept-level rate limiting
	// (before TLS handshake, defends against handshake DoS) and (2) HTTP
	// middleware-level rate limiting (per-request, returns 429). With keep-alive,
	// the TCP limiter fires once per connection while the HTTP limiter counts
	// each request — they defend different attack vectors.
	RateLimit int

	// CORS settings
	EnableCORS     bool
	AllowedOrigins []string

	// IP filtering - compatible with legacy -rpcallowip
	// If empty, only localhost (127.0.0.1, ::1) is allowed
	// Supports CIDR notation (192.168.1.0/24) and single IPs
	AllowedIPs []string

	// Safety double-gate: both YAML and CLI flag required for plaintext on non-loopback
	AllowPlaintextPublic bool

	// TLS settings
	TLS TLSConfig
}

// TLSConfig holds TLS settings for the RPC server runtime
type TLSConfig struct {
	Enabled              bool
	CertFile             string
	KeyFile              string
	ExpiryWarnDays       int
	ReloadPassphraseFile string
	MTLS                 MTLSConfig
	Client               ClientTLSConfig
}

// MTLSConfig holds mutual TLS settings
type MTLSConfig struct {
	Enabled      bool
	ClientCAFile string
}

// ClientTLSConfig holds client-side TLS verification settings
type ClientTLSConfig struct {
	CAFile    string
	PinSHA256 string
}

// DefaultConfig returns a default RPC configuration
func DefaultConfig() *Config {
	return &Config{
		Host:              "127.0.0.1",
		Port:              11771, // Default TWINS RPC port
		UseCookieAuth:     true,
		MaxClients:        100,
		ReadTimeout:       30 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxRequestSize:    10 * 1024 * 1024, // 10MB
		EnableCORS:        false,
	}
}
