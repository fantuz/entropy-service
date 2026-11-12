package terminal

import (
	"os"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func privateConsole(t *testing.T) *os.File {
	t.Helper()

	kernel := windows.NewLazySystemDLL("kernel32.dll")

	allocated, _, _ := kernel.NewProc("AllocConsole").Call()
	if allocated != 0 {
		t.Cleanup(func() { _, _, _ = kernel.NewProc("FreeConsole").Call() })
	}

	handle, _, err := kernel.NewProc("CreateConsoleScreenBuffer").Call(windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, 0, 1, 0)
	if handle == ^uintptr(0) {
		t.Fatalf("create isolated console buffer: %v", err)
	}

	file := os.NewFile(handle, "private console")

	t.Cleanup(func() { _ = file.Close() })
	// A larger backing buffer must not be mistaken for the visible viewport.
	r, _, err := kernel.NewProc("SetConsoleScreenBufferSize").Call(handle, 160|100<<16)
	if r == 0 {
		t.Fatal(err)
	}

	window := windows.SmallRect{Right: 79, Bottom: 19}

	r, _, err = kernel.NewProc("SetConsoleWindowInfo").Call(handle, 1, uintptr(unsafe.Pointer(&window)))
	if r == 0 {
		t.Fatal(err)
	}

	return file
}

func TestWindowsConsoleViewportVTAndRestoration(t *testing.T) {
	file := privateConsole(t)
	handle := windows.Handle(file.Fd())

	const original = windows.ENABLE_PROCESSED_OUTPUT | windows.ENABLE_WRAP_AT_EOL_OUTPUT
	if err := windows.SetConsoleMode(handle, original); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TERM", "")
	t.Setenv("COLORTERM", "")
	t.Setenv("NO_COLOR", "")

	restore := Prepare(file)
	defer restore()

	profile := platformDetect(file, "auto")
	if !profile.Interactive || profile.Color != Monochrome || profile.Columns != 79 || profile.Rows != 20 {
		t.Fatalf("wrong console/viewport policy: %+v", profile)
	}

	for choice, want := range map[string]Color{"never": Monochrome, "always": ANSI16, "256": ANSI256, "truecolor": TrueColor} {
		if got := platformDetect(file, choice); !got.Interactive || got.Color != want {
			t.Fatalf("palette %q = %+v", choice, got)
		}
	}

	window := windows.SmallRect{Right: 99, Bottom: 29}

	r, _, resizeErr := windows.NewLazySystemDLL("kernel32.dll").NewProc("SetConsoleWindowInfo").Call(uintptr(handle), 1, uintptr(unsafe.Pointer(&window)))
	if r == 0 {
		t.Fatal(resizeErr)
	}

	if got := platformDetect(file, "never"); got.Columns != 99 || got.Rows != 30 {
		t.Fatalf("live viewport resize not detected: %+v", got)
	}

	t.Setenv("TERM", "dumb")

	if platformDetect(file, "truecolor").Interactive {
		t.Fatal("dumb console enabled cursor addressing")
	}

	restore()

	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil || mode != original {
		t.Fatalf("console mode not restored: %x %v", mode, err)
	}
}

func TestWindowsRedirectedOutputNeverGetsVTOrForcedColors(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "redirected")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	Prepare(file)()
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "truecolor")

	for _, choice := range []string{"auto", "never", "always", "256", "truecolor"} {
		if got := platformDetect(file, choice); got != (Profile{}) {
			t.Fatalf("redirected %q got console capability: %+v", choice, got)
		}
	}
}
