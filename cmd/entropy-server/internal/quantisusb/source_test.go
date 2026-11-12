package quantisusb

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-server/internal/usbtransport"
)

type fakeUSB struct {
	mu        sync.Mutex
	deadline  time.Time
	closed    bool
	controls  []byte
	bulkCalls int
	mask      byte
	status    byte
	bulk      func([]byte) (int, error)
}

func (d *fakeUSB) Control(request byte, p []byte) (int, error) {
	d.controls = append(d.controls, request)
	switch request {
	case requestMask:
		p[0] = d.mask
	case requestStatus:
		p[0] = d.status
	case requestBoard:
		binary.LittleEndian.PutUint32(p, 0x12345678)
	default:
		return 0, errors.New("unexpected write/reset/request")
	}

	return len(p), nil
}

func (d *fakeUSB) Bulk(p []byte) (int, error) {
	d.bulkCalls++
	if d.bulk != nil {
		return d.bulk(p)
	}

	for i := range p {
		p[i] = byte(d.bulkCalls)
	}

	return len(p), nil
}

func (d *fakeUSB) SetReadDeadline(deadline time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.deadline = deadline

	return nil
}
func (d *fakeUSB) Close() error { d.closed = true; return nil }

func TestUSBReadUsesWholePacketsAndNeverReusesTails(t *testing.T) {
	driver := &fakeUSB{mask: 1, status: 1}

	source, err := newSource(driver, 512)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()

	if err := source.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	for _, want := range []byte{1, 2} {
		data := make([]byte, 64)
		if n, err := source.Read(data); err != nil || n != 64 || !bytes.Equal(data, bytes.Repeat([]byte{want}, 64)) {
			t.Fatalf("packet tail reused or wrong read: %d %v %x", n, err, data)
		}
	}

	if driver.bulkCalls != 2 || !bytes.Equal(driver.controls, []byte{0x15, 0x13, 0x10, 0x13, 0x15, 0x13, 0x10, 0x13}) {
		t.Fatalf("wrong protocol calls: %+v", driver)
	}
}

func TestUSBRejectsUnhealthyShortZeroAndFailedTransfers(t *testing.T) {
	for _, tc := range []struct {
		name         string
		mask, status byte
		read         func([]byte) (int, error)
		want         error
	}{
		{"unhealthy", 1, 0, nil, nil},
		{"unknown module layout", 3, 3, nil, nil},
		{"zero", 1, 1, func([]byte) (int, error) { return 0, nil }, io.ErrNoProgress},
		{"short", 1, 1, func(p []byte) (int, error) { return len(p) - 1, nil }, io.ErrUnexpectedEOF},
		{"error after data", 1, 1, func(p []byte) (int, error) { clear(p); return 1, io.ErrClosedPipe }, io.ErrClosedPipe},
	} {
		t.Run(tc.name, func(t *testing.T) {
			driver := &fakeUSB{mask: tc.mask, status: tc.status, bulk: tc.read}

			source, err := newSource(driver, 512)
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close()

			data := bytes.Repeat([]byte{0xab}, 64)

			n, err := source.Read(data)
			if n != 0 || err == nil || (tc.want != nil && !errors.Is(err, tc.want)) || !bytes.Equal(data, bytes.Repeat([]byte{0xab}, 64)) {
				t.Fatalf("bad transfer exposed bytes: %d %v %x", n, err, data)
			}

			if tc.status != 1 && driver.bulkCalls != 0 {
				t.Fatal("unhealthy source read a packet")
			}
		})
	}
}

