# Technical reference

The [main README](../README.md) provides an introduction and a two-minute quick start. Detailed references are split here to keep the front page approachable without losing original technical material.

| Document | Contents |
| --- | --- |
| [DRBG construction](drbg-construction.md) | Byte-level root/child state transitions, reseed diagram, and limits of the cryptographic assurance claim. |
| [Experimental Barzakh-521](experimental-barzakh.md) | Opt-in original Go scalar backend, build instructions, exact mapping, tests and licensing/patent boundary. |
| [Process-memory hardening](process-memory-hardening.md) | Opt-in systemd protection, core-dump caveats and host security checklist. |
| [DRBG lifecycle benchmarks](drbg-lifecycle-benchmarks.md) | Reproducible microbenchmark suite, before/after comparison, and interpretation. |
| [Full architecture review](architecture-review.md) | Five-question assurance assessment, component map, findings and remediation roadmap. |
| [Security boundaries & threat model](security-boundaries.md) | Trust boundaries, seed failure modes, adversaries, explicit non-guarantees and prioritized threat register. |
| [Configuration](configuration.md) | Server and client flags, timing semantics, source selection and client analysis settings. |
| [Client experiments](client-experiments.md) | Configurable Shannon assessment, timed/headless runs, JSONL schema and reproducible experiments. |
| [Client write-out](client-writeout.md) | Linux pool mixing, limited value, device validation, failure handling and progress counters. |
| [Adaptive client terminal](client-terminal.md) | Compact/full layouts, sizing, bounded history, screenshots and display-cost trade-offs. |
| [Browser showcases](browser-ui.md) | Embedded HTTP gallery, stream counters, word rendering and consolidated status dashboard. |
| [Experimental source/socket labs](source-labs.md) | Generate, meter and compare sources; browser media and registered peer/device/socket connections. |
| [API reference](api.md) | HTTP/WebSocket routes, parameters, formats, browser pages, upload protocol and health checks. |
| [Operations](operations.md) | Memory and concurrency budgets, readiness, shutdown behavior, profiling and diagnostics. |
| [Benchmarks](benchmarks.md) | Measured encoding cost and HTTP throughput, original `wrk` output and reproducibility guidance. |
| [Root reseeding review](root-reseeding.md) | Seed input-size hardening, failure semantics, state-transition diagram and tests. |
| [Statistical tests](statistical-tests.md) | `rngtest`, Dieharder and their limitations. |
| [Randomness screening](randomness-screening.md) | PractRand/NIST/TestU01 results for three backends, runtime budgets and an optional manual workflow. |
| [Security policy](../SECURITY.md) | Source/DRBG trust boundaries, exposure, disclosure and TLS keys. |
| [Contribution guide](../CONTRIBUTING.md) | Build, race tests, lint, benchmark evidence and changes. |
| [Go quality review](go-quality.md) | Go conventions, module/toolchain policy, measured coverage, remaining tests and README badge meanings. |

The reference pages retain the detailed descriptions formerly contained in the README; measurements and API conventions are not rewritten as new guarantees.
