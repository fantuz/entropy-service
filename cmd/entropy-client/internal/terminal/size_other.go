//go:build !linux && !windows

package terminal

import "os"

// Unknown terminal size deliberately selects scrolling output.
func dimensions(_ *os.File) (columns, rows int, tty bool) {
	return 0, 0, false
}

func platformDetect(file *os.File, choice string) Profile { return detectDefault(file, choice) }

// Prepare leaves unsupported consoles in their existing scrolling mode.
func Prepare(_ *os.File) func() { return func() {} }
