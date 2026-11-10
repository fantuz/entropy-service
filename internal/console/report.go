// Package console formats human-readable invocation reports for both utilities.
package console

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
)

// Report uses plain text so captured startup reports need no terminal emulator.
type Report struct {
	out io.Writer
}

func New(out io.Writer, title string) *Report {
	r := &Report{out: out}
	fmt.Fprintf(out, "\n%s\n%s\n", OneLine(title), strings.Repeat("=", 64))

	return r
}

func (r *Report) Section(title string) {
	fmt.Fprintf(r.out, "\n%s\n", OneLine(title))
}

func (r *Report) Value(label string, value any) {
	text := fmt.Sprint(value)
	if text == "" {
		text = "(none)"
	}

	fmt.Fprintf(r.out, "  %-22s %s\n", OneLine(label), OneLine(text))
}

// Check reserves OK for an observed result; OFF/SKIP are not successful checks.
func (r *Report) Check(label, state, detail string) {
	r.Value(label, fmt.Sprintf("[%s] %s", state, detail))
}

// OneLine escapes controls in operator-provided paths/URLs instead of allowing
// them to inject extra log lines or terminal commands.
func OneLine(value string) string {
	if strings.IndexFunc(value, unicode.IsControl) < 0 {
		return value
	}

	quoted := strconv.QuoteToASCII(value)

	return quoted[1 : len(quoted)-1]
}
