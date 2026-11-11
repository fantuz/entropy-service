#!/usr/bin/env python3
"""Verify waterfall-only geometry and live resizing in real Linux PTYs."""

import http.server
import json
import os
from pathlib import Path
import subprocess
import threading

from test_client_terminal import ANSI, Fixture, capture_pty, monitor_screens


def check_screen(screen, columns, rows):
    plain = ANSI.sub("", screen).removesuffix("\n")
    lines = plain.split("\n")
    assert len(lines) == rows - 1, (columns, rows, len(lines))
    assert all(len(line) == columns for line in lines), (columns, rows)
    assert all(value in lines[0] for value in ("MiB/s", "8.00000", "7.99900", "OK")), lines[0]
    assert not any(label in plain for label in ("Sample diagnostics", "Offsets", "Waterfall |", "Latest sample", "History"))


def main():
    server = http.server.HTTPServer(("127.0.0.1", 0), Fixture)
    server.frames = 0
    worker = threading.Thread(target=server.serve_forever, daemon=True)
    worker.start()
    environment = dict(os.environ, TERM="xterm-256color", COLORTERM="truecolor", NO_COLOR="")
    base = [str(Path("bin/entropy-client").resolve()), "-mode=pull", "-layout=waterfall",
            f"-url=http://127.0.0.1:{server.server_port}/data", "-slice=4096", "-refresh=5ms",
            "-analysis-interval=0", "-display-interval=50ms"]
    try:
        for columns, rows, mode, color, escape in [(80, 24, "byte", "truecolor", "\x1b[48;2;"),
                                                  (641, 37, "rgb", "truecolor", "\x1b[48;2;"),
                                                  (100, 240, "gray", "256", "\x1b[48;5;"),
                                                  (900, 180, "byte", "never", None)]:
            output = capture_pty(base + [f"-waterfall-mode={mode}", f"-color={color}", "-duration=350ms"],
                                 environment, columns, rows)
            screens = monitor_screens(output)
            assert screens, output
            for screen in screens:
                check_screen(screen, columns, rows)
                if escape:
                    assert escape in screen
            print(f"PASS: waterfall {columns}x{rows}, mode={mode}, color={color}")

        output = capture_pty(base + ["-color=truecolor", "-duration=1200ms"], environment, 80, 24,
                             resize=[(3.35, 641, 37), (3.65, 100, 240), (3.9, 80, 24)])
        screens = monitor_screens(output)
        sizes = {80: 24, 641: 37, 100: 240}
        seen = set()
        for screen in screens:
            columns = len(ANSI.sub("", screen).split("\n")[0])
            check_screen(screen, columns, sizes[columns])
            seen.add(columns)
        assert seen == set(sizes), seen
        assert len(ANSI.sub("", screens[-1]).split("\n")[0]) == 80
        print("PASS: live waterfall resize 80x24 -> 641x37 -> 100x240 -> 80x24")

        result = subprocess.run(base + ["-headless", "-report=-", "-duration=100ms"],
                                env=environment, capture_output=True, text=True, timeout=3, check=True)
        assert "\x1b" not in result.stdout
        records = [json.loads(line) for line in result.stdout.splitlines()]
        assert records[0]["settings"]["layout"] == "waterfall"
        assert records[0]["settings"]["waterfall"] is True
        assert records[-1]["type"] == "run_summary" and not records[-1]["failed"]
        print("PASS: waterfall request remains JSONL-only in headless mode")
    finally:
        server.shutdown()
        server.server_close()
        worker.join()


if __name__ == "__main__":
    main()
