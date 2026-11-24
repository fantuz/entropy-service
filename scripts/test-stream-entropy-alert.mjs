import assert from "node:assert/strict";
import {readFileSync} from "node:fs";
import vm from "node:vm";

const source = readFileSync(
    new URL("../cmd/entropy-server/web/stream-entropy-alert.js", import.meta.url), "utf8");
const key = "entropy-service:stream:shannon-alert:v2";
const legacy = "entropy-service:stream:shannon-threshold:v1";
const preference = (enabled, threshold) => JSON.stringify({enabled, threshold});
const create = (initial = {}, blockStorage = false) => {
    const saved = new Map(Object.entries(initial)), nodes = new Map(), events = new Map();
    const document = {
        getElementById(id) {
            if (!nodes.has(id)) nodes.set(id, {
                value: "", textContent: "", dataset: {}, attributes: {},
                checked: false, hidden: false, disabled: false, required: false,
                addEventListener(type, cb) { events.set(id + ":" + type, cb); },
                setAttribute(name, value) { this.attributes[name] = value; },
                setCustomValidity(value) { this.validityMessage = value; },
            });
            return nodes.get(id);
        },
    };
    const localStorage = {
        getItem(k) {
            if (blockStorage) throw Error("Storage unavailable");
            return saved.get(k) ?? null;
        },
        setItem(k, v) {
            if (blockStorage) throw Error("Storage unavailable");
            saved.set(k, v);
        },
    };
    const window = {addEventListener(type, cb) { events.set("window:" + type, cb); }};
    vm.runInNewContext(source, {document, window, localStorage, Number});
    const node = id => document.getElementById(id);
    return {alert: window.EntropyShannonAlert, node, saved, events,
        state: () => node("entropy-alert-card").dataset.state,
        edit(value) { node("entropy-threshold").value = value; events.get("entropy-threshold:change")(); },
        toggle(value) { node("entropy-alert-enabled").checked = value; events.get("entropy-alert-enabled:change")(); },
    };
};

// First visits describe even low-entropy samples without choosing a threshold.
let view = create();
assert.equal(view.node("entropy-threshold").value, "");
assert.equal(view.node("entropy-threshold").disabled, true);
assert.equal(view.node("entropy-threshold-settings").hidden, true);
assert.equal(view.node("entropy-alert-enabled").checked, false);
assert.equal(view.state(), "waiting");
assert.equal(view.node("entropy").textContent, "—");
assert.equal(view.saved.size, 0);
view.alert.update(0, 4096);
assert.equal(view.state(), "descriptive");
assert.match(view.node("entropy-alert-message").textContent, /ALERT DISABLED/);
assert.equal(view.node("entropy-sample-size").textContent, "Estimate from 4,096 bytes");
assert.equal(view.node("entropy").textContent, "0.000000");
view.alert.update(7.6, 1024);
assert.equal(view.node("entropy-sample-size").textContent, "Estimate from 1,024 bytes");

// Opting in still requires the user to choose a threshold. A valid edit
// immediately reevaluates the most recent sample, without requesting a new one.
view.toggle(true);
assert.equal(view.state(), "unconfigured");
assert.equal(view.node("entropy-threshold").value, "");
assert.equal(view.node("entropy-threshold").disabled, false);
assert.equal(view.node("entropy-threshold").required, true);
assert.equal(view.node("entropy-threshold-settings").hidden, false);
view.edit("7.70000");
assert.equal(view.state(), "alert");
assert.equal(view.node("entropy-threshold").value, "7.700000");
assert.deepEqual(JSON.parse(view.saved.get(key)), {enabled: true, threshold: "7.700000"});
view.alert.update(7.7, 64);
assert.equal(view.state(), "normal", "Equality is not an alert");
assert.match(view.node("entropy-alert-message").textContent, /WITHIN SELECTED/);
view.alert.update(7.6999999, 64);
assert.equal(view.state(), "alert", "Compare the full-precision estimate");
assert.equal(view.node("entropy").textContent, "7.700000");

