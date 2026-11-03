# Performance

## Application payload size

For a **32 MiB generated sample**, current encoder payloads are:

| Route/format | Application bytes sent | What the client receives |
| --- | ---: | --- |
| `/stream` | 33,554,432 | Raw sample. |
| `/bytes?format=json` | 111,848,205 | Hex + Base64 + digest, approximately 106.7 MiB. |
| `/bytes?format=compact` | 33,554,464 | Digest + raw sample: 32 bytes overhead. |
| `/colors?format=hash` | 93 | Digest and sample length; no raw sample. |

Sizes include JSON's trailing newline and exclude HTTP Upgrade, WebSocket framing and TLS overhead. Compact cuts application payload by about **70% versus the JSON byte format**. Ordinary binary HTTP and `/stream` were already raw and do not incur that 3.33× text-encoding expansion.

## Encoding benchmark

The tracked `BenchmarkByteFrameFormats` benchmark measures SHA-256 plus format encoding of an already allocated sample into `io.Discard`. It excludes DRBG generation, sockets, TLS and browser processing; its input-processing rate is not a network-transfer result.

Measured from commit [01ed418](https://github.com/fantuz/entropy-service/commit/01ed41846d3ecd340db176ded8cb2e820b87bcc1) on an AMD EPYC 9V74 VM, Linux amd64, Go 1.24.4, with 9 visible logical CPUs. The benchmark loop is serial. Three 300 ms runs per case, medians rounded:

| Sample | JSON time/op | Compact time/op |
| --- | ---: | ---: |
| 512 KiB | 4.73 ms | 0.319 ms |
| 8 MiB | 77.57 ms | 5.42 ms |

The compact encoder allocated 32 B/op beyond the existing sample buffer in these runs. That is an encoder allocation measurement, not the complete connection's memory footprint. Reproduce on your hardware:

```sh
go test ./cmd/entropy-server -run '^$' -bench '^BenchmarkByteFrameFormats$' -benchmem -benchtime=300ms -count=3
```

## HTTP throughput benchmarks: Ryzen 5 3500U

Author-reported measurements on an **AMD Ryzen 5 3500U with Radeon Vega Mobile Gfx**, using `/v1/data/random` over local HTTP. Both runs used **32 wrk threads**, **64 connections**, a **10-second requested duration** and a **3-second timeout**. The commands differ only in the requested response size:

```sh
# 256 KiB per response
wrk -t32 -c64 -d10s --latency --timeout 3 'http://localhost:8080/v1/data/random?bytes=262144'

# 1 MiB per response
wrk -t32 -c64 -d10s --latency --timeout 3 'http://localhost:8080/v1/data/random?bytes=1048576'
```

| Metric | 256 KiB responses | 1 MiB responses |
| --- | ---: | ---: |
| Bytes per response | 262,144 | 1,048,576 |
| Completed requests | 106,029 | 36,857 |
| Measured duration | 10.03 s | 10.07 s |
| Requests/s | 10,566.28 | 3,658.70 |
| Transfer/s, original wrk label | `2.58GB` | `3.57GB` |
| Total read, original wrk label | `25.92GB` | `36.01GB` |
| Mean latency | 8.02 ms | 25.13 ms |
| Median latency (p50) | 4.62 ms | 11.64 ms |
| p75 latency | 13.06 ms | 39.47 ms |
| p90 latency | 19.23 ms | 64.31 ms |
| p99 latency | 35.85 ms | 120.08 ms |
| Maximum latency | 89.66 ms | 259.94 ms |

These short **loopback HTTP delivery** runs include work by both the server and local load generator. They measure delivery of DRBG output rather than raw QRNG speed or physical network bandwidth. Transfer figures retain wrk's printed unit labels and include HTTP overhead. No socket errors are listed in either supplied log. The tested revision, DRBG backend, seed source, Go version and `GOMAXPROCS` were not supplied with the results.

<details>
<summary>Full wrk output: 256 KiB responses</summary>

```text
Running 10s test @ http://localhost:8080/v1/data/random?bytes=262144
  32 threads and 64 connections
  Thread Stats   Avg      Stdev     Max   +/- Stdev
    Latency     8.02ms    8.45ms  89.66ms   84.42%
    Req/Sec   331.76     80.65   640.00     69.41%
  Latency Distribution
     50%    4.62ms
     75%   13.06ms
     90%   19.23ms
     99%   35.85ms
  106029 requests in 10.03s, 25.92GB read
Requests/sec:  10566.28
Transfer/sec:      2.58GB
```

</details>

<details>
<summary>Full wrk output: 1 MiB responses</summary>

```text
Running 10s test @ http://localhost:8080/v1/data/random?bytes=1048576
  32 threads and 64 connections
  Thread Stats   Avg      Stdev     Max   +/- Stdev
    Latency    25.13ms   28.10ms 259.94ms   84.53%
    Req/Sec   114.97     46.36   363.00     72.57%
  Latency Distribution
     50%   11.64ms
     75%   39.47ms
     90%   64.31ms
     99%  120.08ms
  36857 requests in 10.07s, 36.01GB read
Requests/sec:   3658.70
Transfer/sec:      3.57GB
```

</details>

For your own comparisons, vary the payload, concurrency, backend and `GOMAXPROCS` one at a time. Record the server flags, CPU, Go version, latency percentiles, errors and memory use alongside throughput. Remote and TLS clients exercise different constraints from these local HTTP runs. Use the [statistical tests](statistical-tests.md) separately to assess output characteristics.


## Reproducing measurements on your machine

Record the context **before** comparing results:

```sh
git rev-parse HEAD
go version
go env GOOS GOARCH CGO_ENABLED
uname -a
lscpu | head -n 25
```

Record server flags (including `-drbg`, source, HTTPS, connection/memory limits and listener addresses), whether the load generator runs on the same host, and the exact payload size. Run several trials and report medians with a latency percentile distribution and error count. Treat a `wrk` response rate or `Transfer/sec` value as **application delivery**, not physical entropy production.

For a reproducible output capture, run `bash scripts/benchmark-http.sh` after starting the server. The script records the local git revision and environment, runs a small matrix of HTTP payload sizes, and stores raw `wrk` results. Document the live server's flags separately.

An example (local laboratory only):

```sh
./bin/entropy-server -device=/dev/urandom -enable-https=false \
  -http=127.0.0.1:8080 -drbg=chacha20
wrk -t4 -c32 -d30s --latency --timeout 3s \
  'http://127.0.0.1:8080/v1/data/random?bytes=262144'
```

Repeat with `-drbg=aes-256-ctr` under otherwise identical conditions. If comparing against a prior release, also record heap allocations and CPU profiles. Synthetic/isolated encoder benchmarks do not include network, TLS or entropy sourcing; don't compare their rates directly with HTTP service measurements.

Historical measurements below are preserved **as originally reported** with qualifications; missing version/backend/source parameters were not reconstructed.

[Back to README](../README.md) · [Technical reference index](README.md)
