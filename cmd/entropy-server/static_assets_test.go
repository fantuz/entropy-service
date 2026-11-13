package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/drbg"
)

func staticFixtureFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":        {Data: []byte("<!doctype html><title>Home</title>" + strings.Repeat("<p>Public sample</p>", 60))},
		"i18n.js":           {Data: []byte(`const version="` + uiCatalogVersionPlaceholder + `";` + strings.Repeat("\n// Public interface catalog loader", 50))},
		"theme.css":         {Data: []byte(strings.Repeat("body { color: #123456; background: white; }\n", 100))},
		"locales/en.json":   {Data: []byte(`{"Hello":"Hello"}`)},
		"locales/fr.json":   {Data: []byte(`{"Hello":"` + strings.Repeat("Bonjour ", 100) + `"}`)},
		"pixel.png":         {Data: append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 1024)...)},
		"folder/index.html": {Data: []byte("<!doctype html><title>Folder</title>")},
	}
}

func makeStaticHandler(tb testing.TB, root fs.FS) *staticAssetHandler {
	tb.Helper()

	handler, err := newStaticAssetHandler(root)
	if err != nil {
		tb.Fatal(err)
	}

	result, ok := handler.(*staticAssetHandler)
	if !ok {
		tb.Fatal("unexpected static handler implementation")
	}

	return result
}

