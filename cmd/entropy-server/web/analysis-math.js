"use strict";

// Shared by the stream worker, file preview and deterministic Node fixtures.
(() => {
    const MAX_BYTES = 1024 * 1024;
    const FFT_SIZES = [512, 2048, 8192, 32768, 65536];
    const plans = new Map();
    function summarize(bytes) {
        if (!(bytes instanceof Uint8Array) || !bytes.length || bytes.length > MAX_BYTES) {
            throw new Error("Analysis needs 1–1,048,576 bytes");
        }
        const histogram = new Uint32Array(256), bitOnes = new Uint32Array(8);
        for (const value of bytes) histogram[value]++;
        let entropy = 0, sum = 0, squares = 0, ones = 0, min = 255, max = 0;
        for (let value = 0; value < 256; value++) {
            const count = histogram[value];
            if (!count) continue;
            const probability = count / bytes.length;
            entropy -= probability * Math.log2(probability);
            sum += value * count; squares += value * value * count;
            min = Math.min(min, value); max = Math.max(max, value);
            for (let bit = 0; bit < 8; bit++) {
                if (value & (1 << bit)) { bitOnes[bit] += count; ones += count; }
            }
        }
        const mean = sum / bytes.length;
        return {histogram, bitOnes, entropy, ones, zeros: bytes.length * 8 - ones,
            min, max, mean, stddev: Math.sqrt(Math.max(0, squares / bytes.length - mean * mean))};
    }
    function plan(size) {
        if (plans.has(size)) return plans.get(size);
        const reverse = new Uint32Array(size), cosine = new Float64Array(size / 2);
        const sine = new Float64Array(size / 2), hann = new Float64Array(size);
        const bits = Math.log2(size);
        for (let i = 0; i < size; i++) {
            let value = i, reversed = 0;
            for (let bit = 0; bit < bits; bit++) { reversed = reversed * 2 + (value & 1); value >>>= 1; }
            reverse[i] = reversed;
            hann[i] = 0.5 * (1 - Math.cos(2 * Math.PI * i / (size - 1)));
            if (i < size / 2) {
                cosine[i] = Math.cos(-2 * Math.PI * i / size);
                sine[i] = Math.sin(-2 * Math.PI * i / size);
            }
        }
        const result = {reverse, cosine, sine, hann, real: new Float64Array(size), imag: new Float64Array(size)};
        plans.set(size, result);
        return result;
    }
    function spectrum(bytes, {size = "auto", window = "hann", removeMean = true} = {}) {
        if (!(bytes instanceof Uint8Array) || bytes.length > MAX_BYTES) throw new Error("Invalid FFT input");
        if (size === "auto") size = 2 ** Math.floor(Math.log2(Math.min(bytes.length, 8192)));
        else if (!FFT_SIZES.includes(size)) throw new Error("Unsupported FFT size");
        if (window !== "hann" && window !== "rectangular") throw new Error("Invalid FFT window");
        if (size < 32 || bytes.length < size) return null; // Never silently zero-pad.
        const {real, imag, reverse, cosine, sine, hann} = plan(size);
        let mean = 0, windowEnergy = 0;
        for (let i = 0; i < size; i++) mean += bytes[i] / size;
        for (let i = 0; i < size; i++) {
            const weight = window === "hann" ? hann[i] : 1;
            real[reverse[i]] = ((bytes[i] - (removeMean ? mean : 127.5)) / 127.5) * weight;
            imag[i] = 0; windowEnergy += weight * weight;
        }
        for (let span = 2; span <= size; span *= 2) {
            const half = span / 2, stride = size / span;
            for (let start = 0; start < size; start += span) {
                for (let k = 0; k < half; k++) {
                    const a = start + k, b = a + half, twiddle = k * stride;
                    const r = real[b] * cosine[twiddle] - imag[b] * sine[twiddle];
                    const im = real[b] * sine[twiddle] + imag[b] * cosine[twiddle];
                    real[b] = real[a] - r; imag[b] = imag[a] - im;
                    real[a] += r; imag[a] += im;
                }
            }
        }
        const power = new Float64Array(size / 2 + 1);
        let integratedPower = 0;
        for (let k = 0; k < power.length; k++) {
            power[k] = (real[k] ** 2 + imag[k] ** 2) / windowEnergy;
            if (k !== 0 && k !== size / 2) power[k] *= 2;
            integratedPower += power[k] / size;
        }
        return {power, size, mean, window, removeMean, binWidth: 1 / size, integratedPower};
    }
    // Exact min/max per screen column: isolated spikes survive trace zoom-out.
    function envelope(bytes, columns) {
        columns = Math.max(1, Math.min(bytes.length, Math.floor(columns)));
        const result = [];
        for (let column = 0; column < columns; column++) {
            const start = Math.floor(column * bytes.length / columns);
            const end = Math.floor((column + 1) * bytes.length / columns);
            let min = 255, max = 0;
            for (let i = start; i < end; i++) { min = Math.min(min, bytes[i]); max = Math.max(max, bytes[i]); }
            result.push({start, end, min, max});
        }
        return result;
    }
    class PowerAverage {
        constructor() { this.reset(); }
        reset() { this.frames = []; this.sum = null; this.key = ""; }
        add(fft, count, key) {
            if (this.key !== key || this.sum?.length !== fft.power.length) {
                this.reset(); this.key = key; this.sum = new Float64Array(fft.power.length);
            }
            this.frames.push(fft.power.slice());
            for (let i = 0; i < fft.power.length; i++) this.sum[i] += fft.power[i];
            while (this.frames.length > count) {
                const old = this.frames.shift();
                for (let i = 0; i < old.length; i++) this.sum[i] -= old[i];
            }
            return {...fft, power: this.sum.map(value => Math.max(0, value / this.frames.length)), averages: this.frames.length};
        }
    }
    class WaterfallHistory {
        constructor(capacity = 240) { this.capacity = capacity; this.rows = []; }
        clear() { this.rows = []; }
        add(bytes, time, frame) {
            this.rows.unshift({bytes: bytes.slice(0, 1024), time, frame});
            if (this.rows.length > this.capacity) this.rows.length = this.capacity;
        }
    }
    globalThis.EntropyAnalysis = Object.freeze({MAX_BYTES, FFT_SIZES, summarize, spectrum, envelope, PowerAverage, WaterfallHistory});
})();
