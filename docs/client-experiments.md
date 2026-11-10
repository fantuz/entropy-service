# Timed entropy-client experiments

Build with `make build` and start a server before running these commands.
The examples use loopback HTTP without TLS and an explicitly selected OS
source; substitute your device and deployment URLs when appropriate:

```sh
./bin/entropy-server -device=/dev/urandom -require-device=true \
  -drbg=chacha20 -enable-https=false -http=127.0.0.1:8080
```

## Shannon entropy and its assessment

For one analysed sample of `n` bytes, let `c[b]` be the count of byte value
`b`, and `p[b] = c[b] / n`. The unchanged empirical estimator is:

```text
H = -sum(p[b] * log2(p[b])) for b = 0..255 with c[b] > 0
```

`H` is measured in **bits per byte**, between 0 and 8. It is neither a rate
per second nor a weighted combination of the other tests. The client now
labels its single threshold result **Shannon assessment**:

| Condition | Assessment |
| --- | --- |
| No analysed bytes | `NO_SAMPLE` (in reports) |
| `H >= -shannon-threshold` | `OK` |
| `H < -shannon-threshold` | `WARN` |

The default threshold is `7.999`, preserving the former monitor badge's
minimum. Set any finite value in `[0, 8]`; equality is `OK`. The former
separate aggregate diagnostics badge, with its different hard-coded
Shannon cutoff, is removed. Chi-square and its approximate right-tail
p-value, the monobit p-value, and lag-1 serial correlation and its approximate
p-value remain visible as independent reference measurements. None changes
the Shannon estimate or its assessment. Optional graphs show that same
estimate over time.

Small samples have a downward-biased empirical estimate even for uniformly
distributed independent bytes. For sufficiently large `n`, the approximate
expected value for that model is `8 - 255 / (2*n*ln(2))`: about `7.99719` at
64 KiB and `7.99982` at 1 MiB. Thus the default can warn on 64 KiB without
identifying a source defect. With fewer than 256 bytes, `H <= log2(n) < 8`.
Setting the threshold to 8 demands exactly balanced byte counts; setting it
to 0 accepts any nonempty sample. Select and report a threshold appropriate
to your sample size and experiment.

A repeated ordered sequence `0, 1, ..., 255` gives `H=8` and `OK` even though
the sequence is predictable. Its serial statistic remains available to
expose that ordering. This assessment describes the sample's byte-frequency
distribution; it does not establish independence, unpredictability, physical
entropy, or security of a source or DRBG.

## A ten-minute run or a longer experiment

```sh
./bin/entropy-client -mode=stream \
  -wsurl=ws://127.0.0.1:8080/stream -slice=1048576 -refresh=50ms \
  -analysis-bytes=1048576 -analysis-interval=250ms \
  -shannon-threshold=7.99 -headless -duration=10m -report=stream-10m.jsonl
```

Use `-duration=3h -report=stream-3h.jsonl` for three hours. Use a fresh file
name for every run: the client refuses to overwrite an existing file.
`-duration=0` retains the indefinite mode; Ctrl+C or SIGTERM stops it cleanly.
Both duration expiry and interruption exit successfully. Invalid arguments,
startup/transport failures and report I/O errors exit unsuccessfully.
`WARN` alone does not change the exit status.

`-duration` measures wall time from immediately before opening the monitored
connection. It includes network setup, pacing, analysis, device writes if
enabled, and JSONL I/O. Interactive runs first take one preflight sample and
display it for three seconds; that work is excluded from measurement and
counters. `-headless` skips it and suppresses terminal output, previews,
graphs and bells. Optional matrix computation still follows `-matrix`.
Pending connections/reads are cancelled at the deadline. The current analysis
or write and cleanup can finish a little later; this is not a hard real-time
deadline.

For JSONL on stdout, use **both** options:

```sh
./bin/entropy-client -headless -duration=10s -report=- > run.jsonl
```

Errors go to stderr. Shell redirection controls file permissions and can
overwrite an existing file; use `-report=run.jsonl` for the client's exclusive
creation and 0600 permissions instead. Without `-report`, headless mode is a
quiet load generator and does not save results.

## Compare transport, chunk size and analysis cost

Change one setting at a time. Keep the same server backend/source, host,
refresh interval, sample cap and threshold, and repeat runs rather than
treating a single short run as a performance guarantee:

```sh
for size in 65536 1048576 16777216; do
  for mode in pull stream; do
    ./bin/entropy-client -mode="$mode" -slice="$size" -refresh=50ms \
      -analysis-bytes=1048576 -analysis-interval=250ms \
      -shannon-threshold=7.99 -headless -duration=30s \
      -report="${mode}-${size}-bounded.jsonl"
  done
done
```

