#!/usr/bin/env python3
"""Qualify optional machine-wide service setup using the real GUI installer."""
import ctypes
import http.client
import json
import os
from pathlib import Path
import shutil
import ssl
import subprocess
import uuid

from test_windows_gui_upgrade import UNINSTALL_KEY, wait_for


SERVICE_KEY = r"SYSTEM\CurrentControlSet\Services\EntropyServer"


def registry_present(root, key):
    import winreg
    try:
        with winreg.OpenKey(root, key, 0, winreg.KEY_READ | winreg.KEY_WOW64_64KEY):
            return True
    except FileNotFoundError:
        return False


def qualify_machine_service(installer, artifacts):
    import winreg
    assert ctypes.windll.shell32.IsUserAnAdmin(), "Service installer qualification needs an administrative runner"
    state = Path(os.environ["ProgramData"]) / "entropy-service"
    assert not state.exists(), "Refusing to alter a pre-existing service state directory"
    assert not registry_present(winreg.HKEY_LOCAL_MACHINE, SERVICE_KEY), "Refusing to alter an existing service"
    assert not registry_present(winreg.HKEY_LOCAL_MACHINE, UNINSTALL_KEY), "Refusing to alter an existing machine installation"
    assert not registry_present(winreg.HKEY_CURRENT_USER, UNINSTALL_KEY), "Finish the per-user fixture first"
    installation = Path(os.environ["ProgramFiles"]) / ("Entropy Service Installer CI " + uuid.uuid4().hex)
    assert not installation.exists()
    binary = installation / "bin/entropy-server.exe"
    artifacts.mkdir(parents=True, exist_ok=True)

    def setup(name, tasks=None, target=installation, mode="/ALLUSERS", expected=0, install_type=None, components=None):
        command = [str(installer), mode, "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/SP-",
                   "/DIR=" + str(target), "/LOG=" + str(artifacts / (name + ".log"))]
        if tasks is not None:
            command.append("/TASKS=" + tasks)
        if install_type is not None:
            command.append("/TYPE=" + install_type)
        if components is not None:
            command.append("/COMPONENTS=" + components)
        result = subprocess.run(command, timeout=240)
        assert (result.returncode == 0) == (expected == 0), (name, result.returncode)

    def service(operation):
        result = subprocess.run([str(binary), "service", operation], capture_output=True, text=True, timeout=45)
        assert result.returncode == 0, (operation, result.stdout, result.stderr)
        return result.stdout

    def live():
        connection = http.client.HTTPConnection("127.0.0.1", 8080, timeout=.5)
        try:
            connection.request("GET", "/live")
            response = connection.getresponse()
            response.read(8192)
            return response.status == 200
        except OSError:
            return False
        finally:
            connection.close()

    try:
        # A service must never be registered from an administrative setup that
        # targets user-writable storage, even when that setup can create SCM entries.
        setup("unsafe-service-location", "server_service", artifacts / "unsafe program", expected=1)
        assert not registry_present(winreg.HKEY_LOCAL_MACHINE, SERVICE_KEY)
        setup("nested-service-location", "server_service", installation / "nested", expected=1)
        assert not registry_present(winreg.HKEY_LOCAL_MACHINE, SERVICE_KEY)
        assert not installation.exists(), "Rejected service setup created an unverified program tree"
        setup("machine-default-off", install_type="english", components="core")
        assert registry_present(winreg.HKEY_LOCAL_MACHINE, UNINSTALL_KEY)
        assert not registry_present(winreg.HKEY_LOCAL_MACHINE, SERVICE_KEY), "Service installed without explicit selection"
        from test_windows_gui_installer import verify_installed_web_languages
        verify_installed_web_languages(installation, ("en",), artifacts, "ui-locales-english-only")
        setup("desktop-service-promotion-rejected", "server_service", expected=1)
        assert not registry_present(winreg.HKEY_LOCAL_MACHINE, SERVICE_KEY), "Desktop directory adopted for a privileged service"
        setup("scope-switch-rejected", target=artifacts / "per-user replacement", mode="/CURRENTUSER", expected=1)
        assert not registry_present(winreg.HKEY_CURRENT_USER, UNINSTALL_KEY)
        subprocess.run([str(installation / "unins000.exe"), "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART"],
                       check=True, timeout=120)
        wait_for(lambda: not registry_present(winreg.HKEY_LOCAL_MACHINE, UNINSTALL_KEY), "desktop-only machine uninstall")
        # Inno's detached uninstaller helper removes the original executable
        # and its parent after the process/registry teardown has completed.
        # Do not race that cleanup or remove the fresh-directory safety check.
        wait_for(lambda: not installation.exists(), "desktop-only uninstaller self-cleanup", timeout=30)
        setup("service-selected", "server_service")
        assert "State: stopped" in service("status"), "Installation started an unchecked Start task"
        retained = installation / "operator export.bin"
        retained.write_bytes(b"retain unowned machine-install export")
        with winreg.OpenKey(winreg.HKEY_LOCAL_MACHINE, SERVICE_KEY) as key:
            assert winreg.QueryValueEx(key, "ObjectName")[0].lower() == r"NT SERVICE\EntropyServer".lower()
            assert winreg.QueryValueEx(key, "Start")[0] == 2
            assert winreg.QueryValueEx(key, "DelayedAutoStart")[0] == 1
            assert str(binary).lower() in winreg.QueryValueEx(key, "ImagePath")[0].lower()
        setup("start-task-selected", "server_service,server_service\\start")
        wait_for(live, "installed optional service HTTP listener")
        certificate = (state / "cert.pem").read_bytes()
        assert certificate.startswith(b"-----BEGIN CERTIFICATE-----")
        secure = http.client.HTTPSConnection("127.0.0.1", 8443,
            context=ssl.create_default_context(cafile=str(state / "cert.pem")), timeout=5)
        try:
            secure.request("GET", "/live")
            response = secure.getresponse()
            assert response.status == 200
            response.read(8192)
        finally:
            secure.close()
        service_state = state / "installer-fixture.txt"
        service_state.write_text("retain service state across upgrades and uninstall")
        setup("managed-service-upgrade", "server_service")
        assert "State: running" in service("status")
        wait_for(live, "running service resumed after upgrade")
        assert (state / "cert.pem").read_bytes() == certificate
        assert retained.read_bytes() == b"retain unowned machine-install export"
        uninstaller = installation / "unins000.exe"
        subprocess.run([str(uninstaller), "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART",
                        "/LOG=" + str(artifacts / "machine-uninstall.log")], check=True, timeout=120)
        wait_for(lambda: not registry_present(winreg.HKEY_LOCAL_MACHINE, SERVICE_KEY), "installer-owned service removal")
        assert not registry_present(winreg.HKEY_LOCAL_MACHINE, UNINSTALL_KEY)
        assert (state / "cert.pem").read_bytes() == certificate and service_state.is_file()
        assert retained.is_file()
        (artifacts / "results.json").write_text(json.dumps({
            "machine_service_default_off": True, "unsafe_location_rejected": True,
            "nested_service_location_rejected": True,
            "desktop_service_promotion_rejected": True,
            "cross_scope_replacement_rejected": True, "dedicated_virtual_account": True,
            "delayed_automatic_start": True, "start_task_unchecked": True,
            "running_upgrade_resumed_same_identity": True, "owned_uninstall_preserves_state": True}, indent=2) + "\n")
    finally:
        if binary.exists() and registry_present(winreg.HKEY_LOCAL_MACHINE, SERVICE_KEY):
            for operation in ("stop", "uninstall"):
                subprocess.run([str(binary), "service", operation], capture_output=True, timeout=45)
        uninstaller = installation / "unins000.exe"
        if uninstaller.exists():
            subprocess.run([str(uninstaller), "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART"],
                           capture_output=True, timeout=120)
        # This state path was absent at entry and belongs only to this fixture.
        # Production uninstall intentionally retains it; fixture cleanup is explicit.
        if state.exists() and not registry_present(winreg.HKEY_LOCAL_MACHINE, SERVICE_KEY):
            subprocess.run(["takeown.exe", "/F", str(state), "/R", "/A", "/D", "Y"], capture_output=True, timeout=30)
            subprocess.run(["icacls.exe", str(state), "/grant", "*S-1-5-32-544:F", "/T", "/C"], capture_output=True, timeout=30)
            shutil.rmtree(state)
        if installation.exists() and not registry_present(winreg.HKEY_LOCAL_MACHINE, SERVICE_KEY):
            shutil.rmtree(installation)
