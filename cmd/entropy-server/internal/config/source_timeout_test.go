package config

import (
	"io"
	"testing"
	"time"
)

func TestSourceTimeoutBounds(t *testing.T) {
	for _, value := range []string{"0", "-1s", "1ns", "6m", "forever"} {
		if _, err := parseConfig([]string{"-source-timeout=" + value}, io.Discard); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}

	for _, value := range []string{"1ms", "3s", "5m"} {
		cfg, err := parseConfig([]string{"-source-timeout=" + value}, io.Discard)

		want, _ := time.ParseDuration(value)
		if err != nil || cfg.SourceTimeout != want {
			t.Fatalf("%s: %v", value, err)
		}
	}
}
