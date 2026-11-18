#!/usr/bin/env python3
"""Run existing GUI model and actual GTK/owned-process checks on native macOS."""
import argparse
from pathlib import Path
import subprocess
import sys

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--bin',type=Path,required=True)
    parser.add_argument('--artifacts',type=Path,required=True)
    args=parser.parse_args()
    assert sys.platform=='darwin'
    failures=[]
    for pattern in ('test_desktop_gui*.py','test_gui_health.py'):
        result=subprocess.run([sys.executable,'-m','unittest','discover','-s','scripts','-p',pattern],check=False)
        if result.returncode:failures.append(pattern)
    subprocess.run([sys.executable,'scripts/test_desktop_gui_gtk.py','--server',str(args.bin/'entropy-server'),
                    '--client',str(args.bin/'entropy-client'),'--artifacts',str(args.artifacts)],check=True)
    if failures:raise RuntimeError('GUI model tests failed: '+', '.join(failures))


if __name__=='__main__':main()
