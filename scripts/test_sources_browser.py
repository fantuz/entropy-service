#!/usr/bin/env python3
"""Real Chromium source/socket lab checks with fake media and local fixtures."""
import argparse
import contextlib
import json
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


def browser_checks(base, peer, artifacts, chromium, udp_port):
    from playwright.sync_api import expect, sync_playwright
    with sync_playwright() as playwright:
        browser=playwright.chromium.launch(executable_path=chromium,headless=True,args=["--no-sandbox","--disable-dev-shm-usage","--use-fake-ui-for-media-stream","--use-fake-device-for-media-stream"])
        context=browser.new_context(viewport={"width":1440,"height":1000},device_scale_factor=2,accept_downloads=True)
        context.add_init_script("""(() => {
          window.labTracks=[];window.mediaDelay=0;window.serialClosed=0;
          const media=navigator.mediaDevices;
          if(media){const original=media.getUserMedia.bind(media);media.getUserMedia=async options=>{
            const delay=mediaDelay,stream=await original(options);labTracks.push(...stream.getTracks());
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
        expect(page.locator("#entropy")).to_have_text("8.00000")
        assert number(page,"lab-input-frames")==number(page,"lab-analysis-frames")==1
        with page.expect_download() as downloaded:
            page.locator("#lab-download").click()
        assert Path(downloaded.value.path()).read_bytes()==bytes(range(256))*16
        page.locator("#lab-record").click()
        page.locator("#lab-kind").select_option("constant")
        page.locator("#lab-once").click()
        expect(page.locator("#lab-download")).to_be_enabled()
        expect(page.locator("#entropy")).to_have_text("0.00000")
        page.locator("#lab-record").click()
        assert page.locator("#lab-measurements tbody tr").count()==2
        with page.expect_download() as exported:
            page.locator("#lab-export").click()
        records=json.loads(Path(exported.value.path()).read_text())
        assert [record["statistics"]["entropy"] for record in records["records"]]==[8,0]
        page.locator("#lab-kind").select_option("counter")
        page.locator("#lab-transform").select_option("lsb")
        page.locator("#lab-once").click();expect(page.locator("#lab-download")).to_be_enabled()
        expect(page.locator("#entropy")).to_have_text("1.00000")
        page.locator("#lab-transform").select_option("xor");page.locator("#lab-refresh").fill("20")
        page.locator("#lab-once").click();expect(page.locator("#lab-download")).to_be_enabled()
        expect(page.locator("#entropy")).to_have_text("0.00000")
        assert number(page,"lab-input-frames")==2 and number(page,"lab-analysis-frames")==1
        page.locator("#lab-transform").select_option("raw")
        page.locator("#lab-start").click()
        page.wait_for_function("Number(document.getElementById('lab-input-frames').textContent.replaceAll(',',''))>5")
        page.locator("#pause-plots").click();wait_paint(page);page.wait_for_timeout(200)
        frames=number(page,"lab-input-frames");analyzed=number(page,"lab-analysis-frames");frozen=page.locator("#sample-info").inner_text()
        page.wait_for_timeout(250)
        assert number(page,"lab-input-frames")>frames and number(page,"lab-analysis-frames")==analyzed
        assert page.locator("#sample-info").inner_text()==frozen
        page.locator("#pause-plots").click()
        page.wait_for_function("Number(document.getElementById('lab-analysis-frames').textContent.replaceAll(',',''))>"+str(analyzed))
        page.locator("#lab-stop").click();expect(page.locator("#lab-start")).to_be_enabled()
        desktop=assert_layout(page,2)
        page.screenshot(path=str(artifacts/"sources-desktop.png"),full_page=True)
        for width in [320,390,768]:
            page.set_viewport_size({"width":width,"height":844});wait_paint(page);assert_layout(page,1)
        page.set_viewport_size({"width":390,"height":844});wait_paint(page)
        page.screenshot(path=str(artifacts/"sources-mobile.png"),full_page=True)
        page.set_viewport_size({"width":1440,"height":1000})
        page.locator("#lab-kind").select_option("pattern")
        page.locator("#lab-pattern").fill("zz")
        page.locator("#lab-once").click();expect(page.locator("#lab-status")).to_have_attribute("data-state","error")
        page.locator("#lab-kind").select_option("crypto")
        page.locator("#lab-once").click();expect(page.locator("#lab-download")).to_be_enabled()
        page.locator("#lab-kind").select_option("pattern");page.locator("#lab-pattern").fill("00ff")
        for kind in ["crypto","pattern","camera","microphone","serial"]:
            page.locator("#lab-kind").select_option(kind);page.locator("#lab-once").click()
            expect(page.locator("#lab-download")).to_be_enabled(timeout=15000)
            if kind in ["camera","microphone"]:
                page.wait_for_function("labTracks.every(track=>track.readyState==='ended')")
                expect(page.locator("#lab-video")).to_be_hidden()
            if kind=="serial":
                page.wait_for_function("serialClosed>0")
                assert number(page,"lab-input-bytes")==4
        # Stop while permission acquisition is unresolved; late tracks must close.
        page.locator("#lab-kind").select_option("camera")
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
        assert number(page,"lab-input-bytes")==10240 and number(page,"lab-input-frames")==3
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
            assert number(page,"lab-input-bytes")==4096
        # A browser-origin peer connection follows the upstream policy; use the
        # server bridge to connect peers without weakening that Origin policy.
        page.locator("#lab-kind").select_option("peer-ws");page.locator("#lab-url").fill(peer)
        page.locator("#lab-once").click();expect(page.locator("#lab-status")).to_have_attribute("data-state","error")
        page.locator("#lab-kind").select_option("bridge")
        page.wait_for_function("document.getElementById('lab-registered').options.length>=5")
        for source in ["peer-ws","peer-http","self-ws","device","tcp","unix"]:
            page.locator("#lab-registered").select_option(source);page.locator("#lab-once").click()
            expect(page.locator("#lab-download")).to_be_enabled(timeout=15000)
            assert number(page,"lab-input-bytes")==4096
            expect(page.locator("#sample-info")).to_contain_text(source)
        page.locator("#lab-registered").select_option("udp")
        page.locator("#lab-start").click()
        with socket.socket(socket.AF_INET,socket.SOCK_DGRAM) as sender:
            for _ in range(10):sender.sendto(bytes(range(256))*4,("127.0.0.1",udp_port));page.wait_for_timeout(30)
        expect(page.locator("#lab-download")).to_be_enabled();page.locator("#lab-stop").click()
        expect(page.locator("#entropy")).to_have_text("8.00000")
        page.locator("#lab-record").click()
        assert page.locator("#lab-measurements tbody tr").count()==3
        assert_layout(page,2);page.screenshot(path=str(artifacts/"socket-desktop.png"),full_page=True)
        page.set_viewport_size({"width":390,"height":844});wait_paint(page);assert_layout(page,1)
        page.screenshot(path=str(artifacts/"socket-mobile.png"),full_page=True)
        assert not errors,errors
        browser.close()
        return {"browser_errors":errors,"desktop":desktop,"comparison_records":len(records["records"]),
            "checks":"generators, exact binary/JSON downloads, XOR/LSB, metering/pause, media/serial release, late permission cancellation, file EOF, self/peer HTTP+WS, registered TCP/UDP/Unix/device, Origin policy, desktop/mobile"}


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--server-binary",type=Path,default=Path("bin/entropy-server"))
    parser.add_argument("--artifacts",type=Path,required=True)
    parser.add_argument("--chromium",default="/usr/bin/chromium")
    args=parser.parse_args();args.artifacts.mkdir(parents=True,exist_ok=True)
    binary=args.server_binary.resolve();a,b,udp=free_port(),free_port(),free_port(socket.SOCK_DGRAM)
    with tempfile.TemporaryDirectory(prefix="entropy-lab-",dir="/tmp") as temp,contextlib.ExitStack() as stack:
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
            results=browser_checks(f"http://127.0.0.1:{a}",f"http://127.0.0.1:{b}",args.artifacts,args.chromium,udp)
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
