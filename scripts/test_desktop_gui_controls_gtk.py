#!/usr/bin/env python3
"""Native settings-editor and owned-server checks used by the GTK suite.

The parent suite supplies real companion binaries and an independently running
server. This module deliberately has no standalone fixtures or GUI imports at
module load, so the core unittest discovery can import it without GTK.
"""
import http.client
import http.server
from http.cookies import SimpleCookie
import json
from pathlib import Path
import socket
import ssl
import subprocess
import sys
import tempfile
import threading

from test_desktop_gui_gtk import capture_window, close_window, free_port, wait_for


def _live_status(port):
    connection = http.client.HTTPConnection("127.0.0.1", port, timeout=.2)
    try:
        connection.request("GET", "/live")
        response = connection.getresponse()
        response.read(8192)
        return response.status
    except (OSError, http.client.HTTPException):
        return None
    finally:
        connection.close()


def _listener_open(port):
    with socket.socket() as connection:
        connection.settimeout(.2)
        return connection.connect_ex(("127.0.0.1", port)) == 0


def _window(context, application_type, role, binary, arguments, identifier, health_url=None):
    app = application_type(role, str(binary), arguments, health_url=health_url)
    app.set_application_id("org.entropyservice.Desktop.TestControls" + identifier)
    assert app.register(None)
    app.activate()
    window = app.window
    wait_for(context, lambda: window.descriptor is not None and window.get_mapped(),
             role + " editable startup configuration")
    return app, window


def _options(context, window):
    assert window.options_button.get_sensitive()
    window.options_button.emit("clicked")
    wait_for(context, lambda: window.settings_window is not None and window.settings_window.get_mapped(),
             "native startup options editor")
    return window.settings_window


def _final_server_drain_view(window):
    """A joined pipe pump can finish without adding another log revision."""
    session = window.session
    original = session.snapshot()
    final_line = "final fixture output without a newline"
    state = {**original, "revision": original["revision"] + 1, "running": True,
             "process_running": False, "returncode": 0, "error": "", "logs": "draining output\n"}

    class FinalDrain:
        def snapshot(self):
            return dict(state)

    try:
        window.session = FinalDrain()
        window._stopping = True
        window.status_label.set_text("Stopping owned server…")
        window._tick_server()
        assert "running" in window.process_state_label.get_text().lower()

        # Final drain and the asynchronous Stop callback are separate events.
        # Neither one is required to append a log line or change its revision.
        state["running"] = False
        state["logs"] += final_line
        window._tick_server()
        assert "stopped" in window.process_state_label.get_text().lower()
        buffer = window.server_log_view.get_buffer()
        assert buffer.get_text(*buffer.get_bounds(), True).endswith(final_line)
        window._stopping = False
        window._tick_server()
        assert "stopped" in window.status_label.get_text().lower()
        assert not window.status_label.get_text().lower().startswith("stopping")
    finally:
        window.session = session
        window._stopping = False
        # Restore the real stopped invocation and its display/cache before the
        # following restart; no synthetic snapshot escapes this boundary test.
        window._revision = None
        window._tick_server()


