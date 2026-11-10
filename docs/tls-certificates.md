# TLS certificates

The repository ships no TLS identity. Standalone HTTPS remains enabled by
default and loads `cert.pem` / `key.pem`, or the explicit `-cert-file` /
`-key-file` paths. Missing or invalid files stop startup before opening the
entropy source or a listener. Use an operator-issued CA/ACME certificate
chain for public deployments. Certificate provisioning provides server
identity and encryption, not client authentication.

## Generate a local identity

After building or installing the binary, run the following as the user who
will run the standalone server, in an operator-controlled directory:

```sh
mkdir -m 700 ./tls
entropy-server generate-cert -cert-file=./tls/cert.pem -key-file=./tls/key.pem \
  -hosts=localhost,127.0.0.1,::1 -valid-for=168h
entropy-server -device=/dev/urandom -http=127.0.0.1:8080 -https=127.0.0.1:8443 \
  -cert-file=./tls/cert.pem -key-file=./tls/key.pem
```

Generation is a standalone subcommand: it does not read service configuration,
open a configured entropy source or start a server. It uses Go's OS-backed
`crypto/rand` for a unique ECDSA P-256 key and random certificate serial,
independently of the project's DRBG. Both PEM files are created exclusively
with mode 0600; neither is printed. Parents must already exist. Protect the
directory and private key, and back up an identity only under your own policy.

| Generation flag | Default | Meaning |
| --- | --- | --- |
| `-cert-file` | `cert.pem` | New certificate file. |
| `-key-file` | `key.pem` | New PKCS#8 private key file. |
| `-hosts` | `localhost,127.0.0.1,::1` | Comma-separated DNS names or bare IPs for Subject Alternative Names; no URLs, ports or wildcards. |
| `-valid-for` | `168h` (7 days) | Lifetime, between 1h and 8760h (365 days). |
| `-if-missing` | `false` | Reuse a complete valid pair for all requested names; create only if both files are missing. |

For a LAN host, include its actual DNS name/IP explicitly. Client verification
must use a name present in the certificate. Self-signed certificates are not
automatically trusted. For example, distribute the **public certificate** by
a trusted channel and verify it using:

```sh
curl --cacert ./tls/cert.pem https://localhost:8443/live
```

Browsers need an explicit trust decision. Never distribute the private key
to clients or turn off certificate validation to make a deployment work.

## Provision at service startup

The package remains local-only HTTP and is not started/enabled by installation.
It does not generate files or restart running services during upgrade. For
an explicitly selected local HTTPS deployment, an operator can use this
systemd drop-in (for example through `sudo systemctl edit entropy-server`):

```ini
[Service]
StateDirectory=entropy-service
StateDirectoryMode=0700
ExecStartPre=/usr/bin/entropy-server generate-cert -if-missing -cert-file=/var/lib/entropy-service/cert.pem -key-file=/var/lib/entropy-service/key.pem -hosts=localhost,127.0.0.1,::1
ExecStart=
ExecStart=/usr/bin/entropy-server -http=127.0.0.1:8080 -https=127.0.0.1:8443 -device=/dev/urandom -require-device=true -cert-file=/var/lib/entropy-service/cert.pem -key-file=/var/lib/entropy-service/key.pem
```

`StateDirectory` supplies writable persistent storage with the correct
ownership for the packaged `DynamicUser` sandbox. The certificate survives
restarts. Review the effective unit before explicitly restarting it. For
root-owned CA-managed private keys, use systemd `LoadCredential` and `%d`
credential paths or another explicit access policy; do not make keys
world-readable to accommodate a dynamic UID. See [packaged deployment](packaged-service.md).

## Renewal and recovery

`-if-missing` never renews or rotates an identity automatically. It rejects
expired/not-yet-valid certificates, missing halves, mismatched keys, wrong
hosts, symlinked files and keys readable by group/other users. An existing
pair keeps its lifetime even if `-valid-for` changes. A process crash between
the two exclusive file creations can leave an incomplete pair; inspect and
recover deliberately instead of blindly deleting one side at every startup.

Monitor expiry; normal server startup reports certificate dates but is not a
renewal agent. Generate a fresh pair in a separate private directory, update
trust/pins and configured paths, then explicitly restart. Failed provisioning
does not fall back to plaintext. Two files cannot be atomically published
as one filesystem operation, and automatic identity replacement would break
existing trust. Use a CA/ACME lifecycle when unattended renewal is required.

## Retired demonstration key

Old revisions contained a publicly distributed certificate/private key pair.
The current TLS loader rejects SHA-256 SPKI fingerprint
`5e0e31f52f824f118de78d4f3563237d680e3603a988affe47ee4bdc548483bb`,
including certificates reissued using the same key. The key remains public
in history and old downloads; removal cannot repair an already deployed
identity. Replace **both** certificate and key, remove old client trust/pins,
and restart that deployment with a unique pair. This change does not claim
to have inspected external running hosts. See [security policy](../SECURITY.md).
