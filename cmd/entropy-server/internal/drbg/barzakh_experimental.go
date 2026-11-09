//go:build experimental_barzakh

// SPDX-License-Identifier: MIT
// Original Go implementation of the published Barzakh-521 scalar construction.
// No upstream Rust implementation is included or linked. See
// docs/experimental-barzakh.md for provenance, mapping and patent limitations.

package drbg

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha3"
	"math/big"
)

const barzakhEnabled = true

// barzakhStream uses variable-time math/big arithmetic. It is a research
// implementation, not a constant-time generator for secrets. Its owner must
// serialize calls; DRBG already does so. Never copy a populated stream.
type barzakhStream struct {
	prime, x, a, b, w, mask big.Int
	denominator, inverse    big.Int
	value                   big.Int
	block                   [32]byte
	position                int
	closed                  bool
}

func newBarzakhStream(key *[32]byte, nonce *[aes.BlockSize]byte) (cipher.Stream, error) {
	s := &barzakhStream{position: 32}
	s.prime.Lsh(big.NewInt(1), 521)
	s.prime.Sub(&s.prime, big.NewInt(1))

	// The existing SHA-512 construction supplies 32 key and 16 nonce bytes.
	// Expand their concatenation in the published v1 order: w, x, a, b, W.
	xof := sha3.NewSHAKE256()
	_, _ = xof.Write(key[:])
	_, _ = xof.Write(nonce[:])

	var field [66]byte
	defer clear(field[:])
	defer xof.Reset()

	for _, value := range []*big.Int{&s.w, &s.x, &s.a, &s.b, &s.mask} {
		_, _ = xof.Read(field[:])
		value.SetBytes(field[:])
		value.Mod(value, &s.prime)
	}

	if s.w.Bit(0) == 0 {
		s.w.Add(&s.w, big.NewInt(1))
		s.w.Mod(&s.w, &s.prime)
	}

	return s, nil
}

func (s *barzakhStream) nextBlock() {
	s.denominator.Sub(&s.x, &s.a)
	s.denominator.Mod(&s.denominator, &s.prime)

	// Match the published v1 singularity rule before computing the numerator.
	if s.denominator.Sign() == 0 {
		s.x.Add(&s.x, &s.mask)
		s.x.Mod(&s.x, &s.prime)
		s.denominator.SetInt64(1)
	}

	if s.inverse.ModInverse(&s.denominator, &s.prime) == nil {
		// A nonzero residue modulo this prime must be invertible.
		panic("Barzakh-521: invalid internal field state")
	}

	s.value.Mul(&s.x, &s.x)
	s.value.Mul(&s.value, &s.a)
	s.value.Add(&s.value, &s.b)
	s.value.Mul(&s.value, &s.inverse)
	s.value.Add(&s.value, &s.w)
	s.value.Mod(&s.value, &s.prime)

	s.x.Xor(&s.mask, &s.value)

	if s.x.Cmp(&s.prime) == 0 {
		s.x.SetInt64(0)
	}

	s.value.Rsh(&s.value, 265)
	s.value.FillBytes(s.block[:])
	s.position = 0
}

// XORKeyStream retains unused output across arbitrary read boundaries. Copying
// the input first also makes overlap safe without unsafe pointer arithmetic.
func (s *barzakhStream) XORKeyStream(dst, src []byte) {
	if s.closed {
		panic("Barzakh-521: use after state clearing")
	}

	if len(dst) < len(src) {
		panic("Barzakh-521: output smaller than input")
	}

	dst = dst[:len(src)]
	copy(dst, src)

	for i := range dst {
		if s.position == len(s.block) {
			s.nextBlock()
		}

		dst[i] ^= s.block[s.position]
		s.block[s.position] = 0
		s.position++
	}
}

func (s *barzakhStream) clear() {
	for _, value := range []*big.Int{&s.prime, &s.x, &s.a, &s.b, &s.w, &s.mask, &s.denominator, &s.inverse, &s.value} {
		words := value.Bits()
		clear(words[:cap(words)])

		*value = big.Int{}
	}

	clear(s.block[:])
	s.position = len(s.block)
	s.closed = true
}
