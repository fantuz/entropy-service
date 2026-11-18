#!/usr/bin/env python3
"""Run existing GUI model and actual GTK/owned-process checks on native macOS."""
import argparse
from pathlib import Path
import subprocess
import sys

parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--bin',type=Path,required=True)
parser.add_argument('--artifacts',type=Path,required=True)
args=parser.parse_args()
assert sys.platform=='darwin'
for pattern in ('test_desktop_gui*.py','test_gui_health.py'):
    subprocess.run([sys.executable,'-m','unittest','discover','-s','scripts','-p',pattern],check=True)
subprocess.run([sys.executable,'scripts/test_desktop_gui_gtk.py','--server',str(args.bin/'entropy-server'),
                '--client',str(args.bin/'entropy-client'),'--artifacts',str(args.artifacts)],check=True)
