package drbg

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"

	"golang.org/x/crypto/chacha20"
)

// Algorithm selects the stream used by the existing seed/reseed construction.
type Algorithm string

const (
	ChaCha20  Algorithm = "chacha20"
	AES256CTR Algorithm = "aes-256-ctr"
	// Barzakh521 is available only with the experimental_barzakh build tag.
	Barzakh521 Algorithm = "barzakh-521-experimental"
)

func ParseAlgorithm(name string) (Algorithm, error) {
	algorithm := Algorithm(name)
	switch algorithm {
	case ChaCha20, AES256CTR:
		return algorithm, nil
	case Barzakh521:
		if !barzakhEnabled {
			return "", fmt.Errorf("DRBG %q requires a build with -tags=experimental_barzakh", name)
		}

		return algorithm, nil
	default:
		return "", fmt.Errorf("unsupported DRBG %q: use chacha20 or aes-256-ctr", name)
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
	default:
		return string(a)
	}
}

// Experimental reports whether this backend is intended only for research.
func (a Algorithm) Experimental() bool {
	return a == Barzakh521
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
