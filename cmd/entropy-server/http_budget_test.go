package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestHTTPBudgetSharedListenersAndRecovery(t *testing.T) {
	for _, limit := range []string{"requests", "memory"} {
		t.Run(limit, func(t *testing.T) {
			budget := newHTTPRequestBudget(1, 2<<20)
			if limit == "memory" {
				budget = newHTTPRequestBudget(128, 1<<20)
			}

			d := observableTestDRBG(t)
			entered, release := make(chan struct{}), make(chan struct{})

			var releaseOnce sync.Once

			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			mux := http.NewServeMux()
			mux.HandleFunc("/hold", budget.wrap(func(_ http.ResponseWriter, r *http.Request) {
				close(entered)

				select {
				case <-release:
				case <-r.Context().Done():
				}
			}, 1<<20))
			mux.HandleFunc("/random", randomBytesHandler(d, 1<<25, 20, false, budget))
			mux.HandleFunc("/test", randomHandler(d, 1<<25, budget))
			mux.HandleFunc("/words", randomWordsHandler(d, 20, 1000, 1<<25, false, budget))
			mux.HandleFunc("/live", livenessHandler)
			mux.HandleFunc("/ready", healthHandler(d, time.Hour))
			mux.HandleFunc("/metrics", metricsHandler(d, time.Hour, newWebSocketBudget(128, 1<<30), budget))
			mux.HandleFunc("/static", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("page")) })

			plain, secure := httptest.NewServer(mux), httptest.NewTLSServer(mux)
			defer plain.Close()
			defer secure.Close()
			defer unblock()

			done := make(chan error, 1)

			go func() {
				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, plain.URL+"/hold", nil)
				if err == nil {
					var response *http.Response

					response, err = plain.Client().Do(req)
					if response != nil {
						_ = response.Body.Close()
					}
				}

				done <- err
			}()

			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("HTTP reservation was not acquired")
			}

			for _, path := range []string{"/random?bytes=64", "/test?bytes=64", "/words?words=1"} {
				code, headers := httpBudgetGet(t, secure.Client(), secure.URL+path)
				if code != http.StatusServiceUnavailable || headers.Get("Retry-After") != "1" {
					t.Fatalf("shared %s budget did not reject %s: %d", limit, path, code)
				}
			}

			for _, server := range []*httptest.Server{plain, secure} {
				for _, path := range []string{"/live", "/ready", "/metrics", "/static"} {
					code, _ := httpBudgetGet(t, server.Client(), server.URL+path)
					if code != http.StatusOK {
						t.Fatalf("budget blocked %s: %d", path, code)
					}
				}
			}

			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))

			values, kinds := parseMetricSamples(t, w.Body.String())
			if values["http_generation_requests_active"] != 1 || values["http_generation_memory_reserved_bytes"] != 1<<20 ||
				kinds["http_generation_requests_active"] != "gauge" || kinds["http_generation_rejected_memory_total"] != "counter" {
				t.Fatal("HTTP generation metrics did not report held work")
			}

			if (limit == "requests" && values["http_generation_rejected_requests_total"] != 3) ||
				(limit == "memory" && values["http_generation_rejected_memory_total"] != 3) {
				t.Fatal("HTTP generation rejection reason was not counted")
			}

			unblock()

			if err := <-done; err != nil {
				t.Fatal(err)
			}

			code, _ := httpBudgetGet(t, secure.Client(), secure.URL+"/random?bytes=64")
			if code != http.StatusOK || budget.usage().Active != 0 || budget.usage().Reserved != 0 {
				t.Fatal("HTTP capacity did not recover after completion")
			}
		})
	}
}

func httpBudgetGet(t *testing.T, client *http.Client, endpoint string) (int, http.Header) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}

	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatal(err)
	}

	return response.StatusCode, response.Header
}

