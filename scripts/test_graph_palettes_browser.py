#!/usr/bin/env python3
"""Focused Chromium/Firefox checks for graph palette rendering and persistence."""
import argparse
from datetime import datetime, timezone
import json
from pathlib import Path
import signal
import subprocess
import traceback

from test_diagnostics_browser import OBSERVE_WORKSPACE, change_and_wait, open_details, selected_hash, upload, wait_result
from test_sources_browser import free_port, serve_process
from test_stream_browser import assert_waterfall_pixels, number, wait_paint


PALETTES = ("classic", "blue-orange", "contrast")
PLOT_IDS = ["wave", "histogram", "fft", "pie", "structure-correlation", "structure-transitions",
            "structure-blocks", "spectrogram", "audio-wave", "audio-spectrum"]
OBSERVE_PALETTE = """(() => {
  window.__paletteJobs=0;window.__paletteAudioStrokes=[];
  const post=Worker.prototype.postMessage;
  Worker.prototype.postMessage=function(...args){__paletteJobs++;return post.apply(this,args);};
  const stroke=CanvasRenderingContext2D.prototype.stroke;
  CanvasRenderingContext2D.prototype.stroke=function(...args){
    if(['audio-wave','audio-spectrum'].includes(this.canvas.id)){
      __paletteAudioStrokes.push({id:this.canvas.id,style:this.strokeStyle,dash:this.getLineDash()});
      if(__paletteAudioStrokes.length>256)__paletteAudioStrokes.shift();
    }
    return stroke.apply(this,args);
  };
})();"""


def choose_palette(page, palette):
    from playwright.sync_api import expect

    page.locator("select[data-graph-palette]").select_option(palette)
    expect(page.locator("html")).to_have_attribute("data-graph-palette", palette)
    expect(page.locator("select[data-graph-palette]")).to_have_value(palette)
    wait_paint(page)


def checkpoint(artifacts, phase, **details):
    entry = {"time_utc": datetime.now(timezone.utc).isoformat(), "phase": phase, **details}
    line = json.dumps(entry)
    (artifacts / "progress.json").write_text(line + "\n")
    with (artifacts / "progress.jsonl").open("a") as log:
        log.write(line + "\n")
    print(line, flush=True)


def snapshot(page):
    return page.evaluate("""()=>{
      const sample=__diagnosticWorkspace.sample;
      return {jobs:__paletteJobs,revision:__diagnosticRevision,frame:sample.frame,
        histogram:[...sample.histogram],entropy:sample.entropy,
        entropyLabel:document.getElementById('entropy').textContent,
        rawHistory:document.getElementById('waterfall-info').textContent,
        spectral:__spectralRows.map(row=>({frame:row.frame,time:row.time,power:row.power})),
        audioRms:sample.audio?.raw.rms,audioPower:sample.audio?[...sample.audio.raw.spectrum.power]:null,
        audioDescription:document.getElementById('audio-filter-info').textContent};
    }""")


def fingerprints(page):
    return page.evaluate("""ids=>{
      // The assertion compares palette colours, not full-resolution raster
      // identity. Bound readback to 32 KiB per chart instead of allocating a
      // multi-MiB ImageData for every DPR-scaled source canvas in every state.
      const probe=document.createElement('canvas');probe.width=128;probe.height=64;
      const ctx=probe.getContext('2d',{willReadFrequently:true});
      const result=Object.fromEntries(ids.map(id=>{
      const canvas=document.getElementById(id);
      ctx.clearRect(0,0,probe.width,probe.height);
      ctx.drawImage(canvas,0,0,probe.width,probe.height);
      const bytes=ctx.getImageData(0,0,probe.width,probe.height).data;
      let hash=2166136261;for(let i=0;i<bytes.length;i+=4){
        hash=Math.imul(hash^bytes[i],16777619);hash=Math.imul(hash^bytes[i+1],16777619);
        hash=Math.imul(hash^bytes[i+2],16777619);hash=Math.imul(hash^bytes[i+3],16777619);
      }
      return [id,hash>>>0];
      }));
      probe.width=probe.height=0;return result;
    }""", PLOT_IDS)


