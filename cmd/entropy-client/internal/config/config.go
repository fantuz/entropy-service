package config

import (
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/diag"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/terminal"
)

type Config struct {
	// ReseedMs         int
	// ReseedSize       int
	// MaxBytes         int
	MaxWords    int
	RefreshRate int
	// BytesFingerprint int
	Refresh          time.Duration
	HTTPUrl          string
	WSUrl            string
	Mode             string
	CertFile         string
	KeyFile          string
	LogLevel         string
	WriteOut         bool
	OutputPath       string
	OutputDevice     string
	OutputBytes      int64
	ShowDebug        bool
	ShowPreview      bool
	ShowGraph        bool
	ShowMatrix       bool
	ShowDashboard    bool
	ShowHistogram    bool
	ShowWaterfall    bool
	WaterfallMode    string
	WaterfallColumns int
	WaterfallRows    int
	Color            string
	Layout           string
	Slice            int64
	AnalysisBytes    int
	AnalysisInterval time.Duration
	DisplayInterval  time.Duration
	ShannonThreshold float64
	Duration         time.Duration
	ReportPath       string
	Headless         bool
}

func ParseConfig() *Config {
	cfg := &Config{}

	flag.Int64Var(&cfg.Slice, "slice", 2097152, "raw bytes per response/message (1..33554432)")
	flag.BoolVar(&cfg.WriteOut, "writeout", false, "Linux only: mix received bytes into /dev/urandom without entropy credit; fail on device errors (normally unnecessary)")
	flag.StringVar(&cfg.OutputPath, "output", "", "export raw received bytes to a new 0600 file; - uses stdout with -headless (e.g. an SSH pipe)")
	flag.StringVar(&cfg.OutputDevice, "output-device", "", "Linux only: export to a validated /dev/random, /dev/urandom or /dev/null device; no entropy credit")
	flag.Int64Var(&cfg.OutputBytes, "output-bytes", 0, "stop after exporting this many bytes (0 = until duration/interruption); requires an output destination or -writeout")
	flag.BoolVar(&cfg.ShowDebug, "debug", false, "print debug information")

	flag.BoolVar(&cfg.ShowPreview, "preview", false, "print hex preview of first 64 bytes")
	flag.BoolVar(&cfg.ShowMatrix, "matrix", false, "print 64x64 matrix")
	flag.BoolVar(&cfg.ShowGraph, "graph", false, "print distribution graph")
	flag.BoolVar(&cfg.ShowDashboard, "dashboard", false, "print dashboard summaries")
	flag.BoolVar(&cfg.ShowHistogram, "histogram", false, "print extended histogram")
	flag.BoolVar(&cfg.ShowWaterfall, "waterfall", false, "show recent analysed sample prefixes as a terminal waterfall")
	flag.StringVar(&cfg.WaterfallMode, "waterfall-mode", diag.WaterfallByte, "waterfall mapping: byte (vivid hue)|rgb (triplets)|gray")
	flag.IntVar(&cfg.WaterfallColumns, "waterfall-columns", 0, "waterfall cells per row (0 = fit layout; otherwise 8..256; two terminal columns per cell)")
	flag.IntVar(&cfg.WaterfallRows, "waterfall-rows", 0, "waterfall history (0 = fit layout; otherwise 1..64; newest first)")
	flag.StringVar(&cfg.Color, "color", "auto", "terminal colors: auto|always|never|256|truecolor; auto respects NO_COLOR, TERM and redirected stdout")
	flag.StringVar(&cfg.Layout, "layout", terminal.LayoutAuto, "terminal layout: auto|compact|full; auto uses panels on large interactive terminals")

	flag.DurationVar(&cfg.Refresh, "refresh", time.Second, "refresh interval for both HTTP and WS")
	flag.IntVar(&cfg.AnalysisBytes, "analysis-bytes", 1<<20, "maximum prefix bytes per analysed frame; 0 analyses the full frame")
	flag.DurationVar(&cfg.AnalysisInterval, "analysis-interval", 250*time.Millisecond, "minimum interval between analysed frames; 0 analyses every frame")
	flag.DurationVar(&cfg.DisplayInterval, "display-interval", time.Second, "terminal redraw interval, independent of reception (interactive maximum 20 FPS)")
	flag.Float64Var(&cfg.ShannonThreshold, "shannon-threshold", diag.DefaultShannonThreshold, "minimum empirical Shannon entropy for OK, in bits/byte (0..8)")
	flag.DurationVar(&cfg.Duration, "duration", 0, "measurement duration, excluding interactive preflight (0 = until interrupted)")
	flag.StringVar(&cfg.ReportPath, "report", "", "write JSONL to a new file; - writes to stdout with -headless")
	flag.BoolVar(&cfg.Headless, "headless", false, "disable terminal output and interactive preflight")
	flag.StringVar(&cfg.Mode, "mode", "pull", "Preferred mode: pull|stream (HTTP vs websocket)")

	flag.StringVar(&cfg.HTTPUrl, "url", "http://127.0.0.1:8080/v1/data/random", "HTTP entropy endpoint (supports existing query parameters)")
	flag.StringVar(&cfg.WSUrl, "wsurl", "ws://127.0.0.1:8080/stream", "WS entropy endpoint (supports existing query parameters)")

	/*
		flag.IntVar(&cfg.MaxWords, "words", 20, "Default number of words presented by /words endpoint (maximum 20)")
		fps := strconv.FormatInt(refresh.Milliseconds(), 10)
	*/

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Entropy Client\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n  %s [options]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	return cfg
}

