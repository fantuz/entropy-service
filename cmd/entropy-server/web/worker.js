"use strict";
importScripts("/analysis-math.js", "/structure-math.js", "/audio-analysis.js");
self.onmessage = ({data}) => {
    try {
        if (data?.type !== "analyze" || !(data.buffer instanceof ArrayBuffer) ||
            !Number.isSafeInteger(data.offset) || data.offset < 0 ||
            data.offset + data.buffer.byteLength > data.totalBytes) throw new Error("Invalid analysis frame");
        const started = performance.now();
        const bytes = new Uint8Array(data.buffer);
        const statistics = EntropyAnalysis.summarize(bytes);
        const fft = EntropyAnalysis.spectrum(bytes, data.fftOptions);
        const structure = data.structureOptions ? EntropyStructure.analyze(bytes, data.structureOptions) : null;
        let audio = null, audioReason = "";
        if (data.audioRequest) {
            if (data.audioRequest.reason) audioReason = data.audioRequest.reason;
            else if (data.offset % 2 || bytes.length % 2 || bytes.length < 2) audioReason = "Select an even byte offset and an even window of at least two bytes for PCM16LE.";
            else {
                try { audio = EntropyAudio.analyze(bytes, data.audioRequest.metadata, {notch: data.audioRequest.notch}); }
                catch (error) { audioReason = error.message; }
            }
        }
        const result = {...data, ...statistics, type: "analysis", analyzedBytes: bytes.length, analysisMs: performance.now() - started, fft, structure, audio, audioReason};
        const transfer = [data.buffer, statistics.histogram.buffer, statistics.bitOnes.buffer];
        if (fft) transfer.push(fft.power.buffer);
        if (structure) transfer.push(structure.correlation.values.buffer, structure.transitions.counts.buffer, structure.blocks.entropy.buffer, structure.blocks.ones.buffer);
        for (const signal of [audio?.raw, audio?.filtered]) {
            if (!signal) continue;
            transfer.push(signal.samples.buffer);
            if (signal.spectrum) transfer.push(signal.spectrum.power.buffer);
        }
        self.postMessage(result, transfer);
    } catch (error) {
        self.postMessage({type: "error", message: error.message});
    }
};
