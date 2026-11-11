package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"runtime"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/config"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/diag"
	"github.com/fantuz/entropy-service/internal/privatefile"
)

const reportSchemaVersion = 1

type reportHeader struct {
	SchemaVersion int       `json:"schema_version"`
	Type          string    `json:"type"`
	Timestamp     time.Time `json:"timestamp"`
}

func newReportHeader(kind string) reportHeader {
	return reportHeader{SchemaVersion: reportSchemaVersion, Type: kind, Timestamp: time.Now().UTC()}
}

// Each Encode writes a complete record immediately. Reports contain statistics,
// not raw entropy; a new file prevents accidentally replacing an earlier run.
type runReport struct {
	encoder *json.Encoder
	file    *os.File
}

func openReport(path string) (*runReport, error) {
	if path == "" {
		return nil, nil //nolint:nilnil // A nil reporter intentionally disables optional reporting.
	}

	if path == "-" {
		return &runReport{encoder: json.NewEncoder(os.Stdout)}, nil
	}

	f, err := privatefile.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create report: %w", err)
	}

	return &runReport{encoder: json.NewEncoder(f), file: f}, nil
}

func (r *runReport) write(record any) error {
	if r == nil {
		return nil
	}

	if err := r.encoder.Encode(record); err != nil {
		return fmt.Errorf("write report: %w", err)
	}

	return nil
}

func (r *runReport) close() error {
	if r == nil || r.file == nil {
		return nil
	}

	if err := errors.Join(r.file.Sync(), r.file.Close()); err != nil {
		return fmt.Errorf("close report: %w", err)
	}

	return nil
}

type reportSettings struct {
	Mode             string  `json:"mode"`
	Endpoint         string  `json:"endpoint"`
	PayloadBytes     int64   `json:"payload_bytes"`
	Refresh          string  `json:"refresh"`
	AnalysisBytes    int     `json:"analysis_bytes"`
	AnalysisInterval string  `json:"analysis_interval"`
	DisplayInterval  string  `json:"display_interval"`
	ShannonThreshold float64 `json:"shannon_threshold_bits_per_byte"`
	Duration         string  `json:"duration"`
	Headless         bool    `json:"headless"`
	Matrix           bool    `json:"matrix"`
	WriteOut         bool    `json:"writeout"`
	OutputKind       string  `json:"output_kind"`
	OutputLimit      int64   `json:"output_limit_bytes"`
	Waterfall        bool    `json:"waterfall"`
	WaterfallMode    string  `json:"waterfall_mode"`
	WaterfallColumns int     `json:"waterfall_columns"`
	WaterfallRows    int     `json:"waterfall_rows"`
	Color            string  `json:"color"`
	Layout           string  `json:"layout"`
	Retries          int     `json:"retries"`
	RetryDelay       string  `json:"retry_delay"`
}

type startRecord struct {
	reportHeader

	Settings  reportSettings `json:"settings"`
	GoVersion string         `json:"go_version"`
	GOOS      string         `json:"goos"`
	GOARCH    string         `json:"goarch"`
}

func (r *runReport) start(cfg *config.Config, endpoint string) error {
	if r == nil {
		return nil
	}

	endpoint, err := sanitizedEndpoint(endpoint)
	if err != nil {
		return fmt.Errorf("report endpoint: %w", err)
	}

	return r.write(startRecord{
		reportHeader: newReportHeader("run_start"),
		Settings: reportSettings{
			Mode: cfg.Mode, Endpoint: endpoint, PayloadBytes: cfg.Slice,
			Refresh: cfg.Refresh.String(), AnalysisBytes: cfg.AnalysisBytes,
			AnalysisInterval: cfg.AnalysisInterval.String(), DisplayInterval: cfg.DisplayInterval.String(),
			ShannonThreshold: cfg.ShannonThreshold, Duration: cfg.Duration.String(),
			Headless: cfg.Headless, Matrix: cfg.ShowMatrix, WriteOut: cfg.WriteOut,
			OutputKind: cfg.OutputKind(), OutputLimit: cfg.OutputBytes,
			Waterfall: cfg.ShowWaterfall, WaterfallMode: cfg.WaterfallMode,
			WaterfallColumns: cfg.WaterfallColumns, WaterfallRows: cfg.WaterfallRows, Color: cfg.Color,
			Layout:  cfg.Layout,
			Retries: cfg.Retries, RetryDelay: cfg.RetryDelay.String(),
		},
		GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
	})
}

