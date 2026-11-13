#!/usr/bin/env python3
"""Focused Chromium/Firefox checks for optional structure and PCM diagnostics.

Uses a disposable local server and public deterministic fixtures. The workspace
factory/plot wrappers below only observe production worker results in the test;
the application has no diagnostic test hooks or alternate analysis path.
"""
import argparse
import hashlib
import json
import math
from pathlib import Path
import signal
import struct
import subprocess

from test_sources_browser import free_port, serve_process
from test_stream_browser import number, set_value, wait_paint


OBSERVE_WORKSPACE = """(() => {
  let factory, plots;
  window.__diagnosticRevision=0; window.__diagnosticRendered=null; window.__spectralRows=[];
  const NativeWorker=window.Worker;
  window.Worker=class extends NativeWorker {
    set onmessage(handler){super.onmessage=event=>{
      const delay=window.__diagnosticWorkerDelay||0;
      if(delay)setTimeout(()=>handler(event),delay);else handler(event);
    };}
    get onmessage(){return super.onmessage;}
  };
  Object.defineProperty(window,'EntropyWorkspace',{configurable:true,
    get:()=>factory,set(value){factory=options=>{
      const render=options.onRender;
      const api=value({...options,onRender(data){
        window.__diagnosticRendered=data;window.__diagnosticRevision++;
        render?.(data);
      }});
      window.__diagnosticWorkspace=api;return api;
    };}
  });
  Object.defineProperty(window,'EntropyStructurePlots',{configurable:true,
    get:()=>plots,set(value){plots={...value,spectrogram(canvas,rows,options){
      window.__spectralRows=rows.map(row=>({...row,power:Array.from(row.power)}));
      return value.spectrogram(canvas,rows,options);
    }};}
  });
})();"""


def open_details(page, identifier):
    page.locator(f"#{identifier} > summary").click()


def wait_result(page, expression):
    page.wait_for_function("() => {const r=window.__diagnosticRendered;return r && (" + expression + ");}")


def change_and_wait(page, identifier, value):
    revision = page.evaluate("__diagnosticRevision")
    field = page.locator("#" + identifier)
    if field.evaluate("e=>e.tagName") == "SELECT":
        field.select_option(str(value))
    else:
        set_value(page, identifier, value)
    page.wait_for_function("revision=>__diagnosticRevision>revision", arg=revision)


def selected_hash(page):
    return page.evaluate("""async()=>{
      const digest=await crypto.subtle.digest('SHA-256',__diagnosticWorkspace.sample.buffer);
      return [...new Uint8Array(digest)].map(v=>v.toString(16).padStart(2,'0')).join('');
    }""")


def assert_optional_layout(page, canvases):
    wait_paint(page)
    result = page.evaluate("""ids=>({overflow:document.documentElement.scrollWidth>innerWidth,
      plots:ids.map(id=>{
        const c=document.getElementById(id),r=c.getBoundingClientRect();
        const pixels=c.getContext('2d').getImageData(0,0,c.width,c.height).data;
        let drawn=0;for(let i=3;i<pixels.length;i+=4)if(pixels[i])drawn++;
        return {id,width:r.width,height:r.height,backing:c.width,
          label:c.getAttribute('aria-label'),drawn};
      })})""", canvases)
    assert not result["overflow"], result
    ratio = min(3, page.evaluate("devicePixelRatio"))
    for plot in result["plots"]:
        assert plot["width"] >= 200 and plot["height"] >= 200, plot
        assert abs(plot["backing"] - plot["width"] * ratio) <= 3, plot
        assert plot["label"] and plot["drawn"] > 50, plot
    return result


def upload(page, name, payload):
    from playwright.sync_api import expect

    page.locator("#fileInput").set_input_files({"name": name, "mimeType": "application/octet-stream", "buffer": payload})
    page.locator("#submit-upload").click()
    expect(page.locator("#upload-status")).to_have_text("Analysis complete")


