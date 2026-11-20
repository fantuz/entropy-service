"""Small native GTK 4 view over the existing command-line utilities.

Each view can launch and stop its own utility using validated startup options.
Server health can also monitor an independently running local or remote server.
"""
import signal
import sys
import threading
from datetime import datetime
from urllib.parse import urlsplit

import cairo
import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Gdk", "4.0")
gi.require_version("Pango", "1.0")
from gi.repository import Gdk, Gio, GLib, GObject, Gtk, Pango

from .model import StreamModel, client_failure_text, performance_text, received_throughput, safe_text
from .process import ClientSession, describe
from .health import HealthSession
from .controls import SettingsWindow
from .server_process import ServerSession
from .options import initial_values
from .web import web_application_url


HISTORY_ROWS = 128
LABELS = {
    "drbg": "DRBG algorithm", "seed_source": "Requested seed source",
    "http_address": "HTTP listener", "https_address": "HTTPS listener",
    "reseed_interval_ms": "Reseed interval (ms)", "max_reseed_age_ms": "Maximum seed age (ms)",
    "reseed_bytes": "Reseed bytes", "reseed_failure_policy": "Reseed failure policy",
    "source_timeout": "Source read timeout", "max_bytes": "Maximum sample bytes",
    "mode": "Transport", "endpoint": "Endpoint", "refresh": "Request interval",
    "payload_bytes": "Bytes per response", "analysis_bytes": "Analysis prefix bytes",
    "analysis_interval": "Analysis interval", "display_interval": "Terminal display interval",
    "shannon_threshold_bits_per_byte": "Shannon threshold (bits/byte)",
    "duration": "Run duration", "retries": "Transport retries", "retry_delay": "Initial retry delay",
    "output_kind": "Export destination type", "output_limit_bytes": "Export limit (bytes)",
    "https": "HTTPS requested", "require_device": "Require requested device", "cpu_mix": "CPU mixing",
    "rdrand_mix": "RDRAND mixing", "debug": "Debug listener", "cookie": "Cookie preferences",
    "lab": "LAB browser and network tools", "lab_registry": "LAB registry configured", "certificate_configured": "Certificate paths supplied",
    "headless": "Headless CLI", "writeout": "Mix into system random device", "output": "Raw export",
    "report_waterfall": "Sample-prefix reporting", "report": "Statistics reporting",
    "waterfall": "Terminal waterfall", "matrix": "Terminal matrix",
    "lab_allow_remote": "LAB remote access allowed",
    "lab_max_connections": "LAB connections", "lab_memory_mib": "LAB memory (MiB)",
    "ws_max_connections": "WebSocket connections", "ws_memory_mib": "WebSocket memory (MiB)",
    "http_max_requests": "HTTP generation requests", "http_memory_mib": "HTTP memory (MiB)",
    "waterfall_mode": "Terminal waterfall mapping", "waterfall_columns": "Terminal columns (0 = automatic)",
    "waterfall_rows": "Terminal history rows (0 = automatic)", "color": "Terminal color policy",
    "layout": "Terminal layout",
}
CSS = b"""
.gui-content { padding: 20px; }
.gui-title { font-size: 20px; font-weight: bold; }
.gui-subtitle { opacity: 0.75; }
.gui-card { padding: 14px; border-radius: 10px; background: alpha(@theme_fg_color, 0.045); }
.gui-heading { font-weight: bold; margin-bottom: 8px; }
.gui-number { font-family: monospace; font-size: 20px; font-weight: bold; }
.gui-status { padding: 4px 0; }
.gui-workspace > separator { min-height: 10px; background: alpha(@theme_fg_color, 0.12); border-radius: 4px; }
.gui-workspace > separator:hover, .gui-workspace > separator:focus { background: alpha(@theme_fg_color, 0.3); }
.status-ok { color: #238636; }
.status-warn { color: #da3633; }
"""


def label(text, css=None):
    widget = Gtk.Label(label=text, xalign=0, wrap=True)
    if css:
        widget.add_css_class(css)
    return widget


