package lab

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

func TestStreamDiscardsCompleteReadAfterTimeout(t *testing.T) {
	data := []byte{1, 2, 3}
	stream := &Stream{maxBytes: 3, timeout: 10 * time.Millisecond, read: func(ctx context.Context, _ int) ([]byte, error) {
		<-ctx.Done()
		return data, nil
	}}

	got, err := stream.Read(t.Context(), 3)
	if !errors.Is(err, context.DeadlineExceeded) || len(got) != 0 || !bytes.Equal(data, make([]byte, 3)) {
		t.Fatalf("late complete sample accepted or retained: bytes=%d err=%v", len(got), err)
	}
}

type undispatchedLabDeadline struct {
	deadline time.Time
	done     <-chan struct{}
}

func (ctx undispatchedLabDeadline) Deadline() (time.Time, bool) { return ctx.deadline, true }
func (ctx undispatchedLabDeadline) Done() <-chan struct{}       { return ctx.done }
func (undispatchedLabDeadline) Err() error                      { return nil }
func (undispatchedLabDeadline) Value(any) any                   { return nil }

func TestStreamDoesNotStartReadAfterAbsoluteDeadline(t *testing.T) {
	parent := undispatchedLabDeadline{deadline: time.Now().Add(-time.Second), done: make(chan struct{})}
	reads := 0
	stream := &Stream{maxBytes: 1, timeout: time.Second, read: func(context.Context, int) ([]byte, error) {
		reads++
		return []byte{1}, nil
	}}

	got, err := stream.Read(parent, 1)
	if !errors.Is(err, context.DeadlineExceeded) || len(got) != 0 || reads != 0 {
		t.Fatalf("expired budget started a read: reads=%d bytes=%d err=%v", reads, len(got), err)
	}
}
