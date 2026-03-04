
let rollingBuffer = [];
let rollingHistBuffer = [];

function handleByteStream(arrayBuffer) {

    const bytes = new Uint8Array(arrayBuffer);
    const histogram = computeHistogram(bytes);
    const entropy = computeShannonEntropy(histogram, bytes.length);
    const fft = computeFFT(bytes);
    //const fft = computeFFT(buffer.payload.fft);
    const raw = bytes;

    //rollingBuffer.push(...bytes);
    rollingHistBuffer.push(...histogram);

    if (rollingHistBuffer.length >= 4194304) {
        const newhistogram = computeHistogram(rollingHistBuffer.slice(0, 4194304));
        rollingBuffer = [];
        self.postMessage({
            type: "RESULT",
            payload: {
                newhistogram,
                entropy,
                fft,
                raw
            }
        });
    } else {
        self.postMessage({
            type: "RESULT",
            payload: {
                histogram,
                entropy,
                fft,
                raw
            }
        });
    }
}

function handleBytesOnly(arrayBuffer) {

    const bytes = new Uint8Array(arrayBuffer);
    //const histogram = computeHistogram(bytes);
    //const entropy = computeShannonEntropy(histogram, bytes.length);
    const fft = computeFFT(bytes);
    //const fft = 0;
    //const raw = bytes;

    self.postMessage({
        type: "TEST",
        payload: {
            fft
        }
    });
}

function computeHistogram(bytes) {
    const hist = new Array(512).fill(0);

    for (let i = 0; i < bytes.length; i++) {
        hist[bytes[i]]++;
    }

    return hist;
}

function computeShannonEntropy(histogram, total) {
    let entropy = 0;

    for (let i = 0; i < histogram.length; i++) {
        if (histogram[i] === 0) continue;

        const p = histogram[i] / total;
        entropy -= p * Math.log2(p);
    }

    return entropy;
}

function fftRadix2(real, imag) {
    const n = real.length;

    if (n <= 1) return;

    if ((n & (n - 1)) !== 0) {
        throw new Error("FFT size must be power of 2");
    }

    // Bit-reversal permutation
    let j = 0;
    for (let i = 0; i < n; i++) {
        if (i < j) {
            [real[i], real[j]] = [real[j], real[i]];
            [imag[i], imag[j]] = [imag[j], imag[i]];
        }

        let m = n >> 1;
        while (j >= m && m > 0) {
            j -= m;
            m >>= 1;
        }
        j += m;
    }

    // Cooley–Tukey
    for (let size = 2; size <= n; size <<= 1) {

        const halfsize = size >> 1;
        const tablestep = n / size;

        for (let i = 0; i < n; i += size) {

            for (let k = 0; k < halfsize; k++) {

                const angle = -2 * Math.PI * k / size;
                const cos = Math.cos(angle);
                const sin = Math.sin(angle);

                const tpre =  real[i + k + halfsize] * cos
                            - imag[i + k + halfsize] * sin;

                const tpim =  real[i + k + halfsize] * sin
                            + imag[i + k + halfsize] * cos;

                real[i + k + halfsize] = real[i + k] - tpre;
                imag[i + k + halfsize] = imag[i + k] - tpim;

                real[i + k] += tpre;
                imag[i + k] += tpim;
            }
        }
    }
}

function computeMagnitude(real, imag) {
    const n = real.length;
    const half = n >> 1;
    const magnitude = new Float32Array(half);

    for (let i = 0; i < half; i++) {
        magnitude[i] = Math.sqrt(
            real[i] * real[i] +
            imag[i] * imag[i]
        );
    }

    return magnitude;
}

function computeFFT(bytes) {

    const N = 1024; // must be power of 2
    const real = new Float32Array(N);
    const imag = new Float32Array(N);

    for (let i = 0; i < N; i++) {
        real[i] = (i < bytes.length ? bytes[i] : 0) - 128;
        imag[i] = 0;
    }

	/*
    for (let i = 0; i < N && i < bytes.length; i++) {
        real[i] = bytes[i] - 128;
        imag[i] = 0;
    }
    	*/

    fftRadix2(real, imag);

    return computeMagnitude(real, imag);
}

self.onmessage = function (event) {
    const { type, payload } = event.data;

    const now = performance.now();
    const fftvalue = 3;
    const histogram = new Array(1024).fill(0);

    const buffer = event.data;
    const bytes = new Uint8Array(buffer);
    const total = bytes.length;

    let ones = 0;
    let zeros = 0;
    let totalBytes = 0;

    for (let b of bytes) {
        histogram[b]++;
        totalBytes++;

        for (let i = 0; i < 8; i++) {
            if (b & (1 << i)) ones++;
            else zeros++;
        }

        if ((totalBytes >= 4194304) || (total >= 4194304)) {
            histogram.fill(0);
            self.event.data.payload.histogram.fill(0);
            payload.histogram.fill(0);
            totalBytes = 0;
            ones = 0;
            zeros = 0;
        }
    }

    switch (type) {
        case "PROCESS_BYTES":
            handleByteStream(payload);
            break;
        case "PROCESS_TEST":
            handleBytesOnly(payload);
            break;
        case "RESET":
            resetState();
            break;
    }

    //self.postMessage({ entropy });
    //self.postMessage({ histogram });
    //self.postMessage({ buffer });
};
