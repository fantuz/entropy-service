"""Owned CLI processes and bounded pipe draining; no GTK or network client."""
import os
import re
import selectors
import subprocess
import sys
import threading
import time

from .model import MAX_RECORD_BYTES, ReportError, StreamModel, configuration, safe_text


MAX_STDERR_BYTES = 8192
_INSPECT_RESERVED = {"h", "help", "describe-json"}
_CLIENT_RESERVED = _INSPECT_RESERVED | {"headless", "report", "report-waterfall", "debug",
                                          "output", "output-device", "output-bytes", "writeout"}
_CONSOLE_LOCK = threading.Lock()
_WINDOWS_API = None


def _windows_api():
    """Load Win32 declarations lazily; Linux never imports Windows bindings."""
    global _WINDOWS_API
    if _WINDOWS_API is None:
        import ctypes
        from ctypes import wintypes

        kernel = ctypes.WinDLL("kernel32", use_last_error=True)
        kernel.PeekNamedPipe.argtypes = [wintypes.HANDLE, wintypes.LPVOID, wintypes.DWORD,
                                        wintypes.LPDWORD, wintypes.LPDWORD, wintypes.LPDWORD]
        kernel.PeekNamedPipe.restype = wintypes.BOOL
        kernel.GetConsoleProcessList.argtypes = [wintypes.LPDWORD, wintypes.DWORD]
        kernel.GetConsoleProcessList.restype = wintypes.DWORD
        kernel.AllocConsole.argtypes = []
        kernel.AllocConsole.restype = wintypes.BOOL
        kernel.GetConsoleWindow.argtypes = []
        kernel.GetConsoleWindow.restype = wintypes.HWND
        kernel.GenerateConsoleCtrlEvent.argtypes = [wintypes.DWORD, wintypes.DWORD]
        kernel.GenerateConsoleCtrlEvent.restype = wintypes.BOOL
        _WINDOWS_API = kernel
    return _WINDOWS_API


def _ensure_windows_console():
    """Keep an existing console, or create one private hidden console for us.

    A pythonw GUI can have no console. Targeted CTRL_BREAK requires the parent
    and child to share one. Never detach, hide or free an existing console, and
    keep an allocated console alive while any owned process might still use it.
    """
    import ctypes
    from ctypes import wintypes

    with _CONSOLE_LOCK:
        kernel = _windows_api()
        processes = (wintypes.DWORD * 1)()
        if kernel.GetConsoleProcessList(processes, 1):
            return True
        if not kernel.AllocConsole():
            return False
        window = kernel.GetConsoleWindow()
        if window:
            user = ctypes.WinDLL("user32", use_last_error=True)
            user.ShowWindow.argtypes = [wintypes.HWND, ctypes.c_int]
            user.ShowWindow.restype = wintypes.BOOL
            user.ShowWindow(window, 0)  # SW_HIDE: only the console just allocated.
        return True


def _arguments(args, reserved):
    result = list(args)
    if any(not isinstance(item, str) or "\x00" in item for item in result):
        raise ValueError("Arguments must be text without NUL bytes")
    for item in result:
        if item == "--":
            raise ValueError("The CLI argument terminator is not supported inside GUI options")
        if item.startswith("-") and item.lstrip("-").split("=", 1)[0] in reserved:
            raise ValueError("This option is managed by the GUI: " + item.split("=", 1)[0])
    return result


def spawn_owned(command, *, stdout=subprocess.PIPE, stderr=subprocess.PIPE):
    """Spawn a native utility without a shell or inherited interactive stdin."""
    options = {}
    if sys.platform == "win32":
        if str(command[0]).rstrip(" .").lower().endswith((".bat", ".cmd")):
            raise ValueError("Select a native executable, not a Windows command script")
        _ensure_windows_console()
        options["creationflags"] = subprocess.CREATE_NEW_PROCESS_GROUP
    return subprocess.Popen(command, stdin=subprocess.DEVNULL, stdout=stdout,
                            stderr=stderr, bufsize=0, close_fds=True, **options)


def _spawn_utility(binary, arguments, role):
    """Keep executable selection explicit and explain OS launch failures."""
    try:
        return spawn_owned([str(binary), *arguments])
    except OSError as error:
        path = safe_text(str(binary))
        detail = safe_text(str(error))
        raise RuntimeError(
            f'Could not launch selected {role} executable "{path}". '
            f"Check that file or pass --{role}-binary PATH before --. "
            "No configuration file is required. " + detail
        ) from error


def _close_pipes(child):
    for pipe in (child.stdout, child.stderr):
        pipe.close()


def _signal(child, *, kill=False):
    """Return whether a graceful signal was sent; never target another group."""
    try:
        if kill:
            child.kill()
            return False
        if sys.platform == "win32":
            # CREATE_NEW_PROCESS_GROUP makes this nonzero PID the group ID.
            # CTRL_BREAK targets that owned group, never group 0 (broadcast).
            if child.pid > 0 and _windows_api().GenerateConsoleCtrlEvent(1, child.pid):
                return True
            if child.poll() is not None:
                return True  # Natural exit raced the console event request.
            child.terminate()
            # TerminateProcess uses exit code 1. A clean exit code 0 here means
            # the utility finished before the fallback could terminate it.
            return child.poll() == 0
        child.terminate()
        return True
    except ProcessLookupError:
        return True  # Exiting between poll() and the signal is normal.


