package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/config"
	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/drbg"
	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/metrics"
)

func testGenerationMux(root *drbg.DRBG, ws *webSocketBudget, requests *httpRequestBudget) *http.ServeMux {
	mux := http.NewServeMux()
	registerGenerationRoutes(mux, root, &config.Config{
		MaxBytes: 1 << 25, MaxWords: 3, PageRefreshMs: 5000,
		RefreshRateMs: 1, RefreshColorMs: 1,
	}, ws, requests)

	return mux
}

func TestReseedPolicyAllProductionGenerationRoutes(t *testing.T) {
	root := observableTestDRBG(t)
	if err := root.ConfigureReseedPolicy(drbg.ReseedStop, time.Hour); err != nil {
		t.Fatal(err)
	}

	ws, requests := newWebSocketBudget(8, 64<<20), newHTTPRequestBudget(8, 64<<20)
	mux := testGenerationMux(root, ws, requests)
	paths := []string{
		"/v1/data/random?bytes=64", "/v1/data/test?bytes=64",
		"/v1/meta/random?words=2", "/paroleparoleparole?words=2",
		"/v1/image/random?size=64", "/v1/image/heatmap?size=64",
		"/v1/qr/random", "/sound?duration_ms=250",
		"/stream", "/bytes", "/colors", "/words",
	}

	root.RecordReseedFailure(io.ErrUnexpectedEOF)

	before := metrics.BytesGenerated()

	for _, path := range paths {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))

		if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") != "1" || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("unhealthy route %s: code=%d headers=%v", path, response.Code, response.Header())
		}
	}

	if metrics.BytesGenerated() != before || ws.usage().Active != 0 || requests.usage().Active != 0 {
		t.Fatal("denied routes generated output or consumed reservations")
	}

	if err := root.Reseed(make([]byte, 64)); err != nil {
		t.Fatal(err)
	}

	for _, path := range paths[:8] {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))

		if response.Code != http.StatusOK || response.Body.Len() == 0 {
			t.Fatalf("route %s did not recover: code=%d body=%q", path, response.Code, response.Body.String())
		}
	}
}

type failingReseedWriter struct {
	http.ResponseWriter

	root   *drbg.DRBG
	writes int
}

func (w *failingReseedWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)

	w.writes++
	if w.writes == 1 {
		w.root.RecordReseedFailure(io.EOF)
	}

	return n, err
}

func TestReseedFailureInterruptsHTTPBetweenChunks(t *testing.T) {
	for _, path := range []string{"/v1/data/random", "/v1/data/test"} {
		t.Run(path, func(t *testing.T) {
			root := observableTestDRBG(t)
			if err := root.ConfigureReseedPolicy(drbg.ReseedStop, time.Hour); err != nil {
				t.Fatal(err)
			}

			requests := newHTTPRequestBudget(8, 64<<20)
			mux := testGenerationMux(root, newWebSocketBudget(8, 64<<20), requests)
			recorder := httptest.NewRecorder()
			response := &failingReseedWriter{ResponseWriter: recorder, root: root}
			mux.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path+"?bytes=65536", nil))

			if recorder.Code != http.StatusOK || response.writes != 1 || recorder.Body.Len() != 32768 || response.Header().Get("Content-Length") != "65536" {
				t.Fatalf("partial transfer was not stopped: code=%d writes=%d length=%d", recorder.Code, response.writes, recorder.Body.Len())
			}

			if bytes.Contains(recorder.Body.Bytes(), []byte("generation paused")) || requests.usage().Active != 0 || requests.usage().Reserved != 0 {
				t.Fatal("partial binary response was contaminated or leaked its reservation")
			}
		})
	}
}

func TestReseedFailureProducesDetectablyTruncatedHTTPBody(t *testing.T) {
	root := observableTestDRBG(t)
	if err := root.ConfigureReseedPolicy(drbg.ReseedStop, time.Hour); err != nil {
		t.Fatal(err)
	}

	handler := randomBytesHandler(root, 1<<25, 3, false, newHTTPRequestBudget(8, 64<<20))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler(&failingReseedWriter{ResponseWriter: w, root: root}, r)
	}))
	defer server.Close()

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"?bytes=65536", nil)
	if err != nil {
		t.Fatal(err)
	}

	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	data, err := io.ReadAll(response.Body)
	if !errors.Is(err, io.ErrUnexpectedEOF) || len(data) != 32768 || response.ContentLength != 65536 {
		t.Fatalf("partial sample looked complete: error=%v bytes=%d expected=%d", err, len(data), response.ContentLength)
	}
}

