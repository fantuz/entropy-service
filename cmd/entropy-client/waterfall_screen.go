package main

import (
	"fmt"
	"io"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/diag"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/terminal"
)

func (m *monitorState) renderWaterfall(out io.Writer, profile terminal.Profile) {
	profile.Columns = min(profile.Columns, fullMaxColumns)
	profile.Rows = min(profile.Rows, fullMaxRows)

	if elapsed := time.Since(m.started); !m.started.IsZero() && elapsed > 0 {
		m.stats.Rate = float64(m.stats.TotalBytes) / elapsed.Seconds()
	}

	entropy, assessment := "--", diag.ShannonNoSample
	if m.hasSample {
		entropy = fmt.Sprintf("%.6f", m.latest.Shannon)
		assessment = diag.ShannonAssessment(&m.latest, m.cfg.ShannonThreshold)
	}

	header := waterfallHeader(profile, entropy, m.cfg.ShannonThreshold, assessment, m.stats.Rate/(1<<20))

	m.screen = append(m.screen[:0], profile.Clear()...)
	m.screen = append(m.screen, header...)
	m.screen = append(m.screen, '\n')

	// Leave the physical last row free so a trailing newline cannot scroll.
	profile.Rows -= 2

	if m.waterfall != nil {
		rows := m.cfg.WaterfallRows
		if rows == 0 {
			rows = diag.MaxWaterfallRows
		}

		m.screen = m.waterfall.AppendCanvas(m.screen, profile, rows)
	}

	_, _ = out.Write(m.screen)
}

func waterfallHeader(profile terminal.Profile, entropy string, threshold float64, assessment string, rate float64) string {
	right := fmt.Sprintf("%.3f MiB/s", rate)

	left := fmt.Sprintf("SHANNON: %s  THRESHOLD: %.6f  STATUS: ", entropy, threshold)
	if len(left)+len(assessment)+len(right)+1 > profile.Columns {
		left = fmt.Sprintf("H:%s MIN:%.6f ", entropy, threshold)
	}

	switch assessment {
	case diag.ShannonOK:
		assessment = profile.Paint(Green, assessment)
	case diag.ShannonWarn:
		assessment = profile.Paint(Red, assessment)
	}

	if len(right)+1 >= profile.Columns {
		return terminal.Fit(left+assessment, profile.Columns)
	}

	return terminal.Fit(left+assessment, profile.Columns-len(right)-1) + " " + right
}
