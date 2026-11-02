package config

import (
	"testing"
	"time"
)

func TestAnalysisLimits(t *testing.T) {
	for _, cfg := range []Config{
		{AnalysisBytes: -1, DisplayInterval: time.Second},
		{AnalysisBytes: 1<<25 + 1, DisplayInterval: time.Second},
		{AnalysisInterval: -1, DisplayInterval: time.Second},
		{DisplayInterval: 0},
	} {
		if cfg.ValidateAnalysis() == nil {
			t.Fatalf("invalid configuration accepted: %+v", cfg)
		}
	}

	for _, limit := range []int{0, 1, 1 << 25} {
		cfg := Config{AnalysisBytes: limit, DisplayInterval: time.Millisecond}
		if err := cfg.ValidateAnalysis(); err != nil {
			t.Fatal(err)
		}
	}
}
