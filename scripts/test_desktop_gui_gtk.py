#!/usr/bin/env python3
"""Exercise native GTK windows, live health and the owned CLI on Linux/Windows.

Run after building both utilities, for example:
  xvfb-run -a /usr/bin/python3 scripts/test_desktop_gui_gtk.py
The core/protocol tests remain independent of GTK in test_desktop_gui.py.
"""
import argparse
import contextlib
import http.client
import http.server
import json
import os
from pathlib import Path
import re
import signal
import shlex
import socket
import subprocess
import sys
import tempfile
import threading
import time


ROOT = Path(__file__).resolve().parents[1]
GUI_SOURCE = ROOT / "tools/desktop-gui"
sys.path.insert(0, str(GUI_SOURCE))
WINDOWS = sys.platform == "win32"
DARWIN = sys.platform == "darwin"
OS_SOURCE = "windows-csprng" if WINDOWS else "macos-csprng" if DARWIN else "/dev/urandom"


def free_port():
    with socket.socket() as connection:
        connection.bind(("127.0.0.1", 0))
        return connection.getsockname()[1]


def wait_for(context, predicate, description, timeout=8):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        # Match a real application loop: stop once its condition is met.
        # AppKit callbacks must not be dispatched after a window was destroyed.
        if predicate():
            return
        # A bounded dispatch pass prevents a constantly-ready source from
        # hiding a timeout or starving the actual integration assertions.
        for _ in range(100):
            if not context.pending():
                break
            context.iteration(False)
            if predicate():
                return
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


def capture_window(context, window, path):
    """Capture a native GTK snapshot without Linux-only screenshot utilities."""
    from gi.repository import GLib, Graphene, Gtk

    clock = window.get_frame_clock()
    if clock is not None:
        # Widget state can be updated before GTK has committed its next paint.
        for _ in range(4):
            previous = clock.get_frame_counter()
            window.queue_draw()
            wait_for(context, lambda: clock.get_frame_counter() > previous,
                     "settled native screenshot frame", timeout=3)
    try:
        width, height = window.get_width(), window.get_height()
        paintable = Gtk.WidgetPaintable.new(window)
        snapshot = Gtk.Snapshot()
        paintable.snapshot(snapshot, width, height)
        node = snapshot.to_node()
        if node is None:
            return {"captured": False, "reason": "GTK snapshot returned no render node"}
        bounds = Graphene.Rect()
        bounds.init(0, 0, width, height)
        texture = window.get_native().get_renderer().render_texture(node, bounds)
        texture.save_to_png(str(path))
        if not path.is_file() or path.stat().st_size == 0:
            return {"captured": False, "reason": "GTK texture did not produce a PNG"}
        return {"captured": True, "file": path.name, "width": width, "height": height}
    except (TypeError, ValueError, AttributeError, RuntimeError, GLib.Error) as error:
        # Some distribution bindings do not marshal GskRenderNode. Keep exact
        # evidence while native paint/geometry assertions still test the UI.
        return {"captured": False, "reason": type(error).__name__ + ": " + str(error)}


