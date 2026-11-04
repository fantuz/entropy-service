# Measuring DRBG key-lifecycle overhead

This suite measures **local CPU and allocations**, not physical QRNG speed,
TLS, HTTP throughput, source quality, or cryptographic security. It was added
after the key-material lifecycle work and **no performance claim is made
until both revisions have been benchmarked on the same machine**.

## Cases

`BenchmarkDRBGLifecycle` runs each case with both `chacha20` and
`aes-256-ctr`:

| Case | Measures | Includes |
| --- | --- | --- |
| `NewClose` | One generator construction and optional close | SHA-512, key/nonce allocation, cipher construction, accounting |
| `Reseed256` | One 256-byte reseed on existing generator | SHA-512, state transition, stream recreation; excludes source I/O |
| `ChildCreateClose` | Derive and close one private HTTP child | 64-byte parent output and new child construction |
| `WSOpenReadClose` | One private WebSocket reader lifecycle with 4 KiB output | Lazy child creation, output, close and accounting |
| `Read64KiB` | Steady-state random generation | Cipher stream processing, locking and 64 KiB output; no socket |

All cases use a deterministic **test-only** seed and input; do not reuse that
seed in any production deployment. The benchmark's `closeIfSupported`
helper permits compiling this **same source file** against older revisions
that lacked `DRBG.Close()`. A baseline without `Close` does less
shutdown cleanup by definition; this is exactly the behavior to quantify.

## Run on the current revision

From the repository root:

```bash
git rev-parse HEAD
go version
go env GOOS GOARCH CGO_ENABLED GOMAXPROCS
GOMAXPROCS=1 go test ./cmd/entropy-server/internal/drbg \
  -run '^$' -bench '^BenchmarkDRBGLifecycle$' \
  -benchtime=1s -benchmem -count=5 \
  | tee drbg-lifecycle-current.txt
```

For CPU and allocation profiles on a controlled run:

```bash
go test ./cmd/entropy-server/internal/drbg \
  -run '^$' -bench '^BenchmarkDRBGLifecycle/chacha20/Reseed256$' \
  -benchtime=5s -benchmem -cpuprofile=drbg-reseed.cpu \
  -memprofile=drbg-reseed.mem
go tool pprof -http=127.0.0.1:0 drbg-reseed.cpu
```

Profiles may contain process metadata. Treat them as potentially sensitive,
especially if taken against a real service; benchmark profiles here use
synthetic seeds.

## Controlled before/after comparison

The pre-lifecycle base of PR #13 can be used as the historical baseline:
`00e9b7b49a2ec749da51fd61b2111a24b3037fa7`.
For reliable numbers, run the **same benchmark source** against both trees.
For example, in a disposable checkout with no uncommitted changes:

```bash
baseline=00e9b7b49a2ec749da51fd61b2111a24b3037fa7
current=$(git rev-parse HEAD)
mkdir -p ./benchmark-results

git worktree add --detach ./benchmark-results/baseline-tree "$baseline"
cp cmd/entropy-server/internal/drbg/lifecycle_bench_test.go \
   ./benchmark-results/baseline-tree/cmd/entropy-server/internal/drbg/
(
  cd ./benchmark-results/baseline-tree
  GOMAXPROCS=1 go test ./cmd/entropy-server/internal/drbg \
    -run '^$' -bench '^BenchmarkDRBGLifecycle$' \
    -benchtime=1s -benchmem -count=5
) | tee ./benchmark-results/baseline.txt

GOMAXPROCS=1 go test ./cmd/entropy-server/internal/drbg \
  -run '^$' -bench '^BenchmarkDRBGLifecycle$' \
  -benchtime=1s -benchmem -count=5 \
  | tee ./benchmark-results/current.txt

# If installed, benchstat compares distributions of ns/op, B/op, allocs/op.
benchstat ./benchmark-results/baseline.txt ./benchmark-results/current.txt
git worktree remove ./benchmark-results/baseline-tree
```

**Important:** do not put worktree paths inside a Go module unless you
understand the nested module behavior. A separate temporary directory
outside the repository is preferable on restricted setups. The above
example uses an untracked `benchmark-results/` directory for convenience;
ensure it is excluded from commits and do not remove a worktree with local
changes you want to keep.

If `benchstat` is unavailable, preserve both raw outputs and compare
`ns/op`, `B/op` and `allocs/op` manually. Performance interpretation
should use medians and variability rather than a single trial. A 1-second
benchmark with five repetitions is a starting point; repeat after warmup,
and run with realistic CPU settings and concurrent workloads separately.

## Interpretation

- `NewClose` and `ChildCreateClose` quantify whether state cleaning,
  cipher creation and extra synchronization increase per-request overhead.
- `Reseed256` captures the cost of the streaming SHA-512 transition plus
  cleanup, **not** hardware source reads. At a 250 ms default interval, a
  modest per-reseed CPU change will usually be small compared to continuous
  bulk output; measure instead of asserting.
- `Read64KiB` checks for unintended hot-path slowdown, but does not
  include network framing or transport overhead.
- Benchmarking against a different Go version, AES acceleration, CPU
  frequency, `GOMAXPROCS`, or GC setting invalidates a clean comparison.
- Passing output fixtures establishes deterministic compatibility, not
  adequate key erasure or cryptographic security.

For end-to-end HTTP testing, see [existing benchmark methodology](benchmarks.md).
