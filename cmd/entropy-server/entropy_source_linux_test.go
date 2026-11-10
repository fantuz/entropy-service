//go:build linux

package main

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestNonpollableEntropyDeviceRejected(t *testing.T) {
	source, err := openCharacterEntropySource("/dev/zero")
	if err == nil {
		_ = source.Close()

		t.Fatal("unsupported device accepted without read deadlines")
	}

	if !strings.Contains(err.Error(), "read deadlines") {
		t.Fatal(err)
	}
}

func TestLinuxOSSeedSourcesRemainUsable(t *testing.T) {
	for _, name := range []string{DefaultCSPRNG, sourceOSFallback} {
		t.Run(name, func(t *testing.T) {
			source, err := openCharacterEntropySource(name)
			if name == sourceOSFallback {
				source, err = openOSSeedSource()
			}

			if err != nil {
				t.Fatal(err)
			}

			defer source.Close()

			deadlineSource, ok := source.(seedDeadlineSource)
			if !ok {
				t.Fatal("source lacks deadline contract")
			}

			input := seedInput{source: deadlineSource, reader: source, timeout: time.Second}
			if n, err := input.Reader(t.Context()).Read(make([]byte, 64)); err != nil || n != 64 {
				t.Fatalf("n=%d err=%v", n, err)
			}
		})
	}
}

func TestOSSeedReadinessRefusesBootWaitAndUnsupportedSyscall(t *testing.T) {
	for _, tc := range []struct {
		name             string
		n                int
		readErr, wantErr error
	}{
		{"ready", 1, nil, nil}, {"initializing", 0, unix.EAGAIN, unix.EAGAIN}, {"unsupported", 0, unix.ENOSYS, unix.ENOSYS}, {"short", 0, nil, io.ErrUnexpectedEOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkOSSeedReady(func(p []byte, flags int) (int, error) {
				if len(p) != 1 || flags != unix.GRND_NONBLOCK {
					t.Fatal("readiness probe must be bounded and nonblocking")
				}

				p[0] = 0xff

				return tc.n, tc.readErr
			})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("readiness %v", err)
			}
		})
	}
}
