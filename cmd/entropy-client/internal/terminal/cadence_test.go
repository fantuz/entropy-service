package terminal

import (
	"testing"
	"time"
)

func TestInteractiveDisplayAllowsTwentyFPSAndCapsFasterRequests(t *testing.T) {
	for _, size := range [][2]int{{60, 24}, {80, 24}, {240, 60}} {
		profile := Profile{Interactive: true, Columns: size[0], Rows: size[1]}
		for _, tc := range []struct{ requested, want time.Duration }{
			{time.Millisecond, 50 * time.Millisecond},
			{49 * time.Millisecond, 50 * time.Millisecond},
			{50 * time.Millisecond, 50 * time.Millisecond},
			{time.Second, time.Second},
		} {
			if got := profile.DisplayInterval(tc.requested); got != tc.want {
				t.Fatalf("%dx%d requested %s: got %s, want %s", size[0], size[1], tc.requested, got, tc.want)
			}
		}
	}

	if got := (Profile{}).DisplayInterval(time.Millisecond); got != time.Millisecond {
		t.Fatal("redirected output cadence changed")
	}
}
