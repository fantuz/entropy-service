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
		"/", "/theme.css", "/entropy-display.js",
		"/entropy-display-worker.js", "/worker.js", "/stream-analysis.js", "/file-upload.js",
		"/api-bytes.html", "/api-colors.html", "/api-words.html",
		"/api-sound.html", "/api-upload.html", "/api-stream.html",
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
		"/api-sound.html", "/api-upload.html", "/api-stream.html",
		"/live", "/ready", "/health-1", "/health-2", "/metrics-1", "/metrics-2",
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
