# Security policy and deployment boundaries

The optional `experimental_barzakh` build includes a research-only
Barzakh-521 backend using variable-time arithmetic. It has no independent
security audit and is unsuitable for secrets. Normal release builds exclude
it. See [its construction and limitations](docs/experimental-barzakh.md).

Entropy Service is a research-oriented, self-hosted random-data distribution application. Its cryptographic design has **not** been independently audited or certified; no FIPS 140 validation or NIST CTR_DRBG conformance is claimed. Passing statistical tests is not evidence of cryptographic certification.

## Current architectural assurance review

This policy is a deployment checklist, not a cryptographic security guarantee.
For **verified implementation boundaries, source-failure behavior, adversary
assumptions, explicit out-of-scope threats and a prioritized risk register**,
read [Security Boundaries & Threat Model](docs/security-boundaries.md).
The accompanying [Full Architecture Review](docs/architecture-review.md)
answers the five larger questions about architecture, cryptography,
operational stability, performance reproducibility and project purpose.

The reviewed version remains a **research-oriented application**, not a
validated hardware entropy source, HSM or certified DRBG.

## Reporting vulnerabilities

Please report suspected security vulnerabilities **privately**, preferably with GitHub's **Report a vulnerability** feature for this repository (if enabled). If private vulnerability reporting is unavailable, contact the repository maintainer privately through their GitHub profile before sharing details. Avoid posting exploits, private keys, service addresses, or sensitive logs in public issues. There is no promised response-time SLA.

## Supported versions

Security fixes are maintained on a best-effort basis for the current `main` branch and the newest tagged release. Older tags are not guaranteed to receive security backports. Review the release notes and repository history before upgrading.

## Source and DRBG boundaries

- The configured hardware device or operating-system CSPRNG supplies **seed material**. The selectable ChaCha20/AES-256-CTR implementation generates the bulk output; it does not continuously deliver hardware entropy for every byte.
- This is a project-specific DRBG/reseed construction, **not NIST CTR_DRBG**. Treat it as unsuitable for deployments requiring externally audited or certified cryptographic generators without further review.
- Startup opens and checks the selected character device. With `-require-device=true`, a nonempty specified path must open successfully and no fallback occurs. Without it, startup can fall back to `/dev/urandom` and then nonblocking Linux `getrandom` if needed. Unsupported device read deadlines count as selection failures. The source metadata reports the actual selected reader. This does **not** validate hardware entropy quality or change the runtime reseed failure policy.
- Serving-root generation now defaults to **fail closed**: `-on-reseed-failure=stop` refuses further output on a recorded reseed failure or excessive seed age, including existing child streams. Successful reseeding restores admission. Explicit `continue` permits existing-state output while readiness remains degraded. Delivered/in-flight bytes cannot be recalled, and source cancellation requires a conforming nonblocking/pollable driver; see [failure policy and boundaries](docs/reseed-failure-policy.md).
- The built-in statistical dashboards are diagnostics, not evidence of cryptographic assurance.

## Network and application exposure

- No built-in client authentication or authorization is provided. Expose endpoints only to trusted callers, or place the service behind appropriate access controls, quotas and a reverse proxy.
- Browser WebSocket requests use Gorilla's default same-host `Origin` policy. Non-browser clients without `Origin` may connect; **origin validation is not authentication**.
- TLS requires valid, deployment-specific certificates. The listener is configured for TLS 1.3; HTTP is also available and is unsuitable for sending sensitive random material over untrusted networks.
- Request-size limits and admission budgets reduce resource-exhaustion risk, but do not cap overall process RSS or eliminate all denial-of-service scenarios.
- The optional `pprof` listener is for debugging and should remain bound to loopback.

Preference cookies are disabled by default. With `-cookie=true`, the
`entropy_prefs` cookie contains only numeric byte/word/refresh settings and
is always `Secure`, `HttpOnly`, `SameSite=Lax` and host-only, with a one-day
lifetime. Use HTTPS directly or at the reverse proxy's browser-facing
boundary; untrusted forwarded headers never remove `Secure`. Cookie values
are unsigned, validated user input and provide no authentication or resource
limit exemption. Plain-HTTP deployments can use explicit query parameters.
See [cookie policy, migration and examples](docs/configuration.md#preference-cookies).

## TLS identities and the retired public demonstration key

No TLS certificate or private key is bundled. Earlier revisions contained
`cmd/entropy-server/cert.pem` and `cmd/entropy-server/key.pem`; that private key
is permanently public, including in Git history and old source archives.
The TLS loader now rejects its public-key fingerprint, even if the same key
is wrapped in a different certificate. Removal does not rotate an existing
deployment: operators must replace that identity, update client trust/pins,
and restart their deployment. This repository cannot establish which external
deployments still use it. No repository history rewrite is required.

`entropy-server generate-cert` explicitly generates a unique ECDSA P-256
self-signed server identity from Go's OS-backed `crypto/rand`, independently
of this service's custom DRBG or configured entropy source. It includes
DNS/IP SANs, defaults to seven days and creates both files with mode 0600.
It never overwrites existing files. `-if-missing` only reuses a complete,
currently valid pair covering the requested names with an owner-only key;
partial, expired, symlinked or mismatched pairs fail. Use operator-controlled
parent directories and monitor expiry; a crash during two-file creation
can leave a partial pair requiring explicit recovery.

See [local generation and startup provisioning](docs/tls-certificates.md).
Self-signed certificates need explicit client trust; use your organization's
CA/ACME lifecycle for public deployments. Normal HTTPS startup requires the
configured pair and never silently generates, replaces or disables TLS.
Do not commit replacement keys. CI rejects tracked PEM/key/PKCS#12 files and
private-key PEM headers in other tracked regular files. This small guard
does not replace a general-purpose secret scanner or deployment audit.

## Packaged service hardening and migration

The DEB/RPM unit is unprivileged, local-only and operator-started by default. It has no remote authentication, and disabling HTTPS is safe only for trusted local traffic. Installation does not grant new device access or alter ownership/udev configuration. Existing running/enabled installations retain their active process until explicitly restarted; inspect unit overrides and hardware permissions before upgrading. See [packaged deployment](docs/packaged-service.md).

## Security checklist

1. Choose an allowed entropy source, enforce `-require-device=true` when required, and monitor reseed freshness.
2. Keep the service on loopback or a protected network unless authentication, rate limits, and TLS termination are provided upstream.
3. Deploy unique TLS key material with appropriately restricted file permissions.
4. Review readiness and failure behavior before relying on generated bytes for any security-sensitive purpose.
5. Review updates, dependencies and platform assumptions; run the Go tests and race detector in your deployment environment.

See [architecture](README.md#architecture), [operations](docs/operations.md) and [configuration](docs/configuration.md).
