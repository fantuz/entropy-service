import assert from "node:assert/strict";
import {readFileSync, readdirSync} from "node:fs";
import vm from "node:vm";

const root = new URL("../cmd/entropy-server/web/", import.meta.url);
const source = readFileSync(new URL("theme.js", root), "utf8");
function page({saved = null, dark = false, blocked = false} = {}) {
    const events = new Map(), documentEvents = new Map(), clicks = new Map(), mediaEvents = new Map();
    const attributes = new Map(), document = {documentElement: {dataset: {}}};
    const button = {hidden: true, setAttribute: (key, value) => attributes.set(key, value),
        addEventListener: (key, handler) => clicks.set(key, handler)};
    let ready = false, stored = saved, changes = 0;
    const media = {matches: dark, addEventListener: (key, handler) => mediaEvents.set(key, handler)};
    document.querySelectorAll = () => ready ? [button] : [];
    document.addEventListener = (key, handler) => documentEvents.set(key, handler);
    const window = {matchMedia: () => media, addEventListener: (key, handler) => events.set(key, handler),
        dispatchEvent: () => changes++};
    const storage = {getItem() {if (blocked) throw new Error("disabled"); return stored;},
        setItem(_key, value) {if (blocked) throw new Error("disabled"); stored = value;}};
    vm.runInNewContext(source, {window, document, localStorage: storage, Event: class {}});
    const initial = document.documentElement.dataset.theme;
    assert.equal(button.hidden, true, "head initializer must not require a body");
    ready = true;
    documentEvents.get("DOMContentLoaded")();
    assert.equal(button.hidden, false);
    return {initial, button, attributes, theme: () => document.documentElement.dataset.theme,
        stored: () => stored, changes: () => changes,
        click: () => clicks.get("click")(),
        system(value) {media.matches = value; mediaEvents.get("change")();},
        setStored(value) {stored = value;},
        event(key, value = {}) {events.get(key)(value);},
        visibility(hidden) {document.hidden = hidden; documentEvents.get("visibilitychange")();},
        storage(value, key = "entropy-theme") {
            if (key === "entropy-theme" || key === null) stored = value;
            events.get("storage")({key, newValue: value});
        }};
}

for (const [options, want] of [
    [{}, "light"], [{dark: true}, "dark"], [{saved: "dark"}, "dark"],
    [{dark: true, saved: "light"}, "light"], [{saved: "unexpected"}, "light"],
    [{dark: true, blocked: true}, "dark"]
]) {
    const p = page(options);
    assert.equal(p.initial, want, "apply saved/system appearance before styles load");
    assert.equal(p.theme(), want);
    assert.equal(p.button.textContent, want === "dark" ? "Light mode" : "Dark mode");
    assert.equal(p.button.title, `Switch to ${want === "dark" ? "light" : "dark"} mode`);
    p.click();
    assert.equal(p.theme(), want === "dark" ? "light" : "dark");
    assert.equal(p.button.textContent, want === "dark" ? "Dark mode" : "Light mode");
    if (!options.blocked) assert.equal(p.stored(), p.theme());
    assert.equal(p.changes(), 2, "notify canvas consumers only when appearance actually changes");
    p.event("focus");
    assert.equal(p.theme(), want === "dark" ? "light" : "dark", "focus must retain the local choice even if storage is blocked");
    assert.equal(p.changes(), 2, "unchanged focus must not repaint plots");
}
const p = page();
p.system(true); assert.equal(p.theme(), "dark");
p.click(); p.system(false); p.system(true); assert.equal(p.theme(), "light", "explicit choice overrides later OS changes");
p.storage("dark"); assert.equal(p.theme(), "dark");
assert.equal(p.button.textContent, "Light mode");
p.storage("light"); assert.equal(p.button.textContent, "Dark mode");
p.storage("dark");
p.storage("light", "unrelated-key"); assert.equal(p.theme(), "dark");
p.storage(null, null); assert.equal(p.theme(), "dark", "cleared preferences fall back to current OS choice");

for (const event of ["focus", "pageshow", "visibilitychange"]) {
    const restored = page({saved: "dark", dark: true});
    restored.setStored("light");
    if (event === "visibilitychange") {
        restored.visibility(true);
        assert.equal(restored.theme(), "dark", "hidden pages wait for reactivation");
        restored.visibility(false);
    } else restored.event(event, {persisted: true});
    assert.equal(restored.theme(), "light", `${event} must reconcile a missed cross-window change`);
    assert.equal(restored.button.textContent, "Dark mode");
}
const delayed = page({saved: "dark", dark: true});
delayed.setStored("light");
delayed.event("storage", {key: "entropy-theme", newValue: "dark"});
assert.equal(delayed.theme(), "light", "a stale event must not override the current stored choice");
delayed.event("storage", {key: null, newValue: null});
assert.equal(delayed.theme(), "light", "a queued clear event must not erase a newer preference");
const noChange = delayed.changes();
delayed.event("pageshow", {persisted: true}); delayed.event("focus"); delayed.visibility(false);
assert.equal(delayed.changes(), noChange, "restoration without a theme change must avoid repeated canvas redraws");

for (const name of readdirSync(root).filter(name => name.endsWith(".html") && !name.startsWith("zz_"))) {
    const html = readFileSync(new URL(name, root), "utf8");
    const script = html.indexOf('src="/theme.js"'), stylesheet = html.indexOf('href="/theme.css"');
    assert.ok(script >= 0 && stylesheet >= 0 && script < stylesheet, name);
    assert.equal(html.split("data-theme-toggle").length, 2, name);
    assert.ok(!html.includes("<style>"), name + " uses the common stylesheet");
}
const help = name => readFileSync(new URL(name, root), "utf8")
    .split("<!-- BEGIN SHARED LAB HELP -->")[1].split("<!-- END SHARED LAB HELP -->")[0];
assert.equal(help("api-sources.html"), help("api-socket.html"));
assert.equal((help("api-sources.html").match(/<details>/g) || []).length, 3);
assert.ok(!/<details\b[^>]*\bopen\b/.test(help("api-socket.html")));
for (const name of ["api-sources.html", "api-socket.html"]) {
    const html = readFileSync(new URL(name, root), "utf8");
    assert.ok(!/<(?:title|h1)>[^<]*Experimental/i.test(html));
    assert.match(html, /Experimental tools:/);
}
console.log("Theme detection, saved preference, blocked storage, cross-tab updates, shared pages and collapsed lab help: passed");
