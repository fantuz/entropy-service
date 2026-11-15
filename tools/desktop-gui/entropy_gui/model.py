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
        self._revision = 0

    def reset(self):
        with self._lock:
            self._rows.clear()
            self._settings = {}
            self._sample = {}
            self._summary = {}
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
            with self._lock:
                self._settings = settings
                self._revision += 1
            return
        if kind not in ("sample", "run_summary"):
            raise ReportError("Unknown report record type")
        _number(record, "received_bytes", integer=True, maximum=(1 << 64) - 1)
        _number(record, "elapsed_ms")
        assessment_key = "shannon_assessment" if kind == "sample" else "last_shannon_assessment"
        if record.get(assessment_key) not in ("OK", "WARN", "NO_SAMPLE"):
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
            if type(record.get("failed")) is not bool:
                raise ReportError("Invalid summary outcome")
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
            else:
                self._summary = clean
            self._revision += 1

    def snapshot(self):
        with self._lock:
            return {"settings": dict(self._settings), "sample": dict(self._sample),
                    "summary": dict(self._summary), "rows": tuple(self._rows), "revision": self._revision}
