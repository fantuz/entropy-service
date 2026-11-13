package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/drbg"
)

const enduranceBudget = 3 * time.Second

type enduranceResources struct {
	ElapsedMS  int64   `json:"elapsed_ms"`
	Goroutines int     `json:"goroutines"`
	HeapBytes  uint64  `json:"heap_bytes"`
	RSSBytes   *uint64 `json:"rss_bytes"`
}

type enduranceResult struct {
	Scenario       string               `json:"scenario"`
	Failed         bool                 `json:"failed"`
	ElapsedMS      int64                `json:"elapsed_ms"`
	Cycles         int                  `json:"cycles"`
	HTTPOK         atomic.Uint64        `json:"-"`
	HTTPBytes      atomic.Uint64        `json:"-"`
	WSFrames       atomic.Uint64        `json:"-"`
	WSConnections  atomic.Uint64        `json:"-"`
	Unavailable    atomic.Uint64        `json:"-"`
	PolicyCloses   atomic.Uint64        `json:"-"`
	Truncated      atomic.Uint64        `json:"-"`
	WorstHTTPNS    atomic.Int64         `json:"-"`
	FaultEpoch     atomic.Uint64        `json:"-"`
	CancelMaxMS    float64              `json:"cancel_max_ms"`
	ShutdownMaxMS  float64              `json:"shutdown_max_ms"`
	HTTPFinal      httpBudgetUsage      `json:"http_final"`
	WSFinal        webSocketUsage       `json:"ws_final"`
	ActiveChildren int64                `json:"active_children_final"`
	Samples        []enduranceResources `json:"samples"`
	errorsMu       sync.Mutex
	errors         []string
}

func enduranceOptions(t *testing.T) (time.Duration, int) {
	t.Helper()

	value := os.Getenv("ENTROPY_ENDURANCE_DURATION")
	if value == "" {
		t.Skip("opt-in endurance suite: use scripts/run_endurance.py")
	}

	duration, err := time.ParseDuration(value)
	if err != nil || duration < time.Second || duration > 24*time.Hour {
		t.Fatal("ENTROPY_ENDURANCE_DURATION must be 1s..24h")
	}

	workers := 4
	if value := os.Getenv("ENTROPY_ENDURANCE_WORKERS"); value != "" {
		workers, err = strconv.Atoi(value)
		if err != nil || workers < 1 || workers > 16 {
			t.Fatal("ENTROPY_ENDURANCE_WORKERS must be 1..16")
		}
	}

	return duration, workers
}

func enduranceSample(start time.Time) enduranceResources {
	var memory runtime.MemStats

	runtime.ReadMemStats(&memory)
	sample := enduranceResources{ElapsedMS: time.Since(start).Milliseconds(), Goroutines: runtime.NumGoroutine(), HeapBytes: memory.HeapAlloc}

	if runtime.GOOS == "linux" {
		data, err := os.ReadFile("/proc/self/statm")

		fields := strings.Fields(string(data))
		if err == nil && len(fields) > 1 {
			if pages, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
				rss := pages * uint64(os.Getpagesize())
				sample.RSSBytes = &rss
			}
		}
	}

	return sample
}

func (result *enduranceResult) unexpected(err error) {
	result.errorsMu.Lock()
	defer result.errorsMu.Unlock()

	if len(result.errors) < 8 {
		result.errors = append(result.errors, err.Error())
	}
}

func (result *enduranceResult) finish(t *testing.T, started time.Time) {
	t.Helper()
	result.errorsMu.Lock()
	defer result.errorsMu.Unlock()

	result.ElapsedMS = time.Since(started).Milliseconds()

	result.Failed = t.Failed() || len(result.errors) > 0
	if len(result.errors) > 0 {
		t.Errorf("unexpected workload errors: %v", result.errors)
	}

	report := struct {
		*enduranceResult

		HTTPOK        uint64   `json:"http_ok"`
		HTTPBytes     uint64   `json:"http_bytes"`
		WSFrames      uint64   `json:"ws_frames"`
		WSConnections uint64   `json:"ws_connections"`
		Unavailable   uint64   `json:"expected_503"`
		PolicyCloses  uint64   `json:"expected_1013"`
		Truncated     uint64   `json:"expected_truncated"`
		WorstHTTPMS   float64  `json:"worst_http_ms"`
		Errors        []string `json:"unexpected_errors"`
	}{
		result, result.HTTPOK.Load(), result.HTTPBytes.Load(), result.WSFrames.Load(), result.WSConnections.Load(),
		result.Unavailable.Load(), result.PolicyCloses.Load(), result.Truncated.Load(), float64(result.WorstHTTPNS.Load()) / float64(time.Millisecond), result.errors,
	}

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("ENDURANCE_RESULT %s", data)
}

