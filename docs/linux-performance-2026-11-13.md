# Linux and browser performance review: 2026-11-13

The review found a confirmed, avoidable web-loading cost after translations were added: each new non-English document downloaded its selected catalog again, without HTTP compression or cache validators. It did not identify a broad Linux throughput regression attributable to Windows support. A modest decrease in small HTTP-request throughput remains uncertain and is reported below rather than dismissed.

The newer terminal renderer is substantially faster at constructing output. Optional browser diagnostics and very large/high-DPI canvases remain more demanding than the default view, independently of translations.

These are controlled source-build comparisons on a shared VM, not release-binary capacity ratings or a guarantee of zero overhead. The [earlier v2.0.3 review](linux-performance-v2.0.3.md) remains a separate historical measurement.

## Revisions

| Evidence label | Commit | Purpose |
| --- | --- | --- |
| `prewindows` | `64a8d61437d2494414c30debbaf296538f17a31b` | Immediately before the Windows port |
| `windowsport` | `ef6362bbb8642f4b565094c614d66dba7fc75e53` | Initial Windows integration, tagged v2.0.0 |
| `v203` | `e7df8569bca23ca04996d8c725d0e883f5979f32` | v2.0.3, before the terminal-renderer optimization |
| `prei18n` | `42b7d8b219795cd922e71e6e52fda7c05e81bc1f` | Main immediately before interface translations |
| `current` | `4561cf2bc037f10edfc61ed8a048a858c9ec746d` | Main after translations, **before the static-asset fix** |

`current` is an evidence label, not a release name. In particular, v2.0.4 points to `dcc01a87aa2d517371f5262dfa95d2eb6e9f9f8d`, the terminal-renderer optimization; it predates translations. The initial Windows-port revision is included in server microbenchmarks and binary-size comparisons. End-to-end traffic compares `prewindows`, `prei18n` and `current`; the client comparison also includes v2.0.3.

## Method and evidence

All CPU-intensive phases ran sequentially. Source builds used Go 1.26.11, Linux amd64, CGO disabled, GOAMD64=v1, GOMAXPROCS=1 and GOGC=100. Release-footprint comparisons also used matching `-trimpath -ldflags='-s -w'` flags. The VM exposes an AMD EPYC 9V74, five CPUs in its affinity mask and a four-core cgroup quota. Microbenchmarks/client runs were pinned to CPU 0; actual servers and load generators used CPUs 1 and 2. Revision order rotated between repetitions.

- Server microbenchmarks: 128 samples, eight cases across four revisions, four rounds, 250 ms per sample, with allocation reporting.
- Loopback traffic: 60 samples, four rounds, 500 ms warm-up then 1.5 s measurement; HTTP/1.1 keep-alive, verified TLS 1.3 and binary WebSocket. Two HTTP clients or one WebSocket client checked complete payload lengths. All runs reported zero transfer errors.
- Small-HTTP follow-up: 12 samples, four rotated rounds, one-second warm-up and three-second measurement, including the **same current binary** under a second label as a negative control. All runs reported zero errors.
- Client: 120 samples, six identical deterministic cases across four revisions, five rounds, 300 ms per sample, with allocated and emitted bytes reported separately.
- Browser: Chromium 151.0.7922.34, three alternating samples of 50 timed frames; Firefox 153.0, two alternating samples of 30 timed frames. Ten warm-up frames preceded each run. Both used the production workspace/worker/draw pipeline with 16 deterministic, varying 64 KiB fixtures submitted every 50 ms. This isolates UI work from entropy generation, transport and device permissions.

[Committed evidence](performance/2026-11-review/README.md) contains compact per-sample CSVs, medians/ranges, environment/revision metadata, browser summaries and catalog network observations. It excludes binaries, private certificate material and large browser traces. Browser timings are callback wall time, not whole-browser OS CPU or physical-display FPS.

## Server: bulk generation remains in the same range

Source inspection found the random-byte HTTP and binary WebSocket handler bodies byte-identical from pre-Windows to the reviewed main. DRBG production changes only remove comments; cipher generation, locking, counters and policy remain the same. Linux keeps its native socket and terminal implementations. Windows BCrypt, WinUSB, ACL and console implementations are selected separately and do not run in Linux binaries.

