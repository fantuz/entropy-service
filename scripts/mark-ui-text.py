#!/usr/bin/env python3
"""Mark literal interface text without touching payloads, routes or code samples."""
from html import escape, unescape
from html.parser import HTMLParser
import json
from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parents[1] / "cmd/entropy-server"
SKIP_TAGS = {"script", "style", "pre", "code", "textarea", "canvas", "svg", "math", "kbd", "samp", "noscript"}
SKIP_IDS = {"words", "words-content", "hash", "hex", "base64", "file-sha256", "server-report-content", "lab-body-preview"}
VOID = {"area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr"}
ATTRIBUTES = {"aria-label", "title", "placeholder", "alt"}
BRANDS = {"Entropy Service", "SHA256", "SHA-256", "HTTP", "HTTPS", "WebSocket", "RGB", "RGBA", "PCM16LE", "FFmpeg", "ONVIF", "RTSP", "TCP", "UDP", "JSON", "JSONL", "Base64", "Hex", "QR", "ChaCha20", "AES-256-CTR"}


def message(value):
    value = re.sub(r"\s+", " ", unescape(value)).strip()
    if not re.search(r"[A-Za-z]{3}", value) or value in BRANDS:
        return None
    if value.startswith(("/", "http://", "https://", "ws://", "wss://", "GET /", "WS /", "POST /", "-")):
        return None
    return value


class Marker(HTMLParser):
    def __init__(self):
        super().__init__(convert_charrefs=False)
        self.output, self.stack, self.keys = [], [], set()
        self.pending = ""

    def handle_starttag(self, tag, attrs):
        self.flush_text()
        values = dict(attrs)
        skipped = bool(self.stack and self.stack[-1][1]) or tag in SKIP_TAGS or values.get("id") in SKIP_IDS or values.get("translate") == "no" or "data-i18n-skip" in values
        bound = "data-i18n" in values
        raw = self.get_starttag_text()
        for attr in ATTRIBUTES:
            key = message(values.get(attr, "")) if not skipped else None
            if key:
                self.keys.add(key)
                marker = "data-i18n-" + attr
                if marker not in values:
                    raw = raw[:-1] + f' {marker}="{escape(key, quote=True)}">'
        if bound:
            self.keys.add(values["data-i18n"])
        index = len(self.output)
        self.output.append(raw)
        if tag not in VOID:
            self.stack.append((tag, skipped or bound, index))

    def handle_startendtag(self, tag, attrs):
        self.flush_text()
        self.output.append(self.get_starttag_text())

    def handle_endtag(self, tag):
        self.flush_text()
        self.output.append(f"</{tag}>")
        if self.stack and self.stack[-1][0] == tag:
            self.stack.pop()

    def handle_data(self, data):
        self.pending += data

    def flush_text(self):
        data, self.pending = self.pending, ""
        if not data:
            return
        key = message(data) if self.stack and not self.stack[-1][1] else None
        if not key:
            self.output.append(data)
            return
        self.keys.add(key)
        tag, skipped, index = self.stack[-1]
        if tag in {"title", "option"}:
            raw = self.output[index]
            self.output[index] = raw[:-1] + f' data-i18n="{escape(key, quote=True)}">'
            self.output.append(data)
        else:
            leading = data[:len(data) - len(data.lstrip())]
            trailing = data[len(data.rstrip()):]
            self.output.append(leading + f'<span data-i18n="{escape(key, quote=True)}">{escape(key)}</span>' + trailing)

    def handle_entityref(self, name):
        self.pending += f"&{name};"

    def handle_charref(self, name):
        self.pending += f"&#{name};"

    def handle_comment(self, data):
        self.flush_text()
        self.output.append(f"<!--{data}-->")

    def handle_decl(self, data):
        self.flush_text()
        self.output.append(f"<!{data}>")


def main():
    check = "--check" in sys.argv
    keys, changed = set(), []
    paths = list((ROOT / "webtemplates").glob("*.html")) + [p for p in (ROOT / "web").glob("*.html") if not p.name.startswith("zz_")]
    for path in paths:
        original = path.read_text()
        parser = Marker()
        parser.feed(original)
        parser.flush_text()
        result = "".join(parser.output)
        keys.update(parser.keys)
        if result != original:
            changed.append(path.name)
            if not check:
                path.write_text(result)
    manifest = ROOT / "web/locales/en.json"
    existing = json.loads(manifest.read_text()) if manifest.exists() else {}
    missing = keys - existing.keys()
    keys.update(existing)
    if check and missing:
        raise SystemExit("UI messages missing from en.json: " + ", ".join(sorted(missing)))
    if not check:
        manifest.parent.mkdir(exist_ok=True)
        # English source messages are also their fallback translations.
        existing.update({key: key for key in sorted(keys - existing.keys())})
        manifest.write_text(json.dumps(existing, ensure_ascii=False, indent=2) + "\n")
    if check and changed:
        raise SystemExit("Unmarked UI text: " + ", ".join(changed) + "; run scripts/mark-ui-text.py")
    print(f"{len(keys)} distinct literal UI messages; {len(changed)} files {'need annotation' if check else 'annotated'}")


if __name__ == "__main__":
    main()
