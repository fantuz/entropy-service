# Randomness screening: scope, cost and recorded results

PractRand and TestU01 are statistical batteries. NIST SP 800-22 describes
statistical tests; running them is not DRBG certification. Z3/Bitwuzla solver
timeouts and avalanche measurements are different experiments, not additional
randomness standards. No finite battery proves that output is random across
all possible tests or establishes cryptographic unpredictability.

## Which tests make sense?

| Check | Useful target | Suggested place |
| --- | --- | --- |
| Published AES/ChaCha known-answer vectors | Primitive correctness and backend wiring | Every CI run; deterministic and very cheap. |
| Seed/reseed fixtures, short reads, limits, child isolation, Close, races | This application's construction and lifecycle | Every CI run, including the optional Barzakh build. |
| PractRand | Bias, correlations and bit-pattern structure across growing streams | Manual bounded screens, followed by planned independent samples. |
| NIST SP 800-22 STS | Statistical behavior of a collection of long bit sequences | Periodic/offline studies; inspect proportions, uniformity and eligibility. |
| TestU01 SmallCrush, then Crush/BigCrush | Additional statistical families and longer-range structure | Start small; reserve full batteries for a dedicated longer study. |
| Strict avalanche criterion | Sensitivity to particular seed/state/input bit flips | Separate experiment with an explicit input/output model and per-bit results. |
| Z3/Bitwuzla | State-recovery hypotheses against a precisely modeled construction | Targeted cryptanalysis, not generic CI or a security-level certificate. |

