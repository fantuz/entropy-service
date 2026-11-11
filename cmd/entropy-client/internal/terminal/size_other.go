//go:build !linux

package terminal

import "os"

// Unknown terminal size deliberately selects scrolling output. Windows config
// rejects explicit colors/full layouts/waterfalls; ordinary text still uses stdout.
func dimensions(_ *os.File) (columns, rows int, tty bool) {
	return 0, 0, false
}
