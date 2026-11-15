"""Small native GTK 4 view over the existing command-line utilities.

The server view describes an invocation; it never starts or changes a server.
Only the client view can start a process, and it owns that process throughout.
"""
import sys
import threading

import cairo
import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Gdk", "4.0")
gi.require_version("Pango", "1.0")
from gi.repository import Gdk, Gio, GLib, Gtk, Pango

from .model import StreamModel
from .process import ClientSession, describe


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
    "lab_registry": "LAB source registry", "certificate_configured": "Certificate paths supplied",
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
.poc-content { padding: 20px; }
.poc-title { font-size: 20px; font-weight: bold; }
.poc-subtitle { opacity: 0.75; }
.poc-card { padding: 14px; border-radius: 10px; background: alpha(@theme_fg_color, 0.045); }
.poc-heading { font-weight: bold; margin-bottom: 8px; }
.poc-number { font-family: monospace; font-size: 20px; font-weight: bold; }
.poc-status { padding: 4px 0; }
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


class DesktopWindow(Gtk.ApplicationWindow):
    def __init__(self, application, role, binary, arguments):
        super().__init__(application=application)
        self.role, self.binary, self.arguments = role, binary, list(arguments)
        self.model = StreamModel(history_limit=HISTORY_ROWS)
        self.session = None
        self.descriptor = None
        self.feature_checks = {}
        self.stat_labels = {}
        self._fullscreen = False
        self._closing = False
        self._stopping = False
        self._was_running = False
        self._last_error = ""
        self._revision = -1
        self._surface = None
        self._pixels = None
        self._rows = ()
        self._palette = "byte"
        self.set_title(f"Entropy {role.title()} · Linux PoC")
        self.set_default_size(1100, 760)
        self.set_size_request(640, 480)
        self.connect("close-request", self._on_close)

        self.header = Gtk.HeaderBar()
        title = Gtk.Box(orientation=Gtk.Orientation.VERTICAL)
        title.append(label(f"Entropy {role.title()}", "poc-heading"))
        title.append(label("Linux desktop proof of concept", "poc-subtitle"))
        self.header.set_title_widget(title)
        self.set_titlebar(self.header)
        if role == "client":
            self.start_button = Gtk.Button(label="Start")
            self.start_button.set_sensitive(False)
            self.start_button.add_css_class("suggested-action")
            self.start_button.connect("clicked", lambda _button: self.start_client())
            self.header.pack_start(self.start_button)
            self.stop_button = Gtk.Button(label="Stop", sensitive=False)
            self.stop_button.connect("clicked", lambda _button: self.stop_client())
            self.header.pack_start(self.stop_button)
            fullscreen = Gtk.Button(icon_name="view-fullscreen-symbolic", tooltip_text="Fullscreen waterfall (F11)")
            fullscreen.connect("clicked", lambda _button: self.set_fullscreen(not self._fullscreen))
            self.header.pack_end(fullscreen)
        keys = Gtk.EventControllerKey()
        keys.connect("key-pressed", self._on_key)
        self.add_controller(keys)

        body = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=12)
        body.add_css_class("poc-content")
        self.set_child(body)
        self.status_label = label("Reading startup options…", "poc-status")
        self.status_label.set_lines(3)
        self.status_label.set_ellipsize(Pango.EllipsizeMode.END)
        if role == "server":
            body.append(label("Startup configuration", "poc-title"))
            body.append(label("Read only · requested options, not observed runtime health. No server is started or modified.", "poc-subtitle"))
            self.settings_box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=12)
            scroller = Gtk.ScrolledWindow(vexpand=True, hexpand=True)
            scroller.set_policy(Gtk.PolicyType.NEVER, Gtk.PolicyType.AUTOMATIC)
            scroller.set_child(self.settings_box)
            body.append(scroller)
        else:
            stats = Gtk.Grid(column_spacing=24, row_spacing=4)
            for column, (key, heading, initial) in enumerate((
                    ("shannon", "Shannon · bits/byte", "—"), ("threshold", "Threshold", "—"),
                    ("assessment", "Status", "WAIT"), ("throughput", "Avg received · MiB/s", "—"))):
                stats.attach(label(heading, "poc-subtitle"), column, 0, 1, 1)
                value = label(initial, "poc-number")
                value.set_hexpand(True)
                stats.attach(value, column, 1, 1, 1)
                self.stat_labels[key] = value
            body.append(stats)
            self.drawing_area = Gtk.DrawingArea(hexpand=True, vexpand=True)
            self.drawing_area.set_content_width(1)
            self.drawing_area.set_content_height(120)
            self.drawing_area.set_draw_func(self._draw_waterfall)
            overlay = Gtk.Overlay(hexpand=True, vexpand=True)
            overlay.set_child(self.drawing_area)
            self.placeholder = Gtk.Label(label="Start the client to view analysed sample prefixes")
            self.placeholder.set_can_target(False)
            overlay.add_overlay(self.placeholder)
            body.append(overlay)
            self.controls = Gtk.Box(spacing=10)
            self.controls.append(label("Waterfall"))
            self.palette_dropdown = Gtk.DropDown.new_from_strings(["Byte hue", "RGB triplets", "Grayscale"])
            self.palette_dropdown.connect("notify::selected", self._select_palette)
            self.controls.append(self.palette_dropdown)
            self.controls.append(label("Newest first · 256 bytes/sample · 128 rows · F11 / Escape", "poc-subtitle"))
            body.append(self.controls)
            self.settings_expander = Gtk.Expander(label="Startup options · read only")
            self.settings_box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=10)
            self.settings_box.append(label("Requested utility options. Start uses headless JSONL reporting for the GUI.", "poc-subtitle"))
            settings_scroller = Gtk.ScrolledWindow()
            settings_scroller.set_policy(Gtk.PolicyType.NEVER, Gtk.PolicyType.AUTOMATIC)
            settings_scroller.set_max_content_height(220)
            settings_scroller.set_propagate_natural_height(True)
            settings_scroller.set_child(self.settings_box)
            self.settings_expander.set_child(settings_scroller)
            body.append(self.settings_expander)
        body.append(self.status_label)
        self._tick_id = GLib.timeout_add(50, self._tick)
        # Inspection is bounded by describe(). Keep its cleanup alive if the
        # window closes before the short-lived description child has exited.
        self._inspection_thread = threading.Thread(target=self._inspect, name="entropy-gui-inspection")
        self._inspection_thread.start()

    def _inspect(self):
        try:
            result = describe(self.binary, self.arguments, self.role)
        except (OSError, RuntimeError, ValueError) as error:
            if not self._closing:
                GLib.idle_add(self._inspection_failed, str(error))
        else:
            if not self._closing:
                GLib.idle_add(self._inspection_finished, result)

    def _inspection_failed(self, message):
        if not self._closing:
            self.status_label.set_text("Configuration could not be read: " + message)
        return GLib.SOURCE_REMOVE

    def _group(self, title, values):
        box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=7)
        box.add_css_class("poc-card")
        box.append(label(title, "poc-heading"))
        grid = Gtk.Grid(column_spacing=22, row_spacing=8)
        for row, (key, value) in enumerate(values):
            grid.attach(label(LABELS.get(key, key.replace("_", " ").capitalize())), 0, row, 1, 1)
            content = label(str(value))
            content.set_selectable(True)
            content.set_hexpand(True)
            grid.attach(content, 1, row, 1, 1)
        box.append(grid)
        self.settings_box.append(box)

    def _inspection_finished(self, descriptor):
        if self._closing:
            return GLib.SOURCE_REMOVE
        self.descriptor = descriptor
        settings = descriptor["settings"]
        features = Gtk.FlowBox(selection_mode=Gtk.SelectionMode.NONE, max_children_per_line=3, row_spacing=6, column_spacing=16)
        for key, enabled in descriptor["features"].items():
            check = Gtk.CheckButton(label=LABELS.get(key, key.replace("_", " ").capitalize()), active=enabled, sensitive=False)
            check.set_tooltip_text("Configured value; this is not a runtime control or a health check")
            self.feature_checks[key] = check
            features.append(check)
        feature_box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=8)
        feature_box.add_css_class("poc-card")
        feature_box.append(label("Configured features · read only", "poc-heading"))
        feature_box.append(features)
        if self.role == "server":
            self.settings_box.append(feature_box)
            groups = [("DRBG & seed configuration", ("drbg", "seed_source", "reseed_failure_policy", "reseed_interval_ms", "max_reseed_age_ms", "reseed_bytes", "source_timeout")),
                      ("Network configuration", ("http_address", "https_address"))]
        else:
            groups = [("Connection & sampling", ("mode", "endpoint", "payload_bytes", "refresh", "analysis_bytes", "analysis_interval", "shannon_threshold_bits_per_byte"))]
            self.stat_labels["threshold"].set_text(f"{settings.get('shannon_threshold_bits_per_byte', 0):.6f}")
            self.palette_dropdown.set_selected(("byte", "rgb", "gray").index(settings.get("waterfall_mode", "byte")))
            self.start_button.set_sensitive(True)
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
        self.status_label.set_text("Configuration read. Runtime DRBG, listener and certificate health have not been checked." if self.role == "server" else "Ready · Start launches an owned client process. No raw export or device writes are enabled by this GUI.")
        return GLib.SOURCE_REMOVE

    def start_client(self):
        if self.role != "client" or self.descriptor is None or self._closing or self._stopping:
            return
        if self.session and self.session.snapshot()["running"]:
            return
        try:
            self.session = ClientSession(self.binary, self.arguments, self.model)
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
        self.status_label.set_text("Connecting · waiting for the first analysed sample…")

    def stop_client(self):
        if not self.session or self._stopping:
            return
        self._stopping = True
        self.stop_button.set_sensitive(False)
        self.status_label.set_text("Stopping client…")
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
        self.start_button.set_sensitive(not state["running"] and not self._stopping)
        self.stop_button.set_sensitive(state["running"] and not self._stopping)
        if state["revision"] != self._revision:
            self._revision = state["revision"]
            sample, settings, summary = state["sample"], state["settings"], state["summary"]
            if settings:
                self.stat_labels["threshold"].set_text(f"{settings['shannon_threshold_bits_per_byte']:.6f}")
            if sample:
                self.stat_labels["shannon"].set_text(f"{sample['shannon_bits_per_byte']:.6f}")
                assessment = sample["shannon_assessment"]
                self.stat_labels["assessment"].set_text(assessment)
                for name in ("status-ok", "status-warn"):
                    self.stat_labels["assessment"].remove_css_class(name)
                self.stat_labels["assessment"].add_css_class("status-ok" if assessment == "OK" else "status-warn")
                elapsed = sample.get("elapsed_ms", 0) / 1000
                rate = sample.get("received_bytes", 0) / (1 << 20) / elapsed if elapsed > 0 else 0
                self.stat_labels["throughput"].set_text(f"{rate:.3f}")
            if summary:
                self.stat_labels["throughput"].set_text(f"{summary.get('received_mib_per_second', 0):.3f}")
            if state["rows"] != self._rows:
                self._rows = state["rows"]
                self.placeholder.set_visible(not self._rows)
                self._make_surface()
                self.drawing_area.queue_draw()
        if not self._stopping:
            if state["error"]:
                self.status_label.set_text("Client error: " + state["error"])
            elif not state["running"]:
                final = state["summary"].get("termination", "process exited")
                detail = state["stderr"].strip().splitlines()
                self.status_label.set_text(f"Stopped · {final}" + (" · " + detail[-1] if state["returncode"] and detail else ""))
            elif state["sample"]:
                sample = state["sample"]
                self.status_label.set_text(f"Receiving · {sample.get('received_frames', 0):,} frames · {sample.get('received_bytes', 0) / (1 << 20):.2f} MiB · statistics cover the latest analysed sample")
        return GLib.SOURCE_CONTINUE

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
        self._fullscreen = bool(enabled)
        self.header.set_visible(not enabled)
        self.settings_expander.set_visible(not enabled)
        self.controls.set_visible(not enabled)
        self.status_label.set_visible(not enabled)
        self.fullscreen() if enabled else self.unfullscreen()

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
        if self.session and self.session.snapshot()["running"]:
            self.stop_client()
            return True
        self._finish_close()
        return True

    def _finish_close(self):
        if self._tick_id:
            GLib.source_remove(self._tick_id)
            self._tick_id = None
        self.destroy()


class DesktopApplication(Gtk.Application):
    def __init__(self, role, binary, arguments):
        super().__init__(application_id="org.entropyservice.LinuxPoC", flags=Gio.ApplicationFlags.NON_UNIQUE)
        self.role, self.binary, self.arguments = role, binary, list(arguments)
        self.window = None

    def do_activate(self):
        provider = Gtk.CssProvider()
        provider.load_from_data(CSS)
        Gtk.StyleContext.add_provider_for_display(Gdk.Display.get_default(), provider, Gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
        if self.window is None:
            self.window = DesktopWindow(self, self.role, self.binary, self.arguments)
        self.window.present()


def run(role, binary, arguments):
    if not Gtk.init_check() or Gdk.Display.get_default() is None:
        print("No graphical display is available. Launch entropy-gui from a Linux desktop session.", file=sys.stderr)
        return 2
    return DesktopApplication(role, binary, arguments).run([])
