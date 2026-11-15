#!/usr/bin/env python3
"""GTK-independent tests for bounded reports and owned-process cancellation."""
import base64
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "tools/desktop-gui"))
from entropy_gui.model import MAX_RECORD_BYTES, ReportError, StreamModel, configuration, decode_record, safe_text
from entropy_gui.process import ClientSession, MAX_STDERR_BYTES, describe, spawn_owned, stop_owned, _signal


def start_record():
    return {"schema_version": 1, "type": "run_start", "settings": {
        "endpoint": "https://user:password@example.org/data?token=secret#fragment",
        "mode": "pull", "shannon_threshold_bits_per_byte": 7.999}}


def sample_record(prefix=b"\x00\xff"):
    return {"schema_version": 1, "type": "sample", "elapsed_ms": 1000.0,
            "received_bytes": 1048576, "sample_bytes": max(256, len(prefix)),
            "shannon_bits_per_byte": 7.999123, "shannon_assessment": "OK",
            "waterfall_prefix_base64": base64.b64encode(prefix).decode()}


def summary_record():
    return {"schema_version": 1, "type": "run_summary", "elapsed_ms": 1000.0,
            "received_bytes": 1048576, "received_mib_per_second": 1.0,
            "last_shannon_assessment": "OK", "failed": False}


def encode(value):
    return json.dumps(value).encode() + b"\n"


def wait_until(check, timeout=4):
    deadline = time.monotonic() + timeout
    while not check():
        if time.monotonic() > deadline:
            raise AssertionError("Child process did not reach the expected state")
        time.sleep(.01)


class ModelTests(unittest.TestCase):
    def test_history_and_snapshots_are_bounded_and_independent(self):
        model = StreamModel(history_limit=2)
        model.feed_line(encode(start_record()))
        for value in range(10):
            model.feed_line(encode(sample_record(bytes([value]) * 256)))
        state = model.snapshot()
        self.assertEqual(state["rows"], (b"\x09" * 256, b"\x08" * 256))
        self.assertEqual(state["settings"]["endpoint"], "https://example.org/data")
        self.assertNotIn("waterfall_prefix_base64", state["sample"])
        state["sample"]["received_bytes"] = 0
        self.assertEqual(model.snapshot()["sample"]["received_bytes"], 1048576)
        revision = model.snapshot()["revision"]
        model.reset()
        self.assertEqual(model.snapshot()["rows"], ())
        self.assertGreater(model.snapshot()["revision"], revision)

    def test_ordinary_reports_without_raw_prefix_still_work(self):
        model = StreamModel()
        value = sample_record()
        del value["waterfall_prefix_base64"]
        model.feed_line(encode(value))
        model.feed_line(encode(summary_record()))
        self.assertFalse(model.snapshot()["rows"])
        self.assertEqual(model.snapshot()["sample"]["shannon_bits_per_byte"], 7.999123)
        self.assertEqual(model.snapshot()["summary"]["received_mib_per_second"], 1)

    def test_actual_client_lifecycle_includes_measurement_start(self):
        model = StreamModel()
        for value in (start_record(), {"schema_version": 1, "type": "measurement_start"},
                      sample_record(), summary_record()):
            model.feed_line(encode(value))
        self.assertEqual(model.snapshot()["rows"], (b"\x00\xff",))
        self.assertFalse(model.snapshot()["summary"]["failed"])

    def test_prefix_must_be_valid_bounded_and_within_sample(self):
        for prefix in ("?", "A" * 345, base64.b64encode(bytes(257)).decode()):
            with self.subTest(prefix=prefix[:12]), self.assertRaises(ReportError):
                StreamModel().feed_line(encode({**sample_record(), "waterfall_prefix_base64": prefix}))
        with self.assertRaises(ReportError):
            StreamModel().feed_line(encode({**sample_record(), "sample_bytes": 1}))

    def test_rejects_nonfinite_corrupt_unsupported_and_oversized_reports(self):
        for data in (b'{"schema_version":1,"type":"sample","x":NaN}',
                     b'{"schema_version":1,"schema_version":2}',
                     b'{"schema_version":true}', b'[]', b'{', b'\xff',
                     b' ' * (MAX_RECORD_BYTES + 1)):
            with self.subTest(data=data[:50]), self.assertRaises(ReportError):
                decode_record(data)
        with self.assertRaises(ReportError):
            StreamModel().feed_line(b" " * (MAX_RECORD_BYTES + 1))
        for changed in ({"shannon_bits_per_byte": float("inf")}, {"shannon_bits_per_byte": 8.01},
                        {"received_bytes": -1}, {"received_bytes": True},
                        {"elapsed_ms": 10 ** 400}, {"shannon_assessment": "UNVERIFIED"}, {"type": "future"}):
            with self.subTest(changed=changed), self.assertRaises(ReportError):
                StreamModel().feed_line(encode({**sample_record(), **changed}))

    def test_configuration_is_explicitly_not_runtime_health(self):
        value = {"schema_version": 1, "type": "configuration", "role": "server", "live": False,
                 "settings": {"drbg": "chacha20"}, "features": {"https": True}}
        self.assertFalse(configuration(encode(value), "server")["live"])
        for changed in ({"live": True}, {"role": "client"}, {"features": {"https": "yes"}}):
            with self.subTest(changed=changed), self.assertRaises(ReportError):
                configuration(encode({**value, **changed}), "server")

    def test_controls_and_credentials_are_not_displayed(self):
        text = safe_text("\x1b[31merror\x1b[0m\x00 https://user:password@example.org/path?token=secret#fragment\n")
        self.assertEqual(text, "error https://example.org/path\n")
        for scheme in ("HTTPS", "HtTp", "WSS", "Ws"):
            text = safe_text(f"failure {scheme}://operator:password@example.org/path?token=secret#fragment")
            self.assertEqual(text, f"failure {scheme.lower()}://example.org/path")
        for limit in (0, 129, True):
            with self.subTest(limit=limit), self.assertRaises(ValueError):
                StreamModel(history_limit=limit)


