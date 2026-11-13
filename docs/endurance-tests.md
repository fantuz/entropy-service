# Repeatable endurance and fault tests

This opt-in suite runs production generation handlers, shared budgets, managed
shutdown, seed input and the reseed worker against local fixtures. Ordinary
`go test ./...` skips it unless `ENTROPY_ENDURANCE_DURATION` is set. It adds no
production flags, fault endpoints, remote traffic, device reads or dependencies.
Its seed material is fixed and public.

## Run locally

Use the Go version in `go.mod` and Python 3.11 or newer. From the repository root:

```sh
# Short focused check; race instrumentation needs a supported C toolchain.
python3 scripts/run_endurance.py --duration=5s --workers=2 --race --artifacts=endurance-artifacts-short

# Longer run without instrumentation overhead.
python3 scripts/run_endurance.py --duration=10m --workers=4 --artifacts=endurance-artifacts-10m

# Equivalent short Make target, without race instrumentation.
make endurance ENDURANCE_DURATION=10s ENDURANCE_WORKERS=4 ENDURANCE_DIR=endurance-artifacts-make
```

In PowerShell use `python` and omit `--race` unless the Windows Go race toolchain
is configured. Make is optional. Windows CI runs natively without race detection.

`--duration` accepts a number with `s`, `m` or `h`, from 1 second to 24 hours.
It applies to **each of three scenarios**: mixed traffic under ChaCha20, mixed
traffic under AES-256-CTR, and source-read cycles. A `10m` run needs about
**30 minutes**, plus compilation/cleanup. Every scenario completes at least two
cycles; very short runs can exceed the target. `--workers` accepts 1..16 concurrent
mixed HTTP/WS workers per backend; source ownership remains serial.

Every run requires a **new artifact directory** and never replaces old reports.
Root directories beginning with `endurance-artifacts` are ignored by Git; manage
custom locations yourself. The runner compiles just the server test binary and
runs it directly with a timeout of three scenario durations plus two minutes and
a short outer watchdog. It starts no installed services. Interrupted/timed-out
test binaries are terminated and the run fails.

For direct debugging, set `ENTROPY_ENDURANCE_DURATION=5s` and
`ENTROPY_ENDURANCE_WORKERS=2`, then run:

```sh
go test -race -count=1 -timeout=2m ./cmd/entropy-server -run '^TestEndurance' -v
```

Increase `-timeout` for longer runs. Coverage from a narrow selection is not
comparable to the whole-module report.

## Scenarios and assertions

| Scenario | Work and fault | Required behavior |
| --- | --- | --- |
| Mixed traffic, both production backends | Concurrent 64 KiB HTTP responses and binary WS frames over HTTP and verified fixture TLS; three-frame connections create churn | Complete responses/frames and no unexpected transport errors |
| Failure/recovery during traffic | Inject failed-seed events while consumers run; observe an active WS and new HTTP/WS probes; then reseed successfully | Ready 503, live 200, new generation/upgrade refusal with retry hint, active WS close 1013, then ready 200 and healthy traffic |
| Slow consumers/admission pressure | Hold the actual WS limit with unread bodies and attempt one more upgrade | Extra connection gets 503; shutdown joins handlers and returns reservations/child instances to baseline |
| Deterministic blocked writer | Production binary WS handler sends 1 MiB through unbuffered `net.Pipe`; client reads its header only | Shutdown interrupts the in-flight write and releases its reservation, independently of OS TCP buffer sizing |
| Repeated source cycles | Linux OS pipe / Windows loopback TCP: partial read times out, next full read uses the same handle, cancellation interrupts a pending reseed worker | Partial seed cleared/rejected; failed generation gate; successful reseed restores it; cancelled worker joins without a spurious failure; deadlines reset for the next cycle |

Mixed traffic injects root failure/recovery directly to control timing. Source
cycles separately drive real `seedInput` reads and a blocked `reseedLoop`; they
do not simulate an actual USB/PCI driver or establish device-disconnect/reopen
behavior. Existing unit tests cover stale-age expiry and individual routes;
this harness adds repetition and interaction.

Cancellation, drain and state transitions have a three-second assertion budget.
Post-GC retained-heap/goroutine checks allow **64 MiB and 16 goroutines** above
the initial baseline for runtime/TLS/pool/scheduler variation. Zero reservations
and zero extra DRBG instances remain mandatory. These tolerances are test
allowances, not production limits or proof that smaller leaks cannot exist.

## Saved evidence

- `report.json`: schema version, source SHA/dirty state, Go/OS/architecture/CGO,
  commands, race setting, test-binary hash, workload, exit/stop reason and results.
- `build.log` and `endurance.log`: compilation and test output, including failures;
  local fixture diagnostics contain no generated samples.
- Compiled test binary for local reproduction; CI uploads only JSON/logs.

Results record cycles, HTTP bytes/responses, WS frames/connections, expected
503/1013/truncated responses, unexpected errors, worst successful HTTP response
time, cancellation/shutdown maxima and final budgets/children. Resource samples
contain Go heap and goroutines; Linux also reads RSS from `/proc/self/statm`.
Unavailable RSS is `null`, including Windows. RSS is observed without a gate:
Go can retain resident arenas after objects are freed. Heap is not RSS.

At most 256 resource samples and eight error details are retained per scenario;
logs grow with the finite duration. Sampling and race instrumentation affect
performance: these are not throughput benchmarks or p99 estimates. The runner
requires exit code zero and all three distinct nonfailed records with progress.
Missing/skipped/duplicate records, failed assertions, build errors and watchdog
interruptions fail. Inspect environment metadata before publishing artifacts.
A short CI pass does not establish 24-hour stability or physical entropy quality.

## GitHub CI and longer runs

The `Endurance and fault tests` workflow runs **5 seconds per scenario** on
Linux with race detection and native Windows without it for relevant code
changes. JSON/log artifacts, including failures, are retained for 14 days.
Normal full regression, lint, browser and packaged Windows workflows remain.

Build/release CI runs the generation-query fuzz target for **500,000 executions**
with `GENERATION_FUZZTIME=500000x`. This avoids a Go 1.26 coordinator race that
can report its own duration expiry as `context deadline exceeded` before child
cancellation propagates. Input assertions and the other target budgets remain.
Locally that target follows `FUZZTIME` unless `GENERATION_FUZZTIME` is set;
the endurance runner has its own duration and watchdog controls.

After merge to the default branch, use GitHub Actions → **Endurance and fault
tests** → **Run workflow** for 1m/10m/30m/1h per scenario and 2/4/8 workers.
The maximum uses roughly three hours of each runner's 240-minute budget.
Manual runs have independent concurrency groups so later pushes do not cancel
an ongoing longer experiment. Automatic PR runs supersede older runs of that PR.
These runs use fixtures. Record physical throughput/disconnect/cancellation
separately in [hardware qualification records](hardware-qualification.md).

Compare resource trends and post-GC drift on the same host/configuration;
retain failed evidence. Extend duration/load deliberately after a short pass.
Diagnose failures instead of broadening tolerances without measurements.
