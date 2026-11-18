#!/usr/bin/env python3
"""Exercise the actual Inno wizard, upgrade cleanup and Explorer handoff.

Small fixtures compile the production recipe, so these checks run before the
large GTK bundle is built. No additional UI automation package is required.
"""
import argparse
import ctypes
from ctypes import wintypes
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time


ROOT = Path(__file__).resolve().parents[1]
UNINSTALL_KEY = r"Software\Microsoft\Windows\CurrentVersion\Uninstall\{C5571481-03FA-4233-92FC-B0EF9BA76A44}_is1"
MANAGED = ("app", "bin", "runtime", "runtime-package-metadata", "docs")


def wait_for(predicate, description, timeout=20):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        result = predicate()
        if result:
            return result
        time.sleep(.1)
    raise AssertionError("Timed out waiting for " + description)


class Wizard:
    def __init__(self):
        self.user = ctypes.WinDLL("user32", use_last_error=True)
        self.callback = ctypes.WINFUNCTYPE(wintypes.BOOL, wintypes.HWND, wintypes.LPARAM)
        for name in ("EnumWindows", "EnumChildWindows"):
            getattr(self.user, name).restype = wintypes.BOOL
        self.user.EnumWindows.argtypes = [self.callback, wintypes.LPARAM]
        self.user.EnumChildWindows.argtypes = [wintypes.HWND, self.callback, wintypes.LPARAM]
        self.user.GetWindowTextW.argtypes = [wintypes.HWND, wintypes.LPWSTR, ctypes.c_int]
        self.user.GetClassNameW.argtypes = [wintypes.HWND, wintypes.LPWSTR, ctypes.c_int]
        self.user.IsWindowVisible.argtypes = [wintypes.HWND]
        self.user.IsWindowEnabled.argtypes = [wintypes.HWND]
        self.user.GetWindow.argtypes = [wintypes.HWND, wintypes.UINT]
        self.user.GetWindow.restype = wintypes.HWND
        self.user.GetWindowThreadProcessId.argtypes = [wintypes.HWND, ctypes.POINTER(wintypes.DWORD)]
        self.user.PostMessageW.argtypes = [wintypes.HWND, wintypes.UINT, wintypes.WPARAM, wintypes.LPARAM]
        self.user.PostMessageW.restype = wintypes.BOOL
        try:
            self.handle = wait_for(self.find_wizard, "Entropy Service setup wizard")
        except AssertionError:
            print("Visible native windows:", [(self.class_name(handle), self.text(handle))
                  for handle in self.windows() if self.user.IsWindowVisible(handle)], flush=True)
            raise
        self.pid = wintypes.DWORD()
        self.user.GetWindowThreadProcessId(self.handle, ctypes.byref(self.pid))

    def windows(self, parent=None):
        result = []
        visitor = self.callback(lambda handle, _data: result.append(handle) is None)
        if parent is None:
            self.user.EnumWindows(visitor, 0)
        else:
            self.user.EnumChildWindows(parent, visitor, 0)
        return result

    def text(self, handle):
        buffer = ctypes.create_unicode_buffer(8192)
        self.user.GetWindowTextW(handle, buffer, len(buffer))
        return buffer.value

    def class_name(self, handle):
        buffer = ctypes.create_unicode_buffer(256)
        self.user.GetClassNameW(handle, buffer, len(buffer))
        return buffer.value

    def find_wizard(self):
        # Inno's caption includes version/localization details; its native
        # wizard class is stable. Never choose between ambiguous installers.
        matches = [handle for handle in self.windows()
                   if self.class_name(handle) == "TWizardForm" and self.user.IsWindowVisible(handle)]
        assert len(matches) <= 1, "More than one native setup wizard is open"
        return matches[0] if matches else None

    def controls(self):
        return [(handle, self.text(handle)) for handle in self.windows(self.handle)
                if self.user.IsWindowVisible(handle)]

    def button(self, caption):
        return next((handle for handle, text in self.controls()
                     if text.replace("&", "").rstrip(" >") == caption and self.user.IsWindowEnabled(handle)), None)

    def click(self, handle):
        assert handle
        # Queue the click: a synchronous cross-process send deadlocks on the
        # modal cancellation confirmation that this test then needs to answer.
        assert self.user.PostMessageW(handle, 0x00F5, 0, 0)  # BM_CLICK, including radio buttons.

    def advance(self, predicate):
        def step():
            if predicate():
                return True
            accept = self.button("I accept the agreement")
            if accept:
                self.click(accept)
            button = self.button("Next") or self.button("Install")
            if button:
                self.click(button)
            return False
        try:
            wait_for(step, "requested installer page", timeout=30)
        except AssertionError:
            print("Visible wizard controls:", self.controls(), flush=True)
            raise

    def cancel(self, process):
        self.click(self.button("Cancel"))
        def confirm():
            if process.poll() is not None:
                return True
            for top in self.windows():
                pid = wintypes.DWORD()
                self.user.GetWindowThreadProcessId(top, ctypes.byref(pid))
                if pid.value != self.pid.value:
                    continue
                for button in self.windows(top):
                    if self.text(button).replace("&", "") == "Yes" and self.user.IsWindowVisible(button):
                        self.click(button)
            return False
        wait_for(confirm, "cancelled setup exit")