Translation has no changes under `cmd/entropy-client` or server `internal`, nor to browser numerical-analysis modules or `worker.js`. Its Go change annotates the random-word HTML template. The byte endpoint, request wrappers, admission, generation guard, listeners and background samplers are unchanged between `prei18n` and `current`. [Source hashes](performance/2026-11-review/source-comparison.json) and the [small-request-path comparison](performance/2026-11-review/i18n-small-request-path.json) retain this attribution evidence. Source identity does not prove identical performance under every workload.

Selected server microbenchmark medians, microseconds/op:

| Case | Pre-Windows | Initial Windows port | Pre-i18n | Current |
| --- | ---: | ---: | ---: | ---: |
| AES read, 64 KiB | 10.08 | 10.44 | 10.15 | 10.68 |
| ChaCha20 read, 64 KiB | 99.07 | 101.94 | 101.94 | 99.57 |
| AES HTTP generation, 2 MiB | 354.27 | 359.11 | 359.48 | 358.37 |
| ChaCha20 HTTP generation, 2 MiB | 3280.72 | 3332.27 | 3440.87 | 3260.71 |

The allocation counts and bytes match across revisions for all measured generation cases. Bulk DRBG reads allocate zero bytes/objects; 2 MiB HTTP generation allocates 36,741 bytes/45 objects for AES and 35,893 bytes/44 objects for ChaCha20. Small timing differences have overlapping sample ranges: for example, current AES reads range 9.86–11.90 µs versus pre-Windows 9.97–10.59 µs. This does not establish a cipher speedup or slowdown.

End-to-end loopback medians, MiB/s:

| Backend / transport | Payload | Pre-Windows | Pre-i18n | Current | Current versus pre-i18n |
| --- | ---: | ---: | ---: | ---: | ---: |
| AES HTTP | 64 KiB | 804.4 | 788.0 | 731.8 | −7.1% |
| AES HTTP | 2 MiB | 1810.3 | 1897.6 | 1833.1 | −3.4% |
| AES HTTPS | 2 MiB | 1128.2 | 1121.8 | 1085.1 | −3.3% |
| AES WebSocket | 2 MiB | 1801.6 | 1820.8 | 1760.0 | −3.3% |
| ChaCha20 HTTP | 2 MiB | 436.0 | 427.3 | 438.6 | +2.7% |

The 2 MiB results are broadly similar in this run; their ranges overlap. WebSocket requests use a 1 ms interval to expose saturation headroom, not the normal browser/client display cadence or physical-network capacity. AES/ChaCha ratios depend strongly on this host's AES acceleration and should not be transferred to ARM or CPUs without equivalent instructions.

### Small HTTP requests: an unresolved modest decrease

The initial 64 KiB AES HTTP median was 7.1% below pre-i18n. A longer, warm follow-up reduced but did not eliminate the difference:

| Follow-up label | Median MiB/s | Observed range MiB/s | Server CPU seconds/GiB |
| --- | ---: | ---: | ---: |
| Pre-i18n | 894.1 | 871.3–924.5 | 1.103 |
| Current | 849.9 | 831.0–872.9 | 1.173 |
| Current-control, same current binary | 817.3 | 717.0–861.3 | 1.199 |

Current is 4.94% below pre-i18n in these follow-up medians. Running the identical current binary under a second label gives another 3.83% decrease relative to current. This demonstrates meaningful run-to-run variation, but does not prove the entire observed difference is noise. Conversely, unchanged request-path source and a faster current 64 KiB handler microbenchmark do not support calling this a confirmed translation-induced regression. A stable dedicated host, longer paired runs and profiling would be needed to resolve a small sustained effect. No server-throughput fix is claimed by this PR.

Shared cancellation changes have bounded work outside bulk generation: handshake watchers run while dialing; the absolute seed-read deadline guard runs during seeding/reseeding. Optional JSONL output has context-aware work per report record. These security/cancellation protections should not be removed on the basis of an uncertain small benchmark delta.

## Web: repeated catalog transfer is a confirmed cost

Before the static-asset fix, embedded assets had no ETag, Last-Modified, Cache-Control or Content-Encoding headers. Locale fetching requested `cache: "no-cache"`; its parsed-catalog Map belonged to one document and disappeared on navigation.

Observed catalog transfer per new document:

| Interface | Catalog body bytes | ResourceTiming transfer-size entry | Reload / cross-page result |
| --- | ---: | ---: | --- |
| English | 0 | 0 | No locale JSON requested |
| French | 118,762 | 119,062 | Full HTTP 200 body every time |
| Greek | 156,511 | 156,811 | Full HTTP 200 body every time |

