import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import vm from "node:vm";

// Integration-style scheduler test using fake DOM, fetch, and timer services.
// No browser packages, network services, or entropy hardware required.
const nodes = new Map();
const listeners = new Map();
const events = new Map();
const document = {
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
const window = { addEventListener: (type, cb) => events.set(type, cb),
    EntropyStatusKPIs: { update() {}, unavailable() {}, updateReady() {} } };
const active = new Map();
let timerSequence = 0;
function setTimeoutFake(fn, delay) { const id = ++timerSequence; active.set(id, {fn, delay}); return id; }
function clearTimeoutFake(id) { active.delete(id); }
let fetchCount = 0;
let resolveMetrics;
let blockMetrics = true;
const fetchFake = async (path) => {
    fetchCount++;
    if (path === "/metrics") {
        if (blockMetrics) await new Promise(resolve => { resolveMetrics = resolve; });
        return {ok:true, text: async () => "rng_bytes_generated_total 100\nws_connections_active 0"};
    }
    if (path === "/live") return {ok:true, json: async () => ({status:"ok"})};
    if (path === "/ready") return {ok:true, json: async () => ({
        ready:true, rng_source:"test", rng_drbg:"AES-CTR",
        reseed_age_ms:100, max_reseed_age_ms:10000, reseed_failures_total:0
    })};
    throw new Error("unexpected endpoint " + path);
};
const source = readFileSync(new URL("../cmd/entropy-server/web/status-dashboard.js", import.meta.url), "utf8");
vm.runInNewContext(source, {
    document, window, fetch: fetchFake, AbortSignal: {timeout: () => ({})},
    setTimeout: setTimeoutFake, clearTimeout: clearTimeoutFake,
});
async function flush() {
    // Let Promise.allSettled, response-body reads and finally blocks complete.
    for (let i = 0; i < 3; i++) {
        await new Promise(resolve => setImmediate(resolve));
    }
}
await flush();
assert.equal(fetchCount, 3);
assert.equal(active.size, 0, "must not schedule a new cycle while requests remain pending");

const interval = document.getElementById("status-interval");
interval.value = "0.05";
listeners.get("status-interval:change")();
assert.equal(interval.value, "0.05");
assert.equal(active.size, 0, "changing interval during a fetch must not overlap");
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
console.log("Status dashboard scheduler: passed");
