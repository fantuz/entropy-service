// Dependency-free browser-state regression test for the embedded HTTP gallery.
import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";

const code = fs.readFileSync(new URL("../cmd/entropy-server/web/gallery.js", import.meta.url), "utf8");
const paths = [];
const events = {};
const timers = new Map();
const elements = new Map();
const cards = new Map();
const revoked = [];
let nextTimer = 0;
let nextBlob = 0;
let wordsResolver;
let delayWords = true;
let failQR = false;

function element(id, hidden = false) {
    const e = { id, hidden, textContent: "", value: "10", disabled: false, dataset: {}, handlers: {},
        addEventListener(event, handler) { this.handlers[event] = handler; } };
    elements.set(id, e);
    return e;
}
for (const key of ["image", "heatmap", "qr", "words"]) {
    element(`placeholder-${key}`);
    if (key !== "words") element(`preview-${key}`, true);
    cards.set(key, { dataset: {} });
}
element("preview-words", true);
element("gallery-word-text");
element("gallery-word-hash");
element("gallery-interval");
element("gallery-refresh");
element("gallery-status");

const document = {
    hidden: false,
    getElementById(id) { return elements.get(id); },
    querySelector(query) {
        const match = /\[data-gallery-key="([^"]+)"\]/.exec(query);
        return match ? cards.get(match[1]) : null;
    },
    addEventListener(event, handler) { events[`document:${event}`] = handler; },
};
const context = {
    document,
    window: { addEventListener(event, handler) { events[`window:${event}`] = handler; } },
    setTimeout(callback, delay) { const id = ++nextTimer; timers.set(id, { callback, delay }); return id; },
    clearTimeout(id) { timers.delete(id); },
    AbortController,
    Date,
    console,
    URL: {
        createObjectURL() { return `blob:sample-${++nextBlob}`; },
        revokeObjectURL(value) { revoked.push(value); },
    },
    Image: class { async decode() {} },
    DOMParser: class {
        parseFromString() {
            return { querySelector(query) {
                return { textContent: query === ".words" ? "alpha beta gamma" : "a".repeat(64) };
            } };
        }
    },
    async fetch(path, options) {
        assert.equal(options.cache, "no-store");
        paths.push(path);
        if (failQR && path === "/v1/qr/random") throw new Error("QR offline");
        if (path.startsWith("/v1/meta/random")) {
            if (delayWords) await new Promise(resolve => { wordsResolver = resolve; });
            return { ok: true, text: async () => "<html><div class=words>alpha beta gamma</div></html>" };
        }
        return { ok: true, blob: async () => ({ type: "image/png" }) };
    },
};
vm.runInNewContext(code, context, { filename: "gallery.js" });

const startup = [...timers.values()].find(t => t.delay === 0);
assert.ok(startup, "Initial synchronized cycle is scheduled");
const pending = startup.callback();
for (let i = 0; i < 12; i++) await Promise.resolve();
assert.deepEqual([...paths].sort(), [
    "/v1/image/random", "/v1/image/heatmap", "/v1/qr/random", "/v1/meta/random?words=8",
].sort());
assert.equal(elements.get("preview-image").hidden, true, "Do not render images while words remain pending");
assert.equal(elements.get("preview-words").hidden, true);
elements.get("gallery-refresh").handlers.click();
assert.equal(paths.length, 4, "No overlapping refresh cycles");
wordsResolver();
await pending;
assert.equal(elements.get("preview-image").hidden, false);
assert.equal(elements.get("preview-heatmap").hidden, false);
assert.equal(elements.get("preview-qr").hidden, false);
assert.equal(elements.get("preview-words").hidden, false);
assert.equal(elements.get("gallery-word-text").textContent, "alpha beta gamma");
assert.match(elements.get("gallery-status").textContent, /4\/4 PREVIEWS UPDATED/);
assert.ok([...timers.values()].some(t => t.delay >= 300), "Next refresh should be scheduled");

// On a partially failed cycle, preserve the QR image and refresh other cards.
const previousQR = elements.get("preview-qr").src;
failQR = true;
delayWords = false;
await elements.get("gallery-refresh").handlers.click();
assert.equal(elements.get("preview-qr").src, previousQR);
assert.match(elements.get("gallery-status").textContent, /3\/4 UPDATED/);
assert.equal(cards.get("qr").dataset.state, "error");
assert.ok(revoked.length >= 2, "Old PNG object URLs are released");
events["window:pagehide"]();
assert.ok(revoked.includes(previousQR), "Object URLs are released on unload");
console.log("PASS: synchronized batch, no overlap, first paint, partial failures, cleanup");
