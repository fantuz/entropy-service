"use strict";

// Browser-only, opt-in watchpoint for a bounded sample's Shannon estimate.
// Neither the estimate nor a user-selected threshold is an RNG health test.
(() => {
    const tr = (message, params = {}) => globalThis.window?.EntropyI18n?.t(message, params) ??
        message.replace(/\{(\w+)\}/g, (_, key) => Object.hasOwn(params, key) ? String(params[key]) : `{${key}}`);
    function text(element, message, params = {}) {
        if (globalThis.window?.EntropyI18n) window.EntropyI18n.set(element, message, params);
        else {
            const value = tr(message, params);
            if (element.textContent !== value) element.textContent = value;
        }
    }
    const storageKey = "entropy-service:stream:shannon-alert:v2";
    const legacyKey = "entropy-service:stream:shannon-threshold:v1";
    const card = document.getElementById("entropy-alert-card");
    const toggle = document.getElementById("entropy-alert-enabled");
    const settings = document.getElementById("entropy-threshold-settings");
    const input = document.getElementById("entropy-threshold");
    const error = document.getElementById("entropy-threshold-error");
    const reading = document.getElementById("entropy");
    const sampleSize = document.getElementById("entropy-sample-size");
    const message = document.getElementById("entropy-alert-message");
    const invalidMessage = "Enter 1–8 bits/byte with up to six decimal places.";
    let enabled = false, threshold = null, invalid = false, latest = null, bytes = null;

    function parseThreshold(raw) {
        // Never round user-supplied precision into a different threshold.
        if (!/^(?:[1-7](?:[.][0-9]{1,6})?|8(?:[.]0{1,6})?)$/.test(raw)) return null;
        const candidate = Number(raw);
        return Number.isFinite(candidate) && candidate >= 1 && candidate <= 8 ? candidate : null;
    }

    function paint() {
        reading.textContent = latest === null ? "—" : latest.toFixed(6);
        text(sampleSize, bytes === null ? "Sample size: —" : "Estimate from {bytes} bytes",
            {bytes: bytes?.toLocaleString()});
        error.hidden = !invalid;
        text(error, invalid ? invalidMessage : "");
        input.setAttribute("aria-invalid", String(invalid));
        input.setCustomValidity(invalid ? tr(invalidMessage) : "");
        if (latest === null) {
            card.dataset.state = "waiting";
            text(message, enabled && threshold === null ? "Enter a custom threshold to enable alerts." :
                "AWAITING FIRST ANALYSIS SAMPLE");
        } else if (!enabled) {
            card.dataset.state = "descriptive";
            text(message, "DESCRIPTIVE ESTIMATE · ALERT DISABLED");
        } else if (threshold === null || invalid) {
            card.dataset.state = "unconfigured";
            text(message, "Enter a custom threshold to enable alerts.");
        } else {
            // Compare full precision; only the readout is rounded to six places.
            const alert = latest < threshold;
            card.dataset.state = alert ? "alert" : "normal";
            text(message, alert ? "BELOW THRESHOLD · {threshold} BITS/BYTE" :
                "WITHIN SELECTED THRESHOLD · {threshold} BITS/BYTE", {threshold: threshold.toFixed(6)});
        }
    }

    function controls() {
        toggle.checked = enabled;
        settings.hidden = !enabled;
        input.disabled = !enabled;
        input.required = enabled;
        input.value = threshold === null ? "" : threshold.toFixed(6);
        invalid = false;
        paint();
    }

    function save() {
        try {
            localStorage.setItem(storageKey, JSON.stringify({enabled,
                threshold: threshold === null ? null : threshold.toFixed(6)}));
        } catch (_) {
            // Restricted browser storage must not prevent local control.
        }
    }

    function preference() {
        try {
            const saved = localStorage.getItem(storageKey);
            if (saved !== null) {
                // A new explicit disabled preference always supersedes legacy
                // values. Corrupt data must never silently activate an alert.
                try {
                    const value = JSON.parse(saved);
                    if (value && typeof value.enabled === "boolean") {
                        const parsed = value.threshold === null ? null :
                            typeof value.threshold === "string" ? parseThreshold(value.threshold) : null;
                        if (value.threshold === null || parsed !== null) return {enabled: value.enabled, threshold: parsed};
                    }
                } catch (_) { /* Fall through to the neutral default. */ }
                return {enabled: false, threshold: null};
            }
            const legacy = localStorage.getItem(legacyKey);
            const parsed = legacy === null ? null : parseThreshold(legacy);
            // The old module only saved after a user edited the selector.
            return {enabled: parsed !== null, threshold: parsed};
        } catch (_) { return null; }
    }

    function sync() {
        const saved = preference();
        if (saved === null) return;
        enabled = saved.enabled;
        threshold = saved.threshold;
        controls();
    }

    function update(value, sampleBytes) {
        latest = Number.isFinite(value) && value >= 0 && value <= 8 ? value : null;
        bytes = latest !== null && Number.isSafeInteger(sampleBytes) && sampleBytes > 0 ? sampleBytes : null;
        paint();
    }

    function setThreshold(raw) {
        const parsed = parseThreshold(String(raw).trim());
        if (parsed === null) {
            invalid = true;
            paint();
            return false;
        }
        threshold = parsed;
        invalid = false;
        input.value = threshold.toFixed(6);
        save();
        paint();
        return true;
    }

    function setEnabled(value) {
        enabled = value === true;
        controls();
        save();
    }

    input.addEventListener("change", () => { setThreshold(input.value); });
    toggle.addEventListener("change", () => { setEnabled(toggle.checked); });
    window.addEventListener("storage", event => {
        if (event.key === storageKey || event.key === legacyKey || event.key === null) sync();
    });
    window.addEventListener("pageshow", sync);
    window.addEventListener("entropylanguagechange", paint);
    sync();
    controls();
    window.EntropyShannonAlert = {update, setThreshold, setEnabled};
})();
