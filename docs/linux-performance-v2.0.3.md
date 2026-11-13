# Linux performance review: v2.0.3 versus pre-Windows

Date: 2026-11-12T18:59:09.379152+01:00 (Europe/Zurich).

Later follow-up: [2026-11-13 Linux and browser performance review](linux-performance-2026-11-13.md) compares newer main revisions, translations and the optimized renderer. The measurements below remain the original v2.0.3 snapshot.

## Finding

No major Linux performance regression attributable to Windows support was found in the inspected code and measured workloads. v2.0.3 remains in the same performance range as the immediately preceding Linux-only revision. This is an estimate on a shared VM, not a precise zero-overhead guarantee or a production capacity rating.

## Revisions and method

- Baseline: `64a8d61437d2494414c30debbaf296538f17a31b` (`64a8d61`, immediately before Windows integration).
- Initial Windows port: `ef6362bbb8642f4b565094c614d66dba7fc75e53` (tag `v2.0.0`), used for the focused attribution check.
- Current: `e7df8569bca23ca04996d8c725d0e883f5979f32` (tag `v2.0.3`).
- Tags `v2.0.0`, `v2.0.1` and `v2.0.2` already include Windows, so they are not valid pre-port baselines.
- Both revisions were built from source on the same Linux amd64 VM with Go 1.26.11, CGO disabled, GOAMD64=v1, matching stripped linker flags and no race instrumentation. These are controlled source builds, not downloaded release assets.
- CPU: AMD EPYC 9V74 with AES acceleration; five visible CPUs and a four-core cgroup quota. GOMAXPROCS=1 and GOGC=100. Microbenchmarks pinned to CPU 0; actual server pinned to CPU 1 and load generator to CPU 2. Benchmarks were not run concurrently.
- Microbenchmarks: identical benchmark functions in both revisions, five alternating-order samples per case, 300 ms each, allocation reporting. 11 cases / 110 individual measurements.
- End-to-end: three alternating-order pairs per backend/protocol/size, 1.5 s per sample; HTTP/1.1 keep-alive, plaintext and verified TLS 1.3, two HTTP clients or one binary WebSocket client. 48 samples with full payload-length checks and zero transfer errors. Tests include connection establishment, amortized across each run.
- WebSocket stress tests request 2 MiB frames at a 1 ms interval to expose generation/transport headroom. These are saturation tests, not a claim about the normal browser/client 20-FPS setting or physical Ethernet bandwidth.
- Focused follow-up: 64 KiB AES HTTP, three source revisions, one-second warm-up, three-second measurements, four rotated-order samples each. 12 samples with zero transfer errors.

## Source inspection

- All six production DRBG Go source files are byte-identical; go.mod and go.sum are identical. Client statistics (six files) and diagnostic calculations (nine files) are byte-identical.
- The randomBytesHandler, randomHandler and wsBinaryHandler bodies are byte-identical. Linux listener implementation is unchanged apart from its !windows build constraint.
- Windows RNG, WinUSB, PCI, ACL and listener implementations are excluded from Linux compilation by OS filenames/build constraints. Linux keeps its native sockets, RNG and optimized AES/ChaCha implementations.
- Hardware inventory, probes and raw capture execute only on explicit commands/source selection. They do not introduce a hardware-polling worker into ordinary OS-source serving.
- Shared changes mainly affect startup/source validation, optional hardware, connection cancellation and output reporting. The new absolute-deadline check occurs when reading seed material, not on every generated payload byte. Defaults reseed 256 bytes every 15 seconds.
- Client WebSocket cancellation bookkeeping applies when establishing a connection. JSONL stdout now has context-aware output overhead per report record; it is optional and not a Windows syscall on Linux. Linux exports have one nil callback check per WriteContext call; file creation retains exclusive owner-only semantics.
- Six-decimal display/status formatting is a later UI change. It is separate from Windows integration. Large-waterfall allocation count increased from 55 to 57 per render, with roughly unchanged total bytes/time.
- TLS certificate provisioning/reuse checks are startup/command work; TLS serving configuration and data handling retain their existing implementation.

## Microbenchmark medians

Positive time changes mean slower. Bytes/op are allocated heap bytes, not bytes sent over the network. Small time differences and apparent speedups should not be treated as established improvements on this shared VM.

