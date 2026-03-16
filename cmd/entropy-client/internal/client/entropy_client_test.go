package client
import (
	"time"
	"context"
	"testing"
)

func TestEntropyEndpoint(t *testing.T) {

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := TestEntropyHTTP(ctx, "http://127.0.0.1:8080/v1/data/random")

	if err != nil {
		t.Fatal(err)
	}
}
