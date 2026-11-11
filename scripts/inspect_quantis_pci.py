#!/usr/bin/env python3
"""Inspect an offline Quantis PCI vendor ZIP; never extract, install or load it."""

import argparse
import configparser
import hashlib
import json
from pathlib import Path
import stat
import struct
import uuid
import zipfile
import zlib


PREFIX = "windows/Drivers/Windows/QuantisPci/"
HARDWARE_ID = r"PCI\VEN_179A&DEV_0001"
GUID = uuid.UUID("f2f5f1e5-380e-4194-8fc0-ed5af63e93df")
REFERENCE_SHA256 = "f088ebb9175592485e9809581b4c8631a4b535c6e4eea0d4f7df6cc0ab56633b"
MAX_MEMBER_BYTES = 1 << 20


def read_member(archive, name):
    matches = [entry for entry in archive.infolist()
               if entry.filename.casefold() == name.casefold()]
    if len(matches) != 1:
        raise ValueError(f"missing or ambiguous member: {name}")
    member = matches[0]
    if (member.is_dir() or stat.S_ISLNK(member.external_attr >> 16)
            or not 0 < member.file_size <= MAX_MEMBER_BYTES):
        raise ValueError(f"not a bounded regular member: {name}")
    return archive.read(member)  # zipfile also checks this member's CRC.


def inspect_pe(data, machine, magic):
    if len(data) < 64 or data[:2] != b"MZ":
        raise ValueError("driver lacks a DOS/PE header")
    pe = struct.unpack_from("<I", data, 0x3c)[0]
    if pe < 64 or pe + 24 > len(data) or data[pe:pe + 4] != b"PE\0\0":
        raise ValueError("invalid PE header offset or signature")
    optional = pe + 24
    optional_size = struct.unpack_from("<H", data, pe + 20)[0]
    directory = 112 if magic == 0x20b else 96
    if optional_size < directory + 40 or optional + optional_size > len(data):
        raise ValueError("truncated PE optional header")
    if (struct.unpack_from("<H", data, pe + 4)[0] != machine
            or struct.unpack_from("<H", data, optional)[0] != magic
            or struct.unpack_from("<H", data, optional + 68)[0] != 1):
        raise ValueError("driver architecture, PE format or native subsystem mismatch")
    if struct.unpack_from("<I", data, optional + directory - 4)[0] < 5:
        raise ValueError("PE security directory is absent")
    offset, size = struct.unpack_from("<II", data, optional + directory + 32)
    if bool(offset) != bool(size) or (size and (offset > len(data) or size > len(data) - offset)):
        raise ValueError("invalid PE security directory range")
    if GUID.bytes_le not in data:
        raise ValueError("expected legacy GUID constant not found in driver")
    return {"machine": f"0x{machine:04x}", "pe_format": "PE32+" if magic == 0x20b else "PE32",
            "subsystem": "native", "legacy_guid_constant_present": True,
            "embedded_signature_present": size > 0, "sha256": hashlib.sha256(data).hexdigest(),
            "bytes": len(data)}


def inspect_archive(path):
    """Return structural observations, not Windows trust or hardware qualification."""
    with zipfile.ZipFile(path) as archive:
        raw = read_member(archive, PREFIX + "QuantisPCI.inf")
        text = raw.decode("utf-16") if raw.startswith((b"\xff\xfe", b"\xfe\xff")) else raw.decode("utf-8-sig")
        inf = configparser.ConfigParser(interpolation=None, allow_no_value=True,
                                       comment_prefixes=(";",))
        inf.read_string(text)
        version = inf["Version"]
        if uuid.UUID(version["ClassGuid"].strip()) != GUID:
            raise ValueError("unexpected INF setup-class GUID")
        if version["CatalogFile"].strip().casefold() != "quantispci.cat":
            raise ValueError("unexpected INF catalog reference")
        for section in ("IDQ.NTx86", "IDQ.NTamd64"):
            models = [value.split(";", 1)[0].split(",")[-1].strip().upper()
                      for value in inf[section].values() if value]
            if HARDWARE_ID not in models:
                raise ValueError(f"legacy PCI ID missing from INF model section {section}")
        driver_version = version["DriverVer"].strip()
        if not driver_version:
            raise ValueError("empty INF DriverVer")
        catalog = read_member(archive, PREFIX + "QuantisPCI.cat")
        if len(catalog) < 16 or catalog[:1] != b"\x30":
            raise ValueError("catalog is not a nonempty ASN.1 candidate")
        drivers = {}
        for architecture, machine, magic in (("x86", 0x14c, 0x10b), ("amd64", 0x8664, 0x20b)):
            member = PREFIX + architecture + "/QuantisPCI.sys"
            drivers[architecture] = {"member": member,
                                     **inspect_pe(read_member(archive, member), machine, magic)}
    with Path(path).open("rb") as source:
        checksum = hashlib.sha256()
        for chunk in iter(lambda: source.read(1 << 20), b""):
            checksum.update(chunk)
        digest = checksum.hexdigest()
    return {
        "schema_version": 1, "metadata_ok": True, "archive_sha256": digest,
        "reference_sha256": REFERENCE_SHA256, "matches_reference_sha256": digest == REFERENCE_SHA256,
        "hardware_id": HARDWARE_ID, "inf_class_guid": str(GUID), "inf_driver_version": driver_version,
        "inf_sha256": hashlib.sha256(raw).hexdigest(), "drivers": drivers,
        "catalog_sha256": hashlib.sha256(catalog).hexdigest(),
        "catalog_signature_verified": False, "catalog_member_hashes_verified": False,
        "windows_kernel_policy_verified": False, "interface_registration_verified": False,
        "hardware_tested": False, "overlapped_io_verified": False, "cancellation_verified": False,
        "quality_assessed": False,
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("archive", type=Path, help="local pci-module-20.2.3.zip; no network access")
    parser.add_argument("--json", action="store_true", help="print a machine-readable metadata report")
    args = parser.parse_args()
    try:
        report = inspect_archive(args.archive)
    except (OSError, ValueError, KeyError, configparser.Error, zipfile.BadZipFile,
            NotImplementedError, RuntimeError, EOFError, zlib.error) as error:
        report = {"schema_version": 1, "metadata_ok": False, "error": str(error)}
    if args.json:
        print(json.dumps(report, sort_keys=True))
    elif report["metadata_ok"]:
        print(f"Metadata matches {HARDWARE_ID}; INF DriverVer {report['inf_driver_version']}")
        print(f"Archive SHA-256: {report['archive_sha256']}")
        print(f"Matches recorded reference archive: {report['matches_reference_sha256']}")
        print("x86/amd64 PE headers and GUID constants match; catalog candidate present.")
        print("Windows signature/policy, interface registration and physical I/O remain unverified.")
    else:
        print(f"Metadata inspection failed: {report['error']}")
    return 0 if report["metadata_ok"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
