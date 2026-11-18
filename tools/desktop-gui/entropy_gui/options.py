"""Editable startup options without GTK, shell parsing or lossy argv rebuilding.

The CLI remains responsible for validating option values. Only changed fields
are written back, so advanced arguments and unredacted endpoints keep their
original spelling when the user edits an unrelated field.
"""
from dataclasses import dataclass
import platform
import sys

from .process import _arguments, _CLIENT_RESERVED, _INSPECT_RESERVED


@dataclass(frozen=True)
class OptionField:
    name: str
    label: str
    group: str
    kind: str = "text"
    choices: tuple = ()
    hint: str = ""


_CLIENT_FIELDS = (
    OptionField("mode", "Transport", "Connection", "choice", ("pull", "push"),
                "Pull uses the HTTP endpoint; push uses the WebSocket endpoint."),
    OptionField("url", "HTTP endpoint (pull)", "Connection",
                hint="Only used in pull mode. Unchanged URL credentials and query parameters are retained."),
    OptionField("wsurl", "WebSocket endpoint (push)", "Connection",
                hint="Only used in push mode."),
    OptionField("capabilities", "Server capability discovery", "Connection", "choice", ("auto", "off", "required"),
                "Auto checks recognized entropy-service routes once before acquisition; required fails if discovery is unavailable."),
    OptionField("adaptive", "Adaptive HTTP pull pacing", "Sampling", "bool",
                hint="Opt in: slow requests under load, then recover gradually. Payload, analysis and failure policies are preserved; unavailable in push mode."),
    OptionField("adaptive-max-interval", "Maximum adaptive interval", "Sampling", hint="Go duration from the request interval to 1m; default 5s."),
    OptionField("slice", "Bytes per response", "Sampling", hint="1–33554432 bytes (32 MiB), subject to the server's configured maximum."),
    OptionField("refresh", "Request interval", "Sampling", hint="Go duration, for example 50ms or 1s."),
    OptionField("analysis-bytes", "Analysis prefix bytes", "Sampling", hint="0 analyses the full frame; up to 33554432 bytes (32 MiB)."),
    OptionField("analysis-interval", "Analysis interval", "Sampling",
                hint="Go duration; 0 analyses every frame."),
    OptionField("shannon-threshold", "Shannon threshold (bits/byte)", "Assessment", hint="0–8 bits/byte."),
    OptionField("waterfall-mode", "Waterfall mapping", "Assessment", "choice", ("byte", "rgb", "gray")),
    OptionField("duration", "Run duration", "Run limits", hint="Go duration; 0 runs until stopped."),
    OptionField("retries", "Transport retries", "Run limits", hint="0–100; 0 fails immediately."),
    OptionField("retry-delay", "Initial retry delay", "Run limits", hint="Go duration from 1ms to 30s."),
)

_SERVER_FIELDS = (
    OptionField("device", "Seed source", "Seed and DRBG",
                hint="OS source, device path or a supported hardware selector."),
    OptionField("drbg", "Output backend", "Seed and DRBG", "choice", ("aes-256-ctr", "chacha20", "rdrand-experimental"),
                "RDRAND is experimental direct CPU output: native x64 only, CPU-only trust, no software fallback."),
    OptionField("require-device", "Require selected seed device", "Seed and DRBG", "bool"),
    OptionField("cpu-mix", "CPU mixing", "Seed and DRBG", "bool"),
    OptionField("rdrand-mix", "RDRAND mixing", "Seed and DRBG", "bool"),
    OptionField("http", "HTTP listener", "Listeners and TLS", hint="For example 127.0.0.1:8080."),
    OptionField("enable-https", "Enable HTTPS", "Listeners and TLS", "bool"),
    OptionField("https", "HTTPS listener", "Listeners and TLS", hint="For example 127.0.0.1:8443."),
    OptionField("cert-file", "TLS certificate file", "Listeners and TLS", "path",
                hint="Public certificate chain PEM; startup inspection does not open this file."),
    OptionField("key-file", "TLS private key file", "Listeners and TLS", "path",
                hint="Matching private key PEM; only its path is edited here."),
    OptionField("cookie", "Remember HTTP preferences", "Listeners and TLS", "bool",
                hint="Remember byte/word counts, refresh and word language in Secure, HttpOnly cookies. Browser persistence requires HTTPS."),
    OptionField("debug", "Enable local debug / pprof listener", "Listeners and TLS", "bool",
                hint="Available on Linux, Windows and macOS at http://127.0.0.1:6060/debug/pprof/. Takes effect on Start; loopback only."),
    OptionField("lab-sources", "LAB source registry JSON", "LAB sources and access", "path",
                hint="Optional operator registry file. Empty disables registered sources; browser-local LAB tools remain available. Read when the server starts, not during validation."),
    OptionField("lab-allow-remote", "Allow remote access to registered LAB sources", "LAB sources and access", "bool",
                hint="Off keeps registered bridges restricted to loopback clients and trusted Host names. Enable only for intended remote clients."),
    OptionField("lab-allowed-hosts", "Trusted LAB Host names", "LAB sources and access",
                hint="Optional comma-separated exact names/IPs, without ports or wildcards. Does not enable remote peers."),
    OptionField("lab-max-connections", "Maximum LAB bridges", "LAB sources and access",
                hint="Positive integer; shared by registered source bridges."),
    OptionField("lab-memory-mib", "LAB memory budget (MiB)", "LAB sources and access",
                hint="Positive integer; estimated bridge working memory, not a process RSS limit."),
    OptionField("reseed-ms", "Reseed interval (ms)", "Reseed policy", hint="Plain integer from 1 to 60000."),
    OptionField("max-reseed-age", "Maximum seed age (ms)", "Reseed policy",
                hint="0 chooses the CLI's automatic age limit."),
    OptionField("on-reseed-failure", "Reseed failure policy", "Reseed policy", "choice", ("stop", "continue"),
                "Stop pauses generation on failed/stale reseeds; continue permits old-state output."),
    OptionField("buffer-reseed", "Bytes per reseed", "Reseed policy", hint="32–1048576 bytes."),
    OptionField("source-timeout", "Source read timeout", "Reseed policy", hint="Go duration from 1ms to 5m."),
    OptionField("max-bytes", "Maximum sample bytes", "Resource limits", hint="Up to 33554432 bytes (32 MiB), the server default. Also bounds LAB samples."),
)

