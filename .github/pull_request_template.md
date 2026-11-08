## Summary

<!-- What changed and why? Link the related issue if one exists. -->

## Scope and compatibility

<!-- Note changes to HTTP/WS contracts, flags, default values, resource budgets, or browser paths. Use "None" if not applicable. -->

## Verification

<!-- Check only what you actually ran. If not run, explain why. -->

- [ ] `go test ./...`
- [ ] `make test-race` (all Go packages)
- [ ] `make coverage` (inspect affected code paths)
- [ ] `go vet ./...`
- [ ] `golangci-lint run --timeout=3m0s`
- [ ] Browser or CLI smoke test (if relevant)
- [ ] Documentation/API reference updated (if relevant)

**Commands/results/limitations:**

<!-- Include failures and unavailable test environments rather than claiming passes. -->

## Security, resources and performance

<!-- Note potential secrets/logs exposure, input validation, source/reseed behavior, concurrency, allocations or throughput changes. For benchmarks, supply reproducible commands, hardware, Go version, payload, DRBG, transport, and before/after data. Write "Not applicable" if appropriate. Do not disclose vulnerabilities here; use SECURITY.md. -->

## Reviewer notes

<!-- Areas needing careful review, compatibility concerns, follow-up work. -->
