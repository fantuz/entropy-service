#!/usr/bin/env python3
"""Check 20-FPS terminal redraws with a local fixture and Linux PTYs."""

import http.server
import os
from pathlib import Path
import threading

from test_client_terminal import Fixture, capture_pty, monitor_screens


def main():
    server = http.server.HTTPServer(("127.0.0.1", 0), Fixture)
    server.frames = 0
    worker = threading.Thread(target=server.serve_forever, daemon=True)
    worker.start()
    client = str(Path("bin/entropy-client").resolve())
    environment = dict(os.environ, TERM="xterm-256color", NO_COLOR="1")
    base = [client, "-mode=pull", f"-url=http://127.0.0.1:{server.server_port}/data",
            "-slice=4096", "-refresh=10ms", "-analysis-interval=50ms", "-duration=1200ms", "-color=never"]
    try:
        for layout, columns, rows, interval in [("compact", 80, 24, "1ms"), ("full", 160, 45, "50ms")]:
            output = capture_pty(base + [f"-layout={layout}", f"-display-interval={interval}"], environment, columns, rows)
            screens = monitor_screens(output)
            # Wide headroom for scheduling jitter, while detecting the old
            # 10-Hz full-layout cap and an uncapped compact redraw loop.
            assert 16 <= len(screens) <= 25, (layout, len(screens))
            if layout == "full":
                assert "terminal: <=20 Hz" in output
            print(f"PASS: {layout} layout, requested {interval}, {len(screens)} redraws / 1.2s")
    finally:
        server.shutdown()
        server.server_close()
        worker.join()


if __name__ == "__main__":
    main()
