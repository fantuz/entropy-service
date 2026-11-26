"""Bounded, verified, read-only polling of the server's existing health API."""
import errno
import http.client
import ipaddress
import json
import math
import os
import selectors
import socket
import ssl
import threading
import time
import urllib.error
import urllib.request
from urllib.parse import urlsplit, urlunsplit

from .model import safe_text


MAX_HEALTH_BYTES = 65536
_MAX_DIAL_ADDRESSES = 16
LOCAL_HEALTH_URLS = ("https://127.0.0.1:8443", "https://[::1]:8443",
                     "http://127.0.0.1:8080", "http://[::1]:8080")
_DISCOVERY_IO_TIMEOUT = 1.0
_CONTEXT_UNSET = object()
_TEXT_FIELDS = ("rng_source", "rng_drbg", "rng_version", "last_reseed_at", "last_reseed_error")
_NUMBER_FIELDS = ("reseed_age_ms", "reseed_failures_total", "reseed_interval_ms", "max_reseed_age_ms")


class HealthError(ValueError):
    """A response cannot safely describe current server health."""


def normalize_url(value):
    if not isinstance(value, str) or not value or len(value) > 2048 or any(ord(c) <= 32 or ord(c) == 127 for c in value):
        raise ValueError("Enter a complete HTTP(S) server base URL")
    try:
        parts = urlsplit(value)
        port = parts.port
    except ValueError as error:
        raise ValueError("Enter a valid HTTP(S) server base URL") from error
    if (parts.scheme not in ("http", "https") or not parts.hostname or parts.username is not None
            or parts.password is not None or "?" in value or "#" in value or "\\" in value
            or port is not None and not 1 <= port <= 65535):
        raise ValueError("Use HTTP(S) without credentials, query parameters or fragments")
    return urlunsplit((parts.scheme, parts.netloc, parts.path.rstrip("/"), "", ""))


def is_loopback_url(url):
    """Return whether a valid health URL uses a literal IPv4 or IPv6 loopback."""
    try:
        host = urlsplit(normalize_url(url)).hostname
        return ipaddress.ip_address(host).is_loopback
    except ValueError:
        return False


def _json_pairs(pairs):
    value = {}
    for key, item in pairs:
        if key in value:
            raise HealthError("Duplicate health field")
        value[key] = item
    return value


def _json_constant(_value):
    raise HealthError("Non-finite health value")


def _decode(data):
    try:
        value = json.loads(data, object_pairs_hook=_json_pairs, parse_constant=_json_constant)
    except (ValueError, UnicodeError, RecursionError) as error:
        raise HealthError("Invalid health JSON") from error
    if not isinstance(value, dict):
        raise HealthError("Expected a health object")
    return value


def _ready(value, code):
    ready = value.get("ready")
    if (type(ready) is not bool or code != (200 if ready else 503)
            or value.get("status") != ("ok" if ready else "degraded")):
        raise HealthError("Inconsistent readiness response")
    if value.get("seed_state") not in ("fresh", "failed", "stale"):
        raise HealthError("Invalid seed state")
    if value.get("on_reseed_failure") not in ("stop", "continue") or type(value.get("generation_allowed")) is not bool:
        raise HealthError("Invalid generation policy")
    if ready and (value["seed_state"] != "fresh" or not value["generation_allowed"]):
        raise HealthError("Inconsistent generator state")
    result = {key: value[key] for key in ("seed_state", "on_reseed_failure", "generation_allowed")}
    for key in _TEXT_FIELDS:
        item = value.get(key)
        if item is None and key not in ("rng_source", "rng_drbg"):
            continue
        if not isinstance(item, str) or len(item) > 2048:
            raise HealthError("Invalid health text")
        result[key] = safe_text(item)
    for key in _NUMBER_FIELDS:
        item = value.get(key)
        if item is None and key not in ("reseed_age_ms", "reseed_failures_total"):
            continue
        if type(item) is not int or not 0 <= item < 1 << 64:
            raise HealthError("Invalid health count")
        result[key] = item
    return result


