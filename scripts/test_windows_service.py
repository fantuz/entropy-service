#!/usr/bin/env python3
"""Real SCM tests of an isolated, uniquely named service; no firewall/trust edits."""

import argparse
import ctypes
import http.client
import os
from pathlib import Path
import shutil
import ssl
import subprocess
import sys
import unittest
import uuid


SERVER = Path(__file__).resolve().parents[1] / "bin/entropy-server.exe"


def free_port():
    import socket
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


@unittest.skipUnless(sys.platform == "win32", "native Windows SCM required")
class WindowsServiceTests(unittest.TestCase):
    def setUp(self):
        if not ctypes.windll.shell32.IsUserAnAdmin():
            self.skipTest("an elevated test process is required")
        self.name = "EntropyTest" + uuid.uuid4().hex
        self.installation = Path(os.environ["ProgramFiles"]) / ("Entropy Service CI " + self.name)
        self.state = Path(os.environ["ProgramData"]) / ("Entropy Service CI " + self.name)
        self.installation.mkdir()
        self.binary = self.installation / "entropy-server.exe"
        shutil.copy2(SERVER, self.binary)
        self.addCleanup(self.cleanup_service)

    def command(self, operation, *args, code=0):
        result = subprocess.run([str(self.binary), "service", operation, "-name=" + self.name, *map(str, args)],
                                capture_output=True, timeout=45, check=False)
        self.assertEqual(result.returncode, code, (result.stdout, result.stderr))
        return result.stdout + result.stderr

    def install(self, *args, code=0):
        return self.command("install", "-state-dir=" + str(self.state), *args, code=code)

    def cleanup_service(self):
        for operation in ("stop", "uninstall"):
            subprocess.run([str(self.binary), "service", operation, "-name=" + self.name],
                           capture_output=True, timeout=45, check=False)
        if self.state.exists():
            # Only after removing this test's own service, reclaim its isolated
            # private key for fixture deletion. Never touch a production path.
            subprocess.run(["takeown.exe", "/F", str(self.state), "/R", "/A", "/D", "Y"],
                           capture_output=True, timeout=20, check=False)
            subprocess.run(["icacls.exe", str(self.state), "/grant", "*S-1-5-32-544:F", "/T", "/C"],
                           capture_output=True, timeout=20, check=False)
            shutil.rmtree(self.state)
        shutil.rmtree(self.installation)

    def test_tls_restart_stop_and_preserving_uninstall(self):
        import winreg
        http_port, tls_port = free_port(), free_port()
        self.install("-self-signed", "--", f"-http=127.0.0.1:{http_port}", f"-https=127.0.0.1:{tls_port}")
        with winreg.OpenKey(winreg.HKEY_LOCAL_MACHINE, "SYSTEM\\CurrentControlSet\\Services\\" + self.name) as key:
            self.assertEqual(winreg.QueryValueEx(key, "Start")[0], 2)
            self.assertEqual(winreg.QueryValueEx(key, "DelayedAutoStart")[0], 1)
            self.assertEqual(winreg.QueryValueEx(key, "ObjectName")[0].lower(), ("NT SERVICE\\" + self.name).lower())
            self.assertTrue(winreg.QueryValueEx(key, "ImagePath")[0].startswith('"' + str(self.binary) + '"'))
        self.assertFalse((self.state / "key.pem").exists())
        self.command("run", code=1)  # A console must not impersonate SCM execution.
        self.command("start")
        cert = (self.state / "cert.pem").read_bytes()
        context = ssl.create_default_context(cafile=str(self.state / "cert.pem"))
        for connection in [http.client.HTTPConnection("127.0.0.1", http_port, timeout=3),
                           http.client.HTTPSConnection("127.0.0.1", tls_port, timeout=3, context=context)]:
            try:
                connection.request("GET", "/ready")
                response = connection.getresponse()
                self.assertEqual(response.status, 200, response.read())
            finally:
                connection.close()
        with self.assertRaises(PermissionError):
            (self.state / "key.pem").read_bytes()
        self.assertIn(b"State: running", self.command("status"))
        self.command("uninstall", code=1)  # Running installations are preserved.
        self.command("stop")
        self.assertIn("shutdown complete", "\n".join(p.read_text() for p in self.state.glob("server-*.log")))
        self.command("start")
        self.assertEqual((self.state / "cert.pem").read_bytes(), cert)
        self.command("stop")
        self.command("uninstall")
        self.assertEqual((self.state / "cert.pem").read_bytes(), cert)
        # Reinstallation reuses the same protected state and service SID.
        self.install("-self-signed", "--", f"-http=127.0.0.1:{http_port}", f"-https=127.0.0.1:{tls_port}")
        self.command("start")
        self.command("stop")

    def test_missing_cert_fails_startup(self):
        self.install("--", "-http=127.0.0.1:0", "-https=127.0.0.1:0")
        self.command("start", code=1)
        self.assertIn(b"State: stopped", self.command("status"))
        logs = "\n".join(p.read_text() for p in self.state.glob("server-*.log"))
        self.assertIn("generate-cert", logs)
        self.assertNotIn("ready: startup completed", logs)

    def test_existing_unsafe_directory_is_not_repaired(self):
        self.state.mkdir()
        sentinel = self.state / "operator.txt"
        sentinel.write_text("retain me")
        before = subprocess.check_output(["icacls.exe", str(self.state)])
        self.install("--", "-enable-https=false", code=1)
        self.assertEqual(subprocess.check_output(["icacls.exe", str(self.state)]), before)
        self.assertEqual(sentinel.read_text(), "retain me")
        self.command("status", code=1)  # Failed install rolled back the SCM entry.


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--server", type=Path, default=SERVER)
    parser.add_argument("--require-admin", action="store_true")
    options, test_args = parser.parse_known_args()
    SERVER = options.server.resolve()
    if options.require_admin and (sys.platform != "win32" or not ctypes.windll.shell32.IsUserAnAdmin()):
        raise SystemExit("Native elevated Windows required; SCM coverage must not silently skip in CI")
    unittest.main(argv=[sys.argv[0], *test_args])
