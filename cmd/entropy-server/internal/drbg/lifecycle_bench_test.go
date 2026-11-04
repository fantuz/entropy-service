package drbg

import (
	"fmt"
	"testing"
)

// BenchmarkDRBGLifecycle is deliberately compatible with both the baseline
// before lifecycle hardening and newer code implementing Close. Copy this
// benchmark file unchanged into each checkout when comparing revisions.
//
// Avoid changing backend, seed, -cpu, GOMAXPROCS, toolchain or GOGC between
// runs. Metrics count bytes generated, including child-seed derivation.
func BenchmarkDRBGLifecycle(b *testing.B) {
	var seed [64]byte

	for i := range seed {
		seed[i] = byte(i)
	}

	var reseed [256]byte

	for i := range reseed {
		reseed[i] = byte(i)
	}

	for _, algorithm := range []Algorithm{ChaCha20, AES256CTR} {
		b.Run(string(algorithm), func(b *testing.B) {
			b.Run("NewClose", func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()

				for range b.N {
					d, err := NewDRBGWithAlgorithm(seed[:], algorithm)
					if err != nil {
						b.Fatal(err)
					}

					closeIfSupported(b, &d)

				}
			})

			b.Run("Reseed256", func(b *testing.B) {
				d, err := NewDRBGWithAlgorithm(seed[:], algorithm)
				if err != nil {
					b.Fatal(err)
				}

				b.ReportAllocs()
				b.ResetTimer()

				for range b.N {
					if err := d.Reseed(reseed[:]); err != nil {
						b.Fatal(err)
					}
				}

				b.StopTimer()
				closeIfSupported(b, &d)

			})

			b.Run("ChildCreateClose", func(b *testing.B) {
				parent, err := NewDRBGWithAlgorithm(seed[:], algorithm)
				if err != nil {
					b.Fatal(err)
				}

				b.ReportAllocs()
				b.ResetTimer()

				for range b.N {
					child, err := NewConnectionDRBG(&parent)
					if err != nil {
						b.Fatal(err)
					}

					closeIfSupported(b, &child)

				}

				b.StopTimer()
				closeIfSupported(b, &parent)

			})

			b.Run("WSOpenReadClose", func(b *testing.B) {
				parent, err := NewDRBGWithAlgorithm(seed[:], algorithm)
				if err != nil {
					b.Fatal(err)
				}

				var frame [4096]byte

				b.ReportAllocs()
				b.SetBytes(int64(len(frame)))
				b.ResetTimer()

				for range b.N {
					conn := NewConnectionReader(&parent)
					if _, err := conn.Read(frame[:]); err != nil {
						b.Fatal(err)
					}

					conn.Close()
				}

				b.StopTimer()
				closeIfSupported(b, &parent)

			})

			b.Run("Read64KiB", func(b *testing.B) {
				d, err := NewDRBGWithAlgorithm(seed[:], algorithm)
				if err != nil {
					b.Fatal(err)
				}

				var buf [64 << 10]byte

				b.ReportAllocs()
				b.SetBytes(int64(len(buf)))
				b.ResetTimer()

				for range b.N {
					if _, err := d.Read(buf[:]); err != nil {
						b.Fatal(err)
					}
				}

				b.StopTimer()
				closeIfSupported(b, &d)

			})
		})
	}
}

func closeIfSupported(b *testing.B, d *DRBG) {
	b.Helper()

	if closer, ok := any(d).(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			b.Fatal(fmt.Errorf("DRBG close: %w", err))
		}
	}
}
