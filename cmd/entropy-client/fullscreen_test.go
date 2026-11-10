package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/client"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/config"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/diag"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/terminal"
)

func fullMonitor(t *testing.T) *monitorState {
	t.Helper()

	cfg := &config.Config{Layout: "auto", Mode: modeStream, ShowWaterfall: true, ShannonThreshold: 7.999}
	m := &monitorState{cfg: cfg, started: time.Now().Add(-time.Second), dashboard: diag.NewDashboard(256), waterfall: diag.NewWaterfall(256, 64, diag.WaterfallByte)}

	data := make([]byte, 4096)
	for i := range data {
		data[i] = byte(i % 256)
	}

	for i := range 100 {
		m.observe(client.StreamFrame{Data: data}, time.Now().Add(time.Duration(i)*time.Millisecond))
	}

	return m
}

func TestFullLayoutFitsAndRetainsSamplesAcrossResize(t *testing.T) {
	m := fullMonitor(t)

	for _, size := range [][2]int{{60, 24}, {80, 24}, {120, 30}, {160, 45}, {240, 60}, {500, 200}, {80, 24}, {240, 60}} {
		for _, color := range []terminal.Color{terminal.Monochrome, terminal.ANSI256, terminal.TrueColor} {
			t.Run(fmt.Sprintf("%dx%d/%d", size[0], size[1], color), func(t *testing.T) {
				profile := terminal.Profile{Columns: size[0], Rows: size[1], Color: color, Interactive: true}

				var out strings.Builder
				m.renderTo(&out, profile)
				text := out.String()

				columns, rows := size[0], size[1]
				if profile.FullLayout("auto") {
					columns, rows = min(columns, fullMaxColumns), min(rows, fullMaxRows)
				}

				if terminal.TextRows(text, columns) >= rows {
					t.Fatalf("monitor scrolled at %dx%d: %q", columns, rows, text)
				}

				for line := range strings.SplitSeq(text, "\n") {
					if terminal.TextRows(line, columns) > 1 {
						t.Fatalf("wrapped line: %q", line)
					}
				}

				if profile.FullLayout("auto") {
					for _, want := range []string{"Latest sample & reception", "History & byte distribution", "Shannon entropy", "Average MiB/s", "Waterfall |"} {
						if !strings.Contains(text, want) {
							t.Errorf("missing %q", want)
						}
					}
				}
			})
		}
	}

	var out strings.Builder
	m.waterfall.Render(&out, terminal.Profile{Columns: 600, Rows: 150})

	if !strings.Contains(out.String(), "#    37") || !strings.Contains(out.String(), "#   100") || !strings.Contains(out.String(), "Offsets [0,256)") {
		t.Fatal("resize discarded retained rows or wide byte prefixes")
	}

	if m.frames != 100 || m.samples != 100 || m.analysedBytes != 409600 {
		t.Fatal("display changed reception or analysis accounting")
	}
}

func TestFullModeWaitingOverloadAndPlainFallback(t *testing.T) {
	m := fullMonitor(t)
	m.cfg.Layout = "full"
	m.hasSample = false

	var out strings.Builder
	m.renderTo(&out, terminal.Profile{Columns: 160, Rows: 45, Interactive: true})

	if !strings.Contains(out.String(), "waiting for an analysed sample") {
		t.Fatal("waiting state disappeared")
	}

	m.hasSample = true
	m.cfg.ShowMatrix = true
	m.cfg.ShowDebug = true
	m.cfg.ShowPreview = true
	m.preview = strings.Repeat("ff", 64)

	out.Reset()
	m.renderTo(&out, terminal.Profile{Columns: 80, Rows: 24, Interactive: true})

	if !strings.Contains(out.String(), "Panels clipped") || terminal.TextRows(out.String(), 80) >= 24 {
		t.Fatal("overloaded small full view was not bounded/labelled")
	}

	out.Reset()

	m.cfg.ShowMatrix = false
	m.cfg.ShowPreview = false
	m.cfg.ShowDebug = false
	m.renderTo(&out, terminal.Profile{Columns: 240, Rows: 60})

	if strings.Contains(out.String(), "full (") || strings.ContainsRune(out.String(), '\x1b') {
		t.Fatal("forced full view emitted cursor controls into a pipe")
	}
}

func TestHistogramGridPreservesEveryBucketCount(t *testing.T) {
	m := fullMonitor(t)
	m.latest.Histogram = [256]int{}
	m.latest.Histogram[0], m.latest.Histogram[255] = 17, 99

	text := m.histogramGrid(150)
	if !strings.Contains(text, "00-07      17") || !strings.Contains(text, "f8-ff      99") || strings.Count(text, "\n") != 17 {
		t.Fatalf("distribution omitted counts/boundaries: %q", text)
	}

	if strings.Count(text, "█") == 0 {
		t.Fatal("distribution omitted bars")
	}

	m.latest.Histogram = [256]int{}

	m.latest.Histogram[0] = 1 << 25
	if text := m.histogramGrid(300); !strings.Contains(text, "33554432") || !strings.Contains(text, strings.Repeat("█", 131)) {
		t.Fatal("large histogram count overflowed wide bars")
	}
}

func TestFullLayoutShowsAcceptedDeviceProgress(t *testing.T) {
	m := fullMonitor(t)
	m.cfg.WriteOut = true
	m.deviceFrames, m.deviceBytes, m.deviceErrors = 7, 4096, 1

	var out strings.Builder
	m.renderTo(&out, terminal.Profile{Columns: 160, Rows: 45, Interactive: true})

	if !strings.Contains(out.String(), "Device mixed    : 7 frames, 0.004 MiB") || !strings.Contains(out.String(), "Device errors   : 1") {
		t.Fatal("full view omitted verified device progress")
	}
}

func BenchmarkFullMonitor240x60(b *testing.B) {
	cfg := &config.Config{Layout: "full", ShowWaterfall: true}
	m := &monitorState{cfg: cfg, started: time.Now().Add(-time.Second), dashboard: diag.NewDashboard(256), waterfall: diag.NewWaterfall(256, 64, diag.WaterfallRGB)}

	data := bytes.Repeat([]byte{2, 50, 200}, 4096)
	for range 256 {
		m.observe(client.StreamFrame{Data: data}, time.Now())
	}

	profile := terminal.Profile{Columns: 240, Rows: 60, Interactive: true, Color: terminal.TrueColor}

	b.ReportAllocs()

	for b.Loop() {
		var out strings.Builder
		m.renderTo(&out, profile)
	}
}
