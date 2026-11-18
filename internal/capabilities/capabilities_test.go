package capabilities

import (
	"strings"
	"testing"
)

func TestDocumentValidationBoundsAndForwardVersionPolicy(t *testing.T) {
	valid := Document{SchemaVersion: 1, Service: "entropy-service", Version: "2.6.0-dev", MaxBytes: 1 << 25, Transports: []string{TransportHTTP, TransportWebSocket}, WebSocketMinRefreshMS: 1, WebSocketMaxRefreshMS: 60000}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}

	for name, change := range map[string]func(*Document){
		"schema":             func(d *Document) { d.SchemaVersion = 2 },
		"service":            func(d *Document) { d.Service = "other" },
		"version":            func(d *Document) { d.Version = strings.Repeat("v", 129) },
		"empty":              func(d *Document) { d.MaxBytes = 0 },
		"oversized":          func(d *Document) { d.MaxBytes++ },
		"no transports":      func(d *Document) { d.Transports = nil },
		"duplicate":          func(d *Document) { d.Transports = []string{TransportHTTP, TransportHTTP} },
		"unknown transport":  func(d *Document) { d.Transports = []string{"udp"} },
		"zero interval":      func(d *Document) { d.WebSocketMinRefreshMS = 0 },
		"reversed interval":  func(d *Document) { d.WebSocketMinRefreshMS = 100; d.WebSocketMaxRefreshMS = 99 },
		"oversized interval": func(d *Document) { d.WebSocketMaxRefreshMS = 60001 },
	} {
		t.Run(name, func(t *testing.T) {
			document := valid
			change(&document)

			if document.Validate() == nil {
				t.Fatal("invalid acquisition contract accepted")
			}
		})
	}
}