Chromium recorded all 18 French and 18 Greek requests as actual wire status 200, without disk-cache reuse. Firefox confirmed all 12 French fetches transferred the full body. ResourceTiming transfer size includes its estimated response-header contribution and is not identical to a packet capture. Within a single page, repeat language switches reused the Map and introduced no worker analysis jobs.

English still adds an 8,681-byte runtime and explicit markup. Raw homepage HTML grows 13,430→21,671 bytes, upload HTML 20,858→30,172 bytes. Main-document HTML plus measured asset body bytes are:

| Page | Pre-i18n English | Current English | Current French | Current Greek |
| --- | ---: | ---: | ---: | ---: |
| Homepage | 59,824 | 77,449 | 196,211 | 233,960 |
| Upload / analysis | 158,380 | 182,456 | 301,218 | 338,967 |

Worker-import resources are excluded equally from both revisions. Only the selected catalog is fetched; adding eight translations does not download all eight at startup.

Local Chromium cold-load medians are about 33→41 ms for the English homepage and 49→59 ms for English upload. Cold translated upload applies its selected language around 100–111 ms, versus about 50 ms in English. These short loopback timings are noisy and not WAN predictions; transferred body counts are the stronger finding. For scale only, at an assumed 10 Mbit/s the French/Greek body alone serializes in approximately 95/125 ms; at 1 Mbit/s, approximately 0.95/1.25 s, before latency. Those are arithmetic illustrations, not measured internet results.

The corrective target is compression and safe reuse of fixed public assets/catalogs, with deployment invalidation. Random bytes, generated words/images and other dynamic API responses must remain outside that cache/compression policy.

## Browser redraw: modest translation cost, larger optional-view cost

At exactly matched 1440×1000 / DPR1 geometry, median main redraw callback milliseconds per rendered frame are:

| Browser / panels | Pre-i18n English | Current English | Current French | Current Greek |
| --- | ---: | ---: | ---: | ---: |
| Chromium, default closed | 2.25 | 2.24 | 2.66 | 2.74 |
| Chromium, structure + PCM + notch | 6.25 | 6.43 | 6.70 | 6.28 |
| Firefox, default closed | 8.50 | 8.12 | 8.15 | — |
| Firefox, structure + PCM + notch | 39.90 | 40.87 | 40.57 | — |

Translated labels add approximately 0.4–0.5 ms to Chromium's default callback in this sample, about 19–22% of that narrow callback measurement. This is not a 19–22% whole-browser CPU increase, and the direction was not reproduced in Firefox. Current English stays in the same range as the preceding interface. Worker-analysis medians remain about 0.6–0.7 ms closed / 3.3–3.6 ms open in Chromium, and about 1 ms closed / 4.5–5 ms open in Firefox.

Software Firefox's optional-view callbacks near 40 ms leave less room in a 50 ms submission interval; their p95 is approximately 43–48 ms, with isolated maxima up to 81 ms. The same cost exists before translations. Product defaults remain a lower display cadence of 5 FPS with optional diagnostic panels closed. Opening those panels enables bounded extra numerical work and additional canvases; increasing display cadence from 5 to 20 FPS requests four times as many redraws.

Counts matter: Chromium's closed cases render 150/150 submitted frames before i18n, 150/150 current English, 149/150 French and 150/150 Greek. Optional-open cases render 148/150, 149/150, 147/150 and 147/150 respectively. Firefox renders all 60/60 frames in every compared configuration. Coalesced/dropped intermediate work must not be presented as a speedup.

Cached language changes bind text and redraw the frozen sample; the worker job count remains zero. Their approximately 44–46 ms two-RAF completion time includes frame scheduling and is not that much CPU work.

### Wide/high-DPI stress predates translations

Both compared versions already contain wider desktop layouts and optional diagnostics. A paired English-only Chromium control at 2560×1440 used three rotated samples of 40 submissions each:

| Setup | Pre-i18n jobs / renders of 120 | Current jobs / renders of 120 | Median p95 submit-to-render latency, before→current |
| --- | ---: | ---: | ---: |
| DPR1, optional open | 71 / 65 | 71 / 66 | 221.5→209.8 ms |
| DPR2, default closed | 117 / 109 | 120 / 104 | 121.1→60.8 ms |
| DPR2, optional open | 56 / 56 | 59 / 55 | 266.0→264.9 ms |

