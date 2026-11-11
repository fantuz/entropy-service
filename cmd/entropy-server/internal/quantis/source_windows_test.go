package quantis

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestLegacyIdentityAndModuleHealth(t *testing.T) {
	for _, instance := range []string{
		`PCI\VEN_179A&DEV_0001&SUBSYS_00000000\1`,
		`pci\ven_179a&dev_0001&subsys_00000000\2`,
	} {
		if !legacyInstance(instance) {
			t.Fatalf("legacy PCI identity rejected: %q", instance)
		}
	}

	for _, instance := range []string{
		`PCI\VEN_10EE&DEV_7012&SUBSYS_00000000\1`,
		`PCI\VEN_179A&DEV_00010&SUBSYS_00000000\1`,
		`USB\VEN_179A&DEV_0001&SUBSYS_00000000\1`,
		`C:\untrusted\PCI\VEN_179A&DEV_0001&`,
	} {
		if legacyInstance(instance) {
			t.Fatalf("unrelated device accepted: %q", instance)
		}
	}

	for _, tc := range []struct {
		mask, status uint32
		ok           bool
	}{
		{1, 1, true},
		{0xf, 0xf, true},
		{5, 5, true},
		{0, 0, false},
		{1, 0, false},
		{3, 1, false},
		{0x10, 0x10, false},
		{1, 3, false},
		{1, 0xffffffff, false},
	} {
		if err := validateModules(tc.mask, tc.status); (err == nil) != tc.ok {
			t.Fatalf("module policy for %+v: %v", tc, err)
		}
	}
}

// A real Windows overlapped named pipe tests Win32 read, wait and cancellation
// mechanics without simulating a QRNG or claiming that a vendor driver works.
func pipeSource(t *testing.T) (*Source, windows.Handle) {
	t.Helper()

	name, err := windows.UTF16PtrFromString(fmt.Sprintf(`\\.\pipe\entropy-quantis-test-%d-%d`, os.Getpid(), time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}

	handle, err := windows.CreateNamedPipe(name,
		windows.PIPE_ACCESS_INBOUND|windows.FILE_FLAG_OVERLAPPED|windows.FILE_FLAG_FIRST_PIPE_INSTANCE,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS,
		1, 0, 65536, 0, nil)
	if err != nil {
		t.Fatal(err)
	}

	source, err := newSource(handle)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = source.Close() })

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

	operation := &windows.Overlapped{HEvent: event}

	err = windows.ConnectNamedPipe(handle, operation)
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		var transferred uint32

		err = windows.GetOverlappedResult(handle, operation, &transferred, true)
	}

	if err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
		t.Fatal(err)
	}

	return source, writer
}

func readPipe(source *Source, buffer []byte) (int, error) {
	source.readMu.Lock()
	defer source.readMu.Unlock()

	return source.transfer(buffer, 0)
}

func writePipe(t *testing.T, writer windows.Handle, data []byte) {
	t.Helper()

	var written uint32
	if err := windows.WriteFile(writer, data, &written, nil); err != nil || int(written) != len(data) {
		t.Fatalf("pipe write: %d, %v", written, err)
	}
}

func TestOverlappedTimeoutDrainsAndSourceCanBeReused(t *testing.T) {
	source, writer := pipeSource(t)
	if err := source.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}

	buffer := make([]byte, 64)
	if n, err := readPipe(source, buffer); n != 0 || !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("pending read was not canceled: %d, %v", n, err)
	}

	if err := source.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	want := []byte("next-seed-after-cancellation")
	writePipe(t, writer, want)

	n, err := readPipe(source, buffer)
	if err != nil || n != len(want) || !bytes.Equal(buffer[:n], want) {
		t.Fatalf("canceled operation consumed later bytes: %d, %v, %q", n, err, buffer[:n])
	}
}