def file_checks(page, base, artifacts):
    from playwright.sync_api import expect

    page.goto(base + "/api-upload.html")
    assert not page.locator("#structure-diagnostics").evaluate("e=>e.open")
    assert not page.locator("#audio-diagnostics").evaluate("e=>e.open")
    counter = bytes(range(256)) * 256
    upload(page, "public-counter.bin", counter)
    assert not page.evaluate("Boolean(__diagnosticRendered.structure || __diagnosticRendered.audio)")
    expected_hash = hashlib.sha256(counter).hexdigest()
    assert selected_hash(page) == expected_hash
    original_frame = page.evaluate("__diagnosticWorkspace.sample.frame")
    original_history = page.locator("#waterfall-info").inner_text().split(" file windows")[0]
    open_details(page, "structure-diagnostics")
    wait_result(page, "r.structure?.transitions.bins===16")
    counter_result = page.evaluate("""()=>{
      const s=__diagnosticRendered.structure;
      return {input:s.inputBytes,analyzed:s.analyzedBytes,correlation:s.correlation.values[0],
        correlationBytes:s.correlation.count,pairs:s.transitions.pairs,
        sum:s.transitions.counts.reduce((a,b)=>a+b,0),
        diagonal:s.transitions.counts.reduce((sum,count,i)=>sum+(Math.floor(i/16)===i%16?count:0),0),
        entropy:[...s.blocks.entropy],ones:[...s.blocks.ones]};
    }""")
    assert counter_result["input"] == counter_result["analyzed"] == len(counter), counter_result
    assert counter_result["correlationBytes"] == 8192 and counter_result["correlation"] > .9, counter_result
    assert counter_result["sum"] == counter_result["pairs"] == len(counter)-1, counter_result
    assert counter_result["diagonal"] > .9 * counter_result["pairs"], counter_result
    assert all(abs(value-8) < 1e-9 for value in counter_result["entropy"]), counter_result
    assert all(value == .5 for value in counter_result["ones"]), counter_result
    wait_paint(page)
    assert len(page.evaluate("__spectralRows")) == 1
    change_and_wait(page, "correlation-lags", "128")
    wait_result(page, "r.structure?.correlation.maxLag===128")
    change_and_wait(page, "transition-bins", "32")
    wait_result(page, "r.structure?.transitions.bins===32")
    change_and_wait(page, "local-block-size", "256")
    wait_result(page, "r.structure?.blocks.blockSize===256")
    wait_paint(page)
    assert page.evaluate("__diagnosticWorkspace.sample.frame") == original_frame
    assert len(page.evaluate("__spectralRows")) == 1, "Frozen recalculation manufactured spectral history"
    assert page.locator("#waterfall-info").inner_text().startswith(original_history)
    assert selected_hash(page) == expected_hash
    canvases = ["structure-correlation", "structure-transitions", "structure-blocks", "spectrogram"]
    layouts = {}
    for theme in ("light", "dark"):
        if page.locator("html").get_attribute("data-theme") != theme:
            page.locator("[data-theme-toggle]").click()
        expect(page.locator("html")).to_have_attribute("data-theme", theme)
        for width in (1440, 320):
            page.set_viewport_size({"width": width, "height": 1000})
            layouts[f"{theme}-{width}"] = assert_optional_layout(page, canvases)
    page.set_viewport_size({"width": 1440, "height": 1000})
    page.locator("#structure-diagnostics").screenshot(path=str(artifacts / "structure-desktop.png"))
    # A local fault occupies a known byte range, rather than changing a global average.
    mixed = bytes(range(256))*16 + bytes(4096)
    upload(page, "counter-then-stuck.bin", mixed)
    wait_result(page, "r.structure?.analyzedBytes===8192")
    blocks = page.evaluate("[...__diagnosticRendered.structure.blocks.entropy]")
    assert all(abs(value-8) < 1e-9 for value in blocks[:16]), blocks
    assert blocks[16:] and all(value == 0 for value in blocks[16:]), blocks
    upload(page, "public-stuck.bin", bytes(8192))
    wait_result(page, "r.structure && r.structure.correlation.defined===false")
    assert page.evaluate("[...__diagnosticRendered.structure.correlation.values].every(Number.isNaN)")
    expect(page.locator("#entropy")).to_have_text("0.000000")
    return {"counter": counter_result, "layouts": layouts, "frozen_frame": original_frame}


