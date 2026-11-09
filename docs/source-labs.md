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
| Network camera | Decoded RGB8 prefix from a registered RTSP or ONVIF camera | Server-side FFmpeg; session-only username/password. ONVIF Media1 obtains the RTSP stream URI. |

Media/serial devices belong to the **computer running the browser**. Devices
on the entropy-server host use registered adapters instead. Camera and audio
Start actions request permission; Stop, a hidden tab or leaving the page stops
tracks, timers and readers. A permission request cannot always be dismissed by
script, but any stream granted after Stop is closed immediately. Microphone
monitoring uses a muted output path, without playing captured audio.

### Optional camera preview

The **Camera preview** card is after **Measurements & output**, before the
statistical plots. **Show preview** defaults to off. Local-camera preview can
be toggled while sampling; network-camera changes apply on the next Start.
Stop releases capture and clears the displayed preview. Turning preview off
hides the image and avoids display work; sampling still reads camera pixels.
Network preview also sends full decoded frames instead of only the requested
prefix. Sampling meters count the requested prefix in either case, while server
serving counters count the actual forwarded frames. Disabling preview saves
network/canvas work but does not remove decoding needed to produce sample bytes.

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

The **Analysis window & byte offset** cap/offset and 1/2/5/10-fps display limit bound analysis separately.
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
| `webpage` | Full HTTP(S) URL, optional additional `urls` | Reads a bounded body prefix; follows at most two redirects on the same origin. A pool chooses one configured URL per sample. |
| `rtsp` | Full `rtsp://` camera URL, without credentials | FFmpeg over TCP; decodes/resizes to RGB8. Username/password arrive in the initial WS message. |
| `onvif` | Explicit HTTP(S) Device service URL | ONVIF Media1 capabilities/profiles/stream URI lookup, followed by RTSP decoding. Optional `profile_token` selects a profile. |
| `tcp` | `host:port` | Connects and reads raw stream bytes, with partial final EOF support. |
| `udp` | Local `host:port` | Binds a receiver; another process sends datagrams there. Preserves datagram boundaries and rejects oversize packets without silently truncating them. One active receiver per address. |
| `unix` | Absolute Unix stream socket path | Connects to a byte-producing helper. |
| `device` | Absolute readable Linux device/file/FIFO path | Read-only, nonblocking I/O with polling/deadlines. A device must support byte reads; `/dev/video0` can require external driver setup. Browser camera capture or a Unix-socket helper is more portable. |

Each entry has `id`, optional display `name`, `kind`, `endpoint`, optional
`max_bytes` (1–1,048,576, default 1 MiB) and `timeout_ms` (100–60,000, default
5000). IDs start with a lowercase letter and contain up to 32 lowercase letters,
digits, hyphens or underscores. Names are at most 80 bytes. The strict JSON file
is at most 64 KiB with at most 32 entries. URL userinfo/fragments are rejected;
credentials should not be put into browser-visible URLs. HTTP(S) TLS certificates are
verified normally. Unsupported configuration fails startup before seed-device
access. Direct device readers require Linux; no shell commands are executed.

`GET /lab/sources` exposes names/kinds/limits and whether the requesting client
can use bridges, not endpoints. `WS /lab/stream?source=ID&bytes=N&refresh=MS`
reads a registered source. Camera registrations additionally accept
`preview=0|1` (default off) and require an initial text frame containing only
`{"username":"...","password":"..."}` within ten seconds, bounded to 8 KiB;
empty strings permit anonymous cameras. Credentials are limited to 256/1024
UTF-8 bytes. Unknown/duplicate/invalid parameters are rejected
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

## Assisted registration

Both pages have a **Register a source…** menu item. Choose a kind, ID, endpoint,
sample limit and timeout, then **Add registration**. Camera fields provide
decoded size and an optional ONVIF profile token; web-page fields accept a pool
of additional URLs. Import an existing JSON registry before adding entries to
preserve those registrations. Download or copy the completed JSON, install it
on the server host and restart with `-lab-sources=lab-sources.json`.

The configurator prepares a file locally; it does not activate sources, write
server files, or retrieve private endpoint paths from the catalog. Imported
registries are limited to 64 KiB/32 entries and duplicate IDs are rejected.
Camera passwords do not belong in this file and are rejected in URL userinfo.

## HTTP bodies and random web pages

**HTTP body prefix** samples response-body bytes, including HTML, text, JSON
and binary data; it does not render a remote web page. In **Browser · needs
CORS** mode, the target must allow the lab's browser origin. Ordinary sites
often do not. Browser redirects and HTTPS-to-HTTP mixed content are rejected;
CORS/network failures now explain the registered-server alternative. Page
presets fill the URL without automatically fetching it.

For pages without CORS, create a `webpage` registration, then select **HTTP body
prefix → Registered server source** and its ID. This fetches from the server,
using its DNS/network/proxy access. It does not bypass login, access controls,
TLS checks or site-specific restrictions. HTTP failures include the status
code without exposing endpoint details. The text preview displays the first
2,048 sampled bytes as UTF-8 via `textContent`; HTML/scripts are never executed.

