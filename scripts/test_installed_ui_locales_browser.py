#!/usr/bin/env python3
"""Real-browser qualification of the optional, installed interface catalogs.

Build the external_ui_locales variant once into an isolated executable directory.
The same browser origin is reused across restarts so saved choices and real HTTP
caches participate in removal/reinstallation checks. Random-word dictionaries
remain independent from these optional interface translations.
"""
import argparse
from contextlib import contextmanager
import json
from pathlib import Path
import re
import shutil
import signal
import subprocess
import tempfile
from urllib.parse import urlsplit

from test_sources_browser import free_port, serve_process


ROOT = Path(__file__).resolve().parents[1]
PREFERENCE = "entropy-ui-language"


@contextmanager
def server(binary, port, log_path):
    process = None
    with log_path.open("w") as log:
        try:
            process = serve_process(binary, port, log, extra=("-refresh=60000",))
            yield
        finally:
            if process is not None and process.poll() is None:
                process.send_signal(signal.SIGINT)
                try:
                    process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)


def assert_interface(page, languages, active, heading):
    from playwright.sync_api import expect

    expect(page.locator("html")).to_have_attribute("lang", active)
    expect(page.locator("html")).to_have_attribute("data-ui-language", active)
    expect(page.locator("#explore-title")).to_have_text(heading)
    assert page.evaluate("Object.keys(EntropyI18n.languages)") == languages
    picker = page.locator("select[data-ui-language]")
    if languages == ["en"]:
        expect(picker).to_have_count(0)
    else:
        expect(picker).to_have_value(active)
        assert picker.evaluate("node=>[...node.options].map(option=>option.value)") == languages


def word_dictionary_checks(page, base):
    from playwright.sync_api import expect

    response = page.context.request.get(base + "/wordlists.json")
    assert response.status == 200
    codes = [item["code"] for item in response.json()["languages"]]
    assert len(codes) == 18 and "ru" in codes, codes
    page.goto(base + "/api-words.html?words=2&bytes=64&refresh=1000&lang=ru")
    picker = page.locator("#word-language")
    expect(picker).to_be_enabled()
    expect(picker).to_have_value("ru")
    assert picker.evaluate("node=>[...node.options].map(option=>option.value)") == codes
    expect(page.locator("#words-content")).to_have_attribute("lang", "ru")
    expect(page.locator("#words-content")).not_to_have_text("—")
    expect(page.locator("#hash")).to_have_text(re.compile(r"^[a-f0-9]{64}$"))
    page.goto(base + "/")
    return codes


