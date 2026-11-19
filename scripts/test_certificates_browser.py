#!/usr/bin/env python3
"""Real browser certificate downloads, fail-closed acquisition and Go TLS interoperability.

Private material exists only in a TemporaryDirectory, never browser evidence.
"""
import argparse
import json
import os
from pathlib import Path
import socket
import ssl
import subprocess
import tempfile
from urllib.parse import parse_qs, unquote, urlsplit
import urllib.request

from test_sources_browser import free_port, serve_process

ROOT = Path(__file__).resolve().parents[1]
ENTROPY = "**/v1/data/random?bytes=64"
COOKIE = "certificate_test_preference"
ALLOWED_PATHS = {
    "/", "/api-certificates.html", "/i18n.js", "/theme.js", "/theme.css", "/certificates.css",
    "/certificate-crypto.js", "/certificates.js", "/v1/data/random", "/locales/fr.json", "/favicon.ico",
}
VERIFY_SOURCE = r'''package main

import (
    "crypto/ecdsa"
    "crypto/elliptic"
    "crypto/tls"
    "crypto/x509"
    "os"
    "time"
)

func require(condition bool, message string) {
    if !condition { panic(message) }
}

func main() {
    require(len(os.Args) == 3, "certificate and key paths required")
    pair, err := tls.LoadX509KeyPair(os.Args[1], os.Args[2])
    require(err == nil && len(pair.Certificate) == 1, "Go could not parse and match one certificate/key pair")
    certificate, err := x509.ParseCertificate(pair.Certificate[0])
    require(err == nil, "Go could not parse the certificate")
    require(certificate.CheckSignature(certificate.SignatureAlgorithm, certificate.RawTBSCertificate,
        certificate.Signature) == nil, "Self-signature verification failed")
    require(certificate.Version == 3 && certificate.SignatureAlgorithm == x509.ECDSAWithSHA256,
        "Expected X.509 v3 ECDSA/SHA-256")
    public, ok := certificate.PublicKey.(*ecdsa.PublicKey)
    require(ok && public.Curve == elliptic.P256(), "Expected P-256 public key")
    require(certificate.BasicConstraintsValid && !certificate.IsCA, "Certificate must be a non-CA leaf")
    require(certificate.KeyUsage == x509.KeyUsageDigitalSignature, "Only digital-signature key usage is allowed")
    require(len(certificate.ExtKeyUsage) == 1 && certificate.ExtKeyUsage[0] == x509.ExtKeyUsageServerAuth &&
        len(certificate.UnknownExtKeyUsage) == 0, "Only TLS server authentication is allowed")
    require(certificate.SerialNumber.Sign() > 0 && certificate.SerialNumber.BitLen() <= 159, "Invalid serial")
    require(len(certificate.UnhandledCriticalExtensions) == 0, "Unhandled critical extension")
    require(len(certificate.DNSNames) == 1 && len(certificate.IPAddresses) == 2, "Unexpected SAN count")
    for _, name := range []string{"localhost", "127.0.0.1", "::1"} {
        require(certificate.VerifyHostname(name) == nil, "Expected DNS/IP SAN is absent")
    }
    require(certificate.VerifyHostname("unlisted.invalid") != nil, "Missing SAN was accepted")
    now := time.Now()
    require(certificate.NotBefore.Before(now) && certificate.NotAfter.After(now), "Certificate is not currently valid")
    require(certificate.NotAfter.Sub(certificate.NotBefore) == 7*24*time.Hour+5*time.Minute,
        "Expected seven days of validity plus five minutes of clock skew")
}
'''


def stop(process):
    process.terminate()
    try:
        process.wait(timeout=10)
    finally:
        if process.poll() is None:
            process.kill()
            process.wait(timeout=5)


