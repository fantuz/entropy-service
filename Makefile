BIN_DIR := ./bin
COVERAGE_DIR := ./coverage

.PHONY: build build-server build-client test test-race coverage lint fmt clean release-snapshot

build: build-server build-client

build-server:
	go build -o $(BIN_DIR)/entropy-server ./cmd/entropy-server

build-client:
	go build -o $(BIN_DIR)/entropy-client ./cmd/entropy-client

test:
	go test ./...

test-race:
	go test -race -count=1 -timeout=10m ./...

coverage:
	mkdir -p $(COVERAGE_DIR)
	go test -coverpkg=./... -covermode=atomic -coverprofile=$(COVERAGE_DIR)/coverage.out ./...
	go tool cover -func=$(COVERAGE_DIR)/coverage.out -o=$(COVERAGE_DIR)/summary.txt
	cat $(COVERAGE_DIR)/summary.txt
	go tool cover -html=$(COVERAGE_DIR)/coverage.out -o=$(COVERAGE_DIR)/coverage.html

lint:
	golangci-lint run

fmt:
	golangci-lint fmt

release-snapshot:
	goreleaser release --snapshot --clean

clean:
	rm -rf $(BIN_DIR) ./dist $(COVERAGE_DIR)
