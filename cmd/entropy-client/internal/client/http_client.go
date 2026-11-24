package client

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/diag"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/metrics"
)

// The same payload bound applies to HTTP bodies and WebSocket messages.
const maxEntropyBytes = 1 << 25 // 32 MiB

// ErrEntropyTooLarge means the response exceeded the client's payload limit.
var ErrEntropyTooLarge = errors.New("entropy response exceeds 32 MiB limit")

// ErrInsecureRedirect means an HTTPS request tried to redirect to plaintext.
var ErrInsecureRedirect = errors.New("refusing redirect from HTTPS to an insecure endpoint")

var entropyHTTPClient = &http.Client{
	Timeout:       30 * time.Second,
	CheckRedirect: checkEntropyRedirect,
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		IdleConnTimeout:     60 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second,
	},
}

func checkEntropyRedirect(req *http.Request, via []*http.Request) error {
	const secureScheme = "https"

	// A custom policy replaces net/http's default ten-request limit.
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}

	if len(via) > 0 && via[len(via)-1].URL.Scheme == secureScheme && req.URL.Scheme != secureScheme {
		return ErrInsecureRedirect
	}

	return nil
}

// FetchEntropy requests `quantity` bytes from `endpoint` and returns the raw bytes.
// The function takes a context so caller can cancel / set a deadline.
func FetchEntropy(ctx context.Context, endpoint string, quantity int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	resp, err := entropyHTTPClient.Do(req)
	if err != nil {
		return nil, &requestError{cause: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))

		return nil, &HTTPStatusError{Code: resp.StatusCode, Body: string(body), RetryAfter: retryAfter(resp.Header.Get("Retry-After"), time.Now())}
	}

	data, err := readPullBody(resp.Body, resp.ContentLength, quantity)
	if err != nil {
		return nil, err
	}

	if len(data) > maxEntropyBytes {
		return nil, ErrEntropyTooLarge
	}

	metrics.AddClientBytesReceived(uint64(len(data)))

	if diag.ClientRateMeter != nil {
		diag.ClientRateMeter.Update(len(data))
	}

	return data, nil
}

func readPullBody(body io.Reader, length, requested int64) ([]byte, error) {
	if length > maxEntropyBytes {
		return nil, ErrEntropyTooLarge
	}

	// The caller checks for the extra byte before counting a response.
	limited := io.LimitReader(body, maxEntropyBytes+1)

	const prefix = 32 << 10
	if length <= prefix || length != requested {
		return io.ReadAll(limited)
	}

	// Require a matching request and actual incoming data before reserving a
	// large result. A stalled peer cannot trigger the allocation with headers alone.
	buf := bytes.NewBuffer(make([]byte, 0, prefix+bytes.MinRead))
	if _, err := buf.ReadFrom(io.LimitReader(limited, prefix)); err != nil || buf.Len() < prefix {
		return buf.Bytes(), err
	}

	// Leave room for the overflow check and ReadFrom's final EOF read.
	buf.Grow(int(length) - buf.Len() + 1 + bytes.MinRead)
	_, err := buf.ReadFrom(limited)

	return buf.Bytes(), err
}
