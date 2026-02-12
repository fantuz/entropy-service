package rng

import (
	"os"
	"sync"
	"time"
	"errors"
	//"sync/atomic"
)

// QRNG represents a hardware or network QRNG
type QRNG interface {
	Read(p []byte) error
}

// PCIe card driver stub
type QRNGCard struct {
	// add fields if needed for device handle
}

// NewQRNGCard returns a new QRNG interface
func NewQRNGCard() *QRNGCard {
	return &QRNGCard{}
}

// Read fills p with true random bytes from the card
func (q *QRNGCard) Read(p []byte) error {
	// TODO: Replace this with real QRNG API calls
	// For example:
	// n, err := q.card.Read(p)
	// if err != nil { return err }
	// if n != len(p) { return errors.New("short read") }
	// return nil

	return errors.New("QRNG Read not implemented yet")
}

// QRNGBuffer holds bytes read asynchronously from a QRNG device
type QRNGBuffer struct {
	buf       []byte       // the current entropy buffer
	mu        sync.Mutex   // protects buf
	capacity  int          // max buffer size in bytes
	fillDelay time.Duration // small delay to avoid busy-wait
	devPath   string       // path to QRNG device, e.g., /dev/qrandom0
	stop      chan struct{} // used to signal background goroutine to exit
}


// NewQRNGBuffer creates a new buffered QRNG reader
func NewQRNGBuffer(dev string, capacity int) *QRNGBuffer {
	q := &QRNGBuffer{
		buf:       make([]byte, 0, capacity),
		capacity:  capacity,
		fillDelay: 10 * time.Millisecond,
		devPath:   dev,
		stop:      make(chan struct{}),
	}

	// Start the background goroutine to fill the buffer
	go q.fillLoop()
	return q
}

// Stop signals the background goroutine to exit
func (q *QRNGBuffer) Stop() {
	close(q.stop)
}

// Get returns n bytes from the buffer, blocking if necessary
func (q *QRNGBuffer) Get(n int) ([]byte, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	// Wait until enough bytes are available
	for len(q.buf) < n {
		q.mu.Unlock()
		time.Sleep(q.fillDelay)
		q.mu.Lock()
	}

	// Take the first n bytes
	out := q.buf[:n]
	q.buf = q.buf[n:]
	return out, nil
}

// fillLoop continuously fills the buffer from the QRNG device
func (q *QRNGBuffer) fillLoop() {
	for {
		select {
		case <-q.stop:
			return
		default:
		}

		q.mu.Lock()
		free := q.capacity - len(q.buf)
		q.mu.Unlock()

		if free <= 0 {
			// Buffer is full, sleep a little
			time.Sleep(q.fillDelay)
			continue
		}

		tmp := make([]byte, free)
		f, err := os.Open(q.devPath)
		if err != nil {
			// Could not open QRNG device, retry after short sleep
			time.Sleep(50 * time.Millisecond)
			continue
		}

		total := 0
		for total < free {
			m, err := f.Read(tmp[total:])
			if err != nil { break }
			total += m
			//incTest(m)
		}
		//atomic.AddUint64(&rngBytesBuffered, uint64(total))
		//atomic.AddUint64(&rngBufferSize, uint64(len(total)))
		f.Close()

		// Append new entropy to the buffer
		q.mu.Lock()
		q.buf = append(q.buf, tmp[:total]...)
		q.mu.Unlock()
	}
}

