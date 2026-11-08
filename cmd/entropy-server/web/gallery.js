"use strict";

(() => {
    const sources = [
        { key: "image", path: "/v1/image/random?size=128", kind: "image" },
        { key: "heatmap", path: "/v1/image/heatmap?size=128", kind: "image" },
        { key: "qr", path: "/v1/qr/random", kind: "image" },
        { key: "words", path: "/v1/meta/random?words=8", kind: "words" },
    ];
    const intervalInput = document.getElementById("gallery-interval");
    const refreshButton = document.getElementById("gallery-refresh");
    const status = document.getElementById("gallery-status");
    const urls = new Map();
    let intervalMs = 10000;
    let timer = null;
    let controller = null;
    let busy = false;
    let stopped = false;
    let cycles = 0;

    function announce(message, state = "ready") {
        status.textContent = message;
        status.dataset.state = state;
    }

    function checkedInterval() {
        const seconds = Number(intervalInput.value);
        if (!Number.isInteger(seconds) || seconds < 5 || seconds > 120) {
            announce("Use 5–120 seconds", "error");
            return false;
        }
        intervalMs = seconds * 1000;
        return true;
    }

    async function readResponse(path, signal) {
        const response = await fetch(path, { signal, cache: "no-store" });
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
        return response;
    }

    async function loadImage(path, signal) {
        const response = await readResponse(path, signal);
        const blob = await response.blob();
        if (!blob.type.startsWith("image/")) throw new Error("Unexpected response type");
        const objectURL = URL.createObjectURL(blob);
        try {
            const preloaded = new Image();
            preloaded.src = objectURL;
            await preloaded.decode();
            return { kind: "image", objectURL };
        } catch (error) {
            URL.revokeObjectURL(objectURL);
            throw error;
        }
    }

    async function loadWords(path, signal) {
        const response = await readResponse(path, signal);
        const html = await response.text();
        const parsed = new DOMParser().parseFromString(html, "text/html");
        const words = parsed.querySelector(".words")?.textContent?.trim();
        const hash = parsed.querySelector(".hash")?.textContent?.trim();
        if (!words || !/^[0-9a-f]{64}$/i.test(hash || "")) {
            throw new Error("Unexpected words response");
        }
        return { kind: "words", words, hash };
    }

    function release(result) {
        if (result?.kind === "image") URL.revokeObjectURL(result.objectURL);
    }

    function render(source, result) {
        const placeholder = document.getElementById(`placeholder-${source.key}`);
        if (result.kind === "image") {
            const preview = document.getElementById(`preview-${source.key}`);
            const previous = urls.get(source.key);
            preview.src = result.objectURL;
            preview.hidden = false;
            urls.set(source.key, result.objectURL);
            if (previous) URL.revokeObjectURL(previous);
        } else {
            document.getElementById("gallery-word-text").textContent = result.words;
            document.getElementById("gallery-word-hash").textContent = result.hash;
            document.getElementById("preview-words").hidden = false;
        }
        placeholder.hidden = true;
        document.querySelector(`[data-gallery-key="${source.key}"]`).dataset.state = "ready";
    }

    function schedule(delay) {
        clearTimeout(timer);
        if (!stopped && !document.hidden) {
            timer = setTimeout(refresh, delay);
        }
    }

    async function refresh() {
        if (busy || stopped || document.hidden || !checkedInterval()) return;
        clearTimeout(timer);
        busy = true;
        refreshButton.disabled = true;
        controller = new AbortController();
        const signal = controller.signal;
        const startedAt = Date.now();
        announce("Loading batch…", "loading");
        // Prevent a slow endpoint from leaving every preview indefinitely waiting.
        const deadline = setTimeout(() => controller.abort(), Math.max(intervalMs, 15000));
        try {
            const settled = await Promise.allSettled(sources.map(source =>
                source.kind === "image" ? loadImage(source.path, signal) : loadWords(source.path, signal)
            ));
            if (stopped) {
                settled.forEach(item => { if (item.status === "fulfilled") release(item.value); });
                return;
            }
            let failed = 0;
            settled.forEach((item, index) => {
                const source = sources[index];
                if (item.status === "fulfilled") {
                    render(source, item.value);
                } else {
                    failed++;
                    document.querySelector(`[data-gallery-key="${source.key}"]`).dataset.state = "error";
                    // Preserve previous successful data, if any.
                    const placeholder = document.getElementById(`placeholder-${source.key}`);
                    if (!placeholder.hidden) placeholder.textContent = "Retrying…";
                }
            });
            cycles++;
            announce(failed ? `BATCH ${cycles} · ${4 - failed}/4 UPDATED · RETRYING` :
                `BATCH ${cycles} · 4/4 UPDATED`, failed ? "error" : "ready");
        } finally {
            clearTimeout(deadline);
            controller = null;
            busy = false;
            refreshButton.disabled = false;
            // Approximately the configured period between batch starts, never overlapping.
            schedule(Math.max(300, intervalMs - (Date.now() - startedAt)));
        }
    }

    refreshButton.addEventListener("click", refresh);
    intervalInput.addEventListener("change", () => {
        if (!checkedInterval()) return;
        if (!busy) schedule(intervalMs);
    });
    document.addEventListener("visibilitychange", () => {
        if (document.hidden) {
            clearTimeout(timer);
        } else if (!busy && !stopped) {
            schedule(0);
        }
    });
    window.addEventListener("pagehide", () => {
        stopped = true;
        clearTimeout(timer);
        controller?.abort();
        for (const objectURL of urls.values()) URL.revokeObjectURL(objectURL);
        urls.clear();
    });
    window.addEventListener("pageshow", event => {
        if (event.persisted) {
            stopped = false;
            schedule(0);
        }
    });
    schedule(0);
})();
