package config

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"strings"
	"testing"
)

func TestTimingDefaultsAndMillisecondOverrides(t *testing.T) {
	cfg, err := parseConfig(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.ReseedMs != 250 || cfg.RefreshRateMs != 5000 || cfg.RefreshColorMs != 1500 ||
		cfg.PageRefreshMs != 5000 || cfg.MaxReseedAgeMs != 1000 {
		t.Fatalf("default timings changed: %+v", cfg)
	}

	cfg, err = parseConfig([]string{
		"-reseed-ms=1000", "-refresh-ms", "200", "-refresh-colors-ms=50",
		"-refresh=1500", "-max-reseed-age=5000", "-enable-https=false",
	}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.ReseedMs != 1000 || cfg.RefreshRateMs != 200 || cfg.RefreshColorMs != 50 ||
		cfg.PageRefreshMs != 1500 || cfg.MaxReseedAgeMs != 5000 || cfg.EnableHTTPS {
		t.Fatalf("timing overrides did not remain millisecond counts: %+v", cfg)
	}
}

func TestInvalidTimingArgumentsAreRejected(t *testing.T) {
	for _, name := range []string{"reseed-ms", "refresh-ms", "refresh-colors-ms", "refresh", "max-reseed-age"} {
		for _, value := range []string{"1s", "100ms", "abc", "-1", "9223372036854775807"} {
			if _, err := parseConfig([]string{"-" + name + "=" + value}, io.Discard); err == nil {
				t.Errorf("accepted invalid %s=%s", name, value)
			}
		}

		if name != "max-reseed-age" {
			if _, err := parseConfig([]string{"-" + name + "=0"}, io.Discard); err == nil {
				t.Errorf("accepted zero %s", name)
			}
		}
	}

	for _, args := range [][]string{
		{"-enable-https", "false", "-reseed-ms=1000"},
		{"run", "-reseed-ms=1000"},
		{"--", "-reseed-ms=1000"},
		{"-reseed-ms=10001"},
	} {
		if _, err := parseConfig(args, io.Discard); err == nil {
			t.Errorf("arguments were silently ignored: %v", args)
		}
	}
}

func TestHelpStatesTimingFormat(t *testing.T) {
	var output bytes.Buffer
	if _, err := parseConfig([]string{"-help"}, &output); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("help: %v", err)
	}

	if !strings.Contains(output.String(), "plain integer milliseconds") || strings.Contains(output.String(), "µs") {
		t.Fatalf("ambiguous help: %s", output.String())
	}
}

func TestReseedBufferSizeBounds(t *testing.T) {
	for _, value := range []string{"-1", "0", "1", "31", "1048577"} {
		if _, err := parseConfig([]string{"-buffer-reseed=" + value}, io.Discard); err == nil {
			t.Errorf("buffer-reseed=%s must not permit a fresh-seed event", value)
		}
	}

	for _, value := range []string{"32", "256", "1048576"} {
		cfg, err := parseConfig([]string{"-buffer-reseed=" + value}, io.Discard)
		if err != nil {
			t.Errorf("buffer-reseed=%s unexpectedly rejected: %v", value, err)

			continue
		}

		if cfg.ReseedSize <= 0 {
			t.Fatalf("invalid size %d", cfg.ReseedSize)
		}
	}
}

func TestStartupConfigurationValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		invalid bool
	}{
		{"defaults", nil, false},
		{"reseed upper bound", []string{"-buffer-reseed=1048576"}, false},
		{"reseed too large", []string{"-buffer-reseed=1048577"}, true},
		{"reseed too small", []string{"-buffer-reseed=31"}, true},
		{"max-bytes zero", []string{"-max-bytes=0"}, true},
		{"max-bytes oversized", []string{"-max-bytes=33554433"}, true},
		{"words zero", []string{"-words=0"}, true},
		{"unsupported DRBG", []string{"-drbg=unknown"}, true},
		{"fingerprint oversized", []string{"-fingerprint=33554433"}, true},
		{"ws connections zero", []string{"-ws-max-connections=0"}, true},
		{"http requests zero", []string{"-http-max-requests=0"}, true},
		{"ws memory overflow", []string{"-ws-memory-mib=8796093022208"}, true},
		{"http memory zero", []string{"-http-memory-mib=0"}, true},
		{"explicit readiness age", []string{"-max-reseed-age=9000"}, false},
		{"derived readiness age", []string{"-reseed-ms=500"}, false},
		{"legacy flags accepted", []string{"-buffer-entropy=0", "-buffer-qrng=0"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := parseConfig(tc.args, io.Discard)
			if (err != nil) != tc.invalid {
				t.Fatalf("parseConfig(%v): cfg=%+v err=%v, invalid=%t", tc.args, cfg, err, tc.invalid)
			}
			if err != nil { return }
			if cfg.MaxReseedAgeMs <= 0 { t.Fatalf("unresolved max reseed age: %d", cfg.MaxReseedAgeMs) }
			if tc.name == "derived readiness age" && cfg.MaxReseedAgeMs != 2000 {
				t.Fatalf("derived age = %d, want 2000", cfg.MaxReseedAgeMs)
			}
		})
	}
}

func TestValidateRejectsDirectInvalidConfiguration(t *testing.T) {
	cfg, err := parseConfig(nil, io.Discard)
	if err != nil { t.Fatal(err) }
	cfg.MaxBytes = 1<<25 + 1
	if err := cfg.Validate(); err == nil { t.Fatal("Validate accepted oversized max-bytes") }
}

func TestDeprecatedBufferHelp(t *testing.T) {
	var output bytes.Buffer
	_, _ = parseConfig([]string{"-help"}, &output)
	for _, word := range []string{"buffer-qrng", "buffer-entropy", "Deprecated; ignored"} {
		if !strings.Contains(output.String(), word) { t.Errorf("help lacks %s", word) }
	}
}