def palette_rendering(page, base, artifacts):
    from playwright.sync_api import expect

    page.goto(base + "/api-upload.html")
    control = page.locator("select[data-graph-palette]")
    assert control.evaluate("e=>[...e.options].map(option=>option.value)") == list(PALETTES)
    assert control.evaluate("e=>[...e.labels].some(label=>label.textContent.toLowerCase().includes('graph'))")
    upload(page, "public-counter.bin", bytes(range(256))*256)
    open_details(page, "structure-diagnostics")
    wait_result(page, "Boolean(r.structure)")
    open_details(page, "audio-diagnostics")
    change_and_wait(page, "pcm-mode", "manual")
    change_and_wait(page, "pcm-notch", "50")
    wait_result(page, "r.audio?.filtered?.notchHz===50")
    wait_paint(page)
    raw_hash = selected_hash(page)
    baseline = snapshot(page)
    assert "solid" in baseline["audioDescription"] and "dashed" in baseline["audioDescription"]
    colors = {}
    literal = None
    for theme in ("light", "dark"):
        checkpoint(artifacts, "render-theme", theme=theme)
        if page.locator("html").get_attribute("data-theme") != theme:
            page.locator("[data-theme-toggle]").click()
        expect(page.locator("html")).to_have_attribute("data-theme", theme)
        wait_paint(page)
        colors[theme] = {}
        for palette in PALETTES:
            checkpoint(artifacts, "render-palette", theme=theme, palette=palette)
            choose_palette(page, palette)
            assert snapshot(page) == baseline, (theme, palette, "Palette changed analysis, counters or history")
            assert selected_hash(page) == raw_hash
            colors[theme][palette] = fingerprints(page)
            data_colors = assert_waterfall_pixels(page)
            actual = {name: data_colors[name] for name in ("rgb", "gray", "bit", "vivid")}
            if literal is None:
                literal = actual
            assert actual == literal, (theme, palette, actual)
            strokes = page.evaluate("__paletteAudioStrokes")
            for identifier in ("audio-wave", "audio-spectrum"):
                assert any(s["id"] == identifier and not s["dash"] for s in strokes)
                assert any(s["id"] == identifier and s["dash"] == [5, 3] for s in strokes)
            if palette == "contrast":
                checkpoint(artifacts, "render-audio-screenshot", theme=theme)
                page.locator("#audio-diagnostics").screenshot(path=str(artifacts / f"audio-{theme}-contrast.png"))
        for identifier in PLOT_IDS:
            assert len({colors[theme][palette][identifier] for palette in PALETTES}) == 3, (theme, identifier, colors[theme])
        page.set_viewport_size({"width": 320, "height": 900})
        wait_paint(page)
        assert not page.evaluate("document.documentElement.scrollWidth>innerWidth")
        expect(control).to_be_enabled()
        control.focus()
        assert control.evaluate("e=>e===document.activeElement")
        page.screenshot(path=str(artifacts / f"palette-{theme}-mobile.png"))
        checkpoint(artifacts, "render-mobile-complete", theme=theme)
        page.set_viewport_size({"width": 1440, "height": 1000})
        wait_paint(page)
    return {"canvas_fingerprints": colors, "literal_waterfall_colors": literal, "unchanged_sha256": raw_hash,
            "worker_jobs": baseline["jobs"], "frozen_frame": baseline["frame"]}


