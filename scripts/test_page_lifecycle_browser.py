#!/usr/bin/env python3
"""Real tab visibility and BFCache ownership checks in Chromium and Firefox.

Run under xvfb-run on Linux. Chromium is attached with Playwright's public
no_defaults option: ordinary automation otherwise makes every tab visible,
which would hide the subscription leak this test is intended to detect.
"""
import argparse
from contextlib import contextmanager
import hashlib
import json
from pathlib import Path
import re
import signal
import subprocess
import tempfile
import time

from test_sources_browser import free_port, serve_process


ROOT = Path(__file__).resolve().parents[1]
OBSERVE = """(() => {
  const state=window.__lifecycle={id:Date.now()+':'+Math.random(),events:[],workers:[],sockets:[]};
  addEventListener('pageshow',event=>state.events.push({type:'show',persisted:event.persisted}));
  addEventListener('pagehide',event=>state.events.push({type:'hide',persisted:event.persisted}));
  document.addEventListener('visibilitychange',()=>state.events.push({type:'visibility',hidden:document.hidden}));
  const NativeWorker=Worker;
  window.Worker=class extends NativeWorker {
    constructor(...args){super(...args);state.workers.push(this);this.retired=false;}
    terminate(){this.retired=true;super.terminate();}
  };
  const NativeSocket=WebSocket;
  window.WebSocket=class extends NativeSocket {
    constructor(...args){super(...args);state.sockets.push(this);}
  };
  let factory;
  Object.defineProperty(window,'EntropyWorkspace',{configurable:true,get:()=>factory,set(value){
    factory=options=>{const api=value(options);state.workspace=api;return api;};
  }});
})();"""


@contextmanager
def browser_context(playwright, engine, artifacts):
    if engine == "firefox":
        browser = playwright.firefox.launch(headless=False, firefox_user_prefs={
            "focusmanager.testmode": False, "gfx.webrender.software": True,
            "layers.acceleration.disabled": True})
        context = browser.new_context(viewport={"width": 1280, "height": 900}, locale="en-US")
        try:
            yield context
        finally:
            context.close()
            browser.close()
        return
    with tempfile.TemporaryDirectory(prefix="entropy-lifecycle-chromium-") as profile:
        with (artifacts / "chromium-process.log").open("w") as log:
            process = subprocess.Popen([playwright.chromium.executable_path, "--no-sandbox",
                "--disable-dev-shm-usage", "--no-first-run", "--no-default-browser-check",
                "--disable-background-networking", "--remote-debugging-address=127.0.0.1",
                "--remote-debugging-port=0", "--user-data-dir=" + profile, "about:blank"],
                stdout=log, stderr=subprocess.STDOUT)
            browser = None
            try:
                endpoint = Path(profile) / "DevToolsActivePort"
                deadline = time.monotonic() + 15
                while not endpoint.is_file():
                    assert process.poll() is None, "Chromium exited before its local test endpoint was ready"
                    assert time.monotonic() < deadline, "Chromium test endpoint startup timed out"
                    time.sleep(.025)
                port = int(endpoint.read_text().splitlines()[0])
                browser = playwright.chromium.connect_over_cdp(f"http://127.0.0.1:{port}", no_defaults=True)
                # no_defaults applies to the native default context. Creating a
                # separate automation context would re-enable focus emulation.
                yield browser.contexts[0]
            finally:
                if browser is not None:
                    browser.close()
                if process.poll() is None:
                    process.terminate()
                    try:
                        process.wait(timeout=5)
                    except subprocess.TimeoutExpired:
                        process.kill()
                        process.wait(timeout=5)


def wait_connections(context, base, expected):
    deadline = time.monotonic() + 8
    while True:
        response = context.request.get(base + "/metrics", timeout=3000)
        assert response.status == 200
        match = re.search(r"^ws_connections_active (\d+)$", response.text(), re.M)
        assert match is not None, "The real server did not expose active WebSocket admission"
        actual = int(match[1])
        if actual == expected:
            return
        assert time.monotonic() < deadline, ("active WebSockets", actual, expected)
        time.sleep(.025)


