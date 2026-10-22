package rdrand

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"testing"
)

func TestUint64(t *testing.T) {
	value, err := Uint64()
	if !Available() {
		if value != 0 || !errors.Is(err, ErrUnsupported) {
			t.Fatalf("unsupported CPU/build: got (%d, %v)", value, err)
		}

		return
	}

	// The external fault-injection harness patches a COPY of the test binary.
	// Production assembly has no test switches or simulation branches.
	switch os.Getenv("ENTROPY_RDRAND_FAULT") {
	case "unavailable":
		if value != 0 || !errors.Is(err, ErrUnavailable) {
			t.Fatalf("exhausted retries: got (%d, %v)", value, err)
		}
	case "zero":
		if value != 0 || err != nil {
			t.Fatalf("successful zero must be accepted: got (%d, %v)", value, err)
		}
	case "last-attempt":
		if err != nil {
			t.Fatalf("success on the final attempt must be accepted: %v", err)
		}
	default:
		if err != nil && !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
	}

	t.Logf("RDRAND available: %t; sample error: %v", Available(), err)
}

func TestUint64Concurrent(t *testing.T) {
	if !Available() {
		t.Skip("RDRAND is unavailable on this CPU/build")
	}

	var (
		wg       sync.WaitGroup
		failures atomic.Int64
	)

	for range 32 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for range 1024 {
				_, err := Uint64()
				if errors.Is(err, ErrUnavailable) {
					failures.Add(1)
				} else if err != nil {
					t.Errorf("unexpected sampling error: %v", err)
				}
			}
		}()
	}

	wg.Wait()
	t.Logf("32,768 samples across 32 goroutines; exhausted budgets: %d", failures.Load())
}

func TestNewMixer(t *testing.T) {
	mixer, err := NewMixer(rand.Reader, nil)
	if !Available() {
		if mixer != nil || !errors.Is(err, ErrUnsupported) {
			t.Fatalf("unsupported mixer: got (%v, %v)", mixer, err)
		}

		return
	}

	if err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 31)
	if n, err := io.ReadFull(mixer, buf); n != len(buf) || err != nil {
		t.Fatalf("real source/mixer read: got (%d, %v)", n, err)
	}
}

func TestMixerTailAndZero(t *testing.T) {
	for _, tc := range []struct {
		name string
		word uint64
		want []byte
	}{
		{
			name: "little-endian tail",
			word: 0x0807060504030201,
			want: []byte{0xfe, 0xfd, 0xfc, 0xfb, 0xfa, 0xf9, 0xf8, 0xf7, 0xfe, 0xfd, 0xee, 0xee},
		},
		{
			name: "successful zero",
			word: 0,
			want: []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xee, 0xee},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mixer := &Mixer{
				source: bytes.NewReader(bytes.Repeat([]byte{0xff}, 10)),
				next:   func() (uint64, error) { return tc.word, nil },
			}
			buf := bytes.Repeat([]byte{0xee}, 12)
			n, err := mixer.Read(buf)

			if n != 10 || err != nil || !bytes.Equal(buf, tc.want) {
				t.Fatalf("tail/guard bytes: got (%x, %d, %v), want %x", buf, n, err, tc.want)
			}
		})
	}
}

func TestMixerHardwareFailure(t *testing.T) {
	var calls, warnings int

	mixer := &Mixer{
		source: bytes.NewReader(bytes.Repeat([]byte{0xff}, 24)),
		next: func() (uint64, error) {
			calls++
			if calls == 1 {
				return 0x0807060504030201, nil
			}

			return 0xdeadbeef, ErrUnavailable // Invalid samples must be ignored.
		},
		onFailure: func(err error) {
			warnings++

			if !errors.Is(err, ErrUnavailable) {
				t.Errorf("unexpected warning: %v", err)
			}
		},
	}

	first := make([]byte, 16)
	if n, err := mixer.Read(first); n != len(first) || err != nil {
		t.Fatalf("hardware failure must preserve source success: got (%d, %v)", n, err)
	}

	want := []byte{0xfe, 0xfd, 0xfc, 0xfb, 0xfa, 0xf9, 0xf8, 0xf7, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	if !bytes.Equal(first, want) {
		t.Fatalf("failed sample altered source bytes: got %x, want %x", first, want)
	}

	second := make([]byte, 8)
	if n, err := mixer.Read(second); n != len(second) || err != nil || !bytes.Equal(second, bytes.Repeat([]byte{0xff}, 8)) {
		t.Fatalf("repeated hardware failure: got (%x, %d, %v)", second, n, err)
	}

	if warnings != 1 || calls != 3 {
		t.Fatalf("got %d warnings and %d sample calls; want 1 and 3", warnings, calls)
	}
}

func TestMixerPreservesSourceErrors(t *testing.T) {
	mixer := &Mixer{
		source: bytes.NewReader(bytes.Repeat([]byte{0xff}, 31)),
		next:   func() (uint64, error) { return 0, nil },
	}
	buf := bytes.Repeat([]byte{0xee}, 64)
	n, err := io.ReadFull(mixer, buf)

	if n != 31 || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("short source: got (%d, %v), want (31, unexpected EOF)", n, err)
	}

	if !bytes.Equal(buf[31:], bytes.Repeat([]byte{0xee}, 33)) {
		t.Fatal("mixer changed bytes the source did not provide")
	}
}
