"use strict";

// UI text only. Payloads, dictionary words, protocol parameters and user input
// are never scanned or translated. English messages are the fallback keys.
(() => {
    const root = document.documentElement, storageKey = "entropy-ui-language";
    // The static handler replaces this token with the catalog content digest.
    // A changed deployment gets a new URL; unchanged catalogs reuse HTTP cache.
    const catalogVersion = "__ENTROPY_UI_CATALOG_VERSION__";
    const languages = {en: "English", fr: "Français", de: "Deutsch", es: "Español", cs: "Čeština",
        ru: "Русский", pl: "Polski", el: "Ελληνικά", it: "Italiano"};
    const supported = value => typeof value === "string" && Object.hasOwn(languages, value);
    const textBindings = new WeakMap(), attributeBindings = new WeakMap();
    const catalogs = new Map([["en", Object.freeze({})]]);
    let active = "en", catalog = catalogs.get("en"), requested = null, localOnly = false;
    let generation = 0, controller = null, inFlight = null, initial = true;

    function format(message, params = {}) {
        return message.replace(/\{([A-Za-z][A-Za-z0-9_]*)\}/g, (token, key) =>
            Object.hasOwn(params, key) ? String(params[key]) : token);
    }
    function t(message, params = {}) {
        const translated = Object.hasOwn(catalog, message) && typeof catalog[message] === "string" ? catalog[message] : message;
        return format(translated, params);
    }
    function set(element, message, params = {}) {
        if (!element) return;
        textBindings.set(element, {message, params: {...params}});
        element.setAttribute("data-i18n-live", "");
        const value = t(message, params);
        if (element.textContent !== value) element.textContent = value;
        return value;
    }
    function attr(element, name, message, params = {}) {
        if (!element || !["title", "aria-label", "placeholder", "alt"].includes(name)) return;
        const bindings = attributeBindings.get(element) || new Map();
        bindings.set(name, {message, params: {...params}});
        attributeBindings.set(element, bindings);
        element.setAttribute("data-i18n-live-attrs", "");
        element.setAttribute(name, t(message, params));
    }
    function nodes(scope, selector) {
        return [...(scope.matches?.(selector) ? [scope] : []), ...scope.querySelectorAll(selector)];
    }
    function translate(scope = document) {
        for (const node of nodes(scope, "[data-i18n]")) {
            if (textBindings.has(node)) continue;
            const key = node.getAttribute("data-i18n");
            const params = Object.fromEntries([...node.attributes]
                .filter(attribute => attribute.name.startsWith("data-i18n-param-"))
                .map(attribute => [attribute.name.slice(16), attribute.value]));
            const value = t(key, params);
            if (node.textContent !== value) node.textContent = value;
        }
        for (const node of nodes(scope, "[data-i18n-live]")) {
            const binding = textBindings.get(node);
            if (binding) set(node, binding.message, binding.params);
        }
        for (const name of ["title", "aria-label", "placeholder", "alt"]) {
            for (const node of nodes(scope, `[data-i18n-${name}]`)) {
                if (!attributeBindings.get(node)?.has(name)) node.setAttribute(name, t(node.getAttribute(`data-i18n-${name}`)));
            }
        }
        for (const node of nodes(scope, "[data-i18n-live-attrs]")) {
            for (const [name, binding] of attributeBindings.get(node) || []) node.setAttribute(name, t(binding.message, binding.params));
        }
    }
    function detectedLanguage() {
        for (const language of navigator.languages || [navigator.language || "en"]) {
            const code = String(language).toLowerCase().split("-")[0];
            if (supported(code)) return code;
        }
        return "en";
    }
    function preference() {
        if (localOnly) return requested || active;
        try {
            const saved = localStorage.getItem(storageKey);
            return saved === null ? detectedLanguage() : supported(saved) ? saved : "en";
        } catch { return requested || detectedLanguage(); }
    }
    function syncSelectors(value) {
        for (const select of document.querySelectorAll("select[data-ui-language]")) select.value = value;
    }
    function validCatalog(value) {
        if (!value || Array.isArray(value) || typeof value !== "object") return false;
        return Object.entries(value).every(([key, text]) => key.length <= 4096 && typeof text === "string" && text.length <= 8192);
    }
    function useLanguage(value, save = false) {
        const next = supported(value) ? value : "en";
        if (save) {
            try { localStorage.setItem(storageKey, next); localOnly = false; }
            catch { localOnly = true; }
        }
        if (requested === next) { syncSelectors(next); return inFlight || Promise.resolve(active); }
        requested = next;
        syncSelectors(next);
        const token = ++generation;
        controller?.abort(); controller = null;
        const work = (async () => {
            let selected = next;
            try {
                if (!catalogs.has(next)) {
                    const pending = new AbortController(); controller = pending;
                    const timer = setTimeout(() => pending.abort(), 8000);
                    try {
                        const response = await fetch(`/locales/${next}.json?v=${catalogVersion}`, {signal: pending.signal, cache: "default", credentials: "omit"});
                        if (!response.ok) throw new Error("Translation unavailable");
                        const source = await response.text();
                        if (source.length > 2 * 1024 * 1024) throw new Error("Translation too large");
                        const loaded = JSON.parse(source);
                        if (!validCatalog(loaded)) throw new Error("Invalid translation catalog");
                        catalogs.set(next, loaded);
                    } finally { clearTimeout(timer); }
                }
            } catch {
                if (token !== generation) return active;
                selected = "en";
            }
            if (token !== generation) return active;
            controller = null;
            const changed = active !== selected || initial;
            active = selected; requested = selected; catalog = catalogs.get(active); initial = false;
            root.lang = active; root.dataset.uiLanguage = active;
            syncSelectors(active);
            translate();
            if (changed) window.dispatchEvent(new Event("entropylanguagechange"));
            return active;
        })();
        inFlight = work;
        void work.finally(() => { if (inFlight === work) inFlight = null; });
        return work;
    }
    function sync() { void useLanguage(preference()); }
    function controls() {
        const toolbar = document.querySelector(".site-toolbar");
        if (!toolbar || toolbar.querySelector("[data-ui-language]")) return;
        const label = document.createElement("label"), caption = document.createElement("span"), select = document.createElement("select");
        label.className = "ui-language-control";
        select.id = "ui-language"; select.setAttribute("data-ui-language", "");
        label.htmlFor = select.id; caption.setAttribute("data-i18n", "Interface language"); caption.textContent = t("Interface language");
        for (const [code, name] of Object.entries(languages)) {
            const option = document.createElement("option"); option.value = code; option.textContent = name; option.lang = code; select.append(option);
        }
        select.value = requested || active;
        select.addEventListener("change", () => { void useLanguage(select.value, true); });
        label.append(caption, select);
        const themeToggle = toolbar.querySelector("[data-theme-toggle]");
        if (themeToggle) toolbar.insertBefore(label, themeToggle); else toolbar.append(label);
    }
    window.EntropyI18n = Object.freeze({t, set, attr, translate, useLanguage, languages: Object.freeze(languages), get language() { return active; }});
    // Initialize from a saved choice/browser preference while HTML is parsed.
    // The body retains English fallback until its selected catalog is ready.
    root.lang = "en"; root.dataset.uiLanguage = "en";
    document.addEventListener("DOMContentLoaded", () => { controls(); translate(); sync(); }, {once: true});
    window.addEventListener("storage", event => { if (event.key === storageKey || event.key === null) sync(); });
    window.addEventListener("pageshow", sync);
    window.addEventListener("focus", sync);
    document.addEventListener("visibilitychange", () => { if (!document.hidden) sync(); });
})();
