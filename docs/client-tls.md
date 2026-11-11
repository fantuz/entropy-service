# Client TLS trust

`entropy-client` supports remote IPv4 servers in both modes. Select HTTPS with
`-mode=pull -url=https://HOST:8443/v1/data/random`, or secure WebSockets with
`-mode=stream -wsurl=wss://HOST:8443/stream`. Only the selected URL is used.
The current server requires TLS 1.3; both client transports can negotiate it.

Both transports verify the certificate chain, validity dates, server usage and
the hostname or IP in the URL using Go's standard TLS verification. The default
trust comes from the operating system. Browser certificate exceptions do not
establish trust for this CLI. There is no CLI option to disable verification.
Certificate failures stop the run, even when transport retries are enabled;
failed handshakes deliver no entropy to analysis or export destinations.

HTTP pull follows ordinary HTTP redirects, HTTP-to-HTTPS upgrades and verified
HTTPS-to-HTTPS redirects, with a ten-request limit. Once a redirect chain uses
HTTPS, a redirect back to plaintext is rejected before contacting that target.
WSS does not follow HTTP redirects. Explicit `http://` and `ws://` URLs remain
available for deployments where the operator deliberately chooses plaintext.

## Trust a deployment on Linux

For a self-signed deployment, obtain its **public certificate** through a trusted
channel and verify its fingerprint with the operator. For a private CA, obtain
the trusted CA's public certificate instead. Never copy the server's private key
to clients. Linux Go programs support `SSL_CERT_FILE` for a PEM trust bundle:

```sh
SSL_CERT_FILE=/absolute/path/entropy-server-public.pem \
  entropy-client -mode=stream -wsurl=wss://192.0.2.10:8443/stream \
  -slice=65536 -refresh=50ms

SSL_CERT_FILE=/absolute/path/entropy-server-public.pem \
  entropy-client -mode=pull -url=https://192.0.2.10:8443/v1/data/random \
  -slice=65536 -refresh=50ms
```

Replace the documentation address `192.0.2.10` with your server's address.
`SSL_CERT_FILE` overrides the default certificate-file search; Linux certificate
directories are also searched unless `SSL_CERT_DIR` overrides them. This is
process-specific configuration, not an exclusive certificate pin. An operator
can alternatively install the public CA/certificate into the system trust store
using the distribution's normal procedure. Publicly trusted CA-issued server
certificates usually require no extra client configuration when system roots
are installed and current. The CLI does not configure mutual TLS client keys.

An IPv4 URL requires that exact address in an **IP subject alternative name
(SAN)**. A DNS SAN or certificate Common Name does not substitute for an IP SAN.
The server's default generated certificate covers only `localhost`, `127.0.0.1`
and `::1`. Include the remote address when generating a new identity, for example
`-hosts=localhost,127.0.0.1,::1,192.0.2.10`. See
[server provisioning and renewal](tls-certificates.md).

The packaged systemd service defaults to HTTP on loopback with HTTPS disabled.
Remote access also requires an explicit reachable listener, HTTPS configuration
and appropriate firewall rules; adding client trust alone does not expose it.
Follow the documented [systemd override](tls-certificates.md#provision-at-service-startup).

## Renewal and replacement

When a CA issues a replacement server certificate under the same trusted CA,
clients can verify the new leaf without replacing their CA trust, provided the
new identity remains valid for the requested host and the server supplies its
required intermediate chain.

A new self-signed identity requires new client trust. Before switching the
server, concatenate the old and new **public certificates** into a PEM bundle,
configure `SSL_CERT_FILE` to use it and restart the client. Switch and restart
the server, verify both client transports, then remove the old trust and restart
the client again. Keep the old identity valid during the planned overlap.
This is the same staged replacement described in the server provisioning guide.

The server loads its identity at startup. Replacing PEM files alone does not
change the certificate it serves. The CLI does not watch trust files, and Go
caches system roots within a process: restart the CLI after changing its trust
configuration. Certificates are verified on a new TLS handshake; an established
WSS stream does not reverify dates or reload a replaced identity for every frame.

## Diagnose a failure

| Error | Check |
| --- | --- |
| `certificate signed by unknown authority` | Public CA/certificate trust, intermediate chain and whether the server's self-signed identity changed. |
| Certificate valid for a different name/IP | URL hostname/address and the loaded certificate's SANs. Regenerate or provision the correct identity. |
| Certificate expired or not yet valid | Server/client clocks and the server renewal procedure. |
| TLS record/protocol error | Use HTTPS/WSS on the configured TLS port, normally 8443; port 8080 normally serves plaintext. |
| Connection refused or timeout | Listener bind address, systemd flags, routing and firewall. This occurs before certificate verification. |

## Regression coverage

Focused Go tests exercise actual HTTPS and WSS handshakes against trusted
self-signed and private-CA identities, renewal, replacement and invalid
certificates. They also verify redirect downgrade rejection and the redirect
limit. `python3 scripts/test_client_tls.py`, after `make build`, runs the actual
Linux server and client executables on a separate loopback IPv4 address. It uses
`entropy-server generate-cert`, checks both transports, restarts the server with
a replacement identity and verifies failed runs export zero bytes. Build and
release CI both run this smoke test. These fixtures do not test an external host
or establish trust for an operator's deployment.
