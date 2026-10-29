package config

import (
	"io"
	"testing"
)

func TestWebSocketBudgetConfiguration(t *testing.T) {
	cfg, err := parseConfig(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.WSMaxConnections != 128 || cfg.WSMemoryMiB != 1024 {
		t.Fatalf("unexpected budget defaults: %+v", cfg)
	}

	cfg, err = parseConfig([]string{"-ws-max-connections=2", "-ws-memory-mib=64"}, io.Discard)
	if err != nil || cfg.WSMaxConnections != 2 || cfg.WSMemoryMiB != 64 {
		t.Fatalf("budget overrides: %+v, %v", cfg, err)
	}

	for _, args := range [][]string{
		{"-ws-max-connections=0"},
		{"-ws-max-connections=-1"},
		{"-ws-memory-mib=0"},
		{"-ws-memory-mib=-1"},
		{"-ws-memory-mib=1GiB"},
		{"-ws-memory-mib=9223372036854775807"},
	} {
		if _, err := parseConfig(args, io.Discard); err == nil {
			t.Errorf("accepted invalid budget: %v", args)
		}
	}
}
