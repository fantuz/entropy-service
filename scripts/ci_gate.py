#!/usr/bin/env python3
"""Fail closed when any required validation is absent, cancelled or unsuccessful."""

import argparse
import json
import os


def failures(needs, mode, required, docs_skips=()):
    if mode not in ("full", "docs"):
        return ["CI plan did not succeed with a recognized mode"]
    errors = []
    for job in required:
        result = needs.get(job, {}).get("result", "missing")
        expected = "skipped" if mode == "docs" and job in docs_skips else "success"
        if result != expected:
            errors.append(f"{job}: {result}; expected {expected}")
    return errors


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mode", choices=("full", "docs"), required=True)
    parser.add_argument("--required", nargs="+", required=True)
    parser.add_argument("--docs-skips", nargs="*", default=[])
    args = parser.parse_args()
    errors = failures(json.loads(os.environ["NEEDS_JSON"]), args.mode, args.required, args.docs_skips)
    if errors:
        raise SystemExit("Validation gate failed:\n" + "\n".join(errors))
    print(f"All required {args.mode} checks succeeded.")


if __name__ == "__main__":
    main()
