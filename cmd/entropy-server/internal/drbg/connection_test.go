package drbg

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

func TestConnectionReaderStreamAndReseed(t *testing.T) {
	for _, algorithm := range []Algorithm{ChaCha20, AES256CTR} {
		t.Run(algorithm.String(), func(t *testing.T) {
			parent, reference := testGenerator(t, algorithm), testGenerator(t, algorithm)

			reader := NewConnectionReader(parent)
			defer reader.Close()

			child, err := NewConnectionDRBG(reference)
			if err != nil {
				t.Fatal(err)
			}

			for step := range 3 {
				if step == 2 {
					seed := bytes.Repeat([]byte{0x5a}, 256)
					for _, d := range []*DRBG{parent, reference} {
						if err := d.Reseed(seed); err != nil {
							t.Fatal(err)
						}
					}

					derived, err := reference.Derive(64)
					if err != nil {
						t.Fatal(err)
					}

					if err := child.Reseed(derived); err != nil {
						t.Fatal(err)
					}
				}

				got, want := make([]byte, 4096), make([]byte, 4096)
				if _, err := io.ReadFull(reader, got); err != nil {
					t.Fatal(err)
				}

				if _, err := io.ReadFull(&child, want); err != nil {
					t.Fatal(err)
				}

				if !bytes.Equal(got, want) || reader.child.Algorithm() != algorithm {
					t.Fatalf("private stream/backend differs at step %d", step)
				}

				if parent.generated != 64 {
					t.Fatal("parent must supply only one 64-byte seed per epoch")
				}
			}
		})
	}
}

func TestConnectionReaderBudgetFailureAndCleanup(t *testing.T) {
	for _, algorithm := range []Algorithm{ChaCha20, AES256CTR} {
		t.Run(algorithm.String(), func(t *testing.T) {
			parent := testGenerator(t, algorithm)
			instances := ActiveInstances()

			reader := NewConnectionReader(parent)
			defer reader.Close()

			if _, err := reader.Read(nil); err != nil || ActiveInstances() != instances || parent.generated != 0 {
				t.Fatal("empty/idle reads must not create or seed a generator")
			}

			buf := make([]byte, 8)
			if _, err := io.ReadFull(reader, buf); err != nil {
				t.Fatal(err)
			}

			reader.child.generated = maxBytesPerKey - 4
			if _, err := io.ReadFull(reader, buf); err != nil || reader.child.generated != 8 || parent.generated != 128 {
				t.Fatalf("byte budget did not trigger rekey: %v", err)
			}

			// A failed refresh must not continue using the old child's stream.
			reader.child.generated = maxBytesPerKey - 4
			parent.generated = maxBytesPerKey

			copy(buf, bytes.Repeat([]byte{0xa5}, len(buf)))

			if n, err := reader.Read(buf); n != 0 || err == nil || !bytes.Equal(buf, bytes.Repeat([]byte{0xa5}, len(buf))) {
				t.Fatal("failed parent derivation must leave output untouched")
			}

			if err := parent.Reseed(bytes.Repeat([]byte{0x5a}, 64)); err != nil {
				t.Fatal(err)
			}

			if _, err := io.ReadFull(reader, buf); err != nil {
				t.Fatal(err)
			}

			if ActiveInstances() != instances+1 {
				t.Fatal("rekeys must reuse the child instance")
			}

			reader.Close()
			reader.Close()

			if ActiveInstances() != instances {
				t.Fatal("close must release exactly one child instance")
			}

			if _, err := reader.Read(buf); !errors.Is(err, io.ErrClosedPipe) {
				t.Fatalf("read after close: %v", err)
			}
		})
	}
}

func TestConnectionReaderBulkDoesNotLockParent(t *testing.T) {
	parent := testGenerator(t, ChaCha20)

	reader := NewConnectionReader(parent)
	defer reader.Close()

	if _, err := reader.Read(make([]byte, 1)); err != nil {
		t.Fatal(err)
	}

	parent.mu.Lock()

	done := make(chan error, 1)
	go func() {
		_, err := reader.Read(make([]byte, 1<<20))
		done <- err
	}()

	select {
	case err := <-done:
		parent.mu.Unlock()

		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		parent.mu.Unlock()
		<-done
		t.Fatal("bulk generation waited for the parent's mutex")
	}
}

func TestConnectionReaderConcurrentReseeds(t *testing.T) {
	for _, algorithm := range []Algorithm{ChaCha20, AES256CTR} {
		t.Run(algorithm.String(), func(t *testing.T) {
			parent := testGenerator(t, algorithm)
			instances := ActiveInstances()

			var wg sync.WaitGroup
			for range 8 {
				wg.Add(1)
				go func() {
					defer wg.Done()

					reader := NewConnectionReader(parent)
					defer reader.Close()

					buf := make([]byte, 65536)
					for range 64 {
						if _, err := io.ReadFull(reader, buf); err != nil {
							t.Error(err)
							return
						}
					}
				}()
			}

			for i := range 64 {
				if err := parent.Reseed(bytes.Repeat([]byte{byte(i)}, 64)); err != nil {
					t.Error(err)
				}
			}

			wg.Wait()

			if ActiveInstances() != instances {
				t.Fatal("concurrent connections leaked child instances")
			}
		})
	}
}
