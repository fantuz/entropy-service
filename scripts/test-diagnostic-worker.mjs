import assert from "node:assert/strict";
import {readFileSync} from "node:fs";
import vm from "node:vm";

// Exercise the actual worker, including browser-like transferable ownership.
// A mock postMessage that only retains references misses detached/duplicate
// buffers and can let a broken optional panel corrupt the basic byte views.
const root = new URL("../cmd/entropy-server/web/", import.meta.url);
const elements = new Map();
function element(id) {
    if (!elements.has(id)) elements.set(id, {open: false, value: "", valid: true, disabled: false,
        addEventListener() {}, checkValidity() {return this.valid;}});
    return elements.get(id);
}
const context = vm.createContext({Uint8Array, Uint32Array, Float64Array, DataView, ArrayBuffer, performance,
    document: {getElementById: element}, window: {}});
context.importScripts = (...paths) => {
    for (const path of paths) vm.runInContext(readFileSync(new URL(path.slice(1), root), "utf8"), context);
};
function buffersOf(value, buffers = new Set(), seen = new Set()) {
    if (!value || typeof value !== "object" || seen.has(value)) return buffers;
    seen.add(value);
    if (value instanceof ArrayBuffer) buffers.add(value);
    else if (ArrayBuffer.isView(value)) buffers.add(value.buffer);
    else for (const entry of Object.values(value)) buffersOf(entry, buffers, seen);
    return buffers;
}
let posted = [];
context.self = {postMessage(data, transfer = []) {
    const referenced = buffersOf(data);
    const lengths = transfer.map(buffer => buffer.byteLength);
    const clone = structuredClone(data, {transfer});
    posted.push({data: clone, transfer, referenced, lengths});
}};
vm.runInContext(readFileSync(new URL("worker.js", root), "utf8"), context);
vm.runInContext(readFileSync(new URL("diagnostic-workspace.js", root), "utf8"), context);
const diagnostics = context.window.EntropyDiagnostics({kind: "stream", requestAnalysis() {}, redraw() {}});
element("correlation-lags").value = "64"; element("transition-bins").value = "16"; element("local-block-size").value = "256";
element("pcm-mode").value = "auto"; element("pcm-rate").value = "8192"; element("pcm-notch").value = "off";

function options(metadata) {return diagnostics.options(metadata);}
function analyze(bytes, extra = {}) {
    posted = [];
    const copy = Uint8Array.from(bytes), offset = extra.offset ?? 0;
    const input = {type: "analyze", buffer: copy.buffer, totalBytes: offset + copy.length, offset,
        frame: 1, captureTime: 100, representation: "raw", fftOptions: {size: "auto", window: "hann", removeMean: true}, ...extra};
    context.self.onmessage({data: input});
    assert.equal(posted.length, 1);
    const record = posted[0], result = record.data;
    assert.equal(result.type, "analysis", result.message);
    assert.equal(new Set(record.transfer).size, record.transfer.length, "Duplicate transfer buffer");
    assert.deepEqual(new Set(record.transfer), record.referenced, "Every result buffer must transfer exactly once");
    assert.ok(record.transfer.every(buffer => buffer.byteLength === 0), "Worker retained transferred payloads");
    assert.equal(copy.byteLength, 0, "The worker result must transfer ownership of the source buffer back");
    assert.deepEqual(new Uint8Array(result.buffer), bytes, "Analysis modified source bytes");
    return {...record, result};
}
const near = (actual, expected, tolerance = 1e-12) => assert.ok(Math.abs(actual - expected) <= tolerance, `${actual} != ${expected}`);
const alternating = Uint8Array.from({length: 4096}, (_, i) => i % 2 ? 255 : 0);

