//go:build linux || windows

package output

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

func TestTextStdoutPipeCancellationAndOwnership(t *testing.T) {
	reader, stdout, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer stdout.Close()

	writer, err := openTextStdout(stdout)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	data := bytes.Repeat([]byte("report-data\n"), 1<<20)

	n, err := writer.WriteContext(ctx, data)
	if !errors.Is(err, context.DeadlineExceeded) || n >= len(data) {
		t.Fatalf("blocked pipe: %d %v", n, err)
	}

	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	if err := stdout.Close(); err != nil {
		t.Fatal("report closed caller stdout:", err)
	}

	received, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(received, data[:n]) {
		t.Fatalf("reported %d bytes, received %d", n, len(received))
	}
}

func TestTextStdoutBrokenPipe(t *testing.T) {
	reader, stdout, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()

	writer, err := openTextStdout(stdout)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}

	if n, err := writer.WriteContext(t.Context(), []byte("record\n")); err == nil || n != 0 {
		t.Fatalf("broken pipe: %d %v", n, err)
	}
}

func TestTextStdoutLargeWriteWithDrainingConsumer(t *testing.T) {
	reader, stdout, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer stdout.Close()

	writer, err := openTextStdout(stdout)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	type result struct {
		data []byte
		err  error
	}

	done := make(chan result, 1)

	go func() { data, err := io.ReadAll(reader); done <- result{data, err} }()

	data := bytes.Repeat([]byte("large JSONL record\n"), 1<<15)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	if n, err := writer.WriteContext(ctx, data); err != nil || n != len(data) {
		t.Fatalf("large write: %d/%d %v", n, len(data), err)
	}

	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	if err := stdout.Close(); err != nil {
		t.Fatal(err)
	}

	received := <-done
	if received.err != nil || !bytes.Equal(received.data, data) {
		t.Fatalf("large round trip: %d bytes, %v", len(received.data), received.err)
	}
}
