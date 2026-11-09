"use strict";

(() => {
    const form = document.getElementById("uploadForm");
    const input = document.getElementById("fileInput");
    const submit = document.getElementById("submit-upload");
    const cancel = document.getElementById("cancel-upload");
    const status = document.getElementById("upload-status");
    function setStatus(state, message) {
        status.dataset.state = state;
        status.textContent = message;
    }
    let controller = null;

    function canvas(id) {
        const element = document.getElementById(id);
        const context = element.getContext("2d");
        context.clearRect(0, 0, element.width, element.height);
        return [element, context];
    }

    function render(result) {
        document.getElementById("entropy").textContent = result.entropy.toFixed(5) + " / 8 bits per byte";
        document.getElementById("sample-info").textContent =
            `Statistics cover the first ${result.analyzed_bytes} of ${result.total_bytes} uploaded bytes. Visual samples use at most 2048 of those bytes.`;
        document.getElementById("file-sha256").textContent = result.sha256;
        const bytes = Uint8Array.from(atob(result.preview), value => value.charCodeAt(0));

        const [histogram, hist] = canvas("histogram");
        const peak = Math.max(...result.histogram, 1);
        const width = histogram.width / 256;
        hist.fillStyle = "#00aacc";
        result.histogram.forEach((count, i) => {
            const height = count / peak * histogram.height;
            hist.fillRect(i * width, histogram.height - height, width, height);
        });

        const [pieCanvas, pie] = canvas("pie");
        const angle = result.zeros / (result.ones + result.zeros) * 2 * Math.PI;
        const centerX = pieCanvas.width / 2;
        for (const [start, end, color] of [[0, angle, "#145b41"], [angle, 2 * Math.PI, "#a7ffcd"]]) {
            pie.beginPath(); pie.moveTo(centerX, 76); pie.arc(centerX, 76, 62, start, end);
            pie.fillStyle = color; pie.fill();
        }
        pie.save();
        pie.font = "12px monospace";
        pie.textAlign = "left";
        pie.textBaseline = "middle";
        const labels = [`0: ${result.zeros}`, `1: ${result.ones}`];
        const keySize = 12, keyGap = 6;
        labels.forEach((label, i) => {
            const legendWidth = keySize + keyGap + pie.measureText(label).width;
            const legendX = (pieCanvas.width - legendWidth) / 2;
            const y = 167 + i * 25;
            pie.fillStyle = i === 0 ? "#145b41" : "#a7ffcd";
            pie.fillRect(legendX, y - keySize / 2, keySize, keySize);
            pie.strokeStyle = "#444";
            pie.strokeRect(legendX, y - keySize / 2, keySize, keySize);
            pie.fillStyle = "#a7ffcd";
            pie.fillText(label, legendX + keySize + keyGap, y);
        });
        pie.restore();

        const [mosaic, tiles] = canvas("mosaic");
        const columns = Math.floor(mosaic.width / 16);
        const tileCount = Math.min(Math.floor(bytes.length / 3), columns * Math.floor(mosaic.height / 16));
        for (let i = 0; i < tileCount; i++) {
            tiles.fillStyle = `rgb(${bytes[i * 3]},${bytes[i * 3 + 1]},${bytes[i * 3 + 2]})`;
            tiles.fillRect(i % columns * 16, Math.floor(i / columns) * 16, 16, 16);
        }

        const [wave, line] = canvas("wave");
        line.beginPath();
        bytes.forEach((value, i) => {
            const x = i / Math.max(bytes.length - 1, 1) * wave.width;
            const y = (1 - value / 255) * wave.height;
            if (i === 0) line.moveTo(x, y); else line.lineTo(x, y);
        });
        line.strokeStyle = "#00ff55"; line.stroke();
        renderFFT(bytes);
    }

    let latestSpectrum = null;
    function renderFFT(bytes) {
        latestSpectrum = window.EntropyAnalysis.spectrum(bytes, {size: "auto", window: "hann", removeMean: true});
        window.EntropyPlots.spectrum(document.getElementById("fft"), latestSpectrum);
        document.getElementById("fftvalue").textContent = latestSpectrum ?
            `${latestSpectrum.size} points · Hann · mean removed · Δf ${latestSpectrum.binWidth.toPrecision(4)} cycles/byte` :
            "requires at least 32 preview bytes";
    }
    new ResizeObserver(() => window.EntropyPlots.spectrum(document.getElementById("fft"), latestSpectrum))
        .observe(document.getElementById("fft"));

    form.addEventListener("submit", async event => {
        event.preventDefault();
        if (controller !== null) return;
        const file = input.files[0];
        if (!file || file.size < 1 || file.size > 32 * 1024 * 1024) {
            setStatus("error", "Choose a non-empty file of at most 32 MiB");
            return;
        }

        controller = new AbortController();
        submit.disabled = true; input.disabled = true; cancel.disabled = false;
        setStatus("connecting", "Uploading and analyzing");
        document.getElementById("file-entropy").textContent = `${file.name} (${file.size} bytes)`;
        document.getElementById("entropy").textContent = "pending";
        document.getElementById("sample-info").textContent = "";
        document.getElementById("file-sha256").textContent = "";
        document.getElementById("fftvalue").textContent = "pending";
        latestSpectrum = null;
        for (const id of ["histogram", "pie", "mosaic", "wave", "fft"]) canvas(id);
        try {
            // Send the File directly: no full-file ArrayBuffer or multipart copy.
            const response = await fetch("/files", {
                method: "POST", headers: { "Content-Type": "application/octet-stream" },
                body: file, signal: controller.signal
            });
            if (!response.ok) throw new Error(`HTTP ${response.status}: ${await response.text()}`);
            render(await response.json());
            setStatus("ready", "Analysis complete");
        } catch (error) {
            setStatus(error.name === "AbortError" ? "disconnected" : "error", error.name === "AbortError" ? "Upload canceled" : error.message);
        } finally {
            controller = null;
            submit.disabled = false; input.disabled = false; cancel.disabled = true;
        }
    });
    cancel.addEventListener("click", () => { if (controller !== null) controller.abort(); });
    window.addEventListener("pagehide", () => { if (controller !== null) controller.abort(); });
})();
