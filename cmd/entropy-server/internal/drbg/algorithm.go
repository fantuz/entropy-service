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
)

func ParseAlgorithm(name string) (Algorithm, error) {
	algorithm := Algorithm(name)
	switch algorithm {
	case ChaCha20, AES256CTR:
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
	default:
		return string(a)
	}
}

func newCipherStream(algorithm Algorithm, key [32]byte, nonce [aes.BlockSize]byte) (cipher.Stream, error) {
	switch algorithm {
	case ChaCha20:
		return chacha20.NewUnauthenticatedCipher(key[:], nonce[:chacha20.NonceSize])
	case AES256CTR:
		block, err := aes.NewCipher(key[:])
		if err != nil {
			return nil, err
		}

		return cipher.NewCTR(block, nonce[:]), nil
	default:
		return nil, fmt.Errorf("unsupported DRBG algorithm %q", algorithm)
	}
}
