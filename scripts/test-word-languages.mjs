import assert from "node:assert/strict";
import {readFileSync} from "node:fs";
import vm from "node:vm";

const root = new URL("../cmd/entropy-server/", import.meta.url);
const source = readFileSync(new URL("web/words-page.js", root), "utf8");
const languages = JSON.parse(readFileSync(new URL("internal/wordlist/manifest.json", root), "utf8"))
    .map(info => ({...info, selection_bits_per_word: Math.log2(info.size)}));
const aliases = {pt: "pt-BR", zh: "zh-Hans", "zh-cn": "zh-Hans", "zh-sg": "zh-Hans", "zh-tw": "zh-Hant", "zh-hk": "zh-Hant", "zh-mo": "zh-Hant"};
const catalog = {default: "en", languages, aliases};
const flush = () => new Promise(resolve => setImmediate(resolve));

class Element {
    constructor() { this.children = []; this.events = new Map(); this.attributes = {}; this.dataset = {}; this.disabled = true; }
    set innerHTML(_value) { throw new Error("word UI must not interpret network text as HTML"); }
    append(...children) { this.children.push(...children); }
    replaceChildren(...children) { this.children = children; }
    setAttribute(key, value) { this.attributes[key] = value; }
    addEventListener(key, callback) { this.events.set(key, callback); }
}

async function page(query = "", {secure = false, pending = false, catalogFailure = false, selected} = {}) {
    const elements = new Map(["stream-notice", "words-content", "hash", "word-language", "word-language-apply", "word-language-form", "wordlist-info"]
        .map(id => [id, new Element()]));
    const events = new Map(), sockets = [], submitted = [], assigned = [];
    let cancellations = 0, reloads = 0, paint, signal, resolveFetch;
    const url = new URL((secure ? "https:" : "http:") + "//localhost:8080/api-words.html" + query);
    const location = {href: url.href, search: url.search, protocol: url.protocol, host: url.host,
        assign: target => assigned.push(target), reload: () => reloads++};
    const document = {getElementById: id => elements.get(id), createElement: () => new Element(), createDocumentFragment: () => new Element()};
    const window = {addEventListener: (key, callback) => events.set(key, callback), EntropyDisplayClock: {
        create(callback) { paint = callback; return {submit(data) { submitted.push(data); paint(data); }, cancel() { cancellations++; }}; }
    }};
    class Socket {
        constructor(endpoint) { this.url = endpoint; this.closed = []; sockets.push(this); }
        close(...args) { this.closed.push(args); }
    }
    const response = {ok: !catalogFailure, json: async () => ({...catalog, selected})};
    const fetch = (endpoint, options) => {
        assert.equal(endpoint, "/wordlists.json"); signal = options.signal;
        return pending ? new Promise(resolve => { resolveFetch = () => resolve(response); }) : Promise.resolve(response);
    };
    vm.runInNewContext(source, {window, document, location, fetch, WebSocket: Socket, AbortController, URL, URLSearchParams});
    await flush();
    return {elements, events, sockets, submitted, assigned, get cancellations() { return cancellations; }, get reloads() { return reloads; },
        get signal() { return signal; }, resolveFetch, status: () => elements.get("stream-notice"),
        submit() { elements.get("word-language-form").events.get("submit")({preventDefault() {}}); }};
}

for (const language of languages) {
    const p = await page(`?words=6&refresh=50&lang=${language.code}`, {secure: true});
    assert.equal(p.elements.get("word-language").children.length, languages.length);
    assert.equal(p.elements.get("word-language").value, language.code);
    assert.equal(p.elements.get("words-content").attributes.lang, language.code);
    assert.equal(p.elements.get("words-content").attributes.dir, "auto");
    const endpoint = new URL(p.sockets[0].url);
    assert.equal(endpoint.protocol, "wss:");
    assert.equal(endpoint.pathname, "/words");
    assert.equal(endpoint.searchParams.get("lang"), language.code);
    assert.equal(endpoint.searchParams.get("words"), "6");
    assert.equal(endpoint.searchParams.get("refresh"), "50");
    const data = {words: "<script>悪</script> 가", hash: "a".repeat(64), language: language.code, wordlist: language.id};
    p.sockets[0].onmessage({data: JSON.stringify(data)});
    assert.equal(p.submitted.length, 1);
    const fragment = p.elements.get("words-content").children[0];
    assert.equal(fragment.children[0].textContent, "<script>悪</script>");
    assert.equal(fragment.children[2].textContent, "가");
    assert.equal(p.elements.get("hash").textContent, data.hash);
    assert.equal(p.status().hidden, true);
    if (language.kind === "bip39-word-data") assert.match(p.elements.get("wordlist-info").textContent, /not wallet mnemonics/);
    if (language.kind === "spelling-word-data") assert.match(p.elements.get("wordlist-info").textContent, /not curated as a passphrase list/);
}

