import assert from "node:assert/strict";
import {readFileSync, readdirSync} from "node:fs";
import vm from "node:vm";

const web = new URL("../cmd/entropy-server/web/", import.meta.url);
const source = readFileSync(new URL("i18n.js", web), "utf8");
const preferenceKey = "entropy-ui-language";
const languages = ["en", "fr", "de", "es", "cs", "ru", "pl", "el", "it"];
const settle = () => new Promise(resolve => setImmediate(resolve));
const response = value => ({ok: true, text: async () => JSON.stringify(value)});
function deferred() {
    let resolve, reject;
    const promise = new Promise((ok, fail) => { resolve = ok; reject = fail; });
    return {promise, resolve, reject};
}

// Model just the DOM operations used by the production module. Refuse innerHTML
// so a future change cannot silently turn a translated message into markup.
class Target {
    listeners = new Map();
    addEventListener(type, callback, options = {}) {
        if (!this.listeners.has(type)) this.listeners.set(type, []);
        this.listeners.get(type).push({callback, once: !!options.once});
    }
    dispatchEvent(event) {
        event.target ??= this;
        for (const listener of [...(this.listeners.get(event.type) || [])]) {
            if (listener.once) this.listeners.set(event.type,
                this.listeners.get(event.type).filter(value => value !== listener));
            listener.callback(event);
        }
        return true;
    }
    emit(type, values = {}) { this.dispatchEvent({type, ...values}); }
}
class Element extends Target {
    constructor(tagName) {
        super();
        this.tagName = tagName.toUpperCase();
        this.children = [];
        this.attributeValues = new Map();
        this.dataset = {};
        this.value = "";
        this.className = "";
        this._text = "";
    }
    get attributes() { return [...this.attributeValues].map(([name, value]) => ({name, value})); }
    get childElementCount() { return this.children.length; }
    get textContent() { return this._text + this.children.map(child => child.textContent).join(""); }
    set textContent(value) { this._text = String(value); this.children = []; }
    set innerHTML(_value) { throw new Error("Translations must never be parsed as HTML"); }
    getAttribute(name) { return this.attributeValues.get(name) ?? null; }
    setAttribute(name, value) {
        this.attributeValues.set(name, String(value));
        if (name.startsWith("data-")) this.dataset[name.slice(5).replace(/-([a-z])/g, (_, letter) => letter.toUpperCase())] = String(value);
    }
    append(...children) {
        for (const child of children) child.parentElement = this;
        this.children.push(...children);
    }
    insertBefore(child, reference) {
        if (reference === null) { this.append(child); return; }
        const position = this.children.indexOf(reference);
        assert.notEqual(position, -1, "The reference node must be a child");
        child.parentElement = this;
        this.children.splice(position, 0, child);
    }
    matches(selector) {
        if (selector.startsWith(".")) return this.className.split(/\s+/).includes(selector.slice(1));
        const match = /^(?:([a-z]+))?\[([^\]]+)\]$/.exec(selector);
        assert.ok(match, "The harness needs support for selector " + selector);
        return (!match[1] || this.tagName === match[1].toUpperCase()) && this.attributeValues.has(match[2]);
    }
    querySelectorAll(selector) {
        return this.children.flatMap(child => [
            ...(child.matches(selector) ? [child] : []), ...child.querySelectorAll(selector),
        ]);
    }
    querySelector(selector) { return this.querySelectorAll(selector)[0] ?? null; }
}

const fixtures = {
    fr: {"Interface language": "Langue de l’interface", Start: "Démarrer", Open: "Ouvrir",
        "Sample {count}": "Échantillon {count}", "Hello {name}": "Bonjour {name}", "Read {value}": "Lire {value}",
        "Random words": "Mots aléatoires", Unsafe: "<img src=x onerror=bad()>",
        "{list}: {bits} bits; {size} words": "{list} : {bits} bits ; {size} mots"},
    de: {"Interface language": "Sprache der Oberfläche", Start: "Starten", Open: "Öffnen",
        "Sample {count}": "Probe {count}", "Hello {name}": "Hallo {name}", "Read {value}": "Lesen {value}",
        "Random words": "Zufällige Wörter"},
    es: {Start: "Iniciar"}, cs: {Start: "Spustit"}, ru: {Start: "Начать"},
    pl: {Start: "Rozpocznij"}, el: {Start: "Έναρξη"}, it: {Start: "Avvia"},
};

