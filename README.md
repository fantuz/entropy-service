# entropy-service

### Theoretical purposes
GO-based software intended to let users fetch randomness via simple API(s).
Behind my setup, entropy is provided by a real QRNG, Quantum Random Number Generator, made by ID Quantique in Geneva. This entropy is then fed into a DRBG module which "amplifies" output of QRNG, using either ChaCha20 or AES-CRT functions. The software can be easily adapted to source entropy from various and different sources, even Linux PRNG itself (for testing only) or an USB Chaos Key (low entropy, still deterministic in a way). Possibilities are endless.

### Practical implementations
With this simple yet very performant software, users can setup their own cryptographically-strong randomness source, and use API(s) to retrieve different amounts of binary randomness, randomly-generated images, and even sounds (later feature to be added soon).

### GO programming techniques and logics implemented, including:
- socket management
- per-connection DRBG
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
- random sound generator
- ...

### Supported/tested hardware
```
 - Bus 001 Device 003: ID 1d50:60c6 OpenMoko, Inc. USBtrng hardware random number generator
 - Quantis PCI by ID Quantique
 - pretty much any character device under Linux, including kernel RNG
```

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
The whole project is just a showcase and PoC built around the use of a rather old PCI card (not PCI0e), a QRNG produced by ID Quantique. Given that support ended with Kernel 4, I had to migrate myself some syscalls to make the drivers compile on Kernel(s) 5 and 6.

### Performances
Software was proven able to respond up to 70'000 requests per second (with payload of 64B), or reaching an impressive bandwidth of 1GB/s (payload 512KB) on a 10+ years old hardware.
```
max@iMac:~/entropy-service$ wrk -t16 -c64 -d5 --latency --timeout 1 http://127.0.0.1:8080/v1/random?bytes=1048576
Running 5s test @ http://127.0.0.1:8080/v1/random?bytes=1048576
  16 threads and 64 connections
  Thread Stats   Avg      Stdev     Max   +/- Stdev
    Latency    92.95ms  106.46ms 704.04ms   86.57%
    Req/Sec    62.26     35.23   202.00     70.57%
  Latency Distribution
     50%   48.30ms
     75%  144.44ms
     90%  231.82ms
     99%  484.71ms
  4838 requests in 5.03s, 4.73GB read
Requests/sec:    962.23
Transfer/sec:      0.94GB

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

max@iMac:~/entropy-service$ grep model\ name /proc/cpuinfo
model name	: Intel(R) Core(TM) i5-3470 CPU @ 3.20GHz
model name	: Intel(R) Core(TM) i5-3470 CPU @ 3.20GHz
model name	: Intel(R) Core(TM) i5-3470 CPU @ 3.20GHz
model name	: Intel(R) Core(TM) i5-3470 CPU @ 3.20GHz
```