All cases submitted 120 windows and reached the final frame within the bounded drain allowance. Both revisions coalesced work under pressure. Current standard closed views have about 0.92 million active backing pixels, versus 11.14 million for open optional panels at 2560/DPR2. Callback durations alone do not explain the presentation/scheduling delays; these runs do not prove a particular compositor defect. Cgroup throttling deltas were zero. Physical hardware-accelerated Firefox/Chromium tests are needed before making display-FPS claims or choosing an adaptive resolution policy.

## Client: output construction improved substantially

The current and pre-i18n Linux client binaries are byte-for-byte identical with matched stripped builds: SHA-256 `14b66b9dc2e769cbd9108df42c04e2fb8dc637007d0d3e6aaf17df534785e0da`. Translation does not add a CLI runtime path. Their measured medians differ by −2.9% to +4.4% with matching byte/allocation counts and overlapping ranges, providing a useful noise control.

Fresh client medians, milliseconds/op:

| Case | Pre-Windows | v2.0.3 | Pre-i18n | Current |
| --- | ---: | ---: | ---: | ---: |
| Default 1 MiB-prefix analysis | 3.475 | 3.516 | 3.320 | 3.303 |
| Full monitor 240×60 | 4.478 | 4.311 | 0.262 | 0.260 |
| Live random RGB waterfall 900×180, fresh builder | 15.143 | 15.194 | 0.165 | 0.160 |
| Live random RGB waterfall, discard writer | 15.248 | 14.789 | 0.0596 | 0.0621 |
| Repeated-color waterfall, fresh builder | 12.825 | 12.866 | 0.0297 | 0.0310 |

The optimized renderer constructs a full 240×60 monitor about 16.6× faster than v2.0.3, and the random RGB waterfall about 95× faster to a fresh builder. These are **output-construction speedups, not terminal FPS speedups**. Row caches are primed outside timing; first-render and resize costs are excluded.

Default prefix analysis remains 80 allocated bytes / one allocation in every revision. For random waterfall output, allocated bytes fall from 10.91 MB to 1.586 MB per builder refresh, or from 9.33 MB to 5,152 bytes with the counting discard writer. Those are transient Go allocations, not retained memory or RSS.

Random truecolor still emits about 1.58 MB/frame, approximately 30.1 MiB/s at 20 FPS. Repeated-color output falls from 1,443,611 to 164,859 bytes/frame, about 88.6% less. Actual terminal/SSH transport and painting can therefore dominate despite faster formatting. The optimization retains bounded buffers; with 512 cached rows and a 900×180 truecolor screen, source capacity bounds are roughly 5.3 MiB encoded rows + 1.8 MiB reusable screen + 0.75 MiB raw rows. This is a deliberate retained-memory/recurring-allocation tradeoff, not a leak finding.

## Binary footprint

Matched stripped source-build bytes:

| Program | Pre-Windows | Initial Windows port | v2.0.3 | Pre-i18n | Current |
| --- | ---: | ---: | ---: | ---: | ---: |
| entropy-server | 12,124,322 | 12,128,418 | 12,402,850 | 12,492,962 | 13,680,802 |
| entropy-client | 6,795,426 | 6,795,426 | 6,807,714 | 6,807,714 | 6,807,714 |

Server growth after translations is about 1.19 MB / 9.5%, mainly embedded assets; all nine JSON files total 1,101,257 bytes. Windows machine code is not bundled into Linux binaries. Idle server RSS medians are approximately 17.6 MiB pre-i18n and 17.5 MiB current, with short loaded observations around 18–25 MiB. File-size growth is not an equivalent resident-heap increase. These short samples do not qualify peak memory or long-duration leak behavior.

## Static-asset remediation validation

The corrective implementation was tested against the unmodified `4561cf2` executable in both Chromium 151 and Firefox 153. [Fix provenance](performance/2026-11-review/static-fix-provenance.json) records matched build flags, executable/source digests and the catalog content version. [Chromium](performance/2026-11-review/static-assets-chromium.json) and [Firefox](performance/2026-11-review/static-assets-firefox.json) retain actual HTTP/cache observations.

| Catalog | Original identity body | Served gzip body | Cold body reduction |
| --- | ---: | ---: | ---: |
| French | 118,762 bytes | 43,678 bytes | 63.2% |
| Greek | 156,511 bytes | 57,081 bytes | 63.5% |