def back_from_home(page, base):
    identity = page.evaluate("__lifecycle.id")
    page.locator(".site-toolbar a").first.click()
    page.wait_for_url(base + "/")
    # A restored document emits pageshow, not another load event.
    page.go_back(wait_until="commit")
    page.wait_for_function("id=>__lifecycle.id===id && __lifecycle.events.some(event=>event.type==='show'&&event.persisted)", arg=identity)
    return page.evaluate("__lifecycle.events")


def retire_callbacks(page, socket=False):
    page.evaluate("""socket=>{
      const old=socket?__lifecycle.sockets.at(-1):__lifecycle.workers.at(-1);
      __lifecycle.staleMessage=old.onmessage;__lifecycle.staleError=old.onerror;
      __lifecycle.staleClose=old.onclose;
    }""", socket)


def compact_view(context, base, path):
    page = context.new_page()
    peer = None
    try:
        page.goto(base + path)
        page.bring_to_front()
        page.wait_for_function("document.getElementById('hash').textContent.replace(/\\s/g,'').length===64")
        page.wait_for_function("__lifecycle.workers.length===1")
        wait_connections(context, base, 1)
        retire_callbacks(page)
        with context.expect_page() as opened:
            page.evaluate("url=>window.open(url,'_blank','noopener')", base + "/")
        peer = opened.value
        peer.wait_for_load_state()
        peer.bring_to_front()
        page.wait_for_function("document.hidden")
        wait_connections(context, base, 0)
        assert page.evaluate("__lifecycle.workers.length===1&&__lifecycle.workers[0].retired")
        page.bring_to_front()
        page.wait_for_function("!document.hidden&&__lifecycle.workers.length===2")
        wait_connections(context, base, 1)
        page.evaluate("""()=>{
          const marker='f'.repeat(64);
          __lifecycle.staleMessage({data:{type:'frame',hash:marker,hex:'stale',base64:'stale',bytes:1,previewBytes:1}});
          __lifecycle.staleError();
          if(document.getElementById('hash').textContent.replace(/\\s/g,'')===marker)throw Error('Retired worker painted');
          if(document.getElementById('stream-notice').dataset.state==='error')throw Error('Retired worker error escaped');
          dispatchEvent(new PageTransitionEvent('pageshow',{persisted:true}));
        }""")
        assert page.evaluate("__lifecycle.workers.length") == 2
        peer.close()
        peer = None
        events = back_from_home(page, base)
        page.wait_for_function("__lifecycle.workers.length===3")
        wait_connections(context, base, 1)
        assert page.evaluate("__lifecycle.workers.slice(0,-1).every(worker=>worker.retired)")
        return {"path": path, "hidden_connections": 0, "visible_connections": 1,
                "worker_generations": 3, "real_bfcache": True, "events": events}
    finally:
        if peer is not None:
            peer.close()
        page.close()
        wait_connections(context, base, 0)


def upload(context, page, payload, name):
    from playwright.sync_api import expect

    page.locator("#fileInput").set_input_files({"name": name, "mimeType": "application/octet-stream", "buffer": payload})
    page.locator("#submit-upload").click()
    digest = hashlib.sha256(payload).hexdigest()
    expect(page.locator("#file-sha256")).to_have_text(digest)
    expect(page.locator("#upload-status")).to_have_text("Analysis complete")
    page.wait_for_function("__lifecycle.workspace.sample!==null")
    return digest


