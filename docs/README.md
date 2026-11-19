# Technical reference

The [main README](../README.md) provides an introduction and a two-minute quick start. Detailed references are split here to keep the front page approachable without losing original technical material.

| Document | Contents |
| --- | --- |
| [Certificate tools](certificate-bootstrap-design.md) | Browser-local self-signed certificates, server/browser entropy, TLS test uses, provisioning and trust limits. |
| [Windows GUI installer](windows-gui-installer.md) | Self-contained per-user Windows GUI setup, runtime/source provenance and native installer tests. |
| [GUI validation and load observations](desktop-gui-validation.md) | Large-frame investigation, failure diagnostics, native settings checks and remaining manual coverage. |
| [Windows Service](windows-service.md) | Native SCM installation, account/state policy, certificates, stop/restart, logs and upgrades. |
| [macOS CLI and GUI](macos.md) | Intel/Apple Silicon binaries, Homebrew GTK launcher, OS source and platform limits. |
| [v2.6.0 release notes](releases/v2.6.0.md) | Linux/Windows/macOS downloads, Homebrew GUI and offline OCI distribution. |
| [v2.5.0 release notes](releases/v2.5.0.md) | Experimental direct CPU output, resilient GHCR publication and parallel CI. |
| [v2.4.1 release notes](releases/v2.4.1.md) | Tested Windows GUI installer, matching runtime sources and release checksums. |
| [v2.4.0 release notes](releases/v2.4.0.md) | Native Windows Service lifecycle, dedicated account, protected state and certificates. |
| [v2.3.1 release notes](releases/v2.3.1.md) | Container distribution, readiness, image maintenance and retry/export test reliability. |
| [DRBG construction](drbg-construction.md) | Byte-level root/child state transitions, reseed diagram, and limits of the cryptographic assurance claim. |
| [Experimental direct RDRAND](experimental-rdrand.md) | Native x64 CPU output, trust and failure boundaries, Linux/Windows examples and repeatable timings. |
| [Experimental Barzakh-521](experimental-barzakh.md) | Opt-in original Go scalar backend, build instructions, exact mapping, tests and licensing/patent boundary. |
| [Process-memory hardening](process-memory-hardening.md) | Opt-in systemd protection, core-dump caveats and host security checklist. |
| [DRBG lifecycle benchmarks](drbg-lifecycle-benchmarks.md) | Reproducible microbenchmark suite, before/after comparison, and interpretation. |
| [Linux v2.0.3 performance assessment](linux-performance-v2.0.3.md) | Pre-Windows comparison, generator/HTTP/client measurements, source inspection, raw samples and limitations. |
| [Architecture and assurance status](architecture-review.md) | Current controls, resolved historical findings, remaining assurance gaps and original review archive. |
| [Coverage review, November 13](coverage-review-2026-11-13.md) | Exact-main CI baseline, weighted package counts, meaningful gaps and platform-test boundaries. |
| [Maintenance change report, November 13](maintenance-review-2026-11-13.md) | Removed/rephrased claims, implementation evidence and unused-source cleanup inventory. |
| [Security boundaries & threat model](security-boundaries.md) | Trust boundaries, seed failure modes, adversaries, explicit non-guarantees and prioritized threat register. |
| [Configuration](configuration.md) | Server and client flags, timing semantics, source selection and client analysis settings. |
| [Containers and GHCR](containers.md) | Server/client Linux images, Docker/Compose, TLS, persistent exports, hardware access, offline OCI downloads and registry fault tests. |
| [Optional Windows port](windows.md) | Native x64 builds, OS randomness, capability limits, private ACL files, console output and validation. |
| [Experimental Quantis PCI adapter](quantis.md) | Windows legacy PCI driver interface, explicit hardware seeding, validation limits and Linux device integration. |
| [Hardware discovery and probe](hardware-probe.md) | Read-only inventory, driver binding, explicit source checks, JSON reports and limits of hardware assurance. |
| [Hardware drivers and source layers](hardware-drivers.md) | Legacy card evidence, bounded Linux overlay, Windows package audit and native USB/ChaosKey source layers. |
| [Hardware command examples](hardware-examples.md) | Linux/Windows Quantis and ChaosKey probes, strict startup, TLS and separate cooked/raw captures. |
| [Hardware qualification records](hardware-qualification.md) | Build/simulation/physical evidence matrix, physical-result template, throughput boundaries and cancellation measurements. |
| [Experimental Quantis USB](quantis-usb.md) | Native WinUSB and Linux usbfs seeding, model/driver prerequisites, deadlines and physical-test limits. |
| [Experimental ChaosKey USB](chaoskey.md) | Cooked seeding, explicit raw diagnostic capture, Linux/Windows access, sample interpretation and qualification limits. |
| [Client experiments](client-experiments.md) | Configurable Shannon assessment, timed/headless runs, JSONL schema and reproducible experiments. |
| [Client write-out](client-writeout.md) | Linux pool mixing, limited value, device validation, failure handling and progress counters. |
| [Raw client exports](client-output.md) | Private files, raw stdout/SSH pipes, Linux devices, exact byte limits and delivery accounting. |
| [Client recovery](client-recovery.md) | Disconnect causes, opt-in bounded retries, backoff, permanent failures and export limits. |
| [Client TLS trust](client-tls.md) | HTTPS/WSS verification, Linux public-certificate trust, IPv4 SANs, renewal and downgrade protection. |
| [Adaptive client terminal](client-terminal.md) | Compact/full layouts, sizing, bounded history, screenshots and display-cost trade-offs. |
| [Browser showcases](browser-ui.md) | Embedded HTTP gallery, stream counters, word rendering and consolidated status dashboard. |
| [Visual diagnostics](visual-diagnostics.md) | Optional dependence/transition/local-block plots, spectral history and explicit PCM audio/notch comparison. |
| [Graph colour palettes](graph-palettes.md) | Three graph palettes, persistence, contrast and colour-independent cues. |
| [Stale material review, November 13](stale-material-review-2026-11-13.md) | Proposed cleanup candidates and stale wording, with evidence; no removals performed. |
| [Experimental source/socket labs](source-labs.md) | Generate, meter and compare sources; browser media and registered peer/device/socket connections. |
| [LAB access and device-read safety](lab-safety.md) | DNS-rebinding Host checks, custom local names, cancellation ownership, native-call limits and regression coverage. |
| [Word languages](word-languages.md) | 18 pinned dictionary choices, language preferences, selection-space differences, provenance and translation guidance. |
| [API reference](api.md) | HTTP/WebSocket routes, parameters, formats, browser pages, upload protocol and health checks. |
| [Operational monitoring](monitoring.md) | Loaded TLS expiry, Prometheus alerts and external HTTPS probes. |
| [Operations](operations.md) | Memory and concurrency budgets, readiness, shutdown behavior, profiling and diagnostics. |
| [Endurance and fault tests](endurance-tests.md) | Repeatable mixed HTTP/TLS/WS, source faults, cancellation, resource evidence and longer Linux/Windows CI runs. |
| [Benchmarks](benchmarks.md) | Measured encoding cost and HTTP throughput, original `wrk` output and reproducibility guidance. |
| [Root reseeding review](root-reseeding.md) | Seed input-size hardening, failure semantics, state-transition diagram and tests. |
| [Reseed failure policy](reseed-failure-policy.md) | Default fail-closed serving, explicit continue mode, recovery, reasoning, migration and blocked-read boundaries. |
| [Statistical tests](statistical-tests.md) | `rngtest`, Dieharder and their limitations. |
| [Randomness screening](randomness-screening.md) | PractRand/NIST/TestU01 results for three backends, runtime budgets and an optional manual workflow. |
| [Security policy](../SECURITY.md) | Source/DRBG trust boundaries, exposure, disclosure and TLS keys. |
| [Contribution guide](../CONTRIBUTING.md) | Build, race tests, lint, benchmark evidence and changes. |
| [Go quality review](go-quality.md) | Go conventions, module/toolchain policy, measured coverage, remaining tests and README badge meanings. |

The reference pages retain the detailed descriptions formerly contained in the README; measurements and API conventions are not rewritten as new guarantees.

[Entropy source I/O deadlines and driver migration](source-io.md).

[Client capability discovery, performance and optional adaptive pacing](client-capabilities-performance.md) describes operator limit discovery, rate/timing interpretation and HTTP-only adaptation.
