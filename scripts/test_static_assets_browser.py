#!/usr/bin/env python3
"""Focused browser proof for static compression, catalog caching and route isolation."""
import argparse
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
from test_ui_languages_browser import OBSERVE_JOBS, choose_language, settle_workers


ROOT = Path(__file__).resolve().parents[1]
HEADER_NAMES = ("cache-control", "content-encoding", "content-length", "content-type", "etag", "vary")


def response_evidence(response):
    return {"url": response.url, "status": response.status,
            "headers": {key: value for key, value in response.headers.items() if key in HEADER_NAMES}}


class NetworkEvidence:
    """Keep real cache behavior: request routing would disable browser HTTP caching."""
    def __init__(self, context, page, engine):
        self.rows = {}
        self.responses = []
        page.on("response", lambda response: self.responses.append(response_evidence(response)))
        if engine == "chromium":
            session = context.new_cdp_session(page)
            session.send("Network.enable")
            session.on("Network.requestWillBeSent", self.request)
            session.on("Network.responseReceived", self.response)
            session.on("Network.responseReceivedExtraInfo", self.extra)
            session.on("Network.loadingFinished", self.finished)

    def clear(self):
        self.rows.clear()
        self.responses.clear()

    def request(self, event):
        self.rows[event["requestId"]] = {"url": event["request"]["url"]}

    def response(self, event):
        response = event["response"]
        self.rows.setdefault(event["requestId"], {}).update(
            url=response["url"], status=response["status"], fromDiskCache=response.get("fromDiskCache", False))

    def extra(self, event):
        self.rows.setdefault(event["requestId"], {})["wireStatus"] = event["statusCode"]

    def finished(self, event):
        self.rows.setdefault(event["requestId"], {})["encodedTransferBytes"] = event["encodedDataLength"]

    def catalog(self, page, language):
        path = f"/locales/{language}.json"
        entries = page.evaluate("""path=>performance.getEntriesByType('resource')
          .filter(entry=>new URL(entry.name).pathname===path).map(entry=>({
            url:entry.name,encodedBodySize:entry.encodedBodySize,decodedBodySize:entry.decodedBodySize,
            transferSize:entry.transferSize,duration:entry.duration}))""", path)
        assert len(entries) == 1, entries
        result = entries[0]
        result["responses"] = [row for row in self.responses if urlsplit(row["url"]).path == path]
        result["network"] = [row for row in self.rows.values() if urlsplit(row.get("url", "")).path == path]
        return result


def new_context(browser, language):
    context = browser.new_context(locale="en-US", color_scheme="dark", viewport={"width": 1440, "height": 1000})
    context.add_init_script(OBSERVE_WORKSPACE)
    context.add_init_script(OBSERVE_JOBS)
    # Seed a saved choice once before the first application script; subsequent
    # documents must read the real persisted preference. No request interception
    # or synthetic cache participates in these navigation cases.
    context.add_init_script("try{if(!sessionStorage.getItem('static-asset-test-seeded')){"
                            "localStorage.setItem('entropy-ui-language'," + json.dumps(language) + ");"
                            "sessionStorage.setItem('static-asset-test-seeded','1');}}catch{}")
    return context


def open_translated(page, url, path, french, reload=False):
    from playwright.sync_api import expect

    if reload:
        page.reload(wait_until="load")
    else:
        page.goto(url + path, wait_until="load")
    expect(page.locator("html")).to_have_attribute("lang", "fr")
    expect(page.locator("select[data-ui-language]")).to_have_value("fr")
    if path == "/":
        expect(page.locator("#explore-title")).to_have_text(french["Explore in your browser"])
    else:
        expect(page.locator("h1")).to_have_text(french["File Analysis"])
    wait_paint(page)
    settle_workers(page)