func TestUSBDeadlineRejectsLateDataAndClosedReads(t *testing.T) {
	driver := &fakeUSB{mask: 1, status: 1}

	source, err := newSource(driver, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()

	driver.bulk = func(p []byte) (int, error) {
		driver.mu.Lock()
		deadline := driver.deadline
		driver.mu.Unlock()
		time.Sleep(max(0, time.Until(deadline)) + time.Millisecond)
		clear(p)

		return len(p), nil
	}

	if err := source.SetReadDeadline(time.Now().Add(5 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}

	data := bytes.Repeat([]byte{0xab}, 64)
	if n, err := source.Read(data); n != 0 || !errors.Is(err, os.ErrDeadlineExceeded) || !bytes.Equal(data, bytes.Repeat([]byte{0xab}, 64)) {
		t.Fatalf("late data escaped: %d %v", n, err)
	}

	if err := source.Close(); err != nil {
		t.Fatal(err)
	}

	if n, err := source.Read(data); n != 0 || !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed read: %d %v", n, err)
	}
}

func TestUSBIdentityDescriptorsAndSetup(t *testing.T) {
	device := []byte{18, 1, 0, 2, 0, 0, 0, 64, 0xba, 0x0a, 2, 1, 0, 1, 0, 0, 0, 1}
	config := []byte{9, 2, 25, 0, 1, 1, 0, 0x80, 50, 9, 4, 0, 0, 1, 0xff, 0, 0, 0, 7, 5, 0x86, 2, 0, 2, 0}

	if err := usbtransport.Quantis.ValidateDevice(device); err != nil {
		t.Fatal(err)
	}

	if packet, err := usbtransport.Quantis.PacketSize(config); err != nil || packet != 512 {
		t.Fatalf("configuration: %d %v", packet, err)
	}

	for _, index := range []int{0, 1, 8, 10, 17} {
		changed := bytes.Clone(device)

		changed[index] ^= 1
		if err := usbtransport.Quantis.ValidateDevice(changed); err == nil {
			t.Fatalf("changed device accepted at %d", index)
		}
	}

	for _, index := range []int{0, 1, 2, 4, 5, 11, 12, 20, 21, 23} {
		changed := bytes.Clone(config)

		changed[index] ^= 1
		if _, err := usbtransport.Quantis.PacketSize(changed); err == nil {
			t.Fatalf("changed endpoint/config accepted at %d", index)
		}
	}

	want := [8]byte{0xc1, 0x10, 0, 0, 0, 0, 4, 0}
	if got := usbtransport.ControlSetup(requestBoard, 4); got != want {
		t.Fatalf("setup packet: %x", got)
	}

	for _, value := range []string{"quantis-usb:0", "quantis-usb:126"} {
		if _, selected, err := ParseSelector(value); !selected || err != nil {
			t.Fatalf("selector %q: %t %v", value, selected, err)
		}
	}

	for _, value := range []string{"quantis-usb", "quantis-usb:-1", "quantis-usb:127", "quantis-usb:01", "quantis-usb:0/file"} {
		if _, selected, err := ParseSelector(value); !selected || err == nil {
			t.Fatalf("invalid selector %q: %t %v", value, selected, err)
		}
	}
}

func TestUSBHealthChangeDiscardsAndClearsPacket(t *testing.T) {
	driver := &fakeUSB{mask: 1, status: 1}

	var captured []byte

	driver.bulk = func(packet []byte) (int, error) {
		captured = packet
		for i := range packet {
			packet[i] = 0x77
		}

		driver.status = 0

		return len(packet), nil
	}

	source, err := newSource(driver, 512)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()

	data := bytes.Repeat([]byte{0xab}, 64)
	if n, err := source.Read(data); n != 0 || err == nil || !bytes.Equal(data, bytes.Repeat([]byte{0xab}, 64)) {
		t.Fatalf("unhealthy final status exposed output: %d %v", n, err)
	}

	if !bytes.Equal(captured, make([]byte, 512)) {
		t.Fatal("discarded native packet was not cleared")
	}
}

func FuzzUSBDescriptors(f *testing.F) {
	f.Add([]byte{9, 2, 25, 0, 1, 1, 0, 0x80, 50, 9, 4, 0, 0, 1, 0xff, 0, 0, 0, 7, 5, 0x86, 2, 0, 2, 0})
	f.Fuzz(func(t *testing.T, descriptor []byte) {
		if len(descriptor) > 65536 {
			t.Skip()
		}

		_, _ = usbtransport.Quantis.PacketSize(descriptor)
		_ = usbtransport.Quantis.ValidateDevice(descriptor)
	})
}
