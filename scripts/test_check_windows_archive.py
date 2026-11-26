"""Verify missing notices, wrong architectures and ambiguous/unsafe ZIP layouts fail."""

from pathlib import Path
import struct
import tempfile
import unittest
import zipfile

from check_windows_archive import GUI_FILES, GUI_SOURCE, REQUIRED, check_archive


def executable(machine=0x8664):
    image = bytearray(128)
    image[:2] = b"MZ"
    struct.pack_into("<I", image, 0x3c, 64)
    image[64:68] = b"PE\0\0"
    struct.pack_into("<H", image, 68, machine)
    struct.pack_into("<H", image, 88, 0x20b)
    return bytes(image)


class WindowsArchiveTests(unittest.TestCase):
    def archive(self, directory, missing=None, machine=0x8664, extra=None, corrupt_gui=None):
        path = Path(directory) / "windows.zip"
        with zipfile.ZipFile(path, "w") as archive:
            for name in sorted(REQUIRED - {missing}):
                if name.endswith(".exe"):
                    data = executable(machine)
                elif name in GUI_FILES and name != corrupt_gui:
                    data = (GUI_SOURCE / name).read_bytes()
                else:
                    data = b"release notice\n"
                archive.writestr(name, data)
            if extra:
                archive.writestr(extra, b"unexpected content")
        return path

    def test_valid_archive_and_fresh_extraction(self):
        with tempfile.TemporaryDirectory() as directory:
            path = self.archive(directory)
            destination = Path(directory) / "extracted"
            self.assertEqual(len(check_archive(path, destination)), 64)
            self.assertEqual((destination / "entropy-client.exe").read_bytes(), executable())
            with self.assertRaises(FileExistsError):
                check_archive(path, destination)

    def test_staged_hash_checked_before_extraction(self):
        with tempfile.TemporaryDirectory() as directory:
            archive = self.archive(directory)
            manifest = Path(directory) / "checksums.txt"
            dest = Path(directory) / "extracted"
            manifest.write_text("0" * 64 + "  windows.zip\n")
            with self.assertRaisesRegex(ValueError, "checksum"):
                check_archive(archive, dest, manifest)
            self.assertFalse(dest.exists())
            manifest.write_text(check_archive(archive) + "  windows.zip\n")
            check_archive(archive, dest, manifest)

    def test_invalid_deliverables_are_rejected(self):
        for options in [
            {"missing": "WORDLIST_NOTICES.txt"},
            {"missing": "QUANTIS_NOTICES.txt"},
            {"missing": "docs/quantis.md"},
            {"missing": "docs/hardware-probe.md"},
            {"missing": "docs/hardware-drivers.md"},
            {"missing": "docs/quantis-usb.md"},
            {"missing": "docs/chaoskey.md"},
            {"missing": "docs/windows-service.md"},
            {"missing": "docs/macos.md"},
            {"missing": "docs/desktop-gui.md"},
            {"missing": "entropy-gui"},
            {"missing": "entropy_gui/app.py", "extra": "app.py"},
            {"corrupt_gui": "entropy_gui/model.py"},
            {"missing": "entropy-client.exe"},
            {"machine": 0xaa64},
            {"extra": "../unexpected"},
            {"extra": "ENTROPY-SERVER.EXE"},
            {"extra": "server-key.pem"},
        ]:
            with self.subTest(options=options), tempfile.TemporaryDirectory() as directory:
                with self.assertRaises(ValueError):
                    check_archive(self.archive(directory, **options))


if __name__ == "__main__":
    unittest.main()
