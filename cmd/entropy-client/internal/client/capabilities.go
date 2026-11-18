package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/fantuz/entropy-service/internal/capabilities"
)

const (
	capabilityHTTP  = "http"
	capabilityHTTPS = "https"
)

// CapabilityEndpoint preserves a reverse-proxy prefix on recognized routes.
// Query credentials are not copied to another path. No alternate hosts/ports
// are guessed, and custom data endpoints are only probed in required mode.
func CapabilityEndpoint(endpoint string, required bool) (string, bool) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" || u.Opaque != "" {
		return "", false
	}

	switch u.Scheme {
	case "ws":
		u.Scheme = capabilityHTTP
	case "wss":
		u.Scheme = capabilityHTTPS
	case capabilityHTTP, capabilityHTTPS:
	default:
		return "", false
	}

	prefix := ""
	recognized := false

	for _, suffix := range []string{"/v1/data/random", "/v1/data/test", "/stream"} {
		if before, ok := strings.CutSuffix(u.Path, suffix); ok {
			prefix = before
			recognized = true

			break
		}
	}

	if !recognized && !required {
		return "", false
	}

	u.Path = prefix + "/v1/capabilities"
	u.RawPath = ""
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""

	return u.String(), true
}

// FetchCapabilities reuses acquisition TLS/proxy configuration but refuses all
// redirects: discovery must not forward credentials or change serving origin.
func FetchCapabilities(ctx context.Context, endpoint string) (*capabilities.Document, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, errors.New("invalid capabilities URL")
	}

	req.Header.Set("Accept", "application/json")

	httpClient := *entropyHTTPClient
	httpClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

	response, err := httpClient.Do(req)
	if err != nil {
		var requestError *url.Error
		if errors.As(err, &requestError) {
			err = requestError.Err
		}

		return nil, fmt.Errorf("request server capabilities: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("capabilities endpoint returned HTTP %d", response.StatusCode)
	}

	const limit = 16384

	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}

	if len(data) > limit {
		return nil, errors.New("capabilities response exceeds 16 KiB")
	}

	var document capabilities.Document
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, errors.New("invalid capabilities JSON")
	}

	if err := document.Validate(); err != nil {
		return nil, err
	}

	return &document, nil
}
