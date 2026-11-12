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
