package drbg

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/rdrand"
)

// BenchmarkRDRANDReseedLatency times the root freshness update after connection
// reads have acquired the serving policy lock. Each read uses the native reader.
func BenchmarkRDRANDReseedLatency(b *testing.B) {
	if !rdrand.Available() {
		b.Skip("native RDRAND is unavailable on this CPU or build")
	}

	b.Run("idle", func(b *testing.B) { benchmarkReseedLatency(b, 0, 0) })

	for _, size := range []int{1 << 20, 8 << 20, 32 << 20} {
		for _, readers := range []int{1, 4} {
			b.Run(fmt.Sprintf("%dMiB/%d-readers", size>>20, readers), func(b *testing.B) {
				benchmarkReseedLatency(b, readers, size)
			})
		}
	}
}

func benchmarkReseedLatency(b *testing.B, readers, size int) {
	b.Helper()

	var seed [256]byte

	root, err := NewDRBGWithAlgorithm(seed[:], RDRAND)
	if err != nil {
		b.Fatal(err)
	}
	defer root.Close()

	if err := root.ConfigureReseedPolicy(ReseedStop, time.Hour); err != nil {
		b.Fatal(err)
	}

	connections := make([]*ConnectionReader, readers)
	buffers := make([][]byte, readers)

	for i := range connections {
		connections[i] = NewConnectionReader(&root)
		defer connections[i].Close()

		buffers[i] = make([]byte, size)
	}

	// Only this benchmark installs a rendezvous at the entry to the hardware
	// read. The original instruction, retry budget and failure latch still run.
	nativeRead := root.cpu.read
	entered := make(chan struct{}, readers)
	done := make(chan error, readers)

	var release chan struct{}

	root.cpu.read = func(p []byte) (int, error) {
		entered <- struct{}{}

		<-release

		return nativeRead(p)
	}

	latencies := make([]time.Duration, min(b.N, 4096))
	b.ResetTimer()

	for i := range b.N {
		release = make(chan struct{})

		for j, connection := range connections {
			go func() {
				n, readErr := connection.Read(buffers[j])
				if n != size && readErr == nil {
					readErr = io.ErrUnexpectedEOF
				}

				done <- readErr
			}()
		}

		for range readers {
			select {
			case <-entered:
			case readErr := <-done:
				close(release)

				for range readers - 1 {
					<-done
				}

				b.Fatalf("read returned before entering the native reader: %v", readErr)
			}
		}

		close(release)

		start := time.Now()
		reseedErr := root.Reseed(seed[:])
		latencies[i%len(latencies)] = time.Since(start)

		for range readers {
			reseedErr = errors.Join(reseedErr, <-done)
		}

		if reseedErr != nil {
			b.Fatal(reseedErr)
		}
	}

	b.StopTimer()
	slices.Sort(latencies)

	for _, p := range []int{50, 95, 99, 100} {
		at := (p*len(latencies)+99)/100 - 1
		b.ReportMetric(float64(latencies[at])/float64(time.Millisecond), fmt.Sprintf("p%d-ms", p))
	}
}
