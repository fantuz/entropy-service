//go:build windows && amd64

package quantisusb

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func windowsUSBPipe(t *testing.T) (*windowsTransport, windows.Handle) {
	t.Helper()

	name, err := windows.UTF16PtrFromString(fmt.Sprintf(`\\.\pipe\quantis-usb-test-%d-%d`, os.Getpid(), time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}

	file, err := windows.CreateNamedPipe(name, windows.PIPE_ACCESS_INBOUND|windows.FILE_FLAG_OVERLAPPED|windows.FILE_FLAG_FIRST_PIPE_INSTANCE,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS, 1, 0, 65536, 0, nil)
	if err != nil {
		t.Fatal(err)
	}

	calls := winCalls{
		read: func(_ uintptr, _ byte, p []byte, n *uint32, op *windows.Overlapped) error {
			return windows.ReadFile(file, p, n, op)
		},
		result: func(_ uintptr, op *windows.Overlapped, n *uint32, wait bool) error {
			return windows.GetOverlappedResult(file, op, n, wait)
		},
		free: func(uintptr) error { return nil },
	}

	driver, err := newWindowsTransport(file, 1, calls)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = driver.Close() })

	writer, err := windows.CreateFile(name, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = windows.CloseHandle(writer) })

	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = windows.CloseHandle(event) }()

	op := &windows.Overlapped{HEvent: event}

	err = windows.ConnectNamedPipe(file, op)
	if err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
		t.Fatal(err)
	}

	return driver, writer
}

func TestWinUSBCompletionCancelDrainAndReuse(t *testing.T) {
	driver, writer := windowsUSBPipe(t)
	if err := driver.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}

	data := make([]byte, 64)
	if n, err := driver.Bulk(data); n != 0 || !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("timeout not drained: %d %v", n, err)
	}

	if err := driver.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	want := bytes.Repeat([]byte{0x77}, 64)

	var n uint32
	if err := windows.WriteFile(writer, want, &n, nil); err != nil {
		t.Fatal(err)
	}

	if count, err := driver.Bulk(data); count != 64 || err != nil || !bytes.Equal(data, want) {
		t.Fatalf("reuse after cancellation: %d %v", count, err)
	}
}

func TestWinUSBCloseJoinsPendingReadAndSystemAPIsExist(t *testing.T) {
	if err := findWinUSB(); err != nil {
		t.Fatalf("system WinUSB APIs missing: %v", err)
	}

	if unsafe.Sizeof(pipeInformation{}) != 12 {
		t.Fatal("WinUSB pipe ABI layout mismatch")
	}

	driver, _ := windowsUSBPipe(t)
	started := make(chan struct{})
	read := driver.calls.read
	driver.calls.read = func(handle uintptr, pipe byte, p []byte, n *uint32, operation *windows.Overlapped) error {
		err := read(handle, pipe, p, n, operation)

		close(started)

		return err
	}

	done := make(chan error, 1)
	go func() { _, err := driver.Bulk(make([]byte, 64)); done <- err }()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("overlapped read was not submitted")
	}

	if err := driver.Close(); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if !errors.Is(err, os.ErrClosed) {
			t.Fatalf("close error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("pending WinUSB-style operation not joined")
	}
}
