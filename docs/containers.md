# Containers and GitHub Container Registry

Two separate images package the existing Go utilities:

| Image | Contents |
| --- | --- |
| `ghcr.io/fantuz/entropy-service` | `entropy-server`, its embedded web interface and LABs, wordlists, health endpoints and metrics |
| `ghcr.io/fantuz/entropy-client` | CLI stream client, terminal waterfall, sample analysis and exports |

Both are Linux images for **amd64, arm64 and ARMv7**. Docker selects the matching
platform automatically. They also run in Docker Desktop's Linux-container mode;
they are separate from the native Windows executables. The optional Python/GTK
[desktop GUI](desktop-gui.md) runs on the host and can connect to the published
server endpoint.

The commands below use a POSIX shell, such as Linux, WSL or Git Bash.

The images run as **UID/GID 65532**, contain CA trust roots and distribution
notices, and use a minimal distroless runtime. They include no Go toolchain,
shell, operator registry or TLS private key. Docker's normal SIGTERM reaches
the utility directly for graceful shutdown.

## Stable downloads and publication

- `edge` follows validated pushes to `main`.
- `vMAJOR.MINOR.PATCH` identifies a validated release; prerelease suffixes are
  retained when present.
- `latest` follows the most recent published stable GitHub release. Backfilling
  an older release reconciles it with the current stable release instead of
  promoting that older backfill.

```sh
docker pull ghcr.io/fantuz/entropy-service:latest
docker pull ghcr.io/fantuz/entropy-client:latest
# Select an explicit application release for controlled deployments.
docker pull ghcr.io/fantuz/entropy-service:v2.6.0
docker pull ghcr.io/fantuz/entropy-client:v2.6.0
```

