package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/terminal"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/tests"
)

const (
	fullMaxColumns = 320
	fullMaxRows    = 96
)

func graphCells(profile terminal.Profile) int {
	if profile.Columns > 0 {
		return max(1, min(60, profile.Columns-23))
	}

	return 60
}

func panelLines(text string) []string {
	return strings.Split(strings.Trim(text, "\n"), "\n")
}

func panelBorder(title string, width int) string {
	return "+" + terminal.Fit(" "+title+" ", width-2) + "+"
}

func panelRow(text string, width int) string {
	return "|" + terminal.Fit(text, width-2) + "|"
}

// Distribution is already part of the analysed sample; only its presentation
// changes. Two columns keep all 32 eight-value bins visible in 16 rows.
func (m *monitorState) histogramGrid(width int) string {
	var bins [32]int
	for value, count := range m.latest.Histogram {
		bins[value/8] += count
	}

	peak := 1
	for _, count := range bins {
		peak = max(peak, count)
	}

	half := (width - 2) / 2
	barWidth := max(1, half-18)
	entry := func(index int) string {
		length := int(int64(bins[index]) * int64(barWidth) / int64(peak))
		return fmt.Sprintf("%02x-%02x %7d %s", index*8, index*8+7, bins[index], strings.Repeat("█", length))
	}

	var out strings.Builder
	fmt.Fprintln(&out, "Byte distribution | counts, 8 values/bin")

	for i := range 16 {
		fmt.Fprintln(&out, terminal.Fit(entry(i), half)+"  "+entry(i+16))
	}

	return out.String()
}

