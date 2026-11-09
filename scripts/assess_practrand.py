#!/usr/bin/env python3
"""Distinguish a completed PractRand screen from statistical review flags."""

import argparse
import json
from pathlib import Path
import re


def assess(manifest, text, count):
    if count <= 0 or count & (count - 1):
        raise ValueError("assessment requires a power-of-two byte target")
    complete = (manifest.get("status") == "complete"
                and manifest.get("bytes") == manifest.get("requested_bytes")
                and manifest.get("bytes", 0) >= count
                and re.search(r"length=.*\(2\^" + str(count.bit_length() - 1) + r" bytes\)", text) is not None)
    flags = [line.strip() for line in text.splitlines()
             if re.search(r"\b(unusual|suspicious|FAIL(?:ED)?)\b", line, re.IGNORECASE)]
    return {"completed": complete, "target_bytes": count,
            "review_required": bool(flags), "flags": flags,
            "interpretation": "statistical screen; no cryptographic certification"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manifest", type=Path, required=True)
    parser.add_argument("--report", type=Path, required=True)
    parser.add_argument("--bytes", type=int, required=True)
    args = parser.parse_args()
    result = assess(json.loads(args.manifest.read_text()), args.report.read_text(), args.bytes)
    print(json.dumps(result, indent=2))
    return 0 if result["completed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
