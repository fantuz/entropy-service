#!/usr/bin/env python3
"""Capture bounded, decoded /stream output for external statistical tools."""

import argparse
import hashlib
import json
import math
from pathlib import Path
import sys
import time
from urllib.parse import parse_qsl, urlencode, urlsplit, urlunsplit


def capture(url, output, count, frame_bytes=65536, timeout=120, connect_fn=None):
    """Keep exact bytes and a completion manifest; never overwrite or rewind."""
    parsed = urlsplit(url)
    if parsed.scheme not in ("ws", "wss") or not parsed.hostname:
        raise ValueError("URL must use ws:// or wss:// and include a host")
    if not 1 <= count <= 1 << 40:
        raise ValueError("byte count must be 1..1099511627776")
    if not 1 <= frame_bytes <= 1 << 20:
        raise ValueError("frame size must be 1..1048576 bytes")
    if not math.isfinite(timeout) or not 0 < timeout <= 3600:
        raise ValueError("timeout must be positive, finite and at most 3600 seconds")
    if connect_fn is None:
        from websockets.sync.client import connect

        connect_fn = connect

    query = dict(parse_qsl(parsed.query, keep_blank_values=True))
    query.update(bytes=str(frame_bytes), refresh="1")
    endpoint = urlunsplit(parsed._replace(query=urlencode(query), fragment=""))
    # Never persist URL credentials or query parameters in the report.
    host = parsed.hostname
    if ":" in host:
        host = "[" + host + "]"
    if parsed.port:
        host += ":" + str(parsed.port)
    safe_url = urlunsplit((parsed.scheme, host, parsed.path, "", ""))
    output = Path(output)
    manifest = Path(str(output) + ".json")
    if manifest.exists() or manifest.is_symlink():
        raise FileExistsError("capture manifest already exists")
    digest = hashlib.sha256()
    result = {"status": "incomplete", "requested_bytes": count, "bytes": 0,
              "sha256": digest.hexdigest(), "source_url": safe_url,
              "frame_bytes": frame_bytes, "timeout_seconds": timeout}
    # Reserve both filenames exclusively before contacting the server.
    # A failure leaves explicit incomplete data rather than a plausible sample.
    with output.open("xb") as data, manifest.open("x", encoding="utf-8") as report:
        json.dump(result, report)
        report.flush()
        started = time.monotonic()
        try:
            with connect_fn(endpoint, max_size=frame_bytes, max_queue=4,
                            compression=None, open_timeout=min(10, timeout),
                            close_timeout=3) as connection:
                while result["bytes"] < count:
                    remaining = timeout - (time.monotonic() - started)
                    if remaining <= 0:
                        raise TimeoutError("capture deadline")
                    block = connection.recv(timeout=remaining)
                    if not isinstance(block, bytes) or not block:
                        raise ValueError("expected a nonempty binary message")
                    if len(block) > frame_bytes:
                        raise ValueError("message exceeds the requested size")
                    block = block[:count - result["bytes"]]
                    data.write(block)
                    digest.update(block)
                    result["bytes"] += len(block)
            data.flush()
            result["status"] = "complete"
        except (Exception, KeyboardInterrupt) as error:
            # Exception text from network clients may contain credentials.
            result["error_type"] = type(error).__name__
        finally:
            result["wall_seconds"] = time.monotonic() - started
            result["sha256"] = digest.hexdigest()
            report.seek(0)
            json.dump(result, report, indent=2)
            report.write("\n")
            report.truncate()
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--url", default="ws://127.0.0.1:8080/stream")
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--bytes", type=int, default=64 << 20)
    parser.add_argument("--frame-bytes", type=int, default=65536)
    parser.add_argument("--timeout", type=float, default=120)
    args = parser.parse_args()
    try:
        result = capture(args.url, args.output, args.bytes, args.frame_bytes, args.timeout)
    except (ValueError, OSError, ImportError) as error:
        print("capture setup failed: " + type(error).__name__, file=sys.stderr)
        return 2
    print(json.dumps(result))
    return 0 if result["status"] == "complete" else 1


if __name__ == "__main__":
    raise SystemExit(main())
