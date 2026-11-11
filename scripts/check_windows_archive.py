#!/usr/bin/env python3
"""Validate the actual GoReleaser Windows x64 ZIP before delivery or execution."""

import argparse
import hashlib
from pathlib import Path, PurePosixPath
import stat
import struct
import zipfile


REQUIRED = {"entropy-server.exe", "entropy-client.exe", "LICENSE", "README.md",
            "WORDLIST_NOTICES.txt", "windows.md", "quantis.md", "QUANTIS_NOTICES.txt"}


def check_archive(path, extract_to=None):
    with zipfile.ZipFile(path) as archive:
        names, files = set(), {}
        for member in archive.infolist():
            name = member.filename
            relative = PurePosixPath(name)
            if relative.is_absolute() or ".." in relative.parts or "\\" in name or ":" in name:
                raise ValueError(f"unsafe archive path: {name}")
            if name.casefold() in names or stat.S_ISLNK(member.external_attr >> 16):
                raise ValueError(f"duplicate path or symlink: {name}")
            names.add(name.casefold())
            if member.is_dir():
                continue
            if relative.name not in REQUIRED or relative.name in files:
                raise ValueError(f"unexpected or duplicated release file: {name}")
            files[relative.name] = member

        if set(files) != REQUIRED:
            raise ValueError(f"missing release files: {sorted(REQUIRED - files.keys())}")
        if archive.testzip() is not None:
            raise ValueError("ZIP CRC verification failed")

        for program in ("entropy-server.exe", "entropy-client.exe"):
            member = files[program]
            if member.filename != program:
                raise ValueError(f"executable must be at archive root: {member.filename}")
            image = archive.read(member)
            if len(image) < 64 or image[:2] != b"MZ":
                raise ValueError(f"not a Windows executable: {program}")
            pe = struct.unpack_from("<I", image, 0x3c)[0]
            if pe + 26 > len(image) or image[pe:pe + 4] != b"PE\0\0":
                raise ValueError(f"invalid PE header: {program}")
            if struct.unpack_from("<H", image, pe + 4)[0] != 0x8664 or struct.unpack_from("<H", image, pe + 24)[0] != 0x20b:
                raise ValueError(f"not a Windows x64 PE32+ executable: {program}")

        for name in REQUIRED - {"entropy-server.exe", "entropy-client.exe"}:
            if not archive.read(files[name]).strip():
                raise ValueError(f"empty release documentation/notice: {name}")

        if extract_to is not None:
            destination = Path(extract_to)
            destination.mkdir(parents=True, exist_ok=False)
            archive.extractall(destination)

    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("archive", type=Path)
    parser.add_argument("--extract-to", type=Path, help="extract verified contents into a new directory")
    args = parser.parse_args()
    digest = check_archive(args.archive, args.extract_to)
    print(f"Verified Windows x64 release ZIP: {args.archive.name}; SHA-256 {digest}")


if __name__ == "__main__":
    main()
