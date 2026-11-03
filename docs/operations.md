# Stability, operations and resource limits

The generated-sample ceiling is **32 MiB**, shared by binary HTTP/WebSocket handlers and the client. Uploads have a separate fixed 32 MiB ceiling. Encoding can make the transmitted message larger than the underlying sample.

| Resource | Default policy |
| --- | --- |
| Incoming WebSocket message | 1 KiB maximum; the server does not need large client messages. |
| Entropy WebSockets | At most 128 admitted connections and 1024 MiB estimated working memory, shared across HTTP/HTTPS. Reservations last until disconnect. |
| HTTP generation and file analysis | At most 128 admitted requests and 512 MiB estimated working memory, shared across HTTP/HTTPS. Probes and static files are excluded. |
| Sound synthesis | An additional two simultaneous synthesis/download slots. |
| CLI receive queue | One queued WS message; other in-flight reader/consumer buffers can also exist. |
| Network lifecycle | Context-driven cancellation, WS write deadlines and a shared five-second server shutdown deadline. |

Budgets are **admission estimates, not process RSS limits**. For N-byte WS samples the reservation is `N + 64 KiB` for raw/compact/hash formats, or `16N + 64 KiB` for JSON. With the default 1 GiB budget and full 32 MiB samples, that admits at most 31 raw/compact/hash connections, or one JSON connection, before other reservations. TLS, kernel buffers, garbage collection and retained allocations require additional headroom.

Invalid sample parameters return 400. A request whose reservation exceeds the entire memory budget returns 413; temporary connection/request or aggregate-memory exhaustion returns 503 with `Retry-After: 1`. Successful WebSocket admission is still followed by the usual Upgrade validation.

HTTP responses with status **400 or higher** are logged to stderr with method, path, status, duration and remote address. Query strings and request bodies are omitted. After a successful WS upgrade, later stream errors are connection events rather than new HTTP response statuses. Under systemd, console logs are available through `journalctl`.

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
