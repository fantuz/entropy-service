"use strict";

const maxDisplayBytes = 32 * 1024 * 1024;
const previewBytes = 256;
let socket = null;
let pending = null;
let awaitingPaint = false;

function hexOf(bytes) {
    return Array.from(bytes, byte => byte.toString(16).padStart(2, "0")).join("");
}

function decodeDisplayFrame(data, view) {
    if (view === "colors") {
        if (typeof data !== "string") throw new Error("Expected a hash response");
        const frame = JSON.parse(data);
        if (!/^[0-9a-f]{64}$/.test(frame.hash) ||
            !Number.isInteger(frame.bytes) || frame.bytes < 1 || frame.bytes > maxDisplayBytes) {
            throw new Error("Invalid hash response");
        }
        return { hash: frame.hash, bytes: frame.bytes };
    }

    if (!(data instanceof ArrayBuffer) || data.byteLength < 33 ||
        data.byteLength > maxDisplayBytes + 32) {
        throw new Error("Invalid compact response");
    }

    // Compact v1: SHA-256 of the full sample, followed by the sample itself.
    // Only encode a bounded preview; never create full-sample display strings.
    const bytes = new Uint8Array(data, 32);
    const preview = bytes.subarray(0, previewBytes);
    return {
        hash: hexOf(new Uint8Array(data, 0, 32)),
        bytes: bytes.length,
        previewBytes: preview.length,
        hex: hexOf(preview),
        base64: btoa(String.fromCharCode(...preview))
    };
}

function publishLatest() {
    if (awaitingPaint || pending === null) return;
    awaitingPaint = true;
    self.postMessage(pending);
    pending = null;
}

self.onmessage = function(event) {
    if (event.data.type === "ready") {
        awaitingPaint = false;
        publishLatest();
        return;
    }
    if (event.data.type === "stop") {
        if (socket !== null) socket.close();
        pending = null;
        self.close();
        return;
    }
    if (event.data.type !== "start" || socket !== null) return;

    const view = event.data.view;
    let failed = false;
    try {
        socket = new WebSocket(event.data.url);
        socket.binaryType = "arraybuffer";
        socket.onopen = () => self.postMessage({ type: "status", text: "Connected" });
        socket.onerror = () => {
            failed = true;
            self.postMessage({ type: "status", text: "Connection failed; check the request size and server availability." });
        };
        socket.onclose = () => {
            if (!failed) self.postMessage({ type: "status", text: "Disconnected" });
        };
        socket.onmessage = function(message) {
            try {
                // Keep one small pending display result, replacing stale previews.
                // Raw samples stay in this worker and are not queued for painting.
                pending = { type: "frame", ...decodeDisplayFrame(message.data, view) };
                publishLatest();
            } catch (error) {
                failed = true;
                self.postMessage({ type: "status", text: error.message });
                socket.close(1002, "invalid entropy response");
            }
        };
    } catch (error) {
        self.postMessage({ type: "status", text: error.message });
    }
};
