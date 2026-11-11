# Experimental Quantis PCI seed adapter

The optional Windows x64 build can read a legacy ID Quantique Quantis PCI QRNG
through its installed Windows driver. This is a user-mode seed-source adapter,
not a replacement kernel driver, an emulated `/dev/urandom`, or support for all
Quantis generations. Linux remains the primary target and retains its existing
device reader. The server/client application tree and DRBG code are shared.

## Windows setup

Install the correct, trusted Windows driver for the **legacy PCI** board. Confirm
Device Manager identifies `PCI\VEN_179A&DEV_0001` and that the driver's device
interface is registered. Then, in PowerShell:

```powershell
.\bin\entropy-server.exe -device=quantis-pci:0 -require-device -source-timeout=3s -enable-https=false -http=127.0.0.1:8080
```

This uses the first discovered legacy board for the initial 64-byte seed and
subsequent root reseeds. `quantis-pci:0` through `quantis-pci:9` select zero-based
boards, ordered by sorted Windows interface path. This ordering can change after
hardware/driver changes; it is not a serial-number binding and can differ from
the vendor SDK's combined PCI/PCIe numbering. This legacy API does not provide a
usable hardware serial number.

The selector always requires the requested hardware, whether or not
`-require-device` is supplied. Missing/incompatible drivers, denied access,
unhealthy/disabled modules, unsupported operations and failed initial reads stop
startup. There is no hardware-to-OS fallback. Default Windows invocations still
use `-device=windows-csprng`. To explicitly choose OS seeding, remove
`-require-device` and select that source.

No DLL, C compiler, Java runtime, CGO, kernel installation or privilege elevation
is performed by entropy-service. Driver installation/signing and account access
are separate administrator tasks. The adapter opens the board exclusively,
matching the vendor's legacy access mode; another vendor application may prevent
opening it. It does not reset the board, enable modules or change its settings.

## Interface and compatibility evidence

