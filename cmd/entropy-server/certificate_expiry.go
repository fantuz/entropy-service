package main

import (
	"context"
	"crypto/x509"
	"fmt"
	"time"
)

const (
	certificateWarningLead = 24 * time.Hour
	reportWarning          = "WARN"
	emptyIssuerWarning     = "empty certificate issuer; some TLS clients reject it; renew explicitly using a new pair of filenames"
)

func certificateExpiry(cert *x509.Certificate, now time.Time) (string, string) {
	deadline := cert.NotAfter.UTC().Format(time.RFC3339)
	switch {
	case now.Before(cert.NotBefore):
		return reportWarning, "not valid before " + cert.NotBefore.UTC().Format(time.RFC3339)
	case !now.Before(cert.NotAfter):
		return reportWarning, "expired at " + deadline + "; renew explicitly and restart with the replacement pair"
	case cert.NotAfter.Sub(now) <= certificateWarningLead:
		return reportWarning, fmt.Sprintf("expires at %s (in %s); prepare renewal and update client trust before restarting", deadline, cert.NotAfter.Sub(now).Round(time.Second))
	default:
		return "OK", fmt.Sprintf("expires at %s (in %s)", deadline, cert.NotAfter.Sub(now).Round(time.Second))
	}
}

// Schedule two bounded notifications for the identity loaded at startup. This
// does not rotate files or reload TLS; cancellation stops and joins via done.
func watchCertificateExpiry(ctx context.Context, cert *x509.Certificate, warn func(string)) {
	for _, at := range []time.Time{cert.NotAfter.Add(-certificateWarningLead), cert.NotAfter} {
		if !at.After(time.Now()) {
			continue // Startup already reports past/near deadlines.
		}

		timer := time.NewTimer(time.Until(at))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		_, message := certificateExpiry(cert, time.Now())
		warn(message)
	}
}
