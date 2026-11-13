# Documentation and maintenance change report, 2026-11-13

Baseline: `main` [`dcc01a8`](https://github.com/fantuz/entropy-service/commit/dcc01a87aa2d517371f5262dfa95d2eb6e9f9f8d).
This report records substantive removals, corrections and additions in this
refresh. The old text remains in Git history. These edits reconcile the guides
with implemented controls; they do not add cryptographic assurance, certify
hardware or change generation/network protocols.

## Claims removed or rephrased

| ID | Location / earlier text | Change and reason | Implementation evidence |
| --- | --- | --- | --- |
| D01 | [Architecture review](architecture-review.md): “no fail-closed generation policy” and existing-state output presented as the default | Replaced with current default `stop`, explicit `continue`, route/child enforcement and in-flight-buffer limits. The November 4 text is linked as an archive. | `internal/drbg/freshness.go`, `generation_policy.go`, policy/HTTP/WS tests; PR #53 |
| D02 | Architecture review: source reads lack deadlines, explicit close and joined shutdown | Replaced with complete-read budgets and supported-source cancellation; retained broken-driver/native-call limits and physical qualification requirement. | `seed_input.go`, `entropy_source_*`, reseed/lifecycle tests; PR #56 and #74 |
| D03 | Architecture review: packaged service is root/all-interface and release workflow lacks test gates | Replaced with unprivileged loopback packaging and current release checks. Standalone all-interface defaults and external authentication limits remain explicit. | `resources/systemd/entropy-server.service`, `release.yml`, package/systemd CI |
| D04 | Architecture review: strict empty paths accepted and fallback metadata may claim hardware | Removed as pending bugs; current configuration rejects strict empty paths and reports actual readers. USB/Windows hardware selectors are strict. | `entropy_source.go`, platform selection, `entropy_source_test.go` |
| D05 | Architecture/security review: shared private key still distributed or only a warning protects it | Rephrased to no bundled identity, retired-key rejection, unique provisioning and CI credential guards. Retained the need to rotate historical deployments. | `internal/transport/tls.go`, certificate command, credential-file checker; PR #52 |
| D06 | Architecture/security review: unminimized owned seed buffers and dormant `DRBG.Stream` / `Write` / `ReadInto` APIs remain | Updated for existing buffer clearing and API removal. Removed the obsolete T13 scenario and generic auxiliary-helper warning. Retained Go heap/runtime/cipher-copy limitations. | Current `internal/drbg` declarations, constructors/reseed/close and lifecycle tests |
| D07 | [Security boundaries](security-boundaries.md): “automatic fail-closed behavior” listed among non-guarantees | Replaced with the actual remaining limit: no immediate revocation of buffered output. Added the implemented default freshness gate to supported properties. | Freshness gate and active-WS expiry/failure tests |
| D08 | Security boundary B1/startup describes all platforms as a Linux character device; every source is described as lacking health checks | Distinguished OS, character and USB adapters and scoped Linux fallback table. Quantis module-status checks are acknowledged; source min-entropy and ChaosKey health remain unassessed. | `entropy_source_linux.go`, Windows adapter, `quantisusb/source.go`, `chaoskey/source.go` |
| D09 | Security availability text treats 32 MiB as an unconditional generation ceiling | Rephrased as configurable `-max-bytes`, default 32 MiB. The separate upload body cap remains 32 MiB. | `internal/config/config.go`, generated-response validation, `upload.go` |
| D10 | [Root reseeding](root-reseeding.md): fail-open diagram/table, indefinitely blocked supported reader and future strict-policy/deadline work | Updated diagram and failure table to the current policies, late-read discard, joined cleanup and recovery. Removed completed policy/deadline tasks from future work; kept source quality and independent review. | Seed validation, source I/O, generation policy and reseed tests |
| D11 | [External-review follow-up](review-followup-2026-11-10.md): strict policy is absent/opt-in and benchmark evidence is still only historical | Preserved PDF provenance and assessment date; refreshed four findings with later PRs, current defaults and measured Linux/rendering evidence. Native/physical/long-duration limits remain. | PR #52/#53/#56/#74/#76 and linked performance samples |
| D12 | [Configuration](configuration.md): “All server timing flags” are integer milliseconds | Scoped that rule to millisecond flags; documented duration-valued `-source-timeout` and hardware `-timeout`. Added missing `-source-timeout` row. | Server `DurationVar`/usage and hardware command flags |
| D13 | Configuration and [Windows](windows.md) source tables omit supported USB/ChaosKey selectors | Added both USB families, platform/driver scope and no-fallback rule. No claim of physical qualification was added. | Config platform validation, native USB selection and hardware guides |
| D14 | [Hardware probe](hardware-probe.md): first-inventory wording and incomplete device allowlist | Added ChaosKey identity and both current selectors; kept raw USB descriptors distinct from entropy data. | `internal/hardware` discovery and USB profiles |
| D15 | [Go quality](go-quality.md): unused client helper removal is future work | Marked the three verified unused files as removed and explained the denominator effect. Other remaining helper candidates are not claimed removed. | Repository-wide caller/import search and current export path |
| D16 | Go quality: four fuzz targets, all duration-budgeted | Corrected to six; four use `FUZZTIME`, two USB targets use `USB_FUZZTIME` with a count default. Updated the short-run example to bound both groups. | `scripts/test-fuzz.sh` |
| D17 | Go quality: dated coverage improvements can be mistaken for current totals | Labelled historical measurements, changed historical present tense and linked the exact-main 82.8% snapshot, counts and gaps. No narrow local-test percentage is advertised as module coverage. | Main CI #43300419200 `go-coverage` artifact |
| D18 | [Technical index](README.md): architecture/driver entries describe the old roadmap/ChaosKey plan | Updated descriptions and linked the current coverage report, this change report and hardware command examples. | Current documentation/source tree |

## Source removals

| Removed item | Caller check / effect |
| --- | --- |
| `cmd/entropy-client/internal/pipe/pipe.go` | `WriteStdout` and its commented HTTP prototype had no callers/imports. Active raw stdout uses the cancellation-aware `internal/output` path. |
| `cmd/entropy-client/internal/protocol/entropy.go` | Unused `EntropyResponse` data structure; no executable statements or callers. It is internal, not a public API schema. |
| `cmd/entropy-client/internal/metrics/bandwidth.go` | `CountWrite` simply forwarded `Write` and had no callers. Live metrics/counters are retained. |
| Commented code in `internal/drbg/drbg.go` | Removed obsolete mutex/channel/generate/return alternatives and old field-type comment; corrected an escaped apostrophe. Executable generator operations and locking remain identical. |
| Commented pools/counter prototype near the start of `entropy-server.go` | Removed inactive `qrngBuffer`/`entropyPool`/32-MB `bufPool` and duplicate `ActiveInstances` sketches. Live pools, counters and compatibility fields are retained. Other legacy comments were not swept indiscriminately. |

The first and third files contain **three uncovered statements** in the
baseline. Removing them improves the ratio without exercising more behavior;
the [baseline CSV](coverage/2026-11-13-main.csv) preserves that distinction.

## Added material and checks

- [Coverage review](coverage-review-2026-11-13.md): exact-main artifact/source
  provenance, weighted counts and prioritized active/native/endurance gaps.
- [Hardware examples](hardware-examples.md): Linux and Windows source mapping,
  inventory/probe, strict server startup, loopback testing, unique TLS identity,
  client waterfall and separate ChaosKey cooked/raw capture. Examples follow
  current option parsing; no physical success or throughput is fabricated.
- Capture CLI regression tests: failure writing human/JSON reports returns
  failure while preserving a completed sample; an unavailable device is
  explained in human output, returns failure and creates no capture file.

Focused local checks cover the changed capture behavior and existing DRBG/output
regressions. Full module coverage, race/vet/lint, platform/package/browser and
security checks belong to GitHub CI for this PR. A new percentage must be read
from that run rather than inferred by adding these narrow tests to the baseline.
