#!/usr/bin/env python3
"""Exercise real GTK windows and the owned CLI client under a Linux display.

Run after building both utilities, for example:
  xvfb-run -a /usr/bin/python3 scripts/test_linux_gui_gtk.py
The core/protocol tests remain independent of GTK in test_linux_gui.py.
"""
import argparse
import contextlib
import http.client
import json
import os
from pathlib import Path
import re
import socket
import subprocess
import sys
import tempfile
import time


ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "tools/linux-gui"))


def free_port():
    with socket.socket() as connection:
        connection.bind(("127.0.0.1", 0))
        return connection.getsockname()[1]


def wait_for(context, predicate, description, timeout=8):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        # A bounded dispatch pass prevents a constantly-ready source from
        # hiding a timeout or starving the actual integration assertions.
        for _ in range(100):
            if not context.pending():
                break
            context.iteration(False)
        if predicate():
            return
        time.sleep(.01)
    raise AssertionError("Timed out waiting for " + description)


def assert_read_only(window):
    assert window.descriptor["live"] is False
    features = window.descriptor["features"]
    assert features and window.feature_checks.keys() == features.keys()
    for name, check in window.feature_checks.items():
        assert not check.get_sensitive(), name
        assert check.get_active() is features[name], name


def close_window(context, window):
    window.close()
    wait_for(context, lambda: not window.get_visible(), "window close and owned-child cleanup", timeout=5)
    if window.session:
        assert not window.session.snapshot()["running"]
        assert window.session._child.poll() is not None


def server_configuration(context, application_type, binary):
    port = free_port()
    missing = "/definitely-not-present/entropy-gui-device"
    app = application_type("server", str(binary), [f"-http=127.0.0.1:{port}",
                           "-enable-https=false", "-require-device=true", f"-device={missing}"])
    # The smoke check creates both roles in one process. Separate application
    # IDs keep their D-Bus object paths distinct, as separate real invocations do.
    app.set_application_id("org.entropyservice.LinuxPoC.TestServer")
    assert app.register(None)
    app.activate()
    window = app.window
    try:
        wait_for(context, lambda: window.descriptor is not None and window.get_mapped(), "server invocation description and window mapping")
        assert_read_only(window)
        assert window.descriptor["settings"]["seed_source"] == missing
        assert window.descriptor["features"]["https"] is False
        assert window.descriptor["features"]["require_device"] is True
        assert "not been checked" in window.status_label.get_text()
        assert window.session is None
        # Description succeeds despite an unreadable device, and the requested
        # address remains unbound: this window never starts a server.
        with socket.socket() as probe:
            probe.settimeout(.2)
            assert probe.connect_ex(("127.0.0.1", port)) != 0
        return {"read_only_features": len(window.feature_checks), "server_started": False,
                "missing_device_opened": False}
    finally:
        close_window(context, window)
        app.quit()


def close_during_inspection(context, application_type):
    """Closing before describe completes must not abandon an owned child."""
    with tempfile.TemporaryDirectory(prefix="entropy-gui-inspection-") as directory:
        inspector = Path(directory) / "hung-inspector"
        pid_file = inspector.with_suffix(".pid")
        inspector.write_text("#!/usr/bin/python3\nimport os,time\nfrom pathlib import Path\n"
                             "Path(__file__).with_suffix('.pid').write_text(str(os.getpid()))\n"
                             "time.sleep(30)\n")
        inspector.chmod(0o700)
        app = application_type("server", str(inspector), [])
        app.set_application_id("org.entropyservice.LinuxPoC.TestInspectionClose")
        assert app.register(None)
        app.activate()
        window = app.window
        try:
            wait_for(context, pid_file.exists, "owned configuration inspector to start")
            pid = int(pid_file.read_text())
            began = time.monotonic()
            close_window(context, window)
            window._inspection_thread.join(timeout=5)
            assert not window._inspection_thread.is_alive()
            assert time.monotonic() - began < 5
            try:
                os.kill(pid, 0)
            except ProcessLookupError:
                pass
            else:
                raise AssertionError("Closed GUI left its configuration inspector alive")
            return {"hung_inspector_reaped": True, "shutdown_seconds": round(time.monotonic() - began, 3)}
        finally:
            if window.get_visible():
                close_window(context, window)
            app.quit()


