package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/diag"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/terminal"
)

type Config struct {
	MaxWords         int
	RefreshRate      int
	Refresh          time.Duration
	HTTPUrl          string
	WSUrl            string
	Mode             string
	CertFile         string
	KeyFile          string
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
	Retries          int
	RetryDelay       time.Duration
}

const (
	ModePull         = "pull"
	ModeStream       = "stream"
	MaxRetryDelay    = 30 * time.Second
	defaultColorMode = "auto"
	noColorMode      = "never"
)

func ParseConfig() *Config {
	cfg, err := parseConfig(os.Args[1:], os.Stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}

		os.Exit(2)
	}

	return cfg
}

func parseConfig(args []string, output io.Writer) (*Config, error) {
	cfg := &Config{}
	flags := flag.NewFlagSet("entropy-client", flag.ContinueOnError)
	flags.SetOutput(output)

	flags.Int64Var(&cfg.Slice, "slice", 2097152, "raw bytes per response/message (1..33554432)")
	flags.BoolVar(&cfg.WriteOut, "writeout", false, "Linux only: mix received bytes into /dev/urandom without entropy credit; fail on device errors (normally unnecessary)")
	flags.StringVar(&cfg.OutputPath, "output", "", "export raw bytes to a new private file (0600 on Linux, user-only ACL on Windows); - uses Linux stdout with -headless")
	flags.StringVar(&cfg.OutputDevice, "output-device", "", "Linux only: export to a validated /dev/random, /dev/urandom or /dev/null device; no entropy credit")
	flags.Int64Var(&cfg.OutputBytes, "output-bytes", 0, "stop after exporting this many bytes (0 = until duration/interruption); requires an output destination or -writeout")
	flags.BoolVar(&cfg.ShowDebug, "debug", false, "print debug information")

	flags.BoolVar(&cfg.ShowPreview, "preview", false, "print hex preview of first 64 bytes")
	flags.BoolVar(&cfg.ShowMatrix, "matrix", false, "print 64x64 matrix")
	flags.BoolVar(&cfg.ShowGraph, "graph", false, "print distribution graph")
	flags.BoolVar(&cfg.ShowDashboard, "dashboard", false, "print dashboard summaries")
	flags.BoolVar(&cfg.ShowHistogram, "histogram", false, "print extended histogram")
	flags.BoolVar(&cfg.ShowWaterfall, "waterfall", false, "show recent analysed sample prefixes as a terminal waterfall")
	flags.StringVar(&cfg.WaterfallMode, "waterfall-mode", diag.WaterfallByte, "waterfall mapping: byte (vivid hue)|rgb (triplets)|gray")
	flags.IntVar(&cfg.WaterfallColumns, "waterfall-columns", 0, "waterfall cells per row (0 = fit layout; otherwise 8..512; two terminal columns per cell)")
	flags.IntVar(&cfg.WaterfallRows, "waterfall-rows", 0, "waterfall history (0 = fit layout; otherwise 1..512; newest first)")
	flags.StringVar(&cfg.Color, "color", defaultColorMode, "terminal colors: auto|always|never|256|truecolor; auto respects NO_COLOR, TERM and redirected stdout")
	flags.StringVar(&cfg.Layout, "layout", terminal.LayoutAuto, "terminal layout: auto|compact|full|waterfall; waterfall fills the viewport with one status line")

	flags.DurationVar(&cfg.Refresh, "refresh", time.Second, "refresh interval for both HTTP and WS")
	flags.IntVar(&cfg.AnalysisBytes, "analysis-bytes", 1<<20, "maximum prefix bytes per analysed frame; 0 analyses the full frame")
	flags.DurationVar(&cfg.AnalysisInterval, "analysis-interval", 250*time.Millisecond, "minimum interval between analysed frames; 0 analyses every frame")
	flags.DurationVar(&cfg.DisplayInterval, "display-interval", time.Second, "terminal redraw interval, independent of reception (interactive maximum 20 FPS)")
	flags.Float64Var(&cfg.ShannonThreshold, "shannon-threshold", diag.DefaultShannonThreshold, "minimum empirical Shannon entropy for OK, in bits/byte (0..8)")
	flags.DurationVar(&cfg.Duration, "duration", 0, "measurement duration, excluding interactive preflight (0 = until interrupted)")
	flags.StringVar(&cfg.ReportPath, "report", "", "write JSONL to a new file; - writes to stdout with -headless")
	flags.BoolVar(&cfg.Headless, "headless", false, "disable terminal output and interactive preflight")
	flags.StringVar(&cfg.Mode, "mode", ModePull, "transport: http|pull for HTTP; websocket|push|stream for WebSocket (stream is retained for compatibility)")
	flags.IntVar(&cfg.Retries, "retries", 0, "Maximum total transport retries per measured run (0..100; default 0 fails immediately)")
	flags.DurationVar(&cfg.RetryDelay, "retry-delay", time.Second, "Initial retry delay (1ms..30s); exponential backoff capped at 30s, with jitter")

	flags.StringVar(&cfg.HTTPUrl, "url", "http://127.0.0.1:8080/v1/data/random", "HTTP entropy endpoint (supports existing query parameters)")
	flags.StringVar(&cfg.WSUrl, "wsurl", "ws://127.0.0.1:8080/stream", "WS entropy endpoint (supports existing query parameters)")

	/*
		flags.IntVar(&cfg.MaxWords, "words", 20, "Default number of words presented by /words endpoint (maximum 20)")
		fps := strconv.FormatInt(refresh.Milliseconds(), 10)
	*/

	flags.Usage = func() {
		fmt.Fprint(output, "Entropy Client\n\nUsage:\n  entropy-client [options]\n\nOptions:\n")
		flags.PrintDefaults()
	}

	if err := flags.Parse(args); err != nil {
		return nil, err
	}

	if flags.NArg() != 0 {
		err := errors.New("unexpected positional arguments; use -flag=false or -flag=true for boolean values")
		fmt.Fprintln(output, err)

		return nil, err
	}

	if cfg.Layout == terminal.LayoutWaterfall {
		cfg.ShowWaterfall = true
	}

	return cfg, nil
}