// Closing both optional panels must bypass their analysis functions entirely.
const structureAPI = context.EntropyStructure, audioAPI = context.EntropyAudio;
context.EntropyStructure = {...structureAPI, analyze() {throw new Error("Closed structure panel ran work");}};
context.EntropyAudio = {...audioAPI, analyze() {throw new Error("Closed audio panel ran work");}};
let result = analyze(alternating, options()).result;
assert.equal(result.structure, null); assert.equal(result.audio, null); assert.equal(result.audioReason, "");
assert.equal(result.entropy, 1); assert.equal(result.histogram[0], 2048); assert.equal(result.histogram[255], 2048);
assert.equal(posted[0].transfer.length, 4, "Disabled diagnostics changed the existing worker transfer contract");
context.EntropyStructure = structureAPI; context.EntropyAudio = audioAPI;

element("structure-diagnostics").open = true;
result = analyze(alternating, options()).result;
assert.equal(result.structure.correlation.maxLag, 64);
near(result.structure.correlation.values[0], -4095 / 4096); near(result.structure.correlation.values[1], 4094 / 4096);
assert.equal(result.structure.transitions.pairs, 4095);
assert.equal(result.structure.transitions.counts[15], 2048); assert.equal(result.structure.transitions.counts[15 * 16], 2047);
assert.equal(result.structure.blocks.count, 16);
assert.ok(result.structure.blocks.entropy.every(value => value === 1));
assert.ok(result.structure.blocks.ones.every(value => value === .5));
assert.equal(posted[0].transfer.length, 8);
result = analyze(Uint8Array.of(0), options()).result;
assert.equal(result.structure.correlation.defined, false); assert.equal(result.structure.correlation.maxLag, 0);
assert.equal(result.structure.transitions.pairs, 0); assert.equal(result.structure.blocks.count, 0);
result = analyze(new Uint8Array(64).fill(255), options()).result;
assert.equal(result.structure.correlation.defined, false);
assert.ok(result.structure.correlation.values.every(Number.isNaN), "Constant input must not resemble zero correlation");

const pcmRate = 8192, metadata = {format: "pcm16le", sampleRate: pcmRate, channels: 1};
const pcm = new Uint8Array(32768 * 2), view = new DataView(pcm.buffer);
for (let i = 0; i < pcm.length / 2; i++) view.setInt16(i * 2,
    Math.round(32768 * (.4 * Math.sin(2 * Math.PI * 50 * i / pcmRate) + .2 * Math.sin(2 * Math.PI * 1024 * i / pcmRate))), true);
element("structure-diagnostics").open = false; element("audio-diagnostics").open = true;
let request = options({representation: "raw", audioMetadata: metadata});
assert.equal(request.audioRequest.declared, false); assert.equal(request.audioRequest.metadata, metadata);
assert.equal(request.audioRequest.reason, ""); assert.equal(request.audioRequest.notch, "off");
const unfiltered = analyze(pcm, request).result;
assert.equal(unfiltered.audio.sampleRate, pcmRate); assert.equal(unfiltered.audio.filtered, null);
assert.equal(unfiltered.audio.sampleCount, 32768); assert.equal(unfiltered.audio.raw.spectrum.size, 8192);
assert.equal(posted[0].transfer.length, 6);

element("pcm-mode").value = "manual";
request = options({representation: "raw"});
assert.equal(request.audioRequest.declared, true); assert.equal(request.audioRequest.metadata.format, "pcm16le");
assert.equal(request.audioRequest.metadata.sampleRate, pcmRate); assert.equal(request.audioRequest.metadata.channels, 1);
result = analyze(pcm, request).result;
assert.equal(result.audioRequest.declared, true); assert.deepEqual(result.audio.raw.samples, unfiltered.audio.raw.samples);
element("pcm-notch").value = "50";
const filtered = analyze(pcm, options({representation: "raw"})).result;
assert.equal(filtered.audio.filtered.notchHz, 50); assert.equal(posted[0].transfer.length, 8);
assert.deepEqual(filtered.buffer, unfiltered.buffer); assert.deepEqual(filtered.histogram, unfiltered.histogram);
assert.deepEqual(filtered.bitOnes, unfiltered.bitOnes); assert.equal(filtered.entropy, unfiltered.entropy);
assert.deepEqual(filtered.fft.power, unfiltered.fft.power);
assert.deepEqual(filtered.audio.raw.samples, unfiltered.audio.raw.samples);
assert.deepEqual(filtered.audio.raw.spectrum.power, unfiltered.audio.raw.spectrum.power);
assert.notDeepEqual(filtered.audio.filtered.samples, filtered.audio.raw.samples);
assert.ok(filtered.audio.filtered.spectrum.power[50] < filtered.audio.raw.spectrum.power[50] * .001);

