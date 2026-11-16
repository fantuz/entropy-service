"""Check installed launcher defaults and forwarding without requiring GTK."""
import importlib.util
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch


SPEC = importlib.util.spec_from_file_location("bundle_bootstrap", Path(__file__).resolve().parents[1] / "tools/windows-installer/gui_bootstrap.py")
BOOTSTRAP = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(BOOTSTRAP)


class BootstrapTests(unittest.TestCase):
    def invocation(self, arguments):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            captured = []
            with patch.object(BOOTSTRAP, "__file__", str(root / "app/gui_bootstrap.py")), \
                    patch.object(BOOTSTRAP, "configure_runtime"), \
                    patch.dict(os.environ, {"LOCALAPPDATA": str(root / "profile")}), \
                    patch.object(sys, "argv", ["gui_bootstrap.py", *arguments]), \
                    patch.object(BOOTSTRAP.runpy, "run_path", side_effect=lambda *a, **kw: captured.extend(sys.argv)):
                self.assertEqual(BOOTSTRAP.main(), 0)
            return root, captured

    def test_server_defaults_are_loopback_http(self):
        root, arguments = self.invocation(["--view", "server"])
        self.assertEqual(arguments[-3:], ["--", "-http=127.0.0.1:8080", "-enable-https=false"])
        self.assertEqual(arguments[arguments.index("--server-binary") + 1], str(root / "bin/entropy-server.exe"))
        self.assertEqual(arguments[arguments.index("--client-binary") + 1], str(root / "bin/entropy-client.exe"))

    def test_client_accepts_both_view_flag_spellings(self):
        for arguments in (["--view", "client"], ["--view=client"]):
            _, actual = self.invocation(arguments)
            self.assertNotIn("-enable-https=false", actual)
            self.assertNotIn("--", actual)

    def test_explicit_startup_settings_are_preserved(self):
        requested = ["--view", "server", "--", "-http=127.0.0.1:8090", "-https=127.0.0.1:8444", "-enable-https=true"]
        _, actual = self.invocation(requested)
        self.assertEqual(actual[-len(requested):], requested)
        self.assertNotIn("-enable-https=false", actual)


if __name__ == "__main__":
    unittest.main()
