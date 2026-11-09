package lab

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

// This fixture is also consumed by the browser registration tests. It guards
// operator-visible defaults and validation, without opening any upstream source.
func TestRegistrationValidationParity(t *testing.T) {
	data, err := os.ReadFile("testdata/registration-validation.json")
	if err != nil {
		t.Fatal(err)
	}

	type limits struct {
		Name      string `json:"name"`
		MaxBytes  int    `json:"max_bytes"`
		TimeoutMs int    `json:"timeout_ms"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
	}

	var cases []struct {
		Name       string          `json:"name"`
		Valid      bool            `json:"valid"`
		Source     json.RawMessage `json:"source"`
		Normalized limits          `json:"normalized"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}

	for _, tt := range cases {
		t.Run(tt.Name, func(t *testing.T) {
			var source Config

			decoder := json.NewDecoder(bytes.NewReader(tt.Source))
			decoder.DisallowUnknownFields()

			err := decoder.Decode(&source)

			var registry *Registry

			if err == nil {
				registry, err = New([]Config{source})
			}

			if (err == nil) != tt.Valid {
				t.Fatalf("valid = %v; expected %v: %v", err == nil, tt.Valid, err)
			}

			if err != nil {
				return
			}

			t.Cleanup(registry.Close)

			got, ok := registry.Lookup(source.ID)
			if !ok {
				t.Fatal("accepted source missing from registry")
			}

			actual := limits{got.Name, got.MaxBytes, got.TimeoutMs, got.Width, got.Height}
			if actual != tt.Normalized {
				t.Fatalf("normalized = %+v; expected %+v", actual, tt.Normalized)
			}
		})
	}
}