def resize_native_window(window, width, height):
    """Request a real user-style resize, including an already mapped Win32 HWND."""
    assert window.get_resizable(), "The GUI must permit user window resizing"
    window.unfullscreen()
    window.unmaximize()
    window.set_default_size(width, height)
    if not WINDOWS:
        return "gtk-default-size"

    # Gtk's Win32 backend can constrain the initial default to the work area
    # and ignore later default-size changes. Resize the owned native window,
    # just as a user dragging its border would; allocation/paint assertions
    # below still validate GTK receives and renders the actual size change.
    import ctypes
    from ctypes import wintypes
    import gi

    user = ctypes.WinDLL("user32", use_last_error=True)
    user.GetWindowThreadProcessId.argtypes = [wintypes.HWND, ctypes.POINTER(wintypes.DWORD)]
    user.GetWindowThreadProcessId.restype = wintypes.DWORD
    handle = None
    try:
        gi.require_version("GdkWin32", "4.0")
        from gi.repository import GdkWin32
        handle = int(GdkWin32.Win32Surface.get_handle(window.get_surface()))
    except (ImportError, ValueError, AttributeError, TypeError):
        pass  # Some GI builds omit the native HWND return-type binding.
    if not handle:
        callback_type = ctypes.WINFUNCTYPE(wintypes.BOOL, wintypes.HWND, wintypes.LPARAM)
        user.EnumWindows.argtypes = [callback_type, wintypes.LPARAM]
        user.EnumWindows.restype = wintypes.BOOL
        user.GetWindowTextW.argtypes = [wintypes.HWND, wintypes.LPWSTR, ctypes.c_int]
        user.GetWindowTextW.restype = ctypes.c_int
        matches = []

        @callback_type
        def find_owned(candidate, _parameter):
            process_id = wintypes.DWORD()
            user.GetWindowThreadProcessId(candidate, ctypes.byref(process_id))
            if process_id.value == os.getpid():
                title = ctypes.create_unicode_buffer(512)
                user.GetWindowTextW(candidate, title, len(title))
                if title.value == window.get_title():
                    matches.append(candidate)
            return True

        if not user.EnumWindows(find_owned, 0):
            raise ctypes.WinError(ctypes.get_last_error())
        assert len(matches) == 1, "Could not identify this GTK window's owned HWND"
        handle = matches[0]
    process_id = wintypes.DWORD()
    user.GetWindowThreadProcessId(handle, ctypes.byref(process_id))
    assert process_id.value == os.getpid(), "Refusing to resize a window owned by another process"
    user.ShowWindow.argtypes = [wintypes.HWND, ctypes.c_int]
    user.ShowWindow.restype = wintypes.BOOL
    user.SetWindowPos.argtypes = [wintypes.HWND, wintypes.HWND, ctypes.c_int, ctypes.c_int,
                                  ctypes.c_int, ctypes.c_int, wintypes.UINT]
    user.SetWindowPos.restype = wintypes.BOOL
    user.ShowWindow(handle, 9)  # SW_RESTORE: leave a work-area-maximized window.
    scale = window.get_scale_factor()
    # SWP_NOMOVE | SWP_NOZORDER | SWP_NOACTIVATE, targeting this HWND only.
    if not user.SetWindowPos(handle, None, 0, 0, width * scale, height * scale, 0x0016):
        raise ctypes.WinError(ctypes.get_last_error())
    return "win32-owned-window-position"


def server_configuration(context, application_type, binary):
    port = free_port()
    missing = "/definitely-not-present/entropy-gui-device"
    if WINDOWS or DARWIN:
        # These platforms deliberately reject Linux device paths, even in a startup
        # description. Missing TLS files instead prove inspection avoids I/O.
        arguments = [f"-http=127.0.0.1:{port}", "-device="+OS_SOURCE,
                     "-enable-https=true", "-cert-file=missing-gui-certificate.pem",
                     "-key-file=missing-gui-key.pem"]
    else:
        arguments = [f"-http=127.0.0.1:{port}", "-enable-https=false",
                     "-require-device=true", f"-device={missing}"]
    app = application_type("server", str(binary), arguments)
    # The smoke check creates both roles in one process. Separate application
    # IDs keep their D-Bus object paths distinct, as separate real invocations do.
    app.set_application_id("org.entropyservice.LinuxPoC.TestServer")
    assert app.register(None)
    app.activate()
    window = app.window
    try:
        wait_for(context, lambda: window.descriptor is not None and window.get_mapped(), "server invocation description and window mapping")
        assert_read_only(window)
        assert window.descriptor["settings"]["seed_source"] == (OS_SOURCE if WINDOWS or DARWIN else missing)
        assert window.descriptor["features"]["https"] is (WINDOWS or DARWIN)
        assert window.descriptor["features"]["require_device"] is (not WINDOWS and not DARWIN)
        assert window.session is None
        assert window.health_session is None, "Startup inspection must not auto-connect"
        # Description succeeds despite an unreadable device, and the requested
        # address remains unbound: this window never starts a server.
        with socket.socket() as probe:
            probe.settimeout(.2)
            assert probe.connect_ex(("127.0.0.1", port)) != 0
        return {"read_only_features": len(window.feature_checks), "server_started": False,
                "runtime_resources_opened": False, "health_auto_connected": False}
    finally:
        close_window(context, window)
        app.quit()


