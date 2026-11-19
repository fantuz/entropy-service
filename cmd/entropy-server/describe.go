package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/config"
)

// The desktop interface inspects requested settings without starting a service. These
// allowlisted fields intentionally omit TLS paths and private source registries.
// Enabled features describe configuration, not successful runtime checks.
type serverDescription struct {
	SchemaVersion int                       `json:"schema_version"`
	Type          string                    `json:"type"`
	Role          string                    `json:"role"`
	Live          bool                      `json:"live"`
	Settings      serverDescriptionSettings `json:"settings"`
	Features      serverDescriptionFeatures `json:"features"`
}

type serverDescriptionSettings struct {
	DRBG                string `json:"drbg"`
	HTTPAddress         string `json:"http_address"`
	HTTPSAddress        string `json:"https_address"`
	SeedSource          string `json:"seed_source"`
	ReseedIntervalMS    int    `json:"reseed_interval_ms"`
	MaxReseedAgeMS      int    `json:"max_reseed_age_ms"`
	ReseedBytes         int    `json:"reseed_bytes"`
	SourceTimeout       string `json:"source_timeout"`
	MaxBytes            int    `json:"max_bytes"`
	ReseedFailurePolicy string `json:"reseed_failure_policy"`
	LabMaxConnections   int    `json:"lab_max_connections"`
	LabMemoryMiB        int64  `json:"lab_memory_mib"`
	WSMaxConnections    int    `json:"ws_max_connections"`
	WSMemoryMiB         int64  `json:"ws_memory_mib"`
	HTTPMaxRequests     int    `json:"http_max_requests"`
	HTTPMemoryMiB       int64  `json:"http_memory_mib"`
}

type serverDescriptionFeatures struct {
	HTTPS                 bool `json:"https"`
	RequireDevice         bool `json:"require_device"`
	CPUMix                bool `json:"cpu_mix"`
	RDRANDMix             bool `json:"rdrand_mix"`
	Debug                 bool `json:"debug"`
	Cookie                bool `json:"cookie"`
	Lab                   bool `json:"lab"`
	LabRegistry           bool `json:"lab_registry"`
	LabAllowRemote        bool `json:"lab_allow_remote"`
	CertificateConfigured bool `json:"certificate_configured"`
}

func writeServerDescription(out io.Writer, cfg *config.Config) error {
	description := serverDescription{
		SchemaVersion: 1, Type: "configuration", Role: "server", Live: false,
		Settings: serverDescriptionSettings{
			DRBG: cfg.DRBG, HTTPAddress: cfg.HTTPAddr, HTTPSAddress: cfg.HTTPSAddr,
			SeedSource: cfg.DevicePath, ReseedIntervalMS: cfg.ReseedMs,
			MaxReseedAgeMS: cfg.MaxReseedAgeMs, ReseedBytes: cfg.ReseedSize,
			SourceTimeout: cfg.SourceTimeout.String(), MaxBytes: cfg.MaxBytes,
			ReseedFailurePolicy: string(cfg.OnReseedFailure),
			LabMaxConnections:   cfg.LabMaxConnections, LabMemoryMiB: cfg.LabMemoryMiB,
			WSMaxConnections: cfg.WSMaxConnections, WSMemoryMiB: cfg.WSMemoryMiB,
			HTTPMaxRequests: cfg.HTTPMaxRequests, HTTPMemoryMiB: cfg.HTTPMemoryMiB,
		},
		Features: serverDescriptionFeatures{
			HTTPS: cfg.EnableHTTPS, RequireDevice: cfg.RequireDevice,
			CPUMix: cfg.MixCPU, RDRANDMix: cfg.MixRDRAND, Debug: cfg.ShowDebug,
			Cookie: cfg.WantsCookie, Lab: true, LabRegistry: cfg.LabSources != "",
			LabAllowRemote:        cfg.LabAllowRemote,
			CertificateConfigured: cfg.CertFile != "" && cfg.KeyFile != "",
		},
	}
	if err := json.NewEncoder(out).Encode(description); err != nil {
		return fmt.Errorf("write server configuration: %w", err)
	}

	return nil
}
