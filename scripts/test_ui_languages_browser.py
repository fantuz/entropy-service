#!/usr/bin/env python3
"""Focused real-browser UI-language, payload isolation and persistence checks."""
import argparse
import base64
import hashlib
import json
from pathlib import Path
import re
import signal
import subprocess
from urllib.parse import parse_qs, urlsplit

from test_diagnostics_browser import OBSERVE_WORKSPACE, selected_hash
from test_graph_palettes_browser import checkpoint
from test_sources_browser import free_port, serve_process
from test_stream_browser import wait_paint


LANGUAGES = ("en", "fr", "de", "es", "cs", "ru", "pl", "el", "it")
ROOT = Path(__file__).resolve().parents[1]
PAGES = [
    ("/", "#explore-title", "Explore in your browser"),
    ("/api-bytes.html", "#bytes-hex-title", "Hex preview"),
    ("/api-colors.html", "title", "Entropy Service — Colours"),
    ("/api-words.html", "label[for='word-language']", "Word language"),
    ("/api-sound.html", "h1", "Harmonic Sounds"),
    ("/api-stream.html?bytes=4096&refresh=1000", "h1", "Live Stream Analysis"),
    ("/api-upload.html", "h1", "File Analysis"),
    ("/api-sources.html", "h1", "Source Lab"),
    ("/api-socket.html", "h1", "Socket Lab"),
    ("/api-gallery.html", "h1", "HTTP endpoint gallery"),
    ("/api-status.html", "h1", "Service Status"),
]
OBSERVE_JOBS = """(()=>{
  window.__languageWorkerJobs=0;
  const original=Worker.prototype.postMessage;
  Worker.prototype.postMessage=function(...args){__languageWorkerJobs++;return original.apply(this,args);};
})();"""


def catalogs():
    return {language: {} if language == "en" else json.loads((ROOT / f"cmd/entropy-server/web/locales/{language}.json").read_text())
            for language in LANGUAGES}


def expected(catalog, language, key, values=None):
    message = key if language == "en" else catalog[language][key]
    if values:
        message = re.sub(r"\{([A-Za-z][A-Za-z0-9_]*)\}", lambda match: str(values.get(match.group(1), match.group(0))), message)
    return message


def choose_language(page, language):
    from playwright.sync_api import expect

    page.locator("select[data-ui-language]").select_option(language)
    expect(page.locator("html")).to_have_attribute("lang", language)
    expect(page.locator("html")).to_have_attribute("data-ui-language", language)
    expect(page.locator("select[data-ui-language]")).to_have_value(language)
    wait_paint(page)


def settle_workers(page):
    # Finish worker imports before navigation; this is a protocol acknowledgement,
    # not a timer retry, and avoids treating cancelled startup as a page failure.
    for worker in page.workers:
        worker.evaluate("new Promise(resolve=>setTimeout(resolve,0))")


def assert_selector(page, catalog, language):
    control = page.locator("select[data-ui-language]")
    assert control.evaluate("e=>[...e.options].map(option=>option.value)") == list(LANGUAGES)
    label = control.evaluate("e=>[...e.labels].map(label=>label.textContent).join(' ')")
    assert expected(catalog, language, "Interface language") in label, (language, label)


def assert_heading(page, selector, value):
    from playwright.sync_api import expect

    if selector == "title":
        expect(page).to_have_title(value)
    else:
        expect(page.locator(selector).first).to_have_text(value)


def language_choices(page, base, catalog, artifacts):
    from playwright.sync_api import expect

    page.goto(base + "/")
    results = []
    for language in LANGUAGES:
        checkpoint(artifacts, "language-choice", language=language)
        choose_language(page, language)
        assert_selector(page, catalog, language)
        key = "Explore in your browser"
        translated = expected(catalog, language, key)
        if language != "en":
            assert translated != key, (language, "Critical heading still uses English")
        expect(page.locator("#explore-title")).to_have_text(translated)
        assert page.evaluate("localStorage.getItem('entropy-ui-language')") == language
        results.append(language)
    return results


def page_coverage(page, base, catalog, artifacts):
    from playwright.sync_api import expect

    results = []
    # Cover every main page and every locale without multiplying the full page
    # matrix by all nine languages. Long-script mobile checks follow separately.
    for index, (path, selector, key) in enumerate(PAGES):
        language = LANGUAGES[1:][index % (len(LANGUAGES)-1)]
        checkpoint(artifacts, "page-translation", path=path, language=language)
        page.evaluate("language=>localStorage.setItem('entropy-ui-language',language)", language)
        page.goto(base + path)
        expect(page.locator("html")).to_have_attribute("lang", language)
        assert_selector(page, catalog, language)
        assert_heading(page, selector, expected(catalog, language, key))
        title_key = page.locator("title").get_attribute("data-i18n")
        if title_key:
            assert page.title() == expected(catalog, language, title_key)
        assert not page.evaluate("document.documentElement.scrollWidth>innerWidth"), (path, language)
        settle_workers(page)
        results.append({"path": path, "language": language})
    return results


