#!/usr/bin/env bash
# Run against a server you have already started. Measures HTTP delivery, not physical entropy.
set -euo pipefail

for tool in wrk go git date; do
  command -v "$tool" >/dev/null 2>&1 || { echo "Missing required tool: $tool" >&2; exit 1; }
done

base=${ENTROPY_BENCH_URL:-http://127.0.0.1:8080/v1/data/random}
threads=${WRK_THREADS:-4}
connections=${WRK_CONNECTIONS:-32}
duration=${WRK_DURATION:-30s}
output=${BENCH_OUTPUT:-${TMPDIR:-/tmp}/entropy-bench-results-$(date -u +%Y%m%dT%H%M%SZ)}

if [[ ! "$threads" =~ ^[1-9][0-9]*$ || ! "$connections" =~ ^[1-9][0-9]*$ ]]; then
  echo 'WRK_THREADS and WRK_CONNECTIONS must be positive integers' >&2
  exit 2
fi

if [[ "$base" != http://* && "$base" != https://* ]]; then
  echo 'ENTROPY_BENCH_URL must start with http:// or https://' >&2
  exit 2
fi

mkdir -p "$output"
{
  echo "UTC: $(date -u +'%Y-%m-%dT%H:%M:%SZ')"
  echo "Git commit: $(git rev-parse HEAD)"
  echo "Go: $(go version)"
  echo "Hostname: $(hostname)"
  echo "GOOS/GOARCH: $(go env GOOS)/$(go env GOARCH)"
  echo "Backend and entropy source: RECORD YOUR RUNNING SERVER FLAGS SEPARATELY"
  echo "Endpoint: $base"
  echo "wrk threads: $threads; connections: $connections; duration: $duration"
  if command -v lscpu >/dev/null; then lscpu; fi
} > "$output/environment.txt"

for bytes in 65536 262144 1048576; do
  separator='?'
  if [[ "$base" == *\?* ]]; then separator='&'; fi
  url="${base}${separator}bytes=${bytes}"
  echo "Benchmarking $bytes bytes: $url"
  wrk -t"$threads" -c"$connections" -d"$duration" \
      --latency --timeout 3s "$url" | tee "$output/wrk-${bytes}.txt"
done

# Independent encoder benchmark; not equivalent to HTTP throughput.
go test ./cmd/entropy-server -run '^$' \
  -bench '^BenchmarkByteFrameFormats$' -benchmem -benchtime=300ms -count=3 \
  | tee "$output/encoder-benchmark.txt"

echo "Results saved in: $output"
