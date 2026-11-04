# Configuration — server and client

Run either binary with `-help` for its complete flag list. Boolean values use `-flag=true` or `-flag=false`.

## Server

All server timing flags accept **plain integer milliseconds**, without a unit suffix: `-reseed-ms=250`, not `250ms`. Sizes below are bytes unless explicitly labelled MiB.

### Source and reseeding

| Flag | Default | Purpose |
| --- | --- | --- |
| `-device` | `/dev/qrandom0` | Source device; OS fallback is allowed unless required. |
| `-require-device` | `false` | Fail startup if the requested device is unavailable. |
| `-drbg` | `chacha20` | Backend: `chacha20` or `aes-256-ctr`. |
| `-cpu-mix` | `false` | Supplement seed reads with RDSEED/RDRAND on supported amd64 CPUs. |
| `-rdrand-mix` | `false` | RDRAND-only mode; requires CPU support. |
| `-reseed-ms` | `250` | Root reseed interval: 1–10000 ms. |
| `-buffer-reseed` | `256` | Bytes per root reseed: 32–65536. |
| `-max-reseed-age` | `0` | Readiness age limit in ms; zero selects `max(1000, 4 × reseed-ms)`. |

### Listeners and TLS

| Flag | Default | Purpose |
| --- | --- | --- |
| `-http` | `:8080` | HTTP address; binds all interfaces by default. |
| `-https` | `:8443` | HTTPS address; binds all interfaces by default. |
| `-enable-https` | `true` | Enable HTTPS; the packaged service sets this to false. |
| `-cert-file` | `cert.pem` | TLS certificate path. |
| `-key-file` | `key.pem` | TLS private key path. |
| `-debug` | `false` | Debug output and loopback pprof on port 6060. |

### Output and resource budgets

| Flag | Default | Purpose |
| --- | --- | --- |
| `-max-bytes` | `33554432` | Maximum binary sample: 1 byte to 32 MiB. |
| `-words` | `20` | Default word count and per-request word limit. |
| `-refresh-ms` | `5000` | Default `/bytes` WS interval in ms. |
| `-refresh-colors-ms` | `1500` | Default `/stream`, `/colors` and `/words` WS interval in ms. |
| `-refresh` | `5000` | HTTP word-page interval in ms; HTML rounds up to seconds. |
| `-fingerprint` | `2097152` | Upload prefix analysed statistically; full-upload SHA-256 is unaffected. |
| `-cookie` | `false` | Enable HTTP byte/word preference cookies. |
| `-ws-max-connections` | `128` | Shared WS connection limit. |
| `-ws-memory-mib` | `1024` | Shared WS estimated memory budget in MiB. |
| `-http-max-requests` | `128` | Separate HTTP work limit. |
| `-http-memory-mib` | `512` | Separate HTTP estimated memory budget in MiB. |

`-buffer-qrng` is deprecated and ignored. `-buffer-entropy` is an unused legacy option; neither controls an active pool or ring buffer.

Source selection opens and inspects the **actual file descriptor**. With
`-require-device=true`, `-device` must be non-empty and the named character
device must be opened successfully; **no startup fallback** is permitted.
`-device=/dev/urandom -require-device=true` deliberately requires that OS
character device, not a physical QRNG. To require a specific hardware device,
name its path explicitly. With strict mode off, failure to open/validate the
requested device selects `/dev/urandom`; if that also fails, the Go OS-backed
`crypto/rand.Reader` is used. `X-RNG-Source`, `/ready` and `/metrics-2`
report the **actual selected reader**, not the originally requested path.
Optional CPU mixing supplements seed bytes but does not replace this
reported base source. This is **startup-only** fallback: an error from the
already opened source during a later reseed does not switch readers.
Source selection establishes neither entropy quality nor physical provenance.

`-cpu-mix` prefers RDSEED, falls back to RDRAND, and takes precedence over `-rdrand-mix`. Mixing applies to **startup and reseed input**, not every output byte. Hardware attempts are bounded; if CPU randomness is unavailable, `-cpu-mix` retains the configured source bytes and logs the first failure. It does not turn the CPU instructions into a replacement for the configured source.

## Client

Client timing options accept **Go durations**, such as `50ms` or `1s`. Byte counts are plain integers. Reception, analysis and terminal display have separate settings; a frame below means one HTTP response or one WebSocket message.

### Transport and payload