class _Control:
    def __init__(self):
        self.lock = threading.Lock()
        self.cancelled = threading.Event()
        self.connection = None
        self.socket = None
        self.connecting = set()
        self.deadline = None
        self.request_generation = 0

    def dial_opened(self, sock):
        with self.lock:
            self.connecting.add(sock)
            cancelled = self.cancelled.is_set()
        if cancelled:
            self.abort()
            raise TimeoutError("Health request cancelled")

    def dial_closed(self, sock):
        with self.lock:
            self.connecting.discard(sock)
        sock.close()

    def dial_connected(self, sock):
        with self.lock:
            self.connecting.discard(sock)
            self.socket = sock
        if self.cancelled.is_set():
            self.abort()
            raise TimeoutError("Health request cancelled")

    def connection_opened(self, connection):
        with self.lock:
            self.connection = connection
            self.socket = None
            cancelled = self.cancelled.is_set()
        if cancelled:
            self.abort()
            raise OSError("Health request cancelled")

    def socket_opened(self, connection):
        with self.lock:
            self.socket = connection.sock
        if self.cancelled.is_set():
            self.abort()
            raise OSError("Health request cancelled")

    def begin_request(self, timeout):
        with self.lock:
            if self.cancelled.is_set():
                raise TimeoutError("Health request cancelled")
            self.request_generation += 1
            self.deadline = time.monotonic() + timeout
            return self.request_generation

    def end_request(self, token):
        with self.lock:
            if token == self.request_generation:
                self.request_generation += 1

    def _cancel_locked(self):
        self.cancelled.set()
        connection = self.connection
        active = self.socket or (connection.sock if connection else None)
        sockets = set(self.connecting)
        if active is not None:
            sockets.add(active)
        return connection, sockets

    def abort_request(self, token):
        with self.lock:
            if token != self.request_generation:
                return
            active = self._cancel_locked()
        self._close_active(*active)

    def abort(self):
        with self.lock:
            self.request_generation += 1
            active = self._cancel_locked()
        self._close_active(*active)

    @staticmethod
    def _close_active(connection, sockets):
        for active in sockets:
            try:
                active.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
            active.close()
        if connection is not None:
            connection.close()


def _addresses(host, port):
    """Keep resolver preference, but do not put every IPv6 address before IPv4."""
    families = {}
    seen = set()
    for item in socket.getaddrinfo(host, port, 0, socket.SOCK_STREAM):
        family, kind, protocol, _name, address = item
        if family not in (socket.AF_INET, socket.AF_INET6):
            continue
        group = families.setdefault(family, [])
        if len(group) >= _MAX_DIAL_ADDRESSES:
            continue
        key = family, kind, protocol, address
        if key not in seen:
            seen.add(key)
            group.append(item)
    ordered = []
    while len(ordered) < _MAX_DIAL_ADDRESSES and any(families.values()):
        for group in families.values():
            if group and len(ordered) < _MAX_DIAL_ADDRESSES:
                ordered.append(group.pop(0))
    return ordered


