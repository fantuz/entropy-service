#!/usr/bin/env python3
"""Optional authenticated RTSP/ONVIF + HTTP browser checks with local fixtures.

Requires FFmpeg and an operator-supplied MediaMTX binary. No physical camera or
LAN scan is used. All credentials in this script are synthetic fixture values.
"""
import argparse
import base64
import contextlib
import hashlib
import http.server
import json
from pathlib import Path
import signal
import socket
import subprocess
import tempfile
import threading
import time
import xml.etree.ElementTree as ET

from test_sources_browser import free_port, serve_process
from test_stream_browser import assert_layout, number, wait_paint

USER="fixture-user"
PASSWORD="fixture-p@ss$&word!"
BODY=b"<html><script>window.remoteMarkupExecuted=true</script><p>sample page</p>"*2000


class WebFixture(http.server.BaseHTTPRequestHandler):
    rtsp_port=0
    soap_calls=0

    def log_message(self,*args):
        pass

    def do_GET(self):
        if self.path=="/redirect":
            self.send_response(302);self.send_header("Location","/page");self.end_headers();return
        self.send_response(200)
        if self.path=="/cors":self.send_header("Access-Control-Allow-Origin","*")
        self.send_header("Content-Type","text/html; charset=utf-8")
        self.end_headers()
        with contextlib.suppress(BrokenPipeError,ConnectionResetError):self.wfile.write(BODY)

    def do_POST(self):
        data=self.rfile.read(int(self.headers["Content-Length"]))
        root=ET.fromstring(data)
        values={node.tag.rsplit("}",1)[-1]:node.text for node in root.iter()}
        expected=base64.b64encode(hashlib.sha1(base64.b64decode(values["Nonce"])+values["Created"].encode()+PASSWORD.encode()).digest()).decode()
        assert values["Username"]==USER and values["Password"]==expected and PASSWORD.encode() not in data
        type(self).soap_calls+=1
        if b"GetCapabilities" in data:body=f'<GetCapabilitiesResponse><Capabilities><Media><XAddr>http://127.0.0.1:{self.server.server_port}/media</XAddr></Media></Capabilities></GetCapabilitiesResponse>'
        elif b"GetProfiles" in data:body='<GetProfilesResponse><Profiles token="fixture-profile"/></GetProfilesResponse>'
        else:body=f'<GetStreamUriResponse><MediaUri><Uri>rtsp://127.0.0.1:{self.rtsp_port}/lab</Uri></MediaUri></GetStreamUriResponse>'
        self.send_response(200);self.send_header("Content-Type","application/soap+xml");self.end_headers();self.wfile.write(f'<Envelope><Body>{body}</Body></Envelope>'.encode())


