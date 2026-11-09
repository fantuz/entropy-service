import hashlib
import json
from pathlib import Path
import tempfile
import unittest

from capture_randomness import capture
from assess_practrand import assess


class Connection:
    def __init__(self, blocks):
        self.blocks = iter(blocks)

    def __enter__(self):
        return self

    def __exit__(self, *args):
        pass

    def recv(self, timeout):
        return next(self.blocks)


class CaptureTests(unittest.TestCase):
    def test_exact_decoded_bytes_and_exclusive_files(self):
        with tempfile.TemporaryDirectory() as folder:
            output = Path(folder) / "sample.bin"
            factory = lambda *a, **k: Connection([b"abcdefgh", b"ijklmnop"])
            result = capture("ws://name:secret@localhost/stream?token=secret", output,
                             13, frame_bytes=8, connect_fn=factory)
            self.assertEqual(result["status"], "complete")
            self.assertEqual(output.read_bytes(), b"abcdefghijklm")
            self.assertEqual(result["sha256"], hashlib.sha256(output.read_bytes()).hexdigest())
            self.assertNotIn("secret", json.dumps(result))
            self.assertEqual(json.loads(Path(str(output) + ".json").read_text()), result)
            with self.assertRaises(FileExistsError):
                capture("ws://localhost/stream", output, 13, connect_fn=factory)
            self.assertEqual(output.read_bytes(), b"abcdefghijklm")

    def test_eof_preserves_incomplete_prefix(self):
        with tempfile.TemporaryDirectory() as folder:
            output = Path(folder) / "sample.bin"
            result = capture("ws://localhost/stream", output, 16, frame_bytes=8,
                             connect_fn=lambda *a, **k: Connection([b"abcdefgh"]))
            self.assertEqual(result["status"], "incomplete")
            self.assertEqual(result["bytes"], 8)
            self.assertEqual(output.read_bytes(), b"abcdefgh")

    def test_text_is_not_random_binary(self):
        with tempfile.TemporaryDirectory() as folder:
            output = Path(folder) / "sample.bin"
            result = capture("ws://localhost/stream", output, 8,
                             connect_fn=lambda *a, **k: Connection(["not binary"]))
            self.assertEqual(result["status"], "incomplete")
            self.assertEqual(output.read_bytes(), b"")

    def test_invalid_limits_do_not_connect_or_create_files(self):
        with tempfile.TemporaryDirectory() as folder:
            for count, timeout in [(0, 1), (-1, 1), (8, float("nan")), (8, -1)]:
                with self.assertRaises(ValueError):
                    capture("ws://localhost/stream", Path(folder) / "sample.bin",
                            count, timeout=timeout,
                            connect_fn=lambda *a, **k: self.fail("unexpected connection"))
            self.assertEqual(list(Path(folder).iterdir()), [])


class AssessmentTests(unittest.TestCase):
    def test_completion_and_flags_are_separate(self):
        manifest = {"status": "complete", "bytes": 65 << 20, "requested_bytes": 65 << 20}
        report = "length= 64 megabytes (2^26 bytes), time= 1.0 seconds\nGap p=1e-3 unusual\n"
        result = assess(manifest, report, 64 << 20)
        self.assertTrue(result["completed"])
        self.assertTrue(result["review_required"])
        self.assertEqual(len(result["flags"]), 1)

    def test_early_stop_is_incomplete_even_without_flags(self):
        manifest = {"status": "complete", "bytes": 65 << 20, "requested_bytes": 65 << 20}
        result = assess(manifest, "length= 32 megabytes (2^25 bytes)\nno anomalies", 64 << 20)
        self.assertFalse(result["completed"])
        manifest["status"] = "incomplete"
        self.assertFalse(assess(manifest, "length=64 megabytes (2^26 bytes)", 64 << 20)["completed"])


if __name__ == "__main__":
    unittest.main()