type reportCounts struct {
	ReceivedFrames  uint64 `json:"received_frames"`
	ReceivedBytes   int64  `json:"received_bytes"`
	AnalysedSamples uint64 `json:"analysed_samples"`
	AnalysedBytes   uint64 `json:"analysed_bytes"`
	ShannonOK       uint64 `json:"shannon_ok_samples"`
	ShannonWarn     uint64 `json:"shannon_warn_samples"`
	DeviceErrors    uint64 `json:"device_write_errors"`
	DeviceBytes     uint64 `json:"device_written_bytes"`
	DeviceFrames    uint64 `json:"device_written_frames"`
	OutputBytes     uint64 `json:"output_written_bytes"`
	OutputFrames    uint64 `json:"output_written_frames"`
	OutputErrors    uint64 `json:"output_write_errors"`
}

func (m *monitorState) counts() reportCounts {
	return reportCounts{
		ReceivedFrames: m.frames, ReceivedBytes: m.stats.TotalBytes,
		AnalysedSamples: m.samples, AnalysedBytes: m.analysedBytes,
		ShannonOK: m.okSamples, ShannonWarn: m.warnSamples, DeviceErrors: m.deviceErrors,
		DeviceBytes: m.deviceBytes, DeviceFrames: m.deviceFrames,
		OutputBytes: m.outputBytes, OutputFrames: m.outputFrames, OutputErrors: m.outputErrors,
	}
}

type sampleRecord struct {
	reportHeader
	reportCounts

	ElapsedMS         float64 `json:"elapsed_ms"`
	FrameBytes        int     `json:"frame_bytes"`
	SampleBytes       int     `json:"sample_bytes"`
	Shannon           float64 `json:"shannon_bits_per_byte"`
	ShannonAssessment string  `json:"shannon_assessment"`
	ChiSquare         float64 `json:"chi_square"`
	ChiSquareP        float64 `json:"chi_square_p"`
	MonobitP          float64 `json:"monobit_p"`
	SerialCorrelation float64 `json:"serial_correlation"`
	SerialP           float64 `json:"serial_p"`
	ReceiveWaitMS     float64 `json:"receive_wait_ms"`
	AnalysisMS        float64 `json:"analysis_ms"`
}

func (r *runReport) sample(m *monitorState) error {
	if r == nil {
		return nil
	}

	return r.write(sampleRecord{
		reportHeader: newReportHeader("sample"), reportCounts: m.counts(),
		ElapsedMS:  time.Since(m.started).Seconds() * 1000,
		FrameBytes: m.sampleBytes, SampleBytes: m.latest.N,
		Shannon: m.latest.Shannon, ShannonAssessment: diag.ShannonAssessment(&m.latest, m.cfg.ShannonThreshold),
		ChiSquare: m.latest.Chi2, ChiSquareP: m.latest.Chi2P,
		MonobitP: m.latest.MonobitP, SerialCorrelation: m.latest.SerialR, SerialP: m.latest.SerialP,
		ReceiveWaitMS: m.lastReceive.Seconds() * 1000, AnalysisMS: m.analysisTime.Seconds() * 1000,
	})
}

type summaryRecord struct {
	reportHeader
	reportCounts

	Termination       string  `json:"termination"`
	Failed            bool    `json:"failed"`
	MeasurementBegan  bool    `json:"measurement_began"`
	ElapsedMS         float64 `json:"elapsed_ms"`
	TotalElapsedMS    float64 `json:"total_elapsed_ms"`
	ReceivedMiBPerSec float64 `json:"received_mib_per_second"`
	ShannonAssessment string  `json:"last_shannon_assessment"`
}

func (r *runReport) summary(m *monitorState, began time.Time, reason string, failed bool) error {
	if r == nil {
		return nil
	}

	var elapsed time.Duration
	if !m.started.IsZero() {
		elapsed = time.Since(m.started)
	}

	var rate float64
	if elapsed > 0 {
		rate = float64(m.stats.TotalBytes) / (1 << 20) / elapsed.Seconds()
	}

	return r.write(summaryRecord{
		reportHeader: newReportHeader("run_summary"), reportCounts: m.counts(),
		Termination: reason, Failed: failed, MeasurementBegan: !m.started.IsZero(),
		ElapsedMS: elapsed.Seconds() * 1000, TotalElapsedMS: time.Since(began).Seconds() * 1000,
		ReceivedMiBPerSec: rate, ShannonAssessment: diag.ShannonAssessment(&m.latest, m.cfg.ShannonThreshold),
	})
}

// Never disclose credentials, arbitrary query values or fragments in reports.
func sanitizedEndpoint(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}

	u.User, u.RawQuery, u.Fragment = nil, "", ""
	u.ForceQuery = false

	return u.String(), nil
}
