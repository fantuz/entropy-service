# CI execution and caching

## Required PR checks

`build` and `browser-ui` start for every pull request. A missing workflow is
no longer the expected result of a documentation change. The Python planner
diffs the actual PR base against the checked-out merge commit with rename
detection disabled. Only regular, non-executable `README.md` and
`docs/**/*.md` files qualify for the documentation path. A deleted Go file
renamed to Markdown, embedded assets, `.gitignore`, workflow changes,
symlinks, missing history and unrecognized inputs select full validation.
Pushes to main, tag builds and manual runs always select full validation.

Documentation PRs validate release links, publication helpers, credential
policy, all workflow syntax and the CI regression tests. Existing native
Windows, archive, container, endurance and both browser check names still
complete with an explicit documentation result; they use Linux workers and
do not install Go or browsers. A documentation result does not assert that
application tests ran. New Linux suite/archive jobs are intentionally
skipped, and the `build` gate permits exactly those skips. Failure or
cancellation in a required job fails the aggregate gate.

## Parallel validation and archive provenance

Code changes run independent Linux quality, bounded fuzz, all-package race
(including the opt-in experimental backend), terminal and package jobs.
The fuzz iteration budgets and terminal checks are unchanged. Compilation
is cached; tests run again, with `-count=1` on coverage, race, fallback and
native unit tests. Coverage profiles, logs and executables are artifacts,
never reusable test results.

The package producer builds the GoReleaser snapshot immediately, validates
download aliases/checksums, actual GUI globbing, real systemd lifecycle and
Windows ZIP layout, and uploads `windows-x64-release-zip`. Native Windows
package testing depends on this producer, rather than waiting for fuzz,
race, terminal or native source tests. It downloads the artifact from the
**same workflow run**, executes its actual binaries and tests the real
Windows Service lifecycle and private state. A failed producer cannot be
replaced by an old cached ZIP.

The existing `build` check requires all planned validations. Main image
publication waits for that gate. Tagged releases use the same reusable
Linux/archive jobs, verify the tag against `VERSION`, and retain the final
gate over Linux, both browsers, native Windows, the tested ZIP, endurance,
containers and the GUI installer. Moving archive production earlier does
not make any publication eligible before validation. Historical release
recovery check names and immutable-source rules are preserved.

In the investigated main run `43984858294`, the Linux build took 384 seconds
and the Windows ZIP job took 77 seconds. The ZIP job started about 390 seconds
after the workflow event because it waited for the Linux build. These are
observations from that run, not a guarantee of new timings. Parallel jobs
can use more concurrent runner slots and initially populate cold caches;
runner availability, containers and browser suites can still determine
overall PR duration.

## Cache contents and retention

The local `setup-go-cache` action disables setup-go's combined cache and
keeps separate verified module downloads and compiled inputs. Module keys
include OS, architecture, exact Go version and `go.sum`. Build keys also
include the compiler workload and a source/embedded-asset hash. A changed
source restores the previous matching workload as a starting point and
saves a fresh snapshot, instead of indefinitely hitting a stale immutable
cache. Race, fuzz, browser, package and ordinary builds do not overwrite one
another's snapshots. Go validates compiler inputs; module checksum checks
remain in the quality, native and package jobs.

Browser jobs install Playwright from `scripts/browser-requirements.txt`,
cache pip downloads and browser downloads by OS, architecture, engine and
the installed Playwright version. They still install OS dependencies and
run Playwright's revision-aware installer after cache restoration. Changing
the pinned version invalidates the browser cache. Firefox virtual audio,
all browser suites and failure artifacts remain enabled.

`Bound CI input caches` runs weekly and when a PR closes. It executes only
tools checked out from the repository's default branch, including on
`pull_request_target`; it never checks out or executes PR code. It first
obtains a complete cache listing and checks PR states, then deletes at most
100 entries per run:

- Caches scoped to confirmed closed PR refs can be removed. Open and
  unknown-state PRs are retained.
- On the default branch, keep the two newest `go-build-v2` snapshots for
  each OS/architecture/workload/Go-version group. Leave other default-branch
  caches, module downloads and BuildKit caches alone.
- Other branch refs are retained. API/pagination failures prevent deletion.

Manual maintenance defaults to **dry run**, reports numeric cache IDs and
does not print tokens or untrusted cache keys. Cache removal only affects
future download/compilation time; it never deletes artifacts or reports.
The new maintenance policy takes effect after this workflow is merged; no
repository cache deletion is needed to validate this PR. GitHub's own
retention/quota policy continues to apply. The investigated 960 entries
occupied about 7.3 GB; that observation does not establish the configured
repository quota.

## Local validation

In a disposable virtual environment:

```sh
python3 -m venv /tmp/entropy-ci-checks
/tmp/entropy-ci-checks/bin/pip install -r .github/ci-requirements.txt
/tmp/entropy-ci-checks/bin/python -m unittest discover -s scripts -p 'test_ci_*.py'
```

CI also runs checksum-pinned actionlint 1.7.12 across every workflow. The
tests exercise real git diffs (including renames and symlinks), aggregate
failures, release tag/source mismatches, pagination failures and bounded
cleanup with mocked GitHub responses. Workflow contracts protect the
publication DAG, artifact provenance and documentation check names.
