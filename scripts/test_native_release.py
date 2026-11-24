"""Check the native build identity, all published aliases, and tamper rejection."""
import hashlib
import json
from pathlib import Path
import tempfile
import unittest

import yaml

from native_release import native_assets, publication_config, stage, verify_native


class NativeReleaseTests(unittest.TestCase):
    version = "2.9.1"
    sha = "f" * 40

    def setUp(self):
        temporary = tempfile.TemporaryDirectory(dir=Path.cwd())
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.dist = self.root / "dist"
        self.native = self.root / "native"
        self.dist.mkdir()
        self.files = native_assets(self.version)
        for name in set(self.files.values()):
            (self.dist / name).write_text(name)
        checksums = {n: hashlib.sha256(f.encode()).hexdigest() for n, f in self.files.items()}
        (self.dist / "checksums.txt").write_text("".join(f"{digest}  {n}\n" for n, digest in checksums.items()))
        (self.dist / "metadata.json").write_text(json.dumps({
            "project_name": "entropy-service", "version": self.version, "tag": "v" + self.version,
            "commit": self.sha}))
        stage(self.dist, self.native, self.version, self.sha)

    def verify(self):
        return verify_native(self.native, self.version, self.sha, staged=True)

    def test_staged_files_become_only_publication_inputs(self):
        files = self.verify()
        self.assertEqual(12, len(set(files.values())))
        self.assertEqual(18, len(files))
        config = yaml.safe_load(publication_config(files))
        self.assertEqual(yaml.safe_load(Path(".goreleaser.yml").read_text())["changelog"], config["changelog"])
        self.assertEqual([{"skip": True}], config["builds"])
        mappings = config["release"]["extra_files"]
        self.assertEqual(mappings, config["checksum"]["extra_files"])
        self.assertEqual({f["name_template"]: Path(f["glob"]).resolve() for f in mappings}, files)

    def test_wrong_source_and_snapshot_rejected(self):
        with self.assertRaisesRegex(ValueError, "metadata"):
            verify_native(self.native, self.version, "e" * 40, staged=True)
        with self.assertRaisesRegex(ValueError, "metadata"):
            verify_native(self.native, self.version + "-SNAPSHOT", self.sha, staged=True)

    def test_payload_corruption_rejected(self):
        (self.native / next(iter(self.files.values()))).write_text("different")
        with self.assertRaisesRegex(ValueError, "checksum"):
            self.verify()

    def test_extra_payload_and_alias_mismatch_rejected(self):
        extra = self.native / "old-binary.zip"
        extra.write_text("stale")
        with self.assertRaisesRegex(ValueError, "unexpected"):
            self.verify()
        extra.unlink()
        manifest = self.native / "checksums.txt"
        lines = manifest.read_text().splitlines(keepends=True)
        alias = next(name for name, file in self.files.items() if name != file)
        manifest.write_text("".join("0" * 64 + line[64:] if line.rstrip().endswith("  " + alias) else line for line in lines))
        with self.assertRaisesRegex(ValueError, "alias"):
            self.verify()


if __name__ == "__main__":
    unittest.main()
