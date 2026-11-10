package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/client"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/config"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/device"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/diag"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/terminal"
)

type monitorState struct {
	cfg           *config.Config
	stats         Stats
	latest        diag.Diagnostics
	hasSample     bool
	frames        uint64
	samples       uint64
	analysedBytes uint64
	okSamples     uint64
	warnSamples   uint64
	deviceErrors  uint64
	sampleBytes   int
	preview       string
	started       time.Time
	nextAnalysis  time.Time
	sampleAt      time.Time
	lastReceive   time.Duration
	lastCycle     time.Duration
	receiveTime   time.Duration
	analysisTime  time.Duration
	graph         *diag.EntropyGraph
	dashboard     *diag.Dashboard
	waterfall     *diag.Waterfall
}

func analysePayload(data []byte, cfg *config.Config) diag.Diagnostics {
	if cfg.AnalysisBytes > 0 && len(data) > cfg.AnalysisBytes {
		data = data[:cfg.AnalysisBytes]
	}

	return diag.RunDiagnosticsWithMatrix(data, cfg.ShowMatrix)
}

// Reception is counted for every frame. Statistics describe only selected
// frames and their configured prefix; no unanalysed bytes are credited to them.
func (m *monitorState) observe(frame client.StreamFrame, now time.Time) bool {
	m.frames++
	m.stats.TotalBytes += int64(len(frame.Data))
	m.lastReceive = frame.ReceiveWait
	m.lastCycle = frame.CycleRuntime
	m.receiveTime += frame.ReceiveWait
	atomic.AddUint64(&diag.BytesFetched, uint64(len(frame.Data)))

	if len(frame.Data) == 0 || (m.cfg.AnalysisInterval > 0 && now.Before(m.nextAnalysis)) {
		return false
	}

	started := time.Now()
	m.latest = analysePayload(frame.Data, m.cfg)
	m.analysisTime = time.Since(started)
	m.sampleAt = now
	m.nextAnalysis = now.Add(m.cfg.AnalysisInterval)
	m.samples++
	m.analysedBytes += uint64(m.latest.N)
	m.sampleBytes = len(frame.Data)

	m.hasSample = true
	if diag.ShannonAssessment(&m.latest, m.cfg.ShannonThreshold) == diag.ShannonOK {
		m.okSamples++
	} else {
		m.warnSamples++
	}

	if m.cfg.ShowPreview && !m.cfg.Headless {
		m.preview = hex.EncodeToString(frame.Data[:min(64, len(frame.Data))])
	}

	if m.graph != nil {
		m.graph.Add(m.latest.Shannon)
	}

	if m.dashboard != nil {
		m.dashboard.Add(m.latest)
	}

	if m.waterfall != nil {
		m.waterfall.Add(frame.Data[:m.latest.N], m.frames)
	}

	return true
}

func (m *monitorState) render() {
	m.renderTo(os.Stdout, terminal.Detect(m.cfg.Color))
}

func (m *monitorState) renderTo(out io.Writer, profile terminal.Profile) {
	elapsed := time.Since(m.started)
	if !m.hasSample {
		fmt.Fprintln(out, "Receiving; waiting for an analysed sample")

		return
	}

	m.stats.Rate = float64(m.stats.TotalBytes) / elapsed.Seconds()

	var summary strings.Builder

	drawUI(&summary, &m.latest, m.cfg.ShannonThreshold, profile)
	fmt.Fprintf(&summary, "Received           : %d frames, %.3f MiB\n", m.frames, float64(m.stats.TotalBytes)/(1<<20))
	fmt.Fprintf(&summary, "Average received   : %.3f MiB/s\n", m.stats.Rate/(1<<20))
	fmt.Fprintf(&summary, "Analysis samples   : %d / %d received frames\n", m.samples, m.frames)
	fmt.Fprintf(&summary, "Analysed prefix    : %d / %d frame bytes\n", m.latest.N, m.sampleBytes)
	fmt.Fprintf(&summary, "Analysed total     : %.3f MiB\n", float64(m.analysedBytes)/(1<<20))
	fmt.Fprintf(&summary, "Sample age         : %.3f ms\n", time.Since(m.sampleAt).Seconds()*1000)
	fmt.Fprintln(&summary, "Statistics cover the last analysed sample.")

	if m.cfg.ShowMatrix {
		m.latest.TMatrix.PrintHeatmapTo(&summary)
	}

	if m.cfg.ShowPreview {
		fmt.Fprintln(&summary, "preview (hex):", m.preview)
	}

	if m.cfg.ShowGraph {
		m.graph.RenderTo(&summary)
	}

	if m.cfg.ShowDashboard {
		m.dashboard.RenderTo(&summary)
	}

	if m.cfg.ShowHistogram {
		diag.PrintHistogramCountsTo(&summary, m.latest.Histogram)
	}

	if m.cfg.ShowDebug {
		if m.cfg.Mode == modeStream {
			fmt.Fprintf(&summary, "Last WS receive    : %.3f ms\n", m.lastReceive.Seconds()*1000)
			fmt.Fprintf(&summary, "WS runtime         : %.3f ms\n", m.receiveTime.Seconds()*1000)
		} else {
			fmt.Fprintf(&summary, "Last HTTP fetch    : %.3f ms\n", m.lastReceive.Seconds()*1000)
			fmt.Fprintf(&summary, "HTTP cycle runtime : %.3f ms\n", m.lastCycle.Seconds()*1000)
			fmt.Fprintf(&summary, "HTTP fetch runtime : %.3f ms\n", m.receiveTime.Seconds()*1000)
		}

		fmt.Fprintf(&summary, "Last analysis      : %.3f ms\n", m.analysisTime.Seconds()*1000)
		fmt.Fprintf(&summary, "Total runtime      : %.3f ms\n", elapsed.Seconds()*1000)
	}

	fmt.Fprint(out, summary.String())

	if m.waterfall != nil {
		if profile.Rows > 0 {
			// Leave the cursor on the last screen row to avoid scrolling.
			profile.Rows -= terminal.TextRows(summary.String(), profile.Columns) + 1
			if profile.Rows < 1 {
				return
			}
		}

		m.waterfall.Render(out, profile)
	}
}

