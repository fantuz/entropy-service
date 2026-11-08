package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/config"
)

func TestRequestEndpoints(t *testing.T) {
	cases := []struct {
		name, mode, endpoint, want string
	}{
		{"bare HTTP", modePull, "http://example.test/entropy", "http://example.test/entropy?bytes=4&refresh=50"},
		{"bare HTTPS", modePull, "https://example.test/entropy?", "https://example.test/entropy?bytes=4&refresh=50"},
		{"bare WS", modeStream, "ws://example.test/stream", "ws://example.test/stream?bytes=4&refresh=50"},
		{"WSS query", modeStream, "wss://example.test/stream?bytes=1&refresh=900&lab=demo", "wss://example.test/stream?bytes=4&lab=demo&refresh=50"},
		{"escaped URL", modePull, "https://example.test/a%2Fb?label=a%26b&label=c%2Bd#preview", "https://example.test/a%2Fb?bytes=4&label=a%26b&label=c%2Bd&refresh=50#preview"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{Mode: tc.mode, HTTPUrl: "://unused", WSUrl: "://unused", Slice: 4, Refresh: 50 * time.Millisecond}
			if tc.mode == modePull {
				cfg.HTTPUrl = tc.endpoint
			} else {
				cfg.WSUrl = tc.endpoint
			}

			httpURL, wsURL, err := requestEndpoints(cfg)
			if err != nil {
				t.Fatal(err)
			}

			got, unused := httpURL, wsURL
			if tc.mode == modeStream {
				got, unused = wsURL, httpURL
			}

			if got != tc.want || unused != "://unused" {
				t.Fatalf("got selected %q and unused %q; want %q and unchanged unused URL", got, unused, tc.want)
			}
		})
	}
}

func TestRequestEndpointsRejectMalformedInput(t *testing.T) {
	for _, endpoint := range []string{"http://[::1", "http://example.test/?label=%zz", "http://example.test/?label=ok;lost=value"} {
		cfg := &config.Config{Mode: modePull, HTTPUrl: endpoint, Slice: 4, Refresh: time.Second}
		if _, _, err := requestEndpoints(cfg); err == nil {
			t.Errorf("accepted malformed endpoint %q", endpoint)
		}
	}
}

func TestConfiguredEndpointRequests(t *testing.T) {
	data := []byte{0, 1, 254, 255}
	wantQuery := url.Values{"bytes": {"4"}, "refresh": {"50"}, "lab": {"a&b"}, "label": {"one", "two"}}

	for _, mode := range []string{modePull, modeStream} {
		t.Run(mode, func(t *testing.T) {
			requests := make(chan url.Values, 1)
			done := make(chan struct{})

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(done)

				query := r.URL.Query()
				requests <- query

				if !reflect.DeepEqual(query, wantQuery) {
					http.Error(w, "unexpected query parameters", http.StatusBadRequest)

					return
				}

				if mode == modePull {
					_, _ = w.Write(data)

					return
				}

				upgrader := websocket.Upgrader{}

				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()

				_ = conn.WriteMessage(websocket.BinaryMessage, data)
				_, _, _ = conn.ReadMessage()
			}))
			defer server.Close()

			endpoint := server.URL + "/entropy?lab=a%26b&label=one&label=two&bytes=2&bytes=99&refresh=1&refresh=2"

			cfg := &config.Config{Mode: mode, HTTPUrl: endpoint, WSUrl: "://unused", Slice: 4, Refresh: 50 * time.Millisecond}
			if mode == modeStream {
				cfg.HTTPUrl, cfg.WSUrl = "://unused", "ws"+strings.TrimPrefix(endpoint, "http")
			}

			httpURL, wsURL, err := requestEndpoints(cfg)
			if err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()

			got, err := sampleEntropy(ctx, mode, httpURL, wsURL, cfg.Slice)
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("got %x, %v", got, err)
			}

			if query := <-requests; !reflect.DeepEqual(query, wantQuery) {
				t.Fatalf("got query %v, want %v", query, wantQuery)
			}

			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal("sample left the connection open")
			}
		})
	}
}
