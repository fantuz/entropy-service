BIN_DIR := ./bin

.PHONY: build build-server build-client test lint fmt clean release-snapshot

build: build-server build-client

build-server:
	go build -o $(BIN_DIR)/entropy-server ./cmd/entropy-server

build-client:
	go build -o $(BIN_DIR)/entropy-client ./cmd/entropy-client

test:
	go test ./...

lint:
	golangci-lint run

fmt:
	golangci-lint fmt

release-snapshot:
	goreleaser release --snapshot --clean

clean:
	rm -rf $(BIN_DIR) ./dist
