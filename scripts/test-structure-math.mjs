import assert from "node:assert/strict";
import {readFileSync} from "node:fs";
import vm from "node:vm";

const root = new URL("../cmd/entropy-server/web/", import.meta.url);
const context = vm.createContext({Uint8Array, Uint32Array, Float64Array});
vm.runInContext(readFileSync(new URL("analysis-math.js", root), "utf8"), context);
vm.runInContext(readFileSync(new URL("structure-math.js", root), "utf8"), context);
const {analyze, SpectralHistory} = context.EntropyStructure;
const close = (actual, expected, tolerance = 1e-12) => assert.ok(Math.abs(actual - expected) <= tolerance, `${actual} ≠ ${expected}`);

// An independent centered-vector oracle checks the complete selected lag range.
function correlationOracle(bytes, lag) {
    const mean = Array.from(bytes).reduce((sum, byte) => sum + byte, 0) / bytes.length;
    const centered = Array.from(bytes, value => value - mean);
    return centered.slice(lag).reduce((sum, value, i) => sum + value * centered[i], 0) /
        centered.reduce((sum, value) => sum + value * value, 0);
}
const fixture = Uint8Array.from({length: 256}, (_, i) => (i * i * 31 + i * 13 + 9) % 256);
const report = analyze(fixture), original = fixture.slice();
for (let lag = 1; lag <= report.correlation.maxLag; lag++) {
    close(report.correlation.values[lag - 1], correlationOracle(fixture, lag));
}
const alternating = Uint8Array.from({length: 8192}, (_, i) => i % 2 ? 255 : 0);
const alternatingReport = analyze(alternating, {lags: 128});
close(alternatingReport.correlation.values[0], -(8191 / 8192));
close(alternatingReport.correlation.values[1], 8190 / 8192);
assert.equal(alternatingReport.correlation.maxLag, 128);
assert.equal(alternatingReport.transitions.pairs, alternating.length - 1);
assert.equal(alternatingReport.transitions.counts[15], 4096);
assert.equal(alternatingReport.transitions.counts[15 * 16], 4095);
assert.equal(alternatingReport.transitions.counts.reduce((a, b) => a + b), alternating.length - 1);

// A byte-uniform counter is predictable; the transition matrix preserves this.
const ramp = Uint8Array.from({length: 65536}, (_, i) => i % 256);
const rampReport = analyze(ramp);
assert.ok(rampReport.blocks.entropy.every(value => value === 8));
assert.ok(rampReport.blocks.ones.every(value => value === .5));
assert.equal(rampReport.transitions.counts[0], 15 * 256);
assert.equal(rampReport.transitions.counts[15 * 16], 255);
assert.equal(rampReport.transitions.counts[1], 256);
const pairs = analyze(Uint8Array.of(0, 8, 16, 255), {bins: 32}).transitions;
assert.equal(pairs.binWidth, 8); assert.equal(pairs.pairs, 3);
assert.equal(pairs.counts[1], 1); assert.equal(pairs.counts[1 * 32 + 2], 1); assert.equal(pairs.counts[2 * 32 + 31], 1);

for (const value of [0, 255, 91]) {
    const constant = analyze(new Uint8Array(4096).fill(value));
    assert.equal(constant.correlation.defined, false);
    assert.ok(constant.correlation.values.every(Number.isNaN));
    assert.equal(constant.correlation.variance, 0);
    assert.ok(constant.blocks.entropy.every(value => value === 0));
    const ones = value.toString(2).replaceAll("0", "").length / 8;
    assert.ok(constant.blocks.ones.every(value => value === ones));
}
const short = analyze(Uint8Array.of(21));
assert.equal(short.correlation.maxLag, 0); assert.equal(short.correlation.defined, false);
assert.equal(short.transitions.pairs, 0); assert.equal(short.blocks.count, 0); assert.equal(short.blocks.omittedBytes, 1);

// Boundaries use complete equal-size blocks and explicitly report an omitted tail.
const blocks = new Uint8Array(256 * 3 + 7);
blocks.fill(255, 256, 512);
blocks.set(Uint8Array.from({length: 256}, (_, i) => i), 512);
const strip = analyze(blocks, {blockSize: 256}).blocks;
assert.equal(strip.count, 3); assert.equal(strip.coveredBytes, 768); assert.equal(strip.omittedBytes, 7);
assert.deepEqual(Array.from(strip.entropy), [0, 0, 8]);
assert.deepEqual(Array.from(strip.ones), [0, 1, .5]);
const maximum = new Uint8Array(1048576); maximum.fill(255, 65536);
const bounded = analyze(maximum);
assert.equal(bounded.inputBytes, 1048576); assert.equal(bounded.analyzedBytes, 65536);
assert.equal(bounded.correlation.count, 8192); assert.equal(bounded.transitions.pairs, 65535);
assert.equal(bounded.blocks.count, 64); assert.ok(bounded.blocks.entropy.every(value => value === 0));
assert.deepEqual(fixture, original);

