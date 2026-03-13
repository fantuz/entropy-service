package config

import (
	"flag"
	"fmt"
	"os"
	"time"
)

type Config struct {
	//ReseedMs         int
	//ReseedSize       int
	//MaxBytes         int
	MaxWords         int
	RefreshRate      int
	//BytesFingerprint int
	//RefreshRateMs    time.Duration
	//RefreshColorMs   time.Duration
	Refresh          time.Duration
	HTTPAddr         string
	WSAddr           string
	Mode             string
	CertFile         string
	KeyFile          string
	LogLevel         string
	//EnableHTTPS      bool
	//RequireDevice    bool
	WriteOut         bool
	ShowDebug        bool
	ShowPreview      bool
	ShowGraph        bool
	ShowMatrix       bool
	ShowDashboard    bool
	ShowHistogram    bool
	Slice            int64
}

func ParseConfig() *Config {
	cfg := &Config{}

	/*
	showDebug := flag.Bool("debug", false, "print debug information")
	showPreview := flag.Bool("preview", false, "print hex preview of first 64 bytes")
	showMatrix := flag.Bool("matrix", false, "print 64x64 matrix ")
	showGraph := flag.Bool("graph", false, "print distribution graph")
	showDashboard := flag.Bool("dashboard", false, "print dashboard summaries")
	showHistogram := flag.Bool("histogram", false, "print extended histogram")
	refresh := flag.Duration("refresh", 50*time.Millisecond, "refresh interval for both HTTP and WS")
	mode := flag.String("mode", "pull", "pull|stream")

	//var size = flag.Int("bytes", 1048576, "bytes to fetch")
	//url := flag.String("url", "http://127.0.0.1:8080/v1/data/random?bytes=1048576", "entropy endpoint")
	//wsurl := flag.String("wsurl", "ws://127.0.0.1:8080/stream?bytes=1048576", "entropy endpoint")
	//fps := strconv.FormatInt(refresh.Milliseconds(), 10)

	//var refwsurl = *wsurl + "&refresh=" + fps
	*/

	flag.Int64Var(&cfg.Slice, "slice", 2097152, "amount of entropy pre fetch")
	flag.BoolVar(&cfg.WriteOut, "writeout", false, "write out the received entropy back to local /dev/urandom (useful for low-entropy or isolated systems)")
	flag.BoolVar(&cfg.ShowDebug, "debug", false, "print debug information")

	flag.BoolVar(&cfg.ShowPreview, "preview", false, "print hex preview of first 64 bytes")
	flag.BoolVar(&cfg.ShowMatrix, "matrix", false, "print 64x64 matrix ")
	flag.BoolVar(&cfg.ShowGraph, "graph", false, "print distribution graph")
	flag.BoolVar(&cfg.ShowDashboard, "dashboard", false, "print dashboard summaries")
	flag.BoolVar(&cfg.ShowHistogram, "histogram", false, "print extended histogram")
	flag.DurationVar(&cfg.Refresh, "refresh", 1500, "refresh interval for both HTTP and WS")
	flag.StringVar(&cfg.Mode, "mode", "pull", "Preferred mode: pull|stream (HTTP vs websocket)")

	/*
	flag.IntVar(&cfg.RefreshRate, "refresh", 5, "Default refresh rate (in seconds) of words presented by /v1/words/random endpoint")
	flag.IntVar(&cfg.MaxWords, "words", 20, "Default number of words presented by /words endpoint (maximum 20)")

	showPreview := flag.Bool("preview", false, "print hex preview of first 64 bytes")
	showMatrix := flag.Bool("matrix", false, "print 64x64 matrix ")
	showGraph := flag.Bool("graph", false, "print distribution graph")
	showDashboard := flag.Bool("dashboard", false, "print dashboard summaries")
	showHistogram := flag.Bool("histogram", false, "print extended histogram")
	refresh := flag.Duration("refresh", 50*time.Millisecond, "refresh interval for both HTTP and WS")
	mode := flag.String("mode", "pull", "pull|stream")

	//var size = flag.Int("bytes", 1048576, "bytes to fetch")
	url := flag.String("url", "http://127.0.0.1:8080/v1/data/random?bytes=1048576", "entropy endpoint")
	wsurl := flag.String("wsurl", "ws://127.0.0.1:8080/stream?bytes=1048576", "entropy endpoint")
	fps := strconv.FormatInt(refresh.Milliseconds(), 10)

	var refwsurl = *wsurl + "&refresh=" + fps
	*/

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Entropy Server\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n  %s [options]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	return cfg
}
