package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/config"
)

func TestClientDescriptionRedactsAndLabelsRequestedOptions(t *testing.T) {
	for _, mode := range []string{modePull, modeStream} {
		t.Run(mode, func(t *testing.T) {
			password := t.Name()
			address := &url.URL{Scheme: "https", User: url.UserPassword("person", password), Host: "example.invalid", Path: "/data", RawQuery: "token=hidden", Fragment: "private"}

			if mode == modeStream {
				address.Scheme, address.Path = "wss", "/stream"
			}

			endpoint := address.String()

			cfg := experimentConfig(mode, endpoint, "/private/report-path")
			cfg.OutputPath = "/private/output-path"

			var out bytes.Buffer
			if err := writeClientDescription(&out, cfg, endpoint); err != nil {
				t.Fatal(err)
			}

			for _, private := range []string{"person", password, "hidden", "private", "token", "output-path", "report-path"} {
				if strings.Contains(out.String(), private) {
					t.Fatalf("description disclosed %q", private)
				}
			}

			decoder := json.NewDecoder(&out)

			var got clientDescription
			if err := decoder.Decode(&got); err != nil {
				t.Fatal(err)
			}

			if got.SchemaVersion != 1 || got.Type != "configuration" || got.Role != "client" || got.Live || !got.Features["https"] || !got.Features["output"] || got.Settings.OutputKind != "file" {
				t.Fatalf("incorrect configured-only description: %+v", got)
			}

			wantOutputs := map[string]bool{"file": true}
			if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
				wantOutputs["stdout"] = true
			}

			if runtime.GOOS == "linux" {
				wantOutputs["device"] = true
			}

			if !maps.Equal(got.Backends["output"], wantOutputs) ||
				!maps.Equal(got.Backends["mode"], map[string]bool{"pull": true, "push": true}) {
				t.Fatalf("incorrect client backend inventory: %v", got.Backends)
			}

			var extra any
			if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
				t.Fatalf("description must be one JSON object: %v", err)
			}
		})
	}
}

func TestClientDescriptionRejectsInvalidSelectedEndpoint(t *testing.T) {
	for _, endpoint := range []string{"/relative", "ftp://example.invalid/data", "https:opaque", "https://"} {
		var out bytes.Buffer
		if err := writeClientDescription(&out, &config.Config{Mode: modePull}, endpoint); err == nil || out.Len() != 0 {
			t.Fatalf("invalid endpoint produced a description: %q, %v", endpoint, err)
		}
	}
}

func TestClientDescribeJSONProcess(t *testing.T) {
	if os.Getenv("ENTROPY_CLIENT_DESCRIBE_TEST") == "1" {
		separator := slices.Index(os.Args, "--")
		os.Args = append([]string{"entropy-client"}, os.Args[separator+1:]...)

		main()
		os.Exit(0)
	}

	directory := t.TempDir()

	for _, invalid := range []bool{false, true} {
		name := "unavailable-endpoint-and-unopened-outputs"
		if invalid {
			name = "invalid-flags"
		}

		t.Run(name, func(t *testing.T) {
			args := []string{
				"-test.run=^TestClientDescribeJSONProcess$", "--", "-describe-json", "-mode=http",
				"-url=https://fixture:secret@127.0.0.1:1/data?token=private",
				"-output=" + filepath.Join(directory, "output.bin"),
				"-report=" + filepath.Join(directory, "report.jsonl"),
			}
			if invalid {
				args = append(args, "-slice=0")
			}

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()

			command := exec.CommandContext(ctx, os.Args[0], args...) //nolint:gosec // G204: executes only this test binary with controlled test flags.

			command.Env = append(os.Environ(), "ENTROPY_CLIENT_DESCRIBE_TEST=1")

			var stdout, stderr bytes.Buffer

			command.Stdout, command.Stderr = &stdout, &stderr

			err := command.Run()
			if invalid {
				if err == nil || stdout.Len() != 0 || !strings.Contains(stderr.String(), "slice") {
					t.Fatalf("invalid configuration accepted: stdout=%s stderr=%s err=%v", &stdout, &stderr, err)
				}

				return
			}

			if err != nil || stderr.Len() != 0 {
				t.Fatalf("description initialized resources: stderr=%s err=%v", &stderr, err)
			}

			decoder := json.NewDecoder(&stdout)

			var got clientDescription
			if err := decoder.Decode(&got); err != nil {
				t.Fatal(err)
			}

			if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
				t.Fatalf("expected one JSON object, got %v", err)
			}

			if got.Live || got.Settings.Mode != modePull || got.Settings.Endpoint != "https://127.0.0.1:1/data" || !got.Features["output"] {
				t.Fatalf("description lost normalization or redaction: %+v", got)
			}
		})
	}

	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("description created output/report files: %v, %v", entries, err)
	}
}
