"use strict";

(() => {
    const $ = id => document.getElementById(id);
    const analysis = window.EntropyAnalysis, plots = window.EntropyPlots;
    const history = new analysis.WaterfallHistory(), average = new analysis.PowerAverage();
    const controls = $("stream-controls");
    let ws, worker, busy = false, queued = null, pending = null, scheduled = false;
    let epoch = 0, paused = false, last = null, displayFFT = null, traceGeometry = null;
    let receivedFrames = 0, receivedBytes = 0, analyzedFrames = 0, analyzedBytes = 0;
    let lastAccepted = -Infinity, statsScheduled = false, fatal = false, closed = false;
    let markers = [], cursor = 0;
    let fftScale = {min: 0, max: .5, floor: -60, ceiling: 50};

    function showNotice(state, text) {
        $("stream-notice").hidden = false;
        $("stream-notice").dataset.state = state;
        $("stream-notice").textContent = text;
    }
    function statistics() {
        if (statsScheduled) return;
        statsScheduled = true;
        requestAnimationFrame(() => {
            statsScheduled = false;
            $("received-bytes").textContent = receivedBytes.toLocaleString();
            $("received-frames").textContent = receivedFrames.toLocaleString();
            $("analysed-bytes").textContent = analyzedBytes.toLocaleString();
            $("analysed-frames").textContent = analyzedFrames.toLocaleString();
        });
    }
    function fftOptions() {
        return {size: $("fft-size").value === "auto" ? "auto" : Number($("fft-size").value),
            window: $("fft-window").value, removeMean: $("fft-demean").checked};
    }
    function pump() {
        if (busy || !queued || !worker) return;
        busy = true;
        const job = queued;
        queued = null;
        worker.postMessage({type: "analyze", ...job}, [job.buffer]);
    }
    function redraw() {
        if (scheduled) return;
        scheduled = true;
        requestAnimationFrame(() => {
            scheduled = false;
            if (pending && pending.epoch === epoch && (!paused || pending.reanalysis)) {
                last = pending;
                const bytes = new Uint8Array(last.buffer);
                if (!last.reanalysis) history.add(bytes, last.captureTime, last.frame);
                if (last.fft) {
                    if (last.reanalysis) {
                        average.reset();
                        displayFFT = {...last.fft, averages: 1};
                    } else {
                        const key = `${last.offset}:${last.analyzedBytes}:${JSON.stringify(last.fftOptions)}`;
                        displayFFT = average.add(last.fft, Number($("fft-average").value), key);
                    }
                } else { average.reset(); displayFFT = null; }
            }
            pending = null;
            draw();
        });
    }
    function traceSettings() {
        const length = last?.analyzedBytes || 1;
        const span = $("trace-span").value === "all" ? length : Math.min(length, Number($("trace-span").value));
        const maximum = Math.max(0, length - span);
        $("trace-start").max = maximum;
        $("trace-start").value = Math.min(Number($("trace-start").value), maximum);
        $("trace-start-value").textContent = Number($("trace-start").value).toLocaleString();
        $("trace-cursor").max = length - 1;
        cursor = Math.min(cursor, length - 1);
        return {span, start: Number($("trace-start").value), offset: last?.offset || 0, style: $("trace-style").value};
    }
    function readCursor() {
        if (!last) return;
        const bytes = new Uint8Array(last.buffer), value = bytes[cursor];
        $("trace-cursor").value = cursor;
        $("trace-reading").textContent = `Byte ${last.offset + cursor}: ${value} · 0x${value.toString(16).padStart(2, "0")} · ${value.toString(2).padStart(8, "0")}`;
    }
    function drawMarkers() {
        for (const [i, id] of ["marker-a", "marker-b"].entries()) {
            const marker = $(id), index = markers[i];
            marker.hidden = !traceGeometry || index === undefined || index < traceGeometry.start || index >= traceGeometry.end;
            if (!marker.hidden) marker.style.left = traceGeometry.x(last.offset + index) + "px";
        }
        $("marker-reading").textContent = markers.length === 2 && last ?
            `A → B: Δ offset ${markers[1] - markers[0]} bytes · Δ value ${new Uint8Array(last.buffer)[markers[1]] - new Uint8Array(last.buffer)[markers[0]]}` :
            markers.length === 1 ? "A set. Click a second byte for B." : "";
    }
    function draw() {
        const trace = traceSettings();
        if (!last) {
            window.EntropyShannonAlert.update(null);
            $("byte-statistics").textContent = "Mean / standard deviation / range: —";
            $("zero-count").textContent = "Zeros: —"; $("one-count").textContent = "Ones: —";
            $("trace-reading").textContent = "Byte: —";
            $("trace-info").textContent = "Awaiting selected bytes.";
            $("fftvalue").textContent = "Awaiting FFT input.";
            $("waterfall-info").textContent = "0 / 240 selected frames";
            $("bit-rates").dataset.state = "waiting";
            for (let bit = 0; bit < 8; bit++) $("rate-" + bit).textContent = "—";
            for (const id of ["histogram", "wave", "fft", "mosaic", "pie"]) plots.empty($(id), "Awaiting selected bytes");
            return;
        }
        const bytes = new Uint8Array(last.buffer);
        $("bit-rates").dataset.state = "ready";
        if (!fatal && !closed) $("stream-notice").hidden = true;
        $("sample-info").textContent = `${paused ? "FROZEN" : "LATEST"} FRAME #${last.frame.toLocaleString()} · ${last.totalBytes.toLocaleString()} bytes received · [${last.offset.toLocaleString()}, ${(last.offset + bytes.length).toLocaleString()}) analysed · ${new Date(last.time).toLocaleTimeString()} · worker ${last.analysisMs.toFixed(1)} ms`;
        window.EntropyShannonAlert.update(last.entropy);
        $("byte-statistics").textContent = `Mean ${last.mean.toFixed(3)} · σ ${last.stddev.toFixed(3)} · range ${last.min}–${last.max}`;
        plots.balance($("pie"), last.zeros, last.ones);
        $("zero-count").textContent = `Zeros: ${last.zeros.toLocaleString()} (${(last.zeros / (last.zeros + last.ones) * 100).toFixed(3)}%)`;
        $("one-count").textContent = `Ones: ${last.ones.toLocaleString()} (${(last.ones / (last.zeros + last.ones) * 100).toFixed(3)}%)`;
        for (let bit = 0; bit < 8; bit++) {
            const rate = last.bitOnes[bit] / bytes.length * 100;
            $("bit-" + bit).value = rate;
            $("rate-" + bit).textContent = rate.toFixed(3) + "%";
        }
        const clipped = plots.histogram($("histogram"), last, {unit: $("histogram-unit").value, scale: $("histogram-scale").value});
        $("histogram-info").textContent = `Uniform reference: 0.390625% (${(bytes.length / 256).toFixed(2)} counts). ${clipped ? clipped + " clipped bars." : "No clipped bars."}`;
        traceGeometry = plots.trace($("wave"), bytes, trace);
        $("trace-info").textContent = `Offsets ${last.offset + traceGeometry.start}–${last.offset + traceGeometry.end - 1} · ${traceGeometry.envelope ? "min/max envelope per display column" : trace.style + " · individual bytes"} · dashed centre: 127.5`;
        readCursor(); drawMarkers();
        plots.spectrum($("fft"), displayFFT, fftScale);
        $("fftvalue").textContent = displayFFT ?
            `${displayFFT.size.toLocaleString()} points · Δf ${displayFFT.binWidth.toPrecision(4)} cycles/byte · ${displayFFT.window} · ${displayFFT.averages} frame(s) · input mean ${displayFFT.mean.toFixed(3)}` :
            `Needs ${$("fft-size").value === "auto" ? "at least 32" : Number($("fft-size").value).toLocaleString()} bytes in the selected window; received ${bytes.length.toLocaleString()}.`;
        const mode = $("waterfall-mode").value;
        const requestedBytes = $("waterfall-bytes").value;
        const view = plots.waterfall($("mosaic"), history.rows, {mode, bit: Number($("waterfall-bit").value),
            rowBytes: requestedBytes === "auto" ? "auto" : Number(requestedBytes),
            historyRows: Number($("waterfall-rows").value), offset: last.offset});
        $("waterfall-info").textContent = `${history.rows.length} / ${history.capacity} selected frames · showing latest ${view?.shownRows || 0} · ${view?.rowBytes || 0} bytes/row · newest at top · ${mode === "rgb" ? "RGB triplets" : mode === "vivid" ? "hue 0–255: red → green → blue → red" : mode === "gray" ? "0 = black, 255 = white" : "bit " + $("waterfall-bit").value + ": 0 = black, 1 = white"}`;
    }
    function invalidate() { epoch++; queued = null; pending = null; average.reset(); }
    function reanalyze() {
        invalidate();
        if (!last) return;
        queued = {buffer: last.buffer.slice(0), totalBytes: last.totalBytes, offset: last.offset,
            frame: last.frame, time: last.time, captureTime: last.captureTime, epoch, reanalysis: true, fftOptions: fftOptions()};
        pump();
    }
    function clearMarkers() { markers = []; drawMarkers(); }
    function setPaused(value) {
        paused = value; invalidate(); clearMarkers();
        $("pause-plots").textContent = paused ? "Resume plots" : "Pause plots";
        $("pause-plots").setAttribute("aria-pressed", String(paused));
        $("analysis-size").disabled = $("analysis-offset").disabled = paused;
        $("capture-state").textContent = paused ? "Paused · plots frozen; received counters continue. Pan, inspect or change FFT settings." : "Live · receiving continues while plots are paused. Hidden tabs skip analysis.";
        lastAccepted = -Infinity;
        redraw();
    }
    for (let bit = 7; bit >= 0; bit--) {
        const row = document.createElement("div"); row.className = "bit-rate-row";
        // Only constant labels/IDs enter this markup; stream bytes never become HTML.
        row.innerHTML = `<label for="bit-${bit}">Bit ${bit}</label><span class="bit-meter"><meter id="bit-${bit}" min="0" max="100" value="50">50%</meter></span><output id="rate-${bit}">—</output>`;
        $("bit-rate-grid").append(row);
    }
    $("pause-plots").addEventListener("click", () => setPaused(!paused));
    $("clear-markers").addEventListener("click", clearMarkers);
    $("clear-history").addEventListener("click", () => { history.clear(); redraw(); });
    for (const id of ["analysis-size", "analysis-offset"]) $(id).addEventListener("change", () => {
        if (!$(id).reportValidity()) return;
        invalidate(); history.clear(); last = null; displayFFT = null; traceGeometry = null; clearMarkers();
        $("sample-info").textContent = "Waiting for the next frame at the new shared window.";
        lastAccepted = -Infinity; redraw();
    });
    for (const id of ["fft-size", "fft-window", "fft-demean"]) $(id).addEventListener("change", reanalyze);
    $("fft-average").addEventListener("change", () => { average.reset(); displayFFT = last?.fft ? {...last.fft, averages: 1} : null; redraw(); });
    for (const id of ["trace-span", "trace-style", "histogram-unit", "histogram-scale", "waterfall-mode", "waterfall-bytes", "waterfall-rows"]) $(id).addEventListener("change", redraw);
    $("waterfall-bit").addEventListener("change", () => {
        $("waterfall-mode").value = "bit";
        redraw();
    });
    $("trace-start").addEventListener("input", redraw);
    $("trace-cursor").addEventListener("input", () => { cursor = Number($("trace-cursor").value); readCursor(); });
    for (const id of ["fft-min", "fft-max", "fft-floor", "fft-ceiling"]) $(id).addEventListener("change", () => {
        const values = ["fft-min", "fft-max", "fft-floor", "fft-ceiling"].map(id => Number($(id).value));
        const [min, max, floor, ceiling] = values;
        const valid = values.every(Number.isFinite) && min >= 0 && max <= .5 && min < max && floor >= -160 && ceiling <= 100 && floor < ceiling;
        $("fft-scale-error").textContent = valid ? "" : "Use 0 ≤ min < max ≤ 0.5 and −160 ≤ floor < ceiling ≤ 100. Keeping the last valid scale.";
        if (valid) { fftScale = {min, max, floor, ceiling}; redraw(); }
    });
    function pointerIndex(event) {
        if (!traceGeometry || !last) return null;
        const {left, right, start, end} = traceGeometry;
        const x = event.clientX - $("wave").getBoundingClientRect().left;
        return Math.min(end - 1, Math.max(start, Math.round(start + (x - left) / (right - left) * (end - start - 1))));
    }
    $("wave").addEventListener("pointermove", event => { const index = pointerIndex(event); if (index !== null) { cursor = index; readCursor(); } });
    $("wave").addEventListener("click", event => {
        if (!paused) return;
        const index = pointerIndex(event); if (index === null) return;
        if (markers.length === 2) markers = [];
        markers.push(index); drawMarkers();
    });
    new ResizeObserver(redraw).observe($("stream-controls").parentElement);
    document.addEventListener("visibilitychange", () => {
        if (document.hidden) invalidate();
        else { lastAccepted = -Infinity; redraw(); }
    });
    function parseOption(params, key, fallback, max) {
        const all = params.getAll(key);
        if (!all.length) return fallback;
        const number = Number(all[0]);
        if (all.length !== 1 || !/^\d+$/.test(all[0]) || !Number.isSafeInteger(number) || number < 1 || number > max) throw new Error(`${key.toUpperCase()} MUST BE 1–${max}`);
        return number;
    }
    controls.addEventListener("submit", event => {
        event.preventDefault(); if (controls.reportValidity()) location.search = new URLSearchParams(new FormData(controls)).toString();
    });
    try {
        const args = new URLSearchParams(location.search);
        const quantity = parseOption(args, "bytes", 65536, 32 * 1024 * 1024), refresh = parseOption(args, "refresh", 500, 60000);
        controls.elements.namedItem("bytes").value = quantity; controls.elements.namedItem("refresh").value = refresh;
        const endpoint = new URL("/stream", location.href);
        endpoint.protocol = location.protocol === "https:" ? "wss:" : "ws:";
        endpoint.searchParams.set("bytes", quantity); endpoint.searchParams.set("refresh", refresh);
        worker = new Worker("/worker.js");
        worker.onerror = () => { fatal = true; showNotice("error", "ANALYSIS WORKER FAILED"); ws?.close(); };
        worker.onmessage = ({data}) => {
            busy = false;
            if (data?.type === "error") { fatal = true; showNotice("error", data.message); ws?.close(1002, "analysis failed"); return; }
            if (data?.type === "analysis") {
                if (!data.reanalysis) { analyzedFrames++; analyzedBytes += data.analyzedBytes; statistics(); }
                if (data.epoch === epoch && (!paused || data.reanalysis) && !document.hidden) { pending = data; redraw(); }
            }
            pump();
        };
        ws = new WebSocket(endpoint.href); ws.binaryType = "arraybuffer";
        ws.onopen = () => showNotice("waiting", "AWAITING FIRST SAMPLE");
        ws.onerror = () => { fatal = true; showNotice("error", "STREAM ERROR — CHECK SERVER AND REQUEST LIMITS"); };
        ws.onclose = () => { closed = true; if (!fatal) showNotice("disconnected", "STREAM CLOSED — RELOAD TO RETRY"); };
        ws.onmessage = event => {
            if (!(event.data instanceof ArrayBuffer) || !event.data.byteLength) {
                fatal = true; showNotice("error", "UNEXPECTED STREAM FRAME"); ws.close(1002, "expected binary"); return;
            }
            receivedFrames++; receivedBytes += event.data.byteLength; statistics();
            const now = performance.now();
            if (paused || document.hidden || now - lastAccepted < 1000 / Number($("display-fps").value)) return;
            const offset = Number($("analysis-offset").value);
            if (!Number.isSafeInteger(offset) || offset < 0 || offset >= event.data.byteLength) {
                showNotice("waiting", "START BYTE MUST BE WITHIN THE RECEIVED FRAME"); return;
            }
            lastAccepted = now;
            queued = {buffer: event.data.slice(offset, Math.min(event.data.byteLength, offset + Number($("analysis-size").value))),
                totalBytes: event.data.byteLength, offset, frame: receivedFrames, time: Date.now(), captureTime: now, epoch, reanalysis: false, fftOptions: fftOptions()};
            pump();
        };
    } catch (error) { fatal = true; showNotice("error", error.message); }
    window.addEventListener("pagehide", () => { ws?.close(); worker?.terminate(); });
    redraw();
})();
