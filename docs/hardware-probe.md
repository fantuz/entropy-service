# Hardware discovery and source probing

For a saved ChaosKey sample, use the separate
[`hardware capture` command](chaoskey.md#diagnostic-capture-cooked-by-default-raw-on-demand).
It defaults to cooked `0x85`; `-mode=raw` explicitly captures diagnostic `0x86`
without changing seed selection. The probes described below still discard
samples and always use cooked output for ChaosKey.

`entropy-server hardware` runs before normal server setup. It creates no TLS
files or listeners, starts no DRBG and never installs or changes a driver.
It supports Linux and Windows from the same application tree.

## Inventory without opening devices

```sh
entropy-server hardware list
entropy-server hardware list -json
```

Inventory reads Linux sysfs or Windows Plug and Play/registry metadata. It does
not open a hardware handle, read random data, reset a board or enable modules.
The allowlist identifies the legacy Quantis PCI interface (`179a:0001`),
PCIe-40M/240M (`1e89:0002`/`0003`) and the referenced Quantis USB protocol
(`0aba:0102`). It does not enumerate every RNG or certify unlisted models.

The report separates a matching device, driver metadata and an available device
node/interface. On Windows, a card without the expected interface is still
listed, making missing driver binding visible. Driver version/package metadata
is reported when readable; absent values mean unknown. Neither a loaded driver
nor a present node proves access, cancellation support or entropy quality.

Linux PCI sources are mapped to existing `qrandomN` character nodes through
sysfs. Windows legacy PCI selectors match the adapter's sorted interface-path
numbering (`quantis-pci:0` through `:9`). These numbers can change after hardware
changes and are not serial-number bindings. USB paths in this first inventory
are metadata, not byte-stream sources. The experimental [USB transport](quantis-usb.md)
uses `quantis-usb:N` on supported Linux/Windows builds; Windows PCIe remains
outside the seed adapters. `probe_adapter` describes software availability,
not verified physical operation.

JSON inventory schema version 1 contains `platform`, `devices` and optional
`warnings`. Device records include identity, model/family, driver/version/package,
available paths, binding state and an optional supported `source` selector.
Device identities and serial numbers are local operator metadata; they are not
automatically exposed through an HTTP endpoint.

## Probe one explicit source

```sh
# Linux: the installed legacy driver is required.
entropy-server hardware probe -device=/dev/qrandom0 -bytes=64 -timeout=3s -json

# Linux OS control; this does not probe a physical QRNG.
entropy-server hardware probe -device=/dev/urandom -json
```

In PowerShell, prefix the executable with `.\`:

```powershell
.\entropy-server.exe hardware probe -device=quantis-pci:0 -bytes=64 -timeout=3s -json
.\entropy-server.exe hardware probe -device=windows-csprng -json
```

Probe always requires a nonempty explicit source. Hardware/device paths use
strict selection and never substitute an OS RNG. Explicit OS controls are
labelled `os_csprng_control`. An exclusive Windows board may be busy while a
server/vendor utility holds it; probe does not interrupt that owner.

The operation queries read-only health/version fields when the backend exposes
them, then accepts one complete sample and clears it. Linux Quantis queries are
issued only after verifying the **opened descriptor's** identity through sysfs,
with different four-module/twelve-sensor masks for legacy/newer hardware. An
unknown generic device is not sent guessed Quantis IOCTLs. Unsupported health
information remains unavailable, not implicitly healthy. No reset, mode change,
module-enable or initialization writes are issued. Disabled/unhealthy modules
or failed status queries stop the sample probe.

Samples are never printed, hashed, saved or used to initialize a serving DRBG.
`bytes_accepted` counts the complete accepted sample, not physical USB/DMA traffic;
it is zero for partial/late/failed samples. The requested size is 32..1048576
bytes; timeout is 1ms..5m. JSON includes opened-source identity, optional device
metadata, raw status values, elapsed time and errors. Raw board/driver version
numbers use the vendor ABI; installed Windows driver version is separate OS
metadata. JSON errors remain on stdout as a report with a nonzero exit code;
usage errors go to stderr.

Exit codes: **0** inventory completed or sample accepted, **1** discovery/probe/
output failed, **2** invalid usage. No detected devices is a successful empty
inventory, not evidence that the OS RNG is absent.

## What success establishes

A successful probe establishes that the selected reader supplied a complete
sample under the observed conditions. It does not establish cryptographic
quality, quantum origin, certification, recovery from faults or cancellation
of a stalled kernel operation. The report explicitly keeps `quality_assessed`
and `cancellation_tested` false.

Health and sample work share one budget, including short reads. Cancellation
requests expire read deadlines; late/partial samples are cleared. Discovery,
open and status/kernel calls depend on the OS/installed driver's behaviour, so
the timeout is **not an unconditional process deadline**. Synchronous OS RNG
calls cannot be interrupted in flight. A broken hardware driver can block
cancellation/completion; no read goroutine is abandoned to simulate a timeout.
Physical-device fault testing is still required before operational reliance.

Tests use fixture sysfs/identities, read-only request fixtures, actual OS controls
and deadline-capable pipes (loopback TCP on Windows). They cover missing drivers,
model-specific health, partial/error reads, budget exhaustion, source identity,
sample non-disclosure and closure. Native Windows and packaged ZIP CLI checks
run without a QRNG; they do not certify a physical card.

[Experimental ChaosKey](https://github.com/fantuz/entropy-service/blob/main/docs/chaoskey.md)
is selected with `chaoskey-usb:N`. Linux inventory distinguishes its existing
kernel `kernel_paths` from raw USB transport `paths`; one does not imply the
other's read contract. The cooked protocol has no documented runtime health
query, so a successful sample still reports `health_available: false`.

Use the [qualification matrix and physical-record template](https://github.com/fantuz/entropy-service/blob/main/docs/hardware-qualification.md)
to record device identity, host/driver versions, source throughput and observed
cancellation separately. A passing probe alone does not complete qualification.
