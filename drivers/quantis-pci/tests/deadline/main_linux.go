//go:build linux

// Exercise the Go runtime's actual epoll/deadline contract against the virtual
// character device. Invoked only in the dedicated VM test, never by go test.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"syscall"
	"time"
)

func checkDeadlines() error {
	file, err := os.OpenFile("/dev/qrandom-test0", os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer file.Close()

	var sample [64]byte

	for range 5 {
		if err := file.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
			return err
		}

		if n, err := file.Read(sample[:]); n != 0 || !errors.Is(err, os.ErrDeadlineExceeded) {
			return fmt.Errorf("deadline: %d, %w", n, err)
		}
	}

	if err := file.SetReadDeadline(time.Time{}); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	done := make(chan struct{})

	stop := context.AfterFunc(ctx, func() { _ = file.SetReadDeadline(time.Now()); close(done) })
	defer stop()

	began := time.Now()
	_, err = file.Read(sample[:])

	<-done

	if !errors.Is(err, os.ErrDeadlineExceeded) || time.Since(began) > time.Second {
		return fmt.Errorf("context cancellation: %w", err)
	}

	if err := file.SetReadDeadline(time.Time{}); err != nil {
		return err
	}

	closed := make(chan struct{})

	timer := time.AfterFunc(20*time.Millisecond, func() { _ = file.Close(); close(closed) })
	defer timer.Stop()

	_, err = file.Read(sample[:])

	<-closed

	if !errors.Is(err, os.ErrClosed) {
		return fmt.Errorf("close cancellation: %w", err)
	}

	return nil
}

func main() {
	if err := checkDeadlines(); err != nil {
		log.Fatal(err)
	}

	fmt.Println("Go epoll registration, repeated deadlines, context cancellation and close: passed")
}
