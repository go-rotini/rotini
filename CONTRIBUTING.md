# Contributing

Contributions are welcome! Here's how to get started.

## Setup

```bash
git clone https://github.com/go-rotini/rotini.git
cd rotini
go mod download
make all   # run every project process (lint, the full test suite, build, install)
```

`rotini` is two-faced: a spec-driven CLI **code generator** (the `go tool rotini`
binary — `init`, `generate`, `mod`, …) and the **runtime library** that generated
CLIs import. Most contributions touch one side or the other; the codegen engine
lives under `internal/`, the runtime surface in the package root.

## Making Changes

1. Fork the repository and create a branch from `main`.
2. Write tests for any new functionality.
3. Ensure `make all` passes before submitting a pull request.
4. Use [Conventional Commits](https://www.conventionalcommits.org/) for commit messages (e.g., `feat:`, `fix:`, `test:`, `docs:`).

If your change alters generated output, update the golden fixtures under
`internal/testdata/` (and the help goldens) and confirm the diff is intentional.
If it changes the spec or conf schema, keep `internal/schema-spec.json` /
`internal/schema-conf.json` and the example references in `.docs/.rotini.spec.yaml`
/ `.docs/.rotini.conf.yaml` in sync.

## Linting

```bash
make lint
```

## Testing

```bash
make test              # unit tests with coverage
make test-acceptance   # end-to-end scenarios (init scaffolding, generate, $ref composition)
make test-bench        # benchmarks
make test-fuzz         # fuzz tests (parser / spec decoding; 60s per fuzzer)
make test-mutation     # mutation tests (long-running, ~18 min)
make test-race         # tests with the race detector
make rotini-build      # build the codegen binary
make rotini-install    # regenerate (go generate ./...) and install the tool
```

`make all` runs the full chain. `test-mutation` is slow; it's usually run on its
own rather than in a tight edit loop.

## Pull Requests

- Keep PRs focused on a single change.
- Include tests that cover the change. Both happy paths and error paths are
  expected — parse/bind/validation failures and the typed error classes
  (`ParseError`, `BindError`, `RemoteError`, `WiringError`, `ServiceError`,
  `PanicError`) all have observable, asserted behavior.
- When you change codegen, include the regenerated golden fixtures in the PR.
- Reference any relevant issues.

## Reporting Bugs

Open an issue with:

- A minimal reproducing example — the `.rotini.spec.*` (and `.rotini.conf.*` if
  relevant), or a runnable `main.go` for runtime bugs.
- The exact `rotini` command run (`init` / `generate` / `mod` / …) or the runtime
  invocation, plus its full output.
- The expected vs. actual behavior.
- Your Go version and the `rotini` version (tool and/or library).

For spec-composition (`$ref`) bugs, also include the ref form (local, `mod://`,
`git::`, or raw `https://`) and the relevant `.rotini.lock` entry if one exists.

## Security

See [SECURITY.md](SECURITY.md) for reporting vulnerabilities.