@unittest.skipUnless(sys.platform.startswith("linux"), "POSIX executable-script fixtures require Linux")
class ProcessTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="entropy-gui-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)

    def binary(self, body):
        path = self.root / ("cli-" + str(len(list(self.root.glob("cli-*")))))
        path.write_text("#!" + sys.executable + "\n" + body)
        path.chmod(0o700)
        return path

    def test_describe_is_dry_run_and_never_uses_a_shell(self):
        marker = self.root / "shell-side-effect"
        literal = "$(touch " + str(marker) + ")"
        binary = self.binary("import sys,json\nassert sys.argv[1]=='-describe-json=true'\n"
                             "print(json.dumps({'schema_version':1,'type':'configuration','role':'server','live':False,"
                             "'settings':{'argument':sys.argv[2]},'features':{'https':True}}))\n")
        value = describe(binary, [literal], "server")
        self.assertEqual(value["settings"]["argument"], literal)
        self.assertFalse(marker.exists())

    def test_rejects_dry_run_overrides_and_hidden_output_before_spawn(self):
        marker = self.root / "ran"
        binary = self.binary("from pathlib import Path\nPath(" + repr(str(marker)) + ").touch()\n")
        for option in ("-describe-json=false", "--describe-json=false", "--", "-h", "--help"):
            with self.subTest(option=option), self.assertRaises(ValueError):
                describe(binary, [option], "server")
        for option in ("-output=bytes.bin", "--output-device=/dev/urandom", "-writeout=false",
                       "-report=stats.jsonl", "-headless=false", "-debug", "-output-bytes=1"):
            with self.subTest(option=option), self.assertRaises(ValueError):
                ClientSession(binary, [option])
        self.assertFalse(marker.exists())

    def test_inspection_timeout_and_output_bound_reap_owned_child(self):
        pid_path = self.root / "pid"
        binary = self.binary("import os,time\nfrom pathlib import Path\nPath(" + repr(str(pid_path)) + ").write_text(str(os.getpid()))\ntime.sleep(30)\n")
        began = time.monotonic()
        with self.assertRaisesRegex(RuntimeError, "timed out"):
            describe(binary, [], "server")
        self.assertLess(time.monotonic() - began, 4)
        with self.assertRaises(ProcessLookupError):
            os.kill(int(pid_path.read_text()), 0)
        binary = self.binary("import os,time\nos.write(1,b'x'*70000)\ntime.sleep(30)\n")
        with self.assertRaisesRegex(RuntimeError, "64 KiB"):
            describe(binary, [], "server")

    def test_session_drains_both_pipes_and_only_keeps_bounded_state(self):
        records = [start_record()] + [sample_record(bytes([index])) for index in range(200)] + [summary_record()]
        data = b"".join(map(encode, records))
        binary = self.binary("import os,sys\nassert sys.argv[-3:]==['-headless=true','-report=-','-report-waterfall=true']\n"
                             "os.write(2,b'e'*1000000)\nos.write(1," + repr(data) + ")\n")
        session = ClientSession(binary, [], StreamModel(history_limit=3))
        self.addCleanup(session.stop)
        session.start()
        wait_until(lambda: bool(session.snapshot()["summary"]))
        session.stop()
        state = session.snapshot()
        self.assertEqual(state["returncode"], 0)
        self.assertFalse(state["error"])
        self.assertLessEqual(len(state["stderr"]), MAX_STDERR_BYTES)
        self.assertEqual(state["rows"], (bytes([199]), bytes([198]), bytes([197])))

    def test_truncated_or_invalid_reports_stop_acquisition(self):
        for data, message in ((b'{"schema_version":1', "Truncated"), (b'bad json\n', "Invalid JSON"),
                              (b'x' * (MAX_RECORD_BYTES + 1), "64 KiB")):
            binary = self.binary("import os\nos.write(1," + repr(data) + ")\n")
            session = ClientSession(binary, [])
            self.addCleanup(session.stop)
            session.start()
            wait_until(lambda: bool(session.snapshot()["error"]))
            session.stop()
            self.assertIn(message, session.snapshot()["error"])
            self.assertFalse(session.snapshot()["running"])

    def test_cancel_stubborn_busy_child_does_not_touch_other_process(self):
        ready = self.root / "ready"
        binary = self.binary("import os,signal\nfrom pathlib import Path\nsignal.signal(signal.SIGTERM,signal.SIG_IGN)\n"
                             "Path(" + repr(str(ready)) + ").touch()\ndata=" + repr(encode(sample_record())) + "\n"
                             "while True:\n os.write(1,data*100)\n os.write(2,b'busy'*1000)\n")
        unrelated = subprocess.Popen([sys.executable, "-c", "import time;time.sleep(30)"])
        self.addCleanup(lambda: (unrelated.terminate(), unrelated.wait(timeout=2)))
        session = ClientSession(binary, [])
        self.addCleanup(session.stop)
        session.start()
        wait_until(ready.exists)
        began = time.monotonic()
        session.stop()
        self.assertLess(time.monotonic() - began, 2.1)
        self.assertFalse(session.snapshot()["running"])
        self.assertTrue(session.snapshot()["forced_stop"])
        self.assertIn("forced termination", session.snapshot()["error"])
        self.assertFalse(session._thread.is_alive())
        self.assertIsNone(unrelated.poll())
        self.assertLessEqual(len(session.snapshot()["rows"]), 128)

    def test_inherited_busy_pipe_does_not_keep_pump_alive_after_client_exit(self):
        leaf = "import os\ndata=" + repr(encode(sample_record())) + "\nwhile True:\n os.write(1,data*100)\n"
        binary = self.binary("import subprocess,sys\nsubprocess.Popen([sys.executable,'-c'," + repr(leaf) + "],stderr=subprocess.DEVNULL)\n")
        session = ClientSession(binary, [])
        self.addCleanup(session.stop)
        session.start()
        wait_until(lambda: session.snapshot()["returncode"] is not None)
        wait_until(lambda: not session._thread.is_alive(), timeout=1)
        self.assertFalse(session.snapshot()["running"])

    def test_restart_resets_model_only_after_previous_pump_has_finished(self):
        body = ("import json,os,time\nrecord=" + repr(sample_record()) + "\n"
                "record['received_bytes']=os.getpid()\n"
                "while True:\n print(json.dumps(record),flush=True);time.sleep(.01)\n")
        session = ClientSession(self.binary(body), [])
        self.addCleanup(session.stop)
        session.start()
        first = session._child.pid
        wait_until(lambda: session.snapshot()["sample"].get("received_bytes") == first)
        with self.assertRaisesRegex(RuntimeError, "still stopping"):
            session.start()
        session.stop()
        self.assertFalse(session._thread.is_alive())
        self.assertFalse(session.snapshot()["running"])
        session.start()
        second = session._child.pid
        self.assertNotEqual(first, second)
        wait_until(lambda: session.snapshot()["sample"].get("received_bytes") == second)
        self.assertFalse(session.snapshot()["forced_stop"])
        self.assertFalse(session.snapshot()["error"])


