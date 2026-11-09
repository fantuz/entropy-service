# Statistical tests

For PractRand, NIST STS, TestU01, measured costs across all three backends
and a bounded manual GitHub Actions workflow, see the
[screening protocol and recorded results](randomness-screening.md).
These are statistical diagnostics, not a standards-compliance certificate.

Use `rngtest` for a quick regression screen and Dieharder for a broader statistical battery. These tests assess statistical behaviour, not maximum throughput or cryptographic certification. The HTTP examples test the **served DRBG output**, not the QRNG device before your software generator.

On Debian/Ubuntu, install the tools if needed:

```sh
sudo apt install curl rng-tools5 dieharder
```

Run the examples in **Bash**, with the server already running. Replace the address if it runs elsewhere:

```bash
set -o pipefail
ENTROPY_URL='http://127.0.0.1:8080/v1/data/random'
```

## Quick screen with rngtest

```bash
curl -fsS --max-time 60 "${ENTROPY_URL}?bytes=25000004" | rngtest -c 10000 -b 1000
```

This requests about 23.84 MiB: 10,000 blocks of 20,000 bits, plus four initialisation bytes, within the default 32 MiB response limit. It reports every 1,000 blocks. If you lowered `-max-bytes`, reduce the block count and request size together: `bytes = 2500 × blocks + 4`.

The five legacy checks are monobit, poker, runs, long runs and continuous run; they are not five separate cryptographic standards. Confirm that successes plus failures totals 10,000. An occasional rejected block can occur with good random data; exit status 1 means at least one block was rejected. See the [rngtest manual](https://github.com/nhorman/rng-tools/blob/master/rngtest.1.in).

## Broader battery with Dieharder

Dieharder needs a continuing supply of fresh input. Do not loop a small capture: file-input mode can rewind and reuse data, making results suspect. Define this feeder once; it downloads a fresh 1 MiB response before forwarding it, so calculation pauses do not hold an HTTP download open:

```bash
entropy_stream() (
    entropy_chunk=$(mktemp) || exit
    trap 'rm -f "$entropy_chunk"' EXIT
    while :; do
        curl -fsS --max-time 30 \
            "${ENTROPY_URL}?bytes=1048576" \
            -o "$entropy_chunk" || exit
        cat "$entropy_chunk" || break
    done
)
```

The temporary file is overwritten with fresh data each cycle and removed on exit. Run the full default battery with this one-liner:

```bash
entropy_stream | dieharder -g 200 -a
```

`-g 200` selects raw binary stdin; `-a` runs the default battery. Allow substantial runtime and data consumption. For a shorter first run, replace `-a` with `-d sts_monobit`. To inspect available tests, use `dieharder -l`.

An isolated `WEAK` result is not proof of a defect. Repeat suspicious tests with fresh input and the same parameters, and compare with an OS-source baseline rather than rerunning until everything passes. Check for premature `EOF` and missing results; exit status alone does not establish completion. See the [Dieharder manual](https://manpages.debian.org/testing/dieharder/dieharder.1.en.html).

For a baseline or direct device check, run the same test with input redirected from `/dev/urandom` or, on the machine with the QRNG and read permission, `/dev/qrandom0`:

```bash
dieharder -g 200 -d sts_monobit < /dev/urandom
dieharder -g 200 -d sts_monobit < /dev/qrandom0
```

Feed each utility the original bytes; do not place `rngtest -p` before Dieharder, because that filters out rejected blocks. Passing statistical tests cannot certify unpredictability or replace cryptographic review; see [NIST SP 800-22](https://csrc.nist.gov/pubs/sp/800/22/r1/upd1/final).

[Back to README](../README.md) · [Technical reference index](README.md)
