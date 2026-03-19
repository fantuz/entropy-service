package qrng

import (
	"errors"
	"os"
	"log"
	"sync"
	"time"
	//"sync/atomic"
)

// QRNG represents hardware or networked QRNG. Supports fallback to default Linux CSPRNG
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
	Buf       []byte        // the current entropy buffer
	Mu        sync.Mutex    // protects buf
	Capacity  int           // max buffer size in bytes
	fillDelay time.Duration // small delay to avoid busy-wait
	devPath   string        // path to QRNG device, e.g., /dev/qrandom0
	StopR     chan struct{} // used to signal background goroutine to exit
}

func InitQRNGBuffer(dev string, l int) {
	// report in KB
	log.Printf("initQRNG() set qrngBuffer to %v KB", l*1024*1024)

	// size in MB
	//qrngBuffer = NewQRNGBuffer(dev, l*1024*1024)
	//qrngBuffer = NewQRNGBuffer(dev, l)
	NewQRNGBuffer(dev, l)

	// Attach it to DRBG
	//drbg.SetEntropyBuffer(qrngBuffer)
}

// NewQRNGBuffer creates a new buffered QRNG reader
//func NewQRNGBuffer(dev string, capacity int) *QRNGBuffer
func NewQRNGBuffer(dev string, capacity int) *QRNGBuffer {
	//q := &QRNGBuffer{
	q := &QRNGBuffer{
		Buf:       make([]byte, 0, capacity),
		Capacity:  capacity,
		fillDelay: 10 * time.Millisecond,
		devPath:   dev,
		StopR:     make(chan struct{}),
	}

	// Start the background goroutine to fill the buffer
	go q.fillLoop()

	//metrics.AddBufferedBytes(uint64(q.capacity))
	//incBuffer()

	return q
}


// Stop signals the background goroutine to exit
func (q *QRNGBuffer) Stop() {
	close(q.StopR)
}

// Get returns n bytes from the buffer, blocking if necessary
func (q QRNGBuffer) Get(n int) ([]byte, error) {
	q.Mu.Lock()
	defer q.Mu.Unlock()

	// Wait until enough bytes are available
	for len(q.Buf) < n {
		q.Mu.Unlock()
		time.Sleep(q.fillDelay)
		q.Mu.Lock()
	}

	// Take the first n bytes
	out := q.Buf[:n]
	q.Buf = q.Buf[n:]
	return out, nil
}

// fillLoop continuously fills the buffer from the QRNG device
func (q QRNGBuffer) fillLoop() {
	var fail int = 0
	for {
		select {
		case <-q.StopR:
			return
		default:
		}

		q.Mu.Lock()
		free := q.Capacity - len(q.Buf)
		q.Mu.Unlock()

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
			fail++ 
			if fail > 3 { panic("QRNG device unavailable")}
			//continue
		}

		total := 0
		for total < free {
			m, err := f.Read(tmp[total:])
			if err != nil {
				break
			}
			total += m
			//incTest(m)
		}
		//atomic.AddUint64(&rngBytesBuffered, uint64(total))
		//atomic.AddUint64(&rngBufferSize, uint64(len(total)))
		f.Close()

		// Append new entropy to the buffer
		q.Mu.Lock()
		q.Buf = append(q.Buf, tmp[:total]...)
		q.Mu.Unlock()
	}
}
