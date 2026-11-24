#!/usr/bin/env python3
"""Measure simultaneous raw HTTP and WebSocket acquisition on a dedicated Linux server."""
import argparse
from contextlib import ExitStack
import hashlib
import json
import math
import os
from pathlib import Path
import platform
import subprocess
import sys
import time
from urllib.request import urlopen

MIB = 1 << 20
METRICS = [f'{name}_rejected_{reason}_total' for name, reasons in (
    ('ws', ('connections', 'memory', 'size')),
    ('http_generation', ('requests', 'memory', 'size')),
) for reason in reasons]


def binary_digest(path):
    with path.open('rb') as binary:
        return hashlib.file_digest(binary, 'sha256').hexdigest()


def rejection_counts(origin):
    with urlopen(origin + '/metrics', timeout=5) as response:
        fields = dict(line.split() for line in response.read().decode().splitlines()
                      if line and not line.startswith('#'))
    return {name: int(fields[name]) for name in METRICS}


def allocated_bytes():
    with urlopen('http://127.0.0.1:6060/debug/pprof/heap?debug=1', timeout=10) as response:
        for line in response:
            if line.startswith(b'# TotalAlloc = '):
                return int(line.split()[-1])
    raise ValueError('TotalAlloc missing from the server heap profile')


def cpu_seconds(proc):
    # Linux /proc/PID/stat fields 14 and 15 follow a parenthesized process name.
    fields = (proc / 'stat').read_text().rsplit(')', 1)[1].split()
    return (int(fields[11]) + int(fields[12])) / os.sysconf('SC_CLK_TCK')


def resident_bytes(proc):
    return int((proc / 'statm').read_text().split()[1]) * os.sysconf('SC_PAGE_SIZE')


def stop(child):
    if child.poll() is None:
        child.terminate()
        try:
            child.wait(timeout=5)
        except subprocess.TimeoutExpired:
            child.kill()
            child.wait()


def summarize(reports, elapsed, frame_bytes):
    waits = []
    received = 0
    for path in reports:
        summary = None
        last_frame = 0
        with path.open() as records:
            for line in records:
                record = json.loads(line)
                if summary is not None:
                    raise ValueError(f'Record after run_summary in {path}')
                if record['type'] == 'sample':
                    wait = record['receive_wait_ms']
                    if (not math.isfinite(wait) or wait < 0 or record['frame_bytes'] != frame_bytes
                            or record['received_frames'] <= last_frame):
                        raise ValueError(f'Invalid timed frame in {path}')
                    waits.append(wait)
                    last_frame = record['received_frames']
                elif record['type'] == 'run_summary':
                    summary = record
        if (not summary or summary['failed'] or summary['termination'] != 'duration'
                or not summary['measurement_began'] or not last_frame
                or last_frame > summary['received_frames']
                or summary['received_bytes'] != frame_bytes * summary['received_frames']):
            raise ValueError(f'Incomplete acquisition in {path}')
        received += summary['received_bytes']
    if not waits:
        raise ValueError('No completed frames in acquisition')
    waits.sort()
    return {'frames_timed': len(waits), 'received_mib_per_s': received / MIB / elapsed,
            'receive_p95_ms': waits[math.ceil(.95 * len(waits)) - 1],
            'receive_p99_ms': waits[math.ceil(.99 * len(waits)) - 1]}


def run_case(args, size, clients):
    origin = f'http://127.0.0.1:{args.port}'
    proc = Path('/proc') / str(args.server_pid)
    prefix = f'{size}MiB-{clients}per-transport'
    reports = {'pull': [], 'push': []}
    jobs = []
    initial_rejections = rejection_counts(origin)
    initial_alloc = allocated_bytes()
    cpu = cpu_seconds(proc)
    peak = resident_bytes(proc)
    started = time.monotonic()
    with ExitStack() as cleanup:
        for index in range(clients):
            for mode, paths in reports.items():
                path = args.out / f'{prefix}-{mode}-{index}.jsonl'
                paths.append(path)
                endpoint = (f'-url={origin}/v1/data/random' if mode == 'pull' else
                            f'-wsurl=ws://127.0.0.1:{args.port}/stream')
                command = [str(args.client.resolve()), '-headless', f'-mode={mode}', endpoint,
                           f'-slice={size * MIB}', '-refresh=50ms', '-analysis-interval=0',
                           '-analysis-bytes=256', '-capabilities=off', f'-duration={args.seconds}s',
                           f'-report={path}']
                log = cleanup.enter_context(path.with_suffix('.log').open('x'))
                child = subprocess.Popen(command, stdout=log, stderr=subprocess.STDOUT)
                cleanup.callback(stop, child)
                jobs.append(child)
        while any(child.poll() is None for child in jobs):
            peak = max(peak, resident_bytes(proc))
            if time.monotonic() - started > args.seconds + 30:
                raise TimeoutError(f'Client did not finish in {prefix}')
            time.sleep(.05)
    elapsed = time.monotonic() - started
    used_cpu = cpu_seconds(proc) - cpu
    alloc_rate = (allocated_bytes() - initial_alloc) / MIB / elapsed
    rejections = {k: n - initial_rejections[k] for k, n in rejection_counts(origin).items()}
    print(json.dumps({'sample_mib': size, 'clients_per_transport': clients,
                      'server_cpu_cores': used_cpu / elapsed, 'server_peak_sampled_rss_mib': peak / MIB,
                      'server_allocated_mib_per_s': alloc_rate, 'admission_rejections': rejections,
                      'elapsed_s': elapsed, 'client_exit_codes': [j.returncode for j in jobs],
                      'transport': {mode: summarize(paths, elapsed, size * MIB) for mode, paths in reports.items()}
                      if all(j.returncode == 0 for j in jobs) else {}}), flush=True)
    if any(j.returncode for j in jobs):
        raise RuntimeError(f'Acquisition failed in {prefix}; inspect the client logs')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--client', type=Path, default=Path('bin/entropy-client'))
    parser.add_argument('--server-pid', type=int, required=True, help='PID of the local entropy-server with -debug=true')
    parser.add_argument('--port', type=int, default=8080)
    parser.add_argument('--seconds', type=int, default=15, help='measurement duration per case')
    parser.add_argument('--out', type=Path, required=True, help='new directory for JSONL and client logs')
    args = parser.parse_args()
    if sys.platform != 'linux' or args.seconds < 1:
        parser.error('Linux and a positive --seconds value are required')
    proc = Path('/proc') / str(args.server_pid)
    with urlopen('http://127.0.0.1:6060/debug/pprof/cmdline', timeout=5) as response:
        if response.read() != (proc / 'cmdline').read_bytes().removesuffix(b'\0'):
            parser.error('The debug listener and --server-pid identify different processes')
    args.out.mkdir(parents=True, exist_ok=False)
    print(json.dumps({'host': platform.platform(), 'checkout_revision': subprocess.check_output(
        ['git', 'rev-parse', 'HEAD'], text=True).strip(), 'server_pid': args.server_pid,
        'server_sha256': binary_digest(proc / 'exe'), 'client_sha256': binary_digest(args.client),
        'duration_s': args.seconds, 'refresh_ms': 50}), flush=True)
    for size in (1, 8, 32):
        for clients in (1, 4):
            run_case(args, size, clients)


if __name__ == '__main__':
    main()
