#!/usr/bin/env python3
"""Prepare an optional driver overlay; never install or load a kernel module."""
import argparse
import hashlib
import io
from pathlib import Path
import shutil
import tarfile
import urllib.request

COMMIT = "00fffc49306d748b34a95c14769b30908ef5985f"
SHA256 = "159989355fbfb688c39864846886e1a2b1e14479b07d46bad06cfa31f1f94f16"
URL = f"https://codeload.github.com/tcdent/quantis-pci/tar.gz/{COMMIT}"


def prepare(destination, archive=None):
    if destination.exists():
        raise ValueError("destination already exists; choose a fresh build directory")
    data = archive.read_bytes() if archive else urllib.request.urlopen(URL, timeout=30).read(4 << 20)
    if hashlib.sha256(data).hexdigest() != SHA256:
        raise ValueError("upstream source SHA-256 mismatch")
    # Extract only source/build files required here, with no archive-supplied paths.
    root = f"quantis-pci-{COMMIT}/"
    wanted = ["common/quantis_pci.h", "common/quantis_pci_common.h", "linux/Kbuild"]
    with tarfile.open(fileobj=io.BytesIO(data), mode="r:gz") as tar:
        source = {}
        for name in wanted:
            member = tar.getmember(root + name)
            if not member.isfile() or member.size > 1 << 20:
                raise ValueError("unexpected upstream source member")
            source[name] = tar.extractfile(member).read()
    destination.mkdir(parents=True)
    for name, content in source.items():
        path = destination / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(content)
    overlay = Path(__file__).resolve().parent
    shutil.copytree(overlay / "linux", destination / "linux", dirs_exist_ok=True)
    shutil.copytree(overlay / "tests", destination / "tests")
    return destination


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("destination", type=Path)
    parser.add_argument("--archive", type=Path, help="previously downloaded pinned archive")
    args = parser.parse_args()
    try:
        print(prepare(args.destination, args.archive))
    except (ValueError, OSError, tarfile.TarError) as error:
        parser.exit(1, f"prepare driver: {error}\n")