Unlike strict binary `http` registrations, `webpage` stops at the requested byte
prefix even when the page is larger. It follows up to two redirects that retain
the exact original scheme/host/port. Cross-origin redirects are rejected; use
the final URL instead. There is no cookie jar or browser-cookie forwarding.
Only operator-registered URLs are fetched server-side.

A `webpage` source may contain up to 16 additional `urls`. Each read chooses
uniformly among the primary `endpoint` and those entries, using Go's crypto RNG.
The example file pairs Example.org with RFC 4086; a configured Wikipedia
`https://en.wikipedia.org/wiki/Special:Random` endpoint can also follow its
same-origin page redirect. Site access and content can change. Web content is
structured comparison material, not evidence of unpredictable entropy. A pool's
measurement records describe the ensemble/source ID; private upstream URLs are
not exposed in the catalog or exported per-frame metadata.

## RTSP and ONVIF cameras

Install **FFmpeg** on the entropy-server host and make `ffmpeg` available in the
service's `PATH`. It is an optional runtime dependency, not included in the Go
binary. Select **Network camera · RTSP / ONVIF**, choose a registered camera,
enter its username/password (or leave both empty for anonymous access), then
Start or Sample once. The password field clears immediately after Start and
must be re-entered for the next capture. A remote server needs HTTPS to accept
credentials from this page; localhost is usable for development.

ONVIF accepts an explicit Device service URL, e.g.
`http://camera/onvif/device_service`. It uses SOAP 1.2/WS-Security UsernameToken
PasswordDigest for GetCapabilities, Media1 GetProfiles and GetStreamUri. The
first profile is selected unless `profile_token` is set. Device/client clocks
must agree for UsernameToken authentication. Returned Media services must stay
on the configured origin, and the RTSP URI on the configured camera hostname.
There is no multicast discovery, PTZ control or ONVIF certification claim.
Media2-only/vendor authentication differences may require using the camera's
RTSP URL directly.

Decode sizes are 160×120, 320×240 (default) and 640×480; `max_bytes`, if supplied,
must equal width×height×3. Samples are prefixes of row-major RGB8 frames. Preview
requires a complete frame to fit the server byte limit. The decoder samples at
the selected cadence, using fixed arguments, bounded threads and no audio. Pick
a camera substream/profile to reduce the underlying decode cost; resizing the
output alone does not reduce the camera's source resolution.

There are at most two concurrent decoders per server registry. Each reserves a
16 MiB working-memory estimate plus frame buffers against the lab budget;
the default 32 MiB budget normally admits one camera. FFmpeg/TLS/kernel/codec
allocations are outside a hard RSS guarantee. Per-frame timeouts, Stop and
server shutdown cancel/reap the decoder. Decoder errors are returned without
URLs/credentials. Passwords reach FFmpeg through a private stdin pipe, never
argv, a temporary config file, exported records or retained decoder logs.

The current decoder path accepts **RTSP over TCP** on a trusted camera network.
RTSP is unencrypted; RTSPS is rejected because this FFmpeg path does not provide
the certificate-verification control needed here. Use a verified external relay
for cameras requiring RTSPS. HTTPS ONVIF service requests retain normal TLS
verification. Camera frames remain processed video, not raw quantum entropy.

## Verification

```sh
node scripts/test-lab-samples.mjs
node scripts/test-lab-registration.mjs
node scripts/render-analysis-pages.mjs --check
go test -race ./cmd/entropy-server/internal/lab ./cmd/entropy-server
make build-server
python3 scripts/test_sources_browser.py --artifacts /tmp/entropy-source-lab
# Optional real RTSP decoder + synthetic camera/ONVIF/HTTP fixtures:
python3 scripts/test_lab_cameras.py --mediamtx /path/to/mediamtx --artifacts /tmp/entropy-camera-lab
```

The browser suite uses synthetic media and a mocked serial port, two temporary
entropy-server processes, and local TCP/UDP/Unix/device fixtures. It checks
metering, transformations, exact downloads, cross-page comparisons, media
release, late permission cancellation, peer/self connections, Origin handling
and desktop/mobile geometry. Physical sensors, device-driver setup and external
networks still need testing on their actual deployment.

Camera integration was exercised with FFmpeg 7.1.5, MediaMTX 1.21.3 and synthetic
H.264 video, including authenticated RTSP with URL-escaped password characters
and a SOAP UsernameToken fixture. Public HTTPS smoke checks also fetched
Example.org (577 bytes at test time) and a 1,024-byte prefix of RFC 4086 from
rfc-editor.org. These are deployment checks, not claims of physical-camera or
all-site compatibility.

[Source lab desktop](images/source-lab-desktop.png) ·
[Source lab mobile](images/source-lab-mobile.png) ·
[Socket lab desktop](images/socket-lab-desktop.png) ·
[Socket lab mobile](images/socket-lab-mobile.png)

[Assisted registration on mobile](images/source-registration-mobile.png)
