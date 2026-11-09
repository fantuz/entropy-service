import assert from "node:assert/strict";
import {readFileSync} from "node:fs";
import vm from "node:vm";

const root = new URL("../cmd/entropy-server/web/", import.meta.url);
const context = vm.createContext({Uint8Array, Uint32Array, Float64Array, ArrayBuffer, performance});
vm.runInContext(readFileSync(new URL("analysis-math.js", root), "utf8"), context);
const {summarize, spectrum, envelope, PowerAverage, WaterfallHistory} = context.EntropyAnalysis;
const close = (actual, expected, tolerance = 1e-10) => assert.ok(Math.abs(actual - expected) <= tolerance, `${actual} ≠ ${expected}`);
const ramp = Uint8Array.from({length: 65536}, (_, i) => i % 256);
const summary = summarize(ramp);
close(summary.entropy, 8); close(summary.mean, 127.5);
assert.equal(summary.histogram.length, 256);
assert.ok(summary.histogram.every(value => value === 256));
assert.ok(summary.bitOnes.every(value => value === ramp.length / 2));
assert.equal(summary.ones, summary.zeros);
close(summary.stddev, Math.sqrt((256 ** 2 - 1) / 12));
for (const value of [0, 255]) {
    const stats = summarize(new Uint8Array(512).fill(value));
    close(stats.entropy, 0); close(stats.stddev, 0); close(stats.mean, value);
    assert.ok(stats.bitOnes.every(count => count === (value ? 512 : 0)));
}
assert.throws(() => summarize(new Uint8Array()));
assert.throws(() => summarize(new Uint8Array(1048577)));
assert.equal(spectrum(new Uint8Array(31)), null);
assert.equal(spectrum(new Uint8Array(2047), {size: 2048}), null);
assert.throws(() => spectrum(ramp, {size: 3000}));
assert.throws(() => spectrum(ramp, {window: "imaginary"}));

// Independent O(N²) DFT oracle, including both endpoints and Hann normalization.
const fixture = Uint8Array.from({length: 32}, (_, i) => (i * i * 19 + 7 * i + 11) & 255);
for (const window of ["hann", "rectangular"]) {
    for (const removeMean of [false, true]) {
        const fft = spectrum(fixture, {window, removeMean});
        assert.equal(fft.power.length, 17); close(fft.binWidth, 1 / 32);
        const mean = fixture.reduce((a, b) => a + b, 0) / fixture.length;
        const weights = Array.from(fixture, (_, i) => window === "hann" ? .5 * (1 - Math.cos(2 * Math.PI * i / 31)) : 1);
        const energy = weights.reduce((sum, w) => sum + w * w, 0);
        const signal = Array.from(fixture, (value, i) => (value - (removeMean ? mean : 127.5)) / 127.5 * weights[i]);
        for (let k = 0; k <= 16; k++) {
            let real = 0, imag = 0;
            signal.forEach((value, n) => { real += value * Math.cos(-2 * Math.PI * k * n / 32); imag += value * Math.sin(-2 * Math.PI * k * n / 32); });
            const expected = (real * real + imag * imag) / energy * (k === 0 || k === 16 ? 1 : 2);
            close(fft.power[k], expected);
        }
        // Parseval: PSD integral = window-weighted mean-square, including mean removal.
        close(fft.integratedPower, signal.reduce((a, b) => a + b * b, 0) / energy);
    }
}
const constant = new Uint8Array(2048).fill(255);
let fft = spectrum(constant, {size: 2048, window: "rectangular", removeMean: false});
close(fft.power[0], 2048); close(fft.integratedPower, 1);
fft = spectrum(constant, {size: 2048, removeMean: true});
assert.ok(fft.power.every(value => value === 0));
const alternating = Uint8Array.from(constant, (_, i) => i % 2 ? 255 : 0);
fft = spectrum(alternating, {size: 2048, window: "rectangular"});
close(fft.power[1024], 2048); close(fft.integratedPower, 1); close(fft.power[0], 0);
for (const size of [512, 2048, 8192, 32768, 65536]) {
    const sine = Uint8Array.from({length: size}, (_, i) => Math.round(127.5 + 100 * Math.sin(2 * Math.PI * i / 32)));
    fft = spectrum(sine, {size});
    const peak = fft.power.indexOf(Math.max(...fft.power));
    assert.equal(peak, size / 32);
    assert.equal(fft.size, size);
}
assert.equal(spectrum(ramp).size, 8192);
const pulse = new Uint8Array(10000).fill(127); pulse[7812] = 255; pulse[851] = 0;
const compressed = envelope(pulse, 211);
assert.ok(compressed.some(column => column.min === 0));
assert.ok(compressed.some(column => column.max === 255));
assert.equal(compressed[0].start, 0); assert.equal(compressed.at(-1).end, pulse.length);
const average = new PowerAverage();
let averaged = average.add({power: Float64Array.of(1, 100), size: 2}, 2, "A");
averaged = average.add({power: Float64Array.of(9, 300), size: 2}, 2, "A");
close(averaged.power[0], 5); close(averaged.power[1], 200); assert.equal(averaged.averages, 2);
averaged = average.add({power: Float64Array.of(3, 500), size: 2}, 2, "A");
close(averaged.power[0], 6); close(averaged.power[1], 400);
averaged = average.add({power: Float64Array.of(42), size: 1}, 2, "B");
assert.equal(averaged.averages, 1); close(averaged.power[0], 42);
const history = new WaterfallHistory();
for (let i = 0; i < 1000; i++) history.add(ramp, i * 501, i, i * 4096);
assert.equal(history.rows.length, 240); assert.equal(history.rows[0].frame, 999); assert.equal(history.rows[0].offset, 999 * 4096);
assert.ok(history.rows.every(row => row.bytes.length === 1024));
const copied = history.rows[0].bytes[0]; ramp[0] = 66;
assert.equal(history.rows[0].bytes[0], copied);
history.clear(); assert.equal(history.rows.length, 0);

// The actual worker must use that same implementation, transfer bounded results,
// reject malformed messages and not silently expand short inputs.
let posted;
context.importScripts = path => vm.runInContext(readFileSync(new URL(path.slice(1), root), "utf8"), context);
context.self = {postMessage(data, transfer) {posted = {data, transfer};}};
vm.runInContext(readFileSync(new URL("worker.js", root), "utf8"), context);
context.self.onmessage({data: {type: "analyze", buffer: alternating.buffer, totalBytes: 4096, offset: 123, fftOptions: {size: 2048, window: "rectangular"}}});
assert.equal(posted.data.type, "analysis"); close(posted.data.fft.power[1024], 2048);
assert.equal(posted.transfer.length, 4); assert.equal(posted.data.offset, 123);
context.self.onmessage({data: {type: "analyze", buffer: new ArrayBuffer(1048577), offset: 0, totalBytes: 1048577}});
assert.equal(posted.data.type, "error");
context.self.onmessage({data: {type: "analyze", buffer: new ArrayBuffer(512), offset: -1, totalBytes: 512}});
assert.equal(posted.data.type, "error");
console.log("Shared FFT, Parseval, DC/Nyquist, histograms, bit rates, envelopes, power averaging, bounded history and worker: passed");