func enduranceWait(t *testing.T, ready func() bool) {
	t.Helper()

	deadline := time.Now().Add(enduranceBudget)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatal("endurance cleanup/state transition exceeded its budget")
		}

		time.Sleep(time.Millisecond)
	}
}

func enduranceJoin(t *testing.T, done <-chan struct{}) {
	t.Helper()

	select {
	case <-done:
	case <-time.After(enduranceBudget):
		t.Fatal("endurance worker did not join")
	}
}

func enduranceHTTP(ctx context.Context, client *http.Client, endpoint string, result *enduranceResult) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+routeRandom+"?bytes=65536", nil)
	if err != nil {
		return err
	}

	started := time.Now()
	epoch := result.FaultEpoch.Load()

	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	count, err := io.Copy(io.Discard, response.Body)
	if response.StatusCode == http.StatusServiceUnavailable && response.Header.Get("Retry-After") == "1" && err == nil {
		result.Unavailable.Add(1)

		return nil
	}

	if errors.Is(err, io.ErrUnexpectedEOF) && response.StatusCode == http.StatusOK && (epoch%2 == 1 || result.FaultEpoch.Load() != epoch) {
		result.Truncated.Add(1)

		return nil
	}

	if err != nil {
		return err
	}

	if response.StatusCode != http.StatusOK || count != 65536 {
		return fmt.Errorf("HTTP sample status=%d bytes=%d", response.StatusCode, count)
	}

	result.HTTPOK.Add(1)
	result.HTTPBytes.Add(uint64(count))

	for elapsed := time.Since(started).Nanoseconds(); ; {
		previous := result.WorstHTTPNS.Load()
		if elapsed <= previous || result.WorstHTTPNS.CompareAndSwap(previous, elapsed) {
			break
		}
	}

	return nil
}

func enduranceDial(ctx context.Context, client *http.Client, endpoint string) (*websocket.Conn, *http.Response, error) {
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		return nil, nil, errors.New("fixture transport is not HTTP")
	}

	dialer := websocket.Dialer{HandshakeTimeout: enduranceBudget, TLSClientConfig: transport.TLSClientConfig}

	return dialer.DialContext(ctx, "ws"+strings.TrimPrefix(endpoint, "http")+"/stream?bytes=65536&refresh=5", nil)
}

func enduranceWS(ctx context.Context, client *http.Client, endpoint string, result *enduranceResult) error {
	conn, response, err := enduranceDial(ctx, client, endpoint)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err != nil {
		if response != nil && response.StatusCode == http.StatusServiceUnavailable && response.Header.Get("Retry-After") == "1" {
			result.Unavailable.Add(1)

			return nil
		}

		return err
	}

	defer conn.Close()

	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	result.WSConnections.Add(1)
	conn.SetReadLimit(65536)
	_ = conn.SetReadDeadline(time.Now().Add(enduranceBudget))

	for range 3 {
		kind, data, err := conn.ReadMessage()
		if websocket.IsCloseError(err, websocket.CloseTryAgainLater) {
			result.PolicyCloses.Add(1)

			return nil
		}

		if err != nil {
			return err
		}

		if kind != websocket.BinaryMessage || len(data) != 65536 {
			return fmt.Errorf("WS sample kind=%d bytes=%d", kind, len(data))
		}

		result.WSFrames.Add(1)
	}

	return nil
}

