"use strict";

(() => {
    const tr = (message, params = {}) => globalThis.window?.EntropyI18n?.t(message, params) ??
        message.replace(/\{(\w+)\}/g, (_, key) => Object.hasOwn(params, key) ? String(params[key]) : `{${key}}`);
    function text(element, message, params = {}) {
        if (globalThis.window?.EntropyI18n) window.EntropyI18n.set(element, message, params);
        else element.textContent = tr(message, params);
    }
    function uiError(message, params = {}) {
        return Object.assign(new Error(tr(message, params)), {uiMessage: message, uiParams: params});
    }
    const $ = id => document.getElementById(id);
    const form = $("uploadForm"), input = $("fileInput"), submit = $("submit-upload"), cancel = $("cancel-upload");
    let controller = null, file = null, sourceId = 0, seekVersion = 0, windowNumber = 0;
    let readBusy = false, queuedRead = null, playing = false, timer = null;
    function setStatus(state, message, params = {}) {
        $("upload-status").dataset.state = state;
        text($("upload-status"), message, params);
    }
    const workspace = window.EntropyWorkspace({kind: "file",
        describe: data => tr("FILE WINDOW #{frame} · [{start}, {end}) of {total} bytes · worker {duration} ms", {
            frame: data.frame.toLocaleString(), start: data.offset.toLocaleString(), end: (data.offset + data.analyzedBytes).toLocaleString(),
            total: data.totalBytes.toLocaleString(), duration: data.analysisMs.toFixed(1)}),
        onRender(data) {
            if (data.sourceId !== sourceId) return;
            if (document.activeElement !== $("analysis-offset")) $("analysis-offset").value = data.offset;
            updateControls();
            setStatus("ready", playing ? "Playing file windows" : "Analysis complete");
            if (playing) scheduleNext(data);
        },
        onError(message) { stopPlayback(); setStatus("error", message === "ANALYSIS WORKER FAILED" ? message : "{error}", {error: message}); },
        onSampleNeeded(sample) {
            const offset = Number($("analysis-offset").value);
            if (file && (!sample || sample.offset !== offset || readBusy || queuedRead)) {
                requestWindow(offset);
                return true;
            }
            return false;
        },
        onSelectionChange() {
            if (!file) return;
            stopPlayback();
            if (!validOffset(Number($("analysis-offset").value))) return;
            workspace.clear("Reading the selected file window.");
            requestWindow(Number($("analysis-offset").value));
        }
    });
    function validOffset(offset) {
        if (file && Number.isSafeInteger(offset) && offset >= 0 && offset < file.size) return true;
        setStatus("error", "Start byte must be an integer within the uploaded file");
        return false;
    }
    function updateControls() {
        const offset = Number($("analysis-offset").value), size = Number($("analysis-size").value);
        $("file-controls").disabled = !file;
        $("previous-window").disabled = !file || offset <= 0;
        $("next-window").disabled = !file || offset + size >= file.size;
        text($("play-file"), playing ? "Pause playback" : file && offset + size >= file.size ? "Play from start" : "Play windows");
        $("play-file").setAttribute("aria-pressed", String(playing));
    }
    function stopPlayback() {
        clearTimeout(timer); timer = null;
        if (playing) {
            playing = false;
            seekVersion++; queuedRead = null;
            workspace.setFrozen(true);
            if (file && workspace.sample) setStatus("ready", "Analysis complete");
        }
        text($("capture-state"), file ? "Paused · inspect bytes or click A/B. Next/Previous advances one window; playback stops at EOF." :
            "Upload a file, then inspect a window or play successive windows.");
        updateControls();
    }
    function scheduleNext(data) {
        clearTimeout(timer);
        const next = data.offset + data.analyzedBytes;
        if (next >= file.size) { stopPlayback(); return; }
        timer = setTimeout(() => { timer = null; if (playing) requestWindow(next); }, 1000 / Number($("display-fps").value));
    }
    function requestWindow(offset) {
        if (!validOffset(offset)) return;
        // Coalesce reads as well as worker jobs: at most one File.slice read is
        // running and one newer seek is queued, each capped at one MiB.
        workspace.cancelPending();
        const size = Number($("analysis-size").value);
        $("analysis-offset").value = offset;
        queuedRead = {file, sourceId, version: ++seekVersion, offset, size};
        updateControls();
        pumpRead();
    }
    async function pumpRead() {
        if (readBusy || !queuedRead) return;
        const job = queuedRead;
        queuedRead = null; readBusy = true;
        try {
            const buffer = await job.file.slice(job.offset, Math.min(job.file.size, job.offset + job.size)).arrayBuffer();
            if (job.sourceId !== sourceId || job.version !== seekVersion) return;
            workspace.submit(buffer, {totalBytes: job.file.size, offset: job.offset, sourceId: job.sourceId,
                frame: ++windowNumber, time: Date.now(), captureTime: performance.now()});
        } catch (error) {
            if (job.sourceId === sourceId && job.version === seekVersion) { stopPlayback(); setStatus("error", "File read failed: {error}", {error: error.message}); }
        } finally { readBusy = false; pumpRead(); }
    }
    function step(direction) {
        if (!file) return;
        stopPlayback();
        const next = Math.max(0, Number($("analysis-offset").value) + direction * Number($("analysis-size").value));
        if (next < file.size) requestWindow(next);
    }
    $("analysis-offset").addEventListener("input", stopPlayback);
    $("previous-window").addEventListener("click", () => step(-1));
    $("next-window").addEventListener("click", () => step(1));
    $("play-file").addEventListener("click", () => {
        if (!file) return;
        if (playing) { stopPlayback(); return; }
        playing = true; workspace.setFrozen(false);
        text($("capture-state"), "Playing · each row is one inspected file window. Pause to freeze and mark A/B.");
        updateControls();
        const sample = workspace.sample;
        if (!sample || sample.offset + sample.analyzedBytes >= file.size) requestWindow(0);
        else scheduleNext(sample);
    });
    $("display-fps").addEventListener("change", () => { if (playing && workspace.sample) scheduleNext(workspace.sample); });
    document.addEventListener("visibilitychange", () => { if (document.hidden) stopPlayback(); });
    form.addEventListener("submit", async event => {
        event.preventDefault();
        if (controller) return;
        const selected = input.files[0];
        if (!selected || selected.size < 1 || selected.size > 32 * 1024 * 1024) {
            setStatus("error", "Choose a non-empty file of at most 32 MiB"); return;
        }
        stopPlayback();
        sourceId++; seekVersion++; queuedRead = null; file = null; windowNumber = 0;
        workspace.clear("Uploading the file for server analysis.");
        updateControls();
        controller = new AbortController();
        submit.disabled = true; input.disabled = true; cancel.disabled = false;
        setStatus("connecting", "Uploading and analyzing");
        text($("file-entropy"), "{name} ({bytes} bytes)", {name: selected.name, bytes: selected.size.toLocaleString()});
        $("file-sha256").textContent = "—";
        text($("server-analysis"), "Awaiting the server report.");
        $("server-report-content").textContent = "";
        try {
            // The server keeps the existing bounded /files contract and hashes
            // the complete upload. Interactive windows use local File slices.
            const response = await fetch("/files", {method: "POST", headers: {"Content-Type": "application/octet-stream"},
                body: selected, signal: controller.signal});
            if (!response.ok) throw uiError("HTTP {status}: {detail}", {status: response.status, detail: (await response.text()).slice(0, 300)});
            const result = await response.json();
            if (result.total_bytes !== selected.size || !Number.isSafeInteger(result.analyzed_bytes) ||
                result.analyzed_bytes < 1 || result.analyzed_bytes > selected.size || !/^[0-9a-f]{64}$/.test(result.sha256)) {
                throw uiError("Invalid file analysis response");
            }
            file = selected;
            $("analysis-offset").max = file.size - 1;
            $("analysis-offset").value = 0;
            $("file-sha256").textContent = result.sha256;
            text($("server-analysis"), "Server report: first {analyzed} of {total} uploaded bytes. Plots below use their own selected window.", {analyzed: result.analyzed_bytes.toLocaleString(), total: result.total_bytes.toLocaleString()});
            $("server-report-content").textContent = JSON.stringify(result, null, 2);
            setStatus("connecting", "Reading the selected file window");
            updateControls();
            requestWindow(0);
        } catch (error) {
            setStatus(error.name === "AbortError" ? "disconnected" : "error", error.name === "AbortError" ? "Upload canceled" : error.uiMessage || "{error}", error.uiParams || {error: error.message});
            text($("sample-info"), "Upload a file to begin analysis.");
        } finally {
            controller = null;
            submit.disabled = false; input.disabled = false; cancel.disabled = true;
        }
    });
    cancel.addEventListener("click", () => controller?.abort());
    window.addEventListener("pagehide", () => {
        controller?.abort(); stopPlayback(); seekVersion++; queuedRead = null; workspace.destroy();
    });
    updateControls();
})();