// Fixed public pseudo-random fixture: a reproducible reference, not an entropy proof.
let state = 0x9e3779b9;
const seeded = Uint8Array.from({length: 8192}, () => {
    state ^= state << 13; state ^= state >>> 17; state ^= state << 5;
    return state >>> 24;
});
const seededReport = analyze(seeded);
assert.ok(seededReport.correlation.values.every(value => Math.abs(value) < .06));
assert.ok(seededReport.blocks.entropy.every(value => value > 7.7 && value <= 8));
for (const bytes of [null, [], new Uint8Array(), new Uint8Array(1048577)]) assert.throws(() => analyze(bytes));
for (const options of [{lags: 0}, {lags: 33}, {lags: NaN}, {bins: 256}, {bins: 16.1}, {blockSize: 1}, {blockSize: 257}]) {
    assert.throws(() => analyze(fixture, options));
}

// Spectral rows reduce peak power, retain exact source-bin metadata and own copies.
const source = Uint8Array.from({length: 2048}, (_, i) => Math.round(127.5 + 100 * Math.sin(2 * Math.PI * i / 32)));
const fft = context.EntropyAnalysis.spectrum(source, {size: 2048});
const history = new SpectralHistory();
assert.equal(history.add(fft, {key: "first", frame: 1, captureTime: 1234, offset: 4096}), true);
const row = history.rows[0];
assert.equal(row.power.length, 256); assert.equal(row.size, 2048); assert.equal(row.binWidth, 1 / 2048);
assert.equal(row.sourceBins, 1025); assert.equal(row.time, 1234); assert.equal(row.offset, 4096);
for (let column = 0; column < row.power.length; column++) {
    const start = Math.floor(column * fft.power.length / row.power.length);
    const end = Math.floor((column + 1) * fft.power.length / row.power.length);
    close(row.power[column], Math.max(...fft.power.slice(start, end)));
}
assert.equal(Math.max(...row.power), Math.max(...fft.power));
const owned = row.power.slice(); fft.power.fill(0);
assert.deepEqual(row.power, owned);
assert.equal(history.add(fft, {key: "first", frame: 1, captureTime: 99999}), false);
assert.equal(history.rows.length, 1); assert.equal(history.rows[0].time, 1234);
for (let frame = 2; frame <= 160; frame++) history.add(fft, {key: "first", frame, captureTime: frame * 100});
assert.equal(history.rows.length, 120); assert.equal(history.rows[0].frame, 160); assert.equal(history.rows.at(-1).frame, 41);
assert.equal(history.rows[0].time, 16000);
assert.equal(history.add(fft, {key: "first", frame: 1, captureTime: 99999}), false,
    "an evicted frame must not reappear as new history");
assert.equal(history.rows.length, 120); assert.equal(history.rows[0].frame, 160);
assert.equal(history.add(fft, {key: "second", frame: 1, captureTime: 50000}), true);
assert.equal(history.rows.length, 1); assert.equal(history.rows[0].frame, 1);
assert.equal(history.add(null, {key: "short-input", frame: 2, captureTime: 50001}), false);
assert.equal(history.rows.length, 0);
history.clear(); assert.equal(history.rows.length, 0); assert.equal(history.key, null);
const smallFFT = context.EntropyAnalysis.spectrum(new Uint8Array(32));
history.add(smallFFT, {key: "small", frame: 0, captureTime: 0});
assert.equal(history.rows[0].power.length, 17);
for (const bounds of [[0], [121], [1.5], [1, 257], [1, 0]]) assert.throws(() => new SpectralHistory(...bounds));
for (const metadata of [{key: 1, frame: 1, captureTime: 1}, {key: "x", frame: -1, captureTime: 1}, {key: "x", frame: 1, captureTime: NaN}, {key: "x", frame: 1, captureTime: 1, offset: -1}]) {
    assert.throws(() => history.add(smallFFT, metadata));
}
assert.throws(() => history.add({...smallFFT, binWidth: 1}, {key: "bad", frame: 1, captureTime: 1}));
assert.throws(() => history.add({...smallFFT, power: Float64Array.of(-1)}, {key: "bad", frame: 1, captureTime: 1}));
console.log("Bounded lag correlation, transition counts, equal-block diagnostics and peak-preserving spectral history: passed");
