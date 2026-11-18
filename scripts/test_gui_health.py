#!/usr/bin/env python3
"""Read-only health polling, TLS verification and lifecycle regression tests."""
import contextlib
import errno
import http.server
import json
import os
from pathlib import Path
import socket
import ssl
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "tools/desktop-gui"))
from entropy_gui.health import HealthSession, MAX_HEALTH_BYTES, _Control, _connect, fetch_health, normalize_url


def health(ready=True):
    return {"status": "ok" if ready else "degraded", "ready": ready,
            "seed_state": "fresh" if ready else "failed", "on_reseed_failure": "stop",
            "generation_allowed": ready, "rng_version": "fixture", "rng_drbg": "chacha20",
            "rng_source": "os-csprng", "reseed_age_ms": 10, "reseed_failures_total": 0 if ready else 3,
            "reseed_interval_ms": 15000, "max_reseed_age_ms": 60000}


def wait_until(predicate, timeout=3):
    deadline = time.monotonic() + timeout
    while not predicate():
        if time.monotonic() >= deadline:
            raise AssertionError("Health worker did not reach expected state")
        time.sleep(.01)


def observe_fetch(url, **options):
    """Keep timing diagnostics outside the production health/error protocol."""
    original_context = ssl.create_default_context
    context_ms = []

    def create_context(*args, **kwargs):
        began = time.monotonic()
        try:
            return original_context(*args, **kwargs)
        finally:
            context_ms.append(round((time.monotonic() - began) * 1000, 1))

    began = time.monotonic()
    with mock.patch("entropy_gui.health.ssl.create_default_context", side_effect=create_context):
        result = fetch_health(url, **options)
    return result, {"elapsed_ms": round((time.monotonic() - began) * 1000, 1), "context_ms": context_ms}


class Fixture:
    def __init__(self, tls=None):
        self.mode = "ready"
        self.delay = .15
        self.requests = []
        self.entered, self.release = threading.Event(), threading.Event()
        fixture = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_args):
                pass

            def do_GET(self):
                fixture.requests.append(self.path)
                mode = fixture.mode
                if mode == "drop":
                    with contextlib.suppress(OSError):
                        self.connection.shutdown(socket.SHUT_RDWR)
                    self.connection.close()
                    return
                if mode == "redirect":
                    self.send_response(302)
                    self.send_header("Location", "/must-not-follow")
                    self.send_header("Content-Length", "0")
                    self.end_headers()
                    return
                ready_path = self.path.endswith("/ready")
                if mode == "delayed":
                    time.sleep(fixture.delay)
                if mode == "hold" and ready_path:
                    fixture.entered.set()
                    fixture.release.wait(3)
                status = 503 if ready_path and mode == "degraded" else 200
                value = health(status == 200) if ready_path else {"status": "ok"}
                if mode == "wrong-type" and ready_path:
                    value["ready"] = 1
                if mode == "credentials" and ready_path:
                    value["last_reseed_error"] = "failed https://operator:password@host.invalid/path?token=secret"
                data = json.dumps(value).encode()
                if mode == "oversized":
                    data = b"x" * (MAX_HEALTH_BYTES + 1)
                elif mode == "malformed" and ready_path:
                    data = b'{"ready":true,"ready":false}'
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                with contextlib.suppress(BrokenPipeError, ConnectionResetError, ConnectionAbortedError, ssl.SSLError):
                    if mode == "drip" or mode == "ready-drip" and ready_path:
                        for item in data:
                            self.wfile.write(bytes([item]))
                            self.wfile.flush()
                            time.sleep(.04)
                    else:
                        self.wfile.write(data)

        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.server.daemon_threads = True
        if tls:
            self.server.socket = tls.wrap_socket(self.server.socket, server_side=True)
        scheme = "https" if tls else "http"
        self.url = f"{scheme}://127.0.0.1:{self.server.server_port}"
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)

    def __enter__(self):
        self.thread.start()
        return self

    def __exit__(self, *_args):
        self.release.set()
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)


class _HealthBase(unittest.TestCase):
    def setUp(self):
        self.proxy = mock.patch.dict(os.environ, {"NO_PROXY": "localhost,127.0.0.1,::1", "no_proxy": "localhost,127.0.0.1,::1"})
        self.proxy.start()
        self.addCleanup(self.proxy.stop)