// Invalid edits are explained accessibly and cannot classify a sample using
// an obsolete threshold or overwrite the last valid saved preference.
const lastSaved = view.saved.get(key);
for (const invalid of ["0.999999", "8.000001", "7.9999001", "NaN", "Infinity", "", "9", "2,5", "  ", "1e0"]) {
    view.edit(invalid);
    assert.equal(view.state(), "unconfigured", "must reject " + invalid);
    assert.equal(view.node("entropy-threshold").value, invalid);
    assert.equal(view.node("entropy-threshold").attributes["aria-invalid"], "true");
    assert.equal(view.node("entropy-threshold-error").hidden, false);
    assert.match(view.node("entropy-threshold").validityMessage, /1–8/);
    assert.equal(view.saved.get(key), lastSaved);
}
assert.equal(view.alert.setThreshold("7.999901"), true);
assert.equal(view.node("entropy-threshold").attributes["aria-invalid"], "false");
assert.equal(view.node("entropy-threshold-error").hidden, true);
assert.equal(view.node("entropy-threshold").validityMessage, "");
assert.equal(view.alert.setThreshold("1.00000"), true);
assert.equal(view.alert.setThreshold("8.000000"), true);
view.alert.update(7.999999, 4096);
assert.equal(view.state(), "alert");
view.alert.update(8, 4096);
assert.equal(view.state(), "normal");
view.alert.update(Number.NaN, 4096);
assert.equal(view.state(), "waiting");
assert.equal(view.node("entropy-sample-size").textContent, "Sample size: —");
for (const count of [undefined, -1, 0, 1.5, Infinity, Number.MAX_SAFE_INTEGER + 1]) {
    view.alert.update(7.5, count);
    assert.equal(view.node("entropy-sample-size").textContent, "Sample size: —");
}

// Disabling keeps the threshold for reuse and overrides any old v1 preference
// on other pages and reloads. Selecting a threshold alone never enables alerts.
view.toggle(false);
assert.equal(view.state(), "descriptive");
assert.equal(view.node("entropy-threshold").disabled, true);
assert.equal(view.node("entropy-threshold-settings").hidden, true);
assert.equal(view.alert.setThreshold("7.123456"), true);
assert.equal(view.state(), "descriptive");
view.saved.set(legacy, "7.9999");
let reload = create(Object.fromEntries(view.saved));
reload.alert.update(0, 4096);
assert.equal(reload.state(), "descriptive");
assert.equal(reload.node("entropy-threshold").value, "7.123456");
reload.toggle(true);
assert.equal(reload.state(), "alert");
reload = create(Object.fromEntries(reload.saved));
assert.equal(reload.node("entropy-alert-enabled").checked, true);
assert.equal(reload.node("entropy-threshold").value, "7.123456");

// A saved legacy choice was explicit: retain its enabled state and precision.
for (const value of ["7.23456", "7.234567"]) {
    view = create({[legacy]: value});
    assert.equal(view.node("entropy-alert-enabled").checked, true);
    assert.equal(view.node("entropy-threshold").value, Number(value).toFixed(6));
    view.alert.update(0, 1);
    assert.equal(view.state(), "alert");
}
for (const invalid of ["invalid", "7.9999001", "8.000001"]) {
    view = create({[legacy]: invalid});
    assert.equal(view.node("entropy-alert-enabled").checked, false);
    assert.equal(view.node("entropy-threshold").value, "");
}
for (const invalid of ["invalid", "null", "[]", '{}', preference("true", "7.5"),
    preference(true, "8.000001"), preference(true, 7.5)]) {
    view = create({[key]: invalid, [legacy]: "7.9999"});
    assert.equal(view.node("entropy-alert-enabled").checked, false, "Corrupt v2 data must not activate legacy alerts");
}
view = create({[key]: preference(true, null)});
assert.equal(view.node("entropy-alert-enabled").checked, true);
view.alert.update(0, 1);
assert.equal(view.state(), "unconfigured");

// Same-origin pages share edits/disable choices via storage events, including
// a returning BFCache page. A storage clear restores the descriptive default.
view = create();
view.alert.update(7.1, 64);
view.saved.set(key, preference(true, "7.200000"));
view.events.get("window:storage")({key});
assert.equal(view.state(), "alert");
assert.equal(view.node("entropy-alert-enabled").checked, true);
view.saved.set(key, preference(false, "7.200000"));
view.events.get("window:pageshow")();
assert.equal(view.state(), "descriptive");
assert.equal(view.node("entropy-alert-enabled").checked, false);
view.saved.clear();
view.events.get("window:storage")({key: null});
assert.equal(view.node("entropy-threshold").value, "");
assert.equal(view.state(), "descriptive");

// Storage restrictions preserve the in-page controls, including after pageshow.
view = create({}, true);
view.toggle(true);
assert.equal(view.alert.setThreshold("7.87654"), true);
view.alert.update(7.8, 256);
view.events.get("window:pageshow")();
assert.equal(view.state(), "alert");
assert.equal(view.node("entropy-threshold").value, "7.876540");
view.toggle(false);
assert.equal(view.state(), "descriptive");
console.log("Opt-in Shannon entropy watchpoint: passed");
