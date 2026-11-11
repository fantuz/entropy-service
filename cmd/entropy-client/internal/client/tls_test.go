package client

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/metrics"
)

type testTLSIdentity struct {
	pair tls.Certificate
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func testTLSTemplate() *x509.Certificate {
	return &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "client TLS fixture"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
		KeyUsage:    x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
}

func issueTestTLSIdentity(t *testing.T, template *x509.Certificate, issuer *testTLSIdentity) testTLSIdentity {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	parent, signer := template, key
	if issuer != nil {
		parent, signer = issuer.cert, issuer.key
	}

	der, err := x509.CreateCertificate(rand.Reader, template, parent, key.Public(), signer)
	if err != nil {
		t.Fatal(err)
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}

	return testTLSIdentity{pair: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: cert}, cert: cert, key: key}
}

func trustTestTLSIdentity(t *testing.T, identities ...testTLSIdentity) {
	t.Helper()

	roots := x509.NewCertPool()
	for _, identity := range identities {
		roots.AddCert(identity.cert)
	}

	originalHTTP, originalWS := entropyHTTPClient, websocket.DefaultDialer

	transport, ok := originalHTTP.Transport.(*http.Transport)
	if !ok {
		t.Fatal("expected the production HTTP transport")
	}

	transport = transport.Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	clonedHTTP, clonedWS := *originalHTTP, *originalWS
	clonedHTTP.Transport = transport
	clonedWS.TLSClientConfig = transport.TLSClientConfig.Clone()
	entropyHTTPClient, websocket.DefaultDialer = &clonedHTTP, &clonedWS

	t.Cleanup(func() {
		transport.CloseIdleConnections()

		entropyHTTPClient, websocket.DefaultDialer = originalHTTP, originalWS
	})
}

func serveTestTLSIdentity(t *testing.T, identity testTLSIdentity, handler http.Handler) *httptest.Server {
	t.Helper()

	server := httptest.NewUnstartedServer(handler)
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{identity.pair}, MinVersion: tls.VersionTLS13}
	server.StartTLS()
	t.Cleanup(server.Close)

	return server
}

func fetchTestTLSFrame(ctx context.Context, mode, endpoint string) ([]byte, error) {
	if mode == "https" {
		return FetchEntropy(ctx, endpoint, 8)
	}

	var payload []byte

	done := errors.New("fixture frame received")

	err := ReceiveEntropy(ctx, "wss"+strings.TrimPrefix(endpoint, "https"), func(frame StreamFrame) error {
		payload = bytes.Clone(frame.Data)
		frame.Release()

		return done
	})
	if errors.Is(err, done) {
		return payload, nil
	}

	return payload, err
}

