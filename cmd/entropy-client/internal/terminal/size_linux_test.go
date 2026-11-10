package terminal

import (
	"fmt"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDetectTTYAndResize(t *testing.T) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()

	fd := int(master.Fd())
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}

	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}

	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer slave.Close()

	original := os.Stdout
	os.Stdout = slave

	t.Cleanup(func() { os.Stdout = original })
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("NO_COLOR", "")

	for _, columns := range []uint16{80, 60, 100} {
		if err := unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Col: columns, Row: 24}); err != nil {
			t.Fatal(err)
		}

		profile := Detect("auto")
		if profile.Columns != int(columns) || profile.Rows != 24 || !profile.Interactive || profile.Color != TrueColor {
			t.Fatalf("TTY resize/capabilities not detected: %+v", profile)
		}
	}

	t.Setenv("NO_COLOR", "1")

	if got := Detect("auto"); got.Color != Monochrome || !got.Interactive {
		t.Fatalf("NO_COLOR changed cursor capability: %+v", got)
	}

	t.Setenv("TERM", "dumb")

	if got := Detect("truecolor"); got.Color != TrueColor || got.Interactive {
		t.Fatalf("forced colors changed dumb-terminal cursor capability: %+v", got)
	}
}

func TestDetectRedirectedStdout(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	original := os.Stdout
	os.Stdout = file

	t.Cleanup(func() { os.Stdout = original })
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("NO_COLOR", "")

	if got := Detect("auto"); got != (Profile{}) {
		t.Fatalf("redirected output was treated as a terminal: %+v", got)
	}

	if got := Detect("always"); got.Color != TrueColor || got.Interactive {
		t.Fatalf("forced color on redirected output: %+v", got)
	}
}