const defaultPage = await page("?words=8&refresh=100#section");
assert.equal(new URL(defaultPage.sockets[0].url).searchParams.get("lang"), "en");
defaultPage.elements.get("word-language").value = "ko";
defaultPage.submit();
const navigation = new URL(defaultPage.assigned[0], "http://localhost:8080");
assert.equal(navigation.searchParams.get("lang"), "ko");
assert.equal(navigation.searchParams.get("words"), "8");
assert.equal(navigation.searchParams.get("refresh"), "100");
assert.equal(navigation.hash, "#section");
for (const query of ["?lang=PT", "?lang=pt-br"]) {
    const p = await page(query);
    assert.equal(new URL(p.sockets[0].url).searchParams.get("lang"), "pt-BR");
}
for (const [alias, code] of Object.entries(aliases)) {
    const p = await page("?lang=" + alias.toUpperCase());
    assert.equal(new URL(p.sockets[0].url).searchParams.get("lang"), code);
}
const remembered = await page("?words=6", {secure: true, selected: "ar"});
assert.equal(remembered.elements.get("word-language").value, "ar");
assert.equal(new URL(remembered.sockets[0].url).searchParams.get("lang"), "ar");
const override = await page("?lang=ru", {selected: "ar"});
assert.equal(override.elements.get("word-language").value, "ru");
const invalidRemembered = await page("?lang=unknown", {selected: "ar"});
assert.equal(invalidRemembered.sockets.length, 0, "invalid explicit query must not use a cookie selection");
for (const query of ["?lang=unknown", "?lang=", "?lang=en&lang=ja"]) {
    const p = await page(query);
    assert.equal(p.sockets.length, 0, "invalid selection must not silently generate English");
    assert.equal(p.status().dataset.state, "error");
    assert.equal(p.elements.get("word-language").disabled, false, "allow correcting an invalid URL selection");
}
const failed = await page("", {catalogFailure: true});
assert.equal(failed.sockets.length, 0);
assert.match(failed.status().textContent, /Could not load/);

for (const payload of ["invalid json", JSON.stringify({words: "x", hash: "bad"}), JSON.stringify({words: "x", hash: "a".repeat(64), language: "ja", wordlist: languages[0].id})]) {
    const p = await page();
    p.sockets[0].onmessage({data: payload});
    assert.equal(p.submitted.length, 0);
    assert.equal(p.status().dataset.state, "error");
    assert.equal(p.sockets[0].closed[0][0], 1002);
    p.sockets[0].onclose();
    assert.equal(p.status().dataset.state, "error", "close must not hide the protocol error");
}
const pending = await page("?lang=ja", {pending: true});
pending.events.get("pagehide")();
assert.equal(pending.signal.aborted, true);
pending.resolveFetch();
await flush();
assert.equal(pending.sockets.length, 0, "late catalog must not open a socket after navigation");
const disposed = await page();
disposed.events.get("pagehide")();
disposed.sockets[0].onmessage({data: JSON.stringify({words: "x", hash: "a".repeat(64), language: "en", wordlist: languages[0].id})});
assert.equal(disposed.submitted.length, 0);
assert.equal(disposed.sockets[0].closed.length, 1);
assert.ok(disposed.cancellations > 0);
disposed.events.get("pageshow")({persisted: true});
assert.equal(disposed.reloads, 1);
console.log(`${languages.length} dictionary choices, aliases, remembered language, Unicode text, source identity, errors, navigation and cancellation passed`);
