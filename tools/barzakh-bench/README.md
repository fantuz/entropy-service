# Barzakh-521 research benchmark

An opt-in timing harness for the upstream **v1** CSPRNG, pinned to commit
`871900c7d9a2ff4d3a4580a4521b6fdb450c3c40`. It measures single-stream and
eight-lane batch generation into 64 KiB and 1 MiB buffers, plus construction
and destruction. It prints one JSON object per measurement.

This tool is independent of the Go module, CI and release binaries. It does
not add a server backend. No upstream implementation is vendored here;
Cargo fetches the pinned repository and locked dependencies on first use.

**Upstream licensing matters:** its LICENSE declares AGPL-3.0-or-later,
an additional four-core notice and a pending-patent notice. Building this
tool links that dependency locally; it does not change those terms or
make the resulting binary MIT-only. Clarify them before redistribution or
service integration. The harness itself follows this project's MIT license.

## Run

From this directory, with Rust/Cargo installed (tested with Rust 1.99.1):

```sh
# Match the measured release optimization. Run only on this machine:
# target-cpu=native binaries may require CPU features absent elsewhere.
RUSTFLAGS='-C target-cpu=native' cargo build --release --locked
RUSTFLAGS='-C target-cpu=native' cargo test --release --locked
cargo fmt -- --check

# Minimum milliseconds per case, repetitions; defaults are 500 and 5.
# Set this directory before redirecting so the output location is explicit.
./target/release/entropy-service-barzakh-bench 500 5 > barzakh-v1.jsonl
```

Milliseconds must be 100–10000 and repetitions 1–10. A complete fill can
exceed the minimum duration. Initialization and buffer allocation are
outside fill timings, and each case performs an untimed warmup. New/drop
cases include construction and destruction. Timing calls and upstream
per-batch allocations remain inside the measured loops. Output is passed
through `black_box`, with no terminal or network I/O in the timed region.
The public seed `00 01 … 3f` is only for reproducible experiments.

The two tests check aligned-read continuity and demonstrate that v1's
partial `fill_bytes` calls discard batch remainders. The latter is an
upstream API behavior an eventual `io.Reader` adapter must account for;
it is not treated as a statistical or cryptographic security test.

Compare with the existing Go benchmark from the repository root:

```sh
go test ./cmd/entropy-server/internal/drbg -run '^$' \
  -bench '^BenchmarkDRBGLifecycle/(chacha20|aes-256-ctr)/(NewClose|Read64KiB)$' \
  -benchtime=500ms -benchmem -count=5 -cpu=1 > go-backends.txt
```

Run sequentially on an otherwise quiet machine. Go includes the existing
DRBG lock, byte clearing, byte-limit check and metrics; Rust measures the
upstream generator without a server wrapper. Results compare implementations
and are not a controlled comparison of algorithms independent of language.
See [evaluation and integration proposal](../../docs/barzakh-evaluation.md)
for the recorded environment, raw measurements and decision gates.
