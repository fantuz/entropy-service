#!/usr/bin/env python3
"""Native-process tests for the GUI's owned server and bounded logs."""
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "tools/desktop-gui"))
from entropy_gui.model import safe_text
from entropy_gui.process import _close_pipes, spawn_owned, stop_owned
from entropy_gui.server_process import MAX_SERVER_LOG_BYTES, ServerSession


def wait_until(predicate, timeout=5):
    deadline = time.monotonic() + timeout
    while not predicate():
        if time.monotonic() >= deadline:
            raise AssertionError("Owned server did not reach its expected state")
        time.sleep(.01)


GRACEFUL = """import signal, sys, time
def finish(*_args):
    print('CLEAN STOP', flush=True)
    raise SystemExit(0)
signal.signal(signal.SIGTERM, finish)
signal.signal(signal.SIGINT, finish)
if sys.platform == 'win32':
    signal.signal(signal.SIGBREAK, finish)
print('READY', flush=True)
while True:
    time.sleep(.02)
"""


class ServerSessionTests(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory(prefix="entropy gui server ")
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)
        self.fixtures = {}
        patch = mock.patch("entropy_gui.server_process._spawn_utility", side_effect=self.spawn_fixture)
        self.spawn = patch.start()
        self.addCleanup(patch.stop)

    def session(self, body, args=None):
        name = "server " + str(len(self.fixtures))
        binary = self.root / (name + ".exe")
        script = self.root / (name + ".py")
        script.write_text(body)
        self.fixtures[str(binary)] = script
        session = ServerSession(binary, args if args is not None else ["-http=127.0.0.1:0"])
        self.addCleanup(session.stop)
        return session

    def spawn_fixture(self, binary, args, role):
        self.assertEqual(role, "server")
        return spawn_owned([sys.executable, str(self.fixtures[str(binary)]), *args])

    def test_start_rejects_subcommands_and_gui_managed_options_without_spawn(self):
        for args in (["generate-cert", "-cert-file=unexpected.pem"], ["hardware", "list"],
                     ["version"], ["-"], [""], ["--"], ["--help"], ["-h=false"],
                     ["-describe-json=false"], ["-http=127.0.0.1:0", "--describe-json=true"]):
            with self.subTest(args=args), self.assertRaises(ValueError):
                ServerSession(self.root / "server.exe", args)
        self.spawn.assert_not_called()
        self.assertEqual(ServerSession("server", ["-http", "127.0.0.1:0", "-device=/dev/urandom"]).args,
                         ["-http", "127.0.0.1:0", "-device=/dev/urandom"])

    def test_logs_are_bounded_drained_and_redacted(self):
        body = ("import os\n"
                "os.write(1,b'x\\n'*100000)\n"
                "os.write(2,b'\\x1b[31mhttps://operator:password@example.org/data?token=secret#fragment\\x1b[0m\\n')\n"
                "os.write(1,b'FINAL WITHOUT NEWLINE')\n")
        session = self.session(body, ["-http", "127.0.0.1:0", "-device=source with spaces"])
        session.start()
        wait_until(lambda: not session.snapshot()["running"])
        state = session.snapshot()
        self.assertEqual(len(session._logs), MAX_SERVER_LOG_BYTES)
        self.assertLessEqual(len(state["logs"]), MAX_SERVER_LOG_BYTES)
        self.assertTrue("https://example.org/data" in state["logs"], "Final stderr message was not retained")
        self.assertTrue("FINAL WITHOUT NEWLINE" in state["logs"], "Final stdout message was not retained")
        self.assertNotIn("password", state["logs"])
        self.assertNotIn("secret", state["logs"])
        self.assertNotIn("operator", state["logs"])
        self.assertNotIn("\x1b", state["logs"])
        self.assertEqual(state["stderr"], state["logs"])
        self.assertEqual(state["returncode"], 0)
        self.assertFalse(state["error"] or state["forced_stop"])
        self.spawn.assert_called_once_with(session.binary, session.args, "server")

    def test_partial_url_log_is_not_exposed_before_complete_line(self):
        body = ("import os,time\n"
                "os.write(1,b'FIRST\\nhttps://operator:password')\n"
                "time.sleep(.3)\n"
                "os.write(1,b'@example.org/data?token=secret\\nFINAL\\n')\n")
        session = self.session(body)
        session.start()
        wait_until(lambda: b"password" in session._logs)
        during = session.snapshot()
        self.assertEqual(during["logs"], "FIRST\n")
        wait_until(lambda: not session.snapshot()["running"])
        final = session.snapshot()["logs"]
        self.assertIn("https://example.org/data", final)
        self.assertNotIn("password", final)
        self.assertNotIn("secret", final)

    def test_unchanged_log_view_is_reused_but_final_partial_line_is_revealed(self):
        closing, release = threading.Event(), threading.Event()

        def hold_final_close(child):
            # The process has exited, but its pump is still completing its
            # final drain. This transition emits no additional log bytes.
            closing.set()
            release.wait(timeout=4)
            _close_pipes(child)

        session = self.session("import os\nos.write(1,b'READY\\nFINAL WITHOUT NEWLINE')\n")
        with mock.patch("entropy_gui.server_process._close_pipes", side_effect=hold_final_close), \
                mock.patch("entropy_gui.server_process.safe_text", wraps=safe_text) as render:
            session.start()
            try:
                self.assertTrue(closing.wait(timeout=4), "Server pump did not reach final cleanup")
                first = session.snapshot()
                self.assertTrue(first["running"])
                self.assertEqual(first["logs"], "READY\n")
                for _ in range(20):
                    self.assertEqual(session.snapshot()["logs"], first["logs"])
                self.assertEqual(render.call_count, 1, "An idle GUI tick re-sanitized unchanged logs")
            finally:
                release.set()
            session._thread.join(timeout=2)
            self.assertFalse(session.snapshot()["running"])
            self.assertEqual(session.snapshot()["logs"], "READY\nFINAL WITHOUT NEWLINE")
            self.assertEqual(render.call_count, 2, "Final EOF did not invalidate the cached log view")

    def test_stop_drains_owned_process_restart_preserves_unrelated_process(self):
        unrelated_script = self.root / "unrelated.py"
        unrelated_script.write_text(GRACEFUL)
        with tempfile.TemporaryFile() as output:
            unrelated = spawn_owned([sys.executable, str(unrelated_script)], stdout=output, stderr=subprocess.STDOUT)
            self.addCleanup(stop_owned, unrelated)
            session = self.session(GRACEFUL)
            for _ in range(2):
                session.start()
                wait_until(lambda: "READY" in session.snapshot()["logs"])
                child = session._child
                with self.assertRaises(RuntimeError):
                    session.start()
                session.stop()
                state = session.snapshot()
                self.assertIsNotNone(child.poll())
                self.assertFalse(state["running"])
                self.assertEqual(state["returncode"], 0)
                self.assertIn("CLEAN STOP", state["logs"])
                self.assertFalse(state["forced_stop"] or state["error"])
                self.assertIsNone(unrelated.poll())
            session.stop()
            self.assertIsNone(unrelated.poll())
            self.assertEqual(self.spawn.call_count, 2)

    def test_failed_process_can_restart_with_fresh_logs_and_outcome(self):
        session = self.session("import sys\nprint('bind failed',file=sys.stderr,flush=True)\nsys.exit(7)\n")
        session.start()
        wait_until(lambda: not session.snapshot()["running"])
        first = session.snapshot()
        self.assertIn("bind failed", first["logs"])
        self.assertIn("status 7", first["error"])
        self.assertFalse(first["forced_stop"])
        self.fixtures[session.binary].write_text("print('second run',flush=True)\n")
        session.start()
        wait_until(lambda: not session.snapshot()["running"])
        second = session.snapshot()
        self.assertEqual(second["logs"], "second run\n")
        self.assertEqual(second["returncode"], 0)
        self.assertFalse(second["error"] or second["forced_stop"])
        self.assertGreater(second["revision"], first["revision"])

    def test_unresponsive_process_is_reaped_and_forced_stop_is_honest(self):
        body = """import signal,sys,time
signal.signal(signal.SIGTERM,signal.SIG_IGN)
signal.signal(signal.SIGINT,signal.SIG_IGN)
if sys.platform=='win32':
    signal.signal(signal.SIGBREAK,signal.SIG_IGN)
print('READY',flush=True)
while True:
    time.sleep(.02)
"""
        session = self.session(body)
        session.start()
        wait_until(lambda: "READY" in session.snapshot()["logs"])
        began = time.monotonic()
        session.stop()
        self.assertLess(time.monotonic() - began, 3)
        state = session.snapshot()
        self.assertFalse(state["running"])
        self.assertIsNotNone(state["returncode"])
        self.assertTrue(state["forced_stop"])
        self.assertIn("forced termination", state["error"])


if __name__ == "__main__":
    unittest.main()
