package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/drbg"
)

type trackedEntropySource struct {
	closed bool
}

func (s *trackedEntropySource) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func (s *trackedEntropySource) Close() error {
	s.closed = true
	return nil
}

func TestSelectEntropySourceStrictAndFallback(t *testing.T) {
	const configured = "/dev/qrandom0"

	for _, tc := range []struct {
		name       string
		path       string
		require    bool
		fail       map[string]bool
		wantOrigin string
		wantCalls  []string
		wantError  bool
	}{
		{name: "strict named device", path: configured, require: true, wantOrigin: configured, wantCalls: []string{configured}},
		{name: "strict missing device", path: configured, require: true, fail: map[string]bool{configured: true}, wantCalls: []string{configured}, wantError: true},
		{name: "strict empty device", require: true, wantError: true},
		{name: "optional named device", path: configured, wantOrigin: configured, wantCalls: []string{configured}},
		{name: "fallback to urandom", path: configured, fail: map[string]bool{configured: true}, wantOrigin: DefaultCSPRNG, wantCalls: []string{configured, DefaultCSPRNG}},
		{name: "fallback to crypto rand", path: configured, fail: map[string]bool{configured: true, DefaultCSPRNG: true}, wantOrigin: sourceGoCryptoRand, wantCalls: []string{configured, DefaultCSPRNG}},
		{name: "explicit urandom", path: DefaultCSPRNG, wantOrigin: DefaultCSPRNG, wantCalls: []string{DefaultCSPRNG}},
		{name: "empty optional chooses urandom", wantOrigin: DefaultCSPRNG, wantCalls: []string{DefaultCSPRNG}},
		{name: "urandom open failure", path: DefaultCSPRNG, fail: map[string]bool{DefaultCSPRNG: true}, wantOrigin: sourceGoCryptoRand, wantCalls: []string{DefaultCSPRNG}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string

			var opened []*trackedEntropySource

			opener := func(path string) (io.ReadCloser, error) {
				calls = append(calls, path)

				if tc.fail[path] {
					return nil, os.ErrPermission
				}

				reader := &trackedEntropySource{}
				opened = append(opened, reader)

				return reader, nil
			}

			reader, origin, err := selectEntropySource(tc.path, tc.require, opener)
			if (err != nil) != tc.wantError {
				t.Fatalf("unexpected source error: origin=%q err=%v", origin, err)
			}

			if origin != tc.wantOrigin {
				t.Fatalf("source origin = %q, want %q", origin, tc.wantOrigin)
			}

			if len(calls) != len(tc.wantCalls) {
				t.Fatalf("open calls: got %v, want %v", calls, tc.wantCalls)
			}

			for i := range calls {
				if calls[i] != tc.wantCalls[i] {
					t.Fatalf("open calls: got %v, want %v", calls, tc.wantCalls)
				}
			}

			if err != nil {
				return
			}

			if reader == nil {
				t.Fatal("successful source selection returned nil reader")
			}

			d, err := drbg.NewDRBG(make([]byte, 64))
			if err != nil {
				t.Fatal(err)
			}

			defer func() { _ = d.Close() }()

			d.SetMetadata("test", origin, "ignored", 0, 256)

			if meta := d.GetMetadata(); meta.Source != tc.wantOrigin {
				t.Fatalf("reported root source %q does not match reader %q", meta.Source, origin)
			}

			if err := reader.Close(); err != nil {
				t.Fatal(err)
			}

			for _, openedReader := range opened {
				if !openedReader.closed {
					t.Fatal("open device was not closed")
				}
			}
		})
	}
}

func TestOpenCharacterEntropySourceRejectsRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "looks-like-device")

	if err := os.WriteFile(path, make([]byte, 64), 0o600); err != nil {
		t.Fatal(err)
	}

	source, err := openCharacterEntropySource(path)
	if err == nil {
		_ = source.Close()

		t.Fatal("a regular file must not count as an entropy device")
	}

	if source != nil {
		t.Fatal("rejected source must not return an open handle")
	}
}
