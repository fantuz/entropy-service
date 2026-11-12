# Experimental source and socket labs

`/api-sources.html` generates, samples and compares browser-side byte sources.
`/api-socket.html` connects binary network sources and operator-registered
server-side adapters. Both reuse the stream/file workspace: histogram, trace,
PSD, Shannon watchpoint, Yin/Yang, bit-position rates and waterfall, with aligned
desktop/mobile layouts.

## Browser sources

| Source | Bytes shown | Access and interpretation |
| --- | --- | --- |
| Web Crypto | `crypto.getRandomValues` bytes, using calls of at most 65,536 bytes | Local browser CSPRNG baseline; up to 8 MiB per sample, capped by reported server limits. |
| Counter | Continuing 0…255 sequence | Deterministic control: flat byte histogram and Shannon eight do not establish unpredictability. |
| Constant | User-selected value 0…255 | Tests bias, extreme bit proportions and histogram clipping. |
| Hex pattern | Repeating 1–512-byte pattern | Whitespace-separated complete hex byte pairs, e.g. `00 ff 3a`. |
| Local file | Successive local slices up to 8 MiB | No upload; maximum file size 32 MiB. Stops at EOF. Use the file lab for detailed seeking. |
| Camera | RGB8 or RGBA8 row-major prefix at 160×120, 320×240 or 640×480 | Explicit permission, HTTPS/localhost; browser-processed/resampled frames. RGBA includes alpha. Actual camera dimensions/rate appear in metadata. |
| Microphone; Line-in / external audio | PCM16LE prefix from an explicitly selected channel or mono mix | Permission, HTTPS/localhost and Web Audio. Input, channel, sample rate and signal level are shown. Windows can overlap; browser/driver processing can apply. |
| Serial | Prefix of each Web Serial read at the chosen baud | Supported browsers only, secure context and explicit port chooser. The lab reads bytes and does not send device commands. |
| Network camera | Decoded RGB8 prefix from a registered RTSP or ONVIF camera | Server-side FFmpeg; session-only username/password. ONVIF Media1 obtains the RTSP stream URI. |

Media/serial devices belong to the **computer running the browser**. Devices
on the entropy-server host use registered adapters instead. Camera and audio
Start actions request permission; Stop, a hidden tab or leaving the page stops
tracks, timers and readers. A permission request cannot always be dismissed by
script, but any stream granted after Stop is closed immediately. Microphone
monitoring uses a muted output path, without playing captured audio. Upstream
WebSocket connection setup is canceled on Stop, browser disconnect or server
shutdown, including a peer that accepts TCP/TLS but never completes its HTTP
upgrade; established streams retain their configured deadlines and size limits.

### Line-in and USB audio inputs

Line-in uses the same browser `getUserMedia` and Web Audio support as a
microphone; **no browser plugin is required**:

1. Select **Microphone PCM** and **Start** or **Sample once** to grant the
   site's microphone permission. Browsers use this same permission for line-in.
   Input names often become available only after permission is granted.
2. Select **Line-in / external audio PCM** and choose a device under **Audio
   input**. This mode requires an explicit device; it never silently uses the
   default microphone. **Refresh inputs** lists devices without opening capture.
   An unavailable selection is retained and reported instead of substituted.
3. Select **First / left**, **Second / right**, or **Mono mix**, then Start.
   First / left is the default: stereo channels with opposite polarity cancel
   when mixed. A known mono device rejects Second / right with guidance.

Microphone mode permits **System default**, deliberately following the operating
system's selected input. Browsers cannot reliably classify physical microphones
versus line-in ports: these two choices express capture intent, while the device
picker and OS settings determine the actual source. A physical jack may share
one capture device with a microphone. On Linux, use sound settings or
PipeWire/PulseAudio controls (`pavucontrol`) to select **Line In** as the input
port/profile, then choose that device in the lab. USB interfaces may appear
separately. On Windows, check **Settings → System → Sound → Input**, input volume,
mute and **Privacy & security → Microphone** permissions, including desktop-app
access. In Firefox, allow microphone permission for this site and refresh inputs.
The browser and sound driver determine which devices and channels are exposed.

Echo cancellation, noise suppression and automatic gain are requested off;
browser/driver processing and resampling can still apply. The selected channel
is converted to mono PCM16LE. A complete analyser window is allowed to accumulate
before the first sample, including **Sample once**. Muted tracks and suspended
audio engines display a waiting explanation. Peak and RMS levels in dBFS help
distinguish an active signal from silence or input below PCM16 resolution. Silent
samples remain zero bytes; the lab never adds noise or fabricates entropy. If
silence persists, check the selected input, cable, channel, mute and input level.

