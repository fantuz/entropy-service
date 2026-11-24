#!/usr/bin/env python3
"""Stage GoReleaser's native packages and prepare a publication-only configuration."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import shutil

from check_release_downloads import ALIASES, read_checksums
from oci_release import names_for, regular, verify as verify_oci
from prepare_gui_release import verify_artifacts


def native_assets(version):
    """Map every public native asset name, including stable aliases, to its file."""
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?", version):
        raise ValueError("invalid native package version")
    aliases = {name: template.replace("{{ .Version }}", version) for name, template in ALIASES.items()}
    canonical = set(aliases.values())
    for arch in ("amd64", "arm64", "armv7"):
        canonical.update(f"entropy-service_{version}_linux_{arch}.{fmt}" for fmt in ("deb", "rpm"))
    return {**{name: name for name in canonical}, **aliases}


def verify_native(directory, version, source_sha, staged):
    names = native_assets(version)
    if not re.fullmatch(r"[0-9a-f]{40}", source_sha):
        raise ValueError("invalid source commit")
    for name in ("metadata.json", "checksums.txt"):
        regular(directory / name)
    meta = json.loads((directory / "metadata.json").read_text())
    if (meta.get("project_name"), meta.get("version"), meta.get("tag"), meta.get("commit")) != (
            "entropy-service", version, "v" + version, source_sha):
        raise ValueError("native GoReleaser metadata differs from the requested source or version")
    files = set(names.values())
    if staged and {p.name for p in directory.iterdir()} != files | {"metadata.json", "checksums.txt"}:
        raise ValueError("unexpected or missing staged native files")
    checksums = read_checksums(directory / "checksums.txt")
    if set(checksums) != set(names):
        raise ValueError("native checksum inventory differs from expected release assets")
    for name in files:
        path = directory / name
        regular(path)
        with path.open("rb") as f:
            if hashlib.file_digest(f, "sha256").hexdigest() != checksums[name]:
                raise ValueError("native package checksum mismatch: " + name)
    if any(checksums[name] != checksums[file] for name, file in names.items()):
        raise ValueError("stable native alias differs from its canonical file")
    return {name: directory / file for name, file in names.items()}


def stage(dist, native, version, source_sha):
    files = verify_native(dist, version, source_sha, staged=False)
    native.mkdir(parents=True)
    for path in set(files.values()) | {dist / "metadata.json", dist / "checksums.txt"}:
        shutil.copyfile(path, native / path.name)


def publication_config(files):
    # GoReleaser globs are relative to its working directory. Keep staged inputs
    # outside the clean output directory and reference alias bytes in place.
    extras = [{"glob": "./" + path.resolve().relative_to(Path.cwd()).as_posix(), "name_template": name}
              for name, path in sorted(files.items())]
    config = {"version": 2, "project_name": "entropy-service", "dist": "bin/publication-dist",
              "builds": [{"skip": True}], "release": {"extra_files": extras},
              "checksum": {"name_template": "checksums.txt", "algorithm": "sha256", "extra_files": extras}}
    # Use the tag's existing changelog policy when a release has no curated notes.
    # JSON values are valid inline YAML and need no optional Python YAML runtime.
    lines = [f"{key}: {json.dumps(value)}" for key, value in config.items()]
    base = Path(".goreleaser.yml").read_text()
    changelog = re.search(r"^changelog:[^\n]*(?:\n(?:[ \t#][^\n]*|(?=\n)))*", base, re.MULTILINE)
    if changelog:
        lines.append(changelog[0])
    return "\n".join(lines) + "\n"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    stage_parser = commands.add_parser("stage", help="verify and copy the native payloads from the build")
    stage_parser.add_argument("--dist", type=Path, default=Path("dist"))
    rehearse = commands.add_parser("rehearse", help="prepare a native-only configuration for a CI dry run")
    prepare = commands.add_parser("prepare", help="verify native, GUI and OCI payloads for publication")
    for command in (stage_parser, rehearse, prepare):
        command.add_argument("--native", type=Path, required=True)
        command.add_argument("--version", required=True)
        command.add_argument("--source-sha", required=True)
    for command in (rehearse, prepare):
        command.add_argument("--output", type=Path, required=True)
    prepare.add_argument("--gui", type=Path, required=True)
    prepare.add_argument("--oci-artifacts", type=Path, required=True)
    prepare.add_argument("--repository", required=True)
    args = parser.parse_args()
    if args.command == "stage":
        stage(args.dist, args.native, args.version, args.source_sha)
        return
    files = verify_native(args.native, args.version, args.source_sha, staged=True)
    if args.command == "prepare":
        gui_files = verify_artifacts(args.gui, args.version, args.source_sha)
        verify_oci(args.oci_artifacts, args.source_sha, args.version, args.repository)
        for path in gui_files + [args.oci_artifacts / name for name in names_for(args.version).values()]:
            files[path.name] = path
    args.output.write_text(publication_config(files))
    print(f"Verified {len(files)} payload names for v{args.version} at {args.source_sha}")


if __name__ == "__main__":
    main()
