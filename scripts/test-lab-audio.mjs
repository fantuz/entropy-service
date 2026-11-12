import assert from "node:assert/strict";
import {readFileSync} from "node:fs";
import vm from "node:vm";

const source = readFileSync(new URL("../cmd/entropy-server/web/lab-adapters.js", import.meta.url), "utf8");
const deferred = () => {let resolve; const promise = new Promise(done => {resolve = done;}); return {promise, resolve};};
const flush = () => new Promise(resolve => setImmediate(resolve));

function harness({devices = [], permission, resume, failure, silent = false, channels = 2} = {}) {
    const requests = [], contexts = [], samples = [], errors = [], notices = [], timers = new Map();
    let ready = 0, enumerations = 0, nextTimer = 0;
    const track = new EventTarget();
    track.label = "USB Line In"; track.readyState = "live"; track.muted = false; track.getSettings = () => ({channelCount: channels});
    track.stop = () => {track.readyState = "ended";};
    const stream = {getTracks: () => [track], getAudioTracks: () => [track]};
    const navigator = {mediaDevices: {
        async enumerateDevices() {enumerations++; return devices;},
        async getUserMedia(options) {requests.push(options); if (failure) throw failure; return permission ? permission.promise : stream;}
    }};
    class Audio {
        constructor() {this.sampleRate = 48000; this.currentTime = 0; this.state = "running"; this.closed = 0; this.connections = []; contexts.push(this);}
        async resume() {if (resume) await resume.promise;}
        async close() {this.closed++; this.state = "closed";}
        createMediaStreamSource(value) {assert.equal(value, stream); return {connect: node => this.connections.push(node)};}
        createAnalyser() {return {connect: node => this.connections.push(node), getFloatTimeDomainData(data) {
            data.fill(0); if (!silent) data.set([-1, -.5, 0, .5, 1, 2, -2]);
        }};}
        createChannelSplitter(channels) {assert.equal(channels, 2); return {connect: (node, index) => {this.channel = index; this.connections.push(node);}};}
        createGain() {const gain = {gain: {value: 1}, connect: node => this.connections.push(node)}; this.gain = gain; return gain;}
    }
    const window = {AudioContext: Audio, EntropyLabSamples: {}};
    vm.runInNewContext(source, {window, navigator, DOMException, AbortController, EventTarget, Uint8Array, Float32Array, DataView,
        performance: {now: () => 0}, setTimeout: callback => {const id = ++nextTimer; timers.set(id, callback); return id;},
        clearTimeout: id => timers.delete(id)});
    const controller = new AbortController();
    const callbacks = {signal: controller.signal, ready: () => {ready++;}, emit: (bytes, encoding) => samples.push({bytes, encoding}),
        error: error => errors.push(error), info: message => notices.push(message)};
    return {api: window.EntropyLabAdapters, controller, navigator, requests, contexts, samples, errors, notices, track, stream, timers,
        async tick(seconds = 1) {contexts.forEach(context => {context.currentTime += seconds;}); const callbacks = [...timers.values()]; timers.clear(); for (const callback of callbacks) await callback(); await flush();},
        get ready() {return ready;}, get enumerations() {return enumerations;},
        start(options = {}) {return window.EntropyLabAdapters.start({kind: "microphone", size: 14, refresh: 50, ...options}, callbacks);}};
}

const listed = harness({devices: [
    {kind: "audioinput", deviceId: "default", label: "Default microphone"},
    {kind: "videoinput", deviceId: "camera", label: "Camera"},
    {kind: "audiooutput", deviceId: "speakers", label: "Speakers"},
    {kind: "audioinput", deviceId: "line-in", label: "Line In <USB>"},
    {kind: "audioinput", deviceId: "", label: "Private"}
]});
const inputs = await listed.api.audioInputs();
assert.equal(inputs.length, 1); assert.equal(inputs[0].id, "line-in"); assert.equal(inputs[0].label, "Line In <USB>");
assert.equal(listed.requests.length, 0, "listing inputs must not request capture permission");
assert.equal(listed.contexts.length, 0, "device discovery must not open Web Audio");

