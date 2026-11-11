#!/usr/bin/env python3
"""Exercise actual CLI startup/exit paths using local sockets and ephemeral TLS."""

import http.client
import json
import os
import re
from pathlib import Path
import socket
import ssl
import subprocess
import tempfile
import time
import unittest


ROOT = Path(__file__).resolve().parents[1]
SERVER = str(ROOT / "bin/entropy-server")
CLIENT = str(ROOT / "bin/entropy-client")


def invoke(binary, *args, code=1, contains=b""):
    result = subprocess.run([binary, *map(str, args)], capture_output=True, timeout=5, check=False)
    assert result.returncode == code, (result.returncode, result.stderr[-8192:])
    assert not result.stdout, "CLI diagnostic/help output contaminated stdout"
    assert contains in result.stderr, result.stderr[-8192:]
    assert b"PRIVATE KEY" not in result.stderr
    return result


def listener():
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    sock.listen()
    return sock


class StartupTests(unittest.TestCase):
    def test_hardware_subcommands_run_without_server_side_effects(self):
        with tempfile.TemporaryDirectory(prefix="entropy-hardware-cli-") as directory:
            def hardware(*args, code=0):
                result = subprocess.run([SERVER, "hardware", *args], cwd=directory,
                                        capture_output=True, timeout=5, check=False)
                self.assertEqual(result.returncode, code, (result.stdout, result.stderr))
                self.assertNotIn(b"PRIVATE KEY", result.stdout + result.stderr)
                self.assertNotIn(b"Root seed", result.stderr)
                return result

            inventory = json.loads(hardware("list", "-json").stdout)
            self.assertEqual(inventory["platform"], "linux")
            self.assertIsInstance(inventory["devices"], list)
            probe = json.loads(hardware("probe", "-device=/dev/urandom", "-json").stdout)
            self.assertTrue(probe["read_ok"])
            self.assertEqual(probe["actual_source"], "/dev/urandom")
            self.assertEqual(probe["bytes_accepted"], 64)
            self.assertFalse(probe["quality_assessed"])
            self.assertFalse(probe["cancellation_tested"])
            failed = json.loads(hardware("probe", "-device=/definitely-missing-qrng", "-json", code=1).stdout)
            self.assertFalse(failed["read_ok"])
            self.assertNotIn("actual_source", failed)
            usb_failed = json.loads(hardware("probe", "-device=quantis-usb:127", "-json", code=1).stdout)
            self.assertFalse(usb_failed["read_ok"])
            self.assertNotIn("actual_source", usb_failed)
            hardware("probe", "-device=/dev/urandom", "-bytes=0", code=2)
            self.assertEqual(list(Path(directory).iterdir()), [])

    @classmethod
    def setUpClass(cls):
        cls.temp = tempfile.TemporaryDirectory(prefix="entropy-startup-")
        cls.addClassCleanup(cls.temp.cleanup)
        cls.directory = Path(cls.temp.name)
        cls.cert, cls.key = cls.directory / "cert.pem", cls.directory / "key.pem"
        cls.other_cert, cls.other_key = cls.directory / "other.pem", cls.directory / "other-key.pem"
        for cert, key in [(cls.cert, cls.key), (cls.other_cert, cls.other_key)]:
            invoke(SERVER, "generate-cert", f"-cert-file={cert}", f"-key-file={key}", code=0, contains=b"Created self-signed")

    def server_flags(self, secure=False):
        return ["-http=127.0.0.1:0", "-device=/dev/urandom", "-require-device=true",
                f"-enable-https={'true' if secure else 'false'}",
                f"-cert-file={self.cert}", f"-key-file={self.key}"]

    def test_help_and_parse_failures(self):
        for binary in (SERVER, CLIENT):
            invoke(binary, "-help", code=0, contains=b"Usage:")
            invoke(binary, "-unknown", code=2, contains=b"flag provided but not defined")
            invoke(binary, "-duration=forever" if binary == CLIENT else "-reseed-ms=1s", code=2, contains=b"invalid value")
            invoke(binary, "unexpected", code=2, contains=b"unexpected positional")
        output = self.directory / "must-not-be-created.bin"
        invoke(CLIENT, "-headless", "false", f"-output={output}", code=2, contains=b"-flag=false")
        self.assertFalse(output.exists(), "invalid arguments opened an output destination")

    def test_occupied_http_and_https_ports(self):
        with listener() as sock:
            port = sock.getsockname()[1]
            invoke(SERVER, *self.server_flags(), f"-http=127.0.0.1:{port}", contains=b"address already in use")
            invoke(SERVER, *self.server_flags(True), f"-https=127.0.0.1:{port}", contains=b"address already in use")

    def test_tls_failure_precedes_entropy_access(self):
        missing = self.directory / "missing.pem"
        for key, cert in [(missing, self.cert), (self.key, missing), (self.other_key, self.cert)]:
            result = invoke(SERVER, *self.server_flags(True), "-device=/missing-required-entropy-device",
                            f"-cert-file={cert}", f"-key-file={key}", contains=b"load TLS certificate")
            self.assertNotIn(b"Source opened", result.stderr)

    @unittest.skipIf(os.geteuid() == 0, "permission-denial test requires an unprivileged user")
    def test_unreadable_private_key(self):
        self.key.chmod(0)
        try:
            invoke(SERVER, *self.server_flags(True), contains=b"permission denied")
        finally:
            self.key.chmod(0o600)

    def test_real_http_https_and_sigterm(self):
        for secure in (False, True):
            with self.subTest(https=secure):
                label = b"HTTPS" if secure else b"HTTP"
                address = f"-{'https' if secure else 'http'}=127.0.0.1:0"
                log_path = self.directory / f"startup-{secure}.log"
                with log_path.open("wb") as log:
                    process = subprocess.Popen([SERVER, *self.server_flags(secure), address], stdout=subprocess.PIPE, stderr=log)
                    try:
                        context = ssl.create_default_context(cafile=str(self.cert))
                        context.minimum_version = ssl.TLSVersion.TLSv1_3
                        deadline = time.monotonic() + 5
                        while True:
                            text = log_path.read_bytes()
                            bound = re.search(label + rb" listener\s+\[OK\] 127\.0\.0\.1:(\d+)\n", text)
                            if bound:
                                port = int(bound.group(1))
                                connection = (http.client.HTTPSConnection("127.0.0.1", port, timeout=.3, context=context)
                                              if secure else http.client.HTTPConnection("127.0.0.1", port, timeout=.3))
                                try:
                                    connection.request("GET", "/ready")
                                    response = connection.getresponse()
                                    response.read(8192)
                                    if response.status == 200:
                                        break
                                except OSError:
                                    pass
                                finally:
                                    connection.close()
                            if process.poll() is not None or time.monotonic() >= deadline:
                                self.fail(f"server did not become ready: {text[-8192:]!r}")
                            time.sleep(.02)
                        connection = (http.client.HTTPSConnection("127.0.0.1", port, timeout=1, context=context)
                                      if secure else http.client.HTTPConnection("127.0.0.1", port, timeout=1))
                        try:
                            connection.request("GET", "/v1/data/random?bytes=64")
                            response = connection.getresponse()
                            self.assertEqual(response.status, 200)
                            self.assertEqual(len(response.read(65)), 64)
                        finally:
                            connection.close()
                        process.terminate()
                        stdout, _ = process.communicate(timeout=3)
                        self.assertEqual(process.returncode, 0)
                        self.assertFalse(stdout)
                        with socket.socket() as probe:
                            probe.settimeout(.3)
                            self.assertNotEqual(probe.connect_ex(("127.0.0.1", port)), 0)
                    finally:
                        if process.poll() is None:
                            process.kill()
                            process.communicate()


if __name__ == "__main__":
    unittest.main()