func (m *monitorState) renderFull(out io.Writer, profile terminal.Profile) {
	columns, rows := min(profile.Columns, fullMaxColumns), min(profile.Rows, fullMaxRows)
	profile.Columns, profile.Rows = columns, rows

	var screen strings.Builder

	line := func(text string) { fmt.Fprintln(&screen, terminal.Fit(text, columns)) }

	fmt.Fprint(&screen, profile.Clear())

	transport := "HTTP pull"
	if m.cfg.Mode == modeStream {
		transport = "WebSocket stream"
	}

	line("EntropyCTL Monitor" + " | " + transport + fmt.Sprintf(" | full (%dx%d)", columns, rows))
	line("Sampled diagnostics and received throughput; Ctrl+C to stop")

	if !m.hasSample {
		line("Receiving; waiting for an analysed sample")
		fmt.Fprint(out, screen.String())

		return
	}

	elapsed := time.Since(m.started)
	if elapsed > 0 {
		m.stats.Rate = float64(m.stats.TotalBytes) / elapsed.Seconds()
	}

	leftWidth := min(58, columns/2)
	rightWidth := columns - leftWidth - 1

	var left, right strings.Builder
	tests.PrintDiagnosticsWithColor(&left, &m.latest, m.cfg.ShannonThreshold, profile.Color != terminal.Monochrome)
	fmt.Fprintf(&left, "\nReceived frames : %d\nReceived MiB    : %.3f\nAverage MiB/s   : %.3f\n", m.frames, float64(m.stats.TotalBytes)/(1<<20), m.stats.Rate/(1<<20))
	fmt.Fprintf(&left, "Analysis samples: %d / %d\nAnalysed prefix : %d / %d bytes\nAnalysed MiB    : %.3f\nSample age ms   : %.3f\n", m.samples, m.frames, m.latest.N, m.sampleBytes, float64(m.analysedBytes)/(1<<20), time.Since(m.sampleAt).Seconds()*1000)
	fmt.Fprintf(&left, "\nAnalysis & display policy\nAnalysis cap    : %d bytes (0 = full frame)\nAnalysis period : %s\nDisplay request : %s (terminal: <=20 Hz)\nRequested bytes : %d\nReceive interval: %s\n", m.cfg.AnalysisBytes, m.cfg.AnalysisInterval, m.cfg.DisplayInterval, m.cfg.Slice, m.cfg.Refresh)

	if m.cfg.WriteOut {
		fmt.Fprintf(&left, "Device mixed    : %d frames, %.3f MiB\nDevice errors   : %d\n", m.deviceFrames, float64(m.deviceBytes)/(1<<20), m.deviceErrors)
	} else if m.cfg.OutputEnabled() {
		fmt.Fprintf(&left, "Output bytes    : %d\nOutput frames   : %d full (%d errors)\n", m.outputBytes, m.outputFrames, m.outputErrors)
	}

	if m.cfg.ShowDebug {
		fmt.Fprintf(&left, "Receive wait ms : %.3f\nAnalysis ms     : %.3f\nRuntime seconds : %.3f\n", m.lastReceive.Seconds()*1000, m.analysisTime.Seconds()*1000, elapsed.Seconds())
	}

	if m.cfg.ShowPreview {
		fmt.Fprintf(&left, "\nPreview (hex, first 64 received bytes)\n%s\n", m.preview)
	}

	if m.cfg.ShowMatrix {
		m.latest.TMatrix.PrintHeatmapTo(&left)
	}

	if m.dashboard != nil {
		m.dashboard.RenderWidth(&right, max(1, rightWidth-25))
		fmt.Fprintln(&right, "History scales: H 7.995..8; p 0..1; r -0.02..0.02")
	} else {
		fmt.Fprintln(&right, "Sampled history: waiting for samples")
	}

	if m.cfg.ShowGraph && m.graph != nil {
		m.graph.RenderWidth(&right, max(1, rightWidth-4))
	}

	fmt.Fprintln(&right)
	fmt.Fprint(&right, m.histogramGrid(rightWidth-2))
	a, b := panelLines(left.String()), panelLines(right.String())
	// Reserve useful space for history when requested. All clipping is labelled.
	panelHeight := max(len(a), len(b))
	if m.waterfall != nil {
		panelHeight = min(panelHeight, max(17, rows-10))
	}

	panelHeight = min(panelHeight, rows-7)

	clipped := len(a) > panelHeight || len(b) > panelHeight
	for _, text := range a {
		clipped = clipped || terminal.TextRows(text, leftWidth-2) > 1
	}

	for _, text := range b {
		clipped = clipped || terminal.TextRows(text, rightWidth-2) > 1
	}

	line(panelBorder("Latest sample & reception", leftWidth) + " " + panelBorder("History & byte distribution", rightWidth))

	for i := range panelHeight {
		x, y := "", ""
		if i < len(a) {
			x = a[i]
		}

		if i < len(b) {
			y = b[i]
		}

		if i == panelHeight-1 && len(a) > panelHeight {
			x = "More sample details: use -layout=compact"
		}

		if i == panelHeight-1 && len(b) > panelHeight {
			y = "More history/distribution: enlarge terminal"
		}

		line(panelRow(x, leftWidth) + " " + panelRow(y, rightWidth))
	}

	line("+" + strings.Repeat("-", leftWidth-2) + "+ +" + strings.Repeat("-", rightWidth-2) + "+")

	used := terminal.TextRows(screen.String(), columns)
	if m.waterfall != nil && rows-used > 4 {
		view := profile
		view.Rows = rows - used - 2

		visibleRows := m.cfg.WaterfallRows
		if visibleRows == 0 {
			visibleRows = 64
		}

		m.waterfall.RenderView(&screen, view, columns, visibleRows)
	}

	used = terminal.TextRows(screen.String(), columns)
	for used < rows-2 {
		line("")

		used++
	}

	footer := "Last analysed sample only | histories: newest at right/top | -layout=compact for stacked output"
	if clipped {
		footer = "Panels clipped to fit | enlarge terminal or use -layout=compact for all selected output"
	}

	line(footer)
	fmt.Fprint(out, screen.String())
}
