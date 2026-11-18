package drbg

import (
	"fmt"
	"testing"
)

// The same file can be copied unchanged to a pre-feature checkout. Unsupported
// CPU selections are skipped; software measurements retain identical settings.
func BenchmarkOutputBackends(b *testing.B) {
	for _, name := range []string{"aes-256-ctr", "chacha20", "rdrand-experimental"} {
		for _, size := range []int{64 << 10, 1 << 20} {
			b.Run(fmt.Sprintf("%s/%d", name, size), func(b *testing.B) {
				algorithm, err := ParseAlgorithm(name)
				if err != nil {
					b.Skip(err)
				}

				d, err := NewDRBGWithAlgorithm(make([]byte, 64), algorithm)
				if err != nil {
					b.Fatal(err)
				}
				defer d.Close()

				buf := make([]byte, size)
				b.SetBytes(int64(size))
				b.ReportAllocs()
				b.ResetTimer()

				for range b.N {
					if _, err := d.Read(buf); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
