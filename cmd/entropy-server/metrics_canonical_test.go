package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCanonicalMetricsHaveUniqueFamiliesAndBaseUnits(t *testing.T) {
	d := observableTestDRBG(t)
	ws, httpBudget := newWebSocketBudget(128, 1<<30), newHTTPRequestBudget(128, 512<<20)
	response := httptest.NewRecorder()
	canonicalMetricsHandler(d, time.Second, ws, httpBudget)(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))
	values, kinds := parseMetricSamples(t, response.Body.String())
	families := make(map[string]string)

	for name, kind := range kinds {
		if legacyMetric(name) {
			t.Fatalf("canonical scrape contains a compatibility alias: %s", name)
		}

		family := name
		if kind == "counter" {
			if !strings.HasSuffix(name, "_total") {
				t.Fatalf("counter lacks the Prometheus total suffix: %s", name)
			}

			family = strings.TrimSuffix(name, "_total")
		}

		if previous, exists := families[family]; exists {
			t.Fatalf("metric names normalize to the same family: %s and %s", previous, name)
		}

		families[family] = name
	}

	for _, name := range []string{"rng_bytes_generated_total", "rng_reseeds_total", "rng_reseed_age_seconds", "wss_payloads_total", "http_requests_total"} {
		if _, exists := values[name]; !exists {
			t.Fatalf("canonical metric missing: %s", name)
		}
	}

	for _, detail := range []string{"not bytes served or physical entropy", "excludes debug listener", "excludes control frames", "not process RSS"} {
		if !strings.Contains(response.Body.String(), detail) {
			t.Fatalf("metric scope is unclear: missing %q", detail)
		}
	}

	legacy := httptest.NewRecorder()
	metricsHandler(d, time.Second, ws, httpBudget)(legacy, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics-2", nil))

	oldValues, oldKinds := parseMetricSamples(t, legacy.Body.String())
	for name, value := range values {
		if name != "rng_reseed_age_seconds" && oldValues[name] != value || oldKinds[name] != kinds[name] {
			t.Fatalf("canonical and compatibility routes disagree for %s", name)
		}
	}

	for _, name := range []string{"rng_mb_generated_total", "rng_generator_reseeds_total", "rng_reseed_age_ms", "wss_payloads", "qrng_buffer_enabled"} {
		if _, exists := oldValues[name]; !exists {
			t.Fatalf("compatibility metric removed: %s", name)
		}
	}

	if !strings.Contains(legacy.Header().Get("Link"), "</metrics>") {
		t.Fatal("compatibility scrape should identify the canonical endpoint")
	}
}
