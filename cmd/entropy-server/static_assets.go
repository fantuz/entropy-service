package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"mime"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	uiCatalogVersionPlaceholder = "__ENTROPY_UI_CATALOG_VERSION__"
	staticIndexAsset            = "index.html"
	staticGzipEncoding          = "gzip"
)

type staticRepresentation struct {
	body []byte
	etag string
	size int
}

type staticAsset struct {
	identity    staticRepresentation
	compressed  staticRepresentation
	contentType string
	locale      bool
}

type staticAssetHandler struct {
	assets         map[string]staticAsset
	fallback       http.Handler
	files          http.FileSystem
	catalogVersion string
}

// newStaticAssetHandler prepares an immutable public filesystem once. Neither
// hashing nor compression occurs on requests. The root must remain immutable for
// the handler lifetime; dynamic routes never use this handler.
func newStaticAssetHandler(root fs.FS) (http.Handler, error) {
	bodies := make(map[string][]byte)

	if err := fs.WalkDir(root, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !entry.Type().IsRegular() {
			return nil
		}

		body, err := fs.ReadFile(root, name)
		if err != nil {
			return fmt.Errorf("read public asset %q: %w", name, err)
		}

		bodies[name] = body

		return nil
	}); err != nil {
		return nil, fmt.Errorf("load public assets: %w", err)
	}

	version := staticCatalogVersion(bodies)
	if body, ok := bodies["i18n.js"]; ok {
		placeholder := []byte(uiCatalogVersionPlaceholder)
		if bytes.Count(body, placeholder) != 1 {
			return nil, errors.New("public i18n.js must contain exactly one catalog-version placeholder")
		}

		bodies["i18n.js"] = bytes.Replace(body, placeholder, []byte(version), 1)
	}

	files := http.FS(root)

	handler := &staticAssetHandler{
		assets:         make(map[string]staticAsset, len(bodies)),
		fallback:       http.FileServer(files),
		files:          files,
		catalogVersion: version,
	}

	compressor, err := newStaticCompressor()
	if err != nil {
		return nil, err
	}

	for name, body := range bodies {
		asset := staticAsset{identity: staticRepresentationFor(body), contentType: staticContentType(name, body), locale: staticLocaleName(name)}

		if staticCompressible(asset.contentType) && len(body) >= 256 {
			compressed, err := compressor.compress(body)
			if err != nil {
				return nil, fmt.Errorf("compress public asset %q: %w", name, err)
			}

			if len(compressed) < len(body) {
				asset.compressed = staticRepresentationFor(compressed)
			}
		}

		// Keep original embedded data in its filesystem, rather than retaining a
		// second raw copy. Only the patched script needs an owned identity body.
		if name != "i18n.js" {
			asset.identity.body = nil
		}

		handler.assets[name] = asset
	}

	return handler, nil
}

func staticCatalogVersion(bodies map[string][]byte) string {
	names := make([]string, 0)

	for name := range bodies {
		if staticLocaleName(name) {
			names = append(names, name)
		}
	}

	sort.Strings(names)

	digest := sha256.New()

	for _, name := range names {
		body := bodies[name]
		// Lengths make the concatenation unambiguous, including binary input.
		_, _ = fmt.Fprintf(digest, "%d:%s:%d:", len(name), name, len(body))
		_, _ = digest.Write(body)
	}

	return hex.EncodeToString(digest.Sum(nil))
}

func staticLocaleName(name string) bool {
	return path.Dir(name) == "locales" && strings.HasSuffix(name, ".json")
}

func staticRepresentationFor(body []byte) staticRepresentation {
	digest := sha256.Sum256(body)

	return staticRepresentation{body: body, etag: `"` + hex.EncodeToString(digest[:]) + `"`, size: len(body)}
}

func staticContentType(name string, body []byte) string {
	if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
		return contentType
	}

	return http.DetectContentType(body)
}

func staticCompressible(contentType string) bool {
	return strings.HasPrefix(contentType, "text/") || strings.HasPrefix(contentType, "application/json") ||
		strings.HasPrefix(contentType, "application/javascript") || strings.HasPrefix(contentType, "image/svg+xml")
}

type staticCompressor struct {
	buffer bytes.Buffer
	writer *gzip.Writer
}

func newStaticCompressor() (*staticCompressor, error) {
	compressor := &staticCompressor{}

	writer, err := gzip.NewWriterLevel(&compressor.buffer, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}

	compressor.writer = writer

	return compressor, nil
}

func (c *staticCompressor) compress(body []byte) ([]byte, error) {
	c.buffer.Reset()
	c.writer.Reset(&c.buffer)

	if _, err := c.writer.Write(body); err != nil {
		return nil, err
	}

	if err := c.writer.Close(); err != nil {
		return nil, err
	}

	return bytes.Clone(c.buffer.Bytes()), nil
}

