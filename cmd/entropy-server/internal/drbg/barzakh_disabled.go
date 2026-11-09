//go:build !experimental_barzakh

package drbg

import (
	"crypto/aes"
	"crypto/cipher"
	"errors"
)

const barzakhEnabled = false

func newBarzakhStream(_ *[32]byte, _ *[aes.BlockSize]byte) (cipher.Stream, error) {
	return nil, errors.New("Barzakh-521 requires -tags=experimental_barzakh")
}
