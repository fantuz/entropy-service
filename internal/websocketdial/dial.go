// Package websocketdial makes cancellation interrupt the entire WebSocket handshake.
package websocketdial

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

// Dial copies dialer and closes newly opened transports if ctx ends while the
// HTTP upgrade (or proxy/TLS handshake) is pending. After success, the caller
// owns connection cancellation. Standard certificate verification is preserved.
func Dial(ctx context.Context, dialer websocket.Dialer, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
	dial := dialer.NetDialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
		if dialer.NetDial != nil {
			dial = func(_ context.Context, network, address string) (net.Conn, error) {
				return dialer.NetDial(network, address)
			}
		}
	}

	var stops []func() bool

	defer func() {
		for _, stop := range stops {
			stop()
		}
	}()

	watch := func(dial func(context.Context, string, string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
		return func(dialCtx context.Context, network, address string) (net.Conn, error) {
			conn, err := dial(dialCtx, network, address)
			if err != nil {
				return nil, err
			}

			stops = append(stops, context.AfterFunc(ctx, func() { _ = conn.Close() }))

			return conn, nil
		}
	}

	dialer.NetDialContext = watch(dial)
	if dialer.NetDialTLSContext != nil {
		dialer.NetDialTLSContext = watch(dialer.NetDialTLSContext)
	}

	conn, response, err := dialer.DialContext(ctx, endpoint, headers)
	if ctx.Err() != nil {
		if conn != nil {
			_ = conn.Close()
		}

		return nil, response, ctx.Err()
	}

	if err != nil {
		// Socket deadlines can fire just before the context timer is scheduled.
		if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
			return nil, response, context.DeadlineExceeded
		}
	}

	return conn, response, err
}
