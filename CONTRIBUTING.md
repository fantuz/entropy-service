# Contributing to Entropy Service

Thanks for considering improvements. Contributions in Go, browser UX, documentation, reproducible benchmarks and tests are welcome. Please keep changes focused and preserve the distinction between demonstrated behavior and unverified cryptographic claims.

## Environment

- Go **1.26.10** or newer, Git, and a supported C toolchain for race testing.
- `golangci-lint` **2.12.2** to match current GitHub Actions configuration, where available.
- GoReleaser for local release snapshots, only when packaging changes are involved.
- The server supports local development with `-device=/dev/urandom`; no QRNG card is necessary to run ordinary tests.

## Build and test

```sh
make build
go test ./...
go test -race -count=1 -timeout=10m ./cmd/entropy-server/...
go vet ./...
golangci-lint run --timeout=3m0s
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
```

The race detector requires a supported platform and a working C compiler with cgo enabled. `go test -race ./...` can provide broader coverage before releases. Browser resources are embedded at build time; rebuild and restart the server after editing `cmd/entropy-server/web/`.

## Workflow

Please follow the [Code of Conduct](CODE_OF_CONDUCT.md) in project discussions.
Use the [issue forms](.github/ISSUE_TEMPLATE/) for reproducible bugs, feature
requests or documentation issues when they fit; blank issues remain available.
Do not report security vulnerabilities through public forms.

1. Open a narrowly scoped issue describing the change, where useful.
2. Create a branch; keep production behavior changes separate from large formatting or documentation rewrites.
3. Add or update regression tests for new handlers, limits, cancellation behavior, WebSocket protocols, and browser assets.
4. Run the tests and linter above. Check `git diff --check` before committing.
5. Open a pull request using the [pull request template](.github/pull_request_template.md),
   explaining motivation, compatibility impact, validation performed, and any benchmark context.

Do not commit device secrets, production TLS keys, credentials, or user data. When submitting a security finding, follow [SECURITY.md](SECURITY.md) rather than posting the details publicly.

## Tagged release validation

A `v*.*.*` tag triggers the release workflow against that **exact tagged
commit**. The `validate` job must finish successfully before the
token-bearing `release` job is eligible to publish any artifacts. Validation
includes module tidiness, all Go tests, four browser regression suites, the
server race detector, `golangci-lint`, a reachable-vulnerability check, and a GoReleaser snapshot build.
Failure in any required check prevents publication.

For a failed release workflow, **correct the source and create a new tag**.
Do not assume that a successful `main` workflow on another commit validates
the current tag. Ensure production keys, generated files and the tagged
source tree are reviewed before tagging.

## Performance claims

Keep benchmark commands, exact payloads, CPU, Go version, backend, seed source, concurrency, transport, TLS status, latency distribution and errors with the results. Prefer reproducible before/after comparisons on the same hardware. Clearly label **author-reported**, synthetic and isolated microbenchmark numbers, and distinguish payload throughput from hardware-entropy production. See [benchmark methodology](docs/benchmarks.md).

## Documentation and browser changes

Update the homepage and [API reference](docs/api.md) when adding or renaming endpoints. Preserve canonical `api-*.html` demonstration paths unless a compatibility change is intentional. Avoid showing internal workers and CSS as navigation items. Keep accessibility, bounded preview data, and the shared monospace theme in mind.

The project uses the [MIT License](LICENSE); contributions are submitted under that license.
