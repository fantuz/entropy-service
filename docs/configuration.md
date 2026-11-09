# Configuration — server and client

Run either binary with `-help` for its complete flag list. Boolean values use `-flag=true` or `-flag=false`.

## Server

All server timing flags accept **plain integer milliseconds**, without a unit suffix: `-reseed-ms=15000`, not `15s`. Sizes below are bytes unless explicitly labelled MiB.

### Source and reseeding

| Flag | Default | Purpose |
| --- | --- | --- |
| `-device` | `/dev/qrandom0` | Source device; OS fallback is allowed unless required. |
| `-require-device` | `false` | Fail startup if the requested device is unavailable. |
| `-drbg` | `aes-256-ctr` on non-ARM/non-RISC-V; `chacha20` on ARM/RISC-V | Backend: `chacha20` or `aes-256-ctr`. An explicit flag always overrides the platform default. |
| `-cpu-mix` | `false` | Supplement seed reads with RDSEED/RDRAND on supported amd64 CPUs. |
| `-rdrand-mix` | `false` | RDRAND-only mode; requires CPU support. |
| `-reseed-ms` | `15000` | Root reseed interval: 1–60000 ms. |
| `-buffer-reseed` | `256` | Bytes per root reseed: 32–1048576. |
| `-max-reseed-age` | `0` (effective 60000 ms) | Readiness age limit in ms; zero selects `max(1000, 4 × reseed-ms)`. |

The architecture-based backend policy uses the binary's Go target architecture (`GOARCH`): `arm`, `arm64` and `riscv64` select ChaCha20; other builds select AES-256-CTR. This does **not** inspect AES acceleration at runtime: some non-ARM platforms without hardware AES may perform worse or have different side-channel characteristics. Operators may override with `-drbg=chacha20` or `-drbg=aes-256-ctr` after evaluating their CPU. Neither algorithm is claimed to be a NIST-certified DRBG. The 15-second reseed interval is a **service policy**, not a proven post-compromise recovery bound. The 60-second readiness-age threshold is monitoring only; an unhealthy `/ready` does not automatically stop output.

### Listeners and TLS

| Flag | Default | Purpose |
| --- | --- | --- |
| `-http` | `:8080` | HTTP address; binds all interfaces by default. |
| `-https` | `:8443` | HTTPS address; binds all interfaces by default. |
| `-enable-https` | `true` | Enable HTTPS; the packaged service explicitly disables it. |
| `-cert-file` | `cert.pem` | TLS certificate path. |
| `-key-file` | `key.pem` | TLS private key path. |
| `-debug` | `false` | Debug output and loopback pprof on port 6060. |

An explicit `-tags=experimental_barzakh` build additionally accepts
`-drbg=barzakh-521-experimental` for local research. The variable-time Go
implementation is excluded from normal releases and unsuitable for secrets.
See [construction, provenance and patent limitations](experimental-barzakh.md).

### Output and resource budgets

| Flag | Default | Purpose |
| --- | --- | --- |
| `-max-bytes` | `33554432` | Maximum binary sample: 1 byte to 32 MiB. |
| `-words` | `20` | Default word count and per-request word limit. |
| `-refresh-ms` | `5000` | Default `/bytes` WS interval in ms. |
| `-refresh-colors-ms` | `1500` | Default `/stream`, `/colors` and `/words` WS interval in ms. |
| `-refresh` | `5000` | HTTP word-page interval in ms; HTML rounds up to seconds. |
| `-fingerprint` | `2097152` | Upload prefix analysed statistically; full-upload SHA-256 is unaffected. |
| `-cookie` | `false` | Remember byte/word preferences in Secure, HttpOnly cookies; use HTTPS for browser persistence. |
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
`crypto/rand.Reader` is used. `X-RNG-Source` and `/ready`
report the **actual selected reader**, not the originally requested path.
Optional CPU mixing supplements seed bytes but does not replace this
reported base source. This is **startup-only** fallback: an error from the
already opened source during a later reseed does not switch readers.
Source selection establishes neither entropy quality nor physical provenance.

