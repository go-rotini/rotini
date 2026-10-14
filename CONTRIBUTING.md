# Contributing

Contributions are welcome! Here's how to get started.

## Setup

```bash
git clone https://github.com/go-rotini/rotini.git
cd rotini
go mod download
go mod download -modfile=tools.mod   # the development tools, kept out of go.mod
make all   # run every project process (lint, the full test suite, build, install)
```

Dependabot updates go.mod and the workflow actions. The development tools in tools.mod are
upgraded by hand with `make tools-upgrade`, which rebuilds tools.mod and scans the result.

Rotini has two parts: a spec-driven CLI **code generator** (the `go tool rotini` binary:
`init`, `generate`, `validate`) and the **runtime library** that generated CLIs import. The
codegen engine lives under `internal/`, the runtime in the package root.

## Making Changes

1. Fork the repository and create a branch from `main`.
2. Write tests for any new functionality.
3. Ensure `make all` passes before submitting a pull request.
4. Use [Conventional Commits](https://www.conventionalcommits.org/) for commit messages and the pull request title (e.g., `feat:`, `fix:`, `test:`, `docs:`); a squash merge releases from the title.

If your change alters generated output, update the golden fixtures under
`internal/codegen/testdata/golden/` and confirm the diff is intentional:

```bash
go test ./internal/codegen -run Golden -update
```

If it changes the spec or conf schema (`internal/codegen/schema-spec.json` /
`schema-conf.json`), refresh the published copies at the repository root, which a release tag
serves as the `$schema` URL, and the reference pages generated from them
(`docs/content/specification/_index.md`, `docs/content/configuration/_index.md`). Those pages
are built from the schema descriptions and the every-key examples in `docs/assets/examples/`,
so edit those, not the pages:

```bash
go test ./internal/codegen -run PublishedSchemas -update-schemas
go test ./internal/codegen -run SchemaDocs -update-schema-docs
```

`TestPublishedSchemasInSync` and `TestSchemaDocsInSync` fail until you do.

If you add a lint rule, add its fixture directory under
`internal/codegen/testdata/lint/<ruleName>/`. `TestLintFixturesComplete` fails until every
rule has one, and `TestLintProblemsArePositioned` until the rule reports a `file:line:col`.

## Linting

```bash
make lint
```

## Testing

```bash
make test              # every test but the e2e tier, with coverage
make test-conformance  # the in-process input-conformance matrix
make test-acceptance   # the process tier: real exit codes, pipes, __complete, signals
make test-e2e          # testscript rigs: spec -> generate -> build -> RUN the binary
make test-completion-shells  # completion in one pinned Docker image per shell, also against the previous release's binary (needs docker and the module proxy)
make test-bench        # benchmarks
make test-fuzz         # every fuzz target, 60s each
make test-mutation     # mutation tests (long-running, ~18 min)
make test-race         # the same tests with the race detector
make rotini-build      # build the codegen binary
make rotini-install    # regenerate (go generate ./...) and install the tool
```

`make all` runs the full chain except `test-completion-shells`. After a change to a completion
script or to what completion offers, run `make test-completion-shells ARGS=-update-shells` and
review the diff in `e2e/testdata/shells/want`. `test-mutation` is slow, so run it on its own rather than in
a tight edit loop.

## Pull Requests

- Keep PRs focused on a single change.
- Include tests for both the success and the error paths, including which typed error
  (`ParseError`, `InputError`, `PluginError`, `WiringError`, `DependencyError`, `PanicError`)
  a failure produces.
- When you change codegen, include the regenerated golden fixtures in the PR.
- Reference any relevant issues.

## Reporting Bugs

Open an issue with:

- A minimal reproducing example — the `.rotini.spec.*` (and `.rotini.conf.*` if
  relevant), or a runnable `main.go` for runtime bugs.
- The exact `rotini` command run (`init` / `generate` / `validate`) or the runtime
  invocation, plus its full output.
- The expected vs. actual behavior.
- Your Go version and the `rotini` version (tool and/or library).

For spec-composition (`$ref`) bugs, also include the ref form (a local relative path
or `mod://<module>@<version>/<path>`) and, for a `mod://` ref, its `go.mod` require.

## Security

See [SECURITY.md](SECURITY.md) for reporting vulnerabilities.
