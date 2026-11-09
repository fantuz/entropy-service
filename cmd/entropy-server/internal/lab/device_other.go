//go:build !linux

package lab

import (
	"context"
	"errors"
)

type deviceReader struct{}

func openDevice(string) (*deviceReader, error) {
	return nil, errors.New("direct device sources require Linux; use a socket adapter on this platform")
}
func (*deviceReader) Close() error { return nil }
func (*deviceReader) Read(context.Context, int) ([]byte, error) {
	return nil, errors.New("device source unavailable")
}