def byte_hue(value):
    """Same vivid byte mapping as the terminal waterfall."""
    hue = value * 6
    rising = 36 + (hue % 256) * 206 // 255
    falling = 278 - rising
    return ((242, rising, 36), (falling, 242, 36), (36, 242, rising),
            (36, falling, 242), (rising, 36, 242), (242, 36, falling))[hue // 256]


BYTE_COLORS = tuple(byte_hue(value) for value in range(256))


def launch_web_application(target):
    """Use the desktop handler only after an explicit button activation."""
    return Gio.AppInfo.launch_default_for_uri(target, None)


def default_health_url(descriptor):
    """Suggest one configured endpoint without claiming that it is listening."""
    secure = descriptor["features"].get("https", False)
    address = descriptor["settings"].get("https_address" if secure else "http_address", "")
    if not isinstance(address, str):
        return ""
    try:
        target = urlsplit("//" + address)
        port = target.port
        if not port:
            return ""
        host = target.hostname or "127.0.0.1"
        host = {"0.0.0.0": "127.0.0.1", "::": "::1"}.get(host, host)
        if ":" in host:
            host = "[" + host + "]"
        return f"{'https' if secure else 'http'}://{host}:{port}"
    except ValueError:
        return ""


class DesktopWindow(Gtk.ApplicationWindow):
    def __init__(self, application, role, binary, arguments, health_url=None, health_ca=None):
        super().__init__(application=application)
        self.role, self.binary, self.arguments = role, binary, list(arguments)
        self.model = StreamModel(history_limit=HISTORY_ROWS)
        self.session = None
        self.descriptor = None
        self.settings_window = None
        self._inspection_busy = False
        self._inspection_serial = 0
        self._inspection_thread = None
        self.inspection_error_label = None
        self.feature_checks = {}
        self.feature_options_button = None
        self.client_log_expander = None
        self._client_log_text = ""
        self.stat_labels = {}
        self.health_session = None
        self.health_labels = {}
        self.health_ca = health_ca
        self._health_revision = -1
        self._fullscreen = False
        self._native_fullscreen_pending = None
        self._fullscreen_source = None
        self._closing = False
        self._destroyed = False
        self._stopping = False
        self._was_running = False
        self._last_error = ""
        self._revision = -1
        self._surface = None
        self._pixels = None
        self._rows = ()
        self._palette = "byte"
        self._pane_defaults_pending = True
        self._pane_default_geometry = None
        self._normal_pane_positions = None
        self._restore_dividers = False
        self._restore_geometry = None
        self.set_title(f"Entropy {role.title()}")
        self.set_default_size(1100, 760)
        self.set_size_request(640, 480)
        self.connect("close-request", self._on_close)

        self.header = Gtk.HeaderBar()
        title = Gtk.Box(orientation=Gtk.Orientation.VERTICAL)
        title.append(label(f"Entropy {role.title()}", "gui-heading"))
        title.append(label(f"{ {'win32': 'Windows', 'darwin': 'macOS'}.get(sys.platform, 'Linux') } desktop interface", "gui-subtitle"))
        self.header.set_title_widget(title)
        self.set_titlebar(self.header)
        self.start_button = Gtk.Button(label="Start", sensitive=False)
        self.start_button.add_css_class("suggested-action")
        self.start_button.set_tooltip_text(f"Start a local {role} owned by this window")
        self.start_button.connect("clicked", lambda _button: self.start_client() if role == "client" else self.start_server())
        self.header.pack_start(self.start_button)
        self.stop_button = Gtk.Button(label="Stop", sensitive=False)
        self.stop_button.set_tooltip_text(f"Stop only this window's local {role}")
        self.stop_button.connect("clicked", lambda _button: self.stop_client())
        self.header.pack_start(self.stop_button)
        self.options_button = Gtk.Button(label="Options…", sensitive=False)
        self.options_button.connect("clicked", lambda _button: self.open_options())
        self.header.pack_end(self.options_button)
        if role == "client":
            fullscreen = Gtk.Button(icon_name="view-fullscreen-symbolic", tooltip_text="Fullscreen waterfall (F11)")
            fullscreen.connect("clicked", lambda _button: self.set_fullscreen(not self._fullscreen))
            self.header.pack_end(fullscreen)
        keys = Gtk.EventControllerKey()
        # Fullscreen Escape must run before a focused divider's own cancel
        # shortcut. Other keys still reach GTK's native handle bindings.
        keys.set_propagation_phase(Gtk.PropagationPhase.CAPTURE)
        keys.connect("key-pressed", self._on_key)
        self.add_controller(keys)

        body = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=8 if role == "server" else 12)
        body.add_css_class("gui-content")
        self.set_child(body)
        self.status_label = label("Inspecting local startup options…", "gui-status")
        self.status_label.set_lines(3)
        self.status_label.set_ellipsize(Pango.EllipsizeMode.END)
        self.status_label.set_selectable(True)
        self.binary_label = label(f"Local {role} executable: {safe_text(str(binary))}", "gui-subtitle")
        self.binary_label.set_selectable(True)
        self.binary_label.set_lines(2)
        self.binary_label.set_ellipsize(Pango.EllipsizeMode.MIDDLE)
        self.binary_label.set_tooltip_text(safe_text(str(binary)))
        if role == "server":
            self._build_local_server_panel(body)
            health_box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL)
            self._build_health_panel(health_box, health_url)
            self.top_pane = self._pane_scroller(health_box)
            startup_box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=8)
            startup_box.append(label("Requested startup configuration", "gui-title"))
            startup_box.append(label("Options edits local startup settings. Start launches a new local server; Connect monitors the selected endpoint independently.", "gui-subtitle"))
            startup_box.append(self.binary_label)
            self.settings_box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=12)
            startup_box.append(self.settings_box)
            self.middle_pane = self._pane_scroller(startup_box)
            self.bottom_pane = self._pane_scroller(self.server_log_expander)
        else:
            stats = Gtk.Grid(column_spacing=24, row_spacing=4)
            for column, (key, heading, initial) in enumerate((
                    ("shannon", "Shannon · bits/byte", "—"), ("threshold", "Threshold", "—"),
                    ("assessment", "Status", "WAIT"), ("throughput", "Avg received · MiB/s", "—"))):
                stats.attach(label(heading, "gui-subtitle"), column, 0, 1, 1)
                value = label(initial, "gui-number")
                value.set_hexpand(True)
                stats.attach(value, column, 1, 1, 1)
                self.stat_labels[key] = value
            body.append(stats)
            self.performance_label = label("Server capabilities and performance appear after Start", "gui-subtitle")
            self.performance_label.set_selectable(True)
            self.performance_label.set_tooltip_text("Rates are averages over the measured run. Receive wait includes server pacing, generation and transfer; it is not network latency. Receive/output timings cover the latest processed frame; analysis covers the latest analysed sample.")
            self.performance_expander = Gtk.Expander(label="Connection & performance", expanded=True)
            self.performance_expander.set_child(self.performance_label)
            self.top_pane = self._pane_scroller(self.performance_expander)
            self.drawing_area = Gtk.DrawingArea(hexpand=True, vexpand=True)
            self.drawing_area.set_content_width(1)
            self.drawing_area.set_content_height(1)
            self.drawing_area.set_draw_func(self._draw_waterfall)
            overlay = Gtk.Overlay(hexpand=True, vexpand=True)
            overlay.set_child(self.drawing_area)
            self.placeholder = Gtk.Label(label="Start the client to view analysed sample prefixes")
            self.placeholder.set_can_target(False)
            overlay.add_overlay(self.placeholder)
            self.middle_pane = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=8, hexpand=True, vexpand=True)
            self.middle_pane.append(overlay)
            self.controls = Gtk.Box(spacing=10)
            self.controls.append(label("Waterfall"))
            self.palette_dropdown = Gtk.DropDown.new_from_strings(["Byte hue", "RGB triplets", "Grayscale"])
            self.palette_dropdown.connect("notify::selected", self._select_palette)
            self.controls.append(self.palette_dropdown)
            self.controls.append(label("Preview · 256 bytes/sample · 128 rows · F11 / Escape", "gui-subtitle"))
            self.middle_pane.append(self.controls)
            self.settings_expander = Gtk.Expander(label="Startup options · read only")
            self.settings_box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=10)
            self.settings_box.append(label("Options edits the next run. Start uses headless JSONL reporting for the GUI.", "gui-subtitle"))
            self.settings_box.append(self.binary_label)
            settings_scroller = Gtk.ScrolledWindow()
            settings_scroller.set_policy(Gtk.PolicyType.NEVER, Gtk.PolicyType.AUTOMATIC)
            settings_scroller.set_min_content_height(60)
            settings_scroller.set_vexpand(True)
            settings_scroller.set_child(self.settings_box)
            self.settings_expander.set_child(settings_scroller)
            self.settings_expander.connect("notify::expanded", lambda widget, _prop: widget.set_vexpand(widget.get_expanded()))
            details_box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=8, vexpand=True)
            details_box.append(self.settings_expander)
        if role == "client":
            self.client_log_view = Gtk.TextView(editable=False, monospace=True, cursor_visible=False)
            self.client_log_view.set_wrap_mode(Gtk.WrapMode.WORD_CHAR)
            diagnostics = Gtk.ScrolledWindow()
            diagnostics.set_policy(Gtk.PolicyType.AUTOMATIC, Gtk.PolicyType.AUTOMATIC)
            diagnostics.set_min_content_height(80)
            diagnostics.set_vexpand(True)
            diagnostics.set_child(self.client_log_view)
            self.client_log_expander = Gtk.Expander(label="Client diagnostics")
            self.client_log_expander.connect("notify::expanded", lambda widget, _prop: widget.set_vexpand(widget.get_expanded()))
            self.client_log_expander.set_child(diagnostics)
            details_box.append(self.client_log_expander)
            self.bottom_pane = self._pane_scroller(details_box)
        self.details_paned = self._vertical_split(self.middle_pane, self.bottom_pane,
            "Resize startup configuration and logs" if role == "server" else "Resize waterfall and startup/diagnostic details")
        self.workspace_paned = self._vertical_split(self.top_pane, self.details_paned,
            "Resize live health and startup configuration" if role == "server" else "Resize performance details and waterfall")
        # Keep the top band stable when the window grows; give new space to
        # configuration/waterfall while retaining a useful lower details band.
        self.workspace_paned.set_resize_start_child(False)
        self.details_paned.set_resize_end_child(False)
        body.append(self.workspace_paned)
        self.web_application_box = Gtk.Box(spacing=12)
        self.web_application_note = label("Web interface · graphs, LAB and service status", "gui-subtitle")
        self.web_application_note.set_hexpand(True)
        self.web_application_box.append(self.web_application_note)
        self.web_application_button = Gtk.Button(label="Open web application", sensitive=False)
        self.web_application_button.connect("clicked", lambda _button: self.open_web_application())
        self.web_application_box.append(self.web_application_button)
        body.append(self.web_application_box)
        if role == "server":
            self.health_url_entry.connect("changed", lambda _entry: self._update_web_application())
        self._update_web_application()
        body.append(self.status_label)
        self._tick_id = GLib.timeout_add(50, self._tick)
        # Inspection is bounded by describe(). Keep its cleanup alive if the
        # window closes before the short-lived description child has exited.
        self._start_inspection(self.binary, self.arguments)

    @staticmethod
    def _pane_scroller(child):
        scroller = Gtk.ScrolledWindow(hexpand=True, vexpand=True)
        scroller.set_policy(Gtk.PolicyType.NEVER, Gtk.PolicyType.AUTOMATIC)
        scroller.set_min_content_height(0)
        scroller.set_child(child)
        return scroller

    @staticmethod
    def _vertical_split(start, end, description):
        paned = Gtk.Paned(orientation=Gtk.Orientation.VERTICAL, hexpand=True, vexpand=True)
        paned.add_css_class("gui-workspace")
        paned.set_wide_handle(True)
        paned.set_shrink_start_child(True)
        paned.set_shrink_end_child(True)
        paned.set_start_child(start)
        paned.set_end_child(end)
        paned.set_focusable(True)
        help_text = "Drag up or down to resize. F8 focuses the divider; use arrow keys to adjust it."
        paned.update_property([Gtk.AccessibleProperty.LABEL, Gtk.AccessibleProperty.DESCRIPTION],
                              [description, help_text])
        # GtkPaned owns the native separator and its keyboard/accessibility
        # behavior. Give that handle a tooltip without covering child content.
        handle = paned.get_first_child()
        while handle is not None:
            if handle != start and handle != end:
                handle.set_tooltip_text(description + ". " + help_text)
            handle = handle.get_next_sibling()
        return paned

    def _maintain_pane_layout(self):
        if self._pane_defaults_pending and not self._fullscreen:
            height = self.workspace_paned.get_height()
            if height > 0:
                top_share, bottom_share = (.45, .12) if self.role == "server" else (.20, .14)
                geometry = (self.workspace_paned.get_width(), height,
                            self.details_paned.get_height(), self.workspace_paned.get_position())
                if geometry != self._pane_default_geometry:
                    self.workspace_paned.set_position(round(height * top_share))
                    self._pane_default_geometry = geometry
                    return
                # Set the inner divider only after its outer allocation has
                # settled; otherwise GTK preserves a stale lower-pane size.
                self.details_paned.set_position(max(0, self.details_paned.get_height() - round(height * bottom_share) - 10))
                self._pane_defaults_pending = False
                self._pane_default_geometry = None
        if (self._restore_dividers and not self._fullscreen and not self.is_fullscreen()
                and self._native_fullscreen_pending is None):
            if self._normal_pane_positions is not None:
                main, details = self._normal_pane_positions
                geometry = (self.workspace_paned.get_width(), self.workspace_paned.get_height(),
                            self.details_paned.get_height(), self.workspace_paned.get_position())
                # Outer allocation happens after set_position(). Restoring the
                # inner handle against its old height would move it again when
                # GTK preserves the lower child's size during the outer resize.
                if geometry != self._restore_geometry:
                    self.workspace_paned.set_position(main)
                    self._restore_geometry = geometry
                    return
                self.details_paned.set_position(details)
            self._restore_dividers = False
            self._restore_geometry = None

    def _web_application_target(self):
        try:
            if self.role == "server":
                endpoint = self.health_url_entry.get_text()
            elif self.descriptor is not None:
                values = initial_values("client", self.arguments, self.descriptor)
                endpoint = values["wsurl" if values["mode"] == "push" else "url"]
            else:
                return ""
            return web_application_url(endpoint)
        except ValueError:
            return ""

    def _update_web_application(self):
        target = self._web_application_target()
        self.web_application_button.set_sensitive(bool(target))
        self.web_application_button.set_tooltip_text(target or "Select a complete server endpoint without embedded credentials. Nothing opens automatically.")

    def open_web_application(self):
        target = self._web_application_target()
        if not target or self._closing:
            return
        try:
            if not launch_web_application(target):
                self.status_label.set_text("The web application could not be opened. Open the server homepage in your browser.")
        except GLib.Error:
            self.status_label.set_text("The web application could not be opened. Open the server homepage in your browser.")

    def _build_local_server_panel(self, body):
        box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=6)
        self.process_state_label = label("Local server · not started", "gui-heading")
        box.append(self.process_state_label)
        self.server_log_view = Gtk.TextView(editable=False, monospace=True, cursor_visible=False)
        self.server_log_view.set_wrap_mode(Gtk.WrapMode.WORD_CHAR)
        scroller = Gtk.ScrolledWindow(hexpand=True, vexpand=True)
        scroller.set_min_content_height(90)
        scroller.set_child(self.server_log_view)
        self.server_log_expander = Gtk.Expander(label="Local server logs", expanded=True, vexpand=True)
        self.server_log_expander.set_child(scroller)
        body.append(box)

    def open_options(self, focus_field=None):
        if self._closing or self._inspection_busy or self._stopping:
            return
        if self.session and self.session.snapshot()["running"]:
            return
        if self.settings_window is None:
            try:
                self.settings_window = SettingsWindow(self, self.role, str(self.binary), self.arguments,
                                                       self.descriptor, self._apply_settings)
            except ValueError as error:
                self.status_label.set_text("Options could not be opened: " + safe_text(str(error)))
                return
            self.settings_window.connect("close-request", self._settings_closed)
        self.settings_window.present()
        if focus_field in self.settings_window.controls:
            editor = self.settings_window
            attempts = 0
            def focus_option():
                nonlocal attempts
                attempts += 1
                if not editor.get_visible() or attempts >= 50:
                    return GLib.SOURCE_REMOVE
                return GLib.SOURCE_REMOVE if editor.reveal_control(focus_field) else GLib.SOURCE_CONTINUE
            GLib.timeout_add(20, focus_option)

    def _settings_closed(self, _window):
        self.settings_window = None
        return False

    def _apply_settings(self, editor, binary, arguments):
        if self._closing or self._inspection_busy or self._stopping or self.session and self.session.snapshot()["running"]:
            editor.set_error("Stop the owned process before applying startup options.")
            return
        if not binary.strip():
            editor.set_error("Select the local utility executable.")
            return
        self._start_inspection(binary, arguments, editor)

    def _build_health_panel(self, body, health_url):
        card = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=8)
        card.add_css_class("gui-card")
        heading = Gtk.Box(spacing=16)
        heading.append(label("Live server health", "gui-heading"))
        state = label("DISCONNECTED", "gui-heading")
        heading.append(state)
        self.health_labels["state"] = state
        card.append(heading)
        controls = Gtk.Box(spacing=8)
        self.health_url_entry = Gtk.Entry(hexpand=True)
        self.health_url_entry.set_placeholder_text("https://127.0.0.1:8443")
        self.health_url_entry.set_text(health_url or "")
        self.health_url_entry.set_tooltip_text("An existing server's base URL. Connect queries /live and /ready without changing its configuration.")
        self.health_url_entry.connect("activate", lambda _entry: self.connect_health())
        controls.append(self.health_url_entry)
        self.health_connect_button = Gtk.Button(label="Connect")
        self.health_connect_button.connect("clicked", lambda _button: self.connect_health())
        controls.append(self.health_connect_button)
        self.health_disconnect_button = Gtk.Button(label="Disconnect", sensitive=False)
        self.health_disconnect_button.connect("clicked", lambda _button: self.disconnect_health())
        controls.append(self.health_disconnect_button)
        card.append(controls)
        card.append(label("TLS verification uses the supplied CA file." if self.health_ca else "TLS verification uses system trust. Connect checks only the selected endpoint.", "gui-subtitle"))
        grid = Gtk.Grid(column_spacing=20, row_spacing=5)
        fields = (
            ("listener", "Selected listener"), ("live", "Process"),
            ("ready", "Readiness"), ("generation_allowed", "Generation"),
            ("rng_drbg", "Observed DRBG"), ("rng_source", "Observed seed source"),
            ("reseed_age_ms", "Seed age"), ("reseed_failures_total", "Reseed failures"),
        )
        for index, (key, caption) in enumerate(fields):
            row, column = divmod(index, 2)
            grid.attach(label(caption, "gui-subtitle"), column * 2, row, 1, 1)
            value = label("—")
            value.set_hexpand(True)
            value.set_selectable(True)
            value.set_max_width_chars(40)
            value.set_ellipsize(Pango.EllipsizeMode.END)
            grid.attach(value, column * 2 + 1, row, 1, 1)
            self.health_labels[key] = value
        card.append(grid)
        self.health_labels["checked_at"] = label("No live check has been made.", "gui-subtitle")
        self.health_labels["checked_at"].set_lines(2)
        self.health_labels["checked_at"].set_ellipsize(Pango.EllipsizeMode.END)
        card.append(self.health_labels["checked_at"])
        body.append(card)

    def connect_health(self):
        if self.role != "server" or self._closing:
            return
        if self.health_session:
            state = self.health_session.snapshot()
            if state["running"] or state.get("stopping"):
                return
        # Move focus out of GtkEntry's internal text widget before disabling
        # the URL control, including when Connect was activated with Enter.
        self.set_focus(None)
        try:
            self.health_session = HealthSession(self.health_url_entry.get_text().strip(), ca_file=self.health_ca)
            self.health_session.start()
        except (OSError, RuntimeError, ValueError) as error:
            self.health_session = None
            self._health_revision = -1
            self._show_health({"state": "invalid", "fresh": False, "listener": "unknown", "running": False,
                               "stopping": False, "health": {}, "live": None, "ready": None,
                               "checked_at": None, "error": str(error)})
            return
        self._health_revision = -1
        self._tick_health()

    def disconnect_health(self):
        if self.health_session:
            self.health_session.stop()
            self._health_revision = -1
            self._tick_health()

    def _tick_health(self):
        if self.health_session is None:
            return
        state = self.health_session.snapshot()
        active = state["running"] or state.get("stopping", False)
        self.health_url_entry.set_sensitive(not active)
        self.health_connect_button.set_sensitive(not active)
        self.health_disconnect_button.set_sensitive(state["running"])
        revision = (state["revision"], state["state"], state.get("fresh"), state.get("checked_at"))
        if revision != self._health_revision:
            self._health_revision = revision
            self._show_health(state)

    def _show_health(self, state):
        fresh = state.get("fresh", False)
        heading = self.health_labels["state"]
        heading.set_text(state["state"].replace("-", " ").upper())
        for css in ("status-ok", "status-warn"):
            heading.remove_css_class(css)
        if fresh and state["state"] == "ready":
            heading.add_css_class("status-ok")
        elif state["state"] not in ("disconnected", "checking"):
            heading.add_css_class("status-warn")
        listener = state.get("listener", "unknown")
        self.health_labels["listener"].set_text({"unknown": "Not checked", "reachable": "Reachable",
                                                "unreachable": "Unreachable", "tls-unverified": "TLS not trusted"}.get(listener, "Unknown"))
        health = state.get("health", {}) if fresh else {}
        self.health_labels["live"].set_text("ONLINE" if fresh and state.get("live") is True else "NOT OK" if fresh and state.get("live") is False else "—")
        self.health_labels["ready"].set_text("READY" if fresh and state.get("ready") is True else "DEGRADED" if fresh and state.get("ready") is False else "—")
        self.health_labels["generation_allowed"].set_text("Allowed" if health.get("generation_allowed") is True else "Paused" if health.get("generation_allowed") is False else "—")
        for key in ("rng_drbg", "rng_source", "reseed_failures_total"):
            self.health_labels[key].set_text(str(health[key]) if key in health else "—")
        age = health.get("reseed_age_ms")
        self.health_labels["reseed_age_ms"].set_text(f"{age:,} ms" if age is not None else "—")
        checked = state.get("checked_at")
        detail = "Checked " + datetime.fromtimestamp(checked).astimezone().strftime("%H:%M:%S %Z") if checked else {
            "disconnected": "Live monitoring is disconnected.",
            "checking": "Checking the selected endpoint…",
        }.get(state["state"], "No fresh live response is available.")
        if state.get("error"):
            detail += " · " + state["error"]
        self.health_labels["checked_at"].set_text(detail)

    def _start_inspection(self, binary, arguments, editor=None):
        self._inspection_busy = True
        self._inspection_serial += 1
        serial = self._inspection_serial
        self.options_button.set_sensitive(False)
        self.start_button.set_sensitive(False)
        self.status_label.set_text("Validating local startup options…")
        self._inspection_thread = threading.Thread(target=self._inspect,
            args=(binary, list(arguments), serial, editor), name="entropy-gui-inspection")
        self._inspection_thread.start()

    def _inspect(self, binary, arguments, serial, editor):
        try:
            result = describe(binary, arguments, self.role)
        except (OSError, RuntimeError, ValueError) as error:
            if not self._closing:
                GLib.idle_add(self._inspection_failed, str(error), serial, editor)
        else:
            if not self._closing:
                GLib.idle_add(self._inspection_finished, result, binary, arguments, serial, editor)

    def _inspection_failed(self, message, serial=None, editor=None):
        if not self._closing and (serial is None or serial == self._inspection_serial):
            self._inspection_busy = False
            self.options_button.set_sensitive(True)
            self.start_button.set_sensitive(self.descriptor is not None)
            if editor is not None:
                editor.set_error(message)
                self.status_label.set_text("Options were not applied. The last validated settings are retained.")
                return GLib.SOURCE_REMOVE
            self.inspection_error_label = label(safe_text(message), "gui-status")
            self.inspection_error_label.set_selectable(True)
            self.settings_box.append(self.inspection_error_label)
            if self.role == "server":
                self.status_label.set_text("Local startup settings unavailable. Live server health can still be checked independently.")
            else:
                self.settings_expander.set_expanded(True)
                self.status_label.set_text("Local startup settings unavailable. Select a compatible client executable to enable Start.")
        return GLib.SOURCE_REMOVE

    def _group(self, title, values):
        box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=7)
        box.add_css_class("gui-card")
        box.append(label(title, "gui-heading"))
        grid = Gtk.Grid(column_spacing=22, row_spacing=8)
        for row, (key, value) in enumerate(values):
            grid.attach(label(LABELS.get(key, key.replace("_", " ").capitalize())), 0, row, 1, 1)
            content = label(str(value))
            content.set_selectable(True)
            content.set_hexpand(True)
            grid.attach(content, 1, row, 1, 1)
        box.append(grid)
        self.settings_box.append(box)

    def _inspection_finished(self, descriptor, binary=None, arguments=None, serial=None, editor=None):
        if self._closing or serial is not None and serial != self._inspection_serial:
            return GLib.SOURCE_REMOVE
        previous_mode = self.descriptor["settings"].get("waterfall_mode") if self.descriptor else None
        if binary is not None:
            self.binary = binary
            self.arguments = list(arguments)
        self._inspection_busy = False
        self.descriptor = descriptor
        self.inspection_error_label = None
        self.binary_label.set_text(f"Local {self.role} executable: {safe_text(str(self.binary))}")
        self.binary_label.set_tooltip_text(safe_text(str(self.binary)))
        child = self.settings_box.get_first_child()
        while child is not None:
            self.settings_box.remove(child)
            child = self.settings_box.get_first_child()
        self.feature_checks.clear()
        if self.role == "client":
            self.settings_box.append(label("Validated startup options. Use Options to edit the next run.", "gui-subtitle"))
            self.settings_box.append(self.binary_label)
        settings = descriptor["settings"]
        features = Gtk.FlowBox(selection_mode=Gtk.SelectionMode.NONE, max_children_per_line=3, row_spacing=6, column_spacing=16)
        for key, enabled in descriptor["features"].items():
            if key == "external_ui_locales":
                continue  # Build metadata, not an editable startup option.
            check = Gtk.CheckButton(label=LABELS.get(key, key.replace("_", " ").capitalize()), active=enabled, sensitive=False)
            check.set_tooltip_text("Validated startup value. Use Options to edit the next run; live health is separate.")
            self.feature_checks[key] = check
            features.append(check)
        feature_box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=8)
        feature_box.add_css_class("gui-card")
        feature_heading = Gtk.Box(spacing=12)
        feature_title = label("Validated startup features", "gui-heading")
        feature_title.set_hexpand(True)
        feature_heading.append(feature_title)
        if self.role == "server":
            self.feature_options_button = Gtk.Button(label="Edit features…")
            self.feature_options_button.set_tooltip_text("Edit LAB, debug, cookies and other settings for the next local server run.")
            self.feature_options_button.connect("clicked", lambda _button: self.open_options("cookie"))
            self.options_button.bind_property("sensitive", self.feature_options_button, "sensitive",
                                             GObject.BindingFlags.SYNC_CREATE)
            feature_heading.append(self.feature_options_button)
        feature_box.append(feature_heading)
        feature_box.append(features)
        if self.role == "server":
            shortcuts = Gtk.FlowBox(selection_mode=Gtk.SelectionMode.NONE, max_children_per_line=2,
                                    min_children_per_line=1, column_spacing=6, row_spacing=6)
            self.lab_options_button = Gtk.Button(label="LAB sources…")
            self.debug_options_button = Gtk.Button(label="Debug / pprof…")
            for button, field in ((self.lab_options_button, "lab-sources"), (self.debug_options_button, "debug")):
                button.connect("clicked", lambda _button, field=field: self.open_options(field))
                self.options_button.bind_property("sensitive", button, "sensitive", GObject.BindingFlags.SYNC_CREATE)
                shortcuts.append(button)
            feature_box.append(shortcuts)
            feature_box.append(label("LAB browser tools are available on Linux, Windows and macOS. A registry adds server-side sources.", "gui-subtitle"))
            feature_box.append(label("Enable Debug / pprof for the next Start to listen at http://127.0.0.1:6060/debug/pprof/ on the server host.", "gui-subtitle"))
            self.settings_box.append(feature_box)
            if not self.health_url_entry.get_text().strip():
                self.health_url_entry.set_text(default_health_url(descriptor))
            groups = [("DRBG & seed configuration", ("drbg", "seed_source", "reseed_failure_policy", "reseed_interval_ms", "max_reseed_age_ms", "reseed_bytes", "source_timeout")),
                      ("Network configuration", ("http_address", "https_address"))]
        else:
            groups = [("Connection & sampling", ("mode", "endpoint", "payload_bytes", "refresh", "analysis_bytes", "analysis_interval", "shannon_threshold_bits_per_byte"))]
            if not self.model.snapshot()["sample"]:
                self.stat_labels["threshold"].set_text(f"{settings.get('shannon_threshold_bits_per_byte', 0):.6f}")
            mode = settings.get("waterfall_mode", "byte")
            if previous_mode is None or previous_mode != mode:
                self.palette_dropdown.set_selected(("byte", "rgb", "gray").index(mode))
        shown = set()
        for title, keys in groups:
            values = [(key, settings[key]) for key in keys if key in settings]
            shown.update(key for key, _value in values)
            if values:
                self._group(title, values)
        other = [(key, value) for key, value in settings.items() if key not in shown]
        if other:
            self._group("Limits & other startup options", other)
        if self.role == "client":
            self.settings_box.append(feature_box)
        self.start_button.set_sensitive(True)
        self.options_button.set_sensitive(True)
        self._update_web_application()
        if editor is not None:
            editor.destroy()
            self.settings_window = None
        self.status_label.set_text("Options validated. Start launches an owned local server; Connect checks the selected health URL independently." if self.role == "server" else "Ready · Start launches an owned client process. Options edits the next run.")
        return GLib.SOURCE_REMOVE

    def start_client(self):
        if self.role != "client" or self.descriptor is None or self._closing or self._stopping or self._inspection_busy:
            return
        if self.session and self.session.snapshot()["running"]:
            return
        try:
            self.session = ClientSession(self.binary, self.arguments, self.model,
                                         report_performance="report_performance" in self.descriptor["features"])
            self.session.start()
        except (OSError, RuntimeError, ValueError) as error:
            self.session = None
            self.status_label.set_text("Client could not start: " + str(error))
            return
        self._revision = -1
        self._was_running = True
        self._last_error = ""
        self._rows = ()
        self._surface = self._pixels = None
        for key in ("shannon", "throughput"):
            self.stat_labels[key].set_text("—")
        self.stat_labels["assessment"].set_text("WAIT")
        self.stat_labels["assessment"].remove_css_class("status-ok")
        self.stat_labels["assessment"].remove_css_class("status-warn")
        self.placeholder.set_visible(True)
        self.drawing_area.queue_draw()
        self.start_button.set_sensitive(False)
        self.stop_button.set_sensitive(True)
        self.options_button.set_sensitive(False)
        self.status_label.set_text("Connecting · waiting for the first analysed sample…")

    def start_server(self):
        if self.role != "server" or self.descriptor is None or self._closing or self._stopping or self._inspection_busy:
            return
        if self.session and self.session.snapshot()["running"]:
            return
        try:
            self.session = ServerSession(self.binary, self.arguments)
            self.session.start()
        except (OSError, RuntimeError, ValueError) as error:
            self.session = None
            self.status_label.set_text("Server could not start: " + safe_text(str(error)))
            return
        self._revision = -1
        self.start_button.set_sensitive(False)
        self.stop_button.set_sensitive(True)
        self.options_button.set_sensitive(False)
        self.process_state_label.set_text("Local server · starting")
        self.status_label.set_text("Starting the owned local server. Connect checks the selected endpoint independently.")

    def stop_server(self):
        self.stop_client()

    def stop_client(self):
        if not self.session or self._stopping:
            return
        self._stopping = True
        self.stop_button.set_sensitive(False)
        self.status_label.set_text(f"Stopping owned {self.role}…")
        def stop():
            try:
                self.session.stop()
            finally:
                GLib.idle_add(self._stopped)
        threading.Thread(target=stop, daemon=True).start()

    def _stopped(self):
        self._stopping = False
        if self._closing:
            self._finish_close()
        return GLib.SOURCE_REMOVE

    def _tick(self):
        if self._closing:
            return GLib.SOURCE_CONTINUE
        self._maintain_pane_layout()
        if self.role == "server":
            self._tick_health()
            self._tick_server()
            return GLib.SOURCE_CONTINUE
        if not self.session:
            return GLib.SOURCE_CONTINUE
        state = self.session.snapshot()
        ended = self._was_running and not state["running"]
        new_error = state["error"] and state["error"] != self._last_error
        if self._fullscreen and (ended or new_error):
            # A frozen final sample must not hide a transport/process outcome.
            # Keep its statistical assessment, but reveal the status message.
            self.set_fullscreen(False)
        self._was_running, self._last_error = state["running"], state["error"]
        diagnostic_text = state["stderr"]
        if state["error"]:
            diagnostic_text = client_failure_text(state["error"], state["stderr"]) + "\n\nCLI output:\n" + diagnostic_text
            self.client_log_expander.set_expanded(True)
        if diagnostic_text != self._client_log_text:
            self.client_log_view.get_buffer().set_text(diagnostic_text)
            self._client_log_text = diagnostic_text
        self.start_button.set_sensitive(not state["running"] and not self._stopping and not self._inspection_busy)
        self.stop_button.set_sensitive(state["running"] and not self._stopping)
        self.options_button.set_sensitive(not state["running"] and not self._stopping and not self._inspection_busy)
        if state["revision"] != self._revision:
            self._revision = state["revision"]
            self.performance_label.set_text(performance_text(state))
            sample, settings = state["sample"], state["settings"]
            if settings:
                self.stat_labels["threshold"].set_text(f"{settings['shannon_threshold_bits_per_byte']:.6f}")
            if sample:
                self.stat_labels["shannon"].set_text(f"{sample['shannon_bits_per_byte']:.6f}")
                assessment = sample["shannon_assessment"]
                self.stat_labels["assessment"].set_text(assessment)
                for name in ("status-ok", "status-warn"):
                    self.stat_labels["assessment"].remove_css_class(name)
                self.stat_labels["assessment"].add_css_class("status-ok" if assessment == "OK" else "status-warn")
            rate = received_throughput(state)
            self.stat_labels["throughput"].set_text("—" if rate is None else f"{rate:.3f}")
            if state["rows"] != self._rows:
                self._rows = state["rows"]
                self.placeholder.set_visible(not self._rows)
                self._make_surface()
                self.drawing_area.queue_draw()
        if not self._stopping:
            if state["error"]:
                message = "Client error: " + client_failure_text(state["error"], state["stderr"])
                self.status_label.set_text(message)
                self.status_label.set_tooltip_text(message)
            elif not state["running"]:
                self.status_label.set_tooltip_text(None)
                final = state["summary"].get("termination", "process exited")
                detail = state["stderr"].strip().splitlines()
                self.status_label.set_text(f"Stopped · {final}" + (" · " + detail[-1] if state["returncode"] and detail else ""))
            elif state["sample"]:
                self.status_label.set_tooltip_text(None)
                sample = state["sample"]
                self.status_label.set_text(f"Receiving · {sample.get('received_frames', 0):,} frames · {sample.get('received_bytes', 0) / (1 << 20):.2f} MiB · statistics cover the latest analysed sample")
        return GLib.SOURCE_CONTINUE

    def _tick_server(self):
        if self.session is None:
            return
        state = self.session.snapshot()
        self.start_button.set_sensitive(not state["running"] and not self._stopping and not self._inspection_busy)
        self.stop_button.set_sensitive(state["running"] and not self._stopping)
        self.options_button.set_sensitive(not state["running"] and not self._stopping and not self._inspection_busy)
        view_revision = (state["revision"], state["running"], state["returncode"], self._stopping)
        if view_revision != self._revision:
            self._revision = view_revision
            self.server_log_view.get_buffer().set_text(state["logs"])
            if state["running"]:
                self.process_state_label.set_text("Local server · running")
                if not self._stopping:
                    self.status_label.set_text("Owned local server is running. Use Connect to check the selected health URL.")
            else:
                self.process_state_label.set_text(f"Local server · stopped (exit {state['returncode']})")
                if state["error"]:
                    self.server_log_expander.set_expanded(True)
                if not self._stopping:
                    self.status_label.set_text("Server error: " + state["error"] if state["error"] else "Owned server stopped.")

    def _select_palette(self, dropdown, _property):
        self._palette = ("byte", "rgb", "gray")[dropdown.get_selected()]
        self._make_surface()
        self.drawing_area.queue_draw()

    def _make_surface(self):
        if not self._rows:
            self._surface = self._pixels = None
            return
        stride = 3 if self._palette == "rgb" else 1
        columns = max(1, max(len(row) // stride for row in self._rows))
        pixels = bytearray(columns * len(self._rows) * 4)
        for y, row in enumerate(self._rows):
            for x in range(len(row) // stride):
                if stride == 3:
                    red, green, blue = row[x * 3:x * 3 + 3]
                elif self._palette == "gray":
                    red = green = blue = row[x]
                else:
                    red, green, blue = BYTE_COLORS[row[x]]
                offset = (y * columns + x) * 4
                pixels[offset:offset + 4] = bytes((blue, green, red, 0)) if sys.byteorder == "little" else bytes((0, red, green, blue))
        self._pixels = pixels
        self._surface = cairo.ImageSurface.create_for_data(pixels, cairo.FORMAT_RGB24, columns, len(self._rows), columns * 4)

    def _draw_waterfall(self, _area, context, width, height):
        context.set_source_rgb(.035, .045, .055)
        context.paint()
        if self._surface:
            context.save()
            context.scale(width / self._surface.get_width(), height / HISTORY_ROWS)
            context.set_source_surface(self._surface, 0, 0)
            context.get_source().set_filter(cairo.FILTER_NEAREST)
            context.paint()
            context.restore()

    def set_fullscreen(self, enabled):
        if self.role != "client":
            return
        if enabled and not self._fullscreen:
            if not self._restore_dividers and not self._pane_defaults_pending:
                self._normal_pane_positions = (self.workspace_paned.get_position(), self.details_paned.get_position())
            self._restore_dividers = False
            self._restore_geometry = None
        elif not enabled and self._fullscreen:
            self._restore_dividers = True
            self._restore_geometry = None
        self._fullscreen = bool(enabled)
        self.header.set_visible(not enabled)
        self.top_pane.set_visible(not enabled)
        self.bottom_pane.set_visible(not enabled)
        self.controls.set_visible(not enabled)
        self.status_label.set_visible(not enabled)
        self.web_application_box.set_visible(not enabled)
        if sys.platform == "darwin":
            self._advance_macos_fullscreen()
        else:
            self.fullscreen() if enabled else self.unfullscreen()

    def _advance_macos_fullscreen(self):
        if self._destroyed:
            return
        from .macos import fullscreen_transition_active
        actual = self.is_fullscreen()
        pending = self._native_fullscreen_pending
        if pending is not None:
            if actual != pending or fullscreen_transition_active(self.get_surface()):
                self._queue_fullscreen_check()
                return
            self._native_fullscreen_pending = None
        elif fullscreen_transition_active(self.get_surface()):
            self._queue_fullscreen_check()
            return
        desired = False if self._closing else self._fullscreen
        if actual != desired:
            self._native_fullscreen_pending = desired
            self.fullscreen() if desired else self.unfullscreen()
            self._queue_fullscreen_check()
        elif self._closing:
            self._finish_close(native_ready=True)

    def _queue_fullscreen_check(self):
        if self._fullscreen_source is not None:
            return
        def advance():
            self._fullscreen_source = None
            self._advance_macos_fullscreen()
            return GLib.SOURCE_REMOVE
        self._fullscreen_source = GLib.timeout_add(50, advance)

    def _on_key(self, _controller, keyval, _keycode, _state):
        if keyval == Gdk.KEY_F11 and self.role == "client":
            self.set_fullscreen(not self._fullscreen)
            return True
        if keyval == Gdk.KEY_Escape and self._fullscreen:
            self.set_fullscreen(False)
            return True
        return False

    def _on_close(self, _window):
        self._closing = True
        if self.health_session:
            self.health_session.stop()
        if self.session and self.session.snapshot()["running"]:
            self.stop_client()
            return True
        self._finish_close()
        return True

    def _finish_close(self, *, native_ready=False):
        if self._destroyed:
            return
        if sys.platform == "darwin" and not native_ready:
            self._advance_macos_fullscreen()
            return
        if self._stopping or self.session and self.session.snapshot()["running"]:
            return
        self._destroyed = True
        if self._fullscreen_source is not None:
            GLib.source_remove(self._fullscreen_source)
            self._fullscreen_source = None
        if self.settings_window is not None:
            self.settings_window.destroy()
            self.settings_window = None
        if self._tick_id:
            GLib.source_remove(self._tick_id)
            self._tick_id = None
        self.set_focus(None)
        self.destroy()


class DesktopApplication(Gtk.Application):
    def __init__(self, role, binary, arguments, health_url=None, health_ca=None):
        super().__init__(application_id="org.entropyservice.Desktop", flags=Gio.ApplicationFlags.NON_UNIQUE)
        self.role, self.binary, self.arguments = role, binary, list(arguments)
        self.health_url, self.health_ca = health_url, health_ca
        self.window = None

    def do_activate(self):
        provider = Gtk.CssProvider()
        provider.load_from_data(CSS)
        Gtk.StyleContext.add_provider_for_display(Gdk.Display.get_default(), provider, Gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
        if self.window is None:
            self.window = DesktopWindow(self, self.role, self.binary, self.arguments, self.health_url, self.health_ca)
        self.window.present()

    def stop_owned_resources(self):
        """Finish owned work even if a signal has already stopped the GTK loop."""
        window = self.window
        if window is None:
            return
        window._closing = True
        try:
            if window.health_session:
                window.health_session.stop()
        finally:
            try:
                if window.session:
                    # Closing the event loop does not emit close-request. Join
                    # the existing bounded stop/drain path synchronously here;
                    # an idle callback cannot finish after run() has returned.
                    window.session.stop()
            finally:
                window._finish_close()
                window._inspection_thread.join(timeout=4)


def run(role, binary, arguments, health_url=None, health_ca=None):
    if not Gtk.init_check() or Gdk.Display.get_default() is None:
        print("No graphical display is available. Launch entropy-gui from a desktop session.", file=sys.stderr)
        return 2
    application = DesktopApplication(role, binary, arguments, health_url, health_ca)
    interrupted = False
    quit_source = None
    previous_handlers = {}

    def quit_after_interrupt():
        nonlocal quit_source
        quit_source = None
        if application.window is not None:
            application.window.close()
        else:
            application.quit()
        return GLib.SOURCE_REMOVE

    def interrupt(_signum, _frame):
        nonlocal interrupted, quit_source
        interrupted = True
        if quit_source is None:
            # Defer into GTK, including a signal arriving just before run().
            # PyGObject's default fallback re-raises KeyboardInterrupt after
            # quitting, without closing the owned client window first.
            quit_source = GLib.idle_add(quit_after_interrupt)

    try:
        signals = [signal.SIGINT]
        if sys.platform == "win32":
            signals.append(signal.SIGBREAK)
        for signum in signals:
            previous_handlers[signum] = signal.signal(signum, interrupt)
        try:
            result = application.run([])
        except KeyboardInterrupt:
            # Also cover an interrupt delivered by an embedding GI fallback.
            interrupted, result = True, 130
    finally:
        try:
            application.stop_owned_resources()
        finally:
            if quit_source is not None:
                GLib.source_remove(quit_source)
            for signum, handler in previous_handlers.items():
                signal.signal(signum, handler)
    return 130 if interrupted else result