func (h *staticAssetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache")

	name := strings.TrimPrefix(r.URL.Path, "/")
	clean := path.Clean(r.URL.Path)

	if r.URL.Path != clean && r.URL.Path != clean+"/" || strings.HasSuffix(name, "/"+staticIndexAsset) || name == staticIndexAsset {
		h.fallback.ServeHTTP(w, r)
		return
	}

	if name == "" || strings.HasSuffix(name, "/") {
		name += staticIndexAsset
	}

	asset, ok := h.assets[name]
	if !ok {
		h.fallback.ServeHTTP(w, r)
		return
	}

	// Even an identity-only asset can reject identity;q=0, so this request
	// header influences both successful representations and 406 responses.
	w.Header().Add("Vary", "Accept-Encoding")

	representation := asset.identity

	encoding, acceptable := staticEncoding(r.Header.Values("Accept-Encoding"), asset.compressed.body != nil)
	if !acceptable {
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "no acceptable static asset encoding", http.StatusNotAcceptable)

		return
	}

	if encoding == staticGzipEncoding && r.Header.Get("Range") != "" {
		// ServeContent cannot gzip its multipart wrapper. Prefer ordinary
		// identity byte ranges; if identity is forbidden, RFC 9110 permits
		// ignoring Range and returning the full selected representation.
		if _, identityAccepted := staticEncoding(r.Header.Values("Accept-Encoding"), false); identityAccepted {
			encoding = ""
		} else {
			r = r.Clone(r.Context())
			r.Header.Del("Range")
			r.Header.Del("If-Range")
		}
	}

	if encoding == staticGzipEncoding {
		representation = asset.compressed

		w.Header().Set("Content-Encoding", encoding)
	}

	var reader io.ReadSeeker
	if representation.body != nil {
		reader = bytes.NewReader(representation.body)
	} else {
		file, err := h.files.Open(name)
		if err != nil {
			w.Header().Set("Cache-Control", "no-store")
			h.fallback.ServeHTTP(w, r)

			return
		}
		defer func() { _ = file.Close() }()

		reader = file
	}

	if asset.locale && h.matchingCatalogVersion(r) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}

	w.Header().Set("ETag", representation.etag)
	w.Header().Set("Content-Type", asset.contentType)

	response := &staticAssetResponseWriter{ResponseWriter: w, size: representation.size}
	http.ServeContent(response, r, name, time.Time{}, reader)
}

// ServeContent deliberately leaves the length to compression middleware. Set
// that known length only on a successful response, after preconditions resolve.
// Error responses never inherit immutable caching or an encoded body length.
type staticAssetResponseWriter struct {
	http.ResponseWriter

	size int
}

func (w *staticAssetResponseWriter) WriteHeader(status int) {
	if w.Header().Get("Content-Encoding") != "" {
		w.Header().Set("Accept-Ranges", "none")
	}

	if status == http.StatusOK && w.Header().Get("Content-Length") == "" {
		w.Header().Set("Content-Length", strconv.Itoa(w.size))
	}

	if status >= http.StatusBadRequest {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Del("Content-Encoding")
		w.Header().Del("Content-Length")
	}

	w.ResponseWriter.WriteHeader(status)
}

func (w *staticAssetResponseWriter) ReadFrom(source io.Reader) (int64, error) {
	if reader, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		return reader.ReadFrom(source)
	}

	return io.Copy(struct{ io.Writer }{w.ResponseWriter}, source)
}

func (h *staticAssetHandler) matchingCatalogVersion(r *http.Request) bool {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query) != 1 {
		return false
	}

	versions := query["v"]

	return len(versions) == 1 && versions[0] == h.catalogVersion
}

// staticEncoding honors explicit exclusions before the wildcard. Identity stays
// acceptable by default unless refused by identity;q=0 or an unqualified *;q=0.
func staticEncoding(values []string, compressed bool) (string, bool) {
	qualities := make(map[string]float64)

	for _, value := range values {
		for field := range strings.SplitSeq(value, ",") {
			parts := strings.Split(field, ";")
			name := strings.ToLower(strings.TrimSpace(parts[0]))

			if name != staticGzipEncoding && name != "identity" && name != "*" {
				continue
			}

			quality := 1.0

			if len(parts) > 1 {
				key, raw, found := strings.Cut(strings.TrimSpace(parts[1]), "=")
				parsed, err := strconv.ParseFloat(raw, 64)

				if !found || !strings.EqualFold(key, "q") || err != nil || parsed < 0 || parsed > 1 || len(parts) != 2 || math.IsNaN(parsed) {
					quality = 0
				} else {
					quality = parsed
				}
			}

			qualities[name] = quality
		}
	}

	identity, explicitIdentity := qualities["identity"]
	if !explicitIdentity {
		identity = 1
		if wildcard, found := qualities["*"]; found && wildcard == 0 {
			identity = 0
		}
	}

	gzipQuality, explicitGzip := qualities[staticGzipEncoding]
	if !explicitGzip {
		gzipQuality = qualities["*"]
	}

	if compressed && gzipQuality > 0 && gzipQuality >= identity {
		return staticGzipEncoding, true
	}

	return "", identity > 0
}