`-cpu-mix` prefers RDSEED, falls back to RDRAND, and takes precedence over `-rdrand-mix`. Mixing applies to **startup and reseed input**, not every output byte. Hardware attempts are bounded; if CPU randomness is unavailable, `-cpu-mix` retains the configured source bytes and logs the first failure. It does not turn the CPU instructions into a replacement for the configured source.

The systemd package overrides standalone defaults: it is **not enabled or started** by installation, uses an unprivileged systemd identity, binds only to 127.0.0.1:8080, and requires /dev/urandom. Switching to a hardware QRNG requires explicit Unix/group and systemd device permissions. See [packaged deployment](packaged-service.md).

### Preference cookies

Cookies remain **opt-in** with `-cookie=true`. They remember byte and word
counts for `/v1/data/random`, `/v1/meta/random` and `/paroleparoleparole`.
The existing format also retains a numeric `refresh` field, which does not
control the HTTP word page's refresh interval. Cookies contain no generated
random bytes, credentials or authentication/session token. WebSocket routes
and the other browser demos continue to use their explicit settings.

The `entropy_prefs` cookie is host-only (`Domain` omitted), uses `Path=/`,
`Secure`, `HttpOnly` and `SameSite=Lax`, and has a one-day `Max-Age`, renewed
when preferences are saved. Keep HTTPS enabled or use HTTPS at your reverse
proxy's browser-facing boundary. The server always sets `Secure`, including
when the backend connection from a TLS-terminating proxy is plain HTTP;
it does not infer cookie security from `X-Forwarded-Proto` or `Forwarded`.

Browsers do not send Secure cookies over ordinary HTTP. Some clients treat
localhost/loopback specially; use HTTPS rather than relying on that
exception. Plain-HTTP endpoints remain usable with query parameters, but
preference persistence over ordinary HTTP is deliberately unsupported.
Cookies issued by an older version without `Secure` gain the attribute on
their next cookie-enabled response; clear old site cookies if retiring an
HTTP deployment.

Explicit query parameters override stored values. Malformed cookie data is
ignored, and individual values outside server limits fall back to defaults.
The cookie is unsigned, untrusted preference input: it never increases
`-max-bytes` or `-words`, bypasses admission budgets or authenticates a
caller. Fresh cookie-mode defaults remain 512 KiB and 3 words, capped by
server limits. Stored `refresh` does not change the server-configured HTTP
word page refresh interval. Invalid/rejected requests and failed generator
setup do not save preferences; a later response-write failure can occur
after cookie headers have already been sent.

For example, start the HTTPS server with a certificate trusted for
`localhost` and opt in:

```sh
./bin/entropy-server -device=/dev/urandom -require-device=true -cookie=true \
  -http=127.0.0.1:8080 -https=127.0.0.1:8443 \
  -cert-file=/path/to/cert.pem -key-file=/path/to/key.pem

# Save a 64 KiB preference, then reuse it without a bytes query parameter.
curl --fail --cacert /path/to/cert.pem --cookie-jar prefs.txt \
  'https://localhost:8443/v1/data/random?bytes=65536' -o first.bin
curl --fail --cacert /path/to/cert.pem --cookie prefs.txt \
  'https://localhost:8443/v1/data/random' -o second.bin
```

