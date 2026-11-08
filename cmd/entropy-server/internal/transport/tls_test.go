package transport

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testCertificate(t *testing.T) (string, string, *x509.CertPool) {
	t.Helper()

	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
		KeyUsage:    x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}

	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}

	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certPEM) {
		t.Fatal("could not trust the generated test certificate")
	}

	return certFile, keyFile, roots
}

func TestTLSConfigAcceptsTLS13AndRejectsTLS12(t *testing.T) {
	certFile, keyFile, roots := testCertificate(t)

	cfg, err := NewTLSConfig(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.MinVersion != tls.VersionTLS13 || cfg.ClientAuth != tls.NoClientCert || cfg.SessionTicketsDisabled || len(cfg.NextProtos) != 0 {
		t.Fatal("TLS version, authentication, session tickets or ALPN policy changed")
	}

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = cfg

	server.StartTLS()
	defer server.Close()

	for _, version := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
		transport := &http.Transport{TLSClientConfig: &tls.Config{
			RootCAs: roots, MinVersion: version, MaxVersion: version,
		}}
		defer transport.CloseIdleConnections()

		client := &http.Client{Transport: transport, Timeout: time.Second}

		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
		if err != nil {
			t.Fatal(err)
		}

		response, err := client.Do(request)
		if response != nil {
			_ = response.Body.Close()
		}

		if version == tls.VersionTLS12 {
			if err == nil {
				t.Fatal("TLS 1.2 unexpectedly connected to a TLS 1.3-only server")
			}

			continue
		}

		if err != nil {
			t.Fatal(err)
		}

		if response.TLS.Version != tls.VersionTLS13 || response.StatusCode != http.StatusNoContent {
			t.Fatalf("TLS 1.3 request failed: %+v", response)
		}
	}
}

func TestTLSConfigReturnsCertificateErrors(t *testing.T) {
	certFile, keyFile, _ := testCertificate(t)
	_, otherKey, _ := testCertificate(t)
	missing := filepath.Join(t.TempDir(), "missing.pem")

	malformed := filepath.Join(t.TempDir(), "malformed.pem")
	if err := os.WriteFile(malformed, []byte("not a PEM certificate"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name, cert, key string
		missing         bool
	}{
		{"missing certificate", missing, keyFile, true},
		{"missing key", certFile, missing, true},
		{"malformed certificate", malformed, keyFile, false},
		{"malformed key", certFile, malformed, false},
		{"mismatched pair", certFile, otherKey, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := NewTLSConfig(tc.cert, tc.key)
			if cfg != nil || err == nil {
				t.Fatalf("invalid pair returned config=%v error=%v", cfg, err)
			}

			if tc.missing && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("file error was not preserved for the caller: %v", err)
			}
		})
	}
}
