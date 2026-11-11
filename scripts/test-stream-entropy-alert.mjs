import assert from "node:assert/strict";
import {readFileSync} from "node:fs";
import vm from "node:vm";

const source = readFileSync(
    new URL("../cmd/entropy-server/web/stream-entropy-alert.js", import.meta.url),
    "utf8",
);
const key = "entropy-service:stream:shannon-threshold:v1";
const create = (initial = {}, blockStorage = false) => {
    const saved = new Map(Object.entries(initial));
    const nodes = new Map();
    const events = new Map();
    const document = {
        getElementById(id) {
            if (!nodes.has(id)) nodes.set(id, {
                value: "", textContent: "", dataset: {},
                addEventListener(type, cb) { events.set(id + ":" + type, cb); },
                setCustomValidity() {},
            });
            return nodes.get(id);
        },
    };
    const localStorage = {
        getItem(k) {
            if (blockStorage) throw Error("Storage unavailable");
            return saved.has(k) ? saved.get(k) : null;
        },
        setItem(k, v) {
            if (blockStorage) throw Error("Storage unavailable");
            saved.set(k, v);
        },
    };
    const window = {};
    vm.runInNewContext(source, {document, window, localStorage, Number});
    const node = id => document.getElementById(id);
    return {alert: window.EntropyShannonAlert, node, saved, events};
};

let view = create();
assert.equal(view.node("entropy-threshold").value, "7.999900");
assert.equal(view.node("entropy-alert-card").dataset.state, "waiting");
assert.equal(view.node("entropy").textContent, "—");

view.alert.update(7.999901);
assert.equal(view.node("entropy-alert-card").dataset.state, "normal");
assert.equal(view.node("entropy").textContent, "7.999901");
view.alert.update(7.999899);
assert.equal(view.node("entropy-alert-card").dataset.state, "alert");
assert.equal(view.node("entropy").textContent, "7.999899");
view.alert.update(7.9998999);
assert.equal(view.node("entropy-alert-card").dataset.state, "alert");
assert.equal(view.node("entropy").textContent, "7.999900");
view.alert.update(7.99990);
assert.equal(view.node("entropy-alert-card").dataset.state, "normal");
assert.match(view.node("entropy-alert-message").textContent, /WITHIN SELECTED/);
assert.equal(view.alert.setThreshold("7.50000"), true);
assert.equal(view.saved.get(key), "7.500000");
view.alert.update(7.4);
assert.equal(view.node("entropy-alert-card").dataset.state, "alert");
view.alert.update(7.5);
assert.equal(view.node("entropy-alert-card").dataset.state, "normal");

// Every accepted user value must be within [1, 8] and have <=6 decimals.
for (const invalid of ["0.999999", "8.000001", "7.9999001", "NaN", "Infinity", "", "9", "2,5", "  "]) {
    assert.equal(view.alert.setThreshold(invalid), false, "must reject " + invalid);
    assert.equal(view.node("entropy-threshold").value, "7.500000");
}
assert.equal(view.alert.setThreshold("7.999901"), true);
assert.equal(view.saved.get(key), "7.999901");
assert.equal(view.alert.setThreshold("1.00000"), true);
assert.equal(view.alert.setThreshold("8.00000"), true);
view.alert.update(7.999999);
assert.equal(view.node("entropy-alert-card").dataset.state, "alert");
view.alert.update(8);
assert.equal(view.node("entropy-alert-card").dataset.state, "normal");
view.alert.update(Number.NaN);
assert.equal(view.node("entropy-alert-card").dataset.state, "waiting");

// The selector must immediately recompute the latest sample's status.
view.alert.setThreshold("7.50000");
view.alert.update(7.6);
assert.equal(view.node("entropy-alert-card").dataset.state, "normal");
view.node("entropy-threshold").value = "7.70000";
view.events.get("entropy-threshold:change")();
assert.equal(view.node("entropy-alert-card").dataset.state, "alert");

// Browser reload retains preference; private/restricted storage remains usable.
view = create({[key]: "7.23456"});
assert.equal(view.node("entropy-threshold").value, "7.234560");
view = create({[key]: "7.234567"});
assert.equal(view.node("entropy-threshold").value, "7.234567");
view = create({[key]: "invalid"});
assert.equal(view.node("entropy-threshold").value, "7.999900");
view = create({}, true);
assert.equal(view.alert.setThreshold("7.87654"), true);
assert.equal(view.node("entropy-threshold").value, "7.876540");
console.log("Stream Shannon entropy threshold: passed");
