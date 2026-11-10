package main

import (
	"context"
	"crypto/x509"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCertificateExpiryStates(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name          string
		before, after time.Time
		state, detail string
	}{
		{"healthy", now.Add(-time.Hour), now.Add(7 * 24 * time.Hour), "OK", "expires at"},
		{"near", now.Add(-time.Hour), now.Add(time.Hour), "WARN", "prepare renewal"},
		{"boundary", now.Add(-time.Hour), now.Add(certificateWarningLead), "WARN", "prepare renewal"},
		{"expired", now.Add(-time.Hour), now, "WARN", "expired at"},
		{"future", now.Add(time.Hour), now.Add(48 * time.Hour), "WARN", "not valid before"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, message := certificateExpiry(&x509.Certificate{NotBefore: tc.before, NotAfter: tc.after}, now)
			if state != tc.state || !strings.Contains(message, tc.detail) {
				t.Fatalf("%s %s", state, message)
			}
		})
	}
}

func TestCertificateExpiryWatcherAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	warnings := make(chan string, 1)

	done := make(chan struct{})
	go func() {
		defer close(done)

		watchCertificateExpiry(ctx, &x509.Certificate{NotAfter: time.Now().Add(200 * time.Millisecond)}, func(message string) { warnings <- message })
	}()

	select {
	case warning := <-warnings:
		if !strings.Contains(warning, "expired at") {
			t.Fatal(warning)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("expiry warning did not fire")
	}

	<-done

	ctx, cancel = context.WithCancel(t.Context())
	cancel()
	watchCertificateExpiry(ctx, &x509.Certificate{NotAfter: time.Now().Add(48 * time.Hour)}, func(string) { t.Fatal("warning after cancellation") })
}

func TestCertificateCommandWarnsBeforeExpiryWithoutRotating(t *testing.T) {
	dir := t.TempDir()
	args := []string{"-cert-file=" + filepath.Join(dir, "cert.pem"), "-key-file=" + filepath.Join(dir, "key.pem"), "-valid-for=1h"}

	var output strings.Builder
	if code := runCertificateCommand(args, &output); code != 0 || !strings.Contains(output.String(), "[WARN] Certificate expiry") {
		t.Fatalf("code=%d %s", code, &output)
	}

	output.Reset()

	reuse := []string{args[0], args[1], "-if-missing"}
	if code := runCertificateCommand(reuse, &output); code != 0 || !strings.Contains(output.String(), "[WARN] Certificate expiry") || !strings.Contains(output.String(), "Reusing valid") {
		t.Fatalf("code=%d %s", code, &output)
	}
}

func TestCertificateWarningLeadFiresBeforeExpiry(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	warnings := make(chan string, 1)

	done := make(chan struct{})
	go func() {
		defer close(done)

		watchCertificateExpiry(ctx, &x509.Certificate{NotAfter: time.Now().Add(certificateWarningLead + 200*time.Millisecond)}, func(message string) { warnings <- message; cancel() })
	}()

	select {
	case message := <-warnings:
		if !strings.Contains(message, "prepare renewal") {
			t.Fatal(message)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("advance warning did not fire")
	}

	<-done
}
