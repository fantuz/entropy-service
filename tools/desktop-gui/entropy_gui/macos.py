"""Inspect GTK's own native window while AppKit animates fullscreen.

Only imported on macOS. No additional Python package or process is required.
The GTK surface must remain alive until its native transition has completed.
"""
import ctypes
import gi

gi.require_version("GdkMacos", "4.0")
from gi.repository import GdkMacos


_OBJC = ctypes.CDLL("/usr/lib/libobjc.A.dylib")
_OBJC.sel_registerName.argtypes = [ctypes.c_char_p]
_OBJC.sel_registerName.restype = ctypes.c_void_p
_TRANSITION = _OBJC.sel_registerName(b"inFullscreenTransition")
_RESPONDS = _OBJC.sel_registerName(b"respondsToSelector:")
_BOOL_MESSAGE = ctypes.CFUNCTYPE(ctypes.c_bool, ctypes.c_void_p, ctypes.c_void_p)(("objc_msgSend", _OBJC))
_RESPONDS_MESSAGE = ctypes.CFUNCTYPE(ctypes.c_bool, ctypes.c_void_p, ctypes.c_void_p,
                                    ctypes.c_void_p)(("objc_msgSend", _OBJC))


def fullscreen_transition_active(surface):
    """GTK supplies the native pointer; never inspect an already freed surface."""
    if surface is None:
        return False
    native = GdkMacos.MacosSurface.get_native_window(surface)
    if not native:
        return False
    # This guarded query reads GTK's transition state; it does not send window
    # commands or depend on private AppKit object layouts.
    if not _RESPONDS_MESSAGE(native, _RESPONDS, _TRANSITION):
        raise RuntimeError("This GTK macOS runtime cannot report fullscreen transitions")
    return bool(_BOOL_MESSAGE(native, _TRANSITION))
