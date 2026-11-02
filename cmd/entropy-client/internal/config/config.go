package config

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"time"
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
	ShowDebug        bool
	ShowPreview      bool
	ShowGraph        bool
	ShowMatrix       bool
	ShowDashboard    bool
	ShowHistogram    bool
	Slice            int64
	AnalysisBytes    int
	AnalysisInterval time.Duration
	DisplayInterval  time.Duration
}

func ParseConfig() *Config {
	cfg := &Config{}

	flag.Int64Var(&cfg.Slice, "slice", 2097152, "raw bytes per response/message (1..33554432)")
	flag.BoolVar(&cfg.WriteOut, "writeout", false, "write out the received entropy back to local /dev/urandom (useful for low-entropy or isolated systems)")
	flag.BoolVar(&cfg.ShowDebug, "debug", false, "print debug information")

	flag.BoolVar(&cfg.ShowPreview, "preview", false, "print hex preview of first 64 bytes")
	flag.BoolVar(&cfg.ShowMatrix, "matrix", false, "print 64x64 matrix")
	flag.BoolVar(&cfg.ShowGraph, "graph", false, "print distribution graph")
	flag.BoolVar(&cfg.ShowDashboard, "dashboard", false, "print dashboard summaries")
	flag.BoolVar(&cfg.ShowHistogram, "histogram", false, "print extended histogram")

	flag.DurationVar(&cfg.Refresh, "refresh", time.Second, "refresh interval for both HTTP and WS")
	flag.IntVar(&cfg.AnalysisBytes, "analysis-bytes", 1<<20, "maximum prefix bytes per analysed frame; 0 analyses the full frame")
	flag.DurationVar(&cfg.AnalysisInterval, "analysis-interval", 250*time.Millisecond, "minimum interval between analysed frames; 0 analyses every frame")
	flag.DurationVar(&cfg.DisplayInterval, "display-interval", time.Second, "terminal redraw interval, independent of reception")
	flag.StringVar(&cfg.Mode, "mode", "pull", "Preferred mode: pull|stream (HTTP vs websocket)")

	flag.StringVar(&cfg.HTTPUrl, "url", "http://127.0.0.1:8080/v1/data/random", "HTTP entropy endpoint")
	flag.StringVar(&cfg.WSUrl, "wsurl", "ws://127.0.0.1:8080/stream", "WS entropy endpoint")

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
