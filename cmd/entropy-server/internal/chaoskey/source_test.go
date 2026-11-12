package chaoskey

import (
	"bytes"
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

type fakeDevice struct {
	bulk     func([]byte) (int, error)
	deadline time.Time
	closed   bool
	buffer   []byte
	controls int
}

func (d *fakeDevice) Bulk(p []byte) (int, error) { d.buffer = p; return d.bulk(p) }
func (d *fakeDevice) Control(byte, []byte) (int, error) {
	d.controls++
	return 0, errors.New("ChaosKey has no Quantis control protocol")
}
func (d *fakeDevice) SetReadDeadline(deadline time.Time) error { d.deadline = deadline; return nil }
func (d *fakeDevice) Close() error                             { d.closed = true; return nil }

func TestCookedReadAcceptsShortPacketsAndClearsUnusedData(t *testing.T) {
	calls := 0
	device := &fakeDevice{bulk: func(p []byte) (int, error) {
		calls++

		if len(p) != 64 {
			t.Fatalf("unaligned request: %d", len(p))
		}

		for i := range p {
			p[i] = byte(calls)
		}

		return 32, nil
	}}

	source, err := newSource(device, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()

	if err := source.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	for _, size := range []int{3, 50} {
		p := make([]byte, size)

		n, err := source.Read(p)
		if err != nil || n != min(size, 32) || !bytes.Equal(p[:n], bytes.Repeat([]byte{byte(calls)}, n)) {
			t.Fatalf("short transfer: %d %v %x", n, err, p)
		}

		if !bytes.Equal(device.buffer, make([]byte, 64)) {
			t.Fatal("packet buffer was not cleared")
		}
	}

	if device.controls != 0 || calls != 2 {
		t.Fatal("unexpected control command or tail reuse")
	}
}

func TestCookedReadRejectsStarvationFailuresAndLateData(t *testing.T) {
	for _, tc := range []struct {
		name string
		n    int
		err  error
		late bool
	}{
		{"zero", 0, nil, false},
		{"negative", -1, nil, false},
		{"oversized", 65, nil, false},
		{"failure with bytes", 16, io.ErrUnexpectedEOF, false},
		{"deadline", 16, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			device := &fakeDevice{bulk: func(p []byte) (int, error) {
				for i := range p {
					p[i] = 0xff
				}

				if tc.late {
					time.Sleep(30 * time.Millisecond)
				}

				return tc.n, tc.err
			}}

			source, err := newSource(device, 64)
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close()

			if tc.late {
				if err := source.SetReadDeadline(time.Now().Add(10 * time.Millisecond)); err != nil {
					t.Fatal(err)
				}
			}

			p := make([]byte, 64)

			n, err := source.Read(p)
			if n != 0 || err == nil || !bytes.Equal(p, make([]byte, 64)) || !bytes.Equal(device.buffer, make([]byte, 64)) {
				t.Fatalf("failed acquisition escaped: %d %v %x", n, err, p)
			}

			if tc.late && !errors.Is(err, os.ErrDeadlineExceeded) {
				t.Fatal(err)
			}
		})
	}
}

func TestCookedBoundsCloseAndSelectors(t *testing.T) {
	device := &fakeDevice{bulk: func(p []byte) (int, error) {
		if len(p) > maxRead {
			t.Fatal(len(p))
		}

		return len(p), nil
	}}

	source, err := newSource(device, 64)
	if err != nil {
		t.Fatal(err)
	}

	if n, err := source.Read(make([]byte, maxRead+1)); n != maxRead || err != nil {
		t.Fatal(n, err)
	}

	if err := source.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := source.Read(make([]byte, 1)); !errors.Is(err, os.ErrClosed) {
		t.Fatal(err)
	}

	if err := source.SetReadDeadline(time.Time{}); !errors.Is(err, os.ErrClosed) {
		t.Fatal(err)
	}

	if !device.closed {
		t.Fatal("transport not closed")
	}

	for _, selector := range []string{"chaoskey:0", "chaoskey-usb", "chaoskey-usb:-1", "chaoskey-usb:127", "chaoskey-usb:00", "chaoskey-usb:0/file"} {
		if _, selected, err := ParseSelector(selector); !selected || err == nil {
			t.Fatal(selector, selected, err)
		}
	}

	if index, selected, err := ParseSelector("chaoskey-usb:0"); index != 0 || !selected || err != nil {
		t.Fatal(index, selected, err)
	}

	if _, selected, _ := ParseSelector("/dev/chaoskey0"); selected {
		t.Fatal("kernel node conflated with direct USB")
	}
}