def navigation_checks(browser, base, engine, french, artifacts, optimized=True):
    context = new_context(browser, "fr")
    page = context.new_page()
    evidence = NetworkEvidence(context, page, engine)
    errors = []
    page.on("pageerror", lambda error: errors.append(str(error)))
    rows = []
    try:
        for mode, path, reload in (("cold", "/", False), ("cross-page", "/api-upload.html", False),
                                   ("return", "/", False), ("reload", "/", True)):
            checkpoint(artifacts, "optimized-navigation" if optimized else "baseline-navigation", mode=mode)
            evidence.clear()
            open_translated(page, base, path, french, reload)
            row = evidence.catalog(page, "fr")
            row.update(mode=mode, page=path)
            rows.append(row)
            evidence_name = "optimized-navigation.json" if optimized else "historical-navigation.json"
            (artifacts / evidence_name).write_text(json.dumps(rows, indent=2) + "\n")
            if optimized:
                version = parse_qs(urlsplit(row["url"]).query).get("v", [])
                assert len(version) == 1 and re.fullmatch(r"[0-9a-f]+", version[0]), row
                assert row["decodedBodySize"] > 0, row
                assert 0 < row["encodedBodySize"] < row["decodedBodySize"] / 2, row
                if mode == "cold":
                    assert row["transferSize"] > row["encodedBodySize"], row
                    response = next(item for item in row["responses"] if item["status"] == 200)
                    assert response["headers"].get("content-encoding") == "gzip", row
                    assert "immutable" in response["headers"].get("cache-control", ""), row
                else:
                    # A browser can use its fresh cache or revalidate on an
                    # explicit reload. Either path must avoid another body.
                    assert row["transferSize"] <= 1024, row
                    assert not any(item.get("wireStatus") == 200 and item.get("encodedTransferBytes", 0) > 1024
                                   for item in row["network"]), row
        assert not errors, errors
        return rows
    finally:
        context.close()


def http_contract_checks(context, base, catalog_url):
    """Use actual deployed HTTP handlers, independent of the browser cache."""
    path = urlsplit(catalog_url).path
    identity = context.request.get(catalog_url, headers={"Accept-Encoding": "identity"})
    compressed = context.request.get(catalog_url, headers={"Accept-Encoding": "gzip"})
    assert identity.status == compressed.status == 200
    assert not identity.headers.get("content-encoding")
    assert compressed.headers.get("content-encoding") == "gzip"
    assert "accept-encoding" in compressed.headers.get("vary", "").lower()
    assert identity.body() == compressed.body(), "Decoded gzip must equal the identity representation"
    catalog = identity.json()
    assert catalog["Interface language"] and catalog["Explore in your browser"]
    assert int(compressed.headers["content-length"]) < len(identity.body()) / 2
    identity_tag, gzip_tag = identity.headers["etag"], compressed.headers["etag"]
    assert identity_tag != gzip_tag, "Validators must identify the selected wire representation"
    conditionals = []
    for encoding, tag in (("identity", identity_tag), ("gzip", gzip_tag)):
        response = context.request.get(catalog_url, headers={"Accept-Encoding": encoding, "If-None-Match": tag})
        assert response.status == 304 and not response.body(), response_evidence(response)
        conditionals.append(response_evidence(response))
    stale = context.request.get(catalog_url, headers={"Accept-Encoding": "gzip", "If-None-Match": '"different-content"'})
    assert stale.status == 200 and stale.json() == catalog
    wrong_representation = context.request.get(catalog_url, headers={"Accept-Encoding": "gzip", "If-None-Match": identity_tag})
    assert wrong_representation.status == 200 and wrong_representation.json() == catalog
    disabled_gzip = context.request.get(catalog_url, headers={"Accept-Encoding": "gzip;q=0, identity;q=1"})
    assert disabled_gzip.status == 200 and not disabled_gzip.headers.get("content-encoding")
    assert disabled_gzip.json() == catalog
    freshness = []
    for suffix in ("", "?v=not-the-current-version"):
        response = context.request.get(base + path + suffix, headers={"Accept-Encoding": "gzip"})
        assert response.status == 200 and response.json() == catalog
        control = response.headers.get("cache-control", "")
        assert "no-cache" in control and "immutable" not in control, response_evidence(response)
        freshness.append(response_evidence(response))
    runtime = context.request.get(base + "/i18n.js", headers={"Accept-Encoding": "identity"})
    assert runtime.status == 200 and "no-cache" in runtime.headers.get("cache-control", "")
    assert "__ENTROPY_UI_CATALOG_VERSION__" not in runtime.text(), "Served runtime must carry this build's catalog digest"
    return {"identity": response_evidence(identity), "gzip": response_evidence(compressed),
            "uncompressedReferenceBodyBytes": len(identity.body()), "gzipBodyBytes": int(compressed.headers["content-length"]),
            "conditionals": conditionals, "staleValidator": response_evidence(stale),
            "wrongRepresentationValidator": response_evidence(wrong_representation), "unversionedOrWrongVersion": freshness}


