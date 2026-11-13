# Hardware source examples: Quantis and ChaosKey

These commands match `main` at `dcc01a8` (2026-11-13). Install a current binary
on PATH, or replace `entropy-server` with `./bin/entropy-server`. In PowerShell
use `.\entropy-server.exe` from the extracted release directory (or its full
path). Run one server example at a time; they share the same loopback ports.

All hardware adapters remain experimental pending
[physical qualification](hardware-qualification.md). Successful compilation,
inventory or a sample probe does not certify source quality or cancellation.

## Choose the actual source

| Source | Linux | Windows x64 | Prerequisite |
| --- | --- | --- | --- |
| Legacy Quantis PCI (`179a:0001`) | `/dev/qrandom0` | `quantis-pci:0` | A matching driver; Linux requires the bounded nonblocking/poll contract. See [PCI guide](quantis.md). |
| Referenced Quantis USB (`0aba:0102`) | `quantis-usb:0` | `quantis-usb:0` | Read/write usbfs access on Linux, documented WinUSB binding on Windows. See [USB guide](quantis-usb.md). |
| ChaosKey (`1d50:60c6`) | `chaoskey-usb:0` | `chaoskey-usb:0` | Free USB interface and local permissions/WinUSB binding. Seeding uses cooked `0x85`. See [ChaosKey guide](chaoskey.md). |
| OS control | `/dev/urandom` | `windows-csprng` | OS CSPRNG; this does not measure physical-device output. |

Start with read-only inventory, then probe the exact selector shown:

```sh
entropy-server hardware list -json
entropy-server hardware probe -device=/dev/qrandom0 -bytes=64 -timeout=3s -json
entropy-server hardware probe -device=quantis-usb:0 -bytes=64 -timeout=3s -json
entropy-server hardware probe -device=chaoskey-usb:0 -bytes=64 -timeout=3s -json
```

Windows examples:

```powershell
.\entropy-server.exe hardware list -json
.\entropy-server.exe hardware probe -device=quantis-pci:0 -bytes=64 -timeout=3s -json
.\entropy-server.exe hardware probe -device=quantis-usb:0 -bytes=64 -timeout=3s -json
.\entropy-server.exe hardware probe -device=chaoskey-usb:0 -bytes=64 -timeout=3s -json
```

Choose the command for the installed device. Probe samples are cleared, not
saved or printed, and no server/DRBG is started. Probe `-bytes` accepts
32..1,048,576 and `-timeout` accepts Go durations from `1ms` to `5m`. A failed
probe returns a nonzero exit code; its JSON can still describe the failure.
Probe an otherwise idle device: the command does not interrupt an owner.
Re-list after reconnecting because selectors are inventory indices, not stable
serial bindings. PCIe-chip/XDMA inventory does not imply a supported seed adapter.

## Local server using hardware

For initial **loopback-only** testing, explicitly disable HTTPS:

```sh
entropy-server -device=/dev/qrandom0 -require-device=true -source-timeout=3s -enable-https=false -http=127.0.0.1:8080
entropy-server -device=quantis-usb:0 -require-device=true -source-timeout=3s -enable-https=false -http=127.0.0.1:8080
entropy-server -device=chaoskey-usb:0 -require-device=true -source-timeout=3s -enable-https=false -http=127.0.0.1:8080
```

Windows, choosing one installed source:

```powershell
.\entropy-server.exe -device=quantis-pci:0 -require-device=true -source-timeout=3s -enable-https=false -http=127.0.0.1:8080
.\entropy-server.exe -device=quantis-usb:0 -require-device=true -source-timeout=3s -enable-https=false -http=127.0.0.1:8080
.\entropy-server.exe -device=chaoskey-usb:0 -require-device=true -source-timeout=3s -enable-https=false -http=127.0.0.1:8080
```

Open <http://127.0.0.1:8080/>. In a second terminal:

```sh
entropy-client -mode=push -slice=65536 -refresh=50ms -layout=waterfall -color=never
```

In PowerShell prefix that client command with `.\entropy-client.exe` instead
of `entropy-client`. Optional `-waterfall-mode=rgb -color=truecolor` uses a
compatible color terminal; see [terminal capabilities](client-terminal.md).

