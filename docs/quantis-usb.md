# Experimental Quantis USB transport

Linux amd64/armv7/arm64 and Windows x64 builds can use the **referenced legacy
Quantis USB protocol**, vendor/product `0aba:0102`. This does not advertise every
USB model sold under the Quantis name. No physical USB QRNG has been tested for
this change; use it experimentally until validated with the actual device,
firmware and OS driver.

## Select and probe

```sh
entropy-server hardware list -json
entropy-server hardware probe -device=quantis-usb:0 -bytes=64 -timeout=3s -json
entropy-server -device=quantis-usb:0 -require-device=true -enable-https=false -http=127.0.0.1:8080
```

Prefix `entropy-server.exe` with `.\` in PowerShell. USB selection is strict
even without `-require-device`; missing, inaccessible, incompatible or unhealthy
hardware never selects the OS CSPRNG instead. Explicit `quantis-usb:N` indices
are 0..126 from the current inventory: sorted sysfs IDs on Linux, sorted device
interface paths on Windows. Re-list after a reconnect; indices are **not**
persistent serial-number bindings.

The initial root seed and subsequent reseeds use the selected transport through
the existing shared seed lifecycle. DRBGs, CPU mixing options, HTTP/WS protocols,
readiness and `-on-reseed-failure` remain shared. A disconnected/unhealthy source
records a reseed failure and follows that policy (default `stop`). There is no
automatic reconnect, source substitution, board reset, module-enable, acquisition
mode change or firmware operation. Device sharing can fail while a vendor
application or another collector owns the interface.

## Windows: system WinUSB

The device must be bound to **Microsoft WinUSB** and expose the reference
interface GUID `{6476DB04-8D6C-49F5-BCF3-E2C1FC313A18}`. A matching trusted vendor
installation/binding package or compatible device descriptors are required.
Having `WinUSB.dll` on the computer alone does not establish that binding.
See Microsoft's [WinUSB installation documentation](https://learn.microsoft.com/en-us/windows-hardware/drivers/usbcon/winusb-installation).

The supplied mirror has USB user-mode source, but its standalone kernel packages
target PCIe. Its application ZIP and MSI do not supply a USB driver installation
package. entropy-service does not create registry entries, install an INF,
replace another device driver, elevate privileges or modify signing/security
policy. Inventory separates observed driver/interface metadata from probe success.

The adapter loads functions from the system `winusb.dll` through native Go
calls. No vendor DLL, Java, C compiler, libusb or CGO is required. The mirrored
`Quantis.dll` is 32-bit; it is not used by the x64 server. Other Windows
architectures retain their existing port limits and explicitly reject this USB
transport.

## Linux: usbfs without libusb

The transport uses the Linux kernel's standard usbfs UAPI on
`/dev/bus/usb/BBB/DDD`; no Quantis-specific kernel module or libusb development
package is needed for this USB protocol. The OS still needs working USB host
support, mounted device/sysfs access, and permission for the service account to
open the matching node for read/write. Manage device permissions locally with
an appropriate udev rule; the application does not change them. An already
claimed interface fails clearly; the adapter does not detach an existing driver.

The opened descriptor's cached device/configuration descriptors must match the
allowlist. The adapter claims interface zero and submits asynchronous usbfs
control/bulk URBs. It polls completion readiness (`POLLOUT`) plus a deadline/
closure notification eventfd. Cancellation discards and **reaps** the submitted
URB before releasing its memory. Fatal completion/reap errors invalidate and
close usbfs, which destroys outstanding transfers before buffers are released.
Pointer-sized UAPI layout is checked for each released architecture at compile
time. Official builds remain `CGO_ENABLED=0` and gain no Go module dependencies.

Raw usbfs descriptor files are **not entropy byte streams**. Generic `-device`
opening rejects their device major number; use `quantis-usb:N` to issue the
required vendor control/bulk protocol. Linux PCI/PCIe drivers and their existing
character-device read path are otherwise unchanged.

## Read contract and limits

The implementation follows ID Quantique Software 20.2.4's
[`QuantisUsb_Commands.h` and USB sources](https://github.com/lfd/idq-driver/tree/d051aeaaaee63a10fad4cdfbeba9def9ecae3feb/pcie-chip-20.2.4-windows/Libs-Apps/Quantis).
It verifies one supported configuration/interface, bulk-IN endpoint `0x86`,
64/512-byte packets and a single enabled/healthy module. Unknown layouts are
rejected. Version/status requests use the documented vendor-IN/interface setup;
no write/reset command is sent. Module health is checked before and after each
accepted sample. These diagnostics do not measure entropy or establish quantum
origin, certification or resistance to a malicious device/driver.

Reads request complete endpoint packets, copy only the requested bytes and clear
scratch buffers. Unused packet tails are discarded rather than cached for a
later reseed. Zero-progress/short/error/late transfers do not supply accepted
seed bytes. At most 32 KiB is accepted per reader call. Physical USB byte traffic
can exceed accepted seed-byte counts, particularly for small samples.

Control and bulk operations share the seed/probe's complete deadline; no new
one-second timeout is restarted for every packet. Windows waits on overlapped
completion, requests `CancelIoEx` on expiry/closure, then drains completion.
Linux uses the analogous discard/reap lifecycle. Native operation state and
buffers remain pinned until completion is drained. No I/O goroutine is abandoned.

Cancellation still depends on the OS/USB stack/host controller. Discovery/open,
cached descriptor queries, interface setup and cleanup include synchronous OS
calls. A broken kernel component can block completion/close; this is not an
unconditional process watchdog. A successful probe keeps cancellation and
entropy-quality assurance flags false.

## Validation and licensing

Shared tests verify wire/setup bytes, descriptors, strict selectors, health,
packet tails, zero/short/late/error output and clearing. Linux tests exercise
submit/reap/cancel/close ordering with UAPI fixtures and real eventfd wakeups;
Windows tests use actual overlapped named pipes and check system WinUSB entry
points. These establish software mechanics, not a physical USB QRNG protocol
exchange. Native and packaged CLI checks cover the hardware-free paths.

Physical validation still needs a matching device: confirm its firmware/layout,
driver binding/permissions, healthy reads, timeouts, USB removal, shutdown,
multi-device numbering and observed reseed behaviour. No physical-card results
or USB throughput numbers are claimed here.

The Go implementation uses the public protocol/UAPI declarations and retains
the vendor notice in `QUANTIS_NOTICES.txt`. No vendor implementation source,
drivers or DLLs are bundled. The application remains MIT licensed.
