package listener

import (
	"context"
	"net"
)

// NewTunedListener uses Go/Windows socket defaults. Linux-specific pre-bind
// tuning is intentionally unavailable; accepted TCP connections keep Go defaults.
func NewTunedListener(ctx context.Context, addr string, _ int) (net.Listener, error) {
	var config net.ListenConfig

	return config.Listen(ctx, "tcp", addr)
}