def upload_history(context, base):
    page = context.new_page()
    posts = []
    page.on("request", lambda request: posts.append(request.url)
            if request.method == "POST" and request.url == base + "/files" else None)
    try:
        page.goto(base + "/api-upload.html")
        page.bring_to_front()
        first = upload(context, page, bytes(range(256)) * 16, "before-back.bin")
        retire_callbacks(page)
        events = back_from_home(page, base)
        page.wait_for_function("__lifecycle.workers.length===2")
        assert page.locator("#file-sha256").text_content() == first
        assert page.locator("#bit-rate-grid > .bit-rate-row").count() == 8, "Resume duplicated control bindings/markup"
        page.evaluate("__lifecycle.staleMessage({data:{type:'error',message:'stale worker'}});__lifecycle.staleError()")
        second = upload(context, page, bytes([255]) * 8192, "after-back.bin")
        assert first != second and len(posts) == 2, posts
        assert page.evaluate("__lifecycle.workspace.sample.analyzedBytes") == 8192
        assert page.evaluate("new Uint8Array(__lifecycle.workspace.sample.buffer).every(byte=>byte===255)")
        return {"real_bfcache": True, "uploads": len(posts), "sha256_after_back": second,
                "worker_generations": 2, "unique_control_rows": 8, "events": events}
    finally:
        page.close()


def stream_history(context, base):
    from playwright.sync_api import expect

    page = context.new_page()
    try:
        page.goto(base + "/api-stream.html?bytes=4096&refresh=50")
        page.bring_to_front()
        page.wait_for_function("__lifecycle.workspace?.sample!==null")
        wait_connections(context, base, 1)
        page.locator("#pause-plots").click()
        expect(page.locator("#pause-plots")).to_have_attribute("aria-pressed", "true")
        before = page.evaluate("__lifecycle.workspace.sample.frame")
        retire_callbacks(page, socket=True)
        events = back_from_home(page, base)
        page.wait_for_function("__lifecycle.sockets.length===2&&__lifecycle.workers.length===2")
        wait_connections(context, base, 1)
        assert page.evaluate("__lifecycle.workspace.sample.frame") == before
        page.evaluate("__lifecycle.staleMessage({data:'stale'});__lifecycle.staleError();__lifecycle.staleClose()")
        expect(page.locator("#pause-plots")).to_have_attribute("aria-pressed", "true")
        page.locator("#pause-plots").click()
        page.wait_for_function("frame=>__lifecycle.workspace.sample.frame>frame", arg=before)
        wait_connections(context, base, 1)
        return {"real_bfcache": True, "socket_generations": 2, "worker_generations": 2,
                "frozen_sample_retained": True, "new_samples_resume": True, "events": events}
    finally:
        page.close()
        wait_connections(context, base, 0)


def main():
    from playwright.sync_api import sync_playwright

    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--browser", choices=("chromium", "firefox"), default="chromium")
    parser.add_argument("--binary", type=Path, default=ROOT / "bin/entropy-server")
    parser.add_argument("--artifacts", type=Path, default=ROOT / "browser-artifacts/lifecycle")
    args = parser.parse_args()
    args.artifacts.mkdir(parents=True, exist_ok=True)
    errors, process = [], None
    with (args.artifacts / "server.log").open("w") as log:
        try:
            port = free_port()
            process = serve_process(args.binary.resolve(), port, log)
            base = f"http://127.0.0.1:{port}"
            with sync_playwright() as playwright, browser_context(playwright, args.browser, args.artifacts) as context:
                context.add_init_script(OBSERVE)
                context.on("page", lambda page: page.on("pageerror", lambda error: errors.append(str(error))))
                results = {"browser": args.browser,
                    "compact_views": [compact_view(context, base, f"/api-{view}.html?bytes=4096&refresh=50")
                                      for view in ("bytes", "colors")],
                    "upload": upload_history(context, base), "stream": stream_history(context, base)}
                assert not errors, errors
                (args.artifacts / "results.json").write_text(json.dumps(results, indent=2) + "\n")
            print(f"{args.browser}: real hidden-tab release, single reattachment, BFCache upload/stream recovery and stale-event ownership passed")
        finally:
            if process is not None and process.poll() is None:
                process.send_signal(signal.SIGTERM)
                try:
                    process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)


if __name__ == "__main__":
    main()
