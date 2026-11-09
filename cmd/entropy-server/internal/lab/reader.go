package lab

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var ErrTooLarge = errors.New("source sample exceeds requested byte limit")

type Stream struct {
	mu       sync.Mutex
	read     func(context.Context, int) ([]byte, error)
	close    func() error
	timeout  time.Duration
	maxBytes int
	stop     func() bool
	once     sync.Once
}

func (s *Stream) Read(ctx context.Context, count int) ([]byte, error) {
	if count < 1 || count > s.maxBytes {
		return nil, ErrTooLarge
	}

	bounded, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	return s.read(bounded, count)
}

func (s *Stream) Close() error {
	var err error

	s.once.Do(func() {
		s.mu.Lock()
		defer s.mu.Unlock()

		if s.stop != nil {
			s.stop()
		}

		err = s.close()
	})

	return err
}

func (r *Registry) Open(ctx context.Context, id string, count, refresh int) (*Stream, error) {
	c, ok := r.sources[id]
	if !ok {
		return nil, errors.New("unknown source ID")
	}

	if count < 1 || count > c.MaxBytes {
		return nil, ErrTooLarge
	}

	stream := &Stream{timeout: time.Duration(c.TimeoutMs) * time.Millisecond, maxBytes: c.MaxBytes, close: func() error { return nil }}

	dialCtx, cancel := context.WithTimeout(ctx, stream.timeout)
	defer cancel()

	switch c.Kind {
	case sourceHTTP, sourceEntropyHTTP:
		stream.read = func(readCtx context.Context, n int) ([]byte, error) {
			endpoint := sourceURL(c, n, refresh)

			request, err := http.NewRequestWithContext(readCtx, http.MethodGet, endpoint, nil)
			if err != nil {
				return nil, err
			}

			response, err := r.client.Do(request)
			if err != nil {
				return nil, err
			}
			defer response.Body.Close()

			if response.StatusCode < 200 || response.StatusCode > 299 {
				return nil, fmt.Errorf("upstream HTTP status %d", response.StatusCode)
			}

			data, err := io.ReadAll(io.LimitReader(response.Body, int64(n)+1))
			if len(data) > n {
				return nil, ErrTooLarge
			}

			if err == nil && len(data) == 0 {
				err = io.EOF
			}

			return data, err
		}
	case sourceWS, sourceEntropyWS:
		dialer := *websocket.DefaultDialer
		dialer.HandshakeTimeout = stream.timeout

		conn, response, err := dialer.DialContext(dialCtx, sourceURL(c, count, refresh), nil)
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}

		if err != nil {
			return nil, err
		}

		stream.close = conn.Close
		conn.SetReadLimit(int64(count))

		stream.read = func(readCtx context.Context, n int) ([]byte, error) {
			deadline, _ := readCtx.Deadline()
			if err := conn.SetReadDeadline(deadline); err != nil {
				return nil, err
			}

			stop := context.AfterFunc(readCtx, func() { _ = conn.Close() })
			defer stop()

			kind, reader, err := conn.NextReader()
			if err != nil {
				if errors.Is(err, websocket.ErrReadLimit) {
					return nil, ErrTooLarge
				}

				if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
					return nil, io.EOF
				}

				return nil, err
			}

			if kind != websocket.BinaryMessage {
				return nil, errors.New("upstream must send binary WebSocket messages")
			}

			data, err := io.ReadAll(io.LimitReader(reader, int64(n)+1))
			if errors.Is(err, websocket.ErrReadLimit) {
				return nil, ErrTooLarge
			}

			if len(data) > n {
				return nil, ErrTooLarge
			}

			if err == nil && len(data) == 0 {
				err = io.EOF
			}

			return data, err
		}
	case sourceTCP, sourceUnix:
		conn, err := (&net.Dialer{}).DialContext(dialCtx, c.Kind, c.Endpoint)
		if err != nil {
			return nil, err
		}

		stream.close = conn.Close
		stream.read = func(readCtx context.Context, n int) ([]byte, error) {
			deadline, _ := readCtx.Deadline()
			if err := conn.SetReadDeadline(deadline); err != nil {
				return nil, err
			}

			stop := context.AfterFunc(readCtx, func() { _ = conn.Close() })
			defer stop()

			buffer := make([]byte, n)

			size, err := io.ReadFull(conn, buffer)
			if errors.Is(err, io.ErrUnexpectedEOF) {
				err = io.EOF
			}

			return buffer[:size], err
		}
	case sourceUDP:
		conn, err := (&net.ListenConfig{}).ListenPacket(dialCtx, sourceUDP, c.Endpoint)
		if err != nil {
			return nil, err
		}

		stream.close = conn.Close
		buffer := make([]byte, 65536)
		stream.read = func(readCtx context.Context, n int) ([]byte, error) {
			deadline, _ := readCtx.Deadline()
			if err := conn.SetReadDeadline(deadline); err != nil {
				return nil, err
			}

			stop := context.AfterFunc(readCtx, func() { _ = conn.Close() })
			defer stop()

			size, _, err := conn.ReadFrom(buffer)
			if size > n {
				return nil, ErrTooLarge
			}

			return append([]byte(nil), buffer[:size]...), err
		}
	case sourceDevice:
		device, err := openDevice(c.Endpoint)
		if err != nil {
			return nil, err
		}

		stream.close = device.Close
		stream.read = device.Read
	default:
		return nil, errors.New("unsupported source")
	}

	stream.mu.Lock()
	stream.stop = context.AfterFunc(ctx, func() { _ = stream.Close() })
	stream.mu.Unlock()

	return stream, nil
}

func sourceURL(c Config, count, refresh int) string {
	if c.Kind != sourceEntropyHTTP && c.Kind != sourceEntropyWS {
		return c.Endpoint
	}

	u, _ := url.Parse(c.Endpoint) // Validated before registration.
	if c.Kind == sourceEntropyWS {
		u.Scheme = strings.Replace(u.Scheme, sourceHTTP, "ws", 1)
		u.Path = "/stream"
	} else {
		u.Path = "/v1/data/random"
	}

	query := u.Query()
	query.Set("bytes", strconv.Itoa(count))
	query.Set("refresh", strconv.Itoa(refresh))
	u.RawQuery = query.Encode()

	return u.String()
}
