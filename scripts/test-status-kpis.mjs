import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import vm from "node:vm";

// Dependency-free test of the actual browser KPI implementation.
const elements = new Map();
const document = {
    getElementById(id) {
        if (!elements.has(id)) {
            elements.set(id, {
                dataset: {}, textContent: "", value: 0, attributes: {},
                setAttribute(key, value) { this.attributes[key] = value; },
            });
        }
        return elements.get(id);
    },
};
const window = {};
let time = 10000;
const performance = { now: () => time };
const js = readFileSync(new URL("../cmd/entropy-server/web/status-kpis.js", import.meta.url), "utf8");
vm.runInNewContext(js, { document, window, performance });
const { update, unavailable } = window.EntropyStatusKPIs;
const value = id => document.getElementById(id).textContent;
const state = id => document.getElementById(id).dataset.state;
const first = [
    "rng_bytes_generated_total 10485760",
    "wss_payloads_total 100",
    "http_requests_total 200",
    "ws_rejected_connections_total 1",
    "ws_rejected_memory_total 2",
    "ws_rejected_size_total 3",
    "http_generation_rejected_requests_total 4",
    "http_generation_rejected_memory_total 5",
    "http_generation_rejected_size_total 6",
    "http_generation_memory_reserved_bytes 268435456",
    "http_generation_memory_budget_bytes 536870912",
    "ws_memory_reserved_bytes 805306368",
    "ws_memory_budget_bytes 1073741824",
    "ws_connections_active 126",
    "ws_connections_limit 128",
    "http_generation_requests_active 25",
    "http_generation_requests_limit 100",
    "drbg_instance_count 3",
    "rng_reseeds_total 77",
].join("\n");
update(first);
assert.equal(value("kpi-httpMemory-value"), "50.0%");
assert.equal(value("kpi-wsMemory-value"), "75.0%");
assert.equal(state("kpi-wsMemory"), "warning");
assert.equal(state("kpi-wsSlots"), "critical");
assert.equal(value("kpi-wsSlots-value"), "98.4%");
assert.equal(value("kpi-generation-rate"), "SAMPLING…");
assert.equal(value("kpi-ws-rejections"), "6");
assert.equal(value("kpi-http-rejections"), "15");
assert.equal(value("kpi-drbg-instances"), "3");
assert.equal(value("kpi-reseed-successes"), "77");
time += 5000;
let second = first.replace("rng_bytes_generated_total 10485760", "rng_bytes_generated_total 15728640")
    .replace("wss_payloads_total 100", "wss_payloads_total 150")
    .replace("http_requests_total 200", "http_requests_total 220")
    .replace("ws_rejected_memory_total 2", "ws_rejected_memory_total 3")
    .replace("http_generation_rejected_memory_total 5", "http_generation_rejected_memory_total 7");
update(second);
assert.equal(value("kpi-generation-rate"), "1.00 MiB/s");
assert.equal(value("kpi-ws-payload-rate"), "10.0 frames/s");
assert.equal(value("kpi-http-request-rate"), "4.0 req/s");
assert.equal(value("kpi-ws-reject-rate"), "12.0 rejects/min");
assert.equal(value("kpi-http-reject-rate"), "24.0 rejects/min");
unavailable();
assert.equal(value("kpi-generation-rate"), "—");
assert.equal(value("kpi-httpMemory-value"), "N/A");
assert.equal(state("kpi-httpMemory"), "unknown");
time += 5000;
update(second);
assert.equal(value("kpi-generation-rate"), "SAMPLING…");
time += 5000;
update(second.replace("rng_bytes_generated_total 15728640", "rng_bytes_generated_total 3"));
assert.equal(value("kpi-generation-rate"), "SAMPLING…");
time += 5000;
update(second.replace("http_generation_memory_budget_bytes 536870912", "http_generation_memory_budget_bytes 0"));
assert.equal(value("kpi-httpMemory-value"), "N/A");
// Freshness is measured against the readiness age limit, not the disabled buffer.
updateReady({reseed_age_ms: 2000, max_reseed_age_ms: 10000, ready: true});
assert.equal(value("kpi-reseed-headroom"), "80.0%");
assert.equal(state("kpi-reseed-headroom-card"), "normal");
updateReady({reseed_age_ms: 8500, max_reseed_age_ms: 10000, ready: true});
assert.equal(state("kpi-reseed-headroom-card"), "warning");
updateReady({reseed_age_ms: 9500, max_reseed_age_ms: 10000, ready: false});
assert.equal(state("kpi-reseed-headroom-card"), "critical");
updateReady({reseed_age_ms: 0, max_reseed_age_ms: 0});
assert.equal(value("kpi-reseed-headroom"), "N/A");

// At 250 ms, use a rolling baseline rather than only the latest tick.
unavailable();
time += 250;
const reseedBase = first + "\n" + "rng_reseed_failures_total 2";
update(reseedBase);
assert.equal(value("kpi-reseed-rate"), "SAMPLING…");
time += 250;
update(reseedBase.replace("rng_reseeds_total 77", "rng_reseeds_total 78")
    .replace("rng_reseed_failures_total 2", "rng_reseed_failures_total 3"));
assert.equal(value("kpi-reseed-rate"), "240.0 reseeds/min");
assert.equal(value("kpi-reseed-failure-rate"), "240.0 failures/min");
time += 250;
update(reseedBase.replace("rng_reseeds_total 77", "rng_reseeds_total 78")
    .replace("rng_reseed_failures_total 2", "rng_reseed_failures_total 3"));
assert.equal(value("kpi-reseed-rate"), "120.0 reseeds/min");
assert.equal(value("kpi-reseed-failure-rate"), "120.0 failures/min");
// Counter rollback forces rates to restart.
time += 250;
update(reseedBase.replace("rng_reseeds_total 77", "rng_reseeds_total 1"));
assert.equal(value("kpi-reseed-rate"), "SAMPLING…");

console.log("Status KPI calculations: passed");
