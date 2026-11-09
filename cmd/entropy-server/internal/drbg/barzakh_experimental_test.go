//go:build experimental_barzakh

package drbg

import (
	"bytes"
	"io"
	"math/big"
	"testing"
)

// Fixtures were calculated with Python hashlib.shake_256 and arbitrary-precision
// modular inversion, independently of the Go implementation. The direct 48-byte
// fixture is also checked against the pinned upstream scalar v1 generator.
func TestExperimentalBarzakhKnownAnswers(t *testing.T) {
	d := testGenerator(t, Barzakh521)

	for i, want := range []string{
		"1b73d6729dc773fbbe9560f72c8889bb1a20373992cd10fb5d8b7e1f2bcd3a060" +
			"fbb3c890ccf28491e629544706df1f69be4f650b53f707e888c9ff79167bd98c" +
			"69c4a6f58e98a53ce4a7829010ad9602dc2ebc9317af263b8c4fb414ea818aa",
		"79a5b0df55df7ee0f95cd5c6b969b47418832046388b9bb3698c094fb9ea4718" +
			"2c7fcfcbbe117ae46b1fb1cf91316030bb5b17a3ccfab4a8e557837f586ecedd" +
			"89bdb77fbceda864e95cf9ee5681d5a2afa013a0901d544637e0a70f56468eb6",
	} {
		if i > 0 {
			if err := d.Reseed(bytes.Repeat([]byte{0x5a}, 256)); err != nil {
				t.Fatal(err)
			}
		}

		got := make([]byte, 96)
		if _, err := io.ReadFull(d, got); err != nil || !bytes.Equal(got, decodeTestHex(t, want)) {
			t.Fatalf("fixture %d differs: %v", i, err)
		}
	}

	s := testBarzakhStream(t)
	got := make([]byte, 96)
	s.XORKeyStream(got, got)

	want := decodeTestHex(t, "f92194f8dce2a7d6e666db1021d0789c32f309ff5953f7aa8c953efb8f2ea520"+
		"87a36724d8b3d5b3b0166290517f3d7c57103a6170f702ed0bade97974d84604"+
		"3d88d02e77793b1511eff8aa26c0e28439bf2639e39678d3fa9c1d6fe756c957")
	if !bytes.Equal(got, want) {
		t.Fatal("direct scalar stream differs from the independent/reference fixture")
	}
}

func testBarzakhStream(t *testing.T) *barzakhStream {
	t.Helper()

	var (
		key   [32]byte
		nonce [16]byte
	)

	for i := range key {
		key[i] = byte(i)
	}

	for i := range nonce {
		nonce[i] = byte(i + len(key))
	}

	c, err := newBarzakhStream(&key, &nonce)
	if err != nil {
		t.Fatal(err)
	}

	s, ok := c.(*barzakhStream)
	if !ok {
		t.Fatal("unexpected experimental stream type")
	}

	t.Cleanup(s.clear)

	return s
}

func TestExperimentalBarzakhXORAndShortReads(t *testing.T) {
	want := bytes.Repeat([]byte{0xa5}, 1031)
	testBarzakhStream(t).XORKeyStream(want, want)

	for _, size := range []int{1, 7, 31, 32, 33, 255, 256} {
		s := testBarzakhStream(t)
		plain := bytes.Repeat([]byte{0xa5}, len(want))

		got := make([]byte, len(want))
		for offset := 0; offset < len(got); offset += size {
			end := min(offset+size, len(got))
			s.XORKeyStream(got[offset:end], plain[offset:end])
			s.XORKeyStream(nil, nil)
		}

		if !bytes.Equal(got, want) || !bytes.Equal(plain, bytes.Repeat([]byte{0xa5}, len(plain))) {
			t.Fatalf("read size %d changed the stream or separate input", size)
		}
	}

	// Invalid output length must not consume the stream or modify the buffer.
	s := testBarzakhStream(t)

	func() {
		defer func() {
			if recover() == nil {
				t.Error("short output did not panic")
			}
		}()

		s.XORKeyStream(make([]byte, 1), make([]byte, 2))
	}()

	got := bytes.Repeat([]byte{0xa5}, len(want))
	s.XORKeyStream(got, got)

	if !bytes.Equal(got, want) {
		t.Fatal("invalid call advanced the stream")
	}
}

func TestExperimentalBarzakhSingularity(t *testing.T) {
	s := testBarzakhStream(t)
	s.x.SetInt64(3)
	s.a.SetInt64(3)
	s.b.SetInt64(5)
	s.w.SetInt64(7)
	s.mask.SetInt64(11)
	s.nextBlock()
	// x becomes 14; denominator is set to 1; k=3*14²+5+7=600.
	// The next state is 11 XOR 600 = 595, and k >> 265 = 0.
	if s.x.Cmp(big.NewInt(595)) != 0 || s.block != [32]byte{} {
		t.Fatal("singularity does not follow the documented scalar v1 rule")
	}
}

func TestExperimentalBarzakhNormalizesXORMersenneValue(t *testing.T) {
	s := testBarzakhStream(t)
	s.x.SetInt64(1)
	s.a.SetInt64(0)
	s.b.Sub(&s.prime, big.NewInt(2))
	s.w.SetInt64(1)
	s.mask.SetInt64(1)
	s.nextBlock()
	// k=p-1; 1 XOR (p-1)=p, which must be represented as field zero.
	if s.x.Sign() != 0 || !bytes.Equal(s.block[:], bytes.Repeat([]byte{0xff}, 32)) {
		t.Fatal("XOR result equal to the modulus was not normalized")
	}
}

func TestExperimentalBarzakhClearsReplacedAndClosedState(t *testing.T) {
	d := testGenerator(t, Barzakh521)
	for range 2 {
		if _, err := d.Read(make([]byte, 1)); err != nil {
			t.Fatal(err)
		}

		old, ok := d.cipher.(*barzakhStream)
		if !ok {
			t.Fatal("wrong backend")
		}

		owned := make([][]big.Word, 0, 8)

		for _, value := range []*big.Int{&old.x, &old.a, &old.b, &old.w, &old.mask, &old.denominator, &old.inverse, &old.value} {
			words := value.Bits()
			owned = append(owned, words[:cap(words)])
		}

		if err := d.Reseed(testSeed()); err != nil {
			t.Fatal(err)
		}

		if !old.closed || old.block != [32]byte{} || old.x.Sign() != 0 {
			t.Fatal("reseed retained readable old state")
		}

		for _, words := range owned {
			for _, word := range words {
				if word != 0 {
					t.Fatal("owned old integer storage was not cleared")
				}
			}
		}
	}

	current, ok := d.cipher.(*barzakhStream)
	if !ok {
		t.Fatal("wrong replacement backend")
	}

	if err := d.Close(); err != nil || !current.closed || current.x.Sign() != 0 {
		t.Fatalf("Close retained experimental state: %v", err)
	}
}
