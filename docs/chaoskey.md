# Experimental ChaosKey cooked USB source

ChaosKey is a **hardware-noise TRNG**, not an ID Quantique QRNG. This optional
backend targets USB **1d50:60c6**, interface **0**, alternate **0**, cooked
bulk-IN endpoint **0x85**, with the referenced full-speed 64-byte packets.
Raw ADC samples (`0x86`) and firmware/flash data (`0x87`) are never seed sources.
Unknown identities/layouts are rejected; there is no raw-endpoint fallback.

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
`-c -b` explicitly requests cooked binary output. Unlike that utility, this backend
does not automatically detach a Linux driver. No third-party ChaosKey reader or
firmware implementation is bundled; the application remains MIT licensed.
