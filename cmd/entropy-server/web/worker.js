"use strict";
importScripts("/analysis-math.js");
self.onmessage = ({data}) => {
    try {
        if (data?.type !== "analyze" || !(data.buffer instanceof ArrayBuffer) ||
            !Number.isSafeInteger(data.offset) || data.offset < 0 ||
            data.offset + data.buffer.byteLength > data.totalBytes) throw new Error("Invalid analysis frame");
        const started = performance.now();
        const bytes = new Uint8Array(data.buffer);
        const statistics = EntropyAnalysis.summarize(bytes);
        const fft = EntropyAnalysis.spectrum(bytes, data.fftOptions);
        const result = {...data, ...statistics, type: "analysis", analyzedBytes: bytes.length, analysisMs: performance.now() - started, fft};
        const transfer = [data.buffer, statistics.histogram.buffer, statistics.bitOnes.buffer];
        if (fft) transfer.push(fft.power.buffer);
        self.postMessage(result, transfer);
    } catch (error) {
        self.postMessage({type: "error", message: error.message});
    }
};
