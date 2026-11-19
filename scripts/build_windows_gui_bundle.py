#!/usr/bin/env python3
"""Stage the installed MSYS2 UCRT64 runtime, notices and exact source packages.

Run with UCRT64 Python after installing GTK/gobject/cairo. The output is consumed
by Inno Setup; no package manager is needed on the destination machine.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor
import hashlib
import json
from pathlib import Path
import re
import shutil
import subprocess
import sys
import urllib.request
import zipfile


ROOT = Path(__file__).resolve().parents[1]
PREFIX = "mingw-w64-ucrt-x86_64-"


def stage_ui_locales(source, destination):
    """Keep web translations separate from binaries and passphrase dictionaries."""
    catalogs = sorted(source.glob("*.json"))
    if not any(path.name == "en.json" for path in catalogs):
        raise ValueError("Web interface catalogs must include the English fallback")
    records = []
    for catalog in catalogs:
        if catalog.is_symlink() or not catalog.is_file() or not re.fullmatch(r"[a-z]{2}(?:-[A-Z]{2})?\.json", catalog.name):
            raise ValueError("Unexpected web interface catalog: " + catalog.name)
        body = catalog.read_bytes()
        if len(body) > 2 * 1024 * 1024:
            raise ValueError("Web interface catalog exceeds 2 MiB: " + catalog.name)
        messages = json.loads(body.decode("utf-8"))
        if not isinstance(messages, dict) or not messages or any(not isinstance(value, str) for value in messages.values()):
            raise ValueError("Invalid web interface message map: " + catalog.name)
        records.append({"language": catalog.stem, "file": catalog.name,
                        "sha256": hashlib.sha256(body).hexdigest()})
    destination.mkdir(parents=True, exist_ok=False)
    for catalog in catalogs:
        shutil.copy2(catalog, destination / catalog.name)
    return records


def fields(text, separator):
    result = {}
    for line in text.splitlines():
        if separator in line:
            key, value = line.split(separator, 1)
            result.setdefault(key.strip(), []).append(value.strip())
    return result


def download_source(item):
    url, destination = item
    request = urllib.request.Request(url, headers={"User-Agent": "entropy-service-runtime-sources"})
    with urllib.request.urlopen(request, timeout=120) as response, destination.open("wb") as output:
        shutil.copyfileobj(response, output)
    with destination.open("rb") as source:
        digest = hashlib.file_digest(source, "sha256").hexdigest()
    return {"url": url, "file": destination.name, "sha256": digest}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--sources", type=Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--source-sha", required=True)
    args = parser.parse_args()
    if sys.platform != "win32" or "ucrt64" not in str(Path(sys.prefix)).lower():
        parser.error("UCRT64 native Windows Python is required")
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+(?:-[a-zA-Z0-9.-]+)?", args.version):
        parser.error("invalid version")
    if not re.fullmatch(r"[0-9a-f]{40}", args.source_sha):
        parser.error("invalid source revision")
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=False)
    source_dir = args.sources.resolve()
    source_dir.mkdir(parents=True, exist_ok=False)
    prefix = Path(sys.prefix)
    msys = prefix.parent
    pacman = msys / "usr/bin/pacman.exe"
    tar = msys / "usr/bin/bsdtar.exe"
    packages = [line.split() for line in subprocess.check_output([str(pacman), "-Q"], text=True).splitlines()
                if line.startswith(PREFIX)]
    subprocess.run([str(pacman), "-Sw", "--noconfirm", *[name for name, _ in packages]], check=True)
    cache = msys / "var/cache/pacman/pkg"
    records, source_requests = [], {}
    metadata = output / "runtime-package-metadata"
    metadata.mkdir()
    for name, version in packages:
        files = list(cache.glob(name + "-" + version.split(":")[-1] + "-*.pkg.tar.zst"))
        if len(files) != 1:
            raise RuntimeError(f"Expected exactly one verified cached archive for {name} {version}: {files}")
        info_text = subprocess.check_output([str(tar), "-xOf", str(files[0]), ".PKGINFO"], text=True)
        info = fields(info_text, " = ")
        if info["pkgname"] != [name] or info["pkgver"] != [version]:
            raise RuntimeError("Installed package metadata differs from the cached archive")
        base = info["pkgbase"][0]
        filename = f"{base}-{version.split(':')[-1]}.src.tar.zst"
        url = "https://repo.msys2.org/mingw/sources/" + filename
        source_requests[filename] = (url, source_dir / filename)
        (metadata / (name + ".PKGINFO")).write_text(info_text, encoding="utf-8")
        records.append({"name": name, "version": version, "licenses": info.get("license", []), "source": filename})
    with ThreadPoolExecutor(max_workers=4) as pool:
        sources = list(pool.map(download_source, source_requests.values()))
    shutil.copytree(prefix, output / "runtime", ignore=shutil.ignore_patterns("__pycache__"))
    shutil.copytree(ROOT / "tools/desktop-gui", output / "app", ignore=shutil.ignore_patterns("__pycache__"))
    shutil.copy2(ROOT / "tools/windows-installer/gui_bootstrap.py", output / "app/gui_bootstrap.py")
    (output / "bin").mkdir()
    for name in ("entropy-server.exe", "entropy-client.exe"):
        shutil.copy2(ROOT / "bin" / name, output / "bin" / name)
    ui_locales = stage_ui_locales(ROOT / "cmd/entropy-server/web/locales", output / "bin/ui-locales")
    for name in ("LICENSE", "QUANTIS_NOTICES.txt"):
        shutil.copy2(ROOT / name, output / name)
    shutil.copy2(ROOT / "cmd/entropy-server/internal/wordlist/NOTICE.txt", output / "WORDLIST_NOTICES.txt")
    shutil.copytree(ROOT / "docs", output / "docs")
    (output / "THIRD_PARTY_NOTICES.txt").write_text("This bundle includes MSYS2 Python/GTK and their dependencies.\nLicense files are retained under runtime/share/licenses and within each runtime package.\nSee bundle-manifest.json for exact package versions, licenses, corresponding source archive URLs and SHA-256 hashes.\nThe companion runtime-sources ZIP contains those complete source packages and recipes.\nEntropy Service source is included under app; the complete project revision is recorded in the manifest.\n", encoding="utf-8")
    manifest = {"version": args.version, "source_sha": args.source_sha, "packages": records, "sources": sources,
                "web_ui_locales": ui_locales}
    (output / "bundle-manifest.json").write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    (source_dir / "bundle-manifest.json").write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    for name in ("LICENSE",):
        shutil.copy2(ROOT / name, source_dir / name)
    sources_zip = Path(str(source_dir) + ".zip")
    with zipfile.ZipFile(sources_zip, "w", compression=zipfile.ZIP_STORED, allowZip64=True) as archive:
        for path in sorted(source_dir.iterdir()):
            archive.write(path, path.name)
    print(f"Bundle: {output}; runtime packages: {len(records)}; corresponding source archive: {sources_zip}")


if __name__ == "__main__":
    main()
