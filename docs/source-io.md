# Seed input deadlines and device migration

`entropy-server -source-timeout=3s` bounds each complete initial/reseed read,
including short reads and CPU mixing. Values from 1ms to 5m are accepted. A
partial/timed-out seed is cleared and never applied. Initial failure prevents
startup; a reseed failure records the error and follows `-on-reseed-failure`.
A later successful read of the same source restores freshness. Runtime errors
do not select a new source or create another reader.

On Linux the device is opened with `O_NONBLOCK | O_CLOEXEC`. Its opened file
must be a character device and support Go read deadlines through the runtime
poller. Cancellation expires that descriptor's deadline, interrupts its wait,
and joins the cancellation callback before clearing the deadline for reuse.
There is no abandoned read goroutine. The single reseed worker is joined before
root-generator cleanup on shutdown.

Linux `/dev/urandom` is an explicit exception to the poll requirement after
OS random initialization. Before accepting either Linux random device (1:8 or
1:9), a one-byte `getrandom(GRND_NONBLOCK)` readiness check must succeed. This
avoids early-boot device waits, including kernel versions whose random device
ignores `O_NONBLOCK` during initialization. A not-ready OS source fails startup;
its state is never used as seed material. The urandom exception requires the
opened descriptor to have device number 1:9, regardless of pathname. Requests are bounded to 32 KiB chunks
with context checks. If optional startup fallback cannot open urandom, Linux
`getrandom(GRND_NONBLOCK)` supplies OS bytes; before OS initialization it returns
an error rather than an indefinite wait. This deliberately requires OS
initialization even when urandom would otherwise return early-boot bytes. No seed contents are logged.

## Hardware compatibility

This is a deliberate compatibility change for hardware drivers without
poll/deadline support. With `-require-device=true` startup fails clearly; with
optional source selection, the existing OS fallback policy applies and reports
the actual selected source. Do not treat fallback as successful hardware use.

Before deploying against a QRNG, verify its driver honors `O_NONBLOCK`, reports
read readiness correctly, and permits deadlines/cancellation through the Go
poller. A driver ignoring these contracts can still block inside a kernel call;
userspace cannot guarantee interruption of arbitrary broken or uninterruptible
kernel code. Registration with the poller alone does not prove driver behavior.
This implementation does not claim physical-QRNG verification in CI. A driver
without these facilities needs a driver-specific adapter or driver changes,
not a timeout implemented by leaving reads running in background goroutines.

Retain strict hardware selection and verify reported `X-RNG-Source`/`/ready`
on the target host. Exercise device starvation, disappearance and SIGTERM while
waiting for input. Adjust the timeout to the documented device latency; its
deadline covers the entire seed, rather than restarting for each short read.
No automatic reopen is attempted. Normal release targets are Linux; other
platforms refuse bounded device seeding until a supported adapter exists.

Focused tests use real OS pipes to verify interrupted waits, partial-read
discard, deadline reset/recovery, total read budget and joined reseed shutdown.
They also check rejection of unsupported character devices and real urandom/
nonblocking OS fallback reads. GitHub CI tests the actual packaged service.
