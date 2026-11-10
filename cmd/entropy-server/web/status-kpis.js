"use strict";

// Derived browser KPIs from existing /metrics data. Admission-budget
// pressure is not RAM usage or QRNG entropy-buffer fill.
(function initKPIs() {
    const metricNames = {
        httpMemory: ["http_generation_memory_reserved_bytes", "http_generation_memory_budget_bytes", "http_generation_memory_peak_reserved_bytes"],
        wsMemory: ["ws_memory_reserved_bytes", "ws_memory_budget_bytes", "ws_memory_peak_reserved_bytes"],
        httpSlots: ["http_generation_requests_active", "http_generation_requests_limit", "http_generation_requests_peak"],
        wsSlots: ["ws_connections_active", "ws_connections_limit", "ws_connections_peak"],
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
    const rateNames = [
        "generation-rate", "ws-payload-rate", "http-request-rate", "ws-reject-rate",
        "http-reject-rate", "reseed-rate", "reseed-failure-rate",
    ];
    const extraNames = ["drbg-instances", "reseed-successes", "ws-rejections", "http-rejections"];
    const counterNames = [
        "rng_bytes_generated_total", "wss_payloads_total", "http_requests_total",
        "rng_reseeds_total", "rng_reseed_failures_total", ...httpRejectNames, ...wsRejectNames,
    ];
    // Store a bounded history for rates. At fast polling intervals a 5s window
    // reduces the noise of using only two adjacent snapshots.
    const history = [];

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

    function gauge(name, used, capacity, peak, isMemory) {
        const card = document.getElementById(`kpi-${name}`);
        const meter = document.getElementById(`kpi-${name}-meter`);
        const shadow = document.getElementById(`kpi-${name}-peak-shadow`);
        if (used === null || capacity === null || capacity <= 0) {
            card.dataset.state = "unknown";
            text(`kpi-${name}-value`, "N/A");
            text(`kpi-${name}-detail`, "No valid budget/limit reported");
            meter.value = 0;
            meter.setAttribute("aria-valuetext", "Not available");
            shadow.style.width = "0%";
            text(`kpi-${name}-peak-detail`, "PEAK N/A");
            return;
        }
        const percent = used / capacity * 100;
        const displayed = `${percent.toFixed(1)}%`;
        card.dataset.state = percent >= 90 ? "critical" : percent >= 75 ? "warning" : "normal";
        text(`kpi-${name}-value`, displayed);
        text(`kpi-${name}-detail`, `${isMemory ? bytes(used) : count(used)} / ${isMemory ? bytes(capacity) : count(capacity)}`);
        meter.value = Math.min(100, Math.max(0, percent));
        meter.setAttribute("aria-valuetext", displayed);
        // Exact process-lifetime high-water marks come from the server,
        // not a per-browser sampled maximum. Missing metrics are explicit.
        if (peak === null || peak < used) {
            shadow.style.width = "0%";
            text(`kpi-${name}-peak-detail`, "PEAK N/A");
        } else {
            const peakPercent = Math.max(0, Math.min(100, peak / capacity * 100));
            shadow.style.width = `${peakPercent}%`;
            const formattedPeak = isMemory ? bytes(peak) : count(peak);
            text(`kpi-${name}-peak-detail`, `PEAK ${peakPercent.toFixed(1)}% · ${formattedPeak}`);
        }
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

    // The dim track represents the highest observed one-second average since
    // server startup; the bright fill is the browser's recent current rate.
    // Distinct sampling windows mean a current estimate may briefly exceed
    // a server peak; clamp the drawing without changing either measurement.
    function peakMeter(id, current, peak, unit, digits) {
        const meter = document.getElementById(`kpi-${id}-peak-meter`);
        if (peak === null || current === null) {
            meter.value = 0;
            text(`kpi-${id}-peak-label`, "PEAK N/A · AWAITING SAMPLES");
            return;
        }

        if (peak <= 0) {
            meter.value = 0;
            text(`kpi-${id}-peak-label`, "PEAK 0 " + unit);
            return;
        }

        const fraction = Math.max(0, Math.min(100, 100 * current / peak));
        meter.value = fraction;
        text(`kpi-${id}-peak-label`, `PEAK ${peak.toFixed(digits)} ${unit}`);
    }

    function update(source) {
        const metrics = readMetrics(source);
        if (metrics.size === 0) throw new Error("No numeric Prometheus metrics available");
        const now = performance.now();
        const latest = history.at(-1);
        if (latest && counterNames.some(name => {
            const old = get(latest.metrics, name);
            const current = get(metrics, name);
            return old !== null && current !== null && current < old;
        })) history.length = 0;

        history.push({at: now, metrics});
        // Retain one older sample even when the refresh interval exceeds 5s.
        while (history.length > 2 && (history.length > 128 || now - history[1].at >= 5000)) history.shift();
        const baseline = history.length > 1 ? history[0] : null;
        const elapsed = baseline ? (now - baseline.at) / 1000 : 0;
        const prior = baseline ? baseline.metrics : null;

        for (const [name, [used, capacity, peak]] of Object.entries(metricNames)) {
            gauge(name, get(metrics, used), get(metrics, capacity), get(metrics, peak), name.includes("Memory"));
        }

        const generationPerSecond = perSecond(metrics, prior, "rng_bytes_generated_total", elapsed);
        rate("kpi-generation-rate", generationPerSecond === null ? null : generationPerSecond / 1048576, "MiB/s", 2);
        rate("kpi-ws-payload-rate", perSecond(metrics, prior, "wss_payloads_total", elapsed), "messages/s", 1);
        rate("kpi-http-request-rate", perSecond(metrics, prior, "http_requests_total", elapsed), "req/s", 1);
        rate("kpi-ws-reject-rate", rejectionRate(metrics, prior, wsRejectNames, elapsed), "rejects/min", 1);
        const httpRejectPace = rejectionRate(metrics, prior, httpRejectNames, elapsed);
        rate("kpi-http-reject-rate", httpRejectPace, "rejects/min", 1);

        const drbgInstances = get(metrics, "drbg_instance_count");
        peakMeter("generation-rate",
            generationPerSecond === null ? null : generationPerSecond / 1048576,
            get(metrics, "rng_generation_peak_bytes_per_second") === null ? null :
                get(metrics, "rng_generation_peak_bytes_per_second") / 1048576, "MiB/s", 2);
        peakMeter("ws-payload-rate", perSecond(metrics, prior, "wss_payloads_total", elapsed),
            get(metrics, "wss_payloads_peak_per_second"), "messages/s", 1);
        peakMeter("http-request-rate", perSecond(metrics, prior, "http_requests_total", elapsed),
            get(metrics, "http_requests_peak_per_second"), "req/s", 1);
        peakMeter("ws-reject-rate", rejectionRate(metrics, prior, wsRejectNames, elapsed),
            get(metrics, "ws_rejections_peak_per_minute"), "rejects/min", 1);
        peakMeter("http-reject-rate", httpRejectPace,
            get(metrics, "http_generation_rejections_peak_per_minute"), "rejects/min", 1);
        peakMeter("drbg-instances", drbgInstances, get(metrics, "drbg_instances_peak"), "instances", 0);
        text("kpi-drbg-instances", count(drbgInstances));
        text("kpi-reseed-successes", count(get(metrics, "rng_reseeds_total")));
        text("kpi-ws-rejections", count(sum(metrics, wsRejectNames)));
        text("kpi-http-rejections", count(sum(metrics, httpRejectNames)));
        const reseedRate = perSecond(metrics, prior, "rng_reseeds_total", elapsed);
        const failureRate = perSecond(metrics, prior, "rng_reseed_failures_total", elapsed);
        rate("kpi-reseed-rate", reseedRate === null ? null : reseedRate * 60, "reseeds/min", 1);
        rate("kpi-reseed-failure-rate", failureRate === null ? null : failureRate * 60, "failures/min", 1);
    }

    function updateReady(body) {
        const target = document.getElementById("kpi-reseed-headroom-card");
        const age = body?.reseed_age_ms;
        const limit = body?.max_reseed_age_ms;
        if (!Number.isFinite(age) || !Number.isFinite(limit) || age < 0 || limit <= 0) {
            target.dataset.state = "unknown";
            text("kpi-reseed-headroom", "N/A");
            return;
        }
        const remaining = Math.max(0, (limit - age) / limit * 100);
        target.dataset.state = body.ready === false || body.last_reseed_error ||
            remaining <= 10 ? "critical" : remaining <= 25 ? "warning" : "normal";
        text("kpi-reseed-headroom", `${remaining.toFixed(1)}%`);
    }

    function unavailable() {
        history.length = 0;
        for (const name of gaugeNames) gauge(name, null, null, null, name.includes("Memory"));
        for (const name of rateNames) text(`kpi-${name}`, "—");
        for (const name of extraNames) text(`kpi-${name}`, "—");
        for (const id of ["generation-rate", "ws-payload-rate", "http-request-rate",
            "ws-reject-rate", "http-reject-rate", "drbg-instances"]) {
            peakMeter(id, null, null, "", 0);
        }
    }

    window.EntropyStatusKPIs = {update, unavailable, updateReady};
})();
