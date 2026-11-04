package drbg

import (
	"bytes"
	"crypto/sha512"
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

// The concatenation used before lifecycle hardening and the streaming
// SHA-512 path must derive exactly the same key and nonce on both backends.
func TestReseedKeepsLegacySHA512StateTransition(t *testing.T) {
	for _, algorithm := range []Algorithm{ChaCha20, AES256CTR} {
		t.Run(algorithm.String(), func(t *testing.T) {
			d := testGenerator(t, algorithm)
			seed := bytes.Repeat([]byte{0x3c}, 256)

			var oldInput [32 + 256]byte
			copy(oldInput[:32], d.key[:])
			copy(oldInput[32:], seed)

			digest := sha512.Sum512(oldInput[:])
			clear(oldInput[:])
			defer clear(digest[:])

			if err := d.Reseed(seed); err != nil {
				t.Fatal(err)
			}

			if !bytes.Equal(d.key[:], digest[:32]) || !bytes.Equal(d.nonce[:], digest[32:48]) {
				t.Fatal("reseed changed the existing SHA-512 old-key plus seed transition")
			}

			if !bytes.Equal(seed, bytes.Repeat([]byte{0x3c}, 256)) {
				t.Fatal("Reseed modified input owned by the caller")
			}
		})
	}
}
