package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/wordlist"
)

func writeUICatalogFixture(t *testing.T, directory, name string, body []byte) {
	t.Helper()

	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = root.Close() }()

	if err := root.WriteFile(name, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestUICatalogEmbeddingMode(t *testing.T) {
	for _, code := range uiLanguageCodes() {
		_, err := webFS.ReadFile("web/locales/" + code + ".json")
		if externalUILocales && code != "en" {
			if !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("optional %s catalog is still compiled into the installer binary: %v", code, err)
			}
		} else if err != nil {
			t.Fatalf("built-in %s catalog missing: %v", code, err)
		}
	}

	entries, err := os.ReadDir("web")
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			if _, err := webFS.ReadFile("web/" + entry.Name()); err != nil {
				t.Errorf("core public asset %s was dropped by the embed pattern: %v", entry.Name(), err)
			}
		}
	}
}

func TestUICatalogMissingDirectoryAndKnownFiles(t *testing.T) {
	directory := t.TempDir()

	catalogs, err := loadUICatalogDirectory(filepath.Join(directory, "absent"))
	if err != nil || len(catalogs) != 0 {
		t.Fatalf("an absent optional directory must retain English: %v, %d catalogs", err, len(catalogs))
	}

	writeUICatalogFixture(t, directory, "unknown.json", []byte("private non-catalog data"))
	writeUICatalogFixture(t, directory, "en.json", []byte("English is always the immutable built-in catalog"))

	catalogs, err = loadUICatalogDirectory(directory)
	if err != nil || len(catalogs) != 0 {
		t.Fatalf("unknown files and optional duplicate English must never enter the public map: %v", err)
	}

	if !externalUILocales {
		installed, err := installedUICatalogs()
		if err != nil || len(installed) != 0 {
			t.Fatal("standalone builds must not discover external catalog files")
		}
	}
}

func TestUICatalogSnapshotsAndCacheVersions(t *testing.T) {
	directory := t.TempDir()

	french, err := os.ReadFile("web/locales/fr.json")
	if err != nil {
		t.Fatal(err)
	}

	writeUICatalogFixture(t, directory, "fr.json", french)

	catalogs, err := loadUICatalogDirectory(directory)
	if err != nil || len(catalogs) != 1 || !bytes.Equal(catalogs["locales/fr.json"], french) {
		t.Fatalf("valid selected catalog was not loaded: %v", err)
	}

	root := staticFixtureFS()
	delete(root, "locales/fr.json")
	englishOnly := makeStaticHandler(t, root)

	prepared, err := newStaticAssetHandlerWithCatalogs(root, catalogs)
	if err != nil {
		t.Fatal(err)
	}

	handler, ok := prepared.(*staticAssetHandler)
	if !ok {
		t.Fatal("unexpected public handler")
	}

	version := handler.catalogVersion
	if version == englishOnly.catalogVersion {
		t.Fatal("installing/removing a catalog must change the cache namespace")
	}
	// Neither later file changes nor mutation of the caller's byte map may
	// change an already-published immutable response or its ETag.
	writeUICatalogFixture(t, directory, "fr.json", []byte("changed after startup"))
	clear(catalogs["locales/fr.json"])

	response := staticRequest(t, handler, http.MethodGet, "/locales/fr.json?v="+version, nil)
	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), french) || !strings.Contains(response.Header().Get("Cache-Control"), "immutable") {
		t.Fatal("external catalog identity response must use the validated immutable snapshot")
	}

	assertStaticETag(t, response)

	gzip := staticRequest(t, handler, http.MethodGet, "/locales/fr.json?v="+version, map[string]string{"Accept-Encoding": "gzip"})
	if gzip.Code != http.StatusOK || !bytes.Equal(decodeStaticGzip(t, gzip), french) {
		t.Fatal("external catalog gzip must match the retained identity bytes")
	}

	assertStaticETag(t, gzip)

	for _, target := range []string{"/locales/de.json", "/locales/unknown.json", "/ui-locales/fr.json"} {
		if got := staticRequest(t, handler, http.MethodGet, target, nil).Code; got != http.StatusNotFound {
			t.Errorf("unselected or nonpublic file %s returned %d", target, got)
		}
	}

	script := staticRequest(t, handler, http.MethodGet, "/i18n.js", nil)
	if !strings.Contains(script.Body.String(), `installedLanguages="en,fr"`) {
		t.Fatalf("selector must expose only installed catalogs: %s", script.Body.String())
	}

	englishScript := staticRequest(t, englishOnly, http.MethodGet, "/i18n.js", nil)
	if !strings.Contains(englishScript.Body.String(), `installedLanguages="en"`) || script.Header().Get("ETag") == englishScript.Header().Get("ETag") {
		t.Fatal("removing catalogs must change the selector and i18n script validator")
	}
}

