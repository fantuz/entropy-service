package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/fantuz/entropy-service/internal/capabilities"
)

func TestCapabilityDiscoveryRejectsOversizedRequestBeforeOutputOrGeneration(t *testing.T) {
	var generated atomic.Int32

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/capabilities", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(capabilities.Document{SchemaVersion: 1, Service: "entropy-service", MaxBytes: 64, Transports: []string{"http", "websocket"}, WebSocketMinRefreshMS: 1, WebSocketMaxRefreshMS: 60000})
	})
	mux.HandleFunc("/v1/data/random", func(w http.ResponseWriter, _ *http.Request) { generated.Add(1); _, _ = w.Write(make([]byte, 256)) })

	server := httptest.NewServer(mux)
	defer server.Close()

	path := filepath.Join(t.TempDir(), "run.jsonl")
	cfg := experimentConfig(modePull, server.URL+"/v1/data/random", path)
	cfg.Capabilities = "required"
	cfg.OutputPath = filepath.Join(t.TempDir(), "must-not-open.bin")
	opened := false

	err := runConfiguredWithWriteout(t.Context(), cfg, func() (writeoutSink, error) { opened = true; return nil, errors.New("unexpected output open") })
	if err == nil || !strings.Contains(err.Error(), "server maximum of 64") || generated.Load() != 0 || opened {
		t.Fatalf("limit not enforced before generation: %v", err)
	}

	records := readReport(t, path)
	start := decodeRecord[startRecord](t, records[0])

	end := decodeRecord[summaryRecord](t, records[len(records)-1])
	if start.Discovery.Document.MaxBytes != 64 || end.MeasurementBegan || !end.Failed || end.Termination != reasonStartupError {
		t.Fatal("discovered limit or startup outcome missing from report")
	}
}

func TestCapabilityDiscoveryCompatibilityAndRequiredMode(t *testing.T) {
	for _, mode := range []string{"auto", "off", "required"} {
		cfg := experimentConfig(modePull, serveExperiment(t, modePull)+"/v1/data/random", "")
		cfg.Capabilities = mode

		result, err := discoverCapabilities(t.Context(), cfg, cfg.HTTPUrl)
		if (err != nil) != (mode == "required") {
			t.Fatalf("mode=%s: %v", mode, err)
		}

		if mode == "auto" && result.Status != "unavailable" {
			t.Fatal("old server should retain acquisition support")
		}
	}
}
