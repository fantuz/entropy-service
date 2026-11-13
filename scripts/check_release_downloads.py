#!/usr/bin/env python3
"""Verify stable README downloads against GoReleaser aliases and real artifacts."""
import argparse
import hashlib
from html.parser import HTMLParser
from pathlib import Path
import re


REPOSITORY = "https://github.com/fantuz/entropy-service"
LATEST = REPOSITORY + "/releases/latest/download/"
ALIASES = {
    "entropy-service_linux_amd64.tar.gz": "entropy-service_{{ .Version }}_linux_amd64.tar.gz",
    "entropy-service_linux_arm64.tar.gz": "entropy-service_{{ .Version }}_linux_arm64.tar.gz",
    "entropy-service_linux_armv7.tar.gz": "entropy-service_{{ .Version }}_linux_armv7.tar.gz",
    "entropy-service_windows_amd64.zip": "entropy-service_{{ .Version }}_windows_amd64.zip",
}


class Links(HTMLParser):
    def __init__(self):
        super().__init__()
        self.hrefs = []

    def handle_starttag(self, tag, attrs):
        if tag == "a":
            self.hrefs.extend(value for name, value in attrs if name == "href" and value is not None)


def configured_aliases(config, section):
    """Check our explicit glob/name pairs; GoReleaser validates the YAML itself."""
    match = re.search(r"^" + re.escape(section) + r":[ \t]*\n((?:[ \t].*\n|\n|#.*\n)*)", config, re.MULTILINE)
    if not match:
        raise ValueError(f"missing {section} configuration")
    pairs = re.findall(r"^    - glob: (.+)\n      name_template: (.+)$", match[1], re.MULTILINE)
    result = {}
    for source, name in pairs:
        if name in result:
            raise ValueError(f"duplicate {section} alias: {name}")
        result[name] = source
    return result


def check_contract(config, readme):
    expected = {name: "./dist/" + source for name, source in ALIASES.items()}
    for section in ("release", "checksum"):
        if configured_aliases(config, section) != expected:
            raise ValueError(f"{section} aliases must map all four stable downloads to canonical archives")
    links = Links()
    links.feed(readme)
    stable = {href.removeprefix(LATEST) for href in links.hrefs if href.startswith(LATEST)}
    if not set(ALIASES).issubset(stable):
        raise ValueError(f"README is missing stable archive links: {sorted(set(ALIASES) - stable)}")
    if any("{{" in href or ".Version" in href for href in links.hrefs):
        raise ValueError("README download links cannot contain unresolved release templates")


def read_checksums(path):
    result = {}
    for line in path.read_text().splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})  (.+)", line)
        if not match:
            raise ValueError(f"invalid SHA-256 checksum line: {line!r}")
        digest, name = match.groups()
        if name in result:
            raise ValueError(f"duplicate checksum name: {name}")
        if Path(name).name != name or "/" in name or "\\" in name:
            raise ValueError(f"checksum must use an asset basename: {name}")
        result[name] = digest
    return result


def check_dist(dist):
    checksums = read_checksums(dist / "checksums.txt")
    outcomes = []
    for alias, template in ALIASES.items():
        # Stable aliases exist on GitHub, not as extra files in dist: this keeps
        # the existing one-file Windows/package validation globs unambiguous.
        if (dist / alias).exists():
            raise ValueError(f"stable aliases must not duplicate files in dist: {alias}")
        pattern = template.replace("{{ .Version }}", "*")
        archives = list(dist.glob(pattern))
        if len(archives) != 1:
            raise ValueError(f"expected one canonical archive for {alias}; found {len(archives)}")
        archive = archives[0]
        with archive.open("rb") as stream:
            digest = hashlib.file_digest(stream, "sha256").hexdigest()
        if checksums.get(alias) != digest or checksums.get(archive.name) != digest:
            raise ValueError(f"alias/canonical checksums do not match actual archive: {alias}")
        outcomes.append({"alias": alias, "canonical": archive.name, "sha256": digest})
    return outcomes


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dist", type=Path, help="validate actual snapshot/release archives and checksums")
    parser.add_argument("--config", type=Path, default=Path(".goreleaser.yml"))
    parser.add_argument("--readme", type=Path, default=Path("README.md"))
    args = parser.parse_args()
    check_contract(args.config.read_text(), args.readme.read_text())
    outcomes = check_dist(args.dist) if args.dist is not None else []
    print(f"Verified four stable README/release/checksum aliases; {len(outcomes)} actual archives checked")


if __name__ == "__main__":
    main()