def _client_options(context, application_type, binary, remote_port, artifacts):
    displayed_endpoint = f"http://127.0.0.1:{remote_port}/v1/data/random"
    endpoint_argument = (f"-url=http://fixture-user:fixture-password@127.0.0.1:{remote_port}/v1/data/random"
                         "?kept=original%20query&token=fixture-query-token#fixture-fragment")
    arguments = ["-mode=http", endpoint_argument, "-slice=4096", "-analysis-bytes=4096",
                 "-refresh=100ms", "-analysis-interval=100ms", "-dashboard=true"]
    app, window = _window(context, application_type, "client", binary, arguments, "Client")
    try:
        assert window.session is None, "Opening the client GUI must not start acquisition"
        editor = _options(context, window)
        assert editor.get_value("url") == displayed_endpoint, "Saved endpoint secrets were displayed"
        editor.set_value("capabilities", "required")
        editor.set_value("adaptive", True)
        editor.set_value("adaptive-max-interval", "5s")
        editor.set_value("slice", "8192")
        editor.set_value("analysis-bytes", "8192")
        editor.set_value("shannon-threshold", "6.123456")
        editor.set_value("refresh", "50ms")
        editor.set_value("analysis-interval", "50ms")
        editor.set_value("duration", "2s")
        candidate = editor.candidate_arguments()
        assert endpoint_argument in candidate, "Unedited credentials/query were replaced by the displayed URL"
        assert sum(argument.startswith("-url=") for argument in candidate) == 1
        screenshot = capture_window(context, editor, artifacts / "client-options.png")
        editor.apply_button.emit("clicked")
        wait_for(context, lambda: not editor.get_visible() and
                 window.descriptor["settings"]["payload_bytes"] == 8192,
                 "client options validated by the CLI")
        settings = window.descriptor["settings"]
        assert settings["analysis_bytes"] == 8192, settings
        assert settings["shannon_threshold_bits_per_byte"] == 6.123456, settings
        assert settings["refresh"] == "50ms" and settings["duration"] == "2s", settings
        assert endpoint_argument in window.arguments, "Unedited endpoint credentials/query were rewritten"
        assert "-dashboard=true" in window.arguments, "Unexposed CLI flag was dropped"
        assert "-mode=http" in window.arguments, "Unedited transport alias was rewritten"
        assert window.session is None, "Validating options must not connect the client"

        applied_arguments = list(window.arguments)
        editor = _options(context, window)
        assert editor.get_value("url") == displayed_endpoint, "Reopening options exposed endpoint secrets"
        editor.set_value("shannon-threshold", "9")
        editor.apply_button.emit("clicked")
        wait_for(context, lambda: editor.apply_button.get_sensitive() and
                 "threshold" in editor.status_label.get_text().lower(),
                 "invalid threshold rejection in the editor")
        assert editor.get_visible()
        assert window.arguments == applied_arguments, "Invalid edits replaced the validated invocation"
        assert window.descriptor["settings"]["shannon_threshold_bits_per_byte"] == 6.123456
        assert window.session is None
        editor.close()
        wait_for(context, lambda: not editor.get_visible() and window.start_button.get_sensitive(),
                 "closing invalid edits preserves the valid client configuration")

        window.start_button.emit("clicked")
        wait_for(context, lambda: window.session is not None and bool(window.model.snapshot()["rows"]) and
                 not window.options_button.get_sensitive(), "edited client acquisition")
        state = window.session.snapshot()
        assert state["sample"]["sample_bytes"] == 8192, state
        assert window.stat_labels["threshold"].get_text() == "6.123456"
        wait_for(context, lambda: "average received" in window.performance_label.get_text(), "native performance view")
        assert state["discovery"]["status"] == "available" and state["discovery"]["max_bytes"] == 33554432
        assert state["settings"]["adaptive"] is True and state["sample"]["effective_interval_ms"] >= 50
        assert "33,554,432" in window.performance_label.get_text()
        capture_window(context, window, artifacts / "client-performance.png")
        window.set_fullscreen(True)
        assert not window.performance_expander.get_visible()
        window.set_fullscreen(False)
        assert window.performance_expander.get_visible()
        wait_for(context, lambda: window._native_fullscreen_pending is None, "fullscreen entry/exit settled", timeout=5)
        child = window.session._child
        window.stop_button.emit("clicked")
        wait_for(context, lambda: not window.session.snapshot()["running"] and
                 not window._stopping and window.options_button.get_sensitive(),
                 "edited client stop and options re-enable", timeout=5)
        assert child.poll() is not None
        assert not window.session.snapshot()["forced_stop"]

        frozen = window.model.snapshot()
        wait_for(context, lambda: window._rows == frozen["rows"] and
                 window.stat_labels["shannon"].get_text() == f"{frozen['sample']['shannon_bits_per_byte']:.6f}" and
                 window.stat_labels["assessment"].get_text() == frozen["sample"]["shannon_assessment"],
                 "last analysed sample frozen after Stop")
        header_keys = ("shannon", "threshold", "assessment")
        frozen_header = tuple(window.stat_labels[key].get_text() for key in header_keys)
        observations = []

        def observe_frozen_header(_label, _property):
            observations.append(tuple(window.stat_labels[key].get_text() for key in header_keys))

        handlers = [(window.stat_labels[key], window.stat_labels[key].connect("notify::label", observe_frozen_header))
                    for key in header_keys]
        try:
            editor = _options(context, window)
            editor.set_value("shannon-threshold", "8")
            editor.apply_button.emit("clicked")
            wait_for(context, lambda: not editor.get_visible() and
                     window.descriptor["settings"]["shannon_threshold_bits_per_byte"] == 8,
                     "next-run threshold validated while the previous sample stays frozen")
            assert window.stat_labels["threshold"].get_text() == "6.123456"
            assert tuple(window.stat_labels[key].get_text() for key in header_keys) == frozen_header
            assert all(header == frozen_header for header in observations), "Applying next-run settings relabelled frozen statistics"
            assert window.model.snapshot() == frozen, "Applying options altered the retained sample or history"
            assert window._rows == frozen["rows"]
        finally:
            for label, handler in handlers:
                label.disconnect(handler)

        window.start_button.emit("clicked")
        wait_for(context, lambda: window.session.snapshot()["settings"].get("shannon_threshold_bits_per_byte") == 8 and
                 window.session.snapshot()["sample"].get("shannon_assessment") == "WARN" and
                 window.stat_labels["threshold"].get_text() == "8.000000" and
                 window.stat_labels["assessment"].get_text() == "WARN", "new acquisition uses the new threshold")
        restarted = window.session._child
        assert restarted is not child
        window.stop_button.emit("clicked")
        wait_for(context, lambda: not window.session.snapshot()["running"] and not window._stopping,
                 "threshold-adjusted client clean shutdown", timeout=5)
        assert restarted.poll() is not None and not window.session.snapshot()["forced_stop"]
        wait_for(context, lambda: window.options_button.get_sensitive(), "options ready for a maximum-size run")
        editor = _options(context, window)
        editor.set_value("slice", "33554432")
        editor.set_value("analysis-bytes", "0")
        editor.set_value("refresh", "1s")
        editor.set_value("duration", "10s")
        editor.apply_button.emit("clicked")
        wait_for(context, lambda: not editor.get_visible() and
                 window.descriptor["settings"]["payload_bytes"] == 33554432,
                 "server-maximum sample size accepted by the GUI")
        window.start_button.emit("clicked")
        try:
            wait_for(context, lambda: window.session.snapshot()["sample"].get("sample_bytes") == 33554432,
                     "full 32 MiB acquisition and analysis", timeout=15)
        except AssertionError as error:
            failure = {key: value for key, value in window.session.snapshot().items() if key != "rows"}
            failure["visible_status"] = window.status_label.get_text()
            (artifacts / "client-max-size-failure.json").write_text(json.dumps(failure, indent=2))
            raise AssertionError("Maximum-size acquisition failed: " + json.dumps(failure)) from error
        maximum = window.session.snapshot()
        assert maximum["sample"]["frame_bytes"] == 33554432 and maximum["sample"]["analysed_bytes"] >= 33554432
        assert len(window.model.snapshot()["rows"]) <= 128
        window.stop_button.emit("clicked")
        wait_for(context, lambda: not window.session.snapshot()["running"] and not window._stopping,
                 "maximum-size client clean shutdown", timeout=5)
        assert not window.session.snapshot()["forced_stop"]
        assert _live_status(remote_port) == 200, "Client settings or Stop affected the independent server"
        return {"validated_payload_bytes": 8192, "validated_threshold": 6.123456,
                "actual_sample_bytes": state["sample"]["sample_bytes"],
                "invalid_edits_preserve_last_configuration": True,
                "frozen_assessment_preserved_when_next_threshold_changes": True,
                "next_run_uses_new_threshold": True,
                "32_MiB_received_and_fully_analysed": True,
                "server_capabilities_and_performance_visible": True, "adaptive_pull_editor_and_acquisition": True,
                "saved_endpoint_credentials_and_query_hidden": True,
                "unmodified_query_and_advanced_flags_preserved": True,
                "editing_disabled_while_running": True, "screenshot": screenshot}
    finally:
        if window.get_visible():
            close_window(context, window)
        app.quit()


