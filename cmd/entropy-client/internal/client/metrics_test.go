package client

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/metrics"
)

func TestHTTPMetricsCountOnlyAcceptedResponseBodies(t *testing.T) {
	payload := []byte{0, 255, 27, 64, 128, 1, 2, 3}
	for _, tc := range []struct {
		name                  string
		status, declaredBytes int
		wantError             bool
		wantCause             error
	}{
		{"complete", http.StatusOK, len(payload), false, nil},
		{"server error", http.StatusServiceUnavailable, len(payload), true, nil},
		{"truncated body", http.StatusOK, len(payload) + 1, true, io.ErrUnexpectedEOF},
		{"declared oversized", http.StatusOK, maxEntropyBytes + 1, true, ErrEntropyTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Length", strconv.Itoa(tc.declaredBytes))
				w.WriteHeader(tc.status)
				_, _ = w.Write(payload)
			}))
			defer server.Close()

			beforeBytes, beforeMessages := metrics.ClientBytesReceived(), metrics.ClientWSSMessages()
			data, err := FetchEntropy(t.Context(), server.URL, int64(len(payload)))

			wantBytes := uint64(len(payload))
			if tc.wantError {
				wantBytes = 0

				if err == nil || data != nil {
					t.Fatalf("rejected response returned data: %x, %v", data, err)
				}
			} else if err != nil || !bytes.Equal(data, payload) {
				t.Fatalf("accepted response changed: %x, %v", data, err)
			}

			if tc.wantCause != nil && !errors.Is(err, tc.wantCause) {
				t.Fatalf("expected rejection cause %v, got %v", tc.wantCause, err)
			}

			if got := metrics.ClientBytesReceived() - beforeBytes; got != wantBytes {
				t.Fatalf("HTTP counted %d received bytes, want %d", got, wantBytes)
			}

			if metrics.ClientWSSMessages() != beforeMessages {
				t.Fatal("HTTP response changed the WebSocket message counter")
			}
		})
	}
}

func TestWebSocketMetricsExcludeRejectedMessagesAndControlTraffic(t *testing.T) {
	payload := []byte{0, 255, 27, 64, 128, 1, 2, 3}
	for _, sized := range []bool{false, true} {
		for _, tc := range []struct {
			name                            string
			kind                            int
			data                            []byte
			variableMessages, sizedMessages uint64
		}{
			{"binary", websocket.BinaryMessage, payload, 1, 1},
			{"text", websocket.TextMessage, payload, 0, 0},
			{"short", websocket.BinaryMessage, payload[:7], 1, 0},
			{"long", websocket.BinaryMessage, bytes.Repeat([]byte{1}, 9), 1, 0},
			{"empty", websocket.BinaryMessage, nil, 1, 0},
			{"control only", 0, nil, 0, 0},
		} {
			label := "variable/" + tc.name
			if sized {
				label = "sized/" + tc.name
			}

			t.Run(label, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var upgrader websocket.Upgrader

					conn, err := upgrader.Upgrade(w, r, nil)
					if err != nil {
						return
					}
					defer conn.Close()

					for _, kind := range []int{websocket.PingMessage, websocket.PongMessage} {
						if err := conn.WriteControl(kind, []byte("control"), time.Now().Add(time.Second)); err != nil {
							return
						}
					}

					if tc.kind != 0 {
						if err := conn.WriteMessage(tc.kind, tc.data); err != nil {
							return
						}
					}

					_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"), time.Now().Add(time.Second))
					_ = conn.SetReadDeadline(time.Now().Add(time.Second))
					_, _, _ = conn.ReadMessage() // Complete the close handshake or observe rejection.
				}))
				defer server.Close()

				ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
				defer cancel()

				endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
				wantMessages := tc.variableMessages

				if sized {
					endpoint += "?bytes=8"
					wantMessages = tc.sizedMessages
				}

				beforeBytes, beforeMessages := metrics.ClientBytesReceived(), metrics.ClientWSSMessages()

				frames, err := StreamEntropy(ctx, endpoint)
				if err != nil {
					t.Fatal(err)
				}

				var received uint64

				for frame := range frames {
					matches := bytes.Equal(frame.Data, tc.data)
					frame.Release()

					if !matches {
						t.Fatal("accepted binary message changed")
					}

					received++
				}

				if ctx.Err() != nil {
					t.Fatalf("stream did not finish before the deadline: %v", ctx.Err())
				}

				wantBytes := wantMessages * uint64(len(tc.data))

				gotBytes, gotMessages := metrics.ClientBytesReceived()-beforeBytes, metrics.ClientWSSMessages()-beforeMessages
				if received != wantMessages || gotMessages != wantMessages || gotBytes != wantBytes {
					t.Fatalf("control/rejected data counted: delivered=%d messages=%d bytes=%d; want messages=%d bytes=%d", received, gotMessages, gotBytes, wantMessages, wantBytes)
				}
			})
		}
	}
}
