package lab

import (
	"context"
	"errors"
	"io"
	"sync"

	"golang.org/x/sys/unix"
)

type deviceReader struct {
	mu     sync.Mutex
	fd     int
	closed bool
}

func openDevice(path string) (*deviceReader, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}

	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}

	kind := stat.Mode & unix.S_IFMT
	if kind != unix.S_IFCHR && kind != unix.S_IFREG && kind != unix.S_IFIFO {
		_ = unix.Close(fd)
		return nil, errors.New("source must be a readable character device, file or FIFO")
	}

	return &deviceReader{fd: fd}, nil
}

func (d *deviceReader) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return nil
	}

	d.closed = true

	return unix.Close(d.fd)
}

func (d *deviceReader) Read(ctx context.Context, count int) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return nil, io.ErrClosedPipe
	}

	data := make([]byte, count)

	size := 0
	for size < count {
		if err := ctx.Err(); err != nil {
			return data[:size], err
		}

		n, err := unix.Read(d.fd, data[size:])
		if n > 0 {
			size += n
		}

		if errors.Is(err, unix.EINTR) {
			continue
		}

		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
			_, err = unix.Poll([]unix.PollFd{{Fd: int32(d.fd), Events: unix.POLLIN}}, 50)
			if err != nil && !errors.Is(err, unix.EINTR) {
				return data[:size], err
			}

			continue
		}

		if err != nil {
			return data[:size], err
		}

		if n == 0 {
			return data[:size], io.EOF
		}
	}

	return data, nil
}
