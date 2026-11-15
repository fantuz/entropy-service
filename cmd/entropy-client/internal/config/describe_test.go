package config

import (
	"io"
	"testing"
)

func TestDescriptionAndWaterfallReportOptions(t *testing.T) {
	cfg, err := parseConfig([]string{"-describe-json", "-headless", "-report=-", "-report-waterfall"}, io.Discard)
	if err != nil || !cfg.DescribeJSON || !cfg.ReportWaterfall {
		t.Fatalf("GUI options not parsed: %+v, %v", cfg, err)
	}

	if err := cfg.ValidateAnalysis(); err != nil {
		t.Fatal(err)
	}

	cfg.ReportPath = ""
	if err := cfg.ValidateAnalysis(); err == nil {
		t.Fatal("raw-prefix reporting accepted without a report destination")
	}
}