class HealthTests(_HealthBase):
    def test_urls_reject_credentials_queries_and_non_http_schemes(self):
        self.assertEqual(normalize_url("https://example.org/entropy/"), "https://example.org/entropy")
        for url in ("file:///tmp/seed", "http://user:password@example.org", "https://host/?token=s",
                    "https://host/#fragment", "http://host:70000", "http://host/\r\nheader", "http://host/with space"):
            with self.subTest(url=url), self.assertRaises(ValueError):
                normalize_url(url)
        with self.assertRaises(ValueError):
            HealthSession("http://localhost", ca_file="unused.pem")

    def test_healthy_and_degraded_observations_include_actual_reseed_state(self):
        with Fixture() as fixture:
            ready = fetch_health(fixture.url + "/proxy-prefix")
            self.assertTrue(ready["fresh"] and ready["live"] and ready["ready"])
            self.assertEqual(ready["listener"], "reachable")
            self.assertEqual(ready["health"]["rng_drbg"], "chacha20")
            self.assertEqual(fixture.requests, ["/proxy-prefix/live", "/proxy-prefix/ready"])
            fixture.mode = "degraded"
            failed = fetch_health(fixture.url)
            self.assertEqual(failed["state"], "degraded")
            self.assertTrue(failed["fresh"] and failed["live"])
            self.assertFalse(failed["ready"] or failed["health"]["generation_allowed"])
            self.assertEqual(failed["health"]["reseed_failures_total"], 3)

    def test_plain_http_does_not_initialize_unused_tls_roots(self):
        with Fixture() as fixture, mock.patch("ssl.create_default_context", side_effect=AssertionError("unexpected TLS initialization")) as create:
            value = fetch_health(fixture.url)
            self.assertTrue(value["fresh"] and value["ready"], value)
            create.assert_not_called()

    def test_invalid_and_oversized_responses_never_show_ready(self):
        with Fixture() as fixture:
            for mode in ("wrong-type", "malformed", "oversized"):
                fixture.mode = mode
                value = fetch_health(fixture.url)
                self.assertEqual(value["state"], "invalid", (mode, value))
                self.assertFalse(value["fresh"])
                self.assertIsNone(value["ready"])
                self.assertEqual(value["health"], {})

    def test_redirects_are_not_followed(self):
        with Fixture() as fixture:
            fixture.mode = "redirect"
            value = fetch_health(fixture.url)
            self.assertEqual(value["state"], "invalid")
            self.assertEqual(fixture.requests, ["/live"])

    def test_remote_health_honors_the_configured_http_proxy(self):
        with Fixture() as proxy:
            with mock.patch.dict(os.environ, {"http_proxy": proxy.url, "HTTP_PROXY": proxy.url,
                                              "no_proxy": "", "NO_PROXY": ""}):
                value = fetch_health("http://health-fixture.invalid/base")
            self.assertTrue(value["fresh"] and value["ready"])
            self.assertEqual(proxy.requests, ["http://health-fixture.invalid/base/live",
                                               "http://health-fixture.invalid/base/ready"])

    def test_error_text_does_not_disclose_url_credentials(self):
        with Fixture() as fixture:
            fixture.mode = "credentials"
            text = fetch_health(fixture.url)["health"]["last_reseed_error"]
            self.assertNotIn("password", text)
            self.assertNotIn("token", text)
            self.assertIn("https://host.invalid/path", text)

    def test_absolute_deadline_stops_a_slow_drip_response(self):
        with Fixture() as fixture:
            fixture.mode = "drip"
            began = time.monotonic()
            value = fetch_health(fixture.url, timeout=.15)
            self.assertLess(time.monotonic() - began, 1)
            self.assertFalse(value["fresh"])
            self.assertIsNone(value["ready"])

    def test_second_endpoint_has_an_independent_body_deadline(self):
        with Fixture() as fixture:
            fixture.mode = "ready-drip"
            began = time.monotonic()
            value = fetch_health(fixture.url, timeout=.15)
            self.assertLess(time.monotonic() - began, 1)
            self.assertEqual(fixture.requests, ["/live", "/ready"])
            self.assertFalse(value["fresh"])
            self.assertIsNone(value["ready"])

    def test_each_healthy_endpoint_can_use_its_own_io_budget(self):
        with Fixture() as fixture:
            fixture.mode = "delayed"
            # Combined response delays exceed one budget; each individual
            # response fits with enough scheduling margin on native Mac CI.
            fixture.delay = .6
            value = fetch_health(fixture.url, timeout=1)
            self.assertTrue(value["fresh"] and value["ready"], value)
            self.assertEqual(fixture.requests, ["/live", "/ready"])

    def test_late_first_endpoint_timer_cannot_abort_the_second_request(self):
        timers = []
        original = threading.Timer

        def timer(*args, **kwargs):
            result = original(*args, **kwargs)
            timers.append(result)
            return result

        with Fixture() as fixture:
            fixture.mode = "hold"
            result = []
            with mock.patch("entropy_gui.health.threading.Timer", side_effect=timer):
                worker = threading.Thread(target=lambda: result.append(fetch_health(fixture.url)))
                worker.start()
                try:
                    self.assertTrue(fixture.entered.wait(1))
                    self.assertEqual(len(timers), 2)
                    # Simulate an already-dispatched first timer arriving after
                    # /live completed and /ready began, despite Timer.cancel().
                    timers[0].function(*timers[0].args, **timers[0].kwargs)
                    fixture.release.set()
                    worker.join(timeout=3)
                    self.assertFalse(worker.is_alive())
                    self.assertTrue(result[0]["fresh"] and result[0]["ready"], result)
                finally:
                    fixture.release.set()
                    worker.join(timeout=3)

    def test_poller_clears_stale_green_after_drop_and_recovers(self):
        with Fixture() as fixture:
            session = HealthSession(fixture.url, interval=.05, timeout=.3)
            try:
                session.start()
                wait_until(lambda: session.snapshot()["ready"] is True)
                fixture.mode = "drop"
                wait_until(lambda: session.snapshot()["state"] == "unavailable")
                value = session.snapshot()
                self.assertFalse(value["fresh"])
                self.assertIsNone(value["ready"])
                self.assertEqual(value["health"], {})
                fixture.mode = "degraded"
                wait_until(lambda: session.snapshot()["state"] == "degraded")
                fixture.mode = "ready"
                wait_until(lambda: session.snapshot()["ready"] is True)
            finally:
                session.stop()
                wait_until(lambda: not session.snapshot()["stopping"])

    def test_disconnect_invalidates_inflight_reply_and_polling_is_single_worker(self):
        with Fixture() as fixture:
            fixture.mode = "hold"
            session = HealthSession(fixture.url, interval=.05, timeout=2)
            try:
                session.start()
                self.assertTrue(fixture.entered.wait(1))
                with self.assertRaises(RuntimeError):
                    session.start()
                time.sleep(.15)
                self.assertEqual(fixture.requests.count("/ready"), 1)
                began = time.monotonic()
                session.stop()
                self.assertLess(time.monotonic() - began, .3)
                self.assertEqual(session.snapshot()["state"], "disconnected")
                self.assertEqual(session.snapshot()["health"], {})
                fixture.release.set()
                wait_until(lambda: not session.snapshot()["stopping"])
                self.assertIsNone(session.snapshot()["ready"])
                fixture.mode = "ready"
                session.start()
                wait_until(lambda: session.snapshot()["ready"] is True)
            finally:
                session.stop()
                wait_until(lambda: not session.snapshot()["stopping"])

    def test_snapshot_freshness_expiry_increments_revision_and_clears_health(self):
        with Fixture() as fixture:
            # This case tests 120-second freshness expiry, not a tight network
            # deadline. Dedicated drip tests keep their short timeout bounds.
            session = HealthSession(fixture.url, interval=60, timeout=2)
            try:
                session.start()
                wait_until(lambda: session.snapshot()["ready"] is True)
                before = session.snapshot()
                observed = time.monotonic()
                with mock.patch("entropy_gui.health.time.monotonic", return_value=observed + 121):
                    stale = session.snapshot()
                self.assertGreater(stale["revision"], before["revision"])
                self.assertFalse(stale["fresh"])
                self.assertIsNone(stale["ready"])
                self.assertEqual(stale["health"], {})
            finally:
                session.stop()
                wait_until(lambda: not session.snapshot()["stopping"])

    def test_freshness_covers_interval_plus_both_endpoint_budgets(self):
        with Fixture() as fixture:
            session = HealthSession(fixture.url, interval=2, timeout=2)
            try:
                session.start()
                wait_until(lambda: session.snapshot()["ready"] is True)
                observed = time.monotonic()
                with mock.patch("entropy_gui.health.time.monotonic", return_value=observed + 6):
                    self.assertTrue(session.snapshot()["fresh"])
                with mock.patch("entropy_gui.health.time.monotonic", return_value=observed + 8):
                    self.assertFalse(session.snapshot()["fresh"])
                    self.assertIsNone(session.snapshot()["ready"])
            finally:
                session.stop()
                wait_until(lambda: not session.snapshot()["stopping"])


