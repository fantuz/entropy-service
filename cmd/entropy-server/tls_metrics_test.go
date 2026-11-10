package main

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTLSMetricsDescribeLoadedIdentityAndHTTPOnly(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(boolLabel(enabled), func(t *testing.T) {
			root := observableTestDRBG(t)

			var cfg *tls.Config

			expires := time.Now().Add(time.Hour).Truncate(time.Second)
			if enabled {
				cfg = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{{Leaf: &x509.Certificate{NotAfter: expires}}}}
			}

			handler := withTLSMetrics(canonicalMetricsHandler(root, time.Hour, newWebSocketBudget(128, 1<<30), newHTTPRequestBudget(128, 512<<20)), cfg)
			if enabled {
				cfg.Certificates[0].Leaf.NotAfter = expires.Add(24 * time.Hour)
			}

			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))
			values, kinds := parseMetricSamples(t, recorder.Body.String())

			wantEnabled := float64(0)
			if enabled {
				wantEnabled = 1
			}

			if values["rng_https_enabled"] != wantEnabled || kinds["rng_https_enabled"] != "gauge" {
				t.Fatal("HTTPS state is incorrect")
			}

			got, present := values["rng_tls_certificate_not_after_timestamp_seconds"]
			if present != enabled || enabled && (got != float64(expires.Unix()) || kinds["rng_tls_certificate_not_after_timestamp_seconds"] != "gauge") {
				t.Fatal("certificate expiry must describe the loaded identity only")
			}

			for _, text := range []string{"not an external connectivity check", "not a certificate file on disk"} {
				if enabled && !strings.Contains(recorder.Body.String(), text) {
					t.Fatal("metric scope missing", text)
				}
			}
		})
	}
}
