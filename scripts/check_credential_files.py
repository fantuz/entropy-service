#!/usr/bin/env python3
"""Reject tracked TLS identity files and private-key PEM headers, without logging secrets."""

import re
import subprocess
import sys
from pathlib import Path


PRIVATE_KEY_HEADER = re.compile(
    rb"(?m)^-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----\r?$"
)
IDENTITY_SUFFIXES = {".pem", ".key", ".p12", ".pfx"}


def credential_files(root: Path) -> list[str]:
    tracked = subprocess.run(
        ["git", "-C", str(root), "ls-files", "-z"],
        check=True,
        stdout=subprocess.PIPE,
    ).stdout
    rejected = []
    for name in tracked.split(b"\0"):
        if not name:
            continue
        relative = name.decode("utf-8", errors="surrogateescape")
        path = root / relative
        if path.suffix.lower() in IDENTITY_SUFFIXES:
            rejected.append(relative)
        elif path.is_symlink():
            # Never follow links outside the checkout to inspect credentials.
            continue
        elif path.is_file() and PRIVATE_KEY_HEADER.search(path.read_bytes()):
            rejected.append(relative)
    return rejected


def main() -> int:
    root = Path(__file__).resolve().parents[1]
    rejected = credential_files(root)
    if rejected:
        for name in rejected:
            print(f"Tracked credential file rejected: {name!r}", file=sys.stderr)
        return 1
    print("No tracked TLS identity files or private-key PEM headers.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