def frozen_data_checks(page, base, catalog, artifacts):
    from playwright.sync_api import expect

    page.evaluate("localStorage.setItem('entropy-ui-language','en')")
    page.goto(base + "/api-upload.html")
    expect(page.locator("html")).to_have_attribute("lang", "en")
    payload = bytes(range(256))*32
    filename = "File Analysis — <b>literal.bin"
    page.locator("#fileInput").set_input_files({"name": filename, "mimeType": "application/octet-stream", "buffer": payload})
    page.locator("#submit-upload").click()
    expect(page.locator("#upload-status")).to_have_text("Analysis complete")
    page.wait_for_function("Boolean(window.__diagnosticWorkspace?.sample)")
    wait_paint(page)
    raw_hash = selected_hash(page)
    assert raw_hash == hashlib.sha256(payload).hexdigest()
    baseline = page.evaluate("""()=>({jobs:__languageWorkerJobs,frame:__diagnosticWorkspace.sample.frame,
      entropy:__diagnosticWorkspace.sample.entropy,histogram:[...__diagnosticWorkspace.sample.histogram]})""")
    page.locator("select[data-graph-palette]").select_option("blue-orange")
    page.locator("[data-theme-toggle]").click()
    theme = page.locator("html").get_attribute("data-theme")
    for language in LANGUAGES:
        checkpoint(artifacts, "frozen-data-translation", language=language)
        choose_language(page, language)
        assert selected_hash(page) == raw_hash
        expect(page.locator("#file-sha256")).to_have_text(raw_hash)
        assert filename in page.locator("#file-entropy").inner_text()
        assert page.locator("#file-entropy b").count() == 0
        expect(page.locator("#upload-status")).to_have_text(expected(catalog, language, "Analysis complete"))
        current = page.evaluate("""()=>({jobs:__languageWorkerJobs,frame:__diagnosticWorkspace.sample.frame,
          entropy:__diagnosticWorkspace.sample.entropy,histogram:[...__diagnosticWorkspace.sample.histogram]})""")
        assert current == baseline, (language, "UI translation changed data or repeated worker analysis")
        expect(page.locator("html")).to_have_attribute("data-theme", theme)
        expect(page.locator("html")).to_have_attribute("data-graph-palette", "blue-orange")
        expect(page.locator("select[data-graph-palette]")).to_have_value("blue-orange")
        label_key = "Light mode" if theme == "dark" else "Dark mode"
        expect(page.locator("[data-theme-toggle]")).to_have_text(expected(catalog, language, label_key))
    page.screenshot(path=str(artifacts / "translated-file-values.png"))
    return {"sha256": raw_hash, "worker_jobs": baseline["jobs"], "frame": baseline["frame"], "filename_html_interpreted": False}


