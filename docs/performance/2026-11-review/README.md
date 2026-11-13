# Performance evidence: 2026-11 review

These measurements describe the **unfixed** reviewed main `4561cf2` and the revisions listed in [the report](../../linux-performance-2026-11-13.md). `current` is a label, not a release. Full hashes and environment are in [environment.json](environment.json); client-specific order/toolchain metadata are in [client-method.json](client-method.json). Timestamps in JSON metadata are UTC.

| Files | Meaning |
| --- | --- |
| `server-micro-{samples,summary}.csv` | 128 samples; four revisions × eight cases × four rotated rounds, 250 ms each. `ns/op`, `B/op`, `allocs/op` are Go benchmark units; summary columns retain median/min/max. |
| `traffic-{samples,summary}.csv` | 60 loopback samples, 500 ms warm-up + 1.5 s measurement. HTTP uses two workers; WebSocket uses one. MiB/s and CPU seconds/GiB are separate measures. All complete-payload checks passed. |
| `http64k-warm-{samples,summary}.csv` | 12 longer small-request samples, one-second warm-up + three-second measurement. `current-control` executes the same current binary and is not another revision. |
| `client-{samples,summary}.csv` | 120 samples, five rotated rounds, 300 ms each. `output_bytes_op` means encoded bytes handed to the writer; `heap_bytes_op` means transient allocated Go heap bytes. These are not interchangeable or terminal frame rates. |
| `client-fixture.go.txt` | Identical deterministic benchmark fixture used in all four client worktrees. Copy into `cmd/entropy-client/review_compare_bench_test.go` to run, then remove it. |
| `chromium-summary.json`, `firefox-summary.json` | Navigation, steady rendering, cached switches and browser metadata. RAF durations are callback wall time, not OS process CPU. Submitted/job/render counts preserve coalescing. |
| `chromium-wide-control-summary.json` | Three paired English-only rounds per large-canvas configuration, 40 submissions each. Both revisions already include the wide layout/optional diagnostics. |
| `catalog-network-samples.csv` | Compact per-fetch observations from the larger browser traces. Empty validator/encoding fields mean the header was absent. `wire_status`/`from_disk_cache` come from Chromium CDP; Firefox leaves these blank and retains ResourceTiming/header evidence. ResourceTiming transfer bytes include its estimated header contribution. |
| `binary-sizes.json` | Matching stripped, trimpath source-build file sizes, not downloaded release assets or resident memory. |
| `source-comparison.json`, `i18n-small-request-path.json` | Source identity checks used for attribution; they do not guarantee identical runtime performance. |
| `locale-footprint.json` | Raw catalog sizes plus an offline gzip-size calculation. `gzip_bytes` is an opportunity estimate, not an HTTP measurement or the corrective PR's served byte count. |

The browser summary field `rafCpuMsPerFrame` retains the harness name but measures summed RAF callback wall-clock duration divided by actual rendered frames. It is not whole-browser OS CPU. CDP renderer-task fields cover warm-up plus timed submissions and are separately normalized by all submissions.

All heavy measurement phases ran sequentially on the same shared Linux VM. No private keys, generated certificates, binaries or large browser traces are committed. Source fixtures and metadata are included where compact; the original detailed traces remain investigation artifacts.

To reproduce common existing server cases, use the same toolchain/environment in each source checkout, pin the process to one CPU, and rotate revision order rather than running all baseline samples first:

```sh
CGO_ENABLED=0 GOAMD64=v1 GOMAXPROCS=1 GOGC=100 go test -run '^$' -bench '^BenchmarkDRBGLifecycle$/(chacha20|aes-256-ctr)/(Read64KiB|ChildCreateClose)$' -benchmem -benchtime=250ms -cpu=1 ./cmd/entropy-server/internal/drbg
CGO_ENABLED=0 GOAMD64=v1 GOMAXPROCS=1 GOGC=100 go test -run '^$' -bench '^BenchmarkHTTPGeneration$/(ChaCha20|AES-256-CTR)/random/(65536|2097152)$' -benchmem -benchtime=250ms -cpu=1 ./cmd/entropy-server
```

The exact client fixture runs with `-run '^$' -bench '^BenchmarkReview' -benchmem -benchtime=300ms -cpu=1`. Build/setup work and row-cache priming precede the timed loop. Report ranges and errors as well as medians; see the report for the unresolved small-HTTP result and scope limits.

## Corrective static-asset validation

- `static-assets-chromium.json` and `static-assets-firefox.json`: actual historical/fixed navigation, encoded/identity representations, validators, entropy-route exclusion and frozen-sample invariance.
- `static-startup-samples.csv` / `static-startup-summary.json`: six alternating before/fixed HTTP-only startup/RSS observations.
- `static-asset-benchmarks.csv`: three constructor and conditional-304 measurements, including retained body bytes.
- `static-fix-provenance.json`: matching build flags, executable/source hashes and catalog version.
