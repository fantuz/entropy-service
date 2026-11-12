#!/usr/bin/env python3
"""Exercise binary CLI exports with a local, predictable HTTP fixture (Linux)."""

import contextlib
import http.server
import json
import os
from pathlib import Path
import pty
import signal
import subprocess
import tempfile
import threading
import time
import urllib.parse


class Fixture(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        count = int(urllib.parse.parse_qs(urllib.parse.urlsplit(self.path).query)["bytes"][0])
        self.server.requests += 1
        if self.server.retry_failures:
            self.server.retry_failures -= 1
            self.send_response(503)
            self.send_header("Retry-After", "0")
            self.end_headers()
            self.wfile.write(b"seed paused\x1b[2J\n")
            return
        data = (bytes(range(256)) * ((count + 255) // 256))[:count]
        self.send_response(200)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        with contextlib.suppress(BrokenPipeError, ConnectionResetError):
            self.wfile.write(data)

    def log_message(self, *_args):
        pass


def main():
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Fixture)
    server.requests = 0
    server.retry_failures = 0
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        run(server)
    finally:
        server.shutdown()
        server.server_close()
        thread.join()


def run(server):
    binary = str(Path("bin/entropy-client").resolve())
    base = [binary, "-headless", f"-url=http://127.0.0.1:{server.server_port}/bytes",
            "-slice=65536", "-refresh=5ms", "-analysis-bytes=256", "-analysis-interval=1h", "-duration=5s"]
    limit = 131089
    expected = (bytes(range(256)) * ((limit + 255) // 256))[:limit]
    with tempfile.TemporaryDirectory(prefix="entropy-output-") as directory:
        folder = Path(directory)
        report = folder / "capture.jsonl"
        result = subprocess.run(base + ["-output=-", f"-output-bytes={limit}", f"-report={report}",
                                       "-layout=full", "-color=truecolor", "-waterfall", "-preview", "-debug"], capture_output=True, timeout=10)
        assert result.returncode == 0 and result.stdout == expected, result.stderr
        summary = json.loads(report.read_text().splitlines()[-1])
        assert summary["termination"] == "output_limit" and not summary["failed"], summary
        assert summary["output_written_bytes"] == limit and summary["output_written_frames"] == 2, summary
        assert summary["analysed_samples"] == 1 and summary["received_frames"] == 3, summary

        server.retry_failures = 2
        retry_report = folder / "retry.jsonl"
        result = subprocess.run(base + ["-retries=2", "-retry-delay=1ms", "-output=-", "-output-bytes=700",
                                       f"-report={retry_report}"], capture_output=True, timeout=10)
        assert result.returncode == 0 and result.stdout == expected[:700], result.stderr
        assert b"transport retry 1/2" in result.stderr and b"transport retry 2/2" in result.stderr
        assert b"\x1b" not in result.stderr, "remote error controls reached the terminal"
        retry_summary = json.loads(retry_report.read_text().splitlines()[-1])
        assert retry_summary["output_written_bytes"] == 700 and retry_summary["termination"] == "output_limit"

        output = folder / "capture.bin"
        result = subprocess.run(base + [f"-output={output}", "-output-bytes=700", "-report=-"], capture_output=True, timeout=10)
        assert result.returncode == 0 and output.read_bytes() == expected[:700], result.stderr
        assert output.stat().st_mode & 0o077 == 0
        assert json.loads(result.stdout.splitlines()[-1])["output_written_bytes"] == 700

        before = server.requests
        for args in (["-output=-", "-report=-"], [f"-output={output}"], ["-output-device=/dev/zero"],
                     ["-writeout", f"-output={folder / 'conflict'}"]):
            result = subprocess.run(base + args, capture_output=True, timeout=5)
            assert result.returncode != 0 and result.stdout == b"", (args, result)
        assert before == server.requests, "invalid output performed network requests"
        assert output.read_bytes() == expected[:700], "failed open replaced an existing file"

        result = subprocess.run(base + ["-output-device=/dev/null", "-output-bytes=700", "-report=-"], capture_output=True, timeout=5)
        assert result.returncode == 0, result.stderr
        summary = json.loads(result.stdout.splitlines()[-1])
        assert summary["device_written_bytes"] == summary["output_written_bytes"] == 700

        master, slave = pty.openpty()
        try:
            result = subprocess.run(base + ["-output=-"], stdout=slave, stderr=subprocess.PIPE, timeout=5)
            assert result.returncode != 0 and b"refusing raw binary output to a terminal" in result.stderr
        finally:
            os.close(master)
            os.close(slave)

        # Leave the pipe unread: duration cancellation must unblock the writer.
        blocked_report = folder / "blocked.jsonl"
        process = subprocess.Popen(base + ["-output=-", "-duration=500ms", f"-report={blocked_report}"], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        try:
            process.wait(timeout=3)
            captured, error = process.communicate(timeout=1)
            assert process.returncode == 0, error
            summary = json.loads(blocked_report.read_text().splitlines()[-1])
            assert summary["termination"] == "duration" and summary["output_write_errors"] == 0, summary
            assert summary["output_written_bytes"] == len(captured) < summary["received_bytes"], summary
        finally:
            if process.poll() is None:
                process.kill()
                process.wait()

        # JSONL stdout also uses cancellable writes, including its final summary.
        report_base = [str(Path("bin/entropy-client").resolve()), "-headless", "-mode=http", "-slice=256",
                       "-refresh=1ms", "-analysis-bytes=64", "-analysis-interval=1ms", "-report=-",
                       f"-url=http://127.0.0.1:{server.server_port}/v1/data/random"]
        for stop in ("duration", "interrupt", "broken"):
            process = subprocess.Popen(report_base + ["-duration=500ms" if stop == "duration" else "-duration=30s"],
                                       stdout=subprocess.PIPE, stderr=subprocess.PIPE, pipesize=4096)
            try:
                if stop == "interrupt":
                    time.sleep(.25)
                    process.send_signal(signal.SIGINT)
                elif stop == "broken":
                    process.stdout.close()
                process.wait(timeout=3)  # Do not drain the pipe until the writer has exited.
                assert process.returncode != 0, "incomplete JSONL report was reported as successful"
                assert b"write report" in process.stderr.read()
            finally:
                if process.poll() is None:
                    process.kill()
                    process.wait()
                process.stderr.close()
                if not process.stdout.closed:
                    process.stdout.close()

        broken_report = folder / "broken.jsonl"
        process = subprocess.Popen(base + ["-output=-", f"-report={broken_report}"], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        process.stdout.close()
        try:
            process.wait(timeout=3)
            assert process.returncode != 0, "broken pipe was ignored"
            summary = json.loads(broken_report.read_text().splitlines()[-1])
            assert summary["termination"] == "writeout_error" and summary["failed"] and summary["output_write_errors"] == 1, summary
        finally:
            process.stderr.close()
            if process.poll() is None:
                process.send_signal(signal.SIGKILL)
                process.wait()
    print("Binary exports: exact limits, raw stdout, private files, devices, terminal refusal, blocked/broken pipes passed")


if __name__ == "__main__":
    main()