func TestEnduranceMixedTraffic(t *testing.T) {
	duration, workers := enduranceOptions(t)
	for _, algorithm := range []drbg.Algorithm{drbg.ChaCha20, drbg.AES256CTR} {
		t.Run(algorithm.String(), func(t *testing.T) {
			root := newBenchmarkDRBG(t, algorithm) // Public fixture seed; never a production source.
			if err := root.ConfigureReseedPolicy(drbg.ReseedStop, time.Hour); err != nil {
				t.Fatal(err)
			}

			baselineInstances := drbg.ActiveInstances()
			ws, requests := newWebSocketBudget(workers*2+4, 64<<20), newHTTPRequestBudget(workers*2+4, 64<<20)
			mux := testGenerationMux(root, ws, requests)
			mux.HandleFunc("/ready", healthHandler(root, time.Hour))
			mux.HandleFunc("/live", livenessHandler)
			plain, secure := httptest.NewUnstartedServer(mux), httptest.NewUnstartedServer(mux)
			servers := []*managedServer{newManagedServer(t.Context(), plain.Config), newManagedServer(t.Context(), secure.Config)}

			plain.Start()
			secure.StartTLS()
			t.Cleanup(plain.Close)
			t.Cleanup(secure.Close)

			ctx, cancel := context.WithCancel(t.Context())
			joined := make(chan struct{})
			started := time.Now()

			result := &enduranceResult{Scenario: algorithm.String()}
			defer result.finish(t, started)

			t.Cleanup(func() {
				cancel()
				enduranceJoin(t, joined)

				for _, server := range servers {
					shutdown, stop := context.WithTimeout(context.Background(), enduranceBudget)
					_ = server.Shutdown(shutdown)

					stop()
				}
			})

			runtime.GC()

			result.Samples = append(result.Samples, enduranceSample(started))

			clients, endpoints := []*http.Client{plain.Client(), secure.Client()}, []string{plain.URL, secure.URL}
			for _, client := range clients {
				client.Timeout = enduranceBudget
			}

			var group sync.WaitGroup

			for range workers {
				group.Go(func() {
					ticker := time.NewTicker(10 * time.Millisecond)
					defer ticker.Stop()

					for iteration := 0; ctx.Err() == nil; iteration++ {
						index := iteration % len(clients)
						if err := enduranceHTTP(ctx, clients[index], endpoints[index], result); err != nil && ctx.Err() == nil {
							result.unexpected(err)
						}

						if err := enduranceWS(ctx, clients[index], endpoints[index], result); err != nil && ctx.Err() == nil {
							result.unexpected(err)
						}

						select {
						case <-ctx.Done():
							return
						case <-ticker.C:
						}
					}
				})
			}

			go func() { group.Wait(); close(joined) }()

			nextSample := time.Now().Add(max(time.Second, duration/250))
			for time.Since(started) < duration || result.Cycles < 2 {
				time.Sleep(100 * time.Millisecond)
				enduranceFaultCycle(t, root, clients, endpoints, result)

				if err := root.Reseed(bytes.Repeat([]byte{byte(result.Cycles)}, 64)); err != nil {
					t.Fatal(err)
				}

				result.FaultEpoch.Add(1)
				enduranceCheckStatus(t, clients[1], secure.URL+"/ready", http.StatusOK)

				result.Cycles++
				if time.Now().After(nextSample) && len(result.Samples) < 255 {
					result.Samples = append(result.Samples, enduranceSample(started))
					nextSample = time.Now().Add(max(time.Second, duration/250))
				}
			}

			cancel()
			enduranceJoin(t, joined)

			for _, client := range clients {
				client.CloseIdleConnections()
			}

			enduranceWait(t, func() bool { return ws.usage().Active == 0 && requests.usage().Active == 0 })

			slow := enduranceSaturateWS(t, clients[0], endpoints[0], ws, result)
			defer func() {
				for _, conn := range slow {
					_ = conn.Close()
				}
			}()

			for _, server := range servers {
				shutdown, stop := context.WithTimeout(context.Background(), enduranceBudget)
				begin := time.Now()
				err := server.Shutdown(shutdown)
				result.ShutdownMaxMS = max(result.ShutdownMaxMS, float64(time.Since(begin))/float64(time.Millisecond))

				stop()

				if err != nil {
					t.Fatal(err)
				}
			}

			enduranceBlockedWrite(t, root, result)
			enduranceWait(t, func() bool {
				return drbg.ActiveInstances() == baselineInstances && ws.usage().Active == 0 && requests.usage().Active == 0
			})

			result.ActiveChildren = drbg.ActiveInstances() - baselineInstances

			result.HTTPFinal, result.WSFinal = requests.usage(), ws.usage()
			if result.HTTPFinal.Reserved != 0 || result.WSFinal.Reserved != 0 || result.HTTPOK.Load() == 0 || result.WSFrames.Load() == 0 || result.Unavailable.Load() == 0 {
				t.Fatal("workload did not exercise both transports/faults or leaked reservations")
			}

			runtime.GC()

			result.Samples = append(result.Samples, enduranceSample(started))

			first, last := result.Samples[0], result.Samples[len(result.Samples)-1]
			if last.Goroutines > first.Goroutines+16 || last.HeapBytes > first.HeapBytes+64<<20 {
				t.Fatal("retained heap/goroutines exceeded the documented drift allowance")
			}
		})
	}
}

