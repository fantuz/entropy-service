package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/lab"
)

func TestLabRejectsRebindingHostBeforeCatalogOrSourceAccess(t *testing.T) {
	var reads atomic.Int64

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reads.Add(1)

		_, _ = io.WriteString(w, "registered-fixture")
	}))
	defer upstream.Close()

	registry, err := lab.New([]lab.Config{{ID: "private", Kind: "http", Endpoint: upstream.URL, MaxBytes: 64}})
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()

	budget := newWebSocketBudget(2, 4<<20)
	mux := http.NewServeMux()
	mux.HandleFunc("/lab/sources", labCatalogHandler(registry, budget, 64, false))
	mux.HandleFunc("/lab/stream", labStreamHandler(registry, budget, 64, false))

	server := httptest.NewServer(mux)
	defer server.Close()

	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	// Model DNS routing an attacker-controlled origin to our own loopback
	// server. Host and Origin still match, so the ordinary WS check permits it.
	foreign := "rebound.invalid:" + port

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/lab/sources", nil)
	if err != nil {
		t.Fatal(err)
	}

	request.Host = foreign
	request.Header.Set("X-Forwarded-Host", "localhost")
	request.Header.Set("Forwarded", "host=localhost;for=127.0.0.1")

	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}

	data, err := io.ReadAll(response.Body)
	_ = response.Body.Close()

	if err != nil || response.StatusCode != http.StatusForbidden || strings.Contains(string(data), `"sources"`) {
		t.Errorf("untrusted Host reached catalog: status=%d error=%v", response.StatusCode, err)
	}

	dialer := websocket.Dialer{HandshakeTimeout: time.Second, NetDialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
	}}
	header := http.Header{"Origin": {"http://" + foreign}, "X-Forwarded-Host": {"localhost"}}

	conn, response, err := dialer.DialContext(t.Context(), "ws://"+foreign+"/lab/stream?source=private&bytes=64&refresh=20", header)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if conn != nil {
		_ = conn.Close()
	}

	if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		t.Error("matching foreign Host/Origin bypassed local-only LAB admission")
	}

	waitBudgetActive(t, budget, 0)

	if reads.Load() != 0 || budget.usage().PeakActive != 0 {
		t.Fatal("untrusted Host acquired a reservation or read its upstream")
	}
}

func TestLabExplicitHostsPreserveOriginPolicyAndRemoteOptIn(t *testing.T) {
	for _, mode := range []string{"explicit-host", "wrong-origin", "remote-opt-in"} {
		t.Run(mode, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "fixture") }))
			defer upstream.Close()

			registry, err := lab.New([]lab.Config{{ID: "peer", Kind: "http", Endpoint: upstream.URL, MaxBytes: 64}})
			if err != nil {
				t.Fatal(err)
			}
			defer registry.Close()

			budget := newWebSocketBudget(1, 4<<20)

			server := httptest.NewServer(labStreamHandler(registry, budget, 64, mode == "remote-opt-in", "lab.private.test"))
			defer server.Close()

			_, port, err := net.SplitHostPort(server.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}

			host := "lab.private.test:" + port
			if mode == "remote-opt-in" {
				host = "remote.example.test:" + port
			}

			origin := "http://" + host
			if mode == "wrong-origin" {
				origin = "https://other.example.test"
			}

			dialer := websocket.Dialer{HandshakeTimeout: time.Second, NetDialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
			}}

			conn, response, err := dialer.DialContext(t.Context(), "ws://"+host+"/lab/stream?source=peer&bytes=64&refresh=20", http.Header{"Origin": {origin}})
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}

			if mode == "wrong-origin" {
				if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
					t.Fatal("explicit host weakened the ordinary WS Origin check")
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			defer conn.Close()

			_ = conn.SetReadDeadline(time.Now().Add(time.Second))

			_, data, err := conn.ReadMessage()
			if err != nil || string(data) != "fixture" {
				t.Fatalf("explicit access mode failed: %v", err)
			}
		})
	}

	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://lab.private.test/lab/stream", nil)
	request.RemoteAddr = "192.0.2.10:1000"
	request.Header.Set("X-Forwarded-For", "127.0.0.1")

	if labAllowed(request, false, "lab.private.test") {
		t.Fatal("additional Host trust also authorized a remote peer")
	}
}