class SpawnTests(unittest.TestCase):
    def test_explicit_file_outputs_are_supported_for_server_fixtures(self):
        with tempfile.TemporaryFile() as output:
            child = spawn_owned([sys.executable, "-c", "print('fixture output')"], stdout=output, stderr=output)
            self.assertEqual(child.wait(timeout=4), 0)
            output.seek(0)
            self.assertIn(b"fixture output", output.read())

    def test_windows_command_scripts_are_rejected_before_spawn(self):
        with mock.patch("entropy_gui.process.sys.platform", "win32"):
            for binary in ("helper.cmd", "helper.BAT", "helper.cmd ", "helper.BAT."):
                with self.subTest(binary=binary), self.assertRaisesRegex(ValueError, "native executable"):
                    spawn_owned([binary, "&echo unwanted"])

    def test_windows_natural_exit_racing_console_signal_is_not_forced(self):
        child = mock.Mock(pid=42)
        child.poll.return_value = 0
        kernel = mock.Mock()
        kernel.GenerateConsoleCtrlEvent.return_value = False
        with mock.patch("entropy_gui.process.sys.platform", "win32"), \
                mock.patch("entropy_gui.process._windows_api", return_value=kernel):
            self.assertTrue(_signal(child))
        kernel.GenerateConsoleCtrlEvent.assert_called_once_with(1, 42)
        child.terminate.assert_not_called()
        child.poll.return_value = None
        child.wait.return_value = 0
        with mock.patch("entropy_gui.process._signal", return_value=False):
            self.assertFalse(stop_owned(child))
        child.wait.side_effect = [subprocess.TimeoutExpired("owned-cli", 1.5), 0]
        with mock.patch("entropy_gui.process._signal", return_value=True):
            self.assertFalse(stop_owned(child))


