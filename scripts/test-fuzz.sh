#!/usr/bin/env bash
set -euo pipefail
budget=${FUZZTIME:-10s}
go test ./cmd/entropy-client/internal/config -run '^$' -fuzz '^FuzzClientArguments$' -fuzztime="$budget" -parallel=2
go test ./cmd/entropy-client/internal/client -run '^$' -fuzz '^FuzzEntropyFrame$' -fuzztime="$budget" -parallel=2
go test ./cmd/entropy-server -run '^$' -fuzz '^FuzzGenerationQuery$' -fuzztime="$budget" -parallel=2
go test ./cmd/entropy-server -run '^$' -fuzz '^FuzzUploadAnalysis$' -fuzztime="$budget" -parallel=2