| Flag | Default | Purpose |
| --- | --- | --- |
| `-mode` | `pull` | HTTP `pull` or WebSocket `stream`. |
| `-slice` | `2097152` | Bytes per response/message: 1 byte to 32 MiB. |
| `-refresh` | `1s` | HTTP request interval or requested server WS interval; minimum 1 ms. |
| `-writeout` | `false` | Experimental Linux write of every received frame to `/dev/urandom`; does not credit the [kernel's entropy count](https://man7.org/linux/man-pages/man4/random.4.html). |

Transport URLs are listed separately to keep the table readable:

- `-url`: `http://127.0.0.1:8080/v1/data/random`
- `-wsurl`: `ws://127.0.0.1:8080/stream`

Pass **bare endpoints** to `-url` and `-wsurl`: the client appends its own `bytes` and `refresh` query parameters. Use `-slice` and `-refresh` to control them. The CLI consumes `/stream` raw messages, not `/bytes` compact envelopes.

### Analysis and terminal display

| Flag | Default | Purpose |
| --- | --- | --- |
| `-analysis-bytes` | `1048576` | Maximum prefix bytes analysed per selected frame.<br>Range: 0 to 33554432; `0` selects the full frame. |
| `-analysis-interval` | `250ms` | Minimum interval between analysed frames.<br>Non-negative; `0` analyses every frame. |
| `-display-interval` | `1s` | Terminal redraw interval; minimum 1 ms.<br>Does not change the requested reception interval. |
| `-dashboard` | `false` | Reception/analysis rates and histories of sampled statistics. |
| `-matrix` | `false` | Compute and display the transition matrix for analysed samples. |
| `-graph` | `false` | Shannon-entropy history of analysed samples. |
| `-preview` | `false` | Up to 64 bytes, in hex, from the last selected frame. |
| `-histogram` | `false` | Byte-distribution bars from the analysed sample's existing counts. |
| `-debug` | `false` | Receive/fetch, analysis and runtime timings, to three decimal places in ms. |

By default, the client receives and counts every frame but analyses **at most the first 1 MiB of a selected frame, once every 250 ms**. The first frame is analysed immediately; later samples require both a new frame and an elapsed analysis interval. Slow sources therefore produce fewer samples. The terminal redraws once per second using the latest available result.

These settings apply to both `pull` and `stream`. They do not reduce the requested payload or filter data written with `-writeout`. Matrix computation is skipped unless `-matrix` is enabled. Statistics describe individual analysed samples; graphs show their history rather than a cumulative test of all received bytes.

For example, request 16 MiB every 50 ms while keeping analysis and display work bounded:

```sh
./bin/entropy-client -mode=stream -dashboard -slice=16777216 -refresh=50ms \
  -analysis-bytes=1048576 -analysis-interval=250ms -display-interval=1s
```

The three analysis/display arguments above are the defaults and may be omitted. The requested payload rate is **320 MiB/s** (`16 MiB / 0.05 s`), a scheduling target rather than a measured capacity. Actual throughput depends on generation, reception, analysis, terminal I/O and the network.

To analyse **every byte of every frame**, set both analysis options to zero:

```sh
./bin/entropy-client -mode=stream -dashboard -slice=16777216 -refresh=50ms \
  -analysis-bytes=0 -analysis-interval=0 -display-interval=1s
```

Add `-matrix` if transition-matrix analysis is also wanted. Setting only `-analysis-bytes=0` still selects frames at the configured interval; setting only `-analysis-interval=0` still respects the prefix cap. Full analysis can reduce throughput. The bounded receive queue applies backpressure when processing cannot keep up; increasing display frequency does not accelerate reception.

### Reading the monitor

| Display | Meaning |
| --- | --- |
| `Requested rate` | Payload size divided by requested interval; a target, in MiB/s. |
| `Received` / `Average received` | All frames handled by the monitor and their cumulative throughput over monitoring runtime. |
| `Total received` / `Transfer Rate` | Receiver byte count and throughput sampled over approximately one-second windows. |
| `Analysed Rate` | Bytes actually analysed per second, including the prefix/frame selection limits. |
| `Analysis samples` / `Analysed prefix` | Number of selected frames versus received frames, and analysed bytes versus the last selected frame's full size. |
| `Analysed total` / `Sample age` | Cumulative sampled bytes and age of the latest analysis sample. |

`Total received`, `Transfer Rate` and `Analysed Rate` are shown with `-dashboard`. Throughput uses **MiB/s**: 1 MiB = 1,048,576 bytes. Analysis rates measure processing of sampled data, not physical entropy production. Receiver and monitor counters can differ briefly because frames may be in flight or queued and rate windows differ.

The client performs startup diagnostics before normal monitoring; its reception-rate window is reset afterwards. With `-debug`, `Last WS receive` includes waiting for the server's next message; `WS runtime` sums message-read durations. `Last HTTP fetch` includes the complete response body; `HTTP cycle runtime` includes request pacing plus that fetch. Analysis is timed separately, and `Total runtime` is wall time since monitoring started. These are client-side timings, not pure network round-trip measurements.

[Back to README](../README.md) · [Technical reference index](README.md)
