# Desktop GUI

The GTK 4/PyGObject desktop interface supports Linux, Windows x64 and macOS
Intel/Apple Silicon. See [portable archives](#portable-archives) for launch
commands and [Linux setup](#linux-setup) for installed DEB/RPM commands.
The [Windows installer](windows-gui-installer.md) bundles its runtime. The
command-line and web interfaces remain independently usable.

Both windows provide an **Options** editor for common startup settings and
**Start/Stop** for a process launched by that window. The server window also
has **Connect/Disconnect** for observing an existing local or remote server.
It does not install or control an operating-system service.

| Window | Control | What it does |
| --- | --- | --- |
| Server (`--view server`) | **Start/Stop**, at the top left | Start or stop the selected local server owned by this window. |
| Server | **Find local** | Look for a running server on the default loopback ports and monitor its health. |
| Server | **Connect/Disconnect** | Start or stop monitoring the server at the selected URL. |
| Client (`--view client`) | **Start/Stop**, at the top left | Run the selected client and display its statistics and waterfall. |
| Both | **Options** | Choose a local executable and edit startup options while its owned process is stopped. |
| Both | **Open web application** | Open the selected server's homepage in the default browser, with graphs, LAB tools and service status. |

The web application is served by entropy-server; the desktop client does not
host a separate website. The server window uses its selected health URL, while
the client window uses its validated pull/push endpoint. WebSocket addresses map
to HTTP or HTTPS, retaining the host and port and removing API paths, queries and
fragments. Nothing opens automatically and the button neither starts a server nor
changes browser certificate trust. Endpoints with embedded credentials are
rejected; open their credential-free server homepage manually instead. The
reference is hidden during the client's fullscreen waterfall.
The server GUI's `--health-ca` applies only to health polling; the default
browser uses its own certificate trust configuration.

| Server — local process and live health | Client — live waterfall |
| --- | --- |
| [![Server GUI running its local process with READY health](images/desktop-gui-server-live.webp)](images/desktop-gui-server-live.webp) | [![Client GUI receiving samples with a filled byte-hue waterfall](images/desktop-gui-client-live.webp)](images/desktop-gui-client-live.webp) |

These Linux captures use an OS-seeded local server and a WebSocket client.
Select an image for the full-size view.

## Resize the information panels

Two horizontal handles divide the main workspace into three adjustable areas.
Drag a handle up or down to give a section more room:

| View | Top | Middle | Bottom |
| --- | --- | --- | --- |
| Server | Live health | Requested startup settings | Local server logs |
| Client | Connection and performance | Waterfall and palette | Startup settings and diagnostics |

Smaller sections scroll rather than removing information. Start/Stop, Options
and the ordinary status area remain outside the dividers. GTK's standard
keyboard handling also lets you focus a divider with **F8** and adjust it with
the arrow keys.

Positions are remembered while that window is open, including when the client
enters and leaves fullscreen with **F11** or **Escape**. Fullscreen retains the
summary indicators and waterfall; normal panels and their positions return on
exit. A new window starts with the default layout. Resizing changes display
space, not acquisition settings, sample contents or the bounded waterfall
history. Adjustable panels are available from v2.8.1.

## Portable archives

From v2.9.3, extract the entire Linux or macOS TAR.GZ or Windows ZIP for your
architecture. The `entropy-gui` launcher, its `entropy_gui/` Python modules
and both CLI executables live together in the extracted directory. Run the
command for the window you want from that directory.

| Platform and desktop dependencies | Server window | Client window |
| --- | --- | --- |
| Linux with [distribution GTK packages](#linux-setup) | `./entropy-gui --view server` | `./entropy-gui --view client` |
| macOS with [Homebrew GTK](macos.md#homebrew-gui) | `./entropy-gui-macos --view server` | `./entropy-gui-macos --view client` |
| Windows in the [MSYS2 UCRT64 terminal](#install-the-desktop-dependencies) | `python ./entropy-gui --view server` | `python ./entropy-gui --view client` |

The launcher finds the matching executables beside it. The plain server
command checks the default local listeners and opens a live health view if it
finds an instance. Use **Start** to launch a local server with the settings
in **Options**. In the client window, click **Start** to receive samples.

## Source and executable versions

For a source installation, use the `tools/desktop-gui` directory from the same
release as your CLI executables. Obtain a checkout of that revision, or use
GitHub's **Code → Download ZIP** on the branch or release you are viewing.
Keep the launcher and its neighboring Python modules together.

The [v2.2.0 source ZIP](https://github.com/fantuz/entropy-service/archive/refs/tags/v2.2.0.zip)
contains the earlier GUI: it can monitor a server and run the client, but its
server settings are read-only and it has no Options editor or local-server
Start/Stop controls. Its separate
[Windows x64 binaries ZIP](https://github.com/fantuz/entropy-service/releases/download/v2.2.0/entropy-service_2.2.0_windows_amd64.zip)
contains its CLI executables. Use matching source and binary versions for the
complete set of startup options. For portable releases through v2.9.2, the GUI
was a separate source download.

## Linux setup

Install the optional desktop dependencies on the machine where you open the
windows. On Debian or Ubuntu (including Ubuntu 26.04), run

```sh
sudo apt install python3-gi python3-gi-cairo python3-cairo gir1.2-gtk-4.0
```

On Fedora, run

```sh
sudo dnf install gtk4 python3-gobject python3-cairo gobject-introspection
```

### Packaged launchers

DEB and RPM packages from v2.9.3 install the desktop launchers in
`/usr/bin` alongside the matching server and client executables. Open either
window as your normal user from a graphical session, in any directory.

```sh
entropy-server-gui
```

The plain server launcher looks for an existing instance, including a running
packaged systemd service at `http://127.0.0.1:8080`. For a custom port or
remote server, enter its base URL and click **Connect**. To launch a separate local
server, choose its listener and TLS settings in **Options** and click
**Start**. Use a free port if a service is already listening.

On Linux the server GUI defaults to `/dev/urandom`, the operating system's
random source. The standalone Linux CLI still defaults to `/dev/qrandom0`.
To use hardware in a GUI-launched server, set **Options → Seed and DRBG →
Seed source** to the device, for example `/dev/qrandom0` for a Quantis PCI
card. Select **Require selected seed device** if startup should fail when
that source is unavailable. You can also pass the choice at launch with
`entropy-server-gui -- -device=/dev/qrandom0 -require-device=true`. See the
[hardware examples](hardware-examples.md) for USB sources and device discovery.

```sh
entropy-client-gui
```

In the client window, choose the server endpoint in **Options** and click
**Start** to receive samples. Press **F11** for a fullscreen waterfall.
The launchers find their respective binaries in `/usr/bin` automatically.

### Source launcher

For DEB and RPM releases through v2.9.2, the GUI launcher is in the matching
source archive. From its extracted directory, point the launcher at the
installed binary.

```sh
./tools/desktop-gui/entropy-gui --view server --server-binary /usr/bin/entropy-server
./tools/desktop-gui/entropy-gui --view client --client-binary /usr/bin/entropy-client
```

If you also want to build the utilities from source, run the following from the
source directory with the Go version required by `go.mod`.

```sh
make build
```

Use the distribution's Python because virtual environments can hide PyGObject.
GTK supports X11 and Wayland. Native Linux CI uses X11 under Xvfb.
`--help` works without GTK or a graphical session.

Open the server GUI for loopback HTTP testing:

```sh
/usr/bin/python3 tools/desktop-gui/entropy-gui --view server \
  --health-url=http://127.0.0.1:8080 -- \
  -device=/dev/urandom -require-device=true -enable-https=false \
  -http=127.0.0.1:8080
```

Click **Start** to launch its local server, then **Connect** to check that
endpoint's live health. If a server is already running there, use **Connect**
without starting another one, or select different listener settings in
**Options**.

In a second terminal in the same source directory, open the client GUI:

```sh
/usr/bin/python3 tools/desktop-gui/entropy-gui --view client -- \
  -mode=pull -url=http://127.0.0.1:8080/v1/data/random \
  -slice=1048576 -refresh=250ms -analysis-interval=250ms
```

Click **Start** at the top left to receive samples. **F11** toggles the
waterfall's fullscreen view and **Escape** returns. An installed executable
can be selected with `--server-binary /usr/bin/entropy-server` or
`--client-binary /usr/bin/entropy-client` before `--`, or in **Options**.

## Windows setup

For a self-contained GUI, use the [bundled installer](windows-gui-installer.md).
For the portable ZIP from v2.9.3, install the [desktop dependencies](#install-the-desktop-dependencies)
and run the [portable launcher](#portable-archives).

### Source setup for earlier releases

For v2.9.2, download the matching [source ZIP](https://github.com/fantuz/entropy-service/archive/refs/tags/v2.9.2.zip)
and [Windows x64 binaries ZIP](https://github.com/fantuz/entropy-service/releases/download/v2.9.2/entropy-service_2.9.2_windows_amd64.zip).
Use matching source and executable versions as explained under
[Source and executable versions](#source-and-executable-versions).
Extract the entire source archive. Its directory must contain `go.mod`, `cmd`,
`docs/desktop-gui.md` and `tools`. You can rename it to `entropy-service-gui`
to match the examples below.

Create a `bin` directory inside it and copy `entropy-server.exe` and
`entropy-client.exe` from the binaries ZIP into `bin`:

```text
entropy-service-gui/
  bin/
    entropy-server.exe
    entropy-client.exe
  tools/
    desktop-gui/
      entropy-gui
      entropy_gui/
```

`entropy-gui` is a Python launcher **without a filename extension**. Keep its
neighboring `entropy_gui` directory; downloading the launcher alone is not
sufficient. Using released executables does not require Go. They may also
remain elsewhere if selected explicitly as shown in
[binary-path troubleshooting](#troubleshooting-startup-inspection).

### Install the desktop dependencies

Install [MSYS2](https://www.msys2.org/) and open its **UCRT64** terminal:

```sh
pacman -Syu
```

Restart the terminal if the update asks you to, then install the native
GTK/Python bindings:

```sh
pacman -S --needed mingw-w64-ucrt-x86_64-gtk4 \
  mingw-w64-ucrt-x86_64-python-gobject mingw-w64-ucrt-x86_64-python-cairo
```

Use UCRT64's `python` for the GUI commands so its GTK DLLs and bindings match.
A [bundled Windows GUI installer](windows-gui-installer.md) is available from the
[latest release](https://github.com/fantuz/entropy-service/releases/latest) and
includes these dependencies. The manual source setup below remains supported.

### Start the server GUI

In **MSYS2 UCRT64**, enter the extracted source directory. Replace `yourname`
and adjust the location if you extracted elsewhere:

```sh
cd /c/Users/yourname/Downloads/entropy-service-gui
ls tools/desktop-gui

python tools/desktop-gui/entropy-gui --view server \
  --health-url=http://127.0.0.1:8080 -- \
  -device=windows-csprng -enable-https=false -http=127.0.0.1:8080
```

The `ls` command should show `entropy-gui` and `entropy_gui`. Click **Start**
at the top left to launch the local server, then **Connect** for live health.
This example uses the Windows OS CSPRNG and loopback HTTP with HTTPS disabled
for initial testing. If another server already occupies that address, monitor
it with **Connect**, or choose a different listener in **Options** before
starting a local server.

### Start the client GUI

In another **MSYS2 UCRT64** terminal in the same source directory:

```sh
python tools/desktop-gui/entropy-gui --view client -- \
  -mode=pull -url=http://127.0.0.1:8080/v1/data/random \
  -slice=1048576 -refresh=250ms -analysis-interval=250ms
```

Click **Start** at the top left of the **client** window to receive samples
and display the waterfall. It becomes enabled after local startup inspection
succeeds. **F11** toggles fullscreen and **Escape** returns. Use **Options**
while the client is stopped to change its connection and sampling settings.

### Windows interface

The native Windows server options editor and running client waterfall:

[![Windows server options](images/desktop-gui-windows-server-options.png)](images/desktop-gui-windows-server-options.png)
[![Windows client waterfall](images/desktop-gui-windows-client.png)](images/desktop-gui-windows-client.png)

### Monitor a separately started server

A server can instead be started from **PowerShell**:

```powershell
cd "$env:USERPROFILE\Downloads\entropy-service-gui"
.\bin\entropy-server.exe -device=windows-csprng -enable-https=false -http=127.0.0.1:8080
```

Keep that terminal running, then launch the server GUI using the UCRT64
command above and click **Connect**. The GUI's **Stop** button does not stop
that separately launched process; it only controls a server started by its
own **Start** button. The v2.2.0 GUI source supports this monitoring workflow
and the client workflow, with `entropy-service-2.2.0` as its extracted folder.

### Build the executables instead

As an alternative to copying the released executables, install the Go version
required by `go.mod` and run from the source directory in PowerShell or CMD:

```powershell
go build -o bin/entropy-server.exe ./cmd/entropy-server
go build -o bin/entropy-client.exe ./cmd/entropy-client
```

Then follow the same GUI launch steps. Make and a C compiler are not required
for these Go builds.

## Edit startup options

The LAB/debug editor additions, **Edit features…** shortcut and expanded client
diagnostics are available from v2.7.0. Use the GUI and CLI executables from
the same release for these controls.

Click **Options** while the process owned by that window is stopped. Select
its local executable with **Browse**, change the fields you need, then use
**Validate and apply**. Validation runs the CLI's `-describe-json` path and
returns before opening runtime devices, listeners or output destinations.
Invalid settings stay in the editor with an explanation. Successful edits
become the options used by the next **Start**; they do not reconfigure a
running process or the server being monitored at the health URL.

The checkboxes under **Validated startup features** show the last validated
startup values. Select **Edit features…** beside them to configure LAB, debug
and cookie options; these controls also require the GUI-owned server to be
stopped. The same editor is used on Linux, Windows and macOS.

| Client options | Server options |
| --- | --- |
| Pull/push transport and separate HTTP/WebSocket endpoints | Seed source and DRBG |
| Bytes per response and analysed prefix size | HTTP/HTTPS listeners and HTTPS enablement |
| Request/analysis intervals and Shannon threshold | Existing TLS certificate and private-key paths |
| Duration, retries and retry delay | Reseed interval, maximum age, policy and reseed bytes |
| Waterfall mapping: byte hue, RGB or grayscale | Source timeout, maximum sample size, required device and optional CPU/cookie flags |
| Up to 32 MiB per response; 0 analyses the full frame | LAB registry file, trusted Host names, remote-access opt-in, bridge and memory limits |
| Automatically opened failure diagnostics | Local debug/pprof listener and HTTPS browser-preference cookies |

[![Client options editor](images/desktop-gui-client-options.png)](images/desktop-gui-client-options.png)
[![Server options editor](images/desktop-gui-server-options.png)](images/desktop-gui-server-options.png)

`pull` uses the HTTP endpoint and `push` uses the WebSocket endpoint. The GUI
keeps the CLI's TLS certificate and hostname verification. For Windows client
trust, follow [client TLS guidance](client-tls.md); selecting a server's
private key is not a client trust mechanism.

For HTTPS serving, select the certificate and private key under **Listeners
and TLS**. Validation checks option syntax. The server opens the files,
listeners and seed source at startup. For a first local identity, follow
[Set up local HTTPS](#set-up-local-https).

**LAB sources and access** selects an operator-prepared registry JSON file.
Clear its path to disable registered sources; browser-local LAB tools remain
available. Registry contents are read only at server startup. Registered
bridges stay loopback-only by default; additional trusted Host names do not
enable remote peers. Remote access is a separate explicit option. Hardware
adapters still follow each OS's supported capabilities; choosing a registry
does not add a missing Windows/macOS device driver.

**Enable local debug / pprof listener** starts the existing debug service at
`http://127.0.0.1:6060/debug/pprof/` with the owned server. It is available on
Linux, Windows and macOS and remains loopback-only. A port already in use is
reported in local server logs. Stop closes that listener with the owned process.

**Remember HTTP preferences** controls Secure, HttpOnly browser cookies for
byte/word counts, refresh and word language. Use HTTPS for browser persistence.
It does not persist GUI startup options or change a separately monitored server.

### Large samples and client failures

**Bytes per response** accepts up to 33,554,432 bytes (32 MiB), matching the
server's default maximum; the operator can configure a lower limit. The 2 MiB
receive default and 1 MiB analysis prefix are configurable defaults, not GUI
ceilings. Set **Analysis prefix bytes** to `0` for whole-frame analysis.
The waterfall shows a bounded preview of each analysed sample, keeping GUI
memory independent of payload size.

An interval of 100 ms requests up to 10 frames/s; it cannot guarantee
320 MiB/s for 32 MiB frames. Generation, analysis and transport may run slower,
especially with direct RDRAND output. The server can remain healthy while an
individual connection times out or closes. Readiness does not measure the
client connection or promise a requested throughput.

On failure, the client reveals the reported cause and opens **Client
diagnostics**, with bounded, selectable CLI output and URL credentials/query
values hidden. EOFs/connection resets, timeouts, rejected sample sizes and
reported CPU-backend failures have different guidance. Check server logs and
compare a smaller sample before attributing a problem to the GUI. An explicitly
reported sticky RDRAND hardware failure requires an operator restart; changing
refresh does not reset it. See the [load checks and coverage notes](desktop-gui-validation.md)
for measured results and the remaining qualification gaps.

Additional CLI arguments supplied after the launcher's `--` are retained.
Editing one supported field replaces that flag without discarding unrelated
flags or changing untouched endpoint query parameters. Options are held for
this GUI session; they are not a saved configuration file. GUI raw exports
and device writes remain unsupported; use the CLI for deliberate exports.

Saved endpoint credentials, query parameters and fragments are hidden in the
editor. Leaving that field unchanged preserves the full original URL. Editing
an endpoint replaces the complete URL, including any hidden components.

## Troubleshooting startup inspection

**No configuration file is required.** Arguments after the launcher's `--`
separator describe the requested CLI invocation. To display those settings,
the GUI briefly runs the selected local executable with `-describe-json`,
which returns before opening runtime devices or listeners. It does not read
the command line of an already-running process or infer its startup options
from the health URL. Use the same CLI arguments after `--` if you want the
requested settings to match the server you started separately.

`Configuration could not be read: [WinError 2]` on Windows, or
`[Errno 2] No such file or directory` on Linux, normally means the inspection
executable could not be found. A green **READY** health panel can still appear:
network health and local startup inspection are separate operations.

In a portable archive, the launcher selects the executables next to it. In a
source checkout it selects `bin/entropy-server` and `bin/entropy-client`,
with `.exe` on Windows. These paths are relative to
the launcher location, independently of the terminal's working directory or
where you started the running server. It does not select another version from
`PATH` automatically.

The executables can be stored in another directory. Supply their paths
explicitly **before** `--`; moving or restarting an already-running server is
not required. Use native Windows paths with forward slashes and quote paths
containing spaces. For example, in UCRT64:

```sh
python tools/desktop-gui/entropy-gui --view server \
  --server-binary "C:/Users/yourname/Downloads/Entropy binaries/entropy-server.exe" \
  --health-url=http://127.0.0.1:8080 -- \
  -device=windows-csprng -enable-https=false -http=127.0.0.1:8080

python tools/desktop-gui/entropy-gui --view client \
  --client-binary "C:/Users/yourname/Downloads/Entropy binaries/entropy-client.exe" -- \
  -mode=pull -url=http://127.0.0.1:8080/v1/data/random
```

An installed Linux executable can be selected in the same way:

```sh
/usr/bin/python3 tools/desktop-gui/entropy-gui --view server \
  --server-binary /usr/bin/entropy-server --health-url=http://127.0.0.1:8080 -- \
  -device=/dev/urandom -enable-https=false -http=127.0.0.1:8080

/usr/bin/python3 tools/desktop-gui/entropy-gui --view client \
  --client-binary /usr/bin/entropy-client -- \
  -mode=pull -url=http://127.0.0.1:8080/v1/data/random
```

If the file exists but the error mentions an unknown `-describe-json` flag,
use matching v2.2.0-or-newer source and executable versions. A CLI option
validation error should be corrected in the arguments after `--`; creating a
configuration file does not resolve a missing executable or invalid option.

## Set up local HTTPS

Open **Set up a self-signed certificate** under **Live server health** for
a command using the selected server executable. Run the displayed command
from a private directory as the user who launches the server. It creates
`cert.pem` and `key.pem` there. The examples below supply full paths and
also work before the first server start.
Its default certificate covers `localhost`, `127.0.0.1` and `::1` for seven
days. With `-if-missing`, it reuses a matching valid pair on subsequent runs.

**Linux and macOS.** From an extracted portable archive, run

```sh
tlsdir="$HOME/.entropy-service-tls"
mkdir -p -m 700 "$tlsdir"
./entropy-server generate-cert -if-missing -cert-file="$tlsdir/cert.pem" -key-file="$tlsdir/key.pem"
```

For a Linux DEB or RPM installation, use `/usr/bin/entropy-server` in that
command.

**Windows PowerShell.** For the default current-user installer location, run

```powershell
$installDir = "$env:LOCALAPPDATA\Programs\Entropy Service"
$serverExe = "$installDir\bin\entropy-server.exe"
$tlsDir = "$env:LOCALAPPDATA\Entropy Service\tls"
New-Item -ItemType Directory -Force -Path $tlsDir | Out-Null
& $serverExe generate-cert -if-missing "-cert-file=$tlsDir\cert.pem" "-key-file=$tlsDir\key.pem"
```

For a portable ZIP or a custom installation, use the executable path shown
in the GUI for `$serverExe`.

To monitor the self-signed HTTPS endpoint, launch the server GUI with the
**public** `cert.pem` as its `--health-ca`. Keep `key.pem` private to the
server account. Examples using the paths above follow.

| Installation | Launch with the public certificate |
| --- | --- |
| Linux DEB/RPM | `entropy-server-gui --health-ca="$tlsdir/cert.pem"` |
| Linux portable archive | `./entropy-gui --view server --health-ca="$tlsdir/cert.pem"` |
| macOS portable archive | `./entropy-gui-macos --view server --health-ca="$tlsdir/cert.pem"` |
| Windows installer, PowerShell | `& "$installDir\runtime\bin\pythonw.exe" -I -B "$installDir\app\gui_bootstrap.py" --view server "--health-ca=$tlsDir\cert.pem"` |

In **Options → Listeners and TLS**, enable HTTPS and select the generated
files for **TLS certificate file** and **TLS private key file**. For a local
test, set **HTTP listener** to `127.0.0.1:8080` and **HTTPS listener** to
`127.0.0.1:8443`. Select **Validate and apply**, then **Start**. For an
HTTP-only first run, clear **Enable HTTPS** and use the loopback
HTTP listener. For deployment names, renewal and systemd, see the
[certificate setup guide](tls-certificates.md).

## Live server health

Opening the server GUI with its defaults runs **Find local** once. Startup
arguments selecting a standard numeric loopback listener also enable this
search, including the Windows Start Menu shortcut's local HTTP settings.
The GUI connects directly to `127.0.0.1` and `[::1]` on HTTPS
port 8443 first. If those listeners are unavailable, it can check the same
loopback addresses on HTTP port 8080. When it finds a running entropy-server,
the health panel begins polling it. Discovery only reads health information
and works even when the local server executable is unavailable.
On v2.9.3 and earlier, enter the base URL and click **Connect**.

Use **Find local** to retry after starting the service. To switch from a
monitored remote instance to a local one, select **Disconnect**, then **Find local**.
For a custom port, a remote host or a dynamically assigned listener,
enter its actual base URL and click **Connect**. An explicit `--health-url`
prefills that field and waits for **Connect**. Forwarding custom listener
ports after `--` also leaves the suggested URL available for **Connect**.
Wildcard bind addresses appear as loopback in the suggestion.

If you haven't selected a monitoring target yourself, **Start** also polls
the numeric loopback endpoint from the validated listener settings. It uses
the configured HTTPS port when HTTPS is enabled and the HTTP port when it
is disabled, including custom ports chosen in **Options**.
Discovery and automatic loopback monitoring bypass HTTP proxy settings.
Editing the health URL, supplying `--health-url`, or choosing **Connect**
or **Disconnect** preserves your selection when a local server starts.

If discovery reports a certificate verification error, use the public CA or
self-signed certificate with `--health-ca` and make sure the address is
listed in the certificate. A TLS trust failure prevents automatic HTTP
discovery. Specifying `--health-ca` restricts discovery to HTTPS. For
example, with a local self-signed server and the Linux package launcher, run

```sh
entropy-server-gui --health-ca=/path/to/cert.pem
```

If no listener responds, check that the service is running and check its
configured ports. On systemd, use `systemctl status entropy-server` and
`journalctl -u entropy-server -b`. A standalone server configured for HTTPS
can exit at startup if its certificate or key is missing. Provision its
identity with `entropy-server generate-cert` and then start it. The packaged
systemd unit defaults to loopback HTTP and supports certificate provisioning
at startup through a drop-in. [Standalone certificate generation](tls-certificates.md#generate-a-local-identity) · [systemd HTTPS configuration](tls-certificates.md#provision-at-service-startup)

The GUI reads `/live` and `/ready` approximately every two seconds. It shows:

- Selected HTTP/HTTPS endpoint reachability and process liveness.
- Actual readiness, observed DRBG and seed source.
- Seed age, reseed-failure count and whether generation is currently allowed.
- The last observation time and any connection or verification failure.

HTTP 503 from `/ready` is a valid **DEGRADED** response. A continuing generator
can be degraded while generation remains allowed; a stop-policy failure can
pause generation. The GUI displays the server's reported values without
turning a readiness failure into a transport failure. **Disconnect**, failed
requests and stale observations clear current green status and live values.
Polling does not generate entropy samples or change server configuration.

**Local server** tracks the child process the window launched. **Live server
health** reports the instance answering the selected URL. If another server
already owns that port, it can report **READY** even after the newly launched
process exits. Check **Local server** and its logs for a bind error.

HTTPS uses certificate and hostname verification. Supply an additional trusted
CA or a deliberately provisioned self-signed server certificate when needed:

```sh
/usr/bin/python3 tools/desktop-gui/entropy-gui --view server \
  --health-url=https://localhost:8443 --health-ca=/path/to/cert.pem
```

On Windows, use UCRT64 `python` and an accessible certificate path. A manual
**Connect** uses the scheme in the selected URL. Redirects and URL
credentials, query parameters and fragments are rejected. Manual connections
honor existing proxy environment settings. Health responses are bounded to 64 KiB, one polling
worker runs at a time, and late responses cannot restore status after a
Disconnect or target change.

System trust and the selected CA are loaded once per Connect; reconnect to
reload them after a trust-store or CA-file change. IPv6/IPv4 connection attempts
use bounded, staggered fallback, with unused sockets closed after a successful
connection. Each of `/live` and `/ready` has its own two-second network budget;
trust-store initialization does not consume it. Both requests run sequentially
inside the single polling worker.

OS DNS resolution and local certificate loading can take longer;
Disconnect clears the displayed state immediately, while reconnect waits for
the previous worker to finish instead of accumulating background lookups.

## Process controls and shutdown

The requested settings summary is separate from observed health. Both binaries
accept `-describe-json`, returning one schema-version-1 object with
`live: false` before runtime I/O. The summary's feature indicators describe
requested settings; they do not prove successful runtime checks. “Certificate
paths supplied” does not mean those files exist or are trusted. TLS paths,
registry contents and client URL credentials/query strings are omitted from
CLI descriptions.

**Server:** **Start** launches the selected local CLI with the applied startup
options. Its bounded log view shows startup failures and shutdown output.
**Stop** affects only that process. **Connect** independently polls the health
URL, which can point to a different local or remote server. Starting a local
server preserves a manually selected health target. Use the operating
system's service manager for systemd or Windows services.

**Client:** **Start** runs the selected CLI using its transport, TLS
verification and analysis. Shannon and threshold use six decimal places;
**OK** is green and **WARN** is red. Throughput is the cumulative average in
MiB/s. Choose byte hue, RGB or grayscale. **F11** toggles fullscreen and
**Escape** returns. Completion or failure restores the status display while
retaining the last sample for inspection.

**Stop** and window close stop and reap only the process owned by that window,
while draining its output. Linux uses SIGTERM; Windows creates an owned
process group and sends targeted CTRL_BREAK. A private hidden console supplies
Windows control delivery if no console exists; an existing console is
preserved. Forced termination is a bounded fallback and is reported honestly.
A separately started server or operating-system service receives no stop
request from these controls.

**Ctrl+C** in the GUI's launch terminal also closes the GUI and cleans up its
owned process and health polling, with exit status 130 and no Python
traceback. Windows also accepts **Ctrl+Break**. Normal window close returns
exit status 0.
Keep a separately started server in its own terminal as shown above.

`--server-binary` selects the local CLI used for server inspection and local
**Start**. It does not select or replace the server at the health URL.
`--client-binary` selects the local CLI inspected and launched by the client
window. Its remote server address is `-url` for pull or `-wsurl` for push after
`--`. The packaged Linux launchers select their installed companions in
`/usr/bin`. The source launcher defaults to the source directory's `bin` folder.
Windows batch/command scripts are rejected. The GUI
manages headless/report flags for the client. Certificate trust for client
acquisition follows [the existing client guidance](client-tls.md).

## Data and performance boundaries

Explicit `-report-waterfall` adds `settings.report_waterfall: true` and a
base64 `waterfall_prefix_base64` to analysed JSONL samples. Each contains at
most 256 raw bytes, also bounded by the analysed sample length. It requires
`-report`; default reports remain statistics-only. GUI prefixes travel through
a local pipe and remain in memory until replaced; the GUI does not save them.

Client history is limited to 128 rows (32 KiB of raw prefixes), report records
to 64 KiB and stderr history to 8 KiB. The server log is limited to 64 KiB.
Each owned process has one bounded pipe pump;
GTK checks for updates at most 20 times per second. The Windows pipe adapter
uses Win32 pipe readiness rather than socket-only selectors. Rows arrive at
the CLI's analysis cadence. A cached raster scales to the window, so larger
screens do not request larger raw samples or unlimited history. Painting and
optional prefix encoding add work to a GUI run; ordinary CLI runs do not
allocate prefix copies for this option.

Shannon **OK** is an empirical threshold assessment, not cryptographic or
physical-entropy certification.

## Validation and platform support

```sh
python3 -m unittest discover -s scripts -p 'test_desktop_gui*.py'
python3 -m unittest discover -s scripts -p 'test_desktop_gui_options.py'
python3 -m unittest discover -s scripts -p 'test_desktop_gui_server.py'
python3 -m unittest discover -s scripts -p 'test_gui_health.py'

# Linux native integration; build both binaries first.
sudo apt install xvfb xauth dbus-x11
xvfb-run -a dbus-run-session -- /usr/bin/python3 scripts/test_desktop_gui_gtk.py \
  --server bin/entropy-server --client bin/entropy-client --artifacts /tmp/entropy-desktop-gui

# Windows UCRT64, after building the native Go executables.
python scripts/test_desktop_gui_gtk.py \
  --server bin/entropy-server.exe --client bin/entropy-client.exe --artifacts gui-artifacts/windows
```

CI exercises native GTK windows on Linux, Windows and both macOS architectures, real CLI acquisition,
option editing, live health, process cleanup and terminal interruption,
alongside the CLI/browser regression suites. Fullscreen geometry and native
Wayland/HiDPI behavior can depend on the desktop compositor. The
[Windows installer](windows-gui-installer.md) bundles the private runtime; native
[Windows Service commands](windows-service.md) are separate from GUI process
controls. Signing and desktop management of operating-system services remain
follow-ups.

**Output backend** in the server editor and **Transport** in the client editor
use the choices advertised by the selected executable. Native amd64 servers
offer RDRAND on CPUs that advertise it. Builds with `experimental_barzakh`
also offer Barzakh-521. After applying a different executable, reopen Options
to see its choices. Older companions that omit the backend inventory use the
legacy choices and validate the selection when applying settings. The CLI
descriptor also lists its compiled export destinations. The desktop client
reserves stdout for its JSONL report. [Descriptor fields](configuration.md) ·
[Direct CPU backend](experimental-rdrand.md)

macOS Intel and Apple Silicon use the same interface through the
[Homebrew launcher](macos.md#homebrew-gui).

## Server limits and acquisition performance

In server **Options → Resource limits**, set the maximum sample size, concurrent
HTTP generation requests, concurrent entropy WebSockets and each transport's
memory budget in MiB. The selected server binary validates the values before
they can be applied to a new run.

Client Options now includes **Server capability discovery**, **Adaptive HTTP pull
pacing**, and its maximum interval. Start checks advertised operator limits
before acquisition; oversized requests report the actual server maximum without
changing the requested sample. Discovery is automatic for recognized routes,
optional for legacy/custom servers, and remains separate from offline validation.

**Connection & performance** displays discovery results, requested/effective and
average received FPS, receive/analysis/output timings and pacing status. Collapse
it to give the waterfall more space. Fullscreen hides it. Older CLI reports can
omit this information. See [the shared CLI/GUI guide](client-capabilities-performance.md)
for interpretation, compatibility and the HTTP-only adaptive policy.

## LAB and profiling startup controls

In the server view, **LAB sources…** and **Debug / pprof…** open the matching
startup controls directly. Validate and apply, then Start a new local run.
The LAB tools are available on Linux, Windows and macOS even without a registry;
**LAB registry configured** describes only the optional registry file.
Profiling, when enabled, listens separately on
`http://127.0.0.1:6060/debug/pprof/` on the server host.
See [LAB platform support](lab-platforms.md) for the source matrix, prerequisites
and CLI equivalents.
