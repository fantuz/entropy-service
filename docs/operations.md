# Stability, operations and resource limits

## Startup reports and logs

Both utilities group their invocation settings, units and startup checks in a
plain-text report. `OK` means an observed check succeeded; `OFF` marks a disabled
feature and `WARN` calls attention to a fallback or limitation. Server startup
and timestamped runtime logs share stderr, with the utility name on log lines;
captured server logs contain no forced ANSI colors. Existing shutdown traffic
and connection summaries are preserved. Interactive client runs also log their
termination reason and reception/analysis totals. `-headless` keeps stdout
available for machine-readable output and skips the interactive report.

Server checks cover registry loading, TLS key-pair loading and certificate
dates, the actual opened seed source (including fallback), complete root seed
reading, DRBG initialization and successful HTTP/HTTPS/debug listener binding.
Certificate date checks do not establish trust or hostname validity. Debug bind
failure now fails startup instead of announcing an unavailable listener.
`/live` and `/ready` remain the authoritative running-service probes; the
startup report is a point-in-time observation.

The client retains its separate preflight sample and three-second pause.
Its report shows requested versus fetched bytes, analysis and display limits,
terminal geometry and enabled panels. Endpoint credentials, queries and
fragments are omitted from human and JSONL reports; control characters in
reported paths are escaped. Statistical diagnostics screen the sample and do
not certify its source or unpredictability.

## Prometheus scrape configuration

Use `/metrics` as the single Prometheus scrape path for each server process.
Choose one HTTP or HTTPS listener per process, since both expose the same counters:

```yaml
scrape_configs:
  - job_name: entropy-service
    metrics_path: /metrics
    scrape_interval: 15s
    static_configs:
      - targets: ["127.0.0.1:8080"]
```

The canonical endpoint emits each metric family once, with seconds and bytes
as base units. `/metrics-1` and `/metrics-2` retain compatibility values, including
the old `wss_payloads`, whole-MiB generated count, millisecond seed age, root-only
reseed view and disabled buffers. Do not add aliases or their unit-converted
values together. Configuring multiple metrics paths for one process can duplicate
series across scrape jobs; the server cannot deduplicate a Prometheus configuration.
The legacy `wss_payloads` and `wss_payloads_total` names can also collide when a
parser normalizes counter family names. These compatibility aliases are absent
from `/metrics`; use that endpoint for Prometheus.

`rng_bytes_generated_total` includes internal DRBG seed derivation, whereas
`wss_payloads_total` counts completed outbound WS/WSS application messages,
including lab events. They measure different work. `http_requests_total` includes
health checks, metrics scrapes, upgrades and static files, excluding the debug
listener. Admission gauges describe reserved estimates and slots; their HELP
text distinguishes them from RSS and physical entropy.

## Resource limits

The generated-sample ceiling is **32 MiB**, shared by binary HTTP/WebSocket handlers and the client. Uploads have a separate fixed 32 MiB ceiling. Encoding can make the transmitted message larger than the underlying sample.

| Resource | Default policy |
| --- | --- |
| Incoming WebSocket message | 1 KiB maximum; the server does not need large client messages. |
| Entropy WebSockets | At most 128 admitted connections and 1024 MiB estimated working memory, shared across HTTP/HTTPS. Reservations last until disconnect. |
| HTTP generation and file analysis | At most 128 admitted requests and 512 MiB estimated working memory, shared across HTTP/HTTPS. Probes and static files are excluded. |
| RGB/bit-count images | Square size 64–1024, default 1024; gallery previews use 128. Reservation: 64 KiB request overhead + 1 MiB PNG scratch + 16 bytes per pixel, before allocation. |
| Sound synthesis | An additional two simultaneous synthesis/download slots. |
| CLI receive queue | One queued WS message; other in-flight reader/consumer buffers can also exist. |
| Network lifecycle | Context-driven cancellation, WS write deadlines and a shared five-second server shutdown deadline. |