func enduranceCheckStatus(t *testing.T, client *http.Client, endpoint string, status int) {
	t.Helper()

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}

	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	_, err = io.Copy(io.Discard, response.Body)
	if err != nil || response.StatusCode != status {
		t.Fatalf("state probe status=%d want=%d err=%v", response.StatusCode, status, err)
	}
}

func enduranceFaultCycle(t *testing.T, root *drbg.DRBG, clients []*http.Client, endpoints []string, result *enduranceResult) {
	t.Helper()

	index := result.Cycles % len(clients)

	conn, response, err := enduranceDial(t.Context(), clients[index], endpoints[index])
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err != nil {
		t.Fatal(err)
	}

	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(enduranceBudget))
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatal(err)
	}

	result.FaultEpoch.Add(1)
	root.RecordReseedFailure(io.ErrUnexpectedEOF)
	enduranceCheckStatus(t, clients[0], endpoints[0]+"/ready", http.StatusServiceUnavailable)
	enduranceCheckStatus(t, clients[1], endpoints[1]+"/live", http.StatusOK)
	enduranceCheckStatus(t, clients[1], endpoints[1]+routeRandom+"?bytes=64", http.StatusServiceUnavailable)
	enduranceRejectWS(t, clients[1], endpoints[1], result)

	for {
		_, _, err := conn.ReadMessage()
		if websocket.IsCloseError(err, websocket.CloseTryAgainLater) {
			result.PolicyCloses.Add(1)

			return
		}

		if err != nil {
			t.Fatalf("active stream did not close with 1013 after failure: %v", err)
		}
	}
}

func enduranceRejectWS(t *testing.T, client *http.Client, endpoint string, result *enduranceResult) {
	t.Helper()

	conn, response, err := enduranceDial(t.Context(), client, endpoint)
	if conn != nil {
		_ = conn.Close()
	}

	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err == nil || response == nil || response.StatusCode != http.StatusServiceUnavailable || response.Header.Get("Retry-After") != "1" {
		t.Fatalf("expected explicit WS refusal: response=%v err=%v", response, err)
	}

	result.Unavailable.Add(1)
}

func enduranceSaturateWS(t *testing.T, client *http.Client, endpoint string, budget *webSocketBudget, result *enduranceResult) []*websocket.Conn {
	t.Helper()

	connections := make([]*websocket.Conn, 0, budget.usage().MaxConnections)

	t.Cleanup(func() {
		for _, conn := range connections {
			_ = conn.Close()
		}
	})

	for range cap(connections) {
		conn, response, err := enduranceDial(t.Context(), client, endpoint)
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}

		if err != nil {
			t.Fatal(err)
		}

		connections = append(connections, conn)

		_ = conn.SetReadDeadline(time.Now().Add(enduranceBudget))
		if _, _, err := conn.NextReader(); err != nil {
			t.Fatal(err)
		}
	}

	enduranceRejectWS(t, client, endpoint, result)

	if budget.usage().RejectedConnections == 0 {
		t.Fatal("held consumers did not exercise the connection budget")
	}

	return connections
}

