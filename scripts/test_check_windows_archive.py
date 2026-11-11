"""Verify missing notices, wrong architectures and ambiguous/unsafe ZIP layouts fail."""

from pathlib import Path
import struct
import tempfile
import unittest
import zipfile

from check_windows_archive import REQUIRED, check_archive


def executable(machine=0x8664):
    image = bytearray(128)
    image[:2] = b"MZ"
    struct.pack_into("<I", image, 0x3c, 64)
    image[64:68] = b"PE\0\0"
    struct.pack_into("<H", image, 68, machine)
    struct.pack_into("<H", image, 88, 0x20b)
    return bytes(image)


class WindowsArchiveTests(unittest.TestCase):
    def archive(self, directory, missing=None, machine=0x8664, extra=None):
        path = Path(directory) / "windows.zip"
        with zipfile.ZipFile(path, "w") as archive:
            for name in sorted(REQUIRED - {missing}):
                archive.writestr(name, executable(machine) if name.endswith(".exe") else b"release notice\n")
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

    def test_invalid_deliverables_are_rejected(self):
        for options in [
            {"missing": "WORDLIST_NOTICES.txt"},
            {"missing": "QUANTIS_NOTICES.txt"},
            {"missing": "quantis.md"},
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
