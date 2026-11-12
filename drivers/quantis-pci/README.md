# Optional legacy Quantis PCI bounded-read overlay

This driver work is maintained separately from the Go server. It is an
experimental replacement for the **Linux source file** in the pinned community
port [tcdent/quantis-pci](https://github.com/tcdent/quantis-pci/tree/00fffc49306d748b34a95c14769b30908ef5985f),
commit `00fffc49306d748b34a95c14769b30908ef5985f`, for PCI **179a:0001**.
It is not a patch for arbitrary vendor 2.9 sources, a PCIe/XDMA driver, or a
hardware qualification. No kernel module is bundled in the application's
release, installed, or loaded by entropy-server.

The original source copyright and BSD/GPL notices are preserved. The build
preparer downloads only the checksum-pinned archive recorded in
[hardware-driver research](../../docs/hardware-drivers.md) and copies the
reviewable Linux overlay files over it. It never extracts archive-supplied paths
or executes the original project's installation targets.

## Read and lifetime contract

- `.poll` registers a real wait queue, so `epoll_ctl` and Go's file deadlines can
  work. It reports cached level readiness; another reader can consume bytes
  before a subsequent read, so callers must still handle `EAGAIN`.
- `O_NONBLOCK` reads return `EAGAIN` when empty or contended. Blocking reads wait
  interruptibly, without busy-waiting or holding the hardware mutex. A read
  returns an available prefix of at most 1 KiB; it never waits to fill a larger
  user request. Pending bytes from a 32-bit FIFO word survive short/odd reads.
- A delayed worker checks hardware registers every jiffy (requested 1 ms;
  actual granularity depends on `HZ` and scheduling). This is **periodic hardware
  checking**, not an assumed interrupt implementation. `.poll` is the separate
  descriptor readiness interface. No undocumented interrupt register is used.
- Module faults, test mode, suspension and removal wake waiters with an error.
  Removal reports `POLLERR|POLLHUP`, and subsequent read/ioctl calls return
  `ENODEV`. `cdev_device_add` ties character-device lifetime to the containing
  device; each open owns a reference. Removal stops/joins work and serializes
  MMIO before unmapping; open files outlive unmapping safely.
- MMIO uses `pci_iomap`, `readl` and `writel`, never `ioremap_cache` or ordinary
  pointer loads. Only BAR 1's documented 64-byte register window is accessed.
- The hardware exposes coarse quarter-full FIFO thresholds. Readiness is
  conservative: an incompletely filled quarter is not advertised. This can
  add latency for low-rate hardware; it is not a precise byte counter.

The historical driver flushed FIFO overflow and retried. Idle hardware can
fill its FIFO even when an application needs only an occasional seed. This
overlay retains recovery **between acquisitions**, discarding the entire FIFO
and any pending word before advertising fresh bytes. Recoveries are counted in
`/proc/qrandom`. Overflow or failed module health **during a read** discards the
whole current batch and returns `EIO`. Test mode and any enabled unhealthy module
are rejected. Recovery from a module fault also flushes stale pre-fault data.
These checks do not establish randomness quality or replace hardware validation.

Legacy public ioctl numbers and the numeric source-driver ABI version 2.6 are
retained; `modinfo` identifies this overlay as `2.6-entropy1`. Driver-version
numbers are distinct from the vendor package's version. Device count and minor
allocation permit hot removal and reuse without selecting an occupied slot.
`/proc/qrandom` gives this overlay's source identity/readiness/recovery counts;
it does not replicate the old verbose procfs presentation. There is no Linux
`hwrng` registration or automatic entropy-pool credit.

## Build without installing

Use Linux 6.1 or later, matching kernel headers and their supported compiler.
Prepare a fresh, user-owned directory from the repository root:

```sh
python3 drivers/quantis-pci/prepare.py /tmp/quantis-pci-build
make -C /lib/modules/"$(uname -r)"/build \
  M=/tmp/quantis-pci-build/linux modules
```

`--archive /path/to/quantis-pci-00fffc49.tar.gz` uses an already downloaded
archive and checks the same SHA-256. The prepared tree is a build product; review
the overlay in this repository. Build errors must be resolved against the actual
target kernel. BTF-enabled kernel configurations may require `pahole`; disabling
optional BTF with `CONFIG_DEBUG_INFO_BTF_MODULES=` changes only debug generation.

Installation, DKMS packaging and module signing are separate operator steps.
Do not replace or unload the active driver while a service is using the card.
Stop consumers before changing drivers, preserve a rollback path, comply with
Secure Boot/module-signing policy, and qualify the card before enabling serving.
A successful compile does not authorize installing this module on a production
machine or loading the simulation module on the host.

After a qualified installation, entropy-server's existing direct device path is
unchanged: select the observed `/dev/qrandomN` with `-require-device=true`. Follow
[source I/O](../../docs/source-io.md) and [hardware probe](../../docs/hardware-probe.md)
for timeout/error checks. The Go server does not bypass missing deadline support
for older drivers.

## Validation and its limits

The focused portable C tests run the exact bounded acquisition core with fake
registers, including empty/partial data, starvation, odd reads, FIFO overflow,
module health/test mode and failed-batch discard. Locally they run under address
and undefined-behaviour sanitizers. Both kernel modules were also compiled
against Debian Linux 6.12 headers without installation or loading.

The dedicated **Quantis PCI driver** CI checks Debian 6.1/6.12 headers and an
Ubuntu kernel. Its QEMU VM loads **only** the simulated register backend, using
the production `.open/.read/.poll/.release`, worker, ioctl, reference and removal
code. Tests exercise actual epoll registration/wakeups, nonblocking/partial reads,
signal interruption, blocking starvation, faults, removal, repeated load/use/close,
and Go `SetReadDeadline`, context cancellation and `Close`. A simulation-specific
control ioctl exists only in that test module. It cannot bind a physical PCI card.

This establishes software behaviour with a controlled backend. The cloud has no
Quantis card: PCI timing, status-bit semantics on the user's core revision,
throughput, power-management behaviour, surprise removal on a real bus and
entropy quality still require physical-device qualification. Compile/simulation
results must remain separate from hardware support claims.
