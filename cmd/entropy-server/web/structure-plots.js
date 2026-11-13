"use strict";

// Optional diagnostic views. All coordinates are CSS pixels; backing canvases
// follow the same capped pixel ratio as the ordinary analysis plots.
(() => {
    let color;
    function updatePalette() {
        const style = getComputedStyle(document.documentElement);
        const value = name => style.getPropertyValue(name).trim();
        color = {grid: value("--border"), text: value("--muted"), green: value("--green"),
            cyan: value("--cyan"), warn: value("--warn"), surface: value("--surface")};
    }
    updatePalette();
    window.addEventListener("entropythemechange", updatePalette);

    function prepare(canvas, rightSpace = 15) {
        const rect = canvas.getBoundingClientRect();
        // A closed details element has no drawable layout. Do not create a
        // misleading one-pixel plot or resize a useful backing image then.
        if (rect.width < 100 || rect.height < 100) return null;
        const {width, height} = rect;
        const ratio = Math.min(3, Math.max(1, globalThis.devicePixelRatio || 1));
        const w = Math.round(width * ratio), h = Math.round(height * ratio);
        if (canvas.width !== w || canvas.height !== h) { canvas.width = w; canvas.height = h; }
        const ctx = canvas.getContext("2d");
        ctx.setTransform(ratio, 0, 0, ratio, 0, 0);
        ctx.clearRect(0, 0, width, height);
        ctx.font = `${width < 360 ? 10 : 11}px monospace`;
        ctx.lineWidth = 1; ctx.globalAlpha = 1; ctx.setLineDash([]);
        return {ctx, width, height, left: 47, top: 24, right: width - rightSpace, bottom: height - 42};
    }

    function empty(canvas, message) {
        const p = prepare(canvas);
        if (!p) return;
        p.ctx.fillStyle = color.text; p.ctx.textAlign = "center";
        // Fit long explanations on narrow canvases without overflowing labels.
        p.ctx.fillText(message, p.width / 2, p.height / 2, p.width - 12);
    }

    function grid(p, {xmin, xmax, ymin, ymax, xlabel, ylabel, xformat = n => String(Math.round(n)),
        yformat = n => String(Math.round(n)), ticks = 4, xlabels = true}) {
        const {ctx, left, right, top, bottom, width, height} = p;
        p.x = value => left + (value - xmin) / (xmax - xmin || 1) * (right - left);
        p.y = value => bottom - (value - ymin) / (ymax - ymin || 1) * (bottom - top);
        ctx.fillStyle = color.text; ctx.strokeStyle = color.grid; ctx.lineWidth = 1;
        ctx.textAlign = "left"; ctx.fillText(ylabel, 2, top - 10, width - 4);
        for (let i = 0; i <= ticks; i++) {
            const yvalue = ymin + i / ticks * (ymax - ymin), py = p.y(yvalue);
            ctx.beginPath(); ctx.moveTo(left, py); ctx.lineTo(right, py); ctx.stroke();
            ctx.textAlign = "right"; ctx.fillText(yformat(yvalue), left - 6, py + 3);
            if (xlabels) {
                const xvalue = xmin + i / ticks * (xmax - xmin), px = p.x(xvalue);
                ctx.textAlign = i === 0 ? "left" : i === ticks ? "right" : "center";
                ctx.fillText(xformat(xvalue), px, bottom + 16);
            }
        }
        if (xlabels) { ctx.textAlign = "center"; ctx.fillText(xlabel, (left + right) / 2, height - 5, width - 8); }
        return p;
    }

    function correlation(canvas, data) {
        if (!data?.defined || !data.values?.length) {
            empty(canvas, "Correlation undefined: constant or insufficient input");
            return {defined: false, maxLag: data?.maxLag || 0};
        }
        const p = prepare(canvas);
        if (!p) return;
        grid(p, {xmin: 1, xmax: Math.max(2, data.maxLag), ymin: -1, ymax: 1,
            xlabel: "Lag (bytes)", ylabel: "Autocorrelation", yformat: n => n.toFixed(1)});
        const {ctx, left, right, top, bottom} = p;
        ctx.save(); ctx.beginPath(); ctx.rect(left, top, right - left, bottom - top); ctx.clip();
        const band = data.count > 4 ? Math.min(1, 1.96 / Math.sqrt(data.count)) : null;
        if (band !== null) {
            ctx.fillStyle = color.warn; ctx.globalAlpha = .1;
            ctx.fillRect(left, p.y(band), right - left, p.y(-band) - p.y(band));
            ctx.globalAlpha = 1; ctx.strokeStyle = color.warn; ctx.setLineDash([3, 4]);
            for (const reference of [-band, band]) {
                ctx.beginPath(); ctx.moveTo(left, p.y(reference)); ctx.lineTo(right, p.y(reference)); ctx.stroke();
            }
            ctx.setLineDash([]);
        }
        ctx.strokeStyle = color.text; ctx.globalAlpha = .6;
        ctx.beginPath(); ctx.moveTo(left, p.y(0)); ctx.lineTo(right, p.y(0)); ctx.stroke(); ctx.globalAlpha = 1;
        ctx.strokeStyle = color.green; ctx.fillStyle = color.green; ctx.lineWidth = 1.2;
        for (let i = 0; i < data.values.length; i++) {
            const value = data.values[i];
            if (!Number.isFinite(value)) continue;
            const x = p.x(i + 1), y = p.y(Math.max(-1, Math.min(1, value)));
            ctx.beginPath(); ctx.moveTo(x, p.y(0)); ctx.lineTo(x, y); ctx.stroke();
            ctx.fillRect(x - 1.5, y - 1.5, 3, 3);
        }
        ctx.restore();
        return {defined: true, maxLag: data.maxLag, band};
    }

    function transitions(canvas, data) {
        if (!data?.pairs || !data.counts?.length) { empty(canvas, "At least two bytes are needed for transitions"); return; }
        const p = prepare(canvas, 55);
        if (!p) return;
        grid(p, {xmin: 0, xmax: 256, ymin: 0, ymax: 256,
            xlabel: "Previous byte (grouped)", ylabel: "Next byte (grouped)",
            xformat: n => String(Math.min(255, Math.round(n))), yformat: n => String(Math.min(255, Math.round(n)))});
        const {ctx, left, right, top, bottom} = p;
        let maximum = 0;
        for (const count of data.counts) maximum = Math.max(maximum, count);
        const denominator = Math.log1p(maximum), cellWidth = (right - left) / data.bins, cellHeight = (bottom - top) / data.bins;
        ctx.fillStyle = color.surface; ctx.fillRect(left, top, right - left, bottom - top);
        ctx.fillStyle = color.cyan;
        for (let from = 0; from < data.bins; from++) {
            for (let to = 0; to < data.bins; to++) {
                const count = data.counts[from * data.bins + to];
                if (!count) continue;
                ctx.globalAlpha = Math.log1p(count) / denominator;
                ctx.fillRect(left + from * cellWidth, bottom - (to + 1) * cellHeight, cellWidth + .2, cellHeight + .2);
            }
        }
        ctx.globalAlpha = 1;
        const legendX = right + 10, legendWidth = 9;
        ctx.fillStyle = color.surface; ctx.fillRect(legendX, top, legendWidth, bottom - top);
        ctx.fillStyle = color.cyan;
        for (let i = 0; i < 32; i++) {
            ctx.globalAlpha = 1 - i / 31;
            ctx.fillRect(legendX, top + i / 32 * (bottom - top), legendWidth, (bottom - top) / 32 + .2);
        }
        ctx.globalAlpha = 1; ctx.fillStyle = color.text; ctx.textAlign = "left";
        ctx.fillText(String(maximum), legendX + legendWidth + 3, top + 6, 30);
        ctx.fillText("0", legendX + legendWidth + 3, bottom, 30);
        return {maximum, pairs: data.pairs, bins: data.bins};
    }

    function blockLine(p, values, blockSize, offset, maximum, stroke) {
        const {ctx, top, bottom, left, right} = p;
        ctx.save(); ctx.beginPath(); ctx.rect(left, top, right - left, bottom - top); ctx.clip();
        ctx.strokeStyle = stroke; ctx.fillStyle = stroke; ctx.lineWidth = 1.5; ctx.beginPath();
        values.forEach((value, i) => {
            if (!Number.isFinite(value)) return;
            const x = p.x(offset + (i + .5) * blockSize), y = p.y(Math.max(0, Math.min(maximum, value)));
            if (i === 0) ctx.moveTo(x, y); else ctx.lineTo(x, y);
            ctx.fillRect(x - 1.5, y - 1.5, 3, 3);
        });
        ctx.stroke(); ctx.restore();
    }

    function blocks(canvas, data, {offset = 0} = {}) {
        if (!data?.count) { empty(canvas, "No complete block: choose a smaller block size"); return; }
        const p = prepare(canvas);
        if (!p) return;
        const mid = Math.round((p.top + p.bottom) / 2);
        const upper = {...p, bottom: mid - 17};
        grid(upper, {xmin: offset, xmax: offset + data.coveredBytes, ymin: 0, ymax: 8,
            ylabel: "Shannon (bits/byte)", ticks: 2, xlabels: false});
        blockLine(upper, data.entropy, data.blockSize, offset, 8, color.green);
        const lower = {...p, top: mid + 28};
        grid(lower, {xmin: offset, xmax: offset + data.coveredBytes, ymin: 0, ymax: 1,
            xlabel: "Byte offset · complete blocks", ylabel: "One bits (%)", ticks: 2, yformat: n => String(Math.round(n * 100))});
        const {ctx, left, right} = lower;
        ctx.strokeStyle = color.warn; ctx.setLineDash([3, 4]);
        ctx.beginPath(); ctx.moveTo(left, lower.y(.5)); ctx.lineTo(right, lower.y(.5)); ctx.stroke(); ctx.setLineDash([]);
        blockLine(lower, data.ones, data.blockSize, offset, 1, color.cyan);
        return {count: data.count, coveredBytes: data.coveredBytes, omittedBytes: data.omittedBytes};
    }

    function compactOffset(value) {
        if (value < 10000) return String(value);
        return `${(value / (value >= 1048576 ? 1048576 : 1024)).toFixed(1)}${value >= 1048576 ? "Mi" : "Ki"}`;
    }

    function spectrogram(canvas, rows, {kind = "stream", floor = -60, ceiling = 50, maxFrequency = .5} = {}) {
        if (!rows?.length) { empty(canvas, "Awaiting distinct spectral samples"); return; }
        const p = prepare(canvas);
        if (!p) return;
        const {ctx, left, right, top, bottom, width, height} = p;
        const latest = rows[0].time, oldest = rows[rows.length - 1].time;
        floor = Number.isFinite(floor) ? floor : -60; ceiling = Number.isFinite(ceiling) && ceiling > floor ? ceiling : floor + 1;
        maxFrequency = Number.isFinite(maxFrequency) && maxFrequency > 0 ? Math.min(.5, maxFrequency) : .5;
        ctx.fillStyle = color.surface; ctx.fillRect(left, top, right - left, bottom - top);
        const rowHeight = (bottom - top) / rows.length;
        rows.forEach((row, index) => {
            const columns = row.power.length;
            ctx.fillStyle = color.cyan;
            for (let cell = 0; cell < columns; cell++) {
                // Bin centres include both DC and Nyquist. Half-bin edges
                // keep those endpoint peaks visible rather than dropping
                // the Nyquist bin as a zero-width interval at x=0.5.
                const start = Math.max(0, (Math.floor(cell * row.sourceBins / columns) - .5) / row.size);
                const end = Math.min(.5, (Math.floor((cell + 1) * row.sourceBins / columns) - .5) / row.size, maxFrequency);
                if (start >= end || start >= maxFrequency) continue;
                const dB = 10 * Math.log10(Math.max(1e-30, row.power[cell]));
                ctx.globalAlpha = Math.max(0, Math.min(1, (dB - floor) / (ceiling - floor)));
                ctx.fillRect(left + start / maxFrequency * (right - left), top + index * rowHeight,
                    (end - start) / maxFrequency * (right - left) + .2, rowHeight + .2);
            }
        });
        ctx.globalAlpha = 1; ctx.fillStyle = color.text; ctx.textAlign = "left";
        ctx.fillText(kind === "file" ? "Window start (bytes)" : "Capture age (seconds)", 2, 12, width - 4);
        const ticks = width < 360 ? 2 : 4;
        for (let i = 0; i <= ticks; i++) {
            const x = left + i / ticks * (right - left);
            ctx.textAlign = i === 0 ? "left" : i === ticks ? "right" : "center";
            ctx.fillText((i / ticks * maxFrequency).toFixed(3), x, bottom + 16);
        }
        const labels = new Set([0, Math.floor((rows.length - 1) / 2), rows.length - 1]);
        ctx.textAlign = "right";
        for (const index of labels) {
            const row = rows[index];
            const label = kind === "file" ? compactOffset(row.offset) : Math.max(0, (latest - row.time) / 1000).toFixed(1);
            ctx.fillText(label, left - 6, Math.max(top + 8, Math.min(bottom, top + (index + .5) * rowHeight + 3)));
        }
        ctx.textAlign = "center"; ctx.fillText("Frequency (cycles/byte)", (left + right) / 2, height - 5, width - 8);
        return {rows: rows.length, latest, oldest};
    }

    globalThis.EntropyStructurePlots = Object.freeze({empty, correlation, transitions, blocks, spectrogram});
})();