func staticRequest(t *testing.T, handler http.Handler, method, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequestWithContext(t.Context(), method, target, nil)
	for name, value := range headers {
		request.Header.Set(name, value)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	return response
}

func decodeStaticGzip(t *testing.T, response *httptest.ResponseRecorder) []byte {
	t.Helper()

	reader, err := gzip.NewReader(bytes.NewReader(response.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()

	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}

	return body
}

func assertStaticETag(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()

	digest := sha256.Sum256(response.Body.Bytes())

	want := `"` + hex.EncodeToString(digest[:]) + `"`
	if got := response.Header().Get("ETag"); got != want {
		t.Fatalf("strong ETag = %q, want representation digest %q", got, want)
	}
}

func TestStaticAssetsEmbeddedRepresentations(t *testing.T) {
	root, err := fs.Sub(webFS, "web")
	if err != nil {
		t.Fatal(err)
	}

	handler := makeStaticHandler(t, root)
	for _, name := range []string{"/", "/theme.css", "/api-stream.html", "/locales/fr.json", "/locales/el.json"} {
		t.Run(name, func(t *testing.T) {
			identity := staticRequest(t, handler, http.MethodGet, name, nil)
			compressed := staticRequest(t, handler, http.MethodGet, name, map[string]string{"Accept-Encoding": "gzip"})

			if identity.Code != http.StatusOK || compressed.Code != http.StatusOK || compressed.Header().Get("Content-Encoding") != "gzip" {
				t.Fatalf("identity/gzip responses = %d/%d; headers %v", identity.Code, compressed.Code, compressed.Header())
			}

			if !bytes.Equal(decodeStaticGzip(t, compressed), identity.Body.Bytes()) || compressed.Body.Len() >= identity.Body.Len() {
				t.Fatal("gzip must preserve the complete public body while reducing transfer bytes")
			}

			assertStaticETag(t, identity)
			assertStaticETag(t, compressed)

			if compressed.Header().Get("ETag") == identity.Header().Get("ETag") || compressed.Header().Get("Vary") != "Accept-Encoding" || identity.Header().Get("Vary") != "Accept-Encoding" {
				t.Fatal("identity and gzip need distinct validators and an encoding cache key")
			}

			if identity.Header().Get("Cache-Control") != "no-cache" || compressed.Header().Get("Cache-Control") != "no-cache" {
				t.Fatal("unversioned public assets must revalidate")
			}
		})
	}

	script := staticRequest(t, handler, http.MethodGet, "/i18n.js", nil)
	if strings.Contains(script.Body.String(), uiCatalogVersionPlaceholder) || !strings.Contains(script.Body.String(), handler.catalogVersion) {
		t.Fatal("served script must contain the real catalog version, not its build placeholder")
	}
}

func TestStaticAssetsEncodingNegotiation(t *testing.T) {
	handler := makeStaticHandler(t, staticFixtureFS())
	for _, test := range []struct {
		header   string
		encoding string
		status   int
	}{
		{"", "", http.StatusOK},
		{"gzip", "gzip", http.StatusOK},
		{"GZip; Q=1", "gzip", http.StatusOK},
		{"deflate, gzip;q=1, br", "gzip", http.StatusOK},
		{"gzip;q=0", "", http.StatusOK},
		{"notgzip, xgzip", "", http.StatusOK},
		{"*", "gzip", http.StatusOK},
		{"*;q=1, gzip;q=0", "", http.StatusOK},
		{"*;q=0, gzip;q=1", "gzip", http.StatusOK},
		{"gzip;q=0.5, identity;q=0.2", "gzip", http.StatusOK},
		{"gzip;q=0.2, identity;q=0.5", "", http.StatusOK},
		{"gzip;q=bogus", "", http.StatusOK},
		{"gzip;q=NaN", "", http.StatusOK},
		{"gzip;q=1.1", "", http.StatusOK},
		{"identity;q=0, gzip;q=0", "", http.StatusNotAcceptable},
		{"*;q=0", "", http.StatusNotAcceptable},
	} {
		t.Run(test.header, func(t *testing.T) {
			response := staticRequest(t, handler, http.MethodGet, "/theme.css", map[string]string{"Accept-Encoding": test.header})
			if response.Code != test.status || response.Header().Get("Content-Encoding") != test.encoding {
				t.Fatalf("encoding %q: code=%d encoding=%q, want %d/%q", test.header, response.Code, response.Header().Get("Content-Encoding"), test.status, test.encoding)
			}
		})
	}

	response := staticRequest(t, handler, http.MethodGet, "/pixel.png", map[string]string{"Accept-Encoding": "gzip"})
	if response.Header().Get("Content-Encoding") != "" || response.Code != http.StatusOK {
		t.Fatal("already compressed/binary assets must retain identity encoding")
	}
}

func TestStaticAssetsConditionalHeadAndRange(t *testing.T) {
	root := staticFixtureFS()
	handler := makeStaticHandler(t, root)

	for _, encoding := range []string{"identity", "gzip"} {
		t.Run(encoding, func(t *testing.T) {
			headers := map[string]string{"Accept-Encoding": encoding}
			full := staticRequest(t, handler, http.MethodGet, "/theme.css", headers)
			etag := full.Header().Get("ETag")
			head := staticRequest(t, handler, http.MethodHead, "/theme.css", headers)

			if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("ETag") != etag || head.Header().Get("Content-Length") != strconv.Itoa(full.Body.Len()) {
				t.Fatal("HEAD must describe the selected representation without a body")
			}

			headers["If-None-Match"] = etag

			notModified := staticRequest(t, handler, http.MethodGet, "/theme.css", headers)
			if notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 || notModified.Header().Get("ETag") != etag || notModified.Header().Get("Vary") != "Accept-Encoding" {
				t.Fatal("matching conditional requests must return a bodyless 304 with validator and Vary")
			}

			delete(headers, "If-None-Match")
			headers["If-Match"] = `"older-deployment"`

			failed := staticRequest(t, handler, http.MethodGet, "/theme.css", headers)
			if failed.Code != http.StatusPreconditionFailed || failed.Body.Len() != 0 || failed.Header().Get("Content-Length") != "" || failed.Header().Get("Content-Encoding") != "" || failed.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("failed preconditions must not retain encoded length or cache policy")
			}

			if encoding == "gzip" {
				return // Encoded range policy is exercised over actual HTTP below.
			}

			delete(headers, "If-Match")
			headers["If-Range"] = etag
			headers["Range"] = "bytes=0-9"

			partial := staticRequest(t, handler, http.MethodGet, "/theme.css", headers)
			if partial.Code != http.StatusPartialContent || !bytes.Equal(partial.Body.Bytes(), full.Body.Bytes()[:10]) {
				t.Fatalf("matching If-Range must return the requested encoded octets, status %d", partial.Code)
			}

			headers["If-Range"] = `"older-deployment"`

			changed := staticRequest(t, handler, http.MethodGet, "/theme.css", headers)
			if changed.Code != http.StatusOK || !bytes.Equal(changed.Body.Bytes(), full.Body.Bytes()) {
				t.Fatal("an old If-Range validator must return the complete current representation")
			}

			delete(headers, "If-Range")
			headers["Range"] = "bytes=999999-"

			invalid := staticRequest(t, handler, http.MethodGet, "/theme.css", headers)
			if invalid.Code != http.StatusRequestedRangeNotSatisfiable {
				t.Fatalf("invalid range status = %d", invalid.Code)
			}
		})
	}

	identity := staticRequest(t, handler, http.MethodGet, "/theme.css", nil)

	mismatch := staticRequest(t, handler, http.MethodGet, "/theme.css", map[string]string{"Accept-Encoding": "gzip", "If-None-Match": identity.Header().Get("ETag")})
	if mismatch.Code != http.StatusOK || mismatch.Header().Get("Content-Encoding") != "gzip" {
		t.Fatal("an identity ETag must not validate a gzip response")
	}
}

func TestStaticAssetsCatalogCacheVersion(t *testing.T) {
	root := staticFixtureFS()
	handler := makeStaticHandler(t, root)
	version := handler.catalogVersion

	for _, query := range []struct {
		value     string
		immutable bool
	}{
		{"", false},
		{"?v=" + version, true},
		{"?v=older", false},
		{"?v=" + version + "&v=" + version, false},
		{"?v=" + version + "&v=older", false},
		{"?v=" + version + "&other=value", false},
		{"?v=%zz", false},
	} {
		response := staticRequest(t, handler, http.MethodGet, "/locales/fr.json"+query.value, nil)

		immutable := strings.Contains(response.Header().Get("Cache-Control"), "immutable")
		if response.Code != http.StatusOK || immutable != query.immutable {
			t.Fatalf("query %q: status=%d Cache-Control=%q", query.value, response.Code, response.Header().Get("Cache-Control"))
		}
	}

	for _, name := range []string{"fr", "en"} {
		rejected := staticRequest(t, handler, http.MethodGet, "/locales/"+name+".json?v="+version, map[string]string{"Accept-Encoding": "gzip;q=0, identity;q=0"})
		if rejected.Code != http.StatusNotAcceptable || rejected.Header().Get("Cache-Control") != "no-store" || rejected.Header().Get("Vary") != "Accept-Encoding" {
			t.Fatalf("negotiation error must never inherit immutable caching: %d %v", rejected.Code, rejected.Header())
		}
	}

	static := staticRequest(t, handler, http.MethodGet, "/theme.css?v="+version, nil)
	if static.Header().Get("Cache-Control") != "no-cache" {
		t.Fatal("catalog versions must not make other public assets immutable")
	}

	oldScript := staticRequest(t, handler, http.MethodGet, "/i18n.js", nil)
	changed := staticFixtureFS()
	changed["locales/fr.json"] = &fstest.MapFile{Data: []byte(`{"Hello":"Nouveau"}`)}
	next := makeStaticHandler(t, changed)

	newScript := staticRequest(t, next, http.MethodGet, "/i18n.js", map[string]string{"If-None-Match": oldScript.Header().Get("ETag")})
	if version == next.catalogVersion || newScript.Code != http.StatusOK || newScript.Header().Get("ETag") == oldScript.Header().Get("ETag") || !strings.Contains(newScript.Body.String(), next.catalogVersion) {
		t.Fatal("changing only a catalog must invalidate the script/version without modifying its source")
	}

	oldURL := staticRequest(t, next, http.MethodGet, "/locales/fr.json?v="+version, nil)
	if strings.Contains(oldURL.Header().Get("Cache-Control"), "immutable") {
		t.Fatal("an old deployment version must not poison immutable cache entries")
	}

	renamed := staticFixtureFS()
	renamed["locales/de.json"] = renamed["locales/fr.json"]
	delete(renamed, "locales/fr.json")

	if makeStaticHandler(t, renamed).catalogVersion == version {
		t.Fatal("renaming a catalog must change the deployment version")
	}

	unrelated := staticFixtureFS()
	unrelated["theme.css"] = &fstest.MapFile{Data: []byte("body { color: blue; }")}

	if makeStaticHandler(t, unrelated).catalogVersion != version || makeStaticHandler(t, root).catalogVersion != version {
		t.Fatal("catalog version must be stable and independent from unrelated static files")
	}
}

func TestStaticAssetsFileServerCompatibility(t *testing.T) {
	root := staticFixtureFS()
	handler := makeStaticHandler(t, root)
	fallback := http.FileServer(http.FS(root))

	for _, target := range []string{"/", "/index.html", "/folder", "/folder/", "/folder/index.html", "/locales/", "/missing", "/theme.css/", "//theme.css", "/./theme.css"} {
		t.Run(target, func(t *testing.T) {
			actual := staticRequest(t, handler, http.MethodGet, target, nil)

			original := staticRequest(t, fallback, http.MethodGet, target, nil)
			if actual.Code != original.Code || actual.Header().Get("Location") != original.Header().Get("Location") || !bytes.Equal(actual.Body.Bytes(), original.Body.Bytes()) {
				t.Fatalf("FileServer behavior changed for %q: status %d/%d Location %q/%q", target, actual.Code, original.Code, actual.Header().Get("Location"), original.Header().Get("Location"))
			}
		})
	}
}

func TestStaticAssetsDoNotWrapRandomOutput(t *testing.T) {
	handler := makeStaticHandler(t, staticFixtureFS())
	mux := http.NewServeMux()
	mux.Handle("/", handler)
	mux.HandleFunc("/v1/data/random", randomBytesHandler(newBenchmarkDRBG(t, drbg.AES256CTR), 1<<20, 20, false, newHTTPRequestBudget(8, 8<<20)))

	response := staticRequest(t, mux, http.MethodGet, "/v1/data/random?bytes=65536&v="+handler.catalogVersion, map[string]string{"Accept-Encoding": "gzip"})
	if response.Code != http.StatusOK || response.Body.Len() != 65536 || response.Header().Get("Content-Encoding") != "" || response.Header().Get("ETag") != "" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("dynamic randomness must remain uncompressed, uncached bytes: %d %v", response.Code, response.Header())
	}
}

