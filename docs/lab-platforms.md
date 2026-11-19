# LAB on Linux, Windows and macOS

Source LAB (`/api-sources.html`) and Socket LAB (`/api-socket.html`) are embedded
in every released server build, including Linux amd64/ARM64/ARMv7, Windows x64,
and macOS Intel/Apple Silicon. No LAB enable switch is required. An unchecked
**LAB registry configured** indicator means that no registry file was supplied;
it does not disable the LAB pages, browser inputs, or network support.

The source catalog at `/lab/sources` reports `platform` and `source_kinds` so
both LAB pages can show the registration types supported by the running server.
The desktop server view separately marks **LAB browser and network tools** as
available and provides a **LAB sources…** shortcut to the startup options.

| Source or interface | Linux | Windows | macOS | Additional requirement |
| --- | --- | --- | --- | --- |
| Browser Web Crypto, deterministic patterns, file analysis | Yes | Yes | Yes | Supported browser |
| Browser HTTP / WebSocket input | Yes | Yes | Yes | Target CORS / WebSocket origin policy and browser mixed-content rules |
| Microphone, line-in and browser camera | Yes | Yes | Yes | Browser permission; HTTPS or localhost; an actual input device |
| Browser serial input | Browser dependent | Browser dependent | Browser dependent | Web Serial, normally Chromium; Firefox does not provide it |
| Registered HTTP(S), WebSocket, entropy-server peers, web-page pools | Yes | Yes | Yes | Operator registry and reachable upstream |
| Registered TCP / UDP | Yes | Yes | Yes | Operator registry; source is read on the server host |
| Registered Unix-domain sockets | Yes | No | Yes | An accessible Unix-domain socket on that server |
| Registered direct devices | Yes | No | No | Linux device reader and permissions; use a network adapter on other OSs |
| RTSP / ONVIF network cameras | Yes | Yes | Yes | FFmpeg on the server's PATH, camera access and registration |
| Local pprof listener | Yes | Yes | Yes | Explicit `-debug=true`; separate loopback port 6060 |

Browser capabilities belong to the **browser machine**, while registry sources
and FFmpeg run on the **server machine**. An audio device being selectable does
not establish that it is receiving a signal. Line-in requires selecting the
actual input and checking its OS recording level/mute state. Browser samples
and network camera bytes are diagnostic inputs, not certified entropy sources.

## Register server-side sources

Prepare/download the JSON from either LAB page, save it on the server, then set
**LAB source registry JSON** in the desktop server options. Validate and apply,
then Start a new server run. The same CLI flag works on all three systems:

```text
entropy-server -lab-sources=lab-sources.json
```

The remaining startup options, including certificate paths or a local HTTP
testing configuration, still apply. Loading a registry requires restarting the
server; preparing a JSON document in a browser does not install it automatically.
File paths follow the server's OS conventions. Remote registered bridges remain
an explicit operator choice with `-lab-allow-remote`; trusted Host checks,
connection/memory budgets, deadlines and byte limits remain enforced.

For network cameras, install FFmpeg using the OS's package manager or the
[FFmpeg download guidance](https://ffmpeg.org/download.html). For example,
`sudo apt install ffmpeg` on Debian/Ubuntu and `brew install ffmpeg` on macOS.
On Windows, install a Windows FFmpeg build and put its `bin` directory on PATH;
restart the GUI after updating PATH. The bundled Windows GUI preserves the
inherited PATH after adding its private runtime. It does not bundle FFmpeg.

## Enable profiling from the server GUI

Select **Debug / pprof…**, check **Enable local debug / pprof listener**, choose
**Validate and apply**, then **Start**. Changes affect the next GUI-owned run;
they do not reconfigure an already running server selected with Connect.

Open **http://127.0.0.1:6060/debug/pprof/** on the server host. This is a separate
HTTP listener; `/debug` on port 8080 or 8443 is not the profiling URL. The same
startup flag is available in Linux, Windows and macOS CLI builds:

```text
entropy-server -debug=true
```

Profiling stays off by default and loopback-only. An occupied port produces a
startup error rather than silently omitting the listener. Stop closes the
GUI-owned server and its debug listener. The GUI's dedicated control and native
integration tests cover enabling, requesting the real pprof index, disabling,
and closing the listener across the three operating systems.

## Qualification boundaries

Native Linux, Windows and both macOS CI runners exercise the LAB catalog,
registered HTTP bridge, host restrictions, resource settings and pprof listener
through the shared GUI/server tests. Go transport tests cover simulated
HTTP/WebSocket/TCP/UDP and cancellation. Browser automation covers source controls
and simulated media; it does not qualify a physical microphone, line-in, camera,
serial device or RNG on every OS. See [hardware qualification](hardware-qualification.md)
for physical-device evidence and [Windows support](windows.md) /
[macOS support](macos.md) for the remaining hardware-specific limitations.
