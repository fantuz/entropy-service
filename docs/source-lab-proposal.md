# Proposal: a browser source lab

A page such as `api-sources.html` would be a useful extension of the stream and
file labs: feed different byte sources into the same histogram, byte trace,
PSD, Yin/Yang, bit rates and waterfall. `api-socket.html` would also work as a
name, but “sources” better describes cameras, audio and generated controls.
This is a proposal; this PR does not create the page or start new listeners.

## Sources and access

| Source | What a normal browser can do | Useful experiment |
| --- | --- | --- |
| Built-in `/stream` or another WebSocket | Receive binary messages; the source must accept the browser's Origin and use a compatible ws/wss connection. | Compare servers and registered generators. |
| HTTP byte endpoint | Fetch or stream a response, subject to CORS, scheme and browser network restrictions. | Compare transport/frame sizes without changing the source bytes. |
| Selected file | Read bounded local slices after explicit file selection. | Replay the same material and compare windows reproducibly. |
| Browser Web Crypto | Generate local comparison samples; `getRandomValues` accepts at most 65,536 bytes per call. | A useful CSPRNG baseline next to remote output. |
| Counter, constant or repeating pattern | Generate known deterministic test material locally. | Demonstrate why a flat histogram or high Shannon estimate alone is insufficient. |
| Camera or microphone | Request access through `getUserMedia`, with permission in a secure context such as HTTPS or localhost. | Inspect RGB/RGBA pixels, PCM samples, frame differences and selected bit positions. |
| TCP/UDP, Unix socket, `/dev/hwrng`, `/dev/video0` | Ordinary web pages cannot open arbitrary raw sockets or Linux device paths. Use a configured local/server adapter. | Compare a lab sensor or device with the same analysis controls. |

Web Serial can cover some attached hardware in browsers that support it, with
an explicit device chooser and secure context. It is not a portable substitute
for arbitrary Linux device access. Browser devices belong to the viewer's computer; devices on the server need an
adapter. Camera APIs expose processed frames rather than guaranteed raw sensor
samples; audio may be resampled or processed too.

## Small first implementation

Start with browser-local sources: registered WebSocket/HTTP endpoints, Web
Crypto, deterministic controls and the existing file adapter. Camera/audio can
follow behind explicit Start/Stop actions. This provides interesting comparisons
without needing to expose the server's filesystem or sockets.

The analysis workspace now has a common byte-input interface in
`web/analysis-workspace.js`. Source adapters can supply bounded buffers plus
metadata: source ID, byte range, sample number and capture time. Keep one
running analysis job and one replaceable pending job; discard obsolete results
when a source changes. Reuse the static panel template and CSS rather than
forking a third plotting implementation.

Show source identity and encoding beside the plots: for example, RGBA row
order/channel layout or PCM word size, channel count and sample rate. The
existing spectrum is **cycles per byte**, even for camera/audio input. A later
Hz/audio mode would need to decode sample words/channels and use the actual
sample clock. Label transformations such as frame differencing or LSB
extraction, and allow inspection of the original bytes as well.

For server-side devices, use administrator-defined source IDs with fixed paths
or destinations, bounded reads, timeouts, cancellation and resource budgets.
A loopback-only first prototype is appropriate. Avoid an endpoint that accepts
arbitrary server-side URLs, file paths or shell commands from a web form; it
would turn a lab viewer into a general network/filesystem access service.
Source selection should feed the **diagnostic viewer** and leave root DRBG
seeding unchanged.

These experiments measure properties of the sampled representation. Network
traffic, video and audio can be correlated or predictable; colour variation
and plug-in Shannon estimates do not establish cryptographic entropy. They are
still useful materials for exploring bias, periodicity, transformations and
how the different views complement one another.
