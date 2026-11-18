#!/usr/bin/env python3
"""Native macOS archive/CLI checks; no trust-store or privileged host changes."""
import argparse
import hashlib
import http.client
import json
import os
from pathlib import Path
import platform
import socket
import struct
import subprocess
import sys
import tarfile
import tempfile
import time


def prepare(directory, output):
    arch = os.environ['MACOS_ARCH']
    actual = {'x86_64': 'amd64', 'arm64': 'arm64'}.get(platform.machine())
    assert arch == actual, (arch, actual)
    archives = list(directory.glob('*_darwin_'+arch+'.tar.gz'))
    assert len(archives) == 1, archives
    sums = {line.split(maxsplit=1)[1].strip(): line.split()[0] for line in (directory/'checksums.txt').read_text().splitlines()}
    archive = archives[0]
    assert hashlib.sha256(archive.read_bytes()).hexdigest() == sums[archive.name]
    output.mkdir(parents=True, exist_ok=True)
    with tarfile.open(archive, 'r:gz') as source:
        for entry in source:
            assert not entry.name.startswith('/') and '..' not in Path(entry.name).parts
            assert entry.isfile() or entry.isdir()
        source.extractall(output, filter='data')
    for role in ('server', 'client'):
        file = output/('entropy-'+role)
        assert os.access(file, os.X_OK)
        with file.open('rb') as source: header = source.read(8)
        magic, cpu = struct.unpack('<II', header)
        assert magic == 0xfeedfacf and cpu == {'amd64': 0x1000007, 'arm64': 0x100000c}[arch], (file, header)
    print('Verified and extracted native '+arch+' Mach-O CLI archives')


def port():
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0)); return s.getsockname()[1]


def run(command, timeout=12, **kwargs):
    result = subprocess.run([str(x) for x in command], capture_output=True, timeout=timeout, **kwargs)
    assert result.returncode == 0, (command, result.stdout, result.stderr)
    return result


def smoke(folder):
    server, client = folder/'entropy-server', folder/'entropy-client'
    result = run([server, '-describe-json', '-enable-https=false'])
    description = json.loads(result.stdout)
    assert description['settings']['seed_source'] == 'macos-csprng', description
    with tempfile.TemporaryDirectory(prefix='entropy macOS native ') as directory:
        root = Path(directory); cert, key = root/'cert.pem', root/'key.pem'
        run([server, 'generate-cert', '-cert-file='+str(cert), '-key-file='+str(key), '-hosts=localhost,127.0.0.1'])
        assert key.stat().st_mode & 0o077 == 0
        http_port, tls_port = port(), port()
        with (root/'server.log').open('wb') as log:
            process = subprocess.Popen([str(server), f'-http=127.0.0.1:{http_port}', f'-https=127.0.0.1:{tls_port}',
                                        '-enable-https=true', '-cert-file='+str(cert), '-key-file='+str(key), '-reseed-ms=100'],
                                       stdout=log, stderr=log)
            try:
                until = time.monotonic()+8
                while True:
                    connection = http.client.HTTPConnection('127.0.0.1', http_port, timeout=.5)
                    try:
                        connection.request('GET', '/ready');response=connection.getresponse();health=json.loads(response.read())
                        if response.status==200:break
                    except OSError:
                        pass
                    finally: connection.close()
                    assert process.poll() is None and time.monotonic()<until,(root/'server.log').read_text()
                    time.sleep(.025)
                assert health['rng_source']=='macos-csprng' and health['generation_allowed'],health
                for mode, scheme, flag, route in [('pull','http','url','/v1/data/random'),('push','ws','wsurl','/stream')]:
                    target=root/(mode+'.bin'); report=root/(mode+'.jsonl')
                    run([client,'-headless','-mode='+mode,f'-{flag}={scheme}://127.0.0.1:{http_port}{route}',
                         '-slice=65536','-refresh=50ms','-analysis-bytes=256','-output='+str(target),'-output-bytes=65536','-report='+str(report)])
                    assert target.stat().st_size==65536 and target.stat().st_mode&0o077==0
                    assert not json.loads(report.read_text().splitlines()[-1])['failed']
                # Cold macOS system-trust initialization may exceed one second.
                # Require an actual TLS rejection, never count a duration expiry
                # with no bytes as successful certificate verification testing.
                for mode,scheme,flag,route in [('pull','https','url','/v1/data/random'),('push','wss','wsurl','/stream')]:
                    target=root/('untrusted-'+mode+'.bin');report=root/('untrusted-'+mode+'.jsonl')
                    rejected=subprocess.run([str(client),'-headless','-mode='+mode,f'-{flag}={scheme}://127.0.0.1:{tls_port}{route}',
                                             '-duration=8s','-retries=1','-retry-delay=1ms','-output='+str(target),'-report='+str(report)],
                                            capture_output=True,timeout=12)
                    summary=json.loads(report.read_text().splitlines()[-1])
                    assert rejected.returncode!=0 and b'certificate' in rejected.stderr.lower(),(rejected.returncode,rejected.stderr,summary)
                    assert summary['failed'] and summary['received_bytes']==0 and target.stat().st_size==0,summary
                raw=run([client,'-headless','-mode=pull',f'-url=http://127.0.0.1:{http_port}/v1/data/random',
                         '-slice=4096','-output=-','-output-bytes=4096'])
                assert len(raw.stdout)==4096
            finally:
                process.terminate()
                try: process.wait(timeout=5)
                except subprocess.TimeoutExpired: process.kill();process.wait();raise
                assert process.returncode==0,(root/'server.log').read_text()
    # Reuse the existing real Unix PTY suite with the packaged client.
    run([sys.executable, Path(__file__).with_name('test_client_terminal.py'), '--client', client], timeout=120)
    print('Native macOS OS source, HTTP/WS, TLS rejection, private files, stdout, PTY and shutdown passed')


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--prepare',type=Path);parser.add_argument('--output',type=Path);parser.add_argument('--bin',type=Path)
    args=parser.parse_args()
    if sys.platform!='darwin':parser.error('Native macOS execution is required')
    if args.prepare: prepare(args.prepare,args.output)
    else: smoke(args.bin.resolve())


if __name__=='__main__':main()
