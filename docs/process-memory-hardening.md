# Protecting Entropy Service process memory

> **Threat addressed:** disclosure of DRBG keys, seed material, recently generated
> output, uploaded files or TLS state through process memory, crash artifacts,
> debugging interfaces, swap and privileged inspection.
> **Scope:** Linux/systemd deployment guidance. This is **defense in depth**,
> not a claim that a Go process is reliably zeroizable or resistant to root.

## Recommended protection layers

| Layer | Recommended action | Caveat |
| --- | --- | --- |
| Service identity | Dedicated non-root `entropy-service` account; access to **only** the selected device through a narrow group/ACL | Existing packaged unit currently uses `User=root`; do not switch users before verifying device and TLS key permissions. |
| Exposure | Listen on loopback or private VLAN; terminate authenticated TLS upstream if remote users are allowed | Application has no built-in per-user authentication. |
| Debugger/process observation | Restrict interactive users, remote shell access, ptrace and `/proc` process disclosure; keep pprof disabled outside controlled labs | The host administrator or kernel still controls process memory. |
| Crashes | Per-service `LimitCORE=0` plus review of host coredump collector | Linux **ignores RLIMIT_CORE when core_pattern pipes dumps to a program**. |
| Memory hygiene | Clear application-owned seeds, hashes and key/nonce fields on exit/rekey/close | Compiler/runtime and stream-cipher internals can retain copies. |
| Swap & host snapshots | Restrict swap, hibernation, VM snapshots, backups and snapshot access according to your environment | Swap encryption and swap disabling are host-wide operational choices. |
| Logs | Never log seeds, keys, generated secret outputs, request bodies or TLS private keys; keep journal ACLs narrow | Operational error messages/source paths are still sensitive infrastructure metadata. |
| Limits | Apply appropriate memory/CPU quotas and admission budgets with measured headroom | `MemoryMax` is a service availability control, **not** cryptographic memory protection. |

## Conservative opt-in systemd drop-in

The repository contains an **example**, not an automatically enabled
deployment policy:

[`resources/systemd/entropy-server-memory-hardening.conf`](../resources/systemd/entropy-server-memory-hardening.conf).

To opt in *after reviewing it*, install as
`/etc/systemd/system/entropy-server.service.d/20-memory-hardening.conf`
(adjust the unit name to your package), run `sudo systemctl daemon-reload`,
restart during a maintenance window, and inspect
`sudo systemctl show entropy-server -p LimitCORE -p NoNewPrivileges -p UMask
-p ProtectSystem -p ProtectHome`.

The example sets `LimitCORE=0`, `NoNewPrivileges=yes`, `UMask=0077`,
`PrivateTmp=yes`, `ProtectSystem=strict`, `ProtectHome=true`,
`ProtectKernelTunables=yes`, `ProtectKernelModules=yes`,
`ProtectControlGroups=yes` and `RestrictSUIDSGID=yes`. Some
applications require writable configuration/state paths; review any need
for `ReadWritePaths=` before enabling `ProtectSystem=strict`.
This snippet intentionally **does not** enable `PrivateDevices=yes`,
because that hides or restricts hardware entropy device access; nor does
it change `User=root` automatically. Moving to a dedicated user is
recommended **as a separate, deliberate deployment step**.

### Dedicated service identity

As a planned administrator action, create a locked system account, grant
only the necessary read access to your actual entropy device and
certificate/key, and override `User=`/`Group=` in a separate drop-in.
For instance, *only after provisioning the correct permissions*:

```ini
[Service]
User=entropy-service
Group=entropy-service
# SupplementaryGroups=your-specific-device-group
# ReadWritePaths=/var/lib/entropy-service   # only if genuinely needed
```

Do not hard-code `dialout`, `video`, `root` or a guessed device group.
The exact device ACL, group name and udev rule depend on your QRNG hardware
and driver. Confirm the service can open the **actual selected** device
without falling back unexpectedly. Use `-require-device=true` when the
configured source must be present at startup.

### Why LimitCORE alone is not enough

Inspect the kernel crash collector:

```sh
cat /proc/sys/kernel/core_pattern
systemd-analyze cat-config systemd/coredump.conf
systemctl show entropy-server -p LimitCORE
coredumpctl list
```

If `core_pattern` starts with `|`, the kernel pipes dumps to a program
and **RLIMIT_CORE / LimitCORE can be ignored** for that path.
`systemd-coredump` ordinarily stores compressed dumps; `Storage=none`
alone can still temporarily write a dump for processing. On a host where
**all services** must forgo crash dumps, the administrator can configure
`/etc/systemd/coredump.conf.d/90-no-coredumps.conf`:

```ini
[Coredump]
Storage=none
ProcessSizeMax=0
```

**This is global host policy**, not a per-Entropy-Service switch. It
prevents systemd-coredump from processing or retaining normal dumps (other
than limited log records), and removes an important diagnostic capability
for other services. Review existing crash artifacts, journal retention and
host-specific crash tools before changing it.

Do **not** test crash suppression by intentionally crashing your production
entropy server. Use an isolated test program/unit and verify the collector
and journaling behavior.

References: [Linux core(5)](https://man7.org/linux/man-pages/man5/core.5.html),
[systemd coredump.conf](https://www.freedesktop.org/software/systemd/man/latest/coredump.conf.html),
[systemd-coredump](https://www.freedesktop.org/software/systemd/man/latest/systemd-coredump.html).

## Optional future memory features — not enabled

- **`prctl(PR_SET_DUMPABLE, 0)`** on Linux: process-specific dump suppression
  and stronger same-user ptrace barriers, but changes debugging and some
  `/proc` behavior. Consider only as an opt-in, tested code change with
  build tags; it does not prevent root/kernel access.
- **`mlock` / `mlockall`**: may reduce swapping of particular memory pages,
  but Go's moving/copying opportunities and cipher allocations prevent
  complete locked-down key custody. `mlockall` can consume large amounts of
  locked memory under pressure and harm availability. Prefer a carefully
  isolated/key-specific design if needed, not a blanket switch.
- **`MADV_DONTDUMP`**: useful for explicitly allocated sensitive regions,
  but ordinary Go heap keys and opaque cipher objects are not confined to
  such an allocation. Could be evaluated with a redesign.
- **`GOTRACEBACK=none`**: limits runtime traceback output, but does **not**
  disable OS coredumps and costs valuable crash diagnostics.

## Verification checklist

1. Confirm service identity, device ACL and TLS private-key permissions.
2. Verify `/proc/sys/kernel/core_pattern` and active systemd-coredump
   settings; `LimitCORE=0` is only one layer.
3. Confirm no unexpected write denial under `ProtectSystem=strict`.
4. Verify readiness source labels match the actually opened device.
5. Smoke-test HTTP/WebSocket, reseeding, logs and shutdown after hardening.
6. Check `coredumpctl`, journal retention and backup/snapshot restrictions.
7. Document host-level policy separately from application-level code changes.

**Residual risk:** a compromised privileged administrator, kernel, remote
debugger, hypervisor or process-memory-reading attacker can still compromise
active state. The current Go cipher implementations are not certified to
erase their opaque internal state.
