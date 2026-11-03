package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/drbg"
)

func TestActivityPeakMonitorIntervalRatesAndReset(t *testing.T) {
	var monitor activityPeakMonitor
	base := time.Unix(100, 0)
	monitor.observe(base, activityPeakCounters{
		generatedBytes: 100, wsFrames: 10, httpRequests: 10,
		wsRejections: 1, httpRejections: 2,
	})
	monitor.observe(base.Add(time.Second), activityPeakCounters{
		generatedBytes: 2100, wsFrames: 20, httpRequests: 15,
		wsRejections: 3, httpRejections: 5,
	})

	first := monitor.snapshot()
	if first.GeneratedBytesPerSecond != 2000 || first.WSFramesPerSecond != 10 ||
		first.HTTPRequestsPerSecond != 5 || first.WSRejectionsPerMinute != 120 ||
		first.HTTPRejectionsPerMinute != 180 {
		t.Fatalf("unexpected 1-second peaks: %+v", first)
	}

	// A lower rate cannot reduce an existing peak.
	monitor.observe(base.Add(3*time.Second), activityPeakCounters{
		generatedBytes: 2300, wsFrames: 22, httpRequests: 17,
		wsRejections: 3, httpRejections: 5,
	})
	if second := monitor.snapshot(); second != first {
		t.Fatalf("peaks must survive quiet intervals: %+v vs %+v", second, first)
	}

	// Counter resets and clock discontinuities cannot create giant spikes.
	monitor.observe(base.Add(4*time.Second), activityPeakCounters{})
	monitor.observe(base.Add(4*time.Second), activityPeakCounters{
		generatedBytes: ^uint64(0), wsFrames: ^uint64(0),
		httpRequests: ^uint64(0), wsRejections: ^uint64(0),
		httpRejections: ^uint64(0),
	})
	if after := monitor.snapshot(); after != first {
		t.Fatalf("counter resets must not inflate existing peaks: %+v", after)
	}
}

func TestActivityPeakExposition(t *testing.T) {
	d := observableTestDRBG(t)
	var monitor activityPeakMonitor
	start := time.Unix(100, 0)
	monitor.observe(start, activityPeakCounters{})
	monitor.observe(start.Add(time.Second), activityPeakCounters{
		generatedBytes: 1048576, wsFrames: 13, httpRequests: 7,
		wsRejections: 2, httpRejections: 3,
	})

	w := httptest.NewRecorder()
	metricsHandler(d, time.Hour, newWebSocketBudget(10, 1<<20),
		newHTTPRequestBudget(10, 1<<20), &monitor)(
		w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics-2", nil),
	)

	values, kinds := parseMetricSamples(t, w.Body.String())
	for name, expected := range map[string]float64{
		"rng_generation_peak_bytes_per_second":    1048576,
		"wss_payloads_peak_per_second":            13,
		"http_requests_peak_per_second":           7,
		"ws_rejections_peak_per_minute":           120,
		"http_generation_rejections_peak_per_minute": 180,
	} {
		if kinds[name] != "gauge" || values[name] != expected {
			t.Errorf("%s = %v (type %s), want %v gauge", name, values[name], kinds[name], expected)
		}
	}

	if kinds["drbg_instances_peak"] != "gauge" ||
		values["drbg_instances_peak"] < float64(drbg.ActiveInstances()) {
		t.Fatal("instance high-water metric is absent or lower than active count")
	}
}