def stop_owned(child, timeout=1.5):
    """Reap only this child, using graceful interruption before a bounded kill.

    Return True if forced termination was necessary. A caller must not report
    that outcome as a clean graceful shutdown or invent a missing summary.
    """
    forced = False
    if child.poll() is None:
        forced = not _signal(child)
        try:
            result = child.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            forced = True
            _signal(child, kill=True)
            result = child.wait(timeout=.5)
        if result == 0:
            forced = False
    return forced


class _PipeReader:
    """One fair, bounded pump for both pipes; Windows selectors only do sockets."""

    def __init__(self, child):
        self.pipes = [child.stdout, child.stderr]
        self.selector = None
        if sys.platform != "win32":
            self.selector = selectors.DefaultSelector()
            for pipe in self.pipes:
                os.set_blocking(pipe.fileno(), False)
                self.selector.register(pipe, selectors.EVENT_READ)

    def __enter__(self):
        return self

    def __exit__(self, *_error):
        if self.selector:
            self.selector.close()

    def _windows_chunk(self, pipe):
        import ctypes
        from ctypes import wintypes
        import msvcrt

        available = wintypes.DWORD()
        handle = msvcrt.get_osfhandle(pipe.fileno())
        if not _windows_api().PeekNamedPipe(handle, None, 0, None, ctypes.byref(available), None):
            code = ctypes.get_last_error()
            if code in (109, 232, 233):  # BROKEN_PIPE, NO_DATA, PIPE_NOT_CONNECTED.
                return b""
            raise ctypes.WinError(code)
        if available.value:
            # This pump is the sole reader. Reading at most the bytes reported
            # available cannot block, even when the child is otherwise idle.
            return os.read(pipe.fileno(), min(4096, available.value))
        return None

    def read(self, timeout=.05):
        if self.selector:
            ready = [(key.fileobj, os.read(key.fd, 4096))
                     for key, _mask in self.selector.select(timeout)]
        else:
            deadline = time.monotonic() + timeout
            while True:
                ready = []
                for pipe in self.pipes:
                    chunk = self._windows_chunk(pipe)
                    if chunk is not None:
                        ready.append((pipe, chunk))
                if ready or time.monotonic() >= deadline:
                    break
                time.sleep(min(.01, max(0, deadline - time.monotonic())))
        for pipe, chunk in ready:
            if not chunk:
                self.pipes.remove(pipe)
                if self.selector:
                    self.selector.unregister(pipe)
        return ready


def describe(binary, args, role):
    """Inspect without starting listeners, devices or output; cap time and I/O."""
    if role not in ("server", "client"):
        raise ValueError("Unknown utility role")
    args = _arguments(args, _INSPECT_RESERVED)
    # Set the dry-run flag before user arguments: a stray positional argument
    # must not cause Go's flag parser to skip our protection at the end.
    child = _spawn_utility(binary, ["-describe-json=true", *args], role)
    output, errors = bytearray(), bytearray()
    deadline = time.monotonic() + 3
    try:
        with _PipeReader(child) as reader:
            while reader.pipes:
                if time.monotonic() > deadline:
                    raise RuntimeError("Configuration inspection timed out")
                ready = reader.read()
                if not ready and child.poll() is not None:
                    break
                for pipe, chunk in ready:
                    if not chunk:
                        continue
                    target = output if pipe is child.stdout else errors
                    target.extend(chunk)
                    if target is errors:
                        del errors[:-MAX_STDERR_BYTES]
                    elif len(output) > MAX_RECORD_BYTES:
                        raise RuntimeError("Configuration report exceeds 64 KiB")
        remaining = max(.01, deadline - time.monotonic())
        try:
            result = child.wait(timeout=remaining)
        except subprocess.TimeoutExpired as error:
            raise RuntimeError("Configuration inspection timed out") from error
        if result:
            detail = safe_text(errors.decode("utf-8", "replace")).strip()
            selected = f'Selected {role} executable "{safe_text(str(binary))}"'
            if re.search(r"flag provided but not defined: --?describe-json(?:\s|$)", detail):
                raise RuntimeError(
                    selected + " does not support -describe-json. "
                    "Use entropy-service v2.2.0 or newer, selected with "
                    f"--{role}-binary PATH before --. No configuration file is required."
                )
            raise RuntimeError(selected + ": " + (detail or "Startup inspection failed"))
        return configuration(bytes(output), role)
    finally:
        if child.poll() is None:
            _signal(child, kill=True)
        child.wait(timeout=1)
        _close_pipes(child)


