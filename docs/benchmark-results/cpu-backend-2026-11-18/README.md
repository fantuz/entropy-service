# Direct CPU backend measurements, 18 November 2026

See [the backend guide](../../experimental-rdrand.md) for rationale, commands,
results and boundaries. `summary.json` records the host and medians.

The same `cpu_bench_test.go` was copied unchanged into an isolated checkout of
pre-feature main. `before-initial.txt` and `after-initial.txt` contain the first
three 300 ms trials; `*-paired.txt` retain four alternating unrestricted trials.
Their variability motivated six alternating **pinned** trials per software case
at 400 ms (`*-pinned.txt`, excluding the separately measured `cpu-pinned.txt`).
Each process used one Go execution thread and the same lowest allowed logical
CPU. Alternating order limits ordering bias; it does not isolate a virtual host
from frequency changes, scheduling or unrelated host load.

`cpu-pinned.txt` contains three direct RDRAND trials at 400 ms on the feature
implementation. Unsupported selection is skipped by the benchmark on old main.
Times include locks, Go wrappers and metrics, not just instruction latency.
The committed harness is opt-in and is not a CI performance threshold.
