# Windows Service

The Windows x64 server can run under the Windows Service Control Manager (SCM).
The console and GTK GUI remain available. This adds native service commands to
the portable executable; it does not bundle GTK, an MSI installer or signing.
Use a build containing these commands; v2.3.1 predates service support.

## Install and start

Extract the release ZIP, verify its SHA-256 checksum, and place its contents in a
permanent administrator-controlled directory such as
`C:\Program Files\Entropy Service`. Open **PowerShell as Administrator**:

```powershell
cd 'C:\Program Files\Entropy Service'
.\entropy-server.exe service install -self-signed
.\entropy-server.exe service start
.\entropy-server.exe service status
```

Installation registers `EntropyServer` for automatic delayed startup after boot.
It does not start it immediately. It uses the dedicated virtual account
`NT SERVICE\EntropyServer`, without a password or LocalSystem privileges. Do not
move/delete the executable while installed. No firewall or trust-store changes
are made. A user-writable binary directory is unsuitable for a persistent service.

Default service listeners are HTTP `127.0.0.1:8080` and HTTPS `127.0.0.1:8443`.
Open <https://127.0.0.1:8443/> after explicitly trusting the public certificate.
For an initial HTTP-only test, install with:

```powershell
.\entropy-server.exe service install -- -enable-https=false
```

Choose one installation example. Reinstalling over an existing service fails
without changing it. To change the registration, stop and uninstall it first.
The `--` separates service-management flags from normal server flags:

```powershell
.\entropy-server.exe service install -name=EntropyLab -state-dir='C:\ProgramData\EntropyLab' -self-signed -cert-hosts=localhost,127.0.0.1,::1 -- -http=127.0.0.1:8081 -https=127.0.0.1:8444 -max-bytes=67108864
.\entropy-server.exe service start -name=EntropyLab
```

All ordinary server options retain their validation and semantics. OS CSPRNG
seeding is the Windows default. Hardware drivers/devices must be accessible to
the service account. An interactive user's PATH and drive mappings may differ;
use absolute paths for external tools and operator-provisioned files.

## State, certificates and logs

The default working directory is `C:\ProgramData\entropy-service`. Relative
server paths resolve there, independently of SCM's initial working directory.
Installation creates a protected directory granting full access only to SYSTEM,
Administrators and this service account. The parent must exist and be controlled
by the operator. Existing directories with different ACLs and paths traversing
junctions/reparse points are rejected; existing permissions are never repaired
automatically. A directory retained after uninstall can be reused by the same
service name/account.

`-self-signed` is an explicit provisioning choice. At startup the service creates
the pair as its own account, or verifies/reuses an existing valid pair. The
private key keeps the existing protected, single-account ACL. Public certificate
and log ACLs also permit Administrators/SYSTEM to read them. A key generated in
an administrator console has a different owner; do not copy it into this mode
and expect the strict reuse check to accept it.

The default identity covers localhost and both loopback addresses and lasts
seven days. It is never overwritten or automatically renewed. Partial, expired,
mismatched or unsafe pairs fail startup. Stop the service, archive/remove an old
self-signed pair explicitly, then start it to request a new one; clients must
trust the new public certificate. An administrator can manage the files using
Windows ownership/ACL tools when necessary. Never distribute the private key.
For a CA-issued identity, omit `-self-signed`, supply `-cert-file`/`-key-file`,
and explicitly grant the service account read access. Monitor expiry and renew
according to your CA policy. See [TLS management](client-tls.md).

Each start creates a unique `server-<UTC timestamp>-<PID>.log`, containing the
startup invocation report and normal server diagnostics. From an elevated shell:

```powershell
Get-Content (Get-ChildItem 'C:\ProgramData\entropy-service\server-*.log' | Sort-Object Name | Select-Object -Last 1).FullName -Wait
```

Logs are retained across restarts and uninstall. Configure operator retention;
there is no automatic log rotation or disk quota. SCM reports startup failures
with a service-specific error; inspect these logs for the cause. A failure before
the log opens can indicate an inaccessible executable or unsafe state directory.

## Stop, upgrade and uninstall

```powershell
.\entropy-server.exe service stop
# Replace the stopped executable with the verified newer build at the same path.
.\entropy-server.exe service start

# To remove the registration:
.\entropy-server.exe service stop
.\entropy-server.exe service uninstall
```

Stop/shutdown cancels the same context as console termination, drains listeners
and WebSockets, joins reseeding and writes the serving summary before SCM sees
the process exit. Control commands wait up to 35 seconds; a timeout reports an
error without forcibly killing the process. Uninstall refuses a running service
and retains all state, keys and logs. Use the installed executable for management:
commands refuse registrations that do not match its executable/account/arguments.
Do not use service control on a server started by the GUI or console.

SCM **Running** means startup and listener binding completed. It does not certify
seed freshness: use `/ready` and `/metrics` for reseed failures and generation
availability. No automatic failure/restart loop is configured by this installer.

## Validation

Native Windows unit tests cover readiness, interrogation, Stop/Shutdown joining,
late readiness during cancellation and failed startup. A required packaged-ZIP
CI test installs an isolated service under a unique name from a protected path
containing spaces, checks verified HTTPS, private key access, restart/reuse,
failed startup, unsafe state rollback and preserving uninstall. It removes only
its own fixtures. Linux retains its systemd, regression and container checks.
Physical hardware access and real reboot behavior still require operator tests.
