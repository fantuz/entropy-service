package main

import (
	"io"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/config"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/diag"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/terminal"
)

// The same benchmark file can be copied to v2.0.3 for an unchanged comparison.
// Each redraw adds a varying row, so caching is exercised with live updates.
func BenchmarkWaterfallLive900x180(b *testing.B) {
	for _, sink := range []string{"builder", "discard"} {
		b.Run(sink, func(b *testing.B) {
			cfg := &config.Config{Layout: terminal.LayoutWaterfall}
			m := monitorState{cfg: cfg, started: time.Now(), hasSample: true, waterfall: diag.NewWaterfall(512, 512, diag.WaterfallRGB)}

			var corpus [256][1536]byte

			rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // Deterministic, non-secret benchmark data, never a seed source.

			for i := range corpus {
				for j := range corpus[i] {
					corpus[i][j] = byte(rng.Uint64())
				}
			}

			profile := terminal.Profile{Columns: 900, Rows: 180, Interactive: true, Color: terminal.TrueColor}

			for i := range 512 {
				m.waterfall.Add(corpus[i%256][:], uint64(i))
			}

			prime := profile
			prime.Rows = 512

			var warmup strings.Builder
			m.waterfall.RenderCanvas(&warmup, prime, 512)
			m.renderTo(io.Discard, profile)

			frame := uint64(512)

			b.ReportAllocs()

			for b.Loop() {
				m.waterfall.Add(corpus[frame%256][:], frame)
				frame++

				if sink == "builder" {
					var out strings.Builder
					m.renderTo(&out, profile)
				} else {
					m.renderTo(io.Discard, profile)
				}
			}
		})
	}
}
