//go:build linux && (amd64 || arm || arm64)

package quantisusb

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const Available = true

// Linux UAPI usbdevice_fs.h; pointer-sized layout is checked in native tests
// and cross-build assertions for every released Linux architecture.
type usbURB struct {
	Type         uint8
	Endpoint     uint8
	_            uint16
	Status       int32
	Flags        uint32
	Buffer       uintptr
	BufferLength int32
	ActualLength int32
	StartFrame   int32
	Packets      int32
	ErrorCount   int32
	Signal       uint32
	UserContext  uintptr
}

const linuxURBSize = 44 + 3*(unsafe.Sizeof(uintptr(0))-4)

var (
	_ [linuxURBSize - unsafe.Sizeof(usbURB{})]byte
	_ [unsafe.Sizeof(usbURB{}) - linuxURBSize]byte
)

type linuxCalls struct {
	ioctl func(int, uint, unsafe.Pointer) error
	poll  func([]unix.PollFd, int) (int, error)
	close func(int) error
}

type linuxTransport struct {
	ioMu     sync.Mutex
	mu       sync.Mutex
	fd       int
	wake     int
	deadline time.Time
	closed   bool
	closeErr error
	calls    linuxCalls
}

func Open(path string) (*Source, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open Quantis USB device: %w", err)
	}

	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}

	if stat.Mode&unix.S_IFMT != unix.S_IFCHR || unix.Major(stat.Rdev) != 189 {
		_ = unix.Close(fd)
		return nil, errors.New("the Quantis USB transport requires a Linux usbfs character device")
	}

	// usbfs read exposes cached descriptors, never randomness. Verify the
	// descriptor of the opened handle, not just a previously enumerated path.
	descriptors := make([]byte, 65536)

	n, err := unix.Read(fd, descriptors)
	if err != nil {
		_ = unix.Close(fd)
		return nil, err
	}

	if err := validateDevice(descriptors[:n]); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}

	packet, err := packetSizeFromConfig(descriptors[18:n])
	if err != nil {
		_ = unix.Close(fd)
		return nil, err
	}

	var number uint32 // Claim interface zero; never detach another kernel driver.
	if err := usbIoctl(fd, 0x8004550f, unsafe.Pointer(&number)); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("claim Quantis USB interface 0 (check permissions/in-use driver): %w", err)
	}

	wake, err := unix.Eventfd(0, unix.EFD_NONBLOCK|unix.EFD_CLOEXEC)
	if err != nil {
		_ = unix.Close(fd) // Closing usbfs also releases the claim.
		return nil, err
	}

	device := &linuxTransport{fd: fd, wake: wake, calls: linuxCalls{ioctl: usbIoctl, poll: unix.Poll, close: unix.Close}}

	return newSource(device, packet)
}

func usbIoctl(fd int, request uint, pointer unsafe.Pointer) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(request), uintptr(pointer))
	if errno != 0 {
		return errno
	}

	return nil
}

func (d *linuxTransport) state() (int, time.Time, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return d.fd, d.deadline, os.ErrClosed
	}

	if !d.deadline.IsZero() && !time.Now().Before(d.deadline) {
		return d.fd, d.deadline, os.ErrDeadlineExceeded
	}

	return d.fd, d.deadline, nil
}

func (d *linuxTransport) signal() error {
	var value [8]byte
	binary.LittleEndian.PutUint64(value[:], 1)

	_, err := unix.Write(d.wake, value[:])
	if errors.Is(err, unix.EAGAIN) {
		return nil
	}

	return err
}

func (d *linuxTransport) SetReadDeadline(deadline time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return os.ErrClosed
	}

	d.deadline = deadline

	return d.signal()
}

func (d *linuxTransport) Close() error {
	d.mu.Lock()

	d.closed = true
	if d.wake >= 0 {
		_ = d.signal()
	}
	d.mu.Unlock()

	d.ioMu.Lock()
	defer d.ioMu.Unlock()

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.fd >= 0 {
		var number uint32

		err := d.calls.ioctl(d.fd, 0x80045510, unsafe.Pointer(&number))
		if errors.Is(err, unix.ENODEV) {
			err = nil
		}

		d.closeErr = errors.Join(d.closeErr, err, d.calls.close(d.fd))
		d.fd = -1
	}

	if d.wake >= 0 {
		d.closeErr = errors.Join(d.closeErr, unix.Close(d.wake))
		d.wake = -1
	}

	return d.closeErr
}

