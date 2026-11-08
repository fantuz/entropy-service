package main

import (
	"net/url"
	"strconv"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/config"
)

// requestEndpoints configures only the selected transport. Other query values
// are preserved; the explicit CLI payload and timing options take precedence.
func requestEndpoints(cfg *config.Config) (string, string, error) {
	endpoint := cfg.HTTPUrl
	if cfg.Mode == modeStream {
		endpoint = cfg.WSUrl
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		return "", "", err
	}

	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", "", err
	}

	query.Set("bytes", strconv.FormatInt(cfg.Slice, 10))
	query.Set("refresh", strconv.FormatInt(cfg.Refresh.Milliseconds(), 10))

	u.RawQuery = query.Encode()

	if cfg.Mode == modeStream {
		return cfg.HTTPUrl, u.String(), nil
	}

	return u.String(), cfg.WSUrl, nil
}
