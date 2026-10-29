package drbg

import (
	"bytes"
	"encoding/hex"
	"io"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func testSeed() []byte {
	seed := make([]byte, 64)
	for i := range seed {
		seed[i] = byte(i)
	}

	return seed
}

func testGenerator(t *testing.T, algorithm Algorithm) *DRBG {
	t.Helper()

	d, err := NewDRBGWithAlgorithm(testSeed(), algorithm)
	if err != nil {
		t.Fatal(err)
	}

	return &d
}

func decodeTestHex(t *testing.T, value string) []byte {
	t.Helper()

	b, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}

	return b
}

func TestAlgorithmValidation(t *testing.T) {
	for _, algorithm := range []Algorithm{ChaCha20, AES256CTR} {
		got, err := ParseAlgorithm(string(algorithm))
		if err != nil || got != algorithm {
			t.Fatalf("parse %q: %q, %v", algorithm, got, err)
		}

		for _, size := range []int{0, 31} {
			if _, err := NewDRBGWithAlgorithm(make([]byte, size), algorithm); err == nil {
				t.Errorf("%s accepted a %d-byte seed", algorithm, size)
			}
		}
	}

	for _, name := range []string{"", "aes", "AES-256-CTR", "aes-256-gcm", " chacha20"} {
		if _, err := ParseAlgorithm(name); err == nil {
			t.Errorf("accepted %q", name)
		}

		if _, err := NewDRBGWithAlgorithm(testSeed(), Algorithm(name)); err == nil {
			t.Errorf("constructed unsupported algorithm %q", name)
		}
	}
}

func TestAES256CTRKnownAnswer(t *testing.T) {
	// NIST SP 800-38A, appendix F.5.5: all four blocks, including counter carry.
	// https://nvlpubs.nist.gov/nistpubs/Legacy/SP/nistspecialpublication800-38a.pdf
	var (
		key     [32]byte
		counter [16]byte
	)

	copy(key[:], decodeTestHex(t, "603deb1015ca71be2b73aef0857d77811f352c073b6108d72d9810a30914dff4"))
	copy(counter[:], decodeTestHex(t, "f0f1f2f3f4f5f6f7f8f9fafbfcfdfeff"))
	input := decodeTestHex(t, "6bc1bee22e409f96e93d7e117393172a"+
		"ae2d8a571e03ac9c9eb76fac45af8e51"+"30c81c46a35ce411e5fbc1191a0a52ef"+"f69f2445df4f9b17ad2b417be66c3710")
	want := decodeTestHex(t, "601ec313775789a5b7a7f504bbf3d228"+
		"f443e3ca4d62b59aca84e990cacaf5c5"+"2b0930daa23de94ce87017ba2d84988d"+"dfc9c58db67aada613c2dd08457941a6")

	stream, err := newCipherStream(AES256CTR, key, counter)
	if err != nil {
		t.Fatal(err)
	}

	stream.XORKeyStream(input, input)

	if !bytes.Equal(input, want) {
		t.Fatal("AES-256-CTR differs from the NIST vector")
	}
}

func TestBackendSeedAndReseedFixtures(t *testing.T) {
	for _, tc := range []struct {
		algorithm Algorithm
		initial   string
		reseeded  string
	}{
		{
			ChaCha20,
			"8ce8bc610ac05ff2e3dd88b49a1404c2844f148037027476b83d58f5609adf65f8fa87d8b5f5287f6cfdbdb69adea2f942e4acf5a9ad9e860623477a73a24c4e",
			"4f140529b1807ec991e72ed71d1e3ed7d003e39e49fd7f3f24ac10a2048b4bc38f1cf390eb54f516b3e432090077ae4cc415b75dbab88021e11cf87fd317fac3",
		},
		{
			AES256CTR,
			"1521bc6f53e669e853318697962626ae3a14ed7940640ccb871ab9a1ef24e83e725e26128360aa4769fa02334dd326e1fd5d5c65b07e8afa626e948c01696d73",
			"8a82d03b2414a5bdcf74fb2df908fcc878af68bfa1bec80e54db09e0b76ea32f0b40f3e6db04eb379fb37c27549f87fd81c91c8776393c5ad2a37a99eaac4dde",
		},
	} {
		t.Run(tc.algorithm.String(), func(t *testing.T) {
			d := testGenerator(t, tc.algorithm)
			if tc.algorithm == ChaCha20 {
				// Exercise the existing constructor as well as the selectable API.
				legacy, err := NewDRBG(testSeed())
				if err != nil {
					t.Fatal(err)
				}

				d = &legacy
			}

			for i, want := range []string{tc.initial, tc.reseeded} {
				if i == 1 {
					if err := d.Reseed(bytes.Repeat([]byte{0x5a}, 256)); err != nil {
						t.Fatal(err)
					}
				}

				got := bytes.Repeat([]byte{0xa5}, 64)
				if _, err := io.ReadFull(d, got); err != nil {
					t.Fatal(err)
				}

				if !bytes.Equal(got, decodeTestHex(t, want)) || d.Algorithm() != tc.algorithm {
					t.Fatalf("backend or stream changed at fixture %d", i)
				}
			}
		})
	}
}

