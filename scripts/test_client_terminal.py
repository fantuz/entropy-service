#!/usr/bin/env python3
"""Smoke-test the built client in pipes and Linux PTYs using synthetic bytes."""

import argparse
import contextlib
import errno
import fcntl
import http.server
import json
import os
from pathlib import Path
import pty
import re
import select
import struct
import subprocess
import termios
import threading
import time
import urllib.parse


ANSI = re.compile(r"\x1b\[[0-?]*[ -/]*[@-~]")
CLEAR = "\x1b[H\x1b[2J"


class Fixture(http.server.BaseHTTPRequestHandler):
    """A predictable byte cycle: display fixture, never random test data."""

    def do_GET(self):
        query = urllib.parse.parse_qs(urllib.parse.urlsplit(self.path).query)
        size = int(query.get("bytes", [4096])[0])
        if not 1 <= size <= 65536:
            self.send_error(400)
            return
        self.server.frames += 1
        cycle = bytes((i * 73 + self.server.frames * 17) % 256 for i in range(256))
        data = (cycle * ((size + 255) // 256))[:size]
        self.send_response(200)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        with contextlib.suppress(BrokenPipeError, ConnectionResetError):
            self.wfile.write(data)

    def log_message(self, *_args):
        pass


def capture_pty(command, environment, columns, rows, resize=()):
    master, slave = pty.openpty()
    try:
        fcntl.ioctl(master, termios.TIOCSWINSZ, struct.pack("HHHH", rows, columns, 0, 0))
        with subprocess.Popen(command, stdout=slave, stderr=subprocess.PIPE, env=environment) as process:
            os.close(slave)
            slave = None
            chunks = []
            began = time.monotonic()
            deadline = began + 12
            pending = list(resize)
            while time.monotonic() < deadline:
                if pending and time.monotonic() - began >= pending[0][0]:
                    _, new_columns, new_rows = pending.pop(0)
                    fcntl.ioctl(master, termios.TIOCSWINSZ, struct.pack("HHHH", new_rows, new_columns, 0, 0))
                if not select.select([master], [], [], 0.1)[0]:
                    continue
                try:
                    chunk = os.read(master, 65536)
                except OSError as error:
                    if error.errno != errno.EIO:
                        raise
                    break
                if not chunk:
                    break
                chunks.append(chunk)
            try:
                process.wait(timeout=1)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
                raise AssertionError("PTY client did not finish before timeout") from None
            assert process.returncode == 0, process.stderr.read().decode()
            return b"".join(chunks).decode().replace("\r", "")
    finally:
        os.close(master)
        if slave is not None:
            os.close(slave)


def monitor_screens(output):
    return [screen for screen in output.split(CLEAR) if "Received           :" in screen or "Latest sample & reception" in screen
            or screen.startswith(("SHANNON: ", "H:"))]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--client", type=Path, default=Path("bin/entropy-client"))
    parser.add_argument("--capture", type=Path, help="also save an 80x32 ANSI preview from the actual client")
    parser.add_argument("--capture-full", type=Path, help="also save a 240x60 full-layout ANSI preview")
    args = parser.parse_args()
    client = args.client.resolve()
    if not client.is_file():
        parser.error("build the client first with make build-client")

    server = http.server.HTTPServer(("127.0.0.1", 0), Fixture)
    server.frames = 0
    worker = threading.Thread(target=server.serve_forever, daemon=True)
    worker.start()
    base = [str(client), "-mode=pull", f"-url=http://127.0.0.1:{server.server_port}/data",
            "-waterfall", "-slice=4096", "-refresh=50ms", "-analysis-interval=100ms",
            "-display-interval=100ms"]
    environment = dict(os.environ, TERM="xterm-256color", COLORTERM="truecolor", NO_COLOR="1")
    try:
        for color, escape in [("auto", None), ("never", None), ("always", "\x1b[48;2;"),
                              ("256", "\x1b[48;5;"), ("truecolor", "\x1b[48;2;")]:
            result = subprocess.run(base + [f"-color={color}", "-duration=350ms"],
                                    env=environment, capture_output=True, text=True, timeout=10, check=True)
            assert "Waterfall |" in result.stdout, result.stdout
            assert CLEAR not in result.stdout and "\a" not in result.stdout
            if escape is None:
                assert "\x1b" not in result.stdout, result.stdout
            else:
                assert escape in result.stdout, result.stdout
            print(f"PASS: redirected stdout, color={color}, NO_COLOR=1")

        result = subprocess.run(base + ["-headless", "-report=-", "-color=truecolor", "-layout=full", "-duration=350ms"],
                                env=environment, capture_output=True, text=True, timeout=5, check=True)
        assert "\x1b" not in result.stdout
        records = [json.loads(line) for line in result.stdout.splitlines()]
        assert records[0]["type"] == "run_start" and records[-1]["type"] == "run_summary"
        assert records[-1]["analysed_samples"] > 0 and not records[-1]["failed"]
        print("PASS: headless forced-color waterfall emits only complete JSONL")

        environment["NO_COLOR"] = ""
        for columns in (80, 60):
            output = capture_pty(base + ["-color=auto", "-duration=450ms"], environment, columns, 24)
            screens = monitor_screens(output)
            assert screens, output
            for screen in screens:
                assert "\x1b[48;2;" in screen and "Waterfall | byte" in screen
                plain = ANSI.sub("", screen).removesuffix("\n")
                lines = plain.split("\n")
                assert sum(max(1, (len(line) + columns - 1) // columns) for line in lines) < 24, plain
                assert all(len(line) <= columns for line in lines if line.startswith("#")), plain
            print(f"PASS: {columns}x24 Linux PTY, automatic truecolor, no waterfall overflow")

        for columns, rows, color in [(120, 30, "auto"), (160, 45, "never"), (240, 60, "auto")]:
            output = capture_pty(base + [f"-color={color}", "-duration=550ms"], environment, columns, rows)
            screens = monitor_screens(output)
            assert screens, output
            for screen in screens:
                assert f"full ({columns}x{rows})" in screen
                plain = ANSI.sub("", screen).removesuffix("\n")
                lines = plain.split("\n")
                assert len(lines) < rows and all(len(line) <= columns for line in lines), plain
                if color == "never":
                    assert not re.search(r"\x1b\[[0-9;]*m", screen), screen
            print(f"PASS: full panels {columns}x{rows}, bounded geometry, color={color}")

        output = capture_pty(base + ["-duration=1500ms"], environment, 80, 24,
                             resize=[(3.35, 160, 45), (3.75, 80, 24)])
        assert "full (160x45)" in output
        screens = monitor_screens(output)
        assert "Received           :" in screens[-1] and "full (" not in screens[-1], output
        print("PASS: live PTY resize 80x24 -> 160x45 -> 80x24, compact/full/compact")

        if args.capture:
            output = capture_pty(base + ["-color=truecolor", "-duration=1500ms"], environment, 80, 32)
            screen = monitor_screens(output)[-1]
            assert screen.count("\n#") == 8, screen
            args.capture.write_text(screen)
            print(f"Captured actual 80x32 client preview: {args.capture}")

        if args.capture_full:
            output = capture_pty(base + ["-color=truecolor", "-analysis-interval=0", "-refresh=20ms", "-duration=3500ms"],
                                 environment, 240, 60)
            screen = monitor_screens(output)[-1]
            assert "full (240x60)" in screen and "Offsets [0,115)" in screen, screen
            args.capture_full.write_text(screen)
            print(f"Captured actual 240x60 client preview: {args.capture_full}")
    finally:
        server.shutdown()
        server.server_close()
        worker.join(timeout=2)


if __name__ == "__main__":
    main()
