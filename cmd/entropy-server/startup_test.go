package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPAndHTTPSStartupServeAndShutdown(t *testing.T) {
	fixture := httptest.NewTLSServer(http.NotFoundHandler())
	certificate := fixture.TLS.Certificates[0]
	roots := x509.NewCertPool()
	roots.AddCert(fixture.Certificate())
	fixture.Close()

	for _, secure := range []bool{false, true} {
		t.Run(boolLabel(secure), func(t *testing.T) {
			// The startup helpers own their listener. Reserve an available loopback
			// address immediately before handing it to the real startup path.
			var listenConfig net.ListenConfig

			reservation, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}

			address := reservation.Addr().String()
			if err := reservation.Close(); err != nil {
				t.Fatal(err)
			}

			handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, "started")
			})

			var server *managedServer

			scheme := "http"

			if secure {
				server, err = startHTTPS(t.Context(), address, handler, &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}})
				scheme = "https"
			} else {
				server, err = startHTTP(t.Context(), address, handler)
			}

			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = server.Close() })

			if server.listenAddr != address {
				t.Fatalf("reported listener %q differs from bound address %q", server.listenAddr, address)
			}

			transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots}}
			defer transport.CloseIdleConnections()

			client := &http.Client{Transport: transport, Timeout: time.Second}

			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, scheme+"://"+address, nil)
			if err != nil {
				t.Fatal(err)
			}

			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}

			body, err := io.ReadAll(response.Body)

			_ = response.Body.Close()
			if err != nil || string(body) != "started" || response.StatusCode != http.StatusOK {
				t.Fatalf("startup did not serve a functional response: %q, %v", body, err)
			}

			if secure && (response.TLS == nil || response.TLS.Version != tls.VersionTLS13) {
				t.Fatal("HTTPS did not negotiate verified TLS 1.3")
			}

			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()

			if err := server.Shutdown(ctx); err != nil {
				t.Fatal(err)
			}

			if response, err := client.Do(request.Clone(t.Context())); err == nil {
				_ = response.Body.Close()

				t.Fatal("shutdown left the listener serving requests")
			}
		})
	}
}

func TestStartupRejectsOccupiedAndInvalidAddresses(t *testing.T) {
	var listenConfig net.ListenConfig

	listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	for _, address := range []string{listener.Addr().String(), "127.0.0.1:invalid"} {
		if server, err := startDebugHTTP(t.Context(), address, http.NotFoundHandler()); err == nil || server != nil {
			t.Fatalf("debug server announced unavailable address %q", address)
		}

		if server, err := startHTTP(t.Context(), address, http.NotFoundHandler()); err == nil || server != nil {
			t.Fatalf("HTTP accepted unavailable address %q", address)
		}

		if server, err := startHTTPS(t.Context(), address, http.NotFoundHandler(), &tls.Config{MinVersion: tls.VersionTLS13}); err == nil || server != nil {
			t.Fatalf("HTTPS accepted unavailable address %q", address)
		}
	}
}
