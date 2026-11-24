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
import shutil
import signal
import subprocess
import tempfile
import time

from test_sources_browser import free_port, serve_process


ROOT = Path(__file__).resolve().parents[1]
OBSERVE = """(() => {
  const state=window.__lifecycle={id:Date.now()+':'+Math.random(),events:[],workers:[],sockets:[]};
  addEventListener('pageshow',event=>state.events.push({type:'show',persisted:event.persisted}));
  addEventListener('pagehide',event=>{
    state.events.push({type:'hide',persisted:event.persisted});
    try{sessionStorage.setItem('lifecycle-hide-'+state.id,JSON.stringify(event.persisted));}catch{}
  });
  document.addEventListener('visibilitychange',()=>state.events.push({type:'visibility',hidden:document.hidden}));
  const NativeWorker=Worker;
  window.Worker=class extends NativeWorker {
    constructor(...args){super(...args);this.record={ref:new WeakRef(this),retired:false};state.workers.push(this.record);}
    terminate(){this.record.retired=true;super.terminate();}
  };
  const NativeSocket=WebSocket;
  window.WebSocket=class extends NativeSocket {
    constructor(...args){super(...args);state.sockets.push({ref:new WeakRef(this)});}
  };
  let factory;
  Object.defineProperty(window,'EntropyWorkspace',{configurable:true,get:()=>factory,set(value){
    factory=options=>{const api=value(options);state.workspace=api;return api;};
  }});
})();"""


