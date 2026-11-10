# Operational alerts and HTTPS probing

Scrape **one `/metrics` endpoint per server process**. The same process exposes
the same metrics over HTTP and HTTPS; scraping both duplicates its series.
The numbered compatibility endpoints are not used by these rules.

The canonical endpoint adds two gauge families:

| Metric | Meaning |
| --- | --- |
| `rng_https_enabled` | 1 when the server loaded an identity for its HTTPS listener, otherwise 0. It is not an external connectivity check. |
| `rng_tls_certificate_not_after_timestamp_seconds` | Expiry Unix timestamp of the identity loaded at startup. Omitted when HTTPS is disabled. |

The timestamp does not read replacement files on disk. The server must restart
with the replacement pair before the metric changes. No private key, subject,
hostname or file path is exported as a label. Certificate expiry is separate
from seed readiness; an HTTP `/ready` success does not verify HTTPS or trust.

## Example configuration

[The monitoring examples](../resources/monitoring/) include:

- `prometheus.yml`: one canonical service scrape and a separate external probe.
- `entropy-alerts.yml`: alert rules, with tested failure and recovery cases.
- `blackbox.yml`: an HTTP probe requiring a verified TLS 1.3 connection and a
  200 response from `/ready`, without following redirects.

Adapt the example targets to the actual service and retain the job names
`entropy-service` and `entropy-service-https`, or update the rules accordingly.
Use a hostname/IP present in the server certificate's SANs. The sample uses
IPv4 localhost because the packaged listener binds 127.0.0.1; adapt protocol
selection for an IPv6 deployment.

Install Prometheus and the official Prometheus Blackbox Exporter using your
normal deployment tooling. Keep their listeners private. Copy only the trusted
**public** certificate/CA chain to
`/etc/blackbox_exporter/entropy-service-ca.pem`, readable by the exporter, and
point the exporter at `blackbox.yml`. For example:

```sh
blackbox_exporter --config.file=resources/monitoring/blackbox.yml \
  --web.listen-address=127.0.0.1:9115
prometheus --config.file=resources/monitoring/prometheus.yml \
  --web.listen-address=127.0.0.1:9090
```

The repository does not install/start these services or alter your firewall.
For CA-managed deployments use the appropriate public trust chain. Never copy
a private key to the exporter or disable certificate verification. Stage trust
for renewal according to the [renewal procedure](tls-certificates.md#renew-a-self-signed-identity-managed-by-systemd).

The exporter performs a separate HTTPS connection, so it detects unavailable
listeners, certificate trust/hostname/expiry failures and degraded seed
readiness. Place it where the relevant clients connect: a localhost probe
does not test remote DNS, firewall rules or a public reverse proxy. Restrict
access to its `/probe` API because callers can select destinations.

## Alert behavior

| Alert | Trigger |
| --- | --- |
| `EntropyServiceDown` | Service scrape fails for 2 minutes. |
| `EntropySeedNotReady` | Seed readiness stays false for 1 minute. |
| `EntropyReseedFailures` | A failure counter increases within 10 minutes, even if readiness subsequently recovers. |
| `EntropyTLSCertificateExpiring` | Loaded certificate has 6–24 hours left for 5 minutes. |
| `EntropyTLSCertificateUrgent` | Loaded certificate has less than 6 hours left, or has expired, for 1 minute. |
| `EntropyAdmissionRejections` | HTTP/WS admission rejection rates remain positive for 5 minutes. |
| `EntropyHTTPSProbeFailed` | External HTTPS readiness probe fails for 1 minute. |
| `EntropyHTTPSProbeUnavailable` | The exporter scrape fails for 2 minutes. |

Warning and urgent certificate ranges do not overlap. HTTP-only operation has
no expiry family and does not produce a certificate alert. Remove the HTTPS
probe scrape when HTTPS is deliberately disabled; otherwise its failure is
expected. Counter resets do not count as new failures. Thresholds and delays
are starting examples, not a universal service-level objective.

**Configure Alertmanager and a notification receiver explicitly.** Evaluating
these rules alone does not deliver notifications. Check the Prometheus Alerts
page, route a controlled test alert through your chosen receiver, and verify
delivery. If Prometheus itself loses a target definition or stops, its own
availability/configuration also needs monitoring. These examples cannot detect
an instance that has been removed from the scrape configuration entirely.

## Validation

```sh
bash scripts/test-monitoring.sh
```

The script uses an installed `promtool` (or `PROMTOOL=/path/to/promtool`). When
absent, it downloads pinned Prometheus 3.5.5 LTS tooling for Linux amd64, verifies
the official SHA-256 checksum and extracts only `promtool` into a temporary
directory. It validates rule/config syntax and evaluates synthetic Prometheus
series for healthy service, expiry bands/disabled HTTPS, failures, recovery,
probe outages, admission pressure and counter resets. Build/release CI runs the
same checks. Runtime exporter reachability/trust and notification delivery must
also be verified in the deployment environment.