def _connect(address, timeout, source_address, control):
    """Race a bounded address list without a thread per address or family.

    Windows can wait for an IPv6 refusal even when IPv4 is immediately usable.
    A small stagger lets the other family connect within the same I/O deadline.
    getaddrinfo itself remains an OS operation that Python cannot preempt.
    """
    deadline = min(time.monotonic() + timeout, control.deadline or float("inf"))
    candidates = iter(_addresses(*address))
    pending = set()
    winner = None
    connected = False
    next_attempt = time.monotonic()
    stagger = min(.2, timeout / 2)
    exhausted = False
    in_progress = {errno.EINPROGRESS, errno.EWOULDBLOCK, errno.EALREADY, errno.EINTR,
                   10035, 10036, 10037}  # Winsock's nonblocking connect results.
    last_error = None
    try:
        with selectors.DefaultSelector() as selector:
            while time.monotonic() < deadline and not control.cancelled.is_set():
                now = time.monotonic()
                if not exhausted and (not pending or now >= next_attempt):
                    item = next(candidates, None)
                    if item is None:
                        exhausted = True
                    else:
                        family, kind, protocol, _name, target = item
                        try:
                            sock = socket.socket(family, kind, protocol)
                        except OSError as error:
                            last_error = error
                            next_attempt = now
                            continue
                        pending.add(sock)
                        control.dial_opened(sock)
                        try:
                            sock.setblocking(False)
                            if source_address:
                                sock.bind(source_address)
                            code = sock.connect_ex(target)
                            if code == 0:
                                winner = sock
                                break
                            if code not in in_progress:
                                raise OSError(code, "Health connection failed")
                            selector.register(sock, selectors.EVENT_WRITE)
                            next_attempt = now + stagger
                        except OSError as error:
                            last_error = error
                            pending.discard(sock)
                            control.dial_closed(sock)
                            next_attempt = now
                if exhausted and not pending:
                    if last_error:
                        raise last_error
                    raise OSError("No usable health endpoint address")
                wait = max(0, min(.05, deadline - time.monotonic(),
                                  next_attempt - time.monotonic() if not exhausted else .05))
                try:
                    events = selector.select(wait)
                except (OSError, ValueError):
                    if control.cancelled.is_set():
                        raise TimeoutError("Health request cancelled")
                    raise
                for key, _mask in events:
                    if control.cancelled.is_set():
                        raise TimeoutError("Health request cancelled")
                    sock = key.fileobj
                    code = sock.getsockopt(socket.SOL_SOCKET, socket.SO_ERROR)
                    selector.unregister(sock)
                    if code == 0:
                        winner = sock
                        break
                    last_error = OSError(code, "Health connection failed")
                    pending.discard(sock)
                    control.dial_closed(sock)
                    next_attempt = time.monotonic()
                if winner is not None:
                    break
        if winner is None or control.cancelled.is_set() or time.monotonic() >= deadline:
            raise TimeoutError("Health connection timed out or was cancelled")
        winner.settimeout(max(.001, deadline - time.monotonic()))
        control.dial_connected(winner)
        connected = True
        return winner
    finally:
        for sock in pending:
            if sock is not winner or not connected or control.cancelled.is_set():
                control.dial_closed(sock)


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, _request, _file, _code, _message, _headers, _newurl):
        return None


def _opener(control, context, direct):
    class Connection(http.client.HTTPConnection):
        def connect(self):
            super().connect()
            control.socket_opened(self)

    class TLSConnection(http.client.HTTPSConnection):
        def connect(self):
            # Retain urllib's CONNECT tunnel handling, then register the SSL
            # socket before its handshake so Disconnect can interrupt it too.
            http.client.HTTPConnection.connect(self)
            self.sock = self._context.wrap_socket(self.sock,
                                                  server_hostname=self._tunnel_host or self.host,
                                                  do_handshake_on_connect=False)
            control.socket_opened(self)
            self.sock.do_handshake()

    def connection(kind, host, **kwargs):
        result = kind(host, **kwargs)
        result._create_connection = lambda address, timeout, source_address=None: _connect(address, timeout, source_address, control)
        control.connection_opened(result)
        return result

    class HTTP(urllib.request.HTTPHandler):
        def http_open(self, request):
            return self.do_open(lambda host, **kw: connection(Connection, host, **kw), request)

    class HTTPS(urllib.request.HTTPSHandler):
        def __init__(self):
            # Python 3.13+ otherwise loads an implicit system trust context here,
            # even for HTTP polls. TLS uses our already initialized/reused context.
            urllib.request.AbstractHTTPHandler.__init__(self)

        def https_open(self, request):
            return self.do_open(lambda host, **kw: connection(TLSConnection, host, **kw), request,
                                context=context)

    # Automatically selected listeners must stay on loopback even on machines
    # with a system-wide proxy. Manual connections use normal proxy settings.
    proxy = urllib.request.ProxyHandler({} if direct else None)
    return urllib.request.build_opener(proxy, HTTP(), HTTPS(), _NoRedirect())


def _read(opener, url, timeout, control):
    token = control.begin_request(timeout)
    timer = threading.Timer(timeout, control.abort_request, args=(token,))
    timer.daemon = True
    timer.start()
    try:
        return _read_response(opener, url, timeout)
    finally:
        # A canceled Timer may already be executing. Invalidate its generation
        # before the next endpoint starts, so it cannot close that connection.
        control.end_request(token)
        timer.cancel()


