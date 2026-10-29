package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestDiagnosticWSCancellation(t *testing.T) {
	for _, handshake := range []bool{false, true} {
		for _, deadline := range []bool{false, true} {
			t.Run(testCancellationLabel(handshake, deadline), func(t *testing.T) {
				ready := make(chan struct{})
				peerClosed := make(chan struct{})

				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					defer close(peerClosed)

					if handshake {
						close(ready)
						<-r.Context().Done()

						return
					}

					upgrader := websocket.Upgrader{}

					conn, err := upgrader.Upgrade(w, r, nil)
					if err != nil {
						return
					}
					defer conn.Close()

					close(ready)

					_, _, _ = conn.ReadMessage() // Peer sends no data.
				}))
				defer server.Close()

				ctx, cancel := context.WithCancel(t.Context())
				want := context.Canceled

				if deadline {
					cancel()
					ctx, cancel = context.WithTimeout(t.Context(), 100*time.Millisecond)
					want = context.DeadlineExceeded
				}

				defer cancel()

				done := make(chan error, 1)

				go func() { done <- TestEntropyWS(ctx, "ws"+strings.TrimPrefix(server.URL, "http")) }()

				<-ready

				if !deadline {
					cancel()
				}

				select {
				case err := <-done:
					if !errors.Is(err, want) {
						t.Fatalf("got %v, want %v", err, want)
					}
				case <-time.After(time.Second):
					t.Fatal("diagnostic ignored cancellation")
				}

				select {
				case <-peerClosed:
				case <-time.After(time.Second):
					t.Fatal("client left the connection open")
				}
			})
		}
	}
}

func testCancellationLabel(handshake, deadline bool) string {
	label := "read"
	if handshake {
		label = "handshake"
	}

	if deadline {
		return label + "/deadline"
	}

	return label + "/cancel"
}

func TestHTTPBodyCancellation(t *testing.T) {
	for _, diagnostic := range []bool{false, true} {
		label := "fetch"
		if diagnostic {
			label = "diagnostic"
		}

		t.Run(label, func(t *testing.T) {
			started := make(chan struct{})

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Length", "64")
				_, _ = w.Write([]byte{1})
				_ = http.NewResponseController(w).Flush()

				close(started)
				<-r.Context().Done()
			}))
			defer server.Close()

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			done := make(chan error, 1)

			go func() {
				if diagnostic {
					done <- TestEntropyHTTP(ctx, server.URL)
				} else {
					_, err := FetchEntropy(ctx, server.URL, 64)
					done <- err
				}
			}()

			<-started
			cancel()

			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("got %v, want cancellation", err)
				}
			case <-time.After(time.Second):
				t.Fatal("HTTP read ignored cancellation")
			}
		})
	}
}

func TestStreamCancellationWithFullQueue(t *testing.T) {
	sent := make(chan struct{})
	peerClosed := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		defer close(peerClosed)

		for range 32 {
			if err := conn.WriteMessage(websocket.BinaryMessage, []byte{1}); err != nil {
				return
			}
		}

		close(sent)

		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	frames, err := StreamEntropy(ctx, "ws"+strings.TrimPrefix(server.URL, "http"))
	if err != nil {
		t.Fatal(err)
	}

	<-sent

	deadline := time.Now().Add(time.Second)
	for len(frames) < cap(frames) {
		if time.Now().After(deadline) {
			t.Fatal("receive queue did not fill")
		}

		time.Sleep(time.Millisecond)
	}

	cancel()

	select {
	case <-peerClosed:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not close a backpressured stream")
	}

	done := make(chan struct{})

	go func() {
		for range frames {
		}

		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream goroutine did not exit")
	}
}
