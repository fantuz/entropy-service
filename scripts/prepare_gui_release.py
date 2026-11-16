#!/usr/bin/env python3
"""Select an installer build version or verify tested GUI artifacts for release."""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import stat
import zipfile

from check_release_downloads import read_checksums


def select_version(version, release_tag, ref_type, ref_name):
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?", version):
        raise ValueError("VERSION is not a supported release version")
    if release_tag and (ref_type != "tag" or ref_name != release_tag or release_tag != "v" + version):
        raise ValueError("release installer requires the matching VERSION tag")
    return version if release_tag else version + "-dev", version.split("-", 1)[0]


def verify_artifacts(directory, version, source_sha):
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?", version):
        raise ValueError("invalid installer version")
    if not re.fullmatch(r"[0-9a-f]{40}", source_sha):
        raise ValueError("invalid source revision")
    names = [f"entropy-service_{version}_windows_amd64_gui_setup.exe",
             f"entropy-service_{version}_windows_amd64_gui_sources.zip"]
    expected = {*names, "checksums.txt"}
    if {p.name for p in directory.iterdir()} != expected:
        raise ValueError("GUI artifact must contain only the matching installer, sources and checksums")
    for name in expected:
        file = directory / name
        if file.is_symlink() or not file.is_file() or file.stat().st_size == 0:
            raise ValueError("GUI assets must be nonempty regular files")
    checksums = read_checksums(directory / "checksums.txt")
    if set(checksums) != set(names):
        raise ValueError("GUI checksum inventory differs from the expected assets")
    for name in names:
        with (directory / name).open("rb") as source:
            digest = hashlib.file_digest(source, "sha256").hexdigest()
        if checksums[name] != digest:
            raise ValueError("GUI artifact checksum mismatch: " + name)
    with (directory / names[0]).open("rb") as source:
        if source.read(2) != b"MZ":
            raise ValueError("GUI setup is not a Windows executable")
    with zipfile.ZipFile(directory / names[1]) as archive:
        entries = archive.infolist()
        seen = set()
        for entry in entries:
            path = PurePosixPath(entry.filename)
            if (path.name != entry.filename or entry.filename in seen or "\\" in entry.filename
                    or ":" in entry.filename or stat.S_ISLNK(entry.external_attr >> 16) or entry.is_dir()):
                raise ValueError("unsafe or duplicate runtime-source archive member")
            seen.add(entry.filename)
        metadata = archive.getinfo("bundle-manifest.json")
        if metadata.file_size > 2 * 1024 * 1024:
            raise ValueError("runtime manifest is unexpectedly large")
        manifest = json.loads(archive.read(metadata))
        if manifest.get("version") != version or manifest.get("source_sha") != source_sha:
            raise ValueError("installer manifest version/source differs from the tagged source")
        sources = manifest.get("sources", [])
        packages = manifest.get("packages", [])
        inventory = {s["file"] for s in sources}
        if not sources or not packages or len(inventory) != len(sources) or not {p["source"] for p in packages} <= inventory:
            raise ValueError("runtime source inventory is incomplete")
        if seen != inventory | {"LICENSE", "bundle-manifest.json"}:
            raise ValueError("runtime source ZIP differs from its manifest")
        for source in sources:
            with archive.open(source["file"]) as stream:
                digest = hashlib.file_digest(stream, "sha256").hexdigest()
            if digest != source["sha256"]:
                raise ValueError("runtime source checksum mismatch")
    return [directory / name for name in names]


def release_config(config, files):
    # Keep snapshot/native packaging independent of the optional Windows job.
    # Only publication consumes the augmented config and the tested artifacts.
    extra = "".join("    - glob: " + json.dumps(str(p.resolve())) + "\n      name_template: " + json.dumps(p.name) + "\n" for p in files)
    for section in ("release", "checksum"):
        match = re.search(r"^" + section + r":[ \t]*\n((?:[ \t].*\n|\n|#.*\n)*)", config, re.MULTILINE)
        if not match or match[1].count("  extra_files:\n") != 1:
            raise ValueError("missing or ambiguous extra_files in " + section)
        at = match.start(1) + match[1].index("  extra_files:\n") + len("  extra_files:\n")
        config = config[:at] + extra + config[at:]
    return config


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    commands.add_parser("version")
    prepare = commands.add_parser("prepare")
    prepare.add_argument("--artifacts", type=Path, required=True)
    prepare.add_argument("--version", required=True)
    prepare.add_argument("--source-sha", required=True)
    prepare.add_argument("--config", type=Path, default=Path(".goreleaser.yml"))
    prepare.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    if args.command == "version":
        version, numeric = select_version(Path("VERSION").read_text().strip(), os.environ.get("GUI_RELEASE_TAG", ""),
                                          os.environ["GITHUB_REF_TYPE"], os.environ["GITHUB_REF_NAME"])
        with open(os.environ["GITHUB_ENV"], "a", encoding="utf-8") as output:
            output.write(f"BUNDLE_VERSION={version}\nBUNDLE_NUMERIC_VERSION={numeric}\n")
        print("Installer version:", version)
    else:
        files = verify_artifacts(args.artifacts, args.version, args.source_sha)
        args.output.write_text(release_config(args.config.read_text(), files))
        print("Verified tagged GUI installer and runtime sources:", *(p.name for p in files))


if __name__ == "__main__":
    main()
