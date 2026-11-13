import assert from "node:assert/strict";
import {readFileSync} from "node:fs";
import vm from "node:vm";

const numericCalls = [], labels = [], strokes = [], palette = {"--muted": "#456", "--border": "#789", "--green": "#012", "--cyan": "#345"};
const ctx = {strokeStyle: "", dash: [],
    setTransform(...values) {numericCalls.push(values);}, clearRect(...values) {numericCalls.push(values);},
    fillText(text, ...values) {labels.push(text); numericCalls.push(values);},
    beginPath() {}, moveTo(...values) {numericCalls.push(values);}, lineTo(...values) {numericCalls.push(values);},
    stroke() {strokes.push({color: this.strokeStyle, dash: this.dash});}, setLineDash(values) {this.dash = values;}};
const canvas = {width: 0, height: 0, getBoundingClientRect: () => ({width: 311, height: 240}), getContext: () => ctx};
const context = vm.createContext({document: {documentElement: {}}, devicePixelRatio: 4,
    getComputedStyle: () => ({getPropertyValue: name => palette[name]})});
for (const name of ["audio-analysis.js", "audio-plots.js"]) vm.runInContext(readFileSync(new URL("../cmd/entropy-server/web/" + name, import.meta.url), "utf8"), context);
const bytes = new Uint8Array(65536), view = new DataView(bytes.buffer);
for (let i = 0; i < bytes.length / 2; i++) view.setInt16(i * 2, Math.round(12000 * Math.sin(2 * Math.PI * 60 * i / 48000)), true);
// Pass the VM's own typed array so validation exercises actual browser realms.
context.pcmBytes = Array.from(bytes);
const audio = vm.runInContext("EntropyAudio.analyze(Uint8Array.from(pcmBytes), {format:'pcm16le',sampleRate:48000,channels:1}, {notch:'60'})", context);
const plots = context.EntropyAudioPlots;
const wave = plots.waveform(canvas, audio), fft = plots.spectrum(canvas, audio);
assert.equal(canvas.width, 933); assert.equal(canvas.height, 720, "DPR is capped at3");
assert.equal(wave.samples, 32768); assert.equal(fft.maxHz, 4000);
assert.equal(fft.startSample, 24576); assert.equal(fft.size, 8192);
assert.ok(labels.includes("Audio frequency (Hz)")); assert.ok(labels.includes("Time within selected window (ms)"));
assert.ok(labels.includes("PSD (dB re FS²/Hz)")); assert.ok(labels.includes("60 Hz notch · dashed"));
assert.ok(strokes.some(stroke => stroke.color === palette["--green"] && stroke.dash.length === 0));
assert.ok(strokes.some(stroke => stroke.color === palette["--cyan"] && stroke.dash.length > 0));
assert.ok(numericCalls.flat().every(Number.isFinite), "sample-heavy render paths emitted a nonfinite coordinate");
palette["--green"] = "#abc"; plots.waveform(canvas, audio);
assert.ok(strokes.some(stroke => stroke.color === "#abc"), "next draw must follow a theme change");
const before = labels.length;
assert.equal(plots.spectrum(canvas, audio, {maxHz: 25000}), undefined);
assert.match(labels.at(-1), /valid audio frequency/);
plots.waveform(canvas, null); plots.spectrum(canvas, {...audio, raw: {...audio.raw, spectrum: null}});
assert.ok(labels.length > before); assert.match(labels.at(-1), /≥32/);
console.log("PCM canvas DPR/theme, finite bounded rendering, explicit Hz/time/PSD axes and raw/notch legends: passed");
