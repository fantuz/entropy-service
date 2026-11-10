# Raw client exports: files, pipes and devices

Transport retries are opt-in with `-retries` and preserve the open destination
and total `-output-bytes` limit. See [disconnect handling and retry policy](client-recovery.md).

The client can save a binary sample, feed another application, or send a stream
to another machine through SSH. These are useful for offline analysis and
remote consumers without inventing a new transport protocol. Export writes
received payload bytes in order: no text encoding, JSON, display controls or
frame headers. Analysis sampling and terminal settings do not filter exports.
It does not certify the source or turn software DRBG output into a new physical
entropy source. Use a trusted HTTPS/WSS endpoint for a remote source.

## Destinations

Choose one destination per invocation. Export is off by default.

| Option | Behavior |
| --- | --- |
| `-output=sample.bin` | Create a new private file (mode 0600, subject to umask). Refuse an existing file, symlink, FIFO or device; never append or truncate. Sync and close before the summary. Works on all supported build platforms. |
| `-output=- -headless` | Write raw bytes to stdout, for a pipe or explicit shell redirection. Linux only. Refuse a terminal and refuse `-report=-`; logs stay on stderr and JSONL can use its own file. Shell redirection has the shell's own overwrite/permission rules. |
| `-output-device=/dev/null` | Discard bytes, useful for checking the export path without changing a random pool. Linux only. |
| `-output-device=/dev/random` or `/dev/urandom` | Ordinary Linux pool input with **no entropy credit**; see [write-out value and safeguards](client-writeout.md). Linux only. |
| `-writeout` | Existing compatibility option for validated `/dev/urandom` mixing. Its meaning is unchanged. |

`-output-device` accepts only those three literal paths and validates the actual
opened character-device identity. It opens without creating, truncating or
following a final symlink. It rejects block devices, arbitrary hardware and
other paths before opening them. No extra privileges, entropy-crediting ioctl,
permission changes or shell commands are used by the client.

## Bounded captures and accounting

For an exact 1 MiB file from the local instance:

```sh
./bin/entropy-client -headless -slice=65536 -refresh=100ms \
  -output=sample.bin -output-bytes=1048576 -duration=30s \
  -report=sample.jsonl
```

`-output-bytes=N` stops successfully with `termination: "output_limit"` after
exactly N bytes have been accepted, including a prefix of the final frame when
needed. Zero means export until duration/interruption; **a file or remote
capture can then grow without a size bound**. A duration can end before the
byte limit: if exact size is required, check both `termination` and the count.
Preflight samples and queued frames discarded during shutdown are excluded.
The receiver counts the entire handled final frame even if only its prefix is
exported. A retained file after a failed or interrupted run can be incomplete;
it is deliberately preserved for inspection, never labeled as a complete capture.

JSONL schema version 1 adds `settings.output_kind` (`disabled`, `file`, `stdout`,
`device` or `urandom`) and `settings.output_limit_bytes`. Paths are omitted from
JSONL. Every sample and summary adds:

| Field | Meaning |
| --- | --- |
| `output_written_bytes` | Bytes accepted by any enabled destination, including partial writes and a byte-limited final prefix. |
| `output_written_frames` | Entire received frames exported successfully. A clipped final frame is excluded. |
| `output_write_errors` | Open, write, short-write, sync and close failures; ordinary context cancellation is excluded. |

Legacy `device_*` counters remain and apply only to device destinations,
including `/dev/null`; file/stdout exports leave them zero. `settings.writeout`
still records the original flag. Errors fail the command: open failure before
network activity uses `startup_error`; write/sync/close failure uses
`writeout_error`. The destination closes before the summary. A failed report
write also fails the process; no program can guarantee a final record after
its report destination stops accepting data.

Exports use backpressure and 32 KiB writes with cancellation checks. A slow
consumer reduces reception throughput rather than creating an unbounded export
queue. Linux stdout pipes use cancellable writes, so duration/interruption can
stop a stalled consumer. A regular filesystem syscall, file sync or device call
already in progress has no hard cancellation deadline. These accepted-byte
counters do not prove downstream consumption or remote durability.

## Send and import a stream over SSH

Use an existing SSH account with verified host keys and public-key authentication.
In Bash, `pipefail` checks both the producer and SSH exit statuses:

```bash
set -o pipefail
./bin/entropy-client -headless \
  -url=https://your-trusted-server/v1/data/random \
  -slice=65536 -refresh=100ms -output=- -output-bytes=1048576 \
  -duration=30s -report=export.jsonl |
  ssh -T -o BatchMode=yes user@remote \
    'umask 077; set -C; cat > entropy-sample.bin'
```

SSH encrypts/authenticates the remote hop; `-T` avoids a PTY. The remote shell
creates a private file and `set -C` refuses replacement of an existing regular
file. Use an account-owned destination directory. The client does not execute
SSH itself or store its credentials. A remote application can consume stdin
in place of `cat`; an exported file can be imported with its usual binary input.
Do not use terminal/JSON output as a binary stream.

Check the pipeline status, the JSONL termination/count, and the remote file size
or hash. Successful local pipe writes alone cannot confirm delivery. If mixing
on a deliberately chosen remote Linux host is required instead of a file,
the SSH receiver can be `cat > /dev/urandom`, subject to normal device permissions.
This retains the [limited, uncredited mixing semantics](client-writeout.md);
it does not bootstrap the remote OS RNG or justify running the network client
as root. The server's existing source selection and reseeding remain unchanged.

## Verification

Go tests cover HTTP and WebSocket exports independent of analysis, exact limits,
private files, refusal to replace files/symlinks, open/write/close failures,
blocked and broken pipes, cancellation and descriptor flag restoration.
`python3 scripts/test_client_output.py` tests the built Linux CLI and is run in
CI. It uses predictable fixture bytes and `/dev/null`, without pool injection.
An additional manual loopback OpenSSH check transferred 131,089 exact binary
bytes using a pinned host key, public-key authentication and a remote 0600 file;
both process exit statuses and the received bytes were checked.
