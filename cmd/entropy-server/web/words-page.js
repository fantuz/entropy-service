/* Language-specific dictionaries; network samples retain the shared display clock. */
"use strict";
(() => {
    const params = new URLSearchParams(location.search);
    const status = document.getElementById("stream-notice");
    const words = document.getElementById("words-content");
    const hash = document.getElementById("hash");
    const select = document.getElementById("word-language");
    const apply = document.getElementById("word-language-apply");
    const form = document.getElementById("word-language-form");
    const info = document.getElementById("wordlist-info");
    const lifetime = new AbortController();
    let ws = null, failed = false, disposed = false, language = null;
    const displayClock = window.EntropyDisplayClock.create(paintLatest);

    function setStatus(state, message) {
        status.hidden = false;
        status.dataset.state = state;
        status.textContent = message;
    }

    function paintLatest(data) {
        const phrase = document.createDocumentFragment();
        data.words.split(" ").forEach((word, i) => {
            if (i > 0) phrase.append(" ");
            const item = document.createElement("span");
            item.textContent = word;
            phrase.append(item);
        });
        words.replaceChildren(phrase);
        hash.textContent = data.hash;
        status.hidden = true;
    }

    form.addEventListener("submit", event => {
        event.preventDefault();
        if (select.disabled) return;
        const next = new URL(location.href);
        next.searchParams.set("lang", select.value);
        location.assign(next.pathname + next.search + next.hash);
    });

    async function start() {
        try {
            const response = await fetch("/wordlists.json", {signal: lifetime.signal});
            if (!response.ok) throw new Error("Could not load word languages");
            const catalog = await response.json();
            if (disposed) return;
            if (!Array.isArray(catalog.languages) || !catalog.languages.length) {
                throw new Error("Invalid wordlist catalog");
            }
            select.replaceChildren();
            catalog.languages.forEach(item => {
                const option = document.createElement("option");
                option.value = item.code;
                option.textContent = item.name === item.native_name ? item.name : `${item.name} — ${item.native_name}`;
                option.selected = item.code === catalog.default;
                select.append(option);
            });
            select.value = catalog.default;
            select.disabled = apply.disabled = false;
            const values = params.getAll("lang");
            if (values.length > 1 || values.length === 1 && !values[0]) {
                throw new Error("lang must be supplied once with a supported code; choose a language and Apply");
            }
            let requested = (values.length ? values[0] : catalog.default).toLowerCase();
            if (requested === "pt") requested = "pt-br";
            language = catalog.languages.find(item => item.code.toLowerCase() === requested);
            if (!language) throw new Error("Unsupported word language; choose a language and Apply");
            select.value = language.code;
            words.setAttribute("lang", language.code);
            const mnemonic = language.kind === "bip39-word-data" ? " Dictionary only; samples are not wallet mnemonics." : "";
            info.textContent = `${language.title} · ${language.size.toLocaleString()} entries · ${language.selection_bits_per_word.toFixed(2)} bits/word of uniform selection.${mnemonic}`;
            params.set("lang", language.code);
            ws = new WebSocket((location.protocol === "https:" ? "wss://" : "ws://") + location.host + "/words?" + params.toString());
            ws.onopen = () => { if (!disposed) setStatus("waiting", "Awaiting first sample"); };
            ws.onerror = () => { if (!disposed) { failed = true; setStatus("error", "Connection failed"); } };
            ws.onclose = () => {
                if (!failed && !disposed) setStatus("disconnected", "Stream closed — reload to retry");
            };
            ws.onmessage = event => {
                if (disposed) return;
                try {
                    const data = JSON.parse(event.data);
                    if (typeof data.words !== "string" || typeof data.hash !== "string" ||
                        !/^[0-9a-f]{64}$/.test(data.hash) || data.language !== language.code || data.wordlist !== language.id) {
                        throw new Error("Invalid words response or wordlist mismatch");
                    }
                    displayClock.submit(data);
                } catch (error) {
                    failed = true;
                    setStatus("error", error.message);
                    displayClock.cancel();
                    ws.close(1002, "invalid words response");
                }
            };
        } catch (error) {
            if (!disposed) setStatus("error", error.message);
        }
    }

    window.addEventListener("pagehide", () => {
        disposed = true;
        lifetime.abort();
        displayClock.cancel();
        if (ws) ws.close();
    });
    window.addEventListener("pageshow", event => { if (event.persisted) location.reload(); });
    start();
})();
