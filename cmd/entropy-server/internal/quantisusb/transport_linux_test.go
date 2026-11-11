//go:build linux && (amd64 || arm || arm64)

package quantisusb

import (
	"bytes"
	"errors"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

type usbfsFixture struct {
	mu        sync.Mutex
	active    *usbURB
	buffer    []byte
	complete  bool
	canceled  bool
	reaped    bool
	closed    bool
	fatal     bool
	requests  []uint
	submitted chan struct{}
}

func usbfsDriver(t *testing.T, fixture *usbfsFixture) *linuxTransport {
	t.Helper()

	wake, err := unix.Eventfd(0, unix.EFD_NONBLOCK|unix.EFD_CLOEXEC)
	if err != nil {
		t.Fatal(err)
	}

	driver := &linuxTransport{fd: 9999, wake: wake}
	driver.calls = linuxCalls{
		ioctl: func(_ int, request uint, pointer unsafe.Pointer) error {
			fixture.mu.Lock()
			defer fixture.mu.Unlock()

			fixture.requests = append(fixture.requests, request)
			switch request & 0xffff {
			case 0x550a:
				fixture.active = (*usbURB)(pointer)
				if fixture.active.Type != 3 || fixture.active.Endpoint != Endpoint || fixture.active.Buffer != uintptr(unsafe.Pointer(&fixture.buffer[0])) || request>>16&0x3fff != uint(unsafe.Sizeof(usbURB{})) {
					t.Error("invalid submitted Linux USB ABI")
				}

				runtime.GC()

				if fixture.submitted != nil {
					close(fixture.submitted)
				}

				return nil
			case 0x550b:
				if fixture.active != (*usbURB)(pointer) {
					t.Error("discarded a different URB")
				}

				fixture.canceled = true

				return nil
			case 0x550c, 0x550d:
				if fixture.fatal {
					return unix.ENODEV
				}

				if !fixture.complete && !fixture.canceled {
					return unix.EAGAIN
				}

				if fixture.active == nil {
					t.Fatal("reaped an inactive URB")
				}

				if fixture.canceled {
					fixture.active.Status = -int32(unix.ENOENT)
				} else {
					fixture.active.ActualLength = int32(len(fixture.buffer))
					copy(fixture.buffer, bytes.Repeat([]byte{0x77}, len(fixture.buffer)))
				}

				*(*uintptr)(pointer) = uintptr(unsafe.Pointer(fixture.active))
				fixture.active = nil
				fixture.reaped = true

				return nil
			case 0x5510:
				return nil
			default:
				t.Fatalf("unexpected ioctl %x", request)
				return nil
			}
		},
		poll: func(fds []unix.PollFd, timeout int) (int, error) {
			if fds[0].Events != unix.POLLOUT {
				t.Error("usbfs completion polled for wrong readiness")
			}

			return unix.Poll(fds[1:], timeout) // Only the real wake eventfd is needed by this kernel fixture.
		},
		close: func(int) error {
			fixture.mu.Lock()
			defer fixture.mu.Unlock()

			fixture.closed = true
			fixture.active = nil

			return nil
		},
	}

	t.Cleanup(func() { _ = driver.Close() })

	return driver
}

func TestLinuxUSBReapsSuccessAndCancellationBeforeReturning(t *testing.T) {
	for _, complete := range []bool{false, true} {
		fixture := &usbfsFixture{buffer: make([]byte, 64), complete: complete}
		driver := usbfsDriver(t, fixture)

		budget := 20 * time.Millisecond
		if complete {
			budget = time.Second
		}

		if err := driver.SetReadDeadline(time.Now().Add(budget)); err != nil {
			t.Fatal(err)
		}

		n, err := driver.Bulk(fixture.buffer)
		if complete {
			if err != nil || n != 64 || !bytes.Equal(fixture.buffer, bytes.Repeat([]byte{0x77}, 64)) {
				t.Fatalf("successful completion: %d %v", n, err)
			}
		} else if n != 0 || !errors.Is(err, os.ErrDeadlineExceeded) || !fixture.canceled {
			t.Fatalf("timeout: %d %v", n, err)
		}

		if !fixture.reaped || fixture.active != nil {
			t.Fatal("operation returned before completion was drained")
		}
	}
}

func TestLinuxUSBCloseInterruptsAndFatalReapClosesHandle(t *testing.T) {
	fixture := &usbfsFixture{buffer: make([]byte, 64), submitted: make(chan struct{})}

	driver := usbfsDriver(t, fixture)
	if err := driver.SetReadDeadline(time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)

	go func() { _, err := driver.Bulk(fixture.buffer); done <- err }()

	select {
	case <-fixture.submitted:
	case <-time.After(time.Second):
		t.Fatal("USB read was not submitted")
	}

	if err := driver.Close(); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if !errors.Is(err, os.ErrClosed) {
			t.Fatalf("close interruption: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("closed USB operation did not join")
	}

	fixture.mu.Lock()
	pending := fixture.active != nil
	fixture.mu.Unlock()

	if pending {
		t.Fatal("close left an active URB")
	}

	fatal := &usbfsFixture{buffer: make([]byte, 64), fatal: true}

	broken := usbfsDriver(t, fatal)
	if _, err := broken.Bulk(fatal.buffer); !errors.Is(err, unix.ENODEV) || !fatal.closed {
		t.Fatalf("fatal reap did not close before returning: %v", err)
	}
}

func TestLinuxUSBRejectsOrdinaryFileAndChecksABILayout(t *testing.T) {
	path := t.TempDir() + "/not-usb"
	if err := os.WriteFile(path, []byte("not a device"), 0o600); err != nil {
		t.Fatal(err)
	}

	if source, err := Open(path); err == nil || source != nil {
		t.Fatalf("ordinary file accepted as USB: %v %v", source, err)
	}

	if unsafe.Sizeof(usbURB{}) != linuxURBSize || unsafe.Offsetof(usbURB{}.BufferLength) != unsafe.Offsetof(usbURB{}.Buffer)+unsafe.Sizeof(uintptr(0)) {
		t.Fatal("Linux USB ABI layout mismatch")
	}
}