func TestReseedTelemetryDistinguishesPolicyAndHealth(t *testing.T) {
	for _, policy := range []drbg.ReseedFailurePolicy{drbg.ReseedStop, drbg.ReseedContinue} {
		for _, state := range []string{"fresh", "failed", "stale"} {
			t.Run(string(policy)+state, func(t *testing.T) {
				root := observableTestDRBG(t)
				if state == "stale" {
					// No concurrent reader exists yet; configure the aged fixture before serving.
					root.Reseeded = time.Now().Add(-2 * time.Hour)
				}

				if err := root.ConfigureReseedPolicy(policy, time.Hour); err != nil {
					t.Fatal(err)
				}

				if state == "failed" {
					root.RecordReseedFailure(io.EOF)
				}

				response := httptest.NewRecorder()
				healthHandler(root, time.Hour)(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ready", nil))

				var health HealthInfo
				if err := json.Unmarshal(response.Body.Bytes(), &health); err != nil {
					t.Fatal(err)
				}

				allowed := state == "fresh" || policy == drbg.ReseedContinue
				if health.SeedState != state || health.Ready != (state == "fresh") || health.GenerationAllowed != allowed || health.ReseedFailurePolicy != policy {
					t.Fatalf("incorrect health snapshot: %+v", health)
				}

				metricsResponse := httptest.NewRecorder()
				canonicalMetricsHandler(root, time.Hour, newWebSocketBudget(8, 64<<20), newHTTPRequestBudget(8, 64<<20))(metricsResponse,
					httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))

				values, kinds := parseMetricSamples(t, metricsResponse.Body.String())
				if (values["rng_generation_allowed"] == 1) != allowed || (values["rng_reseed_fail_closed"] == 1) != (policy == drbg.ReseedStop) ||
					(values["rng_ready"] == 1) != (state == "fresh") || values["rng_max_reseed_age_seconds"] != 3600 {
					t.Fatalf("incorrect policy metrics: %v", values)
				}

				for _, name := range []string{"rng_generation_allowed", "rng_reseed_fail_closed", "rng_max_reseed_age_seconds"} {
					if kinds[name] != "gauge" {
						t.Fatalf("%s must be a gauge, got %s", name, kinds[name])
					}
				}
			})
		}
	}
}

func TestReseedFailureBeforeFirstHTTPByteReturns503(t *testing.T) {
	root := observableTestDRBG(t)
	if err := root.ConfigureReseedPolicy(drbg.ReseedStop, time.Hour); err != nil {
		t.Fatal(err)
	}

	root.RecordReseedFailure(io.EOF)

	response := httptest.NewRecorder()

	err := streamEntropyResponse(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil), root, 64)
	if !errors.Is(err, drbg.ErrSeedUnavailable) || response.Code != http.StatusServiceUnavailable || response.Header().Get("Content-Length") != "" {
		t.Fatalf("first-read failure: error=%v code=%d headers=%v", err, response.Code, response.Header())
	}
}

func dialGenerationSocket(t *testing.T, endpoint string) *websocket.Conn {
	t.Helper()

	conn, response, err := websocket.DefaultDialer.DialContext(t.Context(), endpoint, nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = conn.Close() })

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))

	return conn
}

func expectSeedSocketClose(t *testing.T, conn *websocket.Conn) {
	t.Helper()

	for range 128 {
		_, _, err := conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseTryAgainLater) {
				t.Fatalf("expected retryable 1013 close: %v", err)
			}

			return
		}
	}

	t.Fatal("unhealthy socket kept emitting frames")
}

