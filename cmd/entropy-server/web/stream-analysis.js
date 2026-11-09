"use strict";

(() => {
    const $ = id => document.getElementById(id), controls = $("stream-controls");
    let ws, receivedFrames = 0, receivedBytes = 0, analyzedFrames = 0, analyzedBytes = 0;
    let lastAccepted = -Infinity, statsScheduled = false, fatal = false, closed = false;
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
            for (const [id, value] of [["received-bytes", receivedBytes], ["received-frames", receivedFrames],
                ["analysed-bytes", analyzedBytes], ["analysed-frames", analyzedFrames]]) $(id).textContent = value.toLocaleString();
        });
    }
    function setPaused(value) {
        workspace.setFrozen(value);
        $("pause-plots").textContent = value ? "Resume plots" : "Pause plots";
        $("pause-plots").setAttribute("aria-pressed", String(value));
        $("analysis-size").disabled = $("analysis-offset").disabled = value;
        $("capture-state").textContent = value ? "Paused · plots frozen; received counters continue. Pan, inspect or change FFT settings." :
            "Live · receiving continues while plots are paused. Hidden tabs skip analysis.";
        lastAccepted = -Infinity;
    }
    let workspace;
    try {
        workspace = window.EntropyWorkspace({kind: "stream",
            describe: (data, frozen) => `${frozen ? "FROZEN" : "LATEST"} FRAME #${data.frame.toLocaleString()} · ${data.totalBytes.toLocaleString()} bytes received · [${data.offset.toLocaleString()}, ${(data.offset + data.analyzedBytes).toLocaleString()}) analysed · ${new Date(data.time).toLocaleTimeString()} · worker ${data.analysisMs.toFixed(1)} ms`,
            onAnalyzed(data) { analyzedFrames++; analyzedBytes += data.analyzedBytes; statistics(); },
            onRender() { if (!fatal && !closed) $("stream-notice").hidden = true; },
            onError(message) { fatal = true; showNotice("error", message); ws?.close(1002, "analysis failed"); },
            onSelectionChange() { workspace.clear("Waiting for the next frame at the new shared window."); lastAccepted = -Infinity; }
        });
        $("pause-plots").addEventListener("click", () => setPaused(!workspace.frozen));
        document.addEventListener("visibilitychange", () => {
            if (document.hidden) workspace.invalidate(); else { lastAccepted = -Infinity; workspace.redraw(); }
        });
        const args = new URLSearchParams(location.search);
        const quantity = parseOption(args, "bytes", 65536, 32 * 1024 * 1024), refresh = parseOption(args, "refresh", 500, 60000);
        controls.elements.namedItem("bytes").value = quantity; controls.elements.namedItem("refresh").value = refresh;
        const endpoint = new URL("/stream", location.href);
        endpoint.protocol = location.protocol === "https:" ? "wss:" : "ws:";
        endpoint.searchParams.set("bytes", quantity); endpoint.searchParams.set("refresh", refresh);
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
            if (workspace.frozen || document.hidden || now - lastAccepted < 1000 / Number($("display-fps").value)) return;
            const offset = Number($("analysis-offset").value);
            if (!Number.isSafeInteger(offset) || offset < 0 || offset >= event.data.byteLength) {
                showNotice("waiting", "START BYTE MUST BE WITHIN THE RECEIVED FRAME"); return;
            }
            lastAccepted = now;
            workspace.submit(event.data.slice(offset, Math.min(event.data.byteLength, offset + Number($("analysis-size").value))),
                {totalBytes: event.data.byteLength, offset, frame: receivedFrames, time: Date.now(), captureTime: now});
        };
    } catch (error) { fatal = true; showNotice("error", error.message); }
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
    window.addEventListener("pagehide", () => { ws?.close(); workspace?.destroy(); });
})();
