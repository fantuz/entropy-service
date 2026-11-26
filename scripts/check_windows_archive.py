#!/usr/bin/env python3
"""Validate the actual GoReleaser Windows x64 ZIP before delivery or execution."""

import argparse
import hashlib
from pathlib import Path, PurePosixPath
import stat
import struct
import zipfile

from check_release_downloads import read_checksums


GUI_SOURCE = Path(__file__).resolve().parents[1] / "tools/desktop-gui"
GUI_FILES = {"entropy-gui"} | {
    path.relative_to(GUI_SOURCE).as_posix() for path in (GUI_SOURCE / "entropy_gui").glob("*.py")
}
REQUIRED = {
    "entropy-server.exe", "entropy-client.exe", "LICENSE", "README.md",
    "WORDLIST_NOTICES.txt", "QUANTIS_NOTICES.txt", "docs/desktop-gui.md",
    "docs/macos.md", "docs/windows.md", "docs/windows-service.md", "docs/quantis.md",
    "docs/hardware-probe.md", "docs/hardware-drivers.md", "docs/quantis-usb.md", "docs/chaoskey.md",
} | GUI_FILES


def check_archive(path, extract_to=None, checksums=None):
    path = Path(path)
    with path.open("rb") as source:
        digest = hashlib.file_digest(source, "sha256").hexdigest()
    if checksums is not None and read_checksums(checksums).get(path.name) != digest:
        raise ValueError("Windows archive differs from its staged checksum")
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
                if name not in ("docs/", "entropy_gui/"):
                    raise ValueError(f"unexpected release directory: {name}")
                continue
            if name not in REQUIRED:
                raise ValueError(f"unexpected release file: {name}")
            files[name] = member

        if set(files) != REQUIRED:
            raise ValueError(f"missing release files: {sorted(REQUIRED - files.keys())}")
        if archive.testzip() is not None:
            raise ValueError("ZIP CRC verification failed")

        for program in ("entropy-server.exe", "entropy-client.exe"):
            member = files[program]
            image = archive.read(member)
            if len(image) < 64 or image[:2] != b"MZ":
                raise ValueError(f"not a Windows executable: {program}")
            pe = struct.unpack_from("<I", image, 0x3c)[0]
            if pe + 26 > len(image) or image[pe:pe + 4] != b"PE\0\0":
                raise ValueError(f"invalid PE header: {program}")
            if struct.unpack_from("<H", image, pe + 4)[0] != 0x8664 or struct.unpack_from("<H", image, pe + 24)[0] != 0x20b:
                raise ValueError(f"not a Windows x64 PE32+ executable: {program}")

        for name in GUI_FILES:
            if archive.read(files[name]) != (GUI_SOURCE / name).read_bytes():
                raise ValueError(f"GUI file differs from matching source: {name}")
        for name in REQUIRED - GUI_FILES - {"entropy-server.exe", "entropy-client.exe"}:
            if not archive.read(files[name]).strip():
                raise ValueError(f"empty release documentation/notice: {name}")

        if extract_to is not None:
            destination = Path(extract_to)
            destination.mkdir(parents=True, exist_ok=False)
            archive.extractall(destination)

    return digest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("archive", type=Path)
    parser.add_argument("--extract-to", type=Path, help="extract verified contents into a new directory")
    parser.add_argument("--checksums", type=Path, help="verify against the native package producer manifest")
    args = parser.parse_args()
    digest = check_archive(args.archive, args.extract_to, args.checksums)
    print(f"Verified Windows x64 release ZIP: {args.archive.name}; SHA-256 {digest}")


if __name__ == "__main__":
    main()
