package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fantuz/entropy-service/internal/capabilities"
)

func TestCapabilityEndpointDoesNotCopyQueryCredentialsOrChangeOrigin(t *testing.T) {
	for _, tc := range []struct {
		input, want  string
		required, ok bool
	}{
		{"wss://operator:secret@[::1]:8443/prefix/stream?token=private#hidden", "https://operator:secret@[::1]:8443/prefix/v1/capabilities", false, true},
		{"https://example.test/prefix/v1/data/random?bytes=64", "https://example.test/prefix/v1/capabilities", false, true},
		{"https://example.test/custom", "", false, false},
		{"https://example.test/custom", "https://example.test/v1/capabilities", true, true},
		{"file:///stream", "", true, false},
	} {
		got, ok := CapabilityEndpoint(tc.input, tc.required)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("endpoint=%q, ok=%t; want %q, %t", got, ok, tc.want, tc.ok)
		}
	}
}

func TestCapabilitiesBoundedValidatedAndNeverFollowRedirects(t *testing.T) {
	valid := capabilities.Document{SchemaVersion: 1, Service: "entropy-service", Version: "test", MaxBytes: 64, Transports: []string{"http", "websocket"}, WebSocketMinRefreshMS: 1, WebSocketMaxRefreshMS: 60000}

	data, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}

	for _, body := range []string{string(data), `{"schema_version":999}`, strings.Repeat(" ", 16385) + string(data), string(data) + `{}`, `{"schema_version":1,"service":"entropy-service","max_bytes":33554433}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
		_, err := FetchCapabilities(t.Context(), server.URL)
		server.Close()

		if (err == nil) != (body == string(data)) {
			t.Fatalf("unexpected validation result: %v", err)
		}
	}

	var followed atomic.Bool

	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { followed.Store(true) }))
	defer target.Close()

	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer redirect.Close()

	if _, err := FetchCapabilities(t.Context(), redirect.URL); err == nil || followed.Load() {
		t.Fatal("discovery followed an origin redirect")
	}

	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(data) }))
	defer tls.Close()

	if _, err := FetchCapabilities(t.Context(), tls.URL); err == nil {
		t.Fatal("discovery accepted an untrusted certificate")
	}
}

func TestCapabilitiesCancellationInterruptsAStalledBody(t *testing.T) {
	headers := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("response cannot flush")
			return
		}

		flusher.Flush()
		close(headers)
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() { _, err := FetchCapabilities(ctx, server.URL); done <- err }()

	<-headers
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled discovery succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation blocked on capability body")
	}
}

func TestCapabilityNetworkErrorsDoNotEchoURLSecrets(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := FetchCapabilities(ctx, "https://operator:secret@127.0.0.1:1/v1/capabilities?token=hidden")
	if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "hidden") {
		t.Fatal("capability error exposed URL credentials", err)
	}
}
