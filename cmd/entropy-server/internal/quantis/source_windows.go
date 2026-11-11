package quantis

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

const (
	// CTL_CODE(FILE_DEVICE_UNKNOWN, function, METHOD_BUFFERED, FILE_ANY_ACCESS).
	ioctlModulesMask   = 0x00222004 // function 0x801, four-byte output
	ioctlModulesStatus = 0x0022201c // function 0x807, four-byte output
	maxRead            = 32 << 10
)

// Source serializes reads and honours deadline changes and Close while waiting
// for overlapped I/O. A canceled operation is always drained before its memory
// or handle is released. A broken kernel driver can still prevent cancellation;
// the supplied repository does not include the legacy PCI kernel source.
type Source struct {
	readMu   sync.Mutex
	mu       sync.Mutex
	handle   windows.Handle
	changed  windows.Handle
	deadline time.Time
	closed   bool
	closeErr error
}

// newSource takes ownership of handle even if initialization fails.
func newSource(handle windows.Handle) (*Source, error) {
	changed, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		return nil, errors.Join(err, windows.CloseHandle(handle))
	}

	return &Source{handle: handle, changed: changed}, nil
}

func (s *Source) state() (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return s.deadline, os.ErrClosed
	}

	if !s.deadline.IsZero() && !time.Now().Before(s.deadline) {
		return s.deadline, os.ErrDeadlineExceeded
	}

	return s.deadline, nil
}

func (s *Source) SetReadDeadline(deadline time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return os.ErrClosed
	}

	s.deadline = deadline

	return windows.SetEvent(s.changed)
}

func (s *Source) Close() error {
	s.mu.Lock()
	s.closed = true
	// Keep this handle alive until the active read has observed closure and
	// canceled/drained its operation. Repeated closes join that same read.
	if s.changed != 0 {
		_ = windows.SetEvent(s.changed)
	}
	s.mu.Unlock()

	s.readMu.Lock()
	defer s.readMu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.changed != 0 {
		s.closeErr = errors.Join(windows.CloseHandle(s.handle), windows.CloseHandle(s.changed))
		s.changed = 0
	}

	return s.closeErr
}

func (s *Source) Read(p []byte) (int, error) {
	s.readMu.Lock()
	defer s.readMu.Unlock()

	if _, err := s.state(); err != nil {
		return 0, err
	}

	if len(p) == 0 {
		return 0, nil
	}

	if err := s.checkModules(); err != nil {
		return 0, fmt.Errorf("legacy Quantis PCI module status: %w", err)
	}

	// Never expose a failed, partial or late operation to the seed caller.
	buffer := make([]byte, min(len(p), maxRead))
	defer clear(buffer)

	n, err := s.transfer(buffer, 0)
	if err != nil {
		return 0, fmt.Errorf("legacy Quantis PCI read (overlapped driver support required): %w", err)
	}

	if n == 0 {
		return 0, io.ErrNoProgress
	}

	return copy(p, buffer[:n]), nil
}

func (s *Source) checkModules() error {
	var values [2]uint32

	for i, code := range []uint32{ioctlModulesMask, ioctlModulesStatus} {
		buffer := make([]byte, 4)

		n, err := s.transfer(buffer, code)
		if err != nil {
			return err
		}

		if n != len(buffer) {
			return io.ErrUnexpectedEOF
		}

		values[i] = binary.LittleEndian.Uint32(buffer)
	}

	return validateModules(values[0], values[1])
}

func validateModules(mask, status uint32) error {
	if mask == 0 || mask&^uint32(0xf) != 0 || status != mask {
		return fmt.Errorf("all installed modules must be enabled and healthy (mask=0x%x, status=0x%x)", mask, status)
	}

	return nil
}

// transfer is called with readMu held. code==0 submits ReadFile; otherwise it
// submits a read-only status IOCTL. No board reset/module-enable IOCTL is sent.
func (s *Source) transfer(buffer []byte, code uint32) (int, error) {
	if _, err := s.state(); err != nil {
		return 0, err
	}

	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = windows.CloseHandle(event) }()

	operation := &windows.Overlapped{HEvent: event}
	defer func() {
		// Both addresses must remain valid until completion/cancellation has
		// been drained, including error returns from the wait path.
		runtime.KeepAlive(operation)
		runtime.KeepAlive(buffer)
	}()

	var transferred uint32

	if code == 0 {
		err = windows.ReadFile(s.handle, buffer, &transferred, operation)
	} else {
		err = windows.DeviceIoControl(s.handle, code, nil, 0, &buffer[0], uint32(len(buffer)), &transferred, operation)
	}

	if err != nil && !errors.Is(err, windows.ERROR_IO_PENDING) {
		return 0, err
	}

	if errors.Is(err, windows.ERROR_IO_PENDING) {
		if err := s.wait(operation); err != nil {
			return 0, err
		}
	}

	if err := windows.GetOverlappedResult(s.handle, operation, &transferred, false); err != nil {
		if errors.Is(err, windows.ERROR_IO_INCOMPLETE) {
			return 0, s.cancelAndDrain(operation, err)
		}

		return 0, err
	}

	if _, err := s.state(); err != nil {
		return 0, err
	}

	if uint64(transferred) > uint64(len(buffer)) {
		return 0, errors.New("legacy Quantis PCI driver returned an invalid byte count")
	}

	return int(transferred), nil
}

func (s *Source) wait(operation *windows.Overlapped) error {
	for {
		deadline, err := s.state()
		if err != nil {
			return s.cancelAndDrain(operation, err)
		}

		result, err := windows.WaitForMultipleObjects([]windows.Handle{operation.HEvent, s.changed},
			false, waitMilliseconds(deadline))
		if err != nil {
			return s.cancelAndDrain(operation, err)
		}

		switch result {
		case windows.WAIT_OBJECT_0:
			return nil
		case windows.WAIT_OBJECT_0 + 1, uint32(windows.WAIT_TIMEOUT):
			// Re-read state after a deadline change or timer wakeup. A changed
			// deadline may extend the wait, not just cancel it.
		default:
			return s.cancelAndDrain(operation, fmt.Errorf("unexpected Quantis PCI wait result 0x%x", result))
		}
	}
}

func (s *Source) cancelAndDrain(operation *windows.Overlapped, reason error) error {
	cancelErr := windows.CancelIoEx(s.handle, operation)
	if errors.Is(cancelErr, windows.ERROR_NOT_FOUND) {
		cancelErr = nil // Completion raced with cancellation; still drain it.
	}

	var transferred uint32

	drainErr := windows.GetOverlappedResult(s.handle, operation, &transferred, true)
	if errors.Is(drainErr, windows.ERROR_OPERATION_ABORTED) {
		drainErr = nil
	}

	return errors.Join(reason, cancelErr, drainErr)
}

func waitMilliseconds(deadline time.Time) uint32 {
	if deadline.IsZero() {
		return windows.INFINITE
	}

	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0
	}

	return uint32(min((remaining+time.Millisecond-1)/time.Millisecond, time.Duration(windows.INFINITE-1)))
}