class ClientSession:
    """One owned child, one pipe pump, and bounded snapshots for a UI timer."""

    def __init__(self, binary, args, model=None):
        self.binary = str(binary)
        self.args = _arguments(args, _CLIENT_RESERVED)
        self.model = model if model is not None else StreamModel()
        self._lock = threading.Lock()
        self._lifecycle = threading.Lock()
        self._child = None
        self._thread = None
        self._stderr = bytearray()
        self._stderr_partial = False
        self._error = ""
        self._revision = 0
        self._stopping = False
        self._forced_stop = False

    def start(self):
        if not self._lifecycle.acquire(blocking=False):
            raise RuntimeError("The previous client is still stopping")
        try:
            if self._thread and self._thread.is_alive():
                raise RuntimeError("The previous client is still stopping")
            child = _spawn_utility(self.binary,
                                   [*self.args, "-headless=true", "-report=-", "-report-waterfall=true"],
                                   "client")
            self.model.reset()
            with self._lock:
                self._child = child
                self._stderr.clear()
                self._stderr_partial = False
                self._error = ""
                self._stopping = False
                self._forced_stop = False
                self._revision += 1
            self._thread = threading.Thread(target=self._pump, args=(child,), daemon=True,
                                            name="entropy-gui-client-pipes")
            self._thread.start()
        finally:
            self._lifecycle.release()

    def _fail(self, message):
        with self._lock:
            if not self._error:
                self._error = safe_text(message)
                self._revision += 1

    def _forced(self):
        with self._lock:
            self._forced_stop = True
            message = "Client required forced termination; a clean shutdown summary is unavailable"
            if not self._error:
                self._error = message
            elif message not in self._error:
                self._error += "; " + message
            self._revision += 1

    def _pump(self, child):
        pending = bytearray()
        failed = False
        forced_requested = False
        failure_deadline = None
        exit_deadline = None
        try:
            with _PipeReader(child) as reader:
                while reader.pipes:
                    # A helper inheriting a pipe must not keep the pump alive
                    # after our owned client has exited. Drain its final small
                    # reports briefly, then close our descriptors.
                    now = time.monotonic()
                    if child.poll() is not None and exit_deadline is None:
                        exit_deadline = now + .2
                    if exit_deadline is not None and now >= exit_deadline:
                        break
                    if failure_deadline and time.monotonic() >= failure_deadline and child.poll() is None:
                        _signal(child, kill=True)
                        forced_requested = True
                    ready = reader.read()
                    if not ready and child.poll() is not None:
                        break
                    for pipe, chunk in ready:
                        if not chunk:
                            continue
                        if pipe is child.stderr:
                            with self._lock:
                                self._stderr.extend(chunk)
                                excess = len(self._stderr) - MAX_STDERR_BYTES
                                if excess > 0:
                                    self._stderr_partial = self._stderr[excess - 1] != 10
                                    del self._stderr[:excess]
                                self._revision += 1
                            continue
                        if failed:
                            continue
                        pending.extend(chunk)
                        while b"\n" in pending:
                            line, _, remainder = pending.partition(b"\n")
                            pending = bytearray(remainder)
                            try:
                                self.model.feed_line(bytes(line))
                            except ReportError as error:
                                self._fail(str(error))
                                failed = True
                                break
                        if len(pending) > MAX_RECORD_BYTES:
                            self._fail("Report exceeds the 64 KiB record limit")
                            failed = True
                        if failed:
                            pending.clear()
                            failure_deadline = time.monotonic() + 1
                            if child.poll() is None:
                                if not _signal(child):
                                    forced_requested = True
                if pending and not failed:
                    self._fail("Truncated JSONL report")
        except (OSError, ValueError) as error:
            self._fail("Client report pipe failed: " + str(error))
        finally:
            # A broken protocol must not leave an invisible acquisition running.
            if stop_owned(child, timeout=1):
                forced_requested = True
            result = child.wait(timeout=1)
            if forced_requested and result:
                self._forced()
            with self._lock:
                if result and not self._stopping and not self._error:
                    self._error = "entropy-client exited with status " + str(result)
                self._revision += 1
            _close_pipes(child)

    def stop(self):
        with self._lifecycle:
            with self._lock:
                child = self._child
                self._stopping = True
            if child and stop_owned(child):
                self._forced()
            if self._thread:
                self._thread.join(timeout=.5)
                if self._thread.is_alive():
                    self._fail("Client pipe cleanup did not complete")

    def snapshot(self):
        result = self.model.snapshot()
        with self._lock:
            child = self._child
            returncode = child.poll() if child else None
            # The last report must be drained before Start can reuse the model.
            # A dead process with a live pump is still part of this session.
            active = child and (returncode is None or self._thread and self._thread.is_alive())
            diagnostics = bytes(self._stderr)
            if self._stderr_partial:
                _fragment, _separator, diagnostics = diagnostics.partition(b"\n")
            if active:
                diagnostics = diagnostics[:diagnostics.rfind(b"\n") + 1]
            result.update(running=bool(active), process_running=bool(child and returncode is None),
                          stopping=bool(active and self._stopping), forced_stop=self._forced_stop,
                          returncode=returncode,
                          stderr=safe_text(diagnostics.decode("utf-8", "replace")), error=self._error)
            result["revision"] += self._revision
        return result
