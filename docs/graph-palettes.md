# Graph colour palettes

The stream, uploaded-file, Source LAB and Socket LAB analysis pages have a
**Graph colours** selector. Choose the combination that is easiest for you to
distinguish; the light/dark interface setting remains independent.

| Choice | Display |
| --- | --- |
| Classic · green / cyan | The existing green/cyan graph style. This remains the default. |
| Blue / orange | Blue primary marks and orange comparison marks, with shades adapted to the light/dark background. |
| High contrast · monochrome | Dark marks on the light interface; light marks on the dark interface. Audio comparisons keep solid/dashed lines, and bit balance adds a patterned one-bit wedge. |

The alternative hues and monochrome pattern offer different ways to distinguish
marks. Labels, numbers, axes and line styles support the choice.

## What changes

The selected palette affects byte traces/histograms, the ordinary spectrum,
bit-balance graph/meters, dependence and transition plots, local statistics,
spectral history, and PCM waveform/spectrum comparisons. Frozen plots redraw
immediately from the existing analysed result. Selecting a palette does not
request new source bytes, repeat the worker calculation or add history rows.

Audio still labels raw data with solid lines and notch-filtered comparisons
with dashed lines. Changing colours does not change a filter or its numerical
output. The source bytes, histogram counts, entropy values, counters and exported
bytes remain unchanged.

## Raw byte colours keep their meaning

The raw-colour waterfall's **RGB** and **Vivid hue** modes map actual byte values
to display colours. Their mappings remain unchanged when selecting a graph
palette. Camera previews and generated colour/image endpoints also retain their
data colours.

For a colour-independent view of the byte waterfall, choose **Grayscale** or
**Bit plane** in its existing controls. These are separate from the graph
palette and preserve their existing 0/255 and zero/one meanings. Missing bytes
remain blank. A different display mapping does not change the analysed bytes.

## Preference storage

The preference is saved in browser `localStorage` under
`entropy-graph-palette`, as one plain string:

```text
classic
blue-orange
contrast
```

No new cookie is added. Storage is scoped to the origin: scheme, hostname and
port. For example, `http://localhost:8080` and `https://localhost:8443` have
separate preferences. An absent or invalid stored value selects Classic.

Other windows on the same origin synchronize the choice. A restored, refocused
or newly visible page rereads the latest stored value rather than replaying an
older queued storage-event payload. The selector and plot colours follow the
same value. If browser policy blocks storage, the selector still works within
the current page; persistence across navigation/reload is unavailable.

## Desktop canvas width

On desktop viewports from 1,100 pixels wide, the application uses 12-pixel
outer gutters and lets main panels, homepage/gallery/status grids and graph
cards grow with the viewport, up to 144 rem (2,304 pixels at the default text
size). Rectangular plots gain horizontal detail while retaining their existing
heights and column layout. Ordinary explanatory prose keeps a readable line
length rather than stretching across the screen. Existing mobile layouts,
playback widgets and the compact circular bit-balance plot retain their bounds.

## Validation

The focused browser suite checks all three graph palettes in light/dark modes,
including paused redraws, unchanged worker jobs/statistics/history/raw exports,
literal waterfall pixel colours, audio line styles, mobile layout, 1,440/1,920/
2,560-pixel desktop panel/plot widths and readable prose, navigation,
cross-window synchronization and unavailable/invalid storage:

```sh
python3 scripts/test_graph_palettes_browser.py --browser=chromium
python3 scripts/test_graph_palettes_browser.py --browser=firefox
```

Build `bin/entropy-server` first and install Playwright with the selected browser.
The tests start a disposable loopback server with public deterministic fixtures;
they do not inspect an existing installation or constitute a user study.

[Browser workspace](browser-ui.md) · [Visual diagnostics](visual-diagnostics.md)
