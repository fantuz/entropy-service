#!/usr/bin/env python3
"""Optional real Chromium smoke/regression tests; see docs/browser-ui.md.

Uses a temporary local entropy-server, never an existing server or research data.
Requires Python Playwright and Chromium, in addition to the built server binary.
"""
import argparse
import base64
import json
import hashlib
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
      columns: getComputedStyle(document.querySelector('.analysis-grid')).gridTemplateColumns.split(' ').length,
      plots: [...document.querySelectorAll('.diagnostic-plot')].map(c => ({
        id:c.id, width:c.clientWidth, height:c.clientHeight, backing:c.width,
        label:c.getAttribute('aria-label'), top:c.getBoundingClientRect().top, bottom:c.getBoundingClientRect().bottom
      })),
      cards:[...document.querySelectorAll('.analysis-grid > .analysis-card')].map(c => ({
        top:c.getBoundingClientRect().top,bottom:c.getBoundingClientRect().bottom,
        left:c.getBoundingClientRect().left,right:c.getBoundingClientRect().right,
        padding:getComputedStyle(c).padding
      }))
    })""")
    assert not result["overflow"], result
    assert result["columns"] == columns, result
    for plot in result["plots"]:
        assert plot["height"] >= 230 and plot["width"] >= 200, plot
        assert abs(plot["backing"] - plot["width"] * min(3, page.evaluate("devicePixelRatio"))) <= 3, plot
        assert plot["label"], plot
    assert len({card["padding"] for card in result["cards"]}) == 1, result
    if columns == 2:
        for index in range(0, len(result["cards"]), 2):
            a, b = result["cards"][index:index+2]
            assert abs(a["top"]-b["top"]) <= 1 and abs(a["bottom"]-b["bottom"]) <= 1, result
        for index in [0, 2]:
            a, b = result["plots"][index:index+2]
            assert abs(a["top"]-b["top"]) <= 1 and abs(a["bottom"]-b["bottom"]) <= 1, result
    else:
        assert max(card["left"] for card in result["cards"])-min(card["left"] for card in result["cards"]) <= 1, result
        assert max(card["right"] for card in result["cards"])-min(card["right"] for card in result["cards"]) <= 1, result
    return result


def assert_waterfall_pixels(page):
    result = page.evaluate("""() => {
      const c = document.createElement('canvas');
      c.style.cssText='position:absolute;left:-1000px;width:311px;height:240px';
      document.body.append(c);
      try {
        const bytes=Uint8Array.from({length:1024},(_,i)=>i%3===0?255:0);
        const rows=Array.from({length:240},(_,i)=>({bytes,time:200000-i*500,frame:240-i}));
        const sample = view => Array.from(c.getContext('2d').getImageData(
          Math.round((53+view.cellWidth/2)*Math.min(devicePixelRatio,3)),
          Math.round((23+view.cellHeight/2)*Math.min(devicePixelRatio,3)),1,1).data);
        const auto=EntropyPlots.waterfall(c,rows), rgb=sample(auto);
        const grayView=EntropyPlots.waterfall(c,rows,{mode:'gray'}), gray=sample(grayView);
        const bitView=EntropyPlots.waterfall(c,rows,{mode:'bit',bit:7}), bit=sample(bitView);
        bytes[0]=0;
        const vividView=EntropyPlots.waterfall(c,rows,{mode:'vivid'}), vivid=sample(vividView);
        const all=EntropyPlots.waterfall(c,rows,{historyRows:240});
        c.style.width='583px';
        const wide=EntropyPlots.waterfall(c,rows);
        return {auto,wide,all,retained:rows.length,rgb,gray,bit,vivid};
      } finally {c.remove();}
    }""")
    assert result["auto"]["shownRows"] == 16 and result["all"]["shownRows"] == 240
    assert result["retained"] == 240
    assert result["auto"]["cellWidth"] >= 12 and result["auto"]["cellHeight"] >= 10
    assert result["auto"]["rowBytes"] == 60 and result["wide"]["rowBytes"] == 129
    assert result["rgb"] == [255, 0, 0, 255], result
    assert result["gray"] == result["bit"] == [255, 255, 255, 255], result
    assert result["vivid"] == [242, 36, 36, 255], result
    return result



def check_upload_workspace(page, base, artifacts):
    from playwright.sync_api import expect

    posts = []
    def track(request):
        if request.url == base + "/files" and request.method == "POST":
            posts.append(request.url)
    page.on("request", track)
    # Observe bounded reads and inject latency to exercise real seek/file races.
    page.add_init_script("""(() => {
      const original=Blob.prototype.slice;
      window.fileReadStats={slices:[],active:0,maxActive:0,delay:0};
      Blob.prototype.slice=function(start,end,...args) {
        const result=original.call(this,start,end,...args);
        if (!(this instanceof File)) return result;
        const read=result.arrayBuffer.bind(result), stats=window.fileReadStats;
        stats.slices.push({start,end,size:result.size});
        result.arrayBuffer=async () => {
          stats.active++; stats.maxActive=Math.max(stats.maxActive,stats.active);
          try {
            if(stats.delay) await new Promise(resolve=>setTimeout(resolve,stats.delay));
            return await read();
          } finally {stats.active--;}
        };
        return result;
      };
    })();""")
    page.set_viewport_size({"width":1440,"height":1050})
    page.goto(base + "/api-upload.html")
    expect(page.locator("#analysis-size")).to_be_disabled()
    data = bytes(range(256)) * 4096 + bytes(8192)
    def upload(name, payload):
        page.locator("#fileInput").set_input_files({"name":name,"mimeType":"application/octet-stream","buffer":payload})
        page.locator("#submit-upload").click()
    upload("counter-with-zero-tail.bin", data)
    expect(page.locator("#upload-status")).to_have_text("Analysis complete")
    expect(page.locator("#entropy")).to_have_text("8.000000")
    expect(page.locator("#file-sha256")).to_have_text(hashlib.sha256(data).hexdigest())
    page.locator("#server-report summary").click()
    report = json.loads(page.locator("#server-report-content").inner_text())
    assert report["total_bytes"] == report["analyzed_bytes"] == len(data)
    assert report["entropy"] < 8 and report["histogram"][0] == 12288
    assert len(base64.b64decode(report["preview"])) == 2048
    page.locator("#server-report summary").click()
    page.locator("#analysis-size").select_option("1048576")
    page.locator("#fft-size").select_option("65536")
    expect(page.locator("#sample-info")).to_contain_text("[0, 1,048,576)")
    expect(page.locator("#fftvalue")).to_contain_text("65,536 points")
    page.locator("#bit-rates").evaluate("element=>element.open=true")
    for bit in range(8):
        expect(page.locator(f"#rate-{bit}")).to_have_text("50.000%")
    page.locator("#trace-span").select_option("128")
    page.locator("#wave").click(position={"x":80,"y":60})
    page.locator("#wave").click(position={"x":180,"y":60})
    expect(page.locator("#marker-reading")).to_contain_text("Δ offset")
    page.locator("#waterfall-bit").select_option("0")
    expect(page.locator("#waterfall-mode")).to_have_value("bit")
    for mode in ["gray","vivid","rgb"]:
        page.locator("#waterfall-mode").select_option(mode)
    page.locator("#fft-window").select_option("rectangular")
    expect(page.locator("#fftvalue")).to_contain_text("rectangular")
    page.locator("#fft-window").select_option("hann")
    # Playback is local and averages successive windows, rather than repeating a preview.
    page.locator("#analysis-size").select_option("4096")
    page.locator("#fft-size").select_option("auto")
    expect(page.locator("#fftvalue")).to_contain_text("4,096 points")
    page.evaluate("fileReadStats.delay=100")
    page.locator("#next-window").click()
    page.locator("#fft-window").select_option("rectangular")
    expect(page.locator("#sample-info")).to_contain_text("[4,096, 8,192)")
    expect(page.locator("#fftvalue")).to_contain_text("rectangular")
    page.evaluate("fileReadStats.delay=0")
    page.locator("#previous-window").click()
    expect(page.locator("#sample-info")).to_contain_text("[0, 4,096)")
    page.locator("#fft-window").select_option("hann")
    page.locator("#fft-average").select_option("4")
    page.locator("#play-file").click()
    expect(page.locator("#fftvalue")).to_contain_text("4 window(s)")
    page.locator("#play-file").click()
    wait_paint(page)
    frozen = page.locator("#sample-info").inner_text()
    page.wait_for_timeout(350)
    assert page.locator("#sample-info").inner_text() == frozen
    assert len(posts) == 1, "Plot controls/playback must not upload again"
    desktop = assert_layout(page, 2)
    page.screenshot(path=str(artifacts / "upload-desktop.png"),full_page=True)
    for width in [320,390,768]:
        page.set_viewport_size({"width":width,"height":844})
        wait_paint(page)
        assert_layout(page, 1)
    page.set_viewport_size({"width":390,"height":844})
    wait_paint(page)
    page.screenshot(path=str(artifacts / "upload-mobile.png"),full_page=True)
    page.set_viewport_size({"width":1440,"height":1050})
    # The final windows are zeros, not the API's counter-prefix preview.
    set_value(page,"analysis-offset",1048576)
    expect(page.locator("#entropy")).to_have_text("0.000000")
    expect(page.locator("#one-count")).to_contain_text("0 (0.000%)")
    expect(page.locator("#sample-info")).to_contain_text("[1,048,576, 1,052,672)")
    page.locator("#play-file").click()
    expect(page.locator("#sample-info")).to_contain_text("[1,052,672, 1,056,768)")
    expect(page.locator("#play-file")).to_have_attribute("aria-pressed","false")
    expect(page.locator("#upload-status")).to_have_text("Analysis complete")
    expect(page.locator("#next-window")).to_be_disabled()
    page.locator("#previous-window").click()
    expect(page.locator("#sample-info")).to_contain_text("[1,048,576, 1,052,672)")
    # Only the latest seek survives a slow local read, and reads never overlap.
    before = page.evaluate("fileReadStats.slices.length")
    page.evaluate("fileReadStats.delay=200")
    for offset in [131072,262144,393216]:
        set_value(page,"analysis-offset",offset)
    expect(page.locator("#sample-info")).to_contain_text("[393,216, 397,312)")
    slices = page.evaluate("fileReadStats.slices")[before:]
    assert [s["start"] for s in slices] == [131072,393216], slices
    set_value(page,"analysis-offset",655360)
    upload("ones.bin", bytes([255])*4096)
    expect(page.locator("#upload-status")).to_have_text("Analysis complete")
    expect(page.locator("#file-sha256")).to_have_text(hashlib.sha256(bytes([255])*4096).hexdigest())
    expect(page.locator("#one-count")).to_contain_text("32,768 (100.000%)")
    expect(page.locator("#trace-reading")).to_contain_text("255 · 0xff")
    reads = page.evaluate("fileReadStats")
    assert reads["maxActive"] == 1 and max(s["size"] for s in reads["slices"]) <= 1048576, reads
    page.evaluate("fileReadStats.delay=0")
    # A one-byte file still has meaningful statistics and a correctly unavailable FFT.
    upload("single-byte.bin", bytes([128]))
    expect(page.locator("#upload-status")).to_have_text("Analysis complete")
    expect(page.locator("#fftvalue")).to_contain_text("at least 32 bytes")
    expect(page.locator("#entropy")).to_have_text("0.000000")
    expect(page.locator("#previous-window")).to_be_disabled()
    expect(page.locator("#next-window")).to_be_disabled()
    page.locator("#play-file").click()
    expect(page.locator("#sample-info")).to_contain_text("FILE WINDOW #2")
    expect(page.locator("#play-file")).to_have_attribute("aria-pressed","false")
    assert len(posts) == 3
    # Client limits, aborts and server errors leave no stale hash/plots behind.
    for size in [0,32*1024*1024+1]:
        page.locator("#fileInput").evaluate("""(input,size)=>{
          const transfer=new DataTransfer();
          transfer.items.add(new File([new Uint8Array(size)],'invalid.bin'));
          input.files=transfer.files;
        }""",size)
        page.locator("#submit-upload").click()
        expect(page.locator("#upload-status")).to_contain_text("at most 32 MiB")
        assert len(posts) == 3
    pending = []
    page.route("**/files",lambda route:pending.append(route))
    upload("cancel.bin",bytes(4096))
    expect(page.locator("#cancel-upload")).to_be_enabled()
    page.locator("#cancel-upload").click()
    expect(page.locator("#upload-status")).to_have_text("Upload canceled")
    expect(page.locator("#file-sha256")).to_have_text("—")
    expect(page.locator("#entropy")).to_have_text("—")
    expect(page.locator("#analysis-size")).to_be_disabled()
    for route in pending:
        route.abort()
    page.unroute("**/files")
    page.route("**/files",lambda route:route.fulfill(status=503,body="fixture busy"))
    upload("busy.bin",bytes(4096))
    expect(page.locator("#upload-status")).to_contain_text("HTTP 503")
    expect(page.locator("#file-sha256")).to_have_text("—")
    expect(page.locator("#analysis-size")).to_be_disabled()
    page.unroute("**/files")
    upload("recovered.bin",bytes(range(256))*16)
    expect(page.locator("#upload-status")).to_have_text("Analysis complete")
    expect(page.locator("#entropy")).to_have_text("8.000000")
    page.remove_listener("request",track)
    return {"desktop":desktop,"maximum_read_bytes":max(s["size"] for s in reads["slices"]),
        "maximum_concurrent_reads":reads["maxActive"],"checks":"full SHA, separate server/window scopes, large FFT, EOF/pause/playback/averaging, local-only views, seek/file replacement races, size limits, abort/error/recovery, desktop/mobile"}


def run_browser(base, artifacts, chromium, upload_only=False):
    from playwright.sync_api import expect, sync_playwright

    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(executable_path=chromium, headless=True,
            args=["--no-sandbox", "--disable-dev-shm-usage"])
        context = browser.new_context(viewport={"width": 1440, "height": 1050}, device_scale_factor=2)
        page = context.new_page()
        errors = []
        page.on("pageerror", lambda error: errors.append(str(error)))
        if upload_only:
            results = check_upload_workspace(page, base, artifacts)
            assert not errors, errors
            browser.close()
            return {"upload":results,"browser_errors":errors}
        page.goto(base + "/api-stream.html?bytes=1048576&refresh=50")
        page.wait_for_function("document.getElementById('analysed-frames').textContent !== '0'")
        page.locator("#analysis-size").select_option("1048576")
        page.locator("#fft-size").select_option("65536")
        expect(page.locator("#fftvalue")).to_contain_text("65,536 points")
        expect(page.locator("#sample-info")).to_contain_text("1,048,576) analysed")
        expect(page.locator("#waterfall-bit")).to_be_enabled()
        # Open and use the actual native dropdown while RGB is selected.
        page.locator("#waterfall-bit").click()
        page.keyboard.press("ArrowDown")
        page.keyboard.press("Enter")
        expect(page.locator("#waterfall-bit")).to_have_value("6")
        expect(page.locator("#waterfall-mode")).to_have_value("bit")
        page.locator("#waterfall-mode").select_option("rgb")
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
        for mode in ["vivid", "gray", "bit", "rgb"]:
            page.locator("#waterfall-mode").select_option(mode)
            wait_paint(page)
            assert page.locator("#waterfall-info").inner_text().startswith(history)
        expect(page.locator("#waterfall-bit")).to_be_enabled()
        page.locator("#waterfall-bit").select_option("0")
        expect(page.locator("#waterfall-mode")).to_have_value("bit")
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
        wait_paint(page)
        assert_layout(page, 2)  # Shared plot rows remain aligned with expanded FFT controls.
        waterfall_pixels = assert_waterfall_pixels(page)
        page.locator("#waterfall-rows").select_option("240")
        wait_paint(page)
        assert page.locator("#waterfall-info").inner_text().startswith(history)
        page.locator("#waterfall-rows").select_option("16")
        for width in [390, 320, 768]:
            page.set_viewport_size({"width": width, "height": 844})
            wait_paint(page)
            assert_layout(page, 1)
        page.set_viewport_size({"width": 390, "height": 844})
        wait_paint(page)
        page.locator("#waterfall-mode").select_option("rgb")
        page.locator("#waterfall-bytes").select_option("auto")
        wait_paint(page)
        page.screenshot(path=str(artifacts / "stream-mobile.png"), full_page=True)
        page.locator("#waterfall-mode").select_option("vivid")
        wait_paint(page)
        page.locator(".waterfall-card").screenshot(path=str(artifacts / "waterfall-vivid-mobile.png"))
        page.locator("#waterfall-mode").select_option("rgb")
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
        expect(fixture.locator("#entropy")).to_have_text("8.000000")
        expect(fixture.locator("#zero-count")).to_contain_text("50.000%")
        expect(fixture.locator("#one-count")).to_contain_text("50.000%")
        expect(fixture.locator("#byte-statistics")).to_contain_text("Mean 127.500")
        fixture.locator("#bit-rates").evaluate("element=>element.open=true")
        for bit in range(8):
            expect(fixture.locator(f"#rate-{bit}")).to_have_text("50.000%")
        fixture.close()
        upload_results = check_upload_workspace(page, base, artifacts)
        page.goto(base + "/api-bytes.html?bytes=256&refresh=100")
        page.wait_for_function("document.getElementById('base64').textContent.length > 4")
        base64 = page.locator("#base64").evaluate("element => ({value:element.textContent,align:getComputedStyle(element).textAlign,decoded:atob(element.textContent).length})")
        assert base64["decoded"] == 256 and base64["align"] == "justify"
        assert not re.search(r"\s", base64["value"]), "Layout must not modify copyable Base64"
        assert not page.evaluate("document.documentElement.scrollWidth > innerWidth")
        page.screenshot(path=str(artifacts / "bytes-mobile.png"), full_page=True)
        page.goto(base + "/api-stream.html?bytes=1&refresh=50")
        expect(page.locator("#fftvalue")).to_contain_text("at least 32 bytes")
        expect(page.locator("#entropy")).to_have_text("0.000000")
        # Query validation must not start a nonsensical transport.
        page.goto(base + "/api-stream.html?bytes=1&bytes=2")
        expect(page.locator("#stream-notice")).to_have_attribute("data-state", "error")
        assert not errors, errors
        browser.close()
        return {"desktop": desktop, "bounded_history": bounded, "waterfall_pixels": waterfall_pixels, "upload": upload_results, "browser_errors": errors,
            "checks": "native bit dropdown, aligned card borders and plot rows, enlarged/adaptive waterfall cells, raw/palette pixel fidelity, pause/reception, frozen reanalysis, trace A/B, power average, all colour modes, large/short FFT, offsets, 320/390/768/1440px, shared upload FFT, exact Base64, invalid query"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--server-binary", type=Path, default=Path("bin/entropy-server"))
    parser.add_argument("--artifacts", type=Path, required=True)
    parser.add_argument("--chromium", default="/usr/bin/chromium")
    parser.add_argument("--upload-only", action="store_true", help="Run the file workspace checks only")
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
            results = run_browser(base, args.artifacts, args.chromium, args.upload_only)
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
