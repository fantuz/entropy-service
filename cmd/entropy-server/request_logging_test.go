package main

import (
	"bytes"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestFailedRequestLogging(t *testing.T) {
	for _, code := range []int{200, 204, 400, 404, 413, 415, 500, 503} {
		var output bytes.Buffer

		handler := observeRequests(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(code)
		}), log.New(&output, "", 0))
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/missing?secret=hidden", nil))

		if code < 400 {
			if output.Len() != 0 {
				t.Fatalf("successful request was logged: %s", &output)
			}
		} else if !strings.Contains(output.String(), `method="GET" path="/missing"`) ||
			!strings.Contains(output.String(), "status="+strconv.Itoa(code)) || !strings.Contains(output.String(), "duration_ms=") ||
			strings.Contains(output.String(), "secret") || strings.Count(output.String(), "\n") != 1 {
			t.Fatalf("incorrect failure log: %q", output.String())
		}
	}

	var output bytes.Buffer

	handler := observeRequests(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusEarlyHints)
		w.WriteHeader(http.StatusBadRequest)
		w.WriteHeader(http.StatusInternalServerError) // The first final status wins.
	}), log.New(&output, "", 0))
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	r.URL.Path = "/bad\nline"
	handler.ServeHTTP(httptest.NewRecorder(), r)

	if !strings.Contains(output.String(), `path="/bad\nline" status=400`) || strings.Count(output.String(), "\n") != 1 {
		t.Fatalf("interim status or newline escaping failed: %q", output.String())
	}
}

type loggingReaderFrom struct {
	*httptest.ResponseRecorder

	called bool
}

func (w *loggingReaderFrom) ReadFrom(r io.Reader) (int64, error) {
	w.called = true

	return io.Copy(w.ResponseRecorder, r)
}

type loggingBareWriter struct{ header http.Header }

func (w *loggingBareWriter) Header() http.Header       { return w.header }
func (*loggingBareWriter) WriteHeader(int)             {}
func (*loggingBareWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestLoggingWriterPreservesHTTPFeatures(t *testing.T) {
	underlying := &loggingReaderFrom{ResponseRecorder: httptest.NewRecorder()}
	w := &statusWriter{ResponseWriter: underlying}

	_, err := io.Copy(w, struct{ io.Reader }{strings.NewReader("plain text")})
	if err != nil || !underlying.called || underlying.Body.String() != "plain text" || w.status != http.StatusOK ||
		!strings.HasPrefix(underlying.Header().Get("Content-Type"), "text/plain") {
		t.Fatal("ReaderFrom, body content or automatic content sniffing was lost")
	}

	if w.Unwrap() != underlying || !errors.Is(w.Push("/asset", nil), http.ErrNotSupported) {
		t.Fatal("response unwrapping or unsupported push handling is incorrect")
	}

	empty := &statusWriter{ResponseWriter: &loggingReaderFrom{ResponseRecorder: httptest.NewRecorder()}}
	if _, err := empty.ReadFrom(strings.NewReader("")); err != nil {
		t.Fatal(err)
	}

	empty.WriteHeader(http.StatusBadRequest)

	if empty.status != http.StatusBadRequest {
		t.Fatal("empty copy incorrectly committed success before a later error")
	}

	flushed := httptest.NewRecorder()

	w = &statusWriter{ResponseWriter: flushed}
	if err := http.NewResponseController(w).Flush(); err != nil || !flushed.Flushed || w.status != http.StatusOK {
		t.Fatal("response flushing was lost")
	}

	w = &statusWriter{ResponseWriter: &loggingBareWriter{header: make(http.Header)}}
	if err := http.NewResponseController(w).Flush(); !errors.Is(err, http.ErrNotSupported) {
		t.Fatal("unsupported flush did not propagate")
	}

	w.WriteHeader(http.StatusBadRequest)

	if w.status != http.StatusBadRequest {
		t.Fatal("unsupported flush hid a later failure")
	}

	if _, _, err := w.Hijack(); !errors.Is(err, http.ErrNotSupported) {
		t.Fatal("unsupported hijack did not propagate")
	}
}

func TestFailureLoggingPreservesWebSocketUpgrades(t *testing.T) {
	var output bytes.Buffer

	budget := newWebSocketBudget(2, 1<<20)

	server := httptest.NewServer(observeRequests(wsBinaryHandler(observableTestDRBG(t), 1, 64, 64, budget), log.New(&output, "", 0)))
	defer server.Close()
	defer waitBudgetActive(t, budget, 0)

	conn := budgetConnect(t, server, "/stream?bytes=64")
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if kind, data, err := conn.ReadMessage(); err != nil || kind != websocket.BinaryMessage || len(data) != 64 {
		t.Fatalf("logging broke the upgrade: %v", err)
	}

	_ = conn.Close()

	waitBudgetActive(t, budget, 0)
	// Joining an ordinary request also ensures the preceding observer has returned.
	response := httpBudgetGetCode(t, server, "/stream")
	if response != http.StatusBadRequest || !strings.Contains(output.String(), "status=400") ||
		strings.Contains(output.String(), "status=101") || strings.Count(output.String(), "\n") != 1 {
		t.Fatalf("wrong handshake logging: %q", output.String())
	}
}

func httpBudgetGetCode(t *testing.T, server *httptest.Server, path string) int {
	t.Helper()

	code, _ := httpBudgetGet(t, server.Client(), server.URL+path)

	return code
}
