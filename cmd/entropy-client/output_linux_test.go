package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRawStdoutExportPreservesBytesAndDescriptorOwnership(t *testing.T) {
	for _, mode := range []string{modePull, modeStream} {
		t.Run(mode, func(t *testing.T) {
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			defer writer.Close()

			original := os.Stdout

			os.Stdout = writer
			defer func() { os.Stdout = original }()

			path := filepath.Join(t.TempDir(), "stdout.jsonl")
			cfg := experimentConfig(mode, serveExperiment(t, mode), path)
			cfg.OutputPath, cfg.OutputBytes = "-", 700
			cfg.AnalysisInterval, cfg.Duration = time.Hour, 2*time.Second
			cfg.Color, cfg.Layout = "truecolor", "full"
			cfg.ShowPreview, cfg.ShowWaterfall = true, true

			if err := runConfigured(t.Context(), cfg); err != nil {
				t.Fatal(err)
			}

			os.Stdout = original

			// Export owns a duplicate; the caller can still write to stdout.
			const sentinel = "caller still owns stdout"
			if n, err := writer.WriteString(sentinel); err != nil || n != len(sentinel) {
				t.Fatalf("export closed the caller's stdout: %d bytes, %v", n, err)
			}

			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}

			data, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}

			want := make([]byte, 700+len(sentinel))
			for i := range want[:700] {
				want[i] = byte(i % 256)
			}

			copy(want[700:], sentinel)

			if !bytes.Equal(data, want) {
				t.Fatal("stdout differs from raw payload bytes followed by the caller's sentinel")
			}

			records := readReport(t, path)
			start := decodeRecord[startRecord](t, records[0])

			summary := decodeRecord[summaryRecord](t, records[len(records)-1])
			if start.Settings.OutputKind != "stdout" || start.Settings.OutputLimit != 700 || summary.Termination != reasonOutputLimit || summary.Failed {
				t.Fatalf("stdout export policy or termination: start=%+v summary=%+v", start.Settings, summary)
			}

			if summary.OutputBytes != 700 || summary.OutputFrames != 2 || summary.OutputErrors != 0 || summary.DeviceBytes != 0 || summary.DeviceFrames != 0 || summary.DeviceErrors != 0 || summary.AnalysedSamples != 1 || summary.ReceivedFrames != 3 || summary.ReceivedBytes != 768 {
				t.Fatalf("stdout export confused output, device, reception or sampling counts: %+v", summary)
			}
		})
	}
}
