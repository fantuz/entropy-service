"use strict";

// Runs before the stylesheet so the saved appearance is applied before paint.
(() => {
    const root = document.documentElement;
    const key = "entropy-theme";
    const system = window.matchMedia("(prefers-color-scheme: dark)");
    const valid = value => value === "light" || value === "dark";
    let preference = null;
    try { preference = localStorage.getItem(key); } catch { /* Storage can be disabled. */ }

    function apply() {
        const dark = valid(preference) ? preference === "dark" : system.matches;
        root.dataset.theme = dark ? "dark" : "light";
        for (const button of document.querySelectorAll("[data-theme-toggle]")) {
            button.hidden = false;
            button.textContent = dark ? "Light mode" : "Dark mode";
            button.title = `Switch to ${dark ? "light" : "dark"} mode`;
        }
        window.dispatchEvent(new Event("entropythemechange"));
    }
    apply();
    document.addEventListener("DOMContentLoaded", () => {
        for (const button of document.querySelectorAll("[data-theme-toggle]")) {
            button.addEventListener("click", () => {
                preference = root.dataset.theme === "dark" ? "light" : "dark";
                try { localStorage.setItem(key, preference); } catch { /* The toggle still works for this page. */ }
                apply();
            });
        }
        apply();
    }, {once: true});
    system.addEventListener("change", () => { if (!valid(preference)) apply(); });
    window.addEventListener("storage", event => {
        if (event.key === key || event.key === null) { preference = event.newValue; apply(); }
    });
})();
