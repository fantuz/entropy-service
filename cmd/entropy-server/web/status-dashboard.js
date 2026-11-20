"use strict";

(() => {
    const t = (message, values = {}) => window.EntropyI18n?.t(message, values) ??
        message.replace(/\{(\w+)\}/g, (match, key) => Object.hasOwn(values, key) ? String(values[key]) : match);
    function setText(node, message, values = {}) {
        if (window.EntropyI18n) window.EntropyI18n.set(node, message, values);
        else node.textContent = t(message, values);
    }
    const refresh = document.getElementById("status-refresh");
    const intervalInput = document.getElementById("status-interval");
    let intervalMs = 5000;
    let timer = null;
    let deadline = null;
    let active = null;
    let onPage = true;

    function setStatus(name, state, value) {
        document.getElementById(name + "-card").dataset.state = state;
        setText(document.getElementById(name + "-value"), value);
    }

    function setValue(id, value) {
        const node = document.getElementById(id);
        const next = String(value ?? "—");
        if (node.textContent !== next) node.textContent = next;
    }

    async function read(path, signal) {
        let response;
        try {
            response = await fetch(path, {cache: "no-store", signal});
        } catch { return {error: "UNREACHABLE"}; }
        // A degraded server deliberately answers /ready with JSON and HTTP 503.
        if (!response.ok && path !== "/ready") return {error: "UNREACHABLE"};
        try {
            const body = path === "/metrics" ? await response.text() : await response.json();
            if (path !== "/metrics" && (body === null || typeof body !== "object" || Array.isArray(body))) return {error: "INVALID JSON"};
            return {ok: response.ok, body};
        } catch { return {error: path === "/metrics" ? "INVALID RESPONSE" : "INVALID JSON"}; }
    }

    function render(live, ready, metrics) {
        if (live.error) setStatus("live", "error", live.error);
        else if (live.body.status === "ok") setStatus("live", "ready", "ONLINE");
        else setStatus("live", "degraded", "UNEXPECTED RESPONSE");

        const body = ready.body;
        if (ready.error) setStatus("ready", "error", ready.error);
        else {
            const isReady = ready.ok && body.ready === true;
            setStatus("ready", isReady ? "ready" : "degraded", isReady ? "READY" : "DEGRADED");
        }
        setValue("rng-source", body?.rng_source);
        setValue("rng-drbg", body?.rng_drbg);
        setValue("reseed-age", Number.isFinite(body?.reseed_age_ms) ? `${body.reseed_age_ms} ms` : "—");
        setValue("reseed-failures", Number.isFinite(body?.reseed_failures_total) ? body.reseed_failures_total.toLocaleString() : "—");
        window.EntropyStatusKPIs.updateReady(body);

        if (!metrics.error) {
            try {
                window.EntropyStatusKPIs.update(metrics.body);
                setStatus("metrics", "ready", "AVAILABLE");
            } catch { metrics.error = "INVALID RESPONSE"; }
        }
        if (metrics.error) {
            window.EntropyStatusKPIs.unavailable();
            setStatus("metrics", "error", metrics.error);
        }

        setText(document.getElementById("status-updated"), "Last checked {time}", {time: new Date().toLocaleTimeString()});
    }

    async function update() {
        clearTimeout(timer);
        timer = null;
        if (active || !onPage || document.hidden) return;

        const request = new AbortController();
        active = request;
        refresh.disabled = true;
        deadline = setTimeout(() => request.abort(), 4000);
        try {
            const results = await Promise.all(["/live", "/ready", "/metrics"].map(path => read(path, request.signal)));
            if (active === request) render(...results);
        } finally {
            if (active === request) {
                clearTimeout(deadline);
                deadline = null;
                active = null;
                refresh.disabled = false;
                timer = setTimeout(update, intervalMs);
            }
        }
    }

    function syncVisibility() {
        if (onPage && !document.hidden) {
            if (!active && timer === null) void update();
            return;
        }
        clearTimeout(timer);
        timer = null;
        clearTimeout(deadline);
        deadline = null;
        active?.abort();
        active = null;
        refresh.disabled = false;
        window.EntropyStatusKPIs.resetHistory();
    }

    function changeInterval() {
        const requested = Number(intervalInput.value);
        if (intervalInput.value.trim() === "" || !Number.isFinite(requested)) {
            intervalInput.value = String(intervalMs / 1000);
            return;
        }
        intervalMs = Math.round(Math.min(60, Math.max(0.05, requested)) * 1000);
        intervalInput.value = String(intervalMs / 1000);
        // If a cycle is running, its completion will schedule the new interval.
        if (!active && onPage && !document.hidden) {
            clearTimeout(timer);
            timer = setTimeout(update, intervalMs);
        }
    }

    intervalInput.addEventListener("change", changeInterval);
    refresh.addEventListener("click", update);
    document.addEventListener("visibilitychange", syncVisibility);
    window.addEventListener("pagehide", () => { onPage = false; syncVisibility(); });
    window.addEventListener("pageshow", () => { onPage = true; syncVisibility(); });
    syncVisibility();
})();
