package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

const maxUICatalogBytes = 2 << 20

func uiLanguageCodes() []string {
	return []string{"en", "fr", "de", "es", "cs", "ru", "pl", "el", "it", "nl", "pt-PT", "pt-BR", "ja", "ar"}
}

func newWebAssetHandler() (http.Handler, error) {
	root, err := fs.Sub(webFS, "web")
	if err != nil {
		return nil, fmt.Errorf("open embedded web assets: %w", err)
	}

	catalogs, err := installedUICatalogs()
	if err != nil {
		return nil, fmt.Errorf("load installed interface catalogs: %w", err)
	}

	return newStaticAssetHandlerWithCatalogs(root, catalogs)
}

// Only known non-English JSON files can enter the public asset map. Snapshot
// them before listeners start, rather than serving a mutable directory or cwd.
func loadUICatalogDirectory(directory string) (map[string][]byte, error) {
	info, err := os.Lstat(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string][]byte{}, nil
	}

	if err != nil {
		return nil, fmt.Errorf("inspect interface catalog directory: %w", err)
	}

	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("interface catalog directory must be a real directory, not a link")
	}

	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, fmt.Errorf("open interface catalog directory: %w", err)
	}
	defer func() { _ = root.Close() }()

	englishBytes, err := webFS.ReadFile("web/locales/en.json")
	if err != nil {
		return nil, fmt.Errorf("read built-in English interface catalog: %w", err)
	}

	english, err := parseUICatalog(englishBytes)
	if err != nil {
		return nil, fmt.Errorf("validate built-in English interface catalog: %w", err)
	}

	catalogs := make(map[string][]byte)

	for _, code := range uiLanguageCodes()[1:] {
		name := code + ".json"

		info, err := root.Lstat(name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}

		if err != nil {
			return nil, fmt.Errorf("inspect interface catalog %s: %w", name, err)
		}

		if !info.Mode().IsRegular() || info.Size() > maxUICatalogBytes {
			return nil, fmt.Errorf("interface catalog %s must be a regular file of at most 2 MiB", name)
		}

		body, err := readUICatalog(root, name)
		if err != nil {
			return nil, fmt.Errorf("read interface catalog %s: %w", name, err)
		}

		catalog, err := parseUICatalog(body)
		if err != nil {
			return nil, fmt.Errorf("invalid interface catalog %s: %w", name, err)
		}

		if err := matchUICatalog(english, catalog); err != nil {
			return nil, fmt.Errorf("interface catalog %s does not match this server; reinstall its interface language files: %w", name, err)
		}

		catalogs["locales/"+name] = body
	}

	return catalogs, nil
}

func readUICatalog(root *os.Root, name string) ([]byte, error) {
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}

	if !info.Mode().IsRegular() {
		return nil, errors.New("catalog is not a regular file")
	}

	body, err := io.ReadAll(io.LimitReader(file, maxUICatalogBytes+1))
	if err != nil {
		return nil, err
	}

	if len(body) > maxUICatalogBytes {
		return nil, errors.New("catalog exceeds 2 MiB")
	}

	return body, nil
}

func parseUICatalog(body []byte) (map[string]string, error) {
	if len(body) > maxUICatalogBytes || !utf8.Valid(body) {
		return nil, errors.New("catalog must be UTF-8 JSON of at most 2 MiB")
	}

	decoder := json.NewDecoder(bytes.NewReader(body))

	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return nil, errors.New("catalog must be a JSON object of text messages")
	}

	catalog := make(map[string]string)

	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("read message key: %w", err)
		}

		key, ok := token.(string)
		if !ok || len(key) == 0 || len(key) > 4096 {
			return nil, errors.New("catalog has an empty or oversized message key")
		}

		if _, duplicate := catalog[key]; duplicate {
			return nil, errors.New("catalog repeats a message key")
		}

		var value string
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("catalog message must be text: %w", err)
		}

		if strings.TrimSpace(value) == "" || len(value) > 8192 {
			return nil, errors.New("catalog has an empty or oversized message")
		}

		catalog[key] = value
	}

	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("close catalog: %w", err)
	}

	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("catalog has trailing data")
	}

	return catalog, nil
}

func matchUICatalog(english, catalog map[string]string) error {
	if len(catalog) != len(english) {
		return errors.New("message key count differs from English")
	}

	placeholder := regexp.MustCompile(`\{([A-Za-z][A-Za-z0-9_]*)\}`)

	for key := range english {
		value, ok := catalog[key]
		if !ok {
			return errors.New("message keys differ from English")
		}

		want := placeholder.FindAllString(key, -1)
		got := placeholder.FindAllString(value, -1)

		slices.Sort(want)
		slices.Sort(got)

		if !slices.Equal(want, got) {
			return errors.New("message interpolation placeholders differ from English")
		}
	}

	return nil
}
