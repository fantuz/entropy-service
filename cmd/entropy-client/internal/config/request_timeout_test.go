package config

import (
	"io"
	"testing"
	"time"
)

func TestRequestTimeoutConfiguration(t *testing.T) {
	for _, value := range []string{"-1s", "1ns", "31s"} {
		cfg, err := parseConfig([]string{"-request-timeout=" + value}, io.Discard)
		if err == nil {
			err = cfg.ValidateAnalysis()
		}

		if err == nil {
			t.Fatalf("accepted invalid timeout %s", value)
		}
	}

	for _, value := range []string{"0", "3s", "15s", "30s"} {
		cfg, err := parseConfig([]string{"-request-timeout=" + value}, io.Discard)
		if err != nil {
			t.Fatal(err)
		}

		wanted := 3 * time.Second
		if value != "0" {
			wanted, _ = time.ParseDuration(value)
		}

		if cfg.PullRequestTimeout() != wanted {
			t.Errorf("%s: got %s, want %s", value, cfg.PullRequestTimeout(), wanted)
		}
	}

	if (&Config{}).PullRequestTimeout() != DefaultRequestTimeout {
		t.Fatal("direct Config construction lost the established default")
	}
}
