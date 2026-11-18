package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/url"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/config"
	"github.com/fantuz/entropy-service/internal/capabilities"
)

// Descriptions contain requested configuration only. They do not prove that an
// endpoint, TLS identity, output destination or hardware device is available.
type clientDescription struct {
	SchemaVersion int             `json:"schema_version"`
	Type          string          `json:"type"`
	Role          string          `json:"role"`
	Live          bool            `json:"live"`
	Settings      reportSettings  `json:"settings"`
	Features      map[string]bool `json:"features"`
}

const (
	descriptionHTTPS = "https"
	descriptionWSS   = "wss"
)

func writeClientDescription(out io.Writer, cfg *config.Config, endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" || u.Opaque != "" {
		return errors.New("describe endpoint requires a complete HTTP(S) or WebSocket URL")
	}

	if (cfg.Mode == modePull && u.Scheme != capabilities.TransportHTTP && u.Scheme != descriptionHTTPS) ||
		(cfg.Mode == modeStream && u.Scheme != "ws" && u.Scheme != descriptionWSS) {
		return errors.New("describe endpoint scheme must match the selected transport")
	}

	endpoint, err = sanitizedEndpoint(endpoint)
	if err != nil {
		return errors.New("describe endpoint is invalid")
	}

	return json.NewEncoder(out).Encode(clientDescription{
		SchemaVersion: reportSchemaVersion, Type: "configuration", Role: "client", Live: false,
		Settings: clientReportSettings(cfg, endpoint),
		Features: map[string]bool{
			"adaptive": cfg.Adaptive,
			"https":    u.Scheme == descriptionHTTPS || u.Scheme == descriptionWSS, "headless": cfg.Headless,
			"matrix": cfg.ShowMatrix, "writeout": cfg.WriteOut, "output": cfg.OutputEnabled(),
			"waterfall": cfg.ShowWaterfall, "report": cfg.ReportPath != "", "report_waterfall": cfg.ReportWaterfall,
		},
	})
}