The 16 MiB run still analyses only the first 1 MiB of each selected frame.
For full analysis, repeat with `-analysis-bytes=0 -analysis-interval=0` and
different report names. Add `-matrix` separately to compare its cost.
`analysis_ms` measures diagnostics only; overall throughput also includes
report I/O and backpressure. JSONL adds one line per analysed sample, so
every-frame analysis can increase report size and reduce throughput. Disk
usage grows with duration; the client streams records rather than keeping
the run history in memory.

Threshold comparisons need not fetch different random samples: the JSONL
retains each raw `shannon_bits_per_byte` value, so apply several candidate
thresholds offline to **the same samples**. Do not infer entropy production
from received throughput or sum sample entropy estimates into a stream-wide
security result.

## JSONL schema, version 1

Every line is one JSON object with `schema_version: 1`, `type`, and a UTC
RFC 3339 `timestamp`. Fields are snake_case. Readers should ignore additional
fields for forward compatibility and require a final `run_summary` before
treating a run as complete.

| Record `type` | Contents |
| --- | --- |
| `run_start` | `settings` and client `go_version`, `goos`, `goarch`. Settings include mode, endpoint, requested payload, refresh, analysis cap/interval, display interval, threshold, duration, headless/matrix/writeout flags. Settings durations are Go duration strings; the threshold is `shannon_threshold_bits_per_byte`. |
| `measurement_start` | Boundary after interactive preflight, before monitored network setup. |
| `sample` | One record per analysed frame/prefix, with raw statistics, its threshold assessment, timings and cumulative counters. Unselected frames produce no sample record. |
| `run_summary` | Final counters, timing, throughput, completion reason and last sample's assessment. This is not a cumulative statistical test. |

The additive display settings `waterfall`, `waterfall_mode`,
`waterfall_columns`, `waterfall_rows` and `color` record the requested flags,
including in headless runs. They do not indicate that a display was
allocated or that a particular terminal capability was detected. Raw
waterfall bytes are never included in reports.

`settings.endpoint` retains scheme, host and path but omits URL user
information, **all query parameters** and fragments. Effective payload and
refresh are recorded separately. Raw entropy, hex previews, histograms and
transition matrices are not recorded. Custom query settings and the server's
source/backend are not automatically discovered; record them separately
along with the exact client/server versions when comparing experiments.
Paths can still contain deployment-specific information, so review reports
before sharing.

Sample fields:

| Fields | Meaning |
| --- | --- |
| `frame_bytes`, `sample_bytes` | Full selected frame and actually analysed prefix sizes. |
| `shannon_bits_per_byte`, `shannon_assessment` | Unchanged raw estimate and `OK`/`WARN` from the configured threshold. |
| `chi_square`, `chi_square_p`, `monobit_p`, `serial_correlation`, `serial_p` | Separate raw reference statistics. |
| `elapsed_ms` | Wall time since measurement began, at sample reporting. |
| `receive_wait_ms` | Complete message read or HTTP fetch duration, including server waiting; not a pure RTT. |
| `analysis_ms` | Diagnostics runtime for this sample. |

Every sample and summary includes cumulative `received_frames`,
`received_bytes`, `analysed_samples`, `analysed_bytes`, `shannon_ok_samples`,
`shannon_warn_samples`, and `device_write_errors`. Reception counts all
frames handled by the monitor, including skipped and empty frames. Analysis
counts nonempty selected prefixes only. Bytes still queued or in flight
when the run ends are released and excluded. Thus the received count is
not a wire-level byte counter, and analysed counts may be much lower.

With `-writeout`, `device_written_bytes` counts bytes accepted by Linux's
random device (including partial frames) and `device_written_frames` counts
complete writes. Device errors now fail the run; normal cancellation can
leave a partially written frame. See [write-out semantics and limited value](client-writeout.md).

Summary fields also include:

| Field | Meaning |
| --- | --- |
| `termination` | `duration`, `interrupted`, `startup_error`, `transport_error`, `writeout_error`, or `report_error`. |
| `failed` | Whether the run encountered a fatal startup, transport or report error before summary writing. |
| `measurement_began` | False when preflight failed or was interrupted. |
| `elapsed_ms` | Measurement wall time through connection cleanup; zero if measurement did not begin. |
| `total_elapsed_ms` | Run wall time, including preflight, up to summary writing. |
| `received_mib_per_second` | `received_bytes / 1048576 / measurement_seconds`; zero when measurement did not begin. |
| `last_shannon_assessment` | Last analysed sample's result, or `NO_SAMPLE`; use the OK/WARN counts for the run distribution. |

The file is synchronised and closed on normal completion. Report I/O errors
remain command errors even if cancellation happens at the same time. A
broken writer, full disk, failed sync/close, SIGKILL or machine failure may
leave an incomplete report; a sync/close failure can occur after a summary
was encoded. Error details are on stderr rather than copied into JSONL,
since network errors may contain full URLs.

