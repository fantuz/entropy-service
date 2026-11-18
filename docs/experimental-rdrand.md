# Experimental direct CPU output (RDRAND)

`-drbg=rdrand-experimental` serves CPU-generated RDRAND words instead of
ChaCha20 or AES-256-CTR keystream. It is useful for comparing CPU hardware,
virtual machines, throughput and failure handling using the existing HTTP,
WebSocket, analysis and visualization tools. It is an explicit research option
in ordinary native amd64 builds, with no new dependency or separate build tag.
The default software backends and existing seed-mixing options are unchanged.

## What changes in the trust model

RDRAND exposes the processor vendor's internal random-number generator,
including its internal conditioning/expansion. It is **not raw sensor output**,
a fresh independent physical entropy measurement for every returned bit, or a
CPU implementation of this application's SHA-512 seed/reseed construction.
Software cannot inspect its hidden state or provision its internal key.

The CPU, firmware/microcode and, in a VM, hypervisor must be trusted. CPUID
advertises an instruction; it does not prove the platform's implementation is
secure. A hypervisor can mask or emulate it. Passing statistical tests, a
successful startup probe or CF=1 is not an independent entropy/security
certification. A faulty or malicious platform can return plausible but
predictable data without raising a detectable error. This backend does not add
an independent online entropy-health test.

| Selection | Where output comes from | Effect of configured source bytes |
| --- | --- | --- |
| `aes-256-ctr`, `chacha20` | Application-owned software stream | SHA-512 seed/reseed construction and private child streams |
| Existing `-cpu-mix` | Still the selected software stream | Supplements the source with RDSEED, falling back to RDRAND when appropriate |
| Existing `-rdrand-mix` | Still the selected software stream | Supplements the source with RDRAND; a mixing failure retains source bytes |
| `rdrand-experimental` | Successful RDRAND64 words, little-endian | Source bytes only maintain the existing serving freshness policy; **not used to seed, mix or condition output** |

For a conservative production default, retain AES/ChaCha with an appropriate
OS/device source. Seed mixing can retain the independent source's contribution
without making bulk generation depend directly on CPU instruction availability.
Direct CPU mode has value as an observable comparison path, rather than a claim
of stronger cryptography or better speed.

## Linux and Windows examples

Linux native amd64, local testing:

```sh
./entropy-server -drbg=rdrand-experimental -device=/dev/urandom \
  -require-device=true -enable-https=false -http=127.0.0.1:8080
curl --fail 'http://127.0.0.1:8080/v1/data/random?bytes=1048576' -o cpu-sample.bin
./entropy-client -mode=pull -url='http://127.0.0.1:8080/v1/data/random' \
  -slice=1048576 -duration=10s -waterfall
```

Windows PowerShell, native x64:

```powershell
.\entropy-server.exe -drbg=rdrand-experimental -device=windows-csprng `
  -require-device=true -enable-https=false -http=127.0.0.1:8080
# In another terminal:
.\entropy-client.exe -mode=push -wsurl=ws://127.0.0.1:8080/stream `
  -slice=65536 -duration=10s -waterfall