func TestStaticAssetsRequireCatalogVersionPlaceholder(t *testing.T) {
	for _, script := range []string{"const version='missing';", uiCatalogVersionPlaceholder + uiCatalogVersionPlaceholder} {
		root := staticFixtureFS()
		root["i18n.js"] = &fstest.MapFile{Data: []byte(script)}

		if _, err := newStaticAssetHandler(root); err == nil {
			t.Fatal("missing or repeated script placeholders must fail at startup")
		}
	}
}

func BenchmarkStaticAssetStartup(b *testing.B) {
	root, err := fs.Sub(webFS, "web")
	if err != nil {
		b.Fatal(err)
	}

	sample := makeStaticHandler(b, root)
	retained := 0

	for _, asset := range sample.assets {
		retained += len(asset.identity.body) + len(asset.compressed.body)
	}

	b.ReportAllocs()
	b.ResetTimer()
	b.ReportMetric(float64(retained), "retained-body-B")

	for range b.N {
		if _, err := newStaticAssetHandler(root); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStaticAssetConditional(b *testing.B) {
	root, err := fs.Sub(webFS, "web")
	if err != nil {
		b.Fatal(err)
	}

	handler := makeStaticHandler(b, root)
	request := httptest.NewRequestWithContext(b.Context(), http.MethodGet, "/i18n.js", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	request.Header.Set("If-None-Match", handler.assets["i18n.js"].compressed.etag)

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)

		if response.Code != http.StatusNotModified {
			b.Fatal("conditional request must return 304")
		}
	}
}

type staticHTTPResult struct {
	status int
	header http.Header
}

func staticHTTPGet(t *testing.T, client *http.Client, endpoint string, headers map[string]string) (staticHTTPResult, []byte) {
	t.Helper()

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}

	for name, value := range headers {
		request.Header.Set(name, value)
	}

	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}

	return staticHTTPResult{status: response.StatusCode, header: response.Header}, body
}

