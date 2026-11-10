# Adaptive client terminal layout

**80 columns is a compatibility size, not a maximum.** The previous defaults
used 60 history points and 32 two-column waterfall cells; even the former
explicit 128-cell limit could exceed 80 columns. The adaptive dashboard uses
available horizontal space while preserving a compact alternative.

```sh
# Auto: adjacent panels on a sufficiently large interactive terminal.
./bin/entropy-client -mode=stream -dashboard -waterfall

# Explicit full view, automatic waterfall dimensions.
./bin/entropy-client -mode=stream -layout=full -waterfall \
  -waterfall-columns=0 -waterfall-rows=0

# Keep stacked output at every terminal size.
./bin/entropy-client -mode=stream -layout=compact -dashboard -waterfall
```

## Selection and presentation

| Layout | Behavior |
| --- | --- |
| `auto` (default) | Full panels on supported interactive terminals at least 120×30; compact otherwise. |
| `full` | Request panels down to 80×24; smaller/unknown/noninteractive terminals still use compact output. |
| `compact` | Stacked diagnostics and optional panels at every size. |

Linux terminal dimensions are read on every redraw. Resizing changes the
visible area of retained history and switches automatic layouts on the next
redraw. Ctrl+C retains graceful shutdown. No raw keyboard mode, alternate
screen or UI library is used; the terminal profile owns the background and
default text colors. `-color` and `NO_COLOR` retain their existing meanings.

Full view places sample/reception data and analysis/display settings on the
left, sampled histories and the existing byte histogram on the right, and
an optional waterfall across the remaining width below. Histories and the
histogram appear automatically in full mode. They reuse existing diagnostics;
no additional analysis pass, network request or statistical test is added.
Optional graph, preview, debug and matrix flags still select their data;
expensive matrix computation remains explicitly requested.

One history point represents one selected sample, newest at the right.
Graph scales are labelled: Shannon 7.995–8 bits/byte, p-values 0–1 and serial
correlation −0.02–0.02. Values outside these ranges clamp visually; numerical
diagnostics retain their actual values. Histogram bars share a peak scale,
show exact counts, and cover eight byte values per bin.

Full output leaves one row free to avoid scrolling. Overloaded panels may
be clipped: `~` marks truncated text and the footer reports clipping. Enlarge
the terminal, disable panels or use compact scrollback for the selected
output. Missing waterfall bytes remain blank. Explicit positive waterfall
dimensions are honored; omit them or set zero for automatic fitting.

## Bounds and compatibility

| Resource/setting | Bound or behavior |
| --- | --- |
| Full viewport | At most 320 columns × 96 rows; additional terminal space is intentionally unused. |
| Interactive auto/full redraw | At most 10 Hz; the default one-second interval is unchanged. Reception and analysis cadence are independent. Compact retains its requested interval. |
| Statistic histories | At most 256 selected points; compact displays up to 60 recent points, reduced further on narrow terminals. |
| Waterfall | Up to 256 cells × 64 samples; compact automatic presentation remains 32 cells × 8 visible rows. Full view fits available space. |
| Retained waterfall payload | At most 49,152 bytes (48 KiB) in RGB mode, plus bounded metadata/graph arrays. Rendered strings and Go heap overhead are additional. |
| Automatic dimensions | New defaults: `-waterfall-columns=0 -waterfall-rows=0`. Positive limits: columns 8–256, rows 1–64. Auto-capable sessions retain larger bounded prefixes/history for later enlargement. |
| JSONL | Additive `settings.layout`; zero dimensions record automatic requests, not effective rendered dimensions. Statistics and sample selection are unchanged. |
| Headless/pipes | No headless display histories; `-headless -report=- -layout=full` remains JSONL. Pipes, unknown dimensions and `TERM=dumb` never receive a cursor-based full view, even with forced colors. |
| Platform | Linux sizing is validated. Other platforms retain their existing unknown-size/unstyled fallback; cross-building does not validate terminal behavior on those systems. |

## Trade-offs and criticism

A larger dashboard groups related information and makes use of the screen.
It also sends more output, allocates rendering buffers and can flicker on
slow links. Synchronous terminal writes can reduce reception throughput;
bounded transport queues apply backpressure rather than silently discarding
received frames for animation. Use headless reports for load measurements.
On slow SSH links, reduce visible history, choose 256 colors, lengthen
`-display-interval`, or choose compact mode.

This renderer redraws complete frames. A captured 240×60 byte-hue frame was
about 63 KiB of ANSI/text output; one frame per second is roughly 0.5 Mbit/s
before transport overhead or compression, and higher refresh rates multiply
that cost. On this Linux amd64/Go 1.26.10 runner, the 240×60 RGB renderer
microbenchmark took about 3.1 ms per frame and allocated about 0.67 MiB of
temporary Go memory. It excludes terminal I/O and is not an application
throughput guarantee. Partial-screen updates could reduce output traffic,
with more terminal-state handling and testing.

Dense screens can be harder to scan, and screenshots can conceal clipping
or imply more visible history than the viewport actually contains. Numerical
counters, scale labels, clipping indicators and the compact option make
those limits visible. This dashboard uses existing command-line controls;
it does not add keyboard menus or mouse capture.

Clipping/padding supports the generated single-column labels, numbers and
spark glyphs and preserves ANSI sequences. It is not a general Unicode
terminal engine for arbitrary wide/combining text. A size change during a
write may briefly wrap the old frame; the next redraw uses the updated size.

## Preview and validation

![240×60 adaptive client layout](images/client-fullscreen.png)

This is actual client output from a local deterministic HTTP fixture in a
240×60 Linux PTY, rasterized onto a dark terminal profile. Its predictable
byte cycle has H=8: this demonstrates layout, not source quality or throughput.
The waterfall shows 115 cells in this viewport; retained prefixes remain
bounded independently.

```sh
make build-client
go test ./cmd/entropy-client/...
go test -race ./cmd/entropy-client/...
python3 scripts/test_client_terminal.py --capture-full=/tmp/client-full.ansi
go test ./cmd/entropy-client -run '^$' -bench BenchmarkFullMonitor240x60 -benchmem
```

Go tests cover small/large layouts, colors, ANSI-safe clipping, histogram
counts, retained history, waiting/overload states, viewport caps and unchanged
analysis accounting. PTY checks cover 60×24, 80×24, 120×30, 160×45 and 240×60,
a live 80×24 → 160×45 → 80×24 transition, redirected output and headless JSONL.
The renderer benchmark excludes terminal I/O and cannot predict SSH throughput.
