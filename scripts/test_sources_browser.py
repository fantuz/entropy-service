#!/usr/bin/env python3
"""Real Chromium source/socket lab checks with fake media and local fixtures."""
import argparse
import contextlib
import json
import http.server
from pathlib import Path
import signal
import socket
import socketserver
import subprocess
import tempfile
import threading
import time
import urllib.request

from test_stream_browser import assert_layout, number, set_value, wait_paint


def wait_meters(page, **counts):
    page.wait_for_function(r"""counts=>Object.entries(counts).every(([id,count])=>
        Number(document.getElementById(id).textContent.replace(/\D/g,''))===count)""",arg=counts)


class HTTPFixture(http.server.BaseHTTPRequestHandler):
    body=b"HTTP body sample\n"*1024

    def log_message(self,*args):pass

    def do_GET(self):
        if self.path in ["/cors","/redirect","/created"]:
            self.send_response(302 if self.path=="/redirect" else 201 if self.path=="/created" else 200)
            self.send_header("Access-Control-Allow-Origin","*")
            if self.path!="/cors":self.send_header("Location","/cors" if self.path=="/redirect" else "/must-not-follow")
        else:self.send_response(200)
        self.send_header("Content-Type","text/plain");self.end_headers()
        assert self.path!="/must-not-follow","201 Location was followed"
        with contextlib.suppress(BrokenPipeError,ConnectionResetError):self.wfile.write(b"created" if self.path=="/created" else self.body)


def free_port(kind=socket.SOCK_STREAM):
    with socket.socket(socket.AF_INET,kind) as connection:
        connection.bind(("127.0.0.1",0))
        return connection.getsockname()[1]


def serve_process(binary, port, log, extra=()):
    process=subprocess.Popen([str(binary),"-device=/dev/urandom","-require-device=true","-enable-https=false",f"-http=127.0.0.1:{port}",*extra],stdout=log,stderr=subprocess.STDOUT)
    for _ in range(200):
        if process.poll() is not None:
            raise RuntimeError("test server exited during startup")
        try:
            urllib.request.urlopen(f"http://127.0.0.1:{port}/live",timeout=1).close()
            return process
        except OSError:
            time.sleep(.05)
    process.terminate();process.wait(timeout=10)
    raise RuntimeError("test server did not become live")


class BytesHandler(socketserver.BaseRequestHandler):
    def handle(self):
        try:
            while True:
                self.request.sendall(bytes(range(256))*256)
                time.sleep(.02)
        except OSError:
            pass


class FixtureTCP(socketserver.ThreadingTCPServer):
    daemon_threads=True
    allow_reuse_address=True


