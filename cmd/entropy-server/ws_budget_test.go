package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/drbg"
)

func budgetTestServer(t *testing.T, budget *webSocketBudget, secure bool) *httptest.Server {
	t.Helper()

	parent, err := drbg.NewDRBG(make([]byte, 64))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { drbg.DecreaseActiveInstances(1) })

	mux := http.NewServeMux()
	mux.HandleFunc("/stream", wsBinaryHandler(&parent, 60000, 64, 1<<25, budget))
	mux.HandleFunc("/bytes", wsBytesHandler(&parent, 60000, 1<<25, budget))
	mux.HandleFunc("/colors", wsBytesHandler(&parent, 60000, 1<<25, budget))
	mux.HandleFunc("/words", wsWordsHandler(&parent, 20, 60000, budget))
	mux.HandleFunc("/metrics", metricsHandler(&parent, time.Hour, budget, newHTTPRequestBudget(128, 512<<20)))

	server := httptest.NewUnstartedServer(mux)

	managed := newManagedServer(t.Context(), server.Config)
	if secure {
		server.StartTLS()
	} else {
		server.Start()
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		if err := managed.Shutdown(ctx); err != nil {
			t.Error(err)
		}

		server.Close()
		waitBudgetActive(t, budget, 0)
	})

	return server
}

func budgetDial(server *httptest.Server, path string) (*websocket.Conn, *http.Response, error) {
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = 2 * time.Second

	dialer.WriteBufferSize = 256 // Exercise continuation frames on larger input.
	if transport, ok := server.Client().Transport.(*http.Transport); ok {
		dialer.TLSClientConfig = transport.TLSClientConfig
	}

	return dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+path, nil)
}

func budgetConnect(t *testing.T, server *httptest.Server, path string) *websocket.Conn {
	t.Helper()

	conn, response, err := budgetDial(server, path)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

func budgetReject(t *testing.T, server *httptest.Server, path string, status int) {
	t.Helper()

	conn, response, err := budgetDial(server, path)
	if conn != nil {
		_ = conn.Close()
	}

	if response == nil {
		t.Fatalf("no rejection response: %v", err)
	}
	defer response.Body.Close()

	if err == nil || response.StatusCode != status {
		t.Fatalf("got status %d and error %v, want %d", response.StatusCode, err, status)
	}

	if status == http.StatusServiceUnavailable && response.Header.Get("Retry-After") != "1" {
		t.Fatal("transient overload must include Retry-After")
	}
}

func waitBudgetActive(t *testing.T, budget *webSocketBudget, want int) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)

	for {
		usage := budget.usage()
		if usage.Active == want {
			if want == 0 && usage.Reserved != 0 {
				t.Fatalf("zero connections retained %d reserved bytes", usage.Reserved)
			}

			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("budget did not release: %+v, want active=%d", usage, want)
		}

		time.Sleep(time.Millisecond)
	}
}

func TestWebSocketInboundMessageLimit(t *testing.T) {
	for _, path := range []string{"/stream?bytes=64", "/bytes?bytes=64", "/colors?bytes=64", "/words?words=3"} {
		for _, size := range []int{wsInboundLimit + 1, 128 << 10} {
			t.Run(path+"/"+strconv.Itoa(size), func(t *testing.T) {
				budget := newWebSocketBudget(4, 1<<20)
				server := budgetTestServer(t, budget, false)
				conn := budgetConnect(t, server, path)
				pong := make(chan struct{})
				errCh := make(chan error, 1)

				conn.SetPongHandler(func(string) error { close(pong); return nil })

				go func() {
					_, _, err := conn.ReadMessage()
					errCh <- err
				}()

				// Separate messages at the limit must not accumulate toward it.
				for _, kind := range []int{websocket.BinaryMessage, websocket.TextMessage} {
					if err := conn.WriteMessage(kind, make([]byte, wsInboundLimit)); err != nil {
						t.Fatal(err)
					}
				}

				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second)); err != nil {
					t.Fatal(err)
				}

				select {
				case <-pong:
				case <-time.After(2 * time.Second):
					t.Fatal("legal messages or ping were rejected")
				}

				// A small write buffer fragments this across continuation frames.
				kind := websocket.BinaryMessage
				if size > wsInboundLimit+1 {
					kind = websocket.TextMessage
				}

				_ = conn.WriteMessage(kind, make([]byte, size))

				select {
				case err := <-errCh:
					if !websocket.IsCloseError(err, websocket.CloseMessageTooBig) {
						t.Fatalf("oversized message did not receive close 1009: %v", err)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("oversized message was accepted")
				}

				waitBudgetActive(t, budget, 0)
			})
		}
	}
}

