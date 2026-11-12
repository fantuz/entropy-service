# Hardware qualification records

This matrix distinguishes **compilation**, **simulated transport/file-operation
tests**, and **tests with a physical device**. Each is independent evidence:
building an adapter or passing an OS I/O fixture does not establish that a real
device, driver and host satisfy the same contract. A passing physical record
applies to the identified combination and tested conditions; it is not entropy
certification or a blanket support claim.

## Current evidence matrix

The software results below are historical checkpoints at the linked source
revisions. Changes to code, firmware, driver or host require new evidence.
**No reproducible physical qualification records have been submitted yet.**

| Source and path | Compilation evidence | Simulated transport / file operations | Physical device records |
| --- | --- | --- | --- |
| Legacy Quantis PCI, Linux bounded-read overlay (`179a:0001`, `/dev/qrandomN`) | [6.1 / 6.8 / 6.12 kernel builds][driver-ci] | [QEMU with production file operations][driver-ci]: epoll, nonblocking/partial reads, signals, faults/removal, Go deadlines/context/close; three load/use/close cycles | Not tested — [records](hardware-validation/physical/) |
| Legacy Quantis PCI, Windows x64 (`quantis-pci:N`) | [Go adapter x64 builds][application-ci]; vendor driver not compiled here | Overlapped named-pipe status/read/cancel fixtures; no Quantis kernel driver exercised | Not tested |
| Referenced Quantis USB (`0aba:0102`), Linux / Windows x64 (`quantis-usb:N`) | [Released Linux targets and Windows x64][usb-ci] | usbfs submit/cancel/reap fixtures on Linux amd64; WinUSB call fixtures and native overlapped named pipes on Windows | Not tested |
| ChaosKey cooked USB (`1d50:60c6`), Linux / Windows x64 (`chaoskey-usb:N`) | [PR #73 Linux and Windows builds][usb-ci] | Cooked protocol/descriptor tests; shared native fixtures with the ChaosKey endpoint profile | Not tested |
| ChaosKey Linux kernel node (`/dev/chaoskeyN`) | Upstream source review; no driver build or physical reader result recorded here | No qualified character-driver simulation; reviewed stock drivers lack the required poll/deadline contract | Not tested; no deadline bypass |
| Quantis PCIe chip / XDMA | Inventory support; no seed adapter qualified by these records | Not tested | Not tested |

[driver-ci]: https://github.com/fantuz/entropy-service/actions/runs/43246271901
[application-ci]: https://github.com/fantuz/entropy-service/actions/runs/43246272360
[usb-ci]: https://github.com/fantuz/entropy-service/actions/runs/43245031403

The overlay checkpoint is `8195ffce66a5e525ba39a2ae3a8d2cf4fae6f9ab`;
the shared USB/ChaosKey checkpoint is
`a2e145c059c0fab7c7d7b5a4032eea18e5e2256a` ([PR #73](https://github.com/fantuz/entropy-service/pull/73)).
A record of a tested PR revision does not imply that every release includes it.
Linux USB cross-build targets are amd64, armv7 and arm64; native Linux I/O fixture
results here are amd64, not physical results on all three architectures.

The older [unmodified Linux-driver build report](hardware-validation/quantis-pci-linux-build.json)
and [Windows package inspection](hardware-validation/quantis-pci-package.json)
remain separate evidence. Package/INF inspection establishes neither driver
installation nor physical operation. Historical maintainer observations in the
[driver guide](hardware-drivers.md), including the vendor application's roughly
4 Mbit/s display, are reported observations rather than a reproducible result for
this application's adapter, kernel overlay or cancellation behaviour.

## Add a physical record

Copy [the JSON template](hardware-validation/physical-record.template.json) to
`docs/hardware-validation/physical/YYYY-MM-DD-device-alias-os.json`, fill the
observed fields, add it to the [physical index](hardware-validation/physical/),
and link it in the relevant matrix row. Preserve previous records; use a new
filename for a changed host, driver, firmware, source revision or test session.
The template is documentation, not a completed result or automatic certification.

Set `record_status` to `partial` or `complete`; `complete` means the intended
session finished, not that every test passed. Each measurement has its own
`result`: `pass`, `fail`, `not_tested`, `not_applicable` or `blocked`. Record
failures as well as successes. An unperformed check stays `not_tested`; unknown
values stay `null` with an explanation. Zero is a measured value, not “unknown”.
Do not count templates, unit-test fixtures, source review or CI virtual devices
as physical records.
Use UTC ISO 8601 session timestamps, four-digit hexadecimal VID/PID strings,
hexadecimal endpoint strings, integer byte/trial counts and millisecond latency
values. Include PCI BDF or USB topology in `bus_location` when available.

Each record captures:

- Device family/model, PCI or USB VID:PID, serial or stable local alias, firmware
  or core revision, interface/endpoint or node, and the requested/actual source.
- OS distribution/version/build, kernel release and architecture; for Windows,
  keep its OS build and explain a non-applicable separate kernel-release field.
- Driver name, version/package, binding and relevant policy. If the version is
  unavailable, record that explicitly rather than copying a vendor ZIP version.
- Application commit/release, binary SHA-256 and any separate kernel-overlay
  revision; include commands and read budgets needed to reproduce the session.
- Direct source read throughput with byte counts, elapsed time, chunk size, repeated
  runs, errors/timeouts and measurement boundary.
- Cancellation observations for deadlines, context cancellation, close,
  disconnect and shutdown: trial count, expected budget, worst observed return
  latency, and whether pending I/O was drained before buffers were released.

A successful sample does not measure cancellation or establish entropy quality.
ChaosKey's cooked protocol has no documented per-read health query; keep that
health result unavailable rather than interpreting readable bytes as “healthy”.

## Collect and interpret measurements

Use the existing commands for identity and a bounded accepted/discarded sample:

```sh
entropy-server hardware list -json
entropy-server hardware probe -device=SOURCE -bytes=1048576 -timeout=10s -json
```

Replace `SOURCE` with the explicit source and use `.\entropy-server.exe` in
PowerShell. Keep the actual source equal to the requested source. Record the
reported byte counts and retain cancellation/quality flags as reported: a normal
probe deliberately does not claim those tests were performed.

For throughput, record either `source_read` (a harness times only direct source
reads) or `whole_probe` (the probe's full elapsed time, including discovery,
open, health queries and cleanup). Record the exact method/command. The latter
is end-to-end probe throughput, not steady-state device throughput. For ChaosKey,
source throughput counts cooked bytes from `0x85`, not raw ADC samples. Repeat runs
and report their individual counts/times; for an aggregate:

```text
MiB/s = total accepted source bytes / total measured seconds / 1,048,576
```

Do not substitute HTTP/WebSocket DRBG output speed, client graphs, encoded bytes,
a nominal product rate or a vendor application's display for measured source
read throughput. Keep Mbit/s, MB/s and MiB/s distinct. Use a monotonic timer for
read/cancellation latency; wall-clock timestamps identify the session.

Test cancellation with I/O actually pending in an isolated qualification setup.
State the trigger, configured budget, observed error and return latency, repeated
attempts, and how completion/resource cleanup was checked. If a case cannot be
induced, mark it `blocked` with the reason; a quick successful read does not prove
cancellation. Windows named pipes and Linux virtual MMIO fixtures stay in the
simulation column. Physical disconnect and service shutdown need their own
observations. A forced process kill is not evidence of cooperative cancellation
or completion draining. Repeat deadline/close cycles to catch stale state.

For ChaosKey, record the output used: **cooked-only** means firmware-processed
output from endpoint `0x85`, excluding raw ADC (`0x86`) and flash (`0x87`) data.
This is a source-selection rule, not proof of entropy quality. Endpoint numbers
are device-specific: the referenced Quantis USB protocol legitimately uses
`0x86` for entropy. Also record Linux kernel ownership or the Windows WinUSB
binding; OS RNG output that may incorporate hardware is a separate measurement.

The separate [diagnostic capture command](chaoskey.md#diagnostic-capture-cooked-by-default-raw-on-demand)
can save cooked output by default or raw `0x86` with `-mode=raw`. Keep their
physical records separate, setting `output_kind` and `endpoint` explicitly and
documenting raw sample resolution/packing. Its read timing measures the accepted
source read, not file writing or server DRBG throughput. The original matrix's
historical CI checkpoints predate raw capture; a cooked record does not qualify
raw operation or concurrent use. Do not publish raw capture contents as evidence;
retain sanitized metadata, counts/timings and hashes instead.

Link sanitized logs and their hashes, commands and relevant driver diagnostics.
Record counts and timings rather than raw entropy or seed bytes. A stable device
alias can replace a public serial number; explain any redactions without adding
private keys, host credentials or unrelated logs. Statistical screening, if
performed, belongs in separate evidence with its own interpretation limits.
