# Browser certificate bootstrap

Status: implementation design and interoperability qualification. This branch
does not yet expose a certificate-generation endpoint or change server keys.

## Purpose and initial scope

Generate a self-signed TLS server identity for a local entropy-server or another
operator-managed service. The initial format is an ECDSA P-256 key in PKCS#8 PEM
and an X.509 certificate in PEM, with explicit DNS/IP subject alternative names
and bounded validity. A browser page offers separate certificate/key downloads
and a SHA-256 certificate fingerprint. It does not install files, change trust
stores, restart servers or issue publicly trusted certificates.

## Entropy and key custody

Private keys are constructed locally and never sent to the server by this
implementation. The page obtains 64 bytes from the same-origin generation API,
using a bounded request without preference cookies, and independently obtains
64 bytes from `crypto.getRandomValues`. HKDF-SHA-256 combines the two with a
fixed, versioned domain-separation label. Rejection sampling selects a valid
P-256 private scalar. Native Web Crypto imports and signs with that scalar;
JavaScript does not implement elliptic-curve arithmetic.

This requires qualifying minimal PKCS#8 import and public-key export in both
Firefox and Chromium before enabling the page. The standard Web Crypto
`generateKey` API does not accept an external seed; silently using it while
claiming that server randomness generated the key would be misleading. There
is no fallback that removes either entropy contribution if a provider/import
or entropy request fails.

The server contribution is DRBG output under the selected server backend and
seed policy, rather than an independent hardware measurement for every byte.
The independent browser CSPRNG protects against a weak server contribution;
this does not establish additional certified entropy or make a compromised
browser/source instance trustworthy. A server controlling the JavaScript can
also control its behavior. Use a trusted instance over HTTPS or local loopback.

## Certificate format and qualification

Use a small bounded DER encoder for the certificate structure, with native
ECDSA/SHA-256 signatures. A positive random serial, SANs, server-auth extended
key usage, digital-signature key usage and a non-CA basic constraint limit the
initial scope to server leaves. The subject is display metadata; hostname
verification uses SANs. UTC validity has a small clock-skew allowance and a
maximum duration. The private key is exported only through an explicit user
action; buffers are cleared where possible and keys are never persisted in
cookies, local/session storage, URLs or server logs.

Qualification must parse generated certificates and PKCS#8 keys with Go's
standard `crypto/x509`, verify the self-signature and matching public key,
check DNS/IPv4/IPv6 SANs, validity and non-CA usage, and perform a real TLS
handshake. Browser tests cover both engines, failed entropy acquisition,
repeated generation, bounds, cancellation and the absence of private-key
network requests. A generation/format test is not independent cryptographic
assurance; security review remains appropriate before broad deployment.

## Provisioning and limits

The page can bootstrap an instance initially running on HTTP localhost, where
Web Crypto is available, or an already trusted HTTPS origin. An untrusted
remote plaintext page cannot establish the trust needed for secure bootstrap.
Self-signed output requires an explicit client trust decision or certificate
pin. It does not prove control of a public DNS name, provide a CA signature or
replace ACME/organizational PKI.

Save the key with restrictive filesystem permissions and provision it through
the normal CLI/systemd/Windows Service certificate paths. Browser downloads
cannot guarantee file permissions or erase exported strings from managed
memory. The page will not overwrite the running instance's certificate or key.
Expiry, renewal, revocation and deployment remain operator responsibilities.
CSR creation and externally signed identities can be evaluated separately
after the self-signed format and provider behavior are qualified.
