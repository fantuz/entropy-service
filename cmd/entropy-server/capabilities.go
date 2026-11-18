package main

import (
	"encoding/json"
	"net/http"

	"github.com/fantuz/entropy-service/internal/capabilities"
)

// Configuration limits, not readiness or a guarantee of sustainable throughput.
// No device identities, private paths, registry contents or key material.
func capabilitiesHandler(maxBytes int) http.HandlerFunc {
	document := capabilities.Document{
		SchemaVersion: capabilities.SchemaVersion, Service: "entropy-service", Version: appVersion,
		MaxBytes: int64(maxBytes), Transports: []string{capabilities.TransportHTTP, capabilities.TransportWebSocket}, WebSocketMinRefreshMS: 1, WebSocketMaxRefreshMS: 60000,
	}

	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_ = json.NewEncoder(w).Encode(document)
	}
}
