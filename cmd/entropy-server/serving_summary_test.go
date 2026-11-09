package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/metrics"
)

func TestServingSummaryUnitsAndDrainState(t *testing.T) {
	for _, drained := range []bool{false, true} {
		var output bytes.Buffer

		logServingSummary(log.New(&output, "", 0), 1234*time.Millisecond, drained, metrics.ServingTotals{
			HTTPBodyBytes: 2_000_000, WSPayloadBytes: 3_000_000,
			TCPConnections: 4, HTTPRequests: 7, WSSessions: 2, WSMessages: 8, GeneratedBytes: 9_000_000,
		})

		for _, want := range []string{
			"uptime=1.234s", "served_MB=5.000 HTTP_body_MB=2.000 WS_payload_MB=3.000",
			"TCP_accepted=4 HTTP_requests=7 WS_sessions=2 WS_messages=8", "DRBG_generated_MB=9.000", "include upgrades",
		} {
			if !strings.Contains(output.String(), want) {
				t.Fatalf("missing %q in summary: %s", want, &output)
			}
		}

		if strings.Count(output.String(), "\n") != 3 || strings.Contains(output.String(), "incomplete_snapshot") == drained {
			t.Fatalf("incorrect drain state or summary length: %s", &output)
		}
	}
}

type shortSummaryWriter struct{ header http.Header }

func (w *shortSummaryWriter) Header() http.Header { return w.header }
func (*shortSummaryWriter) WriteHeader(int)       {}
func (*shortSummaryWriter) Write(p []byte) (int, error) {
	return min(3, len(p)), io.ErrClosedPipe
}

func TestHTTPServedByteAccounting(t *testing.T) {
	for _, mode := range []string{"write", "readerfrom", "fallback", "short", "head"} {
		t.Run(mode, func(t *testing.T) {
			before := metrics.ServingSnapshot()

			var underlying http.ResponseWriter = httptest.NewRecorder()

			switch mode {
			case "readerfrom":
				underlying = &loggingReaderFrom{ResponseRecorder: httptest.NewRecorder()}
			case "short":
				underlying = &shortSummaryWriter{header: make(http.Header)}
			}

			handler := observeRequests(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if mode == "readerfrom" || mode == "fallback" {
					// Hide WriterTo to exercise the ResponseWriter's ReadFrom path.
					_, _ = io.Copy(w, struct{ io.Reader }{strings.NewReader("1234567890")})
				} else {
					_, _ = w.Write([]byte("1234567890"))
				}
			}), log.New(io.Discard, "", 0))
			method := http.MethodGet
			want := uint64(10)

			switch mode {
			case "head":
				method = http.MethodHead
				want = 0
			case "short":
				want = 3
			}

			handler.ServeHTTP(underlying, httptest.NewRequestWithContext(t.Context(), method, "/", nil))

			after := metrics.ServingSnapshot()
			if after.HTTPBodyBytes-before.HTTPBodyBytes != want || after.HTTPRequests-before.HTTPRequests != 1 {
				t.Fatalf("%s counted wrong: before=%+v after=%+v want bytes=%d", mode, before, after, want)
			}
		})
	}
}

func TestServingCountersAcrossWebSocketFormats(t *testing.T) {
	for _, secure := range []bool{false, true} {
		for _, path := range []string{"/stream", "/bytes", "/colors", "/bytes?format=compact", "/colors?format=hash", "/words"} {
			t.Run(path+"/TLS="+boolLabel(secure), func(t *testing.T) {
				before := metrics.ServingSnapshot()
				budget := newWebSocketBudget(2, 1<<20)
				generator := observableTestDRBG(t)
				handler := wsBytesHandler(generator, 100, 64, budget)

				switch path {
				case "/stream":
					handler = wsBinaryHandler(generator, 100, 64, 64, budget)
				case "/words":
					handler = wsWordsHandler(generator, 3, 100, budget)
				}

				testServer := httptest.NewUnstartedServer(observeRequests(handler, log.New(io.Discard, "", 0)))
				testServer.Config.ConnContext = observeConnection
				server := newManagedServer(t.Context(), testServer.Config)

				if secure {
					testServer.StartTLS()
				} else {
					testServer.Start()
				}

				defer testServer.Close()

				conn := budgetConnect(t, testServer, path)
				defer conn.Close()

				_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))

				_, first, err := conn.ReadMessage()
				if err != nil {
					t.Fatal(err)
				}

				shutdownCtx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()

				if err := server.Shutdown(shutdownCtx); err != nil {
					t.Fatal(err)
				}

				payloadBytes, messages := uint64(len(first)), uint64(1)

				for {
					_, body, err := conn.ReadMessage()
					if err != nil {
						break
					}

					payloadBytes += uint64(len(body))
					messages++
				}

				after := metrics.ServingSnapshot()
				if after.TCPConnections-before.TCPConnections != 1 || after.HTTPRequests-before.HTTPRequests != 1 ||
					after.WSSessions-before.WSSessions != 1 || after.WSMessages-before.WSMessages != messages ||
					after.WSPayloadBytes-before.WSPayloadBytes != payloadBytes || after.HTTPBodyBytes != before.HTTPBodyBytes {
					t.Fatalf("incorrect totals: before=%+v after=%+v received WS bytes=%d messages=%d", before, after, payloadBytes, messages)
				}
			})
		}
	}
}

func TestFailedWSEncodingDoesNotCountCompletedPayload(t *testing.T) {
	before := metrics.ServingSnapshot()
	done := make(chan error, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			done <- err

			return
		}
		defer conn.Close()

		done <- writeObservedJSON(conn, make(chan int)) // JSON cannot encode channels.
	}))
	defer server.Close()

	conn := budgetConnect(t, server, "/")
	defer conn.Close()

	if err := <-done; err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("expected an encoding error, got %v", err)
	}

	if after := metrics.ServingSnapshot(); after.WSPayloadBytes != before.WSPayloadBytes {
		t.Fatal("failed encoding counted as a completed payload")
	}
}
