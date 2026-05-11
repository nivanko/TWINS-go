package rpcclient

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"time"

	rpc "github.com/twins-dev/twins-core/internal/rpc"
)

// SPKIFingerprint computes the RFC 7469 SPKI hash of a certificate:
// base64(sha256(cert.RawSubjectPublicKeyInfo)).
//
// This is the single source of truth shared between the VerifyConnection
// callback and the twins-cli rpc-cert-fingerprint helper.
func SPKIFingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return base64.StdEncoding.EncodeToString(sum[:])
}

// NewTransport creates an *http.Transport with pluggable TLS trust.
//
// Trust modes (when tlsEnabled is true):
//   - Default (no CAFile, no PinSHA256): system CA roots
//   - CAFile set: custom CA pool loaded from PEM file
//   - PinSHA256 set: SPKI pin verification via VerifyConnection callback
//     (can be combined with CAFile for chain validation against a custom CA)
//
// When tlsEnabled is false, a plain HTTP transport is returned.
func NewTransport(tlsEnabled bool, cfg rpc.ClientTLSConfig, timeout time.Duration) (*http.Transport, error) {
	transport := &http.Transport{
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: timeout,
	}

	if !tlsEnabled {
		return transport, nil
	}

	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS13,
	}

	// Load custom CA bundle if provided
	var roots *x509.CertPool
	if cfg.CAFile != "" {
		caPEM, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA file %s: %w", cfg.CAFile, err)
		}
		roots = x509.NewCertPool()
		if !roots.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("failed to parse CA certificates from %s", cfg.CAFile)
		}
		tlsConfig.RootCAs = roots
	}

	// SPKI pin verification via VerifyConnection callback
	if cfg.PinSHA256 != "" {
		expectedPin := cfg.PinSHA256

		// InsecureSkipVerify disables Go's built-in chain verification so we
		// can perform it ourselves inside VerifyConnection with the pin check.
		tlsConfig.InsecureSkipVerify = true
		tlsConfig.VerifyConnection = func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return fmt.Errorf("server presented no certificates")
			}

			leaf := cs.PeerCertificates[0]

			// When a custom CA is provided alongside the pin, verify the
			// certificate chain against that CA before checking the pin.
			// When no CA is provided, the pin is the sole trust anchor
			// (common for self-signed certificates).
			if roots != nil {
				verifyOpts := x509.VerifyOptions{
					DNSName:       cs.ServerName,
					Intermediates: x509.NewCertPool(),
					Roots:         roots,
				}
				for _, inter := range cs.PeerCertificates[1:] {
					verifyOpts.Intermediates.AddCert(inter)
				}
				if _, err := leaf.Verify(verifyOpts); err != nil {
					return fmt.Errorf("certificate chain verification failed: %w", err)
				}
			}

			// Check SPKI pin
			actual := SPKIFingerprint(leaf)
			if actual != expectedPin {
				return fmt.Errorf("SPKI hash mismatch — use twins-cli rpc-cert-fingerprint to compute the correct value")
			}

			return nil
		}
	}

	transport.TLSClientConfig = tlsConfig
	return transport, nil
}
