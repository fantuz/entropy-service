package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAdmissionHighWaterMarks(t *testing.T) {
	httpBudget := newHTTPRequestBudget(2, 1<<20)

	httpFirst, code, _ := httpBudget.tryReserve(128 << 10)
	if code != 0 {
		t.Fatal("first HTTP reservation rejected")
	}

	httpSecond, code, _ := httpBudget.tryReserve(256 << 10)
	if code != 0 {
		t.Fatal("second HTTP reservation rejected")
	}

	usage := httpBudget.usage()
	if usage.PeakActive != 2 || usage.PeakReserved != 384<<10 {
		t.Fatalf("HTTP peaks incorrect: %+v", usage)
	}

	_, code, _ = httpBudget.tryReserve(128 << 10)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("HTTP slot limit should reject: %d", code)
	}

	httpFirst()
	httpSecond()
	httpSecond()

	usage = httpBudget.usage()
	if usage.Active != 0 || usage.Reserved != 0 ||
		usage.PeakActive != 2 || usage.PeakReserved != 384<<10 {
		t.Fatalf("HTTP peak must persist after release: %+v", usage)
	}

	wsBudget := newWebSocketBudget(2, 1<<20)
	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/stream", nil)

	wsFirst, ok := wsBudget.admit(w, r, 128<<10)
	if !ok {
		t.Fatal("first WebSocket reservation rejected")
	}

	wsSecond, ok := wsBudget.admit(w, r, 256<<10)
	if !ok {
		t.Fatal("second WebSocket reservation rejected")
	}

	wsUsage := wsBudget.usage()
	if wsUsage.PeakActive != 2 || wsUsage.PeakReserved != 384<<10 {
		t.Fatalf("WebSocket peaks incorrect: %+v", wsUsage)
	}

	_, ok = wsBudget.admit(httptest.NewRecorder(), r, 128<<10)
	if ok {
		t.Fatal("WebSocket slot limit should reject")
	}

	wsFirst()
	wsSecond()
	wsSecond()

	wsUsage = wsBudget.usage()
	if wsUsage.Active != 0 || wsUsage.Reserved != 0 ||
		wsUsage.PeakActive != 2 || wsUsage.PeakReserved != 384<<10 {
		t.Fatalf("WebSocket peak must persist after release: %+v", wsUsage)
	}

	d := observableTestDRBG(t)
	response := httptest.NewRecorder()
	metricsHandler(d, time.Hour, wsBudget, httpBudget)(
		response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics-2", nil),
	)
	values, kinds := parseMetricSamples(t, response.Body.String())

	for name, want := range map[string]float64{
		"http_generation_requests_peak":              2,
		"http_generation_memory_peak_reserved_bytes": 384 << 10,
		"ws_connections_peak":                        2,
		"ws_memory_peak_reserved_bytes":              384 << 10,
	} {
		if values[name] != want || kinds[name] != "gauge" {
			t.Fatalf("%s: value=%v type=%s, want %v gauge", name, values[name], kinds[name], want)
		}
	}

	freshHTTP := newHTTPRequestBudget(2, 1<<20)
	freshWS := newWebSocketBudget(2, 1<<20)

	if freshHTTP.usage().PeakActive != 0 || freshHTTP.usage().PeakReserved != 0 ||
		freshWS.usage().PeakActive != 0 || freshWS.usage().PeakReserved != 0 {
		t.Fatal("new budget must begin with zero high-water marks")
	}
}