Before the fix, each of those French document loads transferred 119,062 bytes including ResourceTiming's header allowance. Cold transfer is now 43,978 bytes. Subsequent French cross-page navigation, return navigation and explicit reload each transfer zero catalog bytes in both browsers; they use the fresh versioned catalog cache in these runs. Greek gzip/identity contents were decoded and compared directly. Direct conditional HTTP probes also return empty-body 304 responses for matching validators. Gzip uses `BestSpeed`; the measured served sizes intentionally differ from the earlier offline level-6 compression estimate.

The handler prepares public static-file hashes and gzip representations once. It serves strong, encoding-specific ETags and `Vary: Accept-Encoding`, retaining standard HEAD and conditional semantics. Byte-range requests use identity when acceptable, including multipart ranges; clients forbidding identity receive a complete gzip response with ranges ignored, as HTTP permits. This prevents advertising a gzip encoding around an uncompressed multipart wrapper. The locale URL carries a SHA-256 version derived from sorted catalog filenames and contents. Matching versioned catalogs receive `public, max-age=31536000, immutable`; unversioned or mismatched URLs and normal HTML/scripts/styles revalidate with `no-cache`. Changing a catalog also changes the served i18n script's version/ETag, so a new deployment selects a new catalog URL. Missing/repeated/wrong version parameters cannot make new catalog contents immutable under an obsolete version. Error responses do not inherit immutable caching.

Generated entropy routes are separate from this static handler. The corrective change also adds explicit `Cache-Control: no-store` to successful binary streams, covering `/v1/data/random` and `/v1/data/test`; those older responses previously lacked it. HTTP/browser probes verify byte length, no static ETag and no compression even when gzip is requested. Random-word HTML already forbids storage. Public `/wordlists.json` is dictionary metadata and retains its existing revalidation policy; cookie-dependent metadata keeps its separate privacy policy.

There is a bounded startup tradeoff, measured explicitly rather than calling the optimization free:

| Measure | Before fix | Corrective implementation |
| --- | ---: | ---: |
| Process start to HTTP readiness, median of six alternating samples | 26.3 ms | 44.1 ms |
| Short idle RSS, sampled 150 ms after readiness | 17.3 MiB | 21.0 MiB |
| Prepared representation bodies retained by handler | — | 557,848 bytes (about 545 KiB) |
| Startup asset preparation microbenchmark | — | 19.02 ms; about 3.67 MB transient allocated bytes |
| Conditional static 304 microbenchmark | — | 1.67 µs; 1,128 allocated bytes (including test recorder) |

These startup comparisons disable HTTPS in both executables and use the same warm filesystem/toolchain. Readiness includes process launch/polling; short RSS includes runtime/temporary allocation effects and is not the retained-cache size or a leak test. One compressor/buffer is reused, and ordinary raw identity files stay in their immutable filesystem instead of a second retained raw-body map. Neither hashing nor compression runs on entropy requests or on repeated static-file requests. [Startup samples](performance/2026-11-review/static-startup-samples.csv), [summary](performance/2026-11-review/static-startup-summary.json) and [constructor/304 benchmark samples](performance/2026-11-review/static-asset-benchmarks.csv) retain the measurements.

The existing nine-language browser regression suite also passes in both browsers with the new handler. A cached language switch preserves the frozen sample hash and worker-job count; English still fetches no catalog. This fixes the confirmed delivery overhead. It does not claim to fix the uncertain small-HTTP difference or the separate wide/high-DPI rendering pressure.

## Limits and next priorities

This shared Linux VM provides directional estimates and clear byte-count evidence, not precise small percentage attribution. There are no actual Windows runtime benchmarks, ARM runs, physical devices, physical-network tests, native GPU/display measurements, real-terminal/SSH painting tests or long-duration memory qualifications here. Browser submissions use the real numerical/UI pipeline but deliberately exclude server/network input work. Callback timing excludes asynchronous paint/compositor costs and Firefox timer resolution is coarse.

With the repeated static/catalog transfer corrected, the remaining priorities are to profile wide/high-DPI optional views on physical browsers before changing their rendering policy, and to use a stable-host paired/profiled test if the modest 64 KiB HTTP difference matters operationally. Removing Windows support, translations or cancellation protections is not supported by this evidence.
