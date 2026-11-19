#!/usr/bin/env python3
"""Install, relocate-test and uninstall the GUI without the build-time runtime."""
import argparse
import hashlib
import http.client
import json
import os
from pathlib import Path
import re
import signal
import socket
import subprocess
import sys
import tempfile
import time
from urllib.parse import quote
import urllib.request

from test_windows_gui_upgrade import Wizard, explorer_folder, programs_folder, registry_values


ROOT = Path(__file__).resolve().parents[1]
BASELINE_VERSION = "2.6.0"
BASELINE_SHA256 = "b63746ecc3af4df9ab212d444184ddc4535df541793bd3cea0b9f8ec7dc0c172"
UI_LANGUAGES = tuple(sorted(path.stem for path in (ROOT / "cmd/entropy-server/web/locales").glob("*.json")))
SUBSET_COMPONENTS = r"/COMPONENTS=core,web_locales,web_locales\fr,web_locales\de"


def locale_inventory(installation, expected):
    directory = installation / "bin/ui-locales"
    actual = {path.name for path in directory.iterdir()}
    assert actual == {language + ".json" for language in expected}, ("Installed UI locale files differ", sorted(actual), sorted(expected))


def _local_get(port, path):
    connection = http.client.HTTPConnection("127.0.0.1", port, timeout=5)
    try:
        connection.request("GET", path, headers={"Accept-Encoding": "identity"})
        response = connection.getresponse()
        body = response.read(4 * 1024 * 1024 + 1)
        assert len(body) <= 4 * 1024 * 1024, "Installed server response exceeded the probe bound"
        return response.status, dict(response.getheaders()), body
    finally:
        connection.close()


def _word_frame(port, language):
    """Read one bounded real /words frame without an extra Python dependency."""
    connection = http.client.HTTPConnection("127.0.0.1", port, timeout=5)
    response = None
    try:
        # RFC 6455's public sample nonce is sufficient for this loopback fixture.
        connection.request("GET", "/words?words=2&refresh=1&lang=" + quote(language, safe=""), headers={
            "Connection": "Upgrade", "Upgrade": "websocket", "Sec-WebSocket-Version": "13",
            "Sec-WebSocket-Key": "dGhlIHNhbXBsZSBub25jZQ==", "Origin": f"http://127.0.0.1:{port}"})
        response = connection.getresponse()
        assert response.status == 101 and response.getheader("Sec-WebSocket-Accept") == "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=", language

        def read_exact(count):
            result = response.fp.read(count)
            assert len(result) == count, "Installed word stream ended before its first complete frame"
            return result

        header = read_exact(2)
        assert header[0] == 0x81 and header[1] & 0x80 == 0, "Expected one unmasked text frame"
        length = header[1] & 0x7f
        if length == 126:
            length = int.from_bytes(read_exact(2), "big")
        elif length == 127:
            length = int.from_bytes(read_exact(8), "big")
        assert 0 < length <= 65536, "Installed word frame exceeded the probe bound"
        return json.loads(read_exact(length))
    finally:
        if response is not None:
            response.close()
        connection.close()


