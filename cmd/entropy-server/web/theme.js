"use strict";

// Runs before the stylesheet so the saved appearance is applied before paint.
(() => {
    const root = document.documentElement;
    const key = "entropy-theme";
    const system = window.matchMedia("(prefers-color-scheme: dark)");
    const valid = value => value === "light" || value === "dark";
    let preference = null;

    function sync() {
        try { preference = localStorage.getItem(key); } catch { /* Keep this page's choice when storage is disabled. */ }
        apply();
    }

    function apply() {
        const dark = valid(preference) ? preference === "dark" : system.matches;
        const theme = dark ? "dark" : "light";
        const changed = root.dataset.theme !== theme;
        root.dataset.theme = theme;
        for (const button of document.querySelectorAll("[data-theme-toggle]")) {
            button.hidden = false;
            button.textContent = dark ? "Light mode" : "Dark mode";
            button.title = `Switch to ${dark ? "light" : "dark"} mode`;
        }
        if (changed) window.dispatchEvent(new Event("entropythemechange"));
    }
    sync();
    document.addEventListener("DOMContentLoaded", () => {
        for (const button of document.querySelectorAll("[data-theme-toggle]")) {
            button.addEventListener("click", () => {
                preference = root.dataset.theme === "dark" ? "light" : "dark";
                try { localStorage.setItem(key, preference); } catch { /* The toggle still works for this page. */ }
                apply();
            });
        }
        sync();
    }, {once: true});
    system.addEventListener("change", sync);
    window.addEventListener("storage", event => {
        // An event can be queued while another window makes a newer choice.
        // Read the current value rather than replaying an old event payload.
        if (event.key === key || event.key === null) sync();
    });
    window.addEventListener("pageshow", sync);
    window.addEventListener("focus", sync);
    document.addEventListener("visibilitychange", () => { if (!document.hidden) sync(); });
})();