def go_tls(binary, directory, certificate, key):
    """tls.LoadX509KeyPair parses and matches the key; a separate client verifies trust/SANs."""
    cert_path, key_path = directory / "cert.pem", directory / "key.pem"
    cert_path.write_bytes(certificate)
    descriptor = os.open(key_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as output:
        output.write(key)
    verifier = directory / "verify.go"
    verifier.write_text(VERIFY_SOURCE)
    result = subprocess.run(["go", "run", str(verifier), str(cert_path), str(key_path)],
                            capture_output=True, text=True, timeout=60, check=False)
    # Diagnostics contain parser/assertion details only, never certificate/key bytes.
    assert result.returncode == 0, "Independent Go X.509 validation failed: " + result.stderr[-2000:]
    port = free_port()
    with (directory / "tls-server.log").open("w") as log:
        process = serve_process(binary, free_port(), log, (
            "-enable-https=true", f"-https=127.0.0.1:{port}",
            f"-cert-file={cert_path}", f"-key-file={key_path}"))
        try:
            trust = ssl.create_default_context(cadata=certificate.decode())
            trust.minimum_version = ssl.TLSVersion.TLSv1_3
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPSHandler(context=trust))
            with opener.open(f"https://127.0.0.1:{port}/live", timeout=5) as response:
                assert response.status == 200
            # A trusted self-signed certificate must still reject a missing SAN.
            with socket.create_connection(("127.0.0.1", port), timeout=5) as connection:
                try:
                    trust.wrap_socket(connection, server_hostname="unlisted.invalid")
                except ssl.SSLCertVerificationError:
                    pass
                else:
                    raise AssertionError("TLS accepted a hostname absent from the certificate SANs")
        finally:
            stop(process)