def close_during_inspection(context, application_type):
    """Closing before describe completes must not abandon an owned child."""
    if WINDOWS:
        # A Unix shebang helper is not a Windows executable. Native core tests
        # separately exercise describe deadlines with a Python command argv.
        return {"not_applicable": "POSIX executable-helper fixture; covered by Windows process tests"}
    with tempfile.TemporaryDirectory(prefix="entropy-gui-inspection-") as directory:
        inspector = Path(directory) / "hung-inspector"
        pid_file = inspector.with_suffix(".pid")
        # A POSIX shell avoids Apple's developer-tools Python shim and tests
        # exactly one owned PID: exec replaces the shell with the sleeping tool.
        inspector.write_text("#!/bin/sh\nprintf '%s\\n' \"$$\" > " + shlex.quote(str(pid_file)) + "\nexec /bin/sleep 30\n")
        inspector.chmod(0o700)
        app = application_type("server", str(inspector), [])
        app.set_application_id("org.entropyservice.LinuxPoC.TestInspectionClose")
        assert app.register(None)
        app.activate()
        window = app.window
        try:
            wait_for(context, lambda: pid_file.exists() or window.inspection_error_label is not None,
                     "owned configuration inspector to start")
            assert pid_file.exists(), window.inspection_error_label.get_text()
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
    from entropy_gui.process import spawn_owned, stop_owned

    port = free_port()
    with tempfile.TemporaryFile() as log:
        source = ["-device="+OS_SOURCE] if WINDOWS or DARWIN else ["-device=/dev/urandom", "-require-device=true"]
        child = spawn_owned([str(binary), f"-http=127.0.0.1:{port}",
                             "-enable-https=false", *source], stdout=log, stderr=subprocess.STDOUT)
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
            stop_owned(child)


class HealthFixture(http.server.BaseHTTPRequestHandler):
    """Controlled HTTP health faults without replacing GTK's actual poller."""

    def log_message(self, *_args):
        pass

    def do_GET(self):
        self.server.requests += 1
        mode = self.server.mode
        if mode == "slow":
            self.server.entered.set()
            self.server.release.wait(timeout=4)
            mode = "healthy"
        if mode == "drop":
            self.connection.shutdown(socket.SHUT_RDWR)
            self.connection.close()
            return
        ready = mode == "healthy"
        if self.path == "/live":
            status, body = 200, {"status": "ok"}
        elif self.path == "/ready":
            status = 200 if ready else 503
            body = {"status": "ok" if ready else "degraded", "ready": ready,
                    "rng_version": "test", "rng_source": "fixture-csprng", "rng_drbg": "chacha20",
                    "seed_state": "fresh" if ready else "failed", "on_reseed_failure": "stop",
                    "generation_allowed": ready, "reseed_age_ms": 123,
                    "reseed_interval_ms": 15000, "max_reseed_age_ms": 60000,
                    "reseed_size_bits": 2048, "reseed_failures_total": 0 if ready else 2,
                    "last_reseed_at": "2026-01-01T00:00:00Z"}
        else:
            status, body = 404, {"error": "unexpected test path"}
        encoded = json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        with contextlib.suppress(BrokenPipeError, ConnectionResetError, ConnectionAbortedError):
            self.wfile.write(encoded)


@contextlib.contextmanager
def health_fixture():
    fixture = http.server.ThreadingHTTPServer(("127.0.0.1", 0), HealthFixture)
    fixture.daemon_threads = True
    fixture.mode = "healthy"
    fixture.requests = 0
    fixture.entered = threading.Event()
    fixture.release = threading.Event()
    thread = threading.Thread(target=fixture.serve_forever, daemon=True)
    thread.start()
    try:
        yield fixture
    finally:
        fixture.release.set()
        fixture.shutdown()
        fixture.server_close()
        thread.join(timeout=2)


