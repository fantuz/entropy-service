"use strict";

// PCM-aware, display-only diagnostics. No inferred formats, stream conditioning
// or persistent filter state: each inspected window is independent.
(() => {
    const MAX_BYTES = 1024 * 1024, MAX_SAMPLES = 32768, MAX_FFT = 8192;
    const plans = new Map();
    function plan(size) {
        if (plans.has(size)) return plans.get(size);
        const reverse = new Uint32Array(size), cosine = new Float64Array(size / 2);
        const sine = new Float64Array(size / 2), window = new Float64Array(size);
        const bits = Math.log2(size);
        let energy = 0;
        for (let i = 0; i < size; i++) {
            let value = i, reversed = 0;
            for (let bit = 0; bit < bits; bit++) { reversed = reversed * 2 + (value & 1); value >>>= 1; }
            reverse[i] = reversed;
            window[i] = .5 * (1 - Math.cos(2 * Math.PI * i / (size - 1)));
            energy += window[i] * window[i];
            if (i < size / 2) { cosine[i] = Math.cos(-2 * Math.PI * i / size); sine[i] = Math.sin(-2 * Math.PI * i / size); }
        }
        const result = {reverse, cosine, sine, window, energy, real: new Float64Array(size), imag: new Float64Array(size)};
        plans.set(size, result);
        return result;
    }
    function spectrum(samples, sampleRate) {
        const size = 2 ** Math.floor(Math.log2(Math.min(samples.length, MAX_FFT)));
        if (size < 32) return null;
        const {reverse, cosine, sine, window, energy, real, imag} = plan(size);
        const startSample = samples.length - size;
        for (let i = 0; i < size; i++) { real[reverse[i]] = samples[startSample + i] * window[i]; imag[i] = 0; }
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
        const power = new Float64Array(size / 2 + 1), binWidthHz = sampleRate / size;
        let integratedPower = 0;
        for (let k = 0; k < power.length; k++) {
            power[k] = (real[k] ** 2 + imag[k] ** 2) / (sampleRate * energy);
            if (k !== 0 && k !== size / 2) power[k] *= 2;
            integratedPower += power[k] * binWidthHz;
        }
        return {power, size, startSample, binWidthHz, sampleRate, integratedPower, window: "hann", removeMean: false};
    }
    function metrics(samples) {
        let sum = 0, squares = 0, peak = 0;
        for (const sample of samples) { sum += sample; squares += sample * sample; peak = Math.max(peak, Math.abs(sample)); }
        return {samples, rms: Math.sqrt(squares / samples.length), peak, dc: sum / samples.length};
    }
    function notchFilter(samples, sampleRate, frequency) {
        const q = 30, angle = 2 * Math.PI * frequency / sampleRate, alpha = Math.sin(angle) / (2 * q);
        const b0 = 1 / (1 + alpha), b1 = -2 * Math.cos(angle) / (1 + alpha), a2 = (1 - alpha) / (1 + alpha);
        const output = new Float64Array(samples.length);
        let x1 = 0, x2 = 0, y1 = 0, y2 = 0;
        for (let i = 0; i < samples.length; i++) {
            const x = samples[i], y = b0 * x + b1 * x1 + b0 * x2 - b1 * y1 - a2 * y2;
            output[i] = y; x2 = x1; x1 = x; y2 = y1; y1 = y;
        }
        return {...metrics(output), spectrum: spectrum(output, sampleRate), notchHz: frequency, q, initialState: "zero"};
    }
    function analyze(bytes, metadata, {notch = "off"} = {}) {
        if (!(bytes instanceof Uint8Array) || !bytes.length || bytes.length > MAX_BYTES || bytes.length % 2) throw new Error("Audio analysis needs an even 2–1,048,576 PCM16LE bytes");
        if (metadata?.format !== "pcm16le" || metadata.channels !== 1 || !Number.isFinite(metadata.sampleRate) || metadata.sampleRate < 8000 || metadata.sampleRate > 384000) {
            throw new Error("Audio analysis needs explicit PCM16LE mono metadata and an 8,000–384,000 Hz sample rate");
        }
        if (!["off", "50", "60"].includes(notch)) throw new Error("Choose Off, 50 Hz or 60 Hz notch comparison");
        const sampleRate = metadata.sampleRate, sampleCount = Math.min(bytes.length / 2, MAX_SAMPLES);
        const samples = new Float64Array(sampleCount), view = new DataView(bytes.buffer, bytes.byteOffset, sampleCount * 2);
        let clipped = 0;
        for (let i = 0; i < sampleCount; i++) {
            const integer = view.getInt16(i * 2, true);
            samples[i] = integer / 32768;
            if (integer === -32768 || integer === 32767) clipped++;
        }
        const raw = {...metrics(samples), clipped, clippedFraction: clipped / sampleCount, spectrum: spectrum(samples, sampleRate)};
        const frequency = notch === "off" ? 0 : Number(notch);
        if (frequency >= sampleRate / 2) throw new Error("Notch frequency must be below the sample Nyquist frequency");
        return {format: "pcm16le", sampleRate, channels: 1, inputBytes: bytes.length, sampleCount,
            analyzedBytes: sampleCount * 2, truncated: sampleCount * 2 < bytes.length,
            raw, filtered: frequency ? notchFilter(samples, sampleRate, frequency) : null};
    }
    globalThis.EntropyAudio = Object.freeze({MAX_BYTES, MAX_SAMPLES, MAX_FFT, analyze});
})();
