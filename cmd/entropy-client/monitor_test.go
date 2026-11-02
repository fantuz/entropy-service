package main

import (
	"bytes"
	"crypto/rand"
	"testing"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/client"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/config"
)

func TestMonitorCountsAllBytesButOnlyAnalysesSelectedPrefixes(t *testing.T) {
	cfg := &config.Config{AnalysisBytes: 8, AnalysisInterval: 50 * time.Millisecond}
	m := monitorState{cfg: cfg}
	start := time.Now()

	data := append(bytes.Repeat([]byte{1}, 8), make([]byte, 56)...)
	for i := range 10 {
		m.observe(client.StreamFrame{Data: data}, start.Add(time.Duration(i)*10*time.Millisecond))
	}

	if m.frames != 10 || m.stats.TotalBytes != 640 || m.samples != 2 || m.analysedBytes != 16 {
		t.Fatalf("received and analysed counts mixed: frames=%d, bytes=%d, samples=%d, analysed=%d", m.frames, m.stats.TotalBytes, m.samples, m.analysedBytes)
	}

	if m.latest.N != 8 || m.sampleBytes != 64 || m.latest.Histogram[1] != 8 || m.latest.Histogram[0] != 0 {
		t.Fatal("statistics did not describe only the selected prefix")
	}

	if m.latest.TMatrix.Total != 0 {
		t.Fatal("matrix was computed while disabled")
	}

	cfg.AnalysisBytes, cfg.AnalysisInterval, cfg.ShowMatrix = 0, 0, true

	for i := range 3 {
		m.observe(client.StreamFrame{Data: data}, start.Add(time.Second+time.Duration(i)))
	}

	if m.samples != 5 || m.analysedBytes != 16+3*64 || m.latest.N != 64 || m.latest.TMatrix.Total != 63 {
		t.Fatal("explicit full-frame, every-frame analysis did not analyse all data")
	}
}

var analysisBenchmarkSink int

func BenchmarkClientAnalysis16MiB(b *testing.B) {
	data := make([]byte, 16<<20)
	if _, err := rand.Read(data); err != nil {
		b.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		limit  int
		matrix bool
	}{
		{"FullWithMatrix", 0, true},
		{"FullWithoutMatrix", 0, false},
		{"Default1MiBPrefix", 1 << 20, false},
	} {
		b.Run(tc.name, func(b *testing.B) {
			cfg := &config.Config{AnalysisBytes: tc.limit, ShowMatrix: tc.matrix}

			b.ReportAllocs()

			for b.Loop() {
				analysisBenchmarkSink = analysePayload(data, cfg).N
			}
		})
	}
}
