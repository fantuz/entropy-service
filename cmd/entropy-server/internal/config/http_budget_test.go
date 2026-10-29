package config

import (
	"io"
	"testing"
)

func TestHTTPBudgetConfiguration(t *testing.T) {
	cfg, err := parseConfig(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.HTTPMaxRequests != 128 || cfg.HTTPMemoryMiB != 512 {
		t.Fatalf("unexpected HTTP budget defaults: %+v", cfg)
	}

	cfg, err = parseConfig([]string{"-http-max-requests=2", "-http-memory-mib=64"}, io.Discard)
	if err != nil || cfg.HTTPMaxRequests != 2 || cfg.HTTPMemoryMiB != 64 {
		t.Fatalf("HTTP budget overrides: %+v, %v", cfg, err)
	}

	for _, args := range [][]string{
		{"-http-max-requests=0"},
		{"-http-max-requests=-1"},
		{"-http-memory-mib=0"},
		{"-http-memory-mib=-1"},
		{"-http-memory-mib=1GiB"},
		{"-http-memory-mib=9223372036854775807"},
	} {
		if _, err := parseConfig(args, io.Discard); err == nil {
			t.Errorf("accepted invalid HTTP budget: %v", args)
		}
	}
}
