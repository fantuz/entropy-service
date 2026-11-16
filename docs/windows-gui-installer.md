# Bundled Windows GUI installer

The unsigned Windows x64 GUI installer includes both desktop views, both Go
utilities and private Python/GTK libraries. Users do not need Go, Python, MSYS2
or a separately downloaded source archive. It is separate from the small portable
CLI ZIP and does not change Linux packaging.

This installer is being introduced after v2.4.0. PR/manual workflow builds are
development artifacts, not stable releases. Download the
`windows-gui-installer-and-sources` artifact from a successful **Windows GUI
installer** workflow. It contains the setup `.exe`, runtime source companion ZIP
and `checksums.txt`. Verify the setup checksum before running it:

```powershell
Get-FileHash .\entropy-service_*_windows_amd64_gui_setup.exe -Algorithm SHA256
```

The installer is **unsigned**. Windows may show Unknown publisher or SmartScreen
warnings. Authenticode signing, publisher verification and reputation are not
provided by HTTPS certificates or file checksums. Signing remains a separate
deployment decision; do not disable Windows security controls to install it.

## Install and use

Run setup normally. It installs for your current user under
`%LOCALAPPDATA%\Programs\Entropy Service` and adds two Start menu entries:

- **Entropy Server**: Options, Start/Stop for a local process, or Connect to live
  health from a separately started server.
- **Entropy Client**: Options, Start/Stop acquisition and fullscreen waterfall.

The server shortcut starts with the local demonstration settings: loopback HTTP
`127.0.0.1:8080`, HTTPS disabled. Click **Start** to launch it. Use Options to
configure HTTPS and certificate paths before remote deployment. This shortcut
default differs from the ordinary CLI, which still enables HTTPS by default.
The client initially connects to the local HTTP server. Neither shortcut starts
a background service automatically.

GUI-created relative paths and caches use `%LOCALAPPDATA%\Entropy Service`.
Certificate provisioning and client exports retain the CLI's existing policies.
Startup failures are written to `gui-startup-error.txt` there, with a Windows
message identifying the file. Installed source modules are under `app`, CLI
executables under `bin`, and the private runtime under `runtime`.

Install/uninstall requires no administrator elevation. Windows Service
installation remains explicit and separate: use an administrator-controlled
permanent binary location and the [service guide](windows-service.md). A per-user
GUI installation is unsuitable as the executable location of a persistent
administrator-installed service.

Close the GUI windows before upgrading or uninstalling. Use Windows Apps or the
installed uninstaller. Application/runtime files and shortcuts are removed;
user exports, certificates and data outside the installation are retained.

## Runtime provenance and source

The build installs GTK/Python through MSYS2's verified package mechanism and
copies the UCRT64 prefix intact to preserve DLLs, typelibs, schemas, fonts and
licenses. It is deliberately larger than the CLI ZIP. `bundle-manifest.json`
records every runtime package/version/license and exact corresponding source
archive URL and SHA-256. Package metadata and upstream notices remain installed;
consult `THIRD_PARTY_NOTICES.txt` and `runtime/share/licenses`.

The companion runtime-sources ZIP contains the matching MSYS2 complete source
packages (upstream code, recipes and patches). Distribute it alongside the
installer and checksum manifest, preserving the source URLs/notices. It does not
need installing to run the GUI. The application Python source is also installed;
the manifest identifies the exact project source revision. No operator keys,
trust-store edits, firewall exceptions or signing credentials are bundled.

## Build and validation

The `windows-gui-installer.yml` workflow builds both Go companions from the same
revision, stages Python/GTK and complete source packages, and compiles an Inno
Setup 6 per-user installer. It installs into a path containing spaces, removes
access to the original MSYS2 UCRT64 prefix, strips build-time search paths and
uses the installed runtime to exercise both GTK views, acquisition/fullscreen,
owned-process controls and shutdown. It then uninstalls and checks that user
data is retained. Successful installer/source artifacts and diagnostic evidence
are retained for 14 days. PR builds do not publish releases.

Signing, stable release attachment and Windows ARM64 qualification remain
separate steps. Native Windows x64 is the supported installer target.
