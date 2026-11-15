package main

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/client"
)

func TestReportWaterfallPrefixOptInBoundsAndFrameOwnership(t *testing.T) {
	for _, limit := range []int{0, 64, 1024} {
		cfg := experimentConfig(modePull, "http://example.invalid/data", "report.jsonl")
		cfg.ReportWaterfall = true
		cfg.AnalysisBytes = limit

		m := monitorState{cfg: cfg, started: time.Now()}
		data := make([]byte, 2048)

		for i := range data {
			data[i] = byte(i)
		}

		if !m.observe(client.StreamFrame{Data: data}, time.Now()) {
			t.Fatal("first sample was not analysed")
		}

		want := bytes.Clone(data[:min(m.latest.N, 256)])

		clear(data) // Model the transport recycling its frame before JSON Encode.

		if !bytes.Equal(m.reportPrefix, want) || len(m.reportPrefix) > 256 {
			t.Fatal("report retained a transport buffer or exceeded its prefix bound")
		}

		var out bytes.Buffer

		r := runReport{encoder: json.NewEncoder(&out)}
		if err := r.sample(t.Context(), &m); err != nil {
			t.Fatal(err)
		}

		var record sampleRecord
		if err := json.Unmarshal(out.Bytes(), &record); err != nil {
			t.Fatal(err)
		}

		if !bytes.Equal(record.WaterfallPrefix, want) || record.SampleBytes != m.latest.N || record.FrameBytes != len(data) {
			t.Fatalf("raw prefix/statistical window disagree: %+v", record)
		}

		if m.observe(client.StreamFrame{Data: []byte{255, 255}}, m.sampleAt) || !bytes.Equal(m.reportPrefix, want) {
			t.Fatal("an unanalysed frame changed the reported sample prefix")
		}
	}
}

func TestReportsRemainStatisticsOnlyWithoutWaterfallOptIn(t *testing.T) {
	cfg := experimentConfig(modePull, "http://example.invalid/data", "report.jsonl")
	m := monitorState{cfg: cfg, started: time.Now()}

	m.observe(client.StreamFrame{Data: []byte{0, 1, 2, 3}}, time.Now())

	if m.reportPrefix != nil {
		t.Fatal("disabled waterfall reporting allocated/copied raw data")
	}

	var out bytes.Buffer

	r := runReport{encoder: json.NewEncoder(&out)}
	if err := r.start(t.Context(), cfg, cfg.HTTPUrl); err != nil {
		t.Fatal(err)
	}

	if err := r.sample(t.Context(), &m); err != nil {
		t.Fatal(err)
	}

	if bytes.Contains(out.Bytes(), []byte("waterfall_prefix_base64")) || bytes.Contains(out.Bytes(), []byte("report_waterfall")) {
		t.Fatal("normal reports acquired raw-data fields")
	}

	cfg.ReportWaterfall = true

	out.Reset()

	if err := r.start(t.Context(), cfg, cfg.HTTPUrl); err != nil {
		t.Fatal(err)
	}

	if !bytes.Contains(out.Bytes(), []byte(`"report_waterfall":true`)) {
		t.Fatal("raw-data opt-in missing from run-start settings")
	}
}
