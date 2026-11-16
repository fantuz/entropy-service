#!/usr/bin/env python3
"""Exercise actual GoReleaser extra-file hashing with a tiny isolated Go fixture."""
import argparse
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

from prepare_gui_release import release_config


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--goreleaser", default=shutil.which("goreleaser"))
    args = parser.parse_args()
    if not args.goreleaser:
        parser.error("GoReleaser is required")
    with tempfile.TemporaryDirectory(prefix="entropy-glob-test-") as temporary:
        root = Path(temporary)
        project = root / "project"
        project.mkdir()
        (project / "go.mod").write_text("module fixture.example/installer\n\ngo 1.26.11\n")
        (project / "main.go").write_text("package main\nfunc main() {}\n")
        (project / ".gitignore").write_text("/bin/\n/dist/\n")
        assets = project / "bin/gui-release"
        assets.mkdir(parents=True)
        files = [assets / "setup.exe", assets / "sources.zip"]
        for file in files:
            file.write_bytes(b"unchanged extra release bytes")
        base = '''version: 2
project_name: fixture
builds:
  - main: .
    goos: [linux]
    goarch: [amd64]
archives:
  - formats: [tar.gz]
release:
  extra_files:
checksum:
  name_template: checksums.txt
  algorithm: sha256
  extra_files:
'''
        config = root / "publication.yml"
        config.write_text(release_config(base, files, project))
        for command in (["git", "init", "-q"], ["git", "add", "."],
                        ["git", "-c", "user.name=Release fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture"],
                        ["git", "remote", "add", "origin", "https://github.com/fantuz/entropy-service.git"],
                        ["git", "tag", "v2.4.1"]):
            subprocess.run(command, cwd=project, check=True, capture_output=True)
        environment = dict(os.environ, CGO_ENABLED="0", GOFLAGS="-buildvcs=false")
        result = subprocess.run([args.goreleaser, "release", "--snapshot", "--skip=publish", "--clean", "--config", str(config)],
                                cwd=project, env=environment, capture_output=True, text=True, timeout=90)
        if result.returncode:
            raise RuntimeError(result.stdout + result.stderr)
        manifest = (project / "dist/checksums.txt").read_text()
        for file in files:
            expected = hashlib.sha256(file.read_bytes()).hexdigest() + "  " + file.name
            assert expected in manifest, (file.name, manifest)
        print("Actual GoReleaser extra-file globbing and checksums passed; staged assets remain byte-identical")


if __name__ == "__main__":
    main()
