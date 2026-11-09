"use strict";

// Shared controls, worker pipeline and rendering for live and file sources.
(() => {
    window.EntropyWorkspace = function createWorkspace({kind, describe, onAnalyzed = () => {},
        onRender = () => {}, onError, onSelectionChange, onSampleNeeded = () => false}) {
        const $ = id => document.getElementById(id);
        const analysis = window.EntropyAnalysis, plots = window.EntropyPlots;
        const history = new analysis.WaterfallHistory(), average = new analysis.PowerAverage();
        const worker = new Worker("/worker.js");
        let busy = false, queued = null, pending = null, scheduled = false;
        let epoch = 0, paused = kind === "file", last = null, displayFFT = null, traceGeometry = null;
        let markers = [], cursor = 0;
        let fftScale = {min: 0, max: .5, floor: -60, ceiling: 50};
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
                if (pending && pending.epoch === epoch && (kind === "file" || !paused || pending.reanalysis)) {
                    if (kind === "file" && last?.offset !== pending.offset) clearMarkers();
                    last = pending;
                    const bytes = new Uint8Array(last.buffer);
                    if (!last.reanalysis) history.add(bytes, last.captureTime, last.frame, last.offset);
                    if (last.fft) {
                        if (last.reanalysis) {
                            average.reset();
                            displayFFT = {...last.fft, averages: 1};
                        } else {
                            const key = `${kind === "file" ? "file" : last.offset}:${last.analyzedBytes}:${JSON.stringify(last.fftOptions)}`;
                            displayFFT = average.add(last.fft, Number($("fft-average").value), key);
                        }
                    } else { average.reset(); displayFFT = null; }
                }
                const rendered = pending?.epoch === epoch ? pending : null;
                pending = null;
                draw();
                if (rendered) onRender(rendered);
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
            return {span, start: Number($("trace-start").value), offset: last?.offset || 0, style: $("trace-style").value, source: kind};
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
                $("waterfall-info").textContent = `0 / 240 ${kind === "file" ? "file windows" : "selected frames"}`;
                $("bit-rates").dataset.state = "waiting";
                for (let bit = 0; bit < 8; bit++) $("rate-" + bit).textContent = "—";
                for (const id of ["histogram", "wave", "fft", "mosaic", "pie"]) plots.empty($(id), "Awaiting selected bytes");
                return;
            }
            const bytes = new Uint8Array(last.buffer);
            $("bit-rates").dataset.state = "ready";
            $("sample-info").textContent = describe(last, paused);
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
                `${displayFFT.size.toLocaleString()} points · Δf ${displayFFT.binWidth.toPrecision(4)} cycles/byte · ${displayFFT.window} · ${displayFFT.averages} ${kind === "file" ? "window(s)" : "frame(s)"} · input mean ${displayFFT.mean.toFixed(3)}` :
                `Needs ${$("fft-size").value === "auto" ? "at least 32" : Number($("fft-size").value).toLocaleString()} bytes in the selected window; available ${bytes.length.toLocaleString()}.`;
            const mode = $("waterfall-mode").value;
            const requestedBytes = $("waterfall-bytes").value;
            const view = plots.waterfall($("mosaic"), history.rows, {mode, bit: Number($("waterfall-bit").value),
                rowBytes: requestedBytes === "auto" ? "auto" : Number(requestedBytes),
                historyRows: Number($("waterfall-rows").value), offset: last.offset, source: kind});
            $("waterfall-info").textContent = `${history.rows.length} / ${history.capacity} ${kind === "file" ? "file windows" : "selected frames"} · showing latest ${view?.shownRows || 0} · ${view?.rowBytes || 0} bytes/row · newest at top · ${mode === "rgb" ? "RGB triplets" : mode === "vivid" ? "hue 0–255: red → green → blue → red" : mode === "gray" ? "0 = black, 255 = white" : "bit " + $("waterfall-bit").value + ": 0 = black, 1 = white"}`;
        }
        function invalidate(resetAverage = true) { epoch++; queued = null; pending = null; if (resetAverage) average.reset(); }
        function reanalyze() {
            invalidate();
            if (onSampleNeeded(last) || !last) return;
            queued = {buffer: last.buffer.slice(0), totalBytes: last.totalBytes, offset: last.offset,
                frame: last.frame, time: last.time, captureTime: last.captureTime, sourceId: last.sourceId, sourceName: last.sourceName, representation: last.representation, encoding: last.encoding, epoch, reanalysis: true, fftOptions: fftOptions()};
            pump();
        }
        function clearMarkers() { markers = []; drawMarkers(); }
        for (let bit = 7; bit >= 0; bit--) {
            const row = document.createElement("div"); row.className = "bit-rate-row";
            // Only constant labels/IDs enter this markup; stream bytes never become HTML.
            row.innerHTML = `<label for="bit-${bit}">Bit ${bit}</label><span class="bit-meter"><meter id="bit-${bit}" min="0" max="100" value="50">50%</meter></span><output id="rate-${bit}">—</output>`;
            $("bit-rate-grid").append(row);
        }
        $("clear-markers").addEventListener("click", clearMarkers);
        $("clear-history").addEventListener("click", () => { history.clear(); redraw(); });
        for (const id of ["analysis-size", "analysis-offset"]) $(id).addEventListener("change", () => {
            if ($(id).reportValidity()) onSelectionChange();
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
        const observer = new ResizeObserver(redraw);
        observer.observe(document.querySelector(".analysis-panel"));
        worker.onerror = () => { busy = false; onError("ANALYSIS WORKER FAILED"); };
        worker.onmessage = ({data}) => {
            busy = false;
            if (data?.type === "error") { onError(data.message); return; }
            if (data?.type === "analysis") {
                if (!data.reanalysis) { onAnalyzed(data); }
                if (data.epoch === epoch && (kind === "file" || !paused || data.reanalysis) && (kind === "file" || !document.hidden)) { pending = data; redraw(); }
            }
            pump();
        };
        function clear(message = "Awaiting selected bytes.") {
            invalidate(); history.clear(); last = null; displayFFT = null; traceGeometry = null;
            clearMarkers();
            $("sample-info").textContent = message;
            redraw();
        }
        const api = {
            submit(buffer, metadata) {
                queued = {...metadata, buffer, epoch, reanalysis: false, fftOptions: fftOptions()};
                pump();
            },
            clear, invalidate, redraw,
            cancelPending() { invalidate(false); },
            setFrozen(value) { paused = value; invalidate(); clearMarkers(); redraw(); },
            get frozen() { return paused; },
            get sample() { return last; },
            destroy() { worker.terminate(); observer.disconnect(); }
        };
        redraw();
        return api;
    };
})();
