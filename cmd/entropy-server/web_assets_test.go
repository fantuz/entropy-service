package main

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbeddedWebShowcase(t *testing.T) {
	webRoot, err := fs.Sub(webFS, "web")
	if err != nil {
		t.Fatal(err)
	}

	handler := http.FileServer(http.FS(webRoot))

	for _, path := range []string{
		"/", "/theme.css", "/theme.js", "/words-page.js", "/entropy-display.js",
		"/entropy-display-worker.js", "/worker.js", "/analysis-math.js", "/analysis-plots.js", "/analysis-workspace.js", "/lab-samples.js", "/lab-adapters.js", "/lab-registration.js", "/source-lab.js", "/stream-analysis.js", "/stream-entropy-alert.js", "/file-upload.js", "/status-dashboard.js", "/status-kpis.js", "/gallery.js",
		"/structure-math.js", "/structure-plots.js", "/audio-analysis.js", "/audio-plots.js", "/diagnostic-workspace.js",
		"/api-bytes.html", "/api-colors.html", "/api-words.html",
		"/api-sound.html", "/api-sources.html", "/api-socket.html", "/api-upload.html", "/api-stream.html", "/api-gallery.html", "/api-status.html",
	} {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
			handler.ServeHTTP(w, r)

			if w.Code != http.StatusOK {
				t.Fatalf("GET %s returned %d, want 200", path, w.Code)
			}
		})
	}

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	if !strings.Contains(w.Body.String(), "Explore in your browser") ||
		strings.Contains(w.Body.String(), "<a href=\"worker.js\"") {
		t.Fatal("/ should display the curated homepage, not an asset directory listing")
	}
}

func TestWebPagesHaveNoDuplicateAliases(t *testing.T) {
	webRoot, err := fs.Sub(webFS, "web")
	if err != nil {
		t.Fatal(err)
	}

	handler := http.FileServer(http.FS(webRoot))

	for _, path := range []string{
		"/bytes.html", "/colors.html", "/words.html",
		"/sound.html", "/upload.html", "/stream.html",
	} {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
			handler.ServeHTTP(w, r)

			if w.Code != http.StatusNotFound {
				t.Fatalf("GET %s returned %d, want 404 (use api-prefixed page)", path, w.Code)
			}
		})
	}
}

func TestWebConnectionStatusIsSingleElement(t *testing.T) {
	webRoot, err := fs.Sub(webFS, "web")
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"api-bytes.html", "api-colors.html", "api-words.html", "api-stream.html"} {
		t.Run(name, func(t *testing.T) {
			data, err := fs.ReadFile(webRoot, name)
			if err != nil {
				t.Fatal(err)
			}

			content := string(data)

			if strings.Count(content, "id=\"stream-notice\"") != 1 ||
				strings.Contains(content, "id=\"connection-status\"") ||
				!strings.Contains(content, "href=\"/theme.css\"") {
				t.Fatal("browser page must have one unobtrusive stream notice and the common theme")
			}
		})
	}
}

func TestHomepageLinksAllPublicRoutes(t *testing.T) {
	root, err := fs.Sub(webFS, "web")
	if err != nil {
		t.Fatal(err)
	}

	data, err := fs.ReadFile(root, "index.html")
	if err != nil {
		t.Fatal(err)
	}

	content := string(data)

	for _, route := range []string{
		"/api-bytes.html", "/api-colors.html", "/api-words.html",
		"/api-sound.html", "/api-sources.html", "/api-socket.html", "/api-upload.html", "/api-stream.html", "/api-gallery.html", "/api-status.html",
		"/live", "/ready", "/health-1", "/health-2", "/metrics", "/metrics-1", "/metrics-2",
		"/v1/data/random", "/v1/data/test", "/v1/meta/random",
		"/v1/image/random", "/v1/image/heatmap", "/v1/qr/random",
		"/sound", "/files", "/paroleparoleparole",
		"WS /stream", "WS /bytes", "WS /colors", "WS /words",
		"/aa_health-1", "/aa_health-2", "/aa_metrics-1", "/aa_metrics-2",
		"/zz_v1-data-random.html", "/zz_v1-data-test.html",
		"/zz_v1-image-random.html", "/zz_v1-image-heatmap.html",
		"/zz_v1-meta-random.html", "/zz_v1-qr-random.html",
	} {
		if !strings.Contains(content, route) {
			t.Errorf("homepage does not mention public route %q", route)
		}
	}
}

