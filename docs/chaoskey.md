# Experimental ChaosKey cooked USB source

See [hardware command examples](hardware-examples.md) for Linux/Windows probing,
strict startup, TLS and separate diagnostic capture commands.

ChaosKey is a **hardware-noise TRNG**, not an ID Quantique QRNG. This optional
backend targets USB **1d50:60c6**, interface **0**, alternate **0**, cooked
bulk-IN endpoint **0x85**, with the referenced full-speed 64-byte packets.
Raw ADC samples (`0x86`) and firmware/flash data (`0x87`) are never seed sources.
Unknown identities/layouts are rejected; there is no raw-endpoint fallback.
An explicit [diagnostic capture](#diagnostic-capture-cooked-by-default-raw-on-demand)
can save raw samples separately, without starting or changing server generation.

The implementation uses one shared Go source tree and the same bounded native
USB mechanics as Quantis USB, with separate identity and source-protocol checks.
Linux amd64/armv7/arm64 uses usbfs; Windows x64 uses system WinUSB. No libusb,
vendor DLL, CGO, new Go module dependency or driver installation is required by
the application. A working OS USB stack, correct binding and access permissions
are operator prerequisites. This backend has **not been tested on a physical
ChaosKey** and remains experimental.

## List, select and probe

```sh
entropy-server hardware list -json
entropy-server hardware probe -device=chaoskey-usb:0 -bytes=64 -timeout=3s -json
entropy-server -device=chaoskey-usb:0 -require-device=true -source-timeout=3s -enable-https=false -http=127.0.0.1:8080
```

Use `.\entropy-server.exe` in PowerShell. The example disables HTTPS for initial
loopback testing; provision certificates before using HTTPS normally.

Selection is explicit and strict even without `-require-device`: an absent,
occupied, inaccessible or incompatible ChaosKey never substitutes OS randomness.
The independent `chaoskey-usb:N` indices are 0..126 from the current inventory,
sorted by sysfs identity on Linux and interface path on Windows. Re-list after
reconnect; indices are not persistent serial bindings. Quantis numbering is
independent. Probes accept/discard bounded sample bytes and never print them.

The cooked protocol has no documented per-read health query comparable to
Quantis module status. `health_available` remains **false**; successfully reading
bytes does not establish device health, physical entropy or unpredictability.
Probes also leave hardware cancellation/entropy-quality certification flags
false. Readiness/reseed-failure policy remains shared with all server sources:
by default a failed reseed stops generation until a successful reseed recovers it.
No automatic source substitution or reconnect is added.

## Linux: kernel character device versus direct USB

The normal Linux ChaosKey driver already provides **`/dev/chaoskeyN`** and an
`hwrng` interface. It can contribute to the OS RNG through the configured kernel
RNG policy. That OS CSPRNG output is a separate source and cannot attribute
individual output bytes to ChaosKey.

The reviewed [Linux 6.12 driver](https://github.com/torvalds/linux/blob/v6.12/drivers/usb/misc/chaoskey.c)
and current upstream source have no `.poll` operation. Current upstream adds an
`O_NONBLOCK` mutex trylock, but still calls its interruptible, timeout-based USB
fill routine; this does not establish the server's epoll/nonblocking-read
contract. A successful ordinary `poll()` or the node's existence is insufficient.

You may explicitly select `/dev/chaoskey0` with a driver that actually supports
Go read deadlines and nonblocking reads. The server checks deadline registration;
correct `O_NONBLOCK` behaviour additionally needs driver review/qualification.
It never bypasses deadline support based on a ChaosKey pathname. A named `/dev/chaoskey*` request also
never falls back to the OS source. The reviewed stock driver is therefore
rejected with guidance, rather than allowed to block the seed reader.

Direct `chaoskey-usb:N` opens `/dev/bus/usb/BBB/DDD`, checks descriptors on the
opened handle, and claims interface 0. That node's ordinary `read()` exposes USB
descriptors, not entropy; the backend submits cooked bulk transfers instead.
Inventory reports the raw path in `paths`, existing character nodes separately
in `kernel_paths`, and interface driver ownership. A kernel-owned interface
causes a clear claim failure. **The application never detaches the driver.**

An operator choosing direct USB must explicitly release the correct interface
from the kernel driver and grant the service account read/write USB access.
This stops that interface's kernel `hwrng` contribution while it is unbound.
Use the sysfs interface ID shown by the device's topology (for example `1-2:1.0`),
not a guessed USB address; preserve the original binding for rollback. These are
alternative ownership choices, not simultaneous readers of the same interface.
Use a narrow `1d50:60c6` udev permission rule rather than world-writable USB nodes.

The packaged systemd service defaults to OS randomness with `DevicePolicy=closed`.
Direct USB also needs an operator drop-in with the chosen device source, ordinary
DAC/group permission and an appropriate USB `DeviceAllow` rule. USB addresses can
change on reconnect; choose a cgroup/udev policy that matches your deployment.
See [packaged service configuration](https://github.com/fantuz/entropy-service/blob/main/docs/packaged-service.md).
No permissions, cgroup rules, kernel binding or RNG-pool policy are changed by
entropy-server.

## Windows: operator-provisioned WinUSB

Bind the intended ChaosKey interface to **Microsoft WinUSB**, using a trusted
compatible INF or a reviewed USB binding tool such as
[Zadig](https://zadig.akeo.ie/). Identify `1d50:60c6` and the intended device;
do not change another peripheral's driver. The application does not install an
INF, change a binding, edit the registry, elevate privileges, or disable signing.

The binding must register a device interface. Inventory reads its hardware-key
**`DeviceInterfaceGUIDs` REG_MULTI_SZ** and enumerates those registered interfaces;
it does not assume the Quantis interface GUID or treat the USB bus interface as
a WinUSB handle. If registration is missing, `hardware list` reports the device
and a note explaining why no source selector is available. Follow Microsoft's
[WinUSB installation and device-interface guidance](https://learn.microsoft.com/en-us/windows-hardware/drivers/usbcon/winusb-installation)
for a device-specific binding. A trusted binding package can choose its own GUID;
no hard-coded ChaosKey GUID is required by this backend.

Open validates the identity again through WinUSB, checks interface/alternate 0
and the cooked bulk endpoint, and uses overlapped I/O with cancellation/draining.
WinUSB access is an application seed source: it does **not** register ChaosKey as
a Windows CNG entropy provider or change `crypto/rand` globally.

## Read/cancellation contract and validation

Each call requests up to 32 KiB in 64-byte packet multiples, accepting a nonempty
short bulk transfer as a valid prefix. The shared seed reader fills the bounded
seed across partial reads. Zero progress, invalid lengths, failed or late
transfers return errors; no failed prefix reaches the destination. Scratch bytes
and unused tails are cleared, and tails are not reused by a later reseed.
The source sends no Quantis status/control commands, device reset, firmware read
or configuration write.

Read deadlines, changes to them, and closure reach the native I/O owner. Linux
cancels and reaps URBs; Windows uses `CancelIoEx` and drains overlapped completion
before unpinning buffers. No blocked I/O goroutine is abandoned. Discovery/open,
descriptor queries and cleanup still include synchronous OS calls; a broken USB
stack/host controller can prevent completion. This is not a process watchdog.

Focused tests check identity/configuration rejection, cooked-only routing, partial
and failed transfers, starvation, bounds, clearing, strict selection, Linux kernel
node/USB separation, and source reporting without invented health status. Native
Linux UAPI fixtures and native Windows overlapped named pipes exercise the shared
transport with **both** source profiles. GitHub CI runs full regression/race/lint,
descriptor fuzzing, Windows and packaged-executable smoke tests. These are software
checks, not physical-device qualification. Actual firmware, Windows binding,
Linux ownership, USB removal, sustained reads and reseed recovery still need
checking with a ChaosKey.

Protocol references are the [ChaosKey project](https://altusmetrum.org/ChaosKey/),
[Keith Packard's reader](https://github.com/keith-packard/chaosread/blob/master/chaosread.c)
and the upstream kernel driver. The author's `chaosread` defaults to **raw**;
`-c` selects cooked output and `-b` extracts bits from 16-bit values. Unlike that utility, this backend
does not automatically detach a Linux driver. No third-party ChaosKey reader or
firmware implementation is bundled; the application remains MIT licensed.

## Diagnostic capture: cooked by default, raw on demand

Raw access has value for inspecting sensor bias, correlations, repeated or stuck
readings, and comparing the acquisition path with firmware-processed output.
Firmware conditioning can make those patterns harder to see. Raw is not
automatically better randomness: ADC resolution, word packing and unused bits
affect byte histograms and Shannon values. A low byte-wise Shannon value alone
does not establish a fault, and a high value does not establish unpredictability.

Use the separate `hardware capture` command for a bounded binary recording:

```sh
# The default remains cooked output, endpoint 0x85.
entropy-server hardware capture -device=chaoskey-usb:0 -out=cooked.bin -bytes=1048576 -timeout=10s -json

# Explicit diagnostic raw samples, endpoint 0x86.
entropy-server hardware capture -device=chaoskey-usb:0 -mode=raw -out=raw.bin -bytes=1048576 -timeout=10s -json
```

In PowerShell, replace `entropy-server` with `.\entropy-server.exe`. To retain
the JSON report, append `> raw-report.json` (or the corresponding cooked report
name). Binary bytes go only to `-out`; stdout contains a human-readable report
unless `-json` is selected. The capture command does not start HTTP(S), need a
certificate, seed a DRBG or change the service configuration.

- `-mode=cooked` is the default; `-mode=raw` selects only ChaosKey `0x86`.
  There is no arbitrary endpoint option and no flash access. Normal server
  `-device=chaoskey-usb:N` and `hardware probe` continue to select cooked `0x85`.
  Raw is not a server seed selector or a fallback for missing cooked output.
- `-bytes` is 64..8,388,608, in multiples of 64, default 1 MiB. The file contains
  exactly the requested accepted prefix, unchanged: no ADC decoding, byte-order
  conversion, bit extraction or whitening. Any unused transfer tail is discarded.
  In particular this is not `chaosread -b`'s extraction of eight bits from each
  16-bit value; decode according to the device's actual firmware/sample format.
- `-timeout` is 1 ms..5 min, default 10 s. Discovery/open count toward the read
  budget; partial reads must complete within that budget. The shared native USB
  cancellation/draining contract still applies. Synchronous OS/driver calls and
  output-file writes can outlast it; this is not a process watchdog.
- `-out` must name a new regular file. Existing paths, including existing
  symlinks, are refused; exclusive creation also handles a destination appearing
  during acquisition. Read, cancellation or source-close errors discard the
  sample before file creation. File-write/sync/close failures trigger removal of
  the incomplete file; cleanup errors are reported. A crash or forced process
  kill can still leave an incomplete artifact: require `capture_ok: true`, the
  reported byte count and matching SHA-256 before accepting it. A report-output
  error after a successful save leaves the completed binary file in place.
- Unix creation requests mode `0600`; Windows uses the shared private-file helper
  to create a protected current-account-only DACL before writing, failing closed
  if that policy cannot be verified. Use an ACL-capable filesystem and a directory
  you control. No directories are created and
  no output is sent to stdout, a device node or the network as a binary stream.

Capture needs the same operator-provisioned USB access and WinUSB binding as
cooked operation. It claims the device/interface exclusively: stop the server
or other reader first. It never detaches the Linux driver automatically or
changes OS RNG policy. Simultaneous raw/cooked operation and their physical
interaction have not been qualified.

The JSON report records the requested/actual selector, mode, endpoint, OS and
architecture, available discovery identity/driver metadata, exact byte count,
read timing, output filename and SHA-256. `read_mib_per_second` times the complete
accepted read, including waits/partial reads but excluding discovery, open,
source close and file writing; `elapsed_ms` covers the whole capture operation.
If a fast read completes within the clock's resolution, `read_elapsed_ms` can be
zero and `read_mib_per_second` stays `null`; no rate is invented. A read not
attempted has a `null` duration.
Discovery metadata is an OS inventory snapshot, not a firmware attestation.
`firmware_version` stays `null`: the command sends no flash or undocumented
health queries. `health_available`, `cancellation_tested` and `quality_assessed`
stay false. A successful capture does not qualify the device.

Record raw and cooked sessions separately using the
[qualification guide](https://github.com/fantuz/entropy-service/blob/main/docs/hardware-qualification.md),
including the firmware/sample-format reference, kernel/OS build and driver
version. The historical CI checkpoints in its original matrix predate raw
capture. Raw support has descriptor/routing tests and Linux/Windows simulated
completion/cancellation tests; neither replaces physical qualification. LAB
visualization and live raw network export are not provided by this command.
