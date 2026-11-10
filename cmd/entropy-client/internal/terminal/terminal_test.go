package terminal

import (
	"strings"
	"testing"
)

func TestColorSelection(t *testing.T) {
	for _, tc := range []struct {
		name, choice, term, colorTerm string
		tty, noColor                  bool
		want                          Color
	}{
		{"pipe", "auto", "xterm-256color", "truecolor", false, false, Monochrome},
		{"no-color", "auto", "xterm-256color", "truecolor", true, true, Monochrome},
		{"dumb", "auto", "dumb", "truecolor", true, false, Monochrome},
		{"unset-term", "auto", "", "truecolor", true, false, Monochrome},
		{"basic", "auto", "xterm", "", true, false, ANSI16},
		{"palette", "auto", "xterm-256color", "", true, false, ANSI256},
		{"truecolor", "auto", "xterm-256color", "truecolor", true, false, TrueColor},
		{"24bit", "auto", "xterm", "24BIT", true, false, TrueColor},
		{"direct", "auto", "xterm-direct", "", true, false, TrueColor},
		{"empty-choice", "", "xterm", "", true, false, ANSI16},
		{"never", "never", "xterm-direct", "truecolor", true, false, Monochrome},
		{"always", "always", "dumb", "", false, true, ANSI16},
		{"always-palette", "always", "xterm-256color", "", false, true, ANSI256},
		{"always-truecolor", "always", "xterm", "truecolor", false, true, TrueColor},
		{"force-palette", "256", "dumb", "truecolor", false, true, ANSI256},
		{"force-truecolor", "truecolor", "dumb", "", false, true, TrueColor},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveColor(tc.choice, tc.tty, tc.term, tc.colorTerm, tc.noColor); got != tc.want {
				t.Fatalf("color = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestValidColor(t *testing.T) {
	for _, value := range []string{"", "auto", "always", "never", "256", "truecolor"} {
		if !ValidColor(value) {
			t.Errorf("rejected %q", value)
		}
	}

	for _, value := range []string{"AUTO", "16", "24bit", "yes"} {
		if ValidColor(value) {
			t.Errorf("accepted %q", value)
		}
	}
}

func TestProfileStylesAndCursorAreIndependent(t *testing.T) {
	profile := Profile{Color: TrueColor}
	if profile.Paint("\x1b[31m", "WARN") != "\x1b[31mWARN\x1b[0m" || profile.Clear() != "" {
		t.Fatal("forced colors on a pipe must not enable cursor controls")
	}

	profile = Profile{Interactive: true}
	if profile.Paint("\x1b[31m", "WARN") != "WARN" || profile.Clear() != "\x1b[H\x1b[2J" {
		t.Fatal("a monochrome terminal should still redraw interactively")
	}
}

func TestTextRowsIgnoresANSIAndCountsWrappedLines(t *testing.T) {
	for _, tc := range []struct {
		text    string
		columns int
		want    int
	}{
		{"", 80, 0},
		{"\n", 80, 1},
		{"one\n\ntwo\n", 80, 3},
		{strings.Repeat("a", 80) + "\n", 80, 1},
		{strings.Repeat("a", 81) + "\n", 80, 2},
		{"\x1b[H\x1b[2J\x1b[48;2;255;0;0m" + strings.Repeat("a", 60) + "\x1b[0m\n", 60, 1},
		{"\x1b[31mWARN\x1b[0m\n", 4, 1},
		{"αβγ\n", 2, 2},
		{"abc\ndef", 0, 2},
	} {
		if got := TextRows(tc.text, tc.columns); got != tc.want {
			t.Errorf("TextRows(%q, %d) = %d, want %d", tc.text, tc.columns, got, tc.want)
		}
	}
}

func TestFullLayoutSelection(t *testing.T) {
	for _, tc := range []struct {
		columns, rows int
		tty           bool
		choice        string
		want          bool
	}{
		{80, 24, true, "auto", false},
		{120, 30, true, "auto", true},
		{240, 60, true, "compact", false},
		{80, 24, true, "full", true},
		{60, 24, true, "full", false},
		{160, 20, true, "full", false},
		{240, 60, false, "full", false},
	} {
		if got := (Profile{Columns: tc.columns, Rows: tc.rows, Interactive: tc.tty}).FullLayout(tc.choice); got != tc.want {
			t.Errorf("layout case %+v = %v", tc, got)
		}
	}
}

func TestFitPreservesANSIAndMarksClipping(t *testing.T) {
	for _, tc := range []struct {
		text  string
		width int
		plain string
	}{
		{"abc", 5, "abc  "},
		{"abcdef", 4, "abc~"},
		{"\x1b[31mWARN\x1b[0m", 6, "WARN  "},
		{"\x1b[31mabcdef\x1b[0m", 4, "abc~"},
		{"█▆▃", 2, "█~"},
		{"abc", 0, ""},
	} {
		text := Fit(tc.text, tc.width)
		if got := controlSequence.ReplaceAllString(text, ""); got != tc.plain {
			t.Errorf("Fit(%q,%d)=%q", tc.text, tc.width, got)
		}

		if strings.ContainsRune(controlSequence.ReplaceAllString(text, ""), '\x1b') {
			t.Fatal("partial ANSI sequence survived clipping")
		}
	}
}