func TestReseedFailureClosesExistingSocketsAndAllowsReconnect(t *testing.T) {
	for _, algorithm := range []drbg.Algorithm{drbg.AES256CTR, drbg.ChaCha20} {
		for _, path := range []string{"/stream", "/bytes", "/colors", "/words"} {
			t.Run(string(algorithm)+path, func(t *testing.T) {
				root, err := drbg.NewDRBGWithAlgorithm(make([]byte, 64), algorithm)
				if err != nil {
					t.Fatal(err)
				}

				t.Cleanup(func() { _ = root.Close() })

				if err := root.ConfigureReseedPolicy(drbg.ReseedStop, time.Hour); err != nil {
					t.Fatal(err)
				}

				ws := newWebSocketBudget(8, 64<<20)

				server := httptest.NewServer(testGenerationMux(&root, ws, newHTTPRequestBudget(8, 64<<20)))
				defer server.Close()

				endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + path + "?bytes=64"

				conn := dialGenerationSocket(t, endpoint)
				if _, _, err := conn.ReadMessage(); err != nil {
					t.Fatal(err)
				}

				root.RecordReseedFailure(io.EOF)

				before := metrics.BytesGenerated()

				expectSeedSocketClose(t, conn)
				waitForReseedState(t, func() bool { return ws.usage().Active == 0 })

				if metrics.BytesGenerated() != before || ws.usage().Reserved != 0 {
					t.Fatal("failed socket generated bytes or retained memory")
				}

				if err := root.Reseed(make([]byte, 64)); err != nil {
					t.Fatal(err)
				}

				conn = dialGenerationSocket(t, endpoint)
				if _, _, err := conn.ReadMessage(); err != nil {
					t.Fatal(err)
				}

				_ = conn.Close()

				waitForReseedState(t, func() bool { return ws.usage().Active == 0 })
			})
		}
	}
}

func TestReseedPolicyClosesIdleSocketsWithoutWaitingForFrameTick(t *testing.T) {
	for _, reason := range []string{"failure", "stale"} {
		for _, path := range []string{"/stream", "/bytes", "/colors", "/words"} {
			t.Run(reason+path, func(t *testing.T) {
				root := observableTestDRBG(t)
				if err := root.ConfigureReseedPolicy(drbg.ReseedStop, 250*time.Millisecond); err != nil {
					t.Fatal(err)
				}

				ws := newWebSocketBudget(8, 64<<20)

				server := httptest.NewServer(testGenerationMux(root, ws, newHTTPRequestBudget(8, 64<<20)))
				defer server.Close()

				conn := dialGenerationSocket(t, "ws"+strings.TrimPrefix(server.URL, "http")+path+"?refresh=60000")

				if reason == "failure" {
					root.RecordReseedFailure(io.EOF)
				}

				expectSeedSocketClose(t, conn)
				waitForReseedState(t, func() bool { return ws.usage().Active == 0 })
			})
		}
	}
}

func TestReseedContinueKeepsSocketOpenWhileReadinessFails(t *testing.T) {
	root := observableTestDRBG(t)
	if err := root.ConfigureReseedPolicy(drbg.ReseedContinue, time.Nanosecond); err != nil {
		t.Fatal(err)
	}

	ws := newWebSocketBudget(8, 64<<20)

	server := httptest.NewServer(testGenerationMux(root, ws, newHTTPRequestBudget(8, 64<<20)))
	defer server.Close()

	conn := dialGenerationSocket(t, "ws"+strings.TrimPrefix(server.URL, "http")+"/stream?bytes=64")

	root.RecordReseedFailure(io.EOF)

	for range 3 {
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Fatal(err)
		}
	}

	response := httptest.NewRecorder()
	healthHandler(root, time.Nanosecond)(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ready", nil))

	var health HealthInfo
	if err := json.Unmarshal(response.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}

	if response.Code != http.StatusServiceUnavailable || health.SeedState != "failed" || !health.GenerationAllowed || health.ReseedFailurePolicy != drbg.ReseedContinue {
		t.Fatalf("continue hid health failure: code=%d health=%+v", response.Code, health)
	}

	_ = conn.Close()

	waitForReseedState(t, func() bool { return ws.usage().Active == 0 })
}

func TestReseedBlockedSourceExpiresWithoutWaitingForRead(t *testing.T) {
	root := observableTestDRBG(t)
	if err := root.ConfigureReseedPolicy(drbg.ReseedStop, 75*time.Millisecond); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	source := &scriptedReseedSource{done: ctx.Done(), steps: make(chan reseedStep)}

	done := make(chan struct{})
	go func() {
		defer close(done)

		reseedLoop(ctx, source, 1, 32, root)
	}()

	defer func() { cancel(); <-done }()

	waitForReseedState(t, func() bool { return errors.Is(root.CheckGeneration(), drbg.ErrSeedUnavailable) })

	if meta := root.GetMetadata(); meta.ReseedFailuresTotal != 0 || meta.SeedState(75*time.Millisecond) != "stale" {
		t.Fatalf("blocked read incorrectly recorded a completed failure: %+v", meta)
	}

	response := httptest.NewRecorder()
	livenessHandler(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/live", nil))

	if response.Code != http.StatusOK {
		t.Fatal("source staleness killed process liveness")
	}
}
