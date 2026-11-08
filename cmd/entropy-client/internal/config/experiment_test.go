package config

import (
	"math"
	"testing"
	"time"
)

func TestExperimentOptionsValidation(t *testing.T) {
	for _, threshold := range []float64{0, 7.9, 7.999, 8} {
		cfg := Config{ShannonThreshold: threshold, DisplayInterval: time.Second}
		if err := cfg.ValidateAnalysis(); err != nil {
			t.Errorf("threshold=%g: %v", threshold, err)
		}
	}

	for _, threshold := range []float64{-0.1, 8.001, math.NaN(), math.Inf(1), math.Inf(-1)} {
		cfg := Config{ShannonThreshold: threshold, DisplayInterval: time.Second}
		if err := cfg.ValidateAnalysis(); err == nil {
			t.Errorf("accepted invalid threshold %g", threshold)
		}
	}

	cfg := Config{DisplayInterval: time.Second, Duration: -time.Second}
	if err := cfg.ValidateAnalysis(); err == nil {
		t.Error("accepted negative duration")
	}

	cfg.Duration, cfg.ReportPath = time.Second, "-"
	if err := cfg.ValidateAnalysis(); err == nil {
		t.Error("accepted JSONL stdout with interactive output")
	}

	cfg.Headless = true
	if err := cfg.ValidateAnalysis(); err != nil {
		t.Fatal(err)
	}
}
