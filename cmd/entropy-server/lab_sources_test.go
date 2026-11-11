package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/lab"
)

func TestLabEightMiBSampleAndConfiguredLimits(t *testing.T) {
	const sampleBytes = 8 << 20

	payload := bytes.Repeat([]byte{0x5a}, sampleBytes)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer upstream.Close()

	registry, err := lab.New([]lab.Config{{ID: "large", Kind: "http", Endpoint: upstream.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()

	budget := newWebSocketBudget(1, 32<<20)

	server := httptest.NewServer(labStreamHandler(registry, budget, sampleBytes, false))
	defer server.Close()

	conn := budgetConnect(t, server, "/lab/stream?source=large&bytes="+strconv.Itoa(sampleBytes)+"&refresh=100")
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))

	typ, data, err := conn.ReadMessage()
	if err != nil || typ != websocket.BinaryMessage || !bytes.Equal(data, payload) {
		t.Fatalf("8 MiB source sample changed: type=%d bytes=%d error=%v", typ, len(data), err)
	}

	_ = conn.Close()

	waitBudgetActive(t, budget, 0)

	response := httptest.NewRecorder()
	labCatalogHandler(registry, budget, 256<<10, false)(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/lab/sources", nil))

	var catalog struct {
		MaxBytes int              `json:"max_bytes"`
		Sources  []lab.Descriptor `json:"sources"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &catalog); err != nil || catalog.MaxBytes != 256<<10 || len(catalog.Sources) != 1 || catalog.Sources[0].MaxBytes != 256<<10 {
		t.Fatal("catalog must advertise the configured effective ceiling", err, response.Body.String())
	}

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/lab/stream?source=large&bytes="+strconv.Itoa(sampleBytes+1), nil)
	if err != nil {
		t.Fatal(err)
	}

	result, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Body.Close()

	_, _ = io.Copy(io.Discard, result.Body)
	if result.StatusCode != http.StatusBadRequest || budget.usage().Active != 0 {
		t.Fatal("request bypassed the operator's byte limit", result.StatusCode)
	}
}

func TestLabAccessCatalogAndValidation(t *testing.T) {
	registry, err := lab.New([]lab.Config{{ID: "private", Name: "Device", Kind: "http", Endpoint: "http://127.0.0.1:9000/private?token=hidden"}})
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()

	budget := newWebSocketBudget(1, 4<<20)
	catalog := labCatalogHandler(registry, budget, lab.MaxBytes, false)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/lab/sources", nil)
	request.RemoteAddr = "203.0.113.1:1234"
	recorder := httptest.NewRecorder()
	catalog(recorder, request)

	var result struct {
		Allowed bool             `json:"allowed"`
		Sources []lab.Descriptor `json:"sources"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}

	if result.Allowed || len(result.Sources) != 1 || strings.Contains(recorder.Body.String(), "token") || strings.Contains(recorder.Body.String(), "endpoint") {
		t.Fatal("catalog access/endpoint disclosure regression")
	}

	request.Header.Set("X-Forwarded-For", "127.0.0.1")

	recorder = httptest.NewRecorder()
	labStreamHandler(registry, budget, lab.MaxBytes, false)(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatal("forwarded header bypassed loopback restriction")
	}

	for _, query := range []string{"source=missing", "source=private&source=private", "source=private&bytes=33554433", "source=private&refresh=0", "source=private&refresh=20&refresh=21", "source=private&bytes=8&bytes=9", "source=private&refesh=20", "source=private&bytes=%zz", "source=private&bytes=8;refresh=20", "source=private&preview=1"} {
		request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/lab/stream?"+query, nil)
		request.RemoteAddr = "127.0.0.1:1234"
		recorder = httptest.NewRecorder()
		labStreamHandler(registry, budget, lab.MaxBytes, false)(recorder, request)

		if recorder.Code != http.StatusBadRequest || budget.usage().Active != 0 {
			t.Fatalf("invalid query reached source admission: %s %d", query, recorder.Code)
		}
	}
}

func TestLabCameraHandshakeAndShutdown(t *testing.T) {
	registry, err := lab.New([]lab.Config{{ID: "camera", Kind: "rtsp", Endpoint: "rtsp://127.0.0.1:8554/live", Width: 160, Height: 120}})
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()

	budget := newWebSocketBudget(1, 32<<20)
	testServer := httptest.NewUnstartedServer(labStreamHandler(registry, budget, lab.MaxBytes, false))
	server := newManagedServer(t.Context(), testServer.Config)

	testServer.Start()
	defer testServer.Close()

	for _, payload := range []string{"null", `{"username":42}`, `{"username":"","unknown":true}`} {
		conn := budgetConnect(t, testServer, "/lab/stream?source=camera&bytes=32&refresh=20")
		if err := conn.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
			t.Fatal(err)
		}

		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		if _, _, err := conn.ReadMessage(); !websocket.IsCloseError(err, websocket.ClosePolicyViolation) {
			t.Fatalf("invalid credential frame accepted: %v", err)
		}

		_ = conn.Close()

		waitBudgetActive(t, budget, 0)
	}

	conn := budgetConnect(t, testServer, "/lab/stream?source=camera&bytes=32&refresh=20")
	defer conn.Close()

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	waitBudgetActive(t, budget, 0)
}

func TestLabEntropyPeerAndSelfConnections(t *testing.T) {
	for _, self := range []bool{false, true} {
		for _, kind := range []string{"entropy-http", "entropy-ws"} {
			t.Run(kind+"/self="+boolLabel(self), func(t *testing.T) {
				generator := observableTestDRBG(t)
				upstreamBudget := newWebSocketBudget(8, 16<<20)
				mux := http.NewServeMux()
				mux.HandleFunc("/stream", wsBinaryHandler(generator, 20, 64, 4096, upstreamBudget))
				mux.HandleFunc("/v1/data/random", randomHandler(generator, 4096, newHTTPRequestBudget(8, 16<<20)))

				peer := httptest.NewServer(mux)
				defer peer.Close()

				server := peer

				bridgeMux := mux
				if !self {
					bridgeMux = http.NewServeMux()

					server = httptest.NewServer(bridgeMux)
					defer server.Close()
				}

				registry, err := lab.New([]lab.Config{{ID: "peer", Kind: kind, Endpoint: peer.URL}})
				if err != nil {
					t.Fatal(err)
				}
				defer registry.Close()

				budget := newWebSocketBudget(1, 4<<20)
				handler := labStreamHandler(registry, budget, 4096, false)
				bridgeMux.HandleFunc("/lab/stream", handler)

				conn := budgetConnect(t, server, "/lab/stream?source=peer&bytes=64&refresh=20")

				_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
				for range 2 {
					message, data, err := conn.ReadMessage()
					if err != nil || message != websocket.BinaryMessage || len(data) != 64 {
						t.Fatalf("peer/self stream failed: %v", err)
					}
				}

				budgetReject(t, server, "/lab/stream?source=peer&bytes=64&refresh=20", http.StatusServiceUnavailable)

				_ = conn.Close()

				waitBudgetActive(t, budget, 0)
				waitBudgetActive(t, upstreamBudget, 0)
			})
		}
	}
}

func TestLabShutdownCancelsBlockedUpstream(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})

	peer := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(stopped) }))
	defer peer.Close()

	registry, err := lab.New([]lab.Config{{ID: "stalled", Kind: "http", Endpoint: peer.URL, TimeoutMs: 60000}})
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()

	budget := newWebSocketBudget(1, 4<<20)
	testServer := httptest.NewUnstartedServer(labStreamHandler(registry, budget, 4096, false))
	server := newManagedServer(t.Context(), testServer.Config)

	testServer.Start()
	defer testServer.Close()

	conn := budgetConnect(t, testServer, "/lab/stream?source=stalled&bytes=64&refresh=20")
	defer conn.Close()

	<-started

	shutdownCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}

	waitBudgetActive(t, budget, 0)

	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("upstream HTTP request survived shutdown")
	}
}
