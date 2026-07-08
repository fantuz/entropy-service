# entropy-service

[![golangci-lint](https://img.shields.io/badge/golangci--lint-enabled-brightgreen?logo=go)](https://golangci-lint.run/)
[![build](https://github.com/fantuz/entropy-service/actions/workflows/build.yml/badge.svg)](https://github.com/fantuz/entropy-service/actions/workflows/build.yml)
![Latest GitHub release](https://img.shields.io/github/release/fantuz/entropy-service.svg)
[![GitHub license](https://img.shields.io/github/license/fantuz/entropy-service)](https://github.com/fantuz/entropy-service/blob/main/LICENSE)
![GitHub all releases](https://img.shields.io/github/downloads/fantuz/entropy-service/total)

## Intended usage

entropy-service is a GO-based software intended to let users fetch an amount randomness via simple API(s). Useful in daily cryptographic operations, where the randomness source may be "staving" or being tampered-with.
In my demonstrative setup, real-time entropy is backed by a real QRNG (Quantum Random Number Generator), a PCI card made by ID Quantique in Geneva. This entropy is then fed into a DRBG module written in GO, which "amplifies" the output of entropy source, using well-known algorithms as ChaCha20 or AES-CRT.

## Practical implementations

With this powerful software, users can setup their own "cryptographically-strong entropy source" and use simple APIs to retrieve chunks of randomness, either in form of:

- a binary stream
- a set of words
- a background color
- randomly-generated images and heatmaps
- randomly-generated sounds (to be added soon)

The suite also includes advanced SRE-style views, statistics and metrics:

- an entropy quality monitor and visualization tool (/streams.html)
- a JSON status monitor, showing startup and runtime information (/health)
- a Prometheus-like metrics collector, presenting live runtime counters (/metrics)

The two programs operate as described:

- entropy-server ....
- entropy-client ....

## Architecture and logic

### High-Level Overview

- Summary of the different functions from entropy sourcing to randomness streaming

```

                           ┌─────────────────────────┐
                           │     External QRNG       │
                           │  (hardware / upstream)  │
                           └─────────────┬───────────┘
                                         │ fetch()
                                         ▼
                           ┌─────────────────────────┐
                           │     Entropy Buffer      │
                           │   (ring / pool cache)   │
                           └─────────────┬───────────┘
                                         │ reseed() (time-driven)
                                         ▼
                           ┌─────────────────────────┐
                           │      Master DRBG        │
                           │   (ChaCha20 stream)     │
                           │  - expands entropy      │
                           │  - derives child seeds  │
                           └─────────────┬───────────┘
                                         │ derive()
                                         │
                                         │
                                         │
              ┌──────────────────────────┼──────────────────────────┐
              ▼                          ▼                          ▼
      ┌──────────────┐           ┌──────────────┐           ┌──────────────┐
      │ Conn DRBG #1 │           │ Conn DRBG #2 │           │ Conn DRBG #N │
      │ isolated key │           │ isolated key │           │ isolated key │
      └───────┬──────┘           └───────┬──────┘           └───────┬──────┘
              │ serve()                  │ serve()                  │ serve()
              ▼                          ▼                          ▼
       HTTP/HTTPS/WS              HTTP/HTTPS/WS              HTTP/HTTPS/WS
              └──────────────────────────┴──────────────────────────┘
                                         │
                                         ▼
                                      Clients
                          (browsers, curl, websockets, ..)

Legend
  Fetch()  → External entropy acquisition from QRNG source
  Reseed() → Periodically refreshes the master DRBG providing with new entropy buffer
  Derive() → Per-connection DRBG instantiation (state isolation)
  Serve()  → HTTP/HTTPS streaming of expanded cryptographic output

Supported client protocols
  HTTP/1.1
  h2
  wss://
```

- Separation of roles: server streams binary data, client processes WSS binary data and renders via Javascript engine

```
+----------------------------+
| GO entropy-service backend |             (server)
+----------------------------+
            ↓
    << WebSocket binary >>          (half-duplex channel)
            ↓
  +------------------------+
  | Main Thread (IO + DOM) |
  |           ↓            |               (client)
  |  Worker (math engine)  |
  +------------------------+
```

### Services being offered via API

- entropy control panel with metrics graphs and quality indexes
- entropy heatmaps (to infer the quality of RNG source)
- random binary data generation, length of which is configurable via URI parameter
- random passphrase / wordlist generation, presentated in different encodings, length of which is configurable via URI parameter
- random imgage generation
- support for websockets and JSON payloads (more efficient, less network overhead, entropy is streamed and broadcasted continuously)
- observable metrics, exposing entropy source availability, buffer size, pressure, reseed interval, size of reseed, time since last reseed.
- h2 readyness, now commented out as debug in HTTP/2 is way harder than HTTP/1.1

### Server robustness and stability, by design

- socket management, thread-safe and thread-aware structs
- per-connection DRBG isolation
- rolling buffers, to better feed client expectations, to improve dashboard rendering
- running parallel routines in a context-safe manner, correctly implementing and supporting OS-signalling
- HTTP and HTTPS servers sharing same mux, HTTP headers
- support for modern wesockets, allowing less overhead while guaranteeing a continuous and timely stream of data
- use of GO atomic counters to accomodate atomic updates even under high-concurrency
- pluggable over different /dev/Xrandom sources (as said, for example, a ChaosKey integrated by kernel driver /dev/kaoskeyX or any better/safer/more modern entropy source, for example by ID-Quantique company)
- OS Variables to enable/disable TLS, h2, and other useful test features
- CLI options to control entropy-source device, buffers sizes, reseed intervals, listening ports, TLS on/off, mandatory presence of RNG device, fallback RNG device, timers and more.
- pages are optimezed for client-side renderingm as the separation of DOM, canvas and JS workers is enforced by-design (not-blocking, not-CPU intensive, allows packet drop, recovery and other anti-stall features)
- abstraction and reorganization of code in line with GO guidelines. DRBG, transport, service and domain are now located in more canonical project structure (for example: domain/ rng/ service/ transport/ /cmd)

### Hardware already supported, more to be added soon

The software has been adapted to fetch entropy from etherogeneous sources, as the Linux CSPRNG itself (/dev/urandom, for testing only) or an USB Chaos Key (low entropy, still deterministic in a way). Possibilities are endless as every character device will become a valid choice; ranging from barcode reader to any webcam, mouse, or radio-wave, your imagination is the limit !

```
 - Bus 001 Device 003: ID 1d50:60c6 OpenMoko, Inc. USBtrng hardware random number generator
 - Quantis PCI by ID Quantique
 - Linux default CSRNG source, /dev/urandom, useful when testing or need to fallback
 - pretty much any character device under Linux
 - eventually any SDR radio-receiver, as AirSpy, RTL, BladeRF and others.
```

### API mappings

```
Websockets (Standard HTTP 101 Upgrade)
    /stream.html?bytes=<B>&refresh=<MS>
	/bytes.html?bytes=<B>&refresh=<MS>
    /words.html?words=N>&refresh=<MS>
	/colors.html?refresh=<MS>

Regular HTTP
 - images
	/v1/image/random
	/v1/image/heatmap
 - data
	/v1/data/random
	/v1/data/test
 - words
	/v1/meta/random
    /paroleparoleparole
 - metrics, health
	/health
	/metrics
```

## Build

### Prerequisites

Once cloned the repository via Github, go through the below steps to freshly build your entropy-service.

#### Linux (Debian / Ubuntu)

- Install the only base-package required:

```
sudo apt-get install golang
```

- Optionally, install a few well-known tools for performance testing:

```
sudo apt-get install wrk dieharder rng-tools
```

#### macOS (Homebrew)

- Install the only base-package required:

```
brew install go
```

- Optionally, install a few well-known tools for performance testing:

```
brew install wrk dieharder
```

> Note: `rng-tools` (the `rngd` daemon) is Linux-specific and has no macOS equivalent, so it is omitted above.
>
> macOS notes:
>
> - There is no hardware QRNG support; use the kernel CSPRNG fallback by passing `-device /dev/urandom` (the default `/dev/qrandom0` is a Linux/hardware device).
> - `sudo` is not required for `/dev/urandom`; it is only needed to access privileged hardware entropy devices on Linux.
> - The committed pre-built binaries (if any) are Linux x86-64 ELF and will not run on macOS — always build locally as shown below.
>
### Preliminary and preparation steps

Ensure all go dependencies are satisfied. Follow on-screen instructions in case of issues.

- build entropy-server

```
go build cmd/entropy-server/entropy-server.go
```

- build entropy-client

```
go build cmd/entropy-client/entropy-client.go
```

### Using the Makefile

A `Makefile` wraps the common workflows so you don't have to remember the individual commands:

```
make build            # build both binaries into ./bin
make build-server     # build only entropy-server
make build-client     # build only entropy-client
make test             # go test ./...
make lint             # run golangci-lint
make fmt              # run golangci-lint fmt (gofmt/gofumpt/goimports/gci)
make release-snapshot # build a local snapshot release (archives + deb/rpm) into ./dist
make clean            # remove ./bin and ./dist
```

The `lint`, `fmt` and `release-snapshot` targets need two extra dev tools:

```
# macOS (Homebrew)
brew install golangci-lint goreleaser

# Linux: see https://golangci-lint.run/ and https://goreleaser.com/install/
```

Continuous integration mirrors this: `.github/workflows/build.yml` runs the tests, linters and a snapshot build on every push/PR, while `.github/workflows/release.yml` runs GoReleaser to publish artifacts whenever a `vX.Y.Z` tag is pushed.

## Install (packages)

Tagged releases publish `.deb` and `.rpm` packages (built by GoReleaser) for `linux/amd64`, `linux/armv7` and `linux/arm64`. Grab the package matching your distro and architecture from the [Releases](https://github.com/fantuz/entropy-service/releases) page and install it:

```
# Debian / Ubuntu
sudo dpkg -i entropy-service_*_linux_amd64.deb

# RHEL / Fedora / openSUSE
sudo rpm -i entropy-service_*_linux_amd64.rpm
```

The package installs both `entropy-server` and `entropy-client` into `/usr/bin` and registers a `entropy-server` systemd service (enabled and started automatically). By default the service starts on HTTP `:8080` with HTTPS disabled, using `/var/lib/entropy-service` as its working directory:

```
sudo systemctl status entropy-server
sudo journalctl -u entropy-server -f
```

To enable TLS or change flags, edit `/usr/lib/systemd/system/entropy-server.service` (or a drop-in under `/etc/systemd/system/entropy-server.service.d/`), then `sudo systemctl daemon-reload && sudo systemctl restart entropy-server`.

## Run

- **entropy-server**<br/>
We are now ready to start both HTTP & HTTPS listeners, respectively on ports 8080 and 8443 by default, on all available inet interfaces.
SUDO command may be necessary to access the xRNG devices on different platforms (e. including symbolic links under /dev).
The 'cd' command helps serving the ./web contents, residing in cmd/entropy-server/web. For simplicity you can create a symlink within the execution directory, or copy the whole ./web directory.

```
cd entropy-server
sudo go run . -reseed-ms 2500 -buffer-reseed 512 -buffer-entropy 2 -buffer-qrng 2 -device /dev/urandom -words 8 -max-bytes 2097152 -refresh-ms '50ms' -refresh-colors-ms '50ms' -refresh 1 -cert-file cert.pem -key-file key.pem
```

- **entropy-client**<br/>
If you want fetch entropy using a dedicated GO-CLI client with multiple capabilities, instead of the classic web broser, you need to invoke the following command as described below, instead of launching a browser. Also usefult to monitor the entropy-source itself, or to locally attach remote source of entropy.

```
cd entropy-client

### stream mode (Websockets)
go run . -refresh '50ms' -mode stream -slice 2097152 -wsurl ws://127.0.0.1:8080/stream -url http://127.0.0.1:8080/v1/data/random

### pull mode (HTTP)
go run . -refresh '50ms' -mode pull -slice 2097152 -wsurl ws://127.0.0.1:8080/stream -url http://127.0.0.1:8080/v1/data/random
```

> Important: pass **bare** endpoints to `-wsurl` and `-url` (no `?query` string). The client automatically appends `?bytes=<slice>&refresh=<refresh>` to both URLs. If you add your own query string, you get a malformed double-query URL (e.g. `.../stream?bytes=...&refresh=...?bytes=...&refresh=...`) and the stream silently produces no data. Control the request size with `-slice` and the rate with `-refresh` instead.

### Screenshots

- ntop-style entropy-client interface (cmd/entropy-client/entropy-client.go)<br/>
<img width="640" height="360" alt="Screenshot From 2026-03-08 14-16-42" src="https://github.com/user-attachments/assets/72b2adf4-376c-405e-9460-13a34190ed9a" />

- WSS Endpoint STREAM: real-time entropy quality monitor (/stream.html + /data)<br/>
<img width="640" height="360" alt="Screenshot From 2026-03-08 14-32-49" src="https://github.com/user-attachments/assets/de76d3b0-4916-4cdf-adb0-7ae5e08ce6ae" />

- WSS Endpoint BYTES: Websocket streamer, byte generator (/bytes.html + /bytes)<br/>
<img width="640" height="360" alt="Screenshot From 2026-02-25 16-16-48" src="https://github.com/user-attachments/assets/8d2d4846-5d3b-42e7-bad9-1081aa830177" />

- WSS Endpoint WORDS: Websocket streamer, word generator (/words.html + /words)<br/>
<img width="640" height="360" alt="Screenshot From 2026-02-25 18-35-56" src="https://github.com/user-attachments/assets/b65895bc-2da6-4a3b-9d31-ec3431c08988" />

- WSS Endpoint COLORS: Websocket streamer, color generator (/colors.html + /colors)<br/>
<img width="640" height="360" alt="Screenshot From 2026-02-25 20-09-33" src="https://github.com/user-attachments/assets/c305b5dc-314b-4cdf-b0c0-2cab6bb49e53" />

- File upload & analysis interface (upload.html)<br/>
<img width="600" height="600" alt="Screenshot From 2026-03-09 19-31-49" src="https://github.com/user-attachments/assets/3166ab74-4e09-400a-9325-92e10cbfc28e" />

- Metrics (/metrics)<br/>
<img width="647" height="755" alt="Screenshot From 2026-03-09 19-32-00" src="https://github.com/user-attachments/assets/cc4a0ba0-fec2-4d09-a6f9-1d08b581cc71" />

## Performances

### Results

Pressure tests on a 10+ years old hardware showed pretty impressive numbers:

- **HTTP**\
70'000 requests per second (payload 64B)\
up to 1.1 GB/s (payload 512KB)
- **HTTPS**\
50'000 requests per second (payload 64B)\
up to 600 MB/s (payload 512KB)

Results from WRK test utility are summarized here below:

- Test with HTTP / 2MB payload

```
max@iMac:~/entropy-service$ wrk -t16 -c64 -d5s --latency --timeout 1 http://localhost:8080/v1/random?bytes=2097152
Running 5s test @ http://localhost:8080/v1/random?bytes=2097152
  16 threads and 64 connections
  Thread Stats   Avg      Stdev     Max   +/- Stdev
    Latency   132.02ms  134.43ms 978.98ms   85.13%
    Req/Sec    37.79     23.16   131.00     72.42%
  Latency Distribution
     50%  102.16ms
     75%  217.21ms
     90%  301.35ms
     99%  593.49ms
  2887 requests in 5.04s, 5.64GB read
  Socket errors: connect 0, read 0, write 0, timeout 2
Requests/sec:    572.97
Transfer/sec:      1.12GB
```

- Test with HTTP / 64B payload

```
max@iMac:~/entropy-service$ wrk -t16 -c64 -d5 --latency --timeout 1 http://127.0.0.1:8080/v1/random?bytes=64
Running 5s test @ http://127.0.0.1:8080/v1/random?bytes=64
  16 threads and 64 connections
  Thread Stats   Avg      Stdev     Max   +/- Stdev
    Latency     2.24ms    3.35ms  35.56ms   87.12%
    Req/Sec     4.32k     1.67k   14.34k    78.76%
  Latency Distribution
     50%  750.00us
     75%    3.11ms
     90%    6.57ms
     99%   15.28ms
  346811 requests in 5.10s, 59.53MB read
Requests/sec:  68019.56
Transfer/sec:     11.68MB
```

Evidence of my poor-man test-platform:

```
max@iMac:~/entropy-service$ grep model\ name /proc/cpuinfo
model name	: Intel(R) Core(TM) i5-3470 CPU @ 3.20GHz
model name	: Intel(R) Core(TM) i5-3470 CPU @ 3.20GHz
model name	: Intel(R) Core(TM) i5-3470 CPU @ 3.20GHz
model name	: Intel(R) Core(TM) i5-3470 CPU @ 3.20GHz
```

### sysctl & ulimit tuning

I currently run those commonly tuned parameters:

```
ulimit -n 1048576
```

```
sysctl -w net.core.wmem_max=33554432
sysctl -w net.core.wmem_default=8388608
sysctl -w net.core.rmem_max=33554432
sysctl -w net.core.rmem_default=8388608
sysctl -w net.ipv4.tcp_rmem="4096 87380 33554432"
sysctl -w net.ipv4.tcp_wmem="4096 65536 33554432"
sysctl -w net.ipv4.tcp_slow_start_after_idle=0
sysctl -w net.core.somaxconn=65535
sysctl -w net.ipv4.tcp_max_syn_backlog=65535
sysctl -w net.ipv4.tcp_tw_reuse=1
sysctl -w net.ipv4.tcp_fin_timeout=15
sysctl -w net.ipv4.tcp_keepalive_time=60
sysctl -w net.ipv4.tcp_keepalive_intvl=10
sysctl -w net.ipv4.tcp_keepalive_probes=6
sysctl -w net.ipv4.tcp_no_metrics_save=1
```

### Mature PoC

The whole project is just a showcase and PoC built around the use of a rather old PCI card (not PCI0e), a QRNG produced by ID Quantique. Given that support ended with Kernel 4, I had to migrate myself some syscalls to make the drivers compile on Kernel(s) 5 and 6.

## What is yet to come

### Features to be added

- FUSE device support allowing for "openssl rand -engine entropyctl"
- eventually add go sync.Pool, to be evalueted yet
- add GO build tags for PROD and for DEMO modes
- define and implement GO tests
- replace Diceware dependency with local dictionary, only used for /words endpoint
- conditional systemd startup, probing the respective kernel module if existing (i.e. for Quantis PCI QRNG cards) before starting (a basic systemd unit is already shipped via the deb/rpm packages)
- CUDA-awarness and integration if interesting or found to be relevant in future evaluatons
- ChaCha20 to be replaced by AES-CTR when my test hardware will support CPU extension, to avoid doing it via sowftware.
- random sound generator