def source_export_checks(page, base):
    from playwright.sync_api import expect

    page.goto(base + "/api-sources.html")
    page.locator("#lab-kind").select_option("counter")
    page.locator("#lab-bytes").fill("4096")
    page.locator("#lab-once").click()
    expect(page.locator("#lab-download")).to_be_enabled()
    expect(page.locator("#lab-analysis-frames")).to_have_text("1")
    open_details(page, "structure-diagnostics")
    wait_result(page, "Boolean(r.structure)")
    wait_paint(page)
    before = snapshot(page)
    counters = {identifier: number(page, identifier) for identifier in ("lab-input-frames", "lab-input-bytes", "lab-analysis-frames", "lab-analysis-bytes")}
    for palette in PALETTES:
        choose_palette(page, palette)
        assert snapshot(page) == before
        assert all(number(page, identifier) == value for identifier, value in counters.items())
        with page.expect_download() as downloaded:
            page.locator("#lab-download").click()
        assert Path(downloaded.value.path()).read_bytes() == bytes(range(256))*16
    return counters


def prose_measure(page, selector):
    result = page.locator(selector).first.evaluate("""p=>{
      const parent=p.parentElement,style=getComputedStyle(parent);
      return {width:p.getBoundingClientRect().width,text:p.textContent.trim(),
        available:parent.clientWidth-parseFloat(style.paddingLeft)-parseFloat(style.paddingRight)};
    }""")
    assert result["text"] and result["width"] > 0, result
    assert abs(result["width"] - result["available"]) <= 2, (selector, result)
    return result


def desktop_width_checks(page, base, artifacts):
    # The file workspace is still frozen with structure/audio sections open.
    # The compact balance circle keeps its explicit size; rectangular graphs
    # gain horizontal detail without increasing their fixed heights.
    widths = {}
    rectangular = [identifier for identifier in PLOT_IDS if identifier != "pie"]
    for viewport in (1440, 1920, 2560):
        checkpoint(artifacts, "desktop-analysis-resize", viewport=viewport)
        page.set_viewport_size({"width": viewport, "height": 1000})
        wait_paint(page)
        result = page.evaluate("""ids=>{
          const main=document.querySelector('main.analysis-panel'),r=main.getBoundingClientRect();
          return {viewport:innerWidth,available:document.documentElement.clientWidth,
            width:r.width,gutter:r.left+document.documentElement.clientWidth-r.right,
            overflow:document.documentElement.scrollWidth>innerWidth,
            plots:Object.fromEntries(ids.map(id=>{
              const box=document.getElementById(id).getBoundingClientRect();
              return [id,{width:box.width,height:box.height}];
            }))};
        }""", rectangular)
        assert not result["overflow"], result
        assert abs(result["width"] - (result["available"] - viewport * .1)) <= 2, result
        assert abs(result["gutter"] - viewport * .1) <= 2, result
        widths[viewport] = result
    for identifier in rectangular:
        actual = [widths[viewport]["plots"][identifier] for viewport in (1440, 1920, 2560)]
        assert actual[0]["width"] < actual[1]["width"] < actual[2]["width"], (identifier, actual)
        assert max(plot["height"] for plot in actual) - min(plot["height"] for plot in actual) <= 1, (identifier, actual)
    page.locator("details.analysis-help").evaluate("e=>e.open=true")
    prose = {"analysis_help": prose_measure(page, "details.analysis-help p")}
    checkpoint(artifacts, "desktop-analysis-complete")
    containers = {}
    for path, old_cap, blurb in (("/", 75*16, "header .lede"),
                                 ("/api-gallery.html", 88*16, ".gallery-notes p"),
                                 ("/api-status.html", 75*16, "header .lede")):
        checkpoint(artifacts, "desktop-page-navigation", path=path)
        page.goto(base + path)
        if path == "/api-gallery.html":
            page.locator(".gallery-notes").evaluate("e=>e.open=true")
        measurements = {}
        for viewport in (1440, 1920, 2560):
            checkpoint(artifacts, "desktop-page-resize", path=path, viewport=viewport)
            page.set_viewport_size({"width": viewport, "height": 1000})
            wait_paint(page)
            data = page.evaluate("""()=>({width:document.querySelector('.wrap > main').getBoundingClientRect().width,
              available:document.documentElement.clientWidth,
              overflow:document.documentElement.scrollWidth>innerWidth})""")
            assert not data["overflow"], (path, viewport, data)
            assert abs(data["width"] - (data["available"] - viewport * .1)) <= 2, (path, viewport, data)
            measurements[viewport] = data["width"]
        assert measurements[1440] < measurements[1920] < measurements[2560], (path, measurements)
        assert measurements[1920] > old_cap, (path, measurements)
        containers[path] = measurements
        prose[path] = prose_measure(page, blurb)
    # A regular data endpoint uses the widened common panel independently of
    # the analysis/homepage layouts. Specialized playback widgets stay bounded.
    checkpoint(artifacts, "desktop-data-navigation")
    page.goto(base + "/api-bytes.html?bytes=256&refresh=1000")
    wait_paint(page)
    data_panel = page.locator("main.panel").bounding_box()
    assert data_panel["width"] > 2200, data_panel
    assert not page.evaluate("document.documentElement.scrollWidth>innerWidth")
    for worker in page.workers:
        worker.evaluate("new Promise(resolve=>setTimeout(resolve,0))")
    page.set_viewport_size({"width": 1440, "height": 1000})
    return {"analysis": widths, "containers": containers, "full_width_prose": prose,
            "ordinary_panel_width_at_2560": data_panel["width"]}


