"""Check runtime isolation for local and split-runner installer qualification."""
from pathlib import Path
import tempfile
import unittest

from test_windows_gui_installer import unavailable_build_runtime


class InstallerRuntimeTests(unittest.TestCase):
    def test_absent_runtime_is_valid_only_on_a_clean_runner(self):
        with tempfile.TemporaryDirectory() as directory:
            prefix = Path(directory).resolve() / "ucrt64"
            with unavailable_build_runtime(prefix, clean_runner=True):
                self.assertFalse(prefix.exists())
            with self.assertRaises(ValueError):
                with unavailable_build_runtime(prefix, clean_runner=False):
                    self.fail("An absent original runtime cannot be hidden")
            with self.assertRaises(ValueError):
                with unavailable_build_runtime(prefix.with_name("other"), clean_runner=True):
                    self.fail("A malformed build prefix cannot prove isolation")

    def test_runner_runtime_stays_unavailable_and_is_restored_after_failure(self):
        with tempfile.TemporaryDirectory() as directory:
            prefix = Path(directory).resolve() / "ucrt64"
            prefix.mkdir()
            # Stock runners can have a UCRT directory without the build's Python.
            with self.assertRaisesRegex(RuntimeError, "qualification"):
                with unavailable_build_runtime(prefix, clean_runner=True):
                    self.assertFalse(prefix.exists())
                    raise RuntimeError("qualification")
            self.assertTrue(prefix.is_dir())
            (prefix / "bin").mkdir()
            (prefix / "bin/python.exe").touch()
            with unavailable_build_runtime(prefix, clean_runner=False):
                self.assertFalse(prefix.exists())
            self.assertTrue((prefix / "bin/python.exe").exists())


if __name__ == "__main__":
    unittest.main()
