#!/usr/bin/env python3
"""Native settings-editor and owned-server checks used by the GTK suite.

The parent suite supplies real companion binaries and an independently running
server. This module deliberately has no standalone fixtures or GUI imports at
module load, so the core unittest discovery can import it without GTK.
"""
import http.client
from pathlib import Path
import socket
import sys

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
        assert _live_status(remote_port) == 200, "Client settings or Stop affected the independent server"
        return {"validated_payload_bytes": 8192, "validated_threshold": 6.123456,
                "actual_sample_bytes": state["sample"]["sample_bytes"],
                "invalid_edits_preserve_last_configuration": True,
                "frozen_assessment_preserved_when_next_threshold_changes": True,
                "next_run_uses_new_threshold": True,
                "saved_endpoint_credentials_and_query_hidden": True,
                "unmodified_query_and_advanced_flags_preserved": True,
                "editing_disabled_while_running": True, "screenshot": screenshot}
    finally:
        if window.get_visible():
            close_window(context, window)
        app.quit()


def _server_options(context, application_type, binary, remote_port, artifacts):
    old_port, local_port = free_port(), free_port()
    source = "windows-csprng" if sys.platform == "win32" else "/dev/urandom"
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


def controls_and_owned_server(context, application_type, server_binary, client_binary, remote_port, artifacts):
    """Validate edited options and exercise only the processes this GUI owns."""
    artifacts = Path(artifacts)
    artifacts.mkdir(parents=True, exist_ok=True)
    return {"client": _client_options(context, application_type, client_binary, remote_port, artifacts),
            "server": _server_options(context, application_type, server_binary, remote_port, artifacts)}
