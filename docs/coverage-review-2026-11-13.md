# Coverage review: current main, 2026-11-13

Baseline: [`dcc01a87aa2d517371f5262dfa95d2eb6e9f9f8d`](https://github.com/fantuz/entropy-service/commit/dcc01a87aa2d517371f5262dfa95d2eb6e9f9f8d),
after PR #76. Evidence comes from the successful
[main build #43300419200](https://github.com/fantuz/entropy-service/actions/runs/43300419200),
its `go-coverage` artifact, and source/call-site inspection. The workflow uses
Go 1.26.11 on Linux amd64 (`ubuntu-24.04`) and runs `make coverage` with
`-coverpkg=./... -covermode=atomic`. This is a dated snapshot, not a target or
a new full security audit. The artifact is retained for 14 days; the
[package counts](coverage/2026-11-13-main.csv) preserve the baseline after expiry.

## Measured baseline

| Area | Covered / instrumented statements | Coverage |
| --- | ---: | ---: |
| Whole module | 4,360 / 5,267 | 82.8% |
| Server command | 1,465 / 1,877 | 78.1% |
| Client command | 555 / 615 | 90.2% |
| Server DRBG | 236 / 249 | 94.8% |
| LAB | 375 / 447 | 83.9% |
| Native USB transport, Linux | 169 / 231 | 73.2% |
| Quantis USB protocol | 72 / 88 | 81.8% |
| ChaosKey protocol | 48 / 58 | 82.8% |
| Hardware inventory/status, Linux | 111 / 145 | 76.6% |
| Client raw output | 64 / 74 | 86.5% |
| Client diagnostics | 357 / 395 | 90.4% |
| Shared WebSocket dial/cancellation | 25 / 30 | 83.3% |

Counts sum each distinct profile block's statement weight and consider the
block covered when any recorded execution count is positive. Package totals
are weighted statement counts, not an average of function/test percentages.
No JavaScript, Python, C/kernel or Windows-only Go statements enter this Linux
profile. Build-tagged experimental code has separate CI tests. Subprocess CLI,
PTY, browser, package and kernel-VM checks do not contribute unless instrumented
into this Go profile. A zero here can therefore mean separately tested code,
an inactive wrapper, or a real gap; those require different responses.

## Gaps ranked by practical value

| Follow-up | Exact baseline evidence | Existing evidence and remaining limit |
| --- | --- | --- |
| Physical USB/PCI qualification | `usbtransport.Open` 21.9%, `Control` 18.2%; `openHardwareUSB` 0%; Linux status/discovery has unexecuted OS branches | Native fixtures cover submit/cancel/drain mechanics, and CLI checks cover absent devices. They do not exercise a real descriptor/driver exchange. Use the [physical record template](hardware-qualification.md); do not add guessed-device tests to inflate coverage. |
| Repeated mixed-load failure/recovery and cleanup | Individual generation, budget, seed and shutdown tests exist; statement coverage does not establish endurance | Add an opt-in repeatable workload with source error/recovery, slow consumers, churn, cancellation and resource trends. That is a separate test effort. |
| Capture/report failures | `writeCaptureReport` 20%; `runCapture` 82.6% | This maintenance change adds command-level checks that a failed report write returns failure while preserving a completed sample, and a human-readable device failure returns failure without creating a sample. Successful binary content and partial/cancelled capture tests already exist. |
| LAB response and decoder failures | `OpenAuthenticated` 84.3%; `labStreamHandler` 76.9%; camera/ONVIF files contain unexecuted parser/process paths | Handshake cancellation is fixed and tested. Remaining useful work includes malformed media/upstream failures and real Firefox/Windows audio hardware; synthetic audio tests are not a physical input test. |
| Client output/OS failures | `output.WriteContext` 87.0%; `duplicateStdout` 73.9%; `report.close` 88.9% | Pipe cancellation, broken pipes, private files and native Windows reports have separate checks. Inspect unexecuted close/deadline/OS failures before adding tests; avoid tests of formatting-only wrappers. |
| Startup main and platform execution | `serverInvocation`, command `main` and some native-open wrappers are 0% | Actual executable startup failures, TLS, package/systemd and native/packaged Windows smoke tests are outside this profile. Their existence does not cover every branch; their absence from the percentage is not evidence that commands were never run. |

The kernel deadline fixture's `main_linux.go` contributes **39 uncovered
statements** to the broad module denominator. The separate Quantis-driver
workflow executes that program in QEMU against production file operations;
ordinary Go unit tests do not. That simulation remains distinct from a
physical card. The `terminal` package's Linux 100% likewise says nothing about
Windows console statements, which run in native Windows CI separately.

## Maintenance without manufactured coverage

Three unused client files are removed after repository-wide caller searches:
`internal/pipe/pipe.go` (`WriteStdout`, two statements),
`internal/metrics/bandwidth.go` (`CountWrite`, one statement), and
`internal/protocol/entropy.go` (an unused data structure, no statements).
Active raw exports use `internal/output`; their behavior is unchanged.
Removing those three uncovered statements lowers the denominator; it is not
new test coverage. The baseline CSV intentionally retains the original counts.

Other unused statistics/formatting helpers remain candidates for a separate
caller/API review. This refresh does not force constructor, allocation or
kernel errors simply to turn every function green.

## Reproduce and update

Prefer the full build's current `go-coverage` artifact for the overall result.
For a deliberate local full refresh:

```sh
make coverage
go tool cover -func=coverage/coverage.out
# Open coverage/coverage.html for exact unexecuted blocks.
```

Record the SHA, Go version, OS/architecture, build tags and command with any
replacement snapshot. Run focused tests for changed behavior locally and let
GitHub CI run full coverage, race, lint, fuzz, native/packaged Windows and browser
regressions. The percentage from a narrow `-run` selection is not comparable
to this full-suite baseline. Coverage measures execution, not entropy quality,
cryptographic assurance or a leak-free long-running process.

[Go quality guide](go-quality.md) · [Maintenance change report](maintenance-review-2026-11-13.md)