func TestWebSocketConnectionBudgetSharedAcrossListeners(t *testing.T) {
	budget := newWebSocketBudget(2, 64<<20)
	httpServer := budgetTestServer(t, budget, false)
	httpsServer := budgetTestServer(t, budget, true)
	first := budgetConnect(t, httpServer, "/stream?bytes=64")
	_ = budgetConnect(t, httpsServer, "/words?words=3")
	budgetReject(t, httpsServer, "/colors?bytes=64", http.StatusServiceUnavailable)

	_ = first.Close()

	waitBudgetActive(t, budget, 1)
	_ = budgetConnect(t, httpsServer, "/colors?bytes=64")

	if usage := budget.usage(); usage.Active != 2 || usage.RejectedConnections != 1 {
		t.Fatalf("shared connection cap or recovery failed: %+v", usage)
	}
}

func TestWebSocketMemoryBudgetAndMetrics(t *testing.T) {
	budget := newWebSocketBudget(8, 2*wsByteReservation(64<<10, false))
	server := budgetTestServer(t, budget, false)
	first := budgetConnect(t, server, "/stream?bytes=65536")
	_ = budgetConnect(t, server, "/stream?bytes=65536")
	budgetReject(t, server, "/stream?bytes=65536", http.StatusServiceUnavailable)

	// Equal raw sizes have very different working sets on JSON endpoints.
	budgetReject(t, server, "/bytes?bytes=65536", http.StatusRequestEntityTooLarge)

	_ = first.Close()

	waitBudgetActive(t, budget, 1)
	_ = budgetConnect(t, server, "/bytes?bytes=1024")

	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()
	server.Config.Handler.ServeHTTP(response, request)
	values, kinds := parseMetricSamples(t, response.Body.String())

	usage := budget.usage()
	if values["ws_connections_active"] != 2 || values["ws_connections_limit"] != 8 ||
		values["ws_memory_reserved_bytes"] != float64(usage.Reserved) || values["ws_memory_budget_bytes"] != float64(usage.MaxMemory) ||
		values["ws_rejected_memory_total"] != 1 || values["ws_rejected_size_total"] != 1 ||
		kinds["ws_memory_reserved_bytes"] != "gauge" || kinds["ws_rejected_memory_total"] != "counter" {
		t.Fatalf("incorrect WebSocket admission metrics: %s", response.Body.String())
	}
}

func TestWebSocketFailedUpgradeReleasesBudget(t *testing.T) {
	budget := newWebSocketBudget(1, 1<<20)

	server := budgetTestServer(t, budget, false)
	for range 16 {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/stream?bytes=64", nil)
		if err != nil {
			t.Fatal(err)
		}

		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}

		_, _ = io.Copy(io.Discard, response.Body)

		_ = response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("bad upgrade returned %d", response.StatusCode)
		}

		waitBudgetActive(t, budget, 0)
	}

	_ = budgetConnect(t, server, "/stream?bytes=64")
}

func TestWebSocketConcurrentAdmission(t *testing.T) {
	for _, memoryLimited := range []bool{false, true} {
		t.Run(boolLabel(memoryLimited), func(t *testing.T) {
			budget := newWebSocketBudget(4, 64<<20)
			if memoryLimited {
				budget = newWebSocketBudget(128, 4*wsByteReservation(64, false))
			}

			server := budgetTestServer(t, budget, false)
			connections := make(chan *websocket.Conn, 64)
			failures := make(chan error, 64)

			var attempts sync.WaitGroup

			for range 64 {
				attempts.Add(1)

				go func() {
					defer attempts.Done()

					conn, response, err := budgetDial(server, "/stream?bytes=64")
					if response != nil && response.Body != nil {
						_ = response.Body.Close()
					}

					if err == nil {
						connections <- conn
					} else if response == nil || response.StatusCode != http.StatusServiceUnavailable {
						failures <- fmt.Errorf("unexpected dial failure: %w", err)
					}
				}()
			}

			attempts.Wait()
			close(connections)
			close(failures)

			for err := range failures {
				t.Error(err)
			}

			usage := budget.usage()
			if len(connections) != 4 || usage.Active != 4 || usage.Reserved > usage.MaxMemory ||
				usage.RejectedConnections+usage.RejectedMemory != 60 {
				t.Errorf("concurrent admission exceeded budget: %+v, connections=%d", usage, len(connections))
			}

			for conn := range connections {
				_ = conn.Close()
			}

			waitBudgetActive(t, budget, 0)
			_ = budgetConnect(t, server, "/stream?bytes=64")
		})
	}
}

func TestWebSocketWordReservationCannotOverflow(t *testing.T) {
	maxWords := int(^uint(0) >> 1)
	budget := newWebSocketBudget(128, 1<<30)
	response := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/words?words="+strconv.Itoa(maxWords), nil)
	wsWordsHandler(nil, maxWords, 60000, budget)(response, request)

	if response.Code != http.StatusRequestEntityTooLarge || budget.usage().Reserved != 0 {
		t.Fatalf("large word request bypassed accounting: %d, %+v", response.Code, budget.usage())
	}
}