def missing_local_executables(context, application_type):
    """Explain missing local binaries without disabling remote server health."""
    outcomes = {}
    with tempfile.TemporaryDirectory(prefix="entropy-gui-missing-") as directory, health_fixture() as fixture:
        for role in ("server", "client"):
            missing = Path(directory) / ("missing " + role + (".exe" if WINDOWS else ""))
            app = application_type(role, str(missing), [],
                                   health_url=f"http://127.0.0.1:{fixture.server_port}" if role == "server" else None)
            app.set_application_id("org.entropyservice.DesktopPoC.Missing" + role.title())
            assert app.register(None)
            app.activate()
            window = app.window
            try:
                wait_for(context, lambda: window.inspection_error_label is not None, role + " missing executable guidance")
                detail = window.inspection_error_label.get_text()
                assert str(missing) in detail and "--" + role + "-binary" in detail, detail
                assert "configuration file" in detail.lower(), detail
                assert str(missing) in window.binary_label.get_text()
                assert window.descriptor is None
                if role == "server":
                    window.health_connect_button.emit("clicked")
                    wait_for(context, lambda: window.health_labels["state"].get_text() == "READY",
                             "remote health despite missing local server executable")
                    assert window.inspection_error_label.get_text() == detail
                    outcomes[role] = {"actionable_path_error": True, "independent_live_health": True}
                else:
                    assert not window.start_button.get_sensitive()
                    assert window.settings_expander.get_expanded()
                    outcomes[role] = {"actionable_path_error": True, "start_disabled": True}
            finally:
                close_window(context, window)
                app.quit()
    return outcomes


