//go:build !windows

package main

import (
	"fmt"
	"io"
)

func runServiceCommand(_ []string, _ io.Writer, diagnostics io.Writer) int {
	fmt.Fprintln(diagnostics, "service management requires Windows; on Linux use the packaged systemd unit")
	return 2
}