func TestOverlappedDeadlineChangesAndCloseInterruptWait(t *testing.T) {
	for _, closeSource := range []bool{false, true} {
		t.Run(fmt.Sprintf("close=%t", closeSource), func(t *testing.T) {
			source, _ := pipeSource(t)
			if err := source.SetReadDeadline(time.Now().Add(time.Minute)); err != nil {
				t.Fatal(err)
			}

			started := make(chan struct{})
			done := make(chan error, 1)

			go func() {
				close(started)

				_, err := readPipe(source, make([]byte, 64))
				done <- err
			}()

			<-started
			// Allow the empty pipe read to enter the native wait. Correctness
			// also holds if a heavily loaded runner has not submitted it yet.
			time.Sleep(20 * time.Millisecond)

			want := os.ErrDeadlineExceeded

			var err error

			if closeSource {
				want = os.ErrClosed
				err = source.Close()
			} else {
				err = source.SetReadDeadline(time.Now())
			}

			if err != nil {
				t.Fatal(err)
			}

			select {
			case err := <-done:
				if !errors.Is(err, want) {
					t.Fatalf("wrong interruption: %v, want %v", err, want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("overlapped read did not join after interruption")
			}

			if err := source.Close(); err != nil {
				t.Fatal(err)
			}

			if err := source.SetReadDeadline(time.Time{}); !errors.Is(err, os.ErrClosed) {
				t.Fatalf("closed source accepted a deadline: %v", err)
			}
		})
	}
}

func TestOverlappedDeadlineCanBeExtendedAndCleared(t *testing.T) {
	source, writer := pipeSource(t)
	if err := source.SetReadDeadline(time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)

	go func() {
		buffer := make([]byte, 8)

		n, err := readPipe(source, buffer)
		if err == nil && (n != 1 || buffer[0] != 42) {
			err = fmt.Errorf("unexpected pipe data: %d, %x", n, buffer)
		}

		done <- err
	}()

	for _, deadline := range []time.Time{time.Now().Add(2 * time.Minute), {}} {
		if err := source.SetReadDeadline(deadline); err != nil {
			t.Fatal(err)
		}
	}

	runtime.GC() // Native operations retain pinned memory across Go GC.
	writePipe(t, writer, []byte{42})

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("deadline change lost the successful completion")
	}
}

func TestNonQuantisHandleIsRejectedBeforeReading(t *testing.T) {
	source, _ := pipeSource(t)
	if err := source.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	buffer := bytes.Repeat([]byte{0xab}, 64)
	if n, err := source.Read(buffer); n != 0 || err == nil {
		t.Fatalf("non-Quantis handle accepted: %d, %v", n, err)
	}

	if !bytes.Equal(buffer, bytes.Repeat([]byte{0xab}, 64)) {
		t.Fatal("failed module query exposed seed bytes")
	}
}

func TestWaitTimeoutRounding(t *testing.T) {
	if waitMilliseconds(time.Time{}) != windows.INFINITE || waitMilliseconds(time.Now().Add(-time.Second)) != 0 {
		t.Fatal("unbounded/expired wait conversion")
	}

	if got := waitMilliseconds(time.Now().Add(time.Second)); got == 0 || got > 1000 {
		t.Fatalf("wrong millisecond conversion: %d", got)
	}

	for _, duration := range []time.Duration{2000 * time.Hour, 1<<63 - 1} {
		if got := waitMilliseconds(time.Now().Add(duration)); got != windows.INFINITE-1 {
			t.Fatalf("finite timeout overflowed to INFINITE: %d", got)
		}
	}
}

func TestDiscoveryReportsMissingLegacyDriver(t *testing.T) {
	paths, err := devicePaths()
	if err != nil {
		t.Fatalf("Windows device discovery failed: %v", err)
	}

	if len(paths) > 0 {
		t.Skip("legacy hardware detected; this test does not open a physical card")
	}

	if source, err := Open(0); err == nil || source != nil {
		t.Fatalf("missing legacy driver/card accepted: source=%v error=%v", source, err)
	}
}
