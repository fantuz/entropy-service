package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/client"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/config"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/diag"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/terminal"
)

func TestWaterfallUsesOnlySelectedAnalysisPrefixes(t *testing.T) {
	cfg := &config.Config{AnalysisBytes: 4, AnalysisInterval: 50 * time.Millisecond}
	m := monitorState{cfg: cfg, waterfall: diag.NewWaterfall(8, 2, diag.WaterfallGray)}
	start := time.Now()

	data := append([]byte{1, 2, 3, 4}, bytes.Repeat([]byte{255}, 60)...)
	m.observe(client.StreamFrame{Data: data}, start)
	clear(data) // Simulate recycling the transport's buffer immediately.
	m.observe(client.StreamFrame{Data: data}, start.Add(10*time.Millisecond))
	m.observe(client.StreamFrame{}, start.Add(50*time.Millisecond))
	m.observe(client.StreamFrame{Data: data}, start.Add(50*time.Millisecond))

	var out strings.Builder
	m.waterfall.Render(&out, terminal.Profile{Color: terminal.TrueColor})

	text := out.String()
	if strings.Count(text, "\x1b[48;2;") != 8 || strings.Contains(text, "\x1b[48;2;255;") || !strings.Contains(text, "\x1b[48;2;1;1;1m") {
		t.Fatalf("display retained skipped bytes, unanalysed bytes or recycled buffers: %q", text)
	}

	if !strings.Contains(text, "#     4") || !strings.Contains(text, "#     1") || strings.Contains(text, "#     2") || strings.Contains(text, "#     3") {
		t.Fatalf("waterfall rows do not match analysed frames: %q", text)
	}

	if m.frames != 4 || m.samples != 2 || m.analysedBytes != 8 || m.stats.TotalBytes != 192 {
		t.Fatalf("display changed reception or sampling: %+v", m.counts())
	}
}

func TestWaterfallSurvivesStreamBufferRecycling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var upgrader websocket.Upgrader

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		for i := byte(1); i <= 12; i++ {
			if err := conn.WriteMessage(websocket.BinaryMessage, bytes.Repeat([]byte{i}, 256)); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	frames, err := client.StreamEntropy(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"?bytes=256")
	if err != nil {
		t.Fatal(err)
	}

	m := monitorState{cfg: &config.Config{AnalysisBytes: 8}, waterfall: diag.NewWaterfall(8, 32, diag.WaterfallGray)}

	buffers := make(map[*byte]bool)
	for frame := range frames {
		buffers[&frame.Data[0]] = true
		m.observe(frame, time.Now())
		frame.Release()
	}

	if m.samples != 12 || len(buffers) >= 12 {
		t.Fatalf("fixture did not exercise buffer recycling: samples=%d buffers=%d", m.samples, len(buffers))
	}

	var out strings.Builder
	m.waterfall.Render(&out, terminal.Profile{Color: terminal.TrueColor})

	for i := 1; i <= 12; i++ {
		want := fmt.Sprintf("#%6d | ", i) + strings.Repeat(fmt.Sprintf("\x1b[48;2;%d;%d;%dm  ", i, i, i), 8)
		if !strings.Contains(out.String(), want) {
			t.Fatalf("frame %d changed after recycling: %q", i, out.String())
		}
	}
}

func TestMonitorWaterfallFitsTerminalAndRetainsHistory(t *testing.T) {
	cfg := &config.Config{ShowWaterfall: true, ShannonThreshold: 7.999}

	m := monitorState{cfg: cfg, started: time.Now().Add(-time.Second), waterfall: diag.NewWaterfall(128, 32, diag.WaterfallByte)}
	for i := range 32 {
		m.observe(client.StreamFrame{Data: bytes.Repeat([]byte{byte(i)}, 256)}, time.Now())
	}

	for _, columns := range []int{80, 60, 100, 80} {
		for _, color := range []terminal.Color{terminal.Monochrome, terminal.ANSI16, terminal.ANSI256, terminal.TrueColor} {
			t.Run(fmt.Sprintf("%dx24/%d", columns, color), func(t *testing.T) {
				var out strings.Builder
				m.renderTo(&out, terminal.Profile{Columns: columns, Rows: 24, Color: color, Interactive: true})

				text := out.String()
				if terminal.TextRows(text, columns) >= 24 || !strings.Contains(text, "#    32") || !strings.Contains(text, "Waterfall |") {
					t.Fatalf("monitor scrolled or lost newest sample: %q", text)
				}
			})
		}
	}

	var out strings.Builder
	m.renderTo(&out, terminal.Profile{Columns: 300, Rows: 80})

	if !strings.Contains(out.String(), "#     1") || !strings.Contains(out.String(), "Offsets [0,128)") {
		t.Fatal("resizing destroyed older rows or full-width prefixes")
	}

	cfg.ShowDebug = true

	out.Reset()
	m.renderTo(&out, terminal.Profile{Columns: 80, Rows: 24})

	if strings.Contains(out.String(), "Waterfall") || !strings.Contains(out.String(), "Last analysis") {
		t.Fatal("waterfall ignored the space used by other panels")
	}
}

func TestHeadlessMonitorDoesNotAllocateDisplayHistory(t *testing.T) {
	for _, mode := range []string{modePull, modeStream} {
		t.Run(mode, func(t *testing.T) {
			endpoint := serveExperiment(t, mode)
			cfg := experimentConfig(mode, endpoint, "")
			cfg.ShowWaterfall, cfg.ShowGraph, cfg.ShowDashboard, cfg.ShowPreview = true, true, true, true
			cfg.WaterfallColumns, cfg.WaterfallRows, cfg.WaterfallMode = 128, 32, diag.WaterfallRGB
			m := monitorState{cfg: cfg, started: time.Now()}

			reason, err := runMonitor(t.Context(), &m, nil, endpoint, endpoint)
			if err != nil || reason != reasonDuration || m.samples == 0 {
				t.Fatalf("headless monitor failed: reason=%s err=%v samples=%d", reason, err, m.samples)
			}

			if m.waterfall != nil || m.graph != nil || m.dashboard != nil || m.preview != "" {
				t.Fatal("headless mode allocated display history")
			}
		})
	}
}
