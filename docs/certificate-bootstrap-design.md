# Certificate tools

Open `/api-certificates.html` on a trusted entropy-server over HTTPS or local
loopback HTTP. The page creates a self-signed TLS server certificate and matching
ECDSA P-256 private key locally in the browser. It does not install files, change
trust stores, restart services or issue publicly trusted certificates.

## Uses and workflow

Useful applications include local HTTPS bootstrap, separate identities for LAB
peers, IPv4/IPv6 and private-DNS client tests, certificate pinning and preparing
a replacement identity before deployment. The page supports 1–32 DNS/IP subject
alternative names, a display common name, and 1–365 days of validity. The default
is seven days with `localhost`, `127.0.0.1` and `::1`. Verification uses the SANs,
not the common name. International DNS names are converted to IDNA ASCII.

1. Enter only hostnames or IP addresses, without schemes, paths or ports.
2. Select **Generate certificate**. No entropy request occurs until this action.
3. Review names, UTC validity and the SHA-256 certificate fingerprint.
4. Download the public certificate and private key separately. Only the public
   certificate is displayed in the page. The key is unencrypted PKCS#8 PEM.
5. Protect and deploy the files explicitly through the existing certificate
   options. **Clear** or navigation drops this page's references; downloaded
   files remain on disk. Browser managed memory cannot guarantee secure erasure.

For example, after saving the downloads on Linux:

```sh
chmod 600 entropy-server-key.pem
entropy-server -device=/dev/urandom -http=127.0.0.1:8080 -https=127.0.0.1:8443 -cert-file=/absolute/path/entropy-server-cert.pem -key-file=/absolute/path/entropy-server-key.pem
curl --cacert /absolute/path/entropy-server-cert.pem https://localhost:8443/live
```

On macOS, use `-device=macos-csprng` with the same TLS flags. On Windows, use
`entropy-server.exe -device=windows-csprng` and absolute Windows paths.
Download into a private account directory. For a Windows Service or
systemd unit, place the files under the documented protected state policy and
grant the service account read access; a GUI user's download directory is not
an appropriate long-term service key location. See [TLS provisioning](tls-certificates.md),
[Windows Service](windows-service.md), and [client trust](client-tls.md).
Restart a running server only after provisioning the matching pair. Certificate
renewal and trust/pin updates are explicit operator actions.

## Entropy and key custody

The page fetches exactly 64 bytes from the same-origin
`/v1/data/random?bytes=64` API with a ten-second request deadline, no preference
cookies, no redirects and no response cache. Normal server generation/readiness
and resource limits apply. An unavailable or malformed response stops generation;
there is no fallback that removes either entropy contribution.

Independently, `crypto.getRandomValues` supplies 64 browser CSPRNG bytes.
HKDF-SHA-256 uses the local bytes as input key material, server bytes as salt
and versioned domain labels for the private scalar and certificate serial.
Rejection sampling selects `1 <= d < n` for P-256; it rejects zero and the group
order, with a bounded failure limit. Native Web Crypto imports the private key,
derives the public point and signs using ECDSA/SHA-256. JavaScript formats the
bounded X.509 DER structure; it does not implement elliptic-curve arithmetic.

The standard Web Crypto `generateKey` API cannot accept an external seed.
Importing a derived scalar allows the server contribution to affect the actual
key, instead of only its serial number. A provider that cannot import/export the
required native P-256 key format fails with a CLI alternative; it does not
silently select an unrelated native key.

Private material is never uploaded, included in URLs or persisted in cookies,
local/session storage by this implementation. Private key export requires a
separate download action. Buffers are overwritten where possible; native keys,
temporary strings, browser downloads and operating-system memory do not provide
a general secure-erasure guarantee. Cancellation invalidates a late native
crypto result as well as aborting pending network acquisition.

## Format, trust and limits

The certificate has a positive random serial, DNS/IPv4/IPv6 SANs, a critical
non-CA basic constraint, critical digital-signature key usage and server-auth
extended key usage. Validity includes five minutes of backward clock allowance.
It is a server leaf, not a CA; it cannot provide a general signing service.

The server contribution follows the configured backend/source policy. It is not
an independent fresh hardware measurement for every byte. Browser randomness
provides an independent input against weak server output; combining inputs does
not establish extra certified entropy or prove cryptographic quality.

A server controlling the page's JavaScript can control its behaviour. Use a
trusted origin; generating a key locally does not make compromised software
trustworthy. Remote plaintext HTTP cannot securely bootstrap its own trust and
normally lacks the required Web Crypto secure context. Localhost HTTP is useful
for initial local testing; existing trusted HTTPS is appropriate remotely.

Self-signed output requires an explicit client trust decision or pin. It does
not prove DNS ownership, obtain a public CA signature, or replace ACME and
organisational PKI. Expiry, renewal, revocation and secure deployment remain
operator responsibilities. Browsers do not guarantee restrictive permissions
on downloaded keys. No public-facing signing API or server key store is added.

## Installation and future extensions

Installers can continue using the local `entropy-server generate-cert` command,
which uses the operating-system CSPRNG and writes through the existing private
file policy. It works before any server is running. The browser page is an
interactive option for generation and TLS experiments, not an installer
dependency. This change does not alter automatic provisioning or replace
existing identities during upgrades.

Public-certificate inspection and CSR export for an organisational CA are
possible follow-ups. CA operation, trust-store changes, automated renewal and
remote private-key distribution require separate designs.

## Validation and performance boundaries

Focused Node tests independently parse X.509, verify self-signatures and key
matching, check SANs/usage/validity, force scalar rejection boundaries and
confirm that changing either entropy input changes the public key. Browser CI
runs Chromium and Firefox with real native crypto, explicit public/key downloads,
failed and malformed entropy, cancellation, navigation cleanup, private-data
network/storage checks and light/dark responsive layouts. Downloaded private
keys stay in temporary test directories and are not uploaded as evidence.

Generated files are loaded by a real Go entropy-server TLS listener. A separate
TLS client trusts the generated certificate, completes a verified handshake and
rejects a hostname missing from the SANs. Browser/provider results belong to the
matching CI run; neither cross-compilation nor a format unit test establishes
browser support. Tests are interoperability evidence, not a cryptographic audit.

The page loads its extra modules only when opened, acquires 64 server bytes per
generation, and adds no work to other live visualisations or bulk generation.
There are no third-party script downloads or new cryptographic dependencies.
