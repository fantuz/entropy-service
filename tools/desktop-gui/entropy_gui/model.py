"""Bounded, GTK-independent state for the optional desktop frontend."""
import base64
from collections import deque
import json
import math
import re
import threading
from urllib.parse import urlsplit, urlunsplit


MAX_RECORD_BYTES = 65536
MAX_PREFIX_BYTES = 256
MAX_HISTORY_ROWS = 128


class ReportError(ValueError):
    """An unsupported or malformed child-process report."""


def _constant(_value):
    raise ReportError("Non-finite JSON number")


def _unique(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ReportError("Duplicate JSON field")
        result[key] = value
    return result


def decode_record(data):
    if not isinstance(data, bytes) or len(data) > MAX_RECORD_BYTES:
        raise ReportError("Report exceeds the 64 KiB record limit")
    try:
        value = json.loads(data, parse_constant=_constant, object_pairs_hook=_unique)
    except (ValueError, UnicodeError, RecursionError) as error:
        raise ReportError("Invalid JSON report") from error
    if not isinstance(value, dict) or type(value.get("schema_version")) is not int or value["schema_version"] != 1:
        raise ReportError("Unsupported report schema")
    return value


def safe_endpoint(value):
    """Defensively omit URL credentials, arbitrary queries and fragments."""
    try:
        parts = urlsplit(value)
        return urlunsplit((parts.scheme, parts.netloc.rsplit("@", 1)[-1], parts.path, "", ""))
    except ValueError:
        return "[invalid endpoint]"


def safe_text(value):
    value = re.sub(r"\x1b\[[0-?]*[ -/]*[@-~]", "", value)
    value = "".join(char for char in value if char in "\n\t" or ord(char) >= 32 and ord(char) != 127)
    return re.sub(r"(?:https?|wss?)://[^\s<>]+", lambda match: safe_endpoint(match.group()), value, flags=re.IGNORECASE)


def client_failure_text(error, diagnostics):
    """Explain reported failures without guessing the backend's actual state."""
    error, diagnostics = safe_text(error), safe_text(diagnostics)
    lines = diagnostics.strip().splitlines()
    reason = lines[-1] if lines and error.startswith("entropy-client exited with status ") else error
    detail = (error + "\n" + diagnostics).lower()
    if "rdrand backend stopped" in detail or "cpu backend failed" in detail:
        hint = "The direct CPU backend reports a hardware-read failure. Check server logs/readiness; an operator restart is required."
    elif "server returned 400" in detail or "server returned 413" in detail:
        hint = "Check Bytes per response against the server's configured maximum and the reported request error."
    elif "timed out" in detail or "timeout" in detail or "deadline exceeded" in detail:
        hint = "A request timed out. Try a smaller payload or lower frame rate and check server/network timing."
    elif "unexpected eof" in detail or "connection reset" in detail or "closed with code 1006" in detail:
        hint = "The connection ended before a complete response. With large frames, transport deadlines can expire while the server remains healthy. Try a smaller sample and inspect server logs."
    elif "server returned 503" in detail:
        hint = "The server declined generation. Check its readiness and logs for the reported backend or reseed failure."
    else:
        hint = "See Client diagnostics for the complete CLI message."
    return reason + "\n" + hint


def _discovery(value):
    if value is None:
        return {"status": "not reported"}
    if not isinstance(value, dict) or value.get("status") not in ("available", "unavailable", "off", "custom endpoint; skipped"):
        raise ReportError("Invalid capability discovery status")
    result = {"status": value["status"]}
    if value["status"] == "available":
        document = value.get("document")
        if not isinstance(document, dict) or type(document.get("schema_version")) is not int or document.get("schema_version") != 1 or document.get("service") != "entropy-service":
            raise ReportError("Invalid server capabilities")
        maximum = _number(document, "max_bytes", integer=True, maximum=33554432)
        if not maximum:
            raise ReportError("Invalid server sample maximum")
        result["max_bytes"] = maximum
    return result


def performance_text(state):
    """Compact, scalar-only presentation; old companions may omit these fields."""
    discovery = state.get("discovery", {})
    status = discovery.get("status", "not reported")
    limit = discovery.get("max_bytes")
    server = f"Server capabilities: {status}" + (f" · max {limit:,} bytes" if limit else "")
    sample = state.get("sample", {})
    performance = state.get("performance") or sample
    if sample.get("elapsed_ms", 0) > performance.get("elapsed_ms", 0):
        performance = sample
    record = state.get("summary") or performance
    if "average_received_fps" not in record:
        return server + "\nPerformance: awaiting measurements"
    rates = (f"FPS: {record['requested_fps']:.2f} requested · {record['effective_target_fps']:.2f} effective · "
             f"{record['average_received_fps']:.2f} average received")
    timings = (f"Receive wait: {performance.get('receive_wait_ms', 0):.3f} ms · "
               f"analysis: {sample.get('analysis_ms', 0):.3f} ms · output: {record['output_ms']:.3f} ms")
    pacing = f"{record['pacing_status']} · interval {record['effective_interval_ms']:.3f} ms"
    return server + "\n" + rates + "\n" + timings + "\n" + pacing


def _settings(value):
    if not isinstance(value, dict):
        raise ReportError("Missing configuration settings")
    result = {}
    for key, item in value.items():
        if not isinstance(key, str) or len(key) > 100:
            raise ReportError("Invalid setting name")
        if isinstance(item, str):
            if len(item) > 4096:
                raise ReportError("Setting text is too long")
            result[key] = safe_endpoint(item) if key == "endpoint" else safe_text(item)
        elif type(item) in (bool, int, float) and (type(item) is not float or math.isfinite(item)):
            if type(item) is int and not -(1 << 64) <= item < 1 << 64:
                raise ReportError("Setting integer is out of range")
            result[key] = item
        else:
            raise ReportError("Invalid setting value")
    return result


def configuration(data, role):
    value = decode_record(data)
    if value.get("type") != "configuration" or value.get("role") != role or value.get("live") is not False:
        raise ReportError("Expected a read-only configuration report")
    settings = _settings(value.get("settings"))
    features = value.get("features")
    if not isinstance(features, dict) or any(type(item) is not bool for item in features.values()):
        raise ReportError("Invalid feature switches")
    return {"schema_version": 1, "type": "configuration", "role": role,
            "live": False, "settings": settings, "features": dict(features)}


def _number(record, key, *, integer=False, maximum=None):
    value = record.get(key)
    valid = type(value) is int if integer else type(value) in (int, float)
    bounded = type(value) is not int or -(1 << 64) <= value < 1 << 64
    if not valid or not bounded or not math.isfinite(value) or value < 0 or (maximum is not None and value > maximum):
        raise ReportError("Invalid report value: " + key)
    return value


class StreamModel:
    """Only latest statistics and a fixed number of raw-prefix rows are kept."""

    def __init__(self, history_limit=MAX_HISTORY_ROWS):
        if type(history_limit) is not int or not 1 <= history_limit <= MAX_HISTORY_ROWS:
            raise ValueError("History must contain 1–128 rows")
        self._lock = threading.Lock()
        self._rows = deque(maxlen=history_limit)
        self._settings = {}
        self._sample = {}
        self._summary = {}
        self._performance = {}
        self._discovery = {}
        self._revision = 0

    def reset(self):
        with self._lock:
            self._rows.clear()
            self._settings = {}
            self._sample = {}
            self._summary = {}
            self._performance = {}
            self._discovery = {}
            self._revision += 1

    def feed_line(self, data):
        if not isinstance(data, bytes) or len(data) > MAX_RECORD_BYTES:
            raise ReportError("Report exceeds the 64 KiB record limit")
        if not data.strip():
            return False
        self.consume(decode_record(data))
        return True

    def consume(self, record):
        if not isinstance(record, dict) or type(record.get("schema_version")) is not int or record["schema_version"] != 1:
            raise ReportError("Unsupported report schema")
        kind = record.get("type")
        if kind == "measurement_start":
            # A lifecycle marker emitted after preflight and before samples.
            with self._lock:
                self._revision += 1
            return
        if kind == "run_start":
            settings = _settings(record.get("settings"))
            _number(settings, "shannon_threshold_bits_per_byte", maximum=8)
            discovery = _discovery(record.get("discovery"))
            with self._lock:
                self._settings = settings
                self._discovery = discovery
                self._revision += 1
            return
        if kind not in ("sample", "run_summary", "performance"):
            raise ReportError("Unknown report record type")
        _number(record, "received_bytes", integer=True, maximum=(1 << 64) - 1)
        _number(record, "elapsed_ms")
        assessment_key = "shannon_assessment" if kind == "sample" else "last_shannon_assessment"
        if kind != "performance" and record.get(assessment_key) not in ("OK", "WARN", "NO_SAMPLE"):
            raise ReportError("Invalid Shannon assessment")
        row = None
        if kind == "sample":
            _number(record, "shannon_bits_per_byte", maximum=8)
            _number(record, "sample_bytes", integer=True, maximum=33554432)
            prefix = record.get("waterfall_prefix_base64")
            if prefix is not None:
                if not isinstance(prefix, str) or len(prefix) > 344:
                    raise ReportError("Waterfall prefix exceeds 256 bytes")
                try:
                    row = base64.b64decode(prefix, validate=True)
                except (ValueError, TypeError) as error:
                    raise ReportError("Invalid waterfall prefix") from error
                if len(row) > min(MAX_PREFIX_BYTES, record["sample_bytes"]):
                    raise ReportError("Waterfall prefix exceeds the analysed sample")
        else:
            _number(record, "received_mib_per_second")
            if kind == "run_summary" and type(record.get("failed")) is not bool:
                raise ReportError("Invalid summary outcome")
        if kind == "performance" or "average_received_fps" in record:
            for name in ("average_received_fps", "requested_fps", "effective_target_fps", "requested_mib_per_second",
                         "effective_interval_ms", "output_ms", "total_analysis_ms"):
                _number(record, name)
            if kind == "performance":
                _number(record, "receive_wait_ms")
                _number(record, "analysis_ms")
            if record.get("pacing_status") not in ("warming up", "on target", "below target", "adaptive paced", "adaptive ceiling"):
                raise ReportError("Invalid pacing status")
        # Keep only scalar values; do not retain encoded-prefix duplicates or
        # arbitrary nested report payloads alongside the bounded pixel history.
        clean = {key: safe_text(value) if isinstance(value, str) else value
                 for key, value in record.items()
                 if key != "waterfall_prefix_base64" and type(value) in (str, int, float, bool)}
        if any(type(value) is float and not math.isfinite(value) for value in clean.values()):
            raise ReportError("Non-finite report value")
        with self._lock:
            if kind == "sample":
                self._sample = clean
                if row:
                    self._rows.appendleft(row)
            elif kind == "performance":
                self._performance = clean
            else:
                self._summary = clean
            self._revision += 1

    def snapshot(self):
        with self._lock:
            return {"performance": dict(self._performance), "discovery": dict(self._discovery), "settings": dict(self._settings), "sample": dict(self._sample),
                    "summary": dict(self._summary), "rows": tuple(self._rows), "revision": self._revision}
