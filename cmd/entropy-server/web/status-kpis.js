"use strict";

// Derived browser KPIs from existing /metrics-2 data. Admission-budget
// pressure is not RAM usage or QRNG entropy-buffer fill.
(function initKPIs() {
    const metricNames = {
        httpMemory: ["http_generation_memory_reserved_bytes", "http_generation_memory_budget_bytes"],
        wsMemory: ["ws_memory_reserved_bytes", "ws_memory_budget_bytes"],
        httpSlots: ["http_generation_requests_active", "http_generation_requests_limit"],
        wsSlots: ["ws_connections_active", "ws_connections_limit"],
    };
    const httpRejectNames = [
        "http_generation_rejected_requests_total",
        "http_generation_rejected_memory_total",
        "http_generation_rejected_size_total",
    ];
    const wsRejectNames = [
        "ws_rejected_connections_total",
        "ws_rejected_memory_total",
        "ws_rejected_size_total",
    ];
    const gaugeNames = Object.keys(metricNames);
    const rateNames = ["generation-rate", "ws-payload-rate", "http-request-rate", "ws-reject-rate", "http-reject-rate"];
    const extraNames = ["drbg-instances", "reseed-successes", "ws-rejections", "http-rejections"];
    let previous = null;

    function readMetrics(source) {
        const metrics = new Map();
        for (const line of source.split(/\r?\n/)) {
            if (!line || line.startsWith("#")) continue;
            const fields = /^([a-zA-Z_:][a-zA-Z0-9_:]*)\s+(\S+)(?:\s|$)/.exec(line);
            if (!fields) continue;
            const value = Number(fields[2]);
            if (Number.isFinite(value) && value >= 0) metrics.set(fields[1], value);
        }
        return metrics;
    }

    function get(metrics, name) {
        return metrics.has(name) ? metrics.get(name) : null;
    }

    function sum(metrics, names) {
        let total = 0;
        for (const name of names) {
            const value = get(metrics, name);
            if (value === null) return null;
            total += value;
        }
        return total;
    }

    function text(id, value) {
        document.getElementById(id).textContent = value;
    }

    function count(value) {
        return value === null ? "—" : Math.round(value).toLocaleString("en-US");
    }

    function bytes(value) {
        if (value === null) return "—";
        if (value >= 1073741824) return `${(value / 1073741824).toFixed(2)} GiB`;
        if (value >= 1048576) return `${(value / 1048576).toFixed(1)} MiB`;
        if (value >= 1024) return `${(value / 1024).toFixed(1)} KiB`;
        return `${Math.round(value)} B`;
    }

    function gauge(name, used, capacity, isMemory) {
        const card = document.getElementById(`kpi-${name}`);
        const meter = document.getElementById(`kpi-${name}-meter`);
        if (used === null || capacity === null || capacity <= 0) {
            card.dataset.state = "unknown";
            text(`kpi-${name}-value`, "N/A");
            text(`kpi-${name}-detail`, "No valid budget/limit reported");
            meter.value = 0;
            meter.setAttribute("aria-valuetext", "Not available");
            return;
        }
        const percent = used / capacity * 100;
        const displayed = `${percent.toFixed(1)}%`;
        card.dataset.state = percent >= 90 ? "critical" : percent >= 75 ? "warning" : "normal";
        text(`kpi-${name}-value`, displayed);
        text(`kpi-${name}-detail`, `${isMemory ? bytes(used) : count(used)} / ${isMemory ? bytes(capacity) : count(capacity)}`);
        meter.value = Math.min(100, Math.max(0, percent));
        meter.setAttribute("aria-valuetext", displayed);
    }

    function perSecond(metrics, prior, name, elapsedSeconds) {
        const current = get(metrics, name);
        const old = prior === null ? null : get(prior, name);
        if (current === null || old === null || elapsedSeconds <= 0 || current < old) return null;
        return (current - old) / elapsedSeconds;
    }

    function rejectionRate(metrics, prior, names, elapsedSeconds) {
        if (prior === null || elapsedSeconds <= 0) return null;
        let delta = 0;
        for (const name of names) {
            const current = get(metrics, name);
            const old = get(prior, name);
            if (current === null || old === null || current < old) return null;
            delta += current - old;
        }
        return delta * 60 / elapsedSeconds;
    }

    function rate(id, value, suffix, decimals) {
        text(id, value === null ? "SAMPLING…" : `${value.toFixed(decimals)} ${suffix}`);
    }

    function update(source) {
        const metrics = readMetrics(source);
        if (metrics.size === 0) throw new Error("No numeric Prometheus metrics available");
        const now = performance.now();
        const elapsed = previous ? (now - previous.at) / 1000 : 0;
        const prior = previous ? previous.metrics : null;

        for (const [name, [used, capacity]] of Object.entries(metricNames)) {
            gauge(name, get(metrics, used), get(metrics, capacity), name.includes("Memory"));
        }

        const generationPerSecond = perSecond(metrics, prior, "rng_bytes_generated_total", elapsed);
        rate("kpi-generation-rate", generationPerSecond === null ? null : generationPerSecond / 1048576, "MiB/s", 2);
        rate("kpi-ws-payload-rate", perSecond(metrics, prior, "wss_payloads_total", elapsed), "frames/s", 1);
        rate("kpi-http-request-rate", perSecond(metrics, prior, "http_requests_total", elapsed), "req/s", 1);
        rate("kpi-ws-reject-rate", rejectionRate(metrics, prior, wsRejectNames, elapsed), "rejects/min", 1);
        rate("kpi-http-reject-rate", rejectionRate(metrics, prior, httpRejectNames, elapsed), "rejects/min", 1);
        text("kpi-drbg-instances", count(get(metrics, "drbg_instance_count")));
        text("kpi-reseed-successes", count(get(metrics, "rng_reseeds_total")));
        text("kpi-ws-rejections", count(sum(metrics, wsRejectNames)));
        text("kpi-http-rejections", count(sum(metrics, httpRejectNames)));
        previous = {at: now, metrics};
    }

    function unavailable() {
        previous = null;
        for (const name of gaugeNames) gauge(name, null, null, name.includes("Memory"));
        for (const name of rateNames) text(`kpi-${name}`, "—");
        for (const name of extraNames) text(`kpi-${name}`, "—");
    }

    window.EntropyStatusKPIs = {update, unavailable};
})();