func TestNewWebPagesContainRealEndpoints(t *testing.T) {
	root, err := fs.Sub(webFS, "web")
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string][]string{
		"api-gallery.html": {
			"gallery.js", "gallery-preview", "/v1/image/random", "/v1/image/heatmap",
			"/v1/qr/random", "/v1/meta/random?words=8",
		},
		"api-status.html": {
			"/live", "/ready", "/metrics", "/health-1", "/health-2", "/metrics-1",
		},
		"api-stream.html": {
			"received-bytes", "received-frames", "analysed-bytes", "analysed-frames",
			"entropy-alert-card", "entropy-threshold", "stream-entropy-alert.js",
		},
		"api-upload.html": {
			"uploadForm", "upload-status", "entropy", "file-entropy", "file-sha256",
			"histogram", "mosaic", "pie", "wave", "fft", "/file-upload.js",
		},
		"api-words.html": {"words-content", "page-words"},
	}

	for name, required := range tests {
		t.Run(name, func(t *testing.T) {
			data, err := fs.ReadFile(root, name)
			if err != nil {
				t.Fatal(err)
			}

			for _, fragment := range required {
				if !strings.Contains(string(data), fragment) {
					t.Errorf("%s is missing %q", name, fragment)
				}
			}
		})
	}
}

// The gallery must use one refresh coordinator; separate iframes let the words
// endpoint refresh independently of the PNG endpoints.
func TestGalleryUsesCoordinatedPreviewRefresh(t *testing.T) {
	root, err := fs.Sub(webFS, "web")
	if err != nil {
		t.Fatal(err)
	}

	html, err := fs.ReadFile(root, "api-gallery.html")
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(html), "<iframe") || !strings.Contains(string(html), "/gallery.js") {
		t.Fatal("gallery must use a shared browser refresh coordinator, not independent iframes")
	}

	script, err := fs.ReadFile(root, "gallery.js")
	if err != nil {
		t.Fatal(err)
	}

	for _, token := range []string{
		"Promise.allSettled", "cache: \"no-store\"", "new DOMParser()",
		"/v1/image/random", "/v1/image/heatmap", "/v1/qr/random", "/v1/meta/random?words=8",
	} {
		if !strings.Contains(string(script), token) {
			t.Errorf("gallery controller missing %q", token)
		}
	}
}

// Status KPIs must use existing metrics; the removed software ring buffer
// must not appear as a physical entropy-buffer fullness indicator.
func TestStatusCapacityDashboard(t *testing.T) {
	root, err := fs.Sub(webFS, "web")
	if err != nil {
		t.Fatal(err)
	}

	page, err := fs.ReadFile(root, "api-status.html")
	if err != nil {
		t.Fatal(err)
	}

	for _, item := range []string{
		"/status-kpis.js", "kpi-httpMemory-meter", "kpi-wsMemory-meter",
		"kpi-httpSlots-meter", "kpi-wsSlots-meter", "kpi-generation-rate",
		"kpi-ws-reject-rate", "kpi-http-reject-rate",
		"status-interval", "kpi-reseed-headroom", "kpi-reseed-rate", "kpi-reseed-failure-rate",
	} {
		if !strings.Contains(string(page), item) {
			t.Errorf("status page missing %q", item)
		}
	}

	script, err := fs.ReadFile(root, "status-kpis.js")
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{
		"http_generation_memory_reserved_bytes", "ws_memory_reserved_bytes",
		"ws_rejected_connections_total", "http_generation_rejected_requests_total",
		"rng_bytes_generated_total", "drbg_instance_count",
		"rng_reseeds_total", "rng_reseed_failures_total",
	} {
		if !strings.Contains(string(script), name) {
			t.Errorf("KPI controller missing metric %q", name)
		}
	}

	if strings.Contains(string(script), "qrng_buffer_capacity_pct") {
		t.Fatal("disabled QRNG buffer must not be shown as a fill percentage")
	}
}