func TestStaticAssetsRangeHTTPRepresentations(t *testing.T) {
	root := staticFixtureFS()
	handler := makeStaticHandler(t, root)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	client := server.Client()
	client.Transport = &http.Transport{DisableCompression: true}
	t.Cleanup(client.CloseIdleConnections)

	endpoint := server.URL + "/theme.css"
	identity, identityBody := staticHTTPGet(t, client, endpoint, nil)
	compressed, gzipBody := staticHTTPGet(t, client, endpoint, map[string]string{"Accept-Encoding": "gzip"})

	if compressed.header.Get("Accept-Ranges") != "none" || identity.header.Get("Accept-Ranges") != "bytes" {
		t.Fatal("range capabilities must describe each selected representation")
	}

	t.Run("multipart identity", func(t *testing.T) {
		response, body := staticHTTPGet(t, client, endpoint, map[string]string{"Accept-Encoding": "gzip", "Range": "bytes=0-3,8-15"})
		if response.header.Get("Content-Encoding") == "gzip" {
			reader, err := gzip.NewReader(bytes.NewReader(body))
			if err != nil {
				t.Fatalf("outer gzip encoding is invalid for ranged HTTP response: %v; Content-Type=%q", err, response.header.Get("Content-Type"))
			}

			_ = reader.Close()
		}

		if response.status != http.StatusPartialContent || response.header.Get("Content-Encoding") != "" || response.header.Get("ETag") != identity.header.Get("ETag") {
			t.Fatalf("accepted identity must use an unencoded range response: %d %v", response.status, response.header)
		}

		mediaType, parameters, err := mime.ParseMediaType(response.header.Get("Content-Type"))
		if err != nil || mediaType != "multipart/byteranges" {
			t.Fatalf("expected byte-range multipart body: %q %v", mediaType, err)
		}

		reader := multipart.NewReader(bytes.NewReader(body), parameters["boundary"])
		for _, want := range [][]byte{identityBody[:4], identityBody[8:16]} {
			part, err := reader.NextPart()
			if err != nil {
				t.Fatal(err)
			}

			actual, err := io.ReadAll(io.LimitReader(part, 32))
			if err != nil || !bytes.Equal(actual, want) {
				t.Fatalf("identity range bytes differ: %q != %q, %v", actual, want, err)
			}
		}

		if _, err := reader.NextPart(); !errors.Is(err, io.EOF) {
			t.Fatalf("expected exactly two range parts, got %v", err)
		}
	})

	for _, test := range []struct {
		name     string
		encoding string
		ranges   string
		ifRange  string
		status   int
	}{
		{"single identity", "gzip", "bytes=0-3", "", http.StatusPartialContent},
		{"identity validator", "gzip", "bytes=0-3", identity.header.Get("ETag"), http.StatusPartialContent},
		{"gzip validator does not validate identity", "gzip", "bytes=0-3", compressed.header.Get("ETag"), http.StatusOK},
		{"gzip-only single", "gzip, identity;q=0", "bytes=0-3", "", http.StatusOK},
		{"gzip-only multipart", "gzip, identity;q=0", "bytes=0-3,8-15", "", http.StatusOK},
		{"gzip-only matching validator", "gzip, identity;q=0", "bytes=0-3", compressed.header.Get("ETag"), http.StatusOK},
		{"gzip-only mismatched validator", "gzip, identity;q=0", "bytes=0-3", identity.header.Get("ETag"), http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			response, body := staticHTTPGet(t, client, endpoint, map[string]string{"Accept-Encoding": test.encoding, "Range": test.ranges, "If-Range": test.ifRange})
			if response.status != test.status {
				t.Fatalf("range status = %d, want %d", response.status, test.status)
			}

			if strings.Contains(test.encoding, "identity;q=0") {
				if response.header.Get("Content-Encoding") != "gzip" || response.header.Get("Accept-Ranges") != "none" || response.header.Get("Content-Range") != "" || response.header.Get("ETag") != compressed.header.Get("ETag") || !bytes.Equal(body, gzipBody) {
					t.Fatal("gzip-only range must return the complete unchanged gzip representation")
				}

				reader, err := gzip.NewReader(bytes.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = reader.Close() }()

				decoded, err := io.ReadAll(io.LimitReader(reader, 1<<20))
				if err != nil || !bytes.Equal(decoded, identityBody) {
					t.Fatal("full gzip response must decode into original public bytes")
				}
			} else {
				want := identityBody
				if test.status == http.StatusPartialContent {
					want = want[:4]
				}

				if response.header.Get("Content-Encoding") != "" || response.header.Get("ETag") != identity.header.Get("ETag") || !bytes.Equal(body, want) {
					t.Fatal("identity range or fallback body differs from the selected representation")
				}
			}
		})
	}
}