func TestHTTPBudgetRejectsBeforeGenerating(t *testing.T) {
	d := observableTestDRBG(t)
	budget := newHTTPRequestBudget(128, httpReservation(64, 1))
	maximumInt := int(^uint(0) >> 1)

	source := new(wordTestSource)
	for path, handler := range map[string]http.HandlerFunc{
		"/random?bytes=65":                         randomBytesHandler(d, 1<<25, 20, false, budget),
		"/test?bytes=65":                           randomHandler(d, 1<<25, budget),
		"/words?words=" + strconv.Itoa(maximumInt): randomWordsHandler(source, maximumInt, 1000, 1<<25, false, budget),
		"/image":   budget.wrap(randomImageHandler(d), 16<<20),
		"/heatmap": budget.wrap(entropyHeatmapHandler(d), 16<<20),
		"/qr":      budget.wrap(qrHandler(d), 4<<20),
		"/sound":   budget.wrap(randomSoundHandler(d), 8<<20),
	} {
		w := httptest.NewRecorder()
		handler(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))

		if w.Code != http.StatusRequestEntityTooLarge || budget.usage().Active != 0 || budget.usage().Reserved != 0 {
			t.Fatalf("oversized %s was admitted: %d", path, w.Code)
		}
	}

	if source.reads.Load() != 0 || budget.usage().RejectedSize != 7 {
		t.Fatal("oversized requests generated words or were not counted")
	}

	// Admission uses the validated cookie choice and the query's precedence.
	for _, query := range []string{"", "?bytes=65"} {
		w, cookie := httptest.NewRecorder(), httptest.NewRecorder()
		if err := setPrefsCookie(cookie, ClientPrefs{Bytes: 64, Words: 1, Refresh: 1000}); err != nil {
			t.Fatal(err)
		}

		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/random"+query, nil)
		r.AddCookie(cookie.Result().Cookies()[0])
		randomBytesHandler(d, 1<<25, 20, true, budget)(w, r)

		if (query == "" && (w.Code != http.StatusOK || w.Body.Len() != 64)) ||
			(query != "" && w.Code != http.StatusRequestEntityTooLarge) {
			t.Fatal("reservation did not follow cookie/query size selection")
		}
	}
}

func TestHTTPBudgetReleaseOnFailureAndCancellation(t *testing.T) {
	budget := newHTTPRequestBudget(1, 1<<20)
	w := httptest.NewRecorder()
	randomWordsHandler(bytes.NewReader(nil), 1, 1000, 64, false, budget)(w,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/words", nil))

	if w.Code != http.StatusInternalServerError || budget.usage().Active != 0 || budget.usage().Reserved != 0 {
		t.Fatal("failed generation retained its reservation")
	}

	entered, done := make(chan struct{}), make(chan struct{})
	handler := budget.wrap(func(_ http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
	}, 1<<20)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)

		handler(w, r)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	clientDone := make(chan error, 1)

	go func() {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
		if err == nil {
			var response *http.Response

			response, err = server.Client().Do(req)
			if response != nil {
				_ = response.Body.Close()
			}
		}

		clientDone <- err
	}()

	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not reach the handler")
	}

	cancel()

	if err := <-clientDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("request cancellation: %v", err)
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("canceled HTTP request retained its reservation")
	}

	if budget.usage().Active != 0 || budget.usage().Reserved != 0 {
		t.Fatal("canceled request did not restore capacity")
	}
}

func TestHTTPBudgetConcurrentAdmission(t *testing.T) {
	for _, limit := range []string{"requests", "memory"} {
		budget := newHTTPRequestBudget(4, 64<<20)
		if limit == "memory" {
			budget = newHTTPRequestBudget(64, 4<<20)
		}

		var wg sync.WaitGroup

		releases := make(chan func(), 64)
		codes := make(chan int, 64)

		for range 64 {
			wg.Add(1)

			go func() {
				defer wg.Done()

				w := httptest.NewRecorder()

				release, ok := budget.admit(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil), 1<<20)
				if ok {
					releases <- release
				} else {
					codes <- w.Code
				}
			}()
		}

		wg.Wait()
		close(releases)
		close(codes)

		if len(releases) != 4 || len(codes) != 60 || budget.usage().Reserved != 4<<20 {
			t.Fatalf("concurrent %s admission exceeded capacity: %+v", limit, budget.usage())
		}

		for code := range codes {
			if code != http.StatusServiceUnavailable {
				t.Fatalf("wrong overload status: %d", code)
			}
		}

		for release := range releases {
			release()
			release() // Cleanup remains idempotent.
		}

		if budget.usage().Active != 0 || budget.usage().Reserved != 0 {
			t.Fatal("concurrent cleanup did not restore capacity")
		}
	}
}
