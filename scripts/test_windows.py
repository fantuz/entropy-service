#!/usr/bin/env python3
"""Native Windows smoke tests; no QRNG, external server or trust-store changes."""

import argparse
import ctypes
from ctypes import wintypes
import http.client
import json
from pathlib import Path
import re
import signal
import socket
import ssl
import subprocess
import sys
import tempfile
import time
import unittest


ROOT = Path(__file__).resolve().parents[1]
SERVER = ROOT / "bin/entropy-server.exe"
CLIENT = ROOT / "bin/entropy-client.exe"


def invoke(binary, *args, code=0, contains=b""):
    result = subprocess.run(
        [str(binary), *map(str, args)], capture_output=True, timeout=20, check=False
    )
    assert result.returncode == code, (result.returncode, result.stdout, result.stderr)
    assert contains in result.stdout + result.stderr, (contains, result.stdout, result.stderr)
    return result


def request(port, path, context=None):
    connection = (
        http.client.HTTPSConnection("127.0.0.1", port, context=context, timeout=2)
        if context else http.client.HTTPConnection("127.0.0.1", port, timeout=2)
    )
    try:
        connection.request("GET", path)
        response = connection.getresponse()
        return response.status, response.read()
    finally:
        connection.close()


@unittest.skipUnless(sys.platform == "win32", "native Windows executables required")
class WindowsSmoke(unittest.TestCase):
    def test_hardware_discovery_and_os_control_probe(self):
        inventory = json.loads(invoke(SERVER, "hardware", "list", "-json").stdout)
        self.assertEqual(inventory["platform"], "windows")
        self.assertIsInstance(inventory["devices"], list)
        probe = json.loads(invoke(SERVER, "hardware", "probe", "-device=windows-csprng", "-json").stdout)
        self.assertTrue(probe["read_ok"])
        self.assertEqual(probe["bytes_accepted"], 64)
        self.assertEqual(probe["actual_source"], "windows-csprng")
        self.assertFalse(probe["quality_assessed"])
        self.assertFalse(probe["cancellation_tested"])
        invoke(SERVER, "hardware", "probe", "-bytes=0", code=2)
        missing = json.loads(invoke(SERVER, "hardware", "probe", "-device=quantis-pci:10", "-json", code=1).stdout)
        self.assertFalse(missing["read_ok"])
        self.assertEqual(missing["bytes_accepted"], 0)
        self.assertNotIn("actual_source", missing)
        for source in ["quantis-usb:127", "quantis-usb:-1"]:
            missing = json.loads(invoke(SERVER, "hardware", "probe", f"-device={source}", "-json", code=1).stdout)
            self.assertFalse(missing["read_ok"])
            self.assertNotIn("actual_source", missing)
            result = invoke(SERVER, f"-device={source}", "-cert-file=missing.pem", code=1,
                            contains=b"use -device=quantis-usb:0")
            self.assertNotIn(b"load TLS certificate", result.stderr)

    def test_chaoskey_selection_is_explicit_and_strict(self):
        for source in ["chaoskey-usb:127", "chaoskey-usb:-1", "chaoskey:0"]:
            missing = json.loads(invoke(SERVER, "hardware", "probe", f"-device={source}", "-json", code=1).stdout)
            self.assertFalse(missing["read_ok"])
            self.assertNotIn("actual_source", missing)
            self.assertFalse(missing["health_available"])
            result = invoke(SERVER, f"-device={source}", "-cert-file=missing.pem", code=1,
                            contains=b"use -device=chaoskey-usb:0")
            self.assertNotIn(b"load TLS certificate", result.stderr)

    @classmethod
    def setUpClass(cls):
        # Hosted runners can start without a console. Attach children to a private
        # test console so CTRL_BREAK exercises Go's actual console signal handler.
        cls.kernel = ctypes.WinDLL("kernel32", use_last_error=True)
        cls.kernel.GetConsoleProcessList.argtypes = [ctypes.POINTER(wintypes.DWORD), wintypes.DWORD]
        processes = (wintypes.DWORD * 1)()
        cls.allocated_console = not cls.kernel.GetConsoleProcessList(processes, 1)
        if cls.allocated_console and not cls.kernel.AllocConsole():
            raise ctypes.WinError(ctypes.get_last_error())

    @classmethod
    def tearDownClass(cls):
        if cls.allocated_console:
            cls.kernel.FreeConsole()

    def test_unsupported_options_fail_before_io(self):
        for option in ["-device=/dev/qrandom0", "-require-device"]:
            result = invoke(SERVER, option, "-cert-file=missing.pem", code=1, contains=b"Windows")
            self.assertNotIn(b"load TLS certificate", result.stderr)
            self.assertNotIn(b"Source opened", result.stderr)

        for source in ["quantis-pci", "quantis-pci:10", "quantis-pci:-1"]:
            result = invoke(SERVER, f"-device={source}", "-cert-file=missing.pem",
                            code=1, contains=b"use -device=quantis-pci:0")
            self.assertNotIn(b"load TLS certificate", result.stderr)
            self.assertNotIn(b"Source opened", result.stderr)

        for option in [
            "-writeout", "-output-device=/dev/null", "-output=-", "-waterfall",
            "-layout=full", "-layout=waterfall", "-waterfall-columns=32",
            "-waterfall-mode=rgb", "-color=truecolor",
        ]:
            result = invoke(CLIENT, "-headless", option, "-url=http://127.0.0.1:1", code=1, contains=b"Windows")
            self.assertNotIn(b"connection refused", result.stderr.lower())

    def test_missing_tls_startup_guidance(self):
        with tempfile.TemporaryDirectory(prefix="entropy-missing-tls-") as directory:
            result = invoke(SERVER, f"-cert-file={Path(directory) / 'missing cert.pem'}",
                            f"-key-file={Path(directory) / 'missing key.pem'}", code=1,
                            contains=b"load TLS certificate")
            for text in [b"generate-cert", b"-enable-https=false -http=127.0.0.1:8080",
                         b"docs/windows.md#files-logs-and-tls", b"http://127.0.0.1:8080/"]:
                self.assertIn(text, result.stderr)

    def check_jsonl_stdout(self, port, work):
        base = [str(CLIENT), "-headless", "-mode=http", "-slice=256", "-refresh=1ms",
                "-analysis-bytes=64", "-analysis-interval=1ms", "-report=-",
                f"-url=http://127.0.0.1:{port}/v1/data/random"]
        complete = invoke(CLIENT, *base[1:], "-duration=150ms")
        summary = json.loads(complete.stdout.splitlines()[-1])
        self.assertEqual(summary["termination"], "duration")
        self.assertFalse(summary["failed"])
        for stop in ("duration", "interrupt", "broken"):
            with (work / f"report-{stop}.log").open("wb") as log:
                process = subprocess.Popen(base + ["-duration=800ms" if stop == "duration" else "-duration=30s"],
                                           stdout=subprocess.PIPE, stderr=log,
                                           creationflags=subprocess.CREATE_NEW_PROCESS_GROUP)
                try:
                    if stop == "interrupt":
                        time.sleep(.5)  # Many records fill the unread pipe before the console signal.
                        process.send_signal(signal.CTRL_BREAK_EVENT)
                    elif stop == "broken":
                        process.stdout.close()
                    process.wait(timeout=5)  # Wait without draining stdout: regression must not hang.
                    self.assertNotEqual(process.returncode, 0, (work / f"report-{stop}.log").read_bytes())
                    self.assertIn(b"write report", (work / f"report-{stop}.log").read_bytes())
                finally:
                    if process.poll() is None:
                        process.kill()
                        process.wait()
                    if process.stdout and not process.stdout.closed:
                        process.stdout.close()

    def test_real_http_https_ws_files_and_shutdown(self):
        with tempfile.TemporaryDirectory(prefix="entropy-windows-") as directory:
            work = Path(directory)
            cert, key = work / "cert.pem", work / "key.pem"
            provisioning = ["generate-cert", "-hosts=localhost,127.0.0.1", "-valid-for=1h",
                            f"-cert-file={cert}", f"-key-file={key}"]
            invoke(SERVER, *provisioning, contains=b"Created self-signed")
            before = key.read_bytes()
            invoke(SERVER, *provisioning, "-if-missing", contains=b"Reusing valid")
            self.assertEqual(before, key.read_bytes())

            log_path = work / "server.log"
            with log_path.open("wb") as log:
                process = subprocess.Popen(
                    [str(SERVER), "-http=127.0.0.1:0", "-https=127.0.0.1:0",
                     f"-cert-file={cert}", f"-key-file={key}", "-reseed-ms=100"],
                    stdout=subprocess.PIPE, stderr=log,
                    creationflags=subprocess.CREATE_NEW_PROCESS_GROUP,
                )
                ports = {}
                try:
                    deadline = time.monotonic() + 15
                    while len(ports) != 2:
                        text = log_path.read_bytes()
                        for protocol in ("HTTP", "HTTPS"):
                            match = re.search(protocol.encode() + rb" listener\s+\[OK\] 127\.0\.0\.1:(\d+)", text)
                            if match:
                                ports[protocol] = int(match.group(1))
                        if process.poll() is not None or time.monotonic() >= deadline:
                            self.fail(f"Windows server failed to start: {text[-12000:]!r}")
                        time.sleep(.02)

                    self.assertIn(b"Windows OS CSPRNG (crypto/rand)", text)
                    self.assertIn(b"Go socket defaults", text)
                    self.assertNotIn(b"configured device unavailable", text)
                    self.assertEqual(request(ports["HTTP"], "/ready")[0], 200)
                    self.assertEqual(request(ports["HTTP"], "/api-stream.html")[0], 200)
                    context = ssl.create_default_context(cafile=str(cert))
                    context.minimum_version = ssl.TLSVersion.TLSv1_3
                    status, data = request(ports["HTTPS"], "/v1/data/random?bytes=64", context)
                    self.assertEqual((status, len(data)), (200, 64))

                    for mode, canonical in [("pull", "pull"), ("http", "pull"), ("stream", "stream"),
                                            ("push", "stream"), ("websocket", "stream")]:
                        raw, report = work / f"{mode}.bin", work / f"{mode}.jsonl"
                        invoke(CLIENT, "-headless", f"-mode={mode}", "-slice=256", "-refresh=20ms",
                               "-duration=5s", "-analysis-bytes=64", f"-output={raw}", "-output-bytes=700",
                               f"-report={report}", f"-url=http://127.0.0.1:{ports['HTTP']}/v1/data/random",
                               f"-wsurl=ws://127.0.0.1:{ports['HTTP']}/stream")
                        self.assertEqual(raw.stat().st_size, 700)
                        records = [json.loads(line) for line in report.read_text(encoding="utf-8").splitlines()]
                        summary = records[-1]
                        self.assertEqual(records[0]["settings"]["mode"], canonical)
                        self.assertEqual(summary["termination"], "output_limit")
                        self.assertEqual(summary["output_written_bytes"], 700)
                        self.assertFalse(summary["failed"])
                        self.assertNotIn("\x1b", report.read_text(encoding="utf-8"))

                    self.check_jsonl_stdout(ports["HTTP"], work)

                    plain = invoke(CLIENT, "-mode=http", "-shannon-threshold=7.123456",
                                   "-duration=120ms", "-display-interval=50ms", "-slice=256",
                                   "-refresh=20ms", f"-url=http://127.0.0.1:{ports['HTTP']}/v1/data/random")
                    self.assertIn(b"Windows capabilities", plain.stdout)
                    self.assertIn(b"EntropyCTL Monitor", plain.stdout)
                    self.assertRegex(plain.stdout, rb"Shannon entropy    : [0-9]+\.[0-9]{6} bits/byte")
                    self.assertIn(b"Shannon threshold  : 7.123456 bits/byte", plain.stdout)
                    self.assertNotIn(b"\x1b", plain.stdout)
                    self.assertEqual(request(ports["HTTP"], "/ready")[0], 200)

                    process.send_signal(signal.CTRL_BREAK_EVENT)
                    stdout, _ = process.communicate(timeout=10)
                    self.assertEqual(process.returncode, 0, log_path.read_bytes())
                    self.assertFalse(stdout)
                    self.assertIn(b"shutdown complete", log_path.read_bytes())
                    for port in ports.values():
                        with socket.socket() as probe:
                            probe.settimeout(.5)
                            self.assertNotEqual(probe.connect_ex(("127.0.0.1", port)), 0)
                finally:
                    if process.poll() is None:
                        process.kill()
                        process.communicate()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bin-dir", type=Path, default=ROOT / "bin", help="directory containing the two executables")
    args, unittest_args = parser.parse_known_args()
    SERVER, CLIENT = args.bin_dir / "entropy-server.exe", args.bin_dir / "entropy-client.exe"
    unittest.main(argv=[sys.argv[0], *unittest_args], verbosity=2)
