package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/client"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/config"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/device"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/diag"
)

type monitorState struct {
	cfg           *config.Config
	stats         Stats
	latest        diag.Diagnostics
	hasSample     bool
	frames        uint64
	samples       uint64
	analysedBytes uint64
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
}

func analysePayload(data []byte, cfg *config.Config) diag.Diagnostics {
	if cfg.AnalysisBytes > 0 && len(data) > cfg.AnalysisBytes {
		data = data[:cfg.AnalysisBytes]
	}

	return diag.RunDiagnosticsWithMatrix(data, cfg.ShowMatrix)
}

// Reception is counted for every frame. Statistics describe only selected
// frames and their configured prefix; no unanalysed bytes are credited to them.
func (m *monitorState) observe(frame client.StreamFrame, now time.Time) {
	m.frames++
	m.stats.TotalBytes += int64(len(frame.Data))
	m.lastReceive = frame.ReceiveWait
	m.lastCycle = frame.CycleRuntime
	m.receiveTime += frame.ReceiveWait
	atomic.AddUint64(&diag.BytesFetched, uint64(len(frame.Data)))

	if m.cfg.AnalysisInterval > 0 && now.Before(m.nextAnalysis) {
		return
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
	m.stats.Entropy = m.latest.Shannon
	m.stats.Hist = m.latest.Histogram
	m.preview = hex.EncodeToString(frame.Data[:min(64, len(frame.Data))])

	if m.cfg.ShowGraph {
		m.graph.Add(m.latest.Shannon)
	}

	if m.cfg.ShowDashboard {
		m.dashboard.Add(m.latest)
	}
}

func (m *monitorState) render() {
	elapsed := time.Since(m.started)
	if !m.hasSample {
		fmt.Println("Receiving; waiting for an analysed sample")

		return
	}

	m.stats.Rate = float64(m.stats.TotalBytes) / elapsed.Seconds()
	drawUI(&m.stats, &m.latest)
	fmt.Printf("Received           : %d frames, %.3f MiB\n", m.frames, float64(m.stats.TotalBytes)/(1<<20))
	fmt.Printf("Average received   : %.3f MiB/s\n", m.stats.Rate/(1<<20))
	fmt.Printf("Analysis samples   : %d / %d received frames\n", m.samples, m.frames)
	fmt.Printf("Analysed prefix    : %d / %d bytes in last sampled frame\n", m.latest.N, m.sampleBytes)
	fmt.Printf("Analysed total     : %.3f MiB\n", float64(m.analysedBytes)/(1<<20))
	fmt.Printf("Sample age         : %.3f ms\n", time.Since(m.sampleAt).Seconds()*1000)
	fmt.Println("Statistics describe the last analysed sample, not a cumulative stream test.")

	if m.cfg.ShowMatrix {
		m.latest.TMatrix.PrintHeatmap()
	}

	if m.cfg.ShowPreview {
		fmt.Println("preview (hex):", m.preview)
	}

	if m.cfg.ShowGraph {
		m.graph.Render()
	}

	if m.cfg.ShowDashboard {
		m.dashboard.Render()
	}

	if m.cfg.ShowHistogram {
		diag.PrintHistogramCounts(m.latest.Histogram)
	}

	if m.cfg.ShowDebug {
		if m.cfg.Mode == modeStream {
			fmt.Printf("Last WS receive    : %.3f ms\n", m.lastReceive.Seconds()*1000)
			fmt.Printf("WS runtime         : %.3f ms\n", m.receiveTime.Seconds()*1000)
		} else {
			fmt.Printf("Last HTTP fetch    : %.3f ms\n", m.lastReceive.Seconds()*1000)
			fmt.Printf("HTTP cycle runtime : %.3f ms\n", m.lastCycle.Seconds()*1000)
			fmt.Printf("HTTP fetch runtime : %.3f ms\n", m.receiveTime.Seconds()*1000)
		}

		fmt.Printf("Last analysis      : %.3f ms\n", m.analysisTime.Seconds()*1000)
		fmt.Printf("Total runtime      : %.3f ms\n", elapsed.Seconds()*1000)
	}
}

func runMonitor(parent context.Context, cfg *config.Config, httpURL, wsURL string) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	diag.ClientRateMeter.Reset()
	atomic.StoreUint64(&diag.BytesFetched, 0)

	var frames <-chan client.StreamFrame

	failures := make(chan error, 1)

	if cfg.Mode == modeStream {
		var err error

		frames, err = client.StreamEntropy(ctx, wsURL)
		if err != nil {
			return fmt.Errorf("open entropy stream: %w", err)
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

	m := monitorState{cfg: cfg, started: time.Now(), graph: diag.NewEntropyGraph(60), dashboard: diag.NewDashboard(60)}

	display := time.NewTicker(cfg.DisplayInterval)
	defer display.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-display.C:
			m.render()
		case frame, ok := <-frames:
			if !ok {
				if ctx.Err() != nil {
					return nil
				}

				select {
				case err := <-failures:
					return err
				default:
					return errors.New("entropy stream closed")
				}
			}

			if cfg.WriteOut && len(frame.Data) > 0 {
				if err := device.Write("/dev/urandom", frame.Data); err != nil {
					fmt.Println("device write error:", err)
				}
			}

			m.observe(frame, time.Now())
			frame.Release()
		}
	}
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
