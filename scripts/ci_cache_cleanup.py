#!/usr/bin/env python3
"""Bound CI input caches without evicting open PRs or unrelated main caches."""

import argparse
from collections import defaultdict
import json
import os
import re
from urllib.error import HTTPError
from urllib.request import Request, urlopen


PR_REF = re.compile(r"refs/pull/([1-9][0-9]*)/(?:merge|head)\Z")
BUILD_KEY = re.compile(
    r"go-build-v2-(Linux|Windows|macOS)-(X64|ARM64|ARM)-"
    r"(quality|fuzz|race|terminal|packages|browser|windows|endurance|publication)-"
    r"([0-9]+\.[0-9]+\.[0-9]+)-[0-9a-f]{64}-[0-9a-f]{64}\Z"
)


def candidates(caches, pr_state, default_ref="refs/heads/main", closed_pr=None, keep=2):
    """Inspect all state before returning any deletion; unknown PR state is retained."""
    doomed = []
    groups = defaultdict(list)
    states = {}
    for cache in caches:
        match = PR_REF.fullmatch(cache["ref"])
        if match:
            number = int(match[1])
            if closed_pr is not None and number != closed_pr:
                continue
            if number not in states:
                states[number] = pr_state(number)
            if states[number] == "closed":
                doomed.append(cache)
        elif closed_pr is None and cache["ref"] == default_ref:
            match = BUILD_KEY.fullmatch(cache["key"])
            if match:
                groups[match.groups()].append(cache)
    for group in groups.values():
        group.sort(key=lambda cache: (cache["created_at"], cache["id"]), reverse=True)
        doomed.extend(group[keep:])
    return sorted(doomed, key=lambda cache: (cache["created_at"], cache["id"]))


def list_caches(api):
    """Do not delete anything unless the entire listing is complete and consistent."""
    caches = []
    seen = set()
    expected_count = None
    for page in range(1, 101):
        # Restoring a cache changes last_accessed_at. Paginate by creation time
        # so active CI cannot reshuffle entries between pages during inspection.
        payload = api(f"actions/caches?per_page=100&page={page}&sort=created_at&direction=asc")
        batch = payload["actions_caches"]
        if not isinstance(batch, list):
            raise ValueError("Invalid cache listing")
        if expected_count is None:
            expected_count = payload["total_count"]
        elif batch and payload["total_count"] != expected_count:
            raise ValueError("Cache count changed during pagination; retry later")
        for cache in batch:
            if cache["id"] in seen:
                raise ValueError("Cache listing changed during pagination; retry later")
            seen.add(cache["id"])
            caches.append(cache)
        if len(batch) < 100:
            # GitHub returns total_count=0 on the empty terminating page when
            # the number of caches is exactly divisible by the page size.
            if len(caches) != expected_count:
                raise ValueError("Incomplete cache listing; retry later")
            return caches
    raise ValueError("Cache listing exceeded safety bound; nothing deleted")


class GitHub:
    def __init__(self, repository, token):
        if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository):
            raise ValueError("Invalid repository")
        self.base = f"https://api.github.com/repos/{repository}"
        self.token = token

    def __call__(self, path, method="GET"):
        url = self.base + ("/" + path if path else "")
        request = Request(url, method=method, headers={
            "Authorization": f"Bearer {self.token}",
            "Accept": "application/vnd.github+json",
            "X-GitHub-Api-Version": "2022-11-28",
        })
        with urlopen(request, timeout=30) as response:
            body = response.read()
            return json.loads(body) if body else None

    def pr_state(self, number):
        try:
            return self(f"pulls/{number}").get("state", "unknown")
        except HTTPError as error:
            if error.code in (403, 404):
                return "unknown"
            raise


def cleanup(api, *, dry_run=True, max_delete=100, closed_pr=None, default_ref="refs/heads/main"):
    if not 1 <= max_delete <= 200:
        raise ValueError("Deletion bound must be between 1 and 200")
    caches = list_caches(api)
    planned = candidates(caches, api.pr_state, default_ref, closed_pr)
    chosen = planned[:max_delete]
    print(f"Inspected {len(caches)} caches; eligible {len(planned)}; selected {len(chosen)}; dry_run={dry_run}")
    for cache in chosen:
        # Report only the immutable numeric ID, not untrusted cache keys or refs.
        print(f"{'Would delete' if dry_run else 'Deleting'} cache {int(cache['id'])}")
        if not dry_run:
            api(f"actions/caches/{int(cache['id'])}", method="DELETE")
    return len(chosen)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--dry-run", dest="dry_run", action="store_true", default=True)
    mode.add_argument("--apply", dest="dry_run", action="store_false")
    parser.add_argument("--closed-pr", type=int)
    parser.add_argument("--max-delete", type=int, default=100)
    args = parser.parse_args()
    api = GitHub(os.environ["GITHUB_REPOSITORY"], os.environ["GH_TOKEN"])
    default_ref = "refs/heads/" + api("")["default_branch"]
    cleanup(api, dry_run=args.dry_run, max_delete=args.max_delete,
            closed_pr=args.closed_pr, default_ref=default_ref)


if __name__ == "__main__":
    main()
