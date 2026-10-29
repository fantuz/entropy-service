package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/drbg"
)

func TestExtractByteCountBounds(t *testing.T) {
	const maxBytes = 1 << 25

	for _, tc := range []struct {
		name  string
		query string
		want  int
	}{
		{"default", "", 2048},
		{"minimum", "?bytes=1", 1},
		{"maximum", "?bytes=33554432", maxBytes},
		{"over maximum", "?bytes=33554433", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, err := extractByteCount(httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/"+tc.query, nil), 2048, maxBytes)
			if (err != nil) != (tc.want == 0) || n != tc.want {
				t.Fatalf("bytes = %d, err = %v; want %d", n, err, tc.want)
			}
		})
	}
}

func TestByteEndpointLimits(t *testing.T) {
	// Use a lower cap than every endpoint's default to check that it is
	// enforced for explicit sizes and for requests with no bytes parameter.
	const maxBytes = 64

	d, err := drbg.NewDRBG(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}

	handlers := map[string]http.HandlerFunc{
		"/v1/data/random": randomBytesHandler(&d, maxBytes, 20, false),
		"/v1/data/test":   randomHandler(&d, maxBytes),
		"/stream":         wsBinaryHandler(&d, 1, 2048, maxBytes, newWebSocketBudget(128, 1<<30)),
		"/bytes":          wsBytesHandler(&d, 1, maxBytes, newWebSocketBudget(128, 1<<30)),
		"/colors":         wsBytesHandler(&d, 1, maxBytes, newWebSocketBudget(128, 1<<30)),
	}
	mux := http.NewServeMux()

	for path, handler := range handlers {
		mux.HandleFunc(path, handler)
	}

	server := httptest.NewServer(mux)
	defer server.Close()

	client := &http.Client{Timeout: 3 * time.Second}

	for path, handler := range handlers {
		t.Run(path, func(t *testing.T) {
			for _, query := range []string{
				"bytes=0", "bytes=-1", "bytes=", "bytes=abc", "bytes=1.5",
				"bytes=65", "bytes=33554433", "bytes=999999999999999999999999",
				"bytes=1&bytes=2", "bytes=1&bytes=1",
			} {
				r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path+"?"+query, nil)
				// A valid upgrade request ensures a 400 comes from validation,
				// rather than from missing WebSocket handshake headers.
				r.Header.Set("Connection", "Upgrade")
				r.Header.Set("Upgrade", "websocket")
				r.Header.Set("Sec-WebSocket-Version", "13")
				r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")

				w := httptest.NewRecorder()
				handler(w, r)

				if w.Code != http.StatusBadRequest {
					t.Errorf("%s: status = %d, want 400", query, w.Code)
				}
			}

			for _, tc := range []struct {
				query string
				want  int
			}{
				{"?refresh=1", maxBytes},
				{"?bytes=1&refresh=1", 1},
				{"?bytes=64&refresh=1", maxBytes},
			} {
				var data []byte

				if strings.HasPrefix(path, "/v1/") {
					r, reqErr := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+path+tc.query, nil)
					if reqErr != nil {
						t.Fatal(reqErr)
					}

					resp, fetchErr := client.Do(r)
					if fetchErr != nil {
						t.Fatal(fetchErr)
					}

					data, err = io.ReadAll(resp.Body)
					_ = resp.Body.Close()

					if err != nil || resp.StatusCode != http.StatusOK {
						t.Fatalf("status = %d, read err = %v", resp.StatusCode, err)
					}
				} else {
					data = readLimitTestFrame(t, "ws"+strings.TrimPrefix(server.URL, "http")+path+tc.query)
				}

				if len(data) != tc.want {
					t.Errorf("%s: got %d bytes, want %d", tc.query, len(data), tc.want)
				}
			}
		})
	}
}

func readLimitTestFrame(t *testing.T, url string) []byte {
	t.Helper()

	conn, resp, err := websocket.DefaultDialer.DialContext(t.Context(), url, nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}

	if err != nil {
		t.Fatal(err)
	}

	defer conn.Close()

	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}

	kind, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}

	if kind == websocket.TextMessage {
		var frame EntropyDataFrame

		if err := json.Unmarshal(data, &frame); err != nil {
			t.Fatal(err)
		}

		data, err = base64.StdEncoding.DecodeString(frame.Base64)
		if err != nil {
			t.Fatal(err)
		}
	}

	return data
}