def word_data_checks(browser, base, catalog, artifacts):
    from playwright.sync_api import expect

    context = browser.new_context(locale="en-US", viewport={"width": 1440, "height": 1000})
    # Deliberately supply a preference fixture over this disposable HTTP server.
    # Secure-cookie transport/creation is covered by the separate TLS tests.
    cookie_value = base64.b64encode(json.dumps({"lang": "it"}).encode()).decode()
    context.add_cookies([{"name": "entropy_prefs", "value": cookie_value, "url": base, "httpOnly": True, "secure": False, "sameSite": "Lax"}])
    dictionary_catalog = context.request.get(base + "/wordlists.json").json()
    dictionaries = {entry["code"]: entry for entry in dictionary_catalog["languages"]}
    assert dictionary_catalog["selected"] == "it", dictionary_catalog
    phrase = "<b>wire-word</b> русский ελληνικά"
    digest = "ab"*32
    endpoints = []
    def word_stream(route):
        endpoints.append(route.url)
        language = parse_qs(urlsplit(route.url).query)["lang"][0]
        route.send(json.dumps({"words": phrase, "hash": digest, "language": language, "wordlist": dictionaries[language]["id"]}))
    context.route_web_socket("**/words?*", word_stream)
    page = context.new_page()
    errors = []
    page.on("pageerror", lambda error: errors.append(str(error)))
    try:
        checkpoint(artifacts, "word-data-cookie")
        page.goto(base + "/api-words.html?words=3&refresh=1000")
        expect(page.locator("#word-language")).to_have_value("it")
        expect(page.locator("#words-content")).to_have_text(phrase)
        options = page.locator("#word-language").evaluate("e=>[...e.options].map(option=>({value:option.value,text:option.textContent}))")
        before_cookie = context.cookies(base)
        for language in ("fr", "de", "ru", "el", "en"):
            choose_language(page, language)
            expect(page.locator("#words-content")).to_have_text(phrase)
            expect(page.locator("#hash")).to_have_text(digest)
            expect(page.locator("#word-language")).to_have_value("it")
            expect(page.locator("#words-content")).to_have_attribute("lang", "it")
            assert page.locator("#words-content b").count() == 0
            assert dictionaries["it"]["title"] in page.locator("#wordlist-info").inner_text()
            assert page.locator("#word-language").evaluate("e=>[...e.options].map(option=>({value:option.value,text:option.textContent}))") == options
            assert context.cookies(base) == before_cookie
            assert len(endpoints) == 1, "UI language selection reconnected or changed the word stream"
        page.goto(base + "/api-words.html?words=3&refresh=1000&lang=ru")
        expect(page.locator("#word-language")).to_have_value("ru")
        expect(page.locator("#words-content")).to_have_text(phrase)
        choose_language(page, "fr")
        expect(page.locator("#word-language")).to_have_value("ru")
        assert parse_qs(urlsplit(page.url).query)["lang"] == ["ru"]
        assert parse_qs(urlsplit(endpoints[-1]).query)["lang"] == ["ru"]
        assert context.cookies(base) == before_cookie
        checkpoint(artifacts, "server-rendered-word-page")
        response = page.goto(base + "/v1/meta/random?words=3&bytes=64&lang=it")
        assert response.headers["content-language"] == "it"
        expect(page.locator("html")).to_have_attribute("lang", "fr")
        word_text = page.locator(".words").inner_text()
        word_hash = page.locator(".hash").inner_text()
        assert word_text and re.fullmatch(r"[0-9a-f]{64}", word_hash)
        description = page.locator("p[data-i18n-param-list]")
        description_key = description.get_attribute("data-i18n")
        parameters = {name: description.get_attribute("data-i18n-param-" + name) for name in ("list", "size", "bits")}
        for language in ("de", "el", "fr"):
            choose_language(page, language)
            expect(page.locator("h1")).to_have_text(expected(catalog, language, "Random Words"))
            expect(description).to_have_text(expected(catalog, language, description_key, parameters))
            expect(page.locator(".words")).to_have_text(word_text)
            expect(page.locator(".words")).to_have_attribute("lang", "it")
            expect(page.locator(".hash")).to_have_text(word_hash)
        assert not errors, errors
        return {"cookie_word_language": "it", "query_word_language": "ru", "ui_language": "fr", "phrase_preserved": phrase,
                "hash_preserved": digest, "dictionary_options_unchanged": True, "http_word_language": "it", "http_words_unchanged": True}
    finally:
        context.close()


def preference_checks(context, page, base, catalog, artifacts):
    from playwright.sync_api import expect

    page.goto(base + "/")
    choose_language(page, "fr")
    second = context.new_page()
    try:
        second.goto(base + "/")
        expect(second.locator("html")).to_have_attribute("lang", "fr")
        choose_language(second, "ru")
        expect(page.locator("html")).to_have_attribute("lang", "ru")
        expect(page.locator("#explore-title")).to_have_text(expected(catalog, "ru", "Explore in your browser"))
        page.bring_to_front()
        page.wait_for_function("!document.hidden")
        for event, language in (("focus", "it"), ("pageshow", "fr"), ("visibilitychange", "el")):
            checkpoint(artifacts, "preference-restoration", event=event, language=language)
            before = page.locator("html").get_attribute("lang")
            # Same-window writes do not send that window a storage event. This
            # models a missed/queued event without mutating the engine's DOM.
            page.evaluate("language=>localStorage.setItem('entropy-ui-language',language)", language)
            assert page.locator("html").get_attribute("lang") == before
            page.evaluate("""event=>{
              if(event==='visibilitychange')document.dispatchEvent(new Event(event));
              else window.dispatchEvent(event==='pageshow'?new PageTransitionEvent(event,{persisted:true}):new Event(event));
            }""", event)
            expect(page.locator("html")).to_have_attribute("lang", language)
            expect(page.locator("#explore-title")).to_have_text(expected(catalog, language, "Explore in your browser"))
        page.evaluate("""()=>{
          localStorage.setItem('entropy-ui-language','de');
          dispatchEvent(new StorageEvent('storage',{key:'entropy-ui-language',newValue:'fr'}));
        }""")
        expect(page.locator("html")).to_have_attribute("lang", "de")
        page.reload()
        expect(page.locator("select[data-ui-language]")).to_have_value("de")
        expect(page.locator("#explore-title")).to_have_text(expected(catalog, "de", "Explore in your browser"))
        page.evaluate("""()=>{
          localStorage.setItem('entropy-ui-language','unsupported');dispatchEvent(new Event('focus'));
        }""")
        expect(page.locator("html")).to_have_attribute("lang", "en")
        return {"cross_tab": True, "latest_storage_value": True, "focus_pageshow_visibility": True, "reload": True, "invalid_preference": "en"}
    finally:
        second.close()


