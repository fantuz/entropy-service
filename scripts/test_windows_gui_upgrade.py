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
import shutil
import subprocess
import sys
import tempfile
import time


ROOT = Path(__file__).resolve().parents[1]
UNINSTALL_KEY = r"Software\Microsoft\Windows\CurrentVersion\Uninstall\{C5571481-03FA-4233-92FC-B0EF9BA76A44}_is1"
MANAGED = ("app", "bin", "runtime", "runtime-package-metadata", "docs")
PROJECT_CREDIT = "Software Architecture, Build Pipelines and Security Audit by Massimiliano Fantuzzi"
UI_LANGUAGES = ("en", "it", "fr", "nl", "de", "es", "el", "ru", "pt-PT", "pt-BR", "cs", "pl", "ja", "ar")
COMPONENT_HELP = "Choose additional web interface languages. English and all random-word dictionaries are always installed."


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
        self.user.SendMessageTimeoutW.argtypes = [wintypes.HWND, wintypes.UINT, wintypes.WPARAM,
            wintypes.LPARAM, wintypes.UINT, wintypes.UINT, ctypes.POINTER(ctypes.c_size_t)]
        self.user.SendMessageTimeoutW.restype = ctypes.c_ssize_t
        self.user.GetClientRect.argtypes = [wintypes.HWND, ctypes.POINTER(wintypes.RECT)]
        self.user.GetWindowRect.argtypes = [wintypes.HWND, ctypes.POINTER(wintypes.RECT)]
        self.user.GetParent.argtypes = [wintypes.HWND]
        self.user.GetParent.restype = wintypes.HWND
        self.user.GetDpiForWindow.argtypes = [wintypes.HWND]
        self.user.GetDpiForWindow.restype = wintypes.UINT
        self.user.GetDC.argtypes = [wintypes.HWND]
        self.user.GetDC.restype = wintypes.HDC
        self.user.ReleaseDC.argtypes = [wintypes.HWND, wintypes.HDC]
        self.user.DrawTextW.argtypes = [wintypes.HDC, wintypes.LPCWSTR, ctypes.c_int,
                                      ctypes.POINTER(wintypes.RECT), wintypes.UINT]
        self.gdi = ctypes.WinDLL("gdi32", use_last_error=True)
        self.gdi.SelectObject.argtypes = [wintypes.HDC, wintypes.HANDLE]
        self.gdi.SelectObject.restype = wintypes.HANDLE
        self.layout = []
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
        # GetWindowText cannot retrieve another process's edit-control text.
        # WM_GETTEXT is marshalled by Windows; bound it so a busy installer
        # produces diagnostics rather than hanging the UI test.
        if any(part in self.class_name(handle).lower() for part in ("rich", "memo", "edit")):
            result = ctypes.c_size_t()
            assert self.user.SendMessageTimeoutW(handle, 0x000D, len(buffer), ctypes.addressof(buffer),
                2, 1000, ctypes.byref(result)), "Could not read the native license viewer"
        else:
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

    def advance(self, predicate, *, observe=None, timeout=30):
        def step():
            if observe:
                observe()
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
            wait_for(step, "requested installer page", timeout=timeout)
        except AssertionError:
            print("Visible wizard controls:", self.controls(), flush=True)
            raise

    def observe_upgrade(self, observations):
        controls = self.controls()
        if not any(text.startswith("Removing previous application and runtime files") for _, text in controls):
            return
        bars = [handle for handle, _ in controls if "progress" in self.class_name(handle).lower()]
        assert bars, "Upgrade cleanup has no visible progress bar"
        result = ctypes.c_size_t()
        ctypes.set_last_error(0)
        # Allow a slow filesystem/antivirus operation while retaining Windows'
        # five-second hung-window bound; every completed entry pumps messages.
        assert self.user.SendMessageTimeoutW(self.handle, 0, 0, 0, 2, 5000, ctypes.byref(result)), \
            "Setup did not respond within five seconds during cleanup; Windows error " + str(ctypes.get_last_error())
        observations.append("responsive cleanup with a visible progress bar")

    def capture(self, destination):
        """Preserve real wizard pixels with Windows' built-in drawing library."""
        destination.parent.mkdir(parents=True, exist_ok=True)
        rect = wintypes.RECT()
        self.user.GetWindowRect.argtypes = [wintypes.HWND, ctypes.POINTER(wintypes.RECT)]
        assert self.user.GetWindowRect(self.handle, ctypes.byref(rect))
        script = r'''
        $ErrorActionPreference = 'Stop'
        Add-Type -AssemblyName System.Drawing
        $bounds = $env:ENTROPY_WIZARD_RECT.Split(',') | ForEach-Object { [int]$_ }
        $bitmap = [Drawing.Bitmap]::new($bounds[2], $bounds[3])
        $graphics = [Drawing.Graphics]::FromImage($bitmap)
        try {
          $graphics.CopyFromScreen($bounds[0], $bounds[1], 0, 0, $bitmap.Size)
          $bitmap.Save($env:ENTROPY_WIZARD_IMAGE, [Drawing.Imaging.ImageFormat]::Png)
        } finally { $graphics.Dispose(); $bitmap.Dispose() }
        '''
        subprocess.run(["powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script],
            env=dict(os.environ, ENTROPY_WIZARD_IMAGE=str(destination),
                     ENTROPY_WIZARD_RECT=','.join(map(str, (rect.left, rect.top, rect.right-rect.left, rect.bottom-rect.top)))),
            check=True, timeout=15, capture_output=True)

    def verify_wrapped_text_fits(self, handle, description):
        """Measure what Windows can draw, not merely the stored caption string."""
        client, bounds, parent = wintypes.RECT(), wintypes.RECT(), wintypes.RECT()
        assert self.user.GetClientRect(handle, ctypes.byref(client))
        assert self.user.GetWindowRect(handle, ctypes.byref(bounds))
        assert self.user.GetWindowRect(self.user.GetParent(handle), ctypes.byref(parent))
        width, height = client.right - client.left, client.bottom - client.top
        assert width > 0 and height > 0, (description, width, height)
        assert parent.left <= bounds.left < bounds.right <= parent.right, description
        assert parent.top <= bounds.top < bounds.bottom <= parent.bottom, description
        font = ctypes.c_size_t()
        assert self.user.SendMessageTimeoutW(handle, 0x0031, 0, 0, 2, 1000, ctypes.byref(font))  # WM_GETFONT.
        assert font.value, "Cannot measure the actual native font for " + description
        context = self.user.GetDC(handle)
        assert context
        previous = self.gdi.SelectObject(context, font.value)
        try:
            measured = wintypes.RECT(0, 0, width, 0)
            # Use the control's actual font/width at its current DPI. These
            # flags match multiline static text without accelerator stripping.
            assert self.user.DrawTextW(context, self.text(handle), -1, ctypes.byref(measured),
                                      0x0400 | 0x0010 | 0x0800)  # CALCRECT | WORDBREAK | NOPREFIX.
        finally:
            self.gdi.SelectObject(context, previous)
            self.user.ReleaseDC(handle, context)
        required = measured.bottom - measured.top
        result = {"description": description, "width": width, "height": height,
                  "required_height": required, "dpi": self.user.GetDpiForWindow(handle), "text": self.text(handle)}
        assert required <= height + 1, "Clipped native caption: " + repr(result)
        self.layout.append(result)
        return result

    def verify_components_page(self, artifacts=None):
        self.advance(lambda: any(text == COMPONENT_HELP for _, text in self.controls()))
        helpers = [handle for handle, text in self.controls() if text == COMPONENT_HELP]
        assert len(helpers) == 1
        self.verify_wrapped_text_fits(helpers[0], "component selection explanation")
        lists = [handle for handle, _ in self.controls() if self.class_name(handle) == "TNewCheckListBox"]
        assert len(lists) == 1, "No visible standard component checklist"
        count = ctypes.c_size_t()
        assert self.user.SendMessageTimeoutW(lists[0], 0x018B, 0, 0, 2, 1000, ctypes.byref(count))  # LB_GETCOUNT.
        assert count.value == len(UI_LANGUAGES) + 1, "Expected core, language group and every optional translation"
        if artifacts:
            self.capture(artifacts / "installer-components.png")
            (artifacts / "installer-text-layout.json").write_text(json.dumps(self.layout, indent=2) + "\n")

    def verify_landing_and_license(self, artifacts=None):
        welcome = [(handle, text) for handle, text in self.controls() if PROJECT_CREDIT in text]
        assert len(welcome) == 1, "Welcome page omits the project credit"
        handle, text = welcome[0]
        assert "Current-user setup needs no administrator rights" in text
        assert "optional Windows Service" in text
        assert "MIT License" in text
        self.verify_wrapped_text_fits(handle, "complete welcome explanation")
        if artifacts:
            self.capture(artifacts / "installer-welcome.png")
        self.advance(lambda: self.button("I accept the agreement") is not None)
        licenses = [(handle, text) for handle, text in self.controls()
                    if 'THE SOFTWARE IS PROVIDED "AS IS"' in text.replace('\xa0', ' ')]
        assert len(licenses) == 1, "MIT license is not displayed: " + repr([
            (self.class_name(handle), text[:2000]) for handle, text in self.controls()])
        handle, text = licenses[0]
        text = text.replace('\xa0', ' ')
        assert PROJECT_CREDIT in text
        expected = (ROOT / "LICENSE").read_text()
        assert " ".join(expected.split()) in " ".join(text.split()), "License wording changed"
        assert "DEALINGS IN THE\r\nSOFTWARE" not in text and "THE\r\nSOFTWARE" not in text, \
            "The MIT license retains orphaned hard line breaks"
        if artifacts:
            self.capture(artifacts / "installer-license.png")
            result = ctypes.c_size_t()
            assert self.user.SendMessageTimeoutW(handle, 0x0115, 7, 0, 2, 1000, ctypes.byref(result))  # WM_VSCROLL / SB_BOTTOM.
            self.capture(artifacts / "installer-license-bottom.png")

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
            shutil.copyfile(ROOT / "LICENSE", bundle / "LICENSE")
            (bundle / "bundle-manifest.json").write_text(json.dumps({"version": version}))
            for name in MANAGED:
                folder = bundle / name
                folder.mkdir()
                (folder / ("obsolete.txt" if version == "1.0.0" else "current.txt")).write_text(version)
            (bundle / "runtime/bin").mkdir()
            (bundle / "runtime/bin/pythonw.exe").write_bytes(b"MZ installer fixture, never executed")
            (bundle / "app/gui_bootstrap.py").write_text("# Non-executable installer fixture\n")
            (bundle / "docs/windows-gui-installer.md").write_text("Installer fixture guide\n")
            (bundle / "bin/ui-locales").mkdir()
            for language in UI_LANGUAGES:
                (bundle / f"bin/ui-locales/{language}.json").write_text(json.dumps({"fixture_language": language}))
            (bundle / "bin/mandatory-core-fixture.txt").write_text("core is independent from web translations")
            subprocess.run([str(args.compiler), "/Qp", "/DBundleDir=" + str(bundle), "/DOutputDir=" + str(output),
                            "/DAppVersion=" + version, "/DNumericVersion=" + version + ".0",
                            str(ROOT / "tools/windows-installer/entropy-service.iss")], check=True, timeout=60)
            setups[version] = next(output.glob("*_gui_setup.exe"))

        def launch(version, name, *, silent=False, target=installation, languages=None):
            command = [str(setups[version]), "/SP-", "/CURRENTUSER", "/NORESTART", "/DIR=" + str(target),
                       "/LOG=" + str(artifacts / (name + ".log"))]
            if languages is not None:
                components = ["core"]
                if languages:
                    components += ["web_locales", *["web_locales\\" + code.replace('-', '_').lower() for code in languages]]
                command += ["/TYPE=" + ("custom" if languages else "english"), "/COMPONENTS=" + ','.join(components)]
            if silent:
                command += ["/VERYSILENT", "/SUPPRESSMSGBOXES"]
            return subprocess.Popen(command, stdin=subprocess.DEVNULL)

        def verify_languages(expected):
            catalog = installation / "bin/ui-locales"
            assert {path.stem for path in catalog.glob("*.json")} == set(expected)
            assert (installation / "bin/mandatory-core-fixture.txt").read_text() == "core is independent from web translations"

        active = None
        helpers = []
        try:
            active = launch("1.0.0", "fresh", silent=True)
            assert active.wait(timeout=30) == 0
            assert registry_values()["DisplayVersion"] == "1.0.0"
            verify_languages(UI_LANGUAGES)
            retained = installation / "my sample.bin"
            retained.write_bytes(b"retain user export")
            outside = work / "private certificate.pem"
            outside.write_text("retain separate user data")
            explorer_folder(group, expect_open=False)

            active = launch("2.0.0", "cancel-upgrade")
            wizard = Wizard()
            wizard.verify_landing_and_license(artifacts)
            wizard.advance(lambda: any(text == "Existing installation found" for _, text in wizard.controls()))
            text = "\n".join(text for _, text in wizard.controls())
            assert "Installed version: 1.0.0" in text and "Setup version: 2.0.0" in text, text
            assert "No separate uninstall is needed" in text and str(installation) in text, text
            wizard.cancel(active)
            assert active.returncode != 0 and registry_values()["DisplayVersion"] == "1.0.0"
            assert all((installation / name / "obsolete.txt").is_file() for name in MANAGED)
            verify_languages(UI_LANGUAGES)

            active = launch("2.0.0", "wrong-location", silent=True, target=work / "Other location")
            assert active.wait(timeout=30) != 0
            assert registry_values()["DisplayVersion"] == "1.0.0"
            assert all((installation / name / "obsolete.txt").is_file() for name in MANAGED)

            # Use harmless Windows ping processes with the helper basename.
            # Only the copy inside this private runtime may be terminated.
            private_helper = installation / "runtime/bin/gdbus.exe"
            other_helper = work / "gdbus.exe"
            for path in (private_helper, other_helper):
                shutil.copyfile(Path(os.environ["SystemRoot"]) / "System32/ping.exe", path)
                helpers.append(subprocess.Popen([str(path), "-t", "127.0.0.1"],
                    stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                    creationflags=subprocess.CREATE_NO_WINDOW))
            time.sleep(1)
            assert all(child.poll() is None for child in helpers), "Helper fixtures did not stay alive"

            # A realistic many-small-file workload makes the cleanup phase
            # observable without adding sleeps or test hooks to setup itself.
            old_library = installation / "runtime/old-library"
            old_library.mkdir()
            for index in range(4000):
                (old_library / f"old-{index}.py").write_bytes(b"obsolete runtime fixture")
            readonly = old_library / "readonly.py"
            readonly.write_text("obsolete readonly fixture")
            readonly.chmod(0o444)
            linked_data = work / "linked user data"
            linked_data.mkdir()
            (linked_data / "keep.pem").write_text("retain user data behind a junction")
            subprocess.run(["cmd.exe", "/d", "/c", "mklink", "/J", str(old_library / "external"),
                            str(linked_data)], check=True, capture_output=True, timeout=10)

            active = launch("2.0.0", "upgrade")
            wizard = Wizard()
            wizard.verify_components_page(artifacts)
            progress = []
            wizard.advance(lambda: wizard.button("Finish") is not None,
                           observe=lambda: wizard.observe_upgrade(progress), timeout=90)
            assert progress, "Never observed upgrade-cleanup progress"
            wizard.click(wizard.button("Finish"))
            assert active.wait(timeout=30) == 0
            helpers[0].wait(timeout=10)
            assert helpers[1].poll() is None, "Setup stopped an unrelated helper with the same name"
            assert "Closed private GTK runtime helper:" in (artifacts / "upgrade.log").read_text(encoding="utf-8-sig")
            assert "Upgrade cleanup completed:" in (artifacts / "upgrade.log").read_text(encoding="utf-8-sig")
            assert (linked_data / "keep.pem").read_text() == "retain user data behind a junction"
            explorer_folder(group, expect_open=True)
            values = registry_values()
            assert values["DisplayVersion"] == "2.0.0" and Path(values["Inno Setup: App Path"]) == installation
            assert all(not (installation / name / "obsolete.txt").exists() for name in MANAGED)
            assert all((installation / name / "current.txt").read_text() == "2.0.0" for name in MANAGED)
            assert len(list(group.glob("*.lnk"))) == 3
            assert retained.read_bytes() == b"retain user export" and outside.read_text() == "retain separate user data"
            verify_languages(UI_LANGUAGES)

            active = launch("2.0.0", "reinstall", silent=True)
            assert active.wait(timeout=30) == 0
            explorer_folder(group, expect_open=False)
            assert registry_values()["DisplayVersion"] == "2.0.0"
            assert retained.read_bytes() == b"retain user export"
            verify_languages(UI_LANGUAGES)

            # Changing optional components replaces only installer-owned
            # payloads; no stale web catalog may survive an upgrade/reinstall.
            active = launch("2.0.0", "subset-languages", silent=True, languages=("fr", "de"))
            assert active.wait(timeout=30) == 0
            verify_languages(("en", "fr", "de"))
            active = launch("2.0.0", "retained-subset-languages", silent=True)
            assert active.wait(timeout=30) == 0
            verify_languages(("en", "fr", "de"))
            active = launch("2.0.0", "english-only", silent=True, languages=())
            assert active.wait(timeout=30) == 0
            verify_languages(("en",))
            active = launch("2.0.0", "retained-english-only", silent=True)
            assert active.wait(timeout=30) == 0
            verify_languages(("en",))
            assert retained.read_bytes() == b"retain user export" and outside.read_text() == "retain separate user data"
        finally:
            for child in helpers:
                if child.poll() is None:
                    child.terminate()
                child.wait(timeout=10)
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
        "private_helper_stopped_unrelated_helper_retained": True,
        "cleanup_progress_visible_and_responsive": True, "junction_target_retained": True,
        "readonly_old_file_removed": True, "landing_credit_and_license_reflow": True,
        "welcome_text_fits_native_font_and_bounds": True, "components_page_explains_word_dictionary_independence": True,
        "default_all_ui_languages": True, "partial_ui_language_reinstall_removes_unselected": True,
        "english_only_retains_core": True, "component_selections_remembered": True,
        "reinstall": True, "finish_opens_start_menu_folder": True, "silent_does_not_open_explorer": True,
        "uninstall_keeps_user_data": True}, indent=2) + "\n")
    print("Native installer upgrade, cancellation, cleanup, reinstall and Start Menu handoff passed")


if __name__ == "__main__":
    main()
