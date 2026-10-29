package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/metrics"
)

func TestHealthFailureStalenessAndRecovery(t *testing.T) {
	d := observableTestDRBG(t)
	handler := healthHandler(d, time.Second)
	check := func(wantCode int, wantReady bool, failures uint64) HealthInfo {
		t.Helper()

		w := httptest.NewRecorder()
		handler(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health-2", nil))

		var health HealthInfo
		if err := json.Unmarshal(w.Body.Bytes(), &health); err != nil {
			t.Fatal(err)
		}

		if w.Code != wantCode || health.Ready != wantReady || health.ReseedFailuresTotal != failures {
			t.Fatalf("unexpected health: HTTP %d, %+v", w.Code, health)
		}

		if w.Header().Get("Cache-Control") != "no-store" || health.ReseedSizeBits != 2048 ||
			w.Header().Get("X-Rng-Reseed-Size-Bits") != "2048" || health.EntropyBufferEnabled ||
			health.EntropyBufferedBytes != 0 || health.EntropyBufferedPCT != 0 {
			t.Fatal("health metadata, bit units or disabled-buffer reporting is incorrect")
		}

		return health
	}

	check(http.StatusOK, true, 0)
	d.RecordReseedFailure(io.ErrUnexpectedEOF)

	failed := check(http.StatusServiceUnavailable, false, 1)
	if failed.Status != "degraded" || failed.LastReseedError != io.ErrUnexpectedEOF.Error() {
		t.Fatal("health must identify the current failure")
	}

	// Readiness reports degradation; it does not disable existing DRBG output.
	if _, err := d.Read(make([]byte, 64)); err != nil {
		t.Fatal(err)
	}

	live := httptest.NewRecorder()
	livenessHandler(live, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/live", nil))

	if live.Code != http.StatusOK {
		t.Fatal("seed-source failure must not fail process liveness")
	}

	if err := d.Reseed(make([]byte, 64)); err != nil {
		t.Fatal(err)
	}

	recovered := check(http.StatusOK, true, 1)
	if recovered.LastReseedError != "" || recovered.LastReseedAt.IsZero() {
		t.Fatal("recovered health must clear the current error and expose the seed timestamp")
	}

	d.Reseeded = time.Now().Add(-time.Hour)

	stale := check(http.StatusServiceUnavailable, false, 1)
	if stale.ReseedAgeMs < time.Hour.Milliseconds() || stale.MaxReseedAgeMs != 1000 {
		t.Fatal("stale seed was reported as ready")
	}
}

func TestMetricsNamesTypesUnitsAndReadiness(t *testing.T) {
	d := observableTestDRBG(t)
	before := metrics.BytesGenerated()

	if _, err := d.Read(make([]byte, 37)); err != nil {
		t.Fatal(err)
	}

	d.RecordReseedFailure(io.EOF)

	w := httptest.NewRecorder()
	metricsHandler(d, time.Second, newWebSocketBudget(128, 1<<30), newHTTPRequestBudget(128, 512<<20))(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics-2", nil))

	if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain; version=0.0.4") {
		t.Fatal("metrics must declare Prometheus text format")
	}

	values, kinds := parseMetricSamples(t, w.Body.String())
	if values["rng_bytes_generated_total"] < float64(before+37) ||
		values["rng_mb_generated_total"] != float64(uint64(values["rng_bytes_generated_total"])/(1<<20)) {
		t.Fatal("canonical byte count or compatibility MiB count has incorrect units")
	}

	if values["rng_ready"] != 0 || values["rng_reseed_failures_total"] != 1 || kinds["rng_reseed_failures_total"] != "counter" {
		t.Fatal("failure state was not reflected in metrics")
	}

	if kinds["drbg_instance_count"] != "gauge" || kinds["wss_payloads_total"] != "counter" ||
		values["wss_payloads"] != values["wss_payloads_total"] || kinds["wss_payloads"] != "untyped" {
		t.Fatal("instance gauge or WebSocket counter aliases are incorrect")
	}

	for name, value := range values {
		if strings.Contains(name, "buffer") && value != 0 {
			t.Fatalf("removed buffer metric %s reports %v", name, value)
		}
	}

	if err := d.Reseed(make([]byte, 64)); err != nil {
		t.Fatal(err)
	}

	w = httptest.NewRecorder()
	metricsHandler(d, time.Second, newWebSocketBudget(128, 1<<30), newHTTPRequestBudget(128, 512<<20))(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics-2", nil))

	values, _ = parseMetricSamples(t, w.Body.String())
	if values["rng_ready"] != 1 || values["rng_generator_reseeds_total"] != 1 || values["rng_reseed_failures_total"] != 1 {
		t.Fatal("metrics did not show successful recovery with cumulative failures preserved")
	}
}

// Validate the actual exposition: each sample has matching HELP and TYPE names,
// each declaration has a sample, and every emitted value is numeric.
func parseMetricSamples(t *testing.T, body string) (map[string]float64, map[string]string) {
	t.Helper()

	help, kinds := map[string]bool{}, map[string]string{}
	values := map[string]float64{}

	for line := range strings.SplitSeq(body, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		if strings.HasPrefix(line, "# HELP ") {
			help[fields[2]] = true
			continue
		}

		if strings.HasPrefix(line, "# TYPE ") {
			kinds[fields[2]] = fields[3]
			continue
		}

		if len(fields) != 2 {
			t.Fatalf("invalid metric sample: %q", line)
		}

		if _, exists := values[fields[0]]; exists {
			t.Fatalf("duplicate metric sample: %q", line)
		}

		value, err := strconv.ParseFloat(fields[1], 64)
		if err != nil || !help[fields[0]] || kinds[fields[0]] == "" {
			t.Fatalf("sample without numeric value or matching HELP/TYPE: %q", line)
		}

		values[fields[0]] = value
	}

	if len(values) != len(help) || len(values) != len(kinds) {
		t.Fatal("metric declarations and samples do not match")
	}

	return values, kinds
}

func TestCountRequestsIncludesErrorsAndPreservesWebSockets(t *testing.T) {
	d := observableTestDRBG(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/random", randomBytesHandler(d, 64, 20, false, newHTTPRequestBudget(128, 512<<20)))
	mux.HandleFunc("/live", livenessHandler)
	counted := countRequests(mux)
	before := metrics.NumHTTPRequests()

	for _, path := range []string{"/random?bytes=64", "/random?bytes=-1", "/missing", "/live"} {
		counted.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
	}

	if metrics.NumHTTPRequests()-before != 4 {
		t.Fatal("each accepted or rejected request must be counted once")
	}

	done := make(chan struct{})
	handler := wsBinaryHandler(d, 1, 64, 64, newWebSocketBudget(128, 1<<30))

	server := httptest.NewServer(countRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)

		handler(w, r)
	})))
	defer server.Close()

	conn, response, err := websocket.DefaultDialer.DialContext(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err != nil {
		t.Fatal(err)
	}

	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatal(err)
	}

	_ = conn.Close()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("WebSocket did not clean up")
	}

	if metrics.NumHTTPRequests()-before != 5 {
		t.Fatal("WebSocket handshake must count as one HTTP request")
	}
}
