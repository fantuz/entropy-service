# Experimental source and socket labs

`/api-sources.html` generates, samples and compares browser-side byte sources.
`/api-socket.html` connects binary network sources and operator-registered
server-side adapters. Both reuse the stream/file workspace: histogram, trace,
PSD, Shannon watchpoint, Yin/Yang, bit-position rates and waterfall, with aligned
desktop/mobile layouts.

## Browser sources

| Source | Bytes shown | Access and interpretation |
| --- | --- | --- |
| Web Crypto | `crypto.getRandomValues` bytes, using calls of at most 65,536 bytes | Local browser CSPRNG baseline; up to 1 MiB per sample. |
| Counter | Continuing 0…255 sequence | Deterministic control: flat byte histogram and Shannon eight do not establish unpredictability. |
| Constant | User-selected value 0…255 | Tests bias, extreme bit proportions and histogram clipping. |
| Hex pattern | Repeating 1–512-byte pattern | Whitespace-separated complete hex byte pairs, e.g. `00 ff 3a`. |
| Local file | Successive local slices up to 1 MiB | No upload; maximum file size 32 MiB. Stops at EOF. Use the file lab for detailed seeking. |
| Camera | RGB8 or RGBA8 row-major prefix at 160×120, 320×240 or 640×480 | Explicit permission, HTTPS/localhost; browser-processed/resampled frames. RGBA includes alpha. Actual camera dimensions/rate appear in metadata. |
| Microphone | PCM16LE mono prefix from the latest audio window | Explicit permission, HTTPS/localhost and Web Audio. Actual sample rate is shown. Windows can overlap; browser/driver processing can apply. |
| Serial | Prefix of each Web Serial read at the chosen baud | Supported browsers only, secure context and explicit port chooser. The lab reads bytes and does not send device commands. |

Media/serial devices belong to the **computer running the browser**. Devices
on the entropy-server host use registered adapters instead. Camera and audio
Start actions request permission; Stop, a hidden tab or leaving the page stops
tracks, timers and readers. A permission request cannot always be dismissed by
script, but any stream granted after Stop is closed immediately. Microphone
monitoring uses a muted output path, without playing captured audio.

Audio conversion exposes byte prefixes of a mono PCM16LE representation, up to
65,536 bytes per sample from the Web Audio analysis window. It is not a raw ADC
interface or a calibrated audio analyser. The shared FFT remains **cycles per
byte**, including for media/serial sources; it is not silently relabelled Hz.

## Capture, meter, compare and generate

Choose sample size (1–1,048,576 bytes), cadence (20–60,000 ms), representation,
and **Start** or **Sample once**. Start repeats until Stop; Sample once collects
one represented sample and releases its source. XOR needs two equal-length
inputs before producing a sample.

- **Original bytes** preserves the sampled representation.
- **XOR previous sample** compares equal-length successive inputs; a size
  change starts a new warm-up. It can reveal changes between camera frames.
- **LSB → 0/255** expands each low bit into a whole black/white byte. Its
  byte-alphabet entropy is at most one bit per byte, not eight.

Representation changes are labelled. They do not alter the server DRBG or
supply reseed material. A source's uniformity, media variation or plug-in
Shannon estimate does not establish cryptographic entropy.

**Input samples** counts the sampled bytes delivered by the adapter. It does
not count TCP/TLS/HTTP/WS overhead, discarded HTTP-body suffixes, unsampled
camera pixels or all continuously captured audio. **Sampled MB/s** is decimal
MB (1,000,000 bytes) divided by elapsed active session time after readiness;
it is unavailable for fewer than two inputs. Mean interval is the average
arrival spacing between inputs. Cadence and the source's own clock affect
these numbers; they are application sampling rates, not maximum link or RNG
throughput benchmarks.

Cadence controls local generators, HTTP/file/media polling, entropy-server
links and registered bridges. Arbitrary WebSocket/serial sources arrive on
their own clock. Only one HTTP request or file read runs at a time. Raw
WebSocket messages must be nonempty binary samples up to 1 MiB; larger/text
messages are rejected. Direct WebSocket messages are analysed whole; sample size
sets request/read sizes for the other adapters. Native browser network/media
buffers are outside the bounded analysis window, which is not a total-RSS guarantee.

The shared offset/cap and 1/2/5/10-fps display limit bound analysis separately.
Pause freezes plots while input meters continue. Stop releases the source and
freezes the current diagnostic view. Hidden tabs stop the source. A completed
one-shot or EOF drains the final selected sample before freezing. Averages and
history are cleared when starting a new source or changing selection.

**Record measurement** keeps up to 50 summaries: source/encoding, selected
representation and byte range, settings, Shannon/mean/deviation/bit counts,
sampled rate, intervals and frozen state. Records survive navigation between
the two labs within the same browser tab using `sessionStorage` where available.
Storage restrictions fall back to in-memory records. **Export measurements**
saves `entropy-lab-measurements-v1` JSON; it contains summaries, not sample bytes.
**Download selected bytes** saves the exact latest selected representation and
window as a binary file. Export/download are explicit local browser actions.

## Network connections and connecting instances

The socket page supports:

- **This instance · WebSocket/HTTP**: read this origin's `/stream` or
  `/v1/data/random`, using the chosen bytes/cadence.
- **Entropy-server peer · WebSocket/HTTP**: enter the peer's base HTTP(S) URL.
  Direct browser connections follow its CORS/Origin policy and mixed-content
  rules. The default entropy-server WS policy rejects other browser origins;
  a registered server-side peer link avoids weakening that policy.
