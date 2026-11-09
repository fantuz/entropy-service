# Source-lab design and access model

The implemented `api-sources.html` and `api-socket.html` extend the stream and
file labs: feed different byte sources into the same histogram, byte trace,
PSD, Yin/Yang, bit rates and waterfall. The source page covers browser devices
and generators; the socket page covers network inputs and registered server-side
bridges. See [the source-lab guide](source-labs.md)
for the implemented controls, peer/self-link examples and operational limits.

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

## Implementation and extension points

The current implementation includes browser WebSocket/HTTP, Web Crypto,
deterministic controls, local files, camera/audio and Web Serial. Start/Stop
actions own resource acquisition and cleanup. Registered adapters cover server
peers, raw TCP/UDP/Unix sockets and readable Linux devices with explicit limits.

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
