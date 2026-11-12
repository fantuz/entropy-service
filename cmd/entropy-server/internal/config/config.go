package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/chaoskey"
	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/drbg"
	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/quantisusb"
)

const (
	linuxOS = "linux"
	// WindowsCSPRNG names the native Windows OS source, exposed through crypto/rand.
	WindowsCSPRNG    = "windows-csprng"
	windowsOS        = "windows"
	linuxDevicePath  = "/dev/qrandom0"
	maxReseedAgeFlag = "max-reseed-age"
	minReseedBytes   = 32
	maxReseedBytes   = 1 << 20
	defaultReseedMs  = 15000
	maxReseedMs      = 60000
)

// defaultDRBGForArch chooses an initial stream backend by Go target ISA, not
// by microbenchmark or by detecting AES instructions on the actual CPU.
// Explicit -drbg always overrides this platform default.
func defaultDRBGForArch(goarch string) string {
	switch goarch {
	case "arm", "arm64", "riscv64":
		return string(drbg.ChaCha20)
	default:
		return string(drbg.AES256CTR)
	}
}

type Config struct {
	LabSources        string
	LabAllowRemote    bool
	LabMaxConnections int
	LabMemoryMiB      int64
	ReseedMs          int
	MaxReseedAgeMs    int
	ReseedSize        int
	SourceTimeout     time.Duration
	MaxBytes          int
	QRNGBuffer        int
	SeedBuffer        int
	MaxWords          int
	PageRefreshMs     int
	BytesFingerprint  int
	RefreshRateMs     int
	RefreshColorMs    int
	HTTPAddr          string
	HTTPSAddr         string
	CertFile          string
	KeyFile           string
	LogLevel          string // Legacy field: unused.
	DevicePath        string
	DRBG              string
	OnReseedFailure   drbg.ReseedFailurePolicy
	EnableHTTPS       bool
	RequireDevice     bool
	MixRDRAND         bool
	MixCPU            bool
	ShowDebug         bool
	WantsCookie       bool
	WSMaxConnections  int
	WSMemoryMiB       int64
	HTTPMaxRequests   int
	HTTPMemoryMiB     int64
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

	flags.StringVar(&cfg.LabSources, "lab-sources", "", "Experimental source registry JSON file (device/socket/peer endpoints are configured by the operator)")
	flags.BoolVar(&cfg.LabAllowRemote, "lab-allow-remote", false, "Allow non-loopback clients to use registered experimental source bridges")
	flags.IntVar(&cfg.LabMaxConnections, "lab-max-connections", 8, "Maximum simultaneous experimental source bridges")
	flags.Int64Var(&cfg.LabMemoryMiB, "lab-memory-mib", 32, "Experimental bridge working-memory budget in MiB")
	flags.StringVar(&cfg.HTTPAddr, "http", ":8080", "HTTP listen address")
	flags.StringVar(&cfg.HTTPSAddr, "https", ":8443", "HTTPS listen address")
	flags.StringVar(&cfg.CertFile, "cert-file", "cert.pem", "TLS certificate chain PEM file (provision your own; see generate-cert -help)")
	flags.StringVar(&cfg.KeyFile, "key-file", "key.pem", "TLS private key PEM file matching cert-file; keep owner-only")
	flags.DurationVar(&cfg.SourceTimeout, "source-timeout", 3*time.Second, "Deadline for each complete startup/reseed read (1ms..5m); hardware must support nonblocking read deadlines")
	flags.StringVar(&cfg.DevicePath, "device", defaultDeviceForOS(runtime.GOOS), "Seed source: Linux device path; Windows windows-csprng or experimental quantis-pci:0; experimental quantis-usb:0 or chaoskey-usb:0 on Linux/Windows (see hardware list)")
	flags.StringVar(&cfg.DRBG, "drbg", defaultDRBGForArch(runtime.GOARCH), "DRBG backend: aes-256-ctr by default, chacha20 on ARM/RISC-V; barzakh-521-experimental requires -tags=experimental_barzakh")
	flags.IntVar(&cfg.RefreshRateMs, "refresh-ms", 5000, "Default /bytes WebSocket interval in milliseconds")
	flags.IntVar(&cfg.RefreshColorMs, "refresh-colors-ms", 1500, "Default /stream, /colors and /words WebSocket interval in milliseconds")
	flags.IntVar(&cfg.PageRefreshMs, "refresh", 5000, "Word page refresh in milliseconds (HTML rounds up to whole seconds)")
	flags.IntVar(&cfg.MaxWords, "words", 20, "Default number of items presented by \"words\" endpoint (positive)")
	flags.IntVar(&cfg.ReseedMs, "reseed-ms", defaultReseedMs, "Root reseed interval in milliseconds (1-60000)")
	flags.IntVar(&cfg.MaxReseedAgeMs, maxReseedAgeFlag, 0, "Maximum seed age in milliseconds (0 = max(1000, four reseed intervals))")
	flags.Func("on-reseed-failure", "Reseed failure policy: stop (default) pauses generation on failure/stale seed; continue explicitly permits old-state output", func(value string) error {
		cfg.OnReseedFailure = drbg.ReseedFailurePolicy(value)
		return drbg.ValidateReseedFailurePolicy(cfg.OnReseedFailure)
	})

	cfg.OnReseedFailure = drbg.ReseedStop
	flags.IntVar(&cfg.SeedBuffer, "buffer-entropy", 64, "Deprecated; ignored (no separate entropy buffer)")
	flags.IntVar(&cfg.ReseedSize, "buffer-reseed", 256, "Bytes supplied to each root reseed (32-1048576)")
	flags.IntVar(&cfg.QRNGBuffer, "buffer-qrng", 2048, "Deprecated; ignored (reseeding reads the configured source directly)")
	flags.IntVar(&cfg.MaxBytes, "max-bytes", 1<<25, "Maximum generated or lab sample bytes (default 32 MiB; compact WebSocket messages add a 32-byte digest)")
	flags.IntVar(&cfg.WSMaxConnections, "ws-max-connections", 128, "Maximum simultaneous entropy WebSockets across HTTP and HTTPS (positive)")
	flags.Int64Var(&cfg.WSMemoryMiB, "ws-memory-mib", 1024, "Aggregate estimated WebSocket working-memory budget in MiB (positive; not an RSS limit)")
	flags.IntVar(&cfg.HTTPMaxRequests, "http-max-requests", 128, "Maximum simultaneous HTTP generation requests across HTTP and HTTPS (positive)")
	flags.Int64Var(&cfg.HTTPMemoryMiB, "http-memory-mib", 512, "Aggregate estimated HTTP generation memory budget in MiB (positive; separate from WebSocket budget; not an RSS limit)")
	flags.IntVar(&cfg.BytesFingerprint, "fingerprint", 2097152, "Maximum upload prefix bytes analyzed statistically (1-33554432); SHA-256 covers the full upload")
	flags.BoolVar(&cfg.EnableHTTPS, "enable-https", true, "Enable/Disable HTTPS server (false/true)")
	flags.BoolVar(&cfg.RequireDevice, "require-device", false, "Fail if entropy device unavailable")
	flags.BoolVar(&cfg.MixRDRAND, "rdrand-mix", false, "Supplement startup/reseed bytes with CPU RDRAND (requires amd64 CPU support)")
	flags.BoolVar(&cfg.MixCPU, "cpu-mix", false, "Supplement startup/reseed bytes with RDSEED, falling back to RDRAND; takes precedence over -rdrand-mix")
	flags.BoolVar(&cfg.ShowDebug, "debug", false, "print debug information")
	flags.BoolVar(&cfg.WantsCookie, "cookie", false, "remember HTTP byte/word preferences and word language in Secure, HttpOnly cookies (browser persistence requires HTTPS)")

	flags.Usage = func() {
		fmt.Fprintln(output, "Entropy Server")
		fmt.Fprintln(output, "Usage: entropy-server [options]")
		fmt.Fprintln(output, "Provision a unique self-signed TLS identity: entropy-server generate-cert -help")
		fmt.Fprintln(output, "Millisecond flags use plain integer milliseconds, e.g. 1000 (no unit suffix); source-timeout uses a Go duration, e.g. 3s.")
		fmt.Fprintln(output, "WebSocket budgets are shared by HTTP/HTTPS and reserved until disconnect.")
		fmt.Fprintln(output, "Inbound WebSocket messages are limited to 1 KiB.")
		fmt.Fprintln(output, "/bytes and /colors accept format=json (default), compact, or hash.")
		fmt.Fprintln(output, "HTTP generation budgets are shared by HTTP/HTTPS; probes and static files are excluded.")
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

	if cfg.SourceTimeout == 0 {
		err := errors.New("source-timeout must be between 1ms and 5m")
		fmt.Fprintln(output, err)

		return nil, err
	}

	if err := cfg.Validate(); err != nil {
		fmt.Fprintln(output, err)
		return nil, err
	}

	return cfg, nil
}

func defaultDeviceForOS(goos string) string {
	if goos == windowsOS {
		return WindowsCSPRNG
	}

	return linuxDevicePath
}

// ValidatePlatform rejects unsupported requests before TLS, device or network I/O.
func (cfg *Config) ValidatePlatform() error { return cfg.validatePlatform(runtime.GOOS) }

func (cfg *Config) validatePlatform(goos string) error {
	if _, selected, err := chaoskey.ParseSelector(cfg.DevicePath); selected {
		if err != nil {
			return err
		}

		if (goos == linuxOS || goos == windowsOS) && chaoskey.Available {
			return nil
		}

		return errors.New("native ChaosKey USB is available on Linux amd64/arm/arm64 and Windows x64")
	}

	if _, selected, err := quantisusb.ParseSelector(cfg.DevicePath); selected {
		if err != nil {
			return err
		}

		if (goos == linuxOS || goos == windowsOS) && quantisusb.Available {
			return nil
		}

		return errors.New("native Quantis USB is available on Linux amd64/arm/arm64 and Windows x64")
	}

	_, quantis, err := ParseQuantisPCI(cfg.DevicePath)
	if err != nil {
		return err
	}

	if quantis {
		if goos != windowsOS {
			return errors.New("quantis-pci:N is a Windows source; on Linux use -device=/dev/qrandom0 -require-device")
		}

		return nil
	}

	if goos != windowsOS {
		return nil
	}

	if cfg.RequireDevice {
		return errors.New("-require-device on Windows requires -device=quantis-pci:0 or quantis-usb:0 or chaoskey-usb:0; windows-csprng is an OS source")
	}

	if cfg.DevicePath != "" && cfg.DevicePath != WindowsCSPRNG {
		return errors.New("unsupported Windows seed source; use -device=windows-csprng or experimental -device=quantis-pci:0")
	}

	return nil
}

func (cfg *Config) validateTiming() error {
	maxMilliseconds := int64(time.Duration(1<<63-1) / time.Millisecond)

	fields := []struct {
		name       string
		value, min int
		max        int64
	}{
		{"reseed-ms", cfg.ReseedMs, 1, maxReseedMs},
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

// Validate checks startup configuration before entropy devices are opened.
// It also resolves the default readiness age for both CLI and direct callers.
func (cfg *Config) Validate() error {
	if cfg == nil {
		return errors.New("nil server configuration")
	}

	if err := cfg.validateTiming(); err != nil {
		return err
	}

	if cfg.SourceTimeout == 0 {
		cfg.SourceTimeout = 3 * time.Second
	}

	if cfg.SourceTimeout < time.Millisecond || cfg.SourceTimeout > 5*time.Minute {
		return errors.New("source-timeout must be between 1ms and 5m")
	}

	if cfg.ReseedSize < minReseedBytes || cfg.ReseedSize > maxReseedBytes {
		return fmt.Errorf("buffer-reseed must be between %d and %d bytes", minReseedBytes, maxReseedBytes)
	}

	if cfg.MaxBytes < 1 || cfg.MaxBytes > 1<<25 {
		return errors.New("max-bytes must be between 1 and 33554432")
	}

	if cfg.MaxWords < 1 {
		return errors.New("words must be positive")
	}

	if cfg.BytesFingerprint < 1 || cfg.BytesFingerprint > 1<<25 {
		return errors.New("fingerprint must be between 1 and 33554432 bytes")
	}

	if cfg.LabMaxConnections < 1 || cfg.LabMemoryMiB < 1 || cfg.LabMemoryMiB > (1<<63-1)/(1<<20) {
		return errors.New("lab-max-connections and lab-memory-mib must be positive; memory budget must fit in int64 bytes")
	}

	if cfg.WSMaxConnections < 1 || cfg.WSMemoryMiB < 1 || cfg.WSMemoryMiB > (1<<63-1)/(1<<20) {
		return errors.New("ws-max-connections and ws-memory-mib must be positive; memory budget must fit in int64 bytes")
	}

	if cfg.HTTPMaxRequests < 1 || cfg.HTTPMemoryMiB < 1 || cfg.HTTPMemoryMiB > (1<<63-1)/(1<<20) {
		return errors.New("http-max-requests and http-memory-mib must be positive; memory budget must fit in int64 bytes")
	}

	if _, err := drbg.ParseAlgorithm(cfg.DRBG); err != nil {
		return err
	}

	if err := drbg.ValidateReseedFailurePolicy(cfg.OnReseedFailure); err != nil {
		return err
	}

	if cfg.MaxReseedAgeMs == 0 {
		cfg.MaxReseedAgeMs = max(1000, 4*cfg.ReseedMs)
	}

	return nil
}