func (d *linuxTransport) Control(request byte, p []byte) (int, error) {
	// USBDEVFS control URBs contain the eight-byte setup followed by data.
	buffer := make([]byte, 8+len(p))
	setup := controlSetup(request, len(p))
	copy(buffer, setup[:])

	n, err := d.transfer(2, 0, buffer)
	if err != nil {
		return 0, err
	}

	if n > len(p) {
		return 0, errors.New("invalid Quantis USB control length")
	}

	return copy(p, buffer[8:8+n]), nil
}

func (d *linuxTransport) Bulk(p []byte) (int, error) { return d.transfer(3, Endpoint, p) }

func (d *linuxTransport) transfer(kind, endpoint byte, buffer []byte) (int, error) {
	d.ioMu.Lock()
	defer d.ioMu.Unlock()

	fd, _, err := d.state()
	if err != nil {
		return 0, err
	}

	urb := &usbURB{Type: kind, Endpoint: endpoint, Buffer: uintptr(unsafe.Pointer(&buffer[0])), BufferLength: int32(len(buffer))}

	var memory runtime.Pinner
	memory.Pin(urb)

	memory.Pin(&buffer[0])
	defer memory.Unpin()

	submit := uint(0x8000550a) | uint(unsafe.Sizeof(*urb))<<16
	if err := d.calls.ioctl(fd, submit, unsafe.Pointer(urb)); err != nil {
		return 0, err
	}

	for {
		_, deadline, stateErr := d.state()
		if stateErr != nil {
			return 0, d.cancelAndDrain(fd, urb, stateErr)
		}

		completed, err := d.reap(fd, false)
		if err == nil {
			if completed != uintptr(unsafe.Pointer(urb)) {
				return 0, d.invalidate(fd, errors.New("unexpected USB completion identity"))
			}

			if _, _, err := d.state(); err != nil {
				return 0, err
			}

			if urb.Status < 0 {
				return 0, unix.Errno(-urb.Status)
			}

			limit := len(buffer)
			if kind == 2 {
				limit -= 8
			}

			if urb.ActualLength < 0 || int(urb.ActualLength) > limit {
				return 0, errors.New("invalid Quantis USB completion length")
			}

			return int(urb.ActualLength), nil
		}

		if !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EINTR) {
			return 0, d.invalidate(fd, err)
		}

		// usbfs signals completed URBs with POLLOUT, not POLLIN.
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLOUT}, {Fd: int32(d.wake), Events: unix.POLLIN}}

		_, err = d.calls.poll(fds, linuxWaitMilliseconds(deadline))
		if err != nil && !errors.Is(err, unix.EINTR) {
			return 0, d.cancelAndDrain(fd, urb, err)
		}

		if fds[1].Revents != 0 {
			var value [8]byte

			_, _ = unix.Read(d.wake, value[:])
		}
	}
}

func (d *linuxTransport) reap(fd int, blocking bool) (uintptr, error) {
	request := uint(0x4000550d) | uint(unsafe.Sizeof(uintptr(0)))<<16
	if blocking {
		request = uint(0x4000550c) | uint(unsafe.Sizeof(uintptr(0)))<<16
	}

	var completed uintptr

	err := d.calls.ioctl(fd, request, unsafe.Pointer(&completed))

	return completed, err
}

func (d *linuxTransport) cancelAndDrain(fd int, urb *usbURB, reason error) error {
	err := d.calls.ioctl(fd, 0x550b, unsafe.Pointer(urb))
	if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOENT) {
		err = nil
	} // Completion can race cancellation.

	for {
		completed, reapErr := d.reap(fd, true)
		if errors.Is(reapErr, unix.EINTR) {
			continue
		}

		if reapErr != nil {
			return errors.Join(reason, err, d.invalidate(fd, reapErr))
		}

		if completed != uintptr(unsafe.Pointer(urb)) {
			return errors.Join(reason, err, d.invalidate(fd, errors.New("unexpected canceled USB completion")))
		}

		return errors.Join(reason, err)
	}
}

// A fatal reap error cannot leave DMA using unpinned memory. Closing usbfs
// synchronously destroys its outstanding URBs before this call returns. As with
// all kernel cancellation, a broken kernel/host controller can still block.
func (d *linuxTransport) invalidate(fd int, reason error) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.closed = true
	if d.fd == fd {
		d.fd = -1
		return errors.Join(reason, d.calls.close(fd))
	}

	return reason
}

func linuxWaitMilliseconds(deadline time.Time) int {
	if deadline.IsZero() {
		return -1
	}

	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0
	}

	if remaining >= 2147483647*time.Millisecond {
		return 2147483647
	}

	return int((remaining + time.Millisecond - 1) / time.Millisecond)
}
