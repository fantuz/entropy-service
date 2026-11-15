# Desktop GUI proof of concept

This optional GTK 4/PyGObject frontend supports Linux and Windows x64. It runs
from source, with desktop dependencies separate from the Go binaries. The
command-line and web interfaces remain independently usable. macOS support,
standalone application bundles, installers and signing remain follow-ups.

The server window combines a read-only startup configuration with live health
from an existing server. The client window provides statistics and a bounded,
resizable waterfall with fullscreen mode. Neither window installs or controls
an operating-system service.

[![Server configuration and live health](images/desktop-gui-server-poc.png)](images/desktop-gui-server-poc.png)
[![Client waterfall](images/desktop-gui-client-poc.png)](images/desktop-gui-client-poc.png)

## Linux setup

On Debian/Ubuntu:

```sh
sudo apt install python3-gi python3-gi-cairo python3-cairo gir1.2-gtk-4.0
make build

# Inspect this invocation; this does not start a server.
/usr/bin/python3 tools/desktop-gui/entropy-gui --view server -- \
  -device=/dev/urandom -require-device=true -enable-https=false \
  -http=127.0.0.1:8080 -drbg=chacha20
```

Use the distribution's Python: virtual environments can hide PyGObject even
when its OS package is installed. Ubuntu 24.04 and Debian 13 are the initial
Linux targets. GTK supports X11 and Wayland; native Linux CI uses X11 under
Xvfb. `--help` does not require GTK or a graphical session.

Start a server separately for initial loopback testing:

```sh
./bin/entropy-server -device=/dev/urandom -require-device=true \
  -enable-https=false -http=127.0.0.1:8080
```

## Windows setup

Build the current branch's Go binaries in PowerShell or CMD using the Go
version required by `go.mod`:

```powershell
go build -o bin/entropy-server.exe ./cmd/entropy-server
go build -o bin/entropy-client.exe ./cmd/entropy-client

# Separate terminal: an OS CSPRNG source, with loopback HTTP for initial testing.
./bin/entropy-server.exe -device=windows-csprng -enable-https=false -http=127.0.0.1:8080
```

