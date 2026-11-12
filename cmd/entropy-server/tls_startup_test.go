package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestMissingTLSStartupGuidance(t *testing.T) {
	for _, platform := range []string{"linux", tlsWindowsOS} {
		t.Run(platform, func(t *testing.T) {
			hint := missingTLSHint(platform, "My cert.pem", "key's.pem", fmt.Errorf("load TLS certificate: %w", os.ErrNotExist))
			for _, expected := range []string{"generate-cert", "-cert-file=My cert.pem", "-enable-https=false -http=127.0.0.1:8080", "http://127.0.0.1:8080/", "keep the private key private"} {
				if !strings.Contains(hint, expected) {
					t.Fatalf("missing %q in %s", expected, hint)
				}
			}

			if platform == tlsWindowsOS && (!strings.Contains(hint, `.\entropy-server.exe`) || !strings.Contains(hint, "key''s.pem") || !strings.Contains(hint, "docs/windows.md#files-logs-and-tls")) {
				t.Fatal(hint)
			}
		})
	}

	for _, err := range []error{nil, os.ErrPermission, errors.New("invalid certificate")} {
		if hint := missingTLSHint(tlsWindowsOS, "cert", "key", err); hint != "" {
			t.Fatal("unexpected provisioning advice for non-missing identity:", hint)
		}
	}
}
