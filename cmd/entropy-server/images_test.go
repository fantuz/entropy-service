package main

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"testing"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/drbg"
)

func TestRequestedImageSizes(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  int
	}{
		{"", 1024}, {"?size=64", 64}, {"?size=128", 128}, {"?size=255", 255}, {"?size=1024", 1024},
	} {
		size, err := requestedImageSize(httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/"+tc.query, nil))
		if err != nil || size != tc.want {
			t.Fatalf("%s: got %d, %v; want %d", tc.query, size, err, tc.want)
		}
	}
}

func TestInvalidImageSizesRejectBeforeAdmission(t *testing.T) {
	for _, query := range []string{
		"size=", "size=-1", "size=0", "size=63", "size=1025", "size=abc", "size=1.5",
		"size=999999999999999999999", "size=128&size=128", "size=64&size=128", "size=%zz", "size=128;size=256",
	} {
		for _, factory := range []func(*drbg.DRBG, *httpRequestBudget) http.HandlerFunc{randomImageHandler, entropyHeatmapHandler} {
			budget := newHTTPRequestBudget(1, 1)
			response := httptest.NewRecorder()
			// A nil source makes unintended access before validation fail the test.
			factory(nil, budget)(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/?"+query, nil))

			if response.Code != http.StatusBadRequest {
				t.Fatalf("%s: status=%d, want 400", query, response.Code)
			}

			if usage := budget.usage(); usage.Active != 0 || usage.Reserved != 0 || usage.RejectedSize != 0 {
				t.Fatalf("invalid query reached admission: %+v", usage)
			}
		}
	}
}

func TestPreviewSizeAndProportionalAdmission(t *testing.T) {
	for _, factory := range []func(*drbg.DRBG, *httpRequestBudget) http.HandlerFunc{randomImageHandler, entropyHeatmapHandler} {
		d := observableTestDRBG(t)
		budget := newHTTPRequestBudget(1, 2<<20)
		handler := factory(d, budget)

		for _, size := range []int{64, 65, 128} {
			response := httptest.NewRecorder()
			handler(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/?size="+strconv.Itoa(size), nil))

			if response.Code != http.StatusOK || response.Header().Get("X-Image-Size") != strconv.Itoa(size) || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("small image response: status=%d headers=%v", response.Code, response.Header())
			}

			decoded, err := png.Decode(bytes.NewReader(response.Body.Bytes()))
			if err != nil || decoded.Bounds().Size() != image.Pt(size, size) {
				t.Fatalf("preview did not preserve the requested resolution: %v", err)
			}

			if usage := budget.usage(); usage.Active != 0 || usage.Reserved != 0 || usage.PeakReserved != httpReservation(size*size, 16)+imagePNGWorkingMemory {
				t.Fatalf("preview reservation was not proportional or released: %+v", usage)
			}
		}

		if err := d.Close(); err != nil {
			t.Fatal(err)
		}

		response := httptest.NewRecorder()
		handler(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

		if response.Code != http.StatusRequestEntityTooLarge || budget.usage().RejectedSize != 1 {
			t.Fatal("full-size request bypassed the low budget or reached the closed generator")
		}
	}
}

func TestRGBImagePreservesSamplesAndOpaqueAlpha(t *testing.T) {
	data := []byte{255, 0, 0, 0, 0, 255, 0, 1, 0, 0, 255, 2, 255, 255, 255, 3}
	source := bytes.NewReader(data)

	img, err := renderSampleImage(source, 2, false)
	if err != nil {
		t.Fatal(err)
	}

	for i, want := range []color.RGBA{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}, {255, 255, 255, 255}} {
		if got := img.RGBAAt(i%2, i/2); got != want {
			t.Fatalf("RGB sample %d = %v, want %v", i, got, want)
		}
	}

	if source.Len() != 0 {
		t.Fatal("RGB rendering did not consume exactly four bytes per pixel")
	}
}

func TestHeatmapCountsBitsAndMatchesLegend(t *testing.T) {
	source := bytes.NewReader([]byte{0, 1, 3, 7, 15, 31, 63, 127, 255, 128, 192, 224, 240, 248, 252, 254})

	img, err := renderSampleImage(source, 4, true)
	if err != nil {
		t.Fatal(err)
	}

	for i, count := range []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 1, 2, 3, 4, 5, 6, 7} {
		if got := img.RGBAAt(i%4, i/4); got != popcountPalette[count] || got.A != 255 {
			t.Fatalf("bit count at pixel %d mapped to %v, want count %d", i, got, count)
		}
	}

	if source.Len() != 0 {
		t.Fatal("heatmap did not consume exactly one byte per pixel")
	}

	html, err := webFS.ReadFile("web/api-gallery.html")
	if err != nil {
		t.Fatal(err)
	}

	swatches := regexp.MustCompile(`--swatch: (#[0-9a-f]{6})`).FindAllSubmatch(html, -1)
	if len(swatches) != len(popcountPalette) {
		t.Fatal("gallery legend is missing bit-count colours")
	}

	for i, c := range popcountPalette {
		if string(swatches[i][1]) != fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) {
			t.Fatalf("gallery legend differs from the server palette at bit count %d", i)
		}
	}
}

func TestImageRenderingRejectsShortSamples(t *testing.T) {
	for _, heatmap := range []bool{false, true} {
		img, err := renderSampleImage(io.LimitReader(bytes.NewReader([]byte{1}), 1), 2, heatmap)
		if img != nil || err == nil {
			t.Fatal("short input returned a partially generated image")
		}
	}
}

func BenchmarkImagePNG(b *testing.B) {
	data := make([]byte, 4*1024*1024)
	if _, err := rand.Read(data); err != nil {
		b.Fatal(err)
	}

	for _, size := range []int{128, 1024} {
		for _, heatmap := range []bool{false, true} {
			b.Run(fmt.Sprintf("size=%d/heatmap=%t", size, heatmap), func(b *testing.B) {
				source := bytes.NewReader(data)

				b.ReportAllocs()

				for b.Loop() {
					source.Reset(data)

					img, err := renderSampleImage(source, size, heatmap)
					if err != nil {
						b.Fatal(err)
					}

					if err := png.Encode(io.Discard, img); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