function harness(options = {}) {
    const saved = options.saved instanceof Map ? options.saved : new Map(Object.entries(options.saved || {}));
    const document = new Target(), window = new Target(), requests = [], writes = [], timers = new Map();
    const root = new Element("html"), body = new Element("body"), toolbar = new Element("div");
    toolbar.className = "site-toolbar";
    root.append(body); body.append(toolbar);
    Object.assign(document, {documentElement: root, hidden: false, cookie: "lang=ru; bytes=64; refresh=500",
        createElement: tag => new Element(tag),
        querySelectorAll: selector => [...(root.matches(selector) ? [root] : []), ...root.querySelectorAll(selector)],
        querySelector: selector => document.querySelectorAll(selector)[0] ?? null});
    const node = (tag, text, attributes = {}, parent = body) => {
        const element = new Element(tag);
        element.textContent = text;
        for (const [name, value] of Object.entries(attributes)) element.setAttribute(name, value);
        parent.append(element);
        return element;
    };
    const heading = node("h1", "Start", {"data-i18n": "Start"});
    const brand = node("a", "Entropy Service", {}, toolbar);
    const themeButton = node("button", "Dark mode", {"data-theme-toggle": ""}, toolbar);
    const live = node("output", "Awaiting sample", {"data-i18n": "Start"});
    const title = node("button", "API", {"data-i18n-title": "Open", title: "Open",
        "data-i18n-aria-label": "Start", "aria-label": "Start"});
    const placeholder = node("input", "", {"data-i18n-placeholder": "Start", placeholder: "Start"});
    const image = node("img", "", {"data-i18n-alt": "Start", alt: "Start"});
    const rawWords = node("pre", "Random words"), rawHash = node("code", "0123456789abcdef".repeat(4));
    const field = node("select", ""); field.value = "ru";
    const template = node("p", "", {"data-i18n": "{list}: {bits} bits; {size} words",
        "data-i18n-param-list": "EFF Long", "data-i18n-param-size": "7776", "data-i18n-param-bits": "12.925"});
    let nextTimer = 0;
    class FakeEvent { constructor(type) { this.type = type; } }
    const context = vm.createContext({window, document, Event: FakeEvent, AbortController,
        navigator: options.navigator || {languages: ["en-US"], language: "en-US"},
        location: {href: "https://entropy.test/api-words.html?lang=ru", search: "?lang=ru"},
        localStorage: {
            getItem(key) {
                if (options.blockRead) throw new Error("Storage is disabled");
                return saved.get(key) ?? null;
            },
            setItem(key, value) {
                writes.push([key, String(value)]);
                if (options.blockWrite) throw new Error("Storage is disabled");
                saved.set(key, String(value));
            },
        },
        setTimeout(callback, delay) { const id = ++nextTimer; timers.set(id, {callback, delay}); return id; },
        clearTimeout(id) { timers.delete(id); },
        fetch(url, init) {
            requests.push({url, ...init});
            return options.fetch ? options.fetch(url, init) : Promise.resolve(response(fixtures[url.slice(9, -5)] || {}));
        }});
    vm.runInContext(source, context, {filename: "i18n.js"});
    const api = window.EntropyI18n;
    return {api, saved, writes, requests, root, document, window, body, toolbar, node, timers,
        heading, live, title, placeholder, image, rawWords, rawHash, field, template, brand, themeButton,
        picker: () => document.querySelector("select[data-ui-language]"),
        async start() { document.emit("DOMContentLoaded"); await settle(); },
    };
}

// Saved preferences override browser language; selecting a language persists it
// across a new page and never changes the independent word-language preference.
const saved = new Map([[preferenceKey, "fr"], ["entropy-theme", "light"], ["entropy-graph-palette", "contrast"]]);
let view = harness({saved, navigator: {languages: ["de-DE"]}});
const beforeCookie = view.document.cookie, beforeHash = view.rawHash.textContent;
await view.start();
assert.equal(view.api.language, "fr");
assert.equal(view.root.lang, "fr");
assert.equal(view.root.dataset.uiLanguage, "fr");
assert.equal(view.heading.textContent, "Démarrer");
assert.equal(view.picker().value, "fr");
assert.deepEqual(view.toolbar.children, [view.brand, view.picker().parentElement, view.themeButton]);
assert.deepEqual(Array.from(view.picker().children, option => option.value), languages);
assert.deepEqual(Array.from(view.picker().children, option => option.lang), languages);
assert.deepEqual(Object.keys(view.api.languages), languages);
assert.equal(view.template.textContent, "EFF Long : 12.925 bits ; 7776 mots");
view.picker().value = "it"; view.picker().emit("change"); await settle();
assert.equal(view.api.language, "it"); assert.equal(saved.get(preferenceKey), "it");
assert.deepEqual([...saved], [[preferenceKey, "it"], ["entropy-theme", "light"], ["entropy-graph-palette", "contrast"]]);
assert.ok(view.writes.every(([key]) => key === preferenceKey));
assert.equal(view.document.cookie, beforeCookie);
assert.equal(view.field.value, "ru");
assert.equal(view.rawWords.textContent, "Random words");
assert.equal(view.rawHash.textContent, beforeHash);
view.document.emit("DOMContentLoaded"); await settle();
assert.equal(view.document.querySelectorAll("select[data-ui-language]").length, 1);
const reload = harness({saved, navigator: {languages: ["el-GR"]}});
await reload.start(); assert.equal(reload.api.language, "it");

