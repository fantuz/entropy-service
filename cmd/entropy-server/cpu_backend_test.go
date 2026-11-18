package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/drbg"
	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/rdrand"
)

func TestCPUBackendHTTPAndWebSocket(t *testing.T) {
	if !rdrand.Available() {
		t.Skip("native amd64 RDRAND not available")
	}

	root, err := drbg.NewDRBGWithAlgorithm(nil, drbg.RDRAND)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	if err := root.ConfigureReseedPolicy(drbg.ReseedStop, time.Minute); err != nil {
		t.Fatal(err)
	}

	root.SetMetadata("test", "policy-source", "", time.Second, 512)

	ws, requests := newWebSocketBudget(2, 16<<20), newHTTPRequestBudget(2, 16<<20)
	mux := testGenerationMux(&root, ws, requests)
	mux.HandleFunc("/ready", healthHandler(&root, time.Minute))
	mux.HandleFunc("/metrics", canonicalMetricsHandler(&root, time.Minute, ws, requests))

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/data/random?bytes=65537", nil))

	if response.Code != http.StatusOK || response.Body.Len() != 65537 || response.Header().Get("X-Rng-Output-Mode") != "direct-cpu" || response.Header().Get("X-Rng-Source") != "policy-source" {
		t.Fatalf("direct HTTP response: code=%d, length=%d, headers=%v", response.Code, response.Body.Len(), response.Header())
	}

	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ready", nil))

	var health HealthInfo
	if err := json.Unmarshal(response.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}

	if response.Code != http.StatusOK || !health.Ready || health.OutputMode != "direct-cpu" || health.BackendError != "" {
		t.Fatalf("health: %+v", health)
	}

	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))

	values, kinds := parseMetricSamples(t, response.Body.String())
	if values["rng_backend_failures_total"] != 0 || kinds["rng_backend_failures_total"] != "counter" {
		t.Fatal("CPU fault metric missing or not a counter")
	}

	server := httptest.NewServer(mux)
	defer server.Close()

	conn, handshake, err := websocket.DefaultDialer.DialContext(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http")+"/stream?bytes=33", nil)
	if err != nil {
		t.Fatal(err)
	}

	if handshake != nil && handshake.Body != nil {
		defer handshake.Body.Close()
	}

	defer conn.Close()

	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}

	kind, data, err := conn.ReadMessage()
	if err != nil || kind != websocket.BinaryMessage || len(data) != 33 {
		t.Fatalf("CPU WebSocket output: kind=%d, length=%d, error=%v", kind, len(data), err)
	}
}

func TestCPUFailureReportingAndReadiness(t *testing.T) {
	meta := drbg.Metadata{BackendError: drbg.ErrCPUBackendFailed.Error()}
	if reseedReady(meta, time.Hour) {
		t.Fatal("fresh source policy hid a backend failure")
	}

	response := httptest.NewRecorder()
	response.Header().Set("Content-Length", "65536")
	writeGenerationError(response, drbg.ErrCPUBackendFailed, "ignored fallback")

	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Content-Length") != "" || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), "restart required") {
		t.Fatalf("hardware fault response: %d, %v, %q", response.Code, response.Header(), response.Body.String())
	}
}

type cpuFaultReader struct {
	reads   int
	partial bool
}

func (r *cpuFaultReader) Read(p []byte) (int, error) {
	r.reads++
	if r.partial && r.reads == 1 {
		clear(p)
		return len(p), nil
	}

	return 0, drbg.ErrCPUBackendFailed
}

func TestCPUFailureDuringHTTPStream(t *testing.T) {
	for _, partial := range []bool{false, true} {
		response := httptest.NewRecorder()
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/data/random", nil)

		err := streamEntropyResponse(response, request, &cpuFaultReader{partial: partial}, 65536)
		if err == nil {
			t.Fatal("expected interrupted CPU stream")
		}

		if !partial {
			if response.Code != http.StatusServiceUnavailable || response.Header().Get("Content-Length") != "" {
				t.Fatal("first-read failure was not 503")
			}
		} else if response.Code != http.StatusOK || response.Body.Len() != 32768 || response.Header().Get("Content-Length") != "65536" || strings.Contains(response.Body.String(), "restart") {
			t.Fatal("mid-stream failure was not detectably truncated or contaminated the binary body")
		}
	}
}