Input is routed through a muted output and is not played through the speakers.
Captured samples stay in the browser until an explicit local download/export.
Device IDs remain in page memory; measurement exports can include the input's
display name and channel in the encoding description. Audio is a diagnostic
input, not a server entropy seed. Stop, navigation, a hidden tab and a
disconnected track release capture; a stream granted after Stop is closed.

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

Choose sample size (1–8,388,608 bytes), cadence (20–60,000 ms), representation,
and **Start** or **Sample once**. Start repeats until Stop; Sample once collects
one represented sample and releases its source. XOR needs two equal-length
inputs before producing a sample.

The browser cap is **8 MiB**, also capped by the server's reported `-max-bytes`
and a registered source's effective limit. The initial sample remains 65,536
bytes. Registered server sources accept `max_bytes` up to the server's absolute
32 MiB ceiling; actual bridge requests use the smaller of that source limit
and the configured `-max-bytes`. Existing lab connection/memory admission
budgets still apply. The catalog advertises the effective per-source ceiling.
When `max_bytes` is omitted for a non-camera registration, it now defaults to
32 MiB, subject to those runtime limits; existing explicit smaller limits stay
effective.

The analysis window remains separately capped at **1 MiB** (default 64 KiB),
with its byte-offset control for exploring a larger sample. Increasing capture
size therefore does not automatically increase FFT/statistics work. Large
captures still cost reception/copy/representation memory and time. Camera,
audio and serial sources may return fewer bytes than requested; audio windows
remain at most 65,536 bytes under the browser analyser's platform limit.

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
WebSocket messages must be nonempty binary samples within the effective browser
limit (at most 8 MiB); larger/text
messages are rejected. The selected analysis window is taken from each accepted message; sample size
sets request/read sizes for the other adapters. Native browser network/media
buffers are outside the bounded analysis window, which is not a total-RSS guarantee.

The **Analysis window & byte offset** cap/offset and 1/2/5/10/20-fps display limit bound analysis separately.
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
Each new record includes `capturedAt` (when the browser received the represented
sample), `recordedAt` (when **Record measurement** was clicked) and `sampleAgeMs`
using the browser's monotonic clock. The legacy `timestamp` remains an alias of
`recordedAt`. `meter.scope` is `source-session` and `meter.measuredAt` matches
`recordedAt`: the rate/byte/frame totals include reception while plots are paused,
whereas `window.frame` and `statistics` describe the frozen sample. This makes a
paused comparison explicit without attributing newer bytes to older statistics.
Older records from the same tab still load and show “capture time unavailable”.
The comparison table becomes labelled measurement cards on narrow screens.
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
  HTTP credentials are omitted; browser redirects follow normal CORS rules. Reads stop at the
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
| `webpage` | Full HTTP(S) URL, optional additional `urls` | Reads a bounded body prefix; follows at most two same-origin redirects or same-host default-port HTTP→HTTPS upgrades. A pool chooses one configured URL per sample. |
| `rtsp` | Full `rtsp://` camera URL, without credentials | FFmpeg over TCP; decodes/resizes to RGB8. Username/password arrive in the initial WS message. |
| `onvif` | Explicit HTTP(S) Device service URL | ONVIF Media1 capabilities/profiles/stream URI lookup, followed by RTSP decoding. Optional `profile_token` selects a profile. |
| `tcp` | `host:port` | Connects to a producer and returns available stream bytes up to the requested limit, without waiting to fill the sample. TCP has no message boundaries. |
| `udp` | Local `host:port` | Binds a receiver; another process sends datagrams there. Preserves datagram boundaries and rejects oversize packets without silently truncating them. One active receiver per address. |
| `unix` | Absolute Unix stream socket path | Connects to a byte-producing helper. |
| `device` | Absolute readable Linux device/file/FIFO path | Read-only, nonblocking I/O with polling/deadlines. A device must support byte reads; `/dev/video0` can require external driver setup. Browser camera capture or a Unix-socket helper is more portable. |

