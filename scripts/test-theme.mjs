import assert from "node:assert/strict";
import {readFileSync, readdirSync} from "node:fs";
import vm from "node:vm";

const root = new URL("../cmd/entropy-server/web/", import.meta.url);
const source = readFileSync(new URL("theme.js", root), "utf8");
function page({saved = null, dark = false, blocked = false} = {}) {
    const events = new Map(), clicks = new Map(), mediaEvents = new Map();
    const attributes = new Map(), document = {documentElement: {dataset: {}}};
    const button = {hidden: true, setAttribute: (key, value) => attributes.set(key, value),
        addEventListener: (key, handler) => clicks.set(key, handler)};
    let ready = false, stored = saved, changes = 0;
    const media = {matches: dark, addEventListener: (key, handler) => mediaEvents.set(key, handler)};
    document.querySelectorAll = () => ready ? [button] : [];
    document.addEventListener = (key, handler) => events.set(key, handler);
    const window = {matchMedia: () => media, addEventListener: (key, handler) => events.set(key, handler),
        dispatchEvent: () => changes++};
    const storage = {getItem() {if (blocked) throw new Error("disabled"); return stored;},
        setItem(_key, value) {if (blocked) throw new Error("disabled"); stored = value;}};
    vm.runInNewContext(source, {window, document, localStorage: storage, Event: class {}});
    const initial = document.documentElement.dataset.theme;
    assert.equal(button.hidden, true, "head initializer must not require a body");
    ready = true;
    events.get("DOMContentLoaded")();
    assert.equal(button.hidden, false);
    return {initial, button, attributes, theme: () => document.documentElement.dataset.theme,
        stored: () => stored, changes: () => changes,
        click: () => clicks.get("click")(),
        system(value) {media.matches = value; mediaEvents.get("change")();},
        storage(value, key = "entropy-theme") {events.get("storage")({key, newValue: value});}};
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
    assert.ok(p.changes() >= 3, "notify canvas consumers without reloading");
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
