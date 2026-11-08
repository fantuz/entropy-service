package transport

import (
	"crypto/tls"
	"fmt"
)

// NewTLSConfig loads a certificate and builds a TLS 1.3-only configuration.
// The caller owns startup failure handling; this helper never exits the process.
func NewTLSConfig(certFile, keyFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load TLS certificate: %w", err)
	}

	return &tls.Config{
		MinVersion: tls.VersionTLS13,

		// TLS 1.3 handles cipher selection internally
		Certificates: []tls.Certificate{cert},

		ClientAuth: tls.NoClientCert,

		// Fast, modern curves
		CurvePreferences: []tls.CurveID{
			tls.X25519,
			tls.CurveP256,
		},

		// Enable session resumption (important for API workloads)
		SessionTicketsDisabled: false,
	}, nil
}