def mobile_checks(page, base, catalog, artifacts):
    from playwright.sync_api import expect

    page.set_viewport_size({"width": 320, "height": 900})
    results = []
    for language in ("de", "ru", "el"):
        page.evaluate("language=>localStorage.setItem('entropy-ui-language',language)", language)
        for path, selector, key in PAGES:
            checkpoint(artifacts, "mobile-page", language=language, path=path)
            page.goto(base + path)
            expect(page.locator("html")).to_have_attribute("lang", language)
            assert_heading(page, selector, expected(catalog, language, key))
            assert_selector(page, catalog, language)
            assert not page.evaluate("document.documentElement.scrollWidth>innerWidth"), (language, path)
            settle_workers(page)
            if (language, path) in (("de", "/"), ("ru", "/api-sources.html"), ("el", "/api-gallery.html")):
                page.screenshot(path=str(artifacts / f"mobile-{language}-{path.strip('/').replace('.html','') or 'home'}.png"))
            results.append({"language": language, "path": path, "width": 320})
    page.set_viewport_size({"width": 1440, "height": 1000})
    return results


def run_browser(base, artifacts, engine, chromium):
    from playwright.sync_api import sync_playwright

    catalog = catalogs()
    with sync_playwright() as playwright:
        if engine == "firefox":
            browser = playwright.firefox.launch(headless=True, firefox_user_prefs={"gfx.webrender.software": True, "layers.acceleration.disabled": True})
        else:
            browser = playwright.chromium.launch(executable_path=chromium or None, headless=True, args=["--no-sandbox", "--disable-dev-shm-usage"])
        context = browser.new_context(locale="en-US", color_scheme="dark", viewport={"width": 1440, "height": 1000}, accept_downloads=True)
        context.add_init_script(OBSERVE_WORKSPACE)
        context.add_init_script(OBSERVE_JOBS)
        page = context.new_page()
        errors = []
        context.on("page", lambda new_page: new_page.on("pageerror", lambda error: errors.append(str(error))))
        page.on("pageerror", lambda error: errors.append(str(error)))
        failure = None
        try:
            results = {"choices": language_choices(page, base, catalog, artifacts), "pages": page_coverage(page, base, catalog, artifacts),
                       "frozen_data": frozen_data_checks(page, base, catalog, artifacts),
                       "words": word_data_checks(browser, base, catalog, artifacts),
                       "preferences": preference_checks(context, page, base, catalog, artifacts),
                       "mobile": mobile_checks(page, base, catalog, artifacts)}
            assert not errors, errors
            return {"browser": engine, **results, "browser_errors": errors}
        except Exception as error:
            failure = error
            report = {"type": type(error).__name__, "exception": str(error), "last_checkpoint": json.loads((artifacts / "progress.json").read_text())}
            try:
                if not page.is_closed() and browser.is_connected():
                    page.screenshot(path=str(artifacts / "failure.png"), timeout=3000)
            except Exception as screenshot_error:
                report["screenshot_error"] = str(screenshot_error)
            (artifacts / "failure.json").write_text(json.dumps(report, indent=2) + "\n")
            raise
        finally:
            cleanup_errors = []
            for close in (context.close, browser.close):
                try:
                    close()
                except Exception as error:
                    cleanup_errors.append(error)
            if cleanup_errors and failure is None:
                raise cleanup_errors[0]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", default="bin/entropy-server")
    parser.add_argument("--browser", choices=["chromium", "firefox"], default="chromium")
    parser.add_argument("--chromium", default="")
    parser.add_argument("--artifacts", default="browser-artifacts/ui-languages")
    args = parser.parse_args()
    artifacts = Path(args.artifacts).resolve()
    artifacts.mkdir(parents=True, exist_ok=True)
    process = None
    with (artifacts / "server.log").open("w") as log:
        try:
            port = free_port()
            process = serve_process(Path(args.binary).resolve(), port, log, extra=("-cookie=true", "-refresh=60000"))
            results = run_browser(f"http://127.0.0.1:{port}", artifacts, args.browser, args.chromium)
            (artifacts / "results.json").write_text(json.dumps(results, indent=2, ensure_ascii=False) + "\n")
            print(f"{args.browser}: nine UI languages, main pages, payload/dictionary isolation, preferences and mobile layouts passed")
        finally:
            if process and process.poll() is None:
                process.send_signal(signal.SIGINT)
                try:
                    process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)


if __name__ == "__main__":
    main()
