package main

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/drbg"
)

func TestImageHandlersProducePNGAndReleaseAdmission(t *testing.T) {
	for _, tc := range []struct {
		path        string
		handler     func(*drbg.DRBG) http.HandlerFunc
		size        image.Point
		reservation int64
	}{
		{"/v1/image/random", randomImageHandler, image.Pt(1024, 1024), 16 << 20},
		{"/v1/image/heatmap", entropyHeatmapHandler, image.Pt(1024, 1024), 16 << 20},
		{"/v1/qr/random", qrHandler, image.Pt(256, 256), 4 << 20},
	} {
		t.Run(tc.path, func(t *testing.T) {
			d := observableTestDRBG(t)
			budget := newHTTPRequestBudget(1, 32<<20)
			handler := budget.wrap(tc.handler(d), tc.reservation)
			response := httptest.NewRecorder()
			handler(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.path, nil))

			if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/png" {
				t.Fatalf("image response: status=%d type=%q", response.Code, response.Header().Get("Content-Type"))
			}

			decoded, err := png.Decode(bytes.NewReader(response.Body.Bytes()))
			if err != nil {
				t.Fatalf("response was not a complete PNG: %v", err)
			}

			if decoded.Bounds().Size() != tc.size {
				t.Fatalf("image dimensions = %v, want %v", decoded.Bounds().Size(), tc.size)
			}

			opaque, ok := decoded.(interface{ Opaque() bool })
			if !ok || !opaque.Opaque() {
				t.Fatal("generated image contains unexpected transparency")
			}

			if usage := budget.usage(); usage.Active != 0 || usage.Reserved != 0 {
				t.Fatalf("successful image response retained admission: %+v", usage)
			}

			if err := d.Close(); err != nil {
				t.Fatal(err)
			}

			failed := httptest.NewRecorder()
			handler(failed, httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.path, nil))

			if failed.Code != http.StatusInternalServerError || failed.Header().Get("Content-Type") == "image/png" {
				t.Fatalf("closed generator did not return an image-generation error: status=%d", failed.Code)
			}

			if usage := budget.usage(); usage.Active != 0 || usage.Reserved != 0 {
				t.Fatalf("failed image response retained admission: %+v", usage)
			}
		})
	}
}
