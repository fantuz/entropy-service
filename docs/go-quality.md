# Go conventions, modules and test coverage

The project follows many Go conventions and has automated checks for
formatting, vet, static analysis, race detection and dependency
vulnerabilities. Go has no formal coding-guideline compliance certificate;
a green build is evidence about the checks and paths exercised. This review
uses [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments),
[Effective Go](https://go.dev/doc/effective_go) and
[Go module maintenance guidance](https://go.dev/doc/modules/managing-dependencies)
as references.

## What is already in good shape

- `cmd/entropy-server` and `cmd/entropy-client` are separate commands in one
  module. Their `internal` packages limit accidental external coupling.
- gofmt-compatible formatting and the configured golangci-lint rules run
  in CI. Optional style rules disabled in `.golangci.yml` are documented;
  enabling every opinionated linter is not a compliance requirement.
- DRBG tests include fixed seed/reseed outputs, the NIST AES-CTR vector,
  read continuity, key lifecycle, exhaustion and concurrent reseeding.
- HTTP/WS tests exercise validation, byte limits, admission budgets,
  shutdown, cancellation, private generators and resource release.
- Client tests distinguish received and analysed bytes and exercise timed
  reports, interrupted runs, writer errors and stream-buffer ownership.

New Go code should use conventional initialisms such as `HTTP`, `URL` and
`ID`, document exported declarations, keep error strings concise and use
`%w` when preserving an underlying error matters. Return errors from helpers
rather than calling `log.Fatal` or `os.Exit`; commands decide how to report
fatal startup errors. Use explicit contexts, bounded allocations/queues,
and clear `Close`/`Release` ownership. Avoid copying values containing locks.
Some existing identifiers and commented prototypes still need cleanup;
rename or remove them in focused changes after checking callers and wire
formats. Formatting alone does not establish correct behavior.

## Module and toolchain policy

There is **one root `go.mod` and `go.sum`**. Both binaries are released
together and share the same dependency graph; no nested module, workspace
file or split module path is needed.

`go 1.26.10` declares the minimum supported Go toolchain, including the
project's security-patch baseline. Go's toolchain selection may download a
suitable version when `GOTOOLCHAIN=auto`; installations that disable that
behavior must provide a sufficiently recent Go themselves. An extra
`toolchain` directive pointing to the same version adds no useful policy
here. CI and release builds read the version from `go.mod`. Update the
minimum deliberately and keep installation/contributor documentation in sync.

The November 2026 maintenance update moves `golang.org/x/crypto` from
v0.48.0 to **v0.58.0** and its indirect `golang.org/x/sys` dependency from
v0.41.0 to **v0.49.0**. Both new versions require Go 1.26.0, so they fit the
existing minimum. Other direct dependencies remain at their current
versions. The deterministic DRBG fixtures must continue to pass after the
update; no construction, seed/reseed formula or API change is intended.

For a dependency update:

```sh
go get golang.org/x/crypto@v0.58.0
go mod tidy
go mod tidy -diff
go mod verify
go test ./...
make test-race
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
```

`tidy -diff` checks consistency without silently changing files.
`go mod verify` checks downloaded module-cache contents against their
recorded hashes; it does not assess source-code security. Commit `go.mod`
and `go.sum` together. Keep deliberate pseudo-versions when an upstream has
no appropriate tagged replacement; an old timestamp alone does not prove
a dependency is broken. Review the module graph and advisories rather than
running an unbounded `go get -u ./...`. Unused transitive-module advisories
can remain even when govulncheck reports no affected reachable calls.

## Coverage you can reproduce

```sh
make coverage
go tool cover -func=coverage/coverage.out
# Open coverage/coverage.html in a browser for annotated source.
make test-race
go test -tags=purego ./cmd/entropy-server/internal/drbg ./cmd/entropy-server/internal/rdrand
```

`make coverage` runs `go test -coverpkg=./... -covermode=atomic` so tests in
command packages also count when they exercise internal helpers. The
per-test-binary percentages printed by that command share the broad
instrumented denominator; use the combined profile/summary for the overall
result. Do not average those printed percentages. An ordinary
`go test -cover ./...` report instead measures each package using its own
test binary and can miss indirect coverage from another package's tests.

The baseline on `main` commit `4c94a08`, measured on 2026-11-08 with
Go 1.26.10, Linux amd64 and `GOMAXPROCS=4`, was:

| Area | Combined statement coverage |
| --- | ---: |
| Whole Go module | 69.0% |
| Server command | 70.7% |
| Client command | 61.8% |
| Server configuration | 91.8% |
| Client configuration | 32.5% |
| Server DRBG | 95.4% |
| TLS configuration / tuned listener | 0% / 0% |

With the new regression tests, the same combined measurement is **72.0%**.
TLS configuration is 100% exercised, the listener is 83.3%, and the random
image / heatmap / QR handlers are 100% / 100% / 83.3%. The remaining QR
encoder-error path and low-level socket-control failures are not forced
solely to increase that percentage.

These are dated measurements, not maintained targets. Timing/error paths
can cause small differences between runs. Statement coverage is not branch
coverage, does not measure JavaScript, and cannot prove cryptographic
correctness or the absence of races outside the exercised cases. The
pure-Go tests check portable fallback code on the host architecture; they
do not replace execution on ARM hardware.

The quality update addresses these concrete gaps:

- TLS certificate loading returns wrapped errors to the caller. HTTPS
  configuration is validated before opening an entropy device or reading
  seed material. Tests cover missing/malformed/mismatched certificate/key
  pairs, a verified TLS 1.3 connection and rejection of TLS 1.2. TLS policy
  remains TLS 1.3, without client-certificate authentication or HTTP/2 ALPN.
- Tuned-listener tests transfer data over loopback with default/explicit
  send-buffer settings and check occupied/invalid bind-address errors.
- Image, heatmap and QR tests decode successful PNGs, check dimensions and
  opacity, and verify admission release after success and generator failure.
- Both build and release validation now race-test **all Go packages**, run
  explicit vet and pure-Go fallback checks, and generate coverage reports.

Successful GitHub build and release-validation jobs include a total in
their job summary plus a **`go-coverage` artifact**, retained for 14 days,
with the profile, per-function summary and annotated HTML. The reports need
no third-party coverage account or token. No arbitrary minimum percentage
is imposed: review which meaningful paths a change exercises.

## Remaining test and maintenance priorities

| Priority | Follow-up | Why |
| --- | --- | --- |
| High | Give client argument parsing an isolated `flag.FlagSet`; test defaults, help, malformed/positional arguments and repeated parsing. | Most client config-validation logic is covered, but the CLI parser itself is not directly exercised. |
| High | Add subprocess startup tests for the actual server/client executables, including occupied ports and HTTP-only/HTTPS modes. | Handler tests and helper tests do not fully execute `main` or every startup/exit path. |
| Medium | Add native Go fuzz targets for bounded query/frame/upload parsing, retaining discovered regression cases. | Existing boundary cases do not explore arbitrary malformed inputs. |
| Medium | Exercise real browser disconnect/reconnect and mobile layouts with browser automation. | Node regression scripts cover calculations/scheduling/layout contracts, not every interactive network/UI path. |
| Deployment | Test real QRNG startup/failure, blocking driver reads and supported non-amd64 systems. | OS-backed local tests and cross-compilation do not validate physical hardware or driver behavior. |
| Maintenance | Remove unused prototypes/helpers and stale comments in small reviewed changes; prefer initialisms in new code. | Tests of unused code can inflate coverage while leaving shipped behavior untouched. |

Global counters/state mean many existing tests must remain serial unless
they first gain isolated dependencies. Do not add `t.Parallel` mechanically.
Use temporary files, controlled local servers and bounded waits; ordinary
tests should not require a physical QRNG or external services.

## What the README badges mean

| Badge | Source and scope |
| --- | --- |
| Build | Build workflow status for `main`; follow the link for the exact commit and checks. Markdown-only changes do not trigger it. |
| Go version | Minimum Go version declared by the default branch's root `go.mod`. |
| CodeQL | GitHub's default CodeQL workflow on `main`; a successful analysis is not a certificate that the project has no vulnerabilities. |
| Release workflow | Latest release-workflow result, covering validation and publishing; it can refer to an older tag than current `main`. |
| Last commit | Most recent commit on `main`, not the date of the latest published binary. |
| Latest release / downloads | GitHub release metadata and asset-download counts, excluding Git clones and other distribution channels. |
| License / golangci-lint | License metadata and the fact that lint is configured; build details show the actual lint result. |

Badges may be cached by GitHub/Shields. Open the linked workflow, module or
release for the underlying result; no badge asserts general Go compliance
or physical-entropy quality.

[Contributing](../CONTRIBUTING.md) · [Technical reference](README.md)