func (cfg *Config) ValidateAnalysis() error {
	if err := cfg.validateOutput(); err != nil {
		return err
	}

	if cfg.Layout != "" && cfg.Layout != terminal.LayoutAuto && cfg.Layout != terminal.LayoutCompact && cfg.Layout != terminal.LayoutFull {
		return errors.New("layout must be auto, compact or full")
	}

	if !terminal.ValidColor(cfg.Color) {
		return errors.New("color must be auto, always, never, 256 or truecolor")
	}

	if cfg.WaterfallMode != "" && cfg.WaterfallMode != diag.WaterfallByte && cfg.WaterfallMode != diag.WaterfallRGB && cfg.WaterfallMode != diag.WaterfallGray {
		return errors.New("waterfall-mode must be byte, rgb or gray")
	}

	if cfg.WaterfallColumns != 0 && (cfg.WaterfallColumns < 8 || cfg.WaterfallColumns > diag.MaxWaterfallColumns) {
		return errors.New("waterfall-columns must be 0 (auto) or between 8 and 256")
	}

	if cfg.WaterfallRows < 0 || cfg.WaterfallRows > diag.MaxWaterfallRows {
		return errors.New("waterfall-rows must be between 0 (auto) and 64")
	}

	if math.IsNaN(cfg.ShannonThreshold) || math.IsInf(cfg.ShannonThreshold, 0) || cfg.ShannonThreshold < 0 || cfg.ShannonThreshold > 8 {
		return errors.New("shannon-threshold must be a finite value between 0 and 8 bits/byte")
	}

	if cfg.Duration < 0 {
		return errors.New("duration must not be negative")
	}

	if cfg.ReportPath == "-" && !cfg.Headless {
		return errors.New("report=- requires -headless to keep stdout valid JSONL")
	}

	if cfg.AnalysisBytes < 0 || cfg.AnalysisBytes > 1<<25 {
		return errors.New("analysis-bytes must be between 0 and 33554432")
	}

	if cfg.AnalysisInterval < 0 {
		return errors.New("analysis-interval must not be negative")
	}

	if cfg.DisplayInterval < time.Millisecond {
		return errors.New("display-interval must be at least 1ms")
	}

	return nil
}

func (cfg *Config) OutputEnabled() bool {
	return cfg.WriteOut || cfg.OutputPath != "" || cfg.OutputDevice != ""
}

// OutputKind is safe to include in logs/reports: it contains no operator path.
func (cfg *Config) OutputKind() string {
	switch {
	case cfg.WriteOut:
		return "urandom"
	case cfg.OutputDevice != "":
		return "device"
	case cfg.OutputPath == "-":
		return "stdout"
	case cfg.OutputPath != "":
		return "file"
	default:
		return "disabled"
	}
}

func (cfg *Config) validateOutput() error {
	destinations := 0

	for _, enabled := range []bool{cfg.WriteOut, cfg.OutputPath != "", cfg.OutputDevice != ""} {
		if enabled {
			destinations++
		}
	}

	if destinations > 1 {
		return errors.New("choose only one of -writeout, -output or -output-device")
	}

	if cfg.OutputBytes < 0 || (cfg.OutputBytes > 0 && destinations == 0) {
		return errors.New("output-bytes must be nonnegative and requires an output destination")
	}

	if cfg.OutputPath == "-" && (!cfg.Headless || cfg.ReportPath == "-") {
		return errors.New("output=- requires -headless and a report destination other than stdout")
	}

	return nil
}
