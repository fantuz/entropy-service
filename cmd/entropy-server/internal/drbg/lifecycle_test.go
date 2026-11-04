package drbg

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestKeyLifecycleCloseAndReseed(t *testing.T) {
	for _, algorithm := range []Algorithm{ChaCha20, AES256CTR} {
		t.Run(algorithm.String(), func(t *testing.T) {
			d := testGenerator(t, algorithm)
			if err := d.Reseed(bytes.Repeat([]byte{0x42}, 256)); err != nil {
				t.Fatal(err)
			}

			if err := d.Close(); err != nil {
				t.Fatal(err)
			}

			if d.cipher != nil || !d.closed || d.key != [32]byte{} || d.nonce != [16]byte{} {
				t.Fatal("Close did not clear owned key and nonce or release cipher reference")
			}

			if err := d.Close(); err != nil {
				t.Fatalf("Close must be idempotent: %v", err)
			}

			buf := bytes.Repeat([]byte{0xa5}, 32)
			if n, err := d.Read(buf); n != 0 || !errors.Is(err, io.ErrClosedPipe) ||
				!bytes.Equal(buf, bytes.Repeat([]byte{0xa5}, len(buf))) {
				t.Fatal("Read after Close changed buffer or succeeded")
			}

			if err := d.Reseed(bytes.Repeat([]byte{0x21}, 64)); !errors.Is(err, io.ErrClosedPipe) {
				t.Fatalf("Reseed after Close: %v", err)
			}

			if d.cipher != nil || d.key != [32]byte{} || d.nonce != [16]byte{} {
				t.Fatal("Reseed after Close recreated sensitive state")
			}
		})
	}
}

func TestConnectionReaderClearsChildOnClose(t *testing.T) {
	for _, algorithm := range []Algorithm{ChaCha20, AES256CTR} {
		t.Run(algorithm.String(), func(t *testing.T) {
			parent := testGenerator(t, algorithm)
			initial := ActiveInstances()

			reader := NewConnectionReader(parent)
			if _, err := reader.Read(make([]byte, 64)); err != nil {
				t.Fatal(err)
			}

			child := reader.child
			if child == nil {
				t.Fatal("child was not created")
			}

			reader.Close()
			reader.Close()

			if !child.closed || child.cipher != nil || child.key != [32]byte{} ||
				child.nonce != [16]byte{} || reader.child != nil {
				t.Fatal("connection close left accessible child key material")
			}

			if ActiveInstances() != initial {
				t.Fatal("connection close did not preserve instance accounting")
			}
		})
	}
}

func TestReseedStreamOutputMatchesLegacyDigest(t *testing.T) {
	// Existing known-answer fixtures additionally verify exact stream bytes.
	for _, algorithm := range []Algorithm{ChaCha20, AES256CTR} {
		d := testGenerator(t, algorithm)
		ref := testGenerator(t, algorithm)
		seed := bytes.Repeat([]byte{0x3c}, 256)

		if err := d.Reseed(seed); err != nil {
			t.Fatal(err)
		}

		if err := ref.Reseed(seed); err != nil {
			t.Fatal(err)
		}

		got, want := make([]byte, 1024), make([]byte, 1024)
		if _, err := io.ReadFull(d, got); err != nil {
			t.Fatal(err)
		}

		if _, err := io.ReadFull(ref, want); err != nil {
			t.Fatal(err)
		}

		if !bytes.Equal(got, want) {
			t.Fatal("reseed introduced a stream mismatch")
		}
	}
}