Each entry has `id`, optional display `name`, `kind`, `endpoint`, optional
`max_bytes` (1–33,554,432, default 32 MiB for non-camera sources) and `timeout_ms` (100–60,000, default
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
before opening a source. `-max-bytes` also caps bridge samples, up to
32 MiB; browser controls additionally cap their requests at 8 MiB. An oversized source message or datagram terminates the bridge rather
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

The draft list supports **Edit**, ID renaming and **Remove**. **Save registration**
updates the selected entry; **Cancel edit** keeps the existing entry.
**Clear form** discards an unfinished new entry without deleting draft sources.
Validation feedback appears beside the relevant field. The heading, **Close** button and
export actions stay visible while the form scrolls on desktop and mobile.

An **Unsaved changes** indicator covers both the registry and unfinished form
edits. The browser warns before leaving a page with unsaved changes, and replacing
or resetting an unsaved draft asks before discarding it. Closing the dialog keeps
the draft on the current page. Copying JSON or requesting its download marks that
registry version exported; unfinished form edits must still be saved or cancelled.
Verify a requested download completed before navigating away. The draft is held
only in page memory, with no automatic storage of endpoint URLs or profile tokens.
Browser navigation warnings depend on browser support and cannot guarantee
recovery after a tab/process crash.

Imports preserve valid settings and reject inconsistent fields instead of
silently rewriting them. Camera defaults match the server: 320×240 RGB, 230,400
bytes and a 10,000-ms timeout; other sources default to 32 MiB and 5,000 ms,
with capture additionally capped by the server's configured `-max-bytes`.
Explicit camera sizes require the matching RGB frame byte count. Names, endpoint
paths/URLs and ONVIF profile tokens use the server's UTF-8 **byte** limits, not
JavaScript character counts. A shared browser/Go fixture checks these defaults,
source-specific fields and limits in CI. The server remains the authority when
loading a registry. Resetting or editing a draft invalidates pending imports;
an older asynchronous read cannot replace newer work. A failed import keeps the
last valid draft, and the same file can be selected again after correction.

The builder displays a type-specific example, an explicit **Use example** button
and validation errors containing the expected schema. Camera credential guidance
appears only for RTSP/ONVIF. Common endpoint forms are:

| Registration kind | Example | Meaning |
| --- | --- | --- |
| `websocket` | `ws://127.0.0.1:8081/stream?bytes=4096&refresh=500` or `wss://host/path` | Complete binary WebSocket endpoint. Its messages must fit the capture's Sample bytes limit. |
| `entropy-ws` | `http://127.0.0.1:8081` or `https://host:8443` | HTTP(S) **base** of another entropy-server; `/stream`, bytes and refresh are added automatically. |
| `onvif` | `http://192.168.1.100/onvif/device_service` | Device service address, not the video feed. Media1 discovery obtains the RTSP URI. Actual path/port depends on the camera. |
| `rtsp` | `rtsp://192.168.1.100:554/Streaming/Channels/102` | Direct camera stream, without URL userinfo. |
| `tcp` | `127.0.0.1:9001` | Producer to connect to; no `tcp://` prefix. |
| `udp` | `127.0.0.1:9002` | Local listener to bind on entropy-server; no `udp://` prefix. IPv6 requires brackets, e.g. `[::1]:9002`. |

Generic WebSocket sources must push binary frames without a subscription or
initial application request. Text/JSON feeds and echo-only services need a
protocol adapter. If sampling entropy-server output, `entropy-ws` is easier:
it keeps the upstream frame length aligned with the capture controls.

## HTTP bodies and random web pages

**HTTP body prefix** samples response-body bytes, including HTML, text, JSON
and binary data; it does not render a remote web page. In **Browser · needs
CORS** mode, the target must allow the lab's browser origin. Ordinary sites
often do not. Browser redirects follow the browser's normal CORS policy; HTTPS-to-HTTP mixed content is rejected.
CORS/network failures now explain the registered-server alternative. Page
presets fill the URL without automatically fetching it.

For pages without CORS, create a `webpage` registration, then select **HTTP body
prefix → Registered server source** and its ID. This fetches from the server,
using its DNS/network/proxy access. It does not bypass login, access controls,
TLS checks or site-specific restrictions. HTTP failures include the status
code without exposing endpoint details. The text preview displays the first
2,048 sampled bytes as UTF-8 via `textContent`; HTML/scripts are never executed.

Unlike strict binary `http` registrations, `webpage` stops at the requested byte
prefix even when the page is larger. It follows up to two redirects on the same
origin, with HTTP→HTTPS upgrades allowed on the same hostname's default ports
(80→443). A later downgrade back to HTTP is rejected. Different hosts, other
port changes and longer chains are rejected; register the final URL instead.
There is no cookie jar or browser-cookie forwarding.
Only operator-registered URLs are fetched server-side.

A `webpage` source may contain up to 16 additional `urls`. Each read chooses
uniformly among the primary `endpoint` and those entries, using Go's crypto RNG.
The example file includes Example.org, RFC text and CORS-enabled examples; a configured Wikipedia
`https://en.wikipedia.org/wiki/Special:Random` endpoint can also follow its
same-origin page redirect. Site access and content can change. Web content is
structured comparison material, not evidence of unpredictable entropy. A pool's
measurement records describe the ensemble/source ID; private upstream URLs are
not exposed in the catalog or exported per-frame metadata.

### Examples and CORS without a registry

Without `-lab-sources`, browser HTTP fetch is still usable for targets that grant
CORS permission. A CORS failure does not imply an incorrect URL or a broken RNG:
it means the browser cannot expose that response to this page. Permission must
come from the target site, usually through `Access-Control-Allow-Origin`.
Enabling CORS on entropy-server cannot grant permission for another site.

The selector and registry builder provide seven presets. Selecting a preset
fills the address; Start performs the request. External availability and site
policies can change.

| Preset | URL | Suggested route |
| --- | --- | --- |
| Example.org HTML | `https://example.org/` | Registered `webpage` when browser CORS is denied. |
| RFC 4086, randomness | `https://www.rfc-editor.org/rfc/rfc4086.txt` | Browser mode currently works; registered `webpage` is also available. |
| Random Wikipedia article | `https://en.wikipedia.org/wiki/Special:Random` | Registered `webpage`; includes a redirect and may have site-specific restrictions. |
| 4,096 binary bytes | `https://httpbin.org/bytes/4096` | Browser mode usually works; public CORS example. |
| Structured JSON | `https://httpbin.org/json` | Browser mode usually works; public CORS example. |
| Project README text | `https://raw.githubusercontent.com/fantuz/entropy-service/main/README.md` | Browser mode usually works. |
| RFC 1149, avian carriers | `https://www.rfc-editor.org/rfc/rfc1149.txt` | Browser mode currently works. |

For ordinary pages that block CORS: prepare a `webpage` registration, download
the JSON, put it on the **server host**, restart the existing server command
with `-lab-sources=lab-sources.json`, then choose **HTTP body prefix → Registered
server source**. Adding to the browser draft does not activate a registration.
The catalog/route help now states explicitly when no matching sources are active.

HTTP **201 Created** is a successful response, not a redirect. Its body is
sampled normally; a `Location` header identifies the created resource and is not
automatically followed. Actual redirect statuses are 301, 302, 303, 307 and 308.
Transport feedback now displays the response status and whether redirects were
followed, separately from the statistical sample. A successful response with an
empty body has no bytes to analyse. Browser redirect following remains subject
to CORS and mixed-content restrictions; registered `webpage` redirects obey the
host/port/upgrade policy above. Strict `http` sources continue to reject redirects.

### TCP/UDP demonstrations and waiting for data

Registered socket addresses belong to the entropy-server host/network, not the
computer running the browser. `127.0.0.1` refers to the server itself.

- **TCP** connects to a listening producer and reads available bytes up to the
  chosen limit. It no longer waits to fill the entire requested sample. It sends
  no HTTP request, TLS handshake, echo payload or subscription command; services
  that wait for one will remain idle until the source timeout.
- **UDP** binds a local address and waits for another process to send nonempty
  datagrams there. A remote sensor's IP is not a local bind address. Both sides
  must use matching IPv4/IPv6 addresses, and each datagram must fit Sample bytes.
  One active listener can bind a given address/port. Allow inbound UDP if using a
  remote producer; start with loopback for a local demonstration.

The page acknowledges a successful UDP bind or TCP connection, so waiting for
data can be distinguished from connection/bind failures. Bounds, timeouts and
registration limits still apply. Use Auto FFT for short samples; a TCP read does
not promise an application message or the full selected FFT length.

To reproduce locally, use [the two-source demo registry](../resources/lab-socket-demo.json).
On the server host, start this deterministic TCP producer in another terminal:

```sh
python3 scripts/lab_socket_fixture.py tcp --port 9001 --bytes 512 --interval 0.25
```

For an OS-seeded local demo, start the server with:

```sh
./bin/entropy-server -device=/dev/urandom -require-device=true \
  -enable-https=false -http=127.0.0.1:8080 \
  -lab-sources=resources/lab-socket-demo.json
```

Open `/api-socket.html`, select **Registered server source → demo-tcp**, and Start.
Even with Sample bytes at 65,536, the 512-byte producer should advance the plots.
For UDP, select **demo-udp**, Start, wait for **UDP listener bound**, then run:

```sh
python3 scripts/lab_socket_fixture.py udp --host 127.0.0.1 --port 9002 --bytes 512 --interval 0.25
```

The demo's UDP timeout is 60 seconds; if it expires before the sender starts,
press Start again. The helper generates a repeating counter as a predictable
control, not random entropy; use Ctrl-C to stop it. The TCP helper serves one
client at a time.

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

The ONVIF UsernameToken wire format mandates SHA-1 over nonce, timestamp and
password. It is an interoperable challenge-response digest, not a password
database or DRBG construction; substituting a costly password-storage hash
would break camera interoperability. CodeQL's password-hashing rule flags this
protocol use and its test verification, which need maintainer security review.
Prefer HTTPS ONVIF services where supported and keep camera traffic within a
trusted network.

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
# Install test-only Python dependencies first: pip install playwright defusedxml
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

[Assisted registration on mobile](images/source-registration-mobile.png) ·
[Measurement cards on mobile](images/source-measurements-mobile.png)
