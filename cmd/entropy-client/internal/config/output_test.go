package config

import (
	"testing"
	"time"
)

func TestOutputValidation(t *testing.T) {
	for _, cfg := range []Config{
		{WriteOut: true, OutputPath: "capture.bin"},
		{WriteOut: true, OutputDevice: "/dev/null"},
		{OutputPath: "capture.bin", OutputDevice: "/dev/null"},
		{OutputPath: "-"},
		{OutputPath: "-", Headless: true, ReportPath: "-"},
		{OutputBytes: -1},
		{OutputBytes: 1},
	} {
		cfg.DisplayInterval = time.Second
		if cfg.ValidateAnalysis() == nil {
			t.Fatalf("accepted incompatible outputs: %+v", cfg)
		}
	}

	for _, cfg := range []Config{
		{},
		{WriteOut: true, OutputBytes: 64},
		{OutputPath: "capture.bin", ReportPath: "report"},
		{OutputPath: "-", Headless: true, OutputBytes: 1},
		{OutputPath: "another.bin", Headless: true, ReportPath: "-"},
		{OutputDevice: "/dev/null"},
	} {
		cfg.DisplayInterval = time.Second
		if err := cfg.ValidateAnalysis(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWindowsCapabilitiesRejectUnsupportedRequests(t *testing.T) {
	for _, cfg := range []Config{
		{WriteOut: true},
		{OutputDevice: "/dev/null"},
		{OutputPath: "-", Headless: true},
	} {
		if err := cfg.validatePlatform("windows"); err == nil {
			t.Fatalf("unsupported Windows request silently accepted: %+v", cfg)
		}

		if err := cfg.validatePlatform("linux"); err != nil {
			t.Fatalf("Windows restriction affected Linux: %v", err)
		}
	}

	for _, cfg := range []Config{
		{},
		{Layout: "auto", Color: "auto", WaterfallMode: "byte"},
		{Layout: "compact", Color: "never", ShowDashboard: true},
		{OutputPath: "capture.bin", ReportPath: "report.jsonl", Headless: true},
		{ReportPath: "-", Headless: true},
		{ShowWaterfall: true, Layout: "waterfall", Color: "never", WaterfallMode: "gray"},
		{ShowWaterfall: true, Layout: "full", Color: "truecolor", WaterfallMode: "rgb", WaterfallColumns: 32, WaterfallRows: 8},
	} {
		if err := cfg.validatePlatform("windows"); err != nil {
			t.Fatalf("portable Windows request rejected: %+v, %v", cfg, err)
		}
	}
}