def pcm_checks(page):
    from playwright.sync_api import expect

    sample_rate = 48000
    values = [round(14000*math.sin(2*math.pi*50*i/sample_rate)+5000*math.sin(2*math.pi*1000*i/sample_rate)) for i in range(32768)]
    payload = struct.pack("<" + "h"*len(values), *values)
    upload(page, "public-50hz-plus-1khz.pcm", payload)
    open_details(page, "audio-diagnostics")
    change_and_wait(page, "pcm-mode", "manual")
    wait_result(page, "r.audio?.sampleRate===48000")
    raw_hash = selected_hash(page)
    raw_entropy = page.locator("#entropy").inner_text()
    before = page.evaluate("""()=>({rms:__diagnosticRendered.audio.raw.rms,
      peak:__diagnosticRendered.audio.raw.peak,
      power:[...__diagnosticRendered.audio.raw.spectrum.power]})""")
    change_and_wait(page, "pcm-notch", "50")
    wait_result(page, "r.audio?.filtered?.notchHz===50")
    after = page.evaluate("""()=>({rms:__diagnosticRendered.audio.raw.rms,
      peak:__diagnosticRendered.audio.raw.peak,
      power:[...__diagnosticRendered.audio.raw.spectrum.power],
      filteredRms:__diagnosticRendered.audio.filtered.rms})""")
    assert after["rms"] == before["rms"] and after["peak"] == before["peak"]
    assert after["power"] == before["power"]
    assert after["filteredRms"] < after["rms"]
    assert selected_hash(page) == raw_hash == hashlib.sha256(payload).hexdigest()
    assert page.locator("#entropy").inner_text() == raw_entropy
    expect(page.locator("#file-sha256")).to_have_text(raw_hash)
    assert_optional_layout(page, ["audio-wave", "audio-spectrum"])
    set_value(page, "analysis-offset", 1)
    wait_result(page, "r.offset===1 && !r.audio")
    assert page.evaluate("Boolean(__diagnosticRendered.audioReason)")
    return {"sample_rate": sample_rate, "raw_rms": before["rms"], "filtered_rms": after["filteredRms"], "unchanged_sha256": raw_hash}


def source_checks(page, base):
    from playwright.sync_api import expect

    page.goto(base + "/api-sources.html")
    page.locator("#lab-kind").select_option("counter")
    page.locator("#lab-bytes").fill("4096")
    page.locator("#lab-once").click()
    expect(page.locator("#lab-download")).to_be_enabled()
    expect(page.locator("#lab-analysis-frames")).to_have_text("1")
    analyzed = number(page, "lab-analysis-frames")
    open_details(page, "structure-diagnostics")
    wait_result(page, "r.structure?.transitions.pairs===4095")
    assert number(page, "lab-analysis-frames") == analyzed
    page.locator("#lab-record").click()
    with page.expect_download() as downloaded:
        page.locator("#lab-download").click()
    expected = Path(downloaded.value.path()).read_bytes()
    assert expected == bytes(range(256))*16
    # Simulated producer supplies the same explicit format metadata as the real
    # audio adapter. Hardware capture/permission lifecycles have separate tests.
    page.evaluate("""()=>{
      const original=EntropyLabAdapters.start;
      window.EntropyLabAdapters={...EntropyLabAdapters,start:async(options,callbacks)=>{
        if(options.kind!=='microphone')return original(options,callbacks);
        callbacks.ready?.();
        const bytes=new Uint8Array(4096),view=new DataView(bytes.buffer);
        for(let i=0;i<bytes.length/2;i++)view.setInt16(i*2,Math.round(16000*Math.sin(2*Math.PI*1000*i/48000)),true);
        callbacks.emit(bytes,'Public PCM fixture',{format:'pcm16le',sampleRate:48000,channels:1});
        return ()=>{};
      }};
    }""")
    open_details(page, "audio-diagnostics")
    page.locator("#lab-kind").select_option("microphone")
    page.locator("#lab-once").click()
    wait_result(page, "r.audio?.sampleRate===48000")
    assert page.locator("#pcm-mode").input_value() == "auto"
    with page.expect_download() as downloaded:
        page.locator("#lab-download").click()
    original_audio = Path(downloaded.value.path()).read_bytes()
    change_and_wait(page, "pcm-notch", "60")
    wait_result(page, "r.audio?.filtered?.notchHz===60")
    with page.expect_download() as downloaded:
        page.locator("#lab-download").click()
    assert Path(downloaded.value.path()).read_bytes() == original_audio
    page.locator("#lab-transform").select_option("lsb")
    page.locator("#lab-once").click()
    wait_result(page, "r.representation==='lsb' && !r.audio")
    assert page.evaluate("Boolean(__diagnosticRendered.audioReason)")
    return {"raw_export_bytes": len(expected), "audio_export_bytes": len(original_audio), "same_handle_reanalysis_counted_once": analyzed}


def pending_capture_checks(page, base):
    from playwright.sync_api import expect

    page.goto(base + "/api-sources.html")
    page.locator("#lab-kind").select_option("counter")
    page.locator("#lab-bytes").fill("4096")
    # Delay delivery, not computation, to keep the real first worker job pending
    # while an optional panel requests reanalysis. No source sample exists yet.
    page.evaluate("__diagnosticWorkerDelay=500")
    page.locator("#lab-once").click()
    assert not page.evaluate("Boolean(__diagnosticRendered)")
    page.locator("#structure-diagnostics").evaluate("e=>e.open=true")
    expect(page.locator("#lab-download")).to_be_enabled()
    wait_result(page, "r.structure?.transitions.pairs===4095")
    expect(page.locator("#lab-analysis-frames")).to_have_text("1")
    page.evaluate("__diagnosticWorkerDelay=0")
    return {"first_capture_retained": True, "counted_frames": 1}


