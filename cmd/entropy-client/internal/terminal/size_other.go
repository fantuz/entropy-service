//go:build !linux

package terminal

import "os"

// Packaged releases target Linux. Other systems can select colors explicitly.
func dimensions(_ *os.File) (columns, rows int, tty bool) {
	return 0, 0, false
}
