// Package capabilities defines the bounded public acquisition contract.
package capabilities

import "errors"

const (
	SchemaVersion      = 1
	TransportHTTP      = "http"
	TransportWebSocket = "websocket"
)

type Document struct {
	SchemaVersion         int      `json:"schema_version"`
	Service               string   `json:"service"`
	Version               string   `json:"version"`
	MaxBytes              int64    `json:"max_bytes"`
	Transports            []string `json:"transports"`
	WebSocketMinRefreshMS int64    `json:"websocket_min_refresh_ms"`
	WebSocketMaxRefreshMS int64    `json:"websocket_max_refresh_ms"`
}

func (d Document) Validate() error {
	if d.SchemaVersion != SchemaVersion || d.Service != "entropy-service" || len(d.Version) > 128 || d.MaxBytes < 1 || d.MaxBytes > 1<<25 || len(d.Transports) < 1 || len(d.Transports) > 2 || d.WebSocketMinRefreshMS < 1 || d.WebSocketMaxRefreshMS < d.WebSocketMinRefreshMS || d.WebSocketMaxRefreshMS > 60000 {
		return errors.New("invalid or unsupported acquisition capabilities")
	}

	seen := make(map[string]bool)
	for _, transport := range d.Transports {
		if (transport != TransportHTTP && transport != TransportWebSocket) || seen[transport] {
			return errors.New("invalid capability transport")
		}

		seen[transport] = true
	}

	return nil
}
