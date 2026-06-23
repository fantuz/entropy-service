package client

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/fantuz/entropy-service/cmd/entropy-client/internal/stats"
)

func TestEntropyHTTP(ctx context.Context, endpoint string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	fmt.Println("Starting HTTP entropy streaming test")

	err = stats.RunFromReader(
		ctx,
		resp.Body,
		4096,
		1<<20, // 1 MB checkpoints
		func(r stats.StreamResult) {
			reportStats("HTTP", r)
		},
	)

	return err
}
