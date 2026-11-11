package lab

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // Verify the ONVIF-mandated UsernameToken digest.
	"encoding/base64"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestCameraAndPageConfiguration(t *testing.T) {
	for _, source := range []Config{
		{ID: "camera", Kind: sourceRTSP, Endpoint: "rtsp://camera/live"},
		{ID: "onvif", Kind: sourceONVIF, Endpoint: "http://camera/onvif/device_service"},
		{ID: "page", Kind: sourceWebpage, Endpoint: "https://example.org", URLs: []string{"https://example.net"}},
	} {
		registry := testRegistry(t, source)
		if len(registry.List()) != 1 {
			t.Fatal("source registration failed")
		}
	}

	for _, source := range []Config{
		{ID: "camera", Kind: sourceRTSP, Endpoint: "rtsp://camera/live", Width: 100, Height: 100},
		{ID: "camera", Kind: sourceRTSP, Endpoint: "rtsp://camera/live", MaxBytes: 10},
		{ID: "camera", Kind: sourceRTSP, Endpoint: "rtsp://camera/live", ProfileToken: "wrong-kind"},
		{ID: "camera", Kind: sourceRTSP, Endpoint: "rtsp://test-only:fixture@camera/live"}, //nolint:gosec // Rejection fixture, never dialled.
		{ID: "page", Kind: sourceWebpage, Endpoint: "https://example.org", URLs: []string{"file:///etc/passwd"}},
		{ID: "page", Kind: sourceHTTP, Endpoint: "https://example.org", URLs: []string{"https://example.net"}},
	} {
		if registry, err := New([]Config{source}); err == nil {
			registry.Close()
			t.Fatalf("invalid source accepted: %s", source.Kind)
		}
	}
}

func TestWebPagePrefixPoolAndRedirectBoundary(t *testing.T) {
	foreign := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("followed cross-origin redirect") }))
	defer foreign.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, "/body", http.StatusFound)
		case "/foreign":
			http.Redirect(w, r, foreign.URL, http.StatusFound)
		default:
			_, _ = w.Write(bytes.Repeat([]byte("<p>test</p>"), 10000))
		}
	}))
	defer server.Close()

	registry := testRegistry(t, Config{ID: "page", Kind: sourceWebpage, Endpoint: server.URL + "/redirect", URLs: []string{server.URL + "/body"}}, Config{ID: "foreign", Kind: sourceWebpage, Endpoint: server.URL + "/foreign"})

	stream, err := registry.Open(t.Context(), "page", 100, 20)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	data, err := stream.Read(t.Context(), 100)
	if err != nil || len(data) != 100 || !bytes.HasPrefix(data, []byte("<p>test</p>")) {
		t.Fatalf("web page prefix failed: %v", err)
	}

	blocked, err := registry.Open(t.Context(), "foreign", 100, 20)
	if err != nil {
		t.Fatal(err)
	}
	defer blocked.Close()

	if _, err := blocked.Read(t.Context(), 100); err == nil {
		t.Fatal("cross-origin redirect accepted")
	}
}

func TestONVIFDiscoveryAndCredentialBoundary(t *testing.T) {
	const password = "fixture-only-password"

	var (
		foreign atomic.Bool
		calls   atomic.Int32
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		if bytes.Contains(data, []byte(password)) {
			t.Error("password was sent as plaintext SOAP")
		}

		var envelope struct {
			Header struct {
				Security struct {
					Token struct {
						Username string `xml:"Username"`
						Password string `xml:"Password"`
						Nonce    string `xml:"Nonce"`
						Created  string `xml:"Created"`
					} `xml:"UsernameToken"`
				} `xml:"Security"`
			} `xml:"Header"`
		}
		if err := xml.Unmarshal(data, &envelope); err != nil {
			t.Error(err)
		}

		token := envelope.Header.Security.Token
		nonce, _ := base64.StdEncoding.DecodeString(token.Nonce)

		digest := sha1.Sum(append(append(nonce, []byte(token.Created)...), []byte(password)...)) //nolint:gosec // ONVIF interoperability fixture.
		if token.Username != "lab<&" || token.Password != base64.StdEncoding.EncodeToString(digest[:]) {
			t.Error("invalid UsernameToken digest or XML escaping")
		}

		calls.Add(1)

		var body string

		switch {
		case bytes.Contains(data, []byte("GetCapabilities")):
			body = `<GetCapabilitiesResponse><Capabilities><Media><XAddr>http://` + r.Host + `/media</XAddr></Media></Capabilities></GetCapabilitiesResponse>`
		case bytes.Contains(data, []byte("GetProfiles")):
			body = `<GetProfilesResponse><Profiles token="profile&amp;one"/></GetProfilesResponse>`
		case bytes.Contains(data, []byte("GetStreamUri")):
			if !bytes.Contains(data, []byte("profile&amp;one")) {
				t.Error("profile token lost or unescaped")
			}

			host := "127.0.0.1"
			if foreign.Load() {
				host = "other.example"
			}

			body = `<GetStreamUriResponse><MediaUri><Uri>rtsp://` + host + `:8554/live</Uri></MediaUri></GetStreamUriResponse>`
		}

		w.Header().Set("Content-Type", "application/soap+xml")
		_, _ = io.WriteString(w, `<Envelope><Body>`+body+`</Body></Envelope>`) //nolint:gosec // SOAP-only httptest fixture, not HTML.
	}))
	defer server.Close()

	registry := testRegistry(t, Config{ID: "cam", Kind: sourceONVIF, Endpoint: server.URL + "/device"})

	uri, err := registry.onvifURI(t.Context(), registry.sources["cam"], Credentials{Username: "lab<&", Password: password})
	if err != nil || uri != "rtsp://127.0.0.1:8554/live" || calls.Load() != 3 {
		t.Fatalf("discovery failed: %s %v", uri, err)
	}

	foreign.Store(true)

	if _, err := registry.onvifURI(t.Context(), registry.sources["cam"], Credentials{Username: "lab<&", Password: password}); err == nil {
		t.Fatal("ONVIF returned an unregistered host")
	}
}

