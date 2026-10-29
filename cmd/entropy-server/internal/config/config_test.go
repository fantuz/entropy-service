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
		cfg.PageRefreshMs != 5000 || cfg.MaxReseedAgeMs != 0 {
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