def browser_checks(base,web,artifacts):
    from playwright.sync_api import expect,sync_playwright
    with sync_playwright() as p:
        browser=p.chromium.launch(executable_path="/usr/bin/chromium",headless=True,args=["--no-sandbox","--disable-dev-shm-usage","--use-fake-device-for-media-stream","--use-fake-ui-for-media-stream"])
        context=browser.new_context(viewport={"width":1440,"height":1000},accept_downloads=True)
        page=context.new_page();errors=[];page.on("pageerror",lambda error:errors.append(str(error)))
        page.goto(base+"/api-sources.html")
        expect(page.locator("#lab-show-preview")).not_to_be_checked()
        page.locator("#lab-kind").select_option("camera");page.locator("#lab-refresh").fill("100")
        page.locator("#lab-start").click();expect(page.locator("#lab-download")).to_be_enabled()
        expect(page.locator("#lab-video")).to_be_hidden()
        page.locator("#lab-show-preview").check();expect(page.locator("#lab-video")).to_be_visible()
        before=number(page,"lab-input-frames")
        page.locator("#lab-show-preview").uncheck();expect(page.locator("#lab-video")).to_be_hidden()
        page.wait_for_function("Number(document.getElementById('lab-input-frames').textContent.replaceAll(',',''))>"+str(before))
        page.locator("#lab-stop").click()
        assert page.locator("#lab-preview-card").evaluate("node=>node.previousElementSibling.getAttribute('aria-labelledby')")=="lab-compare-title"
        page.locator("#lab-kind").select_option("rtsp")
        page.wait_for_function("document.getElementById('lab-registered').options.length===2")
        page.locator("#lab-bytes").fill("4096")
        for source in ["camera","onvif"]:
            page.locator("#lab-registered").select_option(source)
            page.locator("#lab-camera-user").fill(USER);page.locator("#lab-camera-password").fill(PASSWORD)
            page.locator("#lab-once").click();expect(page.locator("#lab-download")).to_be_enabled(timeout=20000)
            assert number(page,"lab-input-bytes")==4096
            expect(page.locator("#lab-camera-password")).to_have_value("")
        page.locator("#lab-registered").select_option("camera")
        page.locator("#lab-show-preview").check()
        page.locator("#lab-camera-password").fill(PASSWORD);page.locator("#lab-start").click()
        expect(page.locator("#lab-network-preview")).to_be_visible(timeout=20000)
        assert number(page,"lab-input-bytes")%4096==0
        assert_layout(page,2);page.screenshot(path=str(artifacts/"camera-preview-desktop.png"),full_page=True)
        page.set_viewport_size({"width":390,"height":844});wait_paint(page)
        try:assert_layout(page,1)
        except AssertionError:
            (artifacts/"overflow.json").write_text(json.dumps(page.evaluate("Array.from(document.querySelectorAll('body *')).map(node=>({tag:node.tagName,id:node.id,cls:node.className,right:node.getBoundingClientRect().right,width:node.getBoundingClientRect().width})).filter(node=>node.right>innerWidth+1)"),indent=2))
            page.screenshot(path=str(artifacts/"overflow.png"),full_page=True)
            raise
        page.screenshot(path=str(artifacts/"camera-preview-mobile.png"),full_page=True)
        page.locator("#lab-show-preview").uncheck();expect(page.locator("#lab-network-preview")).to_be_hidden();expect(page.locator("#lab-start")).to_be_enabled()
        page.locator("#lab-camera-password").fill("wrong-fixture-password");page.locator("#lab-once").click()
        expect(page.locator("#lab-status")).to_have_attribute("data-state","error",timeout=20000)
        page.goto(base+"/api-socket.html");page.set_viewport_size({"width":1440,"height":1000})
        page.locator("#lab-kind").select_option("http");page.locator("#lab-bytes").fill("4096")
        page.locator("#lab-url").fill(web+"/page");page.locator("#lab-once").click()
        expect(page.locator("#lab-status")).to_contain_text("registered server source")
        page.locator("#lab-url").fill(web+"/cors");page.locator("#lab-once").click();expect(page.locator("#lab-download")).to_be_enabled()
        with page.expect_download() as downloaded:page.locator("#lab-download").click()
        assert Path(downloaded.value.path()).read_bytes()==BODY[:4096]
        assert page.locator("#lab-body-preview").text_content()==BODY[:2048].decode()
        assert page.evaluate("window.remoteMarkupExecuted===undefined")
        page.locator("#lab-http-route").select_option("registered")
        expect(page.locator("#lab-url")).to_be_hidden()
        page.locator("#lab-registered").select_option("page");page.locator("#lab-once").click();expect(page.locator("#lab-download")).to_be_enabled()
        with page.expect_download() as downloaded:page.locator("#lab-download").click()
        assert Path(downloaded.value.path()).read_bytes()==BODY[:4096]
        page.locator("#lab-create-source").click()
        dialog=page.locator("#lab-registration");expect(dialog).to_be_visible()
        dialog.locator('[name="id"]').fill("new-page");dialog.locator('[name="endpoint"]').fill("https://example.org/")
        dialog.locator('[name="urls"]').fill("https://example.net/\nhttps://example.com/")
        dialog.locator('button[type="submit"]').click();expect(dialog.locator("#registration-status")).to_contain_text("added")
        dialog.locator('[name="kind"]').select_option("onvif");dialog.locator('[name="id"]').fill("new-camera");dialog.locator('[name="endpoint"]').fill("http://camera/onvif/device_service")
        dialog.locator('button[type="submit"]').click()
        with page.expect_download() as downloaded:dialog.locator("#registration-download").click()
        config=json.loads(Path(downloaded.value.path()).read_text())
        assert len(config["sources"])==2 and config["sources"][0]["urls"]==["https://example.net/","https://example.com/"]
        assert config["sources"][1]["max_bytes"]==230400 and PASSWORD not in json.dumps(config)
        page.set_viewport_size({"width":390,"height":844})
        assert dialog.evaluate("node=>node.scrollWidth<=node.clientWidth+1")
        dialog.evaluate("node=>node.scrollTop=0")
        dialog.screenshot(path=str(artifacts/"registration-mobile.png"))
        dialog.locator("#registration-close").click();wait_paint(page);assert_layout(page,1)
        page.screenshot(path=str(artifacts/"http-body-mobile.png"),full_page=True)
        page.set_viewport_size({"width":1440,"height":1000});wait_paint(page)
        assert_layout(page,2);page.screenshot(path=str(artifacts/"http-body-desktop.png"),full_page=True)
        assert not errors,errors
        browser.close()
        return {"browser_errors":errors,"checks":"authenticated RTSP, ONVIF UsernameToken discovery, preview privacy/toggle, CORS failure/help, browser/server body prefixes, inert HTML, registration export and desktop/mobile geometry"}


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mediamtx",type=Path,required=True)
    parser.add_argument("--server-binary",type=Path,default=Path("bin/entropy-server"))
    parser.add_argument("--artifacts",type=Path,required=True)
    args=parser.parse_args();args.artifacts.mkdir(parents=True,exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="camera-lab-",dir="/tmp") as tmp,contextlib.ExitStack() as stack:
        rtsp,port=free_port(),free_port()
        cfg=Path(tmp)/"mediamtx.yml"
        cfg.write_text(f'''rtspAddress: 127.0.0.1:{rtsp}
rtspTransports: [tcp]
rtmp: false
hls: false
webrtc: false
srt: false
authInternalUsers:
- user: any
  ips: [127.0.0.1]
  permissions: [{{action: publish, path: lab}}]
- user: {USER}
  pass: {PASSWORD}
  permissions: [{{action: read, path: lab}}]
paths:
  lab:
''')
        processes=[]
        def launch(command,name):
            log=stack.enter_context((args.artifacts/(name+".log")).open("w"));process=subprocess.Popen(command,stdout=log,stderr=log,cwd=tmp);processes.append(process);return process
        try:
            launch([str(args.mediamtx.resolve()),str(cfg)],"mediamtx")
            for _ in range(100):
                try:
                    with socket.create_connection(("127.0.0.1",rtsp),timeout=.1):break
                except OSError:time.sleep(.05)
            else:raise RuntimeError("MediaMTX did not start; inspect mediamtx.log")
            launch(["ffmpeg","-hide_banner","-loglevel","error","-re","-f","lavfi","-i","testsrc2=size=160x120:rate=10","-c:v","libx264","-preset","ultrafast","-tune","zerolatency","-threads","1","-g","10","-f","rtsp","-rtsp_transport","tcp",f"rtsp://127.0.0.1:{rtsp}/lab"],"publisher")
            WebFixture.rtsp_port=rtsp
            web=http.server.ThreadingHTTPServer(("127.0.0.1",0),WebFixture)
            stack.callback(web.server_close);stack.callback(web.shutdown);threading.Thread(target=web.serve_forever,daemon=True).start()
            web_url=f"http://127.0.0.1:{web.server_port}"
            sources={"sources":[
                {"id":"camera","kind":"rtsp","endpoint":f"rtsp://127.0.0.1:{rtsp}/lab","width":160,"height":120},
                {"id":"onvif","kind":"onvif","endpoint":web_url+"/device","width":160,"height":120},
                {"id":"page","kind":"webpage","endpoint":web_url+"/redirect","urls":[web_url+"/page"]}]}
            config=Path(tmp)/"sources.json";config.write_text(json.dumps(sources))
            log=stack.enter_context((args.artifacts/"server.log").open("w"))
            server=serve_process(args.server_binary.resolve(),port,log,["-lab-sources="+str(config)])
            processes.append(server)
            results=browser_checks(f"http://127.0.0.1:{port}",web_url,args.artifacts)
            assert WebFixture.soap_calls>=3
        finally:
            for process in reversed(processes):
                if process.poll() is None:process.send_signal(signal.SIGTERM)
            for process in reversed(processes):process.wait(timeout=10)
        assert "drain=complete" in (args.artifacts/"server.log").read_text()
        assert PASSWORD not in (args.artifacts/"server.log").read_text()
        (args.artifacts/"camera-results.json").write_text(json.dumps(results,indent=2)+"\n")
        print(json.dumps(results,indent=2))


if __name__=="__main__":main()
