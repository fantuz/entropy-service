"use strict";

// Optional views share the existing worker and frozen sample. Display filters
// never replace the original selected buffer, statistics or export payload.
(() => {
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
                    $("structure-scope").textContent = data ? "Analysing the selected sample…" : "Awaiting selected bytes.";
                    for (const id of ["structure-correlation", "structure-transitions", "structure-blocks", "spectrogram"]) EntropyStructurePlots.empty($(id), "Awaiting analysis");
                    for (const id of ["correlation-info", "transition-info", "local-info", "spectrogram-info"]) $(id).textContent = "Awaiting analysis.";
                } else {
                    const {correlation, transitions, blocks} = result;
                    $("structure-scope").textContent = `Selected ${result.inputBytes.toLocaleString()} bytes · structure prefix [${data.offset.toLocaleString()}, ${(data.offset + result.analyzedBytes).toLocaleString()}) · correlation uses first ${correlation.count.toLocaleString()} bytes.`;
                    EntropyStructurePlots.correlation($("structure-correlation"), correlation);
                    EntropyStructurePlots.transitions($("structure-transitions"), transitions);
                    EntropyStructurePlots.blocks($("structure-blocks"), blocks, {offset: data.offset});
                    $("correlation-info").textContent = correlation.defined ? `${correlation.maxLag} lags · global-mean autocovariance / full-window variance sum · ${correlation.count.toLocaleString()} bytes.` : "Undefined: the selected prefix is constant. This is not a zero-correlation result.";
                    $("transition-info").textContent = `${transitions.pairs.toLocaleString()} sliding pairs · ${transitions.bins} × ${transitions.bins} bins · ${transitions.binWidth} byte values/bin · per-capture logarithmic brightness.`;
                    $("local-info").textContent = `${blocks.count} complete blocks × ${blocks.blockSize.toLocaleString()} bytes · ${blocks.coveredBytes.toLocaleString()} covered bytes · ${blocks.omittedBytes.toLocaleString()} trailing bytes omitted.`;
                    const view = EntropyStructurePlots.spectrogram($("spectrogram"), spectral.rows, {kind, floor: fftScale.floor, ceiling: fftScale.ceiling});
                    $("spectrogram-info").textContent = `${view?.rows || 0} / ${spectral.capacity} sampled rows · newest at top · ${kind === "file" ? "file-window offsets" : "actual capture ages, irregular spacing in time"} · ${fftScale.floor ?? -60}…${fftScale.ceiling ?? 50} dB · cycles/byte.`;
                }
            }
            if (audio.open) {
                const result = data?.audio;
                if (!result) {
                    $("audio-info").textContent = data?.audioReason || (data ? "Analysing PCM interpretation…" : "Choose microphone/line-in or declare a PCM capture to inspect it.");
                    $("audio-filter-info").textContent = "No PCM comparison available. Byte statistics and exports remain unchanged.";
                    EntropyAudioPlots.empty($("audio-wave"), "Awaiting valid PCM input");
                    EntropyAudioPlots.empty($("audio-spectrum"), "Awaiting valid PCM input");
                } else {
                    EntropyAudioPlots.waveform($("audio-wave"), result);
                    EntropyAudioPlots.spectrum($("audio-spectrum"), result, {maxHz: Math.min(result.sampleRate / 2, $("pcm-zoom").value === "nyquist" ? result.sampleRate / 2 : Number($("pcm-zoom").value))});
                    const raw = result.raw;
                    const rateKind = data.audioRequest.declared ? "declared" : "captured";
                    $("audio-info").textContent = `PCM16LE mono · ${result.sampleRate.toLocaleString()} ${rateKind} samples/s · ${result.sampleCount.toLocaleString()} decoded samples (${result.analyzedBytes.toLocaleString()} bytes) · raw RMS ${level(raw.rms)} · peak ${level(raw.peak)} · DC ${raw.dc.toFixed(6)} FS · ${raw.clipped.toLocaleString()} samples at PCM rails.`;
                    const fft = raw.spectrum;
                    $("audio-filter-info").textContent = `${result.filtered ? `Raw (solid) + ${result.filtered.notchHz} Hz notch (dashed), Q=${result.filtered.q}; filter restarts per window, startup transients possible.` : "Raw only (solid)."} ${fft ? `Hann ${fft.size.toLocaleString()} points, Δf ${fft.binWidthHz.toFixed(2)} Hz; FFT uses decoded samples ${fft.startSample}–${fft.startSample + fft.size - 1}.` : "At least 32 decoded samples are needed for the spectrum."} ${result.truncated ? "Prefix capped at 32,768 samples. " : ""}Comparison only; no playback or changes to bytes, statistics or exports.`;
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
