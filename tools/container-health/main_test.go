package main

import (
	"bytes"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func probe(args ...string) int {
	return run(args, func(string) string { return "" }, new(bytes.Buffer))
}

func TestReadinessStatus(t *testing.T) {
	for _, status := range []int{200, 204, 401, 404, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
			defer server.Close()

			got := probe("-url", server.URL+"/ready")
			if (got == 0) != (status == http.StatusOK) {
				t.Fatalf("HTTP %d: exit %d", status, got)
			}
		})
	}
}

func TestDoesNotFollowRedirects(t *testing.T) {
	var requests atomic.Int32

	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer target.Close()

	server := httptest.NewServer(http.RedirectHandler(target.URL, http.StatusFound))
	defer server.Close()

	if probe("-url", server.URL) == 0 || requests.Load() != 0 {
		t.Fatal("redirect must fail without contacting its target")
	}
}

func TestRequestTimeoutCancelsHandler(t *testing.T) {
	canceled := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		close(canceled)
	}))
	defer server.Close()

	if probe("-url", server.URL, "-timeout", "20ms") != 1 {
		t.Fatal("stalled readiness must fail")
	}

	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not observe request cancellation")
	}
}

func TestTLSRequiresTrust(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()

	if probe("-url", server.URL) != 1 {
		t.Fatal("untrusted certificate must fail")
	}

	cert := filepath.Join(t.TempDir(), "public.pem")
	if err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}

	if probe("-url", server.URL, "-ca-file", cert) != 0 {
		t.Fatal("explicit public certificate should establish trust")
	}

	if probe("-url", server.URL, "-ca-file", cert+".missing") != 2 {
		t.Fatal("missing trust file must fail")
	}
}

func TestConfiguration(t *testing.T) {
	for _, args := range [][]string{{"-url", "file:///ready"}, {"-url", "http://user:password@localhost/ready"}, {"-url", "http://localhost/ready#fragment"}, {"-timeout", "0s"}, {"unexpected"}} {
		if probe(args...) != 2 {
			t.Fatalf("invalid arguments accepted: %v", args)
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()

	getenv := func(key string) string {
		if key == "ENTROPY_HEALTH_URL" {
			return server.URL + "/ready"
		}

		return ""
	}
	if run(nil, getenv, new(bytes.Buffer)) != 0 {
		t.Fatal("health URL environment setting was ignored")
	}

	if probe("-help") != 0 {
		t.Fatal("help should succeed")
	}
}
