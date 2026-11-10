package client

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
)

// HTTPStatusError preserves retry information without treating an error body as
// entropy. Remote text is quoted so terminal control characters cannot execute.
type HTTPStatusError struct {
	Code       int
	Body       string
	RetryAfter time.Duration
}

func (e *HTTPStatusError) Error() string {
	message := fmt.Sprintf("server returned %d: %q", e.Code, e.Body)
	if e.RetryAfter > 0 {
		message += fmt.Sprintf(" (Retry-After %s)", e.RetryAfter)
	}

	return message
}

type streamReadError struct{ cause error }

type requestError struct{ cause error }

func (e *requestError) Error() string {
	cause := e.cause

	var urlError *url.Error
	if errors.As(cause, &urlError) {
		cause = urlError.Err // The URL may contain credentials or secret query values.
	}

	return fmt.Sprintf("HTTP entropy request failed: %q", cause.Error())
}

func (e *requestError) Unwrap() error { return e.cause }

func (e *streamReadError) Error() string {
	var closeError *websocket.CloseError
	if errors.As(e.cause, &closeError) {
		return fmt.Sprintf("WebSocket closed with code %d: %q", closeError.Code, closeError.Text)
	}

	return fmt.Sprintf("read WebSocket entropy: %v", e.cause)
}

func (e *streamReadError) Unwrap() error { return e.cause }

func retryAfter(value string, now time.Time) time.Duration {
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err == nil && seconds >= 0 && seconds <= int64((1<<63-1)/time.Second) {
		return time.Duration(seconds) * time.Second
	}

	date, err := http.ParseTime(value)
	if err == nil && date.After(now) {
		return date.Sub(now)
	}

	return 0
}

// RetryableFailure admits only temporary transport failures. Authentication,
// certificate verification, protocol and payload-size failures remain terminal.
func RetryableFailure(err error) (time.Duration, bool) {
	var status *HTTPStatusError
	if errors.As(err, &status) {
		switch status.Code {
		case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError,
			http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return status.RetryAfter, true
		default:
			return 0, false
		}
	}

	var closeError *websocket.CloseError
	if errors.As(err, &closeError) {
		switch closeError.Code {
		case websocket.CloseGoingAway, websocket.CloseAbnormalClosure, websocket.CloseInternalServerErr,
			websocket.CloseServiceRestart, websocket.CloseTryAgainLater:
			return 0, true
		default:
			return 0, false
		}
	}

	var (
		verification *tls.CertificateVerificationError
		unknown      x509.UnknownAuthorityError
		hostname     x509.HostnameError
		invalid      x509.CertificateInvalidError
		record       tls.RecordHeaderError
		dns          *net.DNSError
		network      net.Error
		operation    *net.OpError
		stream       *streamReadError
	)
	if errors.As(err, &verification) || errors.As(err, &unknown) || errors.As(err, &hostname) ||
		errors.As(err, &invalid) || errors.As(err, &record) || (errors.As(err, &dns) && dns.IsNotFound) {
		return 0, false
	}

	if errors.As(err, &stream) && (errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)) {
		return 0, false // A complete WebSocket message with a short payload is a protocol error.
	}

	return 0, errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &operation) || (errors.As(err, &network) && network.Timeout())
}
