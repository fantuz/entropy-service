package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/client"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/config"
)

func TestAdaptiveControllerBoundsHysteresisAndRecovery(t *testing.T) {
	cfg := &config.Config{Refresh: 10 * time.Millisecond, AdaptiveMaxInterval: 100 * time.Millisecond}
	p := newAcquisitionPacing(cfg)
	p.observe(200 * time.Millisecond)

	if got := time.Duration(p.interval.Load()); got != cfg.AdaptiveMaxInterval {
		t.Fatal("did not respect adaptive ceiling", got)
	}

	for range 4 {
		p.observe(time.Millisecond)
	}

	if time.Duration(p.interval.Load()) < 80*time.Millisecond {
		t.Fatal("recovered too abruptly")
	}

	for range 1200 {
		p.observe(time.Millisecond)
	}

	if got := time.Duration(p.interval.Load()); got != cfg.Refresh {
		t.Fatal("did not recover to requested cadence", got)
	}

	p.observe(time.Duration(1<<63 - 1))

	if got := time.Duration(p.interval.Load()); got < cfg.Refresh || got > cfg.AdaptiveMaxInterval {
		t.Fatal("duration overflow escaped pacing bounds")
	}
}

func TestAdaptiveControllerBackoffAndRecoverySteps(t *testing.T) {
	p := newAcquisitionPacing(&config.Config{Refresh: 100 * time.Millisecond, AdaptiveMaxInterval: time.Second})
	p.observe(100 * time.Millisecond)

	if got := time.Duration(p.interval.Load()); got != 133*time.Millisecond {
		t.Fatalf("expected 33%% backoff, got %s", got)
	}

	// A larger demand must be handled immediately rather than requiring many
	// percentage steps; the independent ceiling still bounds the result.
	p.observe(time.Second)

	if got := time.Duration(p.interval.Load()); got != 350*time.Millisecond {
		t.Fatalf("did not jump to measured demand with headroom: %s", got)
	}

	p.interval.Store(int64(800 * time.Millisecond))

	p.smoothed = 100 * time.Millisecond
	for range 7 {
		p.observe(100 * time.Millisecond)
	}

	if got := time.Duration(p.interval.Load()); got != 800*time.Millisecond {
		t.Fatalf("recovered without eight healthy observations: %s", got)
	}

	p.observe(100 * time.Millisecond)

	if got := time.Duration(p.interval.Load()); got != 400*time.Millisecond {
		t.Fatalf("expected 50%% interval recovery, got %s", got)
	}

	for range 16 {
		p.observe(100 * time.Millisecond)
	}

	if got := time.Duration(p.interval.Load()); got != 125*time.Millisecond {
		t.Fatalf("recovery went below measured demand or did not converge: %s", got)
	}
}

func TestPerformanceRatesUseAllFramesAndMeasuredDuration(t *testing.T) {
	began := time.Now()
	cfg := &config.Config{Refresh: 100 * time.Millisecond, Slice: 1 << 20}
	m := monitorState{cfg: cfg, started: began, frames: 10}

	p := m.performance(began.Add(2 * time.Second))
	if p.RequestedFPS != 10 || p.AverageReceivedFPS != 5 || p.PacingStatus != "below target" || p.RequestedMiBPerSecond != 10 {
		t.Fatalf("misleading acquisition rates: %+v", p)
	}
}

func TestAdaptivePullPacesSlowResponsesAndPreservesBytesAndPolicy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("bytes") != "256" {
			t.Error("adaptive mode changed payload")
		}

		select {
		case <-r.Context().Done():
			return
		case <-time.After(20 * time.Millisecond):
		}

		_, _ = w.Write(bytes.Repeat([]byte{42}, 256))
	}))
	defer server.Close()

	cfg := experimentConfig(modePull, server.URL, filepath.Join(t.TempDir(), "adaptive.jsonl"))
	cfg.Adaptive = true
	cfg.AdaptiveMaxInterval = 100 * time.Millisecond
	cfg.Duration = 350 * time.Millisecond
	cfg.AnalysisBytes = 0

	cfg.AnalysisInterval = 0
	if err := runConfigured(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}

	records := readReport(t, cfg.ReportPath)

	end := decodeRecord[summaryRecord](t, records[len(records)-1])
	if end.Failed || end.ReceivedFrames < 3 || end.AnalysedSamples != end.ReceivedFrames || end.AnalysedBytes != uint64(end.ReceivedBytes) || end.EffectiveIntervalMS <= float64(cfg.Refresh.Milliseconds()) || end.EffectiveIntervalMS > 100 {
		t.Fatalf("adaptive acquisition changed analysis or pacing limits: %+v", end)
	}
}

func TestAdaptiveFailureRemainsFailureAndCancelInterruptsLongPacing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "generation paused", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	cfg := experimentConfig(modePull, server.URL, "")
	cfg.Adaptive = true

	cfg.AdaptiveMaxInterval = time.Second
	if err := runConfigured(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatal("adaptive mode suppressed a generation failure")
	}

	cfg.Refresh = time.Second
	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)
	go func() {
		done <- pullFramesWithPacing(ctx, cfg, server.URL, make(chan client.StreamFrame, 1), newAcquisitionPacing(cfg))
	}()

	time.Sleep(25 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("adaptive pacing ignored cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("adaptive wait blocked cancellation")
	}
}