The reference is [lfd/idq-driver at revision
d051aea](https://github.com/lfd/idq-driver/tree/d051aeaaaee63a10fad4cdfbeba9def9ecae3feb),
containing ID Quantique Software 20.2.4:

| Reference | Relevant information |
| --- | --- |
| `pcie-chip-20.2.4-windows/Libs-Apps/Quantis/Public.h` | Legacy interface GUID `{F2F5F1E5-380E-4194-8FC0-ED5AF63E93DF}`; module mask/status IOCTLs. |
| `…/QuantisPci_Windows.c` | Discover interfaces, open exclusively for read/write, check modules, then `ReadFile`. The SDK uses synchronous I/O. |
| `pcie-chip-20.2.4-linux/Libs-Apps/Quantis/quantis_pci.h` | Legacy PCI vendor/device `179a:0001`, four modules maximum, `qrandom` device name. |
| `pcie-chip-20.2.4-windows/Drivers/Windows/*/XDMA.inf` | Bundled x86/x64 driver targets **PCIe**, not proof that the package supplies the legacy PCI driver. |

The repository's C/Java wrappers use these native interfaces. The adapter is
original Go interoperability code using `golang.org/x/sys/windows`, which is
already a project dependency. It enumerates the legacy interface, verifies the
PCI instance ID, and issues only read-only module queries plus reads. All
installed module bits must be healthy/enabled; partial-module operation is
deliberately rejected. At most 32 KiB is requested per read. Status checks are
device diagnostics, not an independent entropy estimate or certification.

The bundled `XDMA.sys` and PCIe C implementation use a different interface and
register/DMA protocol. They cannot be substituted for a missing legacy driver.
Newer PCIe/USB/chip devices are outside this adapter's scope, even if marketed
under the Quantis name. The old README identifies Quantis PCI but the examined
history does not record a PCI ID; `179a:0001` comes from the vendor header and
still needs confirmation on the actual host.

## Deadlines and failure handling

Unlike the SDK's synchronous call, this adapter requires an overlapped handle
and uses completion events for status IOCTLs and `ReadFile`. Deadline changes
and closure wake the waiting reader. Expiry/closure requests `CancelIoEx` and
**drains completion before releasing the buffer, OVERLAPPED structure or handle**.
It never abandons an I/O goroutine to simulate a timeout. Failed or late reads
do not reach the DRBG; partial scratch buffers are cleared.

This is **not an unconditional hard timeout on kernel code**. The supplied
repository lacks the legacy PCI kernel source, and synchronous SDK use does not
establish overlapped/cancellation support. A driver rejecting overlapped I/O
fails clearly without a synchronous fallback. A driver blocking inside dispatch
or ignoring cancellation can still block the process while completion is
drained. Discovery and device opening are synchronous Windows calls too. Startup
reports this experimental limitation. Physical validation must check starvation,
unplug/fault, read deadline and Ctrl+C shutdown on the installed driver before
relying on it operationally.

Runtime reseed errors follow the existing `-on-reseed-failure` policy (default
`stop`), readiness and seed-age reporting. The same open source is retained;
there is no automatic reopen or switch to OS randomness. `X-RNG-Source` reports
`quantis-pci:N`; served bytes are DRBG output seeded by the device, not a direct
export of raw hardware measurements.

## Linux integration findings

Linux owns `/dev/urandom`; the Quantis driver does not create that OS RNG node.
The legacy API names its independent character devices `/dev/qrandom0`,
`/dev/qrandom1`, etc. Direct hardware seeding remains:

```sh
lspci -nn -d 179a:0001
./bin/entropy-server -device=/dev/qrandom0 -require-device=true -source-timeout=3s -enable-https=false -http=127.0.0.1:8080
```

Permissions and a compatible installed driver are required. Current Linux
seeding requires `O_NONBLOCK` plus poll/deadline support, not just a working
blocking C `read()`. The supplied legacy C library opens the node synchronously;
that example alone cannot prove those requirements. Its userspace `isatty` or
terminal-attribute diagnostics do not make the QRNG a terminal and should not
be reproduced in the Go reader. Driver-side readiness/cancellation support is
the useful area for a future Linux driver audit. No Linux driver or syscall
behaviour is changed in this patch. See [device migration](source-io.md).

The bundled **PCIe** Linux `xdma-core.c` creates `qrandomN` nodes too, but its
`sg_fops` table has no `.poll` operation and its read path uses interruptible
kernel waits without an `O_NONBLOCK` branch. This snapshot therefore does not
establish compatibility with the server's current deadline-aware device reader;
driver changes may be needed. This observation concerns that bundled PCIe
driver, not proof about the older PCI kernel driver installed on your host.
Its udev sample grants group `plugdev` read/write access and read access to other
accounts (`0664`); review local permissions and prefer only the access actually
required for the service.

Feeding hardware bytes into the Linux random pool through an explicitly
configured collector is a separate deployment choice; reading `/dev/urandom`
then obtains mixed OS CSPRNG output, not a direct Quantis stream or proof of
which source contributed. Do not claim or credit measured entropy solely from
device presence.

## Validation and licensing

Tests cover source selection/no fallback, malformed board selectors, module
status checks, device identity filtering and native Windows overlapped reads,
timeout cancellation, deadline changes, completion draining, subsequent reuse
and closure using local named pipes. The named-pipe tests validate Windows I/O
mechanics; they do not implement the vendor protocol or emulate a physical QRNG.
CI also exercises driver-absent discovery on hardware-free Windows runners and
the normal OS-seeded packaged binaries.

**No physical Quantis PCI card or legacy Windows driver has been tested for this
change.** Build success, mocked selection and named pipes do not establish
hardware compatibility, throughput, randomness quality or driver certification.

The reference source offers permissive BSD-style terms as an alternative to
GPLv2. Its notice is retained in `QUANTIS_NOTICES.txt` and release archives;
no vendor binaries or implementation source are redistributed. The project
remains MIT licensed. Vendor drivers retain their own installation and licence
terms.