func TestBackendReadContinuityAndChildren(t *testing.T) {
	for _, algorithm := range []Algorithm{ChaCha20, AES256CTR} {
		t.Run(algorithm.String(), func(t *testing.T) {
			a, b := testGenerator(t, algorithm), testGenerator(t, algorithm)

			want, got := make([]byte, 1031), bytes.Repeat([]byte{0xa5}, 1031)
			if _, err := io.ReadFull(a, want); err != nil {
				t.Fatal(err)
			}

			for offset := 0; offset < len(got); offset += 7 {
				if _, err := io.ReadFull(b, got[offset:min(offset+7, len(got))]); err != nil {
					t.Fatal(err)
				}
			}

			if !bytes.Equal(got, want) {
				t.Fatal("partial reads do not preserve stream continuity")
			}

			first, err := NewConnectionDRBG(a)
			if err != nil {
				t.Fatal(err)
			}

			second, err := NewConnectionDRBG(a)
			if err != nil {
				t.Fatal(err)
			}

			if first.Algorithm() != algorithm || second.Algorithm() != algorithm {
				t.Fatal("child did not inherit its parent's backend")
			}

			if _, err := io.ReadFull(&first, got); err != nil {
				t.Fatal(err)
			}

			if _, err := io.ReadFull(&second, want); err != nil {
				t.Fatal(err)
			}

			if bytes.Equal(got, want) {
				t.Fatal("children unexpectedly share the same stream")
			}
		})
	}
}

func TestBackendLimitAndMetadata(t *testing.T) {
	for _, algorithm := range []Algorithm{ChaCha20, AES256CTR} {
		t.Run(algorithm.String(), func(t *testing.T) {
			d := testGenerator(t, algorithm)
			d.SetMetadata("test", "test", "incorrect caller label", time.Second, 256)
			d.generated = maxBytesPerKey - 4

			buf := bytes.Repeat([]byte{0xa5}, 5)
			if n, err := d.Read(buf); n != 0 || err == nil || !bytes.Equal(buf, bytes.Repeat([]byte{0xa5}, 5)) {
				t.Fatal("over-budget read must fail without consuming or changing the buffer")
			}

			if n, err := d.Read(buf[:4]); n != 4 || err != nil {
				t.Fatalf("last permitted read: %d, %v", n, err)
			}

			if n, err := d.Read(nil); n != 0 || err != nil {
				t.Fatalf("empty read at the limit: %d, %v", n, err)
			}

			if _, err := NewConnectionDRBG(d); err == nil {
				t.Fatal("child derivation discarded the parent's exhaustion error")
			}

			if err := d.Reseed(bytes.Repeat([]byte{0x5a}, 256)); err != nil {
				t.Fatal(err)
			}

			if _, err := io.ReadFull(d, buf); err != nil || d.generated != uint64(len(buf)) {
				t.Fatalf("reseed did not restore the output budget: %v", err)
			}

			w := httptest.NewRecorder()
			d.WriteHeaders(w)

			if w.Header().Get("X-RNG-DRBG") != algorithm.String() || d.GetMetadata().DRBG != algorithm.String() {
				t.Fatal("metadata does not identify the actual backend")
			}
		})
	}
}

func TestBackendConcurrentReadsReseedsAndDerivation(t *testing.T) {
	for _, algorithm := range []Algorithm{ChaCha20, AES256CTR} {
		t.Run(algorithm.String(), func(t *testing.T) {
			d := testGenerator(t, algorithm)

			var wg sync.WaitGroup

			for range 4 {
				wg.Add(1)
				go func() {
					defer wg.Done()

					buf := make([]byte, 4096)
					for range 32 {
						if _, err := io.ReadFull(d, buf); err != nil {
							t.Error(err)
							return
						}

						child, err := NewConnectionDRBG(d)
						if err != nil {
							t.Error(err)
							return
						}

						if child.Algorithm() != algorithm {
							t.Error("child backend changed during concurrent reseeding")
							return
						}
					}
				}()
			}

			for i := range 16 {
				if err := d.Reseed(bytes.Repeat([]byte{byte(i)}, 256)); err != nil {
					t.Error(err)
				}
			}

			wg.Wait()
		})
	}
}