def live_server_health(context, application_type, binary, port, artifacts):
    """Fresh state, reseed degradation, disconnection and recovery reach widgets."""
    source = ["-device="+OS_SOURCE]
    arguments = [f"-http=127.0.0.1:{port}", "-enable-https=false", *source]
    base = f"http://127.0.0.1:{port}"
    app = application_type("server", str(binary), arguments, health_url=base)
    app.set_application_id("org.entropyservice.DesktopPoC.TestHealth")
    assert app.register(None)
    app.activate()
    window = app.window
    try:
        wait_for(context, lambda: window.descriptor is not None and window.get_mapped(), "live-health window")
        assert window.health_session is None
        assert window.health_connect_button.get_sensitive()
        window.health_connect_button.emit("clicked")
        wait_for(context, lambda: window.health_session is not None and
                 window.health_labels["state"].get_text() == "READY", "observed real server readiness")
        state = window.health_session.snapshot()
        assert state["fresh"] and state["ready"] is True and state["live"] is True, state
        assert state["listener"] == "reachable", state
        assert state["health"]["rng_source"]
        assert state["health"]["rng_drbg"].lower() in ("aes-256-ctr", "chacha20"), state
        assert state["health"]["generation_allowed"] is True
        assert state["health"]["reseed_age_ms"] >= 0
        assert state["health"]["reseed_failures_total"] == 0
        assert window.health_labels["state"].has_css_class("status-ok")
        assert window.health_labels["rng_drbg"].get_text() == state["health"]["rng_drbg"]
        screenshot = capture_window(context, window, artifacts / "server-health.png")
        window.health_disconnect_button.emit("clicked")
        wait_for(context, lambda: window.health_labels["state"].get_text() == "DISCONNECTED" and
                 window.health_connect_button.get_sensitive(), "explicit health disconnect")
        assert not window.health_session.snapshot()["fresh"]
        assert not window.health_labels["state"].has_css_class("status-ok")
        assert window.health_labels["rng_drbg"].get_text() == "—"

        with health_fixture() as fixture:
            window.health_url_entry.set_text(f"http://127.0.0.1:{fixture.server_port}")
            assert fixture.requests == 0
            window.health_connect_button.emit("clicked")
            wait_for(context, lambda: window.health_labels["state"].get_text() == "READY" and
                     window.health_labels["rng_source"].get_text() == "fixture-csprng", "fixture health connection")
            fixture.mode = "degraded"
            wait_for(context, lambda: window.health_labels["state"].get_text() == "DEGRADED", "503 reseed degradation")
            degraded = window.health_session.snapshot()
            assert degraded["fresh"] and degraded["ready"] is False and degraded["live"] is True
            assert degraded["health"]["reseed_failures_total"] == 2
            assert degraded["health"]["generation_allowed"] is False
            assert window.health_labels["reseed_failures_total"].get_text() == "2"
            assert not window.health_labels["state"].has_css_class("status-ok")
            fixture.mode = "drop"
            wait_for(context, lambda: window.health_labels["state"].get_text() == "UNAVAILABLE", "connection loss clears fresh health")
            assert not window.health_session.snapshot()["fresh"]
            assert window.health_session.snapshot()["ready"] is None
            for key in ("rng_source", "rng_drbg", "reseed_age_ms", "generation_allowed"):
                assert window.health_labels[key].get_text() == "—", key
            assert not window.health_labels["state"].has_css_class("status-ok")
            fixture.mode = "healthy"
            wait_for(context, lambda: window.health_labels["state"].get_text() == "READY", "live health recovery")
            assert window.health_labels["state"].has_css_class("status-ok")
            fixture.mode = "slow"
            wait_for(context, fixture.entered.is_set, "in-flight health request")
            began = time.monotonic()
            window.health_disconnect_button.emit("clicked")
            assert time.monotonic() - began < .5, "Disconnect blocked the GTK thread"
            assert window.health_labels["state"].get_text() == "DISCONNECTED"
            assert not window.health_labels["state"].has_css_class("status-ok")
            fixture.mode = "healthy"
            fixture.release.set()
            wait_for(context, lambda: window.health_connect_button.get_sensitive(), "stopped health request drained")
            assert window.health_labels["state"].get_text() == "DISCONNECTED", "Late reply restored stale ready state"
            assert not window.health_session.snapshot()["fresh"]
            window.health_connect_button.emit("clicked")
            wait_for(context, lambda: window.health_labels["state"].get_text() == "READY", "health reconnect after in-flight stop")
            session = window.health_session
            close_window(context, window)
            wait_for(context, lambda: not session.snapshot()["running"] and not session.snapshot()["stopping"],
                     "health worker close cleanup", timeout=5)
            assert not session.snapshot()["fresh"]
        return {"real_server_ready": True, "reseed_degraded": True,
                "listener_loss_clears_health": True, "recovery": True,
                "inflight_disconnect_ignores_late_reply": True, "health_close_cleanup": True,
                "screenshot": screenshot}
    finally:
        if window.get_visible():
            close_window(context, window)
        app.quit()


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
        wait_for(context, lambda: len(window.model.snapshot()["rows"]) >= 16, "populated native waterfall screenshot")
        screenshot = capture_window(context, window, artifacts / "client-window.png")

        # Real GTK allocation and paint must respond to resize, independent of
        # source arrival or a second byte-acquisition implementation.
        first_width = window.drawing_area.get_width()
        geometry = {"platform": sys.platform, "before_window": [window.get_width(), window.get_height()],
                    "before_canvas_width": first_width, "requested_window": [900, 620],
                    "was_maximized": window.is_maximized()}
        geometry["method"] = resize_native_window(window, 900, 620)
        try:
            wait_for(context, lambda: window.drawing_area.get_width() != first_width, "window resize")
        finally:
            geometry["after_window"] = [window.get_width(), window.get_height()]
            geometry["after_canvas_width"] = window.drawing_area.get_width()
            (artifacts / "resize.json").write_text(json.dumps(geometry, indent=2) + "\n")
            print("Native resize: " + json.dumps(geometry), flush=True)
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
        stopped = window.session.snapshot()
        assert stopped["summary"].get("termination") == "interrupted", stopped
        assert not stopped.get("forced_stop", False), stopped
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
        assert not window.session.snapshot().get("forced_stop", False)
        return {"received_bytes": state["sample"]["received_bytes"], "prefix_rows": len(state["rows"]),
                "maximum_prefix_bytes": max(map(len, state["rows"])), "native_paints": len(draws),
                "resized_canvas_width": resized_width, "fullscreen_shortcuts": True,
                "frozen_palettes": ["byte", "rgb", "gray"],
                "stop_and_close_cleanup": True,
                "screenshot": screenshot,
                "fullscreen_note": "GTK fullscreen requested; a window manager applies compositor state"}
    finally:
        if window.get_visible():
            close_window(context, window)
        app.quit()


