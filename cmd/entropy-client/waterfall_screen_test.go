package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/client"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/diag"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/terminal"
)

func TestWaterfallScreenFitsWideTallAndResizedTerminals(t *testing.T) {
	m := fullMonitor(t)
	m.cfg.Layout = terminal.LayoutWaterfall
	m.cfg.ShowPreview, m.cfg.ShowDebug, m.cfg.ShowMatrix = true, true, true
	m.waterfall = diag.NewWaterfall(diag.MaxWaterfallColumns, diag.MaxWaterfallRows, diag.WaterfallByte)

	data := make([]byte, 4096)
	for i := range data {
		data[i] = byte(i % 256)
	}

	for range 600 {
		m.waterfall.Add(data, 1)
	}

	frames, samples, analysedBytes := m.frames, m.samples, m.analysedBytes

	for _, size := range [][2]int{{80, 24}, {641, 37}, {100, 240}, {900, 180}, {1400, 700}, {47, 101}, {2, 3}, {80, 24}, {900, 180}} {
		for _, color := range []terminal.Color{terminal.Monochrome, terminal.ANSI16, terminal.ANSI256, terminal.TrueColor} {
			t.Run(fmt.Sprintf("%dx%d/%d", size[0], size[1], color), func(t *testing.T) {
				profile := terminal.Profile{Columns: size[0], Rows: size[1], Interactive: true, Color: color}

				var out strings.Builder
				m.renderTo(&out, profile)
				text := out.String()

				columns, rows := min(size[0], fullMaxColumns), min(size[1], fullMaxRows)
				if got := terminal.TextRows(text, columns); got != rows-1 {
					t.Fatalf("waterfall used %d rows, want %d", got, rows-1)
				}

				if color == terminal.TrueColor && strings.Count(text, "\x1b[48;2;") != (columns/2)*(rows-2) {
					t.Fatal("waterfall did not fill all available cells beyond the previous size limits")
				}

				lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
				for _, line := range lines {
					if terminal.TextRows(line, columns) != 1 {
						t.Fatalf("waterfall wrapped a row at %d columns", columns)
					}
				}

				if columns >= 47 {
					for _, want := range []string{"7.999000", "8.000000", "OK", "MiB/s"} {
						if !strings.Contains(lines[0], want) {
							t.Errorf("first line missing %q: %q", want, lines[0])
						}
					}
				}

				for _, unwanted := range []string{"Sample diagnostics", "Waterfall |", "Offsets", "#", "History", "Analysis", "Device", "Preview"} {
					if strings.Contains(text, unwanted) && (unwanted != "#" || color != terminal.Monochrome) {
						t.Errorf("waterfall-only screen contains %q", unwanted)
					}
				}
			})
		}
	}

	if m.frames != frames || m.samples != samples || m.analysedBytes != analysedBytes {
		t.Fatal("resizing changed reception or analysis accounting")
	}
}

func TestWaterfallScreenWaitingAssessmentAndPlainFallback(t *testing.T) {
	m := fullMonitor(t)
	m.cfg.Layout = terminal.LayoutWaterfall
	m.hasSample = false

	var out strings.Builder
	m.renderTo(&out, terminal.Profile{Columns: 120, Rows: 30, Interactive: true})

	if !strings.Contains(out.String(), "SHANNON: --") || !strings.Contains(out.String(), diag.ShannonNoSample) {
		t.Fatal("waiting screen reported an entropy assessment without a sample")
	}

	m.hasSample = true
	m.latest.Shannon = 7

	out.Reset()
	m.renderTo(&out, terminal.Profile{Columns: 120, Rows: 30, Interactive: true})

	if !strings.Contains(out.String(), "SHANNON: 7.000000") || !strings.Contains(out.String(), "STATUS: WARN") {
		t.Fatal("waterfall did not preserve the configured Shannon assessment")
	}

	out.Reset()
	m.renderTo(&out, terminal.Profile{Columns: 120, Rows: 30})

	if strings.ContainsRune(out.String(), '\x1b') || !strings.Contains(out.String(), "Sample diagnostics") {
		t.Fatal("noninteractive waterfall did not retain plain stacked output")
	}
}

func TestWaterfallHeaderAlignmentAndStatusColors(t *testing.T) {
	strip := strings.NewReplacer(Green, "", Red, "", Reset, "")
	for _, color := range []terminal.Color{terminal.Monochrome, terminal.ANSI16, terminal.ANSI256, terminal.TrueColor} {
		for _, tc := range []struct{ status, code string }{{diag.ShannonOK, Green}, {diag.ShannonWarn, Red}, {diag.ShannonNoSample, ""}} {
			profile := terminal.Profile{Columns: 120, Color: color}
			header := waterfallHeader(profile, "7.999901", 7.9999, tc.status, 123.456)
			plain := strip.Replace(header)

			prefix := "SHANNON: 7.999901  THRESHOLD: 7.999900  STATUS: " + tc.status
			if len(plain) != 120 || !strings.HasPrefix(plain, prefix) || !strings.HasSuffix(plain, "123.456 MiB/s") || strings.Contains(plain, "Throughput") {
				t.Fatalf("header positions changed: %q", plain)
			}

			if color == terminal.Monochrome || tc.code == "" {
				if strings.ContainsRune(header, '\x1b') {
					t.Fatalf("unstyled status emitted ANSI colors: %q", header)
				}
			} else if !strings.Contains(header, tc.code+tc.status+Reset) {
				t.Fatalf("status has the wrong color: %q", header)
			}
		}
	}
}

func TestWaterfallAssessmentUsesUnroundedEntropy(t *testing.T) {
	m := fullMonitor(t)
	m.cfg.Layout, m.cfg.ShannonThreshold = terminal.LayoutWaterfall, 7.9999
	m.latest.Shannon = 7.9998999

	var out strings.Builder
	m.renderTo(&out, terminal.Profile{Columns: 120, Rows: 24, Interactive: true, Color: terminal.TrueColor})

	if !strings.Contains(out.String(), "SHANNON: 7.999900  THRESHOLD: 7.999900  STATUS: "+Red+"WARN"+Reset) {
		t.Fatal("formatting rounded away a real threshold warning")
	}
}

func BenchmarkWaterfallScreen900x180(b *testing.B) {
	cfg := experimentConfig(modePull, "", "")
	cfg.Layout, cfg.ShowWaterfall = terminal.LayoutWaterfall, true
	cfg.AnalysisBytes, cfg.AnalysisInterval, cfg.Headless = 0, 0, false
	m := monitorState{cfg: cfg, started: time.Now().Add(-time.Second), waterfall: diag.NewWaterfall(512, 512, diag.WaterfallRGB)}

	data := make([]byte, 4096)
	for range 180 {
		m.observe(client.StreamFrame{Data: data}, time.Now())
	}

	profile := terminal.Profile{Columns: 900, Rows: 180, Interactive: true, Color: terminal.TrueColor}

	b.ReportAllocs()

	for b.Loop() {
		var out strings.Builder
		m.renderTo(&out, profile)
	}
}
