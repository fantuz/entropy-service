//go:build linux

package main

import (
	"strings"
	"testing"
	"time"
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
