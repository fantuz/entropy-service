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
| Words and digest | `/v1/meta/random?words=8` | Server-rendered HTML, refreshed by the server |

Refresh the gallery page to obtain another set of images/QR samples. The words
frame refreshes itself. Binary downloads such as `/v1/data/random` are not
shown in frames; use a client or `/api-bytes.html` instead.

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

This page polls the existing endpoints approximately every five seconds and
renders a small subset of values. The original API endpoints are unchanged.
For monitoring, continue querying the JSON/Prometheus routes directly.

Compatibility aliases `/health-1`, `/health-2` and `/metrics-1` remain available,
and their links are grouped in a collapsible section. Do **not** scrape both
metrics aliases as independent data sources: they represent the same service.

The dashboard is informational and does not add authentication. Use normal
network/access controls for externally exposed deployments.

[Main README](../README.md) · [API reference](api.md) · [Operations](operations.md)