def run(browser, base, binary, directory, artifacts):
    from playwright.sync_api import expect

    context = browser.new_context(accept_downloads=True, viewport={"width": 1440, "height": 1000}, locale="en-US")
    context.add_cookies([{"name": COOKIE, "value": "present", "url": base + "/", "sameSite": "Lax"}])
    page = context.new_page()
    errors, requests = [], []
    page.on("pageerror", lambda error: errors.append(str(error)))
    page.on("request", lambda request: requests.append(request))
    try:
        response = page.goto(base + "/api-certificates.html")
        headers = response.all_headers()
        policy = headers.get("content-security-policy", "")
        for directive in ("default-src 'self'", "script-src 'self'", "connect-src 'self'", "object-src 'none'",
                          "base-uri 'none'", "form-action 'none'", "frame-ancestors 'none'"):
            assert directive in policy, "Missing certificate document security directive: " + directive
        assert "unsafe-inline" not in policy and "unsafe-eval" not in policy
        assert headers.get("x-frame-options") == "DENY"
        assert headers.get("referrer-policy") == "no-referrer"
        assert headers.get("x-content-type-options") == "nosniff"
        assert COOKIE + "=present" in page.evaluate("document.cookie"), "Cookie-omission fixture was not installed"
        expect(page.locator("#certificate-result")).to_be_hidden()
        assert not any("/v1/data/random" in row.url for row in requests), "Generation must require a user action"
        if browser.browser_type.name == "chromium":
            # Firefox naturally needs public-point recovery. Force that same
            # path through Chromium's independent native provider as well.
            page.evaluate("""()=>{const original=crypto.subtle.exportKey.bind(crypto.subtle);
              window.restoreCertificateExport=()=>{crypto.subtle.exportKey=original;};
              crypto.subtle.exportKey=(format,key)=>{
                if(format==='jwk'&&key.type==='private')throw new DOMException('Public point unavailable','OperationError');
                return original(format,key);};}""")
        page.locator("#certificate-generate").click()
        expect(page.locator("#certificate-result")).to_be_visible(timeout=15000)
        expect(page.locator("#certificate-status")).to_have_attribute("data-state", "ready")
        fingerprint = page.locator("#certificate-result-fingerprint").inner_text()
        assert len(fingerprint) == 95
        assert "-----BEGIN PRIVATE KEY-----" not in page.content(), "Private material must not appear in the DOM"
        for identifier in ("public", "key"):
            with page.expect_download() as download:
                page.locator("#certificate-download-" + identifier).click()
            # Never save these files under the uploaded artifacts directory.
            content = download.value.path().read_bytes()
            if identifier == "public":
                certificate = content
                assert content.decode() == page.locator("#certificate-pem").input_value()
            else:
                key = content
                assert b"-----BEGIN PRIVATE KEY-----" in key and len(key) < 4096
        private_body = "".join(line for line in key.decode().splitlines() if not line.startswith("-----"))
        private_tokens = (key.decode(), private_body)
        immediate = page.content() + page.evaluate("()=>JSON.stringify([Object.entries(localStorage),Object.entries(sessionStorage),document.cookie])")
        assert all(token not in immediate for token in private_tokens), "Private export was persisted in the page"
        go_tls(binary, directory, certificate, key)
        if browser.browser_type.name == "chromium":
            page.evaluate("()=>{restoreCertificateExport();}")
        # Changing the interface language must not regenerate a key or translate
        # its public certificate, names or fingerprint.
        french = json.loads((ROOT / "cmd/entropy-server/web/locales/fr.json").read_text())
        page.locator("select[data-ui-language]").select_option("fr")
        expect(page.locator("html")).to_have_attribute("lang", "fr")
        expect(page.locator("h1")).to_have_text(french["Certificate tools"])
        expect(page.locator("#certificate-download-key")).to_have_text(french["Download private key"])
        expect(page.locator("#certificate-result-fingerprint")).to_have_text(fingerprint)
        expect(page.locator("#certificate-pem")).to_have_value(certificate.decode())
        page.locator("select[data-ui-language]").select_option("en")
        expect(page.locator("html")).to_have_attribute("lang", "en")
        page.locator("#certificate-generate").click()
        expect(page.locator("#certificate-result")).to_be_visible()
        expect(page.locator("#certificate-result-fingerprint")).not_to_have_text(fingerprint)
        for width in (390, 1440):
            page.set_viewport_size({"width": width, "height": 1000})
            for theme in ("light", "dark"):
                if page.locator("html").get_attribute("data-theme") != theme:
                    page.locator("[data-theme-toggle]").click()
                expect(page.locator("html")).to_have_attribute("data-theme", theme)
                assert not page.evaluate("document.documentElement.scrollWidth>innerWidth"), (width, theme)
                page.screenshot(path=str(artifacts / f"certificates-{width}-{theme}.png"), full_page=True)
        page.locator("#certificate-clear").click()
        expect(page.locator("#certificate-result")).to_be_hidden()
        expect(page.locator("#certificate-pem")).to_have_value("")

        # Neither a failed request nor a short/oversized body may select a key.
        for status, body in ((503, bytes(64)), (200, bytes(63)), (200, bytes(65))):
            page.route(ENTROPY, lambda route, _request, status=status, body=body: route.fulfill(status=status, body=body))
            page.locator("#certificate-generate").click()
            expect(page.locator("#certificate-status")).to_have_attribute("data-state", "error")
            expect(page.locator("#certificate-result")).to_be_hidden()
            expect(page.locator("#certificate-generate")).to_be_enabled()
            page.unroute(ENTROPY)

        # Clear invalidates late native crypto completion as well as network work.
        page.evaluate("""()=>{window.originalCertificateSign=crypto.subtle.sign.bind(crypto.subtle);
          window.originalCertificateDigest=crypto.subtle.digest.bind(crypto.subtle);
          crypto.subtle.digest=async(...args)=>{const result=await originalCertificateDigest(...args);
            document.documentElement.dataset.certificateTestDigest='done';return result;};
          let blocked=false;
          crypto.subtle.sign=async(...args)=>{const result=await originalCertificateSign(...args);
            if(!blocked){blocked=true;await new Promise(resolve=>{window.releaseCertificateTest=resolve;
              document.documentElement.dataset.certificateTestSigning='ready';});}return result;};}""")
        page.locator("#certificate-generate").click()
        expect(page.locator("html")).to_have_attribute("data-certificate-test-signing", "ready")
        page.locator("#certificate-cancel").click()
        page.evaluate("releaseCertificateTest()")
        expect(page.locator("html")).to_have_attribute("data-certificate-test-digest", "done")
        expect(page.locator("#certificate-result")).to_be_hidden()
        expect(page.locator("#certificate-generate")).to_be_enabled()
        page.evaluate("()=>{crypto.subtle.sign=originalCertificateSign;crypto.subtle.digest=originalCertificateDigest;}")

        # Network cancellation is a separate failure path from an eventual key.
        held = []
        page.route(ENTROPY, lambda route: held.append(route))
        with page.expect_request(ENTROPY):
            page.locator("#certificate-generate").click()
        expect(page.locator("#certificate-cancel")).to_be_enabled()
        with page.expect_event("requestfailed", predicate=lambda request: request.url == base + "/v1/data/random?bytes=64"):
            page.locator("#certificate-cancel").click()
        expect(page.locator("#certificate-result")).to_be_hidden()
        page.unroute(ENTROPY)
        for route in held:
            # The browser already aborted the network request. Finish only the
            # Playwright handler, without attempting a second protocol abort.
            route.fallback()
        # A stalled body must hit the documented acquisition deadline without
        # waiting ten wall-clock seconds or creating a fallback key.
        held.clear()
        page.clock.install()
        page.route(ENTROPY, lambda route: held.append(route))
        with page.expect_request(ENTROPY):
            page.locator("#certificate-generate").click()
        with page.expect_event("requestfailed", predicate=lambda request: request.url == base + "/v1/data/random?bytes=64"):
            page.clock.fast_forward(10001)
        expect(page.locator("#certificate-status")).to_have_attribute("data-state", "error")
        expect(page.locator("#certificate-status")).to_have_text("Generation cancelled or the entropy request timed out.")
        expect(page.locator("#certificate-result")).to_be_hidden()
        expect(page.locator("#certificate-generate")).to_be_enabled()
        page.unroute(ENTROPY)
        for route in held:
            route.fallback()
        page.clock.resume()
        # Navigating away and back with a live key cannot restore it.
        page.locator("#certificate-generate").click()
        expect(page.locator("#certificate-result")).to_be_visible()
        page.goto(base + "/")
        page.go_back()
        expect(page.locator("#certificate-result")).to_be_hidden()
        storage = page.evaluate("()=>Object.fromEntries([...Object.entries(localStorage),...Object.entries(sessionStorage)])")
        assert all(value in ("entropy-theme", "entropy-ui-language", "entropy-graph-palette") for value in storage), "Unexpected persisted state"
        entropy_requests = [row for row in requests if "/v1/data/random" in row.url]
        assert entropy_requests and all(row.method == "GET" and row.post_data is None for row in requests), "Private material was uploaded"
        origin = urlsplit(base)
        for row in requests:
            parsed = urlsplit(row.url)
            assert (parsed.scheme, parsed.netloc) == (origin.scheme, origin.netloc), "Unexpected off-origin request"
            assert parsed.path in ALLOWED_PATHS, "Unexpected certificate page network destination"
            query = parse_qs(parsed.query, keep_blank_values=True)
            if parsed.path == "/v1/data/random":
                assert query == {"bytes": ["64"]}, "Entropy request parameters changed"
                assert "cookie" not in row.all_headers(), "Entropy acquisition sent preference cookies"
            elif parsed.path == "/locales/fr.json":
                assert set(query) == {"v"} and len(query["v"]) == 1 and len(query["v"][0]) == 64, "Unexpected catalog query"
            else:
                assert not parsed.query, "Unexpected private data in an asset URL"
            assert not parsed.fragment, "Unexpected request URL fragment"
        visible_and_persisted = page.content() + json.dumps(storage) + page.evaluate(
            "()=>document.cookie+[...document.querySelectorAll('input,textarea')].map(node=>node.value).join('\\n')")
        network_values = "\n".join(unquote(row.url) + json.dumps(row.all_headers()) for row in requests)
        assert all(token not in visible_and_persisted and token not in network_values for token in private_tokens), "Private key escaped its explicit download"
        assert not errors, errors
        (artifacts / "qualification.json").write_text(json.dumps({
            "native_p256_import": True, "self_signed_go_tls": True,
            "native_compatibility_forced_in_chromium": browser.browser_type.name == "chromium",
            "go_self_signature_usage_and_sans": True, "missing_san_rejected": True,
            "private_upload_requests": 0, "entropy_cookie_omitted": True,
            "security_headers": True, "language_preserves_certificate": True,
            "failure_bounds_timeout_and_cancellation": True, "private_artifacts": False}, indent=2) + "\n")
    finally:
        context.close()