def registry_values():
    import winreg
    with winreg.OpenKey(winreg.HKEY_CURRENT_USER, UNINSTALL_KEY, 0, winreg.KEY_READ | winreg.KEY_WOW64_64KEY) as key:
        return {name: winreg.QueryValueEx(key, name)[0] for name in ("DisplayVersion", "Inno Setup: App Path")}


def programs_folder():
    shell = ctypes.WinDLL("shell32")
    shell.SHGetFolderPathW.argtypes = [wintypes.HWND, ctypes.c_int, wintypes.HANDLE, wintypes.DWORD, wintypes.LPWSTR]
    buffer = ctypes.create_unicode_buffer(32768)
    assert shell.SHGetFolderPathW(None, 2, None, 0, buffer) == 0  # Current user's CSIDL_PROGRAMS.
    return Path(buffer.value)


def explorer_folder(folder, *, expect_open):
    script = r'''
    $ErrorActionPreference = 'Stop'
    $target = [IO.Path]::GetFullPath($env:ENTROPY_INSTALLER_TEST_GROUP).TrimEnd('\')
    $shell = New-Object -ComObject Shell.Application
    $deadline = [DateTime]::UtcNow.AddSeconds(15)
    do {
      foreach ($window in @($shell.Windows())) {
        if (!$window.LocationURL) { continue }
        $uri = [Uri]$window.LocationURL
        if ($uri.IsFile -and $uri.LocalPath.TrimEnd('\') -ieq $target) {
          if ($env:ENTROPY_INSTALLER_EXPECT_OPEN -ne 'true') { throw 'Silent setup opened Explorer' }
          $window.Quit()
          Write-Output 'Verified the installed Start Menu folder in Explorer'
          exit 0
        }
      }
      if ($env:ENTROPY_INSTALLER_EXPECT_OPEN -ne 'true') { exit 0 }
      Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $deadline)
    throw 'Finish did not open the selected Start Menu folder'
    '''
    subprocess.run(["powershell.exe", "-NoProfile", "-STA", "-Command", script], check=True, timeout=25,
                   env=dict(os.environ, ENTROPY_INSTALLER_TEST_GROUP=str(folder),
                            ENTROPY_INSTALLER_EXPECT_OPEN=str(expect_open).lower()))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--compiler", type=Path, required=True)
    parser.add_argument("--artifacts", type=Path, required=True)
    args = parser.parse_args()
    assert sys.platform == "win32"
    artifacts = args.artifacts.resolve()
    artifacts.mkdir(parents=True, exist_ok=True)
    try:
        registry_values()
    except FileNotFoundError:
        pass
    else:
        raise AssertionError("Refusing to alter an existing non-fixture Entropy Service installation")

    with tempfile.TemporaryDirectory(prefix="entropy upgrade wizard ") as temporary:
        work = Path(temporary)
        installation = work / "Application with spaces"
        # The production recipe intentionally disables the group picker and
        # always uses its DefaultGroupName; /GROUP is ignored in that mode.
        group = programs_folder() / "Entropy Service"
        assert not group.exists(), "Refusing to alter an existing non-fixture Start Menu folder"
        setups = {}
        for version in ("1.0.0", "2.0.0"):
            bundle, output = work / ("bundle " + version), work / ("setup " + version)
            bundle.mkdir()
            output.mkdir()
            (bundle / "LICENSE").write_text("Entropy Service installer test fixture. MIT license.")
            (bundle / "bundle-manifest.json").write_text(json.dumps({"version": version}))
            for name in MANAGED:
                folder = bundle / name
                folder.mkdir()
                (folder / ("obsolete.txt" if version == "1.0.0" else "current.txt")).write_text(version)
            (bundle / "runtime/bin").mkdir()
            (bundle / "runtime/bin/pythonw.exe").write_bytes(b"MZ installer fixture, never executed")
            (bundle / "app/gui_bootstrap.py").write_text("# Non-executable installer fixture\n")
            (bundle / "docs/windows-gui-installer.md").write_text("Installer fixture guide\n")
            subprocess.run([str(args.compiler), "/Qp", "/DBundleDir=" + str(bundle), "/DOutputDir=" + str(output),
                            "/DAppVersion=" + version, "/DNumericVersion=" + version + ".0",
                            str(ROOT / "tools/windows-installer/entropy-service.iss")], check=True, timeout=60)
            setups[version] = next(output.glob("*_gui_setup.exe"))

        def launch(version, name, *, silent=False, target=installation):
            command = [str(setups[version]), "/SP-", "/NORESTART", "/DIR=" + str(target),
                       "/LOG=" + str(artifacts / (name + ".log"))]
            if silent:
                command += ["/VERYSILENT", "/SUPPRESSMSGBOXES"]
            return subprocess.Popen(command, stdin=subprocess.DEVNULL)

        active = None
        try:
            active = launch("1.0.0", "fresh", silent=True)
            assert active.wait(timeout=30) == 0
            assert registry_values()["DisplayVersion"] == "1.0.0"
            retained = installation / "my sample.bin"
            retained.write_bytes(b"retain user export")
            outside = work / "private certificate.pem"
            outside.write_text("retain separate user data")
            explorer_folder(group, expect_open=False)

            active = launch("2.0.0", "cancel-upgrade")
            wizard = Wizard()
            wizard.advance(lambda: any(text == "Existing installation found" for _, text in wizard.controls()))
            text = "\n".join(text for _, text in wizard.controls())
            assert "Installed version: 1.0.0" in text and "Setup version: 2.0.0" in text, text
            assert "No separate uninstall is needed" in text and str(installation) in text, text
            wizard.cancel(active)
            assert active.returncode != 0 and registry_values()["DisplayVersion"] == "1.0.0"
            assert all((installation / name / "obsolete.txt").is_file() for name in MANAGED)

            active = launch("2.0.0", "wrong-location", silent=True, target=work / "Other location")
            assert active.wait(timeout=30) != 0
            assert registry_values()["DisplayVersion"] == "1.0.0"
            assert all((installation / name / "obsolete.txt").is_file() for name in MANAGED)

            active = launch("2.0.0", "upgrade")
            wizard = Wizard()
            wizard.advance(lambda: wizard.button("Finish") is not None)
            wizard.click(wizard.button("Finish"))
            assert active.wait(timeout=30) == 0
            explorer_folder(group, expect_open=True)
            values = registry_values()
            assert values["DisplayVersion"] == "2.0.0" and Path(values["Inno Setup: App Path"]) == installation
            assert all(not (installation / name / "obsolete.txt").exists() for name in MANAGED)
            assert all((installation / name / "current.txt").read_text() == "2.0.0" for name in MANAGED)
            assert len(list(group.glob("*.lnk"))) == 3
            assert retained.read_bytes() == b"retain user export" and outside.read_text() == "retain separate user data"

            active = launch("2.0.0", "reinstall", silent=True)
            assert active.wait(timeout=30) == 0
            explorer_folder(group, expect_open=False)
            assert registry_values()["DisplayVersion"] == "2.0.0"
            assert retained.read_bytes() == b"retain user export"
        finally:
            if active is not None and active.poll() is None:
                subprocess.run(["taskkill.exe", "/PID", str(active.pid), "/T", "/F"], check=False, timeout=15,
                               stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                active.wait(timeout=10)
            uninstaller = installation / "unins000.exe"
            if uninstaller.exists():
                subprocess.run([str(uninstaller), "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART"], check=True, timeout=60)
                wait_for(lambda: not (installation / "runtime/bin/pythonw.exe").exists(), "fixture uninstall")
        assert retained.read_bytes() == b"retain user export" and outside.read_text() == "retain separate user data"
        try:
            registry_values()
        except FileNotFoundError:
            pass
        else:
            raise AssertionError("Fixture uninstall left an Apps registration behind")
        assert not list(group.glob("*.lnk"))
    (artifacts / "results.json").write_text(json.dumps({"fresh": True, "cancel_preserves_old_version": True,
        "relocation_rejected": True, "upgrade_removes_old_program_files": True, "single_registration": True,
        "reinstall": True, "finish_opens_start_menu_folder": True, "silent_does_not_open_explorer": True,
        "uninstall_keeps_user_data": True}, indent=2) + "\n")
    print("Native installer upgrade, cancellation, cleanup, reinstall and Start Menu handoff passed")


if __name__ == "__main__":
    main()
