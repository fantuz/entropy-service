package main

import (
	"crypto/tls"
	"fmt"
	"net/http"
)

// Snapshot the identity actually loaded by the HTTPS listener, not files that
// might have been replaced on disk without restarting the server.
func withTLSMetrics(next http.Handler, cfg *tls.Config) http.Handler {
	enabled := 0

	var expires int64

	if cfg != nil && len(cfg.Certificates) > 0 && cfg.Certificates[0].Leaf != nil {
		enabled = 1
		expires = cfg.Certificates[0].Leaf.NotAfter.Unix()
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		fmt.Fprintf(w, "# HELP rng_https_enabled One when the process configured its HTTPS listener with a loaded certificate; not an external connectivity check\n# TYPE rng_https_enabled gauge\nrng_https_enabled %d\n", enabled)

		if enabled == 1 {
			fmt.Fprintf(w, "# HELP rng_tls_certificate_not_after_timestamp_seconds Expiration Unix timestamp of the certificate loaded by the HTTPS listener; not a certificate file on disk or a trust check\n# TYPE rng_tls_certificate_not_after_timestamp_seconds gauge\nrng_tls_certificate_not_after_timestamp_seconds %d\n", expires)
		}
	})
}
