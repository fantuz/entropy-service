package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
)

func TestSoundWAV(t *testing.T) {
	for _, style := range []string{"bloom", "chime"} {
		for _, duration := range []int{250, 2500, 5000} {
			t.Run(style+"/"+strconv.Itoa(duration), func(t *testing.T) {
				// One-byte reads exercise io.ReadFull, including the final byte.
				source := iotest.OneByteReader(bytes.NewReader(make([]byte, 64)))
				w := httptest.NewRecorder()
				r := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
					"/sound?style="+style+"&duration_ms="+strconv.Itoa(duration), nil)
				randomSoundHandler(source)(w, r)

				if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "audio/wav" {
					t.Fatalf("status=%d, headers=%v, body=%s", w.Code, w.Header(), w.Body.String())
				}

				data := w.Body.Bytes()

				wantSize := 44 + 48000*duration/1000*2
				if len(data) != wantSize || w.Header().Get("Content-Length") != strconv.Itoa(wantSize) {
					t.Fatalf("got %d bytes, want %d", len(data), wantSize)
				}

				if string(data[:4]) != "RIFF" || string(data[8:16]) != "WAVEfmt " || string(data[36:40]) != "data" {
					t.Fatal("invalid WAV chunk names")
				}

				for offset, want := range map[int]uint32{
					4: uint32(wantSize - 8), 16: 16, 24: 48000, 28: 96000, 40: uint32(wantSize - 44),
				} {
					if got := binary.LittleEndian.Uint32(data[offset:]); got != want {
						t.Errorf("WAV offset %d = %d, want %d", offset, got, want)
					}
				}

				for offset, want := range map[int]uint16{20: 1, 22: 1, 32: 2, 34: 16} {
					if got := binary.LittleEndian.Uint16(data[offset:]); got != want {
						t.Errorf("WAV offset %d = %d, want %d", offset, got, want)
					}
				}

				peak := 0

				for i := 44; i < len(data); i += 2 {
					sample := int(int16(binary.LittleEndian.Uint16(data[i:])))
					peak = max(peak, sample, -sample)
				}

				if peak < 8000 || peak > 8192 {
					t.Errorf("peak = %d, want a non-silent peak near -12 dBFS", peak)
				}

				if binary.LittleEndian.Uint16(data[44:]) != 0 || binary.LittleEndian.Uint16(data[len(data)-2:]) != 0 {
					t.Error("sound does not start and end at zero")
				}

				if w.Header().Get("Cache-Control") != "no-store" {
					t.Error("random sound must not be cached")
				}
			})
		}
	}
}

func TestSoundRejectsInvalidQueriesBeforeReading(t *testing.T) {
	handler := randomSoundHandler(soundReaderFunc(func([]byte) (int, error) {
		t.Fatal("invalid request consumed entropy")

		return 0, io.EOF
	}))

	for _, query := range []string{
		"duration_ms=0", "duration_ms=-1", "duration_ms=249", "duration_ms=5001",
		"duration_ms=", "duration_ms=abc", "duration_ms=1.5", "duration_ms=999999999999999999999",
		"duration_ms=250&duration_ms=500", "style=", "style=noise", "style=bloom&style=chime",
		"style=%xx", "duration_ms=250;style=bloom", "bytes=33554432",
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/sound?"+query, nil)
		handler(w, r)

		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", query, w.Code)
		}
	}
}

func TestSoundDefaultsAndMethods(t *testing.T) {
	handler := randomSoundHandler(soundReaderFunc(func([]byte) (int, error) {
		t.Fatal("HEAD or unsupported method consumed entropy")

		return 0, io.EOF
	}))
	w := httptest.NewRecorder()
	handler(w, httptest.NewRequestWithContext(t.Context(), http.MethodHead, "/sound", nil))

	if w.Code != http.StatusOK || w.Body.Len() != 0 || w.Header().Get("Content-Length") != "240044" ||
		!strings.Contains(w.Header().Get("Content-Disposition"), "entropy-bloom.wav") {
		t.Fatalf("HEAD: status=%d headers=%v body length=%d", w.Code, w.Header(), w.Body.Len())
	}

	w = httptest.NewRecorder()
	handler(w, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/sound", nil))

	if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST: status=%d headers=%v", w.Code, w.Header())
	}
}

func TestSoundShortSource(t *testing.T) {
	for _, size := range []int{0, 31, 63} {
		w := httptest.NewRecorder()
		randomSoundHandler(bytes.NewReader(make([]byte, size)))(w,
			httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/sound", nil))

		if w.Code != http.StatusInternalServerError || w.Header().Get("Content-Type") == "audio/wav" {
			t.Errorf("source size %d: status=%d headers=%v", size, w.Code, w.Header())
		}
	}
}

func TestSoundCancellationAndVariation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := synthesizeSound(ctx, [64]byte{}, soundOptions{250, "bloom"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled rendering: %v", err)
	}

	randomSoundHandler(soundReaderFunc(func([]byte) (int, error) {
		t.Fatal("canceled request consumed entropy")

		return 0, io.EOF
	}))(httptest.NewRecorder(), httptest.NewRequestWithContext(ctx, http.MethodGet, "/sound", nil))

	a, err := synthesizeSound(t.Context(), [64]byte{}, soundOptions{250, "bloom"})
	if err != nil {
		t.Fatal(err)
	}

	var seed [64]byte

	seed[3] = 128 // Change the root frequency, keeping envelope parameters fixed.

	b, err := synthesizeSound(t.Context(), seed, soundOptions{250, "bloom"})
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Equal(a, b) {
		t.Fatal("different pitch parameters produced identical sounds")
	}
}

type soundReaderFunc func([]byte) (int, error)

func (f soundReaderFunc) Read(p []byte) (int, error) { return f(p) }

func TestSoundConcurrencyLimit(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})

	var releaseOnce sync.Once

	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()

	handler := randomSoundHandler(soundReaderFunc(func(p []byte) (int, error) {
		entered <- struct{}{}

		<-release
		clear(p)

		return len(p), nil
	}))

	var wg sync.WaitGroup

	for range 2 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			w := httptest.NewRecorder()
			handler(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/sound?duration_ms=250", nil))

			if w.Code != http.StatusOK {
				t.Errorf("admitted request: status=%d", w.Code)
			}
		}()
	}

	for range 2 {
		select {
		case <-entered:
		case <-t.Context().Done():
			t.Fatal("requests did not reach the source")
		}
	}

	w := httptest.NewRecorder()
	handler(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/sound", nil))

	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "1" {
		t.Errorf("third request: status=%d headers=%v", w.Code, w.Header())
	}

	unblock()
	wg.Wait()

	w = httptest.NewRecorder()
	handler(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/sound?duration_ms=250", nil))

	if w.Code != http.StatusOK {
		t.Errorf("slot was not released: status=%d", w.Code)
	}
}

func BenchmarkSynthesizeSound(b *testing.B) {
	for _, style := range []string{"bloom", "chime"} {
		b.Run(style, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				if _, err := synthesizeSound(b.Context(), [64]byte{}, soundOptions{2500, style}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