def _server_options(context, application_type, binary, remote_port, artifacts):
    old_port, local_port = free_port(), free_port()
    source = "windows-csprng" if sys.platform == "win32" else "macos-csprng" if sys.platform == "darwin" else "/dev/urandom"
    health_url = f"http://127.0.0.1:{remote_port}"
    arguments = [f"-http=127.0.0.1:{old_port}", "-enable-https=false", "-device=" + source,
                 "-words=3"]
    app, window = _window(context, application_type, "server", binary, arguments, "Server", health_url)
    try:
        assert window.session is None, "Opening the server GUI must not start a server"
        assert not _listener_open(old_port)
        window.health_connect_button.emit("clicked")
        wait_for(context, lambda: window.health_labels["state"].get_text() == "READY",
                 "independent server monitoring before local launch")

        editor = _options(context, window)
        editor.set_value("http", f"127.0.0.1:{local_port}")
        editor.set_value("enable-https", False)
        editor.set_value("device", source)
        editor.set_value("drbg", "chacha20")
        editor.set_value("reseed-ms", "1200")
        editor.set_value("max-bytes", "65536")
        screenshot = capture_window(context, editor, artifacts / "server-options.png")
        editor.apply_button.emit("clicked")
        wait_for(context, lambda: not editor.get_visible() and
                 window.descriptor["settings"]["http_address"] == f"127.0.0.1:{local_port}",
                 "server options validated by the CLI")
        settings = window.descriptor["settings"]
        assert settings["drbg"] == "chacha20" and settings["reseed_interval_ms"] == 1200, settings
        assert settings["max_bytes"] == 65536 and not window.descriptor["features"]["https"], settings
        assert "-words=3" in window.arguments, "Unexposed server CLI flag was dropped"
        assert window.session is None and not _listener_open(local_port), "Validation opened a listener"
        assert window.health_url_entry.get_text() == health_url, "Applying local settings replaced the monitored endpoint"

        window.start_button.emit("clicked")
        wait_for(context, lambda: window.session is not None and window.session.snapshot()["running"] and
                 _live_status(local_port) == 200 and not window.options_button.get_sensitive(),
                 "GUI-owned local server listener")
        child = window.session._child
        assert child.poll() is None
        assert _live_status(remote_port) == 200
        assert window.health_url_entry.get_text() == health_url
        state = window.session.snapshot()
        assert state["logs"], "Owned server output was not captured"

        window.stop_button.emit("clicked")
        wait_for(context, lambda: not window.session.snapshot()["running"] and
                 not window._stopping and window.options_button.get_sensitive() and
                 "stopped" in window.process_state_label.get_text().lower() and
                 not window.status_label.get_text().lower().startswith("stopping"),
                 "GUI-owned local server joined shutdown", timeout=5)
        assert child.poll() is not None and not window.session.snapshot()["forced_stop"]
        assert not _listener_open(local_port), "Owned server listener survived Stop"
        assert _live_status(remote_port) == 200, "Stop affected the independently started server"
        _final_server_drain_view(window)

        window.start_button.emit("clicked")
        wait_for(context, lambda: window.session.snapshot()["running"] and _live_status(local_port) == 200,
                 "GUI-owned local server restart")
        restarted = window.session._child
        assert restarted is not child
        close_window(context, window)
        assert restarted.poll() is not None
        assert not _listener_open(local_port), "Owned server listener survived GUI close"
        assert _live_status(remote_port) == 200, "GUI close affected the independently started server"
        return {"validated_drbg": "chacha20", "validated_reseed_ms": 1200,
                "validation_opens_no_listener": True, "start_stop_restart_close": True,
                "final_drain_same_revision_refreshes_status": True,
                "owned_logs_captured": True, "remote_health_target_preserved": True,
                "independent_server_stays_live": True, "screenshot": screenshot}
    finally:
        if window.get_visible():
            close_window(context, window)
        app.quit()


