"use strict";

// One pending value and one scheduled paint, independent of input throughput.
window.EntropyDisplayClock = {
    intervalMs: 50,
    create(paint) {
        let latest = null, timer = null, animation = null, lastPaint = -Infinity;
        function schedule() {
            if (latest === null || timer !== null || animation !== null) return;
            const delay = Math.max(0, 50 - (performance.now() - lastPaint));
            if (delay > 0) {
                timer = setTimeout(() => { timer = null; schedule(); }, delay);
            } else {
                animation = requestAnimationFrame(draw);
            }
        }
        function draw(timestamp) {
            animation = null;
            if (latest === null) return;
            const now = Number.isFinite(timestamp) ? timestamp : performance.now();
            if (now - lastPaint + 1e-6 < 50) { schedule(); return; }
            const value = latest;
            latest = null;
            lastPaint = now;
            paint(value);
            schedule();
        }
        return {
            submit(value) { latest = value; schedule(); },
            cancel() {
                clearTimeout(timer);
                if (animation !== null) cancelAnimationFrame(animation);
                latest = timer = animation = null;
                lastPaint = -Infinity;
            },
        };
    },
};
