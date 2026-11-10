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
