package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestModeAliasesUseSelectedTransportAndPreserveReports(t *testing.T) {
	for _, tc := range []struct{ name, canonical string }{
		{"pull", modePull},
		{"http", modePull},
		{"stream", modeStream},
		{"push", modeStream},
		{"websocket", modeStream},
	} {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := serveExperiment(t, tc.canonical)
			path := filepath.Join(t.TempDir(), "alias.jsonl")

			cfg := experimentConfig(tc.name, endpoint, path)
			if tc.canonical == modePull {
				cfg.WSUrl = "://unused"
			} else {
				cfg.HTTPUrl = "://unused"
			}

			if err := runConfigured(t.Context(), cfg); err != nil {
				t.Fatal(err)
			}

			records := readReport(t, path)
			start := decodeRecord[startRecord](t, records[0])

			end := decodeRecord[summaryRecord](t, records[len(records)-1])
			if start.Settings.Mode != tc.canonical || end.Failed || end.ReceivedBytes == 0 || end.AnalysedSamples == 0 {
				t.Fatalf("alias changed transport or report semantics: start=%+v summary=%+v", start, end)
			}
		})
	}
}

func TestInvalidModeFailsBeforeOpeningReportOrOutput(t *testing.T) {
	for _, mode := range []string{"", "HTTP", " pull ", "udp"} {
		cfg := experimentConfig(mode, "http://127.0.0.1:1", filepath.Join(t.TempDir(), "absent.jsonl"))
		cfg.OutputPath = filepath.Join(t.TempDir(), "absent.bin")
		opened := false

		err := runConfiguredWithWriteout(t.Context(), cfg, func() (writeoutSink, error) {
			opened = true

			return nil, errors.New("unexpected output initialization")
		})
		if err == nil || opened {
			t.Fatalf("invalid mode %q reached output setup: %v", mode, err)
		}

		if _, err := os.Stat(cfg.ReportPath); !os.IsNotExist(err) {
			t.Fatalf("invalid mode created a report: %v", err)
		}
	}
}
