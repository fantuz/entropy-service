# Entropy Service — Full Architectural and Assurance Review

> **First-pass static review** of repository commit
> [`3e4dfbe71a86708b20ecfa3123b026fd71fe476a`](https://github.com/fantuz/entropy-service/commit/3e4dfbe71a86708b20ecfa3123b026fd71fe476a),
> performed on 2026-11-04. This documents reviewed paths and test evidence,
> not a full audit of every executable statement. There was no independent
> cryptanalysis, hardware assessment, penetration test, live fault injection,
> new benchmark run or endurance test. **No certification is claimed.**
>
> **Primary security specification:** [Trust boundaries and threat model](security-boundaries.md).

> **Later status:** this document preserves the November 4 baseline. Several
> findings have since been addressed (packaged service identity/binding,
> source selection, release validation and the shared TLS key). See the
> [November 10 source-backed follow-up](review-followup-2026-11-10.md) for the
> four findings revisited in the external PDF and remaining priorities.

> The later [reseed failure policy](reseed-failure-policy.md) also addresses
> fail-closed serving for stale/failed seeds, including private children and
> active streams. Source-level cancellation and assurance questions remain.

## Executive summary

The architecture has a coherent division between seed acquisition, one
serving root DRBG, private request/connection generators, independent
HTTP/WebSocket transports, resource admissions, and instrumentation. Important
strengths include **locked generator state**, per-key output ceilings, root
reseed epoch coordination for persistent streams, bounded output and uploads,
request cancellation, and observable source degradation. These properties
are supported by source paths and dedicated unit/race tests.

The principal limitations are **custom cryptographic state transitions without
independent review**, no entropy-source health assessment, no built-in
authentication, retained deterministic output on reseed failure, device-read
cancellation gaps, and permissive packaged privileges/network exposure.
Measured short-loopback performance is valuable, but cannot establish
sustained stability, physical QRNG throughput, or security assurance.

## The five fundamental questions

| Question | Verdict | Code or documentation evidence | Remaining evidence needed |
| --- | --- | --- | --- |
| **Architecturally sound?** | **Yes, with qualifications** | Root/child separation, synchronized DRBG, two admission budgets, controlled WS contexts, embedded static frontend and standalone client | End-to-end resource audit and explicit invariants for every endpoint. |
| **Cryptographically defensible?** | **Limited, assumption-dependent** | SHA-512 seed/key derivation, ChaCha20 and AES-256-CTR stream backends; output ceilings; child derivation | Cryptanalysis of the composed KDF/reseed transitions; assessed source entropy; explicit isolation model and possibly a standardized DRBG. |
| **Operationally stable?** | **Promising but unproven long-term** | Successful CI unit suite, `go test -race` on server, readiness/failure tests, managed WS cleanup, memory-admission controls | Endurance/fault tests, RSS/heap/goroutine trends, device stall and source-recovery exercises. |
| **Performance reproducible?** | **Partially** | Raw `wrk` results, an encoder microbenchmark with recorded host, and `scripts/benchmark-http.sh` | New full-system measurements with commit, DRBG, source, load machine, TLS, Go version, warmup, pprof and latency/errors. |
| **Purpose clear to a new user?** | **Yes** | README quick start and browser landing page, direct APIs, use cases and documentation | Distinguish deployment tiers explicitly; add a bounded, supported-uses declaration and integration examples. |

In particular, the project is presently best described as **a self-hosted
random-data distribution and experimentation platform**. It should not be
marketed as a validated HSM, certified hardware entropy service, or key
management product.

## Component inventory and responsibilities

| Component | Responsibility | Security boundary / notable detail |
| --- | --- | --- |
| `internal/config/config.go` | Flags, limits, source-selection policy | Defaults include `/dev/qrandom0`, HTTP `:8080`, HTTPS `:8443`, `-require-device=false`. |
| `entropy-server.go`: `ResolveEntropyDevice`, `NewEntropySource` | Select/open the startup source | Startup-only fallback; source read is unvalidated for entropy quality. |
| `internal/rdrand` | Optional CPU seed mixing | CPU sample XOR supplementation, not independent certification. |
| `internal/drbg/{algorithm,drbg,connection}.go` | Cipher streams, root reseed, request/connection child derivation | Custom SHA-512 state transition, 256-GiB per-key ceiling, mutexes and reseed epochs. |
| `http_stream.go` and HTTP handlers | Bounded binary responses and cancellation checks | `io.CopyN` avoids response-sized allocations; write may partially succeed before an error. |
| `ws_byte_format.go`, WS handlers and `lifecycle.go` | Frame protocols, writes, WS cancellation/shutdown | Bounded 1-KiB inbound frames; per-connection generator and writer deadlines. |
| `http_budget.go`, `ws_budget.go` | Concurrency and estimated-memory admission | Global within process, not real process memory, identity quotas or anti-DDoS. |
| `upload.go` | Raw octet-stream analysis and SHA-256 | 32 MiB MaxBytesReader, bounded prefix analysis, no intentional upload file persistence. |
| `observability.go`, `activity_peaks.go`, `request_logging.go` | Health, metrics, traffic/peaks, failed-request logs | Honest readiness; metrics and errors can expose infrastructure details. |
| `internal/listener`, `internal/transport` | HTTP/TLS socket configuration | TLS 1.3 configuration; parallel plaintext HTTP endpoint is still present. |
| `web/`, `docs/`, `entropy-client/` | Browser demos, user documentation, CLI consumer | Views are untrusted client-side diagnostics, not RNG security verification. |
| `resources/systemd`, `.github/workflows` | Deployment, build and release | Current packaging is root + unprotected HTTP; release workflow differs from build test gate. |

### Simplified implementation map

```text
  hardware / OS CSPRNG
         │                         [optional CPU mixing]
         └───────────────┬───────────────────────┘
                         ▼
                 64-byte startup read
                         │
                SHA-512 initialization
                         ▼
                    ROOT DRBG ← periodic source reseed (SHA-512 old key + new seed)
                         │
          ┌──────────────┼──────────────────┐
          ▼              ▼                  ▼
  HTTP private child   WS child        direct-root routes
   /v1/data/random   /stream etc.    /v1/data/test, media, words
          │              │                  │
          └──────────────┼──────────────────┘
                         ▼
                   HTTP/WS client
                         │
           operator network/auth/TLS boundary
```

A root DRBG is **not a hardware entropy reservoir**. Fresh device bytes are
requested for startup/reseed. Most output is deterministic expansion from
the current cryptographic state. The removed QRNG software buffer's
compatibility metrics remain zero.

## Detailed answers to secondary questions

### What happens if the physical source disappears?

There are two **distinct phases**:

**Before startup:** if configured hardware is missing/non-character and
`-require-device=false`, resolve to `/dev/urandom`; if an approved path
later fails to open, `NewEntropySource` may fall back to `crypto/rand.Reader`.
With `-require-device=true`, missing or unusable named hardware normally
aborts startup. However, an explicitly **empty path** currently resolves to
`/dev/urandom` even with the required-device flag. A complete 64-byte initial
read must succeed or startup terminates.

**After startup:** the reseed loop holds the already-open reader. It does
**not** switch to the OS CSPRNG on a subsequent read failure. An error is
recorded and readiness becomes 503, but generation can continue from
existing state. A blocking read might instead leave the reseed worker stuck,
causing stale-age readiness without a timely error and possibly delaying
worker termination.

### What guarantees does fallback provide?

When the actual selected source is the host OS CSPRNG, the service inherits
that OS implementation's security assumptions and operating state. Go's
`crypto/rand.Reader` is an OS-backed cryptographic source. Fallback **does
not** preserve a promise of continuing hardware/QRNG entropy, and does not
retroactively establish entropy quality, hardware provenance, or post-compromise
recovery. The CLI's older description of `/dev/urandom` as "weak" should
be corrected. The source label should reflect the **reader actually used**,
not merely a resolved path.

### Can client A infer client B's stream?

No ordinary external API offers direct access to another child's cipher
state. For `/v1/data/random`, the parent generates a fresh child seed on
each request; WS streams also derive their own private generator and refresh
after a root reseed epoch change. Root derivations are synchronized. This
supports computational separation **under standard primitive-security,
secret-state and unpredictable-seed assumptions**.

This is **not a proof of cryptographic isolation**, especially if an
adversary can compromise host memory or if initial seeds are predictable.
All callers share the same root trust domain and can cause resource
contention. Not every endpoint uses a child; `/v1/data/test` explicitly
uses the serving root and some media/word routes draw from it directly.

### How are prolonged reseed failures handled?

The failed attempt increments a failure counter and records the latest
error without updating the root key, timestamp or epoch. `/ready` reports
degradation on error or stale age; `/live` remains green. Output continues
under the existing cryptographic state until a generator's own constraints
or a downstream failure stops it. Recovery on successful seed refresh clears
the recorded root error. There is **no fail-closed generation policy** and
**no guaranteed timeout of a blocking source read**.

For deployments demanding fresh seed material, a readiness-aware gateway
must reject traffic (or the program needs an explicit fail-closed switch).

### Are generator lifetimes, locking and cleanup consistent?

**Positive evidence:** the DRBG uses a mutex to serialize `Read` and
`Reseed`; `ConnectionReader` has its own mutex and an idempotent
`Close`; WS handlers defer source cleanup and connection close; managed
HTTP servers track hijacked WS handler work during shutdown. There are
concurrency and cancellation tests, and the server race suite is active
in CI.

**Residual gaps:** the source reader is neither guaranteed to implement
`io.Closer` nor explicitly closed; indefinite hardware reads are not
cancelled by context. Some legacy/unused DRBG helper methods remain in
the exported type. Resource reservations are accounting estimates, not
proof that every allocation, decoder or transport buffer remains bounded.

### Are cryptographic state and seeds handled safely?

The key/nonce are derived from SHA-512 and kept in generator state; reads
clear the destination before XORing stream output, and parent updates are
locked. The per-key ceiling prevents unlimited output under one cipher
initialization. These are good design choices.

**But key/seed zeroization is not systematic:** Go may retain temporary
startup seeds, derived child seeds, intermediate SHA-512 outputs and cipher
object allocations. `DRBG.Reseed` also appends to `d.key[:]` when constructing
a seed-hash input; this does not expose a value to clients, but deserves a
copy/alias/lifecycle review. Locking memory, disabling or protecting core
dumps, removing stale crypto helper methods and limiting administrative
access are still deployment responsibilities. No independently validated
cryptographic-security strength or prediction-resistance claim is established.

### Which attacks are outside the model?

No security guarantee is made against compromised administrators/kernels,
arbitrary process-memory reads, malicious hypervisors, corrupt source
firmware/drivers, supply-chain compromise, physical/side-channel attacks,
network interception of plaintext output, downstream misuse of bytes,
uncontrolled Internet DDoS, or certified multi-tenant key isolation.
See [the explicit threat register](security-boundaries.md#7-threat-scenarios-and-mitigation-register).

## Priority findings and remediation plan

**Severity is conditional on deployment and intended claims.**
A high-risk **deployment** item is not evidence of an exploitable
cryptographic flaw in a local-only lab installation.

| ID / priority | Finding | Evidence | Proposed next change | Validation |
| --- | --- | --- | --- | --- |
| **P0 deploy** | Root + unauthenticated all-interface HTTP in packaged service | `resources/systemd/entropy-server.service` `User=root`, `ExecStart=... -enable-https=false`; `-http=:8080` | Dedicated user, device group, loopback or private bind, systemd sandbox, documented proxy with TLS/auth and quotas | Deployment smoke test: permissions, bind reachability, systemd security analysis. |
| **P0 deploy** | Public demo TLS private key must not be used as a real identity | Tracked `cmd/entropy-server/key.pem`, existing `SECURITY.md` warning | Require operator-provided unique key/cert, tighten file permissions, avoid bundling production-like secrets | Confirm TLS certificate identity and private key rotation. |
| **P1** | Runtime source-failure policy is fail-open for existing DRBG output | `reseedLoop`, `RecordReseedFailure`, `healthHandler` | Add explicit strict freshness mode or require readiness-aware upstream; document defaults | Loss/error/stall/recovery tests while output is requested. |
| **P1** | Hardware reader can block indefinitely | `io.ReadFull(source, seed)` is not intrinsically cancellable | Device-specific bounded/cancellable read adapter; explicit closure and worker joining | Simulate hung char device/FIFO and TERM. |
| **P1** | Source quality/health tests absent | Startup validates character type and full read, not entropy content | Assess trusted device/driver; add source-appropriate online tests, avoid unearned entropy credits | Constant/stuck/correlated source fault injection. |
| **P1 assurance** | Custom DRBG/reseed construction | SHA-512 seed init and old-key+new-seed reseed, AES-CTR/ChaCha20 | Expert cryptographic review; clarify or replace with standardized construction if required | Independent design/implementation review and test vectors. |
| **P1** | `-require-device=true` with empty path accepts OS source | `ResolveEntropyDevice` empty-path branch | Reject empty path under strict flag, add negative config test | `go test` startup selection matrix. |
| **P1** | Open-time fallback metadata may claim hardware path | `ResolveEntropyDevice` and `NewEntropySource` separate stages | Single source descriptor reflecting **actual opened reader** | Force open failure after successful stat; inspect `/ready`. |
| **P1** | No auth/identity-level rate limiting | HTTP/WS handlers and metrics share unauthenticated mux | Access proxy, network firewall, per-identity admission and monitoring policy | Cross-origin/no-Origin and hostile-load tests. |
| **P2** | Cryptographic state lifetime not minimized | Go seed/key copies, unsanitized temporary arrays, public `DRBG.Stream`/helpers | Remove or repair dormant public helpers, audit seed copies and memory dumps | Dedicated helper tests, static analysis and threat-model review. |
| **P2** | Tagged release does not explicitly gate on full tests/race/lint | `.github/workflows/release.yml` invokes GoReleaser only | Tie release to successful build checks for tagged SHA | CI dry-run/failure injection. |
| **P2** | Estimated admission memory is not actual RSS | `http_budget.go`, `ws_budget.go` | Compare reservation gauges with heap/RSS; add telemetry if needed | Endurance load and pprof. |
| **P2** | Extensive commented-out legacy implementations | `entropy-server.go`, `internal/drbg/drbg.go`, `internal/listener/listener.go` | Delete unused/commented prototypes in a behavior-preserving cleanup | Full CI and focused regression. |
| **P3** | Reproducible historical performance incomplete | `docs/benchmarks.md` notes missing revision/backend/source | Record full environment and rerun standardized trials; keep old numbers qualified | Comparisons with raw logs and error/latency distribution. |

## Operational and performance assurance

**Current positive evidence:** source tests cover both generator backends,
parent/child reseed transitions, concurrency, request budgets, WS cleanup,
readiness degradation and recovery, and the build workflow runs Go tests,
JS tests, server race detector, lint and GoReleaser snapshot. The reviewed
head's GitHub Actions build
[`#41690698162`](https://github.com/fantuz/entropy-service/actions/runs/41690698162)
completed successfully.

**Not measured by this review:**

- 1/4/24-hour workloads, goroutine/heap/RSS plateau, device read stalls,
  failover recovery times, production TLS overhead or load under a proxy.
- Memory-compromise resilience, robust independence across children,
  source health-test effectiveness, or standardized DRBG conformance.
- New throughput on the reviewed commit or production hardware.

### Recommended evidence matrix

| Exercise | Conditions | Success criteria to establish before testing |
| --- | --- | --- |
| HTTP sustained load | 1h small/large payloads; multiple concurrency limits | Stable error rate, bounded heap/RSS and goroutine count, documented latency and throughput. |
| WS sustained load | 4h steady stream and churn | No leaked instances or reservations; predictable cancellation and disconnections. |
| Mixed HTTP + WS | 1h with intentional pressure | Denied admissions and peak counters reconcile; healthy clients remain responsive. |
| Device failure/recovery | Missing/open error, partial read, EIO and hung read | Document exact readiness status and chosen output policy; demonstrate recovery or timed failure. |
| Cryptographic regression | Both backends, epoch changes, key ceiling, diverse outputs | Correct state transitions and expected failures; **not** a proof of unpredictability. |
| Bench reproducibility | Same SHA, flags and hardware, ≥3 trials per case | Full raw output, medians, p50/p99, error counts and source/DRBG descriptions. |
| Release / deployment | Clean package install on isolated host | Dedicated identity, key permissions, private bind, no unprotected public endpoints. |

## Architecture evolution recommendation

Keep the current **one-root / derived-child** structure for the next
stabilization phase while hardening its boundaries. Do **not** add another
DRBG backend, networking protocol or dashboard feature before establishing
source failure policy and authenticated deployment guidance.

Treat two changes as separate efforts:

1. **Documentation/assurance:** this review states the current behavior,
   assumptions, out-of-scope threats, and concrete validation gaps.
2. **Production-code remediation:** later, independently test strict source
   selection, cancellable hardware I/O, safer deployment defaults, and
   cryptographic API cleanup. Those fixes have behavior or compatibility
   consequences and deserve focused PRs and operator approval.

See [security boundaries and threat model](security-boundaries.md),
[the project security policy](../SECURITY.md),
[operations](operations.md), [API reference](api.md),
[benchmarks](benchmarks.md), and [tests](../CONTRIBUTING.md).

