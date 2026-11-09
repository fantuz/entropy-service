# Experimental Barzakh-521 backend

The optional `barzakh-521-experimental` backend is an original Go
implementation of the published scalar Barzakh-521 construction. Compile
with `-tags=experimental_barzakh`, then select it explicitly at startup.
Normal builds and release packages exclude the implementation and retain
their existing AES-256-CTR/ChaCha20 defaults.

Use it for local demonstrations and research. It uses **variable-time
`math/big` arithmetic**, has no independent security audit, and is unsuitable
for generating secrets. Matching reference outputs and passing statistical
tests do not establish security or superiority over Linux randomness. It
still needs an unpredictable seed and does not create fresh physical entropy.

## Build and run

From the repository root, using the Go version required by `go.mod`:

```sh
go build -tags=experimental_barzakh \
  -o ./bin/entropy-server-experimental ./cmd/entropy-server

./bin/entropy-server-experimental \
  -drbg=barzakh-521-experimental \
  -device=/dev/urandom -require-device=true \
  -enable-https=false -http=127.0.0.1:8080
```

Startup logs an experimental-use warning. HTTP `X-RNG-DRBG` and health/metrics
metadata identify `Barzakh-521 (experimental)`; the source remains the actual
seed source. Selecting this algorithm in a normal binary fails with the
required build tag in the error. There is no silent fallback.

In another terminal:

```sh
curl --fail -D - 'http://127.0.0.1:8080/v1/data/random?bytes=4096' \
  -o sample.bin

# Existing client; no experimental client build is needed.
./bin/entropy-client -mode=stream \
  -wsurl=ws://127.0.0.1:8080/stream \
  -slice=4096 -refresh=100ms -headless -duration=10s
```

Existing admission controls, request limits, reseeding and private HTTP/WS
generator ownership remain in force. Start with small payloads: arithmetic
and allocation costs differ substantially from the optimized ciphers. The
64 KiB per-request/connection admission overhead remains an estimate, not an
RSS or cumulative GC-allocation limit. Arithmetic operand sizes are bounded
by the fixed 521-bit field and fixed transition.
Generation is synchronous within each Read; large frames can delay cancellation
and shutdown until that Read finishes.

## Exact construction and integration

