package drbg

import (
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"fmt"

	"golang.org/x/crypto/chacha20"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/rdrand"
)

// Algorithm selects a seeded software stream or explicit direct CPU output.
type Algorithm string

const OutputModeDirectCPU = "direct-cpu"

const (
	ChaCha20  Algorithm = "chacha20"
	AES256CTR Algorithm = "aes-256-ctr"
	// RDRAND is direct CPU output, not an application-seeded DRBG.
	RDRAND Algorithm = "rdrand-experimental"
	// Barzakh521 is available only with the experimental_barzakh build tag.
	Barzakh521 Algorithm = "barzakh-521-experimental"
)

// CompiledAlgorithms lists the compiled backends and their host availability.
// CPU feature detection uses CPUID. No generator or seed source is opened.
func CompiledAlgorithms() map[string]bool {
	backends := map[string]bool{string(AES256CTR): true, string(ChaCha20): true}
	if barzakhEnabled {
		backends[string(Barzakh521)] = true
	}

	if rdrand.Compiled {
		backends[string(RDRAND)] = rdrand.Available()
	}

	return backends
}

func ParseAlgorithm(name string) (Algorithm, error) {
	algorithm := Algorithm(name)
	switch algorithm {
	case ChaCha20, AES256CTR:
		return algorithm, nil
	case RDRAND:
		if !rdrand.Available() {
			return "", fmt.Errorf("backend %q requires native amd64 with CPU RDRAND support (unavailable in purego builds): %w", name, rdrand.ErrUnsupported)
		}

		return algorithm, nil
	case Barzakh521:
		if !barzakhEnabled {
			return "", fmt.Errorf("DRBG %q requires a build with -tags=experimental_barzakh", name)
		}

		return algorithm, nil
	default:
		return "", fmt.Errorf("unsupported DRBG %q: use chacha20, aes-256-ctr or rdrand-experimental", name)
	}
}

func (a Algorithm) String() string {
	switch a {
	case ChaCha20:
		return "ChaCha20"
	case AES256CTR:
		return "AES-256-CTR"
	case Barzakh521:
		return "Barzakh-521 (experimental)"
	case RDRAND:
		return "RDRAND (experimental; direct CPU)"
	default:
		return string(a)
	}
}

// Experimental reports whether this backend is intended only for research.
func (a Algorithm) Experimental() bool {
	return a == Barzakh521 || a == RDRAND
}

func newCipherStream(algorithm Algorithm, key *[32]byte, nonce *[aes.BlockSize]byte) (cipher.Stream, error) {
	switch algorithm {
	case ChaCha20:
		return chacha20.NewUnauthenticatedCipher(key[:], nonce[:chacha20.NonceSize])
	case AES256CTR:
		block, err := aes.NewCipher(key[:])
		if err != nil {
			return nil, err
		}

		return cipher.NewCTR(block, nonce[:]), nil
	case Barzakh521:
		return newBarzakhStream(key, nonce)
	case RDRAND:
		return nil, errors.New("direct CPU output does not use a cipher stream")
	default:
		return nil, fmt.Errorf("unsupported DRBG algorithm %q", algorithm)
	}
}

// clearStreamState erases state owned by our experimental implementation.
// Opaque standard-library/x/crypto cipher state remains outside our control.
func clearStreamState(stream cipher.Stream) {
	if state, ok := stream.(interface{ clear() }); ok {
		state.clear()
	}
}

// OutputMode distinguishes deterministic expansion from direct CPU words.
func (a Algorithm) OutputMode() string {
	if a == RDRAND {
		return OutputModeDirectCPU
	}

	return "seeded-stream"
}
