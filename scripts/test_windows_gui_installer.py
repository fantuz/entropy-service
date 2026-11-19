#!/usr/bin/env python3
"""Install, relocate-test and uninstall the GUI without the build-time runtime."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import urllib.request

from test_windows_gui_upgrade import Wizard, explorer_folder, programs_folder, registry_values


ROOT = Path(__file__).resolve().parents[1]
BASELINE_VERSION = "2.6.0"
BASELINE_SHA256 = "b63746ecc3af4df9ab212d444184ddc4535df541793bd3cea0b9f8ec7dc0c172"


def install_v260_and_upgrade(installer, installation, work, artifacts):
    """Exercise the released baseline, not two freshly compiled fixture setups."""
    baseline = work / "entropy-service-2.6.0-setup.exe"
    url = "https://github.com/fantuz/entropy-service/releases/download/v2.6.0/entropy-service_2.6.0_windows_amd64_gui_setup.exe"
    with urllib.request.urlopen(url, timeout=120) as source, baseline.open("wb") as target:
        size = 0
        while chunk := source.read(1024 * 1024):
            size += len(chunk)
            assert size <= 256 * 1024 * 1024, "Baseline installer exceeded its download bound"
            target.write(chunk)
    with baseline.open("rb") as source:
        assert hashlib.file_digest(source, "sha256").hexdigest() == BASELINE_SHA256, "Unverified baseline installer"
    subprocess.run([str(baseline), "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/SP-",
                    "/DIR=" + str(installation), "/LOG=" + str(artifacts / "baseline-2.6.0.log")], check=True, timeout=180)
    assert registry_values()["DisplayVersion"] == BASELINE_VERSION
    sentinel = installation / "retained export.bin"
    sentinel.write_bytes(b"user export from 2.6.0")
    obsolete = installation / "runtime/obsolete-v260.dll"
    obsolete.write_bytes(b"obsolete private file")
    process = subprocess.Popen([str(installer), "/NORESTART", "/SP-", "/DIR=" + str(installation),
                                "/LOG=" + str(artifacts / "upgrade-from-2.6.0.log")])
    try:
        wizard = Wizard()
        wizard.verify_landing_and_license()
        progress = []
        wizard.advance(lambda: wizard.button("Finish") is not None,
                       observe=lambda: wizard.observe_upgrade(progress), timeout=240)
        assert progress, "Released-baseline upgrade showed no cleanup progress"
        wizard.click(wizard.button("Finish"))
        assert process.wait(timeout=30) == 0
        explorer_folder(programs_folder() / "Entropy Service", expect_open=True)
        assert not obsolete.exists() and sentinel.read_bytes() == b"user export from 2.6.0"
        (artifacts / "baseline-upgrade.json").write_text(json.dumps({"from": BASELINE_VERSION,
            "to": registry_values()["DisplayVersion"], "verified_baseline_sha256": BASELINE_SHA256,
            "progress_visible_and_responsive": True, "user_export_retained": True}, indent=2) + "\n")
    finally:
        if process.poll() is None:
            subprocess.run(["taskkill.exe", "/PID", str(process.pid), "/T", "/F"], check=False, timeout=15,
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            process.wait(timeout=10)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--installer", type=Path, required=True)
    parser.add_argument("--msys-prefix", type=Path, required=True)
    parser.add_argument("--artifacts", type=Path, required=True)
    parser.add_argument("--qualify-v260", action="store_true", help="Upgrade the checksum-pinned released 2.6.0 installer")
    args = parser.parse_args()
    assert sys.platform == "win32"
    try:
        registry_values()
    except FileNotFoundError:
        pass
    else:
        raise AssertionError("Refusing to replace a non-fixture Entropy Service installation")
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
        try:
            if args.qualify_v260:
                install_v260_and_upgrade(args.installer.resolve(), installation, work, artifacts)
            else:
                subprocess.run([str(args.installer.resolve()), "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/SP-",
                                "/DIR=" + str(installation), "/LOG=" + str(artifacts / "install.log")], check=True, timeout=180)
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
                def test_installed_gui(name):
                    code = ("import importlib.util,runpy,sys; "
                            f"spec=importlib.util.spec_from_file_location('bundle_bootstrap',{str(bootstrap)!r}); "
                            "module=importlib.util.module_from_spec(spec); spec.loader.exec_module(module); "
                            f"module.configure_runtime({str(installation)!r},{str(state)!r}); "
                            f"sys.path.insert(0,{str(ROOT / 'scripts')!r}); "
                            f"sys.argv={[str(script), '--gui-source', str(installation / 'app'), '--server', str(installation / 'bin/entropy-server.exe'), '--client', str(installation / 'bin/entropy-client.exe'), '--artifacts', str(artifacts / name)]!r}; "
                            f"runpy.run_path({str(script)!r},run_name='__main__')")
                    subprocess.run([str(python), "-I", "-B", "-c", code], env=environment, check=True, timeout=180)

                test_installed_gui("gtk")
                obsolete = installation / "runtime/obsolete-upgrade-fixture.dll"
                obsolete.write_bytes(b"remove obsolete bundled runtime files")
                retained = installation / "user sample.bin"
                retained.write_bytes(b"retain unowned user files")
                active = subprocess.Popen([str(python), "-I", "-B", "-u", "-c",
                    "import time; print('ready', flush=True); time.sleep(60)"], env=environment,
                    stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
                try:
                    assert active.stdout.readline().strip() == b"ready"
                    blocked = subprocess.run([str(args.installer.resolve()), "/VERYSILENT", "/SUPPRESSMSGBOXES",
                        "/NORESTART", "/SP-", "/DIR=" + str(installation),
                        "/LOG=" + str(artifacts / "active-process.log")], check=False, timeout=60)
                    assert blocked.returncode != 0 and active.poll() is None
                    assert obsolete.exists() and retained.read_bytes() == b"retain unowned user files"
                finally:
                    active.terminate()
                    active.wait(timeout=10)
                    active.stdout.close()
                    active.stderr.close()
                subprocess.run([str(args.installer.resolve()), "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/SP-",
                                "/DIR=" + str(installation), "/LOG=" + str(artifacts / "upgrade.log")], check=True, timeout=180)
                assert not obsolete.exists()
                assert retained.read_bytes() == b"retain unowned user files"
                assert sentinel.read_text() == "retain user data"
                assert json.loads((installation / "bundle-manifest.json").read_text()) == manifest
                test_installed_gui("gtk-upgraded")
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
