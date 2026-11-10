# Packaged service: operator-controlled deployment (Patch C)

The DEB/RPM package installs both binaries and a **disabled-by-default**
systemd unit. Installation no longer starts, enables or restarts the server.
Package removal stops/disables the service; an upgrade preserves the current
enablement/running state but the new unit policy applies on the next restart.
Review overrides and device access **before restarting an existing installation**.

## Conservative profile

The unit uses `DynamicUser=yes` and `User=entropy-service`: systemd supplies
an unprivileged service UID/GID at runtime (unless an existing named user
already exists). No persistent user/group, home directory or group membership
is created by the package. The process is never run as root. It has no Linux
capabilities and cannot write arbitrary directories. `LimitCORE=0` disables
the normal core-file size, but does **not** prove that all runtime memory copies,
swap or privileged external crash collectors are inaccessible.

The packaged server binds **only** to `127.0.0.1:8080` over HTTP, with
HTTPS disabled. There is no built-in authentication or authorization.
The default source is explicitly `/dev/urandom`, required for startup.
This is an OS CSPRNG source, not evidence of a tested physical QRNG.
The unit permits reading /dev/urandom through the systemd device cgroup and
does not edit host device permissions. It does not alter the standalone
binary's default listener/source settings.

## Before enabling for the first time

For an explicitly selected HTTPS deployment, see [certificate generation and
the opt-in startup provisioning drop-in](tls-certificates.md#provision-at-service-startup).
The package never installs a shared private key or silently generates an identity.

Inspect the effective configuration and start only when satisfied:

```sh
sudo systemctl cat entropy-server
sudo systemctl enable --now entropy-server
systemctl status entropy-server
curl --fail http://127.0.0.1:8080/live
```

For a **previously installed** package, check `systemctl is-enabled` and
`systemctl is-active` before upgrading: existing systemd enablement and a
running process can survive the package upgrade. The new security policy takes
effect on the next restart. Never assume an upgrade automatically removes
external access or invalidates old overrides.

## Hardware-specific operation: explicit opt-in

> **Hardware verification deferred:** As of Patch C, access to `/dev/qrandom0`
> has **not** been verified on the intended QRNG-equipped host. This does not
> block an OS-CSPRNG installation: the packaged unit intentionally selects
> `/dev/urandom` and does not touch `/dev/qrandom0`. Do **not** change its
> `-device` argument to the hardware device until the checks below succeed.
> An unprivileged service with `-require-device=true` will **fail startup**
> if the configured device cannot be opened; it will not silently fall back.

**On the QRNG-equipped host, when it becomes available:**

```sh
# Confirm that the driver created a usable character device.
test -c /dev/qrandom0 && echo "QRNG node found" || echo "QRNG node missing"
stat -c '%F %A %U:%G %a %n' /dev/qrandom0
ls -l /dev/qrandom0

# Inspect the effective service identity and sandbox, including local overrides.
sudo systemctl cat entropy-server
systemctl show entropy-server -p DynamicUser -p User -p Group -p SupplementaryGroups -p DevicePolicy
getent group qrng || true
```

Interpret the results before editing anything:

| Check | Expected action if it fails |
| --- | --- |
| Character device absent | Check the PCI card and kernel driver; don't add a permissive fallback pretending hardware is available. |
| Device accessible only to root | Assign the device a restricted, *persistent* access policy through operator-managed udev/group settings; do not use `chmod 666`. |
| Correct Unix group but denied inside service | Add/verify both `SupplementaryGroups` and a matching `DeviceAllow` drop-in. |
| Device read fails or blocks | Diagnose driver/device separately; do not weaken the default service policy just to obtain a green status. |
| Hardware-required service cannot start | Keep the default `/dev/urandom` profile for ordinary operation until QRNG access is established; use strict hardware mode only once ready. |

Do not assume a root-readable QRNG device is available to the service's
unprivileged identity. Check its real owner, group, mode, and driver behavior:

```sh
stat -c '%A %U:%G %n' /dev/qrandom0
systemctl show entropy-server -p User -p Group -p DynamicUser
```

If your deployment specifically requires a hardware QRNG, set *all* of:
1. A stable kernel/udev device node and read permissions for an appropriate
   host group (for example `qrng`), configured **outside** this package.
2. A service drop-in specifying `SupplementaryGroups=qrng` and
   `DeviceAllow=/dev/qrandom0 r` so both Unix DAC and the device cgroup
   permit reading. Confirm your hardware driver and systemd version support it.
3. An `ExecStart=` override changing the source to
   `-device=/dev/qrandom0 -require-device=true`; strict mode ensures startup
   fails rather than silently switching to a different source.

Example **template** (adapt paths, group, permissions, and certificate policy):

```ini
[Service]
SupplementaryGroups=qrng
DeviceAllow=/dev/qrandom0 r
ExecStart=
ExecStart=/usr/bin/entropy-server -http=127.0.0.1:8080 -enable-https=false -device=/dev/qrandom0 -require-device=true
```

Apply with `sudo systemctl edit entropy-server`, followed by
`sudo systemctl daemon-reload` and an explicitly approved restart.

**After configuring hardware, validate the actual source** (not just service
liveness), the service logs, and connectivity:

```sh
sudo systemctl restart entropy-server
systemctl status entropy-server --no-pager
journalctl -u entropy-server -b -n 100 --no-pager
curl -fsS -D - -o /dev/null 'http://127.0.0.1:8080/v1/data/random?bytes=32'
curl -fsS http://127.0.0.1:8080/ready
```

Confirm `X-RNG-Source` reports the intended source path and that readiness
is healthy. A success from `/live` alone does not establish that the
correct hardware source was opened, that reseeding continues successfully,
or that the hardware output has adequate entropy quality.
The package does not run `chmod`, `chown`, `udevadm`, `usermod`, or load
kernel modules. Avoid world-readable QRNG device permissions; verify with
`systemctl status` and `journalctl -u entropy-server`.

## Network exposure is a separate approval

To expose entropy-service beyond loopback, modify the `-http` listener via
a drop-in **only after** deciding on an upstream authenticated TLS endpoint,
firewall rules, quotas and monitoring. For example, a reverse proxy can talk to
127.0.0.1:8080 locally while providing authenticated HTTPS remotely.
Do not merely change `-http` to `:8080`: that would expose unauthenticated
randomness, uploads and operational metadata over plaintext.

The unit's `ProtectHome=yes` also hides private home directories. If TLS is
later enabled directly, place deployment-unique cert/key files in a location
readable by the service identity and permitted by the systemd sandbox; never
reuse public repository test keys. The process has no automatic writable state
directory, as the application currently does not require one. Adjust this
deliberately if future application changes need persistent writable state.

## Review and validation

```sh
sh scripts/test-package-service.sh
systemctl cat entropy-server
systemd-analyze security entropy-server.service
ss -lntp | grep ':8080'
```

`systemd-analyze security` is a heuristic, not a penetration test.
Packaging smoke tests inspect the unit and installation scripts without
starting services, creating Unix users, or changing devices. Test actual hardware
access and effective systemd restrictions on the target Linux system.
