package main

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/config"
)

// The acquisition goroutine reads only interval; smoothing/recovery belong to
// the monitor goroutine. Fixed pacing does not use feedback or change scheduling.
type acquisitionPacing struct {
	interval      atomic.Int64
	base, ceiling time.Duration
	smoothed      time.Duration
	recovery      int
}

func newAcquisitionPacing(cfg *config.Config) *acquisitionPacing {
	p := &acquisitionPacing{base: cfg.Refresh, ceiling: cfg.AdaptiveMaxInterval}
	p.interval.Store(int64(cfg.Refresh))

	return p
}

func (p *acquisitionPacing) observe(work time.Duration) {
	work = min(max(work, 0), time.Minute)
	if p.smoothed == 0 {
		p.smoothed = work
	} else {
		p.smoothed += (work - p.smoothed) / 5
	}

	target := max(p.base, min(p.ceiling, p.smoothed+p.smoothed/4))

	current := time.Duration(p.interval.Load())
	switch {
	case target > current:
		p.interval.Store(int64(target))
		p.recovery = 0
	case target < current-current/10 || target == p.base && current > p.base:
		p.recovery++
		if p.recovery >= 8 {
			p.interval.Store(int64(max(target, current-max(time.Millisecond, current/20))))
			p.recovery = 0
		}
	default:
		p.recovery = 0
	}
}

type performanceValues struct {
	RequestedFPS          float64 `json:"requested_fps"`
	EffectiveTargetFPS    float64 `json:"effective_target_fps"`
	AverageReceivedFPS    float64 `json:"average_received_fps"`
	RequestedMiBPerSecond float64 `json:"requested_mib_per_second"`
	EffectiveIntervalMS   float64 `json:"effective_interval_ms"`
	OutputMS              float64 `json:"output_ms"`
	TotalAnalysisMS       float64 `json:"total_analysis_ms"`
	PacingStatus          string  `json:"pacing_status"`
}

func (m *monitorState) performance(now time.Time) performanceValues {
	interval := m.cfg.Refresh
	if m.pacing != nil {
		interval = time.Duration(m.pacing.interval.Load())
	}

	p := performanceValues{PacingStatus: "warming up", OutputMS: m.outputTime.Seconds() * 1000, TotalAnalysisMS: m.totalAnalysis.Seconds() * 1000}
	if m.cfg.Refresh > 0 {
		p.RequestedFPS = 1 / m.cfg.Refresh.Seconds()
		p.RequestedMiBPerSecond = float64(m.cfg.Slice) / (1 << 20) / m.cfg.Refresh.Seconds()
	}

	if interval > 0 {
		p.EffectiveTargetFPS = 1 / interval.Seconds()
		p.EffectiveIntervalMS = interval.Seconds() * 1000
	}

	elapsed := now.Sub(m.started)
	if !m.started.IsZero() && elapsed > 0 {
		p.AverageReceivedFPS = float64(m.frames) / elapsed.Seconds()
	}

	if !m.started.IsZero() && elapsed >= max(time.Second, 2*m.cfg.Refresh) {
		p.PacingStatus = "on target"
		if p.AverageReceivedFPS < p.EffectiveTargetFPS*.85 {
			p.PacingStatus = "below target"
		}
	}

	if m.cfg.Adaptive && interval > m.cfg.Refresh {
		p.PacingStatus = "adaptive paced"
		if interval >= m.cfg.AdaptiveMaxInterval {
			p.PacingStatus = "adaptive ceiling"
		}
	}

	return p
}

func (m *monitorState) performanceText() string {
	p := m.performance(time.Now())
	return fmt.Sprintf("Acquisition FPS    : %.2f requested / %.2f effective / %.2f average received\nPacing             : %s; %.3f ms interval\nReceive / analysis : %.3f / %.3f ms (receive includes server wait and transfer)\nOutput write       : %.3f ms\n", p.RequestedFPS, p.EffectiveTargetFPS, p.AverageReceivedFPS, p.PacingStatus, p.EffectiveIntervalMS, m.lastReceive.Seconds()*1000, m.analysisTime.Seconds()*1000, m.outputTime.Seconds()*1000)
}