The AES backend already has a NIST SP 800-38A vector; CI now additionally
checks ChaCha20 against [RFC 8439 section 2.4.2](https://www.rfc-editor.org/rfc/rfc8439#section-2.4.2) through the actual backend
factory. Those checks do not establish conformance of the entire custom
SHA-512 seed/reseed construction to SP 800-90A CTR_DRBG.

SP 800-90B concerns entropy-source assessment and source health tests. Testing
post-DRBG bytes cannot validate a QRNG's raw entropy rate. Such a study needs
the physical source, raw acquisition, restart datasets and an appropriate
source model. No physical QRNG was available for the study below.

NIST's [SP 800-22 page](https://csrc.nist.gov/pubs/sp/800/22/r1/upd1/final)
also records its decision to revise the publication. Treat the suite as a
diagnostic tool, not an endorsement of a generator's security.

## Recorded first pass, 2026-11-09

Tested server revision `0f542cb9703124c4475da4cfbfb6af0d5babdab1` on Linux
amd64, Intel Xeon Platinum 8573C VM, Go 1.26.10. Each backend ran in a separate
local server, explicitly requiring `/dev/urandom`. We captured 1 GiB plus
1 MiB of decoded binary WebSocket output from `/stream?bytes=1048576&refresh=1`.
The extra MiB accommodates tester read-ahead. Frames/HTTP headers were not
included in the data, and no filtering or file rewinding occurred.

**Default 15-second reseeding stayed enabled.** The slower Barzakh capture
crossed multiple reseed epochs, while the fast captures completed before
the first periodic reseed. These results describe served output under that
policy, not an uninterrupted, fixed-seed core-generator test. A backend
research campaign should test both separately over several seeds.

| Backend | PractRand through 1 GiB | SmallCrush | NIST STS, 100 × 1,000,000 bits |
| --- | --- | --- | --- |
| AES-256-CTR | Completed; one interim unusual result | 15/15 statistics passed | 188 summary rows, no flags |
| ChaCha20 | Completed; no reported anomalies | 15/15 statistics passed | 188 summary rows, no flags |
| Go Barzakh-521 experimental | Completed; two unusual results | 15/15 statistics passed | 188 summary rows, no flags; excursion eligibility supplemented below |

PractRand used the official **pre-0.95** archive, core test set, standard
64-bit folding, with checkpoints at 128, 256, 512 and 1024 MiB. No `FAIL`
or `suspicious` evaluations occurred. Preserve these `unusual` evaluations:

- AES at 128 MiB: `Gap-16:A`, `p = 1.0e-3`; later checkpoints had no anomalies.
- Barzakh at 256 MiB: `FPF-14+6/16:all`, `p = 1-4.9e-4`.
- Barzakh at 1 GiB: `[Low16/64]Gap-16:A`, `p = 1.0e-3`.

Those are review signals, not proof of a defect. Many statistics/checkpoints
are examined, and occasional tail values occur under the null hypothesis.
Prespecify additional independent samples and investigate recurring patterns;
do not reroll data until a green report appears. This first pass does not
resolve whether either Barzakh flag would recur in a longer study.

NIST STS **2.1.2** ran all 15 families at their default parameters and
`alpha=0.01`. The 188 rows include 148 non-overlapping-template variants;
they are not 188 independent certifications. The excursion families had
59, 55 and 52 eligible walks respectively. Barzakh's 52 is below the usual
55-sample guidance for the second-level uniformity assessment, despite no
printed flag. Therefore we supplemented **only those two families**, using
200 sequences for every backend. Eligible counts became **119, 120, 119**;
all 26 rows per backend had no flags. Original 100-sequence reports remain
available and are not replaced by the supplement.

TestU01 **1.2.3** used a custom external-generator callback that consumes
successive little-endian 32-bit words. It consumed 907,396,560 bytes for AES,
907,255,716 for ChaCha20 and 908,004,048 for Barzakh. The adapter refuses EOF
instead of recycling data. All 15 statistics completed and passed.

The tools reused portions of the same captured sample. They are complementary
screens, not independent seed trials. SHA-256 sample identifiers, full reports,
commands/tool identities and timing summaries are in
[the result directory](benchmark-results/randomness-2026-11-09/results.json).
The multi-gigabyte captures and untouched original logs remain local and are
not committed. Repository report copies only normalize trailing whitespace.

### Tool validation and limitations

The initial official PractRand 0.94 build aborted with allocator errors on
all three datasets after its first checkpoint. Those runs are **incomplete**,
not generator failures or passes. We retained their error logs and reran the
identical saved input with upstream pre-0.95, which completed. This is a
pre-release tool, not a validated laboratory certification service.

Constant-byte controls produced severe PractRand failures and rejected all
100 NIST frequency-test sequences. We also independently recomputed the first
NIST frequency p-value for each real dataset and verified all 100 results
were present. STS returns **1 even after successful completion**; completion
was established from its console message, result counts and final report,
rather than interpreting exit zero as the success criterion.

## Runtime and computing budget

Observed wall times, with some tasks overlapping on the VM:

| Task | AES | ChaCha20 | Barzakh |
| --- | ---: | ---: | ---: |
| Capture 1025 MiB, including connection cleanup | 4.4 s | 2.4 s | 252.9 s |
| PractRand through 1 GiB | 17.2 s | 21.2 s | 20.4 s |
| SmallCrush on saved input | 6.4 s | 7.6 s | 6.6 s |
| NIST, all families / 100 sequences | 187.2 s | 172.1 s | 183.6 s |
| Excursion-family supplement / 200 sequences | 2.5 s | 2.6 s | 2.5 s |

These are experiment timings, not throughput benchmarks. Generation and
statistical analysis are different costs: NIST's 100-sequence dataset is only
12.5 MB, but its tests use appreciable CPU time. The slower backend determines
data-generation cost. No GPU or paid external testing API is needed.

A 128 TB claim is a very different budget. At the earlier Go Barzakh rate
of 4.77 MB/s, producing 128 decimal TB alone takes about **311 days**;
128 TiB takes about **342 days**, before analysis overhead. Full BigCrush
also requires a much larger workload than SmallCrush and can take hours on
fast generators or potentially days with this scalar backend. It was not
run here. Neither solver experiments nor a full avalanche study were run.

## Repeat a bounded screen

Once the workflow is merged onto the default branch, use **Actions →
Statistical randomness screen → Run workflow**. It tests all three backends
with 64, 256 or 1024 MiB each;
the default is 64 MiB. It is not triggered by ordinary pushes, is not a
required merge gate, and does not add a compliance badge. Checkpoints start
at 64 MiB and double up to the selected target. Reports and sample hashes
are retained for 14 days; raw samples are not uploaded. A completed
job means capture and analysis completed. Statistical flags generate review
warnings and remain in the report, rather than turning the job into a
cryptographic pass/fail certificate.

For a local run, build the desired server and start it on loopback. Barzakh
requires the [explicit experimental build](experimental-barzakh.md). Then:

```sh
python3 -m venv /tmp/entropy-screen-venv
/tmp/entropy-screen-venv/bin/pip install websockets==16.0

# New destination only; official pre-0.95 source archive and digest are pinned.
python3 scripts/build_practrand.py /tmp/entropy-practrand

# 65 MiB provides 64 MiB of test input plus one MiB of read-ahead headroom.
/tmp/entropy-screen-venv/bin/python scripts/capture_randomness.py \
  --output sample.bin --bytes 68157440 --timeout 120

timeout 600 /tmp/entropy-practrand/RNG_test stdin64 \
  -tlmin 64MB -tlmax 64MB < sample.bin > practrand.txt
python3 scripts/assess_practrand.py --manifest sample.bin.json \
  --report practrand.txt --bytes 67108864
```

Capture filenames must be new. The manifest records completion, exact byte
count, sample hash and frame size; URL credentials/query parameters are
omitted. Partial captures are marked incomplete and preserved. Only decoded
binary messages are accepted. The capture and tool runner need Python 3.11+
and a C++ compiler; these optional dependencies do not enter the Go binaries.

To reproduce NIST, obtain the official STS 2.1.2 archive, build its C sources
with `gcc -O2 -fcommon`, and run in a fresh suite directory containing
`templates` and `experiments`. A binary `input.bin` of 12,500,000 bytes is
sufficient for 100 million-bit sequences:

```sh
printf '0\ninput.bin\n1\n0\n100\n1\n' | ./assess 1000000
# For only the two excursion families on 200 sequences, use 25,000,000 bytes:
printf '0\ninput.bin\n0\n000000000001100\n200\n1\n' | ./assess 1000000
```

There is no parameter-adjustment prompt when only excursion families are
selected. Check the complete report and eligible sample counts, not just
the process exit code. For SmallCrush, build external TestU01 1.2.3 at the
recorded revision and link [the stdin adapter](../scripts/smallcrush_stdin.c)
against `libtestu01`, `libprobdist`, `libmylib` and `libm`. Feed the saved file
once; a nonzero EOF result means the battery is incomplete.

[Statistical-test examples](statistical-tests.md) · [Security boundaries](security-boundaries.md)
