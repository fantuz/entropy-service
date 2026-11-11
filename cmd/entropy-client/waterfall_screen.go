package main

import (
	"fmt"
	"io"
	"strings"
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
		entropy = fmt.Sprintf("%.5f", m.latest.Shannon)
		assessment = diag.ShannonAssessment(&m.latest, m.cfg.ShannonThreshold)
	}

	header := fmt.Sprintf("Throughput %.3f MiB/s | Shannon %s | threshold %.5f | assessment %s",
		m.stats.Rate/(1<<20), entropy, m.cfg.ShannonThreshold, assessment)
	if terminal.TextRows(header, profile.Columns) > 1 {
		header = fmt.Sprintf("RX %.3fMiB/s H %s min %.5f %s", m.stats.Rate/(1<<20), entropy, m.cfg.ShannonThreshold, assessment)
	}

	var screen strings.Builder
	fmt.Fprint(&screen, profile.Clear())
	fmt.Fprintln(&screen, terminal.Fit(header, profile.Columns))

	// Leave the physical last row free so a trailing newline cannot scroll.
	profile.Rows -= 2

	if m.waterfall != nil {
		rows := m.cfg.WaterfallRows
		if rows == 0 {
			rows = diag.MaxWaterfallRows
		}

		m.waterfall.RenderCanvas(&screen, profile, rows)
	}

	fmt.Fprint(out, screen.String())
}
