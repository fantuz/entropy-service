"use strict";

// Independent of the light/dark theme. Apply before CSS and reconcile the
// current stored value when a page returns from another tab or browser cache.
(() => {
    const root = document.documentElement, key = "entropy-graph-palette";
    const choices = new Set(["classic", "blue-orange", "contrast"]);
    let preference = "classic", localOnly = false;

    function apply() {
        const palette = choices.has(preference) ? preference : "classic";
        const changed = root.dataset.graphPalette !== palette;
        root.dataset.graphPalette = palette;
        for (const select of document.querySelectorAll("[data-graph-palette]")) select.value = palette;
        if (changed) window.dispatchEvent(new Event("entropygraphpalettechange"));
    }
    function sync() {
        if (!localOnly) {
            try { preference = localStorage.getItem(key); } catch { /* Keep this page's choice if storage is unavailable. */ }
        }
        apply();
    }
    sync();
    document.addEventListener("DOMContentLoaded", () => {
        for (const select of document.querySelectorAll("[data-graph-palette]")) {
            select.addEventListener("change", () => {
                if (!choices.has(select.value)) { apply(); return; }
                preference = select.value;
                try { localStorage.setItem(key, preference); localOnly = false; }
                catch { localOnly = true; /* Retain this page's choice even if reads still return an older saved value. */ }
                apply();
            });
        }
        sync();
    }, {once: true});
    window.addEventListener("storage", event => {
        // Queued events can describe an older choice. The current value wins.
        if (event.key === key || event.key === null) sync();
    });
    window.addEventListener("pageshow", sync);
    window.addEventListener("focus", sync);
    document.addEventListener("visibilitychange", () => { if (!document.hidden) sync(); });
})();
