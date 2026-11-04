package drbg

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"testing"
)

func TestDeriveRejectsInvalidSizeWithoutConsumingStream(t *testing.T) {
	for _, algorithm := range []Algorithm{ChaCha20, AES256CTR} {
		t.Run(algorithm.String(), func(t *testing.T) {
			d := testGenerator(t, algorithm)
			reference := testGenerator(t, algorithm)

			for _, size := range []int{-1, 0, maxDeriveBytes + 1, int(^uint(0) >> 1)} {
				got, err := d.Derive(size)
				if err == nil || got != nil {
					t.Fatalf("invalid derivation %d: got %d bytes, err=%v", size, len(got), err)
				}
			}

			if d.generated != 0 {
				t.Fatalf("invalid Derive advanced output counter by %d", d.generated)
			}

			got, err := d.Derive(64)
			if err != nil {
				t.Fatal(err)
			}

			want := make([]byte, 64)
			if _, err := io.ReadFull(reference, want); err != nil {
				t.Fatal(err)
			}

			if !bytes.Equal(got, want) {
				t.Fatal("invalid Derive changed the generator stream")
			}

			if _, err := d.Derive(maxDeriveBytes); err != nil {
				t.Fatalf("maximum supported derivation rejected: %v", err)
			}
		})
	}
}

func TestDeriveAfterCloseAndOnExhaustedGenerator(t *testing.T) {
	for _, algorithm := range []Algorithm{ChaCha20, AES256CTR} {
		t.Run(algorithm.String(), func(t *testing.T) {
			d := testGenerator(t, algorithm)
			d.generated = maxBytesPerKey

			if result, err := d.Derive(64); err == nil || result != nil {
				t.Fatal("exhausted generator returned derived bytes")
			}

			if err := d.Close(); err != nil {
				t.Fatal(err)
			}

			if result, err := d.Derive(64); !errors.Is(err, io.ErrClosedPipe) || result != nil {
				t.Fatalf("derive after close: %v", err)
			}
		})
	}
}

func TestCloseCountsExactlyOnceUnderConcurrency(t *testing.T) {
	for _, algorithm := range []Algorithm{ChaCha20, AES256CTR} {
		t.Run(algorithm.String(), func(t *testing.T) {
			baseline := ActiveInstances()

			d, err := NewDRBGWithAlgorithm(testSeed(), algorithm)
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = d.Close() })

			if ActiveInstances() != baseline+1 {
				t.Fatal("construction did not register one active instance")
			}

			var wg sync.WaitGroup

			for range 32 {
				wg.Add(1)

				go func() {
					defer wg.Done()

					if err := d.Close(); err != nil {
						t.Error(err)
					}
				}()
			}

			wg.Wait()

			if got := ActiveInstances(); got != baseline {
				t.Fatalf("repeated concurrent Close left %d active instances; want %d", got, baseline)
			}

			if err := d.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFailedChildConstructionLeavesInstanceCountUnchanged(t *testing.T) {
	for _, algorithm := range []Algorithm{ChaCha20, AES256CTR} {
		t.Run(algorithm.String(), func(t *testing.T) {
			parent := testGenerator(t, algorithm)
			baseline := ActiveInstances()

			if _, err := NewConnectionDRBG(nil); err == nil {
				t.Fatal("accepted nil parent")
			}

			if err := parent.Close(); err != nil {
				t.Fatal(err)
			}

			if _, err := NewConnectionDRBG(parent); !errors.Is(err, io.ErrClosedPipe) {
				t.Fatalf("unexpected failed child creation error: %v", err)
			}

			if got := ActiveInstances(); got != baseline-1 {
				t.Fatalf("failed child creation leaked accounting: got %d, want %d", got, baseline-1)
			}
		})
	}
}

func TestConnectionReaderFailedParentDerivationReleasesPriorChild(t *testing.T) {
	parent := testGenerator(t, ChaCha20)
	baseline := ActiveInstances()
	reader := NewConnectionReader(parent)

	if _, err := reader.Read(make([]byte, 64)); err != nil {
		t.Fatal(err)
	}

	if ActiveInstances() != baseline+1 {
		t.Fatal("connection did not own a private child")
	}

	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := reader.Read(make([]byte, 64)); err != nil {
		// Existing private child is allowed to finish its current epoch.
		// Force refresh below to check the parent error path.
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("unexpected read error after parent close: %v", err)
		}
	}

	reader.child.generated = maxBytesPerKey
	if _, err := reader.Read(make([]byte, 64)); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("expected failed parent re-derivation: %v", err)
	}

	reader.Close()
	reader.Close()

	if got := ActiveInstances(); got != baseline-1 {
		t.Fatalf("connection child not released after error: got %d, want %d", got, baseline-1)
	}
}
