"use strict";

(() => {
    const refresh = document.getElementById("status-refresh");
    const intervalInput = document.getElementById("status-interval");
    let intervalMs = 5000;
    let timer = null;
    let running = false;
    let disposed = false;

    function setStatus(name, state, value) {
        document.getElementById(name + "-card").dataset.state = state;
        document.getElementById(name + "-value").textContent = value;
    }

    function setValue(id, value) {
        document.getElementById(id).textContent = value ?? "—";
    }

    async function read(url) {
        // Status 503 from /ready is a valid, informative JSON response.
        const response = await fetch(url, {cache: "no-store", signal: AbortSignal.timeout(4000)});
        return response;
    }

    function metric(source, name) {
        const line = source.split("\n").find(row => row.startsWith(name + " "));
        return line?.trim().split(/\s+/)[1] ?? null;
    }

    async function update() {
        if (running || disposed) return;
        clearTimeout(timer);
        timer = null;
        running = true;
        refresh.disabled = true;

        try {
        const checks = await Promise.allSettled([read("/live"), read("/ready"), read("/metrics-2")]);
        const [live, ready, metrics] = checks;

        if (live.status === "fulfilled" && live.value.ok) {
            try {
                const body = await live.value.json();
                setStatus("live", body.status === "ok" ? "ready" : "degraded", body.status === "ok" ? "ONLINE" : "UNEXPECTED RESPONSE");
            } catch (_) { setStatus("live", "error", "INVALID JSON"); }
        } else { setStatus("live", "error", "UNREACHABLE"); }

        if (ready.status === "fulfilled") {
            try {
                const body = await ready.value.json();
                const isReady = ready.value.ok && body.ready === true;
                setStatus("ready", isReady ? "ready" : "degraded", isReady ? "READY" : "DEGRADED");
                setValue("rng-source", body.rng_source);
                setValue("rng-drbg", body.rng_drbg);
                setValue("reseed-age", Number.isFinite(body.reseed_age_ms) ? `${body.reseed_age_ms} ms` : "—");
                setValue("reseed-failures", body.reseed_failures_total?.toLocaleString());
                window.EntropyStatusKPIs.updateReady(body);
            } catch (_) {
                setStatus("ready", "error", "INVALID JSON");
                window.EntropyStatusKPIs.updateReady(null);
                for (const id of ["rng-source", "rng-drbg", "reseed-age", "reseed-failures"]) setValue(id, "—");
            }
        } else {
            setStatus("ready", "error", "UNREACHABLE");
            window.EntropyStatusKPIs.updateReady(null);
            for (const id of ["rng-source", "rng-drbg", "reseed-age", "reseed-failures"]) setValue(id, "—");
        }

        if (metrics.status === "fulfilled" && metrics.value.ok) {
            try {
                const body = await metrics.value.text();
                setValue("bytes-generated", metric(body, "rng_bytes_generated_total"));
                setValue("active-websockets", metric(body, "ws_connections_active"));
                window.EntropyStatusKPIs.update(body);
                setStatus("metrics", "ready", "AVAILABLE");
            } catch (_) {
                window.EntropyStatusKPIs.unavailable();
                setStatus("metrics", "error", "INVALID RESPONSE");
                setValue("bytes-generated", "—");
                setValue("active-websockets", "—");
            }
        } else {
            window.EntropyStatusKPIs.unavailable();
            setStatus("metrics", "error", "UNREACHABLE");
            setValue("bytes-generated", "—");
            setValue("active-websockets", "—");
        }

        setValue("status-updated", `Last checked ${new Date().toLocaleTimeString()}`);
        } finally {
            running = false;
            refresh.disabled = false;
            if (!disposed) timer = setTimeout(update, intervalMs);
        }
    }

    function changeInterval() {
        const requested = Number(intervalInput.value);
        if (intervalInput.value.trim() === "" || !Number.isFinite(requested)) {
            intervalInput.value = String(intervalMs / 1000);
            return;
        }
        intervalMs = Math.round(Math.min(60, Math.max(0.25, requested)) * 1000);
        intervalInput.value = String(intervalMs / 1000);
        // If a cycle is running, its completion will schedule the new interval.
        if (!running && !disposed) {
            clearTimeout(timer);
            timer = setTimeout(update, intervalMs);
        }
    }

    intervalInput.addEventListener("change", changeInterval);
    refresh.addEventListener("click", () => {
        clearTimeout(timer);
        void update();
    });
    window.addEventListener("pagehide", () => { disposed = true; clearTimeout(timer); });
    void update();
})();
