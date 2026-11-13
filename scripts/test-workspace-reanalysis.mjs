import assert from "node:assert/strict";
import {readFileSync} from "node:fs";
import vm from "node:vm";

// Schedule the production workspace/worker explicitly so settings can change
// between capture, worker completion and painting. Only DOM/canvas are fakes.
const root = new URL("../cmd/entropy-server/web/", import.meta.url);
const source = name => readFileSync(new URL(name, root), "utf8");
function harness() {
    const nodes = new Map(), animationFrames = [], analyzed = [], rendered = [], errors = [];
    const settings = {"fft-size": "auto", "fft-window": "hann", "fft-average": "1", "trace-span": "1024",
        "trace-style": "line", "histogram-unit": "percent", "histogram-scale": "detail", "waterfall-mode": "gray",
        "waterfall-bytes": "auto", "waterfall-rows": "16", "correlation-lags": "64", "transition-bins": "16",
        "local-block-size": "1024", "pcm-mode": "auto", "pcm-notch": "off", "pcm-rate": "48000"};
    function element(id) {
        if (nodes.has(id)) return nodes.get(id);
        const listeners = new Map();
        const node = {value: settings[id] ?? "0", dataset: {}, style: {}, checked: true, textContent: "", open: false,
            addEventListener(event, handler) {
                if (!listeners.has(event)) listeners.set(event, []);
                listeners.get(event).push(handler);
            },
            emit(event) { for (const handler of listeners.get(event) || []) handler({target: node}); },
            append() {}, reportValidity() { return true; }, checkValidity() { return true; }};
        nodes.set(id, node);
        return node;
    }
    let worker, rawRows = [], spectralRows = [], finalFrame = null, api;
    const shared = {Uint8Array, Uint32Array, Float64Array, ArrayBuffer, DataView, performance};
    const context = vm.createContext({...shared,
        document: {getElementById: element, createElement: () => element(Symbol()),
            querySelector: () => element("panel"), hidden: false},
        requestAnimationFrame(callback) { animationFrames.push(callback); },
        ResizeObserver: class { observe() {} disconnect() {} },
        addEventListener() {}, removeEventListener() {},
        Worker: class {
            constructor(url) {
                assert.equal(url, "/worker.js");
                this.jobs = [];
                this.context = vm.createContext({...shared});
                this.context.self = {postMessage: (result, transfer) => {
                    this.onmessage({data: structuredClone(result, {transfer})});
                }};
                this.context.importScripts = (...paths) => {
                    for (const path of paths) vm.runInContext(source(path.slice(1)), this.context);
                };
                vm.runInContext(source("worker.js"), this.context);
                worker = this;
            }
            postMessage(data, transfer) { this.jobs.push(structuredClone(data, {transfer})); }
            complete() {
                assert.equal(this.jobs.length, 1, "only one actual worker job may run at a time");
                this.context.self.onmessage({data: this.jobs.shift()});
            }
            terminate() {}
        }});
    context.window = context;
    context.EntropyPlots = {empty() {}, balance() {}, histogram() { return 0; }, spectrum() {},
        trace(_canvas, bytes, options) {
            return {start: options.start, end: Math.min(bytes.length, options.start + options.span),
                left: 0, right: 300, x: value => value, envelope: false};
        },
        waterfall(_canvas, rows) {
            rawRows = Array.from(rows, row => ({...row, bytes: row.bytes.slice()}));
            return {shownRows: rows.length, rowBytes: 128};
        }};
    context.EntropyStructurePlots = {empty() {}, correlation() {}, transitions() {}, blocks() {},
        spectrogram(_canvas, rows) {
            spectralRows = Array.from(rows, row => ({...row, power: row.power.slice()}));
            return {rows: rows.length};
        }};
    context.EntropyAudioPlots = {empty() {}};
    context.EntropyShannonAlert = {update() {}};
    for (const name of ["analysis-math.js", "structure-math.js", "diagnostic-workspace.js", "analysis-workspace.js"]) {
        vm.runInContext(source(name), context);
    }
    api = context.EntropyWorkspace({kind: "stream", describe: () => "fixture",
        onError(message) { errors.push(message); },
        onAnalyzed(data) { analyzed.push(data.frame); },
        onRender(data) {
            rendered.push({frame: data.frame, reanalysis: data.reanalysis});
            // LAB's final one-shot/EOF callback freezes immediately after the
            // final real frame renders. A subsequent comparison must survive it.
            if (data.frame === finalFrame && !data.reanalysis) api.setFrozen(true);
        }});
    function paint() {
        let callbacks = 0;
        while (animationFrames.length) {
            assert.ok(++callbacks < 30, "animation callbacks did not settle");
            animationFrames.shift()();
        }
        assert.deepEqual(errors, [], "production worker raised an error");
    }
    paint();
    return {api, worker, analyzed, rendered, paint,
        get rawRows() { return rawRows; }, get spectralRows() { return spectralRows; },
        finishAt(frame) { finalFrame = frame; },
        submit(frame, sourceId = 1) {
            api.submit(Uint8Array.from({length: 4096}, (_, i) => (i + frame) % 256).buffer,
                {frame, sourceId, offset: 0, totalBytes: 4096, time: frame * 10,
                    captureTime: frame * 10, representation: "raw"});
        },
        openStructure() { const node = element("structure-diagnostics"); node.open = true; node.emit("toggle"); },
        change(id, value) { const node = element(id); node.value = String(value); node.emit("change"); }
    };
}