def registration_checks(page, artifacts):
    from playwright.sync_api import expect

    page.set_viewport_size({"width":1440,"height":1000})
    page.locator("#lab-create-source").click();dialog=page.locator("#lab-registration")
    field=lambda name:dialog.locator(f'[name="{name}"]')
    draft=lambda:json.loads(dialog.locator("#registration-json").input_value())["sources"]
    field("id").fill("ws-draft");field("kind").select_option("websocket")
    field("endpoint").fill("http://127.0.0.1:8081/stream");dialog.locator('#registration-submit').click()
    expect(dialog.locator("#registration-status")).to_contain_text("ws://")
    expect(field("endpoint")).to_have_attribute("aria-invalid","true")
    expect(dialog.locator("#registration-error-endpoint")).to_be_visible()
    assert "camera" not in dialog.locator("#registration-status").text_content().lower()
    expect(dialog.locator("#registration-camera-note")).to_be_hidden()
    dialog.locator("#registration-use-example").click()
    expect(field("endpoint")).to_have_value("ws://127.0.0.1:8081/stream?bytes=4096&refresh=500")
    dialog.locator('#registration-submit').click();expect(dialog.locator("#registration-status")).to_contain_text("not active yet")
    expect(dialog.locator("#registration-draft-state")).to_contain_text("Unsaved changes")
    dialog.get_by_role("button",name="Edit ws-draft",exact=True).click()
    field("id").fill("ws-renamed");field("name").fill("My WebSocket");field("max_bytes").fill("16384")
    dialog.locator('#registration-submit').click()
    assert len(draft())==1 and draft()[0]["id"]=="ws-renamed" and draft()[0]["max_bytes"]==16384
    field("id").fill("ws-renamed");dialog.locator("#registration-use-example").click();dialog.locator('#registration-submit').click()
    expect(dialog.locator("#registration-error-id")).to_contain_text("already exists")
    field("id").fill("page");dialog.locator('#registration-submit').click()
    dialog.get_by_role("button",name="Remove page",exact=True).click();assert len(draft())==1
    field("id").fill("peer");field("kind").select_option("entropy-ws");field("endpoint").fill("ws://127.0.0.1:8081/stream")
    dialog.locator('#registration-submit').click();expect(dialog.locator("#registration-status")).to_contain_text("http://127.0.0.1:8081")
    for kind,example in [("onvif","http://192.168.1.100/onvif/device_service"),("udp","127.0.0.1:9002")]:
        field("kind").select_option(kind);expect(dialog.locator("#registration-example")).to_contain_text(example)
    expect(dialog.locator("#registration-camera-note")).to_be_hidden()
    dialog.locator("#registration-close").click()
    # Unsaved registry/form edits survive a cancelled navigation.
    dialogs=[]
    def reject_navigation(event):dialogs.append(event.type);event.dismiss()
    page.once("dialog",reject_navigation);page.locator(".lab-menu a").click()
    assert dialogs==["beforeunload"] and page.url.endswith("api-socket.html")
    page.once("dialog",lambda event:event.accept());page.locator(".lab-menu a").click()
    page.wait_for_url("**/api-sources.html")
    page.locator("#lab-create-source").click();assert draft()==[]

    def delayed_import():
        page.evaluate('''() => {
            const input=document.getElementById('registration-import');
            const data=JSON.stringify({sources:[{id:'obsolete',kind:'http',endpoint:'https://example.org/'}]});
            const file=new File([data],'delayed.json',{type:'application/json'});
            file.text=()=>new Promise(resolve=>window.finishRegistryImport=()=>resolve(data));
            Object.defineProperty(input,'files',{configurable:true,value:[file]});
            input.dispatchEvent(new Event('change',{bubbles:true}));delete input.files;
        }''')

    delayed_import();dialog.locator("#registration-reset").click();page.evaluate("finishRegistryImport()");wait_paint(page)
    assert draft()==[]
    expect(dialog.locator("#registration-status")).to_have_text("New empty configuration.")
    delayed_import()
    camera={"id":"camera","kind":"onvif","endpoint":"http://camera/onvif/device_service","width":160,"height":120,"timeout_ms":5000}
    dialog.locator("#registration-import").set_input_files({"name":"camera.json","mimeType":"application/json","buffer":json.dumps({"sources":[camera]}).encode()})
    expect(dialog.locator("#registration-status")).to_contain_text("Imported 1")
    page.evaluate("finishRegistryImport()");wait_paint(page)
    assert draft()[0]["id"]=="camera" and draft()[0]["max_bytes"]==57600
    dialog.get_by_role("button",name="Edit camera",exact=True).click()
    expect(field("timeout_ms")).to_have_value("5000")
    field("name").fill("Network camera");dialog.locator('#registration-submit').click()
    assert draft()[0]["timeout_ms"]==5000
    with page.expect_download() as downloaded:dialog.locator("#registration-download").click()
    assert json.loads(Path(downloaded.value.path()).read_text())["sources"]==draft()
    expect(dialog.locator("#registration-draft-state")).to_contain_text("No pending changes")
    # Invalid imports preserve the last good draft; the same file can be retried.
    invalid={**camera,"max_bytes":4096}
    for _ in range(2):
        dialog.locator("#registration-import").set_input_files({"name":"bad.json","mimeType":"application/json","buffer":json.dumps({"sources":[invalid]}).encode()})
        expect(dialog.locator("#registration-status")).to_contain_text("RGB frame size")
        assert draft()[0]["name"]=="Network camera"
    dialog.get_by_role("button",name="Edit camera",exact=True).click()
    dialog.locator(".registration-body").evaluate("node=>node.scrollTop=0")
    dialog.screenshot(path=str(artifacts/"registration-desktop.png"))
    page.set_viewport_size({"width":390,"height":844})
    dialog.locator(".registration-body").evaluate("node=>node.scrollTop=node.scrollHeight")
    expect(dialog.locator("#registration-close")).to_be_in_viewport()
    expect(dialog.locator("#registration-download")).to_be_in_viewport()
    assert dialog.evaluate("node=>node.scrollWidth<=node.clientWidth+1")
    dialog.locator(".registration-body").evaluate("node=>node.scrollTop=0")
    dialog.screenshot(path=str(artifacts/"registration-feedback-mobile.png"))
    dialog.locator("#registration-close").click()


