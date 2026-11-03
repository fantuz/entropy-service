"use strict";

// Browser-only threshold for Shannon entropy calculated from the bounded
// sample in stream-analysis.js. It is a diagnostic, not an RNG health test.
(() => {
    const storageKey = "entropy-service:stream:shannon-threshold:v1";
    const defaultThreshold = 7.99990;
    const card = document.getElementById("entropy-alert-card");
    const input = document.getElementById("entropy-threshold");
    const reading = document.getElementById("entropy");
    const message = document.getElementById("entropy-alert-message");
    let threshold = defaultThreshold;
    let latest = null;

    function parseThreshold(raw) {
        // Require at most five decimal places, in the inclusive range [1,8].
        // Reject 8.00001 and any silent rounding of user-supplied precision.
        if (!/^(?:[1-7](?:[.][0-9]{1,5})?|8(?:[.]0{1,5})?)$/.test(raw)) return null;
        const candidate = Number(raw);
        return Number.isFinite(candidate) && candidate >= 1 && candidate <= 8 ?
            candidate : null;
    }

    function paint() {
        if (latest === null) {
            card.dataset.state = "waiting";
            reading.textContent = "—";
            message.textContent = "AWAITING FIRST ANALYSIS SAMPLE";
            return;
        }
        // Compare full-precision source data; only the visual value is rounded.
        const alert = latest < threshold;
        card.dataset.state = alert ? "alert" : "normal";
        reading.textContent = latest.toFixed(5);
        message.textContent = alert ?
            `BELOW THRESHOLD · ${threshold.toFixed(5)} BITS/BYTE` :
            `WITHIN SELECTED THRESHOLD · ${threshold.toFixed(5)} BITS/BYTE`;
    }

    function update(value) {
        if (!Number.isFinite(value) || value < 0 || value > 8) {
            latest = null;
        } else {
            latest = value;
        }
        paint();
    }

    function setThreshold(raw) {
        const parsed = parseThreshold(String(raw).trim());
        if (parsed === null) {
            input.value = threshold.toFixed(5);
            input.setCustomValidity("");
            return false;
        }
        threshold = parsed;
        input.value = threshold.toFixed(5);
        try {
            localStorage.setItem(storageKey, input.value);
        } catch (_) {
            // Restricted browser storage must not prevent threshold control.
        }
        paint();
        return true;
    }

    try {
        const saved = localStorage.getItem(storageKey);
        const parsed = saved === null ? null : parseThreshold(saved);
        if (parsed !== null) threshold = parsed;
    } catch (_) {
        // Private browsing or restricted storage: use the default.
    }

    input.value = threshold.toFixed(5);
    input.addEventListener("change", () => { setThreshold(input.value); });
    paint();
    window.EntropyShannonAlert = {update, setThreshold};
})();