def stream_checks(page, base):
    from playwright.sync_api import expect

    page.goto(base + "/api-stream.html?bytes=4096&refresh=50")
    page.wait_for_function("window.__diagnosticRendered?.analyzedBytes===4096")
    open_details(page, "structure-diagnostics")
    wait_result(page, "r.structure?.analyzedBytes===4096")
    page.wait_for_function("__spectralRows.length>=3")
    page.locator("#pause-plots").click()
    expect(page.locator("#pause-plots")).to_have_attribute("aria-pressed", "true")
    page.wait_for_timeout(200)  # Let the one already running worker job settle.
    frames = number(page, "analysed-frames")
    history = page.evaluate("__spectralRows.map(r=>({frame:r.frame,time:r.time}))")
    change_and_wait(page, "correlation-lags", "32")
    wait_result(page, "r.structure?.correlation.maxLag===32")
    wait_paint(page)
    assert number(page, "analysed-frames") == frames
    assert page.evaluate("__spectralRows.map(r=>({frame:r.frame,time:r.time}))") == history
    assert len(history) <= 120 and all(history[i]["time"] >= history[i+1]["time"] for i in range(len(history)-1))
    page.locator("#clear-spectral-history").click()
    wait_paint(page)
    assert page.evaluate("__spectralRows.length") == 0
    change_and_wait(page, "correlation-lags", "64")
    wait_result(page, "r.structure?.correlation.maxLag===64")
    wait_paint(page)
    assert page.evaluate("__spectralRows.length") == 0, "Frozen reanalysis resurrected a cleared spectral row"
    page.locator("#pause-plots").click()
    page.wait_for_function("__spectralRows.length>=1")
    page.goto(base + "/api-socket.html")
    assert not page.locator("#structure-diagnostics").evaluate("e=>e.open")
    page.locator("#lab-kind").select_option("self-http")
    page.locator("#lab-bytes").fill("4096")
    open_details(page, "structure-diagnostics")
    page.locator("#lab-once").click()
    wait_result(page, "r.structure?.transitions.pairs===4095")
    return {"paused_history_rows": len(history), "paused_analyzed_frames": frames, "socket_pairs": 4095}


def run_browser(base, artifacts, engine, chromium):
    from playwright.sync_api import sync_playwright

    with sync_playwright() as playwright:
        if engine == "firefox":
            browser = playwright.firefox.launch(headless=True, firefox_user_prefs={"gfx.webrender.software": True, "layers.acceleration.disabled": True})
        else:
            browser = playwright.chromium.launch(executable_path=chromium or None, headless=True, args=["--no-sandbox", "--disable-dev-shm-usage"])
        context = browser.new_context(viewport={"width": 1440, "height": 1000}, device_scale_factor=2, accept_downloads=True)
        context.add_init_script(OBSERVE_WORKSPACE)
        page = context.new_page()
        errors = []
        page.on("pageerror", lambda error: errors.append(str(error)))
        try:
            results = {"file": file_checks(page, base, artifacts), "pcm": pcm_checks(page),
                       "sources": source_checks(page, base), "pending_capture": pending_capture_checks(page, base),
                       "stream": stream_checks(page, base)}
            assert not errors, errors
            return {"browser": engine, **results, "browser_errors": errors}
        except Exception:
            page.screenshot(path=str(artifacts / "failure.png"), full_page=True)
            raise
        finally:
            context.close()
            browser.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", default="bin/entropy-server")
    parser.add_argument("--browser", choices=["chromium", "firefox"], default="chromium")
    parser.add_argument("--chromium", default="")
    parser.add_argument("--artifacts", default="browser-artifacts/diagnostics")
    args = parser.parse_args()
    artifacts = Path(args.artifacts).resolve()
    artifacts.mkdir(parents=True, exist_ok=True)
    port = free_port()
    process = None
    with (artifacts / "server.log").open("w") as log:
        try:
            process = serve_process(Path(args.binary).resolve(), port, log)
            results = run_browser(f"http://127.0.0.1:{port}", artifacts, args.browser, args.chromium)
            (artifacts / "results.json").write_text(json.dumps(results, indent=2) + "\n")
            print(f"{args.browser}: optional structure/PCM views, raw exports, worker reanalysis, history, themes and mobile layout passed")
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
