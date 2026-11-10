package config

import (
	"io"
	"strings"
	"testing"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/drbg"
)

func TestReseedFailurePolicyDefaultsAndValidation(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want drbg.ReseedFailurePolicy
	}{
		{nil, drbg.ReseedStop},
		{[]string{"-on-reseed-failure=stop"}, drbg.ReseedStop},
		{[]string{"-on-reseed-failure=continue"}, drbg.ReseedContinue},
	} {
		cfg, err := parseConfig(tc.args, io.Discard)
		if err != nil || cfg.OnReseedFailure != tc.want {
			t.Fatalf("args=%v policy=%v error=%v", tc.args, cfg, err)
		}
	}

	for _, value := range []string{"", "degrade", "STOP", "stop,continue"} {
		if _, err := parseConfig([]string{"-on-reseed-failure=" + value}, io.Discard); err == nil || !strings.Contains(err.Error(), "stop or continue") {
			t.Fatalf("accepted invalid policy %q: %v", value, err)
		}
	}
}
