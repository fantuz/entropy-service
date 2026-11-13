"""Release-download contract checks without network or a GoReleaser dependency."""
import hashlib
from pathlib import Path
import tempfile
import unittest

from check_release_downloads import ALIASES, LATEST, check_contract, check_dist


def configuration():
    pairs = "".join(f"    - glob: ./dist/{source}\n      name_template: {name}\n" for name, source in ALIASES.items())
    return "release:\n  extra_files:\n" + pairs + "\nchecksum:\n  algorithm: sha256\n  extra_files:\n" + pairs


def readme():
    return "\n".join(f'<a href="{LATEST}{name}">Download</a>' for name in ALIASES)


def artifacts(root):
    lines = []
    for index, (alias, source) in enumerate(ALIASES.items()):
        name = source.replace("{{ .Version }}", "2.1.0")
        data = bytes([index])*64 + b"public archive fixture"
        (root / name).write_bytes(data)
        digest = hashlib.sha256(data).hexdigest()
        lines += [f"{digest}  {name}", f"{digest}  {alias}"]
    (root / "checksums.txt").write_text("\n".join(lines) + "\n")


class ReleaseDownloadsTests(unittest.TestCase):
    def test_all_four_architectures_have_matching_config_and_readme(self):
        check_contract(configuration(), readme())
        for section in ("release", "checksum"):
            broken = configuration().replace(f"{section}:\n", f"wrong-{section}:\n")
            with self.assertRaisesRegex(ValueError, section):
                check_contract(broken, readme())
        missing = readme().replace(LATEST + "entropy-service_windows_amd64.zip", LATEST + "not-published.zip")
        with self.assertRaisesRegex(ValueError, "missing stable"):
            check_contract(configuration(), missing)

    def test_source_mapping_must_stay_versioned_and_checksum_covered(self):
        broken = configuration().replace("./dist/entropy-service_{{ .Version }}_linux_amd64.tar.gz", "./dist/wrong.tar.gz", 1)
        with self.assertRaisesRegex(ValueError, "release aliases"):
            check_contract(broken, readme())

    def test_actual_bytes_are_checked_without_duplicate_dist_files(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            artifacts(root)
            self.assertEqual(len(check_dist(root)), 4)
            source = next(root.glob("*_linux_amd64.tar.gz"))
            source.write_bytes(b"corrupted")
            with self.assertRaisesRegex(ValueError, "actual archive"):
                check_dist(root)

    def test_alias_must_have_its_own_matching_checksum_entry(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            artifacts(root)
            sums = root / "checksums.txt"
            sums.write_text("\n".join(line for line in sums.read_text().splitlines() if not line.endswith("entropy-service_windows_amd64.zip")) + "\n")
            with self.assertRaisesRegex(ValueError, "checksums"):
                check_dist(root)

    def test_alias_duplicates_or_ambiguous_canonical_files_fail(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            artifacts(root)
            alias = root / "entropy-service_windows_amd64.zip"
            alias.write_bytes(b"duplicate")
            with self.assertRaisesRegex(ValueError, "duplicate files"):
                check_dist(root)
            alias.unlink()
            (root / "entropy-service_older_windows_amd64.zip").write_bytes(b"old archive")
            with self.assertRaisesRegex(ValueError, "one canonical archive"):
                check_dist(root)


if __name__ == "__main__":
    unittest.main()
