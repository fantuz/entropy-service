//go:build windows && amd64

package quantisusb

import (
	"encoding/binary"
	"errors"
	"os"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const Available = true

var (
	winUSB        = windows.NewLazySystemDLL("winusb.dll")
	winInitialize = winUSB.NewProc("WinUsb_Initialize")
	winFree       = winUSB.NewProc("WinUsb_Free")
	winDescriptor = winUSB.NewProc("WinUsb_GetDescriptor")
	winInterface  = winUSB.NewProc("WinUsb_QueryInterfaceSettings")
	winPipe       = winUSB.NewProc("WinUsb_QueryPipe")
	winControl    = winUSB.NewProc("WinUsb_ControlTransfer")
	winRead       = winUSB.NewProc("WinUsb_ReadPipe")
	winResult     = winUSB.NewProc("WinUsb_GetOverlappedResult")
)

type winCalls struct {
	control func(uintptr, uint64, []byte, *uint32, *windows.Overlapped) error
	read    func(uintptr, byte, []byte, *uint32, *windows.Overlapped) error
	result  func(uintptr, *windows.Overlapped, *uint32, bool) error
	free    func(uintptr) error
}

type windowsTransport struct {
	ioMu     sync.Mutex
	mu       sync.Mutex
	file     windows.Handle
	changed  windows.Handle
	iface    uintptr
	deadline time.Time
	closed   bool
	closeErr error
	calls    winCalls
}

type pipeInformation struct {
	Type       uint32
	ID         uint8
	_          uint8
	PacketSize uint16
	Interval   uint8
	_          [3]byte
}

func findWinUSB() error {
	for _, procedure := range []*windows.LazyProc{winInitialize, winFree, winDescriptor, winInterface, winPipe, winControl, winRead, winResult} {
		if err := procedure.Find(); err != nil {
			return err
		}
	}

	return nil
}

func Open(path string) (*Source, error) {
	if err := findWinUSB(); err != nil {
		return nil, err
	}

	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}

	file, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return nil, err
	}

	var (
		iface  uintptr
		memory runtime.Pinner
	)

	memory.Pin(&iface)
	defer memory.Unpin()

	r, _, callErr := winInitialize.Call(uintptr(file), uintptr(unsafe.Pointer(&iface)))
	if err := winError(r, callErr); err != nil {
		_ = windows.CloseHandle(file)
		return nil, err
	}

	device, err := newWindowsTransport(file, iface, nativeWinCalls())
	if err != nil {
		return nil, err
	}

	packet, err := windowsPacketSize(iface)
	if err != nil {
		return nil, errors.Join(err, device.Close())
	}

	return newSource(device, packet)
}

func windowsPacketSize(iface uintptr) (int, error) {
	device := make([]byte, 18)

	var (
		transferred uint32
		memory      runtime.Pinner
	)
	memory.Pin(&device[0])

	memory.Pin(&transferred)
	defer memory.Unpin()

	r, _, callErr := winDescriptor.Call(iface, 1, 0, 0, uintptr(unsafe.Pointer(&device[0])), uintptr(len(device)), uintptr(unsafe.Pointer(&transferred)))
	if err := winError(r, callErr); err != nil {
		return 0, err
	}

	if transferred != 18 {
		return 0, errors.New("short WinUSB device descriptor")
	}

	if err := validateDevice(device); err != nil {
		return 0, err
	}

	var setting [9]byte
	memory.Pin(&setting[0])

	r, _, callErr = winInterface.Call(iface, 0, uintptr(unsafe.Pointer(&setting[0])))
	if err := winError(r, callErr); err != nil {
		return 0, err
	}

	if setting[0] != 9 || setting[1] != 4 || setting[2] != 0 || setting[3] != 0 || setting[4] == 0 || setting[4] > 32 {
		return 0, errors.New("unsupported WinUSB interface layout")
	}

	for index := byte(0); index < setting[4]; index++ {
		var pipe pipeInformation
		memory.Pin(&pipe)

		r, _, callErr = winPipe.Call(iface, 0, uintptr(index), uintptr(unsafe.Pointer(&pipe)))
		if err := winError(r, callErr); err != nil {
			return 0, err
		}

		if pipe.Type == 2 && pipe.ID == Endpoint && (pipe.PacketSize == 64 || pipe.PacketSize == 512) {
			return int(pipe.PacketSize), nil
		}
	}

	return 0, errors.New("expected WinUSB bulk-IN endpoint 0x86 with 64/512-byte packets")
}

func nativeWinCalls() winCalls {
	return winCalls{
		control: func(handle uintptr, setup uint64, buffer []byte, done *uint32, operation *windows.Overlapped) error {
			r, _, err := winControl.Call(handle, uintptr(setup), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), uintptr(unsafe.Pointer(done)), uintptr(unsafe.Pointer(operation)))
			return winError(r, err)
		},
		read: func(handle uintptr, pipe byte, buffer []byte, done *uint32, operation *windows.Overlapped) error {
			r, _, err := winRead.Call(handle, uintptr(pipe), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), uintptr(unsafe.Pointer(done)), uintptr(unsafe.Pointer(operation)))
			return winError(r, err)
		},
		result: func(handle uintptr, operation *windows.Overlapped, done *uint32, wait bool) error {
			var blocking uintptr
			if wait {
				blocking = 1
			}

			r, _, err := winResult.Call(handle, uintptr(unsafe.Pointer(operation)), uintptr(unsafe.Pointer(done)), blocking)

			return winError(r, err)
		},
		free: func(handle uintptr) error {
			r, _, err := winFree.Call(handle)
			return winError(r, err)
		},
	}
}

