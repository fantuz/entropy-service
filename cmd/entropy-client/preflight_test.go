package main

import (
	"strings"
	"testing"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/terminal"
)

func TestPreflightRedactsEndpointsAndWarnsOnFrameMismatch(t *testing.T) {
	cfg := experimentConfig(modePull, "", "")

	var out strings.Builder
	printPreflight(&out, cfg, "https://user:secret@example.test/bytes?token=private#fragment", "wss://example.test/stream?key=hidden", []byte{1, 2}, terminal.Profile{})

	text := out.String()
	for _, secret := range []string{"user", "secret", "token", "private", "fragment", "hidden", "key="} {
		if strings.Contains(text, secret) {
			t.Fatalf("disclosed %q: %s", secret, text)
		}
	}

	if !strings.Contains(text, "https://example.test/bytes") || !strings.Contains(text, "[WARN] 2 / 256 requested bytes") {
		t.Fatal(text)
	}
}
