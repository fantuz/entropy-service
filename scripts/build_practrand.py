#!/usr/bin/env python3
"""Build a pinned external statistical tool in a new, user-owned directory."""

import argparse
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import urllib.request
import zipfile

URL = "https://downloads.sourceforge.net/project/pracrand/PractRand-pre0.95.zip"
SHA256 = "5738c322ccb983b63fbe693f305afa2a7c73e64a80b303c9139278c453ce27b4"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("destination", type=Path)
    parser.add_argument("--archive", type=Path, help="use a local copy with the same required digest")
    args = parser.parse_args()
    if not shutil.which("g++"):
        parser.error("g++ is required")
    destination = args.destination.resolve()
    if destination.exists():
        parser.error("destination must not already exist")
    if args.archive:
        archive = args.archive.read_bytes()
    else:
        with urllib.request.urlopen(URL, timeout=60) as response:
            archive = response.read(2 << 20)
    if hashlib.sha256(archive).hexdigest() != SHA256:
        parser.error("archive digest differs from the pinned source")
    destination.mkdir(parents=True)
    source = destination / "PractRand-pre0.95.zip"
    source.write_bytes(archive)
    with zipfile.ZipFile(source) as zipped:
        for entry in zipped.infolist():
            if not (destination / entry.filename).resolve().is_relative_to(destination):
                parser.error("archive path escapes destination")
            if (entry.external_attr >> 16) & 0o170000 == 0o120000:
                parser.error("archive contains a symbolic link")
        zipped.extractall(destination)
    sources = sorted(str(path) for path in (destination / "src").rglob("*.cpp"))
    sources.append(str(destination / "tools/RNG_test.cpp"))
    command = ["g++", "-O2", "-std=c++11", "-pthread",
               "-I" + str(destination / "include"), *sources,
               "-o", str(destination / "RNG_test")]
    subprocess.run(command, check=True)
    (destination / "provenance.json").write_text(
        json.dumps({"url": URL, "sha256": SHA256, "command": command}, indent=2) + "\n")


if __name__ == "__main__":
    main()