def preference_checks(context, page, base):
    from playwright.sync_api import expect

    page.goto(base + "/api-upload.html")
    choose_palette(page, "classic")
    second = context.new_page()
    second.goto(base + "/api-upload.html")
    choose_palette(second, "blue-orange")
    expect(page.locator("html")).to_have_attribute("data-graph-palette", "blue-orange")
    choose_palette(page, "contrast")
    expect(second.locator("select[data-graph-palette]")).to_have_value("contrast")
    for path in ("/api-upload.html", "/api-sources.html", "/api-socket.html", "/api-stream.html?bytes=4096"):
        page.goto(base + path)
        expect(page.locator("html")).to_have_attribute("data-graph-palette", "contrast")
        expect(page.locator("select[data-graph-palette]")).to_have_value("contrast")
        for worker in page.workers:
            worker.evaluate("new Promise(resolve=>setTimeout(resolve,0))")
    page.goto(base + "/api-upload.html")
    page.reload()
    expect(page.locator("select[data-graph-palette]")).to_have_value("contrast")
    page.bring_to_front()
    page.wait_for_function("!document.hidden")
    for event in ("focus", "pageshow", "visibilitychange"):
        page.evaluate("""event=>{
          document.documentElement.dataset.graphPalette='classic';
          if(event==='visibilitychange')document.dispatchEvent(new Event(event));
          else window.dispatchEvent(event==='pageshow'?new PageTransitionEvent(event,{persisted:true}):new Event(event));
        }""", event)
        expect(page.locator("html")).to_have_attribute("data-graph-palette", "contrast")
    page.evaluate("""()=>{
      localStorage.setItem('entropy-graph-palette','blue-orange');
      dispatchEvent(new StorageEvent('storage',{key:'entropy-graph-palette',newValue:'classic'}));
    }""")
    expect(page.locator("html")).to_have_attribute("data-graph-palette", "blue-orange")
    page.evaluate("""()=>{
      localStorage.setItem('entropy-graph-palette','not-a-palette');
      dispatchEvent(new Event('focus'));
    }""")
    expect(page.locator("select[data-graph-palette]")).to_have_value("classic")
    page.evaluate("""()=>{
      localStorage.removeItem('entropy-graph-palette');
      dispatchEvent(new StorageEvent('storage',{key:null}));
    }""")
    expect(page.locator("html")).to_have_attribute("data-graph-palette", "classic")
    second.close()
    return {"cross_window": True, "navigation": True, "restoration_events": True,
            "stale_storage_payload": True, "invalid_value_falls_back": True}


