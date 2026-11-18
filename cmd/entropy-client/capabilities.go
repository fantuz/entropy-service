package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/client"
	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/config"
	"github.com/fantuz/entropy-service/internal/capabilities"
)

const discoveryOff = "off"

type discoveryResult struct {
	Status   string                 `json:"status"`
	Document *capabilities.Document `json:"document,omitempty"`
}

func discoverCapabilities(ctx context.Context, cfg *config.Config, endpoint string) (discoveryResult, error) {
	result := discoveryResult{Status: discoveryOff}
	if cfg.Capabilities == "" || cfg.Capabilities == config.CapabilitiesOff || ctx.Err() != nil {
		return result, nil
	}

	result.Status = "unavailable"
	required := cfg.Capabilities == config.CapabilitiesRequired

	target, ok := client.CapabilityEndpoint(endpoint, required)
	if !ok {
		if required {
			return result, errors.New("capability discovery requires an HTTP(S) or WebSocket endpoint")
		}

		result.Status = "custom endpoint; skipped"

		return result, nil
	}

	probeCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	document, err := client.FetchCapabilities(probeCtx, target)
	if err != nil {
		if required {
			return result, errors.New("capability discovery failed; check the server's /v1/capabilities endpoint and TLS trust")
		}

		return result, nil
	}

	result.Status = "available"

	result.Document = document
	if cfg.Slice > document.MaxBytes {
		return result, fmt.Errorf("requested %d bytes exceeds the server maximum of %d; choose a smaller -slice", cfg.Slice, document.MaxBytes)
	}

	transport := capabilities.TransportHTTP
	if cfg.Mode == modeStream {
		transport = capabilities.TransportWebSocket
	}

	if !slices.Contains(document.Transports, transport) {
		return result, fmt.Errorf("server does not advertise the selected %s transport", transport)
	}

	if cfg.Mode == modeStream && (cfg.Refresh < time.Duration(document.WebSocketMinRefreshMS)*time.Millisecond || cfg.Refresh > time.Duration(document.WebSocketMaxRefreshMS)*time.Millisecond) {
		return result, fmt.Errorf("WebSocket refresh must be between %d and %d milliseconds for this server", document.WebSocketMinRefreshMS, document.WebSocketMaxRefreshMS)
	}

	return result, nil
}
