package config

import (
	"io"
	"testing"
)

func TestUploadAnalysisWindow(t *testing.T) {
	for _, value := range []string{"0", "-1", "33554433"} {
		if _, err := parseConfig([]string{"-fingerprint=" + value}, io.Discard); err == nil {
			t.Fatalf("accepted invalid analysis window %s", value)
		}
	}

	for _, value := range []string{"1", "33554432"} {
		if _, err := parseConfig([]string{"-fingerprint=" + value}, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
}
