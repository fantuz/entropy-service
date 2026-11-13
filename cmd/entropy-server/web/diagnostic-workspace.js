"use strict";

// Optional views share the existing worker and frozen sample. Display filters
// never replace the original selected buffer, statistics or export payload.
(() => {
    const tr = (message, params = {}) => globalThis.window?.EntropyI18n?.t(message, params) ??
        message.replace(/\{(\w+)\}/g, (_, key) => Object.hasOwn(params, key) ? String(params[key]) : `{${key}}`);
    function text(element, message, params = {}) {
        if (globalThis.window?.EntropyI18n) window.EntropyI18n.set(element, message, params);
        else element.textContent = tr(message, params);
    }
    const audioReasons = new Set([
        "Choose Original bytes for PCM audio interpretation; transformed bytes are not the captured PCM signal.",
        "Declare a sample rate from 8,000 to 384,000 Hz.",
        "Choose microphone/line-in with Original bytes, or explicitly declare a PCM16LE mono capture and its sample rate.",
        "Select an even byte offset and an even window of at least two bytes for PCM16LE.",
        "Audio analysis needs an even 2–1,048,576 PCM16LE bytes",
        "Audio analysis needs explicit PCM16LE mono metadata and an 8,000–384,000 Hz sample rate",
        "Choose Off, 50 Hz or 60 Hz notch comparison",
        "Notch frequency must be below the sample Nyquist frequency"
    ]);
    window.EntropyDiagnostics = function createDiagnostics({kind, requestAnalysis, redraw}) {
        const $ = id => document.getElementById(id);
        const structure = $("structure-diagnostics"), audio = $("audio-diagnostics");
        const spectral = new EntropyStructure.SpectralHistory();
        let latest = null, clearedFrame = null;
        const identity = data => JSON.stringify([data?.sourceId, data?.frame, data?.captureTime]);
        function options(metadata) {
            const result = {structureOptions: null, audioRequest: null};
            if (structure.open) result.structureOptions = {lags: Number($("correlation-lags").value),
                bins: Number($("transition-bins").value), blockSize: Number($("local-block-size").value)};
            if (audio.open) {
                let reason = "", format = metadata?.audioMetadata;
                if (metadata?.representation && metadata.representation !== "raw") reason = "Choose Original bytes for PCM audio interpretation; transformed bytes are not the captured PCM signal.";
                else if ($("pcm-mode").value === "manual") {
                    if (!$("pcm-rate").checkValidity()) reason = "Declare a sample rate from 8,000 to 384,000 Hz.";
                    format = {format: "pcm16le", channels: 1, sampleRate: Number($("pcm-rate").value)};
                } else if (!format) reason = "Choose microphone/line-in with Original bytes, or explicitly declare a PCM16LE mono capture and its sample rate.";
                result.audioRequest = {metadata: format, notch: $("pcm-notch").value, declared: $("pcm-mode").value === "manual", reason};
            }
            return result;
        }
        function accept(data) {
            latest = data;
            if (structure.open) {
                if (clearedFrame === identity(data)) return;
                clearedFrame = null;
                const key = JSON.stringify([data.sourceId, data.representation, kind === "file" ? "file" : data.offset,
                    data.analyzedBytes, data.fftOptions, data.fft?.size]);
                spectral.add(data.fft, {key, frame: data.frame, captureTime: data.captureTime, offset: data.offset});
            }
        }
        function level(value) { return value > 0 ? (20 * Math.log10(value)).toFixed(1) + " dBFS" : "−∞ dBFS"; }
        function draw(data, fftScale = {}) {
            if (structure.open) {
                const result = data?.structure;
                if (!result) {
                    text($("structure-scope"), data ? "Analysing the selected sample…" : "Awaiting selected bytes.");
                    for (const id of ["structure-correlation", "structure-transitions", "structure-blocks", "spectrogram"]) EntropyStructurePlots.empty($(id), "Awaiting analysis");
                    for (const id of ["correlation-info", "transition-info", "local-info", "spectrogram-info"]) text($(id), "Awaiting analysis.");
                } else {
                    const {correlation, transitions, blocks} = result;
                    text($("structure-scope"), "Selected {input} bytes · structure prefix [{start}, {end}) · correlation uses first {count} bytes.", {input: result.inputBytes.toLocaleString(), start: data.offset.toLocaleString(), end: (data.offset + result.analyzedBytes).toLocaleString(), count: correlation.count.toLocaleString()});
                    EntropyStructurePlots.correlation($("structure-correlation"), correlation);
                    EntropyStructurePlots.transitions($("structure-transitions"), transitions);
                    EntropyStructurePlots.blocks($("structure-blocks"), blocks, {offset: data.offset});
                    text($("correlation-info"), correlation.defined ? "{lags} lags · global-mean autocovariance / full-window variance sum · {count} bytes." : "Undefined: the selected prefix is constant. This is not a zero-correlation result.", {lags: correlation.maxLag, count: correlation.count.toLocaleString()});
                    text($("transition-info"), "{pairs} sliding pairs · {bins} × {bins} bins · {width} byte values/bin · per-capture logarithmic brightness.", {pairs: transitions.pairs.toLocaleString(), bins: transitions.bins, width: transitions.binWidth});
                    text($("local-info"), "{blocks} complete blocks × {size} bytes · {covered} covered bytes · {omitted} trailing bytes omitted.", {blocks: blocks.count, size: blocks.blockSize.toLocaleString(), covered: blocks.coveredBytes.toLocaleString(), omitted: blocks.omittedBytes.toLocaleString()});
                    const view = EntropyStructurePlots.spectrogram($("spectrogram"), spectral.rows, {kind, floor: fftScale.floor, ceiling: fftScale.ceiling});
                    text($("spectrogram-info"), "{rows} / {capacity} sampled rows · newest at top · {scope} · {floor}…{ceiling} dB · cycles/byte.", {rows: view?.rows || 0, capacity: spectral.capacity, scope: tr(kind === "file" ? "file-window offsets" : "actual capture ages, irregular spacing in time"), floor: fftScale.floor ?? -60, ceiling: fftScale.ceiling ?? 50});
                }
            }
            if (audio.open) {
                const result = data?.audio;
                if (!result) {
                    if (data?.audioReason) text($("audio-info"), audioReasons.has(data.audioReason) ? data.audioReason : "{error}", {error: data.audioReason});
                    else text($("audio-info"), data ? "Analysing PCM interpretation…" : "Choose microphone/line-in or declare a PCM capture to inspect it.");
                    text($("audio-filter-info"), "No PCM comparison available. Byte statistics and exports remain unchanged.");
                    EntropyAudioPlots.empty($("audio-wave"), "Awaiting valid PCM input");
                    EntropyAudioPlots.empty($("audio-spectrum"), "Awaiting valid PCM input");
                } else {
                    EntropyAudioPlots.waveform($("audio-wave"), result);
                    EntropyAudioPlots.spectrum($("audio-spectrum"), result, {maxHz: Math.min(result.sampleRate / 2, $("pcm-zoom").value === "nyquist" ? result.sampleRate / 2 : Number($("pcm-zoom").value))});
                    const raw = result.raw;
                    const rateKind = tr(data.audioRequest.declared ? "declared" : "captured");
                    text($("audio-info"), "PCM16LE mono · {rate} {rateKind} samples/s · {count} decoded samples ({bytes} bytes) · raw RMS {rms} · peak {peak} · DC {dc} FS · {clipped} samples at PCM rails.", {rate: result.sampleRate.toLocaleString(), rateKind, count: result.sampleCount.toLocaleString(), bytes: result.analyzedBytes.toLocaleString(), rms: level(raw.rms), peak: level(raw.peak), dc: raw.dc.toFixed(6), clipped: raw.clipped.toLocaleString()});
                    const fft = raw.spectrum;
                    text($("audio-filter-info"), "{filter} {spectrum} {truncation}Comparison only; no playback or changes to bytes, statistics or exports.", {
                        filter: result.filtered ? tr("Raw (solid) + {notch} Hz notch (dashed), Q={q}; filter restarts per window, startup transients possible.", {notch: result.filtered.notchHz, q: result.filtered.q}) : tr("Raw only (solid)."),
                        spectrum: fft ? tr("Hann {points} points, Δf {width} Hz; FFT uses decoded samples {start}–{end}.", {points: fft.size.toLocaleString(), width: fft.binWidthHz.toFixed(2), start: fft.startSample, end: fft.startSample + fft.size - 1}) : tr("At least 32 decoded samples are needed for the spectrum."),
                        truncation: result.truncated ? tr("Prefix capped at 32,768 samples. ") : ""});
                }
            }
        }
        for (const panel of [structure, audio]) panel.addEventListener("toggle", () => {
            if (!panel.open && panel === structure) { spectral.clear(); clearedFrame = null; }
            requestAnalysis(); redraw();
        });
        for (const id of ["correlation-lags", "transition-bins", "local-block-size", "pcm-notch", "pcm-rate"]) $(id).addEventListener("change", requestAnalysis);
        $("pcm-mode").addEventListener("change", () => { $("pcm-rate").disabled = $("pcm-mode").value !== "manual"; requestAnalysis(); });
        $("pcm-zoom").addEventListener("change", redraw);
        $("clear-spectral-history").addEventListener("click", () => { spectral.clear(); clearedFrame = identity(latest); redraw(); });
        return {options, accept, draw, clear() { spectral.clear(); latest = null; clearedFrame = null; }};
    };
})();
