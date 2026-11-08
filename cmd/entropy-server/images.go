package main

import (
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"math/bits"
	"net/http"
	"net/url"
	"strconv"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/drbg"
)

const (
	defaultImageSize            = 1024
	minImageSize                = 64
	maxImageSize                = 1024
	imagePNGWorkingMemory int64 = 1 << 20
)

// A discrete, ordered palette makes neighbouring bit counts distinguishable.
// Colour encodes the number of one-bits in a byte, not entropy or quality.
var popcountPalette = [9]color.RGBA{
	{0x00, 0x00, 0x04, 0xff},
	{0x1f, 0x0c, 0x48, 0xff},
	{0x55, 0x0f, 0x6d, 0xff},
	{0x88, 0x22, 0x6a, 0xff},
	{0xbc, 0x37, 0x54, 0xff},
	{0xe3, 0x59, 0x33, 0xff},
	{0xf9, 0x8e, 0x09, 0xff},
	{0xf9, 0xc9, 0x32, 0xff},
	{0xfc, 0xff, 0xa4, 0xff},
}

func requestedImageSize(r *http.Request) (int, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return 0, errors.New("invalid image query")
	}

	values, present := query["size"]
	if !present {
		return defaultImageSize, nil
	}

	if len(values) != 1 {
		return 0, errors.New("size must be supplied once")
	}

	size, err := strconv.Atoi(values[0])
	if err != nil || size < minImageSize || size > maxImageSize {
		return 0, errors.New("size must be between 64 and 1024 pixels")
	}

	return size, nil
}

func randomImageHandler(d *drbg.DRBG, budget *httpRequestBudget) http.HandlerFunc {
	return generatedImageHandler(d, budget, false)
}

func entropyHeatmapHandler(d *drbg.DRBG, budget *httpRequestBudget) http.HandlerFunc {
	return generatedImageHandler(d, budget, true)
}

func generatedImageHandler(d *drbg.DRBG, budget *httpRequestBudget, heatmap bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		size, err := requestedImageSize(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		// Allow for RGBA pixels, input bytes and PNG scratch before allocating.
		release, ok := budget.admit(w, r, httpReservation(size*size, 16)+imagePNGWorkingMemory)
		if !ok {
			return
		}
		defer release()

		img, err := renderSampleImage(d, size, heatmap)
		if err != nil {
			http.Error(w, "image generation failed", http.StatusInternalServerError)

			return
		}

		metric, palette, ageHeader := "random-image", "rgb", "X-Rng-Reseed-Age-Ms-Test"
		if heatmap {
			metric, palette, ageHeader = "bit-popcount", "popcount-9", "X-Rng-Reseed-Age-Ms"
		}

		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Refresh", "5")
		w.Header().Set("X-Entropy-Metric", metric)
		w.Header().Set("X-Image-Size", strconv.Itoa(size))
		w.Header().Set("X-Image-Palette", palette)
		w.Header().Set(ageHeader, strconv.FormatInt(d.ReseedAge().Milliseconds(), 10))

		if err := png.Encode(w, img); err != nil {
			// A PNG response may already have started; do not append an error body.
			log.Printf("image response interrupted: %v", err)
		}
	}
}

func renderSampleImage(source io.Reader, size int, heatmap bool) (*image.RGBA, error) {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	if !heatmap {
		// Preserve the RGB mapping and four source bytes per pixel. Alpha is
		// always opaque, so sample alpha bytes never hide the colours.
		if _, err := io.ReadFull(source, img.Pix); err != nil {
			return nil, err
		}

		for i := 3; i < len(img.Pix); i += 4 {
			img.Pix[i] = 255
		}

		return img, nil
	}

	data := make([]byte, size*size)
	if _, err := io.ReadFull(source, data); err != nil {
		return nil, err
	}

	for i, value := range data {
		c := popcountPalette[bits.OnesCount8(value)]
		offset := i * 4
		img.Pix[offset], img.Pix[offset+1], img.Pix[offset+2], img.Pix[offset+3] = c.R, c.G, c.B, c.A
	}

	return img, nil
}
