//go:build !external_ui_locales

package main

import "embed"

// Standalone builds remain self-contained, with every interface catalog.
//
//go:embed web
var webFS embed.FS

const externalUILocales = false

func installedUICatalogs() (map[string][]byte, error) {
	return map[string][]byte{}, nil
}
