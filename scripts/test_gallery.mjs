// Dependency-free browser-state regression test for the embedded HTTP gallery.
import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";

const code = fs.readFileSync(new URL("../cmd/entropy-server/web/gallery.js", import.meta.url), "utf8");
const html = fs.readFileSync(new URL("../cmd/entropy-server/web/api-gallery.html", import.meta.url), "utf8");
const intervalMarkup = /<input\b[^>]*id="gallery-interval"[^>]*>/.exec(html)?.[0];
assert.ok(intervalMarkup, "Gallery interval input is present");
const initialInterval = /\bvalue="([^"]+)"/.exec(intervalMarkup)?.[1];
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
let now = 0;

function element(id, hidden = false) {
    const e = { id, hidden, textContent: "", value: "", disabled: false, dataset: {}, handlers: {},
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
element("gallery-interval").value = initialInterval;
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
    Date: { now: () => now },
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

function takeScheduled(delay) {
    const entry = [...timers.entries()].find(([, timer]) => timer.delay === delay);
    assert.ok(entry, `Expected a refresh scheduled after ${delay} ms`);
    const [id, timer] = entry;
    timers.delete(id);
    return timer.callback;
}

function scheduledDelay() {
    assert.equal(timers.size, 1, "Only one next refresh is scheduled");
    return [...timers.values()][0].delay;
}

const pending = takeScheduled(0)();
for (let i = 0; i < 12; i++) await Promise.resolve();
assert.deepEqual([...paths].sort(), [
    "/v1/image/random?size=128", "/v1/image/heatmap?size=128", "/v1/qr/random", "/v1/meta/random?words=8",
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
assert.match(elements.get("gallery-status").textContent, /4\/4 UPDATED/);
assert.equal(scheduledDelay(), 2000, "HTML default produces a two-second period");

delayWords = false;
const interval = elements.get("gallery-interval");
for (const seconds of [0.05, 0.1, 0.15, 0.5, 0.75, 1, 1.5, 2, 2.5, 120]) {
    interval.value = String(seconds);
    interval.handlers.change();
    assert.equal(scheduledDelay(), seconds * 1000);
    now += seconds * 1000;
    await takeScheduled(seconds * 1000)();
    assert.equal(scheduledDelay(), seconds * 1000, "Fractional periods remain stable");
}

// Slow batches at the minimum interval still complete before another begins.
interval.value = "0.5";
interval.handlers.change();
delayWords = true;
const slow = takeScheduled(500)();
for (let i = 0; i < 12; i++) await Promise.resolve();
const duringSlowBatch = paths.length;
elements.get("gallery-refresh").handlers.click();
assert.equal(paths.length, duringSlowBatch, "Half-second refreshes do not overlap slow work");
now += 750;
wordsResolver();
await slow;
assert.equal(scheduledDelay(), 0, "Overdue batches start again only after finishing, without an extra slow-rate floor");
delayWords = false;

for (const value of ["", "0", "0.04", "0.075", "120.5", "-1", "NaN", "Infinity"]) {
    interval.value = value;
    interval.handlers.change();
    const before = paths.length;
    await elements.get("gallery-refresh").handlers.click();
    assert.equal(paths.length, before, `Invalid interval ${value} does not fetch`);
    assert.match(elements.get("gallery-status").textContent, /0.05–120/);
}
interval.value = "0.5";
interval.handlers.change();

// On a partially failed cycle, preserve the QR image and refresh other cards.
const previousQR = elements.get("preview-qr").src;
failQR = true;
delayWords = false;
await elements.get("gallery-refresh").handlers.click();
assert.equal(elements.get("preview-qr").src, previousQR);
assert.match(elements.get("gallery-status").textContent, /3\/4 UPDATED/);
assert.equal(cards.get("qr").dataset.state, "error");
assert.ok(revoked.length >= 2, "Old PNG object URLs are released");

document.hidden = true;
events["document:visibilitychange"]();
assert.equal(timers.size, 0, "Hidden galleries stop polling");
const beforeHiddenRefresh = paths.length;
await elements.get("gallery-refresh").handlers.click();
assert.equal(paths.length, beforeHiddenRefresh);
document.hidden = false;
events["document:visibilitychange"]();
await takeScheduled(0)();
assert.equal(scheduledDelay(), 500, "Visible galleries resume the selected half-second cadence");
events["window:pagehide"]();
assert.ok(revoked.includes(previousQR), "Object URLs are released on unload");
console.log("PASS: two-second default, 20-FPS option, validation, slow batches, visibility, partial failures, cleanup");