- **Binary WebSocket URL / HTTP body prefix**: inspect an explicit endpoint.
  HTTP credentials are omitted and redirects rejected; reads stop at the
  requested prefix. WebSocket sources must supply binary messages.
- **Registered server source**: choose an operator-provided source ID. The
  receiving entropy-server opens the pinned upstream; the browser connects to
  its own origin. Private endpoint/device paths are not returned in the catalog.

For **A reading B**, start B on port 8081 and register its base URL on A:

```sh
# B: a separate terminal/process
./bin/entropy-server -device=/dev/urandom -require-device=true \
  -enable-https=false -http=127.0.0.1:8081

# A's lab.json:
# {"sources":[{"id":"peer","kind":"entropy-ws", "endpoint":"http://127.0.0.1:8081"}]}
./bin/entropy-server -device=/dev/urandom -require-device=true \
  -enable-https=false -http=127.0.0.1:8080 -lab-sources=lab.json
```

Open A's `/api-socket.html`, choose **Registered server source → peer**, then
Start or Sample once. Use `entropy-http` for an HTTP peer. Point the registered
base URL at A's own `127.0.0.1:8080` for a real server-side self-connection.
Direct “This instance” modes are useful without any registry configuration.

Peer/self links consume generated output for diagnostics and comparison. A
self-bridge has both an upstream serving connection and a downstream forwarding
connection, so lifetime serving counters can count both transfers. This is not
fresh entropy amplification. Relay endpoints under `/lab/` are excluded from
registrations, preventing accidental relay-to-relay cycles.

## Registered adapters and operator controls

Copy and edit [the complete example](../resources/lab-sources.example.json).
Only the configured sources are opened, and only on a client Start action.

| Kind | Endpoint | Behaviour |
| --- | --- | --- |
| `entropy-ws` | Base HTTP(S) URL | Converts scheme to WS(S), uses `/stream`, adds selected `bytes`/`refresh`. |
| `entropy-http` | Base HTTP(S) URL | Uses `/v1/data/random` with selected `bytes`/`refresh`. |
| `websocket` | Full WS(S) URL | Binary messages with read limit/deadline; no redirects or text-frame decoding. |
| `http` | Full HTTP(S) URL | GET body must fit the requested byte limit; rejects redirects/status errors and empty bodies. |
| `tcp` | `host:port` | Connects and reads raw stream bytes, with partial final EOF support. |
| `udp` | Local `host:port` | Binds a receiver; another process sends datagrams there. Preserves datagram boundaries and rejects oversize packets without silently truncating them. One active receiver per address. |
| `unix` | Absolute Unix stream socket path | Connects to a byte-producing helper. |
| `device` | Absolute readable Linux device/file/FIFO path | Read-only, nonblocking I/O with polling/deadlines. A device must support byte reads; `/dev/video0` can require external driver setup. Browser camera capture or a Unix-socket helper is more portable. |

Each entry has `id`, optional display `name`, `kind`, `endpoint`, optional
`max_bytes` (1–1,048,576, default 1 MiB) and `timeout_ms` (100–60,000, default
5000). IDs start with a lowercase letter and contain up to 32 lowercase letters,
digits, hyphens or underscores. Names are at most 80 bytes. The strict JSON file
is at most 64 KiB with at most 32 entries. URL userinfo/fragments are rejected;
credentials should not be put into browser-visible URLs. TLS certificates are
verified normally. Unsupported configuration fails startup before seed-device
access. Direct device readers require Linux; no shell commands are executed.

`GET /lab/sources` exposes names/kinds/limits and whether the requesting client
can use bridges, not endpoints. `WS /lab/stream?source=ID&bytes=N&refresh=MS`
reads a registered source. Unknown/duplicate/invalid parameters are rejected
before opening a source. Existing `-max-bytes` also caps bridge samples, up to
1 MiB. An oversized source message or datagram terminates the bridge rather
than disguising truncation.

Bridges have a separate `-lab-max-connections=8` and `-lab-memory-mib=32`
admission budget. These are working-memory estimates, not RSS limits. The
server joins bridged handlers and cancels blocked upstream I/O on shutdown.
All completed downstream payloads contribute to the existing traffic summary;
forwarded bytes are not counted as locally DRBG-generated bytes.

By default bridges accept **loopback TCP clients only**. `-lab-allow-remote`
explicitly permits remote clients; Origin validation is not authentication.
A reverse proxy connecting over loopback also counts as a loopback client, so
protect these routes at the proxy boundary when exposing them. Keep device or
private-network registries inside a trusted lab/access-controlled deployment.
No arbitrary URL/path form is forwarded to server-side dial/open functions.

## Verification

```sh
node scripts/test-lab-samples.mjs
node scripts/render-analysis-pages.mjs --check
go test -race ./cmd/entropy-server/internal/lab ./cmd/entropy-server
make build-server
python3 scripts/test_sources_browser.py --artifacts /tmp/entropy-source-lab
```

The browser suite uses synthetic media and a mocked serial port, two temporary
entropy-server processes, and local TCP/UDP/Unix/device fixtures. It checks
metering, transformations, exact downloads, cross-page comparisons, media
release, late permission cancellation, peer/self connections, Origin handling
and desktop/mobile geometry. Physical sensors, device-driver setup and external
networks still need testing on their actual deployment.

[Source lab desktop](images/source-lab-desktop.png) ·
[Source lab mobile](images/source-lab-mobile.png) ·
[Socket lab desktop](images/socket-lab-desktop.png) ·
[Socket lab mobile](images/socket-lab-mobile.png)
