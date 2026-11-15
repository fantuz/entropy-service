"""Owned CLI processes and bounded pipe draining; no GTK or network client."""
import os
import selectors
import subprocess
import threading
import time

from .model import MAX_RECORD_BYTES, ReportError, StreamModel, configuration, safe_text


MAX_STDERR_BYTES = 8192
_INSPECT_RESERVED = {"h", "help", "describe-json"}
_CLIENT_RESERVED = _INSPECT_RESERVED | {"headless", "report", "report-waterfall", "debug",
                                          "output", "output-device", "output-bytes", "writeout"}


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


def _spawn(command):
    return subprocess.Popen(command, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, bufsize=0, close_fds=True)


def _close_pipes(child):
    for pipe in (child.stdout, child.stderr):
        pipe.close()


def _signal(child, *, kill=False):
    try:
        child.kill() if kill else child.terminate()
    except ProcessLookupError:
        pass  # Exiting between poll() and the signal is normal.


def describe(binary, args, role):
    """Inspect without starting listeners, devices or output; cap time and I/O."""
    if role not in ("server", "client"):
        raise ValueError("Unknown utility role")
    args = _arguments(args, _INSPECT_RESERVED)
    # Set the dry-run flag before user arguments: a stray positional argument
    # must not cause Go's flag parser to skip our protection at the end.
    child = _spawn([str(binary), "-describe-json=true", *args])
    output, errors = bytearray(), bytearray()
    deadline = time.monotonic() + 3
    try:
        with selectors.DefaultSelector() as selector:
            for pipe in (child.stdout, child.stderr):
                os.set_blocking(pipe.fileno(), False)
                selector.register(pipe, selectors.EVENT_READ)
            while selector.get_map():
                if time.monotonic() > deadline:
                    raise RuntimeError("Configuration inspection timed out")
                ready = selector.select(.05)
                if not ready and child.poll() is not None:
                    break
                for key, _mask in ready:
                    chunk = os.read(key.fd, 4096)
                    if not chunk:
                        selector.unregister(key.fileobj)
                        continue
                    target = output if key.fileobj is child.stdout else errors
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
            raise RuntimeError(detail or "Configuration inspection failed")
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
        self._child = None
        self._thread = None
        self._stderr = bytearray()
        self._error = ""
        self._revision = 0
        self._stopping = False

    def start(self):
        if self._thread and self._thread.is_alive():
            raise RuntimeError("The previous client is still stopping")
        child = _spawn([self.binary, *self.args, "-headless=true", "-report=-", "-report-waterfall=true"])
        self.model.reset()
        with self._lock:
            self._child = child
            self._stderr.clear()
            self._error = ""
            self._stopping = False
            self._revision += 1
        self._thread = threading.Thread(target=self._pump, args=(child,), daemon=True,
                                        name="entropy-gui-client-pipes")
        self._thread.start()

    def _fail(self, message):
        with self._lock:
            if not self._error:
                self._error = safe_text(message)
                self._revision += 1

    def _pump(self, child):
        pending = bytearray()
        failed = False
        failure_deadline = None
        exit_deadline = None
        try:
            with selectors.DefaultSelector() as selector:
                for pipe in (child.stdout, child.stderr):
                    os.set_blocking(pipe.fileno(), False)
                    selector.register(pipe, selectors.EVENT_READ)
                while selector.get_map():
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
                    ready = selector.select(.05)
                    if not ready and child.poll() is not None:
                        break
                    for key, _mask in ready:
                        chunk = os.read(key.fd, 4096)
                        if not chunk:
                            selector.unregister(key.fileobj)
                            continue
                        if key.fileobj is child.stderr:
                            with self._lock:
                                self._stderr.extend(chunk)
                                del self._stderr[:-MAX_STDERR_BYTES]
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
                                _signal(child)
                if pending and not failed:
                    self._fail("Truncated JSONL report")
        except (OSError, ValueError) as error:
            self._fail("Client report pipe failed: " + str(error))
        finally:
            # A broken protocol must not leave an invisible acquisition running.
            if child.poll() is None:
                _signal(child)
                try:
                    child.wait(timeout=1)
                except subprocess.TimeoutExpired:
                    _signal(child, kill=True)
            result = child.wait(timeout=1)
            with self._lock:
                if result and not self._stopping and not self._error:
                    self._error = "entropy-client exited with status " + str(result)
                self._revision += 1
            _close_pipes(child)

    def stop(self):
        with self._lock:
            child = self._child
            self._stopping = True
        if child and child.poll() is None:
            _signal(child)
            try:
                child.wait(timeout=1)
            except subprocess.TimeoutExpired:
                _signal(child, kill=True)
                child.wait(timeout=.5)
        if self._thread:
            self._thread.join(timeout=.5)

    def snapshot(self):
        result = self.model.snapshot()
        with self._lock:
            child = self._child
            returncode = child.poll() if child else None
            result.update(running=bool(child and returncode is None), returncode=returncode,
                          stderr=safe_text(bytes(self._stderr).decode("utf-8", "replace")), error=self._error)
            result["revision"] += self._revision
        return result
