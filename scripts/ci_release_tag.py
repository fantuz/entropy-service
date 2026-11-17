#!/usr/bin/env python3
"""Verify normal release jobs run on the exact tag named by VERSION."""

import os
from pathlib import Path


if __name__ == "__main__":
    tag = os.environ.get("RELEASE_TAG", "")
    if tag:
        expected = "v" + Path("VERSION").read_text(encoding="utf-8").strip()
        if os.environ.get("GITHUB_REF_TYPE") != "tag" or tag != expected:
            raise SystemExit(f"Select the exact VERSION tag {expected!r}; received {tag!r}")
        print(f"Validated release source tag {tag}")
