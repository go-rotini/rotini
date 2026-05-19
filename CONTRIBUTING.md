# Contributing

Contributions are welcome! Here's how to get started.

## Setup

```bash
git clone https://github.com/go-rotini/fs.git
cd fs
go mod download
make all   # run all project processes
```

## Making Changes

1. Fork the repository and create a branch from `main`.
2. Write tests for any new functionality.
3. Ensure `make all` passes before submitting a pull request.
4. Use [Conventional Commits](https://www.conventionalcommits.org/) for commit messages (e.g., `feat:`, `fix:`, `test:`, `docs:`).

## Linting

```bash
make lint
```

## Testing

```bash
make test              # run unit tests with coverage
make test-acceptance   # run real-world end-to-end scenarios (project root discovery, archive round-trip, scaffold idempotency, etc.)
make test-bench        # run benchmarks
make test-conformance  # run cross-platform invariants (atomic-write, zip-slip defense, TOCTOU OpenNoFollow, symlink-loop detection)
make test-fuzz         # run fuzz tests (60s per fuzzer)
make test-mutation     # run mutation tests
make test-race         # run tests with the race detector
```

CI exercises `make test`, `make test-race`, and `make test-conformance` on Linux, macOS, and Windows; FreeBSD is built and vetted via build tags.

## Pull Requests

- Keep PRs focused on a single change.
- Include tests that cover the change. Filesystem-touching code is expected to exercise both happy paths and error paths; the fault-injection layer in `fault_hooks.go` is available for the defensive branches.
- Reference any relevant issues.

## Reporting Bugs

Open an issue with:

- A minimal reproducing example (a `go test` snippet or a runnable `main.go`).
- The filesystem operation being attempted and the path layout it ran against.
- The expected vs. actual behavior.
- OS, filesystem type (apfs / ext4 / ntfs / xfs / smb / nfs / etc.), and Go version.

For watcher / event-delivery bugs, also include whether the polling backend was forced via `WithPolling(...)` or selected automatically.

## Security

See [SECURITY.md](SECURITY.md) for reporting vulnerabilities.
