package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"runtime"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/config"
	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/drbg"
	"github.com/fantuz/entropy-service/internal/console"
)

func serverInvocation(out io.Writer, cfg *config.Config) *console.Report {
	r := console.New(out, "entropy-server "+appVersion+" | Startup invocation report")
	r.Section("Runtime & listeners")
	r.Value("Build / platform", fmt.Sprintf("%s / %s/%s", runtime.Version(), runtime.GOOS, runtime.GOARCH))

	if runtime.GOOS == "windows" {
		r.Value("Windows capabilities", "Windows OS CSPRNG (crypto/rand); Go socket defaults; direct QRNG/device access and custom socket tuning unavailable")
	}

	r.Value("HTTP address", cfg.HTTPAddr)
	r.Value("HTTPS enabled", cfg.EnableHTTPS)
	r.Value("HTTPS address", cfg.HTTPSAddr)
	r.Value("TLS certificate", cfg.CertFile)
	r.Value("TLS private key", cfg.KeyFile)
	r.Value("Cookie preferences", cfg.WantsCookie)
	r.Value("Debug / pprof", cfg.ShowDebug)
	r.Section("Source & reseeding")
	r.Value("Requested source", cfg.DevicePath)
	r.Value("Require device", cfg.RequireDevice)
	r.Value("DRBG backend", cfg.DRBG)
	r.Value("CPU / RDRAND mixing", fmt.Sprintf("%t / %t (CPU takes precedence)", cfg.MixCPU, cfg.MixRDRAND))
	r.Value("Source read timeout", cfg.SourceTimeout)
	r.Value("Reseed size", fmt.Sprintf("%d bytes", cfg.ReseedSize))
	r.Value("Reseed interval", time.Duration(cfg.ReseedMs)*time.Millisecond)
	r.Value("Maximum seed age", time.Duration(cfg.MaxReseedAgeMs)*time.Millisecond)
	r.Value("Reseed failure policy", cfg.OnReseedFailure)

	if cfg.OnReseedFailure == drbg.ReseedContinue {
		r.Check("Seed freshness", "WARN", "continue permits output with failed or stale reseeds; readiness still degrades")
	}

	r.Value("Legacy buffers", "-buffer-entropy / -buffer-qrng ignored")
	r.Section("Request & memory limits")
	r.Value("Maximum sample", fmt.Sprintf("%d bytes (%.2f MiB)", cfg.MaxBytes, float64(cfg.MaxBytes)/(1<<20)))
	r.Value("Maximum words", cfg.MaxWords)
	r.Value("Upload analysis prefix", fmt.Sprintf("%d bytes", cfg.BytesFingerprint))
	r.Value("HTTP generation", fmt.Sprintf("%d requests / %d MiB estimated working memory", cfg.HTTPMaxRequests, cfg.HTTPMemoryMiB))
	r.Value("WebSockets", fmt.Sprintf("%d connections / %d MiB estimated working memory", cfg.WSMaxConnections, cfg.WSMemoryMiB))
	r.Value("WS inbound messages", fmt.Sprintf("%d bytes maximum", wsInboundLimit))
	r.Value("Lab bridges", fmt.Sprintf("%d connections / %d MiB; loopback only=%t", cfg.LabMaxConnections, cfg.LabMemoryMiB, !cfg.LabAllowRemote))
	r.Value("Lab registry", cfg.LabSources)
	r.Section("Page & stream defaults")
	r.Value("Word page refresh", time.Duration(cfg.PageRefreshMs)*time.Millisecond)
	r.Value("Bytes stream refresh", time.Duration(cfg.RefreshRateMs)*time.Millisecond)
	r.Value("Color / word refresh", time.Duration(cfg.RefreshColorMs)*time.Millisecond)
	r.Section("Startup checks")
	r.Check("Configuration", "OK", "flags, timing and resource limits validated")

	return r
}

// This checks the loaded certificate's dates, not trust or hostname validity.
func reportTLS(r *console.Report, cfg *tls.Config, now time.Time) {
	if cfg == nil {
		r.Check("HTTPS / TLS", "OFF", "HTTP listener only")
		return
	}

	if len(cfg.Certificates) == 0 || len(cfg.Certificates[0].Certificate) == 0 {
		r.Check("Certificate dates", "WARN", "no loaded leaf certificate")
		return
	}

	r.Check("TLS key pair", "OK", "loaded; TLS 1.3 minimum")

	cert, err := x509.ParseCertificate(cfg.Certificates[0].Certificate[0])
	if err != nil {
		r.Check("Certificate dates", "WARN", "could not parse loaded leaf certificate")
		return
	}

	state := "OK"
	if now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
		state = reportWarning
	}

	r.Check("Certificate dates", state, fmt.Sprintf("%s to %s (UTC; trust/hostname not checked)", cert.NotBefore.UTC().Format(time.DateOnly), cert.NotAfter.UTC().Format(time.DateOnly)))
	expiryState, message := certificateExpiry(cert, now)
	r.Check("Certificate expiry", expiryState, message)

	if cert.Issuer.String() == "" {
		r.Check("Certificate issuer", reportWarning, emptyIssuerWarning)
	}
}

func reportSource(r *console.Report, requested, actual string) {
	r.Check("Source opened", "OK", actual)

	if requested != actual {
		r.Check("Source selection", "WARN", "configured device unavailable; using operating-system CSPRNG fallback")
	}

	if actual == DefaultCSPRNG || actual == sourceOSFallback {
		r.Value("Source type", "operating-system CSPRNG")
	} else {
		r.Value("Source type", "character device; quality not independently verified")
	}
}