This Python example validates completion, compares candidate thresholds on
the same data, and prints the final accounting without loading the whole
report into memory:

```sh
python3 - stream-10m.jsonl <<'PY'
import json
import sys

thresholds = (7.9, 7.99, 7.999)
ok = dict.fromkeys(thresholds, 0)
samples = 0
last = None
with open(sys.argv[1], encoding="utf-8") as report:
    for line in report:
        last = json.loads(line)
        if last["schema_version"] != 1:
            raise SystemExit("unsupported report schema")
        if last["type"] == "sample":
            samples += 1
            for threshold in thresholds:
                ok[threshold] += last["shannon_bits_per_byte"] >= threshold
if last is None or last["type"] != "run_summary":
    raise SystemExit("incomplete report: no final run_summary")
if last["analysed_samples"] != samples:
    raise SystemExit("incomplete report: sample count differs")
print("samples:", samples, "OK by threshold:", ok)
print(json.dumps(last, indent=2))
if last["failed"]:
    raise SystemExit("run failed; inspect termination and client stderr")
PY
```

## Local verification experiment, 2026-11-08

Short integration runs on Linux amd64, an Intel Xeon Platinum 8573C VM
(five visible vCPUs, `GOMAXPROCS=4`), Go 1.26.10, used one client at a time
against a temporary loopback server. The server explicitly required
`/dev/urandom`, used ChaCha20 and disabled HTTPS. These are paced behavior
checks, **not maximum-throughput benchmarks** or evidence about a physical
QRNG. No before/after performance improvement is claimed.

The every-frame cases used `-refresh=20ms -analysis-bytes=0
-analysis-interval=0 -duration=3s -headless`, with the default threshold.

| Transport / payload | Samples | Raw H range (bits/byte) | OK / WARN at 7.999 | Received MiB/s | Mean analysis ms |
| --- | ---: | --- | --- | ---: | ---: |
| HTTP / 64 KiB | 149 | 7.99647–7.99786 | 0 / 149 | 3.103 | 0.211 |
| WS / 64 KiB | 149 | 7.99656–7.99782 | 0 / 149 | 3.103 | 0.192 |
| HTTP / 1 MiB | 149 | 7.99978–7.99987 | 149 / 0 | 49.661 | 3.390 |
| WS / 1 MiB | 149 | 7.99978–7.99987 | 149 / 0 | 49.634 | 2.965 |

Reapplying 7.99 offline accepted all 149 samples in each run; the raw values
were unchanged. No sample reached exactly 8. Measured durations were
3.000–3.002 seconds, and the JSONL counts matched the selected data.

A five-second WS run with `-slice=16777216 -refresh=50ms` and default
analysis limits handled 98 frames: **1,568 MiB received**, versus 18 samples
of 1 MiB: **18 MiB analysed**. Its report recorded about 313.5 MiB/s received,
and all 18 sampled prefixes met 7.999. The requested payload rate was
320 MiB/s; this run exercised bounded analysis rather than measuring the
server's capacity.

Additional deterministic HTTP fixtures verified `H=8` for ordered byte
cycles despite strong serial correlation, `H=0` for constant bytes with
`WARN` at threshold 1 and `OK` at threshold 0, and `NO_SAMPLE` for empty
responses. An actual SIGINT produced a successful `interrupted` summary;
an HTTP failure produced a nonzero exit and `transport_error` summary.
All reports were parsed as JSONL and checked against sample/count totals.

## Automated regression coverage

`go test ./cmd/entropy-client/...` covers threshold bounds (including NaN and
infinity), equality, empty input, predictable high-Shannon data, a single
terminal assessment, both transports, timed cancellation of pending HTTP and
WS handshakes, indefinite-mode interruption, JSONL order and accounting,
startup/transport failures, URL redaction, stdout purity, overwrite
protection and report failure concurrent with cancellation. Display tests
cover copied bounded waterfall history, actual WebSocket buffer recycling,
analysis-prefix and sampling limits, RGB triplets and incomplete cells,
hue/grayscale mappings and palette fallbacks, Linux PTY detection and
resizing, `NO_COLOR`/redirected output, 80×24 and 60×24 geometry, and
headless JSONL with forced color and display flags enabled. Run
`go test -race ./cmd/entropy-client/...` for lifecycle/concurrency checks.

For the built CLI, run `make build-client && python3 scripts/test_client_terminal.py`
on Linux. This uses a local deterministic HTTP fixture to check all five
color choices on redirected stdout, headless JSONL, and automatic truecolor
in 80×24 and 60×24 PTYs. It requires only Python's standard library. Add
`--capture=/tmp/client-waterfall.ansi` to capture the actual 80×32 display
used for the preview; it contains synthetic fixture bytes only.

[Client configuration](configuration.md#client) · [Technical reference](README.md)