// An untouched/default workspace performs just its ordinary byte analysis.
const closed = harness();
closed.submit(1); closed.worker.complete(); closed.paint();
assert.equal(closed.api.sample.structure, null); assert.equal(closed.api.sample.audio, null);
assert.equal(closed.worker.jobs.length, 0); assert.deepEqual(closed.analyzed, [1]);

// Settings changed during the very first capture must not discard its only
// worker result or strand Sample once waiting for a final onRender callback.
const first = harness(); first.finishAt(1); first.submit(1);
first.openStructure(); first.paint();
first.worker.complete(); first.paint();
assert.equal(first.api.sample.frame, 1); assert.equal(first.api.frozen, true);
assert.deepEqual(first.analyzed, [1]);
assert.deepEqual(first.rendered, [{frame: 1, reanalysis: false}]);
assert.equal(first.worker.jobs.length, 1, "apply newly requested diagnostics after the capture drains");
first.worker.complete(); first.paint();
assert.equal(first.api.sample.structure.transitions.pairs, 4095);
assert.deepEqual(first.analyzed, [1], "frozen reanalysis must not increment capture counters");
assert.deepEqual(first.rawRows.map(row => row.frame), [1]);
assert.deepEqual(first.spectralRows.map(row => row.frame), [1]);

// A completed result waiting for its animation frame has the same ownership
// guarantee as a still-running worker result.
const pending = harness(); pending.finishAt(1); pending.submit(1);
pending.worker.complete(); pending.openStructure(); pending.paint();
assert.equal(pending.api.sample.frame, 1); assert.equal(pending.api.frozen, true);
pending.worker.complete(); pending.paint();
assert.equal(pending.api.sample.structure.transitions.pairs, 4095);
assert.deepEqual(pending.analyzed, [1]);

// Preserve the newest queued EOF capture even while an earlier worker job is
// active and an older rendered sample already exists.
const eof = harness(); eof.openStructure(); eof.paint();
eof.submit(1); eof.worker.complete(); eof.paint(); eof.finishAt(3);
eof.submit(2); eof.submit(3); eof.change("correlation-lags", 128); eof.paint();
eof.worker.complete(); eof.paint();
assert.equal(eof.api.sample.frame, 2); assert.equal(eof.worker.jobs.length, 1);
eof.worker.complete(); eof.paint();
assert.equal(eof.api.sample.frame, 3); assert.equal(eof.api.frozen, true);
assert.equal(eof.worker.jobs.length, 1);
eof.worker.complete(); eof.paint();
assert.equal(eof.api.sample.structure.correlation.maxLag, 128);
assert.deepEqual(eof.analyzed, [1, 2, 3]);
assert.deepEqual(eof.rendered.filter(row => !row.reanalysis).map(row => row.frame), [1, 2, 3]);
assert.deepEqual(eof.rawRows.map(row => row.frame), [3, 2, 1]);
assert.deepEqual(eof.spectralRows.map(row => row.frame), [3, 2, 1]);

// Pause invalidates an in-flight result. Pending settings still need to run on
// the last retained sample once that discarded job releases the worker.
const paused = harness(); paused.openStructure(); paused.paint();
paused.submit(1); paused.worker.complete(); paused.paint();
paused.submit(2); paused.api.setFrozen(true); paused.change("correlation-lags", 128); paused.paint();
paused.worker.complete(); paused.paint();
assert.equal(paused.worker.jobs.length, 1, "settings must not remain stranded behind a discarded worker result");
paused.worker.complete(); paused.paint();
assert.equal(paused.api.sample.frame, 1); assert.equal(paused.api.sample.structure.correlation.maxLag, 128);
assert.deepEqual(paused.analyzed, [1, 2]);
assert.deepEqual(paused.rawRows.map(row => row.frame), [1]);
assert.deepEqual(paused.spectralRows.map(row => row.frame), [1]);

// Clearing/replacing a source cancels a pending comparison request. Finishing
// an old worker job may count actual work, but must never resurrect old views.
const cleared = harness(); cleared.openStructure(); cleared.paint();
cleared.submit(1); cleared.worker.complete(); cleared.paint();
cleared.submit(2); cleared.change("correlation-lags", 128);
cleared.api.clear(); cleared.submit(3, 2); cleared.paint();
cleared.worker.complete(); cleared.paint();
cleared.worker.complete(); cleared.paint();
assert.equal(cleared.api.sample.frame, 3); assert.equal(cleared.api.sample.sourceId, 2);
assert.equal(cleared.worker.jobs.length, 0, "cleared comparison requests must stay cancelled");
assert.deepEqual(cleared.rendered.filter(row => !row.reanalysis).map(row => row.frame), [1, 3]);
assert.deepEqual(cleared.rawRows.map(row => row.frame), [3]);
assert.deepEqual(cleared.spectralRows.map(row => row.frame), [3]);

console.log("Production workspace/worker: first/final capture drain, queued capture ownership, paused reanalysis, clear/reset and exact counters/history: passed");
