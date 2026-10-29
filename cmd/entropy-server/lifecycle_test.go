package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/drbg"
)

func TestWebSocketShutdown(t *testing.T) {
	for _, secure := range []bool{false, true} {
		for _, cancelParent := range []bool{false, true} {
			for _, path := range []string{"/stream", "/bytes", "/colors", "/words"} {
				t.Run(strings.Join([]string{path, boolLabel(secure), boolLabel(cancelParent)}, "/"), func(t *testing.T) {
					parent, err := drbg.NewDRBG(bytes.Repeat([]byte{0x42}, 64))
					if err != nil {
						t.Fatal(err)
					}
					defer drbg.DecreaseActiveInstances(1)

					baseline := drbg.ActiveInstances()
					budget := newWebSocketBudget(128, 1<<30)
					handler := wsBytesHandler(&parent, 5, 64, budget)

					switch path {
					case "/stream":
						handler = wsBinaryHandler(&parent, 5, 64, 64, budget)
					case "/words":
						handler = wsWordsHandler(&parent, 3, 5, budget)
					}

					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()

					testServer := httptest.NewUnstartedServer(handler)
					server := newManagedServer(ctx, testServer.Config)

					if secure {
						testServer.StartTLS()
					} else {
						testServer.Start()
					}

					defer testServer.Close()

					dialer := *websocket.DefaultDialer

					transport, ok := testServer.Client().Transport.(*http.Transport)
					if !ok {
						t.Fatal("test transport is not HTTP")
					}

					dialer.TLSClientConfig = transport.TLSClientConfig

					conn, response, err := dialer.DialContext(t.Context(), "ws"+strings.TrimPrefix(testServer.URL, "http")+path, nil)
					if response != nil && response.Body != nil {
						_ = response.Body.Close()
					}

					if err != nil {
						t.Fatal(err)
					}

					defer conn.Close()

					_ = conn.SetReadDeadline(time.Now().Add(time.Second))
					if _, _, err := conn.ReadMessage(); err != nil {
						t.Fatal(err)
					}

					if cancelParent {
						cancel()
					}

					shutdownCtx, stop := context.WithTimeout(t.Context(), time.Second)
					defer stop()

					if err := server.Shutdown(shutdownCtx); err != nil {
						t.Fatal(err)
					}

					if usage := budget.usage(); usage.Active != 0 || usage.Reserved != 0 {
						t.Fatalf("shutdown did not release reservations: %+v", usage)
					}

					if got := drbg.ActiveInstances(); got != baseline {
						t.Fatalf("shutdown returned before child cleanup: got %d, want %d", got, baseline)
					}

					// Frames queued before cancellation may still be in the TCP buffer.
					for {
						if _, _, err := conn.ReadMessage(); err != nil {
							break
						}
					}
				})
			}
		}
	}
}

func boolLabel(value bool) string {
	if value {
		return "true"
	}

	return "false"
}

func TestShutdownJoinsHijackedHandlersAndRejectsNewWork(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	testServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		ctx, cleanup := websocketContext(r.Context(), conn)
		defer cleanup()

		close(started)
		<-ctx.Done()
		close(canceled)
		<-release
	}))
	server := newManagedServer(t.Context(), testServer.Config)

	testServer.Start()
	defer testServer.Close()
	defer func() {
		close(release)

		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	}()

	conn, response, err := websocket.DefaultDialer.DialContext(t.Context(), "ws"+strings.TrimPrefix(testServer.URL, "http"), nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err != nil {
		t.Fatal(err)
	}

	defer conn.Close()

	<-started

	shutdownCtx, stop := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer stop()

	if err := server.Shutdown(shutdownCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected bounded wait for blocked handler, got %v", err)
	}

	select {
	case <-canceled:
	default:
		t.Fatal("request context did not observe shutdown")
	}

	recorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("late request returned %d", recorder.Code)
	}
}

func TestShutdownInterruptsBlockedWebSocketWrite(t *testing.T) {
	writing := make(chan struct{})
	finished := make(chan struct{})
	testServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		_, cleanup := websocketContext(r.Context(), conn)

		defer close(finished)
		defer cleanup()

		close(writing)

		_ = conn.WriteMessage(websocket.BinaryMessage, make([]byte, 16<<20))
	}))
	server := newManagedServer(t.Context(), testServer.Config)

	testServer.Start()
	defer testServer.Close()

	conn, response, err := websocket.DefaultDialer.DialContext(t.Context(), "ws"+strings.TrimPrefix(testServer.URL, "http"), nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err != nil {
		t.Fatal(err)
	}

	defer conn.Close()

	<-writing

	// Read the frame header but leave its 16 MiB body unread, so the write
	// has entered the transport and cannot finish before cancellation.
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := conn.NextReader(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-finished:
		t.Fatal("expected a backpressured write")
	default:
	}

	shutdownCtx, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()

	if err := server.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}

	select {
	case <-finished:
	default:
		t.Fatal("shutdown returned before writer and reader cleanup")
	}
}