Install [MSYS2](https://www.msys2.org/), open its **UCRT64** terminal, and install
the native GTK/Python bindings:

```sh
pacman -Syu
pacman -S --needed mingw-w64-ucrt-x86_64-gtk4 \
  mingw-w64-ucrt-x86_64-python-gobject mingw-w64-ucrt-x86_64-python-cairo
```

Follow MSYS2's prompt to restart the terminal if an update requires it. From
that same UCRT64 terminal, enter your checkout, for example
`cd /c/Users/yourname/entropy-service`, then run:

```sh
python tools/desktop-gui/entropy-gui --view server \
  --health-url=http://127.0.0.1:8080 -- \
  -device=windows-csprng -enable-https=false -http=127.0.0.1:8080

python tools/desktop-gui/entropy-gui --view client -- \
  -mode=pull -url=http://127.0.0.1:8080/v1/data/random \
  -slice=1048576 -refresh=250ms -analysis-interval=250ms
```

Use UCRT64's Python, not an unrelated Python installation: its GTK DLLs and
bindings must match. The launcher chooses `.exe` binary names on Windows.
This PoC has no self-contained `.exe` bundle or installer; the normal Go
release ZIP continues to contain the CLI utilities.

## Live server health

In the server view, enter an existing server's base URL and press **Connect**.
`--health-url` prepopulates that field; it does not start polling automatically.
The suggested URL comes from the requested listener settings, with wildcard
bind addresses replaced by loopback. It is a suggestion, not evidence that a
listener exists. Port-zero/dynamically assigned listeners need an explicit URL.

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

“Selected listener” describes the endpoint being checked. The GUI does not
claim that every configured HTTP, HTTPS or debug port is reachable. A remote
health target can belong to a different instance from the startup invocation
shown below it; those sections are deliberately labelled separately.

HTTPS uses certificate and hostname verification. Supply an additional trusted
CA or a deliberately provisioned self-signed server certificate when needed:

```sh
/usr/bin/python3 tools/desktop-gui/entropy-gui --view server \
  --health-url=https://localhost:8443 --health-ca=/path/to/cert.pem
```

On Windows, use UCRT64 `python` and an accessible certificate path. See
[TLS provisioning](tls-certificates.md). There is no insecure verification
switch or automatic HTTPS-to-HTTP fallback. Redirects and URL credentials,
query parameters and fragments are rejected. Existing proxy environment
settings are honored. Health responses are bounded to 64 KiB, one polling
worker runs at a time, and late responses cannot restore status after a
Disconnect or target change.

Active HTTP requests have a short deadline. OS DNS resolution can take longer;
Disconnect clears the displayed state immediately, while reconnect waits for
the previous worker to finish instead of accumulating background lookups.

## Startup configuration and client controls

**Requested configuration:** both binaries accept `-describe-json`, returning
one schema-version-1 object with `live: false` and exiting before runtime I/O.
The GUI displays grouped settings and read-only feature checkboxes. Inspection
does not open devices or listeners, generate certificates, load source
registries, or modify a service. “Certificate paths supplied” does not mean
those files exist or are trusted. TLS paths, registry contents and client URL
credentials/query strings are omitted from descriptions.

**Client:** press **Start** to run the existing CLI, using its transport, TLS
verification and analysis. Shannon and threshold use six decimal places;
**OK** is green and **WARN** is red. Throughput is the cumulative average in
MiB/s. Choose byte hue, RGB or grayscale. **F11** toggles fullscreen and
**Escape** returns. Completion or failure restores the status display while
retaining the last sample for inspection.

**Stop** and window close affect only the owned client process. The GUI drains
both report pipes while stopping, then reaps that process. Linux uses SIGTERM;
Windows starts an owned process group and sends targeted CTRL_BREAK. If no
console exists, a private hidden console supplies Windows control delivery.
An existing console is preserved. Forced termination is a bounded fallback,
reported as forced instead of inventing a clean summary. The GUI never stops
a separately started entropy-server or an operating-system service.

Change utility options by reopening the window with arguments after `--`.
`--server-binary` and `--client-binary` select explicit native executables;
Windows batch/command scripts are rejected. The GUI manages headless/report
flags. Raw exports and device writes through the GUI are unsupported; use the
CLI for deliberate exports. Certificate trust for client acquisition follows
[the existing client guidance](client-tls.md).

## Data and performance boundaries

Explicit `-report-waterfall` adds `settings.report_waterfall: true` and a
base64 `waterfall_prefix_base64` to analysed JSONL samples. Each contains at
most 256 raw bytes, also bounded by the analysed sample length. It requires
`-report`; default reports remain statistics-only. GUI prefixes travel through
a local pipe and remain in memory until replaced; the GUI does not save them.

History is limited to 128 rows (32 KiB of raw prefixes), report records to
64 KiB and stderr history to 8 KiB. One bounded pipe pump updates state;
GTK checks for updates at most 20 times per second. The Windows pipe adapter
uses Win32 pipe readiness rather than socket-only selectors. Rows arrive at
the CLI's analysis cadence. A cached raster scales to the window, so larger
screens do not request larger raw samples or unlimited history. Painting and
optional prefix encoding add work to a GUI run; ordinary CLI runs do not
allocate prefix copies for this option.

Shannon **OK** is an empirical threshold assessment, not cryptographic or
physical-entropy certification.

## Validation

```sh
python3 -m unittest discover -s scripts -p 'test_desktop_gui.py'
python3 -m unittest discover -s scripts -p 'test_gui_health.py'

# Linux native GUI integration; build both binaries first.
sudo apt install xvfb xauth dbus-x11
xvfb-run -a dbus-run-session -- /usr/bin/python3 scripts/test_desktop_gui_gtk.py \
  --server bin/entropy-server --client bin/entropy-client --artifacts /tmp/entropy-desktop-gui

# Windows UCRT64, after building the native Go executables.
python scripts/test_desktop_gui_gtk.py \
  --server bin/entropy-server.exe --client bin/entropy-client.exe --artifacts gui-artifacts/windows
```

CI exercises native GTK windows on Linux and Windows, real CLI acquisition,
verified health polling, failure/recovery, pipe bounds and owned-process
cleanup, alongside existing CLI/browser regressions. Xvfb validates fullscreen
requests and UI behavior; actual fullscreen geometry and native Wayland/HiDPI
behavior still need desktop user testing. Packaging, service control and
interactive configuration editing remain follow-ups.