@unittest.skipUnless(sys.platform == "win32", "Native Windows pipe and console adapter required")
class WindowsProcessTests(unittest.TestCase):
    def python_session(self, body, model=None):
        # -c executes this controlled test program before the GUI-owned flags;
        # unlike .cmd wrappers, it does not introduce shell interpretation.
        session = ClientSession(sys.executable, ["-c", body], model)
        self.addCleanup(session.stop)
        session.start()
        return session

    def test_both_pipe_drain_and_buffer_bounds(self):
        records = [start_record()] + [sample_record(bytes([index])) for index in range(200)] + [summary_record()]
        body = "import os\nos.write(2,b'e'*1000000)\nos.write(1," + repr(b"".join(map(encode, records))) + ")\n"
        session = self.python_session(body, StreamModel(history_limit=3))
        wait_until(lambda: not session.snapshot()["running"], timeout=8)
        state = session.snapshot()
        self.assertEqual(state["returncode"], 0)
        self.assertFalse(state["error"])
        self.assertLessEqual(len(state["stderr"]), MAX_STDERR_BYTES)
        self.assertEqual(state["rows"], (bytes([199]), bytes([198]), bytes([197])))

    def test_targeted_console_interrupt_keeps_summary_and_other_process_alive(self):
        with tempfile.TemporaryDirectory() as directory:
            ready = Path(directory) / "ready"
            body = ("import json,signal,time\nfrom pathlib import Path\n"
                    "def stopped(*_):\n print(" + repr(json.dumps(summary_record())) + ",flush=True);raise SystemExit(0)\n"
                    "signal.signal(signal.SIGBREAK,stopped)\n"
                    "Path(" + repr(str(ready)) + ").touch()\n"
                    "while True:time.sleep(.01)\n")
            unrelated = spawn_owned([sys.executable, "-c", "import time;time.sleep(30)"],
                                    stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            self.addCleanup(lambda: (unrelated.kill(), unrelated.wait(timeout=3)))
            session = self.python_session(body)
            wait_until(ready.exists)
            session.stop()
            state = session.snapshot()
            self.assertFalse(state["running"])
            self.assertFalse(state["forced_stop"])
            self.assertFalse(state["error"])
            self.assertFalse(state["summary"]["failed"])
            self.assertIsNone(unrelated.poll())

    def test_stubborn_busy_pipe_child_is_forced_and_reaped(self):
        with tempfile.TemporaryDirectory() as directory:
            ready = Path(directory) / "ready"
            body = ("import os,signal\nfrom pathlib import Path\n"
                    "signal.signal(signal.SIGBREAK,signal.SIG_IGN)\n"
                    "Path(" + repr(str(ready)) + ").touch()\n"
                    "data=" + repr(encode(sample_record())) + "\n"
                    "while True:\n os.write(1,data*100);os.write(2,b'busy'*1000)\n")
            session = self.python_session(body)
            wait_until(ready.exists)
            began = time.monotonic()
            session.stop()
            self.assertLess(time.monotonic() - began, 3)
            state = session.snapshot()
            self.assertFalse(state["running"])
            self.assertTrue(state["forced_stop"])
            self.assertIn("forced termination", state["error"])
            self.assertFalse(session._thread.is_alive())


if __name__ == "__main__":
    unittest.main()
