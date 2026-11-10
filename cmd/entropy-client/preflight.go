package main

import (
	"fmt"
	"io"
	"runtime"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/config"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/terminal"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/tests"
	"github.com/fantuz/entropy-service/internal/console"
)

func printPreflight(out io.Writer, cfg *config.Config, httpURL, wsURL string, data []byte, profile terminal.Profile) {
	r := console.New(out, "entropy-client | Startup invocation report")
	r.Section("Connection & reception")
	r.Value("Build / platform", fmt.Sprintf("%s / %s/%s", runtime.Version(), runtime.GOOS, runtime.GOARCH))
	r.Value("Transport", cfg.Mode)
	r.Value("HTTP endpoint", displayEndpoint(httpURL))
	r.Value("WebSocket endpoint", displayEndpoint(wsURL))
	r.Value("Frame size", fmt.Sprintf("%d bytes", cfg.Slice))
	r.Value("Refresh interval", fmt.Sprintf("%s (%.2f frames/s)", cfg.Refresh, 1/cfg.Refresh.Seconds()))
	r.Value("Requested rate", fmt.Sprintf("%.3f MiB/s (payload target)", float64(cfg.Slice)/cfg.Refresh.Seconds()/(1<<20)))
	r.Section("Analysis & output")
	r.Value("Analysis prefix cap", fmt.Sprintf("%d bytes (0 = full frame)", cfg.AnalysisBytes))
	r.Value("Analysis interval", fmt.Sprintf("%s (0 = every frame)", cfg.AnalysisInterval))
	r.Value("Shannon threshold", fmt.Sprintf("%.5f bits/byte", cfg.ShannonThreshold))
	r.Value("Measurement duration", fmt.Sprintf("%s (0 = until interrupted; excludes preflight)", cfg.Duration))
	r.Value("JSONL report", cfg.ReportPath)
	r.Value("Write-out /dev/urandom", fmt.Sprintf("%t (mix only; no entropy credit)", cfg.WriteOut))
	r.Value("Raw output", cfg.OutputKind())
	r.Value("Output file / device", cfg.OutputPath+cfg.OutputDevice)
	r.Value("Output byte limit", fmt.Sprintf("%d (0 = until duration/interruption)", cfg.OutputBytes))
	r.Section("Display")
	r.Value("Terminal", fmt.Sprintf("%d columns x %d rows; interactive=%t", profile.Columns, profile.Rows, profile.Interactive))
	r.Value("Layout / color", fmt.Sprintf("%s / %s", cfg.Layout, cfg.Color))
	r.Value("Display interval", fmt.Sprintf("%s requested; %s effective", cfg.DisplayInterval, profile.DisplayInterval(cfg.DisplayInterval)))
	r.Value("Dashboard / graph", fmt.Sprintf("%t / %t", cfg.ShowDashboard, cfg.ShowGraph))
	r.Value("Matrix / histogram", fmt.Sprintf("%t / %t", cfg.ShowMatrix, cfg.ShowHistogram))
	r.Value("Preview / debug", fmt.Sprintf("%t / %t", cfg.ShowPreview, cfg.ShowDebug))
	r.Value("Waterfall / mapping", fmt.Sprintf("%t / %s", cfg.ShowWaterfall, cfg.WaterfallMode))
	r.Value("Waterfall bounds", fmt.Sprintf("%d columns x %d rows (0 = auto)", cfg.WaterfallColumns, cfg.WaterfallRows))
	r.Section("Pre-flight diagnostics")
	r.Check("Configuration", "OK", "flags, sampling and display limits validated")

	if len(data) > 0 {
		r.Check("Selected transport", "OK", "data received from the selected endpoint")
	} else {
		r.Check("Selected transport", "WARN", "empty response from the selected endpoint")
	}

	state := "OK"
	if int64(len(data)) != cfg.Slice {
		state = "WARN"
	}

	r.Check("Fetched size", state, fmt.Sprintf("%d / %d requested bytes", len(data), cfg.Slice))
	result := analysePayload(data, cfg)
	r.Value("Pre-flight analysed", fmt.Sprintf("%d / %d bytes", result.N, len(data)))
	fmt.Fprintln(out)
	tests.PrintDiagnosticsWithColor(out, &result, cfg.ShannonThreshold, profile.Color != terminal.Monochrome)
	fmt.Fprintln(out, "\nMeasurement starts after 3s. Diagnostics screen samples; they do not certify randomness.")
}

func displayEndpoint(raw string) string {
	u, err := sanitizedEndpoint(raw)
	if err != nil {
		return "(invalid endpoint)"
	}

	return u
}
