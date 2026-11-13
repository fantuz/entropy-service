"use strict";

// Separate PCM axes: byte plots remain in byte offsets and cycles/byte.
(() => {
    function prepare(canvas) {
        const rect = canvas.getBoundingClientRect(), width = Math.max(1, rect.width), height = Math.max(1, rect.height);
        const ratio = Math.min(3, globalThis.devicePixelRatio || 1);
        const w = Math.round(width * ratio), h = Math.round(height * ratio);
        if (canvas.width !== w || canvas.height !== h) { canvas.width = w; canvas.height = h; }
        const ctx = canvas.getContext("2d"), style = getComputedStyle(document.documentElement);
        const color = name => style.getPropertyValue(name).trim();
        ctx.setTransform(ratio, 0, 0, ratio, 0, 0); ctx.clearRect(0, 0, width, height); ctx.font = "11px monospace";
        return {ctx, width, height, left: 53, top: 31, right: width - 14, bottom: height - 43,
            text: color("--muted"), grid: color("--border"), raw: color("--green"), filtered: color("--cyan")};
    }
    function empty(canvas, message = "Select an original PCM16LE audio sample") {
        const p = prepare(canvas);
        p.ctx.fillStyle = p.text; p.ctx.textAlign = "center"; p.ctx.fillText(message, p.width / 2, p.height / 2);
    }
    function axes(canvas, {xmin, xmax, ymin, ymax, xlabel, ylabel, xformat = n => String(Math.round(n)), yformat = n => n.toFixed(1)}, audio) {
        const p = prepare(canvas), {ctx, width, height, left, right, top, bottom} = p;
        p.x = value => left + (value - xmin) / (xmax - xmin || 1) * (right - left);
        p.y = value => bottom - (value - ymin) / (ymax - ymin || 1) * (bottom - top);
        ctx.fillStyle = p.text; ctx.strokeStyle = p.grid; ctx.lineWidth = 1;
        ctx.textAlign = "left"; ctx.fillText(ylabel, 2, 12);
        ctx.fillStyle = p.raw; ctx.fillText("Raw —", left, 25);
        if (audio.filtered) { ctx.fillStyle = p.filtered; ctx.fillText(`${audio.filtered.notchHz} Hz notch · dashed`, left + 70, 25); }
        const ticks = width < 400 ? 3 : 4;
        for (let i = 0; i <= ticks; i++) {
            const xv = xmin + i / ticks * (xmax - xmin), yv = ymin + i / ticks * (ymax - ymin), px = p.x(xv), py = p.y(yv);
            ctx.strokeStyle = p.grid; ctx.beginPath(); ctx.moveTo(left, py); ctx.lineTo(right, py); ctx.stroke();
            ctx.fillStyle = p.text; ctx.textAlign = "right"; ctx.fillText(yformat(yv), left - 7, py + 4);
            ctx.textAlign = i === 0 ? "left" : i === ticks ? "right" : "center"; ctx.fillText(xformat(xv), px, bottom + 16);
        }
        ctx.fillStyle = p.text; ctx.textAlign = "center"; ctx.fillText(xlabel, (left + right) / 2, height - 5);
        return p;
    }
    function series(p, samples, sampleRate, color, dashed) {
        const {ctx, left, right} = p, columns = Math.max(1, Math.floor(right - left));
        ctx.strokeStyle = color; ctx.lineWidth = 1.2; ctx.setLineDash(dashed ? [5, 3] : []); ctx.beginPath();
        if (samples.length > columns) {
            // Exact min/max per column preserves short impulses and clipping.
            for (let column = 0; column < columns; column++) {
                const start = Math.floor(column * samples.length / columns), end = Math.floor((column + 1) * samples.length / columns);
                let min = Infinity, max = -Infinity;
                for (let i = start; i < end; i++) { min = Math.min(min, samples[i]); max = Math.max(max, samples[i]); }
                const x = left + (column + .5) / columns * (right - left);
                ctx.moveTo(x, p.y(min)); ctx.lineTo(x, p.y(max) - .4);
            }
        } else {
            for (let i = 0; i < samples.length; i++) {
                const x = p.x(1000 * i / sampleRate), y = p.y(samples[i]);
                if (i) ctx.lineTo(x, y); else ctx.moveTo(x, y);
                if (samples.length === 1) { ctx.moveTo(x - 1, y); ctx.lineTo(x + 1, y); }
            }
        }
        ctx.stroke(); ctx.setLineDash([]);
    }
    function waveform(canvas, audio) {
        if (!audio?.raw?.samples?.length) { empty(canvas); return; }
        const limit = Math.max(1, audio.raw.peak, audio.filtered?.peak || 0) * 1.05;
        const p = axes(canvas, {xmin: 0, xmax: Math.max(1, audio.sampleCount - 1) * 1000 / audio.sampleRate,
            ymin: -limit, ymax: limit, xlabel: "Time within selected window (ms)", ylabel: "Amplitude (FS)", xformat: n => n.toFixed(n < 10 ? 1 : 0)}, audio);
        series(p, audio.raw.samples, audio.sampleRate, p.raw, false);
        if (audio.filtered) series(p, audio.filtered.samples, audio.sampleRate, p.filtered, true);
        return {samples: audio.sampleCount, durationMs: audio.sampleCount * 1000 / audio.sampleRate, amplitudeLimit: limit};
    }
    function powerLine(p, fft, {maxHz, floor, ceiling}, color, dashed) {
        const {ctx, left, right} = p, last = Math.min(fft.power.length - 1, Math.floor(maxHz / fft.binWidthHz));
        const columns = Math.max(1, Math.min(last + 1, Math.floor(right - left)));
        ctx.strokeStyle = color; ctx.lineWidth = 1.2; ctx.setLineDash(dashed ? [5, 3] : []); ctx.beginPath();
        for (let column = 0; column < columns; column++) {
            const start = Math.floor(column * (last + 1) / columns), end = Math.floor((column + 1) * (last + 1) / columns);
            let peak = 0;
            for (let k = start; k < end; k++) peak = Math.max(peak, fft.power[k]);
            const x = columns === last + 1 ? p.x(start * fft.binWidthHz) : left + (column + .5) / columns * (right - left);
            const y = p.y(Math.min(ceiling, Math.max(floor, 10 * Math.log10(Math.max(peak, 1e-30)))));
            if (column) ctx.lineTo(x, y); else ctx.moveTo(x, y);
        }
        ctx.stroke(); ctx.setLineDash([]);
    }
    function spectrum(canvas, audio, {maxHz = Math.min(4000, (audio?.sampleRate || 8000) / 2), floor = -120, ceiling = 0} = {}) {
        if (!audio?.raw?.spectrum) { empty(canvas, "Audio FFT needs ≥32 PCM samples"); return; }
        if (!Number.isFinite(maxHz) || maxHz <= 0 || maxHz > audio.sampleRate / 2 || !Number.isFinite(floor) || !Number.isFinite(ceiling) || floor >= ceiling) {
            empty(canvas, "Choose a valid audio frequency and dB range"); return;
        }
        const p = axes(canvas, {xmin: 0, xmax: maxHz, ymin: floor, ymax: ceiling,
            xlabel: "Audio frequency (Hz)", ylabel: "PSD (dB re FS²/Hz)", yformat: n => String(Math.round(n))}, audio);
        powerLine(p, audio.raw.spectrum, {maxHz, floor, ceiling}, p.raw, false);
        if (audio.filtered?.spectrum) powerLine(p, audio.filtered.spectrum, {maxHz, floor, ceiling}, p.filtered, true);
        return {maxHz, floor, ceiling, binWidthHz: audio.raw.spectrum.binWidthHz, size: audio.raw.spectrum.size, startSample: audio.raw.spectrum.startSample};
    }
    globalThis.EntropyAudioPlots = Object.freeze({empty, waveform, spectrum});
})();