`-require-device=true` makes the Linux device-path example strict. Explicit
USB and Windows PCI selectors are strict even without it. Missing, busy,
unsupported or inaccessible hardware fails; it does not silently select an OS
source. The default runtime policy is `-on-reseed-failure=stop`; failed/stale
reseeds pause generation, and a later successful reseed of the same open
source restores admission. A disconnected device is not automatically reopened.
`/ready` and `X-RNG-Source` report the actual source. The served output is DRBG
expansion from seed material, not a direct source-throughput measurement.

Linux stock `/dev/chaoskeyN` drivers lack the required read contract in reviewed
versions; use the explicit USB path only after choosing interface ownership.
The application does not detach a Linux driver, install WinUSB, change device
permissions or modify the OS RNG policy. Packaged systemd services also need
operator device permissions; changing only `ExecStart` is insufficient. Follow
the [driver guide](hardware-drivers.md) and [packaged service guide](packaged-service.md).

## Add HTTPS with a unique identity

Create a new private directory and provision once. This Linux example uses
Quantis USB; replace the selector with the actual supported source:

```sh
mkdir -m 700 tls
entropy-server generate-cert -cert-file=tls/cert.pem -key-file=tls/key.pem -hosts=localhost,127.0.0.1 -valid-for=168h
entropy-server -device=quantis-usb:0 -require-device=true -source-timeout=3s -http=127.0.0.1:8080 -https=127.0.0.1:8443 -cert-file=tls/cert.pem -key-file=tls/key.pem
curl --fail --cacert tls/cert.pem https://localhost:8443/ready
```

PowerShell, in an operator-controlled directory:

```powershell
New-Item -ItemType Directory -Path tls
.\entropy-server.exe generate-cert -cert-file=tls/cert.pem -key-file=tls/key.pem -hosts=localhost,127.0.0.1 -valid-for=168h
.\entropy-server.exe -device=chaoskey-usb:0 -require-device=true -source-timeout=3s -http=127.0.0.1:8080 -https=127.0.0.1:8443 -cert-file=tls/cert.pem -key-file=tls/key.pem
curl.exe --fail --cacert tls/cert.pem https://localhost:8443/ready
```

The generator creates new files exclusively and applies the private-file
policy. Self-signed certificates still need explicit client trust. Include the
real DNS/IP SANs for a remote deployment and provision authenticated access at
the network boundary. Enabling HTTPS leaves HTTP enabled too; the examples
keep both loopback-bound. See [certificates and systemd overrides](tls-certificates.md)
and [client trust](client-tls.md) before using HTTPS/WSS remotely.

## Capture ChaosKey diagnostics separately

Cooked data is the default; raw ADC `0x86` is explicit and stays outside seeding:

```sh
entropy-server hardware capture -device=chaoskey-usb:0 -out=cooked.bin -bytes=1048576 -timeout=10s -json
entropy-server hardware capture -device=chaoskey-usb:0 -mode=raw -out=raw.bin -bytes=1048576 -timeout=10s -json
```

Use `.\entropy-server.exe` in PowerShell. Capture requires a new regular file,
64..8,388,608 bytes in multiples of 64, and `-timeout=1ms..5m`. It preserves
USB bytes without ADC decoding, bit extraction or entropy credit. It starts no
listeners and never changes the server source. Do not run capture concurrently
with another owner of the same interface. No flash access is offered.

Only a complete accepted read is saved. The timeout covers discovery/open/read
checks; synchronous OS calls and regular-file writes/syncs can outlast it.
A report-output failure returns failure even if the complete sample was already
saved: inspect the destination before retrying with a new name. Existing files
are never overwritten. See the [capture contract](chaoskey.md#diagnostic-capture-cooked-by-default-raw-on-demand).

Record firmware, OS/kernel, installed driver, application SHA, accepted bytes,
timings and observed cancellation in a [physical qualification record](hardware-qualification.md).
Keep raw samples and seed material out of public reports. Neither raw nor cooked
capture certifies min-entropy; ChaosKey has no documented per-read health query.
