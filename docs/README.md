# Technical reference

The [main README](../README.md) provides an introduction and a two-minute quick start. Detailed references are split here to keep the front page approachable without losing original technical material.

| Document | Contents |
| --- | --- |
| [Full architecture review](architecture-review.md) | Five-question assurance assessment, component map, findings and remediation roadmap. |
| [Security boundaries & threat model](security-boundaries.md) | Trust boundaries, seed failure modes, adversaries, explicit non-guarantees and prioritized threat register. |
| [Configuration](configuration.md) | Server and client flags, timing semantics, source selection and client analysis settings. |
| [Browser showcases](browser-ui.md) | Embedded HTTP gallery, stream counters, word rendering and consolidated status dashboard. |
| [API reference](api.md) | HTTP/WebSocket routes, parameters, formats, browser pages, upload protocol and health checks. |
| [Operations](operations.md) | Memory and concurrency budgets, readiness, shutdown behavior, profiling and diagnostics. |
| [Benchmarks](benchmarks.md) | Measured encoding cost and HTTP throughput, original `wrk` output and reproducibility guidance. |
| [Statistical tests](statistical-tests.md) | `rngtest`, Dieharder and their limitations. |
| [Security policy](../SECURITY.md) | Source/DRBG trust boundaries, exposure, disclosure and TLS keys. |
| [Contribution guide](../CONTRIBUTING.md) | Build, race tests, lint, benchmark evidence and changes. |

The reference pages retain the detailed descriptions formerly contained in the README; measurements and API conventions are not rewritten as new guarantees.
