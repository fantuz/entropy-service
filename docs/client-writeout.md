# Client write-out: Linux pool mixing

`entropy-client -writeout` is an opt-in Linux experiment that writes received
bytes to `/dev/urandom`. It is **off by default** and normally unnecessary.
It is not an entropy-crediting service, an early-boot seed mechanism, a way to
speed up `/dev/random`, or evidence that the local random generator became
more secure.

## What it actually does

Linux accepts writes to its random devices as additional input to its pool.
Ordinary writes do **not** increase the kernel's entropy count. This differs
from the privileged `RNDADDENTROPY` ioctl, which this client never uses.
Mixing can affect subsequent pool extraction/reseeding; the client neither
forces an immediate CSPRNG reseed nor proves a change in subsequent outputs.
See [Linux random(4)](https://man7.org/linux/man-pages/man4/random.4.html) and
the kernel's [random write implementation](https://github.com/torvalds/linux/blob/master/drivers/char/random.c)
(`random_write_iter` / `write_pool_user`).

Modern Linux already has its own initialized CSPRNG. Most users should leave
this option off and use the service's HTTP/WebSocket bytes directly when
their application needs remote data. It has limited value for controlled
research into uncredited pool input, or as an explicit supplemental input
from a separately trusted source. That value depends on the actual source
and deployment; the software DRBG output is not a new physical measurement
for each byte. A same-machine/self-link offers no independent source.

Fetching over TLS already depends on a working local RNG. This client cannot
bootstrap an uninitialized machine securely. Use the operating system's
supported boot seeding/getrandom mechanisms, and an appropriately designed
entropy service if genuine entropy crediting is a requirement.

## Behavior and failure handling

- Open the fixed `/dev/urandom` destination once before preflight/network
  sampling. Fail immediately if unavailable or forbidden; non-Linux builds
  reject `-writeout` explicitly.
- Open without creating/truncating files or following a symlink. Check the
  actual file descriptor is Linux's urandom character device, major/minor
  `1:9`, before any write. Other devices, ordinary files and replaced symlinks
  are rejected. This is defense in depth, not protection from a compromised
  kernel or administrator controlling the process's device namespace.
- Mix every nonempty frame handled during the measurement, including frames
  skipped by analysis sampling and bytes outside the analysed prefix.
  Preflight samples and queued frames discarded at shutdown are not mixed.
- Split writes into at most 32 KiB chunks, check cancellation between chunks,
  and account for partial writes. The descriptor is opened nonblocking;
  cancellation is not a hard deadline for a kernel call already running.
- Fail the run on device write/short-write/close errors, preserving the error
  and a nonzero process exit. Avoid the former repeated log-and-continue loop.
  The device closes before the final JSONL summary, so close errors count.
- Normal deadline/interruption cancellation can leave a partially mixed
  frame; accepted bytes remain counted and are not rolled back. A distinct
  device error is still a failure even if cancellation happens concurrently.

The earlier implementation could write to a standard Linux device but reopened
it for every frame, discarded close errors, and logged failures while allowing
successful termination. It also did not expose successful write progress.
Those limits made it unsuitable for unattended use expecting confirmed output.

## Security and resource boundaries

The remote server and transport are additional trust and availability
dependencies. Use a trusted source over authenticated HTTPS/WSS with normal
certificate validation and deployment access controls. The standalone server
does not provide client authentication. Do not infer trust from a high Shannon
estimate or from a successful HTTP request. Predictable or attacker-chosen
bytes are not verified entropy; diagnostics do not gate or certify mixing.

Under Linux's intended mixing construction, additional input is not treated
as credited entropy, even when its contents are known. This does not establish
security under an arbitrary compromised OS, unknown kernel changes or an
untrusted local device namespace. Avoid using this option as a cryptographic
assurance claim.

No extra privilege is requested. Normal Linux device permissions may already
allow writing; a denied policy is a failure, not a reason to run the whole
network client as root or relax device permissions. No raw received bytes
are stored in reports or logs. The client retains transport bounds and uses
backpressure; mixing every frame adds CPU/kernel work and can reduce reception
throughput. Increasing input volume does not imply a security improvement.

## Running and observing it

For a deliberate test with a trusted, certificate-validated endpoint:

```sh
./bin/entropy-client -mode=stream -wsurl=wss://your-trusted-server/stream \
  -slice=65536 -refresh=500ms -writeout -headless -duration=10s \
  -report=writeout-test.jsonl
```

JSONL schema version 1 adds these cumulative sample/summary counters:

| Field | Meaning |
| --- | --- |
| `device_written_bytes` | Bytes accepted by the device, including partial frames. |
| `device_written_frames` | Frames whose entire payload was successfully written. |
| `device_write_errors` | Device open, write and close errors; ordinary context cancellation is excluded. |

`settings.writeout` records the requested flag. Disabled runs report zero
device counters. Open failures produce `startup_error` before measurement;
write failures produce `writeout_error`; a close failure changes an otherwise
successful termination to `writeout_error`. Existing transport/report errors
remain errors if a close also fails. A final `failed: false` with zero device
errors means the requested writes completed up to normal cancellation, not
that their contents had verified entropy. Compare byte/frame counters to
reception counters, especially after interruption. A broken report destination
cannot guarantee a summary; stderr and the process exit remain authoritative.

## Validation

`go test ./cmd/entropy-client/...` and its `-race` variant exercise both
transports using synthetic write sinks: all received bytes versus analysed
prefixes, open-once/close-once ownership, partial writes, short writes, denied
opens before network activity, close errors, and cancellation concurrent with
real errors. Device tests reject files, symlinks, missing paths and `/dev/null`
without modifying them. These routine tests do not inject bytes into the host.

An explicit Linux integration check writes only a 64-byte synthetic sample:

```sh
ENTROPY_TEST_URANDOM=1 go test ./cmd/entropy-client/internal/device \
  -run TestLinuxMixOnlyOptIn -count=1 -v
```

This was run successfully on Linux 6.18.57, Go 1.26.10, as the ordinary user.
It confirms descriptor validation, accepted byte count and close behavior;
it does not measure kernel entropy or certify the remote source. No entropy
crediting ioctl or device-permission change is part of the check.

A separate end-to-end CLI run used a local HTTP fixture, `-slice=64
-refresh=500ms -duration=750ms -headless -writeout -report=-`: one received
frame, 64 accepted device bytes, one completed device frame, zero errors and
a successful summary. This was also an explicit bounded integration check,
not part of the routine host-independent test suite.