def dynamic_route_checks(context, base, static_tag):
    rows = []
    for path in ("/v1/data/random?bytes=4096", "/v1/data/test?bytes=4096", "/v1/meta/random?words=3"):
        response = context.request.get(base + path, headers={"Accept-Encoding": "gzip", "If-None-Match": static_tag})
        assert response.status == 200, response_evidence(response)
        assert "no-store" in response.headers.get("cache-control", ""), response_evidence(response)
        assert not response.headers.get("etag") and not response.headers.get("content-encoding"), response_evidence(response)
        if path.startswith(("/v1/data/random", "/v1/data/test")):
            assert len(response.body()) == 4096, (path, len(response.body()))
        rows.append({**response_evidence(response), "decodedBytes": len(response.body())})
    metadata = context.request.get(base + "/wordlists.json", headers={"Accept-Encoding": "gzip", "If-None-Match": static_tag})
    assert metadata.status == 200 and metadata.headers.get("cache-control") == "no-cache"
    assert metadata.headers.get("etag") and not metadata.headers.get("content-encoding")
    assert "immutable" not in metadata.headers["cache-control"]
    return {"generation": rows, "publicWordlistMetadata": response_evidence(metadata)}


def greek_representation_checks(context, french_url):
    url = urlsplit(french_url)._replace(path="/locales/el.json").geturl()
    identity = context.request.get(url, headers={"Accept-Encoding": "identity"})
    compressed = context.request.get(url, headers={"Accept-Encoding": "gzip"})
    assert identity.status == compressed.status == 200
    assert identity.json() == compressed.json()
    assert not identity.headers.get("content-encoding") and compressed.headers.get("content-encoding") == "gzip"
    assert "immutable" in compressed.headers.get("cache-control", "")
    assert 0 < int(compressed.headers["content-length"]) < len(identity.body()) / 2
    return {"url": url, "identityBodyBytes": len(identity.body()), "gzipBodyBytes": int(compressed.headers["content-length"]),
            "identity": response_evidence(identity), "gzip": response_evidence(compressed)}


def english_and_frozen_checks(browser, base, french, artifacts):
    from playwright.sync_api import expect

    context = new_context(browser, "en")
    page = context.new_page()
    errors = []
    page.on("pageerror", lambda error: errors.append(str(error)))
    try:
        page.goto(base + "/api-upload.html", wait_until="load")
        expect(page.locator("html")).to_have_attribute("lang", "en")
        expect(page.locator("h1")).to_have_text("File Analysis")
        settle_workers(page)
        assert not page.evaluate("performance.getEntriesByType('resource').some(e=>new URL(e.name).pathname.startsWith('/locales/'))")
        payload = bytes(range(256)) * 32
        page.locator("#fileInput").set_input_files({"name": "public-cache-fixture.bin", "mimeType": "application/octet-stream", "buffer": payload})
        with page.expect_response(lambda response: urlsplit(response.url).path == "/files" and response.request.method == "POST") as upload:
            page.locator("#submit-upload").click()
        assert "no-store" in upload.value.headers.get("cache-control", "")
        assert not upload.value.headers.get("content-encoding") and not upload.value.headers.get("etag")
        page.wait_for_function("Boolean(window.__diagnosticWorkspace?.sample)")
        wait_paint(page)
        digest = selected_hash(page)
        assert digest == hashlib.sha256(payload).hexdigest()
        baseline = page.evaluate("""()=>({jobs:__languageWorkerJobs,frame:__diagnosticWorkspace.sample.frame,
          entropy:__diagnosticWorkspace.sample.entropy,histogram:[...__diagnosticWorkspace.sample.histogram]})""")
        for language in ("fr", "el", "en", "fr"):
            choose_language(page, language)
            current = page.evaluate("""()=>({jobs:__languageWorkerJobs,frame:__diagnosticWorkspace.sample.frame,
              entropy:__diagnosticWorkspace.sample.entropy,histogram:[...__diagnosticWorkspace.sample.histogram]})""")
            assert current == baseline, (language, "Static delivery changed analysis or scheduled another worker job")
            assert selected_hash(page) == digest
            expect(page.locator("#file-sha256")).to_have_text(digest)
        expect(page.locator("#upload-status")).to_have_text(french["Analysis complete"])
        assert not errors, errors
        page.screenshot(path=str(artifacts / "cached-translated-file.png"))
        return {"englishCatalogRequests": 0, "sha256": digest, "workerJobs": baseline["jobs"], "frame": baseline["frame"], "languageSwitches": 4}
    finally:
        context.close()


