// Package terminal detects display capabilities without changing stream data.
package terminal

import (
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

type Color uint8

const (
	Monochrome Color = iota
	ANSI16
	ANSI256
	TrueColor
)

const (
	colorAuto    = "auto"
	colorAlways  = "always"
	colorNever   = "never"
	color256     = "256"
	colorTrue    = "truecolor"
	color24Bit   = "24bit"
	dumbTerminal = "dumb"
)

type Profile struct {
	Color       Color
	Interactive bool
	Columns     int
	Rows        int
}

func ValidColor(value string) bool {
	switch value {
	case "", colorAuto, colorAlways, colorNever, color256, colorTrue:
		return true
	default:
		return false
	}
}

func Detect(choice string) Profile {
	columns, rows, tty := dimensions(os.Stdout)
	term := os.Getenv("TERM")

	return Profile{
		Color:       resolveColor(choice, tty, term, os.Getenv("COLORTERM"), os.Getenv("NO_COLOR") != ""),
		Interactive: tty && term != dumbTerminal && term != "",
		Columns:     columns,
		Rows:        rows,
	}
}

func resolveColor(choice string, tty bool, term, colorTerm string, noColor bool) Color {
	switch choice {
	case colorNever:
		return Monochrome
	case colorTrue:
		return TrueColor
	case color256:
		return ANSI256
	case "", colorAuto:
		if !tty || noColor || term == dumbTerminal || term == "" {
			return Monochrome
		}
	}

	colorTerm = strings.ToLower(colorTerm)
	if colorTerm == colorTrue || colorTerm == color24Bit || strings.HasSuffix(term, "-direct") {
		return TrueColor
	}

	if strings.Contains(term, "256color") {
		return ANSI256
	}

	return ANSI16
}

func (p Profile) Paint(code, text string) string {
	if p.Color == Monochrome {
		return text
	}

	return code + text + "\033[0m"
}

func (p Profile) Clear() string {
	if p.Interactive {
		return "\033[H\033[2J"
	}

	return ""
}

var controlSequence = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

// TextRows counts monitor rows, including wrapped ASCII text.
// Colour and cursor sequences occupy no display columns.
func TextRows(text string, columns int) int {
	text = controlSequence.ReplaceAllString(text, "")
	if text == "" {
		return 0
	}

	text = strings.TrimSuffix(text, "\n")

	rows := 0

	for line := range strings.SplitSeq(text, "\n") {
		n := utf8.RuneCountInString(line)
		if columns > 0 {
			rows += max(1, (n+columns-1)/columns)
		} else {
			rows++
		}
	}

	return rows
}