class HealthTLSTests(_HealthBase):
    @classmethod
    def setUpClass(cls):
        cls.directory = tempfile.TemporaryDirectory(prefix="entropy-health-tls-")
        root = Path(cls.directory.name)
        cls.cert, cls.key = root / "cert.pem", root / "key.pem"
        cls.extra_cert, cls.extra_key = root / "extra-cert.pem", root / "extra-key.pem"
        server = ROOT / ("bin/entropy-server.exe" if sys.platform == "win32" else "bin/entropy-server")
        if not server.is_file():
            cls.directory.cleanup()
            raise unittest.SkipTest("Build entropy-server before the health TLS tests")
        subprocess.run([str(server), "generate-cert", f"-cert-file={cls.cert}", f"-key-file={cls.key}",
                        "-hosts=localhost", "-valid-for=1h"], check=True, stdout=subprocess.DEVNULL,
                       stderr=subprocess.PIPE, timeout=10)
        subprocess.run([str(server), "generate-cert", f"-cert-file={cls.extra_cert}", f"-key-file={cls.extra_key}",
                        "-hosts=unrelated.invalid", "-valid-for=1h"], check=True, stdout=subprocess.DEVNULL,
                       stderr=subprocess.PIPE, timeout=10)

    @classmethod
    def tearDownClass(cls):
        cls.directory.cleanup()

    def test_tls_requires_trust_and_the_correct_hostname(self):
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.minimum_version = ssl.TLSVersion.TLSv1_2
        context.load_cert_chain(self.cert, self.key)
        with Fixture(context) as fixture:
            url = fixture.url.replace("127.0.0.1", "localhost")
            rejected = fetch_health(url)
            self.assertEqual(rejected["state"], "tls-error")
            self.assertEqual(rejected["listener"], "tls-unverified")
            self.assertFalse(rejected["fresh"])
            verified, timing = observe_fetch(url, ca_file=str(self.cert))
            self.assertTrue(verified["fresh"] and verified["ready"], (verified, timing, list(fixture.requests)))
            wrong_host = fetch_health(fixture.url, ca_file=str(self.cert))
            self.assertEqual(wrong_host["state"], "tls-error")
            self.assertFalse(wrong_host["fresh"])

    def test_selected_ca_adds_to_the_environment_trust_roots(self):
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.minimum_version = ssl.TLSVersion.TLSv1_2
        context.load_cert_chain(self.cert, self.key)
        with Fixture(context) as fixture:
            with mock.patch.dict(os.environ, {"SSL_CERT_FILE": str(self.cert)}):
                value, timing = observe_fetch(fixture.url.replace("127.0.0.1", "localhost"), ca_file=str(self.extra_cert))
            self.assertTrue(value["fresh"] and value["ready"], (value, timing, list(fixture.requests)))

    def test_context_initialization_is_not_an_http_io_deadline(self):
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.minimum_version = ssl.TLSVersion.TLSv1_2
        context.load_cert_chain(self.cert, self.key)
        original = ssl.create_default_context

        def slow_context():
            time.sleep(.35)
            return original()

        with Fixture(context) as fixture:
            with mock.patch("entropy_gui.health.ssl.create_default_context", side_effect=slow_context):
                value = fetch_health(fixture.url.replace("127.0.0.1", "localhost"),
                                     ca_file=str(self.cert), timeout=.3)
            self.assertTrue(value["fresh"] and value["ready"], value)

    def test_session_reuses_initialized_context_and_reloads_on_reconnect(self):
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.minimum_version = ssl.TLSVersion.TLSv1_2
        context.load_cert_chain(self.cert, self.key)
        original = ssl.create_default_context
        with Fixture(context) as fixture:
            session = HealthSession(fixture.url.replace("127.0.0.1", "localhost"),
                                    ca_file=str(self.cert), interval=.05)
            with mock.patch("entropy_gui.health.ssl.create_default_context", wraps=original) as build:
                try:
                    session.start()
                    wait_until(lambda: len(fixture.requests) >= 4 and session.snapshot()["ready"] is True)
                    self.assertEqual(build.call_count, 1)
                    session.stop()
                    wait_until(lambda: not session.snapshot()["stopping"])
                    session.start()
                    wait_until(lambda: session.snapshot()["ready"] is True)
                    self.assertEqual(build.call_count, 2)
                finally:
                    session.stop()
                    wait_until(lambda: not session.snapshot()["stopping"])

    def test_context_load_failure_can_recover_without_caching_the_failure(self):
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.minimum_version = ssl.TLSVersion.TLSv1_2
        context.load_cert_chain(self.cert, self.key)
        with tempfile.TemporaryDirectory() as directory, Fixture(context) as fixture:
            ca = Path(directory) / "not-created-yet.pem"
            session = HealthSession(fixture.url.replace("127.0.0.1", "localhost"), ca_file=str(ca), interval=.05)
            try:
                session.start()
                wait_until(lambda: session.snapshot()["state"] == "tls-error")
                self.assertFalse(fixture.requests)
                ca.write_bytes(self.cert.read_bytes())
                wait_until(lambda: session.snapshot()["ready"] is True)
            finally:
                session.stop()
                wait_until(lambda: not session.snapshot()["stopping"])

    def test_disconnect_during_context_loading_prevents_late_network_io(self):
        entered, release = threading.Event(), threading.Event()
        original = ssl.create_default_context

        def delayed_context():
            entered.set()
            release.wait(2)
            return original()

        with Fixture() as fixture:
            # Initialization is independent from the server fixture's transport;
            # cancellation must win before any HTTP operation can be attempted.
            session = HealthSession(fixture.url)
            with mock.patch("entropy_gui.health._tls_context", side_effect=lambda *_args: delayed_context()):
                try:
                    session.start()
                    self.assertTrue(entered.wait(1))
                    session.stop()
                    self.assertEqual(session.snapshot()["state"], "disconnected")
                    with self.assertRaises(RuntimeError):
                        session.start()
                    release.set()
                    wait_until(lambda: not session.snapshot()["stopping"])
                    self.assertFalse(fixture.requests)
                    self.assertIsNone(session.snapshot()["ready"])
                finally:
                    release.set()
                    session.stop()


