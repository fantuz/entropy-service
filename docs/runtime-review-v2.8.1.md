# Runtime and build review of v2.8.1

## Cost of a large WebSocket sample

An 8 MiB sample on the default JSON form of `/bytes` or `/colors` produces a 27,962,125-byte text message. v2.8.1 creates the full hexadecimal and Base64 strings before JSON serialization. Streaming the same fields through a buffer of at most 32 KiB removes most allocation from this path. Each message still includes both encodings, the SHA-256 digest of the entire sample and the trailing newline. WebSocket fragmentation can vary.

| Sample | v2.8.1 encoder | Streaming encoder | v2.8.1 allocated bytes/op | Streaming allocated bytes/op |
| --- | ---: | ---: | ---: | ---: |
| 2 KiB | 17.9 µs | 9.6 µs | 14,544 | 10,720 |
| 512 KiB | 3.81 ms | 1.20 ms | 3,569,674 | 35,296 |
| 8 MiB | 61.27 ms | 18.71 ms | 71,601,736 | 35,296 |

Medians from five runs on a shared Linux amd64 host with an AMD EPYC 9V74 and Go 1.26.11. The benchmark uses one logical CPU, a prepared sample and `io.Discard`. It includes hashing and encoding. Allocated bytes/op measures allocation traffic and excludes the preallocated input. Process RSS requires a separate measurement. Run the encoder benchmark from the repository root.

```sh
go test ./cmd/entropy-server -run '^$' -bench '^BenchmarkByteFrameFormats/.*KiB/json$' -benchmem -cpu=1 -benchtime=250ms -count=5
```

A second comparison ran the server and a Go WebSocket reader on separate logical CPUs over plaintext loopback. The server used AES-256-CTR with an OS seed. One client requested `/bytes?bytes=8388608&format=json&refresh=1`, discarded the first message and drained eight complete messages per run. Across three runs per binary, median time per message fell from **63.1 ms to 37.1 ms**. Every message had the expected length. WAN bandwidth, TLS, multiple clients and physical RNG devices require separate measurements.

The encoder uses private scratch per message. Server admission still reserves conservatively for JSON, and the freshness checks still run before generation. A receiver needing the digest and every byte can select `format=compact` for an 8,388,640-byte binary message at this sample size.

## Browser, desktop and CI findings

- A dashboard restored from the browser's back/forward cache could stay frozen. Its polling loop now suspends in the background, restarts on return and discards results from the previous visit. One pass over each Prometheus scrape supplies both headline counters and derived metrics.
- The GTK server editor now exposes the HTTP and WebSocket concurrency and memory limits that the server CLI already validates. The client GUI orders throughput readings by their elapsed time. An older periodic report previously overwrote a newer sample during the same redraw.
- The Go Actions cache key now includes assembly sources, including the amd64 RDRAND implementation. The installer-locale test explicitly runs on cache hits. For five v2.8.1 macOS, Windows and OCI downloads, a second pass of level-six DEFLATE saved 0.22–0.68% per file, or 0.41% combined. CI now stores those already-compressed artifacts without a second compression pass.

## Follow-up measurements and changes

- **Browser preview bandwidth.** With an 8 MiB sample, the byte viewer received 8,388,640 application bytes in compact mode. Its new preview protocol delivers 292 bytes. At 20 delivered messages per second, that amounts to 5,840 bytes/s. Generation and SHA-256 still cover 160 MiB of sample data each second at that rate. For samples up to 256 bytes, preview carries four additional length bytes compared with compact. [Wire formats](api.md)
- **Concurrent reseed latency.** On KVM with RDRAND exposed, the 99th percentile for applying a root freshness update with four concurrent 32 MiB reads ranged from 155 to 208 ms across three runs of 100 trials. The test exercises the existing serving-policy lock and can be repeated on bare metal. [Method and results](experimental-rdrand.md#reseed-latency-during-concurrent-reads)
- **GUI backend choices.** The local companion CLI now reports the compiled acquisition or generation backends and whether the host meets their selection requirements. The editor uses that inventory, including for pure-Go and tagged builds. [Descriptor fields](configuration.md)