def _client_failures(context, application_type, binary, remote_port, artifacts):
    """Controlled transport faults leave the independent real server healthy."""
    class FailureHandler(http.server.BaseHTTPRequestHandler):
        def log_message(self, _format, *_args):
            pass

        def do_GET(self):
            if self.server.case == "truncated":
                self.send_response(200)
                self.send_header("Content-Length", str(32 << 20))
                self.end_headers()
                self.wfile.write(bytes(4096))
                self.close_connection = True
            else:
                body = (b"RDRAND backend stopped after hardware failure; restart required. "
                        b"Details https://operator:secret@example.org/diagnostic?token=private\n")
                self.send_response(503)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

    fixture = http.server.ThreadingHTTPServer(("127.0.0.1", 0), FailureHandler)
    thread = threading.Thread(target=fixture.serve_forever, daemon=True)
    thread.start()
    results = {}
    try:
        for case, cause in (("truncated", "unexpected EOF"), ("backend", "RDRAND backend stopped")):
            fixture.case = case
            app, window = _window(context, application_type, "client", binary,
                [f"-url=http://127.0.0.1:{fixture.server_address[1]}/v1/data/random", "-slice=33554432",
                 "-refresh=50ms", "-analysis-bytes=0", "-duration=10s", "-retries=0"], "Failure" + case)
            try:
                window.start_button.emit("clicked")
                wait_for(context, lambda: window.session is not None and not window.session.snapshot()["running"] and
                         cause in window.status_label.get_text() and window.client_log_expander.get_expanded(),
                         "visible client failure cause: " + case)
                state = window.session.snapshot()
                assert state["returncode"] and state["summary"]["failed"] and not state["forced_stop"]
                buffer = window.client_log_view.get_buffer()
                text = buffer.get_text(buffer.get_start_iter(), buffer.get_end_iter(), True)
                assert cause in text and "CLI output" in text
                assert "secret" not in text and "private" not in text
                assert cause in window.status_label.get_tooltip_text()
                assert _live_status(remote_port) == 200
                results[case] = {"actual_cause_visible": True, "diagnostics_opened": True,
                    "credentials_redacted": True, "real_server_still_healthy": True,
                    "controlled_fault": True,
                    "screenshot": capture_window(context, window, artifacts / ("client-failure-" + case + ".png"))}
            finally:
                if window.get_visible():
                    close_window(context, window)
                app.quit()
    finally:
        fixture.shutdown()
        fixture.server_close()
        thread.join(timeout=2)
    return results


