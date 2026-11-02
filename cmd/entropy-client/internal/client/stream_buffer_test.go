package client

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestSizedFramesRemainOwnedUntilReleased(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		for i := range 12 {
			writer, err := conn.NextWriter(websocket.BinaryMessage)
			if err != nil {
				return
			}

			// Multiple writes also exercise fragmented message assembly.
			for range 8 {
				if _, err := writer.Write(bytes.Repeat([]byte{byte(i)}, 1024)); err != nil {
					return
				}
			}

			if err := writer.Close(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	frames, err := StreamEntropy(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"?bytes=8192")
	if err != nil {
		t.Fatal(err)
	}

	var retained []StreamFrame

	count := 0
	for frame := range frames {
		if !bytes.Equal(frame.Data, bytes.Repeat([]byte{byte(count)}, 8192)) {
			t.Fatalf("frame %d was overwritten or truncated", count)
		}

		if count < 3 {
			retained = append(retained, frame)
		} else {
			frame.Release()
			frame.Release() // Releasing copies twice must not return two leases.
		}

		count++
	}

	if count != 12 {
		t.Fatalf("received %d frames, want 12", count)
	}

	for i, frame := range retained {
		if !bytes.Equal(frame.Data, bytes.Repeat([]byte{byte(i)}, 8192)) {
			t.Fatal("retained frame changed before Release")
		}

		frame.Release()
	}
}

func TestFrameReleaseIsIdempotentAcrossCopies(t *testing.T) {
	pool := make(chan []byte, 3)
	frame := StreamFrame{lease: &frameLease{pool: pool, data: make([]byte, 64)}}

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func(copyOfFrame StreamFrame) {
			defer wg.Done()

			copyOfFrame.Release()
		}(frame)
	}

	wg.Wait()

	if len(pool) != 1 {
		t.Fatalf("returned %d buffers for one frame", len(pool))
	}
}

func TestSizedStreamRejectsWrongLengths(t *testing.T) {
	for _, length := range []int{63, 65} {
		t.Run(strconv.Itoa(length), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upgrader := websocket.Upgrader{}

				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()

				_ = conn.WriteMessage(websocket.BinaryMessage, make([]byte, length))
			}))
			defer server.Close()

			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()

			frames, err := StreamEntropy(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"?bytes=64")
			if err != nil {
				t.Fatal(err)
			}

			select {
			case frame, ok := <-frames:
				if ok {
					frame.Release()
					t.Fatal("mismatched frame was accepted")
				}
			case <-ctx.Done():
				t.Fatal("length error did not close the stream")
			}
		})
	}
}

func TestSizedReadCancellation(t *testing.T) {
	ready, closed := make(chan struct{}), make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(closed)

		upgrader := websocket.Upgrader{}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		writer, err := conn.NextWriter(websocket.BinaryMessage)
		if err != nil {
			return
		}
		defer writer.Close()

		_, _ = writer.Write(make([]byte, 8192))

		close(ready)

		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	frames, err := StreamEntropy(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"?bytes=1048576")
	if err != nil {
		t.Fatal(err)
	}

	<-ready
	cancel()

	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("partial frame read ignored cancellation")
	}

	for frame := range frames {
		frame.Release()
	}
}

func BenchmarkSizedStream16MiB(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		data := make([]byte, 16<<20)
		for {
			if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(b.Context())

	defer cancel()

	frames, err := StreamEntropy(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"?bytes=16777216")
	if err != nil {
		b.Fatal(err)
	}
	defer func() {
		cancel()

		for frame := range frames {
			frame.Release()
		}
	}()
	// Populate the small reuse pool before measuring the steady state.
	for range 8 {
		frame := <-frames
		frame.Release()
	}

	b.SetBytes(16 << 20)
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		frame, ok := <-frames
		if !ok || len(frame.Data) != 16<<20 {
			b.Fatal("unexpected frame")
		}

		frame.Release()
	}
}
