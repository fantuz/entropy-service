# Structure, spectral history and audio diagnostics

The stream, uploaded-file, Source LAB and Socket LAB pages share two optional
sections: **Structure & dependence** and **PCM audio**. Both start closed.
Opening a section analyses the selected window in the existing browser worker;
closing it avoids its extra work on subsequent samples. Existing histograms,
Shannon values, source bytes and downloads retain their original meaning.

## Structure and dependence

| View | What to look for | Scope and interpretation |
| --- | --- | --- |
| Autocorrelation | Peaks at regular lags reveal repetition or dependence on preceding bytes. | First 8,192 selected bytes, up to 32/64/128 lags. Correlation is undefined for a constant sample; that is reported explicitly. |
| Adjacent-byte transitions | A diagonal or concentrated cells show which byte ranges tend to follow others. | Sliding adjacent pairs in the first 65,536 selected bytes. Choose 16×16 or 32×32 bins; brightness uses log count relative to the largest cell in this sample. |
| Local diagnostic strip | A known byte range becoming biased or stuck can stand out despite a healthy-looking whole-window average. | Bit-one fraction and plug-in Shannon entropy for equal blocks of 256/1,024/4,096 bytes within the same 65,536-byte prefix. Incomplete final blocks are omitted and counted in the caption. |
| Spectral waterfall | Persistent lines, intermittent periodic components or changing frequency structure. | Existing per-frame FFT results in cycles/byte, stored as at most 120 rows of 256 peak-reduced frequency bins. This is separate from the existing raw byte-colour waterfall. |

The structure caption reports the actual byte range. Increasing the main
analysis window above 64 KiB does not silently increase these diagnostic
budgets. Correlation has its own smaller prefix, and the captions distinguish
these scopes. The ordinary histogram and Shannon panel still cover the entire
selected analysis window.

For intuition, a repeating counter `0,1,…,255,0,…` has a flat byte histogram and
Shannon estimate of eight bits/byte. Its autocorrelation and transition map
nevertheless reveal structure. Conversely, a uniform-looking transition map
cannot establish independence or unpredictability.

The autocorrelation estimator subtracts the sample's global mean and divides
lagged covariance sums by the full centred sum of squares. The plot's approximate
per-lag reference band describes variation under an independent-sample model;
testing many lags naturally produces chance excursions. It is not a simultaneous
confidence guarantee or a cryptographic pass/fail test. Small-block Shannon
estimates also depend strongly on block size: compare equal block sizes and do
not expect healthy short blocks to reach exactly eight bits/byte.

### Spectral history

Each row describes a distinct analysed frame/window. Frozen reanalysis, theme
changes and resizing do not manufacture additional history. The history resets
when incompatible source/selection/FFT settings change. Closing the structure
section clears its spectral history. **Clear spectral history** clears it without
changing input capture or the raw-colour waterfall; reanalysing that same frozen
frame does not restore a cleared row. A fresh capture/file window can add rows.

Frequency reduction retains the maximum linear PSD value in each group, keeping
narrow peaks visible. The colour scale is a fixed dB range rather than a new
auto-normalization every frame. Peak reduction is a display envelope, not an
average or an integrated-power estimate. The existing FFT windowing/mean-removal
settings apply. For reducing variance in the ordinary spectrum, the existing
**Average power** control offers 1/4/16 distinct frames and averages linear power
before conversion to dB.

History uses actual capture timestamps for stream age labels and byte offsets
for file-window labels. Rows are spaced uniformly per inspected sample, not
uniformly in time; paused or skipped frames can leave real capture-time gaps.
The frequency unit remains **cycles/byte**, not Hz. Network refresh is not the
sample clock of a byte stream.

## PCM audio

**Auto** uses explicit PCM16LE-mono format and sample-rate metadata from the
Microphone/Line-in adapter. It does not guess an audio format from a byte pattern
or a description string. To inspect a known raw PCM file or socket, select the
manual PCM16LE-mono interpretation and declare the actual sample rate. A wrong
declared rate gives a wrong Hz scale. Container headers, compressed audio,
stereo/interleaved PCM and other encodings must be decoded separately first.

The selected byte offset and length must align to complete 16-bit samples.
The automatically supplied rate is the browser audio-context rate; browser or
driver resampling can apply before PCM capture.
An odd offset/length or a transformed LAB representation disables the audio
interpretation with guidance; ordinary byte diagnostics remain usable. Select
the original representation for audio inspection.

The audio view decodes at most 32,768 samples from the selected prefix. Its
waveform shows normalized signed amplitude and milliseconds. Raw RMS, peak,
DC offset and PCM rail counts help distinguish silence, offsets and saturation.
A sample reaching a digital PCM rail is not by itself proof of analog clipping.
Silence stays silent; these views do not add or invent entropy.

The audio spectrum uses the largest fitting power of two up to 8,192 samples,
without zero-padding, from the end of that inspected prefix. It uses Hann
weighting and a one-sided power density in normalized full-scale squared per
Hz, including DC and Nyquist. Its integral gives window-weighted mean-square
amplitude. It is a diagnostic display, not a calibrated audio instrument.

### Optional 50/60 Hz notch comparison

The notch selector overlays a processed waveform and spectrum against the raw
signal. It can help investigate mains interference in a known audio signal.
The biquad uses Q=30 and starts fresh for each inspected window; it is not a
continuous streaming audio processor. Startup transients and short-window
frequency resolution still matter, and the spectrum's trailing window only
reduces their influence when enough samples precede it.

Filtering never replaces source bytes, raw statistics or downloads. It does not
improve or certify a randomness source: filtering arbitrary random bytes can
introduce correlation and hide defects. Hardware entropy conditioning is a
separate design with separate assumptions.

## Validation and cost

The additional calculations run only for open sections and retain bounded
results/history. They share the existing worker queue rather than starting a
new request loop or buffering an unlimited stream. Larger selected FFTs, an open
diagnostic section, browser rendering and device speed can still reduce achieved
display FPS; the 20-FPS control is a ceiling, not a throughput guarantee.

Focused Node tests check numerical oracles and deterministic counter, constant,
repetition, local-fault and PCM fixtures. The browser suite exercises real worker
results, frozen reanalysis, history, file/LAB downloads, PCM alignment and notch
comparison, and responsive light/dark layouts in Chromium and Firefox:

```sh
node scripts/test-structure-math.mjs
node scripts/test-structure-plots.mjs
node scripts/test-audio-analysis.mjs
node scripts/test-audio-plots.mjs
node scripts/test-diagnostic-worker.mjs
node scripts/test-workspace-reanalysis.mjs
python3 scripts/test_diagnostics_browser.py --browser=chromium
python3 scripts/test_diagnostics_browser.py --browser=firefox
```

Build `bin/entropy-server` first; browser checks need Playwright and its browser
binaries. They start a disposable loopback server and use public fixtures, with
no physical audio/RNG qualification. Full regression remains in GitHub CI.

[Browser workspace reference](browser-ui.md) · [Source LAB reference](source-labs.md)
