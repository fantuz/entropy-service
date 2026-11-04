package main

import (
	"context"
	"sync"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/metrics"
)

// activityPeakCounters are cumulative server counters, not browser samples.
type activityPeakCounters struct {
	generatedBytes uint64
	wsFrames       uint64
	httpRequests   uint64
	wsRejections   uint64
	httpRejections uint64
}

type activityPeakValues struct {
	GeneratedBytesPerSecond float64
	WSFramesPerSecond       float64
	HTTPRequestsPerSecond   float64
	WSRejectionsPerMinute   float64
	HTTPRejectionsPerMinute float64
}

// activityPeakMonitor records lifetime maximum *observed interval averages*.
// It adds one low-frequency sampler, not instrumentation in the DRBG hot path.
type activityPeakMonitor struct {
	mu       sync.RWMutex
	previous activityPeakCounters
	last     time.Time
	peak     activityPeakValues
}

func (m *activityPeakMonitor) observe(now time.Time, current activityPeakCounters) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.last.IsZero() {
		m.last, m.previous = now, current
		return
	}

	seconds := now.Sub(m.last).Seconds()
	if seconds <= 0 {
		return
	}

	observe := func(current, previous uint64, peak *float64, scale float64) {
		if current < previous {
			return // Counter reset: do not interpret underflow as a rate spike.
		}

		*peak = max(*peak, float64(current-previous)/seconds*scale)
	}

	observe(current.generatedBytes, m.previous.generatedBytes, &m.peak.GeneratedBytesPerSecond, 1)
	observe(current.wsFrames, m.previous.wsFrames, &m.peak.WSFramesPerSecond, 1)
	observe(current.httpRequests, m.previous.httpRequests, &m.peak.HTTPRequestsPerSecond, 1)
	observe(current.wsRejections, m.previous.wsRejections, &m.peak.WSRejectionsPerMinute, 60)
	observe(current.httpRejections, m.previous.httpRejections, &m.peak.HTTPRejectionsPerMinute, 60)

	m.previous = current
	m.last = now
}

func (m *activityPeakMonitor) snapshot() activityPeakValues {
	if m == nil {
		return activityPeakValues{}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.peak
}

func readActivityCounters(ws *webSocketBudget, httpBudget *httpRequestBudget) activityPeakCounters {
	wsUsage := ws.usage()
	httpUsage := httpBudget.usage()

	return activityPeakCounters{
		generatedBytes: metrics.BytesGenerated(),
		wsFrames:       metrics.NumWSPayloads(),
		httpRequests:   metrics.NumHTTPRequests(),
		wsRejections:   wsUsage.RejectedConnections + wsUsage.RejectedMemory + wsUsage.RejectedSize,
		httpRejections: httpUsage.RejectedRequests + httpUsage.RejectedMemory + httpUsage.RejectedSize,
	}
}

func startActivityPeakMonitor(ctx context.Context, ws *webSocketBudget, httpBudget *httpRequestBudget) *activityPeakMonitor {
	monitor := &activityPeakMonitor{}
	monitor.sample(ws, httpBudget)

	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				monitor.sample(ws, httpBudget)
			case <-ctx.Done():
				return
			}
		}
	}()

	return monitor
}

// sample timestamps the actual completed counter read, not the scheduled tick.
func (m *activityPeakMonitor) sample(ws *webSocketBudget, httpBudget *httpRequestBudget) {
	current := readActivityCounters(ws, httpBudget)
	m.observe(time.Now(), current)
}
