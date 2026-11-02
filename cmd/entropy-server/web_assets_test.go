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
		"/entropy-display-worker.js", "/worker.js", "/file-upload.js",
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

	for _, name := range []string{"api-bytes.html", "api-colors.html", "api-words.html"} {
		t.Run(name, func(t *testing.T) {
			data, err := fs.ReadFile(webRoot, name)
			if err != nil {
				t.Fatal(err)
			}

			content := string(data)

			if strings.Count(content, "id=\"connection-status\"") != 1 ||
				!strings.Contains(content, "href=\"/theme.css\"") {
				t.Fatal("browser page must have one connection status and the common theme")
			}
		})
	}
}
