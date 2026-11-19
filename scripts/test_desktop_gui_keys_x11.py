"""Bounded native-key checks for the GTK window owned by the X11 test process.

XTest is a Linux CI dependency, not a product dependency. Other native backends
report that this input-injection qualification does not apply to them.
"""
import ctypes
import sys
import threading
import warnings


class OwnedX11Keyboard:
    def __init__(self, display, window_id):
        try:
            self.x11 = ctypes.CDLL("libX11.so.6")
            self.xtest = ctypes.CDLL("libXtst.so.6")
        except OSError as error:
            raise RuntimeError("Native X11 key qualification requires libX11 and libxtst6") from error
        pointer, integer, window = ctypes.c_void_p, ctypes.c_int, ctypes.c_ulong
        bindings = {
            "XOpenDisplay": ([ctypes.c_char_p], pointer),
            "XCloseDisplay": ([pointer], integer),
            "XStringToKeysym": ([ctypes.c_char_p], window),
            "XKeysymToKeycode": ([pointer, window], ctypes.c_ubyte),
            "XSetInputFocus": ([pointer, window, integer, window], integer),
            "XGetInputFocus": ([pointer, ctypes.POINTER(window), ctypes.POINTER(integer)], integer),
            "XGrabServer": ([pointer], integer), "XUngrabServer": ([pointer], integer),
            "XSync": ([pointer, integer], integer),
        }
        for name, (arguments, result) in bindings.items():
            function = getattr(self.x11, name)
            function.argtypes, function.restype = arguments, result
        self.xtest.XTestFakeKeyEvent.argtypes = [pointer, ctypes.c_uint, integer, window]
        self.xtest.XTestFakeKeyEvent.restype = integer
        self.xtest.XTestQueryExtension.argtypes = [pointer, *([ctypes.POINTER(integer)] * 4)]
        self.xtest.XTestQueryExtension.restype = integer
        self.connection = self.x11.XOpenDisplay(display.get_name().encode())
        assert self.connection, "Could not connect to this GTK window's X11 display"
        self.window_id = window_id

    def close(self):
        self.x11.XCloseDisplay(self.connection)

    def key(self, name):
        assert threading.current_thread() is threading.main_thread()
        code = self.x11.XKeysymToKeycode(self.connection, self.x11.XStringToKeysym(name.encode()))
        assert code, name
        # Keep focus selection and injection in one server batch. No other X11
        # client can move focus to an unrelated window between these requests.
        self.x11.XGrabServer(self.connection)
        try:
            self.x11.XSetInputFocus(self.connection, self.window_id, 2, 0)
            actual, revert = ctypes.c_ulong(), ctypes.c_int()
            self.x11.XGetInputFocus(self.connection, ctypes.byref(actual), ctypes.byref(revert))
            assert actual.value == self.window_id, "Refusing to inject keys outside the owned GTK surface"
            pressed = self.xtest.XTestFakeKeyEvent(self.connection, code, 1, 0)
            released = self.xtest.XTestFakeKeyEvent(self.connection, code, 0, 0)
            assert pressed and released, name
        finally:
            self.x11.XUngrabServer(self.connection)
            self.x11.XSync(self.connection, 0)


def focused_divider_keys(context, window, wait_for):
    """Real F11/Escape input must bypass a focused Paned's cancel binding."""
    backend = window.get_display().__gtype__.name
    if not sys.platform.startswith("linux") or backend != "GdkX11Display":
        return {"applicable": False, "backend": backend,
                "reason": "Native keyboard injection is qualified by the Linux X11/XTest job"}
    assert threading.current_thread() is threading.main_thread()
    assert window.get_mapped() and window.role == "client"
    import gi
    gi.require_version("GdkX11", "4.0")
    from gi.repository import GdkX11

    with warnings.catch_warnings():
        warnings.simplefilter("ignore", DeprecationWarning)
        owned_id = GdkX11.X11Surface.get_xid(window.get_surface())
    assert owned_id
    keyboard = OwnedX11Keyboard(window.get_display(), owned_id)
    rows = []

    def paint():
        clock = window.get_frame_clock()
        frame = clock.get_frame_counter()
        window.queue_draw()
        wait_for(context, lambda: clock.get_frame_counter() > frame, "native key layout paint", timeout=3)

    try:
        versions = [ctypes.c_int() for _ in range(4)]
        assert keyboard.xtest.XTestQueryExtension(keyboard.connection,
            *(ctypes.byref(value) for value in versions)), "XTest extension is unavailable"
        keyboard.x11.XSetInputFocus(keyboard.connection, owned_id, 2, 0)
        keyboard.x11.XSync(keyboard.connection, 0)
        paint()
        splitters = (window.workspace_paned, window.details_paned)
        for name, paned in zip(("workspace", "details"), splitters):
            assert paned.grab_focus()
            before_arrow = paned.get_position()
            keyboard.key("Down")
            wait_for(context, lambda: paned.get_position() != before_arrow,
                     name + " real windowed divider arrow", timeout=3)
            keyboard.key("Escape")
            wait_for(context, lambda: not paned.has_focus(), name + " native windowed Escape", timeout=3)
            assert not window._fullscreen
            paint()

            assert paned.grab_focus()
            saved = [splitter.get_position() for splitter in splitters]
            keyboard.key("F11")
            wait_for(context, lambda: window._fullscreen, name + " real F11 fullscreen", timeout=3)
            paint()
            keyboard.key("Escape")
            wait_for(context, lambda: not window._fullscreen and not window._restore_dividers,
                     name + " one real Escape exits fullscreen", timeout=5)
            paint()
            assert [splitter.get_position() for splitter in splitters] == saved, name
            rows.append({"divider": name, "real_f11_enters": True, "one_real_escape_exits": True,
                         "positions_restored": True, "windowed_arrow_and_escape": True})
        return {"applicable": True, "backend": backend, "owned_surface_only": True, "dividers": rows}
    finally:
        keyboard.close()
        window.start_button.grab_focus()
