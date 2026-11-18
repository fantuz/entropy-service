# macOS CLI and desktop interface

Both utilities support Intel (`darwin_amd64`) and Apple Silicon
(`darwin_arm64`). Native CI tests macOS 15 on both architectures. v2.5.0
predates this support; before the first macOS release, use the matching
`macos-cli-archives` CI artifact or build from this branch.

## CLI installation

Download the appropriate `entropy-service_<version>_darwin_<architecture>.tar.gz`
from a release containing macOS support. It contains both `entropy-server` and
`entropy-client`. Compare the archive with that release's `checksums.txt`:

```sh
shasum -a 256 entropy-service_*_darwin_*.tar.gz
```

Extract it into a directory you own. The CLI binaries need neither Go nor
Homebrew. They are unsigned/not notarized; macOS may require explicit approval
under System Settings → Privacy & Security. Verify the published checksum first.

For a local demonstration, run the server in one Terminal:

```sh
./entropy-server -enable-https=false -http=127.0.0.1:8080
```

Then run the client in another:

```sh
./entropy-client -mode=push -wsurl=ws://127.0.0.1:8080/stream -waterfall
```

Open `http://127.0.0.1:8080/` for the web interface. Normal CLI defaults retain
HTTPS enabled; see [certificate provisioning](tls-certificates.md) and
[client trust](client-tls.md) before using remote TLS endpoints.

The server defaults to `-device=macos-csprng`, using Go's native macOS
`crypto/rand` source. Apple Silicon defaults to ChaCha20, Intel to AES-256-CTR;
explicit software-backend overrides work on both. Experimental direct RDRAND is
available only on Intel CPUs that advertise the instruction, never on Apple
Silicon. OS RNG calls are synchronous: cancellation/deadlines are checked before
and after bounded reads, not by abandoning a goroutine or interrupting an OS call.

File, raw-stdout and network exports, adaptive terminal sizing, fullscreen
waterfall and JSONL reports are supported. Linux random-device injection
(`-writeout`/device destinations), native Quantis/ChaosKey adapters and systemd/
Windows Service management are unavailable on macOS. Unsupported hardware
requests fail explicitly; they do not silently substitute an OS source.

## Homebrew GUI

The GUI uses the same GTK 4 interface and Go utilities as Linux/Windows. It can
start/stop its owned server/client, monitor live server health, edit startup
options and display the client waterfall/fullscreen view. This distribution is
a Terminal launcher with Homebrew dependencies, not a self-contained `.app`.

```sh
brew install gtk4 pygobject3
```

Obtain the matching source tree (or source archive) containing
`tools/desktop-gui`, then launch from its root. Pass the absolute path to each
CLI binary if it is installed elsewhere:

```sh
tools/desktop-gui/entropy-gui-macos --view server \
  --server-binary /absolute/path/entropy-server -- \
  -enable-https=false -http=127.0.0.1:8080

tools/desktop-gui/entropy-gui-macos --view client \
  --client-binary /absolute/path/entropy-client -- \
  -mode=push -wsurl=ws://127.0.0.1:8080/stream
```

Click **Start** in each view. The launcher selects a Homebrew Python that can
import GTK/PyGObject/Cairo; it does not replace the system Python or change the
user's global environment. Install dependencies using Homebrew's supported
macOS versions. Stop closes only GUI-owned child processes; connecting to a
separately launched server remains read-only.

To build instead of downloading CLI archives:

```sh
brew install go
make build
```

The default GUI binary paths then resolve to `bin/entropy-server` and
`bin/entropy-client`. Ordinary unit tests are GTK-independent; native CI also
exercises real GTK windows, client acquisition, server health, options,
fullscreen, resizing and joined process shutdown on both Mac architectures.