@contextlib.contextmanager
def entropy_server(binary):
    port = free_port()
    with tempfile.TemporaryFile() as log:
        child = subprocess.Popen([str(binary), f"-http=127.0.0.1:{port}",
                                  "-enable-https=false", "-device=/dev/urandom", "-require-device=true"],
                                 stdin=subprocess.DEVNULL, stdout=log, stderr=subprocess.STDOUT)
        try:
            deadline = time.monotonic() + 8
            while True:
                connection = http.client.HTTPConnection("127.0.0.1", port, timeout=.2)
                try:
                    connection.request("GET", "/live")
                    response = connection.getresponse()
                    response.read(8192)
                    if response.status == 200:
                        break
                except OSError:
                    pass
                finally:
                    connection.close()
                if child.poll() is not None or time.monotonic() >= deadline:
                    log.seek(0)
                    raise AssertionError("GUI fixture server did not start: " + log.read(8192).decode("utf-8", "replace"))
                time.sleep(.02)
            yield port
        finally:
            if child.poll() is None:
                child.terminate()
            try:
                child.wait(timeout=4)
            except subprocess.TimeoutExpired:
                child.kill()
                child.wait(timeout=1)


def client_waterfall(context, application_type, gdk, binary, port, artifacts):
    from entropy_gui.app import BYTE_COLORS

    arguments = [f"-url=http://127.0.0.1:{port}/v1/data/random", "-slice=4096",
                 "-refresh=50ms", "-analysis-bytes=4096", "-analysis-interval=50ms",
                 "-shannon-threshold=8"]
    app = application_type("client", str(binary), arguments)
    app.set_application_id("org.entropyservice.LinuxPoC.TestClient")
    assert app.register(None)
    app.activate()
    window = app.window
    draws = []
    original_draw = window._draw_waterfall

    def observe_draw(area, cairo_context, width, height):
        original_draw(area, cairo_context, width, height)
        draws.append((width, height, bool(window._surface)))
        del draws[:-128]

    window.drawing_area.set_draw_func(observe_draw)
    try:
        wait_for(context, lambda: window.descriptor is not None, "client invocation description")
        assert_read_only(window)
        assert window.start_button.get_sensitive()
        assert window.session is None
        window.start_button.emit("clicked")
        wait_for(context, lambda: window.session is not None and len(window.model.snapshot()["rows"]) >= 3,
                 "real CLI sample prefixes")
        wait_for(context, lambda: any(drawn for _width, _height, drawn in draws), "native Cairo waterfall paint")
        wait_for(context, lambda: window.stat_labels["assessment"].get_text() == "WARN", "Shannon WARN display")
        state = window.session.snapshot()
        assert state["running"] and not state["error"], state
        assert 1 <= len(state["rows"]) <= 128
        assert all(1 <= len(row) <= 256 for row in state["rows"])
        assert state["sample"]["received_bytes"] > 0
        assert re.fullmatch(r"[0-8]\.\d{6}", window.stat_labels["shannon"].get_text())
        assert window.stat_labels["threshold"].get_text() == "8.000000"
        assert window.stat_labels["assessment"].has_css_class("status-warn")
        assert float(window.stat_labels["throughput"].get_text()) > 0
        assert window._surface.get_width() <= 256 and window._surface.get_height() <= 128
        assert len(set(bytes(window._pixels)[offset:offset + 3] for offset in range(0, len(window._pixels), 4))) > 16
        window._surface.write_to_png(str(artifacts / "waterfall-raster.png"))

        # Real GTK allocation and paint must respond to resize, independent of
        # source arrival or a second byte-acquisition implementation.
        first_width = window.drawing_area.get_width()
        window.set_default_size(900, 620)
        wait_for(context, lambda: window.drawing_area.get_width() != first_width, "window resize")
        resized_width = window.drawing_area.get_width()
        wait_for(context, lambda: any(width == resized_width and drawn for width, _height, drawn in draws),
                 "Cairo repaint at resized allocation")
        assert window._on_key(None, gdk.KEY_F11, 0, 0)
        assert window._fullscreen and not window.header.get_visible()
        assert not window.controls.get_visible() and not window.status_label.get_visible()
        assert window._on_key(None, gdk.KEY_Escape, 0, 0)
        assert not window._fullscreen and window.header.get_visible()
        assert window.controls.get_visible() and window.status_label.get_visible()

        owned_child = window.session._child
        window.stop_button.emit("clicked")
        wait_for(context, lambda: not window.session.snapshot()["running"] and not window._stopping,
                 "Stop and joined owned-client shutdown", timeout=5)
        assert owned_child.poll() is not None
        assert window.session.snapshot()["summary"].get("termination") == "interrupted"
        frozen_rows = window.model.snapshot()["rows"]
        wait_for(context, lambda: window._rows == frozen_rows, "final frozen rows displayed")
        palette = window.palette_dropdown
        first = frozen_rows[0]
        for selected, mode, expected in ((0, "byte", BYTE_COLORS[first[0]]),
                                         (1, "rgb", tuple(first[:3])),
                                         (2, "gray", (first[0],) * 3)):
            palette.set_selected(selected)
            wait_for(context, lambda: window._palette == mode, mode + " palette selection")
            pixel = bytes(window._pixels[:4])
            actual = tuple(pixel[2::-1]) if sys.byteorder == "little" else tuple(pixel[1:4])
            assert actual == expected, (mode, actual, expected)
            assert window.model.snapshot()["rows"] == frozen_rows, mode
            assert window._surface.get_width() == (256 // 3 if mode == "rgb" else 256)
            assert window._surface.get_height() <= 128
            window._surface.write_to_png(str(artifacts / f"waterfall-{mode}.png"))
        # Closing while receiving exercises the asynchronous close path too.
        wait_for(context, lambda: window.start_button.get_sensitive(), "Start re-enabled")
        window.start_button.emit("clicked")
        wait_for(context, lambda: window.session.snapshot()["running"] and bool(window.model.snapshot()["rows"]),
                 "client restart")
        restarted_child = window.session._child
        close_window(context, window)
        assert restarted_child.poll() is not None
        return {"received_bytes": state["sample"]["received_bytes"], "prefix_rows": len(state["rows"]),
                "maximum_prefix_bytes": max(map(len, state["rows"])), "native_paints": len(draws),
                "resized_canvas_width": resized_width, "fullscreen_shortcuts": True,
                "frozen_palettes": ["byte", "rgb", "gray"],
                "stop_and_close_cleanup": True,
                "fullscreen_note": "GTK fullscreen requested; a window manager applies compositor state"}
    finally:
        if window.get_visible():
            close_window(context, window)
        app.quit()


def fullscreen_outcomes(context, application_type, gdk, binary, port):
    """Transport termination must expose status instead of a frozen full screen."""
    results = {}
    for outcome, target in (("Duration", port), ("Failure", free_port())):
        arguments = [f"-url=http://127.0.0.1:{target}/v1/data/random", "-slice=4096",
                     "-refresh=50ms", "-analysis-interval=50ms", "-duration=400ms"]
        app = application_type("client", str(binary), arguments)
        app.set_application_id("org.entropyservice.LinuxPoC.Test" + outcome)
        assert app.register(None)
        app.activate()
        window = app.window
        try:
            wait_for(context, lambda: window.descriptor is not None, outcome + " configuration")
            window.start_button.emit("clicked")
            assert window._on_key(None, gdk.KEY_F11, 0, 0)
            assert window._fullscreen
            wait_for(context, lambda: not window.session.snapshot()["running"] and not window._fullscreen,
                     outcome + " returns from fullscreen", timeout=5)
            assert window.status_label.get_visible() and window.header.get_visible()
            state = window.session.snapshot()
            if outcome == "Duration":
                assert state["summary"].get("termination") == "duration", state
                assert state["sample"] and not state["error"], state
                assert "Stopped" in window.status_label.get_text()
            else:
                assert state["error"], state
                assert "error" in window.status_label.get_text().lower()
            assert window.session._child.poll() is not None
            results[outcome.lower()] = {"chrome_restored": True, "child_reaped": True}
        finally:
            close_window(context, window)
            app.quit()
    return results


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--server", "--server-binary", type=Path, default=ROOT / "bin/entropy-server")
    parser.add_argument("--client", "--client-binary", type=Path, default=ROOT / "bin/entropy-client")
    parser.add_argument("--artifacts", type=Path, default=ROOT / "gui-artifacts")
    args = parser.parse_args()
    if not sys.platform.startswith("linux"):
        parser.error("the GTK PoC smoke test is Linux-only")
    import gi
    gi.require_version("Gtk", "4.0")
    gi.require_version("Gdk", "4.0")
    from gi.repository import Gdk, GLib, Gtk
    from entropy_gui.app import DesktopApplication

    if not Gtk.init_check():
        parser.error("no GTK display; run this check with xvfb-run -a")
    args.artifacts.mkdir(parents=True, exist_ok=True)
    context = GLib.MainContext.default()
    callback_errors = []
    old_hook = sys.excepthook
    def observe_callback_error(kind, error, traceback):
        callback_errors.append(f"{kind.__name__}: {error}")
        old_hook(kind, error, traceback)
    sys.excepthook = observe_callback_error
    try:
        result = {"server": server_configuration(context, DesktopApplication, args.server.resolve())}
        result["inspection_close"] = close_during_inspection(context, DesktopApplication)
        with entropy_server(args.server.resolve()) as port:
            result["client"] = client_waterfall(context, DesktopApplication, Gdk, args.client.resolve(), port, args.artifacts)
            result["fullscreen_outcomes"] = fullscreen_outcomes(context, DesktopApplication, Gdk, args.client.resolve(), port)
    finally:
        sys.excepthook = old_hook
    assert not callback_errors, callback_errors
    result["passed"] = True
    (args.artifacts / "results.json").write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