// Browser language matching uses supported primary tags; invalid stored values
// safely fall back to English instead of becoming a URL or a selector value.
for (const [options, expected] of [
    [{navigator: {languages: ["pt-BR", "DE-at", "fr"]}}, "de"],
    [{navigator: {languages: ["pt-BR", "fr-CA"]}}, "fr"],
    [{navigator: {language: "el-GR"}}, "el"],
    [{navigator: {languages: ["pt-BR", "ja-JP"]}}, "en"],
    [{saved: {[preferenceKey]: "../../private"}, navigator: {languages: ["fr"]}}, "en"],
]) {
    const test = harness(options); await test.start();
    assert.equal(test.api.language, expected);
    assert.equal(test.picker().value, expected);
    assert.ok(test.requests.every(request => /^\/locales\/(?:fr|de|es|cs|ru|pl|el|it)\.json$/.test(request.url)));
}

// A local choice remains useful when reads and writes are blocked. Restoring
// focus or the page must not reset it to the browser's original preference.
view = harness({blockRead: true, blockWrite: true, navigator: {languages: ["fr"]}});
await view.start(); assert.equal(view.api.language, "fr");
await view.api.useLanguage("it", true);
for (const type of ["focus", "pageshow"]) { view.window.emit(type); await settle(); assert.equal(view.api.language, "it"); }
view.document.emit("visibilitychange"); await settle(); assert.equal(view.api.language, "it");
assert.equal(view.saved.size, 0);

// Missed storage notifications are repaired by focus/pageshow/visibility, and
// changes to other preferences cannot unexpectedly switch the interface.
view = harness({navigator: {languages: ["cs-CZ"]}}); await view.start();
view.saved.set(preferenceKey, "de"); view.window.emit("storage", {key: preferenceKey}); await settle();
assert.equal(view.api.language, "de");
view.saved.set(preferenceKey, "fr"); view.window.emit("storage", {key: "entropy-theme"}); await settle();
assert.equal(view.api.language, "de");
view.window.emit("focus"); await settle(); assert.equal(view.api.language, "fr");
view.saved.set(preferenceKey, "it"); view.window.emit("pageshow"); await settle(); assert.equal(view.api.language, "it");
view.saved.set(preferenceKey, "el"); view.document.hidden = true; view.document.emit("visibilitychange"); await settle();
assert.equal(view.api.language, "it");
view.document.hidden = false; view.document.emit("visibilitychange"); await settle(); assert.equal(view.api.language, "el");
view.saved.delete(preferenceKey); view.window.emit("storage", {key: null}); await settle(); assert.equal(view.api.language, "cs");

// Interpolation is text, including static template parameters and accessible
// attributes. Live values win over old static keys and keep copied parameters.
view = harness(); await view.start();
const attack = '<img src=x onerror="bad()"> & <script>bad()</script>';
const params = {count: 3}; view.api.set(view.live, "Sample {count}", params); params.count = 99;
view.api.attr(view.title, "title", "Read {value}", {value: attack});
view.api.attr(view.title, "onclick", "Unsafe"); view.api.attr(view.title, "href", "Unsafe");
view.api.attr(view.title, "style", "Unsafe");
view.api.set(null, "Start"); view.api.attr(null, "title", "Open");
const unsafe = view.node("output", "", {"data-i18n": "Unsafe"});
const injectedParam = view.node("p", "", {"data-i18n": "Hello {name}", "data-i18n-param-name": attack});
const observations = [];
view.window.addEventListener("entropylanguagechange", () => observations.push({language: view.api.language,
    root: view.root.lang, heading: view.heading.textContent, live: view.live.textContent, title: view.title.getAttribute("title")}));
