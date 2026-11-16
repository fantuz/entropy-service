#!/usr/bin/env python3
"""Promote the exact CI-tested image archives to GHCR, without rebuilding."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess


PLATFORMS = {"linux-amd64": ("amd64", ""), "linux-arm64": ("arm64", ""), "linux-armv7": ("arm", "v7")}
VERSION = re.compile(r"v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:-[0-9A-Za-z][0-9A-Za-z.-]*)?")


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


def docker(*args, capture=False):
    result = subprocess.run(["docker", *args], check=True, stdout=subprocess.PIPE if capture else None)
    return result.stdout if capture else None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--tag", required=True)
    parser.add_argument("--source-sha", required=True)
    parser.add_argument("--artifacts", type=Path, required=True)
    parser.add_argument("--latest", choices=["true", "false"], default="false")
    args = parser.parse_args()
    latest = args.latest == "true"
    validate_request(args.repository, args.tag, args.source_sha, latest)
    version = args.tag.removeprefix("v")
    repository = args.repository.lower()
    owner = repository.split("/", 1)[0]
    destinations = {"server": "ghcr.io/" + repository, "client": f"ghcr.io/{owner}/entropy-client"}

    # Validate all archives and all six loaded images before the first push.
    for platform in PLATFORMS:
        directory = args.artifacts / ("container-images-" + platform)
        archive = verify_archive(directory)
        docker("image", "load", "--input", str(archive))
        for role in destinations:
            source = f"entropy-service/{role}:{platform}"
            info = json.loads(docker("image", "inspect", source, capture=True))[0]
            verify_image(info, role, platform, args.repository, args.source_sha, version)

    references = []
    for role, destination in destinations.items():
        parts = []
        for platform in PLATFORMS:
            source = f"entropy-service/{role}:{platform}"
            destination_part = f"{destination}:{args.tag}-{platform}"
            docker("image", "tag", source, destination_part)
            docker("image", "push", destination_part)
            parts.append(destination_part)
        reference = f"{destination}:{args.tag}"
        docker("buildx", "imagetools", "create", "--tag", reference, *parts)
        index = json.loads(docker("buildx", "imagetools", "inspect", "--raw", reference, capture=True))
        platforms = {(m["platform"]["os"], m["platform"]["architecture"], m["platform"].get("variant", "")) for m in index["manifests"]}
        if {("linux", "amd64", ""), ("linux", "arm", "v7")} - platforms or not any(p[:2] == ("linux", "arm64") for p in platforms) or len(platforms) != 3:
            raise ValueError("published image index does not contain the three tested platforms")
        if latest:
            docker("buildx", "imagetools", "create", "--tag", destination + ":latest", reference)
        references.append(reference)
    print("Published tested images:\n" + "\n".join(references))
    if os.environ.get("GITHUB_STEP_SUMMARY"):
        with Path(os.environ["GITHUB_STEP_SUMMARY"]).open("a") as summary:
            summary.write("### Published container images\n\n" + "\n".join("- `" + r + "`" for r in references) + "\n\nPlatforms: Linux amd64, arm64 and ARMv7.\n")


if __name__ == "__main__":
    main()
