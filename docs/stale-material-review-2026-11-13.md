# Stale material review, 13 November 2026

Baseline: main [`de5e28e`](https://github.com/fantuz/entropy-service/commit/de5e28e85ef3696aeec02e54962f335c24984cad),
after PR #80. **This is a proposed cleanup list. All listed material remains
unchanged in this PR.** The graph-palette/layout changes are separate from this
audit; no stale file, function, field or prototype comment was removed.

Evidence comprises tracked path/symbol searches, file contents/sizes,
build/packaging inputs and read-only Go executable metadata. The old binaries
were not executed. Absence of an in-tree caller does not establish absence of
unpublished branches, external bookmarks or downstream consumers.

## What I would retire in a later cleanup

| Priority | Candidate | Why it can go | Safe scope / replacement |
| --- | --- | --- | --- |
| First | [Client executable](../cmd/entropy-client/entropy-client), [server executable](../cmd/entropy-server/entropy-server) | Frozen Linux amd64 build products, **23,633,142 bytes combined**. Both record Go 1.26.10, `v1.7.1+dirty`, source revision `6cd908df` dated 8 November. The server still records the retired Diceware dependency. Running these from `cmd/` can give behavior unrelated to current source. | Remove these generated files from tracking and ignore the nested output paths. `make build` already writes fresh binaries to `bin/`; GoReleaser builds release archives from source. Removing HEAD copies would not erase Git history or automatically shrink historical clones. |
| Next | Client batch-statistics [chi.go](../cmd/entropy-client/internal/stats/chi.go), [monobit.go](../cmd/entropy-client/internal/stats/monobit.go), [serial.go](../cmd/entropy-client/internal/stats/serial.go), [runner.go](../cmd/entropy-client/internal/stats/runner.go), [result.go](../cmd/entropy-client/internal/stats/result.go) | A self-contained, unused 3,171-byte chain: its `RunAll` calls these batch tests, with no caller found outside the chain. Its old approximations can confuse readers about the running diagnostics. | Remove the five together after a fresh caller/compile check. Keep `stats/stream.go`, stream tests and active `internal/diag`. This concerns inactive maintenance code, not a flaw in running generation. |
| Next | `FetchEntropySimple` in [http_client.go](../cmd/entropy-client/internal/client/http_client.go) | Only the declaration was found. The active `FetchEntropy(ctx, ...)` path already supplies caller-controlled cancellation and bounded network reads. | Remove the function only; retain the file's active HTTP/TLS/redirect/payload handling. |
| Next | [source-lab-proposal.md](source-lab-proposal.md) | A 4,342-byte, previously unlinked proposal that largely duplicates [source-labs.md](source-labs.md). It describes decoded audio/Hz analysis as future work, although PR #80 implemented it, and loopback deployment as a first prototype. | Preserve any unique adapter-design rationale in the implemented guides, then retire the duplicate or explicitly archive/date it. External documentation bookmarks may still exist. |
| Low | `RunAll` wrapper in [internal/tests/tests.go](../cmd/entropy-client/internal/tests/tests.go) | No callers found; the wrapper directly chooses stdout while active rendering uses writer-aware diagnostics. | Remove this function only; retain the writer-aware printing functions and tests. |
| Low | Client/server `Config.LogLevel` fields | No parser binding or consumer found; the server already labels its field unused. | Remove only these fields after checking pending branches; keep both active config files. |
| Low | Commented HTTP/cookie prototypes in [http_client.go](../cmd/entropy-client/internal/client/http_client.go), predecessor bodies in the batch statistic files | Inactive `http.Get`, cookie-file, query-mutation and alternative-statistic sketches add reading/search noise. | Remove prototype comments when touching those areas; retain current rationale and security-boundary comments. Git history preserves the old sketches. |

## Stale wording to update, not documents to delete

These are recommendations only; this audit leaves the old text intact.

| Document | What is stale | Recommended correction |
| --- | --- | --- |
| [DRBG construction](drbg-construction.md) | Character-device-only acquisition description and wording that no source-level timeout exists. | Describe current OS/platform/USB readers and `-source-timeout`; link [source I/O](source-io.md). Retain native-call/broken-driver limits and the byte-level specification. |
| [Architecture status](architecture-review.md), [external-review follow-up](review-followup-2026-11-10.md) | Repeated endurance/fault tests remain a future priority; release summaries omit their gate. | Distinguish existing short Linux-race/native-Windows checks/release dependency from still-needed longer physical/host runs. Preserve assessment dates. |
| [Go quality](go-quality.md), [coverage review](coverage-review-2026-11-13.md) and links calling that snapshot current | The exact `dcc01a8`/82.8% baseline is historical; some listed gaps have since been addressed. | Relabel historical/current pointers while preserving exact-SHA counts/CSV. Never replace old measurements or infer full coverage from narrow checks. |
| [Browser UI](browser-ui.md), first pacing paragraph | The first display-rate list omits 20 FPS, although later sections and implemented controls include it. | Include 20 in that list. |
| [API reference](api.md) and older LAB summaries | “Loopback only” shorthand omits trusted inbound Host checks. | Mention loopback peer **and** trusted Host and link [LAB safety](lab-safety.md)/custom-host migration. |
| [README](../README.md), “Formal review” label | The target is a first-pass code-informed assessment, not formal/independent assurance. | Rename the label to match “Architecture and assurance status”; keep the reference. |

## Conditional historical-gallery reduction

Four images total **308,316 bytes**:
[homepage.webp](images/homepage.webp),
[homepage-preview.webp](images/homepage-preview.webp),
[stream-analysis.webp](images/stream-analysis.webp) and
[stream-analysis-preview.webp](images/stream-analysis-preview.webp).
They are linked in the README's explicitly historical **Earlier November 2026
interface** gallery. Retire them only if intentionally retiring that gallery
and its links. My default is to keep them: they are not orphans or misleading
current previews. All 21 tracked `docs/images/` files have references at this
baseline.

## Material I would keep

- The `aa_` health/metrics and six `zz_v1-*.html` helpers are linked, documented
  and tested public compatibility paths. Retiring/replacing them needs an
  explicit compatibility decision.
- Hardware adapters, driver overlays, qualification templates, wordlists, source
  pins and notices have runtime/build/provenance/licensing roles. Experimental
  status or an old device model does not establish that a file is dead.
- Raw performance, coverage and statistical results, including failed/control
  logs, explain measurement validity after CI artifacts expire. Their dates
  are intentional evidence.
- Transport diagnostic helpers with test callers and active metrics are not
  safe whole-file deletions. Some helpers could move into test files after
  preserving useful coverage; the localhost-dependent HTTP test would benefit
  from a hermetic fixture instead of deletion.
- Ignored legacy buffer flags are deliberate, tested CLI compatibility. Review
  deprecation before removing accepted inputs.

I would start a later cleanup with the two checked-in build products, then the
isolated batch-statistics chain and duplicated proposal. Keep cleanup separate
and reviewable, with focused local checks and full Linux/Windows CI. Removing
unexecuted statements may change a coverage ratio without testing more behavior;
report that distinction explicitly.
