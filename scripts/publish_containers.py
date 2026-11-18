#!/usr/bin/env python3
"""Promote the exact CI-tested image archives to GHCR, without rebuilding."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import time

from check_container_scan import check_report


PLATFORMS = {"linux-amd64": ("amd64", ""), "linux-arm64": ("arm64", ""), "linux-armv7": ("arm", "v7")}
VERSION = re.compile(r"v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:-[0-9A-Za-z][0-9A-Za-z.-]*)?")
REGISTRY_ATTEMPTS = 4
REGISTRY_TIMEOUT = 120


def transient_registry_error(output):
    message = output.decode("utf-8", errors="replace").lower()
    # Never turn authentication/authorization or invalid-content failures into
    # retries, even when another diagnostic contains a transient status code.
    if any(detail in message for detail in ("unauthorized", "authentication required", "forbidden",
                                             "denied", "insufficient_scope", "invalid reference",
                                             "digest invalid", "digest_invalid", "manifest invalid", "manifest_invalid")):
        return False
    status = re.search(r"\b(?:http(?:/\d(?:\.\d)?)?(?:\s+status)?|status(?:\s+code)?)\s*[:=]?\s*(?:429|500|502|503|504)\b", message)
    return bool(status) or any(
        detail in message for detail in ("unknown blob", "blob unknown", "blob_unknown", "manifest unknown",
                                        "too many requests", "toomanyrequests", "connection reset",
                                        "connection refused", "connection timed out", "i/o timeout",
                                        "tls handshake timeout", "unexpected eof", "service unavailable",
                                        "bad gateway", "internal server error", "gateway timeout"))


def registry_command(command, *, capture=False, cwd=None):
    """Retry an idempotent registry operation, never builds or validation.

    Keep failed-command diagnostics visible without mixing them into captured
    JSON. Each attempt is time-bounded; permanent/unknown errors fail at once.
    """
    for attempt in range(REGISTRY_ATTEMPTS):
        try:
            result = subprocess.run(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                    cwd=cwd, timeout=REGISTRY_TIMEOUT, check=False)
        except subprocess.TimeoutExpired as error:
            if attempt == REGISTRY_ATTEMPTS - 1:
                raise
            output = (error.stdout or b"") + (error.stderr or b"")
            sys.stderr.write(output.decode("utf-8", errors="replace"))
        else:
            if result.returncode == 0:
                if result.stderr:
                    sys.stderr.write(result.stderr.decode("utf-8", errors="replace"))
                if not capture:
                    sys.stdout.write(result.stdout.decode("utf-8", errors="replace"))
                return result.stdout if capture else None
            output = result.stdout + result.stderr
            sys.stderr.write(output.decode("utf-8", errors="replace"))
            if attempt == REGISTRY_ATTEMPTS - 1 or not transient_registry_error(output):
                result.check_returncode()
        delay = 2 ** (attempt + 1)
        print(f"Transient registry failure; retry {attempt + 2}/{REGISTRY_ATTEMPTS} in {delay}s", file=sys.stderr, flush=True)
        time.sleep(delay)


def validate_request(repository, tag, source_sha, latest):
    if not re.fullmatch(r"[A-Za-z0-9-]+/[A-Za-z0-9_.-]+", repository):
        raise ValueError("expected an owner/repository name")
    if len(tag) > 100 or (tag != "edge" and not VERSION.fullmatch(tag)):
        raise ValueError("image tag must be edge or vMAJOR.MINOR.PATCH with an optional prerelease")
    if not re.fullmatch(r"[0-9a-f]{40}|[0-9a-f]{64}", source_sha):
        raise ValueError("an immutable full source SHA is required")
    if latest and (tag == "edge" or "-" in tag):
        raise ValueError("only a stable release may update latest")


def verify_image(info, role, platform, repository, source_sha, version):
    config = info["Config"]
    labels = config.get("Labels") or {}
    expected_arch, expected_variant = PLATFORMS[platform]
    if info["Os"] != "linux" or info["Architecture"] != expected_arch:
        raise ValueError("image platform does not match its tested artifact")
    if expected_variant and info.get("Variant") != expected_variant:
        raise ValueError("image ARM variant does not match its tested artifact")
    if config["User"] != "65532:65532" or config["Entrypoint"] != ["/usr/local/bin/entropy-" + role]:
        raise ValueError("unexpected image identity or entrypoint")
    if labels.get("org.opencontainers.image.revision") != source_sha:
        raise ValueError("image source differs from the validated source commit")
    if labels.get("org.opencontainers.image.version") != version:
        raise ValueError("image version differs from the requested channel/release")
    if labels.get("org.opencontainers.image.source", "").lower() != "https://github.com/" + repository.lower():
        raise ValueError("image is not linked to the requested source repository")


def verify_archive(directory):
    checksum = (directory / "checksums.txt").read_text().strip()
    match = re.fullmatch(r"([0-9a-f]{64})  images\.tar\.gz", checksum)
    if not match:
        raise ValueError("invalid tested-image checksum manifest")
    archive = directory / "images.tar.gz"
    with archive.open("rb") as stream:
        digest = hashlib.file_digest(stream, "sha256").hexdigest()
    if digest != match[1]:
        raise ValueError("tested-image archive checksum mismatch")
    return archive


def verify_security_reports(directory, role, image_id):
    expected = {r + suffix for r in ("server", "client") for suffix in (".spdx.json", ".vulnerabilities.json")}
    entries = {}
    for line in (directory / "security-checksums.txt").read_text().splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})  ([a-z.]+)", line)
        if not match or match[2] not in expected or match[2] in entries:
            raise ValueError("invalid image-security checksum manifest")
        entries[match[2]] = match[1]
    if set(entries) != expected:
        raise ValueError("missing image-security reports")
    for name, digest in entries.items():
        if hashlib.sha256((directory / name).read_bytes()).hexdigest() != digest:
            raise ValueError("image-security report checksum mismatch")
    report = json.loads((directory / (role + ".vulnerabilities.json")).read_text())
    if check_report(report, image_id):
        raise ValueError("image has fixable HIGH/CRITICAL vulnerabilities")
    sbom = directory / (role + ".spdx.json")
    document = json.loads(sbom.read_text())
    if document.get("SPDXID") != "SPDXRef-DOCUMENT" or not document.get("spdxVersion", "").startswith("SPDX-") or not document.get("packages"):
        raise ValueError("invalid or empty image SBOM")
    return sbom


def docker(*args, capture=False):
    if args[:2] == ("image", "push") or args[:2] == ("buildx", "imagetools"):
        return registry_command(["docker", *args], capture=capture)
    result = subprocess.run(["docker", *args], check=True, stdout=subprocess.PIPE if capture else None)
    return result.stdout if capture else None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--tag", required=True)
    parser.add_argument("--source-sha", required=True)
    parser.add_argument("--artifacts", type=Path, required=True)
    args = parser.parse_args()
    validate_request(args.repository, args.tag, args.source_sha, False)
    version = args.tag.removeprefix("v")
    repository = args.repository.lower()
    owner = repository.split("/", 1)[0]
    destinations = {"server": "ghcr.io/" + repository, "client": f"ghcr.io/{owner}/entropy-client"}

    # Validate all archives and all six loaded images before the first push.
    sboms, image_ids = {}, {}
    for platform in PLATFORMS:
        directory = args.artifacts / ("container-images-" + platform)
        archive = verify_archive(directory)
        docker("image", "load", "--input", str(archive))
        for role in destinations:
            source = f"entropy-service/{role}:{platform}"
            info = json.loads(docker("image", "inspect", source, capture=True))[0]
            verify_image(info, role, platform, args.repository, args.source_sha, version)
            sboms[platform, role] = verify_security_reports(directory, role, info["Id"])
            image_ids[platform, role] = info["Id"]

    # Stage every platform of BOTH roles before either consumer-facing index
    # changes. A failed client upload must not already advance the server tag.
    staged = {}
    for role, destination in destinations.items():
        parts = []
        for platform in PLATFORMS:
            source = f"entropy-service/{role}:{platform}"
            destination_part = f"{destination}:{args.tag}-{platform}"
            docker("image", "tag", source, destination_part)
            docker("image", "push", destination_part)
            descriptor = json.loads(docker("buildx", "imagetools", "inspect", destination_part,
                                           "--format", "{{json .Manifest}}", capture=True))
            digest = descriptor["digest"]
            if not re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
                raise ValueError("invalid published platform digest")
            immutable = destination + "@" + digest
            manifest = json.loads(docker("buildx", "imagetools", "inspect", "--raw", immutable, capture=True))
            if manifest.get("config", {}).get("digest") != image_ids[platform, role]:
                raise ValueError("published image config differs from the exact tested image")
            sbom = sboms[platform, role]
            registry_command(["oras", "attach", "--artifact-type", "application/spdx+json",
                              immutable, sbom.name + ":application/spdx+json"], cwd=sbom.parent)
            parts.append(immutable)
        staged[destination] = parts

    references = []
    for destination, parts in staged.items():
        reference = f"{destination}:{args.tag}"
        docker("buildx", "imagetools", "create", "--tag", reference, *parts)
        index = json.loads(docker("buildx", "imagetools", "inspect", "--raw", reference, capture=True))
        platforms = {(m["platform"]["os"], m["platform"]["architecture"], m["platform"].get("variant", "")) for m in index["manifests"]}
        if {("linux", "amd64", ""), ("linux", "arm", "v7")} - platforms or not any(p[:2] == ("linux", "arm64") for p in platforms) or len(platforms) != 3:
            raise ValueError("published image index does not contain the three tested platforms")
        if len(index["manifests"]) != 3 or {m["digest"] for m in index["manifests"]} != {part.split("@", 1)[1] for part in parts}:
            raise ValueError("published image index differs from the three tested platform digests")
        expected_platforms = {part.split("@", 1)[1]: PLATFORMS[platform] for platform, part in zip(PLATFORMS, parts)}
        for manifest in index["manifests"]:
            arch, variant = expected_platforms[manifest["digest"]]
            platform = manifest["platform"]
            if platform["architecture"] != arch or (variant and platform.get("variant") != variant):
                raise ValueError("published image index assigns a tested digest to the wrong platform")
        references.append(reference)
    print("Published tested images:\n" + "\n".join(references))
    if os.environ.get("GITHUB_STEP_SUMMARY"):
        with Path(os.environ["GITHUB_STEP_SUMMARY"]).open("a") as summary:
            summary.write("### Published container images\n\n" + "\n".join("- `" + r + "`" for r in references) + "\n\nPlatforms: Linux amd64, arm64 and ARMv7.\n")


if __name__ == "__main__":
    main()
