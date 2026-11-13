#!/usr/bin/env python3
"""Build and run the opt-in server fault/endurance suite with saved evidence."""

import argparse
from datetime import datetime, timezone
import hashlib
import json
import math
import os
from pathlib import Path
import re
import subprocess
import time


ROOT = Path(__file__).resolve().parents[1]
SCENARIOS = {"ChaCha20", "AES-256-CTR", "source_cycles"}
RESULT_MARKER = "ENDURANCE_RESULT "


def duration_seconds(value):
    match = re.fullmatch(r"([0-9]+(?:\.[0-9]+)?)(s|m|h)", value)
    if match is None:
        raise argparse.ArgumentTypeError("use a duration such as 5s, 10m or 1h")
    seconds = float(match[1]) * {"s": 1, "m": 60, "h": 3600}[match[2]]
    if not 1 <= seconds <= 86400:
        raise argparse.ArgumentTypeError("duration must be between 1s and 24h")
    return seconds


def command_text(args):
    return subprocess.check_output(args, cwd=ROOT, text=True).strip()


def run_logged(command, path, timeout, environment):
    with path.open("x", encoding="utf-8") as log:
        with subprocess.Popen(command, cwd=ROOT, env=environment, stdout=log,
                              stderr=subprocess.STDOUT) as process:
            try:
                return process.wait(timeout=timeout), None
            except (subprocess.TimeoutExpired, KeyboardInterrupt) as error:
                process.terminate()
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
                reason = "interrupted" if isinstance(error, KeyboardInterrupt) else "watchdog_timeout"
                return process.returncode, reason


def read_results(path):
    results = []
    with path.open(encoding="utf-8", errors="replace") as log:
        for line in log:
            if RESULT_MARKER in line:
                results.append(json.loads(line.split(RESULT_MARKER, 1)[1]))
                if len(results) > len(SCENARIOS):
                    raise ValueError("more scenario results than expected")
    return results


def results_complete(results):
    return (len(results) == len(SCENARIOS)
            and {result.get("scenario") for result in results} == SCENARIOS
            and all(result.get("failed") is False and result.get("cycles", 0) >= 2
                    and not result.get("unexpected_errors") for result in results))


def run(args, folder):
    environment = os.environ.copy()
    environment["ENTROPY_ENDURANCE_DURATION"] = args.duration
    environment["ENTROPY_ENDURANCE_WORKERS"] = str(args.workers)
    binary = folder / ("server-endurance.test.exe" if os.name == "nt" else "server-endurance.test")
    build = ["go", "test", "-c", "-o", str(binary)]
    if args.race:
        build.append("-race")
    build.append("./cmd/entropy-server")
    timeout = math.ceil(3 * duration_seconds(args.duration)) + 120
    command = [str(binary), "-test.v", "-test.run=^TestEndurance", f"-test.timeout={timeout}s"]
    report = {
        "schema_version": 1,
        "started_at_utc": datetime.now(timezone.utc).isoformat(),
        "source_commit": command_text(["git", "rev-parse", "HEAD"]),
        "source_dirty": bool(command_text(["git", "status", "--porcelain"])),
        "go_version": command_text(["go", "version"]),
        "go_environment": json.loads(command_text(["go", "env", "-json", "GOOS", "GOARCH", "CGO_ENABLED"])),
        "duration_per_scenario": args.duration,
        "workers_per_backend": args.workers,
        "race": args.race,
        "seed_source": "public test fixtures / OS pipe or loopback TCP; no physical device",
        "build_command": build,
        "test_command": command,
        "test_timeout_seconds": timeout,
        "success": False,
        "results": [],
    }
    started = time.monotonic()
    try:
        code, reason = run_logged(build, folder / "build.log", 300, environment)
        report.update(build_exit_code=code, build_stop_reason=reason)
        if code != 0 or reason:
            return report
        with binary.open("rb") as file:
            report["test_binary_sha256"] = hashlib.file_digest(file, "sha256").hexdigest()
        code, reason = run_logged(command, folder / "endurance.log", timeout + 15, environment)
        report.update(test_exit_code=code, test_stop_reason=reason)
        report["results"] = read_results(folder / "endurance.log")
        report["success"] = code == 0 and reason is None and results_complete(report["results"])
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        report["runner_error"] = str(error)
    finally:
        report["elapsed_seconds"] = time.monotonic() - started
        with (folder / "report.json").open("x", encoding="utf-8") as file:
            json.dump(report, file, indent=2, ensure_ascii=False)
            file.write("\n")
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--duration", default="10s", help="per scenario, 1s..24h; total is about three times this")
    parser.add_argument("--workers", type=int, choices=range(1, 17), default=4)
    parser.add_argument("--race", action="store_true", help="instrument the test binary; needs a supported Go race toolchain")
    parser.add_argument("--artifacts", type=Path, default=ROOT / "endurance-artifacts",
                        help="new directory for report/logs and local test binary; never reused")
    args = parser.parse_args()
    try:
        duration_seconds(args.duration)
    except argparse.ArgumentTypeError as error:
        parser.error(str(error))
    folder = args.artifacts.resolve()
    try:
        folder.mkdir(parents=True, exist_ok=False)
    except OSError as error:
        parser.error(f"cannot create a new artifact directory: {error}")
    report = run(args, folder)
    print(f"Endurance {'PASS' if report['success'] else 'FAIL'}: {folder / 'report.json'}")
    return 0 if report["success"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
