import assert from "node:assert/strict";
import {readFileSync} from "node:fs";
import vm from "node:vm";

const context = vm.createContext({Uint8Array, Uint32Array, Float64Array, DataView});
vm.runInContext(readFileSync(new URL("../cmd/entropy-server/web/audio-analysis.js", import.meta.url), "utf8"), context);
const {analyze, MAX_SAMPLES, MAX_FFT} = context.EntropyAudio;
const metadata = {format: "pcm16le", sampleRate: 48000, channels: 1};
const near = (actual, expected, tolerance = 1e-11) => assert.ok(Math.abs(actual - expected) <= tolerance, `${actual} ≠ ${expected}`);
function pcm(values) {
    const bytes = new Uint8Array(values.length * 2), view = new DataView(bytes.buffer);
    values.forEach((value, index) => view.setInt16(index * 2, Math.max(-32768, Math.min(32767, Math.round(value * 32768))), true));
    return bytes;
}
function tone(length, sampleRate, frequencies) {
    return pcm(Array.from({length}, (_, i) => frequencies.reduce((sum, [frequency, amplitude]) => sum + amplitude * Math.sin(2 * Math.PI * frequency * i / sampleRate), 0)));
}
const silence = analyze(new Uint8Array(4096), metadata, {notch: "50"});
assert.equal(silence.sampleCount, 2048);
for (const signal of [silence.raw, silence.filtered]) {
    assert.equal(signal.rms, 0); assert.equal(signal.peak, 0); assert.equal(signal.dc, 0);
    assert.ok(signal.samples.every(value => value === 0)); assert.ok(signal.spectrum.power.every(value => value === 0));
}
const rails = new Uint8Array([0, 128, 0, 192, 0, 0, 0, 64, 255, 127]);
const saved = rails.slice(), decoded = analyze(rails, metadata);
assert.deepEqual(Array.from(decoded.raw.samples), [-1, -.5, 0, .5, 32767 / 32768]);
assert.equal(decoded.raw.clipped, 2); near(decoded.raw.clippedFraction, .4);
near(decoded.raw.dc, -1 / (32768 * 5));
assert.equal(decoded.raw.spectrum, null); assert.equal(decoded.filtered, null);
assert.deepEqual(rails, saved);

// Independent DFT and Parseval oracle: no shared FFT implementation or byte
// conversion shortcuts. The last fitting sample window is explicit.
const fixture = pcm(Array.from({length: 37}, (_, i) => .6 * Math.sin(i * .71) + .15));
const result = analyze(fixture, {...metadata, sampleRate: 8192});
const fft = result.raw.spectrum, n = 32, weights = Array.from({length: n}, (_, i) => .5 * (1 - Math.cos(2 * Math.PI * i / (n - 1))));
assert.equal(fft.size, n); assert.equal(fft.startSample, 5); near(fft.binWidthHz, 256);
const samples = Array.from(result.raw.samples).slice(5), windowed = samples.map((sample, i) => sample * weights[i]);
const energy = weights.reduce((sum, weight) => sum + weight * weight, 0);
for (let k = 0; k <= n / 2; k++) {
    let real = 0, imaginary = 0;
    for (let i = 0; i < n; i++) { real += windowed[i] * Math.cos(-2 * Math.PI * k * i / n); imaginary += windowed[i] * Math.sin(-2 * Math.PI * k * i / n); }
    const expected = (real * real + imaginary * imaginary) / (8192 * energy) * (k === 0 || k === n / 2 ? 1 : 2);
    near(fft.power[k], expected);
}
near(fft.integratedPower, windowed.reduce((sum, value) => sum + value * value, 0) / energy);

const sine = analyze(tone(8192, 8192, [[1024, .5]]), {...metadata, sampleRate: 8192});
assert.equal(sine.raw.spectrum.power.indexOf(Math.max(...sine.raw.spectrum.power)), 1024);
near(sine.raw.rms, Math.SQRT1_2 / 2, .00003); near(sine.raw.dc, 0, .00003);
const dc = analyze(pcm(new Array(2048).fill(.25)), metadata);
near(dc.raw.dc, .25); near(dc.raw.rms, .25); near(dc.raw.peak, .25); near(dc.raw.spectrum.integratedPower, .0625);

// A per-window zero-state filter has a transient. With enough samples, each
// mains notch strongly suppresses its tone while preserving a distant tone.
for (const frequency of [50, 60]) {
    const bytes = tone(MAX_SAMPLES, 8192, [[frequency, .4], [1024, .25]]), original = bytes.slice();
    const output = analyze(bytes, {...metadata, sampleRate: 8192}, {notch: String(frequency)});
    const rawPower = output.raw.spectrum.power, filteredPower = output.filtered.spectrum.power;
    assert.ok(filteredPower[frequency] < rawPower[frequency] * .001, `${frequency} Hz notch attenuation`);
    near(filteredPower[1024] / rawPower[1024], 1, .002);
    assert.ok(output.filtered.samples.every(Number.isFinite));
    assert.deepEqual(bytes, original, "display filtering changed raw PCM bytes");
    assert.equal(output.filtered.notchHz, frequency); assert.equal(output.filtered.initialState, "zero");
}

const large = analyze(new Uint8Array(1024 * 1024), metadata);
assert.equal(large.sampleCount, MAX_SAMPLES); assert.equal(large.raw.spectrum.size, MAX_FFT);
assert.equal(large.raw.spectrum.startSample, MAX_SAMPLES - MAX_FFT); assert.ok(large.truncated);
const unalignedStorage = new Uint8Array(67); unalignedStorage.set(fixture.subarray(0, 64), 1);
assert.equal(analyze(unalignedStorage.subarray(1, 65), metadata).sampleCount, 32, "DataView must honor typed-array storage offsets");
for (const bad of [new Uint8Array(), new Uint8Array(3), new Uint8Array(1048578), new Float64Array(32)]) assert.throws(() => analyze(bad, metadata));
for (const bad of [null, {}, {...metadata, format: "guess"}, {...metadata, channels: 2}, {...metadata, sampleRate: NaN}, {...metadata, sampleRate: 7999}, {...metadata, sampleRate: 384001}]) assert.throws(() => analyze(rails, bad));
for (const notch of ["100", 50, null]) assert.throws(() => analyze(rails, metadata, {notch}));
console.log("PCM decoding, metadata, raw metrics, Hz PSD/DFT/Parseval, notch comparisons, immutable sources and bounded work: passed");
