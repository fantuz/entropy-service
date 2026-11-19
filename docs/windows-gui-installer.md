# Bundled Windows GUI installer

The unsigned Windows x64 GUI installer includes both desktop views, both Go
utilities and private Python/GTK libraries. Users do not need Go, Python, MSYS2
or a separately downloaded source archive. It is separate from the small portable
CLI ZIP and does not change Linux packaging.

Published v2.7.0 uses current-user installation. Repository builds containing
the service options also support an all-users installation and optional server
service, described below. The service option is off by default.

Starting with **v2.4.1**, download the Windows GUI setup `.exe` from the
[latest release](https://github.com/fantuz/entropy-service/releases/latest).
For example, v2.7.0 provides `entropy-service_2.7.0_windows_amd64_gui_setup.exe`.
Download `checksums.txt` from the same release to verify it. The versioned
`entropy-service_2.7.0_windows_amd64_gui_sources.zip` contains matching runtime
sources; it is a separate download and is not needed to run the application.

The replacement notice and Finish-page Start Menu option described below are
available from v2.7.0.

PR/manual workflow builds remain development artifacts. Their installer, sources
and checksum manifest are available together as the
`windows-gui-installer-and-sources` CI artifact. Verify the setup checksum before
running it:

```powershell
Get-FileHash .\entropy-service_*_windows_amd64_gui_setup.exe -Algorithm SHA256
```

The installer is **unsigned**. Windows may show Unknown publisher or SmartScreen
warnings. Authenticode signing, publisher verification and reputation are not
provided by HTTPS certificates or file checksums. Signing remains a separate
deployment decision; do not disable Windows security controls to install it.

## Install and use

Choose the current-user mode for a desktop installation without elevation. It installs under
`%LOCALAPPDATA%\Programs\Entropy Service` and adds two Start menu entries:

- **Entropy Server**: Options, Start/Stop for a local process, or Connect to live
  health from a separately started server.
- **Entropy Client**: Options, Start/Stop acquisition and fullscreen waterfall.

On the last setup page, **Open the Entropy Service Start Menu folder** is
checked by default. Leave it checked and select **Finish** to open the folder
containing both shortcuts and the GUI guide. Setup uses Windows' current-user
Start Menu location, including redirected profiles; the usual path is
`%APPDATA%\Microsoft\Windows\Start Menu\Programs\Entropy Service`.
Silent installations do not open Explorer.

The server shortcut starts with the local demonstration settings: loopback HTTP
`127.0.0.1:8080`, HTTPS disabled. Click **Start** to launch it. Use Options to
configure HTTPS and certificate paths before remote deployment. This shortcut
default differs from the ordinary CLI, which still enables HTTPS by default.
The client initially connects to the local HTTP server. Neither shortcut starts
a background service automatically.

GUI-created relative paths and caches use `%LOCALAPPDATA%\Entropy Service`.
Both GUI windows also offer **Open web application** for the selected server's
browser interface, including graphs, LAB tools and service status. It opens only
on a click, uses the selected server origin rather than an API route, and does
not bypass certificate verification. See the [desktop guide](desktop-gui.md).
Certificate provisioning and client exports retain the CLI's existing policies.
Startup failures are written to `gui-startup-error.txt` there, with a Windows
message identifying the file. Installed source modules are under `app`, CLI
executables under `bin`, and the private runtime under `runtime`.

Current-user install/uninstall requires no administrator elevation. The
all-users mode and optional Windows Service require administrator rights.
Per-user program directories are unsuitable for persistent privileged services.

### Optional Windows server service

Choose **Install for all users** and approve elevation, using the default
Program Files location. On the task page, explicitly select **Install
entropy-server as a Windows Service**. This is unchecked by default. A separate,
also unchecked **Start the server service after installation** option controls
initial startup. entropy-client remains a GUI/CLI application.

The first service installation requires a new directory directly inside
Program Files. Setup rejects nested paths and existing desktop-only program
directories because their inherited permissions may allow ordinary users to
replace an executable. To add a service after a desktop-only installation,
uninstall that program copy, retain its separate user data, then install afresh
with the service selected. Existing installer-managed services support normal
in-place upgrades.

The service is named `EntropyServer`, uses `NT SERVICE\EntropyServer` and
automatic delayed startup. It binds HTTP `127.0.0.1:8080` and HTTPS
`127.0.0.1:8443`. On first start it provisions a seven-day self-signed identity
under protected `C:\ProgramData\entropy-service`; renewal is manual. No firewall
or trust-store changes are made. See the [service guide](windows-service.md)
for certificate trust, renewal, state ACLs and alternative manual configuration.

Use the **Windows Services** Start Menu entry or CLI service commands to manage
the service. In the desktop server window, **Connect** monitors it; **Start**
launches a separate GUI-owned process and its options do not reconfigure SCM.

Setup manages only services it registered, with an ownership marker and the
CLI's executable/account checks. An unrelated existing `EntropyServer` is not
replaced. Upgrades stop an installer-managed service gracefully, preserve its
configuration and identity, and resume it if it was running or Start is selected.
Unchecking Install does not remove an existing service. Uninstall removes the
installer-owned registration and retains all state, certificates and logs.

To switch from an existing current-user installation to all-users mode, uninstall
the program first; retain its separate user data. Cross-mode replacement is
rejected before copying/removing files. Silent automation uses `/CURRENTUSER`
for desktop-only or `/ALLUSERS /TASKS=server_service` for explicit service
registration. Add `server_service\start` to the task list only to start it now.

### Upgrade or reinstall

The replacement setup shows a preparation message while checking running
programs, then an animated progress bar and a count of removed files/folders
while removing the previous private runtime. This phase can take time because
Python/GTK contains many small files. It is cleanup inside the new setup, not
a separate uninstaller. Normal installation progress follows when copying the
replacement files. Cleanup continues to process Windows messages, and a file
that cannot be removed produces an actionable error rather than an indefinite
blank wait. The installed `LICENSE` is unchanged; its wizard display reflows
paragraphs to avoid isolated final words.

Setup detects previous bundled installations for the current user through the
existing Windows Apps registration. It adds one **Existing installation found**
page showing the installed version, setup version and installation folder.
This also applies to reinstalling the same version. Cancel before installation
to keep the existing copy unchanged; continuing replaces the old application
and bundled runtime without a separate manual uninstall or a second Apps entry.

Close the GUI windows before continuing. The installer-owned `app`, `bin`,
`runtime`, `runtime-package-metadata` and `docs` trees are replaced, including
obsolete DLLs/Python files. Keep custom files outside those program trees.
The separate `%LOCALAPPDATA%\Entropy Service` data directory, certificates,
exports and other unowned files are retained. Interactive GUI/CLI processes are
not restarted automatically; the optional installer-owned service follows the
resume policy above.

Replacement waits for GUI windows and local utilities from this installation
to close. Once they are closed, setup stops leftover `gdbus`/`dbus-daemon`
helpers only when their executable belongs to this private runtime. It does
not force-close the GUI/CLI or unrelated system D-Bus processes. Windows'
normal file-in-use checks remain enabled.

An upgrade uses the existing installation folder. To relocate it, uninstall
through Windows Apps first, then choose a new location; a conflicting `/DIR`
is rejected before replacing files. Silent upgrades use the same replacement
policy and omit the interactive notice. Portable ZIP copies, other users'
installations and separately installed Windows Services are not managed by
this per-user installer.

Use Windows Apps or the installed uninstaller to remove the program. Application
and runtime files and shortcuts are removed; separate user data is retained.

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
Setup 6.7+ installer. A small native fixture first exercises the actual
upgrade notice, cancellation, old-file cleanup, location guard, reinstall,
uninstall and Explorer handoff using the production installer recipe. The full
test also installs the checksum-pinned **released v2.6.0** setup and upgrades it
with the candidate installer, checking visible/responsive cleanup and retained
user exports. Fixtures additionally cover read-only old files and a directory
junction whose external target must survive cleanup. The full
bundle then installs into a path containing spaces, removes
access to the original MSYS2 UCRT64 prefix, strips build-time search paths and
uses the installed runtime to exercise both GTK views, acquisition/fullscreen,
owned-process controls and shutdown. It upgrades that installed bundle and
repeats the GUI checks with the original runtime still unavailable, verifying
obsolete-file removal and preservation of user data, then uninstalls. It also
checks that an active runtime process blocks replacement without being killed.
An administrative fixture additionally checks the actual service task defaults,
unsafe locations and cross-mode rejection, account/startup policy, explicit
Start, running-service upgrade with the same TLS identity, and preserving
service uninstall. It refuses pre-existing installations/state and removes only
its own service fixture.
Successful
installer/source artifacts and diagnostic evidence
are retained for 14 days. PR builds do not publish releases.

Tagged releases require the installer build/install tests before publication.
The publication job downloads that run's tested setup and source archive, verifies
checksums and exact version/source identity, and publishes the same bytes with
the native CLI packages and one combined checksum manifest. No installer rebuild
occurs during publication. Signing and Windows ARM64 qualification remain separate
steps. Native Windows x64 is the supported installer target.

For the cross-platform LAB controls and the **Debug / pprof…** startup option,
see [LAB platform support](lab-platforms.md).

## Recovering a publication failure

A failure after all validation jobs succeed can be recovered without moving the
release tag or rebuilding tested GUI/container artifacts. Run the current main
`release` workflow manually with `release_tag` and `validated_run_id` set to the
original failed tag run. Leave both fields empty for ordinary release operation
on a selected tag.

Recovery checks the original workflow/event, unchanged tag commit, complete
successful validation matrix and recent evidence (within seven days). It rejects
missing, cancelled or failed validation gates. Artifacts must still be retained.
The corrected publication tools check out the exact original tagged source,
verify/reuse its tested installer and images, publish native assets and reconcile
`latest` using the existing shared promotion lock. This repairs the packaging
recipe while preserving the version's code and artifact provenance.
