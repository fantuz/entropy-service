package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/config"
	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/drbg"
)

func TestServerDescriptionOmitsPrivateConfiguration(t *testing.T) {
	cfg := &config.Config{
		DRBG: string(drbg.ChaCha20), HTTPAddr: "127.0.0.1:8080", HTTPSAddr: "127.0.0.1:8443",
		DevicePath: "/dev/urandom", EnableHTTPS: true, RequireDevice: true,
		ReseedMs: 2000, MaxReseedAgeMs: 8000, ReseedSize: 256, MaxBytes: 4096,
		SourceTimeout: time.Second, OnReseedFailure: drbg.ReseedStop,
		CertFile: "private-certificate-path", KeyFile: "private-key-path", LabSources: "private-registry-path",
		LabMaxConnections: 8, LabMemoryMiB: 32, WSMaxConnections: 128, WSMemoryMiB: 1024,
		HTTPMaxRequests: 128, HTTPMemoryMiB: 512,
	}

	var out bytes.Buffer
	if err := writeServerDescription(&out, cfg); err != nil {
		t.Fatal(err)
	}

	for _, private := range []string{cfg.CertFile, cfg.KeyFile, cfg.LabSources} {
		if strings.Contains(out.String(), private) {
			t.Fatalf("description disclosed private configuration %q", private)
		}
	}

	var got serverDescription
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}

	if got.SchemaVersion != 1 || got.Type != "configuration" || got.Role != "server" || got.Live {
		t.Fatalf("invalid configured-only descriptor: %+v", got)
	}

	if got.Settings.DRBG != cfg.DRBG || got.Settings.MaxBytes != cfg.MaxBytes || !got.Features.HTTPS || !got.Features.LabRegistry || !got.Features.CertificateConfigured {
		t.Fatalf("description does not reflect supplied configuration: %+v", got)
	}

	if got.Features.ExternalUILocales != externalUILocales {
		t.Fatal("descriptor must report its catalog build mode without inspecting installed files")
	}

	if !maps.Equal(got.Backends["drbg"], drbg.CompiledAlgorithms()) {
		t.Fatalf("wrong generator inventory: %v", got.Backends)
	}

	if err := writeServerDescription(failingDescriptionWriter{}, cfg); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("writer error was not preserved: %v", err)
	}
}

type failingDescriptionWriter struct{}

func (failingDescriptionWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestServerDescribeJSONProcess(t *testing.T) {
	if os.Getenv("ENTROPY_SERVER_DESCRIBE_TEST") == "1" {
		separator := slices.Index(os.Args, "--")
		os.Args = append([]string{"entropy-server"}, os.Args[separator+1:]...)

		main()
		os.Exit(0)
	}

	if runtime.GOOS != "linux" {
		t.Skip("the Linux desktop PoC inspects Linux-only seed device configuration")
	}

	directory := t.TempDir()

	for _, invalid := range []bool{false, true} {
		name := "missing-resources"
		if invalid {
			name = "invalid-flags"
		}

		t.Run(name, func(t *testing.T) {
			args := []string{
				"-test.run=^TestServerDescribeJSONProcess$", "--", "-describe-json",
				"-require-device=true", "-device=" + filepath.Join(directory, "missing-device"),
				"-cert-file=" + filepath.Join(directory, "missing-cert"),
				"-key-file=" + filepath.Join(directory, "missing-key"),
				"-lab-sources=" + filepath.Join(directory, "missing-registry"), "-drbg=chacha20",
			}
			if invalid {
				args = append(args, "-max-bytes=0")
			}

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()

			command := exec.CommandContext(ctx, os.Args[0], args...) //nolint:gosec // G204: runs only this test binary with controlled test flags.

			command.Env = append(os.Environ(), "ENTROPY_SERVER_DESCRIBE_TEST=1")

			var stdout, stderr bytes.Buffer

			command.Stdout, command.Stderr = &stdout, &stderr

			err := command.Run()
			if invalid {
				if err == nil || stdout.Len() != 0 || !strings.Contains(stderr.String(), "max-bytes") {
					t.Fatalf("invalid configuration accepted: stdout=%s stderr=%s err=%v", &stdout, &stderr, err)
				}

				return
			}

			if err != nil || stderr.Len() != 0 {
				t.Fatalf("description tried to initialize resources: stderr=%s err=%v", &stderr, err)
			}

			var got serverDescription

			decoder := json.NewDecoder(&stdout)
			if err := decoder.Decode(&got); err != nil {
				t.Fatal(err)
			}

			if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
				t.Fatalf("expected exactly one JSON object, got %v", err)
			}

			if got.Live || got.Settings.DRBG != "chacha20" || got.Settings.MaxReseedAgeMS != 60000 {
				t.Fatalf("invalid description: %+v", got)
			}
		})
	}

	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("configuration inspection changed resource directory: %v, %v", entries, err)
	}
}
