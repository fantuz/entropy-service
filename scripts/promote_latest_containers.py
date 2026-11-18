#!/usr/bin/env python3
"""Reconcile latest with the current stable release under the workflow's shared lock."""

import argparse
import json
import os
import re
import urllib.request

from publish_containers import PLATFORMS, docker, validate_request, verify_image


def github_json(path):
    headers = {"Accept": "application/vnd.github+json", "User-Agent": "entropy-container-promotion"}
    if os.environ.get("GH_TOKEN"):
        headers["Authorization"] = "Bearer " + os.environ["GH_TOKEN"]
    request = urllib.request.Request("https://api.github.com/" + path, headers=headers)
    with urllib.request.urlopen(request, timeout=30) as response:
        return json.load(response)


def latest_release(repository):
    release = github_json(f"repos/{repository}/releases/latest")
    tag = release["tag_name"]
    if release.get("draft") or release.get("prerelease") or not release.get("published_at"):
        raise ValueError("latest must be an already-published stable release")
    validate_request(repository, tag, "0" * 40, True)
    obj = github_json(f"repos/{repository}/git/ref/tags/{tag}")["object"]
    for _ in range(5):
        if obj["type"] == "commit":
            validate_request(repository, tag, obj["sha"], True)
            return tag, obj["sha"]
        if obj["type"] != "tag" or not re.fullmatch(r"[0-9a-f]{40}|[0-9a-f]{64}", obj["sha"]):
            break
        obj = github_json(f"repos/{repository}/git/tags/{obj['sha']}")["object"]
    raise ValueError("release tag does not resolve to an immutable commit")


def inspect_index(reference):
    descriptor = json.loads(docker("buildx", "imagetools", "inspect", reference,
                                   "--format", "{{json .Manifest}}", capture=True))
    digest = descriptor["digest"]
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
        raise ValueError("registry returned an invalid image-index digest")
    index = json.loads(docker("buildx", "imagetools", "inspect", reference.split("@", 1)[0].rsplit(":", 1)[0] + "@" + digest,
                             "--raw", capture=True))
    platforms = {(m["platform"]["os"], m["platform"]["architecture"], m["platform"].get("variant", ""))
                 for m in index["manifests"]}
    if len(index["manifests"]) != 3 or len(platforms) != 3 or ("linux", "amd64", "") not in platforms or ("linux", "arm", "v7") not in platforms or not any(p[:2] == ("linux", "arm64") for p in platforms):
        raise ValueError("release image must contain exactly the three tested Linux platforms")
    return digest, index


def prepare_images(repository, release):
    tag, sha = release
    owner = repository.split("/", 1)[0]
    destinations = {"server": "ghcr.io/" + repository.lower(), "client": f"ghcr.io/{owner.lower()}/entropy-client"}
    prepared = []
    for role, destination in destinations.items():
        digest, index = inspect_index(destination + ":" + tag)
        for manifest in index["manifests"]:
            config = json.loads(docker("buildx", "imagetools", "inspect", destination + "@" + manifest["digest"],
                                       "--format", "{{json .Image}}", capture=True))
            info = {"Os": config["os"], "Architecture": config["architecture"], "Variant": config.get("variant", ""), "Config": config["config"]}
            platform = next(name for name, (arch, _) in PLATFORMS.items() if arch == info["Architecture"])
            verify_image(info, role, platform, repository, sha, tag.removeprefix("v"))
        prepared.append((destination, digest))
    return prepared


def synchronise_latest(repository):
    # Every queued stable/backfill job reconciles the current release, including
    # when GitHub coalesces pending concurrency jobs. It never promotes its own
    # older input tag on the strength of a decision made before waiting.
    for _ in range(3):
        release = latest_release(repository)
        prepared = prepare_images(repository, release)
        if latest_release(repository) != release:
            continue
        for destination, digest in prepared:
            if latest_release(repository) != release:
                break
            docker("buildx", "imagetools", "create", "--tag", destination + ":latest", destination + "@" + digest)
            published, _ = inspect_index(destination + ":latest")
            if published != digest:
                raise ValueError("latest does not resolve to the validated release index")
        else:
            # A retry of the last registry write may outlive a newly published
            # release. Reconcile again instead of returning with stale aliases.
            if latest_release(repository) != release:
                continue
            print("latest now follows", release[0], "at", release[1])
            return
    raise RuntimeError("latest release kept changing; retry promotion")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repository", required=True)
    args = parser.parse_args()
    validate_request(args.repository, "edge", "0" * 40, False)
    synchronise_latest(args.repository)


if __name__ == "__main__":
    main()
