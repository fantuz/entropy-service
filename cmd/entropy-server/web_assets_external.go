//go:build external_ui_locales

package main

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

// The bundled installer supplies optional non-English interface catalogs as
// files. Only core public assets and the English validation/fallback catalog
// are compiled into this variant. Random-word dictionaries use their own embed.
//
//go:embed web/*.html web/*.js web/*.css web/aa_* web/locales/en.json
var webFS embed.FS

const externalUILocales = true

func installedUICatalogs() (map[string][]byte, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate executable for interface catalogs: %w", err)
	}

	return loadUICatalogDirectory(filepath.Join(filepath.Dir(executable), "ui-locales"))
}