const selected = harness();
const close = await selected.start({audioDeviceId: "line-in"});
await flush();
assert.equal(selected.samples.length, 0, "Sample once must wait for a complete audio window");
await selected.tick();
assert.equal(selected.contexts[0].channel, 0, "default capture must avoid stereo downmix cancellation");
assert.equal(selected.requests[0].video, false);
assert.equal(selected.requests[0].audio.deviceId.exact, "line-in", "an explicit input must not fall back to a microphone");
for (const flag of ["echoCancellation", "noiseSuppression", "autoGainControl"]) assert.equal(selected.requests[0].audio[flag], false);
assert.equal(selected.ready, 1); assert.equal(selected.contexts[0].gain.gain.value, 0, "captured input must not play through speakers");
assert.deepEqual(Array.from(selected.samples[0].bytes), [0,128,0,192,0,0,0,64,255,127,255,127,0,128]);
assert.match(selected.samples[0].encoding, /PCM16LE mono.*48000.*USB Line In.*may overlap/);
assert.match(selected.notices[0], /USB Line In/);
selected.controller.abort(); close();
assert.equal(selected.contexts[0].closed, 1); assert.equal(selected.track.readyState, "ended"); assert.equal(selected.timers.size, 0);

const bounded = harness(); await bounded.start({size: 1048576});
await bounded.tick();
assert.equal(bounded.samples[0].bytes.length, 65536); assert.equal("deviceId" in bounded.requests[0].audio, false);
bounded.controller.abort();

for (const [name, expected] of [["NotAllowedError", /permission denied/], ["OverconstrainedError", /unavailable/], ["NotFoundError", /unavailable/], ["NotReadableError", /could not be opened/]]) {
    const failing = harness({failure: new DOMException("test failure", name)});
    await assert.rejects(failing.start({audioDeviceId: "unplugged"}), expected);
    assert.equal(failing.requests.length, 1, "failed exact selection must not retry on another device");
    assert.equal(failing.contexts[0].closed, 1); assert.equal(failing.samples.length, 0); assert.equal(failing.timers.size, 0);
}

const pending = deferred(), late = harness({permission: pending});
const starting = late.start(); await flush();
late.controller.abort(); pending.resolve(late.stream);
await assert.rejects(starting, error => error.name === "AbortError");
assert.equal(late.track.readyState, "ended"); assert.equal(late.contexts[0].closed, 1); assert.equal(late.samples.length, 0);

const pausedResume = deferred(), stopped = harness({resume: pausedResume});
const resuming = stopped.start(); stopped.controller.abort(); pausedResume.resolve();
await assert.rejects(resuming, error => error.name === "AbortError");
assert.equal(stopped.requests.length, 0, "Stop before permission must prevent a later capture prompt");

const unplugged = harness(); await unplugged.start(); await flush();
unplugged.track.dispatchEvent(new Event("ended"));
assert.match(unplugged.errors[0].message, /disconnected/);
assert.equal(unplugged.contexts[0].closed, 1); assert.equal(unplugged.timers.size, 0);

const unsupported = harness(); delete unsupported.navigator.mediaDevices.enumerateDevices;
assert.equal(await unsupported.api.audioInputs(), null);
unsupported.controller.abort();
await assert.rejects(unsupported.start(), error => error.name === "AbortError");
assert.equal(unsupported.contexts.length, 0);
console.log("Audio discovery without capture, exact input selection, PCM bytes, muted output, bounds, permission failures, late abort and hot-unplug cleanup: passed");

const linein = harness();
await assert.rejects(linein.start({kind: "linein"}), /explicit line-in/);
assert.equal(linein.requests.length, 0, "line-in must not silently capture the default microphone");
await linein.start({kind: "linein", audioDeviceId: "line-in", audioChannel: "right"});
assert.equal(linein.contexts[0].channel, 1);
linein.controller.abort();
const mono = harness({channels: 1});
await assert.rejects(mono.start({audioChannel: "right"}), /input is mono/);
assert.equal(mono.track.readyState, "ended");
const mixed = harness(); await mixed.start({audioChannel: "mix"});
assert.equal(mixed.contexts[0].channel, undefined); mixed.controller.abort();
const quiet = harness({silent: true}); await quiet.start(); await quiet.tick();
assert.ok(quiet.samples[0].bytes.every(byte => byte === 0), "silence must not be transformed into invented entropy");
assert.match(quiet.notices.at(-1), /Silent input.*device.*channel/); quiet.controller.abort();
const muted = harness(); await muted.start(); muted.track.muted = true; await muted.tick();
assert.equal(muted.samples.length, 0); assert.match(muted.notices.at(-1), /muted or unavailable/);
muted.track.muted = false; muted.contexts[0].state = "suspended"; await muted.tick();
assert.equal(muted.samples.length, 0); assert.match(muted.notices.at(-1), /engine is suspended/);
muted.contexts[0].state = "running"; await muted.tick(); assert.equal(muted.samples.length, 1); muted.controller.abort();
console.log("Audio warm-up, explicit line-in, per-channel capture, mono rejection, silence and suspended/muted diagnostics: passed");