Both package pages list available tags:
[server](https://github.com/fantuz/entropy-service/pkgs/container/entropy-service)
and [client](https://github.com/fantuz/entropy-service/pkgs/container/entropy-client).
`latest` is a moving stable channel; pin an image digest for an exact image,
including its base, readiness probe and SBOM.

Validated merges publish `edge` after the main build, native Windows/package and
endurance checks plus all three container-platform checks pass. Subsequent
release tags publish containers after the full release validation and binary
release. Publishing promotes the exact tested image archives without rebuilding.

Transient registry errors (including GHCR `unknown blob`, throttling, service
errors and connection failures) receive up to four attempts with 2/4/8-second
backoff. Each registry command has a 120-second attempt limit; the enclosing
workflow also has its own timeout. Authentication/authorization, invalid
content, failed validation and unclassified errors fail immediately. Exhausted
retries fail the job; they never convert missing images into success.

All six platform images, their remote config digests and SBOM attachments are
staged before either role's consumer-facing version/edge index is updated.
Indexes use immutable platform digests, and the published index must contain
exactly those three digests. A failed client upload therefore cannot first
advance the server channel. Final server/client channel writes remain separate
registry operations; rerun a failed publication to reconcile a partially
completed pair. No rollback or cross-repository atomicity is implied.

For an existing release such as **v2.3.0**, open **Actions → Publish containers
for an existing release → Run workflow**, choose `main`, and enter `v2.3.0`.
The workflow requires an already-published release, checks out that tag's exact
source commit, and builds/tests it with the maintained container recipe. It
does not change the tag, GitHub release or existing binary downloads.

With an authenticated GitHub CLI, the same one-time action is:

```sh
gh workflow run container-backfill.yml --ref main -f release_tag=v2.3.0
gh run list --workflow container-backfill.yml
```

Run it after the maintained recipe is merged, and wait for successful image
validation and publication before using that tag. Re-running an application
release can refresh its container base/probe without changing its application
source; existing image digests continue to identify the previous builds.

All stable/backfill publishers share a separate `latest` promotion lock. A queued
job resolves the current published stable GitHub release after acquiring that
lock, validates both three-platform image indexes and all six application
source/version labels, and copies the indexes by digest. It rechecks the release
before writing either alias. A missing or invalid current-release image fails
promotion and leaves it visible for retry; it never falls back to the older
triggering tag. A queued older backfill therefore also repairs the current stable
aliases if GitHub coalesces pending promotion jobs. The server/client registry
updates are separate operations, not an atomic pair.

GitHub's `GITHUB_TOKEN` supplies registry authentication with `packages: write`;
no separate registry password is required. After the first publication, check
both packages under the owner's **Packages** page. New GHCR packages start private:
to allow anonymous pulls, the owner must change each package's visibility to
**Public** in its package settings. Existing packages may also need this source
repository granted access under **Manage Actions access**. Private pulls require
authentication with a GitHub identity allowed to read those packages.

## Local server and browser interface

The server image's default command explicitly uses `/dev/urandom` as its required
seed source, listens on container port 8080 and disables HTTPS for a
certificate-free local demonstration. The examples publish only to host loopback.
Hardware or OS input seeds the DRBG; containerization does not change generation
semantics or qualify a physical entropy source.

```sh
docker network create entropy-net
docker run -d --name entropy-server --network entropy-net \
  --read-only --cap-drop=ALL --security-opt=no-new-privileges:true \
  -p 127.0.0.1:8080:8080 ghcr.io/fantuz/entropy-service:latest

curl --fail http://127.0.0.1:8080/ready
docker logs entropy-server
```

Open **http://127.0.0.1:8080/** for the web interface. Source LAB browser capture
still needs browser permissions and a secure context (HTTPS or localhost).
Readiness is available at `/ready`; container orchestrators can use an HTTP
probe directly. The image does not depend on an in-container shell or `curl`.

Use HTTPS or an appropriately configured TLS reverse proxy when exposing the
service beyond the local demonstration. Authentication and access policy remain
deployment responsibilities; see [security boundaries](security-boundaries.md).

Docker networking can make a host browser appear as a nonloopback peer to the
server. The registered-source LAB bridge retains its default peer/Host checks;
browser-only sources and ordinary generation endpoints remain available. To
allow registered sources across that network boundary, mount an operator-owned
registry read-only and explicitly configure `-lab-sources` with
`-lab-allow-remote=true`. Put those routes behind an authenticated access boundary.
`-lab-allowed-hosts` alone does not permit nonloopback peers; see [LAB access](lab-safety.md).

Registered RTSP/ONVIF camera decoding also requires an external `ffmpeg` binary.
The minimal image does not bundle that decoder; use a host installation or an
extended image with the required media tools for those sources. Browser camera
and microphone capture runs in the browser and does not need that server-side
decoder.

## Client and fullscreen waterfall

Run the CLI against that container:

```sh
docker run --rm -it --network entropy-net \
  --read-only --cap-drop=ALL --security-opt=no-new-privileges:true \
  -e NO_PROXY=entropy-server,localhost,127.0.0.1 \
  ghcr.io/fantuz/entropy-client:latest \
  -mode=push -wsurl=ws://entropy-server:8080/stream \
  -slice=1048576 -refresh=50ms -analysis-interval=50ms \
  -layout=waterfall -display-interval=50ms -color=auto
```

`-it` supplies the interactive terminal. For automation, use `-headless` and a
duration or output limit. A remote server can be selected with the same `-url`
or `-wsurl` options as the native client. HTTPS/WSS uses the image's normal CA
trust; an additional public CA/certificate can be mounted read-only and selected
with `SSL_CERT_FILE`. The client needs the public trust material, not a server
private key. See [client TLS trust](client-tls.md).

Alternatively, the checked-in Compose example starts the server and an opt-in
interactive client:

```sh
docker compose up -d entropy-server
docker compose run --rm entropy-client
docker compose down
```

Set `ENTROPY_TAG=v2.3.0` after that version's images have been published, or
`ENTROPY_HTTP_PORT=8081` to select a different host port. Named service DNS works
inside the Docker network; the host desktop GUI uses the published loopback URL.

## Persistent exports

The image's `/data` directory belongs to the non-root user. A new named volume
inherits that ownership, including with rootless Docker:

```sh
docker volume create entropy-output
docker run --rm --network entropy-net --read-only \
  --mount type=volume,src=entropy-output,dst=/data \
  -e NO_PROXY=entropy-server,localhost,127.0.0.1 \
  ghcr.io/fantuz/entropy-client:latest \
  -headless -mode=pull -url=http://entropy-server:8080/v1/data/random \
  -output=/data/sample.bin -output-bytes=1048576 -report=/data/sample.jsonl
```

Choose new filenames for another run: the client refuses to overwrite exports
and reports. A host bind mount is also possible if its ownership/UID mapping
allows the container user to write. Raw stdout remains available with
`-output=- -headless`; diagnostics stay on stderr. See [raw exports](client-output.md).

## Provisioning HTTPS

Generate a unique short-lived identity into a persistent named volume:

```sh
docker volume create entropy-tls
docker run --rm --mount type=volume,src=entropy-tls,dst=/data \
  ghcr.io/fantuz/entropy-service:latest generate-cert \
  -cert-file=/data/cert.pem -key-file=/data/key.pem \
  -hosts=localhost,127.0.0.1,entropy-tls-server

docker run -d --name entropy-tls-server --network entropy-net \
  --read-only --cap-drop=ALL --security-opt=no-new-privileges:true \
  --mount type=volume,src=entropy-tls,dst=/data,readonly \
  -p 127.0.0.1:8443:8443 ghcr.io/fantuz/entropy-service:latest \
  -device=/dev/urandom -require-device=true \
  -http=127.0.0.1:8080 -https=:8443 -enable-https=true \
  -cert-file=/data/cert.pem -key-file=/data/key.pem

docker cp entropy-tls-server:/data/cert.pem ./entropy-ca.pem
chmod 644 ./entropy-ca.pem # Public certificate, readable by the container client.
curl --fail --cacert ./entropy-ca.pem https://localhost:8443/ready

docker run --rm --network entropy-net --read-only \
  --mount "type=bind,src=$PWD/entropy-ca.pem,dst=/run/entropy-ca.pem,readonly" \
  -e SSL_CERT_FILE=/run/entropy-ca.pem -e NO_PROXY=entropy-tls-server \
  ghcr.io/fantuz/entropy-client:latest \
  -headless -mode=push -wsurl=wss://entropy-tls-server:8443/stream \
  -duration=5s -report=-
```

Keep the private key in the server volume. The self-signed certificate requires
explicit client trust and explicit renewal before expiry; use a CA-issued
identity for a public deployment. Container replacement preserves the named
volume. The existing [certificate provisioning](tls-certificates.md)
guide explains SANs, safe replacement and expiry handling.

## Hardware sources

Container users do not automatically gain access to host hardware. An existing
qualified Linux character device can be mapped explicitly, with its group
available to the container user:

```sh
docker run --rm --device=/dev/qrandom0:/dev/qrandom0:r \
  --group-add "$(stat -c %g /dev/qrandom0)" \
  -p 127.0.0.1:8080:8080 ghcr.io/fantuz/entropy-service:latest \
  -device=/dev/qrandom0 -require-device=true -http=:8080 -enable-https=false
```

The host driver, read permissions and bounded cancellation contract still apply.
USB selectors additionally need their host USB nodes/sysfs and suitable binding;
there is no automatic device discovery or passthrough from Docker Desktop into
its Linux VM. Consult [hardware examples](hardware-examples.md), the source-specific
guides and [qualification records](hardware-qualification.md). The container
checks exercise OS seeding and simulated application traffic, not physical cards.

## Readiness and Compose startup

The maintained server image includes `/usr/local/bin/entropy-health`. Docker
runs it every 30 seconds, with a three-second external limit, a ten-second startup
grace period and three retries. The probe itself times out after two seconds and
accepts only HTTP 200 from `http://127.0.0.1:8080/ready`. Failed reseed readiness
therefore marks the container unhealthy. It does not follow redirects or use an
outbound proxy.

```sh
docker inspect --format '{{json .State.Health}}' entropy-server
docker exec entropy-server /usr/local/bin/entropy-health
```

For a changed listener, configure the local readiness URL with
`-e ENTROPY_HEALTH_URL=http://127.0.0.1:8090/ready`. HTTPS verification remains
enabled; for a private/self-signed identity, mount the public certificate and
set `ENTROPY_HEALTH_CA_FILE` to that path. For example, with the earlier TLS
volume mounted at `/data`:

```sh
docker exec entropy-tls-server /usr/local/bin/entropy-health \
  -url=https://127.0.0.1:8443/ready -ca-file=/data/cert.pem
```

The Compose example defaults to `latest` and waits for a healthy server before
starting the optional client. Health status does not automatically restart a
running process: `restart: unless-stopped` applies when the process exits.
Inspect `/ready`, logs and metrics to diagnose a persistent source failure.
Kubernetes can use `/ready` for readiness and `/live` for liveness directly.

The standalone probe is built from the maintained container recipe, including
when backfilling an older application release. The application revision and
`io.entropy-service.recipe.revision` label identify these two sources separately.

## Offline OCI downloads

Starting with **v2.6.0**, releases include two portable OCI image archives:

- `entropy-service_<version>_linux.oci.tar` (server)
- `entropy-client_<version>_linux.oci.tar` (client)

Each contains Linux amd64, arm64 and ARMv7. These are container images, not
native CLI archives. Download them from the [release page](https://github.com/fantuz/entropy-service/releases/latest).
Matching full CI builds also retain the `container-oci-bundles` artifact.

Verify against the release's `checksums.txt` before transferring to an offline
host. On Linux, import the matching platform into Docker with Skopeo:

```sh
sha256sum --check checksums.txt --ignore-missing
skopeo --override-os linux --override-arch amd64 copy \
  oci-archive:entropy-service_<version>_linux.oci.tar \
  docker-daemon:entropy-service:offline
skopeo --override-os linux --override-arch amd64 copy \
  oci-archive:entropy-client_<version>_linux.oci.tar \
  docker-daemon:entropy-client:offline
```

Use `--override-arch arm64` or `--override-arch arm --override-variant v7`
for other hosts. Run the imported images using the same volume, listener,
non-root and TLS options documented above, replacing the GHCR image name with
`entropy-service:offline` or `entropy-client:offline`. Docker Desktop still
runs Linux containers; these archives do not install Windows or macOS services.

For an internal registry, copy all platforms without rebuilding:

```sh
skopeo copy --all --preserve-digests \
  oci-archive:entropy-service_<version>_linux.oci.tar \
  docker://registry.example.org/team/entropy-service:<version>
```

Use the registry's usual authentication and TLS trust. Each offline archive's
root index digest equals its corresponding versioned GHCR index digest. CI
combines the exact runtime-tested/scanned manifests and blobs; it does not
rebuild or recompress them. SBOMs remain available through GHCR referrers and
CI security artifacts; an offline image copy alone does not copy referrers.
The `oci-images.json` CI metadata records index/platform/config digests.

## Registry publication fault tests

Every full container build exercises a digest-pinned, disposable Distribution
registry bound only to loopback. The real publisher uploads both roles and six
SPDX attachments, checks all manifest/config digests, and repeats publication
to verify idempotency. A local fault proxy injects HTTP 503, a missing-blob
response and permanent denial. Transient faults must recover through bounded
retries; denial must fail immediately while public channel/platform aliases
remain unchanged. These tests never publish to GHCR.

Two workers stage the server and client images in parallel. Each checks its
image digests and attaches the three platform SBOMs. Consumer aliases move
only after both workers succeed.
Registry APIs cannot atomically update two repositories: a failure during the
final alias updates can still leave a partial promotion. Re-running publication
is idempotent and repairs that state. Stable `latest` reconciliation retains
its existing cross-run lock and current-release checks.

To reproduce against the same-run downloaded native artifacts:

```sh
python3 scripts/oci_release.py build --artifacts tested-images \
  --output tested-images/container-oci-bundles --source-sha <full-commit> \
  --version edge
python3 scripts/test_registry_publication.py --artifacts tested-images \
  --source-sha <full-commit> --version edge
```

Install Docker, Skopeo and ORAS first. Native artifacts must include their
actual scan reports, SPDX files and checksums. `registry-publication-evidence`
records the tested source and cases. Historical Docker archives remain
readable for recovery of older validated publication runs.

## Image maintenance and SBOMs

Every image platform is scanned with pinned Trivy before publication. The scan
covers the final runtime and Go binaries. Fixable **HIGH/CRITICAL** findings block
publication; complete reports retain lower-severity and currently unfixed
findings for review. Missing/failed scans or mismatched image IDs also block
publication. The publisher checks the report/SBOM checksums and exact image ID
before its first registry write.

Each platform image has an SPDX JSON SBOM attached as an OCI referrer, accessible
with ORAS, for example:

```sh
oras discover --artifact-type application/spdx+json \
  ghcr.io/fantuz/entropy-service:v2.3.0-linux-amd64
```

Use the reported SBOM artifact digest with `oras pull` to download its JSON file.
CI also retains the complete scan reports and SBOMs for 90 days in the
`container-security-*` artifacts. They describe the scanned image, without operator
configuration or exported sample data.

The scheduled **Container maintenance** workflow rescans both published `latest`
and `edge` channels, across all three architectures, every Monday and can also
be run manually. These runs refresh vulnerability evidence without changing
registry tags. Failures are reported through the normal Actions workflow status;
reports are retained even when the vulnerability gate fails.

Dependabot checks Docker base references and GitHub Actions weekly and proposes
reviewable updates. Go major/minor toolchain changes remain deliberate; review
patch updates alongside `go.mod` and native release builds. Base images remain
digest-pinned, and updates pass the existing runtime/regression checks.

## Build and validation

For an amd64 build host (select matching image tags and `--platform` for ARM):

```sh
docker build --target server -t entropy-service/server:linux-amd64 .
docker build --target client -t entropy-service/client:linux-amd64 .
python3 scripts/test_containers.py
python3 -m unittest discover -s scripts -p 'test_publish_containers.py'
```

The smoke test runs the real non-root images with a read-only root filesystem,
HTTP/WebSocket samples, browser assets, private bounded exports, certificate
provisioning, HTTPS/WSS acceptance/rejection and graceful shutdown. CI repeats
it on amd64, arm64 and ARMv7 (ARM uses QEMU), then preserves checked archives.
The publisher validates their checksums, image platform, entrypoint, source
commit and version before pushing anything. Publication never runs on a PR.

The Go and distroless base images are digest-pinned. Update the Go image with
`go.mod` and refresh the runtime digest for CA/security maintenance. An optional
BuildKit secret supplies enterprise/session build trust without copying it into
the runtime image:

```sh
docker build --target server --secret id=proxy_ca,src=/path/to/combined-ca-bundle.crt \
  -t entropy-service/server:linux-amd64 .
```

Container registry publication follows binary publication, so these two external
services are not an atomic transaction. If a registry push fails, rerun the failed
publication job while the tested artifacts remain available (seven days), or use
the existing-release workflow afterward. This avoids rebuilding/replacing the
GitHub release just to repair container delivery.
