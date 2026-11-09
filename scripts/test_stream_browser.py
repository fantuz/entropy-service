#!/usr/bin/env python3
"""Optional real Chromium smoke/regression tests; see docs/browser-ui.md.

Uses a temporary local entropy-server, never an existing server or research data.
Requires Python Playwright and Chromium, in addition to the built server binary.
"""
import argparse
import json
from pathlib import Path
import re
import signal
import socket
import subprocess
import time
import urllib.request


def number(page, element):
    return int(re.sub(r"\D", "", page.locator("#" + element).inner_text()))


def set_value(page, element, value, event="change"):
    page.locator("#" + element).evaluate(
        "(element, args) => {element.value=args[0]; element.dispatchEvent(new Event(args[1], {bubbles:true}));}",
        [str(value), event],
    )


def wait_paint(page):
    page.evaluate("() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))")


def assert_layout(page, columns):
    result = page.evaluate("""() => ({
      overflow: document.documentElement.scrollWidth > innerWidth,
      columns: getComputedStyle(document.querySelector('.stream-grid')).gridTemplateColumns.split(' ').length,
      plots: [...document.querySelectorAll('.diagnostic-plot')].map(c => ({
        id:c.id, width:c.clientWidth, height:c.clientHeight, backing:c.width,
        label:c.getAttribute('aria-label')
      }))
    })""")
    assert not result["overflow"], result
    assert result["columns"] == columns, result
    for plot in result["plots"]:
        assert plot["height"] >= 230 and plot["width"] >= 200, plot
        assert abs(plot["backing"] - plot["width"] * min(3, page.evaluate("devicePixelRatio"))) <= 3, plot
        assert plot["label"], plot
    return result


