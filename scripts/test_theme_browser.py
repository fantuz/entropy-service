#!/usr/bin/env python3
"""Real Chromium checks for shared themes, navigation, plots and lab disclosures."""
import argparse
import json
from pathlib import Path
import signal
import subprocess
import tempfile

from test_sources_browser import free_port, serve_process
from test_stream_browser import wait_paint


PAGES = ["/", "/api-bytes.html", "/api-colors.html", "/api-words.html", "/api-sound.html",
         "/api-stream.html", "/api-upload.html", "/api-sources.html", "/api-socket.html",
         "/api-gallery.html", "/api-status.html", "/v1/meta/random?words=3"]


def main():
    from playwright.sync_api import sync_playwright, expect

    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, default=Path("bin/entropy-server"))
    parser.add_argument("--artifacts", type=Path, default=Path("/tmp/entropy-theme-browser"))
    args = parser.parse_args()
    args.artifacts.mkdir(parents=True, exist_ok=True)
    results = []
    with tempfile.TemporaryFile() as log:
        port = free_port()
        server = serve_process(args.binary.resolve(), port, log)
        base = f"http://127.0.0.1:{port}"
        try:
            with sync_playwright() as playwright:
                browser = playwright.chromium.launch(headless=True)
                for width, height in [(1440, 1000), (390, 844)]:
                    for theme in ["light", "dark"]:
                        context = browser.new_context(viewport={"width": width, "height": height}, color_scheme=theme)
                        page = context.new_page()
                        errors = []
                        page.on("pageerror", lambda error: errors.append(str(error)))
                        for path in PAGES:
                            page.goto(base + path)
                            expect(page.locator("html")).to_have_attribute("data-theme", theme)
                            toggle = page.get_by_role("button", name="Dark mode", exact=True)
                            expect(toggle).to_have_attribute("aria-pressed", str(theme == "dark").lower())
                            assert not page.evaluate("document.documentElement.scrollWidth > innerWidth"), (path, width)
                            if path in ["/api-sources.html", "/api-socket.html"]:
                                assert "Experimental" not in page.title()
                                help_sections = page.locator(".lab-help details")
                                assert help_sections.count() == 3
                                assert page.locator(".lab-help details[open]").count() == 0
                                for index in range(3):
                                    help_sections.nth(index).locator("summary").click()
                                    expect(help_sections.nth(index)).to_have_attribute("open", "")
                                page.reload()
                                assert page.locator(".lab-help details[open]").count() == 0
                                help_sections.nth(0).locator("summary").click()
                                page.evaluate("window.dispatchEvent(new PageTransitionEvent('pageshow', {persisted:true}))")
                                assert page.locator(".lab-help details[open]").count() == 0
                            if path in ["/", "/api-sources.html", "/api-socket.html", "/api-status.html"]:
                                name = path.strip("/").replace(".html", "") or "home"
                                page.screenshot(path=str(args.artifacts / f"{name}-{theme}-{width}.png"))
                            results.append({"page": path, "theme": theme, "width": width, "passed": True})
                        assert not errors, errors
                        context.close()

                context = browser.new_context(color_scheme="dark")
                page = context.new_page()
                page.goto(base + "/api-sources.html")
                page.get_by_role("button", name="Dark mode", exact=True).click()
                page.get_by_role("link", name="Socket lab", exact=True).first.click()
                expect(page.locator("html")).to_have_attribute("data-theme", "light")
                page.reload()
                expect(page.locator("html")).to_have_attribute("data-theme", "light")

                # Check charts redraw from frozen data rather than needing a new source sample.
                page.goto(base + "/api-sources.html")
                page.locator("#lab-kind").select_option("counter")
                page.locator("#lab-once").click()
                expect(page.locator("#entropy")).not_to_have_text("—")
                for theme in ["light", "dark"]:
                    if page.locator("html").get_attribute("data-theme") != theme:
                        page.get_by_role("button", name="Dark mode", exact=True).click()
                    wait_paint(page)
                    colors = page.evaluate("""() => {
                        const style = getComputedStyle(document.documentElement);
                        const probe = document.createElement('canvas'); probe.width=200; probe.height=100;
                        probe.style.cssText='width:200px;height:100px'; document.body.append(probe);
                        EntropyPlots.empty(probe,'Theme label');
                        const label=probe.getContext('2d').fillStyle; probe.remove();
                        return {label, expected:style.getPropertyValue('--muted').trim(),
                            trace:document.getElementById('wave').getContext('2d').strokeStyle,
                            green:style.getPropertyValue('--green').trim()};
                    }""")
                    assert colors["label"] == colors["expected"] and colors["trace"] == colors["green"], colors
                    page.screenshot(path=str(args.artifacts / f"source-sample-{theme}.png"))
                second = context.new_page()
                second.goto(base + "/api-socket.html")
                second.get_by_role("button", name="Dark mode", exact=True).click()
                expect(page.locator("html")).to_have_attribute("data-theme", "light")
                context.close()

                context = browser.new_context(color_scheme="light")
                context.add_init_script("Object.defineProperty(window, 'localStorage', {get() {throw new DOMException('disabled','SecurityError')}})")
                page = context.new_page()
                page.goto(base + "/api-socket.html")
                page.get_by_role("button", name="Dark mode", exact=True).click()
                expect(page.locator("html")).to_have_attribute("data-theme", "dark")
                context.close()
                browser.close()
            (args.artifacts / "results.json").write_text(json.dumps({"pages": results, "persistence": True,
                "cross_tab": True, "blocked_storage": True, "frozen_plot_repaint": True}, indent=2))
            print(f"PASS: {len(results)} page/theme/viewport cases; saved theme, cross-tab sync, blocked storage, frozen plots and collapsed lab help")
        finally:
            server.send_signal(signal.SIGTERM)
            try:
                server.wait(timeout=10)
            except subprocess.TimeoutExpired:
                server.kill()
                server.wait()


if __name__ == "__main__":
    main()