def qualify(binary, artifacts, engine, chromium):
    from playwright.sync_api import sync_playwright

    locale_directory = binary.parent / "ui-locales"
    french_path = ROOT / "cmd/entropy-server/web/locales/fr.json"
    german_path = ROOT / "cmd/entropy-server/web/locales/de.json"
    french = json.loads(french_path.read_text())
    german = json.loads(german_path.read_text())
    english_heading = "Explore in your browser"
    port = free_port()
    base = f"http://127.0.0.1:{port}"
    results = {"browser": engine, "build_tag": "external_ui_locales", "stages": []}
    with sync_playwright() as playwright:
        if engine == "firefox":
            browser = playwright.firefox.launch(headless=True, firefox_user_prefs={
                "gfx.webrender.software": True, "layers.acceleration.disabled": True})
        else:
            browser = playwright.chromium.launch(executable_path=chromium or None, headless=True,
                args=["--no-sandbox", "--disable-dev-shm-usage"])
        context = browser.new_context(locale="fr-FR", viewport={"width": 1440, "height": 1000})
        page = context.new_page()
        errors, catalog_requests = [], []
        page.on("pageerror", lambda error: errors.append(str(error)))
        page.on("request", lambda request: catalog_requests.append(request.url)
                if urlsplit(request.url).path.startswith("/locales/") else None)
        try:
            # A French browser must fall back locally when no optional catalog
            # directory exists. The English-only build needs no language picker.
            with server(binary, port, artifacts / "english-only-server.log"):
                page.goto(base + "/")
                assert_interface(page, ["en"], "en", english_heading)
                assert not catalog_requests, catalog_requests
                assert page.evaluate(f"localStorage.getItem('{PREFERENCE}')") is None
                assert context.request.get(base + "/locales/fr.json").status == 404
                assert context.request.get(base + "/locales/ru.json").status == 404
                results["word_languages"] = word_dictionary_checks(page, base)
                page.screenshot(path=str(artifacts / "english-only.png"))
                locale_directory.mkdir()
                shutil.copyfile(french_path, locale_directory / "fr.json")
                shutil.copyfile(german_path, locale_directory / "de.json")
                page.reload()
                assert_interface(page, ["en"], "en", english_heading)
                assert context.request.get(base + "/locales/fr.json").status == 404
                results["stages"].append({"stage": "missing-directory", "languages": ["en"],
                    "catalog_requests": list(catalog_requests), "new_files_require_restart": True})

            with server(binary, port, artifacts / "french-german-server.log"):
                page.reload()
                assert_interface(page, ["en", "fr", "de"], "fr", french[english_heading])
                assert context.request.get(base + "/locales/fr.json").json() == french
                assert context.request.get(base + "/locales/de.json").json() == german
                assert context.request.get(base + "/locales/ru.json").status == 404
                page.locator("select[data-ui-language]").select_option("de")
                assert_interface(page, ["en", "fr", "de"], "de", german[english_heading])
                page.locator("select[data-ui-language]").select_option("en")
                assert_interface(page, ["en", "fr", "de"], "en", english_heading)
                page.locator("select[data-ui-language]").select_option("fr")
                assert_interface(page, ["en", "fr", "de"], "fr", french[english_heading])
                assert page.evaluate(f"localStorage.getItem('{PREFERENCE}')") == "fr"
                assert word_dictionary_checks(page, base) == results["word_languages"]
                assert_interface(page, ["en", "fr", "de"], "fr", french[english_heading])
                page.screenshot(path=str(artifacts / "french-installed.png"))
                (locale_directory / "fr.json").unlink()
                # Running servers serve an immutable startup snapshot, rather
                # than observing a partly updated installer directory.
                assert context.request.get(base + "/locales/fr.json").status == 200
                results["stages"].append({"stage": "french-german", "languages": ["en", "fr", "de"],
                    "selected": "fr", "removed_file_remains_until_restart": True})

            with server(binary, port, artifacts / "french-removed-server.log"):
                catalog_requests.clear()
                page.reload()
                assert_interface(page, ["en", "de"], "en", english_heading)
                assert page.evaluate(f"localStorage.getItem('{PREFERENCE}')") == "fr"
                assert not any(urlsplit(url).path == "/locales/fr.json" for url in catalog_requests), catalog_requests
                assert context.request.get(base + "/locales/fr.json").status == 404
                assert context.request.get(base + "/locales/de.json").status == 200
                page.screenshot(path=str(artifacts / "french-removed.png"))
                results["stages"].append({"stage": "french-removed", "languages": ["en", "de"],
                    "selected": "en", "saved_preference": "fr", "catalog_requests": list(catalog_requests)})

            shutil.copyfile(french_path, locale_directory / "fr.json")
            with server(binary, port, artifacts / "french-restored-server.log"):
                page.reload()
                assert_interface(page, ["en", "fr", "de"], "fr", french[english_heading])
                assert page.evaluate(f"localStorage.getItem('{PREFERENCE}')") == "fr"
                results["stages"].append({"stage": "french-restored", "languages": ["en", "fr", "de"],
                    "selected": "fr", "saved_preference_restored": True})
            assert not errors, errors
            return {**results, "browser_errors": errors}
        except Exception as error:
            failure = {"type": type(error).__name__, "message": str(error), "stages": results["stages"],
                "catalog_requests": catalog_requests, "browser_errors": errors}
            (artifacts / "failure.json").write_text(json.dumps(failure, indent=2) + "\n")
            try:
                page.screenshot(path=str(artifacts / "failure.png"), timeout=3000)
            except Exception:
                pass
            raise
        finally:
            try:
                context.close()
            finally:
                browser.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--browser", choices=("chromium", "firefox"), default="chromium")
    parser.add_argument("--chromium", default="")
    parser.add_argument("--binary", type=Path, help="Already-built external_ui_locales server; copied into an isolated directory")
    parser.add_argument("--artifacts", type=Path, default=ROOT / "browser-artifacts/installed-ui-locales")
    args = parser.parse_args()
    artifacts = args.artifacts.resolve()
    artifacts.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="entropy-installed-ui-locales-") as temporary:
        binary = Path(temporary) / "entropy-server"
        if args.binary:
            shutil.copy2(args.binary.resolve(), binary)
        else:
            with (artifacts / "build.log").open("w") as log:
                subprocess.run(["go", "build", "-tags=external_ui_locales", "-o", str(binary), "./cmd/entropy-server"],
                    cwd=ROOT, stdout=log, stderr=subprocess.STDOUT, timeout=180, check=True)
        results = qualify(binary, artifacts, args.browser, args.chromium)
    (artifacts / "results.json").write_text(json.dumps(results, ensure_ascii=False, indent=2) + "\n")
    print(f"{args.browser}: installed UI subsets, saved-choice removal/restoration and all 18 word languages passed")


if __name__ == "__main__":
    main()
