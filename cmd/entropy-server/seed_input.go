package main

import (
	"context"
	"errors"
	"io"
	"time"
)

type seedDeadlineSource interface {
	io.ReadCloser
	SetReadDeadline(deadline time.Time) error
}

// One caller owns the source: initial seeding precedes the joined reseed worker.
// The deadline covers the entire seed, including short reads and CPU mixing.
type seedInput struct {
	source  seedDeadlineSource
	reader  io.Reader
	timeout time.Duration
}

func (s *seedInput) Reader(parent context.Context) io.Reader {
	return seedReadFunc(func(p []byte) (int, error) { return s.readSeed(parent, p) })
}

func (s *seedInput) readSeed(parent context.Context, p []byte) (n int, err error) {
	ctx, cancel := context.WithTimeout(parent, s.timeout)
	defer cancel()

	if err := ctx.Err(); err != nil {
		return 0, err
	}

	deadline, _ := ctx.Deadline()
	if err := s.source.SetReadDeadline(deadline); err != nil {
		return 0, err
	}

	interrupted := make(chan struct{})

	stop := context.AfterFunc(ctx, func() {
		defer close(interrupted)

		_ = s.source.SetReadDeadline(time.Now())
	})
	defer func() {
		if !stop() {
			<-interrupted
		}

		err = errors.Join(err, s.source.SetReadDeadline(time.Time{}))
		if err != nil {
			n = 0

			clear(p)
		}
	}()

	checked := seedReadFunc(func(p []byte) (int, error) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}

		n, err := s.reader.Read(p[:min(len(p), 32<<10)])
		if n == 0 && err == nil && len(p) > 0 {
			return 0, io.ErrNoProgress
		}

		return n, err
	})
	n, err = io.ReadFull(checked, p)

	if ctx.Err() != nil {
		return 0, ctx.Err()
	}

	if !time.Now().Before(deadline) {
		return 0, context.DeadlineExceeded
	}

	return n, err
}

type seedReadFunc func([]byte) (int, error)

func (read seedReadFunc) Read(p []byte) (int, error) { return read(p) }
