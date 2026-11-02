"use strict";

// Stream-analysis worker: binary /stream payloads, never JSON envelopes.
// A frame is sampled to 64 KiB to bound CPU cost at any server payload size.
const ANALYSIS_LIMIT = 64 * 1024;
const FFT_LENGTH = 2048;
const FFT_BARS = 128;
const oneBits = Uint8Array.from({ length: 256 }, (_, value) => {
    let n = value;
    let count = 0;
    while (n !== 0) { count += n & 1; n >>= 1; }
    return count;
});

function spectrum(bytes) {
    if (bytes.length < FFT_LENGTH) return null;
    const real = new Float64Array(FFT_LENGTH);
    const imag = new Float64Array(FFT_LENGTH);

    for (let i = 0; i < FFT_LENGTH; i++) {
        // Hann window improves visual stability for an arbitrary sample window.
        const weight = 0.5 * (1 - Math.cos((2 * Math.PI * i) / (FFT_LENGTH - 1)));
        real[i] = (bytes[i] - 127.5) * weight;
    }

    for (let i = 1, j = 0; i < FFT_LENGTH; i++) {
        let bit = FFT_LENGTH >> 1;
        for (; j & bit; bit >>= 1) j ^= bit;
        j ^= bit;
        if (i < j) {
            [real[i], real[j]] = [real[j], real[i]];
            [imag[i], imag[j]] = [imag[j], imag[i]];
        }
    }

    for (let size = 2; size <= FFT_LENGTH; size <<= 1) {
        const half = size >> 1;
        for (let start = 0; start < FFT_LENGTH; start += size) {
            for (let i = 0; i < half; i++) {
                const angle = -2 * Math.PI * i / size;
                const cosine = Math.cos(angle);
                const sine = Math.sin(angle);
                const a = start + i;
                const b = a + half;
                const re = real[b] * cosine - imag[b] * sine;
                const im = real[b] * sine + imag[b] * cosine;
                real[b] = real[a] - re;
                imag[b] = imag[a] - im;
                real[a] += re;
                imag[a] += im;
            }
        }
    }

    const bars = new Float32Array(FFT_BARS);
    const binsPerBar = (FFT_LENGTH / 2) / FFT_BARS;
    for (let i = 0; i < FFT_BARS; i++) {
        let sum = 0;
        for (let j = 0; j < binsPerBar; j++) {
            const k = i * binsPerBar + j;
            sum += Math.hypot(real[k], imag[k]);
        }
        bars[i] = Math.log10(1 + sum / binsPerBar);
    }
    return bars;
}

function analyze(buffer, totalBytes) {
    if (!(buffer instanceof ArrayBuffer) || !Number.isInteger(totalBytes) ||
        totalBytes < buffer.byteLength || buffer.byteLength < 1 || buffer.byteLength > ANALYSIS_LIMIT) {
        throw new Error("Invalid binary sample");
    }

    const bytes = new Uint8Array(buffer);
    const histogram = new Uint32Array(256);
    let ones = 0;
    for (const byte of bytes) { histogram[byte]++; ones += oneBits[byte]; }

    let entropy = 0;
    for (const count of histogram) {
        if (!count) continue;
        const p = count / bytes.length;
        entropy -= p * Math.log2(p);
    }

    const mosaic = bytes.slice(0, Math.min(bytes.length, 50 * 6 * 3));
    const wave = bytes.slice(0, Math.min(bytes.length, 800));
    const fft = spectrum(bytes);
    return {
        type: "analysis", totalBytes, analyzedBytes: bytes.length,
        histogram: Array.from(histogram), entropy,
        ones, zeros: bytes.length * 8 - ones, mosaic, wave, fft
    };
}

self.onmessage = ({data}) => {
    if (data?.type !== "analyze") return;
    try {
        const result = analyze(data.buffer, data.totalBytes);
        const transfers = [result.mosaic.buffer, result.wave.buffer];
        if (result.fft !== null) transfers.push(result.fft.buffer);
        self.postMessage(result, transfers);
    } catch (error) {
        self.postMessage({ type: "error", message: error.message });
    }
};
