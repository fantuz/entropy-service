package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/client"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/config"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/diag"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/terminal"
)

func TestMonitorOptionalPanelsUseSelectedPrefix(t *testing.T) {
	for _, mode := range []string{modePull, modeStream} {
		cfg := &config.Config{Mode: mode, AnalysisBytes: 64, ShowMatrix: true, ShowPreview: true, ShowGraph: true, ShowDashboard: true, ShowHistogram: true, ShowDebug: true}
		m := monitorState{cfg: cfg, started: time.Now().Add(-time.Second), graph: diag.NewEntropyGraph(3), dashboard: diag.NewDashboard(3), waterfall: diag.NewWaterfall(8, 2, diag.WaterfallGray)}

		data := make([]byte, 256)
		for i := range data {
			data[i] = byte(i)
		}

		m.observe(client.StreamFrame{Data: data, ReceiveWait: 1250 * time.Microsecond, CycleRuntime: 1500 * time.Microsecond}, time.Now())

		var out strings.Builder
		m.renderTo(&out, terminal.Profile{Columns: 80, Rows: 24})

		for _, want := range []string{"Analysed prefix    : 64 / 256 frame bytes", "Shannon entropy    : 6.00000", "Transition Matrix", "preview (hex): 00010203", "Shannon entropy history", "Sampled statistics history", "Analysed byte distribution", "Last analysis", "Total runtime"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("%s missing %q: %q", mode, want, out.String())
			}
		}

		if mode == modeStream && !strings.Contains(out.String(), "Last WS receive    : 1.250 ms") ||
			mode == modePull && !strings.Contains(out.String(), "HTTP cycle runtime : 1.500 ms") || strings.Contains(out.String(), "Waterfall") {
			t.Fatalf("transport labels or optional-panel screen budgeting changed: %q", out.String())
		}
	}
}

func TestPreflightBothTransportsAndUnstyledOutput(t *testing.T) {
	for _, mode := range []string{modePull, modeStream} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "preflight.txt")

			file, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()

			original := os.Stdout

			os.Stdout = file
			defer func() { os.Stdout = original }()

			endpoint := serveExperiment(t, mode)
			cfg := experimentConfig(mode, endpoint, "")

			cfg.Color, cfg.ShowWaterfall, cfg.WaterfallMode = "never", true, "rgb"
			if err := showPreflight(t.Context(), cfg, endpoint, endpoint); err != nil {
				t.Fatal(err)
			}

			output, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			text := string(output)
			for _, want := range []string{"Pre-flight diagnostics", "Waterfall / mapping  : true rgb", "Terminal colors      : never", "Fetched Size         : 256", "Pre-flight analysed  : 64 / 256 bytes", "Link                 : data received", "Shannon assessment : WARN"} {
				if !strings.Contains(text, want) {
					t.Errorf("preflight missing %q: %q", want, text)
				}
			}

			if strings.ContainsAny(text, "\x1b\a") {
				t.Fatal("redirected unstyled preflight emitted terminal controls")
			}
		})
	}
}
