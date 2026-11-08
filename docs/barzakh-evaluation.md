# Barzakh-521 backend evaluation and proposal

**Recommendation: retain AES-256-CTR and ChaCha20 for serving; keep
Barzakh-521 as a separate research experiment for now.** It is interesting
as a different generator construction, but the measured implementation is
much slower and has unresolved licensing and integration questions.

This review, dated 2026-11-08, uses upstream
[`khettany/barzakh-521-V1` at `871900c7`](https://github.com/khettany/barzakh-521-V1/tree/871900c7d9a2ff4d3a4580a4521b6fdb450c3c40).
It proposes a possible future backend; it does not introduce a new `-drbg`
value or alter the existing seed/reseed construction, defaults or releases.

## What would change

Barzakh is a complete deterministic generator, rather than an encryption
cipher stream. Its state transition uses a rational quadratic expression
and modular inversion in the prime field modulo `2^521 - 1`, emitting
32 bytes per step. Upstream expands the seed with SHAKE256 into five field
elements. Its v1 batch implementation interleaves eight separately seeded
lanes and amortizes inversion using Montgomery's batch-inversion trick.

The existing Go backends fit `cipher.Stream` and use the project's SHA-512
seed/reseed construction. Substituting a complete CSPRNG requires an explicit
seed mapping, reseed policy and byte-reader adapter; it cannot be justified
as merely another cipher name. A 521-bit field does not by itself establish
521-bit security. No deterministic backend creates additional physical
entropy from the same seed.

## Measured performance

Environment: Linux amd64 VM, Intel Xeon Platinum 8573C, AES/AVX2 available,
Go 1.26.10, Rust 1.99.1 (`58cf8f9c3`), one benchmark thread. Rust uses
release optimization, fat LTO, one codegen unit and
`RUSTFLAGS='-C target-cpu=native'`; Go uses the normal accelerated build
and `-cpu=1`. Both use the public 64-byte `00 01 … 3f` seed. There is no
QRNG/device read, HTTP, WebSocket or TLS in these timings.

Five repetitions per case, each targeting at least 500 ms after warmup:

| Implementation | Buffer | Median MB/s | Observed range MB/s | Median time per fill |
| --- | ---: | ---: | ---: | ---: |
| Existing Go ChaCha20 DRBG | 64 KiB | 603.09 | 598.76–608.38 | 108.7 µs |
| Existing Go AES-256-CTR DRBG | 64 KiB | 6015.31 | 5866.00–6054.88 | 10.9 µs |
| Upstream Barzakh v1 single | 64 KiB | 1.136 | 1.036–1.173 | 57.7 ms |
| Upstream Barzakh v1 batch ×8 | 64 KiB | 7.864 | 7.503–7.965 | 8.33 ms |
| Upstream Barzakh v1 single | 1 MiB | 1.121 | 1.063–1.163 | 935 ms |
| Upstream Barzakh v1 batch ×8 | 1 MiB | 7.853 | 5.795–7.940 | 134 ms |

MB/s means decimal megabytes per second; buffer sizes use binary units.
The batch implementation is approximately **77× slower than ChaCha20**
and **765× slower than AES-256-CTR** for the measured 64 KiB case. One
1 MiB batch case slowed noticeably; the table retains that spread rather
than hiding it behind a single median. These are short implementation
microbenchmarks on one VM, without confidence intervals or a capacity claim.

Construction/destruction also costs more:

| Implementation | Median new + close/drop |
| --- | ---: |
| Existing Go ChaCha20 DRBG | 0.739 µs |
| Existing Go AES-256-CTR DRBG | 1.007 µs |
| Upstream Barzakh v1 single | 3.760 µs |
| Upstream Barzakh v1 batch ×8 | 30.756 µs |

The Go measurements include the current DRBG wrapper, metrics and locking;
Rust measures upstream without an HTTP/server wrapper. Go steady 64 KiB
reads reported zero allocations. The upstream v1 batch path allocates
several vectors per 256-byte batch. Neither measurement includes transport,
an actual entropy source or periodic reseeding. New/drop timings include
different lifecycle implementations; they do not establish memory-erasure
equivalence.

At the measured batch rate, producing 16 MiB would take roughly 2.1 seconds
of generator time on one core. That is a rate projection, not a measured
16 MiB request latency. Even ideal four-core scaling from this result would
be around 31 MB/s before service overhead, far below a 2 GB/s bulk target.
There is no measured throughput benefit for this project's bulk endpoints.

Raw data: [Go output](benchmark-results/barzakh-2026-11-08/go-backends.txt)
and [Barzakh JSONL](benchmark-results/barzakh-2026-11-08/barzakh-v1.jsonl).
The [standalone harness](../tools/barzakh-bench/README.md) contains exact
commands and a lockfile. Run the two programs sequentially; no production
server, hardware device or release configuration is needed.

## Compatibility and assurance findings

- **v1 tests:** the upstream release tests passed (10 unit + 3 integration).
  The harness's two read-contract tests also pass. These tests validate
  exercised arithmetic/API behavior, not unpredictability.
- **Partial reads:** v1 `fill_bytes` consumes a full 256-byte batch even
  when the caller requests fewer bytes, discarding the rest. An adapter
  must buffer remainders if stream output is to stay independent of read
  boundaries, as the existing DRBG tests require.
- **v2 is separate source:** Cargo currently selects `src/lib.rs` (v1),
  not `src/v2-constant-time/lib.rs`. Compiling the latter unchanged with
  the same dependency versions and Rust 1.99.1 fails with E0502 at line
  519 (overlapping mutable/immutable borrows of `dest`). This review does
  not silently patch it or attribute v1 throughput to constant-time v2.
- **Versions are not interchangeable:** v1 and v2 assign SHAKE output to
  their parameters in a different order. v1 appends an eight-byte lane
  index, whereas v2 appends one byte; v2's batch method calls each lane's
  scalar generator instead of using the shared-inversion path. The README's
  matching-output claim needs interoperability fixtures before reliance.
- **Security claims:** PractRand/BigCrush passes, avalanche measurements
  and solver timeouts are statistical or bounded experimental evidence;
  they are not independent cryptographic certification. The README-linked
  paper URL returned HTTP 404 during this review, so its formal arguments
  were not assessed. This is an implementation/performance evaluation,
  not a security audit of the construction or proof of constant-time code.
- **License:** entropy-service is MIT. The pinned upstream LICENSE declares
  AGPL-3.0-or-later plus a four-core notice and a pending-patent notice.
  The extra restriction needs clarification; linking and distributing this
  dependency cannot be treated as a transparent MIT-only addition. No
  upstream implementation or binary is vendored in this proposal.

The standalone Rust tool is opt-in and outside the Go module and release
builds. Its dependency is still subject to upstream terms when built.
Existing CI verifies the Go application; it does not run this Rust experiment.

## Proposed path for an experimental backend

1. **Clarify terms and freeze a version.** Resolve the four-core and patent
   notices with upstream before distribution/integration. Choose v1 or a
   corrected, reviewed v2 and obtain deterministic seed/output fixtures.
2. **Review the construction.** Obtain independent cryptographic analysis
   appropriate to the intended use. Define a reseed policy, per-state
   output ceiling and source-failure semantics; do not inherit ChaCha20's
   counter limit as an asserted Barzakh security bound.
3. **Prototype behind an explicit experimental build.** Decide between a
   reviewed Go implementation and a narrow Rust boundary. Avoid per-32-byte
   cgo/IPC calls. Preserve existing default builds, backend labels and
   existing AES/ChaCha deterministic outputs. An external process is an
   isolation option, not a licensing workaround.
4. **Specify lifecycle and resource costs.** Document the root/child seed
   mapping, domain separation, state ownership, reseed epochs, Close and
   best-effort clearing. Account for eight lanes and residual output buffers
   in admission estimates, and serialize state access or allocate children.
5. **Validate the adapter.** Add known-answer, short/unaligned read,
   reseed, child-independence, close, concurrent-use and output-limit tests.
   Compare actual HTTP and WebSocket latency, throughput, allocations and
   resource budgets under bounded load against both current backends.

The benefit to investigate is comparative CSPRNG research and an educational
demonstration of modular-inversion cost. Current measurements do not support
adding it to improve throughput, increasing seed entropy, or presenting it
as stronger cryptography than the existing backends.

[Technical reference](README.md) · [DRBG construction](drbg-construction.md)