_FIELDS = {"client": _CLIENT_FIELDS, "server": _SERVER_FIELDS}

# All Go boolean flags, including ones not exposed in the editor. Their bare
# form consumes no next argument. Unknown/advanced scalar flags are otherwise
# kept as opaque option/value pairs, including values beginning with '-'.
_BOOL_FLAGS = {
    "client": frozenset(("writeout", "debug", "preview", "matrix", "graph", "dashboard", "histogram",
                         "waterfall", "report-waterfall", "report-performance", "describe-json", "headless", "adaptive")),
    "server": frozenset(("describe-json", "lab-allow-remote", "enable-https", "require-device",
                         "rdrand-mix", "cpu-mix", "debug", "cookie")),
}

_CLIENT_DEFAULTS = {
    "capabilities": "auto", "adaptive": False, "adaptive-max-interval": "5s",
    "mode": "pull", "url": "http://127.0.0.1:8080/v1/data/random", "wsurl": "ws://127.0.0.1:8080/stream",
    "slice": "2097152", "refresh": "1s", "analysis-bytes": "1048576", "analysis-interval": "250ms",
    "shannon-threshold": "7.999", "waterfall-mode": "byte", "duration": "0s", "retries": "0", "retry-delay": "1s",
}
_SERVER_DEFAULTS = {
    "http": ":8080", "enable-https": True, "https": ":8443", "cert-file": "cert.pem", "key-file": "key.pem",
    "require-device": False, "cpu-mix": False, "rdrand-mix": False, "cookie": False,
    "debug": False, "lab-sources": "", "lab-allow-remote": False, "lab-allowed-hosts": "",
    "lab-max-connections": "8", "lab-memory-mib": "32",
    "reseed-ms": "15000", "max-reseed-age": "0", "on-reseed-failure": "stop", "buffer-reseed": "256",
    "source-timeout": "3s", "max-bytes": "33554432",
}
_DESCRIPTION_SETTINGS = {
    "client": {"capabilities":"capabilities", "adaptive":"adaptive", "adaptive-max-interval":"adaptive_max_interval", "mode": "mode", "slice": "payload_bytes", "refresh": "refresh", "analysis-bytes": "analysis_bytes",
               "analysis-interval": "analysis_interval", "shannon-threshold": "shannon_threshold_bits_per_byte",
               "duration": "duration", "retries": "retries", "retry-delay": "retry_delay", "waterfall-mode": "waterfall_mode"},
    "server": {"device": "seed_source", "drbg": "drbg", "http": "http_address", "https": "https_address",
               "reseed-ms": "reseed_interval_ms", "max-reseed-age": "max_reseed_age_ms",
               "on-reseed-failure": "reseed_failure_policy", "buffer-reseed": "reseed_bytes",
               "source-timeout": "source_timeout", "max-bytes": "max_bytes",
               "lab-max-connections": "lab_max_connections", "lab-memory-mib": "lab_memory_mib"},
}
_DESCRIPTION_FEATURES = {"enable-https": "https", "require-device": "require_device", "cpu-mix": "cpu_mix",
                         "rdrand-mix": "rdrand_mix", "cookie": "cookie", "debug": "debug",
                         "lab-allow-remote": "lab_allow_remote"}


