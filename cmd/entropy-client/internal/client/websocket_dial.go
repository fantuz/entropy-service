package client

import (
	"context"
	"net"
	"time"

	"github.com/gorilla/websocket"
)

// Gorilla v1.5.3 applies a deadline to the handshake, but cancellation alone
// does not interrupt its HTTP upgrade read. Close the transport during dialing;
// callers take over cancellation once the WebSocket has been returned.
func dialWebSocket(ctx context.Context, endpoint string) (*websocket.Conn, error) {
	dialer := *websocket.DefaultDialer

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

	conn, response, err := dialer.DialContext(ctx, endpoint, nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// A socket deadline can fire just before the context timer is scheduled.
		if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
			return nil, context.DeadlineExceeded
		}
	}

	return conn, err
}
