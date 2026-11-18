"""Verify the scan/SBOM evidence bound to a tested image config digest."""
import hashlib
import json
import re

from check_container_scan import check_report


def verify_security_reports(directory, role, image_id):
    expected = {r + suffix for r in ("server", "client") for suffix in (".spdx.json", ".vulnerabilities.json")}
    entries = {}
    for line in (directory / "security-checksums.txt").read_text().splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})  ([a-z.]+)", line)
        if not match or match[2] not in expected or match[2] in entries:
            raise ValueError("invalid image-security checksum manifest")
        entries[match[2]] = match[1]
    if set(entries) != expected:
        raise ValueError("missing image-security reports")
    for name, digest in entries.items():
        if hashlib.sha256((directory / name).read_bytes()).hexdigest() != digest:
            raise ValueError("image-security report checksum mismatch")
    report = json.loads((directory / (role + ".vulnerabilities.json")).read_text())
    if check_report(report, image_id):
        raise ValueError("image has fixable HIGH/CRITICAL vulnerabilities")
    sbom = directory / (role + ".spdx.json")
    document = json.loads(sbom.read_text())
    if document.get("SPDXID") != "SPDXRef-DOCUMENT" or not document.get("spdxVersion", "").startswith("SPDX-") or not document.get("packages"):
        raise ValueError("invalid or empty image SBOM")
    return sbom


