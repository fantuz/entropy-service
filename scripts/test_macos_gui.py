#!/usr/bin/env python3
"""Run existing GUI model and actual GTK/owned-process checks on native macOS."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--bin',type=Path,required=True)
    parser.add_argument('--artifacts',type=Path,required=True)
    args=parser.parse_args()
    assert sys.platform=='darwin'
    # Exercise TLS health tests with the selected packaged binary, even though
    # it is outside bin/. An explicitly missing path must fail, never skip.
    environment=dict(os.environ, ENTROPY_GUI_TEST_SERVER=str((args.bin/'entropy-server').resolve()))
    failures=[]
    for pattern in ('test_desktop_gui*.py','test_gui_health.py'):
        result=subprocess.run([sys.executable,'-m','unittest','discover','-s','scripts','-p',pattern],check=False,env=environment)
        if result.returncode:failures.append(pattern)
    command=[sys.executable,'-X','faulthandler','scripts/test_desktop_gui_gtk.py',
             '--server',str(args.bin/'entropy-server'),'--client',str(args.bin/'entropy-client')]
    # AppKit has one native application lifecycle per process. Avoid carrying
    # pending fullscreen/native-window callbacks between GtkApplication objects.
    subprocess.run([*command,'--skip-fullscreen-outcomes','--artifacts',str(args.artifacts)],check=True,timeout=240)
    evidence=json.loads((args.artifacts/'results.json').read_text())
    assert evidence['passed'] and evidence['platform']=='darwin',evidence
    assert {'server','controls','terminal_interrupts','live_health','client','inspection_close'} <= evidence.keys(),evidence
    assert evidence['terminal_interrupts']['client_fullscreen_close']['fullscreen_close_requested'],evidence
    evidence['fullscreen_outcomes']={}
    for outcome in ('Duration','Failure'):
        folder=args.artifacts/('fullscreen-'+outcome.lower())
        subprocess.run([*command,'--fullscreen-outcome',outcome,'--artifacts',str(folder)],check=True,timeout=60)
        selected=json.loads((folder/'results.json').read_text())
        assert selected['passed'] and selected['platform']=='darwin',selected
        assert set(selected['fullscreen_outcomes'])=={outcome.lower()},selected
        evidence['fullscreen_outcomes'].update(selected['fullscreen_outcomes'])
    (args.artifacts/'results.json').write_text(json.dumps(evidence,indent=2)+'\n')
    if failures:raise RuntimeError('GUI model tests failed: '+', '.join(failures))


if __name__=='__main__':main()