def fullscreen_outcomes(context, application_type, gdk, binary, port, outcomes=("Duration", "Failure")):
    """Transport termination must expose status instead of a frozen full screen."""
    results = {}
    for outcome in outcomes:
        target = port if outcome == "Duration" else free_port()
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


def terminal_interrupts(context, server_binary, client_binary, port, artifacts):
    """Real terminal interruption quits GTK and reaps only its owned client."""
    from entropy_gui.process import _signal, spawn_owned

    probe = '''import faulthandler, http.client, json, signal, sys
faulthandler.enable()
from pathlib import Path
sys.path.insert(0, sys.argv[1])
from gi.repository import GLib
from entropy_gui import app

configuration = json.loads(Path(sys.argv[2]).read_text())
ready = Path(configuration['ready'])
finished = Path(configuration['finished'])
original = app.DesktopApplication
instances = []

class ProbeApplication(original):
    def do_activate(self):
        super().do_activate()
        instances.append(self)
        GLib.timeout_add(25, self.mark_ready)

    def mark_ready(self):
        window = self.window
        if window.descriptor is None:
            return GLib.SOURCE_CONTINUE
        if configuration['role'] == 'client':
            if window.session is None:
                window.start_client()
            if not window.model.snapshot()['rows']:
                return GLib.SOURCE_CONTINUE
        else:
            if configuration['action'] == 'owned_interrupt':
                if window.session is None:
                    window.start_server()
                if not window.session.snapshot()['process_running']:
                    return GLib.SOURCE_CONTINUE
                connection = http.client.HTTPConnection('127.0.0.1', configuration['owned_port'], timeout=.1)
                try:
                    connection.request('GET', '/live')
                    response = connection.getresponse()
                    response.read(8192)
                    if response.status != 200:
                        return GLib.SOURCE_CONTINUE
                except (OSError, http.client.HTTPException):
                    return GLib.SOURCE_CONTINUE
                finally:
                    connection.close()
            if window.health_session is None:
                window.connect_health()
            if not window.health_session.snapshot()['fresh']:
                return GLib.SOURCE_CONTINUE
        ready.write_text(json.dumps({'ready': True}))
        if configuration['action'] == 'fullscreen_close':
            window.set_fullscreen(True)
        if configuration['action'] in ('close', 'fullscreen_close'):
            window.close()
        return GLib.SOURCE_REMOVE

app.DesktopApplication = ProbeApplication
signals = [signal.SIGINT] + ([signal.SIGBREAK] if sys.platform == 'win32' else [])
before = {signum: signal.getsignal(signum) for signum in signals}
result = app.run(configuration['role'], configuration['binary'], configuration['arguments'],
                 health_url=configuration.get('health_url'))
window = instances[0].window
state = window.session.snapshot() if window.session else None
health_stopped = True
if window.health_session:
    window.health_session._thread.join(timeout=2)
    health_stopped = not window.health_session._thread.is_alive()
finished.write_text(json.dumps({
    'exit_code': result,
    'handlers_restored': all(signal.getsignal(signum) is handler for signum, handler in before.items()),
    'window_destroyed': window._destroyed,
    'fullscreen_transition_settled': window._native_fullscreen_pending is None,
    'fullscreen_close_requested': configuration['action'] == 'fullscreen_close',
    'inspection_reaped': not window._inspection_thread.is_alive(),
    'health_stopped': health_stopped,
    'owned_process_reaped': state is None or window.session._child.poll() is not None,
    'client_summary': state.get('summary') if state else None,
    'owned_error': state.get('error', '') if state else '',
    'forced_stop': state['forced_stop'] if state else False,
}))
raise SystemExit(result)
'''
    results = {}
    with tempfile.TemporaryDirectory(prefix="entropy-gui-interrupt-") as directory:
        directory = Path(directory)
        script = directory / "interrupt_probe.py"
        script.write_text(probe)
        cases = [("server", server_binary, "interrupt"),
                 ("server", server_binary, "owned_interrupt"),
                 ("client", client_binary, "interrupt"),
                 ("client", client_binary, "close")]
        if DARWIN:
            cases.append(("client", client_binary, "fullscreen_close"))
        for role, binary, action in cases:
            name = role + "_owned" if action == "owned_interrupt" else role if action == "interrupt" else role + "_" + action
            ready, finished = directory / (name + "-ready.json"), directory / (name + "-finished.json")
            options = directory / (name + "-options.json")
            arguments = (["-device="+OS_SOURCE, "-enable-https=false"]
                         if role == "server" else
                         [f"-url=http://127.0.0.1:{port}/v1/data/random", "-slice=4096", "-refresh=50ms",
                          "-analysis-interval=50ms", "-duration=15s"])
            owned_port = free_port() if action == "owned_interrupt" else None
            if owned_port is not None:
                arguments.append(f"-http=127.0.0.1:{owned_port}")
            options.write_text(json.dumps({"role": role, "action": action, "binary": str(binary), "arguments": arguments,
                                          "health_url": f"http://127.0.0.1:{port}" if role == "server" else None,
                                          "ready": str(ready), "finished": str(finished), "owned_port": owned_port}))
            with (artifacts / (name + "-interrupt.log")).open("w+b") as log:
                child = spawn_owned([sys.executable, str(script), str(GUI_SOURCE), str(options)],
                                    stdout=log, stderr=subprocess.STDOUT)
                try:
                    wait_for(context, lambda: ready.is_file() or child.poll() is not None,
                             role + " GUI interruption readiness", timeout=12)
                    assert ready.is_file(), role + " GUI exited before interruption"
                    if action in ("close", "fullscreen_close"):
                        expected_exit = 0
                    elif WINDOWS:
                        # CTRL_C cannot target an owned process group. The run()
                        # handler treats targeted CTRL_BREAK the same as Ctrl+C.
                        assert _signal(child), "Targeted Windows CTRL_BREAK could not be sent"
                        expected_exit = 130
                    else:
                        child.send_signal(signal.SIGINT)
                        expected_exit = 130
                    assert child.wait(timeout=8) == expected_exit
                    log.seek(0)
                    output = log.read(65536).decode("utf-8", "replace")
                    assert "Traceback" not in output and "KeyboardInterrupt" not in output, output
                    state = json.loads(finished.read_text())
                    assert state["exit_code"] == expected_exit and state["handlers_restored"], state
                    assert state["window_destroyed"] and state["inspection_reaped"] and state["health_stopped"], state
                    assert state["fullscreen_transition_settled"], state
                    assert state["owned_process_reaped"] and not state["forced_stop"], state
                    assert not state["owned_error"], state
                    if role == "client":
                        assert state["client_summary"]["termination"] == "interrupted", state
                    connection = http.client.HTTPConnection("127.0.0.1", port, timeout=2)
                    try:
                        connection.request("GET", "/live")
                        response = connection.getresponse()
                        response.read(8192)
                        assert response.status == 200
                    finally:
                        connection.close()
                    results[name] = {**state, "separate_server_still_live": True,
                                     "signal": "window close" if action in ("close", "fullscreen_close") else
                                     "targeted CTRL_BREAK" if WINDOWS else "SIGINT"}
                finally:
                    if child.poll() is None:
                        # Preserve the same graceful path for assertion cleanup.
                        if WINDOWS:
                            _signal(child)
                        else:
                            child.send_signal(signal.SIGINT)
                        try:
                            child.wait(timeout=5)
                        except subprocess.TimeoutExpired:
                            child.kill()
                            child.wait(timeout=2)
    return results


