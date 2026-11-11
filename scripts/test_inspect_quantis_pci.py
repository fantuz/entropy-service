"""Exercise malformed/mismatched metadata; synthetic images are not vendor drivers."""

import json
from pathlib import Path
import struct
import subprocess
import sys
import tempfile
import unittest
import zipfile

from inspect_quantis_pci import GUID, HARDWARE_ID, PREFIX, inspect_archive


INF = f"""[Version]
ClassGuid={{{GUID}}}
CatalogFile=QuantisPCI.cat
DriverVer=04/01/2010, 5.0
[IDQ.NTx86]
%device%=Install, {HARDWARE_ID}
[IDQ.NTamd64]
%device%=Install, {HARDWARE_ID}
"""


def pe(machine, magic):
    image = bytearray(512)
    image[:2] = b"MZ"
    struct.pack_into("<I", image, 0x3c, 64)
    image[64:68] = b"PE\0\0"
    struct.pack_into("<H", image, 68, machine)
    struct.pack_into("<H", image, 84, 240 if magic == 0x20b else 224)
    struct.pack_into("<H", image, 88, magic)
    struct.pack_into("<H", image, 88 + 68, 1)
    struct.pack_into("<I", image, 88 + (108 if magic == 0x20b else 92), 16)
    image[384:400] = GUID.bytes_le
    return image


class QuantisPackageTests(unittest.TestCase):
    def package(self, directory, changes=None, missing=None, duplicate=None):
        members = {"QuantisPCI.inf": INF.encode(), "QuantisPCI.cat": b"\x30" + bytes(31),
                   "x86/QuantisPCI.sys": pe(0x14c, 0x10b),
                   "amd64/QuantisPCI.sys": pe(0x8664, 0x20b)}
        members.update(changes or {})
        path = Path(directory) / "candidate.zip"
        with zipfile.ZipFile(path, "w") as archive:
            for name, data in members.items():
                if name != missing:
                    archive.writestr(PREFIX + name, data)
            if duplicate:
                archive.writestr((PREFIX + duplicate).upper(), members[duplicate])
        return path

    def test_metadata_reports_do_not_claim_trust_or_hardware_validation(self):
        for encoding in ("utf-8", "utf-16"):
            with self.subTest(encoding=encoding), tempfile.TemporaryDirectory() as directory:
                path = self.package(directory, {"QuantisPCI.inf": INF.encode(encoding)})
                report = inspect_archive(path)
                self.assertTrue(report["metadata_ok"])
                self.assertFalse(report["matches_reference_sha256"])
                for field in ("catalog_signature_verified", "catalog_member_hashes_verified",
                              "windows_kernel_policy_verified", "interface_registration_verified",
                              "hardware_tested", "overlapped_io_verified", "cancellation_verified",
                              "quality_assessed"):
                    self.assertIs(report[field], False)
                self.assertEqual(report["drivers"]["amd64"]["machine"], "0x8664")
                self.assertFalse(report["drivers"]["amd64"]["embedded_signature_present"])
                self.assertEqual(sorted(p.name for p in Path(directory).iterdir()), ["candidate.zip"])

    def test_mismatches_and_ambiguous_members_fail(self):
        bad_offset = pe(0x8664, 0x20b)
        struct.pack_into("<I", bad_offset, 0x3c, 0xffffffff)
        bad_certificate = pe(0x8664, 0x20b)
        struct.pack_into("<II", bad_certificate, 88 + 112 + 32, 500, 64)
        application = pe(0x8664, 0x20b)
        struct.pack_into("<H", application, 88 + 68, 3)
        wrong_guid = pe(0x8664, 0x20b)
        wrong_guid[384:400] = bytes(16)
        for options in (
            {"missing": "QuantisPCI.cat"}, {"duplicate": "QuantisPCI.inf"},
            {"changes": {"QuantisPCI.inf": INF.replace(HARDWARE_ID, r"PCI\VEN_1E89&DEV_0002").encode()}},
            {"changes": {"QuantisPCI.inf": INF.replace(str(GUID), "00000000-0000-0000-0000-000000000000").encode()}},
            {"changes": {"amd64/QuantisPCI.sys": pe(0x14c, 0x10b)}},
            {"changes": {"amd64/QuantisPCI.sys": bytes(8)}},
            {"changes": {"amd64/QuantisPCI.sys": bad_offset}},
            {"changes": {"amd64/QuantisPCI.sys": bad_certificate}},
            {"changes": {"amd64/QuantisPCI.sys": application}},
            {"changes": {"amd64/QuantisPCI.sys": wrong_guid}},
            {"changes": {"QuantisPCI.cat": b"not a catalog"}},
        ):
            with self.subTest(options=options), tempfile.TemporaryDirectory() as directory:
                with self.assertRaises(ValueError):
                    inspect_archive(self.package(directory, **options))

    def test_cli_failure_is_json_and_nonzero(self):
        with tempfile.TemporaryDirectory() as directory:
            path = self.package(directory, missing="QuantisPCI.cat")
            result = subprocess.run([sys.executable, str(Path(__file__).with_name("inspect_quantis_pci.py")),
                                     str(path), "--json"], capture_output=True, text=True, check=False)
            self.assertEqual(result.returncode, 1)
            self.assertFalse(json.loads(result.stdout)["metadata_ok"])


if __name__ == "__main__":
    unittest.main()
