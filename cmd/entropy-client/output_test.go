package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRawFileExportAndExactLimitBothTransports(t *testing.T) {
	for _, mode := range []string{modePull, modeStream} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			cfg := experimentConfig(mode, serveExperiment(t, mode), filepath.Join(dir, "report.jsonl"))
			cfg.OutputPath = filepath.Join(dir, "raw.bin")
			cfg.OutputBytes = 700 // Two whole 256-byte frames and a final prefix.
			cfg.AnalysisInterval = time.Hour

			cfg.Duration = 2 * time.Second
			if err := runConfigured(t.Context(), cfg); err != nil {
				t.Fatal(err)
			}

			data, err := os.ReadFile(cfg.OutputPath)

			want := bytes.Repeat(make([]byte, 256), 3)[:700]
			for i := range want {
				want[i] = byte(i % 256)
			}

			if err != nil || !bytes.Equal(data, want) {
				t.Fatalf("export changed/truncated bytes: %d %v", len(data), err)
			}

			records := readReport(t, cfg.ReportPath)
			start := decodeRecord[startRecord](t, records[0])

			summary := decodeRecord[summaryRecord](t, records[len(records)-1])
			if start.Settings.OutputKind != "file" || start.Settings.OutputLimit != 700 || summary.Failed || summary.Termination != reasonOutputLimit || summary.OutputBytes != 700 || summary.OutputFrames != 2 || summary.OutputErrors != 0 || summary.DeviceBytes != 0 || summary.AnalysedSamples != 1 || summary.ReceivedFrames != 3 || summary.ReceivedBytes != 768 {
				t.Fatalf("export accounting differs from reception/analysis: %+v", summary)
			}
		})
	}
}

func TestRawFileOpenFailureBeforeTransport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing")
	if err := os.WriteFile(path, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := experimentConfig(modePull, "http://127.0.0.1:1/not-requested", filepath.Join(t.TempDir(), "report.jsonl"))

	cfg.OutputPath = path
	if err := runConfigured(t.Context(), cfg); err == nil {
		t.Fatal("existing output was accepted")
	}

	records := readReport(t, cfg.ReportPath)

	summary := decodeRecord[summaryRecord](t, records[len(records)-1])
	if !summary.Failed || summary.MeasurementBegan || summary.OutputErrors != 1 || summary.DeviceErrors != 0 || summary.Termination != reasonStartupError {
		t.Fatalf("%+v", summary)
	}

	data, err := os.ReadFile(path)
	if err != nil || string(data) != "preserve" {
		t.Fatal("existing file modified", err)
	}
}
