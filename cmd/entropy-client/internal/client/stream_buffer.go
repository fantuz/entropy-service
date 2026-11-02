package client

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"sync"

	"github.com/gorilla/websocket"
)

type frameLease struct {
	once sync.Once
	pool chan []byte
	data []byte
}

func (l *frameLease) release() {
	l.once.Do(func() {
		select {
		case l.pool <- l.data:
		default:
		}

		l.data = nil
	})
}

// A validated request size avoids ReadMessage's incremental buffer growth.
// URLs without a byte count retain the bounded, variable-size read path.
func streamFrameSize(endpoint string) (int, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return 0, err
	}

	values, present := u.Query()["bytes"]
	if !present {
		return 0, nil
	}

	if len(values) != 1 {
		return 0, errors.New("bytes must be supplied once")
	}

	n, err := strconv.Atoi(values[0])
	if err != nil || n < 1 || n > maxEntropyBytes {
		return 0, fmt.Errorf("bytes must be between 1 and %d", maxEntropyBytes)
	}

	return n, nil
}

func readStreamFrame(conn *websocket.Conn, pool chan []byte, size int) (StreamFrame, error) {
	if size == 0 {
		kind, data, err := conn.ReadMessage()
		if err == nil && kind != websocket.BinaryMessage {
			err = errors.New("expected a binary entropy message")
		}

		return StreamFrame{Data: data}, err
	}

	kind, reader, err := conn.NextReader()
	if err != nil {
		return StreamFrame{}, err
	}

	if kind != websocket.BinaryMessage {
		return StreamFrame{}, errors.New("expected a binary entropy message")
	}

	var buf []byte
	select {
	case buf = <-pool:
	default:
		buf = make([]byte, size)
	}

	frame := StreamFrame{Data: buf, lease: &frameLease{pool: pool, data: buf}}
	if _, err := io.ReadFull(reader, buf); err != nil {
		frame.Release()

		return StreamFrame{}, err
	}

	// Consume the message boundary: extra bytes must not become the next
	// frame, nor be silently accepted as the requested size.
	var extra [1]byte
	if n, err := io.ReadFull(reader, extra[:]); n != 0 || err != io.EOF {
		frame.Release()

		if err != nil && err != io.EOF {
			return StreamFrame{}, err
		}

		return StreamFrame{}, errors.New("entropy message exceeds requested size")
	}

	return frame, nil
}
