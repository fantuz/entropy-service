#!/usr/bin/env python3
"""Conservatively choose the PR documentation fast path from the actual git diff."""

import json
import os
from pathlib import Path, PurePosixPath
import re
import subprocess


def documentation_path(path):
    parts = PurePosixPath(path).parts
    return path == "README.md" or (
        len(parts) >= 2 and parts[0] == "docs" and path.endswith(".md")
    )


def classify(base, head="HEAD", cwd=None):
    """Unknown bases, modes and filenames always select the complete checks."""
    if not isinstance(base, str) or not re.fullmatch(r"[0-9a-fA-F]{40}", base):
        return "full"
    try:
        raw = subprocess.check_output(
            ["git", "diff", "--raw", "-z", "--no-renames", base, head, "--"],
            cwd=cwd, stderr=subprocess.DEVNULL,
        )
        fields = raw.split(b"\0")
        if fields.pop() != b"" or not fields:
            return "full"
        if len(fields) % 2:
            return "full"
        for index in range(0, len(fields), 2):
            header = fields[index].decode("ascii").split()
            path = fields[index + 1].decode("utf-8", errors="strict")
            if (len(header) != 5 or header[0] not in (":100644", ":000000")
                    or header[1] not in ("100644", "000000")
                    or header[4] not in ("A", "M", "D")
                    or not documentation_path(path)):
                return "full"
        return "docs"
    except (subprocess.CalledProcessError, UnicodeError, ValueError, OSError):
        return "full"


def main():
    mode = "full"
    if os.environ.get("GITHUB_EVENT_NAME") == "pull_request":
        try:
            event = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())
            mode = classify(event["pull_request"]["base"]["sha"])
        except (KeyError, TypeError, ValueError, OSError):
            pass
    with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as output:
        output.write(f"mode={mode}\n")
    print(f"CI plan: {mode}. Only regular README.md and docs/**/*.md PR changes use the fast path.")


if __name__ == "__main__":
    main()
