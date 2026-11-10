package main

import (
	"crypto/tls"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fantuz/entropy-service/internal/console"
)

func TestStartupChecksDistinguishFallbackAndCertificateDates(t *testing.T) {
	fixture := httptest.NewTLSServer(nil)
	defer fixture.Close()

	cert := fixture.Certificate()
	for _, tc := range []struct {
		name string
		now  time.Time
		want string
	}{
		{"valid", cert.NotBefore.Add(time.Second), "[OK]"},
		{"expired", cert.NotAfter.Add(time.Second), "[WARN]"},
		{"future", cert.NotBefore.Add(-time.Second), "[WARN]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder

			r := console.New(&out, "checks")
			reportTLS(r, &tls.Config{Certificates: fixture.TLS.Certificates}, tc.now)
			reportSource(r, "/dev/unavailable", DefaultCSPRNG)

			text := out.String()
			for line := range strings.SplitSeq(text, "\n") {
				if strings.Contains(line, "Certificate dates") && !strings.Contains(line, tc.want) {
					t.Fatal(line)
				}
			}

			for _, want := range []string{"TLS 1.3", "trust/hostname not checked", "[WARN] configured device unavailable", DefaultCSPRNG, "operating-system CSPRNG"} {
				if !strings.Contains(text, want) {
					t.Fatalf("missing %q in %s", want, text)
				}
			}
		})
	}

	var out strings.Builder
	reportTLS(console.New(&out, "checks"), nil, time.Now())

	if !strings.Contains(out.String(), "[OFF]") || strings.Contains(out.String(), "[OK]") {
		t.Fatal("disabled TLS reported as a successful check")
	}
}
