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

For `entropy-client` HTTPS/WSS commands, Linux trust configuration and the
effect of renewal on existing streams, see [client TLS trust](client-tls.md).

## Provision at service startup

The package remains local-only HTTP and is not started/enabled by installation.
It does not generate files or restart running services during upgrade. For
an explicitly selected local HTTPS deployment, use either method to create
`/etc/systemd/system/entropy-server.service.d/override.conf`.

Use systemd's editor:

```sh
sudo systemctl edit entropy-server
```

Or create the directory and edit the override file directly:

```sh
sudo mkdir -p /etc/systemd/system/entropy-server.service.d
sudoedit /etc/systemd/system/entropy-server.service.d/override.conf
```

With either method, paste the following contents into the editable section
and save:

```ini
[Service]
StateDirectory=entropy-service
StateDirectoryMode=0700
ExecStartPre=/usr/bin/entropy-server generate-cert -if-missing -cert-file=/var/lib/entropy-service/cert.pem -key-file=/var/lib/entropy-service/key.pem -hosts=localhost,127.0.0.1,::1
ExecStart=
ExecStart=/usr/bin/entropy-server -http=127.0.0.1:8080 -https=127.0.0.1:8443 -enable-https=true -device=/dev/urandom -require-device=true -cert-file=/var/lib/entropy-service/cert.pem -key-file=/var/lib/entropy-service/key.pem
```

`StateDirectory` supplies writable persistent storage with the correct
ownership for the packaged `DynamicUser` sandbox. The certificate survives
restarts. Direct file edits require `sudo systemctl daemon-reload` before
restarting the service. Review the effective unit before explicitly restarting it. For
root-owned CA-managed private keys, use systemd `LoadCredential` and `%d`
credential paths or another explicit access policy; do not make keys
world-readable to accommodate a dynamic UID. See [packaged deployment](packaged-service.md).

## Renewal and recovery

Startup and `generate-cert` report the loaded certificate's expiry time and
warn when it has 24 hours or less remaining. A running HTTPS server logs a
warning at that boundary and again at expiry, without changing its identity.
These notices do not renew files or update client trust automatically.
For Prometheus expiry alerts and a separate verified HTTPS probe, see
[operational monitoring](monitoring.md).

Newly generated certificates include a nonempty subject/issuer name for TLS
client compatibility; SANs remain the authority for hostname verification.
Earlier generated pairs with an empty issuer can be rejected by some curl/TLS
backends. Existing pairs are preserved and now produce a warning: use the
explicit renewal procedure below to replace them rather than expecting
`-if-missing` to change an established identity.

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

### Renew a self-signed identity managed by systemd

The following procedure is exercised by the real systemd CI test. Use new
filenames, retain the existing pair for rollback, and complete renewal before
the old certificate expires. Keep the same DNS/IP SANs required by your clients.

Generate the replacement under systemd's service identity and state-directory
mapping. This also works with the packaged `DynamicUser` sandbox:

```sh
sudo systemd-run --wait --collect --unit=entropy-cert-renewal \
  -p DynamicUser=yes -p User=entropy-service \
  -p StateDirectory=entropy-service -p StateDirectoryMode=0700 -p UMask=0077 \
  /usr/bin/entropy-server generate-cert \
  -cert-file=/var/lib/entropy-service/renewed-cert.pem \
  -key-file=/var/lib/entropy-service/renewed-key.pem \
  -hosts=localhost,127.0.0.1,::1
```

Generation refuses existing filenames. For subsequent rotations choose another
new pair of names. Distribute only the replacement **public certificate** over
a trusted channel and verify its fingerprint. Stage client trust for both old
and new identities before switching. For a local verification copy:

```sh
sudo openssl x509 -in /var/lib/entropy-service/renewed-cert.pem -noout -fingerprint -sha256 -dates
sudo cat /var/lib/entropy-service/renewed-cert.pem > ./entropy-server-renewed.pem
sudo cp /etc/systemd/system/entropy-server.service.d/override.conf \
  /etc/systemd/system/entropy-server.service.d/override.conf.previous
```

Edit the existing override and **replace both certificate paths in both
`ExecStartPre` and `ExecStart`** with the replacement paths. Keep the other
listener/source settings. Do not append a second provisioning command that
still checks an expired pair. The `.previous` backup is not a systemd `.conf`
drop-in and is not loaded.

```sh
sudo systemctl daemon-reload
sudo systemctl restart entropy-server
curl --cacert ./entropy-server-renewed.pem https://localhost:8443/live
```

Clients trusting only the old self-signed certificate will reject the new
identity until their trust/pins are updated. If verification fails, restore
the retained override and reload/restart again; rollback requires that the
previous certificate is still valid:

```sh
sudo cp /etc/systemd/system/entropy-server.service.d/override.conf.previous \
  /etc/systemd/system/entropy-server.service.d/override.conf
sudo systemctl daemon-reload
sudo systemctl restart entropy-server
```

After all clients use the replacement successfully, remove old trust and
retire the old private key under your own retention policy. The server loads
TLS once per process, so replacing a file alone does not switch the active
certificate. CA/ACME-managed deployments should use their existing renewal
and service-reload lifecycle instead of the self-signed workflow above.

## Retired demonstration key

Old revisions contained a publicly distributed certificate/private key pair.
The current TLS loader rejects SHA-256 SPKI fingerprint
`5e0e31f52f824f118de78d4f3563237d680e3603a988affe47ee4bdc548483bb`,
including certificates reissued using the same key. The key remains public
in history and old downloads; removal cannot repair an already deployed
identity. Replace **both** certificate and key, remove old client trust/pins,
and restart that deployment with a unique pair. This change does not claim
to have inspected external running hosts. See [security policy](../SECURITY.md).
