package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/config"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/diag"
)

func readReport(t *testing.T, path string) []json.RawMessage {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(data) == 0 || data[len(data)-1] != '\n' || bytes.Contains(data, []byte{0x1b}) {
		t.Fatal("report must be nonempty, newline-terminated JSONL without terminal escapes")
	}

	records := make([]json.RawMessage, 0, bytes.Count(data, []byte{'\n'}))
	for line := range bytes.SplitSeq(bytes.TrimSuffix(data, []byte{'\n'}), []byte{'\n'}) {
		if !json.Valid(line) {
			t.Fatalf("invalid JSONL record: %s", line)
		}

		records = append(records, bytes.Clone(line))
	}

	return records
}

func decodeRecord[T any](t *testing.T, data json.RawMessage) T {
	t.Helper()

	var record T
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}

	return record
}

func experimentConfig(mode, endpoint, report string) *config.Config {
	return &config.Config{
		Mode: mode, HTTPUrl: endpoint, WSUrl: endpoint, Slice: 256,
		Refresh: 5 * time.Millisecond, DisplayInterval: time.Second,
		AnalysisBytes: 64, AnalysisInterval: 20 * time.Millisecond,
		Duration: 150 * time.Millisecond, ShannonThreshold: 6.1,
		ReportPath: report, Headless: true,
	}
}

func serveExperiment(t *testing.T, mode string) string {
	t.Helper()

	data := make([]byte, 256)
	for i := range data {
		data[i] = byte(i)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode == modePull {
			_, _ = w.Write(data)

			return
		}

		var upgrader websocket.Upgrader

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()

		for range ticker.C {
			if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)

	if mode == modeStream {
		return "ws" + strings.TrimPrefix(server.URL, "http")
	}

	return server.URL
}

func TestTimedReportsForBothTransports(t *testing.T) {
	for _, mode := range []string{modePull, modeStream} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "experiment.jsonl")

			cfg := experimentConfig(mode, serveExperiment(t, mode), path)
			if err := runConfigured(t.Context(), cfg); err != nil {
				t.Fatal(err)
			}

			records := readReport(t, path)
			if len(records) < 4 {
				t.Fatalf("expected start, measurement, sample and summary: %d records", len(records))
			}

			start := decodeRecord[startRecord](t, records[0])
			if start.Type != "run_start" || start.SchemaVersion != 1 || start.Settings.ShannonThreshold != 6.1 || start.Settings.Mode != mode {
				t.Fatalf("incorrect run metadata: %+v", start)
			}

			measurement := decodeRecord[reportHeader](t, records[1])
			if measurement.Type != "measurement_start" || measurement.Timestamp.Before(start.Timestamp) {
				t.Fatal("missing measurement boundary")
			}

			var previous sampleRecord

			for i, raw := range records[2 : len(records)-1] {
				sample := decodeRecord[sampleRecord](t, raw)
				if sample.Type != "sample" || sample.AnalysedSamples != uint64(i+1) || sample.SampleBytes != 64 || sample.FrameBytes != 256 {
					t.Fatalf("incorrect sampled prefix: %+v", sample)
				}

				if sample.Shannon != 6 || sample.ShannonAssessment != diag.ShannonWarn || sample.ShannonWarn != sample.AnalysedSamples || sample.ShannonOK != 0 {
					t.Fatalf("threshold changed the measured entropy or assessment: %+v", sample)
				}

				if sample.AnalysedBytes != sample.AnalysedSamples*64 || sample.ReceivedBytes != int64(sample.ReceivedFrames)*256 || sample.ReceivedFrames < sample.AnalysedSamples {
					t.Fatalf("received/analysed accounting differs: %+v", sample)
				}

				if sample.ReceivedFrames <= previous.ReceivedFrames || sample.ElapsedMS < previous.ElapsedMS || sample.AnalysisMS < 0 || sample.ReceiveWaitMS < 0 {
					t.Fatal("sample counters or timings moved backwards")
				}

				previous = sample
			}

			summary := decodeRecord[summaryRecord](t, records[len(records)-1])
			if summary.Type != "run_summary" || summary.Termination != "duration" || summary.Failed || !summary.MeasurementBegan {
				t.Fatalf("missing successful duration summary: %+v", summary)
			}

			if summary.AnalysedSamples != uint64(len(records)-3) || summary.AnalysedBytes != summary.AnalysedSamples*64 || summary.ReceivedBytes != int64(summary.ReceivedFrames)*256 || summary.ReceivedFrames < summary.AnalysedSamples {
				t.Fatalf("summary accounting differs: %+v", summary)
			}

			if summary.ElapsedMS < float64(cfg.Duration.Milliseconds()) || summary.ElapsedMS > 3000 || summary.ReceivedMiBPerSec <= 0 {
				t.Fatalf("duration/throughput incorrect: %+v", summary)
			}

			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("report permissions: %v, %v", info, err)
			}
		})
	}
}

func TestDurationInterruptsPendingHTTPAndWSHandshake(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	for _, mode := range []string{modePull, modeStream} {
		t.Run(mode, func(t *testing.T) {
			endpoint := server.URL
			if mode == modeStream {
				endpoint = "ws" + strings.TrimPrefix(server.URL, "http")
			}

			path := filepath.Join(t.TempDir(), "pending.jsonl")
			cfg := experimentConfig(mode, endpoint, path)

			cfg.Duration = 50 * time.Millisecond
			if err := runConfigured(t.Context(), cfg); err != nil {
				t.Fatal(err)
			}

			records := readReport(t, path)

			summary := decodeRecord[summaryRecord](t, records[len(records)-1])
			if len(records) != 3 || summary.Termination != "duration" || summary.ShannonAssessment != diag.ShannonNoSample || summary.ElapsedMS > 3000 {
				t.Fatalf("pending request was not cancelled with NO_SAMPLE: %+v", summary)
			}
		})
	}
}