def main():
    global GUI_SOURCE
    parser = argparse.ArgumentParser(description=__doc__)
    suffix = ".exe" if WINDOWS else ""
    parser.add_argument("--server", "--server-binary", type=Path, default=ROOT / ("bin/entropy-server" + suffix))
    parser.add_argument("--client", "--client-binary", type=Path, default=ROOT / ("bin/entropy-client" + suffix))
    parser.add_argument("--gui-source", type=Path, default=GUI_SOURCE)
    parser.add_argument("--artifacts", type=Path, default=ROOT / "gui-artifacts")
    fullscreen = parser.add_mutually_exclusive_group()
    fullscreen.add_argument("--fullscreen-outcome", choices=("Duration", "Failure"))
    fullscreen.add_argument("--skip-fullscreen-outcomes", action="store_true")
    args = parser.parse_args()
    GUI_SOURCE = args.gui_source.resolve()
    sys.path.insert(0, str(GUI_SOURCE))
    if not sys.platform.startswith("linux") and not WINDOWS and not DARWIN:
        parser.error("the GTK smoke test supports Linux, Windows and macOS")
    import gi
    gi.require_version("Gtk", "4.0")
    gi.require_version("Gdk", "4.0")
    gi.require_version("Graphene", "1.0")
    from gi.repository import Gdk, GLib, Gtk
    from entropy_gui.app import DesktopApplication

    if not Gtk.init_check() or Gdk.Display.get_default() is None:
        parser.error("no GTK display; use a macOS/Windows desktop session or xvfb-run -a on Linux")
    args.artifacts.mkdir(parents=True, exist_ok=True)
    context = GLib.MainContext.default()
    callback_errors = []
    old_hook = sys.excepthook
    def observe_callback_error(kind, error, traceback):
        callback_errors.append(f"{kind.__name__}: {error}")
        old_hook(kind, error, traceback)
    sys.excepthook = observe_callback_error
    try:
        if args.fullscreen_outcome:
            result = {"platform": sys.platform}
            with entropy_server(args.server.resolve()) as port:
                result["fullscreen_outcomes"] = fullscreen_outcomes(context, DesktopApplication, Gdk,
                    args.client.resolve(), port, outcomes=(args.fullscreen_outcome,))
        else:
            print("Native GTK: server configuration", flush=True)
            result = {"platform": sys.platform,
                      "server": server_configuration(context, DesktopApplication, args.server.resolve())}
            print("Native GTK: missing_local_executables", flush=True)
            result["missing_local_executables"] = missing_local_executables(context, DesktopApplication)
            print("Native GTK: inspection_close", flush=True)
            result["inspection_close"] = close_during_inspection(context, DesktopApplication)
            with entropy_server(args.server.resolve()) as port:
                from test_desktop_gui_controls_gtk import controls_and_owned_server
                print("Native GTK: controls", flush=True)
                result["controls"] = controls_and_owned_server(context, DesktopApplication, args.server.resolve(),
                                                              args.client.resolve(), port, args.artifacts)
                print("Native GTK: terminal_interrupts", flush=True)
                result["terminal_interrupts"] = terminal_interrupts(context, args.server.resolve(), args.client.resolve(),
                                                                    port, args.artifacts)
                print("Native GTK: live_health", flush=True)
                result["live_health"] = live_server_health(context, DesktopApplication, args.server.resolve(), port, args.artifacts)
                print("Native GTK: client", flush=True)
                result["client"] = client_waterfall(context, DesktopApplication, Gdk, args.client.resolve(), port, args.artifacts)
                if not args.skip_fullscreen_outcomes:
                    print("Native GTK: fullscreen_outcomes", flush=True)
                    result["fullscreen_outcomes"] = fullscreen_outcomes(context, DesktopApplication, Gdk, args.client.resolve(), port)
    finally:
        sys.excepthook = old_hook
    assert not callback_errors, callback_errors
    result["passed"] = True
    (args.artifacts / "results.json").write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