func winError(result uintptr, err error) error {
	if result != 0 {
		return nil
	}

	if err == nil || errors.Is(err, windows.ERROR_SUCCESS) {
		return errors.New("WinUSB call failed without an error code")
	}

	return err
}

func newWindowsTransport(file windows.Handle, iface uintptr, calls winCalls) (*windowsTransport, error) {
	changed, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		return nil, errors.Join(err, calls.free(iface), windows.CloseHandle(file))
	}

	return &windowsTransport{file: file, changed: changed, iface: iface, calls: calls}, nil
}

func (d *windowsTransport) state() (time.Time, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return d.deadline, os.ErrClosed
	}

	if !d.deadline.IsZero() && !time.Now().Before(d.deadline) {
		return d.deadline, os.ErrDeadlineExceeded
	}

	return d.deadline, nil
}

func (d *windowsTransport) SetReadDeadline(deadline time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return os.ErrClosed
	}

	d.deadline = deadline

	return windows.SetEvent(d.changed)
}

func (d *windowsTransport) Close() error {
	d.mu.Lock()

	d.closed = true
	if d.changed != 0 {
		_ = windows.SetEvent(d.changed)
	}
	d.mu.Unlock()
	d.ioMu.Lock()
	defer d.ioMu.Unlock()

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.changed != 0 {
		d.closeErr = errors.Join(d.calls.free(d.iface), windows.CloseHandle(d.file), windows.CloseHandle(d.changed))
		d.changed = 0
	}

	return d.closeErr
}

func (d *windowsTransport) Control(request byte, p []byte) (int, error) {
	setup := controlSetup(request, len(p))

	return d.transfer(p, true, binary.LittleEndian.Uint64(setup[:]))
}

func (d *windowsTransport) Bulk(p []byte) (int, error) { return d.transfer(p, false, 0) }

func (d *windowsTransport) transfer(buffer []byte, control bool, setup uint64) (int, error) {
	d.ioMu.Lock()
	defer d.ioMu.Unlock()

	if _, err := d.state(); err != nil {
		return 0, err
	}

	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return 0, err
	}

	defer func() { _ = windows.CloseHandle(event) }()

	operation := &windows.Overlapped{HEvent: event}

	var (
		transferred uint32
		memory      runtime.Pinner
	)
	memory.Pin(operation)
	memory.Pin(&buffer[0])

	memory.Pin(&transferred)
	defer memory.Unpin()

	if control {
		err = d.calls.control(d.iface, setup, buffer, &transferred, operation)
	} else {
		err = d.calls.read(d.iface, Endpoint, buffer, &transferred, operation)
	}

	if err != nil && !errors.Is(err, windows.ERROR_IO_PENDING) {
		return 0, err
	}

	if errors.Is(err, windows.ERROR_IO_PENDING) {
		if err := d.wait(operation); err != nil {
			return 0, err
		}
	}

	if err := d.calls.result(d.iface, operation, &transferred, false); err != nil {
		if errors.Is(err, windows.ERROR_IO_INCOMPLETE) {
			return 0, d.cancelAndDrain(operation, err)
		}

		return 0, err
	}

	if _, err := d.state(); err != nil {
		return 0, err
	}

	if uint64(transferred) > uint64(len(buffer)) {
		return 0, errors.New("invalid WinUSB completion length")
	}

	return int(transferred), nil
}

func (d *windowsTransport) wait(operation *windows.Overlapped) error {
	for {
		deadline, err := d.state()
		if err != nil {
			return d.cancelAndDrain(operation, err)
		}

		result, err := windows.WaitForMultipleObjects([]windows.Handle{operation.HEvent, d.changed}, false, windowsWaitMilliseconds(deadline))
		if err != nil {
			return d.cancelAndDrain(operation, err)
		}

		switch result {
		case windows.WAIT_OBJECT_0:
			return nil
		case windows.WAIT_OBJECT_0 + 1, uint32(windows.WAIT_TIMEOUT):
		default:
			return d.cancelAndDrain(operation, errors.New("unexpected WinUSB wait result"))
		}
	}
}

func (d *windowsTransport) cancelAndDrain(operation *windows.Overlapped, reason error) error {
	err := windows.CancelIoEx(d.file, operation)
	if errors.Is(err, windows.ERROR_NOT_FOUND) {
		err = nil
	}

	var transferred uint32

	drainErr := d.calls.result(d.iface, operation, &transferred, true)
	if errors.Is(drainErr, windows.ERROR_OPERATION_ABORTED) {
		drainErr = nil
	}

	return errors.Join(reason, err, drainErr)
}

func windowsWaitMilliseconds(deadline time.Time) uint32 {
	if deadline.IsZero() {
		return windows.INFINITE
	}

	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0
	}

	if remaining >= time.Duration(windows.INFINITE-1)*time.Millisecond {
		return windows.INFINITE - 1
	}

	return uint32((remaining + time.Millisecond - 1) / time.Millisecond)
}
