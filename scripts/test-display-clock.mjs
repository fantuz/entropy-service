import assert from "node:assert/strict";
import {readFileSync} from "node:fs";
import vm from "node:vm";

let now = 0, sequence = 0;
const timers = new Map(), animations = new Map();
const window = {};
vm.runInNewContext(readFileSync(new URL("../cmd/entropy-server/web/display-clock.js", import.meta.url), "utf8"), {
    window, performance: {now: () => now},
    setTimeout(fn, delay) { const id = ++sequence; timers.set(id, {fn, at: now + delay}); return id; },
    clearTimeout(id) { timers.delete(id); },
    requestAnimationFrame(fn) { const id = ++sequence; animations.set(id, fn); return id; },
    cancelAnimationFrame(id) { animations.delete(id); },
});
const painted = [];
const clock = window.EntropyDisplayClock.create(value => painted.push({at: now, value}));
function tick(at) {
    now = at;
    for (const [id, timer] of [...timers]) {
        if (timer.at <= now) { timers.delete(id); timer.fn(); }
    }
    for (const [id, draw] of [...animations]) { animations.delete(id); draw(now); }
}

// A burst replaces pending data, without allocating one callback per sample.
for (let i = 0; i < 1000; i++) clock.submit(i);
assert.equal(animations.size, 1);
tick(0);
assert.deepEqual(painted, [{at: 0, value: 999}]);
for (let at = 1; at <= 1000; at++) {
    clock.submit(at);
    tick(at);
    assert.ok(timers.size <= 1 && animations.size <= 1);
}
assert.equal(painted.length, 21, "initial paint plus twenty subsequent frames in one second");
for (let i = 1; i < painted.length; i++) assert.equal(painted[i].at - painted[i - 1].at, 50);
assert.equal(painted.at(-1).value, 1000, "each paint uses the newest input");

clock.submit("cancelled");
assert.equal(timers.size, 1);
clock.cancel();
assert.equal(timers.size, 0);
assert.equal(animations.size, 0);
tick(2000);
assert.equal(painted.length, 21, "cancel removes pending work");
clock.submit("restored");
tick(2000);
assert.deepEqual(painted.at(-1), {at: 2000, value: "restored"});

// Existing analysis/playback choices retain defaults and now offer 20 FPS.
for (const name of ["api-stream.html", "api-sources.html", "api-socket.html", "api-upload.html"]) {
    const html = readFileSync(new URL("../cmd/entropy-server/web/" + name, import.meta.url), "utf8");
    assert.match(html, /id="display-fps"[^]*?<option value="20">20 (?:fps|windows\/s)<\/option>/);
    assert.match(html, /<option value="5" selected>/);
}
for (const name of ["api-bytes.html", "api-colors.html", "api-words.html", "api-stream.html", "api-sources.html", "api-socket.html"]) {
    const html = readFileSync(new URL("../cmd/entropy-server/web/" + name, import.meta.url), "utf8");
    assert.equal((html.match(/src="\/display-clock.js"/g) || []).length, 1, name + " loads the shared clock once");
}
console.log("Display clock: 20 FPS, latest-only coalescing, cancellation, resume and page controls passed");
