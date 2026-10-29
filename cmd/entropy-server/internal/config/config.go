package config

import (
	"flag"
	"fmt"
	"os"
	"time"
)

type Config struct {
	ReseedMs         int
	MaxReseedAge     time.Duration
	ReseedSize       int
	MaxBytes         int
	QRNGBuffer       int
	SeedBuffer       int
	MaxWords         int
	RefreshRate      int
	BytesFingerprint int
	RefreshRateMs    time.Duration
	RefreshColorMs   time.Duration
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
	cfg := &Config{}

	flag.StringVar(&cfg.HTTPAddr, "http", ":8080", "HTTP listen address")
	flag.StringVar(&cfg.HTTPSAddr, "https", ":8443", "HTTPS listen address")
	flag.StringVar(&cfg.CertFile, "cert-file", "cert.pem", "Public Key File")
	flag.StringVar(&cfg.KeyFile, "key-file", "key.pem", "Private Key File")
	flag.StringVar(&cfg.DevicePath, "device", "/dev/qrandom0", "Entropy source, defaults to /dev/qrandom0")
	flag.StringVar(&cfg.DRBG, "drbg", "chacha20", "DRBG stream backend: chacha20 or aes-256-ctr")
	flag.DurationVar(&cfg.RefreshRateMs, "refresh-ms", 5000, "Default refresh rate (in ms) for data presented over websockets (default: 5 sec)")
	flag.DurationVar(&cfg.RefreshColorMs, "refresh-colors-ms", 1500, "Default refresh rate (in ms) for colors-only websocket /colors.html (default: 1.5 sec")
	flag.IntVar(&cfg.RefreshRate, "refresh", 5, "Default refresh rate (in seconds) of words presented by /v1/words/random endpoint")
	flag.IntVar(&cfg.MaxWords, "words", 20, "Default number of items presented by \"words\" endpoint (maximum 20)")
	flag.IntVar(&cfg.ReseedMs, "reseed-ms", 250, "Reseed interval (ms)")
	flag.DurationVar(&cfg.MaxReseedAge, "max-reseed-age", 0, "Maximum seed age for readiness (0 = max(1s, four reseed intervals))")
	flag.IntVar(&cfg.SeedBuffer, "buffer-entropy", 64, "Size of Entropy buffer in KB")
	flag.IntVar(&cfg.ReseedSize, "buffer-reseed", 256, "Size of Reseed buffer in Bytes")
	flag.IntVar(&cfg.QRNGBuffer, "buffer-qrng", 2048, "Deprecated; ignored (reseeding reads the configured source directly)")
	flag.IntVar(&cfg.MaxBytes, "max-bytes", 1<<25, "Maximum raw bytes per HTTP response or WebSocket message (default 32 MiB)")
	flag.IntVar(&cfg.BytesFingerprint, "fingerprint", 2097152, "Maximum bytes analized for fingerprinting upon file upload")
	flag.BoolVar(&cfg.EnableHTTPS, "enable-https", true, "Enable/Disable HTTPS server (false/true)")
	flag.BoolVar(&cfg.RequireDevice, "require-device", false, "Fail if entropy device unavailable")
	flag.BoolVar(&cfg.MixRDRAND, "rdrand-mix", false, "Supplement startup/reseed bytes with CPU RDRAND (requires amd64 CPU support)")
	flag.BoolVar(&cfg.MixCPU, "cpu-mix", false, "Supplement startup/reseed bytes with RDSEED, falling back to RDRAND; takes precedence over -rdrand-mix")
	flag.BoolVar(&cfg.ShowDebug, "debug", false, "print debug information")
	flag.BoolVar(&cfg.WantsCookie, "cookie", false, "store and use HTTP cookies")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Entropy Server\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n  %s [options]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	return cfg
}
