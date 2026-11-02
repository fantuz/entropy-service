package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/drbg"
)

func TestStreamingRoutesAndBudgetRelease(t *testing.T) {
	d := observableTestDRBG(t)

	for _, endpoint := range []string{"/v1/data/random", "/v1/data/test"} {
		for _, count := range []int{1, 32768, 32769, 2 << 20} {
			t.Run(endpoint+"/"+strconv.Itoa(count), func(t *testing.T) {
				budget := newHTTPRequestBudget(8, 64<<20)

				var handler http.HandlerFunc
				if strings.HasSuffix(endpoint, "/random") {
					handler = randomBytesHandler(d, 1<<25, 20, false, budget)
				} else {
					handler = randomHandler(d, 1<<25, budget)
				}

				w := httptest.NewRecorder()
				handler(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, endpoint+"?bytes="+strconv.Itoa(count), nil))
				if w.Code != http.StatusOK || w.Body.Len() != count || w.Header().Get("Content-Length") != strconv.Itoa(count) {
					t.Fatalf("status=%d len=%d headers=%v", w.Code, w.Body.Len(), w.Header())
				}
				if usage := budget.usage(); usage.Active != 0 || usage.Reserved != 0 {
					t.Fatalf("reservation leaked: %+v", usage)
				}
			})
		}
	}
}

func TestWebSocketOriginPolicyEndToEnd(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			_ = c.Close()
		}
	}))
	defer server.Close()

	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	for _, tc := range []struct {
		name, origin string
		allowed      bool
	}{
		{"NoOrigin", "", true},
		{"SameOrigin", server.URL, true},
		{"CrossOrigin", "https://other.example", false},
		{"NullOrigin", "null", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hdr := http.Header{}
			if tc.origin != "" {
				hdr.Set("Origin", tc.origin)
			}

			dialer := websocket.Dialer{HandshakeTimeout: 2 * time.Second}

			conn, response, err := dialer.Dial(endpoint, hdr)
			if response != nil && response.Body != nil {
				defer response.Body.Close()
			}

			if conn != nil {
				_ = conn.Close()
			}

			if tc.allowed && err != nil {
				t.Fatalf("expected successful handshake; %v", err)
			}

			if !tc.allowed && (response == nil || response.StatusCode != http.StatusForbidden) {
				t.Fatalf("expected 403; response=%v err=%v", response, err)
			}
		})
	}
}

func BenchmarkHTTPGeneration(b *testing.B) {
	for _, algo := range []drbg.Algorithm{drbg.ChaCha20, drbg.AES256CTR} {
		for _, n := range []int{64 << 10, 512 << 10, 2 << 20, 32 << 20} {
			for _, endpoint := range []string{"random", "test"} {
				b.Run(algo.String()+"/"+endpoint+"/"+strconv.Itoa(n), func(b *testing.B) {
					root := newBenchmarkDRBG(b, algo)
					budget := newHTTPRequestBudget(128, 2<<30)

					var handler http.HandlerFunc
					if endpoint == "random" {
						handler = randomBytesHandler(root, 1<<25, 20, false, budget)
					} else {
						handler = randomHandler(root, 1<<25, budget)
					}

					req := httptest.NewRequestWithContext(b.Context(), http.MethodGet, "/v1/data/"+endpoint+"?bytes="+strconv.Itoa(n), nil)
					b.SetBytes(int64(n))
					b.ReportAllocs()
					b.ResetTimer()

					for range b.N {
						w := &streamDiscardWriter{header: make(http.Header)}
						handler(w, req)

						if w.count != int64(n) {
							b.Fatalf("got=%d want=%d", w.count, n)
						}
					}
				})
			}
		}
	}
}

func newBenchmarkDRBG(tb testing.TB, algo drbg.Algorithm) *drbg.DRBG {
	tb.Helper()

	seed := make([]byte, 64)
	for i := range seed {
		seed[i] = byte(i + 1)
	}

	d, err := drbg.NewDRBGWithAlgorithm(seed, algo)
	if err != nil {
		tb.Fatal(err)
	}

	tb.Cleanup(func() { drbg.DecreaseActiveInstances(1) })

	return &d
}