Budgets are **admission estimates, not process RSS limits**. For N-byte WS samples the reservation is `N + 64 KiB` for raw/compact/hash formats, or `16N + 64 KiB` for JSON. With the default 1 GiB budget and full 32 MiB samples, that admits at most 31 raw/compact/hash connections, or one JSON connection, before other reservations. TLS, kernel buffers, garbage collection and retained allocations require additional headroom.

Invalid sample parameters return 400. A request whose reservation exceeds the entire memory budget returns 413; temporary connection/request or aggregate-memory exhaustion returns 503 with `Retry-After: 1`. Successful WebSocket admission is still followed by the usual Upgrade validation.

HTTP responses with status **400 or higher** are logged to stderr with method, path, status, duration and remote address. Query strings and request bodies are omitted. After a successful WS upgrade, later stream errors are connection events rather than new HTTP response statuses. Under systemd, console logs are available through `journalctl`.

## Shutdown executive summary

On SIGINT or SIGTERM, the server stops its public listeners and joins HTTP and
hijacked WebSocket handlers under the shared five-second deadline. It then logs
three process-lifetime summary lines before `shutdown complete`, for example:

```text
shutdown summary uptime=1m0s drain=complete served_MB=5.000 HTTP_body_MB=2.000 WS_payload_MB=3.000
shutdown connections TCP_accepted=4 HTTP_requests=7 WS_sessions=2 WS_messages=8 (HTTP requests include upgrades)
shutdown DRBG_generated_MB=9.000 (includes internal seed work; served bytes exclude transport framing and are not peer acknowledgements)
```

- **MB** is decimal (1,000,000 bytes). `served_MB` adds HTTP response-body
  writes and successfully completed WS application payloads. HTTP includes
  public static files, health/metrics, errors and generated outputs, excluding
  HEAD bodies. WS includes binary, compact, JSON/hash and word payloads, with
  their encoding/digest overhead; it excludes failed/incomplete messages.
- **TCP_accepted** counts accepted public HTTP/HTTPS connections, including
  failed TLS/HTTP sessions and sockets later upgraded to WS. It counts a
  keep-alive socket once; **HTTP_requests** counts its requests individually,
  including WS handshake attempts. **WS_sessions** counts successful upgrades;
  **WS_messages** counts successfully written application messages.
- Body/payload counters exclude HTTP headers, WS framing/control traffic,
  TLS/TCP overhead and debug-server traffic. Writes reflect server-side
  acceptance, not proof that a peer consumed the data.
- **DRBG_generated_MB** is separate: generation includes internal seed
  derivation and data whose response/message might not finish. It is not a
  served-byte measure.

If a server fails to drain within the deadline, the log uses
`drain=incomplete_snapshot`; remaining handlers might update counters after the
snapshot. SIGKILL, process crashes and fatal startup exits cannot produce a
normal shutdown summary. Counters reset when the process starts.

## Recent improvements: October–November 2026

| Area | Implemented change and practical effect |
| --- | --- |
| Seeding | Full startup/reseed reads, explicit errors and a single context-controlled reseed loop; readiness now reports stale or failed seeding. |
| CPU and concurrency | Removed the disconnected producer and busy-spinning WS branches; optional AES backend and private bulk generators reduce shared-lock contention. |
| Validation and budgets | Validated byte counts/cookies, a common 32 MiB sample ceiling, inbound WS limits and aggregate HTTP/WS admission budgets. |
| Lifecycle and diagnostics | Joined WS cleanup, client cancellation, synchronised client rate metrics and separate HTTP/WS cycle timing. |
| Transmission and UI | Opt-in compact/hash WS formats, bounded browser previews and worker-based presentation instead of sending/rendering both full text encodings. |
| Features and operations | Harmonic WAV output, streaming file analysis, embedded web assets and failed-request logging; word sampling loads the dictionary once instead of shuffling it per request. |

These are implementation improvements, not a claim of a measured percentage gain for every workload. The results below separate protocol size, encoding CPU cost and historical network throughput.

[Back to README](../README.md) · [Technical reference index](README.md)
