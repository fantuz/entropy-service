package client

import (
	"context"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fantuz/entropy-service/internal/websocketdial"
)

// Gorilla v1.5.3 applies a deadline to the handshake, but cancellation alone
// does not interrupt its HTTP upgrade read. Close the transport during dialing;
// callers take over cancellation once the WebSocket has been returned.
func dialWebSocket(ctx context.Context, endpoint string) (*websocket.Conn, error) {
	conn, response, err := websocketdial.Dial(ctx, *websocket.DefaultDialer, endpoint, nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		if response != nil {
			return nil, &HTTPStatusError{Code: response.StatusCode, RetryAfter: retryAfter(response.Header.Get("Retry-After"), time.Now())}
		}
	}

	return conn, err
}
