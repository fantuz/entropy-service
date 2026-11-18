package drbg

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/metrics"
	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/rdrand"
)

// Deterministic fault injection works on ARM and purego too. It never changes
// process-global CPUID flags or executes an unsupported instruction.
func fakeCPURoot(t *testing.T, read func([]byte) (int, error)) *DRBG {
	t.Helper()
	trackInstance()

	d := &DRBG{
		cpu: &cpuBackend{read: read}, algorithm: RDRAND, counted: true,
		algo: RDRAND.String(), Reseeded: time.Now(), failurePolicy: ReseedContinue,
		generationChanged: make(chan struct{}),
	}

	t.Cleanup(func() { _ = d.Close() })

	return d
}

func TestCPUFailureLatchesAcrossChildrenAndReseeds(t *testing.T) {
	for _, policy := range []ReseedFailurePolicy{ReseedStop, ReseedContinue} {
		t.Run(string(policy), func(t *testing.T) {
			var calls atomic.Int32

			root := fakeCPURoot(t, func(p []byte) (int, error) {
				calls.Add(1)
				copy(p, "partial hardware output")

				return 7, rdrand.ErrUnavailable
			})
			if err := root.ConfigureReseedPolicy(policy, time.Minute); err != nil {
				t.Fatal(err)
			}

			changed, _, _ := root.WatchGeneration()

			child, err := NewConnectionDRBG(root)
			if err != nil {
				t.Fatal(err)
			}
			defer child.Close()

			buf := bytes.Repeat([]byte{0xaa}, 32)
			before := metrics.BytesGenerated()

			if n, readErr := child.Read(buf); n != 0 || !errors.Is(readErr, ErrCPUBackendFailed) || !bytes.Equal(buf, make([]byte, len(buf))) {
				t.Fatalf("partial read: %d, %v, %x", n, readErr, buf)
			}

			select {
			case <-changed:
			default:
				t.Fatal("backend failure did not wake policy watcher")
			}

			if meta := root.GetMetadata(); meta.GenerationAllowed || meta.BackendError == "" || meta.BackendFailuresTotal != 1 || meta.ReseedFailuresTotal != 0 || meta.OutputMode != OutputModeDirectCPU {
				t.Fatalf("misreported failure: %+v", meta)
			}

			if err := root.Reseed(testSeed()); err != nil {
				t.Fatal(err)
			}

			for _, d := range []*DRBG{root, &child} {
				if _, err := d.Read(buf); !errors.Is(err, ErrCPUBackendFailed) {
					t.Fatalf("reseed bypassed hardware failure: %v", err)
				}
			}

			if _, err := NewConnectionDRBG(root); !errors.Is(err, ErrCPUBackendFailed) {
				t.Fatalf("new child bypassed hardware failure: %v", err)
			}

			if calls.Load() != 1 || metrics.BytesGenerated() != before {
				t.Fatal("failed output was retried or counted")
			}
		})
	}
}

func TestCPUConnectionUsesDirectWordsAndFreshnessGate(t *testing.T) {
	var calls int

	root := fakeCPURoot(t, func(p []byte) (int, error) { calls++; clear(p); return len(p), nil })
	if err := root.ConfigureReseedPolicy(ReseedStop, time.Minute); err != nil {
		t.Fatal(err)
	}

	before := metrics.BytesGenerated()

	conn := NewConnectionReader(root)
	defer conn.Close()

	for range 2 {
		if n, err := conn.Read(make([]byte, 17)); n != 17 || err != nil {
			t.Fatalf("direct read: %d, %v", n, err)
		}

		if err := root.Reseed(testSeed()); err != nil {
			t.Fatal(err)
		}
	}

	if calls != 2 || metrics.BytesGenerated()-before != 34 {
		t.Fatal("direct CPU mode derived unused software seeds")
	}

	root.RecordReseedFailure(io.ErrUnexpectedEOF)

	if _, err := conn.Read(make([]byte, 1)); !errors.Is(err, ErrSeedUnavailable) {
		t.Fatalf("source policy bypassed: %v", err)
	}

	if calls != 2 {
		t.Fatal("hardware called while policy denied generation")
	}

	if err := root.Reseed(testSeed()); err != nil {
		t.Fatal(err)
	}

	root.generated = maxBytesPerKey
	if _, err := root.Read(make([]byte, 1)); err != nil {
		t.Fatalf("CPU output inherited a software key budget: %v", err)
	}

	conn.Close()

	if _, err := conn.Read(make([]byte, 1)); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal("closed connection remained usable")
	}
}