await view.api.useLanguage("fr", true);
assert.equal(view.live.textContent, "Échantillon 3");
assert.equal(view.title.getAttribute("title"), "Lire " + attack);
assert.equal(view.title.getAttribute("aria-label"), "Démarrer");
assert.equal(view.placeholder.getAttribute("placeholder"), "Démarrer");
assert.equal(view.image.getAttribute("alt"), "Démarrer");
assert.equal(view.title.getAttribute("onclick"), null); assert.equal(view.title.getAttribute("href"), null);
assert.equal(view.title.getAttribute("style"), null);
assert.equal(unsafe.textContent, "<img src=x onerror=bad()>"); assert.equal(unsafe.childElementCount, 0);
assert.equal(injectedParam.textContent, "Bonjour " + attack); assert.equal(injectedParam.childElementCount, 0);
assert.equal(view.api.t("Hello {name}", {name: attack}), "Bonjour " + attack);
assert.equal(view.api.t("Unknown {zero} {boolean} {null} {missing}", {zero: 0, boolean: false, null: null}), "Unknown 0 false null {missing}");
assert.equal(view.api.t("Hello {name}", Object.create({name: "inherited"})), "Bonjour {name}");
assert.equal(view.api.t("constructor"), "constructor"); assert.equal(view.api.t("__proto__"), "__proto__");
assert.equal(view.rawWords.textContent, "Random words"); assert.equal(view.rawHash.textContent, beforeHash);
assert.deepEqual(observations, [{language: "fr", root: "fr", heading: "Démarrer", live: "Échantillon 3", title: "Lire " + attack}]);
await view.api.useLanguage("de");
assert.equal(view.live.textContent, "Probe 3"); assert.equal(view.title.getAttribute("title"), "Lesen " + attack);
await view.api.useLanguage("en");
assert.equal(view.live.textContent, "Sample 3"); assert.equal(view.title.getAttribute("title"), "Read " + attack);
const requestsBefore = view.requests.length, eventsBefore = observations.length;
await view.api.useLanguage("de"); await view.api.useLanguage("de");
assert.equal(view.requests.length, requestsBefore, "Loaded catalogs should be reused without another fetch");
assert.equal(observations.length, eventsBefore + 1, "The same applied language must not emit an extra redraw");
const subtree = view.node("div", "", {"data-i18n": "Start"}); view.api.translate(subtree);
assert.equal(subtree.textContent, "Starten", "Explicit subtree translation includes its own root");

// An obsolete request may complete after cancellation (for example from an
// intermediary cache); it must never replace the user's newer selection.
const fr = deferred(), de = deferred();
view = harness({fetch: url => url.includes("/fr.") ? fr.promise : de.promise});
const older = view.api.useLanguage("fr", true), newer = view.api.useLanguage("de", true);
assert.equal(view.requests[0].signal.aborted, true);
de.resolve(response(fixtures.de)); assert.equal(await newer, "de");
fr.resolve(response(fixtures.fr)); assert.equal(await older, "de");
assert.equal(view.root.lang, "de"); assert.equal(view.api.t("Start"), "Starten");
assert.equal(view.saved.get(preferenceKey), "de"); assert.equal(view.timers.size, 0);
const canceled = deferred(); view = harness({fetch: () => canceled.promise});
const pending = view.api.useLanguage("fr", true);
assert.equal(await view.api.useLanguage("en", true), "en");
assert.equal(view.requests[0].signal.aborted, true);
canceled.resolve(response(fixtures.fr)); assert.equal(await pending, "en");
assert.equal(view.heading.textContent, "Start"); assert.equal(view.saved.get(preferenceKey), "en");

// Repeated callers for the same pending language share the fetch and wait for
// the applied result, rather than observing the previous language prematurely.
const same = deferred(); view = harness({fetch: () => same.promise});
const one = view.api.useLanguage("fr"), two = view.api.useLanguage("fr");
let secondFinished = false; void two.then(() => { secondFinished = true; }); await settle();
assert.equal(secondFinished, false, "A caller awaiting a pending language must wait for its catalog");
assert.equal(view.requests.length, 1);
same.resolve(response(fixtures.fr)); assert.deepEqual(await Promise.all([one, two]), ["fr", "fr"]);