func enduranceBlockedWrite(t *testing.T, root *drbg.DRBG, result *enduranceResult) {
	t.Helper()

	serverPipe, clientPipe := net.Pipe()
	defer serverPipe.Close()
	defer clientPipe.Close()

	budget := newWebSocketBudget(1, 2<<20)
	fixture := httptest.NewUnstartedServer(wsBinaryHandler(root, 1, 1<<20, 1<<20, budget))
	_ = fixture.Listener.Close()
	fixture.Listener = &lifecyclePipeListener{connection: serverPipe, closed: make(chan struct{})}
	server := newManagedServer(t.Context(), fixture.Config)

	fixture.Start()
	defer fixture.Close()

	dialer := websocket.Dialer{HandshakeTimeout: enduranceBudget, NetDialContext: func(context.Context, string, string) (net.Conn, error) { return clientPipe, nil }}

	conn, response, err := dialer.DialContext(t.Context(), "ws"+strings.TrimPrefix(fixture.URL, "http"), nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err != nil {
		t.Fatal(err)
	}

	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(enduranceBudget))
	if _, _, err := conn.NextReader(); err != nil {
		t.Fatal(err)
	}

	// No body is read and net.Pipe has no transport buffer: the production
	// handler's 1 MiB frame is still in flight when managed shutdown starts.
	if budget.usage().Active != 1 {
		t.Fatal("blocked production writer released its reservation prematurely")
	}

	shutdown, stop := context.WithTimeout(context.Background(), enduranceBudget)
	defer stop()

	begin := time.Now()

	if err := server.Shutdown(shutdown); err != nil {
		t.Fatal(err)
	}

	result.ShutdownMaxMS = max(result.ShutdownMaxMS, float64(time.Since(begin))/float64(time.Millisecond))

	if usage := budget.usage(); usage.Active != 0 || usage.Reserved != 0 {
		t.Fatalf("blocked writer survived shutdown: %+v", usage)
	}
}

func TestEnduranceSourceCycles(t *testing.T) {
	duration, _ := enduranceOptions(t)

	root := observableTestDRBG(t)
	if err := root.ConfigureReseedPolicy(drbg.ReseedStop, time.Hour); err != nil {
		t.Fatal(err)
	}

	reader, writer := deadlineSeedPipe(t, t.Context())
	input := &seedInput{source: reader, reader: reader, timeout: 100 * time.Millisecond}
	started := time.Now()

	result := &enduranceResult{Scenario: "source_cycles"}
	defer result.finish(t, started)

	result.Samples = append(result.Samples, enduranceSample(started))
	nextSample := time.Now().Add(max(time.Second, duration/250))

	for time.Since(started) < duration || result.Cycles < 2 {
		if _, err := writer.Write(bytes.Repeat([]byte{1}, 16)); err != nil {
			t.Fatal(err)
		}

		buffer := bytes.Repeat([]byte{0xff}, 32)

		n, err := input.Reader(t.Context()).Read(buffer)
		if n != 0 || (!errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, os.ErrDeadlineExceeded)) || !bytes.Equal(buffer, make([]byte, 32)) {
			t.Fatalf("partial seed accepted/retained: n=%d err=%v", n, err)
		}

		root.RecordReseedFailure(err)

		if !errors.Is(root.CheckGeneration(), drbg.ErrSeedUnavailable) {
			t.Fatal("failed seed did not close generation")
		}

		if _, err := writer.Write(bytes.Repeat([]byte{7}, 32)); err != nil {
			t.Fatal(err)
		}

		if n, err := input.Reader(t.Context()).Read(buffer); err != nil || n != 32 {
			t.Fatalf("same source did not recover: n=%d err=%v", n, err)
		}

		if err := root.Reseed(buffer); err != nil {
			t.Fatal(err)
		}

		clear(buffer)

		if err := root.CheckGeneration(); err != nil {
			t.Fatal(err)
		}

		failures := root.GetMetadata().ReseedFailuresTotal
		result.CancelMaxMS = max(result.CancelMaxMS, enduranceCancelReseed(t, reader, root))

		if root.GetMetadata().ReseedFailuresTotal != failures {
			t.Fatal("joined shutdown was counted as a source fault")
		}

		result.Cycles++
		if time.Now().After(nextSample) && len(result.Samples) < 255 {
			result.Samples = append(result.Samples, enduranceSample(started))
			nextSample = time.Now().Add(max(time.Second, duration/250))
		}
	}

	result.Samples = append(result.Samples, enduranceSample(started))
}

func enduranceCancelReseed(t *testing.T, reader seedDeadlineSource, root *drbg.DRBG) float64 {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	signal := &seedReadSignal{reader: reader, started: make(chan struct{})}
	input := &seedInput{source: reader, reader: signal, timeout: time.Minute}
	joined := make(chan struct{})

	go func() { reseedLoop(ctx, input.Reader(ctx), 1, 32, root); close(joined) }()

	defer func() { cancel(); enduranceJoin(t, joined) }()

	enduranceJoin(t, signal.started)

	begin := time.Now()

	cancel()
	enduranceJoin(t, joined)

	return float64(time.Since(begin)) / float64(time.Millisecond)
}
