"use strict";

// Optional diagnostics use bounded prefixes, never mutate source bytes and do
// not interpret visual uniformity as an entropy or unpredictability guarantee.
(() => {
    const MAX_INPUT_BYTES = 1024 * 1024, MAX_ANALYZED_BYTES = 65536;
    const MAX_CORRELATION_BYTES = 8192, MAX_LAGS = 128;
    const MAX_SPECTRAL_ROWS = 120, MAX_SPECTRAL_BINS = 256;
    const bitCounts = Uint8Array.from({length: 256}, (_, value) => {
        let count = 0;
        for (let bits = value; bits; bits >>>= 1) count += bits & 1;
        return count;
    });
    function option(value, choices, name) {
        if (!choices.includes(value)) throw new Error("Invalid " + name);
        return value;
    }
    function analyze(bytes, {lags = 64, bins = 16, blockSize = 1024} = {}) {
        if (!(bytes instanceof Uint8Array) || !bytes.length || bytes.length > MAX_INPUT_BYTES) {
            throw new Error("Structure analysis needs 1–1,048,576 bytes");
        }
        option(lags, [32, 64, 128], "correlation lag count");
        option(bins, [16, 32], "transition bin count");
        option(blockSize, [256, 1024, 4096], "diagnostic block size");
        const count = Math.min(bytes.length, MAX_ANALYZED_BYTES);
        const correlationCount = Math.min(count, MAX_CORRELATION_BYTES);
        let sum = 0;
        for (let i = 0; i < correlationCount; i++) sum += bytes[i];
        const mean = sum / correlationCount;
        let squared = 0;
        for (let i = 0; i < correlationCount; i++) squared += (bytes[i] - mean) ** 2;
        const maxLag = Math.min(lags, correlationCount - 1), values = new Float64Array(maxLag);
        // Biased global-mean ACF: every lag uses the same full-window sum of
        // squares. Constant data has undefined correlation, not perfect noise.
        for (let lag = 1; lag <= maxLag; lag++) {
            let covariance = 0;
            for (let i = 0; i < correlationCount - lag; i++) {
                covariance += (bytes[i] - mean) * (bytes[i + lag] - mean);
            }
            values[lag - 1] = squared ? covariance / squared : NaN;
        }
        const counts = new Uint32Array(bins * bins), binWidth = 256 / bins;
        for (let i = 1; i < count; i++) {
            const from = Math.floor(bytes[i - 1] / binWidth), to = Math.floor(bytes[i] / binWidth);
            counts[from * bins + to]++;
        }
        const blocks = Math.floor(count / blockSize), histogram = new Uint32Array(256);
        const entropy = new Float64Array(blocks), ones = new Float64Array(blocks);
        for (let block = 0; block < blocks; block++) {
            histogram.fill(0);
            let bitOnes = 0;
            const start = block * blockSize;
            for (let i = start; i < start + blockSize; i++) {
                histogram[bytes[i]]++;
                bitOnes += bitCounts[bytes[i]];
            }
            let estimate = 0;
            for (const frequency of histogram) {
                if (frequency) {
                    const probability = frequency / blockSize;
                    estimate -= probability * Math.log2(probability);
                }
            }
            entropy[block] = estimate;
            ones[block] = bitOnes / (blockSize * 8);
        }
        return {
            inputBytes: bytes.length, analyzedBytes: count,
            correlation: {values, count: correlationCount, maxLag, mean,
                variance: squared / correlationCount, defined: squared > 0},
            transitions: {counts, bins, pairs: count - 1, count, binWidth},
            blocks: {entropy, ones, blockSize, count: blocks, coveredBytes: blocks * blockSize,
                omittedBytes: count - blocks * blockSize}
        };
    }
    class SpectralHistory {
        constructor(capacity = MAX_SPECTRAL_ROWS, columns = MAX_SPECTRAL_BINS) {
            if (!Number.isSafeInteger(capacity) || capacity < 1 || capacity > MAX_SPECTRAL_ROWS ||
                !Number.isSafeInteger(columns) || columns < 1 || columns > MAX_SPECTRAL_BINS) {
                throw new Error("Invalid spectral history bounds");
            }
            this.capacity = capacity; this.columns = columns;
            this.clear();
        }
        clear() { this.rows = []; this.key = null; this.lastFrame = -1; }
        add(fft, {key, frame, captureTime, offset = 0}) {
            if (typeof key !== "string" || !Number.isSafeInteger(frame) || frame < 0 ||
                !Number.isFinite(captureTime) || captureTime < 0 || !Number.isSafeInteger(offset) || offset < 0) {
                throw new Error("Invalid spectral history metadata");
            }
            if (this.key !== key) { this.clear(); this.key = key; }
            if (!fft) return false;
            if (!(fft.power instanceof Float64Array) || !Number.isSafeInteger(fft.size) ||
                fft.size < 32 || fft.size > 65536 || (fft.size & (fft.size - 1)) !== 0 ||
                fft.power.length !== fft.size / 2 + 1 || fft.binWidth !== 1 / fft.size ||
                !fft.power.every(value => Number.isFinite(value) && value >= 0)) {
                throw new Error("Invalid spectrum for history");
            }
            // Re-analysis of a frozen frame must never manufacture time rows.
            if (frame <= this.lastFrame) return false;
            const columns = Math.min(this.columns, fft.power.length), power = new Float64Array(columns);
            for (let column = 0; column < columns; column++) {
                const start = Math.floor(column * fft.power.length / columns);
                const end = Math.floor((column + 1) * fft.power.length / columns);
                let peak = 0;
                for (let i = start; i < end; i++) peak = Math.max(peak, fft.power[i]);
                power[column] = peak;
            }
            this.rows.unshift({power, size: fft.size, binWidth: fft.binWidth, sourceBins: fft.power.length,
                time: captureTime, frame, offset});
            this.lastFrame = frame;
            if (this.rows.length > this.capacity) this.rows.length = this.capacity;
            return true;
        }
    }
    globalThis.EntropyStructure = Object.freeze({analyze, SpectralHistory, MAX_INPUT_BYTES,
        MAX_ANALYZED_BYTES, MAX_CORRELATION_BYTES, MAX_LAGS, MAX_SPECTRAL_ROWS, MAX_SPECTRAL_BINS});
})();