@contextmanager
def browser_context(playwright, engine, artifacts, firefox_binary):
    if engine == "firefox":
        # Juggler unconditionally forces every attached document active. The
        # native BiDi channel lets actual tab selection drive visibility.
        browser = playwright.firefox.launch(channel="moz-firefox", executable_path=str(firefox_binary),
            headless=False, timeout=30000, firefox_user_prefs={
            "focusmanager.testmode": False, "layout.testing.top-level-always-active": False, "gfx.webrender.software": True,
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
    wait_connections(page.context, base, 0)
    # A cached document can emit pageshow without a new BiDi navigation-commit
    # notification. Invoke the real History API and observe the resulting page.
    page.evaluate("history.back()")
    page.wait_for_function("location.pathname!=='/'")
    page.wait_for_function("id=>window.__lifecycle && (__lifecycle.id!==id || __lifecycle.events.some(event=>event.type==='show'&&event.persisted))", arg=identity)
    history = page.evaluate("""id=>({cached:__lifecycle.id===id,events:__lifecycle.events,
      navigation_type:performance.getEntriesByType('navigation')[0].type,
      previous_pagehide_persisted:JSON.parse(sessionStorage.getItem('lifecycle-hide-'+id))})""", identity)
    if page.context.browser.browser_type.name == "chromium":
        assert history["cached"], ("Chromium must exercise the real BFCache restoration path", history)
    if history["cached"]:
        assert history["previous_pagehide_persisted"] is True, history
    else:
        assert history["navigation_type"] == "back_forward", history
        assert any(event["type"] == "show" and not event["persisted"] for event in history["events"]), history
    return history


def retire_callbacks(page, socket=False):
    page.evaluate("""socket=>{
      const old=(socket?__lifecycle.sockets.at(-1):__lifecycle.workers.at(-1)).ref.deref();
      if(!old)throw Error('The active fixture resource was collected');
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
        # Background tabs stop animation frames. Timer polling must observe
        # the visibility transition without waiting for another visible paint.
        page.wait_for_function("document.hidden", polling=50)
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
          delete __lifecycle.staleMessage;delete __lifecycle.staleError;delete __lifecycle.staleClose;
        }""")
        assert page.evaluate("__lifecycle.workers.length") == 2
        peer.close()
        peer = None
        history = back_from_home(page, base)
        generations = 3 if history["cached"] else 1
        page.wait_for_function("count=>__lifecycle.workers.length===count", arg=generations)
        wait_connections(context, base, 1)
        assert page.evaluate("__lifecycle.workers.slice(0,-1).every(worker=>worker.retired)")
        return {"path": path, "hidden_connections": 0, "visible_connections": 1,
                "worker_generations": generations, "history": history}
    finally:
        if peer is not None:
            peer.close()
        page.close()
        wait_connections(context, base, 0)


def upload(context, page, payload, name):
    # BiDi restores this document correctly but can retain a stale navigation
    # barrier for locator actions after BFCache. Use standard File/Form APIs;
    # the production submit handler, network upload and worker still run.
    page.evaluate("""({name,bytes})=>{
      const input=document.getElementById('fileInput'),button=document.getElementById('submit-upload');
      if(input.disabled||button.disabled)throw Error('Upload controls did not recover');
      const files=new DataTransfer();
      files.items.add(new File([new Uint8Array(bytes)],name,{type:'application/octet-stream'}));
      input.files=files.files;document.getElementById('uploadForm').requestSubmit();
    }""", {"name": name, "bytes": list(payload)})
    digest = hashlib.sha256(payload).hexdigest()
    page.wait_for_function("digest=>document.getElementById('file-sha256').textContent===digest", arg=digest)
    page.wait_for_function("document.getElementById('upload-status').textContent==='Analysis complete'")
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
        history = back_from_home(page, base)
        generations = 2 if history["cached"] else 1
        page.wait_for_function("count=>__lifecycle.workers.length===count", arg=generations)
        if history["cached"]:
            assert page.locator("#file-sha256").text_content() == first
        assert page.locator("#bit-rate-grid > .bit-rate-row").count() == 8, "Resume duplicated control bindings/markup"
        second = upload(context, page, bytes([255]) * 8192, "after-back.bin")
        assert first != second and len(posts) == 2, posts
        assert page.evaluate("__lifecycle.workspace.sample.analyzedBytes") == 8192
        assert page.evaluate("new Uint8Array(__lifecycle.workspace.sample.buffer).every(byte=>byte===255)")
        return {"history": history, "uploads": len(posts), "sha256_after_back": second,
                "worker_generations": generations, "unique_control_rows": 8}
    finally:
        page.close()


def upload_request_ownership(context, base):
    """Delay real upload responses across an explicit lifecycle race."""
    from playwright.sync_api import expect

    page = context.new_page()
    try:
        page.goto(base + "/api-upload.html")
        page.bring_to_front()
        page.evaluate("""()=>{
          const original=fetch;
          window.__uploadResponses=[];
          window.fetch=async(...args)=>{
            const response=await original(...args);
            if(args[0]!=='/files')return response;
            const body=await response.text(),status=response.status,headers=[...response.headers];
            return new Promise(resolve=>__uploadResponses.push(()=>resolve(new Response(body,{status,headers}))));
          };
        }""")
        page.locator("#fileInput").set_input_files({"name": "retired.bin", "mimeType": "application/octet-stream", "buffer": bytes(1024)})
        page.locator("#submit-upload").click()
        page.wait_for_function("__uploadResponses.length===1")
        retire_callbacks(page)
        page.evaluate("""()=>{
          dispatchEvent(new PageTransitionEvent('pagehide',{persisted:true}));
          dispatchEvent(new PageTransitionEvent('pageshow',{persisted:true}));
          __lifecycle.staleMessage({data:{type:'error',message:'retired analysis worker'}});
          __lifecycle.staleError();
        }""")
        current = bytes([42]) * 2048
        page.locator("#fileInput").set_input_files({"name": "current.bin", "mimeType": "application/octet-stream", "buffer": current})
        page.locator("#submit-upload").click()
        page.wait_for_function("__uploadResponses.length===2")
        page.evaluate("async()=>{__uploadResponses[0]();await new Promise(resolve=>setTimeout(resolve,0));}")
        expect(page.locator("#submit-upload")).to_be_disabled()
        expect(page.locator("#upload-status")).to_have_text("Uploading and analyzing")
        expect(page.locator("#file-sha256")).to_have_text("—")
        page.evaluate("__uploadResponses[1]()")
        expect(page.locator("#file-sha256")).to_have_text(hashlib.sha256(current).hexdigest())
        expect(page.locator("#upload-status")).to_have_text("Analysis complete")
        return {"real_server_responses": 2, "retired_response_cannot_reset_new_request": True}
    finally:
        page.close()


def stream_history(context, base):
    from playwright.sync_api import expect

    page = context.new_page()
    try:
        page.goto(base + "/api-stream.html?bytes=4096&refresh=50")
        page.bring_to_front()
        page.wait_for_function("Boolean(__lifecycle.workspace?.sample)")
        wait_connections(context, base, 1)
        page.locator("#pause-plots").click()
        expect(page.locator("#pause-plots")).to_have_attribute("aria-pressed", "true")
        before = page.evaluate("__lifecycle.workspace.sample.frame")
        history = back_from_home(page, base)
        generations = 2 if history["cached"] else 1
        page.wait_for_function("count=>__lifecycle.sockets.length===count&&__lifecycle.workers.length===count", arg=generations)
        wait_connections(context, base, 1)
        page.wait_for_function("Boolean(__lifecycle.workspace?.sample)")
        if history["cached"]:
            assert page.evaluate("__lifecycle.workspace.sample.frame") == before
            assert page.evaluate("document.getElementById('pause-plots').getAttribute('aria-pressed')") == "true"
        else:
            assert page.evaluate("document.getElementById('pause-plots').getAttribute('aria-pressed')") == "false"
        retire_callbacks(page, socket=True)
        page.evaluate("dispatchEvent(new PageTransitionEvent('pagehide',{persisted:true}))")
        wait_connections(context, base, 0)
        page.evaluate("dispatchEvent(new PageTransitionEvent('pageshow',{persisted:true}))")
        page.wait_for_function("count=>__lifecycle.sockets.length===count&&__lifecycle.workers.length===count", arg=generations + 1)
        wait_connections(context, base, 1)
        page.evaluate("__lifecycle.staleMessage({data:'stale'});__lifecycle.staleError();__lifecycle.staleClose()")
        if history["cached"]:
            page.evaluate("document.getElementById('pause-plots').click()")
        before_resume = page.evaluate("__lifecycle.workspace.sample.frame")
        page.wait_for_function("frame=>__lifecycle.workspace.sample.frame>frame", arg=before_resume)
        wait_connections(context, base, 1)
        return {"history": history, "history_socket_generations": generations,
                "controlled_stale_event_cycle": True, "final_socket_generations": generations + 1,
                "frozen_sample_retained": history["cached"], "new_samples_resume": True}
    finally:
        page.close()
        wait_connections(context, base, 0)


def main():
    from playwright.sync_api import sync_playwright

    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--browser", choices=("chromium", "firefox"), default="chromium")
    parser.add_argument("--binary", type=Path, default=ROOT / "bin/entropy-server")
    parser.add_argument("--firefox-binary", type=Path, default=shutil.which("firefox"),
                        help="Stock Firefox executable; Playwright's patched Firefox forces visibility and disables BFCache")
    parser.add_argument("--artifacts", type=Path, default=ROOT / "browser-artifacts/lifecycle")
    args = parser.parse_args()
    if args.browser == "firefox" and args.firefox_binary is None:
        parser.error("Stock Firefox is required: install it or pass --firefox-binary (the patched Playwright build cannot qualify lifecycle behavior)")
    args.artifacts.mkdir(parents=True, exist_ok=True)
    errors, process = [], None
    with (args.artifacts / "server.log").open("w") as log:
        try:
            port = free_port()
            process = serve_process(args.binary.resolve(), port, log)
            base = f"http://127.0.0.1:{port}"
            with sync_playwright() as playwright, browser_context(playwright, args.browser, args.artifacts, args.firefox_binary) as context:
                context.add_init_script(OBSERVE)
                context.set_default_timeout(10000)
                context.set_default_navigation_timeout(10000)
                context.on("page", lambda page: page.on("pageerror", lambda error: errors.append(str(error))))
                results = {"browser": args.browser, "browser_version": context.browser.version,
                    "compact_views": [compact_view(context, base, f"/api-{view}.html?bytes=4096&refresh=50")
                                      for view in ("bytes", "colors")],
                    "upload": upload_history(context, base),
                    "upload_request_ownership": upload_request_ownership(context, base),
                    "stream": stream_history(context, base)}
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
