"use strict";

(() => {
    const tr = (message, params = {}) => globalThis.window?.EntropyI18n?.t(message, params) ??
        message.replace(/\{(\w+)\}/g, (_, key) => Object.hasOwn(params, key) ? String(params[key]) : `{${key}}`);
    let color;
    function updatePalette() {
        const style = getComputedStyle(document.documentElement);
        const value = name => (style.getPropertyValue(name) || "").trim();
        color = {grid: value("--border"), text: value("--muted"), green: value("--plot-primary") || value("--green"),
            cyan: value("--plot-secondary") || value("--cyan"), warn: value("--plot-reference") || value("--warn"),
            zero: value("--plot-zero"), one: value("--plot-one"), outline: value("--plot-outline") || value("--text")};
    }
    updatePalette();
    window.addEventListener("entropythemechange", updatePalette);
    window.addEventListener("entropygraphpalettechange", updatePalette);
    function prepare(canvas) {
        const rect = canvas.getBoundingClientRect();
        const width = Math.max(1, rect.width), height = Math.max(1, rect.height);
        const ratio = Math.min(3, globalThis.devicePixelRatio || 1);
        const w = Math.round(width * ratio), h = Math.round(height * ratio);
        if (canvas.width !== w || canvas.height !== h) { canvas.width = w; canvas.height = h; }
        const ctx = canvas.getContext("2d");
        ctx.setTransform(ratio, 0, 0, ratio, 0, 0);
        ctx.clearRect(0, 0, width, height);
        ctx.font = "11px monospace";
        return {ctx, width, height, left: 53, top: 23, right: width - 14, bottom: height - 43};
    }
    function axes(canvas, {xmin, xmax, ymin, ymax, xlabel, ylabel, xformat = n => String(Math.round(n)), yformat = n => String(Math.round(n))}, p = prepare(canvas)) {
        const {ctx, left, top, right, bottom, width, height} = p;
        p.x = value => left + (value - xmin) / (xmax - xmin || 1) * (right - left);
        p.y = value => bottom - (value - ymin) / (ymax - ymin || 1) * (bottom - top);
        ctx.fillStyle = color.text; ctx.strokeStyle = color.grid; ctx.lineWidth = 1;
        ctx.textAlign = "left"; ctx.fillText(tr(ylabel), 2, 12, width - 4);
        const ticks = width < 400 ? 3 : 4;
        for (let i = 0; i <= ticks; i++) {
            const xvalue = xmin + i / ticks * (xmax - xmin), px = p.x(xvalue);
            const yvalue = ymin + i / ticks * (ymax - ymin), py = p.y(yvalue);
            ctx.beginPath(); ctx.moveTo(left, py); ctx.lineTo(right, py); ctx.stroke();
            ctx.textAlign = "right"; ctx.fillText(yformat(yvalue), left - 7, py + 4);
            ctx.textAlign = i === 0 ? "left" : i === ticks ? "right" : "center";
            ctx.fillText(xformat(xvalue), px, bottom + 16);
        }
        ctx.textAlign = "center"; ctx.fillText(tr(xlabel), (left + right) / 2, height - 5, width - 8);
        return p;
    }
    function empty(canvas, message) {
        const {ctx, width, height} = prepare(canvas);
        ctx.fillStyle = color.text; ctx.textAlign = "center";
        ctx.fillText(tr(message), width / 2, height / 2, Math.max(1, width - 12));
    }
    function histogram(canvas, data, {unit = "percent", scale = "detail"} = {}) {
        const total = data.analyzedBytes;
        const percent = unit === "percent", factor = percent ? 100 / total : 1;
        const maximum = scale === "full" ? total : scale === "detail" ? total / 100 : Math.max(...data.histogram, total / 256) * 1.1;
        const p = axes(canvas, {xmin: 0, xmax: 255, ymin: 0, ymax: maximum * factor,
            xlabel: "Byte value (0–255)", ylabel: percent ? "Frequency (%)" : "Count",
            yformat: value => percent ? value.toFixed(value < 2 ? 2 : 0) : String(Math.round(value))});
        const {ctx, left, right, bottom, top} = p;
        const barWidth = (right - left) / 256;
        ctx.fillStyle = color.cyan;
        let clipped = 0;
        for (let i = 0; i < 256; i++) {
            const value = data.histogram[i];
            const y = p.y(Math.min(value, maximum) * factor);
            ctx.fillRect(left + i * barWidth, y, Math.max(.4, barWidth - .15), bottom - y);
            if (value > maximum) { clipped++; ctx.fillStyle = color.warn; ctx.fillRect(left + i * barWidth, top, Math.max(1, barWidth), 4); ctx.fillStyle = color.cyan; }
        }
        ctx.strokeStyle = color.warn; ctx.setLineDash([4, 3]);
        ctx.beginPath(); ctx.moveTo(left, p.y(total / 256 * factor)); ctx.lineTo(right, p.y(total / 256 * factor)); ctx.stroke(); ctx.setLineDash([]);
        return clipped;
    }
    function trace(canvas, bytes, {start = 0, span = 1024, offset = 0, style = "line", source = "stream"} = {}) {
        start = Math.max(0, Math.min(start, bytes.length - 1));
        const end = Math.min(bytes.length, start + span), sample = bytes.subarray(start, end);
        const p = axes(canvas, {xmin: offset + start, xmax: offset + Math.max(start + 1, end - 1),
            ymin: 0, ymax: 255, xlabel: source === "file" ? "Byte offset in file" : "Byte offset in frame", ylabel: "Byte value"});
        const {ctx, left, right} = p;
        ctx.strokeStyle = color.warn; ctx.setLineDash([3, 4]);
        ctx.beginPath(); ctx.moveTo(left, p.y(127.5)); ctx.lineTo(right, p.y(127.5)); ctx.stroke(); ctx.setLineDash([]);
        ctx.strokeStyle = color.green; ctx.fillStyle = color.green; ctx.lineWidth = 1.2;
        if (sample.length > right - left) {
            const columns = EntropyAnalysis.envelope(sample, right - left);
            ctx.beginPath();
            columns.forEach((column, i) => {
                const x = left + (i + .5) / columns.length * (right - left);
                ctx.moveTo(x, p.y(column.min)); ctx.lineTo(x, p.y(column.max) - .5);
            });
            ctx.stroke();
        } else {
            ctx.beginPath();
            sample.forEach((value, i) => {
                const x = p.x(offset + start + i), y = p.y(value);
                if (style === "dots") ctx.fillRect(x - 1.6, y - 1.6, 3.2, 3.2);
                else if (i === 0) ctx.moveTo(x, y);
                else { if (style === "steps") ctx.lineTo(x, p.y(sample[i - 1])); ctx.lineTo(x, y); }
            });
            if (sample.length === 1) ctx.fillRect(p.x(offset + start) - 2, p.y(sample[0]) - 2, 4, 4);
            ctx.stroke();
        }
        return {...p, start, end, envelope: sample.length > right - left};
    }
    function spectrum(canvas, fft, {min = 0, max = .5, floor = -60, ceiling = 50} = {}) {
        if (!fft) { empty(canvas, "Not enough bytes for this FFT size"); return; }
        const p = axes(canvas, {xmin: min, xmax: max, ymin: floor, ymax: ceiling,
            xlabel: "Frequency (cycles/byte)", ylabel: "PSD (dB re FS²/(cycles/byte))", xformat: n => n.toFixed(3)});
        const {ctx, left, right, top, bottom} = p;
        const first = Math.ceil(min * fft.size), last = Math.floor(max * fft.size);
        if (first > last) {
            ctx.fillStyle = color.text; ctx.textAlign = "center";
            ctx.fillText(tr("No FFT bin in this range"), (left + right) / 2, (top + bottom) / 2, Math.max(1, right - left));
            return;
        }
        const columns = Math.max(1, Math.min(last - first + 1, Math.floor(right - left)));
        ctx.save(); ctx.beginPath(); ctx.rect(left, top, right - left, bottom - top); ctx.clip();
        ctx.strokeStyle = color.green; ctx.lineWidth = 1.2; ctx.beginPath();
        for (let column = 0; column < columns; column++) {
            const start = first + Math.floor(column * (last - first + 1) / columns);
            const end = first + Math.floor((column + 1) * (last - first + 1) / columns);
            let peak = 0;
            for (let k = start; k < end; k++) peak = Math.max(peak, fft.power[k]);
            const x = columns === last - first + 1 ? p.x(start / fft.size) : left + (column + .5) / columns * (right - left);
            const y = p.y(Math.max(floor, 10 * Math.log10(Math.max(peak, 1e-30))));
            if (column === 0) ctx.moveTo(x, y); else ctx.lineTo(x, y);
            if (columns === 1) { ctx.moveTo(x, bottom); ctx.lineTo(x, y); }
        }
        ctx.stroke(); ctx.restore();
    }
    function balance(canvas, zeros, ones) {
        const {ctx, width, height} = prepare(canvas), total = zeros + ones;
        if (!total) return;
        const x = width / 2, y = height / 2, radius = Math.min(width, height) / 2 - 8;
        const cut = 2 * Math.PI * zeros / total;
        const contrast = document.documentElement.dataset.graphPalette === "contrast";
        for (const [index, start, end, fill] of [[0, 0, cut, color.zero], [1, cut, 2 * Math.PI, color.one]]) {
            ctx.beginPath(); ctx.moveTo(x, y); ctx.arc(x, y, radius, start, end); ctx.closePath(); ctx.fillStyle = fill; ctx.fill();
            if (contrast) {
                // A hatched one-bit wedge remains distinguishable without hue.
                if (index === 1) {
                    ctx.save(); ctx.clip(); ctx.strokeStyle = color.outline; ctx.lineWidth = 1;
                    ctx.beginPath();
                    for (let line = -height; line < width + height; line += 8) { ctx.moveTo(line, 0); ctx.lineTo(line + height, height); }
                    ctx.stroke(); ctx.restore();
                }
                ctx.beginPath(); ctx.moveTo(x, y); ctx.arc(x, y, radius, start, end); ctx.closePath();
                ctx.strokeStyle = color.outline; ctx.lineWidth = 1.5; ctx.stroke();
            }
        }
    }
    const scratch = document.createElement("canvas");
    // Optional display palette, independent of byte statistics and raw RGB mode.
    const vivid = Array.from({length: 256}, (_, byte) => {
        const hue = byte * 6 / 256, chroma = .95 * .85, base = .95 - chroma;
        const secondary = chroma * (1 - Math.abs(hue % 2 - 1));
        const channels = [[chroma, secondary, 0], [secondary, chroma, 0], [0, chroma, secondary],
            [0, secondary, chroma], [secondary, 0, chroma], [chroma, 0, secondary]][Math.floor(hue)];
        return channels.map(value => Math.round((value + base) * 255));
    });
    function waterfall(canvas, rows, {mode = "rgb", bit = 7, rowBytes = "auto", historyRows = 16, offset = 0, source = "stream"} = {}) {
        if (!rows.length) { empty(canvas, "Awaiting history"); return; }
        const p = prepare(canvas);
        const bytesPerCell = mode === "rgb" ? 3 : 1;
        if (rowBytes === "auto") {
            rowBytes = Math.min(Math.floor(1024 / bytesPerCell), Math.max(1, Math.floor((p.right - p.left) / 12))) * bytesPerCell;
        }
        // Keep the full history in the caller; only the visible subset is enlarged.
        rows = rows.slice(0, historyRows);
        const latest = rows[0].time, oldest = rows[rows.length - 1].time;
        function rowLabel(row) {
            if (source !== "file") return ((latest - row.time) / 1000).toFixed(1) + "s";
            const value = row.offset;
            if (value < 10000) return String(value);
            const unit = value >= 1048576 ? 1048576 : 1024, suffix = unit === 1024 ? "Ki" : "Mi";
            return `${value % unit ? "~" : ""}${(value / unit).toFixed(unit === 1024 ? 0 : 1)}${suffix}`;
        }
        const xoffset = source === "file" ? 0 : offset;
        axes(canvas, {xmin: xoffset, xmax: xoffset + rowBytes - 1, ymin: 0, ymax: Math.max(1, rows.length),
            xlabel: source === "file" ? "Byte offset within window" : "Byte offset in frame", ylabel: source === "file" ? "Window start (bytes)" : "History · newest at top", yformat: n => {
                const row = rows[Math.min(rows.length - 1, Math.max(0, rows.length - 1 - Math.round(n)))];
                return rowLabel(row);
            }}, p);
        const columns = mode === "rgb" ? Math.ceil(rowBytes / 3) : rowBytes;
        scratch.width = columns; scratch.height = rows.length;
        const work = scratch.getContext("2d"), image = work.createImageData(columns, rows.length);
        rows.forEach((row, y) => {
            const count = Math.min(rowBytes, row.bytes.length);
            for (let x = 0; x < columns; x++) {
                const i = (y * columns + x) * 4, index = mode === "rgb" ? x * 3 : x;
                if (index >= count || (mode === "rgb" && index + 2 >= count)) continue;
                if (mode === "rgb") {
                    image.data[i] = row.bytes[index]; image.data[i + 1] = row.bytes[index + 1]; image.data[i + 2] = row.bytes[index + 2];
                } else if (mode === "vivid") {
                    const channels = vivid[row.bytes[index]];
                    image.data[i] = channels[0]; image.data[i + 1] = channels[1]; image.data[i + 2] = channels[2];
                } else {
                    const value = mode === "bit" ? ((row.bytes[index] >>> bit) & 1) * 255 : row.bytes[index];
                    image.data[i] = image.data[i + 1] = image.data[i + 2] = value;
                }
                image.data[i + 3] = 255;
            }
        });
        work.putImageData(image, 0, 0);
        p.ctx.imageSmoothingEnabled = false;
        const visibleHeight = p.bottom - p.top;
        p.ctx.drawImage(scratch, p.left, p.top, p.right - p.left, visibleHeight);
        // Age ticks correspond to actual rows, including gaps, rather than a nominal refresh clock.
        p.ctx.clearRect(0, p.top - 6, p.left - 3, p.bottom - p.top + 16);
        p.ctx.fillStyle = color.text; p.ctx.textAlign = "right";
        p.ctx.fillText(rowLabel(rows[0]), p.left - 6, p.top + 9);
        if (rows.length > 1) p.ctx.fillText(rowLabel(rows[rows.length - 1]), p.left - 6, p.bottom);
        if (rows.length > 3) {
            const middle = Math.floor(rows.length / 2);
            p.ctx.fillText(rowLabel(rows[middle]), p.left - 6,
                p.top + (middle + .5) / rows.length * visibleHeight + 4);
        }
        return {oldest, latest, shownRows: rows.length, rowBytes, columns,
            cellWidth: (p.right - p.left) / columns, cellHeight: visibleHeight / rows.length};
    }
    globalThis.EntropyPlots = Object.freeze({empty, histogram, trace, spectrum, balance, waterfall});
})();
