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

func TestAdaptivePacingAndCapabilityFlagsAreExplicit(t *testing.T) {
	cfg, err := parseConfig([]string{"-mode=http", "-adaptive", "-refresh=50ms", "-adaptive-max-interval=2s", "-capabilities=required"}, nil)
	if err != nil || cfg.ValidateAnalysis() != nil {
		t.Fatal("valid pull pacing rejected", err)
	}

	for _, change := range []func(){
		func() { cfg.Mode = "push" },
		func() { cfg.AdaptiveMaxInterval = time.Millisecond },
		func() { cfg.AdaptiveMaxInterval = 2 * time.Minute },
		func() { cfg.Capabilities = "guess" },
	} {
		saved := *cfg

		change()

		if cfg.ValidateAnalysis() == nil {
			t.Fatal("invalid adaptive/discovery configuration accepted")
		}

		*cfg = saved
	}
}