class DialTests(unittest.TestCase):
    def blocked_socket(self):
        writer, reader = socket.socketpair()
        writer.setblocking(False)
        try:
            while True:
                writer.send(bytes(4096))
        except BlockingIOError:
            pass

        class Pending:
            def __init__(self):
                self.started = threading.Event()

            def fileno(self):
                return writer.fileno()

            def setblocking(self, value):
                writer.setblocking(value)

            def connect_ex(self, _address):
                self.started.set()
                return errno.EINPROGRESS

            def getsockopt(self, *args):
                return writer.getsockopt(*args)

            def shutdown(self, how):
                writer.shutdown(how)

            def close(self):
                writer.close()

        self.addCleanup(reader.close)
        self.addCleanup(writer.close)
        return Pending()

    def test_slow_first_family_does_not_delay_a_working_second_family(self):
        blocked = self.blocked_socket()
        original = socket.socket
        with Fixture() as fixture:
            port = fixture.server.server_port
            addresses = [(socket.AF_INET6, socket.SOCK_STREAM, 0, "", ("::1", port, 0, 0)),
                         (socket.AF_INET, socket.SOCK_STREAM, 0, "", ("127.0.0.1", port))]

            def create_socket(family, *args, **kwargs):
                return blocked if family == socket.AF_INET6 and "fileno" not in kwargs else original(family, *args, **kwargs)

            control = _Control()
            began = time.monotonic()
            with mock.patch("entropy_gui.health.socket.getaddrinfo", return_value=addresses), \
                    mock.patch("entropy_gui.health.socket.socket", side_effect=create_socket):
                connection = _connect(("fixture.invalid", port), 1, None, control)
            try:
                self.assertLess(time.monotonic() - began, .8)
                self.assertEqual(connection.getpeername(), ("127.0.0.1", port))
                self.assertEqual(blocked.fileno(), -1)
            finally:
                connection.close()

    def test_cancel_closes_a_pending_dial_without_an_address_worker_thread(self):
        blocked = self.blocked_socket()
        addresses = [(socket.AF_INET6, socket.SOCK_STREAM, 0, "", ("::1", 12345, 0, 0))]
        control = _Control()
        failures = []

        def dial():
            try:
                _connect(("fixture.invalid", 12345), 2, None, control)
            except OSError as error:
                failures.append(error)

        with mock.patch("entropy_gui.health.socket.getaddrinfo", return_value=addresses), \
                mock.patch("entropy_gui.health.socket.socket", return_value=blocked):
            worker = threading.Thread(target=dial)
            worker.start()
            self.assertTrue(blocked.started.wait(1))
            began = time.monotonic()
            control.abort()
            worker.join(timeout=.5)
        self.assertLess(time.monotonic() - began, .5)
        self.assertFalse(worker.is_alive())
        self.assertTrue(failures)
        self.assertEqual(blocked.fileno(), -1)


if __name__ == "__main__":
    unittest.main()
