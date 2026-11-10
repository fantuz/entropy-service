# Follow-up to the November 9 external review

Compared the supplied Claude PDF with source at `66ce1ce` (PR #51), then
implemented the TLS changes described below. The PDF explicitly could not
read the source tree or linked documentation: its recommendations are review
input, not executable instructions or an independent cryptographic audit.
This follow-up is also a source review and targeted test exercise, not an
audit of deployed hardware, external hosts or entropy quality.

| PDF finding | Current evidence | Assessment and remaining work |
| --- | --- | --- |
| 1. Committed TLS private key | The reviewed tree still tracked `cmd/entropy-server/cert.pem` and `key.pem`; configuration loaded them from the working directory. | **Confirmed and addressed by this change.** Removed both files, added explicit unique certificate generation and startup provisioning documentation, rejected the retired public key in the TLS loader, and added tracked-credential guards to build/release CI. External users of the old key must rotate their own deployments. |
| 2. Reseed-failure policy | `observability.go` and `internal/config/config.go` already implement `-max-reseed-age`, `/ready` 503 on stale/failed reseeds, and age/failure/readiness metrics. `reseed_test.go` exercises source failures, invalid sizes and recovery; `observability_test.go` covers stale/failure/recovery readiness. | **Partly addressed; PDF understates existing controls.** Output continues from existing state. A selectable fail-closed generation policy remains absent, including for existing WebSockets and legacy root readers. A blocked device read also lacks a source-level deadline. |
| 4. Reviewable DRBG | [Construction specification](drbg-construction.md) specifies seed/reseed bytes, child derivation, usage ceiling and state transitions. `internal/drbg/algorithm_test.go` includes NIST AES-CTR and RFC 8439 ChaCha20 primitive vectors plus fixed initial/reseed output fixtures; `lifecycle_test.go` checks the SHA-512 transition. | **Specification and vectors already exist.** The composition remains custom, without independent cryptographic assurance or SP 800-90A conformance. Changing the default to HMAC_DRBG is a separate design/migration decision, not a safe incidental cleanup. The Barzakh backend is excluded from normal builds by its experimental build tag. |
| 7. Benchmarks | [Benchmark documentation](benchmarks.md) qualifies historical HTTP results, publishes raw logs and describes an encoder microbenchmark with revision/runtime/host and repeated medians. `scripts/benchmark-http.sh` records some environment information. [Lifecycle benchmark instructions](drbg-lifecycle-benchmarks.md) add repeatable microbenchmarks. | **Still partial.** Historical HTTP runs lack server revision/backend/source/runtime. The HTTP script does not verify running-server provenance or aggregate repeated scenarios into a JSON median/spread report. A new controlled full-system benchmark campaign remains useful; no replacement throughput claim was invented in this review. |

## Other observations

- **Authentication remains external.** Neither HTTP nor WebSockets authenticate
  clients. The packaged systemd unit is now unprivileged, loopback-only,
  sandboxed and operator-started; the architecture review's November 4 root/
  all-interface package finding is historical. Standalone defaults still bind
  all interfaces, so remote deployments need an explicit protected boundary.
- **WebSocket origins are deliberate and tested.** `http_stream_integration_test.go`
  admits no-Origin native clients and same-origin browsers while rejecting
  cross-origin/null browser requests. Origin checking is not authentication;
  there is no configurable origin allowlist.
- **Routes retain compatibility.** Versioned binary endpoints coexist with
  legacy media/WebSocket routes. PR #51 added canonical `/metrics` without
  duplicate metric families; `/metrics-1` and `/metrics-2` are compatibility
  exports. Scrape the canonical endpoint once per process.
- **Release validation has advanced.** The release workflow now requires
  successful full tests, race tests, vet, lint, vulnerability checks and
  snapshot packaging before publishing a tag. The older architecture review
  describes the baseline before that gate existed.

## Suggested next work

1. Design an opt-in strict freshness policy across **every** generation path,
   with tests for failure, stale age, recovery and already-open WebSockets.
   Decide how to handle an entropy reader that blocks before implementing it.
2. Commission an independent review of the custom DRBG composition; use the
   specification and output fixtures as inputs. Do not infer assurance from
   statistical tests or introduce a new default without a migration plan.
3. Run a repeatable HTTP/TLS benchmark campaign recording the running binary,
   complete flags, source, backend, runtime, load host, repetitions and errors.

The TLS provisioning fix is separate from these cryptographic, availability
and measurement projects. It does not change reseed semantics or DRBG output.