func runMonitor(parent context.Context, m *monitorState, report *runReport, httpURL, wsURL string) (string, error) {
	cfg := m.cfg

	ctx, cancel := context.WithCancel(parent)
	if cfg.Duration > 0 {
		cancel()

		ctx, cancel = context.WithDeadline(parent, m.started.Add(cfg.Duration))
	}

	defer cancel()

	diag.ClientRateMeter.Reset()
	atomic.StoreUint64(&diag.BytesFetched, 0)

	var frames <-chan client.StreamFrame

	failures := make(chan error, 1)

	if cfg.Mode == modeStream {
		var err error

		frames, err = client.StreamEntropy(ctx, wsURL)
		if err != nil {
			if contextEnded(ctx) {
				return stopReason(parent), nil
			}

			return reasonTransportError, fmt.Errorf("open entropy stream: %w", err)
		}
	} else {
		out := make(chan client.StreamFrame, 1)
		frames = out

		go pullFrames(ctx, cfg, httpURL, out, failures)
	}

	defer func() {
		cancel()

		for frame := range frames {
			frame.Release()
		}
	}()

	if cfg.ShowGraph && !cfg.Headless {
		m.graph = diag.NewEntropyGraph(60)
	}

	if cfg.ShowDashboard && !cfg.Headless {
		m.dashboard = diag.NewDashboard(60)
	}

	if cfg.ShowWaterfall && !cfg.Headless {
		m.waterfall = diag.NewWaterfall(cfg.WaterfallColumns, cfg.WaterfallRows, cfg.WaterfallMode)
	}

	display := time.NewTicker(cfg.DisplayInterval)
	defer display.Stop()

	for {
		select {
		case <-ctx.Done():
			return stopReason(parent), nil
		case <-display.C:
			if !cfg.Headless {
				m.render()
			}
		case frame, ok := <-frames:
			if !ok {
				if contextEnded(ctx) {
					return stopReason(parent), nil
				}

				select {
				case err := <-failures:
					return reasonTransportError, err
				default:
					return reasonTransportError, errors.New("entropy stream closed")
				}
			}

			if contextEnded(ctx) {
				frame.Release()

				return stopReason(parent), nil
			}

			if cfg.WriteOut && len(frame.Data) > 0 {
				if err := device.Write("/dev/urandom", frame.Data); err != nil {
					m.deviceErrors++

					log.Println("device write error:", err)
				}
			}

			analysed := m.observe(frame, time.Now())
			frame.Release()

			if analysed {
				if err := report.sample(m); err != nil {
					return reasonReportError, err
				}
			}
		}
	}
}

func stopReason(parent context.Context) string {
	if contextEnded(parent) {
		return reasonInterrupted
	}

	return reasonDuration
}

// Socket deadlines may fire just before the context timer is scheduled.
func contextEnded(ctx context.Context) bool {
	if ctx.Err() != nil {
		return true
	}

	deadline, ok := ctx.Deadline()

	return ok && !time.Now().Before(deadline)
}

func pullFrames(ctx context.Context, cfg *config.Config, endpoint string, out chan<- client.StreamFrame, failures chan<- error) {
	defer close(out)

	ticker := time.NewTicker(cfg.Refresh)
	defer ticker.Stop()

	for {
		cycleStarted := time.Now()

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		started := time.Now()
		requestCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		data, err := client.FetchEntropy(requestCtx, endpoint, cfg.Slice)
		wait := time.Since(started)

		cancel()

		if err != nil {
			if ctx.Err() == nil {
				failures <- fmt.Errorf("fetch entropy: %w", err)
			}

			return
		}

		select {
		case out <- client.StreamFrame{Data: data, ReceiveWait: wait, CycleRuntime: time.Since(cycleStarted)}:
		case <-ctx.Done():
			return
		}
	}
}
