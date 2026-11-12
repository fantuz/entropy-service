package terminal

import (
	"os"

	"golang.org/x/sys/windows"
)

// Prepare enables VT cursor control only on a real console. Its restoration
// removes only flags this session added; pipes/files are never modified.
func Prepare(file *os.File) func() {
	handle := windows.Handle(file.Fd())

	var original uint32
	if err := windows.GetConsoleMode(handle, &original); err != nil {
		return func() {}
	}

	const wanted = windows.ENABLE_PROCESSED_OUTPUT | windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING

	added := uint32(wanted) &^ original
	if added == 0 || windows.SetConsoleMode(handle, original|wanted) != nil {
		return func() {}
	}

	return func() {
		var current uint32
		if windows.GetConsoleMode(handle, &current) == nil {
			_ = windows.SetConsoleMode(handle, current&^added)
		}
	}
}

func dimensions(file *os.File) (columns, rows int, tty bool) {
	handle := windows.Handle(file.Fd())

	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return 0, 0, false
	}

	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(handle, &info); err != nil {
		return 0, 0, false
	}

	// Measure the viewport, not scrollback. One spare column avoids classic
	// console immediate-EOL wrapping followed by a second newline advance.
	columns = max(0, int(info.Window.Right-info.Window.Left))
	rows = max(0, int(info.Window.Bottom-info.Window.Top)+1)

	return columns, rows, mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0
}

func platformDetect(file *os.File, choice string) Profile {
	columns, rows, vt := dimensions(file)
	if !vt || os.Getenv("TERM") == dumbTerminal {
		return Profile{Columns: columns}
	}

	// Auto deliberately starts monochrome. Modern Windows Terminal/ConHost
	// users can explicitly request a palette after successful VT detection.
	color := Monochrome
	if choice != "" && choice != colorAuto {
		color = resolveColor(choice, true, "windows-vt", os.Getenv("COLORTERM"), os.Getenv("NO_COLOR") != "")
	}

	return Profile{Columns: columns, Rows: rows, Interactive: true, Color: color}
}