def run_browser(base, artifacts, engine, chromium, baseline_base=None):
    from playwright.sync_api import sync_playwright

    french = json.loads((ROOT / "cmd/entropy-server/web/locales/fr.json").read_text())
    with sync_playwright() as playwright:
        if engine == "firefox":
            browser = playwright.firefox.launch(headless=True, firefox_user_prefs={"gfx.webrender.software": True, "layers.acceleration.disabled": True})
        else:
            browser = playwright.chromium.launch(executable_path=chromium or None, headless=True, args=["--no-sandbox", "--disable-dev-shm-usage"])
        try:
            historical = navigation_checks(browser, baseline_base, engine, french, artifacts, optimized=False) if baseline_base else None
            navigations = navigation_checks(browser, base, engine, french, artifacts)
            api_context = browser.new_context()
            try:
                contracts = http_contract_checks(api_context, base, navigations[0]["url"])
                dynamic = dynamic_route_checks(api_context, base, contracts["gzip"]["headers"]["etag"])
                greek = greek_representation_checks(api_context, navigations[0]["url"])
            finally:
                api_context.close()
            frozen = english_and_frozen_checks(browser, base, french, artifacts)
            return {"browser": engine, "browserVersion": browser.version, "historicalBaselineNavigations": historical,
                    "optimizedNavigations": navigations, "representationContracts": contracts, "greekRepresentation": greek, "dynamicRoutes": dynamic,
                    "frozenData": frozen,
                    "comparison": {"reference": "Uncompressed identity catalog; optional historical binary when supplied",
                                   "uncompressedCatalogBodyBytes": contracts["uncompressedReferenceBodyBytes"],
                                   "coldGzipCatalogBodyBytes": contracts["gzipBodyBytes"],
                                   "repeatTransferSizes": [row["transferSize"] for row in navigations[1:]]}}
        finally:
            browser.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", default="bin/entropy-server")
    parser.add_argument("--baseline-binary", help="Optional pre-fix executable for historical transfer evidence")
    parser.add_argument("--browser", choices=["chromium", "firefox"], default="chromium")
    parser.add_argument("--chromium", default="")
    parser.add_argument("--artifacts", default="browser-artifacts/static-assets")
    args = parser.parse_args()
    artifacts = Path(args.artifacts).resolve()
    artifacts.mkdir(parents=True, exist_ok=True)
    (artifacts / "failure.json").unlink(missing_ok=True)
    processes = []
    logs = []
    try:
        def start(binary, name):
            port = free_port()
            log = (artifacts / name).open("w")
            logs.append(log)
            processes.append(serve_process(Path(binary).resolve(), port, log, extra=("-refresh=60000",)))
            return f"http://127.0.0.1:{port}"
        base = start(args.binary, "server.log")
        baseline = start(args.baseline_binary, "baseline-server.log") if args.baseline_binary else None
        results = run_browser(base, artifacts, args.browser, args.chromium, baseline)
        (artifacts / "results.json").write_text(json.dumps(results, indent=2, ensure_ascii=False) + "\n")
        print(f"{args.browser}: compressed/versioned assets, repeat-navigation cache, validators, readable identity, raw routes and frozen data passed")
    except Exception as error:
        (artifacts / "failure.json").write_text(json.dumps({"type": type(error).__name__, "exception": str(error)}, indent=2) + "\n")
        raise
    finally:
        for process in reversed(processes):
            if process.poll() is None:
                process.send_signal(signal.SIGINT)
                try:
                    process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)
        for log in logs:
            log.close()


if __name__ == "__main__":
    main()
