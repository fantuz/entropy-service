"""Reject stale, corrupt or incomplete installer assets before native publication."""
import hashlib
import json
from pathlib import Path
import tempfile
import unittest
import zipfile

from prepare_gui_release import release_config, select_version, verify_artifacts


VERSION = "2.4.1"
SHA = "1" * 40


def assets(root, version=VERSION, sha=SHA, extra=None, source_digest=None):
    source = b"complete source package fixture"
    manifest = {"version": version, "source_sha": sha,
                "packages": [{"name": "library", "source": "library.src.tar.zst"}],
                "sources": [{"file": "library.src.tar.zst", "sha256": source_digest or hashlib.sha256(source).hexdigest()}]}
    setup = root / f"entropy-service_{VERSION}_windows_amd64_gui_setup.exe"
    setup.write_bytes(b"MZinstaller fixture")
    sources = root / f"entropy-service_{VERSION}_windows_amd64_gui_sources.zip"
    with zipfile.ZipFile(sources, "w") as archive:
        archive.writestr("bundle-manifest.json", json.dumps(manifest))
        archive.writestr("LICENSE", "License fixture")
        archive.writestr("library.src.tar.zst", source)
        if extra:
            archive.writestr(extra, b"unsafe")
    lines = [hashlib.sha256(p.read_bytes()).hexdigest() + "  " + p.name for p in (setup, sources)]
    (root / "checksums.txt").write_text("\n".join(lines) + "\n")
    return setup, sources


class GuiReleaseTests(unittest.TestCase):
    def test_tagged_and_development_versions(self):
        self.assertEqual(select_version(VERSION, "", "branch", "feature"), (VERSION + "-dev", VERSION))
        self.assertEqual(select_version(VERSION, "v" + VERSION, "tag", "v" + VERSION), (VERSION, VERSION))
        self.assertEqual(select_version("2.4.1-rc.1", "v2.4.1-rc.1", "tag", "v2.4.1-rc.1"), ("2.4.1-rc.1", VERSION))
        for tag, ref_type, ref in [("v2.4.0", "tag", "v2.4.0"), ("v" + VERSION, "branch", "v" + VERSION), ("v" + VERSION, "tag", "v2.4.0")]:
            with self.subTest(tag=tag, ref_type=ref_type), self.assertRaises(ValueError):
                select_version(VERSION, tag, ref_type, ref)

    def test_exact_assets_and_release_config(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            files = assets(root)
            self.assertEqual(verify_artifacts(root, VERSION, SHA), list(files))
            base = Path(__file__).resolve().parents[1] / ".goreleaser.yml"
            config = release_config(base.read_text(), files)
            for file in files:
                self.assertEqual(config.count('name_template: ' + json.dumps(file.name)), 2)
            self.assertIn('name_template: entropy-service_windows_amd64.zip', config)
            self.assertIn('name_template: entropy-service_linux_armv7.tar.gz', config)

    def test_old_source_or_version_is_rejected(self):
        for version, sha in [("2.4.1-dev", SHA), (VERSION, "2" * 40)]:
            with self.subTest(version=version, sha=sha), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                assets(root, version, sha)
                with self.assertRaisesRegex(ValueError, "manifest"):
                    verify_artifacts(root, VERSION, SHA)

    def test_corruption_or_unexpected_assets_is_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            setup, _ = assets(root)
            setup.write_bytes(b"corrupted")
            with self.assertRaisesRegex(ValueError, "checksum"):
                verify_artifacts(root, VERSION, SHA)
            assets(root)
            (root / "extra.exe").write_bytes(b"unexpected")
            with self.assertRaisesRegex(ValueError, "only"):
                verify_artifacts(root, VERSION, SHA)

    def test_source_corruption_and_missing_config_sections_are_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            files = assets(root, source_digest="0" * 64)
            with self.assertRaisesRegex(ValueError, "source checksum"):
                verify_artifacts(root, VERSION, SHA)
            with self.assertRaisesRegex(ValueError, "release"):
                release_config("release:\n  draft: false\nchecksum:\n  extra_files:\n", files)

    def test_unsafe_source_zip_is_rejected(self):
        for name in ("../escape", "C:drive", "nested/source"):
            with self.subTest(name=name), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                assets(root, extra=name)
                with self.assertRaisesRegex(ValueError, "unsafe"):
                    verify_artifacts(root, VERSION, SHA)


if __name__ == "__main__":
    unittest.main()
