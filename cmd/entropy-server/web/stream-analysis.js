"use strict";

(() => {
    const limit = 64 * 1024;
    const minDisplayGap = 200; // Maximum five accepted analysis frames per second.
    const notice = document.getElementById("stream-notice");
    const info = document.getElementById("sample-info");
    const controls = document.getElementById("stream-controls");
    const colors = { zeros: "#145b41", ones: "#a7ffcd", green: "#42f5a2", cyan: "#7ee6cf" };
    let ws = null;
    let worker = null;
    let busy = false;
    let queued = null;
    let pending = null;
    let redrawScheduled = false;
    let haveSample = false;
    let receivedFrames = 0;
    let receivedBytes = 0;
    let analyzedFrames = 0;
    let analyzedBytes = 0;
    let statsScheduled = false;

    function updateStatistics() {
        document.getElementById("received-bytes").textContent = receivedBytes.toLocaleString();
        document.getElementById("received-frames").textContent = receivedFrames.toLocaleString();
        document.getElementById("analysed-bytes").textContent = analyzedBytes.toLocaleString();
        document.getElementById("analysed-frames").textContent = analyzedFrames.toLocaleString();
    }

    // Count every valid received frame, but avoid repainting the counters on
    // every message of a high-throughput WebSocket stream.
    function scheduleStatistics() {
        if (statsScheduled) return;
        statsScheduled = true;
        requestAnimationFrame(() => {
            statsScheduled = false;
            updateStatistics();
        });
    }
    let lastAccepted = -Infinity;
    let fatalError = false;
    let streamClosed = false;

    function showNotice(state, text) {
        notice.hidden = false;
        notice.dataset.state = state;
        notice.textContent = text;
    }

    function getContext(id) {
        const canvas = document.getElementById(id);
        const context = canvas.getContext("2d");
        context.clearRect(0, 0, canvas.width, canvas.height);
        return [canvas, context];
    }

    function histogramPlot(values) {
        const [canvas, ctx] = getContext("histogram");
        const peak = Math.max(1, ...values);
        const width = canvas.width / 256;
        ctx.fillStyle = colors.cyan;
        values.forEach((count, i) => {
            const height = count / peak * canvas.height;
            ctx.fillRect(i * width, canvas.height - height, Math.max(.8, width - .7), height);
        });
    }

    function mosaicPlot(bytes) {
        const [canvas, ctx] = getContext("mosaic");
        const tile = 16;
        const columns = Math.floor(canvas.width / tile);
        const tiles = Math.min(Math.floor(bytes.length / 3), columns * Math.floor(canvas.height / tile));
        for (let i = 0; i < tiles; i++) {
            ctx.fillStyle = `rgb(${bytes[i * 3]},${bytes[i * 3 + 1]},${bytes[i * 3 + 2]})`;
            ctx.fillRect((i % columns) * tile, Math.floor(i / columns) * tile, tile, tile);
        }
    }

    function balancePlot(zeros, ones) {
        const [canvas, ctx] = getContext("pie");
        const total = zeros + ones;
        if (!total) return;
        const cut = 2 * Math.PI * zeros / total;
        const x = canvas.width / 2;
        const y = 76;
        for (const [start, end, color] of [[0, cut, colors.zeros], [cut, 2 * Math.PI, colors.ones]]) {
            ctx.beginPath();
            ctx.moveTo(x, y);
            ctx.arc(x, y, 65, start, end);
            ctx.closePath();
            ctx.fillStyle = color;
            ctx.fill();
        }
        ctx.textAlign = "left";
        ctx.textBaseline = "middle";
        ctx.font = "12px monospace";
        const entries = [
            { color: colors.zeros, label: `ZEROS: ${zeros.toLocaleString()} (${(100 * zeros / total).toFixed(2)}%)` },
            { color: colors.ones, label: `ONES:  ${ones.toLocaleString()} (${(100 * ones / total).toFixed(2)}%)` }
        ];
        entries.forEach(({color, label}, i) => {
            const yy = 162 + i * 25;
            const textWidth = ctx.measureText(label).width;
            const legendWidth = 14 + 8 + textWidth;
            const startX = Math.max(0, (canvas.width - legendWidth) / 2);
            ctx.fillStyle = color;
            ctx.fillRect(startX, yy - 7, 14, 14);
            ctx.strokeStyle = colors.cyan;
            ctx.strokeRect(startX, yy - 7, 14, 14);
            ctx.fillStyle = colors.ones;
            ctx.fillText(label, startX + 22, yy);
        });
    }

    function wavePlot(bytes) {
        const [canvas, ctx] = getContext("wave");
        if (!bytes.length) return;
        ctx.strokeStyle = colors.green;
        ctx.lineWidth = 1.5;
        ctx.beginPath();
        bytes.forEach((value, i) => {
            const x = i / Math.max(1, bytes.length - 1) * canvas.width;
            const y = (1 - value / 255) * canvas.height;
            if (!i) ctx.moveTo(x, y);
            else ctx.lineTo(x, y);
        });
        ctx.stroke();
    }

    function fftPlot(bars) {
        const [canvas, ctx] = getContext("fft");
        document.getElementById("fftvalue").textContent =
            bars === null ? "· REQUIRES 2048 BYTES" : "· FIRST 2048 BYTES / 128 BANDS";
        if (bars === null) return;
        const max = Math.max(1, ...bars);
        const width = canvas.width / bars.length;
        ctx.fillStyle = colors.green;
        bars.forEach((value, i) => {
            const height = (value / max) * canvas.height;
            ctx.fillRect(i * width, canvas.height - height, Math.max(1, width - 1), height);
        });
    }

    function render(data) {
        haveSample = true;
        if (!fatalError && !streamClosed) notice.hidden = true;
        analyzedFrames++;
        analyzedBytes += data.analyzedBytes;
        scheduleStatistics();
        window.EntropyShannonAlert.update(data.entropy);
        info.textContent = `LATEST SAMPLE · ${data.totalBytes.toLocaleString()} BYTES DELIVERED · ${data.analyzedBytes.toLocaleString()}-BYTE PREFIX ANALYSED`;
        histogramPlot(data.histogram);
        mosaicPlot(data.mosaic);
        balancePlot(data.zeros, data.ones);
        wavePlot(data.wave);
        fftPlot(data.fft);
    }

    function pump() {
        if (busy || queued === null || worker === null) return;
        busy = true;
        const next = queued;
        queued = null;
        worker.postMessage({type: "analyze", ...next}, [next.buffer]);
    }

    function scheduleFrame(data) {
        pending = data;
        if (redrawScheduled) return;
        redrawScheduled = true;
        requestAnimationFrame(() => {
            redrawScheduled = false;
            if (pending !== null) render(pending);
            pending = null;
        });
    }

    function parseOption(params, key, defaultValue, max) {
        const all = params.getAll(key);
        if (!all.length) return defaultValue;
        if (all.length !== 1 || !/^\d+$/.test(all[0])) throw new Error(`INVALID ${key.toUpperCase()} PARAMETER`);
        const count = Number(all[0]);
        if (!Number.isSafeInteger(count) || count < 1 || count > max) {
            throw new Error(`${key.toUpperCase()} MUST BE 1–${max}`);
        }
        return count;
    }

    controls.addEventListener("submit", event => {
        event.preventDefault();
        if (!controls.reportValidity()) return;
        const args = new URLSearchParams(new FormData(controls));
        location.search = args.toString();
    });

    try {
        const args = new URLSearchParams(location.search);
        const quantity = parseOption(args, "bytes", 4096, 32 * 1024 * 1024);
        const refresh = parseOption(args, "refresh", 500, 60000);
        controls.elements.namedItem("bytes").value = quantity;
        controls.elements.namedItem("refresh").value = refresh;

        const endpoint = new URL("/stream", location.href);
        endpoint.protocol = location.protocol === "https:" ? "wss:" : "ws:";
        endpoint.searchParams.set("bytes", quantity);
        endpoint.searchParams.set("refresh", refresh);
        worker = new Worker("/worker.js");
        worker.onerror = () => { fatalError = true; showNotice("error", "ANALYSIS WORKER FAILED"); };
        worker.onmessage = ({data}) => {
            busy = false;
            if (data?.type === "error") {
                fatalError = true;
                showNotice("error", data.message);
                ws?.close(1002, "analysis failed");
                return;
            }
            if (data?.type === "analysis") scheduleFrame(data);
            pump();
        };
        ws = new WebSocket(endpoint.href);
        ws.binaryType = "arraybuffer";
        ws.onopen = () => showNotice("waiting", "AWAITING FIRST SAMPLE");
        ws.onerror = () => { fatalError = true; showNotice("error", "STREAM ERROR — CHECK SERVER AND REQUEST LIMITS"); };
        ws.onclose = () => {
            streamClosed = true;
            if (!fatalError) showNotice("disconnected", "STREAM CLOSED — RELOAD TO RETRY");
        };
        ws.onmessage = event => {
            if (!(event.data instanceof ArrayBuffer) || event.data.byteLength === 0) {
                fatalError = true;
                showNotice("error", "UNEXPECTED STREAM FRAME");
                ws.close(1002, "expected binary");
                return;
            }
            receivedFrames++;
            receivedBytes += event.data.byteLength;
            scheduleStatistics();
            const now = performance.now();
            if (now - lastAccepted < minDisplayGap && haveSample) return;
            lastAccepted = now;
            // Copy at most 64 KiB, regardless of the complete frame size.
            queued = { buffer: event.data.slice(0, limit), totalBytes: event.data.byteLength };
            pump();
        };
    } catch (error) {
        fatalError = true;
        showNotice("error", error.message);
    }
    window.addEventListener("pagehide", () => { ws?.close(); worker?.terminate(); });
})();