| Benchmark | Before, ms/op | v2.0.3, ms/op | Time change | Allocations/op before → current | Heap bytes/op before → current |
| --- | ---: | ---: | ---: | ---: | ---: |
| ClientAnalysis16MiB/Default1MiBPrefix | 3.746226 | 3.770523 | +0.6% | 1 → 1 | 80 → 80 |
| DRBGLifecycle/aes-256-ctr/ChildCreateClose | 0.001045 | 0.001004 | -3.9% | 5 → 5 | 1424 → 1424 |
| DRBGLifecycle/aes-256-ctr/Read64KiB | 0.010851 | 0.011045 | +1.8% | 0 → 0 | 0 → 0 |
| DRBGLifecycle/chacha20/ChildCreateClose | 0.000910 | 0.000978 | +7.5% | 4 → 4 | 576 → 576 |
| DRBGLifecycle/chacha20/Read64KiB | 0.108007 | 0.106420 | -1.5% | 0 → 0 | 0 → 0 |
| FullMonitor240x60 | 4.797040 | 4.545662 | -5.2% | 1607 → 1607 | 684539 → 684541 |
| HTTPGeneration/AES-256-CTR/random/2097152 | 0.393420 | 0.391845 | -0.4% | 45 → 45 | 36741 → 36741 |
| HTTPGeneration/AES-256-CTR/random/65536 | 0.031590 | 0.030218 | -4.3% | 45 → 45 | 36731 → 36731 |
| HTTPGeneration/ChaCha20/random/2097152 | 3.435247 | 3.597126 | +4.7% | 44 → 44 | 35893 → 35893 |
| HTTPGeneration/ChaCha20/random/65536 | 0.129353 | 0.131638 | +1.8% | 44 → 44 | 35883 → 35883 |
| WaterfallScreen900x180 | 14.795338 | 14.882759 | +0.6% | 55 → 57 | 8.43788e+06 → 8.4385e+06 |

Bulk DRBG read timings differ by about −1.5% (ChaCha20) and +1.8% (AES). HTTP handler timings are approximately −4.3%..+4.7%. The +7.5% child-creation result is about 68 ns per child, with unchanged allocation counts and unchanged source; it does not translate into a large-payload generation regression. No bulk-generation/HTTP/analysis allocation growth was found.

## End-to-end loopback medians

| Backend | Protocol | Payload | Before MiB/s | v2.0.3 MiB/s | Throughput change | CPU seconds/GiB before → current |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| aes-256-ctr | http | 64 KiB | 704.8 | 629.4 | -10.7% | 1.414 → 1.573 |
| aes-256-ctr | http | 2048 KiB | 1817.2 | 1907.9 | +5.0% | 0.560 → 0.537 |
| aes-256-ctr | https | 2048 KiB | 1127.9 | 1111.9 | -1.4% | 0.902 → 0.921 |
| aes-256-ctr | ws | 2048 KiB | 1819.4 | 1817.2 | -0.1% | 0.345 → 0.347 |
| chacha20 | http | 64 KiB | 336.0 | 331.2 | -1.4% | 3.027 → 3.070 |
| chacha20 | http | 2048 KiB | 457.3 | 427.9 | -6.4% | 2.224 → 2.408 |
| chacha20 | https | 2048 KiB | 381.3 | 364.0 | -4.5% | 2.685 → 2.794 |
| chacha20 | ws | 2048 KiB | 535.5 | 553.0 | +3.3% | 1.923 → 1.851 |

The initial 64 KiB AES HTTP test showed a 10.7% reduction in its short-run median. It was investigated rather than treated as proof of a regression. After a warm-up and longer, rotated-order samples, the difference reversed:

| Warm 64 KiB AES HTTP | Median MiB/s | Sample range MiB/s | CPU seconds/GiB |
| --- | ---: | ---: | ---: |
| baseline | 722.2 | 695.6–754.9 | 1.371 |
| windows-port | 823.4 | 751.2–838.8 | 1.205 |
| v2.0.3 | 739.2 | 697.7–783.1 | 1.329 |