def run_browser(base, artifacts, chromium):
    from playwright.sync_api import expect, sync_playwright

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(executable_path=chromium, headless=True,
            args=["--no-sandbox", "--disable-dev-shm-usage"])
        context = browser.new_context(viewport={"width": 1440, "height": 1050}, device_scale_factor=2)
        page = context.new_page()
        errors = []
        page.on("pageerror", lambda error: errors.append(str(error)))
        page.goto(base + "/api-stream.html?bytes=1048576&refresh=50")
        page.wait_for_function("document.getElementById('analysed-frames').textContent !== '0'")
        page.locator("#analysis-size").select_option("1048576")
        page.locator("#fft-size").select_option("65536")
        expect(page.locator("#fftvalue")).to_contain_text("65,536 points")
        expect(page.locator("#sample-info")).to_contain_text("1,048,576) analysed")
        page.locator("#fft-average").select_option("4")
        expect(page.locator("#fftvalue")).to_contain_text("4 frame(s)")
        page.locator("#pause-plots").click()
        expect(page.locator("#pause-plots")).to_have_attribute("aria-pressed", "true")
        wait_paint(page)
        frozen = page.locator("#sample-info").inner_text()
        received = number(page, "received-frames")
        page.wait_for_timeout(400)  # Allow the one already-running worker job to finish.
        analyzed = number(page, "analysed-frames")
        page.wait_for_timeout(400)
        assert page.locator("#sample-info").inner_text() == frozen
        assert number(page, "received-frames") > received
        assert number(page, "analysed-frames") == analyzed
        expect(page.locator("#analysis-size")).to_be_disabled()
        expect(page.locator("#analysis-offset")).to_be_disabled()
        page.locator("#bit-rates").evaluate("element => element.open = true")
        assert page.locator("#bit-rate-grid meter").count() == 8
        assert all(0 <= float(value) <= 100 for value in page.locator("#bit-rate-grid meter").evaluate_all("nodes=>nodes.map(n=>n.value)"))
        desktop = assert_layout(page, 2)
        page.screenshot(path=str(artifacts / "stream-desktop.png"), full_page=True)
        # View changes preserve capture/history and don't count repeated analysis.
        history = page.locator("#waterfall-info").inner_text().split(" selected frames")[0]
        for mode in ["gray", "bit", "rgb"]:
            page.locator("#waterfall-mode").select_option(mode)
            wait_paint(page)
            assert page.locator("#waterfall-info").inner_text().startswith(history)
        page.locator("#waterfall-mode").select_option("bit")
        expect(page.locator("#waterfall-bit")).to_be_enabled()
        page.locator("#waterfall-bit").select_option("0")
        page.locator("#waterfall-bytes").select_option("1024")
        page.locator("#trace-span").select_option("128")
        page.locator("#trace-style").select_option("steps")
        set_value(page, "trace-start", 4096, "input")
        wait_paint(page)
        expect(page.locator("#trace-info")).to_contain_text("Offsets 4096–4223")
        page.locator("#wave").click(position={"x": 80, "y": 60})
        page.locator("#wave").click(position={"x": 180, "y": 60})
        expect(page.locator("#marker-reading")).to_contain_text("Δ offset")
        page.locator("#trace-cursor").focus()
        page.keyboard.press("ArrowRight")
        expect(page.locator("#trace-reading")).to_contain_text("0x")
        page.locator("#fft-window").select_option("rectangular")
        expect(page.locator("#fftvalue")).to_contain_text("rectangular")
        assert number(page, "analysed-frames") == analyzed  # Recalculation isn't a new sample.
        assert page.locator("#waterfall-info").inner_text().startswith(history)
        page.locator(".spectrum-card details").evaluate("element => element.open = true")
        set_value(page, "fft-min", ".4")
        set_value(page, "fft-max", ".2")
        expect(page.locator("#fft-scale-error")).to_contain_text("Keeping the last valid scale")
        set_value(page, "fft-max", ".5")
        expect(page.locator("#fft-scale-error")).to_have_text("")
        for width in [390, 320, 768]:
            page.set_viewport_size({"width": width, "height": 844})
            wait_paint(page)
            assert_layout(page, 1)
        page.set_viewport_size({"width": 390, "height": 844})
        wait_paint(page)
        page.screenshot(path=str(artifacts / "stream-mobile.png"), full_page=True)
        page.locator("#pause-plots").click()
        set_value(page, "analysis-offset", 1024)
        page.locator("#analysis-size").select_option("4096")
        expect(page.locator("#fftvalue")).to_contain_text("Needs 65,536 bytes")
        expect(page.locator("#sample-info")).to_contain_text("[1,024, 5,120)")
        page.locator("#fft-size").select_option("auto")
        expect(page.locator("#fftvalue")).to_contain_text("4,096 points")
        # A valid offset outside the received frame is reported, not clamped silently.
        set_value(page, "analysis-offset", 1048576)
        expect(page.locator("#stream-notice")).to_contain_text("START BYTE MUST BE WITHIN")
        set_value(page, "analysis-offset", 0)
        expect(page.locator("#sample-info")).to_contain_text("[0, 4,096)")
        # Bounded history even after a long stream, and independent raw copies.
        bounded = page.evaluate("""() => {
          const h = new EntropyAnalysis.WaterfallHistory(), b = new Uint8Array(1048576);
          for(let i=0;i<1000;i++) h.add(b,i*100,i);
          return {rows:h.rows.length,bytes:h.rows.reduce((n,r)=>n+r.bytes.length,0)};
        }""")
        assert bounded == {"rows": 240, "bytes": 245760}, bounded
        # Deterministic transport fixture tests statistics, not just nonempty canvases.
        fixture = context.new_page()
        fixture.on("pageerror", lambda error: errors.append(str(error)))
        def route(socket_route):
            socket_route.send(bytes(range(256)) * 16)
        fixture.route_web_socket("**/stream?*", route)
        fixture.goto(base + "/api-stream.html?bytes=4096")
        expect(fixture.locator("#entropy")).to_have_text("8.00000")
        expect(fixture.locator("#zero-count")).to_contain_text("50.000%")
        expect(fixture.locator("#one-count")).to_contain_text("50.000%")
        expect(fixture.locator("#byte-statistics")).to_contain_text("Mean 127.500")
        fixture.locator("#bit-rates").evaluate("element=>element.open=true")
        for bit in range(8):
            expect(fixture.locator(f"#rate-{bit}")).to_have_text("50.000%")
        fixture.close()
        page.goto(base + "/api-upload.html")
        page.locator("#fileInput").set_input_files({"name": "counter.bin", "mimeType": "application/octet-stream", "buffer": bytes(range(256)) * 16})
        page.locator("#submit-upload").click()
        expect(page.locator("#upload-status")).to_have_text("Analysis complete")
        expect(page.locator("#fftvalue")).to_contain_text("2048 points · Hann · mean removed")
        expect(page.locator("#entropy")).to_have_text("8.00000 / 8 bits per byte")
        page.screenshot(path=str(artifacts / "upload-mobile.png"), full_page=True)
        page.goto(base + "/api-bytes.html?bytes=256&refresh=100")
        page.wait_for_function("document.getElementById('base64').textContent.length > 4")
        base64 = page.locator("#base64").evaluate("element => ({value:element.textContent,align:getComputedStyle(element).textAlign,decoded:atob(element.textContent).length})")
        assert base64["decoded"] == 256 and base64["align"] == "justify"
        assert not re.search(r"\s", base64["value"]), "Layout must not modify copyable Base64"
        assert not page.evaluate("document.documentElement.scrollWidth > innerWidth")
        page.screenshot(path=str(artifacts / "bytes-mobile.png"), full_page=True)
        page.goto(base + "/api-stream.html?bytes=1&refresh=50")
        expect(page.locator("#fftvalue")).to_contain_text("at least 32 bytes")
        expect(page.locator("#entropy")).to_have_text("0.00000")
        # Query validation must not start a nonsensical transport.
        page.goto(base + "/api-stream.html?bytes=1&bytes=2")
        expect(page.locator("#stream-notice")).to_have_attribute("data-state", "error")
        assert not errors, errors
        browser.close()
        return {"desktop": desktop, "bounded_history": bounded, "browser_errors": errors,
            "checks": "pause/reception, frozen reanalysis, trace A/B, power average, all colour modes, large/short FFT, offsets, 320/390/768/1440px, shared upload FFT, exact Base64, invalid query"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--server-binary", type=Path, default=Path("bin/entropy-server"))
    parser.add_argument("--artifacts", type=Path, required=True)
    parser.add_argument("--chromium", default="/usr/bin/chromium")
    args = parser.parse_args()
    args.artifacts.mkdir(parents=True, exist_ok=True)
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    base = f"http://127.0.0.1:{port}"
    log_path = args.artifacts / "server.log"
    with log_path.open("w") as log:
        process = subprocess.Popen([str(args.server_binary.resolve()), "-device=/dev/urandom", "-require-device=true",
            "-drbg=chacha20", "-enable-https=false", f"-http=127.0.0.1:{port}"], stdout=log, stderr=subprocess.STDOUT)
        try:
            for _ in range(200):
                if process.poll() is not None:
                    raise RuntimeError("Test server exited: " + log_path.read_text())
                try:
                    urllib.request.urlopen(base + "/live", timeout=1).close()
                    break
                except OSError:
                    time.sleep(.05)
            else:
                raise RuntimeError("Test server did not become live")
            results = run_browser(base, args.artifacts, args.chromium)
        finally:
            if process.poll() is None:
                process.send_signal(signal.SIGTERM)
            process.wait(timeout=10)
    summary = log_path.read_text()
    assert "drain=complete" in summary and "WS_payload_MB=" in summary and "TCP_accepted=" in summary
    assert process.returncode == 0, summary
    results["shutdown"] = [line for line in summary.splitlines() if "shutdown summary" in line or "shutdown connections" in line]
    (args.artifacts / "browser-results.json").write_text(json.dumps(results, indent=2) + "\n")
    print(json.dumps(results, indent=2))


if __name__ == "__main__":
    main()