Reference: Abdelghaffour Khettany's
[Barzakh-521 v1 repository](https://github.com/khettany/barzakh-521-V1/tree/871900c7d9a2ff4d3a4580a4521b6fdb450c3c40).
This implements the **scalar v1** formula and parameter mapping, without the
upstream eight-lane batch optimizer or the separate v2 implementation.

The existing [DRBG construction](drbg-construction.md) derives `K || N` from
`SHA-512(seed)` (32 key bytes, 16 nonce bytes). This backend expands those
48 bytes with SHAKE256 into five consecutive 66-byte, big-endian integers,
each reduced modulo `p = 2^521 - 1`. Assignment order is `w, x, a, b, W`.
If `w` is even, set `w = (w + 1) mod p`.

Each 32-byte output block follows:

```text
d = (x - a) mod p
if d == 0:
    x = (x + W) mod p
    d = 1
k = ((a*x*x + b) * inverse_mod(d, p) + w) mod p
x = W XOR k
if x == p:
    x = 0
output = big_endian_32_bytes(k >> 265)
```

Inversion uses variable-time `big.Int.ModInverse`. Unused output is retained
across reads, so splitting a request into one-byte reads preserves stream
continuity. The enclosing DRBG clears the destination before requesting
keystream, as for existing backends.

Reseeding keeps `SHA-512(oldKey || seed)` and recreates this backend from the
resulting 48 bytes. Parent/child derivation and reseed epochs are unchanged.
The existing 256 GiB per-key operational ceiling applies; it is **not a
proven Barzakh security bound**.

Reseed replacement and Close clear owned integer backing arrays and unused
output. This is best-effort clearing: temporary `math/big` allocations,
SHAKE internals, compiler/runtime copies and swap/core dumps cannot be
guaranteed erased. Close stays idempotent and releases instance accounting
once. The existing DRBG mutex serializes access.

## Copyright licensing and patent boundary

The new Go source follows entropy-service's **MIT license**. It contains
newly written standard-library arithmetic and stream/lifecycle integration;
it does not copy upstream Rust source, import its library, use cgo, or bundle
an upstream binary. Mathematical rules and implementation source are
distinct: publishing an algorithm does not make its source public domain.
Upstream was reviewed during evaluation, so this is not a claim of a formally
separated clean-room process or legal certification.

Upstream source declares AGPL terms, a four-core notice and a pending patent.
Those notices matter if its implementation is reused; a build tag by itself
does not avoid copyleft obligations. This backend introduces no upstream
AGPL component and does not change LICENSE. Attribution acknowledges the
construction rather than importing source under a different license. The
separate Rust research harness proposed in PR #34 has an upstream dependency
and must not be confused with this Go implementation.

**Patent clearance remains unresolved.** Independent source, public formulas,
free availability and experimental use do not automatically grant patent
permission or establish an exemption. An unreferenced pending-patent notice
is not evidence of an enforceable granted claim, nor proof that no relevant
application exists. This PR does not verify ownership, application numbers,
jurisdictions, claims or freedom to operate, and does not assume an upstream
patent grant covers this implementation. Obtain clarification and qualified
legal review before depending on it for distribution/deployment where such
rights could matter. No permission is asserted on the author's behalf.

## Verification and measurement

```sh
# Normal builds reject the selection; existing fixtures still pass.
go test ./...

# Includes fixtures, short reads, parent/child epochs, failures, accounting,
# owned-state clearing, HTTP headers and real WebSocket cleanup.
go test -race -tags=experimental_barzakh \
  ./cmd/entropy-server/internal/drbg \
  ./cmd/entropy-server/internal/config ./cmd/entropy-server

# Match payload, seed, toolchain and CPU when comparing all three backends.
go test -tags=experimental_barzakh ./cmd/entropy-server/internal/drbg \
  -run '^$' -bench '^BenchmarkDRBGLifecycle/.*/(NewClose|Read64KiB)$' \
  -benchtime=500ms -benchmem -count=5 -cpu=1
```

Fixtures use public synthetic seeds and an independent Python `hashlib`/
integer-arithmetic reference. The direct 48-byte seed fixture also matches
the pinned upstream scalar v1 program. Existing AES/ChaCha fixtures are
unchanged. CI race-tests the tagged build; releases still omit the tag.

Measurements must name this **Go scalar implementation**. Upstream Rust
single-stream, Rust batch and a future constant-time implementation have
different costs. Rust rates from the separate evaluation are not this
backend's measured throughput.

On 2026-11-09, Linux amd64, Intel Xeon Platinum 8573C VM, Go 1.26.10,
one benchmark thread (`-cpu=1`), five repetitions of at least 500 ms each,
the command above measured the following 64 KiB fills:

| Backend | Median MB/s | Observed range MB/s | Median time/fill |
| --- | ---: | ---: | ---: |
| ChaCha20 | 621.24 | 605.45–622.89 | 105.5 µs |
| AES-256-CTR | 6243.84 | 6135.36–6301.72 | 10.5 µs |
| Go scalar Barzakh-521 experimental | 4.77 | 4.75–4.85 | 13.75 ms |

MB/s is decimal. All cases include the same DRBG wrapper and public
`00 01 … 3f` seed, with no device reads, HTTP, WebSocket or TLS in the timed
region. Barzakh's median 64 KiB read allocated about 3.42 MB cumulatively in
30,722 allocations; the two established backends reported zero allocations.
That is allocation traffic, not simultaneous live memory. This reference
implementation prioritizes inspectable mathematics over bulk performance;
the measurements are not service-capacity or security claims.

[Raw benchmark output](benchmark-results/barzakh-go-2026-11-09/go-backends.txt)
retains every repetition and constructor/Close timing. A separate actual
binary smoke test exercised normal-build rejection, tagged-server headers,
and one-second headless client runs over both HTTP and WebSocket. Cross-builds
for ARMv7 and arm64 do not substitute for execution on those processors.

[Configuration](configuration.md) · [Technical reference](README.md)