```

HTTP-only examples bind to loopback for initial testing. Use the existing
[certificate provisioning](tls-certificates.md) and client trust instructions
for remote TLS deployment. The desktop server GUI offers the same explicit
backend in its Output backend selector; the client needs no new backend flag.

To validate options and CPUID support without opening sources/listeners or
executing an output probe:

```sh
./entropy-server -drbg=rdrand-experimental -describe-json
```

Only native **amd64** builds with advertised RDRAND support can select this
backend, including Linux x64 and Windows x64. ARM, ARM64, 386 and `purego`
builds reject selection with an actionable error. Existing other backends
remain available there. No RDSEED support is required for direct output.

## Failure handling and limits

- Each 64-bit word receives **one attempt plus ten retries** (11 maximum),
  with `PAUSE` after a failed attempt. Only carry-flag success is accepted;
  zero is a valid successful word. These are the existing instruction helpers,
  not a new unbounded retry loop. RDSEED remains a seed-material operation.
- A read of `N` bytes consumes `ceil(N/8)` successful words. Unused tail bytes
  are discarded, rather than buffered between calls. Concurrent connections
  use the CPU facilities without application-owned independent keyed streams;
  there is no deterministic replay or seed-to-output fixture for this mode.
- An exhausted hardware retry budget clears the **entire current read buffer**
  and returns zero bytes. Successfully delivered earlier HTTP chunks/WS frames
  cannot be retracted. Failed reads do not increment generated-byte totals.
- The first CPU output failure latches across the root and every child. Output
  remains denied even with `-on-reseed-failure=continue` or a later successful
  external-source read. There is no OS/AES/ChaCha fallback and no automatic
  recovery probe. Startup performs and discards a 64-byte output probe.
- The latch wakes generation-policy watchers so idle/blocked WebSockets are
  cancelled too. In-flight CPU reads check the latch before returning output.
  Fresh source state cannot hide a CPU failure: `/ready` returns 503,
  `generation_allowed=false`, and includes `backend_error`. `/live`, metrics,
  static pages and independent LAB sources remain available for diagnosis.
- The shared `rng_backend_failures_total` counter becomes 1, excluding optional
  seed-mixing errors. Startup/failure logs identify direct CPU mode. Existing
  `X-RNG-DRBG` identifies RDRAND and `X-RNG-Output-Mode: direct-cpu` distinguishes
  it from `seeded-stream`. `X-RNG-Source` still means the policy source.
- Investigate host load, CPU/VM capability and firmware after a failure. Restart
  explicitly to test a new instance; switch the configured backend explicitly
  if software generation is desired. The latch is an operational fail-closed
  choice, **not proof that a transient CF failure compromised earlier bytes**.

The external source still performs the usual 64-byte startup read and periodic
reads. Keeping its admission/freshness policy avoids silently bypassing existing
operator controls. Those events do **not** reseed the CPU's internal DRNG.
Selecting an unavailable required source can therefore prevent startup/output
even though RDRAND itself works. Use the OS source in the examples for a simple
CPU-only output experiment. Mixing flags affect policy input only in this mode.

The software 256 GiB per-key ceiling does not apply: there is no application CPU
key to rotate. HTTP/WS sample-size, concurrency, memory and cadence limits still
apply. A configured 20 FPS is a maximum requested cadence, not a delivery-rate
guarantee; large frames or competing requests can exceed the frame interval.

## Timing and cancellation boundaries

The retry count bounds **instruction attempts**, not elapsed time. CPU model,
clock, firmware, contention, VM exits and host scheduling affect latency.
Neither an instruction nor a currently executing synchronous Go `Read` can be
interrupted by its caller's context. HTTP generation checks cancellation between
32 KiB copy reads; WebSocket generation fills its selected frame before the
next context check. Root freshness-policy changes synchronize with active reads.
Choose moderate frame sizes for responsive cancellation and test under actual
load. Do not interpret a microbenchmark or `-source-timeout` as a hard deadline
for CPU output generation.

Readiness measures observed failures and external policy state, not a prediction
that the next instruction will succeed. Multiple clients can compete for CPU
resources; more goroutines need not increase aggregate hardware throughput.
Avoid exposing unrestricted public load against this experimental path.

## Reproducible performance evidence

The accompanying [measurement evidence](benchmark-results/cpu-backend-2026-11-18/)
compares the unchanged benchmark harness on pre-feature main `5a4199791129946f`
and this implementation. It is a virtualized AMD EPYC 9V74, Linux amd64,
Go 1.26.11, with RDRAND and the hypervisor CPUID bits set. No physical CPU
qualification or Intel/Windows timing claim is made.

Pinned median results (decimal MB/s):

| Backend | 64 KiB read | 1 MiB read | 1 MiB elapsed |
| --- | ---: | ---: | ---: |
| AES-256-CTR | 6,174 MB/s | 6,071 MB/s | about 0.173 ms |
| ChaCha20 | 649 MB/s | 639 MB/s | about 1.64 ms |
| Direct RDRAND | 308 MB/s | 316 MB/s | about 3.32 ms |

All measured reads reported **zero allocations**. RDRAND's amortized rate is
about 25 ns per 64-bit word here, including wrapper/loop overhead, not a raw
instruction-latency guarantee. An 8 MiB frame would take roughly 27 ms at this
single-reader rate before networking or analysis; contention or retries can
make it longer. Treat that as a size/rate estimate, not a measured frame SLA.

Against pre-feature main, six alternating, CPU-pinned trials gave software
throughput median changes of +0.34% / −0.36% for AES (64 KiB / 1 MiB), and
+1.70% / +0.13% for ChaCha20. These differences are within the observed run
variation and show no clear software-backend slowdown in this measurement.
They do not rule out small regressions elsewhere. Initial unrestricted runs
were noisier and are retained alongside the pinned evidence.

Run the included harness with:

```sh
go test -run '^$' -bench '^BenchmarkOutputBackends$' \
  -benchtime=300ms -count=3 -cpu=1 ./cmd/entropy-server/internal/drbg
```

This measures complete synchronized generation reads, including metrics, with
no network, renderer or statistical analysis. Allocation counts are reported
by Go; they are not an assertion that the CPU instruction itself allocates.
Benchmarks are opt-in and do not run during ordinary `go test`.

## Test evidence and remaining qualification

Deterministic fault-injection tests cover partial-buffer clearing, valid zero
words, tail handling, no unused child seeds, shared failure latching, in-flight
output discard, notification, Close, source gating, failure across reseeds and
byte accounting. They run without CPU instructions on unsupported architectures
and pure-Go builds. Native capability tests and binary HTTP/WebSocket tests use
actual RDRAND when the CI host advertises support; an unsupported host skips
those instruction tests, not the simulated failure tests. Linux race tests,
Windows-native tests and pure-Go selection tests cover the platform boundaries.

Exhausted CF retries, physical firmware defects, VM emulation quality,
multi-socket contention and real-hardware entropy/security qualification still
require host-specific evidence. A successful CI run is not evidence that those
physical conditions were tested.

Instruction reference: [Intel Intrinsics Guide](https://www.intel.com/content/www/us/en/docs/intrinsics-guide/index.html#text=rdrand)
and the processor vendor's current architecture/software manuals. The project
uses CPUID leaf 1 ECX bit 30 for RDRAND; it does not assume RDSEED merely because
RDRAND is available.
