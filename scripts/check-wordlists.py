#!/usr/bin/env python3
"""Check pinned dictionaries, or explicitly reproduce them from upstream data."""

import argparse
import hashlib
import itertools
import json
from pathlib import Path
import re
import unicodedata
import urllib.request


ROOT = Path(__file__).resolve().parents[1] / "cmd/entropy-server/internal/wordlist"


def digest(data):
    return hashlib.sha256(data).hexdigest()


def spelling_subset(raw, code):
    lines = raw.decode("utf-8-sig").splitlines()
    # The pinned Tamil source overstates its record count by one. Do not
    # silently accept other header mismatches, or repair the upstream data.
    adjustment = 1 if code == "ta" else 0
    if not lines or int(lines[0].split()[0]) != len(lines) - 1 + adjustment:
        raise ValueError("Hunspell entry count differs from the pinned source contract")
    stems = {unicodedata.normalize("NFC", line.split("/", 1)[0]) for line in lines[1:]}
    if code == "af":
        eligible = {word for word in stems if 4 <= len(word) <= 12 and word.isalpha() and word.islower()}
    else:
        script = {"ta": "TAMIL", "ne": "DEVANAGARI", "ar": "ARABIC"}[code]
        eligible = {word for word in stems if 3 <= len(word) <= 12 and unicodedata.category(word[0]).startswith("L")
                    and all(unicodedata.category(char)[0] in "LM" and unicodedata.name(char, "").startswith(script + " ") for char in word)}
    if len(eligible) < 8192:
        raise ValueError("Spelling subset requires at least 8192 eligible stems")
    prefix = f"entropy-service:{code}:v1:".encode("ascii")
    ranked = sorted(eligible, key=lambda word: (hashlib.sha256(prefix + word.encode("utf-8")).digest(), word))
    return sorted(ranked[:8192])


def convert(raw, kind):
    lines = raw.decode("utf-8").splitlines()
    if re.fullmatch(r"hunspell-(af|ta|ne|ar)-subset-v1", kind):
        lines = spelling_subset(raw, kind.split("-")[1])
    elif kind == "dice-indexed":
        fields = [line.split() for line in lines]
        expected = ["".join(roll) for roll in itertools.product("123456", repeat=5)]
        assert len(fields) == len(expected)
        assert all(len(row) == 2 and row[0] == code for row, code in zip(fields, expected))
        lines = [row[1] for row in fields]
    else:
        assert kind == "plain"
    return ("\n".join(unicodedata.normalize("NFC", word) for word in lines) + "\n").encode("utf-8")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--import", dest="fetch", action="store_true", help="fetch pinned sources, verify and reproduce bundled data")
    args = parser.parse_args()
    catalog = json.loads((ROOT / "manifest.json").read_text())
    assert catalog and catalog[0]["code"] == "en"
    codes = set()
    for item in catalog:
        code = item["code"]
        assert re.fullmatch(r"[a-z]{2}(?:-[A-Za-z]{2,8})?", code) and code not in codes
        codes.add(code)
        path = ROOT / "data" / (code + ".txt")
        if args.fetch:
            request = urllib.request.Request(item["source_url"], headers={"User-Agent": "entropy-service-wordlist-import"})
            with urllib.request.urlopen(request, timeout=30) as response:
                raw = response.read((16 << 20) + 1)
            assert len(raw) <= 16 << 20, "source exceeds bounded import size"
            assert digest(raw) == item["source_sha256"], f"upstream checksum differs: {code}"
            data = convert(raw, item["source_format"])
            assert digest(data) == item["sha256"], f"derived checksum differs: {code}"
            path.write_bytes(data)
        data = path.read_bytes()
        assert digest(data) == item["sha256"], f"bundled checksum differs: {code}"
        words = data.decode("utf-8").splitlines()
        assert len(words) == item["size"] == len(set(words)), f"word count or duplicates: {code}"
        assert data.endswith(b"\n")
        for word in words:
            assert word and len(word.encode("utf-8")) <= 128 and unicodedata.normalize("NFC", word) == word
            assert not any(char.isspace() or unicodedata.category(char) in ("Cc", "Cf") for char in word)
        assert item["license"] and item["author"] and item["source_url"].startswith("https://raw.githubusercontent.com/")
        assert "/main/" not in item["source_url"] and "/master/" not in item["source_url"]
        print(f"{code}: {len(words)} distinct NFC words, checksum and attribution verified")


if __name__ == "__main__":
    main()
