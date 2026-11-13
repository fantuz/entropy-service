#!/usr/bin/env bash
set -euo pipefail
budget=${FUZZTIME:-10s}
# CI gives this query target a count budget: Go 1.26 can report its own
# duration expiry as a coordinator error before child cancellation propagates.
# Keep FUZZTIME's local default/override and permit an explicit target budget.
generation_budget=${GENERATION_FUZZTIME:-$budget}
# Avoid a timer-expiry race in Go's fuzz coordinator for the descriptor target.
# Its default execution limit still bounds CI work; an override accepts Go's
# usual duration or count syntax (e.g. USB_FUZZTIME=30s or 1000000x).
usb_budget=${USB_FUZZTIME:-500000x}
go test ./cmd/entropy-client/internal/config -run '^$' -fuzz '^FuzzClientArguments$' -fuzztime="$budget" -parallel=2
go test ./cmd/entropy-client/internal/client -run '^$' -fuzz '^FuzzEntropyFrame$' -fuzztime="$budget" -parallel=2
go test ./cmd/entropy-server -run '^$' -fuzz '^FuzzGenerationQuery$' -fuzztime="$generation_budget" -parallel=2
go test ./cmd/entropy-server -run '^$' -fuzz '^FuzzUploadAnalysis$' -fuzztime="$budget" -parallel=2
go test ./cmd/entropy-server/internal/quantisusb -run '^$' -fuzz '^FuzzUSBDescriptors$' -fuzztime="$usb_budget" -parallel=2
go test ./cmd/entropy-server/internal/usbtransport -run '^$' -fuzz '^FuzzProfiles$' -fuzztime="$usb_budget" -parallel=2