def main():
    from playwright.sync_api import sync_playwright

    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--browser", choices=("chromium", "firefox"), default="chromium")
    parser.add_argument("--server", type=Path, default=ROOT / "bin/entropy-server")
    parser.add_argument("--artifacts", type=Path, default=ROOT / "browser-artifacts/certificates")
    args = parser.parse_args()
    args.artifacts.mkdir(parents=True, exist_ok=True)
    port = free_port()
    with tempfile.TemporaryDirectory(prefix="entropy-certificate-test-") as temporary:
        with (args.artifacts / "server.log").open("w") as log:
            process = serve_process(args.server.resolve(), port, log)
            try:
                with sync_playwright() as playwright:
                    engine = getattr(playwright, args.browser)
                    launch = {"headless": True}
                    if args.browser == "firefox":
                        launch["firefox_user_prefs"] = {"gfx.webrender.software": True, "layers.acceleration.disabled": True}
                    browser = engine.launch(**launch)
                    try:
                        run(browser, f"http://127.0.0.1:{port}", args.server.resolve(), Path(temporary), args.artifacts)
                    finally:
                        browser.close()
            finally:
                stop(process)
    print(f"{args.browser}: browser-local certificate generation, cancellation and Go TLS passed")


if __name__ == "__main__":
    main()
