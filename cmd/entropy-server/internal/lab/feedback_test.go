package lab

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestTCPAvailableBytesBeforeEOF(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	release := make(chan struct{})
	defer close(release)

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		_, _ = conn.Write([]byte{42})

		<-release
	}()

	registry := testRegistry(t, Config{ID: "tcp", Kind: sourceTCP, Endpoint: listener.Addr().String(), TimeoutMs: 200})

	stream, err := registry.Open(t.Context(), "tcp", 65536, 20)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	data, err := stream.Read(t.Context(), 65536)
	if err != nil || len(data) != 1 || data[0] != 42 {
		t.Fatalf("available bytes waited for a full sample/EOF: %v %v", data, err)
	}
}

func TestWebPageCreatedAndRedirectStatuses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/created":
			w.Header().Set("Location", "/must-not-fetch")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, "created")
		case r.URL.Path == "/must-not-fetch":
			t.Error("201 Location was treated as a redirect")
		case strings.HasPrefix(r.URL.Path, "/redirect/"):
			code, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/redirect/"))
			http.Redirect(w, r, "/ok", code)
		case r.URL.Path == "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		default:
			_, _ = io.WriteString(w, "ok")
		}
	}))
	defer server.Close()

	for _, path := range []string{"/created", "/redirect/301", "/redirect/302", "/redirect/303", "/redirect/307", "/redirect/308", "/loop"} {
		registry := testRegistry(t, Config{ID: "web", Kind: sourceWebpage, Endpoint: server.URL + path})

		stream, err := registry.Open(t.Context(), "web", 64, 20)
		if err != nil {
			t.Fatal(err)
		}

		data, err := stream.Read(t.Context(), 64)
		_ = stream.Close()

		info := stream.HTTPInfo()

		switch path {
		case "/loop":
			var status *HTTPStatusError
			if !errors.As(err, &status) || !status.RedirectBlocked || info.Redirects != 2 {
				t.Fatalf("redirect loop was not bounded: %v %+v", err, info)
			}
		case "/created":
			if err != nil || string(data) != "created" || info.Status != 201 || !info.Location || info.Redirects != 0 {
				t.Fatalf("201 should return its own body: %q %v %+v", data, err, info)
			}
		default:
			if err != nil || string(data) != "ok" || info.Status != 200 || info.Redirects != 1 {
				t.Fatalf("redirect failed: %s %q %v %+v", path, data, err, info)
			}
		}
	}
}

func TestWebPageRedirectTransportPolicy(t *testing.T) {
	for _, test := range []struct {
		from, to string
		allowed  bool
	}{
		{"http://example.test/a", "https://example.test/b", true},
		{"http://example.test:80/a", "https://EXAMPLE.test:443/b", true},
		{"https://example.test/a", "https://example.test/b", true},
		{"https://example.test/a", "http://example.test/b", false},
		{"http://example.test/a", "https://other.test/b", false},
		{"http://example.test/a", "https://example.test:8443/b", false},
		{"http://example.test:8080/a", "https://example.test/b", false},
	} {
		from, _ := url.Parse(test.from)

		to, _ := url.Parse(test.to)
		if webRedirectAllowed(to, from) != test.allowed {
			t.Fatalf("unexpected redirect policy: %+v", test)
		}
	}
}

type feedbackTransport func(*http.Request) (*http.Response, error)

func (f feedbackTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWebPageRedirectCannotDowngradeAfterUpgrade(t *testing.T) {
	registry := testRegistry(t, Config{ID: "web", Kind: sourceWebpage, Endpoint: "http://example.test/start"})
	registry.client.Transport = feedbackTransport(func(r *http.Request) (*http.Response, error) {
		location := "https://example.test/secure"
		if r.URL.Scheme == sourceHTTPS {
			location = "http://example.test/must-not-fetch"
		}

		if r.URL.Path == "/must-not-fetch" {
			t.Error("redirect chain downgraded after HTTPS upgrade")
		}

		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{location}}, Body: io.NopCloser(strings.NewReader("redirect")), Request: r}, nil
	})

	stream, err := registry.Open(t.Context(), "web", 64, 20)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	_, err = stream.Read(t.Context(), 64)

	var status *HTTPStatusError
	if !errors.As(err, &status) || !status.RedirectBlocked || stream.HTTPInfo().Redirects != 1 {
		t.Fatalf("upgrade/downgrade policy failed: %v %+v", err, stream.HTTPInfo())
	}
}