The warm v2.0.3 result is about 2.3% ahead of baseline, and the initial Windows port also shows no drop. This does not establish a Windows-induced speedup: it shows that the earlier short-run slowdown is not a stable finding. ChaCha20 large HTTP/TLS results were about 4–6% lower in the short samples, while its WebSocket throughput was about 3% higher. Given the spread, short durations and unchanged hot implementations, a dedicated-host test would be needed to assign small percentage changes to code.

## Footprint and practical bottlenecks

| Stripped source-build binary | Before bytes | v2.0.3 bytes | Change |
| --- | ---: | ---: | ---: |
| entropy-server | 12,124,322 | 12,402,850 | +2.30% |
| entropy-client | 6,795,426 | 6,807,714 | +0.18% |

Server idle RSS was approximately 17.3 → 17.7 MiB. Sampled loaded RSS was generally 19–23 MiB with a small increase in v2.0.3. These are short observations, not a leak/peak-memory qualification. Binary growth reflects added linked Linux/shared hardware and command code plus assets; Windows machine code is not bundled into the Linux executable.

The larger practical optimization target identified by this assessment was terminal presentation. The following rendering costs describe v2.0.3 before the optimization in the [current terminal guide](client-terminal.md). The original 900×180 benchmark uses zero-filled RGB rows; it describes repeated colors, not a random-pixel workload. At 900×180, that waterfall renderer takes about 14.9 ms and allocates about 8.0 MiB per refresh. At 20 refreshes/s, that is roughly 30% of one CPU core and 161 MiB/s of transient allocations, before terminal/SSH output cost. This cost already exists in the pre-Windows tree. At a more ordinary 240×60, full-monitor rendering is about 4.5 ms/refresh. Buffer reuse and reducing redundant ANSI output would be more promising than removing Windows support.

Keep an eye on backend selection, sample size, optional full-frame statistical analysis and redraw cadence. On this AES-capable amd64 host, AES is much faster than ChaCha20; these absolute ratios should not be transferred to ARM or CPUs without AES acceleration.

## Limits and reproduction

- Shared cloud VM, limited repetitions, short runs and a one-core server. No claim of an exact zero-percent cost or multi-core production capacity.
- No ARM, physical USB/PCI device, long-duration leak, physical network or real terminal emulator benchmark. Rendering benchmarks build output strings without writing a terminal.
- The original assessment made no production source changes. Its results are archived here before the subsequent waterfall optimization; these measurements describe the unoptimized v2.0.3 release.
- Benchmark sources are existing repository benchmarks. Rebuild with the same toolchain/environment and run the same functions in each checkout. [Raw benchmark samples](performance/v2.0.3/microbench-samples.csv), [transfer samples](performance/v2.0.3/traffic-samples.csv) and [environment](performance/v2.0.3/environment.json) retain the observed results; transfer samples distinguish the initial sweep from the warm follow-up.

Example focused commands (from each worktree):

```sh
CGO_ENABLED=0 GOMAXPROCS=1 GOGC=100 go test -run "^$" -bench "^BenchmarkDRBGLifecycle$/(chacha20|aes-256-ctr)/(Read64KiB|ChildCreateClose)$" -benchmem -benchtime=300ms -count=5 -cpu=1 ./cmd/entropy-server/internal/drbg
CGO_ENABLED=0 GOMAXPROCS=1 GOGC=100 go test -run "^$" -bench "^BenchmarkHTTPGeneration$/(ChaCha20|AES-256-CTR)/random/(65536|2097152)$" -benchmem -benchtime=300ms -count=5 -cpu=1 ./cmd/entropy-server
CGO_ENABLED=0 GOMAXPROCS=1 GOGC=100 go test -run "^$" -bench "^BenchmarkClientAnalysis16MiB$/^Default1MiBPrefix$" -benchmem -benchtime=300ms -count=5 -cpu=1 ./cmd/entropy-client
```

For comparisons, alternate the revision order rather than interpreting a single before/after run. Pin each server/load generator to separate CPUs and alternate revision order, keeping backend, payload, connection count and toolchain fixed.

Supporting prior analysis: [Windows port Linux runtime cost](https://github.com/fantuz/entropy-service/blob/v2.0.3/docs/windows.md#linux-runtime-cost). Source comparison: [64a8d61…v2.0.3](https://github.com/fantuz/entropy-service/compare/64a8d61...v2.0.3).
