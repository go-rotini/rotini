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

`rotini` is two-faced: a spec-driven CLI **code generator** (the `go tool rotini`
binary — `init`, `generate`, `validate`, …) and the **runtime library** that generated
CLIs import. Most contributions touch one side or the other; the codegen engine
lives under `internal/`, the runtime surface in the package root.

## Making Changes

1. Fork the repository and create a branch from `main`.
2. Write tests for any new functionality.
3. Ensure `make all` passes before submitting a pull request.
4. Use [Conventional Commits](https://www.conventionalcommits.org/) for commit messages (e.g., `feat:`, `fix:`, `test:`, `docs:`).

If your change alters generated output, update the golden fixtures under
`internal/codegen/testdata/golden/` (`go test ./internal/codegen -run Golden -update`) and
confirm the diff is intentional. If it changes the spec or conf schema, keep
`internal/codegen/schema-spec.json` / `internal/codegen/schema-conf.json`, the
root-level published copies (`schema-spec.json` / `schema-conf.json`, which a release
tag serves as the `$schema` URL), and the generated reference pages under
`docs/content/specification/_index.md` and `docs/content/configuration/_index.md`, plus the every-key examples in `docs/assets/examples/`, in sync (`go test ./internal/codegen -run PublishedSchemas -update-schemas`, then
`go test ./internal/codegen -run SchemaDocs -update-schema-docs`).
`TestPublishedSchemasInSync` and `TestSchemaDocsInSync` fail until they are.

If you add a lint rule, add its fixture directory under
`internal/codegen/testdata/lint/<ruleName>/` — `TestLintFixturesComplete` fails until
every rule in the registry has one, and `TestLintProblemsArePositioned` until the rule
reports a `file:line:col`.

The docs site's reference pages are GENERATED from the schemas, so a schema description
is the only place that key is documented. After editing one:

```bash
go test ./internal/codegen -run SchemaDocs -update-schema-docs
```

`TestSchemaDocsInSync` fails until you do.

## Linting

```bash
make lint
```

## Testing

```bash
make test              # every test in the module, with coverage
make test-conformance  # the in-process input-conformance matrix
make test-acceptance   # the process tier: real exit codes, pipes, __complete, signals
make test-e2e          # testscript rigs: spec -> generate -> build -> RUN the binary
make test-bench        # benchmarks
make test-fuzz         # fuzz targets (60s each; see FUZZ_TARGETS in the Makefile)
make test-mutation     # mutation tests (long-running, ~18 min)
make test-race         # tests with the race detector
make rotini-build      # build the codegen binary
make rotini-install    # regenerate (go generate ./...) and install the tool
```

`make all` runs the full chain. `test-mutation` is slow; it's usually run on its
own rather than in a tight edit loop.

The tiers are cumulative, and each proves something the one before it cannot:
`test` covers units and codegen goldens, `test-conformance` covers every input
channel in-process, `test-acceptance` covers what only a real process shows (exit
codes, pipes, signals), and `test-e2e` covers what only a real *user module* shows —
that generated code not merely compiles but behaves.

Planning documents live in a sibling repository at `../.docs`, deliberately outside
this module so they are not published with it.

## Pull Requests

- Keep PRs focused on a single change.
- Include tests that cover the change. Both happy paths and error paths are
  expected — parse/bind/validation failures and the typed error classes
  (`ParseError`, `InputError`, `PluginError`, `WiringError`, `DependencyError`,
  `PanicError`) all have observable, asserted behavior.
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