// NormalizeMode preserves the established runtime/report names for all aliases.
func (cfg *Config) NormalizeMode() error {
	switch cfg.Mode {
	case ModePull, "http":
		cfg.Mode = ModePull
	case ModeStream, "push", "websocket":
		cfg.Mode = ModeStream
	default:
		return errors.New("mode must be http (pull) or websocket (push, stream)")
	}

	return nil
}

// ValidatePlatform keeps unsupported Windows features explicit. Formula/flag
// validation stays shared and can be tested independently of platform support.
func (cfg *Config) ValidatePlatform() error {
	// Keep Windows capability dispatch out of non-Windows release binaries,
	// even when the larger policy helper exceeds the compiler's inline budget.
	if runtime.GOOS != "windows" {
		return nil
	}

	return cfg.validatePlatform(runtime.GOOS)
}

func (cfg *Config) validatePlatform(goos string) error {
	if goos != "windows" {
		return nil
	}

	switch {
	case cfg.WriteOut || cfg.OutputDevice != "":
		return errors.New("device output and -writeout are unavailable on Windows; use -output=FILE")
	case cfg.OutputPath == "-":
		return errors.New("raw stdout export is unavailable on Windows; use -output=FILE (normal text stdout remains available)")
	default:
		return nil
	}
}

func (cfg *Config) ValidateAnalysis() error {
	if cfg.Retries < 0 || cfg.Retries > 100 {
		return errors.New("retries must be between 0 and 100")
	}

	if cfg.RetryDelay < 0 || cfg.RetryDelay > MaxRetryDelay || (cfg.Retries > 0 && cfg.RetryDelay < time.Millisecond) {
		return errors.New("retry-delay must be between 1ms and 30s when retries are enabled")
	}

	if err := cfg.validateOutput(); err != nil {
		return err
	}

	if cfg.Layout != "" && cfg.Layout != terminal.LayoutAuto && cfg.Layout != terminal.LayoutCompact && cfg.Layout != terminal.LayoutFull && cfg.Layout != terminal.LayoutWaterfall {
		return errors.New("layout must be auto, compact, full or waterfall")
	}

	if !terminal.ValidColor(cfg.Color) {
		return errors.New("color must be auto, always, never, 256 or truecolor")
	}

	if cfg.WaterfallMode != "" && cfg.WaterfallMode != diag.WaterfallByte && cfg.WaterfallMode != diag.WaterfallRGB && cfg.WaterfallMode != diag.WaterfallGray {
		return errors.New("waterfall-mode must be byte, rgb or gray")
	}

	if cfg.WaterfallColumns != 0 && (cfg.WaterfallColumns < 8 || cfg.WaterfallColumns > diag.MaxWaterfallColumns) {
		return errors.New("waterfall-columns must be 0 (auto) or between 8 and 512")
	}

	if cfg.WaterfallRows < 0 || cfg.WaterfallRows > diag.MaxWaterfallRows {
		return errors.New("waterfall-rows must be between 0 (auto) and 512")
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