func TestUICatalogInvalidFilesFailStartup(t *testing.T) {
	english, err := webFS.ReadFile("web/locales/en.json")
	if err != nil {
		t.Fatal(err)
	}

	for name, body := range map[string][]byte{
		"malformed": []byte("{"), "nonobject": []byte("[]"), "null": []byte("null"),
		"duplicate": []byte(`{"A":"B","A":"C"}`), "trailing": append(bytes.Clone(english), []byte(`{}`)...),
		"stale": []byte(`{"Obsolete message":"Translation"}`), "nontext": []byte(`{"A":42}`),
		"invalid-utf8": {0xff}, "oversize": bytes.Repeat([]byte("x"), maxUICatalogBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			writeUICatalogFixture(t, directory, "fr.json", body)

			if _, err := loadUICatalogDirectory(directory); err == nil {
				t.Fatal("an installed invalid catalog must fail startup, not disappear silently")
			}
		})
	}

	catalog, err := parseUICatalog(english)
	if err != nil {
		t.Fatal(err)
	}

	for key := range catalog {
		if strings.Contains(key, "{status}") {
			catalog[key] = "Lost interpolation placeholder"
			break
		}
	}

	body, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}

	directory := t.TempDir()
	writeUICatalogFixture(t, directory, "fr.json", body)

	if _, err := loadUICatalogDirectory(directory); err == nil || !strings.Contains(err.Error(), "placeholders") {
		t.Fatalf("changed interpolation must fail with an actionable error: %v", err)
	}
}

func TestUICatalogLinksAndNonfilesAreNotPublic(t *testing.T) {
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "fr.json"), 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := loadUICatalogDirectory(directory); err == nil {
		t.Fatal("a catalog path must not be a directory")
	}

	linked := filepath.Join(t.TempDir(), "ui-locales")
	if err := os.Symlink(directory, linked); err != nil {
		t.Skipf("symlink creation unavailable on this host: %v", err)
	}

	if _, err := loadUICatalogDirectory(linked); err == nil {
		t.Fatal("a catalog directory must not be a symlink")
	}

	directory = t.TempDir()

	outside := filepath.Join(t.TempDir(), "secret.json")
	if err := os.WriteFile(outside, []byte(`{"secret":"not public"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(outside, filepath.Join(directory, "fr.json")); err != nil {
		t.Fatal(err)
	}

	if _, err := loadUICatalogDirectory(directory); err == nil {
		t.Fatal("an external catalog file must not follow a symlink to private data")
	}
}

func TestUICatalogSelectionDoesNotChangeRandomWordDictionaries(t *testing.T) {
	before := wordlist.Catalog()
	if len(before) != 18 {
		t.Fatalf("random-word dictionary catalog unexpectedly has %d entries", len(before))
	}

	for _, info := range before {
		dictionary, ok := wordlist.Lookup(info.Code)
		if !ok || dictionary.Size() != info.Size || dictionary.Word(0) == "" {
			t.Fatalf("random-word dictionary %s is incomplete", info.Code)
		}
	}

	if _, err := loadUICatalogDirectory(filepath.Join(t.TempDir(), "missing")); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(before, wordlist.Catalog()) {
		t.Fatal("optional interface catalogs changed random-word dictionaries")
	}
}