def _read_response(opener, url, timeout):
    request = urllib.request.Request(url, headers={"Accept": "application/json", "Cache-Control": "no-cache"})
    try:
        response = opener.open(request, timeout=timeout)
    except urllib.error.HTTPError as error:
        response = error  # A 503 readiness body is useful, not a transport failure.
    with response:
        code = response.status
        if code not in (200, 503):
            raise HealthError("Unexpected health HTTP status")
        if response.headers.get_content_type() != "application/json":
            raise HealthError("Health response is not JSON")
        length = response.headers.get("Content-Length")
        if length is not None and (not length.isdecimal() or int(length) > MAX_HEALTH_BYTES):
            raise HealthError("Health response exceeds 64 KiB")
        data = response.read(MAX_HEALTH_BYTES + 1)
        if len(data) > MAX_HEALTH_BYTES:
            raise HealthError("Health response exceeds 64 KiB")
        return code, _decode(data)


def _empty(url, state="disconnected", error="", listener="unknown"):
    return {"url": url, "state": state, "fresh": False, "listener": listener,
            "live": None, "ready": None, "health": {}, "checked_at": None, "error": error}


def _tls_context(url, ca_file):
    context = ssl.create_default_context() if urlsplit(url).scheme == "https" else None
    if context is not None and ca_file:
        context.load_verify_locations(cafile=ca_file)
    return context


def _ca_error(url):
    result = _empty(url, "tls-error", "The CA certificate could not be loaded. Check the --health-ca file.", "tls-unverified")
    result["checked_at"] = time.time()
    return result


def _fetch(url, timeout, control, context, *, direct=False):
    result = _empty(url)
    try:
        opener = _opener(control, context, direct)
        code, live = _read(opener, url + "/live", timeout, control)
        if code != 200 or live.get("status") != "ok":
            raise HealthError("Unexpected liveness response")
        code, ready = _read(opener, url + "/ready", timeout, control)
        health = _ready(ready, code)
        if control.cancelled.is_set():
            raise OSError("Health request cancelled")
        result.update(state="ready" if ready["ready"] else "degraded", fresh=True,
                      listener="reachable", live=True, ready=ready["ready"], health=health)
    except HealthError:
        result.update(state="invalid", listener="reachable", error="The endpoint returned an invalid health response.")
    except (OSError, ValueError, http.client.HTTPException) as error:
        reason = error.reason if isinstance(error, urllib.error.URLError) else error
        # A non-TLS service on port 8443 can cause an SSL protocol error. Only
        # an actual certificate rejection prevents discovery of a local HTTP service.
        if isinstance(reason, ssl.SSLCertVerificationError) or (not direct and isinstance(reason, ssl.SSLError)):
            result.update(state="tls-error", listener="tls-unverified", error="TLS certificate verification failed; use a trusted server identity or supply its CA certificate.")
        else:
            result.update(state="unavailable", listener="unreachable", error="The health endpoint could not be reached.")
    finally:
        control.abort()
    result["checked_at"] = time.time()
    return result


def fetch_health(url, ca_file=None, timeout=2.0):
    """One bounded health observation; redirects and TLS bypasses are unsupported."""
    url = normalize_url(url)
    if type(timeout) not in (int, float) or not math.isfinite(timeout) or not .05 <= timeout <= 10:
        raise ValueError("Health timeout must be 0.05–10 seconds")
    if ca_file and urlsplit(url).scheme != "https":
        raise ValueError("A CA certificate is only used with an HTTPS health URL")
    try:
        context = _tls_context(url, ca_file)
    except (OSError, ValueError, ssl.SSLError):
        return _ca_error(url)
    return _fetch(url, timeout, _Control(), context)


