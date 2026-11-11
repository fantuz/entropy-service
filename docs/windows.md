# Optional Windows x64 port

Linux remains the primary deployment target. Windows builds use the same server,
client, DRBGs, HTTP/WebSocket protocols, analysis, reports and embedded browser
pages. Small platform adapters select OS operations at compilation; there is no
separate application tree or copied Windows implementation.

This first port targets x64 console applications on modern Windows. It does not
install a Windows service. Windows ARM64 releases await native validation. Linux
release targets and socket/device implementations remain unchanged.

## Build or install

Extract a Windows release ZIP into a directory you control. It contains both
executables, license and wordlist notices. Normal loopback use needs no QRNG
driver or administrator privilege. To build from source, install the Go version
required by `go.mod`, then run from the repository in PowerShell or CMD:

```powershell
go build -o bin/entropy-server.exe ./cmd/entropy-server
go build -o bin/entropy-client.exe ./cmd/entropy-client
```

Make and a C compiler are not required.

## Local demonstration

Start an HTTP-only loopback demonstration without TLS certificate provisioning:

```powershell
.\bin\entropy-server.exe -http=127.0.0.1:8080 -enable-https=false
```

Open <http://127.0.0.1:8080/>. The seed defaults to `-device=windows-csprng`:
Go's `crypto/rand` uses the native Windows OS CSPRNG (`ProcessPrng` in the current
toolchain). This is an OS seed source, not a physical QRNG measurement. The shared
DRBG backends and reseed policy continue to generate and govern served output.

Press Ctrl+C for normal shutdown and its traffic summary. Closing the console or
forcibly terminating a process may prevent a complete drain/report. Binding beyond
loopback can require a Windows Firewall rule; the application does not create
rules or elevate privileges automatically.

In a second terminal:

```powershell
.\bin\entropy-client.exe -mode=stream -slice=65536 -refresh=100ms -duration=30s -layout=compact
.\bin\entropy-client.exe -headless -slice=65536 -refresh=100ms -duration=30s -output=sample.bin -output-bytes=1048576 -report=run.jsonl
```

`-output-bytes` stops after that many accepted bytes, possibly before `-duration`.
Inspect the JSONL termination reason/count to distinguish a complete capture from
an interrupted or failed one. Existing output/report files are never replaced.

## Explicit capability limits

| Feature | Initial Windows behaviour |
| --- | --- |
| OS seeding | `windows-csprng`, through `crypto/rand`. |
| Direct QRNG/device seeding | A different `-device` or `-require-device` is rejected before TLS/source/network I/O, without silently substituting a source. |
| Processor randomness | Optional `-cpu-mix` / `-rdrand-mix` retains shared support checks and retry limits. It supplements the OS seed. |
| Socket tuning | Go/Windows defaults; custom Linux tuning is disabled and reported at startup. |
| Browser visualizations | Shared pages, including browser waterfalls and layout controls. |
| Terminal output | Plain scrolling text in CMD, PowerShell or Windows Terminal. `-layout=auto` selects compact output; no sizing or cursor refresh. |
| Terminal full/waterfall layouts | Explicit requests, including waterfall bounds/nondefault modes, are rejected. Use compact diagnostics or the browser stream page. |
| Terminal colours | `auto`/`never` select plain text; forced colours are rejected in this first port. |
| Raw binary export | Private `-output=FILE` with byte-limit accounting. `-output=-` is rejected with a file-export alternative. |
| JSONL | Private `-report=FILE`, or text stdout with `-report=- -headless`. |
| Linux device output | `-writeout` and `-output-device` are rejected. |
| LAB sources | HTTP/WS peers, page fetches, TCP/UDP and browser camera/microphone remain shared. Direct devices and Unix socket registrations are rejected; use a network adapter. |
| RTSP/ONVIF | Requires compatible `ffmpeg.exe` on PATH; Windows CI does not certify physical camera/decoder integration. |

The existing non-Linux fallback files have narrow meanings: terminal sizing
returns “unknown”, selecting scrolling text; device/raw-stdout adapters return
explicit errors. They do **not** disable normal stdout, stderr, JSONL or file
exports. Windows capability validation rejects explicit unsupported options
before beginning an experiment, even with `-headless`.

OS randomness is read synchronously in bounded chunks. Deadlines/closure are
checked before/after calls; the shared seed reader checks its context between
chunks and rejects late output. An OS call already in progress cannot be
interrupted through Go's reader interface. No background read is abandoned to
simulate cancellation. In-progress regular file writes/syncs likewise have no
hard cancellation deadline.

## Files, logs and TLS

Windows private files receive a protected DACL granting full access only to the
current account **at creation, before writing data**. They do not inherit a broad
parent ACL. Creation verifies the policy and fails on unsupported filesystems;
Unix `0600` is not treated as a Windows privacy guarantee. Use an ACL-capable
filesystem such as NTFS and an operator-controlled directory. This policy does
not protect parent directories or defeat privileged administrators/host compromise.

The helper is shared by raw captures, JSONL and generated TLS files. Linux keeps
exclusive `0600` creation and existing permission semantics.

Normal text can be redirected in CMD or PowerShell:

```powershell
.\bin\entropy-server.exe -http=127.0.0.1:8080 -enable-https=false 2> server.log
.\bin\entropy-client.exe -duration=30s > monitor.txt 2> client-errors.log
```

Shell-created logs use the shell's file/access policy. For application-created
private structured records, use `-report=run.jsonl`. A log is not raw entropy.
Direct `-output=sample.bin` avoids binary-redirection differences between
PowerShell versions; no Windows raw-stdout adapter is enabled here.

For HTTPS, generate or provision a unique identity in your controlled directory:

```powershell
.\bin\entropy-server.exe generate-cert -hosts=localhost,127.0.0.1
.\bin\entropy-server.exe generate-cert -hosts=localhost,127.0.0.1 -if-missing
.\bin\entropy-server.exe -http=127.0.0.1:8080 -https=127.0.0.1:8443
```

Certificate reuse checks the key's protected owner-only ACL on Windows, not Unix
mode bits. It refuses broad/inherited/foreign-owner ACLs without modifying them;
the narrow reuse policy requires one explicit owner grant. Provisioned files can
also be loaded normally with `-cert-file`/`-key-file`, with ACLs managed by the
operator. Self-signed certificates require explicit client trust. Trust the
**public certificate** according to your Windows account policy, or use a trusted
CA; never distribute the key or disable TLS verification. See [client TLS
trust](client-tls.md).

## Validation

Linux CI/regression and packaging remain in place. A reusable Windows job runs
all Go tests, vet/lint, builds both executables and runs
`python scripts/test_windows.py` for real HTTP/HTTPS/WS exchanges, file/JSONL
captures, certificate reuse, platform errors, plain stdout and console shutdown.
Release publication waits for Linux/browser validation and native Windows checks.

Pinned dictionaries are marked `-text` in `.gitattributes` so Windows Git checkout
cannot convert their LF bytes to CRLF and break their SHA-256 integrity checks.

Existing suffixes select Linux device/PTY tests. POSIX shell camera fixtures and
mode-bit cases are explicitly skipped on Windows; native ACL tests cover its file
policy. Missing symlink privilege skips only the relevant symlink test. Hardware
QRNG, Windows ARM64, RTSP devices and interactive colour/resize support are not
established by cross-compilation.

Shared seed-lifecycle tests retain real OS pipes on Linux. Windows anonymous pipes
lack read deadlines, so those tests use a buffered loopback TCP pair on Windows.
The native RNG adapter has separate tests for its actual deadline/close semantics.
