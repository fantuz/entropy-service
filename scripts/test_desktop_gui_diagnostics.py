"""Preserve useful client failure reasons without leaking partial log URLs."""
from pathlib import Path
import sys
import tempfile
import time
import unittest
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "tools/desktop-gui"))
from entropy_gui.model import client_failure_text
from entropy_gui.process import ClientSession, MAX_STDERR_BYTES, spawn_owned


def wait_for(predicate):
    deadline = time.monotonic() + 5
    while not predicate():
        if time.monotonic() >= deadline:
            raise AssertionError("Native client fixture did not reach its expected state")
        time.sleep(.01)


class DiagnosticTests(unittest.TestCase):
    def test_reported_causes_have_specific_actions_without_changing_the_reason(self):
        for diagnostic, expected in (
            ('server returned 503: "RDRAND backend stopped after hardware failure; restart required"', "operator restart"),
            ("HTTP entropy request failed: deadline exceeded", "request timed out"),
            ("read entropy: unexpected EOF", "server remains healthy"),
            ('server returned 400: "bytes must be between 1 and 4096"', "configured maximum"),
            ('server returned 503: "generation paused"', "readiness"),
        ):
            with self.subTest(diagnostic=diagnostic):
                text = client_failure_text("entropy-client exited with status 1", diagnostic)
                self.assertIn(diagnostic, text)
                self.assertIn(expected, text)
        self.assertTrue(client_failure_text("Invalid JSON report", "normal startup line").startswith("Invalid JSON report"))
        text = client_failure_text("transport error", "failed https://operator:secret@example.org/path?token=private#fragment")
        self.assertNotIn("secret", text)
        self.assertNotIn("private", text)

    def session(self, code):
        session = ClientSession("fixture-selected-through-native-spawn", [])
        with mock.patch("entropy_gui.process._spawn_utility", side_effect=lambda *_args:
                        spawn_owned([sys.executable, "-u", "-c", code])):
            session.start()
        self.addCleanup(session.stop)
        return session

    def test_truncated_native_stderr_drops_a_url_fragment_but_preserves_the_failure(self):
        code = ("import os\n"
                "os.write(2,b'https://operator:secret@example.org/?padding='+b'x'*10000+"
                "b'&token=DO-NOT-DISPLAY\\nRDRAND backend stopped after hardware failure; restart required\\n')\n"
                "raise SystemExit(1)\n")
        session = self.session(code)
        wait_for(lambda: not session.snapshot()["running"])
        state = session.snapshot()
        self.assertEqual(state["returncode"], 1)
        self.assertIn("RDRAND backend stopped", state["stderr"])
        self.assertNotIn("DO-NOT-DISPLAY", state["stderr"])
        self.assertLessEqual(len(state["stderr"]), MAX_STDERR_BYTES)
        self.assertFalse(state["forced_stop"])

    def test_native_stderr_waits_for_a_complete_credential_bearing_line(self):
        with tempfile.TemporaryDirectory() as temporary:
            ready, release = Path(temporary) / "ready", Path(temporary) / "release"
            code = ("import os,time\nfrom pathlib import Path\n"
                    "os.write(2,b'https://operator:')\n"
                    f"Path({str(ready)!r}).touch()\n"
                    f"while not Path({str(release)!r}).exists(): time.sleep(.01)\n"
                    "os.write(2,b'secret@example.org/path?token=private\\nnetwork error: unexpected EOF\\n')\n"
                    "raise SystemExit(1)\n")
            session = self.session(code)
            try:
                wait_for(ready.exists)
                self.assertEqual(session.snapshot()["stderr"], "")
            finally:
                release.touch()
            wait_for(lambda: not session.snapshot()["running"])
            state = session.snapshot()
            self.assertIn("https://example.org/path", state["stderr"])
            self.assertIn("unexpected EOF", state["stderr"])
            self.assertNotIn("secret", state["stderr"])
            self.assertNotIn("private", state["stderr"])


if __name__ == "__main__":
    unittest.main()
