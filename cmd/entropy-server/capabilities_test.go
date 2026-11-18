package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fantuz/entropy-service/internal/capabilities"
)

func TestCapabilitiesDescribeConfiguredLimitsWithoutGenerating(t *testing.T) {
	for _, maximum := range []int{1, 65536, 1 << 25} {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /v1/capabilities", capabilitiesHandler(maximum))

		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/capabilities", nil))

		var document capabilities.Document
		if err := json.Unmarshal(w.Body.Bytes(), &document); err != nil {
			t.Fatal(err)
		}

		if err := document.Validate(); err != nil {
			t.Fatal(err)
		}

		if w.Code != 200 || document.MaxBytes != int64(maximum) || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("invalid public contract: %s", w.Body)
		}

		for _, private := range []string{"key_file", "seed_source", "lab_sources", "certificate", "private_key"} {
			if strings.Contains(w.Body.String(), private) {
				t.Fatal("private startup configuration leaked")
			}
		}

		w = httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/capabilities", nil))

		if w.Code != http.StatusMethodNotAllowed {
			t.Fatal("capabilities must be read-only")
		}
	}
}
