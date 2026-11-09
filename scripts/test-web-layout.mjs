import assert from "node:assert/strict";
import {readFileSync} from "node:fs";
import vm from "node:vm";

const root = new URL("../cmd/entropy-server/web/", import.meta.url);
const source = readFileSync(new URL("entropy-display.js", root), "utf8");
const html = name => readFileSync(new URL(name, root), "utf8");
const css = html("theme.css");

function simulateFrame(view, frame) {
    const elements = new Map();
    const document = {
        body: { dataset: { entropyView: view }, style: {} },
        currentScript: {src: "http://localhost/entropy-display.js"},
        getElementById(id) {
            if (!elements.has(id)) {
                elements.set(id, {textContent: "", hidden: false, dataset: {}});
            }
            return elements.get(id);
        },
    };
    const listeners = new Map();
    const window = {
        addEventListener(type, cb) {listeners.set(type, cb);},
    };
    const workers = [];
    class FakeWorker {
        constructor(url) {this.url = url; workers.push(this); this.messages = [];}
        postMessage(data) {this.messages.push(data);}
        terminate() {}
    }
    vm.runInNewContext(source, {
        document, window, Worker: FakeWorker, URL, URLSearchParams,
        location: { href: "http://localhost/api-" + view + ".html", protocol: "http:", search: "" },
        requestAnimationFrame: callback => {callback(); return 1;},
        cancelAnimationFrame() {},
    });
    assert.equal(workers.length, 1);
    workers[0].onmessage({data: {type: "frame", ...frame}});
    return {value: id => document.getElementById(id).textContent, style: document.body.style, worker: workers[0]};
}

const hash = "0123456789abcdef".repeat(4);
const frame = {hash, hex: "11 22", base64: "ESI=", bytes: 64, previewBytes: 2};
const colors = simulateFrame("colors", frame);
assert.equal(colors.value("hash"), hash.slice(0, 32) + "\n" + hash.slice(32));
assert.equal(colors.value("hash").split("\n").length, 2);
assert.ok(colors.value("hash").split("\n").every(part => part.length === 32));
const bytes = simulateFrame("bytes", frame);
assert.equal(bytes.value("hash"), hash);
assert.equal(bytes.value("base64"), "ESI=");
assert.equal(bytes.value("hex"), "11 22");

// The old inline CSS hid the shared Base64 layout; the new page must use only
// the common stylesheet. The WebSocket path labels must be flex-pinned.
assert.ok(!html("api-bytes.html").includes("<style>"));
assert.match(html("api-bytes.html"), /id="base64"/);
assert.match(css, /\.page-bytes #base64/);
assert.match(css, /\.page-home \.websocket-grid > \.card > \.path/);
assert.match(css, /margin-top:\s*auto/);
assert.match(css, /\.page-colors #hash[\s\S]*?white-space:\s*pre-line/);

// Upload metadata precedes the same shared analysis panels used by the stream.
// IDs remain unique; the generated markup must not drift between the two labs.
const upload = html("api-upload.html");
assert.ok(!upload.includes("<style>"));
assert.ok(!upload.includes("</br>"));
const ordered = ["uploadForm", "upload-status", "file-entropy", "file-sha256",
    "analysis-size", "analysis-offset", "entropy", "pie", "wave", "histogram", "fft", "mosaic"];
let previous = -1;
for (const id of ordered) {
    const token = 'id="' + id + '"';
    const position = upload.indexOf(token);
    assert.ok(position > previous, "DOM ordering of " + id);
    assert.equal(upload.split(token).length, 2, "unique ID " + id);
    previous = position;
}
assert.match(upload, /class="analysis-card upload-input-card"/);
for (const page of [upload, html("api-stream.html")]) {
    assert.match(page, /class="analysis-card entropy-alert-card"/);
    assert.match(page, /page-analysis-lab/);
    assert.match(page, /src="\/analysis-workspace.js"/);
}
assert.match(upload, /id="server-report-content"/);
// Homepage API examples belong under the output explanation, before legacy links.
// The curl/WebSocket example uses a real HTTP/1.1 Upgrade request rather than
// treating a ws:// URL as a plain HTTP download. Curl exposes raw WS framing.
const home = html("index.html");
const pos = value => home.indexOf(value);
assert.ok(pos('id="ws-title"') < pos('id="scope-title"'));
assert.ok(pos('id="scope-title"') < pos("HTTP example · download 4,096 bytes"));
assert.ok(pos("HTTP example · download 4,096 bytes") < pos("WebSocket example · stream 4,096 bytes per message"));
assert.ok(pos("WebSocket example · stream 4,096 bytes per message") < pos("Legacy navigation shortcuts (optional)"));
assert.match(home, /Sec-WebSocket-Key: \$\(openssl rand -base64 16\)/);
assert.match(home, /Sec-WebSocket-Version: 13/);
assert.match(home, /Connection: Upgrade/);
assert.match(home, /--max-time 3/);
assert.match(home, /raw WebSocket frames/);
assert.match(home, /bytes=4096&amp;refresh=500/);
// The compatibility aliases now live alongside examples, directly before
// the optional legacy navigation group, not in the top monitoring section.
const compatibility = home.indexOf("Compatibility health and metrics aliases");
const legacy = home.indexOf("Legacy navigation shortcuts (optional)");
const scopeStart = home.indexOf('id="scope-title"');
const statusStart = home.indexOf('id="status-title"');
const browserStart = home.indexOf('id="explore-title"');
assert.ok(statusStart < browserStart);
assert.ok(scopeStart < compatibility && compatibility < legacy);
assert.equal(home.split("Compatibility health and metrics aliases").length, 2);
for (const route of ["/health-1", "/health-2", "/metrics-1"]) {
    assert.match(home.slice(compatibility, legacy), new RegExp(route));
}
assert.ok(pos("WebSocket example · stream 8 MiB to rngtest") < compatibility);
assert.match(home, /curl --no-buffer -N "ws:\/\/127\.0\.0\.1:8080\/stream\?bytes=\$\(echo 1024\*1024\*8 \| bc\)&amp;refresh=500"/);
assert.match(home, /rngtest --pipe &gt;\/dev\/null|rngtest --pipe >\/dev\/null/);
assert.match(home, /curl build supporting the/);

console.log("Browser layout and digest formatting: passed");
