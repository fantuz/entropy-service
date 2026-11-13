import assert from "node:assert/strict";
import {readFileSync} from "node:fs";
import vm from "node:vm";

const root = new URL("../cmd/entropy-server/web/", import.meta.url);
const source = readFileSync(new URL("graph-palette.js", root), "utf8");
const key = "entropy-graph-palette", names = ["classic", "blue-orange", "contrast"];

function page({saved = null, blocked = false, writeBlocked = false, theme = "dark"} = {}) {
    const events = new Map(), documentEvents = new Map(), controls = [], notifications = [];
    let ready = false, stored = saved;
    const selectors = Array.from({length: 2}, () => {
        const listeners = new Map();
        const select = {value: "classic", addEventListener: (event, callback) => listeners.set(event, callback)};
        controls.push({select, listeners}); return select;
    });
    const document = {documentElement: {dataset: {theme}}, hidden: false,
        querySelectorAll: query => {assert.equal(query, "[data-graph-palette]"); return ready ? selectors : [];},
        addEventListener: (event, callback) => documentEvents.set(event, callback)};
    const window = {addEventListener: (event, callback) => events.set(event, callback),
        dispatchEvent: event => notifications.push(event.type)};
    const localStorage = {
        getItem(name) {assert.equal(name, key); if (blocked) throw new Error("disabled"); return stored;},
        setItem(name, value) {assert.equal(name, key); if (blocked || writeBlocked) throw new Error("disabled"); stored = value;}
    };
    vm.runInNewContext(source, {document, window, localStorage, Event: class {constructor(type) {this.type = type;}}});
    const initial = document.documentElement.dataset.graphPalette;
    assert.equal(controls[0].listeners.size, 0, "head initialization must not require body controls");
    ready = true; documentEvents.get("DOMContentLoaded")();
    return {initial, notifications, document, selectors,
        palette: () => document.documentElement.dataset.graphPalette, stored: () => stored,
        select(value, index = 0) {selectors[index].value = value; controls[index].listeners.get("change")();},
        setStored(value) {stored = value;},
        event(name, value = {}) {events.get(name)(value);},
        visibility(value) {document.hidden = value; documentEvents.get("visibilitychange")();}};
}

for (const saved of [null, "", "invalid", "<script>", ...names]) {
    const p = page({saved});
    assert.equal(p.initial, names.includes(saved) ? saved : "classic");
    assert.ok(p.selectors.every(select => select.value === p.palette()));
    assert.deepEqual(p.notifications, ["entropygraphpalettechange"], "initialization emits only one palette event");
    for (const choice of names) {
        p.select(choice);
        assert.equal(p.palette(), choice); assert.equal(p.stored(), choice);
        assert.ok(p.selectors.every(select => select.value === choice));
        assert.equal(p.document.documentElement.dataset.theme, "dark", "palette must not change the theme");
        const count = p.notifications.length;
        p.select(choice, 1); p.event("focus"); p.event("pageshow", {persisted: true}); p.visibility(false);
        assert.equal(p.notifications.length, count, "same choice/restoration must not repaint graphs");
    }
    const before = p.notifications.length;
    p.select("unrecognized");
    assert.equal(p.palette(), "contrast"); assert.equal(p.stored(), "contrast");
    assert.equal(p.selectors[0].value, "contrast"); assert.equal(p.notifications.length, before);
}

const blocked = page({blocked: true, saved: "blue-orange", theme: "light"});
assert.equal(blocked.palette(), "classic"); blocked.select("contrast");
blocked.event("focus"); blocked.event("pageshow", {persisted: true}); blocked.visibility(false);
assert.equal(blocked.palette(), "contrast", "storage failure must retain the in-page preference");
assert.equal(blocked.document.documentElement.dataset.theme, "light");
const readonly = page({saved: "blue-orange", writeBlocked: true});
readonly.select("contrast"); readonly.event("focus"); readonly.event("pageshow", {persisted: true}); readonly.visibility(false);
assert.equal(readonly.palette(), "contrast", "a failed write must not restore an older readable value");
assert.equal(readonly.stored(), "blue-orange");

for (const restore of ["focus", "pageshow", "visibilitychange"]) {
    const p = page({saved: "classic"}); p.setStored("blue-orange");
    if (restore === "visibilitychange") {
        p.visibility(true); assert.equal(p.palette(), "classic"); p.visibility(false);
    } else p.event(restore, {persisted: true});
    assert.equal(p.palette(), "blue-orange", restore + " must reconcile a missed change");
}
const p = page({saved: "contrast"});
p.setStored("blue-orange"); p.event("storage", {key, newValue: "classic"});
assert.equal(p.palette(), "blue-orange", "current stored choice wins over stale payload");
p.event("storage", {key: "unrelated", newValue: "contrast"}); assert.equal(p.palette(), "blue-orange");
p.event("storage", {key: null, newValue: null}); assert.equal(p.palette(), "blue-orange");
p.setStored(null); p.event("storage", {key: null, newValue: null}); assert.equal(p.palette(), "classic");
p.setStored("invalid"); p.event("focus"); assert.equal(p.palette(), "classic");

// Read the actual semantic CSS declarations and measure mark/background
// contrast, rather than testing a second hard-coded colour implementation.
const css = readFileSync(new URL("theme.css", root), "utf8");
const declarations = selector => {
    const start = css.indexOf(selector + " {"); assert.ok(start >= 0, selector);
    const block = css.slice(start, css.indexOf("}", start));
    return Object.fromEntries([...block.matchAll(/(--[\w-]+):\s*([^;]+);/g)].map(match => [match[1], match[2].trim()]));
};
const luminance = hex => {
    const rgb = hex.match(/^#([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i); assert.ok(rgb, hex);
    const linear = rgb.slice(1).map(channel => parseInt(channel, 16) / 255).map(value => value <= .04045 ? value / 12.92 : ((value + .055) / 1.055) ** 2.4);
    return linear[0] * .2126 + linear[1] * .7152 + linear[2] * .0722;
};
const contrast = (a, b) => {const x = luminance(a), y = luminance(b); return (Math.max(x, y) + .05) / (Math.min(x, y) + .05);};
const base = declarations(":root"), dark = declarations(':root[data-theme="dark"]');
for (const mode of ["blue-orange", "contrast"]) {
    for (const theme of ["light", "dark"]) {
        const selector = theme === "light" ? `:root[data-graph-palette="${mode}"]` : `:root[data-theme="dark"][data-graph-palette="${mode}"]`;
        const palette = declarations(selector), surface = theme === "dark" ? dark : base;
        for (const mark of ["--plot-primary", "--plot-secondary", "--plot-reference", "--plot-zero", "--plot-one"]) {
            for (const background of ["--surface", "--surface-raised"]) {
                assert.ok(contrast(palette[mark], surface[background]) >= 3, `${mode}/${theme} ${mark} against ${background}`);
            }
        }
        for (const name of ["--green", "--cyan", "--warn", "--bg", "--surface", "--text"]) assert.equal(palette[name], undefined, "palette must not recolor page chrome");
    }
}
assert.ok(contrast(declarations(':root[data-graph-palette="contrast"]')["--plot-zero"], declarations(':root[data-graph-palette="contrast"]')["--plot-one"]) >= 3);
assert.ok(contrast(declarations(':root[data-theme="dark"][data-graph-palette="contrast"]')["--plot-zero"], declarations(':root[data-theme="dark"][data-graph-palette="contrast"]')["--plot-one"]) >= 3);
console.log("Three graph palettes: safe preference/restoration, storage isolation, unchanged themes and measured light/dark mark contrast passed");