def _server_features(context, application_type, binary, remote_port, artifacts):
    """Apply the same LAB/debug/cookie controls on all native GUI platforms."""
    assert not _listener_open(6060), "The fixed loopback debug port is already occupied"
    http_port, https_port = free_port(), free_port()
    source = "windows-csprng" if sys.platform == "win32" else "macos-csprng" if sys.platform == "darwin" else "/dev/urandom"
    with tempfile.TemporaryDirectory(prefix="entropy GUI features ") as temporary:
        work = Path(temporary)
        cert, key, registry = work / "cert.pem", work / "key.pem", work / "lab sources.json"
        subprocess.run([str(binary), "generate-cert", "-cert-file=" + str(cert), "-key-file=" + str(key),
                        "-hosts=localhost,127.0.0.1", "-valid-for=1h"], check=True, timeout=20,
                       stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        trust = ssl.create_default_context(cafile=str(cert))
        app, window = _window(context, application_type, "server", binary,
                              [f"-http=127.0.0.1:{http_port}", "-enable-https=false", "-device=" + source],
                              "Features", f"http://127.0.0.1:{remote_port}")

        def request(port, path, *, host=None, secure=False):
            connection = (http.client.HTTPSConnection("127.0.0.1", port, context=trust, timeout=2) if secure else
                          http.client.HTTPConnection("127.0.0.1", port, timeout=2))
            try:
                connection.request("GET", path, headers={"Host": host} if host else {})
                response = connection.getresponse()
                return response.status, response.read(65536), response.getheaders()
            finally:
                connection.close()

        def stop():
            child = window.session._child
            window.stop_button.emit("clicked")
            wait_for(context, lambda: not window.session.snapshot()["running"] and not window._stopping and
                     window.options_button.get_sensitive(),
                     "feature-enabled server joined shutdown", timeout=5)
            assert child.poll() is not None and not window.session.snapshot()["forced_stop"]
            assert not any(_listener_open(port) for port in (http_port, https_port, 6060))

        try:
            window.feature_options_button.emit("clicked")
            wait_for(context, lambda: window.settings_window is not None and window.settings_window.get_mapped(),
                     "Edit features opens the native options editor")
            editor = window.settings_window
            for name, value in {"enable-https": True, "https": f"127.0.0.1:{https_port}",
                                "cert-file": str(cert), "key-file": str(key), "cookie": True, "debug": True,
                                "lab-sources": str(registry), "lab-allow-remote": False,
                                "lab-allowed-hosts": "lab-gui.test", "lab-max-connections": "2",
                                "lab-memory-mib": "4"}.items():
                editor.set_value(name, value)
            screenshot = capture_window(context, editor, artifacts / "server-features-options.png")
            editor.apply_button.emit("clicked")
            assert not window.feature_options_button.get_sensitive()
            wait_for(context, lambda: not editor.get_visible() and window.feature_options_button.get_sensitive(),
                     "LAB/debug/cookie options validated")
            assert all(window.feature_checks[name].get_active() for name in ("https", "cookie", "debug", "lab_registry"))
            assert not window.feature_checks["lab_allow_remote"].get_active()
            assert not registry.exists(), "Startup inspection unexpectedly created/read a registry"
            assert not any(_listener_open(port) for port in (http_port, https_port, 6060))

            arguments = list(window.arguments)
            editor = _options(context, window)
            editor.set_value("lab-max-connections", "0")
            editor.apply_button.emit("clicked")
            wait_for(context, lambda: editor.apply_button.get_sensitive() and bool(editor.status_label.get_text()),
                     "invalid LAB limits rejected")
            assert "lab" in editor.status_label.get_text().lower()
            assert window.arguments == arguments and window.descriptor["settings"]["lab_max_connections"] == 2
            editor.close()
            wait_for(context, lambda: not editor.get_visible(), "invalid LAB edit closed")

            window.start_button.emit("clicked")
            wait_for(context, lambda: window.session is not None and not window.session.snapshot()["running"] and
                     "lab" in window.session.snapshot()["logs"].lower() and window.start_button.get_sensitive(),
                     "missing registry reported at actual startup", timeout=8)
            assert window.session._child.poll() is not None
            assert not any(_listener_open(port) for port in (http_port, https_port, 6060))
            registry.write_text(json.dumps({"sources": [{"id": "gui-http", "kind": "http", "max_bytes": 64,
                "endpoint": f"http://127.0.0.1:{remote_port}/v1/data/random?bytes=64"}]}))

            window.start_button.emit("clicked")
            wait_for(context, lambda: window.session.snapshot()["running"] and
                     all(_listener_open(port) for port in (http_port, https_port, 6060)),
                     "GUI starts public, TLS and local debug listeners", timeout=8)
            assert not window.feature_options_button.get_sensitive()
            status, body, _headers = request(http_port, "/lab/sources", host="lab-gui.test")
            assert status == 200 and json.loads(body)["sources"][0]["id"] == "gui-http", (status, body)
            assert request(http_port, "/lab/sources", host="outside.invalid")[0] == 403
            status, body, _headers = request(6060, "/debug/pprof/")
            assert status == 200 and b"/debug/pprof/" in body
            status, _body, headers = request(https_port, "/v1/meta/random?bytes=64&words=2&refresh=250&lang=it", secure=True)
            cookies = SimpleCookie()
            for name, value in headers:
                if name.lower() == "set-cookie":
                    cookies.load(value)
            assert status == 200 and len(cookies) == 1
            cookie = next(iter(cookies.values()))
            assert cookie["secure"] and cookie["httponly"], headers
            stop()

            editor = _options(context, window)
            for name, value in {"debug": False, "cookie": False, "lab-sources": "", "lab-allow-remote": True}.items():
                editor.set_value(name, value)
            editor.apply_button.emit("clicked")
            wait_for(context, lambda: not editor.get_visible() and not window.feature_checks["debug"].get_active(),
                     "optional features disabled for the next run")
            window.start_button.emit("clicked")
            wait_for(context, lambda: window.session.snapshot()["running"] and _live_status(http_port) == 200,
                     "server restarted with edited feature flags")
            assert not _listener_open(6060)
            status, body, _headers = request(http_port, "/lab/sources", host="outside.invalid")
            assert status == 200 and json.loads(body)["sources"] == [], (status, body)
            status, _body, headers = request(https_port, "/v1/meta/random?bytes=64", secure=True)
            assert status == 200 and not any(name.lower() == "set-cookie" for name, _value in headers)
            stop()
            assert _live_status(remote_port) == 200
            return {"LAB_registry_and_limits": True, "trusted_host_policy_and_remote_opt_in": True,
                    "HTTPS_secure_httponly_cookies": True, "debug_on_off_and_listener_cleanup": True,
                    "invalid_limit_keeps_last_valid_settings": True, "missing_registry_startup_recovers": True,
                    "validation_opens_no_files_or_listeners": True, "screenshot": screenshot}
        finally:
            if window.get_visible():
                close_window(context, window)
            app.quit()


def _client_performance_cadence(context, application_type, binary, remote_port):
    app, window = _window(context, application_type, "client", binary,
        [f"-url=http://127.0.0.1:{remote_port}/v1/data/random", "-slice=4096", "-refresh=50ms",
         "-analysis-interval=1h", "-duration=3s"], "PerformanceCadence")
    try:
        window.start_button.emit("clicked")
        wait_for(context, lambda: window.session is not None and
                 window.session.snapshot().get("performance", {}).get("received_frames", 0) >= 5,
                 "performance updates independently of statistical analysis")
        state = window.session.snapshot()
        assert state["performance"]["analysed_samples"] == 1 and len(state["rows"]) == 1
        assert state["sample"]["received_frames"] == 1
        wait_for(context, lambda: "average received" in window.performance_label.get_text(), "independent performance view")
        return {"rates_update_with_one_hour_analysis_interval": True, "sample_history_not_duplicated": True}
    finally:
        close_window(context, window)
        app.quit()


def _client_capability_limit(context, application_type, binary, artifacts):
    """A controlled public contract rejects oversize acquisition visibly."""
    class Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_args):
            pass

        def do_GET(self):
            if self.path == "/v1/capabilities":
                body = json.dumps({"schema_version": 1, "service": "entropy-service", "version": "fixture",
                    "max_bytes": 64, "transports": ["http", "websocket"],
                    "websocket_min_refresh_ms": 1, "websocket_max_refresh_ms": 60000}).encode()
            else:
                self.server.generation_requests += 1
                body = bytes(128)
            self.send_response(200)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

    fixture = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    fixture.generation_requests = 0
    thread = threading.Thread(target=fixture.serve_forever, daemon=True)
    thread.start()
    app = window = None
    try:
        app, window = _window(context, application_type, "client", binary,
            [f"-url=http://127.0.0.1:{fixture.server_address[1]}/v1/data/random", "-slice=128", "-duration=2s"], "CapabilityLimit")
        window.start_button.emit("clicked")
        wait_for(context, lambda: window.session is not None and not window.session.snapshot()["running"] and
                 "server maximum of 64" in window.status_label.get_text() and
                 "max 64 bytes" in window.performance_label.get_text(), "visible server capability size rejection")
        assert not window.model.snapshot()["sample"] and fixture.generation_requests == 0
        return {"discovered_operator_limit_visible": True, "oversize_stopped_before_acquisition": True,
                "screenshot": capture_window(context, window, artifacts / "client-capability-limit.png")}
    finally:
        if window is not None and window.get_visible():
            close_window(context, window)
        if app is not None:
            app.quit()
        fixture.shutdown()
        fixture.server_close()
        thread.join(timeout=2)


def controls_and_owned_server(context, application_type, server_binary, client_binary, remote_port, artifacts):
    """Validate edited options and exercise only the processes this GUI owns."""
    artifacts = Path(artifacts)
    artifacts.mkdir(parents=True, exist_ok=True)
    return {"client": _client_options(context, application_type, client_binary, remote_port, artifacts),
            "server": _server_options(context, application_type, server_binary, remote_port, artifacts),
            "performance_cadence": _client_performance_cadence(context, application_type, client_binary, remote_port),
            "capability_limit": _client_capability_limit(context, application_type, client_binary, artifacts),
            "client_failures": _client_failures(context, application_type, client_binary, remote_port, artifacts),
            "features": _server_features(context, application_type, server_binary, remote_port, artifacts)}
