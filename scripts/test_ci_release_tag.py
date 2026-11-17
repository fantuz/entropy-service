import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("ci_release_tag.py").resolve()


class ReleaseTagTests(unittest.TestCase):
    def test_tag_version_and_ref_type_must_match(self):
        with tempfile.TemporaryDirectory() as directory:
            Path(directory, "VERSION").write_text("2.4.1\n")
            for tag, ref_type, expected in (("v2.4.1", "tag", 0), ("v2.4.0", "tag", 1),
                                             ("v2.4.1", "branch", 1), ("main", "branch", 1), ("", "branch", 0)):
                result = subprocess.run([sys.executable, str(SCRIPT)], cwd=directory,
                                        env=dict(os.environ, RELEASE_TAG=tag, GITHUB_REF_TYPE=ref_type),
                                        capture_output=True, text=True)
                self.assertEqual(expected, result.returncode, result.stdout + result.stderr)