// Invalid semantic interpretation is local to the audio panel. The ordinary
// byte statistics/FFT must survive odd windows, missing or malformed metadata.
const invalidCases = [
    [pcm.subarray(0, 64), {audioRequest: {metadata, notch: "off"}, offset: 1}, /even byte offset/],
    [pcm.subarray(0, 63), {audioRequest: {metadata, notch: "off"}}, /even byte offset.*even window/],
    [pcm.subarray(0, 64), {audioRequest: {metadata: {...metadata, sampleRate: 0}, notch: "off"}}, /sample rate/],
    [pcm.subarray(0, 64), {audioRequest: {metadata: {...metadata, channels: 2}, notch: "off"}}, /mono metadata/],
    [pcm.subarray(0, 64), {audioRequest: {metadata: null, notch: "off"}}, /explicit PCM16LE/],
    [pcm.subarray(0, 64), {audioRequest: {metadata, notch: "unsafe"}}, /Off, 50 Hz or 60 Hz/]
];
for (const [bytes, extra, reason] of invalidCases) {
    const output = analyze(bytes, extra).result;
    assert.equal(output.audio, null); assert.match(output.audioReason, reason);
    assert.equal(output.analyzedBytes, bytes.length); assert.equal(output.histogram.reduce((sum, count) => sum + count, 0), bytes.length);
    assert.equal(output.structure, null); assert.ok(Number.isFinite(output.entropy));
}
element("pcm-mode").value = "auto";
for (const source of [undefined, {representation: "raw"}, {representation: "xor", audioMetadata: metadata}]) {
    request = options(source);
    assert.ok(request.audioRequest.reason);
    result = analyze(pcm.subarray(0, 64), request).result;
    assert.equal(result.audio, null); assert.equal(result.audioReason, request.audioRequest.reason);
}
element("pcm-mode").value = "manual"; element("pcm-rate").valid = false;
request = options({representation: "raw"}); assert.match(request.audioRequest.reason, /Declare a sample rate/);
result = analyze(pcm.subarray(0, 64), request).result;
assert.equal(result.audio, null); assert.match(result.audioReason, /Declare a sample rate/);

// One maximum selection checks the optional result bounds without an endurance
// loop: prefix work and returned arrays remain much smaller than the selection.
element("structure-diagnostics").open = true; element("pcm-rate").valid = true;
result = analyze(new Uint8Array(1024 * 1024), options({representation: "raw"})).result;
assert.equal(result.analyzedBytes, 1024 * 1024); assert.equal(result.structure.analyzedBytes, 65536);
assert.equal(result.structure.correlation.count, 8192); assert.equal(result.structure.correlation.values.length, 64);
assert.equal(result.structure.transitions.counts.length, 256); assert.equal(result.structure.blocks.count, 256);
assert.equal(result.audio.sampleCount, 32768); assert.equal(result.audio.raw.samples.length, 32768);
assert.equal(result.audio.raw.spectrum.power.length, 4097); assert.equal(result.audio.truncated, true);
console.log("Actual diagnostic worker: default bypass, structure/PCM request integration, immutable raw/notch comparison, soft audio errors, complete unique transfers and bounded results passed");
