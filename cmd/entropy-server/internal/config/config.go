package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

const maxReseedAgeFlag = "max-reseed-age"

type Config struct {
	ReseedMs         int
	MaxReseedAgeMs   int
	ReseedSize       int
	MaxBytes         int
	QRNGBuffer       int
	SeedBuffer       int
	MaxWords         int
	PageRefreshMs    int
	BytesFingerprint int
	RefreshRateMs    int
	RefreshColorMs   int
	HTTPAddr         string
	HTTPSAddr        string
	CertFile         string
	KeyFile          string
	LogLevel         string
	DevicePath       string
	DRBG             string
	EnableHTTPS      bool
	RequireDevice    bool
	MixRDRAND        bool
	MixCPU           bool
	ShowDebug        bool
	WantsCookie      bool
}

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
	flags := flag.NewFlagSet("entropy-server", flag.ContinueOnError)
	flags.SetOutput(output)

	flags.StringVar(&cfg.HTTPAddr, "http", ":8080", "HTTP listen address")
	flags.StringVar(&cfg.HTTPSAddr, "https", ":8443", "HTTPS listen address")
	flags.StringVar(&cfg.CertFile, "cert-file", "cert.pem", "Public Key File")
	flags.StringVar(&cfg.KeyFile, "key-file", "key.pem", "Private Key File")
	flags.StringVar(&cfg.DevicePath, "device", "/dev/qrandom0", "Entropy source, defaults to /dev/qrandom0")
	flags.StringVar(&cfg.DRBG, "drbg", "chacha20", "DRBG stream backend: chacha20 or aes-256-ctr")
	flags.IntVar(&cfg.RefreshRateMs, "refresh-ms", 5000, "Default /bytes WebSocket interval in milliseconds")
	flags.IntVar(&cfg.RefreshColorMs, "refresh-colors-ms", 1500, "Default /stream, /colors and /words WebSocket interval in milliseconds")
	flags.IntVar(&cfg.PageRefreshMs, "refresh", 5000, "Word page refresh in milliseconds (HTML rounds up to whole seconds)")
	flags.IntVar(&cfg.MaxWords, "words", 20, "Default number of items presented by \"words\" endpoint (maximum 20)")
	flags.IntVar(&cfg.ReseedMs, "reseed-ms", 250, "Root reseed interval in milliseconds (1-10000)")
	flags.IntVar(&cfg.MaxReseedAgeMs, maxReseedAgeFlag, 0, "Maximum seed age in milliseconds (0 = max(1000, four reseed intervals))")
	flags.IntVar(&cfg.SeedBuffer, "buffer-entropy", 64, "Size of Entropy buffer in KB")
	flags.IntVar(&cfg.ReseedSize, "buffer-reseed", 256, "Size of Reseed buffer in Bytes")
	flags.IntVar(&cfg.QRNGBuffer, "buffer-qrng", 2048, "Deprecated; ignored (reseeding reads the configured source directly)")
	flags.IntVar(&cfg.MaxBytes, "max-bytes", 1<<25, "Maximum raw bytes per HTTP response or WebSocket message (default 32 MiB)")
	flags.IntVar(&cfg.BytesFingerprint, "fingerprint", 2097152, "Maximum bytes analized for fingerprinting upon file upload")
	flags.BoolVar(&cfg.EnableHTTPS, "enable-https", true, "Enable/Disable HTTPS server (false/true)")
	flags.BoolVar(&cfg.RequireDevice, "require-device", false, "Fail if entropy device unavailable")
	flags.BoolVar(&cfg.MixRDRAND, "rdrand-mix", false, "Supplement startup/reseed bytes with CPU RDRAND (requires amd64 CPU support)")
	flags.BoolVar(&cfg.MixCPU, "cpu-mix", false, "Supplement startup/reseed bytes with RDSEED, falling back to RDRAND; takes precedence over -rdrand-mix")
	flags.BoolVar(&cfg.ShowDebug, "debug", false, "print debug information")
	flags.BoolVar(&cfg.WantsCookie, "cookie", false, "store and use HTTP cookies")

	flags.Usage = func() {
		fmt.Fprintln(output, "Entropy Server")
		fmt.Fprintln(output, "Usage: entropy-server [options]")
		fmt.Fprintln(output, "Timing values are plain integer milliseconds, e.g. 1000 (no unit suffix).")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return nil, err
	}

	if flags.NArg() != 0 {
		err := fmt.Errorf("unexpected positional arguments %q; use -flag=false or -flag=true for boolean values", flags.Args())
		fmt.Fprintln(output, err)

		return nil, err
	}

	if err := cfg.validateTiming(); err != nil {
		fmt.Fprintln(output, err)
		return nil, err
	}

	return cfg, nil
}

func (cfg *Config) validateTiming() error {
	maxMilliseconds := int64(time.Duration(1<<63-1) / time.Millisecond)

	fields := []struct {
		name       string
		value, min int
		max        int64
	}{
		{"reseed-ms", cfg.ReseedMs, 1, 10000},
		{"refresh-ms", cfg.RefreshRateMs, 1, maxMilliseconds},
		{"refresh-colors-ms", cfg.RefreshColorMs, 1, maxMilliseconds},
		{"refresh", cfg.PageRefreshMs, 1, maxMilliseconds},
		{maxReseedAgeFlag, cfg.MaxReseedAgeMs, 0, maxMilliseconds},
	}
	for _, f := range fields {
		if f.value < f.min || int64(f.value) > f.max {
			return fmt.Errorf("%s must be between %d and %d milliseconds", f.name, f.min, f.max)
		}
	}

	return nil
}