def fields_for(role):
    """Return the explicit editable allowlist for one utility."""
    try:
        return _FIELDS[role]
    except (KeyError, TypeError) as error:
        raise ValueError("Unknown utility role") from error


def _validated_arguments(role, arguments):
    fields_for(role)
    return _arguments(arguments, _CLIENT_RESERVED if role == "client" else _INSPECT_RESERVED)


def _spans(role, arguments):
    """Yield untouched argv slices and their option name/value, without a shell."""
    index = 0
    while index < len(arguments):
        start = index
        token = arguments[index]
        index += 1
        if token == "-" or not token.startswith("-"):
            yield start, index, None, None
            continue
        name, equals, value = token[2 if token.startswith("--") else 1:].partition("=")
        if not equals:
            if name in _BOOL_FLAGS[role]:
                value = True
            elif index < len(arguments):
                value = arguments[index]
                index += 1
            else:
                value = ""
        yield start, index, name, value


def _boolean(value):
    if type(value) is bool:
        return value
    if isinstance(value, str):
        if value in ("1", "t", "T", "TRUE", "true", "True"):
            return True
        if value in ("0", "f", "F", "FALSE", "false", "False"):
            return False
    raise ValueError("Boolean startup options must be true or false")


def _mode(value):
    return {"http": "pull", "stream": "push", "websocket": "push"}.get(value, value)


def initial_values(role, arguments, descriptor=None):
    """Read display values, preferring explicit original arguments over reports.

    Reports intentionally omit credentials, queries and TLS paths. They are
    therefore fallbacks for display, never a replacement for original argv.
    """
    arguments = _validated_arguments(role, arguments)
    if role == "client":
        values = dict(_CLIENT_DEFAULTS)
    else:
        values = dict(_SERVER_DEFAULTS)
        values["device"] = "windows-csprng" if sys.platform == "win32" else "macos-csprng" if sys.platform == "darwin" else "/dev/qrandom0"
        values["drbg"] = "chacha20" if platform.machine().lower().startswith(("arm", "aarch", "riscv")) else "aes-256-ctr"
    if descriptor is not None:
        if not isinstance(descriptor, dict):
            raise ValueError("Startup description must be an object")
        settings, features = descriptor.get("settings", {}), descriptor.get("features", {})
        if not isinstance(settings, dict) or not isinstance(features, dict):
            raise ValueError("Startup description settings and features must be objects")
        for flag, key in _DESCRIPTION_SETTINGS[role].items():
            if key in settings and type(settings[key]) in (str, int, float):
                values[flag] = str(settings[key])
        if role == "server":
            for flag, key in _DESCRIPTION_FEATURES.items():
                if type(features.get(key)) is bool:
                    values[flag] = features[key]
        elif isinstance(settings.get("endpoint"), str):
            selected = "wsurl" if _mode(settings.get("mode", "pull")) == "push" else "url"
            values[selected] = settings["endpoint"]
    exposed = {field.name: field for field in fields_for(role)}
    for _start, _end, name, value in _spans(role, arguments):
        if name in exposed:
            values[name] = _boolean(value) if exposed[name].kind == "bool" else value
    if role == "client":
        values["mode"] = _mode(values["mode"])
    return values


def edited_arguments(role, arguments, updates):
    """Replace only named changed fields, retaining every other token verbatim."""
    arguments = _validated_arguments(role, arguments)
    if not isinstance(updates, dict):
        raise ValueError("Changed startup options must be an object")
    exposed = {field.name: field for field in fields_for(role)}
    replacements = {}
    for name, value in updates.items():
        if name not in exposed:
            raise ValueError("This startup option is not editable in the GUI: " + str(name))
        if exposed[name].kind == "bool":
            replacements[name] = "true" if _boolean(value) else "false"
        elif not isinstance(value, str):
            raise ValueError("Startup option values must be text")
        elif "\x00" in value or value == "--":
            raise ValueError("Startup option values cannot contain NUL or an argument terminator")
        else:
            replacements[name] = value
    result = []
    for start, end, name, _value in _spans(role, arguments):
        if name not in replacements:
            result.extend(arguments[start:end])
    result.extend("-" + name + "=" + value for name, value in replacements.items())
    return result
