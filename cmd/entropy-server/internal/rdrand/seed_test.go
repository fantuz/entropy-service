package rdrand

import (
	"bytes"
	"errors"
	"io"
	"os"
	"testing"
)

func TestSeedUint64(t *testing.T) {
	value, err := SeedUint64()
	if !RDSEEDAvailable() {
		if value != 0 || !errors.Is(err, ErrSeedUnsupported) {
			t.Fatalf("unsupported CPU/build: got (%d, %v)", value, err)
		}

		return
	}

	// Disposable test-binary copies can force assembly carry-flag outcomes.
	switch os.Getenv("ENTROPY_RDSEED_FAULT") {
	case "unavailable":
		if value != 0 || !errors.Is(err, ErrSeedUnavailable) {
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
		if err != nil && !errors.Is(err, ErrSeedUnavailable) {
			t.Fatal(err)
		}
	}

	t.Logf("RDSEED available: %t; sample error: %v", RDSEEDAvailable(), err)
}

func TestSeedOrRandom(t *testing.T) {
	for _, tc := range []struct {
		name        string
		seed        uint64
		seedErr     error
		random      uint64
		randomErr   error
		want        uint64
		wantCalls   int
		wantFailure bool
	}{
		{name: "prefer seed", seed: 11, random: 22, want: 11},
		{name: "accept seed zero", random: 22},
		{name: "unsupported seed", seedErr: ErrSeedUnsupported, random: 22, want: 22, wantCalls: 1},
		{name: "exhausted seed", seed: 99, seedErr: ErrSeedUnavailable, random: 22, want: 22, wantCalls: 1},
		{name: "accept random zero", seedErr: ErrSeedUnavailable, wantCalls: 1},
		{name: "both exhausted", seed: 99, seedErr: ErrSeedUnavailable, random: 88, randomErr: ErrUnavailable, wantCalls: 1, wantFailure: true},
		{name: "both unsupported", seedErr: ErrSeedUnsupported, randomErr: ErrUnsupported, wantCalls: 1, wantFailure: true},
		{name: "exhausted seed only", seedErr: ErrSeedUnavailable, randomErr: ErrUnsupported, wantCalls: 1, wantFailure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0

			value, err := seedOrRandom(
				func() (uint64, error) { return tc.seed, tc.seedErr },
				func() (uint64, error) {
					calls++

					return tc.random, tc.randomErr
				},
			)
			if value != tc.want || calls != tc.wantCalls || (err != nil) != tc.wantFailure {
				t.Fatalf("got (%d, %v), %d fallback calls; want %d, failure=%t, calls=%d", value, err, calls, tc.want, tc.wantFailure, tc.wantCalls)
			}

			if tc.wantFailure && (!errors.Is(err, tc.seedErr) || !errors.Is(err, tc.randomErr)) {
				t.Fatalf("combined error lost a source failure: %v", err)
			}
		})
	}
}

func TestCPUUint64(t *testing.T) {
	value, err := cpuUint64()

	switch os.Getenv("ENTROPY_CPU_FAULT") {
	case "seed":
		if value != 0 || err != nil {
			t.Fatalf("CPU selection must accept the simulated seed zero: got (%d, %v)", value, err)
		}
	case "random":
		if value != 22 || err != nil {
			t.Fatalf("CPU selection must use the simulated RDRAND fallback: got (%d, %v)", value, err)
		}
	case "unavailable":
		if value != 0 || !errors.Is(err, ErrSeedUnavailable) || !errors.Is(err, ErrUnavailable) {
			t.Fatalf("both exhausted budgets must be reported: got (%d, %v)", value, err)
		}
	default:
		if !RDSEEDAvailable() && !Available() {
			if value != 0 || !errors.Is(err, ErrSeedUnsupported) || !errors.Is(err, ErrUnsupported) {
				t.Fatalf("both unsupported instructions must be reported: got (%d, %v)", value, err)
			}
		}
	}
}

func TestNewCPUMixer(t *testing.T) {
	var warnings int

	want := bytes.Repeat([]byte{0xa5}, 31)
	mixer := NewCPUMixer(bytes.NewReader(want), func(error) { warnings++ })
	buf := make([]byte, 64)

	n, err := io.ReadFull(mixer, buf)
	if n != len(want) || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("source error must survive CPU mixing: got (%d, %v)", n, err)
	}

	if !RDSEEDAvailable() && !Available() {
		if !bytes.Equal(buf[:n], want) || warnings != 1 {
			t.Fatalf("unsupported CPUs must retain source bytes and warn once: bytes=%x warnings=%d", buf[:n], warnings)
		}
	}

	if !bytes.Equal(buf[n:], make([]byte, len(buf)-n)) {
		t.Fatal("CPU mixing changed bytes beyond the source read")
	}
}