// Network and validation failures must leave readable English and allow the
// very same selection to be retried, including retry after timeout abort.
for (const [name, badResponse] of [
    ["HTTP error", {ok: false}],
    ["non-JSON response", {ok: true, text: async () => "<html>unavailable</html>"}],
    ["array", response([])], ["non-string value", response({Start: 7})],
    ["oversize response", {ok: true, text: async () => "x".repeat(2 * 1024 * 1024 + 1)}],
    ["oversize key", response({["x".repeat(4097)]: "value"})],
    ["oversize value", response({Start: "x".repeat(8193)})],
]) {
    let attempts = 0;
    const test = harness({fetch: async () => ++attempts === 1 ? badResponse : response(fixtures.fr)});
    assert.equal(await test.api.useLanguage("fr", true), "en", name);
    assert.equal(test.heading.textContent, "Start", name);
    assert.equal(test.api.t("Start"), "Start", name);
    assert.equal(test.timers.size, 0, name + " must clear its timeout");
    assert.equal(await test.api.useLanguage("fr", true), "fr", name + " must allow retry");
    assert.equal(attempts, 2, name); assert.equal(test.heading.textContent, "Démarrer", name);
}
let timeoutAttempts = 0;
view = harness({fetch: (_url, {signal}) => ++timeoutAttempts === 1 ? new Promise((_resolve, reject) => {
    signal.addEventListener("abort", () => reject(new Error("aborted")), {once: true});
}) : Promise.resolve(response(fixtures.fr))});
const timedOut = view.api.useLanguage("fr", true);
assert.equal(view.timers.size, 1);
const timer = [...view.timers.values()][0]; assert.equal(timer.delay, 8000); timer.callback();
assert.equal(await timedOut, "en"); assert.equal(view.requests[0].signal.aborted, true);
assert.equal(await view.api.useLanguage("fr", true), "fr"); assert.equal(view.timers.size, 0);
assert.ok(view.requests.every(request => request.credentials === "omit" && request.cache === "no-cache"));
assert.equal(await view.api.useLanguage("../../private", true), "en");
assert.equal(view.saved.get(preferenceKey), "en");
console.log("UI language: persistence, detection, lifecycle sync, safe bindings, isolated payloads, async races, retry and timeout passed");

// --runtime-only permits focused engine work while a translator is updating
// catalogs. CI/default invocation always verifies the complete shipped set.
if (!process.argv.includes("--runtime-only")) {
    const directory = new URL("locales/", web);
    assert.deepEqual(readdirSync(directory).filter(name => name.endsWith(".json")).sort(), languages.map(code => code + ".json").sort());
    const english = JSON.parse(readFileSync(new URL("en.json", directory), "utf8"));
    const expectedKeys = Object.keys(english).sort();
    assert.ok(expectedKeys.length > 100, "The interface catalog should include the actual application");
    const placeholders = value => [...value.matchAll(/\{([A-Za-z][A-Za-z0-9_]*)\}/g)].map(match => match[1]).sort();
    for (const language of languages) {
        const raw = readFileSync(new URL(language + ".json", directory), "utf8"), catalog = JSON.parse(raw);
        assert.ok(raw.length <= 2 * 1024 * 1024, language + " catalog exceeds the browser limit");
        assert.ok(catalog && !Array.isArray(catalog) && typeof catalog === "object", language + " must be a message map");
        const actualKeys = Object.keys(catalog).sort();
        const missing = expectedKeys.filter(key => !Object.hasOwn(catalog, key));
        const extra = actualKeys.filter(key => !Object.hasOwn(english, key));
        assert.ok(!missing.length && !extra.length,
            `${language}: ${missing.length} missing / ${extra.length} extra keys; examples: ${JSON.stringify({missing: missing.slice(0, 4), extra: extra.slice(0, 4)})}`);
        if (language !== "en") assert.notEqual(catalog["Interface language"], english["Interface language"],
            language + " must translate the language control");
        for (const key of expectedKeys) {
            assert.equal(typeof catalog[key], "string", language + ": " + key);
            assert.ok(catalog[key].trim().length > 0, language + ": empty translation for " + key);
            assert.ok(key.length <= 4096 && catalog[key].length <= 8192, language + ": entry exceeds the browser limit");
            assert.deepEqual(placeholders(catalog[key]), placeholders(key), language + ": changed placeholders in " + key);
            if (language === "en") assert.equal(catalog[key], key, "English fallback changed for " + key);
        }
    }
    console.log(`UI catalogs: ${languages.length} languages, identical ${expectedKeys.length} message keys and interpolation placeholders passed`);
}
