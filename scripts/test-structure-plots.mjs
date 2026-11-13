import assert from "node:assert/strict";
import {readFileSync} from "node:fs";
import vm from "node:vm";

// Capture actual canvas commands: verify the plotted coordinates/scales and
// endpoint peaks, independently of the numerical diagnostics implementation.
const events = new Map();
let dark = false;
const palettes = {light: {"--border":"border-light", "--muted":"text-light", "--green":"green-light", "--cyan":"cyan-light", "--warn":"warn-light", "--surface":"surface-light"},
    dark: {"--border":"border-dark", "--muted":"text-dark", "--green":"green-dark", "--cyan":"cyan-dark", "--warn":"warn-dark", "--surface":"surface-dark"}};
const context = vm.createContext({document: {documentElement: {}}, devicePixelRatio: 2,
    window: {addEventListener(name, callback) {events.set(name, callback);}},
    getComputedStyle() {return {getPropertyValue(name) {return palettes[dark ? "dark" : "light"][name];}};}});
vm.runInContext(readFileSync(new URL("../cmd/entropy-server/web/structure-plots.js", import.meta.url), "utf8"), context);
const plots = context.EntropyStructurePlots;
function canvas(width = 300, height = 240) {
    const commands = [], stack = [];
    const draw = {globalAlpha: 1, fillStyle: "", strokeStyle: "",
        save() {stack.push({globalAlpha: this.globalAlpha, fillStyle: this.fillStyle, strokeStyle: this.strokeStyle});},
        restore() {Object.assign(this, stack.pop());},
        setTransform(...values) {commands.push({name: "transform", values});},
        clearRect(...values) {commands.push({name: "clear", values});},
        fillRect(...values) {commands.push({name: "fill", values, alpha: this.globalAlpha, fill: this.fillStyle});},
        fillText(text, ...values) {commands.push({name: "text", text, values, fill: this.fillStyle});},
        moveTo(...values) {commands.push({name: "move", values});},
        lineTo(...values) {commands.push({name: "line", values});},
        rect(...values) {commands.push({name: "rect", values});},
        beginPath() {}, stroke() {}, clip() {}, setLineDash() {}};
    return {commands, width: 0, height: 0, getBoundingClientRect() {return {width, height};}, getContext() {return draw;}};
}
const close = (actual, expected) => assert.ok(Math.abs(actual - expected) < 1e-9, `${actual} != ${expected}`);
function finiteCommands(c) {
    for (const command of c.commands) for (const value of command.values) assert.ok(Number.isFinite(value), JSON.stringify(command));
}

const corr = canvas();
const result = plots.correlation(corr, {defined: true, count: 100, maxLag: 2, values: [-1, 1]});
close(result.band, .196);
assert.equal(corr.width, 600); assert.equal(corr.height, 480);
const greenPoints = corr.commands.filter(c => c.name === "fill" && c.fill === "green-light");
assert.equal(greenPoints.length, 2);
close(greenPoints[0].values[0] + 1.5, 47); close(greenPoints[0].values[1] + 1.5, 198);
close(greenPoints[1].values[0] + 1.5, 285); close(greenPoints[1].values[1] + 1.5, 24);
assert.ok(corr.commands.some(c => c.name === "fill" && c.fill === "warn-light" && c.alpha === .1));
assert.ok(corr.commands.some(c => c.name === "text" && c.text === "Lag (bytes)"));
finiteCommands(corr);
const constant = canvas();
assert.equal(plots.correlation(constant, {defined: false, values: [NaN], maxLag: 1}).defined, false);
assert.ok(constant.commands.some(c => c.name === "text" && /undefined/.test(c.text)));
assert.ok(!constant.commands.some(c => c.fill === "green-light"));

const transition = canvas(), counts = new Uint32Array(256);
counts[1] = 3; counts[16] = 1; // From low byte to next higher group, then converse.
const heat = plots.transitions(transition, {counts, bins: 16, pairs: 4});
assert.equal(heat.maximum, 3);
const cells = transition.commands.filter(c => c.name === "fill" && c.fill === "cyan-light" && Math.abs(c.values[2] - 12.575) < 1e-9);
assert.equal(cells.length, 2);
close(cells[0].values[0], 47); close(cells[0].values[1], 176.25); close(cells[0].alpha, 1);
close(cells[1].values[0], 59.375); close(cells[1].values[1], 187.125); close(cells[1].alpha, .5);
assert.ok(transition.commands.some(c => c.name === "text" && c.text === "3"));
assert.ok(transition.commands.some(c => c.name === "text" && c.text === "Previous byte (grouped)"));
finiteCommands(transition);

const local = canvas();
plots.blocks(local, {count: 2, entropy: [8, 0], ones: [1, 0], blockSize: 1024, coveredBytes: 2048, omittedBytes: 13}, {offset: 4096});
const entropyPoints = local.commands.filter(c => c.name === "fill" && c.fill === "green-light");
const biasPoints = local.commands.filter(c => c.name === "fill" && c.fill === "cyan-light");
close(entropyPoints[0].values[1] + 1.5, 24); close(entropyPoints[1].values[1] + 1.5, 94);
close(biasPoints[0].values[1] + 1.5, 139); close(biasPoints[1].values[1] + 1.5, 198);
for (const label of ["Shannon (bits/byte)", "One bits (%)", "4096", "6144"]) assert.ok(local.commands.some(c => c.name === "text" && c.text === label), label);
finiteCommands(local);

const spectral = canvas(), nyquist = new Float64Array(5); nyquist[4] = 100;
const history = [
    {power: nyquist, size: 8, sourceBins: 5, time: 12000, offset: 4096},
    {power: nyquist, size: 8, sourceBins: 5, time: 2000, offset: 8192}
];
const info = plots.spectrogram(spectral, history, {floor: 0, ceiling: 20});
assert.equal(info.latest - info.oldest, 10000);
const endpoint = spectral.commands.filter(c => c.name === "fill" && c.fill === "cyan-light" && c.alpha === 1);
assert.equal(endpoint.length, 2, "The Nyquist peak must remain visible in both rows");
close(endpoint[0].values[0], 255.25); close(endpoint[0].values[2], 29.95);
assert.ok(spectral.commands.some(c => c.name === "text" && c.text === "10.0"), "Use actual capture age rather than inferred frame cadence");
assert.ok(spectral.commands.some(c => c.name === "text" && c.text === "Frequency (cycles/byte)"));
finiteCommands(spectral);
const fileHistory = canvas(); plots.spectrogram(fileHistory, history, {kind: "file"});
assert.ok(fileHistory.commands.some(c => c.name === "text" && c.text === "4096"));
assert.ok(fileHistory.commands.some(c => c.name === "text" && c.text === "8192"));

dark = true; events.get("entropythemechange")();
const themed = canvas(); plots.blocks(themed, {count: 1, entropy: [4], ones: [.5], blockSize: 256, coveredBytes: 256});
assert.ok(themed.commands.some(c => c.name === "fill" && c.fill === "cyan-dark"));
assert.ok(themed.commands.some(c => c.name === "text" && c.fill === "text-dark"));
const hidden = canvas(0, 0); plots.correlation(hidden, {defined: true, count: 32, maxLag: 1, values: [0]});
assert.equal(hidden.commands.length, 0); assert.equal(hidden.width, 0);
const highDPI = canvas(); context.devicePixelRatio = 9; plots.empty(highDPI, "Waiting");
assert.equal(highDPI.width, 900); assert.equal(highDPI.height, 720);
console.log("Diagnostic plotting: correlation bounds/reference, transition orientation/log scale, separate local scales, Nyquist/age/offset history, themes and DPR bounds passed");