func TestCPUConcurrentFailureDiscardsInFlightOutput(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})

	var calls atomic.Int32

	root := fakeCPURoot(t, func(p []byte) (int, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
			copy(p, "successful-but-now-invalid")

			return len(p), nil
		}

		return 0, rdrand.ErrUnavailable
	})

	first, err := NewConnectionDRBG(root)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	second, err := NewConnectionDRBG(root)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	var wg sync.WaitGroup
	wg.Go(func() {
		buf := make([]byte, 32)
		if n, readErr := first.Read(buf); n != 0 || !errors.Is(readErr, ErrCPUBackendFailed) || !bytes.Equal(buf, make([]byte, len(buf))) {
			t.Errorf("in-flight output escaped: %d, %v, %x", n, readErr, buf)
		}
	})
	<-entered
	// Inject a real failed fill while the first Read holds the root policy
	// lock; its final check must discard the previously successful data.
	if err := second.cpu.fill(make([]byte, 32)); !errors.Is(err, ErrCPUBackendFailed) {
		t.Errorf("expected hardware failure: %v", err)
	}

	close(release)
	wg.Wait()
}

func TestCPUShortSuccessIsFailureAndCloseStopsReads(t *testing.T) {
	root := fakeCPURoot(t, func(p []byte) (int, error) {
		copy(p, "incomplete")
		return 1, nil
	})

	buf := bytes.Repeat([]byte{0xff}, 17)
	if n, err := root.Read(buf); n != 0 || !errors.Is(err, ErrCPUBackendFailed) || !bytes.Equal(buf, make([]byte, len(buf))) {
		t.Fatalf("short success escaped: %d, %v, %x", n, err, buf)
	}

	if err := root.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := root.Read(buf); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("closed CPU generator: %v", err)
	}
}

func TestCPUSelectionAndMetadata(t *testing.T) {
	algorithm, err := ParseAlgorithm(string(RDRAND))
	if !rdrand.Available() {
		if !errors.Is(err, rdrand.ErrUnsupported) {
			t.Fatalf("unsupported backend accepted: %v", err)
		}

		return
	}

	if err != nil || algorithm != RDRAND || !algorithm.Experimental() {
		t.Fatalf("selection: %q, %v", algorithm, err)
	}

	d, err := NewDRBGWithAlgorithm(nil, algorithm)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	if d.cipher != nil || d.key != [32]byte{} || d.nonce != [16]byte{} {
		t.Fatal("CPU backend retained an unused software key")
	}

	if meta := d.GetMetadata(); meta.OutputMode != OutputModeDirectCPU || meta.DRBG != RDRAND.String() || !meta.GenerationAllowed {
		t.Fatalf("metadata: %+v", meta)
	}

	if n, err := d.Read(make([]byte, 65)); n != 65 || err != nil {
		t.Fatalf("native CPU output: %d, %v", n, err)
	}
}

func TestCPUChildCannotBypassClosedRoot(t *testing.T) {
	root := fakeCPURoot(t, func(p []byte) (int, error) { clear(p); return len(p), nil })

	child, err := NewConnectionDRBG(root)
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()

	if err := root.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := NewConnectionDRBG(&child); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("new child bypassed closed serving root: %v", err)
	}

	if _, err := child.Read(make([]byte, 8)); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("child bypassed closed serving root: %v", err)
	}
}
