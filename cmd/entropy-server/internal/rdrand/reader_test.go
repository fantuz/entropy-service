package rdrand

import (
	"bytes"
	"errors"
	"testing"
)

func TestDirectReaderWordOrderAndTail(t *testing.T) {
	for _, size := range []int{0, 1, 7, 8, 9, 17} {
		calls := 0
		r := Reader{next: func() (uint64, error) {
			calls++
			return 0x0807060504030201, nil
		}}
		buf := make([]byte, size)

		n, err := r.Read(buf)
		if n != size || err != nil || calls != (size+7)/8 {
			t.Fatalf("size %d: n=%d, calls=%d, error=%v", size, n, calls, err)
		}

		for i, value := range buf {
			if value != byte(i%8+1) {
				t.Fatalf("byte %d: %d", i, value)
			}
		}
	}
}

func TestDirectReaderDiscardsPartialOutput(t *testing.T) {
	calls := 0
	r := Reader{next: func() (uint64, error) {
		calls++
		if calls == 2 {
			return 0, ErrUnavailable
		}

		return ^uint64(0), nil
	}}

	buf := bytes.Repeat([]byte{0xaa}, 25)
	if n, err := r.Read(buf); n != 0 || !errors.Is(err, ErrUnavailable) || calls != 2 || !bytes.Equal(buf, make([]byte, len(buf))) {
		t.Fatalf("partial output escaped: n=%d, calls=%d, err=%v, data=%x", n, calls, err, buf)
	}
}

func TestDirectReaderAcceptsSuccessfulZero(t *testing.T) {
	r := Reader{next: func() (uint64, error) { return 0, nil }}

	buf := bytes.Repeat([]byte{0xff}, 11)
	if n, err := r.Read(buf); err != nil || n != len(buf) || !bytes.Equal(buf, make([]byte, len(buf))) {
		t.Fatal("a successful zero is valid RDRAND output")
	}
}

func TestDirectReaderNativeCapability(t *testing.T) {
	r, err := NewReader()
	if !Available() {
		if !errors.Is(err, ErrUnsupported) || r != nil {
			t.Fatalf("unsupported CPU/build accepted: %v", err)
		}

		return
	}

	if err != nil {
		t.Fatal(err)
	}

	var buf [33]byte
	if n, readErr := r.Read(buf[:]); readErr != nil || n != len(buf) {
		t.Fatalf("hardware read: %d, %v", n, readErr)
	}
}