Both files are 65,536 bytes and contain separately generated responses.
Omit `-cookie` or use `-cookie=false` for stateless operation: the server
ignores incoming preference cookies and emits none. To remove previously
stored preferences, clear this site's cookies in the browser or delete the
local curl cookie jar. Cookie transport protection follows
[CWE-614](https://cwe.mitre.org/data/definitions/614.html).

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

`-url` and `-wsurl` accept endpoints with existing query parameters. The client preserves other parameters, including repeated values, while `-slice` and `-refresh` replace any existing `bytes` and `refresh` values. Malformed query strings are rejected. Only the selected transport's endpoint is configured and used. The CLI consumes `/stream` raw messages, not `/bytes` compact envelopes.

For example, this requests 64 KiB with a 100 ms refresh setting, overriding the values already in the URL:

```sh
./bin/entropy-client -mode=pull \
  -url='http://127.0.0.1:8080/v1/data/random?bytes=1024&refresh=1000' \
  -slice=65536 -refresh=100ms
```

### Analysis and terminal display

| Flag | Default | Purpose |
| --- | --- | --- |
| `-analysis-bytes` | `1048576` | Maximum prefix bytes analysed per selected frame.<br>Range: 0 to 33554432; `0` selects the full frame. |
| `-analysis-interval` | `250ms` | Minimum interval between analysed frames.<br>Non-negative; `0` analyses every frame. |
| `-display-interval` | `1s` | Terminal redraw interval; minimum 1 ms.<br>Does not change the requested reception interval. |
| `-shannon-threshold` | `7.999` | Minimum raw empirical Shannon estimate for `OK`, in bits/byte; finite value from 0 to 8, inclusive. |
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

The monitor shows one raw **Shannon entropy** estimate, in bits/byte, and one
**Shannon assessment**: `OK` when the estimate is at least
`-shannon-threshold`, otherwise `WARN`. No analysed data means `NO_SAMPLE` in
reports. The threshold does not change the estimate. Chi-square, monobit and
serial-correlation results remain independent reference measurements; they
are not combined into another pass/fail badge. Optional histories use the
same raw estimates. See [client experiments](client-experiments.md#shannon-entropy-and-its-assessment)
for the formula, sample-size effects and interpretation.

| Display | Meaning |
| --- | --- |
| `Requested rate` | Payload size divided by requested interval; a target, in MiB/s. |
| `Received` / `Average received` | All frames handled by the monitor and their cumulative throughput over monitoring runtime. |
| `Total received` / `Transfer Rate` | Receiver byte count and throughput sampled over approximately one-second windows. |
| `Analysed Rate` | Bytes actually analysed per second, including the prefix/frame selection limits. |
| `Analysis samples` / `Analysed prefix` | Number of selected frames versus received frames, and analysed bytes versus the last selected frame's full size. |
| `Analysed total` / `Sample age` | Cumulative sampled bytes and age of the latest analysis sample. |

`Total received`, `Transfer Rate` and `Analysed Rate` are shown with `-dashboard`. Throughput uses **MiB/s**: 1 MiB = 1,048,576 bytes. Analysis rates measure processing of sampled data, not physical entropy production. Receiver and monitor counters can differ briefly because frames may be in flight or queued and rate windows differ.

The interactive client samples the selected transport once, shows preflight
diagnostics for three seconds, then resets the reception-rate window before
monitoring. The former separate cumulative startup demonstration is removed,
so startup and monitoring use the same diagnostics and threshold. With
`-debug`, `Last WS receive` includes waiting for the server's next message;
`WS runtime` sums message-read durations. `Last HTTP fetch` includes the
complete response body; `HTTP cycle runtime` includes request pacing plus
that fetch. Analysis is timed separately, and `Total runtime` is wall time
since monitoring started. These are client-side timings, not pure network
round-trip measurements.

### Timed runs and reports

| Flag | Default | Purpose |
| --- | --- | --- |
| `-duration` | `0s` | Non-negative measurement duration, e.g. `10m` or `3h`; zero runs until interrupted. Excludes interactive preflight. |
| `-report` | empty | Write JSONL metadata, analysed samples and a final summary to a **new** file (mode 0600). `-` selects stdout and requires `-headless`. |
| `-headless` | `false` | Skip interactive preflight and all terminal rendering, previews and bells; suitable for scripts and JSONL stdout. |

The measurement clock includes connection establishment, reception, analysis
and report-writing overhead. Expiry cancels pending network work and releases
queued frames. An analysis or write already in progress and connection
cleanup can make the process finish slightly after the requested duration.
Without `-report`, no report is created; `-headless` alone runs quietly.
With a writable report, normal duration expiry, Ctrl+C/SIGTERM and transport
failures produce a final summary. A failed report write, sync or close is a
command error; a broken destination or forced kill cannot guarantee a final
record. `WARN` is an assessment, not a nonzero exit code.

```sh
./bin/entropy-client -mode=pull -slice=1048576 -refresh=50ms \
  -headless -duration=10m -shannon-threshold=7.99 -report=pull-10m.jsonl
```

See [client experiments](client-experiments.md) for the JSONL contract, longer
runs, payload comparisons and a report-analysis example.

[Back to README](../README.md) · [Technical reference index](README.md)