def storage_disabled_checks(browser, base):
    from playwright.sync_api import expect

    context = browser.new_context()
    context.add_init_script("""(()=>{
      for(const name of ['getItem','setItem']){
        const original=Storage.prototype[name];
        Storage.prototype[name]=function(key,...args){
          if(key==='entropy-graph-palette')throw new DOMException('Fixture: storage disabled','SecurityError');
          return original.call(this,key,...args);
        };
      }
    })();""")
    page = context.new_page()
    errors = []
    page.on("pageerror", lambda error: errors.append(str(error)))
    try:
        page.goto(base + "/api-upload.html")
        choose_palette(page, "blue-orange")
        page.evaluate("dispatchEvent(new Event('focus'));dispatchEvent(new PageTransitionEvent('pageshow'))")
        expect(page.locator("html")).to_have_attribute("data-graph-palette", "blue-orange")
        choose_palette(page, "contrast")
        page.reload()
        expect(page.locator("select[data-graph-palette]")).to_have_value("classic")
        assert not errors, errors
        return {"in_page_selection": True, "reload_default_without_storage": True}
    finally:
        context.close()


def run_browser(base, artifacts, engine, chromium):
    from playwright.sync_api import sync_playwright

    with sync_playwright() as playwright:
        if engine == "firefox":
            browser = playwright.firefox.launch(headless=True, firefox_user_prefs={"gfx.webrender.software": True, "layers.acceleration.disabled": True})
        else:
            browser = playwright.chromium.launch(executable_path=chromium or None, headless=True, args=["--no-sandbox", "--disable-dev-shm-usage"])
        context = browser.new_context(viewport={"width": 1440, "height": 1000}, device_scale_factor=2, accept_downloads=True)
        context.add_init_script(OBSERVE_WORKSPACE)
        context.add_init_script(OBSERVE_PALETTE)
        page = context.new_page()
        errors = []
        context.on("page", lambda new_page: new_page.on("pageerror", lambda error: errors.append(str(error))))
        page.on("pageerror", lambda error: errors.append(str(error)))
        failure = None
        def phase(name, action):
            checkpoint(artifacts, name, status="started")
            result = action()
            checkpoint(artifacts, name, status="passed")
            return result
        try:
            results = {"rendering": phase("palette-rendering", lambda: palette_rendering(page, base, artifacts)),
                       "desktop_width": phase("desktop-width", lambda: desktop_width_checks(page, base, artifacts)),
                       "source_exports": phase("source-exports", lambda: source_export_checks(page, base)),
                       "preferences": phase("preferences", lambda: preference_checks(context, page, base)),
                       "storage_disabled": phase("storage-disabled", lambda: storage_disabled_checks(browser, base))}
            assert not errors, errors
            return {"browser": engine, **results, "browser_errors": errors}
        except Exception as error:
            failure = error
            traceback.print_exc()
            report = {"type": type(error).__name__, "exception": str(error), "page_closed": page.is_closed(),
                      "browser_connected": browser.is_connected(), "last_checkpoint": json.loads((artifacts / "progress.json").read_text())}
            # Failure evidence must not replace the original test exception,
            # especially when a crashed content process cannot take a picture.
            try:
                if not page.is_closed() and browser.is_connected():
                    page.screenshot(path=str(artifacts / "failure.png"), full_page=False, timeout=3000)
                else:
                    report["screenshot"] = "Page/browser already closed"
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
            if cleanup_errors:
                (artifacts / "cleanup-errors.json").write_text(json.dumps([str(error) for error in cleanup_errors], indent=2) + "\n")
                if failure is None:
                    raise cleanup_errors[0]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", default="bin/entropy-server")
    parser.add_argument("--browser", choices=["chromium", "firefox"], default="chromium")
    parser.add_argument("--chromium", default="")
    parser.add_argument("--artifacts", default="browser-artifacts/graph-palettes")
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
            print(f"{args.browser}: graph palette pixels, raw data, frozen redraw, persistence and accessible mobile controls passed")
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
