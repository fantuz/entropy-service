package transport

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
)

// SHA-256 of the public SubjectPublicKeyInfo from the retired demonstration pair.
// Blocking the key also rejects a new certificate signed around the same key.
const retiredPublicKeySHA256 = "5e0e31f52f824f118de78d4f3563237d680e3603a988affe47ee4bdc548483bb"

// NewTLSConfig loads a certificate and builds a TLS 1.3-only configuration.
// The caller owns startup failure handling; this helper never exits the process.
func NewTLSConfig(certFile, keyFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load TLS certificate: %w", err)
	}

	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("parse TLS certificate: %w", err)
	}

	if err := rejectRetiredPublicKey(leaf); err != nil {
		return nil, err
	}

	cert.Leaf = leaf

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

func rejectRetiredPublicKey(cert *x509.Certificate) error {
	if fmt.Sprintf("%x", sha256.Sum256(cert.RawSubjectPublicKeyInfo)) == retiredPublicKeySHA256 {
		return errors.New("TLS certificate uses the publicly distributed demonstration private key; generate a new key pair (entropy-server generate-cert -help) or provision a CA-issued identity")
	}

	return nil
}
