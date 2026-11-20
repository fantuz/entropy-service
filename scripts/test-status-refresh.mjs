import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import vm from "node:vm";

// Integration-style scheduler test using fake DOM, fetch, and timer services.
// No browser packages, network services, or entropy hardware required.
const nodes = new Map();
const listeners = new Map();
const events = new Map();
const document = {
    hidden: false,
    addEventListener: (type, cb) => events.set(type, cb),
    getElementById(id) {
        if (!nodes.has(id)) {
            nodes.set(id, {
                id, value: id === "status-interval" ? "5" : "",
                textContent: "", dataset: {}, disabled: false,
                addEventListener(type, cb) { listeners.set(id + ":" + type, cb); },
            });
        }
        return nodes.get(id);
    },
};
const renderedMetrics = [];
const window = { addEventListener: (type, cb) => events.set(type, cb),
    EntropyStatusKPIs: { update: body => renderedMetrics.push(body), unavailable() {}, updateReady() {}, resetHistory() {} } };
const active = new Map();
let timerSequence = 0;
function setTimeoutFake(fn, delay) { const id = ++timerSequence; active.set(id, {fn, delay}); return id; }
function clearTimeoutFake(id) { active.delete(id); }
let fetchCount = 0;
let resolveMetrics;
let latestSignal;
let metricsBody = "rng_bytes_generated_total 100\nws_connections_active 0";
let blockMetrics = true;
let ready = true;
const fetchFake = async (path, {signal}) => {
    fetchCount++;
    latestSignal = signal;
    if (path === "/metrics") {
        const captured = metricsBody;
        return {ok:true, text: async () => {
            // Simulate a delayed body that settles even after cancellation.
            if (blockMetrics) await new Promise(resolve => { resolveMetrics = resolve; });
            return captured;
        }};
    }
    if (path === "/live") return {ok:true, json: async () => ({status:"ok"})};
    if (path === "/ready") return {ok:ready, status:ready ? 200 : 503, json: async () => ({
        ready, rng_source:"test", rng_drbg:"AES-CTR",
        reseed_age_ms:100, max_reseed_age_ms:10000, reseed_failures_total:0
    })};
    throw new Error("unexpected endpoint " + path);
};
const source = readFileSync(new URL("../cmd/entropy-server/web/status-dashboard.js", import.meta.url), "utf8");
vm.runInNewContext(source, {
    document, window, fetch: fetchFake, AbortController,
    setTimeout: setTimeoutFake, clearTimeout: clearTimeoutFake,
});
async function flush() {
    // Let the response-body reads and the polling cycle complete.
    for (let i = 0; i < 3; i++) {
        await new Promise(resolve => setImmediate(resolve));
    }
}
await flush();
assert.equal(fetchCount, 3);
assert.deepEqual([...active.values()].map(t => t.delay), [4000], "only the request deadline may run");

const interval = document.getElementById("status-interval");
interval.value = "0.05";
listeners.get("status-interval:change")();
assert.equal(interval.value, "0.05");
assert.deepEqual([...active.values()].map(t => t.delay), [4000], "changing the interval must not overlap");
resolveMetrics();
await flush();
assert.equal(active.size, 1);
assert.equal([...active.values()][0].delay, 50);
assert.equal(document.getElementById("status-refresh").disabled, false);

const [id, pending] = [...active.entries()][0];
active.delete(id);
blockMetrics = false; // All subsequent scrapes complete without the initial artificial hold.
pending.fn();
await flush();
assert.equal(fetchCount, 6, "second round must start only after the scheduled delay");
assert.equal(active.size, 1);
assert.equal([...active.values()][0].delay, 50);

interval.value = "0.01";
listeners.get("status-interval:change")();
assert.equal(interval.value, "0.05", "invalid low interval clamps at 50ms");
assert.equal(active.size, 1);
assert.equal([...active.values()][0].delay, 50);

interval.value = "90";
listeners.get("status-interval:change")();
assert.equal(interval.value, "60");
assert.equal([...active.values()][0].delay, 60000);

interval.value = "1.75";
listeners.get("status-interval:change")();
assert.equal(interval.value, "1.75");
assert.equal([...active.values()][0].delay, 1750);

events.get("pagehide")();
assert.equal(active.size, 0, "pagehide must cancel pending refresh");
events.get("pageshow")({persisted: true});
await flush();
assert.equal(fetchCount, 9, "Back must refresh immediately");
events.get("pageshow")({persisted: true});
assert.equal(fetchCount, 9, "duplicate visibility/restoration events must not start another cycle");

blockMetrics = true;
metricsBody = "old observation";
listeners.get("status-refresh:click")();
await flush();
const stale = resolveMetrics;
const hiddenRequest = latestSignal;
document.hidden = true;
events.get("visibilitychange")();
assert.equal(hiddenRequest.aborted, true);
assert.equal(active.size, 0, "suspension clears the deadline even if a response body is slow to settle");
listeners.get("status-refresh:click")();
assert.equal(fetchCount, 12, "a hidden tab must not poll");
blockMetrics = false;
metricsBody = "fresh observation";
ready = false;
document.hidden = false;
events.get("visibilitychange")();
await flush();
stale();
await flush();
assert.equal(fetchCount, 15);
assert.equal(document.getElementById("ready-value").textContent, "DEGRADED", "HTTP 503 carries a valid observation");
assert.equal(document.getElementById("rng-source").textContent, "test");
assert.equal(renderedMetrics.at(-1), metricsBody, "a stale response must not overwrite the restored view");
assert.ok(!renderedMetrics.includes("old observation"));
assert.deepEqual([...active.values()].map(t => t.delay), [1750], "restoring leaves one polling timer");
console.log("Status dashboard scheduler: passed");