class HealthSession:
    """Poll an explicit URL or discover one local listener on the polling worker."""
    def __init__(self, url, ca_file=None, interval=2.0, timeout=2.0, *, direct=False):
        self.url = normalize_url(url) if url is not None else None
        self._direct = direct or self.url is None
        if direct and self.url is not None and not is_loopback_url(self.url):
            raise ValueError("Direct health polling requires a numeric loopback address")
        if ca_file is not None:
            if not isinstance(ca_file, (str, os.PathLike)) or len(os.fspath(ca_file)) > 4096 or "\0" in os.fspath(ca_file):
                raise ValueError("Select a CA certificate file")
        if ca_file and self.url is not None and urlsplit(self.url).scheme != "https":
            raise ValueError("A CA certificate is only used with an HTTPS health URL")
        if type(interval) not in (int, float) or not math.isfinite(interval) or not .05 <= interval <= 60:
            raise ValueError("Health interval must be 0.05–60 seconds")
        if type(timeout) not in (int, float) or not math.isfinite(timeout) or not .05 <= timeout <= 10:
            raise ValueError("Health timeout must be 0.05–10 seconds")
        self.ca_file, self.interval, self.timeout = ca_file, interval, timeout
        self._lock = threading.Lock()
        self._stop = threading.Event()
        self._thread = None
        self._control = None
        self._generation = 0
        self._revision = 0
        self._value = _empty(self.url)
        self._observed = None

    def start(self):
        with self._lock:
            if self._thread and self._thread.is_alive():
                raise RuntimeError("The previous health request is still running or stopping")
            self._stop.clear()
            self._generation += 1
            generation = self._generation
            self._value = _empty(self.url, "discovering" if self.url is None else "checking")
            self._observed = None
            self._revision += 1
            self._thread = threading.Thread(target=self._run, args=(generation,), daemon=True,
                                            name="entropy-gui-health")
            self._thread.start()

    def _observe(self, generation, url, context, timeout):
        control = _Control()
        with self._lock:
            if generation != self._generation:
                return None, context
            self._control = control
        if context is _CONTEXT_UNSET:
            # Trust loading is outside the network deadline. Reuse the context
            # across requests, and reload it after an explicit reconnect.
            try:
                context = _tls_context(url, self.ca_file)
            except (OSError, ValueError, ssl.SSLError):
                return _ca_error(url), context
        if control.cancelled.is_set():
            return None, context
        return _fetch(url, timeout, control, context, direct=self._direct), context

    def _discover(self, generation):
        trust_failure = False
        context = _CONTEXT_UNSET
        timeout = min(self.timeout, _DISCOVERY_IO_TIMEOUT)
        for url in LOCAL_HEALTH_URLS:
            if urlsplit(url).scheme == "http":
                if trust_failure or self.ca_file:
                    break
                context = None
            value, context = self._observe(generation, url, context, timeout)
            if value is None or value["fresh"]:
                return value, context
            if value["state"] == "tls-error":
                if context is _CONTEXT_UNSET:
                    return _ca_error(None), context
                trust_failure = True
        if trust_failure:
            detail = "Local HTTPS certificate verification failed. Check the server CA (--health-ca) and the hostname in the certificate."
            if not self.ca_file:
                detail += " If you use local HTTP, enter http://127.0.0.1:8080 and click Connect."
            value = _empty(None, "tls-error", detail, "tls-unverified")
        else:
            ports = "HTTPS port 8443" if self.ca_file else "HTTPS port 8443 or HTTP port 8080"
            value = _empty(None, "not-found", f"No entropy-server found on {ports}. Enter its URL or start the server "
                           "and choose Find local. If Start failed, check Local server logs.")
        value["checked_at"] = time.time()
        return value, None

    def _run(self, generation):
        url = self.url
        context = _CONTEXT_UNSET
        try:
            while not self._stop.is_set():
                if url is None:
                    value, context = self._discover(generation)
                else:
                    value, context = self._observe(generation, url, context, self.timeout)
                with self._lock:
                    if value is None or generation != self._generation or self._stop.is_set():
                        return
                    url = value["url"]
                    self._value = value
                    self._observed = time.monotonic()
                    self._revision += 1
                    self._control = None
                if url is None or self._stop.wait(self.interval):
                    break
        finally:
            with self._lock:
                self._control = None
                self._revision += 1

    def stop(self):
        with self._lock:
            self._generation += 1
            self._stop.set()
            control = self._control
            self._value = _empty(self.url)
            self._observed = None
            self._revision += 1
        if control:
            control.abort()

    def snapshot(self):
        with self._lock:
            stale_after = max(2 * self.interval, self.interval + 2 * self.timeout + 1)
            if self._value["fresh"] and self._observed is not None and time.monotonic() - self._observed > stale_after:
                checked = self._value["checked_at"]
                self._value = _empty(self._value["url"], "unavailable", "Health data is stale; waiting for a new observation.")
                self._value["checked_at"] = checked
                self._revision += 1
            value = dict(self._value)
            value["health"] = dict(value["health"])
            active = bool(self._thread and self._thread.is_alive())
            value.update(running=active and not self._stop.is_set(), stopping=active and self._stop.is_set(), revision=self._revision)
            return value
