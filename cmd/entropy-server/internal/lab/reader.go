package lab

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"math/big"
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

type HTTPStatusError struct {
	Code            int
	RedirectBlocked bool
}

func (e *HTTPStatusError) Error() string { return fmt.Sprintf("upstream HTTP status %d", e.Code) }

type Stream struct {
	mu       sync.Mutex
	read     func(context.Context, int) ([]byte, error)
	close    func() error
	timeout  time.Duration
	maxBytes int
	stop     func() bool
	once     sync.Once
	info     HTTPInfo
}

// HTTPInfo describes the last response without disclosing endpoint URLs.
type HTTPInfo struct {
	Status    int  `json:"status"`
	Redirects int  `json:"redirects"`
	Location  bool `json:"location_present"`
}

func (s *Stream) HTTPInfo() HTTPInfo { return s.info }

func (s *Stream) Read(ctx context.Context, count int) ([]byte, error) {
	if count < 1 || count > s.maxBytes {
		return nil, ErrTooLarge
	}

	bounded, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	data, err := s.read(bounded, count)
	if err != nil && bounded.Err() != nil {
		err = bounded.Err()
	}

	return data, err
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
	return r.OpenAuthenticated(ctx, id, count, refresh, Credentials{})
}

func (r *Registry) OpenAuthenticated(ctx context.Context, id string, count, refresh int, credentials Credentials) (*Stream, error) {
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
	case sourceHTTP, sourceEntropyHTTP, sourceWebpage:
		stream.read = func(readCtx context.Context, n int) ([]byte, error) {
			stream.info = HTTPInfo{}
			endpoint := sourceURL(c, n, refresh)
			client := r.client
			redirects, blocked := 0, false

			if c.Kind == sourceWebpage {
				if len(c.URLs) > 0 {
					index, err := rand.Int(rand.Reader, big.NewInt(int64(len(c.URLs)+1)))
					if err != nil {
						return nil, err
					}

					if index.Int64() > 0 {
						endpoint = c.URLs[index.Int64()-1]
					}
				}

				copyClient := *r.client
				copyClient.CheckRedirect = func(request *http.Request, via []*http.Request) error {
					if len(via) >= 3 || !webRedirectAllowed(request.URL, via[len(via)-1].URL) || request.URL.User != nil {
						blocked = true
						return http.ErrUseLastResponse
					}

					redirects++

					return nil
				}
				client = &copyClient
			}

			request, err := http.NewRequestWithContext(readCtx, http.MethodGet, endpoint, nil)
			if err != nil {
				return nil, err
			}

			if c.Kind == sourceWebpage {
				request.Header.Set("User-Agent", "entropy-service source-lab (+https://github.com/fantuz/entropy-service)")
			}

			response, err := client.Do(request)
			if err != nil {
				return nil, err
			}
			defer response.Body.Close()

			stream.info = HTTPInfo{Status: response.StatusCode, Redirects: redirects, Location: response.Header.Get("Location") != ""}

			if response.StatusCode < 200 || response.StatusCode > 299 {
				return nil, &HTTPStatusError{Code: response.StatusCode, RedirectBlocked: blocked || (isRedirect(response.StatusCode) && response.Header.Get("Location") != "")}
			}

			limit := int64(n) + 1
			if c.Kind == sourceWebpage {
				limit = int64(n)
			}

			data, err := io.ReadAll(io.LimitReader(response.Body, limit))
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

			size, err := conn.Read(buffer)

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
	case sourceRTSP, sourceONVIF:
		if err := r.openCamera(ctx, dialCtx, stream, c, refresh, credentials); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("unsupported source")
	}

	stream.mu.Lock()
	stream.stop = context.AfterFunc(ctx, func() { _ = stream.Close() })
	stream.mu.Unlock()

	return stream, nil
}

func isRedirect(code int) bool {
	return code == 301 || code == 302 || code == 303 || code == 307 || code == 308
}

func webRedirectAllowed(target, original *url.URL) bool {
	return sameOrigin(target, original) || (original.Scheme == sourceHTTP && target.Scheme == sourceHTTPS && strings.EqualFold(target.Hostname(), original.Hostname()) && (original.Port() == "" || original.Port() == "80") && (target.Port() == "" || target.Port() == "443"))
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
