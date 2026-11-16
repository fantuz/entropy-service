"""Relocatable entry point for the private Windows Python/GTK runtime."""
import argparse
import ctypes
import os
from pathlib import Path
import runpy
import subprocess
import tempfile
import sys
import traceback


DLL_DIRECTORIES = []


def configure_runtime(root, state):
    root, state = Path(root).resolve(), Path(state).resolve()
    runtime = root / "runtime"
    state.mkdir(parents=True, exist_ok=True)
    DLL_DIRECTORIES.append(os.add_dll_directory(str(runtime / "bin")))
    os.environ["PATH"] = str(runtime / "bin") + os.pathsep + os.environ.get("PATH", "")
    os.environ["GI_TYPELIB_PATH"] = str(runtime / "lib/girepository-1.0")
    os.environ["GSETTINGS_SCHEMA_DIR"] = str(runtime / "share/glib-2.0/schemas")
    os.environ["XDG_DATA_DIRS"] = str(runtime / "share")
    os.environ["XDG_CACHE_HOME"] = str(state / "cache")
    os.environ["GTK_EXE_PREFIX"] = str(runtime)
    os.environ["GTK_DATA_PREFIX"] = str(runtime)
    os.environ["GIO_MODULE_DIR"] = str(runtime / "lib/gio/modules")
    os.environ["FONTCONFIG_PATH"] = str(runtime / "etc/fonts")
    os.environ["FONTCONFIG_FILE"] = str(runtime / "etc/fonts/fonts.conf")
    loaders = runtime / "lib/gdk-pixbuf-2.0/2.10.0/loaders"
    query = runtime / "bin/gdk-pixbuf-query-loaders.exe"
    if query.is_file() and loaders.is_dir():
        os.environ["GDK_PIXBUF_MODULEDIR"] = str(loaders)
        cache = state / "pixbuf-loaders.cache"
        with tempfile.NamedTemporaryFile(dir=state, delete=False) as output:
            temporary = Path(output.name)
            try:
                subprocess.run([str(query)], stdout=output, stderr=subprocess.PIPE, timeout=15, check=True)
            except BaseException:
                output.close()
                temporary.unlink(missing_ok=True)
                raise
        os.replace(temporary, cache)
        os.environ["GDK_PIXBUF_MODULE_FILE"] = str(cache)
    os.chdir(state)
    sys.path.insert(0, str(root / "app"))


def main():
    root = Path(__file__).resolve().parents[1]
    state = Path(os.environ["LOCALAPPDATA"]) / "Entropy Service"
    try:
        configure_runtime(root, state)
        arguments = sys.argv[1:]
        # Only the bundled server shortcut defaults to the local HTTP demo.
        # The ordinary CLI and operator-supplied startup options stay intact.
        parser = argparse.ArgumentParser(add_help=False)
        parser.add_argument("--view", default="server")
        options, _ = parser.parse_known_args(arguments[:arguments.index("--")] if "--" in arguments else arguments)
        if "--" not in arguments and options.view == "server":
            arguments += ["--", "-http=127.0.0.1:8080", "-enable-https=false"]
        launcher = root / "app/entropy-gui"
        sys.argv = [str(launcher), "--server-binary", str(root / "bin/entropy-server.exe"),
                    "--client-binary", str(root / "bin/entropy-client.exe"), *arguments]
        try:
            runpy.run_path(str(launcher), run_name="__main__")
        except SystemExit as error:
            if error.code not in (None, 0):
                raise RuntimeError(f"GUI exited with status {error.code}") from error
        return 0
    except Exception:
        state.mkdir(parents=True, exist_ok=True)
        log = state / "gui-startup-error.txt"
        log.write_text(traceback.format_exc(), encoding="utf-8")
        ctypes.windll.user32.MessageBoxW(None, "The GUI could not start. Diagnostics: " + str(log),
                                        "Entropy Service", 0x10)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
