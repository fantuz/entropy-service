# entropy-service

### Theoretical purposes
GO-based software intended to let users fetch randomness via simple API(s).
Behind my setup, entropy is provided by a real QRNG, Quantum Random Number Generator, made by ID Quantique in Geneva. This entropy is then fed into a DRBG module which "amplifies" output of QRNG, using either ChaCha20 or AES-CRT functions. The software can be easily adapted to source entropy from various and different sources, even Linux PRNG itself (for testing only) or an USB Chaos Key (low entropy, still deterministic in a way). Possibilities are endless.

### Practical implementations
With this simple yet very performant software, users can setup their own cryptographically-strong randomness source, and use API(s) to retrieve different amounts of binary randomness, randomly-generated images, and even sounds (later feature to be added soon).

### GO programming techniques and logics implemented, including:
- socket management
- HTTP and HTTPS servers sharing same mux, HTTP headers & JSON telemetry
- thread-safe and thread-aware structs
- observable metrics and counters (entropy source availability, entropy buffer, pressure, reseed interval, reseed bits etc)
- running multiple routines in a context-safe manner, correctly implementing and supporting OS-signalling
- pluggable over different /dev/Xrandom sources (as said, for example, a ChaosKey integrated by kernel driver /dev/kaoskeyX or any better/safer/more modern entropy source, for example by ID-Quantique company)
- h2 readyness, now commented. Whole implementation is 3 lines away, but commented out as debug in HTTP/2 is way harder than HTTP/1.1 and I consider this a beta-phase
- OS Variables to enable/disable TLS, h2, and other useful test features. Already present somehow, but currently commented out, as other points above.
- random imgage generation, heatmaps
- ...

### What is (yet) missing:
- CLI options, for example to enable different /dev/Xrandom sources, different reseed interval, different buffer sizes and so on
- systemd implementation, to have it startup at boot, eventually after inserting or at leas probing, the proper kernel module to support the RNG source
- CUDA-awarness and integration if interesting or found to be relevant in future evaluatons
- ChaCha20 to be replaced by AES-CTR when my test hardware will support CPU extension, to avoid doing it via sowftware.
- per-connection DRBG
- random sound generator
- ...

### GO build and run
In the base directory, simply run:
```
$ sudo apt-get install golang

## optionally, install testing tools
$ sudo apt-get install wrk dieharder rng-tools

$ go vet
go: downloading golang.org/x/crypto v0.47.0

$ go fmt
$ go build

$ sudo go run entropy-service
2026/02/12 07:28:54 HTTP server running on :8080
2026/02/12 07:28:54 HTTPs server running on :8443
```
The last command will start the HTTP & HHTPS listeners on all available interfaces. SUDO command may be necessary to access the /dev device on some platforms (e.g. when you compile with ChaosKey).
NB: GO may hint about the lack of several dependancies, imported libraries from our main.go. Follow on-screen instructions to proceed with "go get" libraries installation.

### Mature PoC
The whole project is just a showcase and PoC using very very old PCI-not-Express motherboard, an old-unsuppoorted QRNG card by ID Quantique (as support ended with Kernel 4, I had to migrate some calls to make it compile on Kernel(s) 5 and 6. PC is equipped with a very old Core Duo 2, having only two cores, about 3Ghz and a bus limited to 3Gbit (I believe is the old PCI bandwidth).

### Performances
Nonetheless this software was easily able to respond up to 29'500 requests per second (with payload of 1KB), and also reaching an impressive bandwidth of 380MB/s (payload 256KB) on that very same hardware dating circa 2012.



