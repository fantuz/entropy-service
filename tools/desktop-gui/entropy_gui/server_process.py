"""One owned local server, with bounded log capture and native shutdown."""
import threading
import time

from .model import safe_text
from .process import _arguments, _close_pipes, _INSPECT_RESERVED, _PipeReader, _spawn_utility, stop_owned


MAX_SERVER_LOG_BYTES = 65536


class ServerSession:
    """Own only the server started here; never locate or stop another server."""

    def __init__(self, binary, args):
        self.binary = str(binary)
        self.args = _arguments(args, _INSPECT_RESERVED)
        # The Go executable dispatches subcommands before parsing startup
        # flags. In particular, generate-cert must not run through Start.
        if self.args and (not self.args[0].startswith("-") or self.args[0] == "-"):
            raise ValueError("Server startup options must begin with a flag; subcommands are not supported")
        self._lock = threading.Lock()
        self._lifecycle = threading.Lock()
        self._termination = threading.Lock()
        self._child = None
        self._thread = None
        self._logs = bytearray()
        self._leading_partial = False
        self._error = ""
        self._revision = 0
        self._log_cache_key = None
        self._log_cache = ""
        self._stopping = False
        self._forced_stop = False

    def start(self):
        if not self._lifecycle.acquire(blocking=False):
            raise RuntimeError("The previous server is still stopping")
        try:
            if ((self._thread and self._thread.is_alive()) or
                    (self._child and self._child.poll() is None)):
                raise RuntimeError("The owned server is already running or still stopping")
            child = _spawn_utility(self.binary, self.args, "server")
            with self._lock:
                self._child = child
                self._logs.clear()
                self._leading_partial = False
                self._error = ""
                self._stopping = False
                self._forced_stop = False
                self._revision += 1
            thread = threading.Thread(target=self._pump, args=(child,), daemon=True,
                                      name="entropy-gui-server-logs")
            self._thread = thread
            try:
                thread.start()
            except RuntimeError:
                self._thread = None
                self._fail("Server log reader could not start")
                self._stop_child(child)
                _close_pipes(child)
                raise
        finally:
            self._lifecycle.release()

    def _append(self, chunk):
        with self._lock:
            self._logs.extend(chunk)
            excess = len(self._logs) - MAX_SERVER_LOG_BYTES
            if excess > 0:
                # Remember whether the ring now starts in the middle of a
                # line. That fragment may have lost its URL's scheme prefix.
                self._leading_partial = self._logs[excess - 1] != 10
                del self._logs[:excess]
            self._revision += 1

    def _fail(self, message):
        with self._lock:
            if not self._error:
                self._error = safe_text(message)
            self._revision += 1

    def _stop_child(self, child, timeout=1.5):
        # Stop may race EOF or a log-reader failure. Serialize signal/reap
        # requests without holding the lifecycle lock while joining the pump.
        with self._termination:
            forced = stop_owned(child, timeout=timeout)
        if forced:
            with self._lock:
                self._forced_stop = True
                message = "Server required forced termination; shutdown may be incomplete"
                if not self._error:
                    self._error = message
                elif message not in self._error:
                    self._error += "; " + message
                self._revision += 1
        return forced

    def _pump(self, child):
        exit_deadline = None
        try:
            with _PipeReader(child) as reader:
                while reader.pipes:
                    now = time.monotonic()
                    if child.poll() is not None and exit_deadline is None:
                        exit_deadline = now + .2
                    if exit_deadline is not None and now >= exit_deadline:
                        break
                    ready = reader.read()
                    if not ready and child.poll() is not None:
                        break
                    for _pipe, chunk in ready:
                        if chunk:
                            self._append(chunk)
        except (OSError, ValueError) as error:
            self._fail("Server log pipe failed: " + str(error))
        finally:
            # EOF or a failed reader must not leave an invisible daemon owned
            # by a window whose log pump has disappeared.
            try:
                self._stop_child(child, timeout=1)
                result = child.wait(timeout=1)
                with self._lock:
                    if result and not self._stopping and not self._error:
                        self._error = "entropy-server exited with status " + str(result)
                    self._revision += 1
            finally:
                _close_pipes(child)

    def stop(self):
        with self._lifecycle:
            with self._lock:
                child = self._child
                self._stopping = True
            if child:
                self._stop_child(child)
            if self._thread:
                self._thread.join(timeout=.5)
                if self._thread.is_alive():
                    self._fail("Server log cleanup did not complete")

    def snapshot(self):
        with self._lock:
            child = self._child
            returncode = child.poll() if child else None
            process_running = child is not None and returncode is None
            running = bool(child and (process_running or self._thread and self._thread.is_alive()))
            key = (self._revision, running)
            state = {"running": running, "process_running": process_running,
                     "stopping": bool(running and self._stopping), "returncode": returncode,
                     "error": self._error, "forced_stop": self._forced_stop,
                     "revision": self._revision}
            if key == self._log_cache_key:
                state.update(logs=self._log_cache, stderr=self._log_cache)
                return state
            logs = bytes(self._logs)
            if self._leading_partial:
                _fragment, separator, logs = logs.partition(b"\n")
                if not separator:
                    logs = b""
            if running:
                # Do not temporarily display half a credential-bearing URL
                # before the rest of its log line reaches the next pipe read.
                logs = logs[:logs.rfind(b"\n") + 1]
        text = safe_text(logs.decode("utf-8", "replace"))
        with self._lock:
            # Keep one bounded rendered view. Never replace a newer snapshot
            # after output arrived while this copy was being sanitized.
            if self._revision == key[0]:
                self._log_cache_key, self._log_cache = key, text
        state.update(logs=text, stderr=text)
        return state