type streamDiscardWriter struct {
	header http.Header
	count  int64
}

func (w *streamDiscardWriter) Header() http.Header { return w.header }
func (w *streamDiscardWriter) WriteHeader(int)     {}
func (w *streamDiscardWriter) Write(p []byte) (int, error) {
	w.count += int64(len(p))
	return len(p), nil
}

// Baseline uses the same child-generator derivation as /v1/data/random but
// buffers the entire output before writing, to compare B/op against streaming.
func BenchmarkHTTPFullBufferBaseline(b *testing.B) {
	for _, algo := range []drbg.Algorithm{drbg.ChaCha20, drbg.AES256CTR} {
		for _, n := range []int{64 << 10, 512 << 10, 2 << 20, 32 << 20} {
			b.Run(algo.String()+"/"+strconv.Itoa(n), func(b *testing.B) {
				root := newBenchmarkDRBG(b, algo)
				b.SetBytes(int64(n))
				b.ReportAllocs()
				b.ResetTimer()

				for range b.N {
					child, err := drbg.NewConnectionDRBG(root)
					if err != nil {
						b.Fatal(err)
					}

					buf := make([]byte, n)
					_, err = io.ReadFull(&child, buf)

					drbg.DecreaseActiveInstances(1)

					if err != nil {
						b.Fatal(err)
					}

					if _, err = io.Discard.Write(buf); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func TestStreamingBudgetReleaseAfterWriteFailure(t *testing.T) {
	d := observableTestDRBG(t)
	budget := newHTTPRequestBudget(1, 1<<20)
	h := randomBytesHandler(d, 1<<25, 20, false, budget)
	w := &shortOutputWriter{header: make(http.Header)}
	h(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/data/random?bytes=65536", nil))

	if usage := budget.usage(); usage.Active != 0 || usage.Reserved != 0 {
		t.Fatalf("write failure leaked request budget: %+v", usage)
	}
}

func TestStreamingBudgetReleaseAfterCancellation(t *testing.T) {
	d := observableTestDRBG(t)
	budget := newHTTPRequestBudget(1, 1<<20)
	h := randomBytesHandler(d, 1<<25, 20, false, budget)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w := &cancelOutputWriter{header: make(http.Header), cancel: cancel}
	h(w, httptest.NewRequestWithContext(ctx, http.MethodGet, "/v1/data/random?bytes=131072", nil))
	if w.calls != 1 {
		t.Fatalf("expected cancellation after first chunk, calls=%d", w.calls)
	}
	if usage := budget.usage(); usage.Active != 0 || usage.Reserved != 0 {
		t.Fatalf("cancellation leaked request budget: %+v", usage)
	}
}

func TestStreamingConcurrencyAndBudget(t *testing.T) {
	d := observableTestDRBG(t)
	budget := newHTTPRequestBudget(16, 64<<20)
	h := randomBytesHandler(d, 1<<25, 20, false, budget)

	const workers = 16

	errs := make(chan error, workers)

	var wg sync.WaitGroup

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()

			w := httptest.NewRecorder()
			h(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/data/random?bytes=524288", nil))
			if w.Code != http.StatusOK || w.Body.Len() != 524288 {
				errs <- fmt.Errorf("status=%d bytes=%d", w.Code, w.Body.Len())
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}

	if usage := budget.usage(); usage.Active != 0 || usage.Reserved != 0 {
		t.Fatalf("concurrent requests leaked budget: %+v", usage)
	}
}

func TestHTTPStreamingPreservesConservativeMemoryLimit(t *testing.T) {
	// This revision intentionally preserves the current request-byte
	// reservation policy, so previously rejected 2 MiB requests stay rejected.
	budget := newHTTPRequestBudget(8, 1<<20)
	d := observableTestDRBG(t)
	w := httptest.NewRecorder()
	randomBytesHandler(d, 1<<25, 20, false, budget)(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/data/random?bytes=2097152", nil))
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("memory admission unexpectedly relaxed: %d", w.Code)
	}
}
