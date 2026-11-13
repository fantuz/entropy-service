# LAB access and device-read safety

Registered LAB sources can reach operator-configured private services and
devices. They are diagnostic inputs, separate from the server's seed source.
These controls restrict that bridge; they do not add authentication to the
ordinary public generation APIs or change DRBG/reseed behavior.

## Inbound Host trust

Loopback peer checks alone do not establish which browser origin owns a request.
A controlled server-side test routed an attacker-selected `rebound.invalid`
hostname to a local fixture while keeping HTTP Host and WebSocket Origin equal.
Previously the catalog and bridge accepted it: the peer was loopback and the
normal WebSocket same-origin comparison passed. The fixture could read an
already registered source. It could not select arbitrary URLs or file paths.

This reproduces the server-side conditions of DNS rebinding. It is not evidence
of a working exploit in a particular browser: DNS caching and browser private
network policy also matter. TLS hostname verification can stop this pattern on
WSS, but enabling HTTPS does not remove the parallel HTTP listener. Checking
the actual Host closes the server-side gap independently of those defenses.

With the default `-lab-allow-remote=false`:

- `/lab/stream` requires a loopback TCP peer and a trusted actual HTTP Host.
- Trusted defaults are exact `localhost` and literal loopback IPv4/IPv6
  addresses, with an optional valid port. `localhost.evil.test` is not trusted.
- `/lab/sources` rejects untrusted Hosts with 403 before returning the catalog.
  A trusted Host from a nonloopback peer still returns the catalog with
  `allowed:false`; names/kinds/limits are public in that response, endpoints
  remain private. Catalog metadata is not an authentication boundary.
- Unknown Hosts are rejected before bridge reservations, upgrades or upstream
  reads. `Forwarded` and `X-Forwarded-*` cannot authorize a request.

For an intentional local alias:

```sh
entropy-server -lab-sources=lab-sources.json -lab-allowed-hosts=lab.example.test
```

The comma-separated list accepts at most 32 exact DNS names or bare IPs, without
URLs, ports, wildcards or IPv6 zones. Names are case-insensitive; a trailing DNS
root dot is normalized. Bracket IPv6 addresses in request URLs, but use bare
addresses in this flag. No DNS lookup grants trust. Only add names whose
authority you control; an allowlist entry does not make hostile DNS safe.
Adding a name does not permit remote TCP clients. Local users can instead
browse `http://localhost:8080` or their configured HTTPS loopback endpoint.

`-lab-allow-remote=true` preserves the explicit remote opt-in, including arbitrary
Hosts. The existing WebSocket Origin comparison still applies in either mode;
an allowed Host does not authorize a different Origin. Neither option is user
authentication. Put remote LAB routes behind an authenticated, access-controlled
boundary. A loopback reverse proxy is a loopback peer: preserve/validate its Host
and restrict these routes at the proxy. Rewriting every public Host to localhost
would defeat the intended inbound Host boundary.

Browser-only generators remain usable if the registered-source catalog is
restricted. No key, endpoint or payload bytes are added to access-error logs.

## Device-read ownership and cancellation

The earlier seed-input, upstream-handshake and shutdown cancellation fixes cover
different adapters. Their tests remain in place. The separate Linux LAB raw
device reader held one mutex across `unix.Read` and `Close`. A native read that
did not honor nonblocking semantics could keep `Close` waiting. Normal FIFO
timeouts passed, which is why that test did not prove the broader contract.
Simply releasing the mutex around a raw descriptor would risk descriptor reuse.

LAB now uses `os.File` ownership and the Go runtime poller for supported reads:

- Reads and deadline resets serialize; `Close` never takes the read mutex.
  `os.File` owns descriptor references and wakes supported pending reads safely.
- Each pollable read has its absolute deadline and a context-cancellation
  callback. Cleanup stops or joins the callback before clearing the deadline
  and allowing the next read. Cancellation therefore need not destroy a source
  handle that will be reused.
- FIFOs and character devices must support runtime read deadlines. The only
  nonpollable character exceptions are Linux memory devices identified from
  the opened descriptor: null, zero, full (zero reads), and initialized urandom.
  OS random devices also require a successful nonblocking initialization check.
  Path aliases do not confer an exception. Unsupported drivers fail explicitly;
  use a qualified driver or a separately supervised socket helper.
- Reads use bounded chunks. Context and absolute time are checked before and
  after reads. The shared Stream wrapper also checks after a successful read,
  discards/clears late samples and reports cancellation/deadline expiry. Timely
  partial EOF still returns the available bytes and EOF.

Regular files remain supported. A native filesystem call can still block, and
a broken kernel driver can misrepresent its nonblocking/poll behavior. Userspace
cannot guarantee cancellation inside arbitrary kernel code. There is no
abandoned background reader to conceal such a stall; physical driver behavior
needs [hardware qualification](hardware-qualification.md). This change does not
certify a device, alter seeding, or promise interruption of every native call.

## Regression and release evidence

The controlled Host test failed against the previous implementation and now
requires catalog/upgrade rejection with zero upstream reads and reservations.
Additional tests preserve explicit aliases, remote opt-in and Origin rejection;
malformed Hosts/allowlists and spoofed forwarded headers are covered. These run
on Linux and native Windows CI; Linux-only device code stays platform-scoped.

Real Linux FIFO tests exercise `Close` without cancelling the parent, parent
cancellation, repeated timeout/read reuse and deadline reset. A nonpollable
handle with synthetic unknown-device identity tests rejection/descriptor cleanup;
it is not a physical-driver result. Stream tests reject complete late samples
and expired absolute budgets even before a context timer is dispatched. Existing
file/random/EOF, network, seed-input and shutdown tests remain required.

The same short [endurance suite](endurance-tests.md) now runs through a reusable
workflow called by both build and release. Release publication depends on its
Linux race and native Windows jobs for the tagged commit. Manual longer runs
remain independent; a short CI pass is not a long-duration stability guarantee.
