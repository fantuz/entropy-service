package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestStreamEntropyResponseLengths(t *testing.T) {
	for _, n := range []int{1, 31, 32768, 32769, 65536, 512 << 10, 2 << 20} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			data := bytes.Repeat([]byte{0xa5}, n)
			w := httptest.NewRecorder()

			err := streamEntropyResponse(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil), bytes.NewReader(data), n)

			if err != nil || w.Body.Len() != n || !bytes.Equal(data, w.Body.Bytes()) {
				t.Fatalf("err=%v len=%d want=%d", err, w.Body.Len(), n)
			}

			if w.Result().Header.Get("Content-Length") != strconv.Itoa(n) {
				t.Fatal("wrong Content-Length")
			}
		})
	}
}

type shortOutputWriter struct{ header http.Header }

func (w *shortOutputWriter) Header() http.Header         { return w.header }
func (w *shortOutputWriter) WriteHeader(int)             {}
func (w *shortOutputWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }
func TestStreamEntropyResponseShortWrite(t *testing.T) {
	w := &shortOutputWriter{header: make(http.Header)}

	err := streamEntropyResponse(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil), bytes.NewReader(make([]byte, 65536)), 65536)
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("expected ErrShortWrite; got %v", err)
	}
}

type cancelOutputWriter struct {
	header http.Header
	cancel context.CancelFunc
	calls  int
}

func (w *cancelOutputWriter) Header() http.Header         { return w.header }
func (w *cancelOutputWriter) WriteHeader(int)             {}
func (w *cancelOutputWriter) Write(p []byte) (int, error) { w.calls++; w.cancel(); return len(p), nil }
func TestStreamEntropyResponseCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	w := &cancelOutputWriter{header: make(http.Header), cancel: cancel}

	err := streamEntropyResponse(w, r, bytes.NewReader(make([]byte, 128<<10)), 128<<10)
	if !errors.Is(err, context.Canceled) || w.calls != 1 {
		t.Fatalf("err=%v calls=%d", err, w.calls)
	}
}

func TestStreamEntropyResponsePreCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	w := httptest.NewRecorder()

	err := streamEntropyResponse(w, httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil), bytes.NewReader(make([]byte, 64)), 64)
	if !errors.Is(err, context.Canceled) || w.Header().Get("Content-Length") != "" {
		t.Fatalf("err=%v header=%v", err, w.Header())
	}
}

func TestStreamEntropyResponseRejectZero(t *testing.T) {
	w := httptest.NewRecorder()
	if err := streamEntropyResponse(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil), bytes.NewReader(nil), 0); err == nil {
		t.Fatal("expected error")
	}
}

type failReader struct{ err error }

func (f failReader) Read([]byte) (int, error) { return 0, f.err }
func TestStreamEntropyResponseSourceFailure(t *testing.T) {
	sentinel := errors.New("synthetic entropy failure")
	src := io.MultiReader(bytes.NewReader(make([]byte, 32<<10)), failReader{sentinel})
	w := httptest.NewRecorder()

	err := streamEntropyResponse(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil), src, 64<<10)
	if !errors.Is(err, sentinel) || w.Body.Len() != 32<<10 {
		t.Fatalf("err=%v len=%d", err, w.Body.Len())
	}
}
