package main

import (
	"crypto/tls"
	"log"
)

// newTLSConfig builds a TLS 1.3–only config suitable for high-throughput APIs
func newTLSConfig(certFile, keyFile string) *tls.Config {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil { log.Fatalf("failed to load TLS certificate: %v", err) }

	return &tls.Config{
		MinVersion: tls.VersionTLS13,

		// TLS 1.3 handles cipher selection internally
		Certificates: []tls.Certificate{cert},

		// Fast, modern curves
		CurvePreferences: []tls.CurveID{
			tls.X25519,
			tls.CurveP256,
		},

		// remove comment to enable HTTP/2
		//NextProtos: []string{"h2", "http/1.1"},

		// Enable session resumption (important for API workloads)
		SessionTicketsDisabled: false,
	}
}
