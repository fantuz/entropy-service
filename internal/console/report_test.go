package console

import (
	"strings"
	"testing"
)

func TestOperatorValuesCannotInjectLinesOrTerminalCommands(t *testing.T) {
	var out strings.Builder

	r := New(&out, "startup")
	r.Value("source", "/dev/example\nFORGED OK\x1b[2J\r\a")
	r.Value("absent", "")

	text := out.String()
	if strings.ContainsAny(text, "\x1b\r\a") || strings.Contains(text, "\nFORGED") || !strings.Contains(text, `\nFORGED OK\x1b[2J\r\a`) || !strings.Contains(text, "(none)") {
		t.Fatal(text)
	}
}
