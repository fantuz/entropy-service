package client

import (
	"io"
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

func fetchEntropy(endpoint *string, quantity *int) ([]byte, error) {

	resp, err := http.Get(*endpoint)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)

	return data, err
}

func FetchEntropySimple(endpoint *string, quantity *int) ([]byte, error) {
    ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
    defer cancel()
    return FetchEntropy(ctx, *endpoint, *quantity)
}

// FetchEntropy requests `quantity` bytes from `endpoint` and returns the raw bytes.
// The function takes a context so caller can cancel / set a deadline.
func FetchEntropy(ctx context.Context, endpoint string, quantity int) ([]byte, error) {
	// Build URL: if endpoint already includes a query param for bytes, you can skip formatting;
	// This example appends "?bytes=" if endpoint doesn't already contain "bytes=".
	url := endpoint
	if quantity > 0 && !containsBytesParam(endpoint) {
		sep := "?"
		if hasQuery(endpoint) {
			sep = "&"
		}
		url = fmt.Sprintf("%s%sbytes=%d", endpoint, sep, quantity)
	}

	// Create a client with a conservative timeout bound (per request).
	// Caller can still use ctx to cancel earlier.
	httpClient := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 5 * time.Second,
			//TLSClientConfig: &tls.Config{InsecureSkipVerify: true},

		},
	}
	
	//fmt.Println("HERE-HTTP:")

	// Build request with context
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Best-effort guard: if server returns an error code, surface it
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}

	// We read the body fully. If you expect very large responses, consider streaming/chunking.
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return data, nil
}

// small helpers to avoid clobbering the endpoint if caller gave "/entropy?bytes=..."
func containsBytesParam(u string) bool {
	return (len(u) >= 6 && (                   // trivial check for "bytes="
		// quick substring search
		// avoid importing strings for tiny helper; but using strings is ok — keep it simple:
		func() bool {
			for i := 0; i+6 <= len(u); i++ {
				if u[i:i+6] == "bytes=" {
					return true
				}
			}
			return false
		}()))
}

func hasQuery(u string) bool {
	for i := 0; i < len(u); i++ {
		if u[i] == '?' {
			return true
		}
	}
	return false
}