func TestCameraDecoderCredentialsPrefixAndCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell decoder fixture; ONVIF/URL/credential checks run on every platform")
	}

	dir := t.TempDir()
	input, args := filepath.Join(dir, "input"), filepath.Join(dir, "args")

	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + args + "'\ncat > '" + input + "'\nhead -c 57600 /dev/zero\n"
	if err := os.WriteFile(filepath.Join(dir, "ffmpeg"), []byte(script), 0o700); err != nil { //nolint:gosec // Executable decoder fixture, owner-only access.
		t.Fatal(err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	registry := testRegistry(t, Config{ID: "cam", Kind: sourceRTSP, Endpoint: "rtsp://camera/live", Width: 160, Height: 120})

	stream, err := registry.OpenAuthenticated(t.Context(), "cam", 32, 500, Credentials{Username: "fixture-user", Password: "fixture-pass"})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	data, err := stream.Read(t.Context(), 32)
	if err != nil || len(data) != 32 {
		t.Fatalf("decoded prefix failed: %v", err)
	}

	arguments, _ := os.ReadFile(args)

	stdin, _ := os.ReadFile(input)
	if bytes.Contains(arguments, []byte("fixture")) || !bytes.Contains(stdin, []byte("rtsp://fixture-user:fixture-pass@camera/live")) {
		t.Fatal("camera credentials must use stdin, never argv")
	}

	if _, err := stream.Read(t.Context(), 32); !errors.Is(err, io.EOF) {
		t.Fatalf("finite camera did not reach EOF: %v", err)
	}

	_ = stream.Close()

	if len(registry.cameras) != 0 {
		t.Fatal("decoder slot leaked")
	}

	if err := os.WriteFile(filepath.Join(dir, "ffmpeg"), []byte("#!/bin/sh\ncat >/dev/null\nexec sleep 30\n"), 0o700); err != nil { //nolint:gosec // Executable decoder fixture, owner-only access.
		t.Fatal(err)
	}

	blocked, err := registry.Open(t.Context(), "cam", 32, 500)
	if err != nil {
		t.Fatal(err)
	}
	defer blocked.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := blocked.Read(ctx, 32); err == nil || time.Since(start) > time.Second {
		t.Fatal("camera read did not cancel")
	}
}

func TestCameraURLRejectsCredentials(t *testing.T) {
	for _, raw := range []string{"file:///tmp/video", "rtsp://camera/\nmalformed", "rtsp://user:fixture@camera/live", "rtsp://camera/live#fragment", "rtsps://camera/live"} {
		if _, err := cameraURL(raw); err == nil {
			t.Fatal("unsafe camera URL accepted")
		}
	}
}

func TestONVIFDefaultPortOrigin(t *testing.T) {
	base, err := url.Parse("http://camera/onvif/device_service")
	if err != nil {
		t.Fatal(err)
	}

	if !sameCameraHost("http://CAMERA:80/media", base, false) {
		t.Fatal("equivalent default HTTP port was rejected")
	}

	for _, endpoint := range []string{"http://camera:81/media", "https://camera/media", "http://other-camera/media"} {
		if sameCameraHost(endpoint, base, false) {
			t.Fatal("ONVIF Media origin boundary was relaxed")
		}
	}
}