func TestTLSCertificateVerification(t *testing.T) {
	valid := issueTestTLSIdentity(t, testTLSTemplate(), nil)
	replacement := issueTestTLSIdentity(t, testTLSTemplate(), nil)
	caTemplate := testTLSTemplate()
	caTemplate.IsCA, caTemplate.BasicConstraintsValid = true, true
	caTemplate.KeyUsage |= x509.KeyUsageCertSign
	ca := issueTestTLSIdentity(t, caTemplate, nil)
	leaf := issueTestTLSIdentity(t, testTLSTemplate(), &ca)
	renewalTemplate := testTLSTemplate()
	renewalTemplate.SerialNumber = big.NewInt(2)
	renewal := issueTestTLSIdentity(t, renewalTemplate, &ca)
	wrongHostTemplate := testTLSTemplate()
	wrongHostTemplate.IPAddresses = nil
	wrongHostTemplate.DNSNames = []string{"localhost"}
	wrongHost := issueTestTLSIdentity(t, wrongHostTemplate, nil)
	expiredTemplate := testTLSTemplate()
	expiredTemplate.NotAfter = time.Now().Add(-time.Minute)
	expired := issueTestTLSIdentity(t, expiredTemplate, nil)
	futureTemplate := testTLSTemplate()
	futureTemplate.NotBefore = time.Now().Add(time.Minute)
	future := issueTestTLSIdentity(t, futureTemplate, nil)
	wrongUsageTemplate := testTLSTemplate()
	wrongUsageTemplate.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	wrongUsage := issueTestTLSIdentity(t, wrongUsageTemplate, nil)
	payload := []byte{0, 255, 27, 64, 128, 1, 2, 3}

	for _, tc := range []struct {
		name     string
		identity testTLSIdentity
		trust    []testTLSIdentity
		failure  string
	}{
		{"trusted self-signed", valid, []testTLSIdentity{valid}, ""},
		{"private CA", leaf, []testTLSIdentity{ca}, ""},
		{"renewed leaf with same CA", renewal, []testTLSIdentity{ca}, ""},
		{"unknown authority", valid, nil, "authority"},
		{"wrong IPv4 SAN", wrongHost, []testTLSIdentity{wrongHost}, "hostname"},
		{"expired", expired, []testTLSIdentity{expired}, "invalid"},
		{"not yet valid", future, []testTLSIdentity{future}, "invalid"},
		{"wrong extended key usage", wrongUsage, []testTLSIdentity{wrongUsage}, "invalid"},
		{"replacement with old trust", replacement, []testTLSIdentity{valid}, "authority"},
		{"replacement with overlapping trust", replacement, []testTLSIdentity{valid, replacement}, ""},
	} {
		for _, mode := range []string{"https", "wss"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				trustTestTLSIdentity(t, tc.trust...)

				var requests atomic.Int32

				server := serveTestTLSIdentity(t, tc.identity, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)

					if r.TLS == nil || r.TLS.Version != tls.VersionTLS13 {
						t.Error("did not negotiate the server's TLS 1.3 policy")
					}

					if mode == "https" {
						_, _ = w.Write(payload)

						return
					}

					upgrader := websocket.Upgrader{}

					conn, err := upgrader.Upgrade(w, r, nil)
					if err != nil {
						t.Error(err)

						return
					}
					defer conn.Close()

					_ = conn.WriteMessage(websocket.BinaryMessage, payload)
				}))

				ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
				defer cancel()

				before := metrics.ClientBytesReceived()

				data, err := fetchTestTLSFrame(ctx, mode, server.URL+"?bytes=8")
				if tc.failure == "" {
					if err != nil || !bytes.Equal(data, payload) || requests.Load() != 1 {
						t.Fatalf("trusted entropy rejected: data=%x requests=%d err=%v", data, requests.Load(), err)
					}

					return
				}

				if err == nil || len(data) != 0 || requests.Load() != 0 || metrics.ClientBytesReceived() != before {
					t.Fatalf("invalid certificate delivered/counted entropy: data=%x requests=%d err=%v", data, requests.Load(), err)
				}

				var (
					authority x509.UnknownAuthorityError
					hostname  x509.HostnameError
					invalid   x509.CertificateInvalidError
				)

				if (tc.failure == "authority" && !errors.As(err, &authority)) ||
					(tc.failure == "hostname" && !errors.As(err, &hostname)) ||
					(tc.failure == "invalid" && !errors.As(err, &invalid)) {
					t.Fatalf("certificate rejection cause lost: %v", err)
				}

				if _, retry := RetryableFailure(err); retry {
					t.Fatalf("certificate failure admitted for automatic retry: %v", err)
				}
			})
		}
	}
}

func TestHTTPSRedirectDoesNotDowngrade(t *testing.T) {
	identity := issueTestTLSIdentity(t, testTLSTemplate(), nil)
	trustTestTLSIdentity(t, identity)

	var insecureRequests atomic.Int32

	insecure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		insecureRequests.Add(1)

		_, _ = w.Write([]byte("unprotected entropy"))
	}))
	defer insecure.Close()

	secure := serveTestTLSIdentity(t, identity, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, insecure.URL, http.StatusFound)
	}))
	for _, mode := range []string{"pull", "diagnostic"} {
		t.Run(mode, func(t *testing.T) {
			var (
				data []byte
				err  error
			)
			if mode == "pull" {
				data, err = FetchEntropy(t.Context(), secure.URL, 8)
			} else {
				err = TestEntropyHTTP(t.Context(), secure.URL)
			}

			if !errors.Is(err, ErrInsecureRedirect) || len(data) != 0 || insecureRequests.Load() != 0 {
				t.Fatalf("HTTPS downgraded to HTTP: data=%q insecure requests=%d err=%v", data, insecureRequests.Load(), err)
			}

			if _, retry := RetryableFailure(err); retry {
				t.Fatalf("insecure redirect admitted for automatic retry: %v", err)
			}
		})
	}
}

func TestEntropyRedirectsRemainBoundedAndSupportTLS(t *testing.T) {
	identity := issueTestTLSIdentity(t, testTLSTemplate(), nil)
	trustTestTLSIdentity(t, identity)

	payload := []byte("entropy fixture")
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, "/entropy", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		default:
			_, _ = w.Write(payload)
		}
	})
	secure := serveTestTLSIdentity(t, identity, handler)

	insecure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/upgrade" {
			http.Redirect(w, r, secure.URL+"/entropy", http.StatusFound)

			return
		}

		handler.ServeHTTP(w, r)
	}))
	defer insecure.Close()

	for _, endpoint := range []string{insecure.URL + "/redirect", insecure.URL + "/upgrade", secure.URL + "/redirect"} {
		data, err := FetchEntropy(t.Context(), endpoint, 8)
		if err != nil || !bytes.Equal(data, payload) {
			t.Fatalf("valid redirect rejected: %s: %q, %v", endpoint, data, err)
		}
	}

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	data, err := FetchEntropy(ctx, secure.URL+"/loop", 8)
	if err == nil || len(data) != 0 || !strings.Contains(err.Error(), "stopped after 10 redirects") {
		t.Fatalf("redirect loop was not bounded: %q, %v", data, err)
	}
}