def verify_installed_web_languages(installation, expected, artifacts, name):
    """Probe the installed EXE from an unrelated CWD; no browser or system service is started."""
    assert "en" in expected and set(expected) <= set(UI_LANGUAGES)
    assert len(UI_LANGUAGES) == 14, "The full installer must offer the fourteen interface languages"
    locale_inventory(installation, expected)
    binary = installation / ("bin/entropy-server.exe" if sys.platform == "win32" else "bin/entropy-server")
    source = "windows-csprng" if sys.platform == "win32" else "macos-csprng" if sys.platform == "darwin" else "/dev/urandom"
    with tempfile.TemporaryDirectory(prefix="entropy unrelated launch directory ") as temporary:
        working = Path(temporary)
        decoy = working / "ui-locales"
        decoy.mkdir()
        (decoy / "fr.json").write_text('{"Interface language":"CWD decoy must never load"}')
        description = subprocess.run([str(binary), "-describe-json", "-enable-https=false", "-device=" + source],
                                     cwd=working, capture_output=True, check=True, timeout=20)
        configuration = json.loads(description.stdout)
        assert configuration["role"] == "server" and configuration["live"] is False
        assert configuration["features"]["external_ui_locales"] is True
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            port = listener.getsockname()[1]
        with (artifacts / (name + "-server.log")).open("wb") as log:
            child = subprocess.Popen([str(binary), "-enable-https=false", "-device=" + source, "-drbg=chacha20",
                                      f"-http=127.0.0.1:{port}"], cwd=working, stdout=log, stderr=subprocess.STDOUT,
                                     creationflags=subprocess.CREATE_NEW_PROCESS_GROUP if sys.platform == "win32" else 0)
            try:
                deadline = time.monotonic() + 15
                while True:
                    try:
                        if _local_get(port, "/live")[0] == 200:
                            break
                    except (OSError, http.client.HTTPException):
                        pass
                    assert child.poll() is None and time.monotonic() < deadline, "Installed locale-probe server failed to start"
                    time.sleep(.05)
                status, headers, script = _local_get(port, "/i18n.js")
                assert status == 200
                declared = re.search(rb'const installedLanguages\s*=\s*"([^"]+)"\.split\(","\)', script)
                assert declared is not None and set(declared[1].decode().split(",")) == set(expected)
                assert b"__ENTROPY_UI_LANGUAGES__" not in script and b"__ENTROPY_UI_CATALOG_VERSION__" not in script
                english_keys = json.loads((installation / "bin/ui-locales/en.json").read_bytes()).keys()
                for language in UI_LANGUAGES:
                    status, _, body = _local_get(port, "/locales/" + language + ".json")
                    if language in expected:
                        assert status == 200, (name, language, status)
                        assert body == (installation / ("bin/ui-locales/" + language + ".json")).read_bytes()
                        assert json.loads(body).keys() == english_keys, ("Installed translation inventory changed", language)
                    else:
                        assert status == 404, ("Unselected language remained available", name, language, status)
                status, _, body = _local_get(port, "/wordlists.json")
                assert status == 200
                words = json.loads(body)["languages"]
                original = json.loads((ROOT / "cmd/entropy-server/internal/wordlist/manifest.json").read_text())
                assert len(words) == 18 and {item["code"] for item in words} == {item["code"] for item in original}
                for entry in words:
                    frame = _word_frame(port, entry["code"])
                    assert frame["language"] == entry["code"] and frame["wordlist"] == entry["id"]
                    samples = frame["words"].split()
                    dictionary = set((ROOT / ("cmd/entropy-server/internal/wordlist/data/" + entry["code"] + ".txt")).read_text(encoding="utf-8").splitlines())
                    assert len(samples) == 2 and set(samples) <= dictionary
                    assert frame["hash"] == hashlib.sha256(frame["words"].encode()).hexdigest()
                result = {"selected_ui_languages": sorted(expected), "available_ui_languages": len(expected),
                          "unselected_files_and_http_routes_absent": True, "executable_relative_locale_directory": True,
                          "offline_description_external_build": True, "word_languages_unchanged": sorted(item["code"] for item in words),
                          "all_18_word_websockets_work": True, "i18n_etag": headers.get("Etag", headers.get("ETag", ""))}
                (artifacts / (name + ".json")).write_text(json.dumps(result, indent=2) + "\n")
                return result
            finally:
                if child.poll() is None:
                    try:
                        child.send_signal(signal.CTRL_BREAK_EVENT) if sys.platform == "win32" else child.terminate()
                    except OSError:
                        if child.poll() is None:
                            child.terminate()
                    try:
                        child.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        child.kill()
                        child.wait(timeout=5)


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
    subprocess.run([str(baseline), "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/SP-", "/CURRENTUSER",
                    "/DIR=" + str(installation), "/LOG=" + str(artifacts / "baseline-2.6.0.log")], check=True, timeout=180)
    assert registry_values()["DisplayVersion"] == BASELINE_VERSION
    sentinel = installation / "retained export.bin"
    sentinel.write_bytes(b"user export from 2.6.0")
    obsolete = installation / "runtime/obsolete-v260.dll"
    obsolete.write_bytes(b"obsolete private file")
    process = subprocess.Popen([str(installer), "/NORESTART", "/SP-", "/CURRENTUSER", "/DIR=" + str(installation),
                                "/LOG=" + str(artifacts / "upgrade-from-2.6.0.log")])
    try:
        wizard = Wizard()
        wizard.verify_landing_and_license(artifacts)
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
    parser.add_argument("--qualify-service", action="store_true", help="Qualify explicitly selected machine-wide SCM installation")
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
                subprocess.run([str(args.installer.resolve()), "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/SP-", "/CURRENTUSER",
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
                all_languages = verify_installed_web_languages(installation, UI_LANGUAGES, artifacts, "ui-locales-default-all")
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
                        "/NORESTART", "/SP-", "/CURRENTUSER", "/DIR=" + str(installation),
                        "/LOG=" + str(artifacts / "active-process.log")], check=False, timeout=60)
                    assert blocked.returncode != 0 and active.poll() is None
                    assert obsolete.exists() and retained.read_bytes() == b"retain unowned user files"
                    locale_inventory(installation, UI_LANGUAGES)
                finally:
                    active.terminate()
                    active.wait(timeout=10)
                    active.stdout.close()
                    active.stderr.close()
                subprocess.run([str(args.installer.resolve()), "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/SP-", "/CURRENTUSER",
                                "/TYPE=custom", SUBSET_COMPONENTS,
                                "/DIR=" + str(installation), "/LOG=" + str(artifacts / "upgrade.log")], check=True, timeout=180)
                assert not obsolete.exists()
                assert retained.read_bytes() == b"retain unowned user files"
                assert sentinel.read_text() == "retain user data"
                assert json.loads((installation / "bundle-manifest.json").read_text()) == manifest
                subset = verify_installed_web_languages(installation, ("en", "fr", "de"), artifacts, "ui-locales-french-german")
                assert all_languages["i18n_etag"] and subset["i18n_etag"] and all_languages["i18n_etag"] != subset["i18n_etag"], "Locale removal did not invalidate the served script"
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
    if args.qualify_service:
        from test_windows_gui_installer_service import qualify_machine_service
        qualify_machine_service(args.installer.resolve(), artifacts / "machine-service")
    print("Installed GTK server/client, owned-process lifecycle and uninstall passed without MSYS2 runtime access")


if __name__ == "__main__":
    main()
