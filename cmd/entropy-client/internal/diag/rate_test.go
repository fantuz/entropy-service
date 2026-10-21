package diag

import (
	"math"
	"sync"
	"testing"
	"time"
)

func TestRateMeterConcurrentAccess(t *testing.T) {
	const workers, updates, chunkSize = 4, 1000, 64

	meter := NewRateMeter()
	// Exercise the sampling-window rollover without waiting for a timer.
	meter.last = time.Now().Add(-2 * time.Second)
	start := make(chan struct{})

	var wg sync.WaitGroup

	for worker := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()

			<-start

			var previous uint64

			for range updates {
				if worker%2 == 0 {
					meter.Update(chunkSize)
					continue
				}

				total := meter.Total()
				if total < previous {
					t.Error("total bytes decreased")
				}

				previous = total

				if meter.Bytes() < 0 {
					t.Error("negative byte count")
				}

				if rate := meter.RateMbps(); rate < 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
					t.Errorf("invalid rate: %v", rate)
				}
			}
		}()
	}

	close(start)
	wg.Wait()

	if got, want := meter.Total(), uint64(workers/2*updates*chunkSize); got != want {
		t.Fatalf("total bytes = %d, want %d", got, want)
	}
}
