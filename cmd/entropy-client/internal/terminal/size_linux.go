package terminal

import (
	"os"

	"golang.org/x/sys/unix"
)

func dimensions(file *os.File) (columns, rows int, tty bool) {
	size, err := unix.IoctlGetWinsize(int(file.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return 0, 0, false
	}

	return int(size.Col), int(size.Row), true
}