func TestUnlimitedRunEndsWithInterruptSummary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "interrupted.jsonl")
	cfg := experimentConfig(modePull, serveExperiment(t, modePull), path)
	cfg.Duration = 0

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Millisecond)
	defer cancel()

	if err := runConfigured(ctx, cfg); err != nil {
		t.Fatal(err)
	}

	records := readReport(t, path)

	summary := decodeRecord[summaryRecord](t, records[len(records)-1])
	if summary.Termination != "interrupted" || summary.Failed || summary.AnalysedSamples == 0 {
		t.Fatalf("missing interruption summary: %+v", summary)
	}
}

func TestReportCapturesTransportAndStartupFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "test transport failure", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	for _, headless := range []bool{true, false} {
		path := filepath.Join(t.TempDir(), "failure.jsonl")
		cfg := experimentConfig(modePull, server.URL, path)

		cfg.Headless = headless
		if err := runConfigured(t.Context(), cfg); err == nil {
			t.Fatal("transport failure returned success")
		}

		records := readReport(t, path)
		summary := decodeRecord[summaryRecord](t, records[len(records)-1])

		want := "startup_error"
		if headless {
			want = "transport_error"
		}

		if summary.Termination != want || !summary.Failed || summary.MeasurementBegan != headless || summary.ShannonAssessment != diag.ShannonNoSample {
			t.Fatalf("missing failure summary: %+v", summary)
		}
	}
}

func TestReportRefusesOverwriteAndRemovesURLSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.jsonl")

	report, err := openReport(path)
	if err != nil {
		t.Fatal(err)
	}

	cfg := experimentConfig(modePull, "", path)
	if err := report.start(cfg, "http://secret-user:secret-pass@localhost/data?token=secret-query#secret-fragment"); err != nil {
		t.Fatal(err)
	}

	if err := report.close(); err != nil {
		t.Fatal(err)
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(before), "secret-") {
		t.Fatalf("URL secrets leaked into report: %s", before)
	}

	start := decodeRecord[startRecord](t, readReport(t, path)[0])
	if start.Settings.Endpoint != "http://localhost/data" {
		t.Fatalf("incorrect sanitized endpoint: %s", start.Settings.Endpoint)
	}

	if err := runConfigured(t.Context(), cfg); err == nil || !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing report accepted: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("previous report was changed")
	}
}

type failingReportWriter struct {
	cancel context.CancelFunc
}

func (w failingReportWriter) Write(_ []byte) (int, error) {
	w.cancel()

	return 0, io.ErrClosedPipe
}

func TestReportWriteFailureSurvivesConcurrentCancellation(t *testing.T) {
	endpoint := serveExperiment(t, modePull)
	cfg := experimentConfig(modePull, endpoint, "")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	report := &runReport{encoder: json.NewEncoder(failingReportWriter{cancel: cancel})}
	m := monitorState{cfg: cfg, started: time.Now()}

	reason, err := runMonitor(ctx, &m, report, endpoint, "")
	if reason != "report_error" || !errors.Is(err, io.ErrClosedPipe) || ctx.Err() == nil {
		t.Fatalf("write failure was hidden by cancellation: reason=%s err=%v", reason, err)
	}
}

func TestReportCloseSurfacesFileErrors(t *testing.T) {
	report, err := openReport(filepath.Join(t.TempDir(), "closed.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	if err := report.file.Close(); err != nil {
		t.Fatal(err)
	}

	if err := report.close(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("sync/close failure was hidden: %v", err)
	}
}

func TestPreflightInterruptHasNoMeasuredSamples(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preflight-interrupted.jsonl")
	cfg := experimentConfig(modePull, serveExperiment(t, modePull), path)
	cfg.Headless = false
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := runConfigured(ctx, cfg); err != nil {
		t.Fatal(err)
	}

	records := readReport(t, path)

	summary := decodeRecord[summaryRecord](t, records[len(records)-1])
	if len(records) != 2 || summary.Termination != "interrupted" || summary.MeasurementBegan || summary.ElapsedMS != 0 || summary.AnalysedSamples != 0 {
		t.Fatalf("preflight was counted as measurement: %+v", summary)
	}
}

func TestHeadlessStdoutContainsOnlyJSONL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stdout.jsonl")

	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()

	original := os.Stdout

	os.Stdout = out
	defer func() { os.Stdout = original }()

	cfg := experimentConfig(modePull, serveExperiment(t, modePull), "-")
	cfg.ShowWaterfall, cfg.ShowPreview, cfg.ShowGraph, cfg.ShowDashboard = true, true, true, true
	cfg.WaterfallMode, cfg.WaterfallColumns, cfg.WaterfallRows, cfg.Color = "rgb", 128, 32, "truecolor"
	cfg.Layout = "full"

	cfg.Duration = 40 * time.Millisecond
	if err := runConfigured(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}

	records := readReport(t, path)
	if decodeRecord[reportHeader](t, records[0]).Type != "run_start" || decodeRecord[reportHeader](t, records[len(records)-1]).Type != "run_summary" {
		t.Fatal("headless stdout is not a complete report")
	}

	settings := decodeRecord[startRecord](t, records[0]).Settings
	if !settings.Waterfall || settings.WaterfallMode != "rgb" || settings.WaterfallColumns != 128 || settings.WaterfallRows != 32 || settings.Color != "truecolor" || settings.Layout != "full" {
		t.Fatalf("display settings missing from metadata: %+v", settings)
	}
}
