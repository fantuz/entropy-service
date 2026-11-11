package config

import (
	"io"
	"testing"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/diag"
)

func TestDisplayFlags(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"-waterfall", "-waterfall-mode=rgb", "-waterfall-columns=128", "-waterfall-rows=32", "-color=256"},
	} {
		cfg, err := parseConfig(args, io.Discard)
		if err != nil {
			t.Fatal(err)
		}

		if err := cfg.ValidateAnalysis(); err != nil {
			t.Fatal(err)
		}

		if len(args) == 0 {
			if cfg.ShowWaterfall || cfg.WaterfallMode != "byte" || cfg.WaterfallColumns != 0 || cfg.WaterfallRows != 0 || cfg.Color != "auto" || cfg.Layout != "auto" {
				t.Fatalf("incorrect display defaults: %+v", cfg)
			}
		} else if !cfg.ShowWaterfall || cfg.WaterfallMode != "rgb" || cfg.WaterfallColumns != 128 || cfg.WaterfallRows != 32 || cfg.Color != "256" {
			t.Fatalf("display flags not parsed: %+v", cfg)
		}
	}
}

func TestDisplayValidation(t *testing.T) {
	valid := Config{DisplayInterval: time.Second, ShowWaterfall: true, WaterfallMode: "byte", WaterfallColumns: 32, WaterfallRows: 8, Color: "auto"}

	for _, tc := range []struct {
		name string
		edit func(*Config)
	}{
		{"color", func(c *Config) { c.Color = "rainbow" }},
		{"mode", func(c *Config) { c.WaterfallMode = "rgba" }},
		{"columns-low", func(c *Config) { c.WaterfallColumns = 7 }},
		{"columns-high", func(c *Config) { c.WaterfallColumns = diag.MaxWaterfallColumns + 1 }},
		{"rows-low", func(c *Config) { c.WaterfallRows = -1 }},
		{"rows-high", func(c *Config) { c.WaterfallRows = diag.MaxWaterfallRows + 1 }},
		{"layout", func(c *Config) { c.Layout = "huge" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := valid
			tc.edit(&cfg)

			if cfg.ValidateAnalysis() == nil {
				t.Fatalf("accepted invalid display setting: %+v", cfg)
			}
		})
	}

	for _, color := range []string{"auto", "always", "never", "256", "truecolor"} {
		for _, mode := range []string{"byte", "rgb", "gray"} {
			cfg := valid
			cfg.Color, cfg.WaterfallMode = color, mode

			cfg.WaterfallColumns, cfg.WaterfallRows = 8, 1
			if err := cfg.ValidateAnalysis(); err != nil {
				t.Fatal(err)
			}

			cfg.WaterfallColumns, cfg.WaterfallRows = diag.MaxWaterfallColumns, diag.MaxWaterfallRows
			if err := cfg.ValidateAnalysis(); err != nil {
				t.Fatal(err)
			}

			cfg.WaterfallColumns, cfg.WaterfallRows = 0, 0
			for _, layout := range []string{"auto", "compact", "full", "waterfall"} {
				cfg.Layout = layout
				if err := cfg.ValidateAnalysis(); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

func TestWaterfallLayoutEnablesWaterfall(t *testing.T) {
	cfg, err := parseConfig([]string{"-layout=waterfall", "-waterfall-columns=512", "-waterfall-rows=512"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	if err := cfg.ValidateAnalysis(); err != nil || !cfg.ShowWaterfall {
		t.Fatalf("waterfall layout did not enable its display: %+v, %v", cfg, err)
	}
}
