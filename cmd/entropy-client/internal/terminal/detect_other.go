//go:build !windows

package terminal

import "os"

func detectDefault(file *os.File, choice string) Profile {
	columns, rows, tty := dimensions(file)
	term := os.Getenv("TERM")

	return Profile{
		Color:       resolveColor(choice, tty, term, os.Getenv("COLORTERM"), os.Getenv("NO_COLOR") != ""),
		Interactive: tty && term != dumbTerminal && term != "",
		Columns:     columns,
		Rows:        rows,
	}
}
