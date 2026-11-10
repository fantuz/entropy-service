package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestTransportRetryClassification(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"HTTP 503", &HTTPStatusError{Code: 503}, true},
		{"HTTP 429", &HTTPStatusError{Code: 429}, true},
		{"HTTP forbidden", &HTTPStatusError{Code: 403}, false},
		{"WS retry", &websocket.CloseError{Code: 1013}, true},
		{"WS restart", &websocket.CloseError{Code: 1012}, true},
		{"WS abrupt disconnect", &websocket.CloseError{Code: 1006}, true},
		{"WS normal end", &websocket.CloseError{Code: 1000}, false},
		{"WS policy", &websocket.CloseError{Code: 1008}, false},
		{"WS short complete payload", &streamReadError{cause: io.ErrUnexpectedEOF}, false},
		{"HTTP truncated body", io.ErrUnexpectedEOF, true},
		{"network", &net.OpError{Op: "read", Err: io.EOF}, true},
		{"deadline", context.DeadlineExceeded, true},
		{"cancel", context.Canceled, false},
		{"unknown authority", &url.Error{Op: "Get", Err: x509.UnknownAuthorityError{}}, false},
		{"hostname", x509.HostnameError{}, false},
		{"expired certificate", x509.CertificateInvalidError{}, false},
		{"certificate verification", &tls.CertificateVerificationError{Err: errors.New("untrusted")}, false},
		{"plain HTTP on TLS port", tls.RecordHeaderError{}, false},
		{"DNS not found", &net.DNSError{IsNotFound: true}, false},
		{"bad URL", &url.Error{Op: "Get", Err: errors.New("unsupported protocol")}, false},
		{"oversized", ErrEntropyTooLarge, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, got := RetryableFailure(fmt.Errorf("wrapped: %w", tc.err))
			if got != tc.want {
				t.Fatalf("retry=%t, want %t for %T", got, tc.want, tc.err)
			}
		})
	}
}

func TestHTTPFailurePreservesStatusAndRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("paused\x1b[2J\n"))
	}))
	defer server.Close()

	for _, endpoint := range []string{server.URL, "ws" + strings.TrimPrefix(server.URL, "http")} {
		var err error
		if strings.HasPrefix(endpoint, "ws") {
			_, err = StreamEntropy(t.Context(), endpoint)
		} else {
			_, err = FetchEntropy(t.Context(), endpoint, 64)
		}

		var status *HTTPStatusError
		if !errors.As(err, &status) || status.Code != 503 || status.RetryAfter != 2*time.Second {
			t.Fatalf("status/retry hint lost: %v", err)
		}

		if strings.ContainsAny(err.Error(), "\x1b\n\r") {
			t.Fatal("remote error text escaped into terminal control output")
		}
	}
}

func TestReceiveEntropyPreservesCloseReasonAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		_ = conn.WriteMessage(websocket.BinaryMessage, []byte{1, 2, 3})
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(1013, "source paused\x1b[2J"), time.Now().Add(time.Second))
	}))
	defer server.Close()

	count := 0
	err := ReceiveEntropy(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http"), func(frame StreamFrame) error {
		count++

		frame.Release()

		return nil
	})

	var cause *websocket.CloseError
	if count != 1 || !errors.As(err, &cause) || cause.Code != 1013 || !strings.Contains(err.Error(), "source paused") || strings.ContainsRune(err.Error(), '\x1b') {
		t.Fatalf("disconnect details lost: frames=%d error=%v", count, err)
	}
}

func TestRetryAfterParsing(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		value string
		want  time.Duration
	}{
		{"3", 3 * time.Second},
		{now.Add(5 * time.Second).Format(http.TimeFormat), 5 * time.Second},
		{"invalid", 0},
		{"-1", 0},
		{"99999999999999999999", 0},
		{now.Add(-time.Second).Format(http.TimeFormat), 0},
	} {
		if got := retryAfter(tc.value, now); got != tc.want {
			t.Fatalf("%q: %v, want %v", tc.value, got, tc.want)
		}
	}
}

func TestHTTPTransportDiagnosticsOmitURLSecrets(t *testing.T) {
	secret := t.Name()
	endpoint := &url.URL{Scheme: "https", Host: "example.invalid", User: url.UserPassword("fixture", secret), RawQuery: "token=" + url.QueryEscape(secret)}
	underlying := &url.Error{Op: "Get", URL: endpoint.String(), Err: context.DeadlineExceeded}

	err := &requestError{cause: underlying}
	for _, value := range []string{secret, "token=", "fixture@"} {
		if strings.Contains(err.Error(), value) {
			t.Fatalf("leaked URL information: %v", err)
		}
	}

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("redaction lost the underlying cause")
	}
}