def shannon_precision_checks(browser, base):
    from playwright.sync_api import expect
    for path in ("/api-stream.html", "/api-upload.html", "/api-sources.html", "/api-socket.html"):
        page = browser.new_page()
        try:
            page.goto(base + path)
            page.wait_for_function("Boolean(window.EntropyShannonAlert)")
            selector = page.locator("#entropy-threshold")
            expect(selector).to_have_attribute("step", "0.000001")
            selector.fill("7.123456")
            selector.dispatch_event("change")
            expect(selector).to_have_value("7.123456")
            value = page.evaluate("""() => {
                EntropyShannonAlert.update(7.1234556);
                return {text: document.getElementById('entropy').textContent,
                        state: document.getElementById('entropy-alert-card').dataset.state};
            }""")
            assert value == {"text": "7.123456", "state": "alert"}, (path, value)
            page.reload()
            expect(page.locator("#entropy-threshold")).to_have_value("7.123456")
        finally:
            page.close()


def browser_checks(base, peer, artifacts, chromium, udp_port, http_url, engine="chromium"):
    from playwright.sync_api import expect, sync_playwright
    with sync_playwright() as playwright:
        if engine=="firefox":
            browser=playwright.firefox.launch(headless=True,firefox_user_prefs={"media.navigator.streams.fake":True,"media.navigator.permission.disabled":True,"media.autoplay.default":0,"gfx.webrender.software":True,"layers.acceleration.disabled":True})
        else:
            browser=playwright.chromium.launch(executable_path=chromium or None,headless=True,args=["--no-sandbox","--disable-dev-shm-usage","--use-fake-ui-for-media-stream","--use-fake-device-for-media-stream"])
        shannon_precision_checks(browser, base)
        context=browser.new_context(viewport={"width":1440,"height":1000},device_scale_factor=2,accept_downloads=True)
        context.add_init_script("""(() => {
          window.labTracks=[];window.labAudioTracks=[];window.labMediaRequests=[];window.mediaDelay=0;window.serialClosed=0;
          const media=navigator.mediaDevices;
          if(media){const original=media.getUserMedia.bind(media);media.getUserMedia=async options=>{
            labMediaRequests.push(options);
            const delay=mediaDelay,stream=await original(options);labTracks.push(...stream.getTracks());
            const audioTracks=stream.getAudioTracks.bind(stream);
            stream.getAudioTracks=()=>{
              const tracks=audioTracks();labAudioTracks.push(...tracks);
              for(const track of tracks){
                const add=track.addEventListener.bind(track);
                // Firefox ignores synthetic media events unless this
                // test-only listener opts in. Hardware events are trusted.
                // Other engines ignore the optional fourth argument.
                track.addEventListener=(type,listener,options)=>add(type,listener,options,type==='ended');
              }
              return tracks;
            };
            if(delay)await new Promise(resolve=>setTimeout(resolve,delay));return stream;
          };}
          Object.defineProperty(navigator,'serial',{configurable:true,value:{requestPort:async()=>({
            open:async()=>{},close:async()=>{serialClosed++;},
            readable:new ReadableStream({pull(c){c.enqueue(Uint8Array.of(1,2,3,4));},cancel(){}})
          })}});
        })();""")
        page=context.new_page();errors=[];page.on("pageerror",lambda error:errors.append(str(error)))
        page.goto(base+"/api-sources.html")
        page.locator("#lab-kind").select_option("counter")
        page.locator("#lab-bytes").fill("4096")
        page.locator("#lab-once").click()
        expect(page.locator("#lab-download")).to_be_enabled()
        expect(page.locator("#entropy")).to_have_text("8.000000")
        wait_meters(page,**{"lab-input-frames":1,"lab-analysis-frames":1})
        with page.expect_download() as downloaded:
            page.locator("#lab-download").click()
        assert Path(downloaded.value.path()).read_bytes()==bytes(range(256))*16
        page.locator("#lab-record").click()
        page.locator("#lab-kind").select_option("constant")
        page.locator("#lab-once").click()
        expect(page.locator("#lab-download")).to_be_enabled()
        expect(page.locator("#entropy")).to_have_text("0.000000")
        page.locator("#lab-record").click()
        assert page.locator("#lab-measurements tbody tr").count()==2
        expect(page.locator("#lab-measurements tbody tr").nth(0).locator("td").nth(3)).to_have_text("8.000000")
        expect(page.locator("#lab-measurements tbody tr").nth(1).locator("td").nth(3)).to_have_text("0.000000")
        with page.expect_download() as exported:
            page.locator("#lab-export").click()
        records=json.loads(Path(exported.value.path()).read_text())
        assert [record["statistics"]["entropy"] for record in records["records"]]==[8,0]
        expect(page.locator("#lab-input-unit")).to_have_text("sample")
        expect(page.locator("#lab-analysis-unit")).to_have_text("window")
        page.locator("#lab-kind").select_option("counter")
        page.locator("#lab-transform").select_option("lsb")
        page.locator("#lab-once").click();expect(page.locator("#lab-download")).to_be_enabled()
        expect(page.locator("#entropy")).to_have_text("1.000000")
        page.locator("#lab-transform").select_option("xor");page.locator("#lab-refresh").fill("20")
        page.locator("#lab-once").click();expect(page.locator("#lab-download")).to_be_enabled()
        expect(page.locator("#entropy")).to_have_text("0.000000")
        wait_meters(page,**{"lab-input-frames":2,"lab-analysis-frames":1})
        page.locator("#lab-transform").select_option("raw")
        page.locator("#lab-start").click()
        page.wait_for_function("Number(document.getElementById('lab-input-frames').textContent.replaceAll(',',''))>5")
        page.locator("#pause-plots").click();wait_paint(page);page.wait_for_timeout(200)
        frames=number(page,"lab-input-frames");analyzed=number(page,"lab-analysis-frames");frozen=page.locator("#sample-info").inner_text()
        page.wait_for_timeout(250)
        assert number(page,"lab-input-frames")>frames and number(page,"lab-analysis-frames")==analyzed
        assert page.locator("#sample-info").inner_text()==frozen
        page.locator("#lab-record").click()
        with page.expect_download() as paused_export:page.locator("#lab-export").click()
        paused_record=json.loads(Path(paused_export.value.path()).read_text())["records"][-1]
        assert paused_record["frozen"] and paused_record["sampleAgeMs"]>=200
        assert paused_record["capturedAt"]<paused_record["recordedAt"]==paused_record["timestamp"]
        assert paused_record["meter"]["scope"]=="source-session" and paused_record["meter"]["measuredAt"]==paused_record["recordedAt"]
        assert paused_record["meter"]["frames"]>paused_record["window"]["frame"]
        page.locator("#pause-plots").click()
        page.wait_for_function("Number(document.getElementById('lab-analysis-frames').textContent.replaceAll(',',''))>"+str(analyzed))
        page.locator("#lab-stop").click();expect(page.locator("#lab-start")).to_be_enabled()
        desktop=assert_layout(page,2)
        page.screenshot(path=str(artifacts/"sources-desktop.png"),full_page=True)
        for width in [320,390,768]:
            page.set_viewport_size({"width":width,"height":844});wait_paint(page);assert_layout(page,1)
        page.set_viewport_size({"width":390,"height":844});wait_paint(page)
        page.screenshot(path=str(artifacts/"sources-mobile.png"),full_page=True)
        caption=page.locator("#lab-measurements caption").bounding_box()
        table=page.locator("#lab-measurements").bounding_box()
        assert caption["width"]>=table["width"]-2 and caption["height"]<50,(caption,table)
        page.locator("#lab-measurements").screenshot(path=str(artifacts/"measurements-mobile.png"))
        page.set_viewport_size({"width":1440,"height":1000})
        page.locator("#lab-kind").select_option("pattern")
        page.locator("#lab-pattern").fill("zz")
        page.locator("#lab-once").click();expect(page.locator("#lab-status")).to_have_attribute("data-state","error")
        page.locator("#lab-kind").select_option("crypto")
        page.locator("#lab-once").click();expect(page.locator("#lab-download")).to_be_enabled()
        page.locator("#lab-kind").select_option("pattern");page.locator("#lab-pattern").fill("00ff")
        # One large sample verifies the expanded browser cap. Analysis stays
        # on its existing bounded window; normal sample defaults stay small.
        page.locator("#lab-kind").select_option("crypto")
        page.locator("#lab-transform").select_option("raw")
        page.locator("#lab-bytes").fill("8388608")
        page.locator("#lab-once").click()
        expect(page.locator("#lab-download")).to_be_enabled()
        wait_meters(page,**{"lab-input-bytes":8388608})
        page.locator("#lab-bytes").fill("4096")
        for kind in ["crypto","pattern","camera","microphone","serial"]:
            print(f"{engine}: capture {kind}",flush=True)
            page.locator("#lab-kind").select_option(kind);page.locator("#lab-once").click()
            try:
                expect(page.locator("#lab-download")).to_be_enabled(timeout=15000)
            except AssertionError:
                print("Capture state:",page.locator("#lab-status").inner_text(),page.evaluate("({requests:labMediaRequests,tracks:labTracks.map(t=>({kind:t.kind,state:t.readyState}))})"),flush=True)
                page.screenshot(path=str(artifacts/f"{kind}-failure.png"))
                raise
            if kind in ["camera","microphone"]:
                page.wait_for_function("labTracks.every(track=>track.readyState==='ended')")
                expect(page.locator("#lab-video")).to_be_hidden()
            if kind=="serial":
                page.wait_for_function("serialClosed>0")
                expect(page.locator("#lab-input-bytes")).to_have_text("4")
        # Discovery never captures; a removed exact input cannot fall back to
        # the microphone. Device names and actual PCM capture use fake media.
        captures=page.evaluate("labMediaRequests.length")
        page.locator("#lab-kind").select_option("microphone")
        page.wait_for_function("document.querySelectorAll('#lab-audio-device option').length>1")
        assert page.evaluate("labMediaRequests.length")==captures
        device=page.locator("#lab-audio-device option").nth(1).get_attribute("value")
        page.locator("#lab-audio-device").select_option(device)
        page.locator("#lab-once").click()
        expect(page.locator("#lab-download")).to_be_enabled()
        assert page.evaluate("labMediaRequests.at(-1).audio.deviceId.exact")==device
        assert page.evaluate("labTracks.every(track=>track.readyState==='ended')")
        expect(page.locator("#lab-source-info")).to_contain_text("PCM16LE mono")
        page.evaluate("""device=>{
            window.restoreAudioInputs=navigator.mediaDevices.enumerateDevices.bind(navigator.mediaDevices);
            navigator.mediaDevices.enumerateDevices=async()=> (await restoreAudioInputs()).filter(input=>input.deviceId!==device);
        }""",device)
        page.locator("#lab-audio-refresh").click()
        expect(page.locator("#lab-audio-device option:checked")).to_have_text("Selected input unavailable")
        captures=page.evaluate("labMediaRequests.length")
        page.locator("#lab-start").click()
        expect(page.locator("#lab-status")).to_contain_text("unavailable")
        assert page.evaluate("labMediaRequests.length")==captures
        page.evaluate("navigator.mediaDevices.enumerateDevices=restoreAudioInputs")
        page.locator("#lab-audio-refresh").click()
        expect(page.locator("#lab-audio-device option:checked")).not_to_have_text("Selected input unavailable")
        page.locator("#lab-start").click()
        expect(page.locator("#lab-status")).to_contain_text("Source active")
        # End the same audio-track object delivered to the adapter. The generic
        # track log also contains previous captures and camera tracks.
        page.evaluate("""() => {
            const track=labAudioTracks.findLast(track=>track.readyState==='live');
            if(!track)throw new Error('Expected a live selected audio input');
            track.stop();
            track.dispatchEvent(new Event('ended'));
        }""")
        expect(page.locator("#lab-status")).to_contain_text("disconnected")
        assert page.evaluate("labTracks.every(track=>track.readyState==='ended')")
        page.set_viewport_size({"width":390,"height":844})
        assert not page.evaluate("document.documentElement.scrollWidth>innerWidth")
        page.locator("#lab-audio-device").scroll_into_view_if_needed()
        page.screenshot(path=str(artifacts/"audio-input-mobile.png"))
        page.set_viewport_size({"width":1440,"height":1000})
        page.locator("#lab-audio-device").select_option("")
        page.evaluate("mediaDelay=250")
        captures=page.evaluate("labMediaRequests.length")
        page.locator("#lab-start").click()
        page.wait_for_function("count=>labMediaRequests.length>count",arg=captures)
        page.locator("#lab-stop").click();page.wait_for_timeout(500)
        assert page.evaluate("labTracks.every(track=>track.readyState==='ended')")
        page.evaluate("mediaDelay=0")
        # Stop while camera permission is unresolved; late tracks must close.
        page.locator("#lab-kind").select_option("camera")
        page.locator("#lab-show-preview").check()
        page.evaluate("mediaDelay=250")
        page.locator("#lab-start").click();page.locator("#lab-stop").click()
        page.evaluate("mediaDelay=0")
        page.locator("#lab-start").click()
        expect(page.locator("#lab-video")).to_be_visible()
        page.wait_for_timeout(600)
        expect(page.locator("#lab-video")).to_be_visible()
        page.locator("#lab-stop").click()
        assert page.evaluate("labTracks.every(track=>track.readyState==='ended')")
        expect(page.locator("#lab-video")).to_be_hidden()
        page.locator("#lab-kind").select_option("file")
        page.locator("#lab-file").set_input_files({"name":"counter.bin","mimeType":"application/octet-stream","buffer":bytes(range(256))*40})
        page.locator("#lab-start").click()
        expect(page.locator("#lab-start")).to_be_enabled()
        expect(page.locator("#lab-download")).to_be_enabled()
        wait_meters(page,**{"lab-input-bytes":10240,"lab-input-frames":3})
        expect(page.locator("#sample-info")).to_contain_text("sample #3")
        page.locator("#lab-refresh").fill("200")
        page.locator("#lab-start").click();expect(page.locator("#lab-download")).to_be_enabled()
        page.locator("#pause-plots").click();wait_paint(page)
        frozen=page.locator("#sample-info").inner_text()
        expect(page.locator("#lab-start")).to_be_enabled()
        assert page.locator("#sample-info").inner_text()==frozen
        expect(page.locator("#lab-status")).to_contain_text("plots frozen")
        # Socket page: the same instance, another instance and configured readers.
        page.goto(base+"/api-socket.html")
        page.locator("#lab-bytes").fill("4096")
        for kind in ["self-ws","self-http"]:
            page.locator("#lab-kind").select_option(kind);page.locator("#lab-once").click()
            expect(page.locator("#lab-download")).to_be_enabled()
            wait_meters(page,**{"lab-input-bytes":4096})
        # A browser-origin peer connection follows the upstream policy; use the
        # server bridge to connect peers without weakening that Origin policy.
        page.locator("#lab-kind").select_option("peer-ws");page.locator("#lab-url").fill(peer)
        page.locator("#lab-once").click();expect(page.locator("#lab-status")).to_have_attribute("data-state","error")
        page.locator("#lab-kind").select_option("bridge")
        page.wait_for_function("document.getElementById('lab-registered').options.length>=5")
        for source in ["peer-ws","peer-http","self-ws","raw-ws","device","tcp","unix"]:
            page.locator("#lab-registered").select_option(source);page.locator("#lab-once").click()
            expect(page.locator("#lab-download")).to_be_enabled(timeout=15000)
            wait_meters(page,**{"lab-input-bytes":4096})
            expect(page.locator("#sample-info")).to_contain_text(source)
        page.locator("#lab-registered").select_option("udp")
        page.locator("#lab-start").click()
        expect(page.locator("#lab-transport-info")).to_contain_text("UDP listener bound")
        with socket.socket(socket.AF_INET,socket.SOCK_DGRAM) as sender:
            sender.sendto(bytes(range(256))*4,("127.0.0.1",udp_port))
        expect(page.locator("#lab-download")).to_be_enabled();page.locator("#lab-stop").click()
        expect(page.locator("#entropy")).to_have_text("8.000000")
        page.locator("#lab-record").click()
        assert page.locator("#lab-measurements tbody tr").count()==4
        assert_layout(page,2);page.screenshot(path=str(artifacts/"socket-desktop.png"),full_page=True)
        page.set_viewport_size({"width":390,"height":844});wait_paint(page);assert_layout(page,1)
        page.screenshot(path=str(artifacts/"socket-mobile.png"),full_page=True)
        page.set_viewport_size({"width":1440,"height":1000})
        # A short, live TCP producer must advance without filling 64 KiB or closing.
        page.locator("#lab-registered").select_option("tcp-short");page.locator("#lab-bytes").fill("65536")
        page.locator("#lab-once").click();expect(page.locator("#lab-download")).to_be_enabled(timeout=2000)
        assert 0<number(page,"lab-input-bytes")<65536 and number(page,"lab-input-frames")==1
        expect(page.locator("#lab-transport-info")).to_contain_text("TCP producer connected")
        # Metadata stays separate from binary sampling; oversized WS frames give actionable feedback.
        page.locator("#lab-registered").select_option("raw-ws");page.locator("#lab-bytes").fill("128")
        page.locator("#lab-once").click();expect(page.locator("#lab-status")).to_have_attribute("data-state","error")
        expect(page.locator("#lab-status")).to_contain_text("Sample bytes")
        page.locator("#lab-kind").select_option("http");page.locator("#lab-http-route").select_option("registered")
        page.locator("#lab-bytes").fill("1024")
        for source in ["created","redirect"]:
            page.locator("#lab-registered").select_option(source);page.locator("#lab-once").click();expect(page.locator("#lab-download")).to_be_enabled()
            expect(page.locator("#lab-transport-info")).to_contain_text("201 Created" if source=="created" else "1 redirect")
            if source=="created":expect(page.locator("#lab-transport-info")).to_contain_text("not a redirect")
        registration_checks(page,artifacts)
        # No startup registry: direct CORS-enabled pages/redirects work; CORS denial is explained.
        page.goto(peer+"/api-socket.html")
        expect(page.locator("#lab-catalog-info")).to_contain_text("No registered sources are active")
        page.locator("#lab-kind").select_option("http");page.locator("#lab-bytes").fill("1024")
        assert page.locator("#lab-page-preset option").count()==8
        page.locator("#lab-url").fill(http_url+"/redirect");page.locator("#lab-once").click();expect(page.locator("#lab-download")).to_be_enabled()
        expect(page.locator("#lab-transport-info")).to_contain_text("redirect followed")
        page.locator("#lab-url").fill(http_url+"/created");page.locator("#lab-once").click();expect(page.locator("#lab-download")).to_be_enabled()
        expect(page.locator("#lab-transport-info")).to_contain_text("201 Created")
        page.locator("#lab-url").fill(http_url+"/no-cors");page.locator("#lab-once").click();expect(page.locator("#lab-status")).to_have_attribute("data-state","error")
        expect(page.locator("#lab-route-help")).to_contain_text("CORS")
        page.locator("#lab-http-route").select_option("registered")
        expect(page.locator("#lab-route-help")).to_contain_text("No HTTP sources are active")
        page.locator("#lab-once").click();expect(page.locator("#lab-status")).to_contain_text("-lab-sources=lab-sources.json")
        assert not errors,errors
        browser.close()
        return {"browser_errors":errors,"desktop":desktop,"comparison_records":len(records["records"]),
            "checks":"generators, exact binary/JSON downloads, XOR/LSB, metering/pause, media/serial release, late permission cancellation, file EOF, self/peer HTTP+WS, registered TCP/UDP/Unix/device, short TCP reads, UDP readiness, WebSocket schema feedback, 201/redirects, CORS without a registry, Origin policy, desktop/mobile"}


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--server-binary",type=Path,default=Path("bin/entropy-server"))
    parser.add_argument("--artifacts",type=Path,required=True)
    parser.add_argument("--chromium",default="/usr/bin/chromium")
    parser.add_argument("--browser",choices=["chromium","firefox"],default="chromium")
    args=parser.parse_args();args.artifacts.mkdir(parents=True,exist_ok=True)
    binary=args.server_binary.resolve();a,b,udp=free_port(),free_port(),free_port(socket.SOCK_DGRAM)
    with tempfile.TemporaryDirectory(prefix="entropy-lab-",dir="/tmp") as temp,contextlib.ExitStack() as stack:
        web=http.server.ThreadingHTTPServer(("127.0.0.1",0),HTTPFixture)
        stack.callback(web.server_close);stack.callback(web.shutdown);threading.Thread(target=web.serve_forever,daemon=True).start()
        http_url=f"http://127.0.0.1:{web.server_port}"
        short_port=free_port()
        fixture_log=stack.enter_context((args.artifacts/"socket-fixture.log").open("w"))
        producer=subprocess.Popen(["python3",str(Path(__file__).with_name("lab_socket_fixture.py")),"tcp","--port",str(short_port),"--interval","0.1"],stdout=fixture_log,stderr=fixture_log)
        def stop_producer():
            if producer.poll() is None:producer.terminate()
            producer.wait(timeout=5)
        stack.callback(stop_producer)
        for _ in range(100):
            try:
                with socket.create_connection(("127.0.0.1",short_port),timeout=.1):break
            except OSError:time.sleep(.02)
        else:raise RuntimeError("TCP demo producer failed to listen")
        tcp=FixtureTCP(("127.0.0.1",0),BytesHandler);stack.callback(tcp.server_close);stack.callback(tcp.shutdown)
        threading.Thread(target=tcp.serve_forever,daemon=True).start()
        unix_path=str(Path(temp)/"source.sock")
        unix=socketserver.ThreadingUnixStreamServer(unix_path,BytesHandler);unix.daemon_threads=True
        stack.callback(unix.server_close);stack.callback(unix.shutdown)
        threading.Thread(target=unix.serve_forever,daemon=True).start()
        config={"sources":[
            {"id":"peer-ws","kind":"entropy-ws","endpoint":f"http://127.0.0.1:{b}"},
            {"id":"peer-http","kind":"entropy-http","endpoint":f"http://127.0.0.1:{b}"},
            {"id":"self-ws","kind":"entropy-ws","endpoint":f"http://127.0.0.1:{a}"},
            {"id":"raw-ws","kind":"websocket","endpoint":f"ws://127.0.0.1:{b}/stream?bytes=4096&refresh=20"},
            {"id":"tcp-short","kind":"tcp","endpoint":f"127.0.0.1:{short_port}"},
            {"id":"created","kind":"webpage","endpoint":http_url+"/created"},
            {"id":"redirect","kind":"webpage","endpoint":http_url+"/redirect"},
            {"id":"device","kind":"device","endpoint":"/dev/urandom"},
            {"id":"tcp","kind":"tcp","endpoint":f"127.0.0.1:{tcp.server_address[1]}"},
            {"id":"udp","kind":"udp","endpoint":f"127.0.0.1:{udp}"},
            {"id":"unix","kind":"unix","endpoint":unix_path}
        ]}
        config_path=Path(temp)/"sources.json";config_path.write_text(json.dumps(config))
        processes=[]
        try:
            for port,extra,name in [(b,(),"peer"),(a,("-lab-sources="+str(config_path),),"server")]:
                log=stack.enter_context((args.artifacts/(name+".log")).open("w"))
                processes.append(serve_process(binary,port,log,extra))
            results=browser_checks(f"http://127.0.0.1:{a}",f"http://127.0.0.1:{b}",args.artifacts,args.chromium,udp,http_url,args.browser)
        finally:
            for process in processes:process.send_signal(signal.SIGTERM)
            for process in processes:process.wait(timeout=10)
        assert all(process.returncode==0 for process in processes)
    results["shutdown"]={}
    for name in ["peer","server"]:
        log=(args.artifacts/(name+".log")).read_text()
        assert "drain=complete" in log
        results["shutdown"][name]=[line for line in log.splitlines() if "shutdown summary" in line or "shutdown connections" in line]
    (args.artifacts/"source-results.json").write_text(json.dumps(results,indent=2)+"\n")
    print(json.dumps(results,indent=2))


if __name__=="__main__":
    main()
