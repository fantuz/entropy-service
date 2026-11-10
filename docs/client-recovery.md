# Client disconnects and bounded retries

The client now reports the actual WebSocket close code and reason, including
server code **1013** when reseed freshness pauses output. HTTP failures retain
their status and `Retry-After` hint. Remote text is quoted in errors and logs
so control characters cannot clear or alter a terminal. Error messages never
become random output bytes.

The default remains **`-retries=0`**: a failed measured transport stops the
run with a nonzero exit and the underlying error. Normal completion, output
limits, duration and interruption retain their existing meanings.

For unattended reception, explicitly permit a finite number of retries:

```sh
entropy-client -headless -mode=stream \
  -wsurl=ws://127.0.0.1:8080/stream \
  -retries=5 -retry-delay=1s \
  -output=received.bin -output-bytes=1048576 -duration=2m
```

| Setting | Policy |
| --- | --- |
| `-retries` | Maximum **total** retries during one measured run, 0–100; never resets after a successful frame. |
| `-retry-delay` | Initial delay, 1ms–30s; factory default 1s. |
| Backoff | Exponential, with up to 25% scheduling jitter, capped at 30s. |
| `Retry-After` | Valid seconds or HTTP dates are honored up to 30s. A longer hint ends automatic retry rather than retrying earlier than requested. |
| Deadlines | Run duration, Ctrl+C and SIGTERM interrupt dialing, reads and backoff. |

Retryable conditions are HTTP 408/429/500/502/503/504, WebSocket
1001/1006/1011/1012/1013 and temporary network failures/truncated HTTP bodies.
Authentication/policy denials, untrusted/expired/wrong-host certificates,
normal WebSocket completion, malformed or oversized payloads, unknown DNS
names and configuration errors remain terminal. Changing transport or disabling
TLS verification is never a recovery strategy.

The interactive preflight remains a separate three-second check; retry
settings apply to the **measured run**. Use `-headless` for unattended jobs.
Startup and the JSONL run settings record the retry policy. Retry progress is
logged to stderr, preserving raw stdout/SSH pipes and JSONL output ownership.

The output file/device is opened once. Reconnecting does not reopen, truncate,
append a second header, reset accounting or reset the global byte/duration
limit. Only complete accepted frames/bodies reach output; failed HTTP samples
are discarded. Retries request **new random bytes**, not replay of missing
network data. No gap-free or exactly-once remote delivery is claimed.

The receiver keeps one delivery queue and the existing owned-buffer leases.
Private files, binary stdout, device writes and pool mixing use the same
validated sinks and failure rules as before. An output-device, pipe, file or
report failure is terminal and is not retried as a network outage.
