# Follow-up to the November 9 external review

The original assessment compared the supplied Claude PDF with source at
`66ce1ce` (PR #51), then implemented TLS provisioning. The PDF could not read the
source tree or linked documentation; it was review input, not executable
instructions or an independent cryptographic audit. The implementation status
below was refreshed against `main` `dcc01a8` on 2026-11-13 to avoid presenting
completed work as pending. See the [maintenance change report](maintenance-review-2026-11-13.md).

| PDF finding | Status after the subsequent changes | Remaining boundary |
| --- | --- | --- |
| Committed TLS private key | Shared PEM files removed; explicit unique generation, retired-key rejection and CI credential guards implemented in PR #52. Certificate expiry monitoring and deployment guidance followed. | Existing users of the old identity must rotate their deployments. Client trust and remote access policy remain operator responsibilities. |
| Reseed-failure policy | PR #53 defaults to `stop` on failed/stale seeds across root/child routes and existing entropy WS. PR #56 adds complete-read deadlines and joined shutdown; PR #74 guards absolute deadlines before another read. | Buffered output is not revoked. Native-driver conformance and physical cancellation still need device qualification. Source content quality is not measured by freshness. |
| Reviewable DRBG | Construction specification, primitive vectors and fixed initial/reseed fixtures exist. Owned state lifetime has been hardened; dormant bypass helpers removed. | Composition is still custom, without independent assurance or SP 800-90A conformance. A new construction/default requires separate design and migration. |
| Benchmarks | Controlled pre-Windows/v2.0.3 Linux results and raw samples are published; PR #76 measures the waterfall optimization with varying input and explicitly excludes terminal I/O. | Historical HTTP logs retain missing provenance. Physical-source, real terminal/network and long-duration measurements remain incomplete. |

Authentication remains external. The packaged systemd service is unprivileged,
loopback-only and operator-started; standalone listeners retain all-interface
defaults. Same-origin WebSocket checks are tested and intentionally permit
no-Origin native clients; Origin is not authentication.

Canonical `/metrics` has unique families; numbered exports are compatibility
aliases. Scrape the canonical endpoint once per process. Release validation
now gates tag publication on tests, race/vet/lint, vulnerabilities and packaging;
this supersedes the original review's missing-gate finding.

Current next steps are repeated endurance/fault tests, physical device records,
source-specific health assessment and independent cryptographic review. The
[current architecture assessment](architecture-review.md),
[coverage review](coverage-review-2026-11-13.md) and
[security boundary](security-boundaries.md) distinguish those from completed fixes.
