"""Native startup-option editing without spawning or configuring a process."""
import sys

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Gdk", "4.0")
gi.require_version("Pango", "1.0")
from gi.repository import Gdk, Gtk, Pango

from .model import safe_endpoint, safe_text
from .options import edited_arguments, fields_for, initial_values


_ENDPOINT_NOTICE = (
    "Saved endpoint credentials, query parameters and fragments are hidden. "
    "Leaving an endpoint unchanged preserves its full URL; editing it replaces the complete URL."
)


def _label(text, *, dim=False):
    widget = Gtk.Label(label=text, xalign=0, wrap=True)
    if dim:
        widget.add_css_class("dim-label")
    return widget


class SettingsWindow(Gtk.Window):
    """Edit allowlisted options; the parent owns asynchronous CLI validation.

    Only values changed in this editor are sent to ``edited_arguments``.
    Redacted descriptions are consequently never used to rewrite an unchanged
    URL, and unexposed command-line options keep their original spelling/order.
    """

    def __init__(self, parent, role, binary, arguments, descriptor, on_apply):
        fields = fields_for(role)
        initial = initial_values(role, list(arguments), descriptor)
        super().__init__(title=f"Entropy {role.title()} · Options", transient_for=parent,
                         modal=True, destroy_with_parent=True)
        self.role = role
        self._arguments = list(arguments)
        self._initial_binary = str(binary)
        self._fields = fields
        self._initial_values = initial
        # Compare edits with the displayed values, while retaining the original
        # arguments verbatim. Opening or resetting the editor must neither
        # disclose saved credentials nor remove hidden URL components.
        for name in ("url", "wsurl"):
            if name in self._initial_values:
                self._initial_values[name] = safe_endpoint(self._initial_values[name])
        self._on_apply = on_apply
        self._busy = False
        self._chooser = None
        self._choices = {}
        self.controls = {}
        self.set_default_size(720, 680)
        self.set_size_request(480, 400)
        self.connect("close-request", self._close_requested)
        self.connect("notify::visible", self._visibility_changed)

        header = Gtk.HeaderBar()
        header.set_title_widget(_label(f"{role.title()} options"))
        self.set_titlebar(header)

        body = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=12)
        for side in ("top", "bottom", "start", "end"):
            getattr(body, "set_margin_" + side)(16)
        self.set_child(body)
        body.append(_label("Edit startup options, then validate and apply before starting.", dim=True))
        if role == "client":
            body.append(_label(_ENDPOINT_NOTICE, dim=True))

        self.fields_box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=16)
        scroller = Gtk.ScrolledWindow(vexpand=True, hexpand=True)
        scroller.set_policy(Gtk.PolicyType.NEVER, Gtk.PolicyType.AUTOMATIC)
        scroller.set_child(self.fields_box)
        body.append(scroller)

        executable = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=6)
        executable.append(_label(f"Local {role} executable"))
        binary_row = Gtk.Box(spacing=6)
        self.binary_entry = Gtk.Entry(hexpand=True)
        self.binary_entry.set_text(self._initial_binary)
        self.binary_entry.set_tooltip_text("The local executable used for startup inspection and GUI-owned runs.")
        binary_row.append(self.binary_entry)
        self.browse_button = Gtk.Button(label="Browse…")
        self.browse_button.connect("clicked", lambda _button: self._browse(
            self.binary_entry, f"Select entropy-{role} executable", executable=True))
        binary_row.append(self.browse_button)
        executable.append(binary_row)
        self.fields_box.append(executable)

        groups = {}
        for field in self._fields:
            if field.group not in groups:
                group = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=8)
                heading = _label(field.group)
                heading.add_css_class("heading")
                group.append(heading)
                grid = Gtk.Grid(column_spacing=14, row_spacing=10, hexpand=True)
                group.append(grid)
                self.fields_box.append(group)
                groups[field.group] = [grid, 0]
            grid, row = groups[field.group]
            groups[field.group][1] += 1
            if field.kind == "bool":
                control = Gtk.CheckButton(label=field.label)
                grid.attach(control, 0, row, 2, 1)
            else:
                caption = _label(field.label)
                caption.set_max_width_chars(24)
                grid.attach(caption, 0, row, 1, 1)
                if field.kind == "choice":
                    choices = list(field.choices)
                    current = str(self._initial_values[field.name])
                    if current not in choices:
                        choices.append(current)
                    self._choices[field.name] = choices
                    control = Gtk.DropDown.new_from_strings(choices)
                    control.set_hexpand(True)
                    grid.attach(control, 1, row, 1, 1)
                else:
                    control = Gtk.Entry(hexpand=True)
                    if field.kind == "path":
                        path_row = Gtk.Box(spacing=6)
                        path_row.append(control)
                        browse = Gtk.Button(label="Browse…")
                        browse.connect("clicked", lambda _button, entry=control, title=field.label:
                                       self._browse(entry, "Select " + title.lower()))
                        path_row.append(browse)
                        grid.attach(path_row, 1, row, 1, 1)
                    else:
                        grid.attach(control, 1, row, 1, 1)
                caption.set_mnemonic_widget(control)
            hint = field.hint
            if field.name in ("url", "wsurl"):
                hint = (hint + "\n" if hint else "") + _ENDPOINT_NOTICE
            if hint:
                control.set_tooltip_text(hint)
            self.controls[field.name] = control
            self.set_value(field.name, self._initial_values[field.name])

        self.status_label = _label("")
        self.status_label.set_selectable(True)
        self.status_label.set_lines(4)
        self.status_label.set_ellipsize(Pango.EllipsizeMode.END)
        body.append(self.status_label)

        buttons = Gtk.Box(spacing=8)
        self.close_button = Gtk.Button(label="Close")
        self.close_button.connect("clicked", lambda _button: self.close())
        buttons.append(self.close_button)
        self.reset_button = Gtk.Button(label="Reset changes")
        self.reset_button.connect("clicked", self._reset)
        buttons.append(self.reset_button)
        buttons.append(Gtk.Box(hexpand=True))
        self.apply_button = Gtk.Button(label="Validate and apply")
        self.apply_button.add_css_class("suggested-action")
        self.apply_button.connect("clicked", self._submit)
        buttons.append(self.apply_button)
        body.append(buttons)

        keys = Gtk.EventControllerKey()
        keys.connect("key-pressed", self._key_pressed)
        self.add_controller(keys)

    def get_value(self, name):
        control = self.controls[name]
        kind = next(field.kind for field in self._fields if field.name == name)
        if kind == "bool":
            return control.get_active()
        if kind == "choice":
            selected = control.get_selected()
            if selected >= len(self._choices[name]):
                raise ValueError("Choose a value for " + name)
            return self._choices[name][selected]
        return control.get_text()

    def set_value(self, name, value):
        control = self.controls[name]
        kind = next(field.kind for field in self._fields if field.name == name)
        if kind == "bool":
            if type(value) is not bool:
                raise ValueError("Boolean options require true or false")
            control.set_active(value)
        elif kind == "choice":
            value = str(value)
            if value not in self._choices[name]:
                self._choices[name].append(value)
                control.get_model().append(value)
            control.set_selected(self._choices[name].index(value))
        else:
            control.set_text(str(value))

    def candidate_arguments(self):
        values = {name: self.get_value(name) for name in self.controls}
        changed = {name: value for name, value in values.items() if value != self._initial_values[name]}
        return edited_arguments(self.role, self._arguments, changed)

    def set_busy(self, busy):
        self._busy = bool(busy)
        if self._busy:
            self._dispose_chooser()
            self.set_focus(None)
        self.fields_box.set_sensitive(not self._busy)
        for button in (self.apply_button, self.reset_button, self.close_button):
            button.set_sensitive(not self._busy)
        self.set_deletable(not self._busy)
        if self._busy:
            self.status_label.remove_css_class("error")
            self.status_label.set_text("Validating startup options…")
            self.status_label.set_tooltip_text(None)

    def set_error(self, message):
        self.set_busy(False)
        text = safe_text(str(message))
        self.status_label.add_css_class("error")
        self.status_label.set_text(text)
        self.status_label.set_tooltip_text(text)

    def _reset(self, _button=None):
        if self._busy:
            return
        self.binary_entry.set_text(self._initial_binary)
        for name, value in self._initial_values.items():
            self.set_value(name, value)
        self.status_label.remove_css_class("error")
        self.status_label.set_text("")
        self.status_label.set_tooltip_text(None)

    def _submit(self, _button=None):
        if self._busy:
            return
        try:
            binary = self.binary_entry.get_text()
            if not binary.strip():
                raise ValueError(f"Select the local entropy-{self.role} executable.")
            arguments = self.candidate_arguments()
            self.set_busy(True)
            self._on_apply(self, binary, arguments)
        except (OSError, RuntimeError, ValueError) as error:
            self.set_error(error)

    def _browse(self, entry, title, *, executable=False):
        if self._busy:
            return
        if self._chooser is not None:
            self._chooser.show()
            return
        chooser = Gtk.FileChooserNative.new(title, self, Gtk.FileChooserAction.OPEN, "Select", "Cancel")
        chooser.set_modal(True)
        if executable and sys.platform == "win32":
            files = Gtk.FileFilter()
            files.set_name("Windows executables (*.exe)")
            files.add_pattern("*.exe")
            files.add_pattern("*.EXE")
            chooser.add_filter(files)
        self._chooser = chooser

        def selected(dialog, response):
            try:
                if response == Gtk.ResponseType.ACCEPT:
                    chosen = dialog.get_file()
                    path = chosen.get_path() if chosen is not None else None
                    if path is None:
                        self.set_error("Select a local file.")
                    else:
                        entry.set_text(path)
            finally:
                if self._chooser is dialog:
                    self._chooser = None
                dialog.destroy()

        chooser.connect("response", selected)
        chooser.show()

    def _dispose_chooser(self):
        chooser, self._chooser = self._chooser, None
        if chooser is not None:
            chooser.destroy()

    def _close_requested(self, _window):
        if self._busy:
            return True
        self._dispose_chooser()
        return False

    def _visibility_changed(self, _window, _property):
        if not self.get_visible():
            self._dispose_chooser()

    def _key_pressed(self, _controller, keyval, _keycode, _state):
        if keyval == Gdk.KEY_Escape:
            if not self._busy:
                self.close()
            return True
        return False
