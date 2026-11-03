# Browser showcases and operations view

The embedded homepage is available at `/` on the configured HTTP or HTTPS address.
It displays interactive demonstrations and links to the original HTTP/WebSocket APIs.
There is no separate `/web` HTTP prefix; the assets are embedded in `entropy-server`.

## Endpoint gallery

Open `/api-gallery.html` for a framed view of these existing HTTP responses:

| Frame | Actual HTTP route | Content |
| --- | --- | --- |
| Random image | `/v1/image/random` | RGB PNG (1024 × 1024) |
| Heatmap | `/v1/image/heatmap` | Population-count PNG (1024 × 1024) |
| QR code | `/v1/qr/random` | PNG QR code containing generated hex |
| Words and digest | `/v1/meta/random?words=8` | Words and SHA-256 extracted from server-rendered HTML |

The gallery requests all four endpoints together on one browser-controlled clock
(default: approximately 10 seconds; adjustable from 5 to 120 seconds). It waits
for the batch to finish before swapping previews and never overlaps cycles.
Slow or failed requests retain their last successful preview. The word response
is parsed rather than embedded as a self-refreshing iframe, so its HTTP meta
refresh does not desynchronise the gallery. PNGs are displayed as returned by
the server, with no added opaque iframe background or border; a QR code's own
quiet zone is preserved for scanning. All previews share a common square region
and short captions are constrained to one line. Binary downloads such as
`/v1/data/random` are not shown; use a client or `/api-bytes.html` instead.

**These are demonstration views**, not statistical evidence or an entropy
certificate. The implementation does not introduce aliases such as
`/v1/images` or `/v1/words`.

## Stream-analysis counters

`/api-stream.html` displays **two independent cumulative rows**:

- **Received**: every valid binary message delivered to the browser and its full
  byte size, including frames skipped by the browser-side analysis sampler.
- **Analysed**: only frames whose bounded samples were processed and rendered,
  and the number of bytes actually analysed from each frame.

The latter counters intentionally lag under load. The analysis uses at most the
first 64 KiB per accepted frame, and display updates are throttled to five per
second. Receiver counters are not equivalent to byte generation or network
throughput benchmarks. The Yin/Yang legend is centered under the chart.

### Shannon entropy sample alert

The **Shannon entropy** instant value now has its own panel, independent of the
byte histogram. Choose a red-alert threshold between **1.00000** and **8.00000**
bits per byte, in increments of **0.00001** (default **7.99990**). The panel
alone turns red when the *full-precision* sample estimate is strictly below
that threshold. Equality is not an alert. The numeric readout is rounded to
five decimals and can therefore appear equal to the threshold at its boundary.
Changing the selector immediately reevaluates the most recently analysed
sample, without reconnecting the WebSocket or contacting the server.

The threshold is a **per-browser preference**, stored in `localStorage` if
available. It persists across page reloads in that browser, but never changes
the server, API responses or the preferences of other visitors. Restricted
browser storage falls back gracefully to the default.

**Interpretation:** Shannon's plug-in entropy estimator is sample-size-dependent
and commonly *below* 8 even for excellent random streams. In particular, a
threshold of 7.99990 can flag perfectly healthy 4 KiB or 64 KiB samples. This
alert is a user-selected **statistical watchpoint**, *not* a cryptographic
health check, entropy-source exhaustion detector, or independent randomness
certification. Only the Shannon panel changes colour; other charts remain
unaffected.


## Word display stability

`/api-words.html` reserves a stable display region so new strings do not resize
the surrounding layout. Long messages scroll inside the words panel, including
on narrow mobile devices, where words may be displayed one per line. The SHA-256
field and connection notice retain their positions.

## Health and metrics

`/api-status.html` presents a *single browser dashboard* using existing routes:

| Canonical route | Meaning |
| --- | --- |
| `/live` | HTTP liveness, independent of seed freshness |
| `/ready` | Fresh reseeding and readiness (may return 503 when degraded) |
| `/metrics-2` | Prometheus service counters and limits |

This page polls the existing endpoints on a configurable interval (default
five seconds, selectable from **0.25 to 60 seconds**). The next cycle starts only
after the current cycle completes, so slow responses never produce overlapping
scrapes. Choosing 0.25 seconds specifies a minimum idle delay, **not** a
guaranteed four updates per second; the network and parsing time are additional.

The dashboard renders an operational snapshot, four admission-capacity gauges
and twelve activity indicators. Three additional indicators show **seed
freshness headroom**, **successful root reseeds/min**, and **failed root
reseeds/min**. The freshness percentage is remaining time before the configured
readiness age limit; errors or readiness degradation mark it critical.

### Reading the capacity gauges and KPIs

- **HTTP/WS memory pressure** is estimated reserved working memory divided by
  the respective configured admission budget. This is *not* actual process RAM,
  Go heap, or physical entropy-buffer fill.
- **HTTP/WS slot utilization** is active admitted requests or connections divided
  by the configured limit. Short HTTP requests can finish between scrapes.
- **Dim peak shadows** show server-side high-water marks for those same four
  gauges. New metrics (`http_generation_requests_peak`,
  `http_generation_memory_peak_reserved_bytes`, `ws_connections_peak`
  and `ws_memory_peak_reserved_bytes`) update on each successful admission
  under the existing budget lock, so brief peaks remain visible even if
  no dashboard was open at the time. The darker bar spans up to the peak;
  the bright bar still displays current utilization.
  The peaks reset when the server process restarts, not when a browser
  refreshes, and do not represent measured process RAM.
- **DRBG generation MiB/s**, **WebSocket frames/s** and **HTTP requests/s** are
  deltas between successful scrapes, not measurements of QRNG source throughput.
  HTTP requests include static assets and the dashboard's own polling.
- **HTTP/WS rejection pace** sums admission rejections due to connection/request
  limits, estimated-memory pressure and excessive requested sizes. The latest
  interval is scaled to *rejections per minute*, not a percentage of requests.
  Cumulative rejection totals are also shown.
- **DRBG instance count** and **successful root reseeds** are existing server
  counters. Rate calculations show *SAMPLING* until two good snapshots exist,
  and reset on failed scrapes or counter rollback. A bounded window of recent
  samples (up to approximately five seconds, plus one prior baseline for
  slower polling) reduces rate jitter at 250 ms intervals.
- **Seed freshness headroom** is computed from `/ready` values
  `reseed_age_ms` and `max_reseed_age_ms`, not an entropy-buffer measurement.
  A stale or error-marked root is flagged regardless of age headroom.
- **Successful and failed reseeds per minute** are derived from changes in
  `rng_reseeds_total` and `rng_reseed_failures_total`. They do not indicate
  entropy-source throughput or guarantee fresh randomness.
- The historical `qrng_buffer_*` Prometheus metrics are fixed at zero because
  the older software buffer is no longer active. Displaying its fill percent
  would be misleading, so this dashboard does not use it.

These estimates help indicate admission exhaustion and concurrency pressure;
they are not a complete memory profiler. Use runtime/pprof data for heap/RSS
investigation and hardware instrumentation for actual seed-source buffering. The original API endpoints are unchanged.
For monitoring, continue querying the JSON/Prometheus routes directly.

Compatibility aliases `/health-1`, `/health-2` and `/metrics-1` remain available,
and their links are grouped in a collapsible section. Do **not** scrape both
metrics aliases as independent data sources: they represent the same service.

The dashboard is informational and does not add authentication. Use normal
network/access controls for externally exposed deployments.

[Main README](../README.md) · [API reference](api.md) · [Operations](operations.md)
