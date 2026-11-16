#!/usr/bin/env python3
"""Install, relocate-test and uninstall the GUI without the build-time runtime."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time


ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--installer", type=Path, required=True)
    parser.add_argument("--msys-prefix", type=Path, required=True)
    parser.add_argument("--artifacts", type=Path, required=True)
    args = parser.parse_args()
    assert sys.platform == "win32"
    prefix = args.msys_prefix.resolve()
    assert prefix.name.lower() == "ucrt64" and (prefix / "bin/python.exe").is_file()
    assert Path(sys.prefix).resolve() != prefix
    hidden = prefix.with_name("ucrt64-unavailable-for-installer-test")
    assert not hidden.exists()
    artifacts = args.artifacts.resolve()
    artifacts.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="entropy installed GUI ") as temporary:
        work = Path(temporary)
        installation = work / "Application with spaces"
        state = work / "User data"
        state.mkdir()
        sentinel = state / "retain.txt"
        sentinel.write_text("retain user data")
        subprocess.run([str(args.installer.resolve()), "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/SP-",
                        "/DIR=" + str(installation), "/LOG=" + str(artifacts / "install.log")], check=True, timeout=180)
        try:
            manifest = json.loads((installation / "bundle-manifest.json").read_text())
            assert manifest["packages"] and manifest["sources"]
            prefix.rename(hidden)
            try:
                environment = dict(os.environ)
                environment["PATH"] = os.pathsep.join([str(installation / "runtime/bin"), str(Path(os.environ["SystemRoot"]) / "System32"), os.environ["SystemRoot"]])
                environment["LOCALAPPDATA"] = str(state)
                for name in ("PYTHONPATH", "PYTHONHOME", "GI_TYPELIB_PATH", "GSETTINGS_SCHEMA_DIR", "XDG_DATA_DIRS"):
                    environment.pop(name, None)
                python = installation / "runtime/bin/python.exe"
                subprocess.run([str(python), "-I", "-B", str(installation / "app/gui_bootstrap.py"), "--help"], env=environment, check=True, timeout=30)
                bootstrap = installation / "app/gui_bootstrap.py"
                script = ROOT / "scripts/test_desktop_gui_gtk.py"
                code = ("import importlib.util,runpy,sys; "
                        f"spec=importlib.util.spec_from_file_location('bundle_bootstrap',{str(bootstrap)!r}); "
                        "module=importlib.util.module_from_spec(spec); spec.loader.exec_module(module); "
                        f"module.configure_runtime({str(installation)!r},{str(state)!r}); "
                        f"sys.path.insert(0,{str(ROOT / 'scripts')!r}); "
                        f"sys.argv={[str(script), '--gui-source', str(installation / 'app'), '--server', str(installation / 'bin/entropy-server.exe'), '--client', str(installation / 'bin/entropy-client.exe'), '--artifacts', str(artifacts / 'gtk')]!r}; "
                        f"runpy.run_path({str(script)!r},run_name='__main__')")
                subprocess.run([str(python), "-I", "-B", "-c", code], env=environment, check=True, timeout=180)
            finally:
                hidden.rename(prefix)
        finally:
            uninstaller = installation / "unins000.exe"
            if uninstaller.exists():
                subprocess.run([str(uninstaller), "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART"], check=True, timeout=60)
            deadline = time.monotonic() + 30
            while (installation / "bin/entropy-server.exe").exists() and time.monotonic() < deadline:
                time.sleep(.2)
            assert not (installation / "bin/entropy-server.exe").exists()
            assert sentinel.read_text() == "retain user data"
    print("Installed GTK server/client, owned-process lifecycle and uninstall passed without MSYS2 runtime access")


if __name__ == "__main__":
    main()
