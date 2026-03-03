function computeFFT(bytes) {

    const N = 1024; // power of 2
    const real = new Float32Array(N);
    const imag = new Float32Array(N);

    for (let i = 0; i < N && i < bytes.length; i++) {
        real[i] = bytes[i] - 128;
        imag[i] = 0;
    }

    fftRadix2(real, imag);

    return computeMagnitude(real, imag);
}

let rollingBuffer = [];

function handleByteStream(arrayBuffer) {

    const bytes = new Uint8Array(arrayBuffer);
    const histogram = computeHistogram(bytes);
    const entropy = computeShannonEntropy(histogram, bytes.length);
    //const fft = computeFFT(bytes);
    const fft = 0;
    const raw = bytes;

    rollingBuffer.push(...bytes);
    //rollingBuffer.push(...histogram);
    if (rollingBuffer.length >= 1024) {
        newhistogram = computeHistogram(rollingBuffer.slice(0, 1024));
        self.postMessage({
            type: "RESULT",
            payload: {
                newhistogram,
                entropy,
                fft,
                raw
            }
        });
        rollingBuffer = [];
    }

	/*
    self.postMessage({
        type: "RESULT",
        payload: {
            histogram,
            entropy,
            fft,
            raw
        }
    });
        */
}

function handleBytesOnly(arrayBuffer) {

    const bytes = new Uint8Array(arrayBuffer);
    const histogram = computeHistogram(bytes);
    const entropy = computeShannonEntropy(histogram, bytes.length);
    const fft = computeFFT(bytes);
    //const fft = 0;
    const raw = bytes;

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
        case "PROCESS_STREAM":
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
