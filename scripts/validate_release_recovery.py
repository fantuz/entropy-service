#!/usr/bin/env python3
"""Permit publication recovery only for the unchanged, fully validated tag."""
import argparse
from datetime import datetime, timedelta, timezone
import json
import os
import re
import urllib.request


REQUIRED = {
    "Validate tagged commit before publishing",
    "Validate Windows before publishing / Native Windows x64",
    "Validate packaged Windows archive / Windows release ZIP",
    "Validate bundled Windows GUI installer / Bundled Windows GTK installer",
    "Validate container images before publishing / Images (linux-amd64)",
    "Validate container images before publishing / Images (linux-arm64)",
    "Validate container images before publishing / Images (linux-armv7)",
    "Validate endurance before publishing / Endurance (ubuntu-24.04)",
    "Validate endurance before publishing / Endurance (windows-2025)",
    "Validate Firefox and Chromium UI / Browser UI (chromium)",
    "Validate Firefox and Chromium UI / Browser UI (firefox)",
}
PUBLICATION = {
    "Publish only after full tag validation", "Publish released images to GHCR",
    "Publish released images to GHCR / GHCR publication",
    "Publish released images to GHCR / Reconcile the stable latest channel",
}


def validate(run, jobs, tag, sha, now):
    if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?", tag):
        raise ValueError("unsupported release tag")
    if not re.fullmatch(r"[0-9a-f]{40}", sha):
        raise ValueError("invalid tagged source SHA")
    if (run.get("event") != "push" or run.get("path") != ".github/workflows/release.yml"
            or run.get("head_branch") != tag or run.get("head_sha") != sha
            or run.get("status") != "completed" or run.get("conclusion") != "failure"):
        raise ValueError("recovery requires a failed release push for this exact immutable tag/source")
    created = datetime.fromisoformat(run["created_at"].replace("Z", "+00:00"))
    if now - created > timedelta(days=7) or created - now > timedelta(minutes=5):
        raise ValueError("validation evidence must be recent (within seven days)")
    names = [j["name"] for j in jobs]
    if len(names) != len(set(names)) or not REQUIRED <= set(names):
        raise ValueError("required release validation jobs are missing or ambiguous")
    for job in jobs:
        if job["name"] not in PUBLICATION and (job["status"] != "completed" or job["conclusion"] != "success"):
            raise ValueError("release validation did not pass: " + job["name"])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--tag", required=True)
    parser.add_argument("--run-id", required=True)
    args = parser.parse_args()
    if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", args.repository) or not args.run_id.isdigit():
        parser.error("invalid repository or run ID")
    if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?", args.tag):
        parser.error("invalid release tag")
    base = "https://api.github.com/repos/" + args.repository

    def get(path):
        request = urllib.request.Request(base + path, headers={"Accept": "application/vnd.github+json",
            "Authorization": "Bearer " + os.environ["GH_TOKEN"], "User-Agent": "entropy-release-recovery"})
        with urllib.request.urlopen(request, timeout=30) as response:
            return json.load(response)

    obj = get("/git/ref/tags/" + args.tag)["object"]
    for _ in range(5):
        if obj["type"] == "commit":
            break
        if obj["type"] != "tag" or not re.fullmatch(r"[0-9a-f]{40}", obj["sha"]):
            raise ValueError("release tag does not resolve to a commit")
        obj = get("/git/tags/" + obj["sha"])["object"]
    if obj["type"] != "commit":
        raise ValueError("release tag nesting exceeded")
    run = get("/actions/runs/" + args.run_id)
    jobs = []
    for page in range(1, 11):
        batch = get(f"/actions/runs/{args.run_id}/jobs?per_page=100&page={page}")
        jobs.extend(batch["jobs"])
        if len(jobs) >= batch["total_count"]:
            break
    if len(jobs) != batch["total_count"]:
        raise ValueError("incomplete job evidence")
    validate(run, jobs, args.tag, obj["sha"], datetime.now(timezone.utc))
    with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as output:
        output.write("sha=" + obj["sha"] + "\nversion=" + args.tag.removeprefix("v") + "\n")
    print("Validated all release gates for", args.tag, "at", obj["sha"], "using run", args.run_id)


if __name__ == "__main__":
    main()
