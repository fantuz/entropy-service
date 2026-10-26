package drbg

import (
	"errors"
	"io"
	"sync"
)

// ConnectionReader owns a private stream, created lazily on its first read.
// Close releases its instance count. Do not copy a ConnectionReader.
type ConnectionReader struct {
	mu     sync.Mutex
	parent *DRBG
	child  *DRBG
	epoch  uint64
	closed bool
}

func NewConnectionReader(parent *DRBG) *ConnectionReader {
	return &ConnectionReader{parent: parent}
}

// Read refreshes the private stream before using a new parent reseed epoch or
// exceeding its per-key byte budget. Only refreshes take the parent's mutex.
// Derived seeds are parent DRBG output, not additional device entropy reads.
func (c *ConnectionReader) Read(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return 0, io.ErrClosedPipe
	}

	if len(p) == 0 {
		return 0, nil
	}

	if uint64(len(p)) > maxBytesPerKey {
		return 0, errors.New("connection read exceeds DRBG per-key output limit")
	}

	if c.child == nil || c.epoch != c.parent.reseedEpoch.Load() ||
		uint64(len(p)) > maxBytesPerKey-c.child.generated {
		seed, epoch, err := c.parent.connectionSeed()
		if err != nil {
			return 0, err
		}

		if c.child == nil {
			child, err := NewDRBGWithAlgorithm(seed, c.parent.algorithm)
			if err != nil {
				return 0, err
			}

			c.child = &child
		} else if err := c.child.Reseed(seed); err != nil {
			return 0, err
		}

		c.epoch = epoch
	}

	return c.child.Read(p)
}

// connectionSeed keeps the derived seed and its epoch in the same snapshot.
func (d *DRBG) connectionSeed() ([]byte, uint64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	seed := make([]byte, 64)
	_, err := d.readLocked(seed)

	return seed, d.reseedEpoch.Load(), err
}

// Close is idempotent; an idle connection has no child instance to release.
func (c *ConnectionReader) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.closed && c.child != nil {
		DecreaseActiveInstances(1)

		c.child = nil
	}

	c.closed = true
}
