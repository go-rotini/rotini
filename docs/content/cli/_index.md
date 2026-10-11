---
title: "cli"
---

# CLI

The `rotini` companion CLI sets up, generates and validates rotini programs. It is itself built
with rotini: its spec lives at `cmd/rotini/.rotini.spec.yaml`, and each help page below is its
own generated output.

Add it to your module as a tool, so everyone working on the project runs the same version:

{{< code title="terminal" language="sh" open="true" collapsible="false" copy="true" >}}
go get -tool github.com/go-rotini/rotini/cmd/rotini@latest
{{< /code >}}

Then run it with `go tool rotini <command>`. To call `rotini` directly instead, install it with
`go install github.com/go-rotini/rotini/cmd/rotini@latest`.

A mistyped command, flag or value names the nearest one the tool accepts:

{{< code title="terminal" language="text" open="true" collapsible="false" copy="false" >}}
$ go tool rotini genrate
Error: unknown command "genrate" for "rotini"; did you mean "generate"?
{{< /code >}}

The companion CLI opts into these suggestions with rotini's `Suggestor`. A CLI you build with
rotini suggests nothing unless you opt in the same way; see
[suggesting a correction](/docs#suggesting-a-correction).

## rotini

{{< code title="$ rotini --help" language="text" open="true" collapsible="false" copy="false" >}}
The companion CLI for Rotini, the spec-first Go CLI framework: scaffold, validate and generate a CLI from its spec.

Documentation: https://rotini.dev

Usage:
  rotini [flags] <command>

Commands:
  initialize, init    scaffold a cli program
  import              build a rotini spec from an existing cli
  generate, gen       generate a cli program
  validate, val       validate a spec and conf
  tree                print a spec's command tree
  explain             describe a spec or conf key
  fmt                 format a spec and conf
  help                print help
  version             print version
  completion          print a shell completion script
  man                 print or install the man pages
  diff                compare two versions of a cli's contract

Flags:
  -v, --version    print version
  -h, --help       print help

Examples:
  rotini init mycli
  rotini validate .rotini.spec.yaml
  rotini generate ./path/to/.rotini.spec.json

Use "rotini help <command>" for more information about a command.
{{< /code >}}

## rotini initialize

Sets up a new CLI. It writes a starter spec and conf under `cmd/<name>/`, then runs the same
`generate` every later run does. That writes the entrypoint (`main.go`), the generated code
(`zz_rotini.go`), copies of rotini's JSON Schemas for your editor, and three handlers (the root,
`help` and `version`) already wired so the new CLI answers `--help` and `--version` on its first
build. The `name` argument becomes the root command's name and the expected binary name.

`main.go` is created once and never overwritten, since it carries your build metadata. `--force`
replaces an existing spec and conf with the starter ones and nothing else: `init` never deletes a
file. Handlers for commands the starter spec does not have stay until your next `generate`, which
disables them with `//go:build ignore`; the one after deletes them. Each step is reported.

On success `init` reports the way `generate` and `validate` do: the spec and conf it wrote, then
the time and how long it took. In a module that does not yet require the rotini package, it also
warns, since the first build would fail without it:

{{< code title="rotini init mycli — output" language="text" open="true" collapsible="false" copy="false" >}}
spec: cmd/mycli/.rotini.spec.yaml
conf: cmd/mycli/.rotini.conf.yaml
[14:02:11] 5.05ms
Warning: go.mod does not require github.com/go-rotini/rotini yet; run `go get github.com/go-rotini/rotini` before building ./cmd/mycli
{{< /code >}}

A module that added the tool with `go get -tool` already requires the package, so this warning
does not appear there.

`--dry-run` (`-n`) shows what `init` would write and writes nothing. It prints the spec and conf
lines, lists each file it would create (or, with `--force`, replace) on stderr, and exits 2.
`--no-dry-run` runs a real init; given after `--dry-run`, the last one wins.

{{< code title="$ rotini help initialize" language="text" open="true" collapsible="false" copy="false" >}}
Scaffold a new rotini cli — write the spec + conf, then run the first generate (entrypoint, wired handler stubs, codegen) so it is ready to build.

Usage:
  rotini initialize [flags] <name>

Arguments:
  <name>    the root command name written to the created spec file (expected binary name)

Flags:
      --format string      the created rotini spec file format (default yaml) [yaml|yml|json|jsonc|toml]
      --template string    start from a shape other than the plain seed [plugin|daemon|suite]
                             plugin    a kubectl-style plugin named <host>-<plugin>, with completion through the host
                             daemon    a long-running serve command that stops cleanly on a signal
                             suite     two binaries composing one shared command from its own spec
      --force              replace an existing spec and conf with the seed (never deletes a file)
  -n, --[no-]dry-run       show what init would write, and change nothing

Global Flags:
  -v, --version    print version
  -h, --help       print help

Examples:
  rotini initialize mycli
  rotini init mycli --format json
  rotini init mycli --force
  rotini init mycli --dry-run
  rotini init kubectl-hello --template plugin

Use "rotini help <command>" for more information about a command.
{{< /code >}}

## rotini import

Builds a spec, conf and handlers from an existing Go command-line program. `rotini import cobra`
reads the command tree a Cobra program builds and writes the spec and conf under `cmd/<dir>/`,
then generates, the way `init` does, with a handler for each imported command. See
[importing a Cobra CLI](/docs#importing-a-cobra-cli) for what carries across and how to finish
the move.

The import runs `go test` in the program's package, with a test file added through `-overlay` and
the importer (`github.com/go-rotini/import`) added to a temporary copy of `go.mod`. The program's
files, `go.mod` and `go.sum` are left as they are. The package's `init` functions run, as they do
in its tests, with stdin empty and `--timeout` (2 minutes by default) as the limit.

What the import can't carry across exactly is reported on stderr, one line per item, graded
`lossy`, `unsupported` or `info`, then a count. A YAML spec also carries the `lossy` and
`unsupported` lines, and the file and line of each hook to port, as comments above each command:

{{< code title="rotini import cobra ./cmd — output" language="text" open="true" collapsible="false" copy="false" >}}
[info] acme: the root is rootCmd (a package-level command variable); pass --root to choose another
[info] acme serve: Args unset: Cobra accepts any positionals, so a trailing variadic args was added; delete it to reject extras
[lossy] acme: viper is linked in: its bindings (BindPFlag, AutomaticEnv, SetEnvPrefix, config paths) were not imported; declare variable: and key: on inputs, env_prefix and config_files by hand
imported 6 commands, 5 flags: 1 lossy, 0 unsupported, 2 info
spec: cmd/acme/.rotini.spec.yaml
conf: cmd/acme/.rotini.conf.yaml
[14:07:02] 1.41s
{{< /code >}}

The root is found from the package's code: a package-level `*cobra.Command` variable (any one
works, since the import walks up to the root), else a function such as `NewRootCmd()`. `--root`
takes any Go expression that yields a command, evaluated in the package, so it can name an
unexported identifier. The import refuses to write over an existing spec, into an
`internal/cmd/<dir>` it didn't generate, or beside the program's own `cmd/<dir>/main.go`; pass
`--dir` to write the rotini CLI beside it. `--dry-run` reports what it would write, as `init`
does. The import runs with `GOWORK=off`, so the package must build from its own module.

`--importer-version` picks the importer release to run, or the path of a local checkout of
`github.com/go-rotini/import`.

{{< code title="$ rotini help import" language="text" open="true" collapsible="false" copy="false" >}}
Build a rotini spec, conf and handler stubs from an existing Go command-line program, read from the command tree it builds.

Usage:
  rotini import <command>

Commands:
  cobra    import a Cobra program

Global Flags:
  -v, --version    print version
  -h, --help       print help

Examples:
  rotini import cobra ./cmd

Use "rotini help <command>" for more information about a command.
{{< /code >}}

{{< code title="$ rotini help import cobra" language="text" open="true" collapsible="false" copy="false" >}}
Read the command tree a Cobra program builds in <package> and write a rotini spec and
conf for it under cmd/<dir>, then generate, seeding handler stubs that match the
imported spec. Anything that can't be carried across exactly is reported on stderr,
one line per item, and in comments in a YAML spec, with the file and line of each
hook to port.

The import runs go test in the package with a test file added through -overlay and
the importer added to a temporary copy of go.mod, so the program's files and go.mod
are left as they are. The package's init functions run, as they do in its tests. The
root is found from the package's code: a package-level command variable, or a root
constructor such as NewRootCmd(); --root names it otherwise.

Usage:
  rotini import cobra [flags] <package>

Arguments:
  <package>    the Go package that builds the command tree, such as ./cmd

Flags:
      --root EXPR                  a Go expression, evaluated in the package, that yields a command of the tree
      --name string                the CLI's name (default the program's root command name)
      --dir string                 the directory under cmd/ and internal/cmd/ to write to (default the CLI's name)
      --strict                     give a command with no argument rule only the arguments its usage line names, not any number more
      --format string              the created rotini spec file format (default yaml) [yaml|yml|json|jsonc|toml]
      --tags string                build tags for go test, comma separated
      --timeout duration           how long the import's go test may run (default 2m)
      --importer-version string    the github.com/go-rotini/import version to run (default v0.1.0), or the path of a local checkout
      --force                      replace an existing spec and conf, and write into an internal/cmd/<dir> rotini didn't generate
  -n, --[no-]dry-run               show what the import would write, and change nothing

Global Flags:
  -v, --version    print version
  -h, --help       print help

Examples:
  rotini import cobra ./cmd
  rotini import cobra --root 'cmds.New(false)' --name dlv ./pkg/terminal/cmds
  rotini import cobra ./cmd --dir acme-rotini --strict

Use "rotini help <command>" for more information about a command.
{{< /code >}}

## rotini generate

Turns a spec and conf into Go code: the command tree, the typed inputs for each command, the table
that maps each command to its handler, the outputs of any features the conf turns on (help, man
pages and so on), and a handler file for each new command. A handler file whose command has left
the spec is disabled with `//go:build ignore`, then deleted by the next `generate`. See
[generated code](/generated) for what each file holds and which ones are yours.

With no arguments, `generate` reads the `.rotini.spec.*` in the working directory and the
`.rotini.conf.*` beside it; with no conf there, the conf defaults apply. It prints the two paths
it read first. A `--config` path that does not exist is an error. It runs the same checks as
`validate` first and prints the same warnings.

Before its timing line, `generate` lists each file it created that is yours from then on: a
handler for a new command, `main.go`, or an editable feature template. Files it rewrites on every
run are not listed.

{{< code title="rotini generate — output" language="text" open="true" collapsible="false" copy="false" >}}
spec: cmd/mycli/.rotini.spec.yaml
conf: cmd/mycli/.rotini.conf.yaml
created: internal/cmd/mycli/mycli_greet.go
[14:05:37] 6.12ms
{{< /code >}}

`--watch` keeps running and regenerates whenever the spec or conf changes. Leave it open in a
terminal while you edit.

`--dry-run` (`-n`) works out everything `generate` would write, create or remove, and changes
nothing. The exit code says what it found:

| Exit code | Meaning |
|---|---|
| 0 | The files on disk are already what the spec generates. The report is the same as a `generate` that wrote nothing. |
| 1 | An error, such as an invalid spec, reported as `generate` reports it. |
| 2 | Something would change. Each change is listed on stderr, then a count. |

{{< code title="rotini generate --dry-run — output" language="text" open="true" collapsible="false" copy="false" >}}
spec: cmd/mycli/.rotini.spec.yaml
conf: cmd/mycli/.rotini.conf.yaml
1. update internal/cmd/mycli/zz_rotini.go (14211 bytes, mode 0644)
2. create internal/cmd/mycli/mycli_greet.go (311 bytes, mode 0644)
dry run: 2 changes not written
{{< /code >}}

That makes it a check for CI that the committed code matches the spec. Run
`go tool rotini generate --dry-run <spec>` for each CLI, or set `generate.dry_run_env` in the conf to
an environment variable, and every `generate` dry-runs while that variable is 1, true, yes or on.
With `dry_run_env: CI`, `go generate ./...` checks every CLI in the module on most CI systems,
which set `CI=true`. `--no-dry-run` runs a real generate whatever the variable says, and
`--dry-run` can't be combined with `--watch`.

{{< code title="$ rotini help generate" language="text" open="true" collapsible="false" copy="false" >}}
Generate a cli program from a rotini spec file and its conf.

Usage:
  rotini generate [flags] [spec_file_path]

Arguments:
  [spec_file_path]    path to the spec file (default the .rotini.spec.* in the working directory)

Flags:
  -c, --config string    path to the rotini conf file (default the .rotini.conf.* beside the spec)
  -w, --watch            watch the spec and conf for changes and re-generate
  -n, --[no-]dry-run     show what would be written, created or removed, and change nothing; exits 2 when something would change

Global Flags:
  -v, --version    print version
  -h, --help       print help

Examples:
  rotini generate
  rotini generate ./path/to/.rotini.spec.json --watch
  rotini generate --dry-run

Use "rotini help <command>" for more information about a command.
{{< /code >}}

## rotini validate

Checks a spec and conf without generating anything, which makes it the command to run in CI and
in a pre-commit hook. It applies the JSON Schemas and rotini's lint rules to both files and
reports each problem with a `file:line:col`.

It finds the spec and conf the same way `generate` does. `--fail fast` stops at the first
problem; `--fail collect` reports every problem at once, and is the default unless the conf's
`validate.fail` says otherwise. `--watch` validates again whenever the spec or conf changes.

`--release 2.0.0` also fails for each command, input or deprecated identifier still declared
whose `removed_in` is at or below that release, so a planned removal can't ship by accident.
Without the flag, the variable the conf's `validate.release_env` names is read instead; unset,
nothing is checked. A prerelease such as `2.0.0-rc.1` counts as `2.0.0`, so the check fails early.
Specs composed from another module (`mod://`) are left out: they follow that module's releases.

`--format json` writes one JSON object to stdout instead, listing every problem with its
`severity`, `document`, `file`, `line`, `col`, JSON `pointer`, `message` and `hint` (what to do
about it), for an editor or an agent to read. The exit code is the same: 1 when any problem is an
error. It can't be combined with `--watch`.

{{< code title="$ rotini help validate" language="text" open="true" collapsible="false" copy="false" >}}
Validate a rotini spec file and its conf for correctness.

Usage:
  rotini validate [flags] [spec_file_path]

Arguments:
  [spec_file_path]    path to the spec file (default the .rotini.spec.* in the working directory)

Flags:
  -c, --config string    path to the rotini conf file (default the .rotini.conf.* beside the spec)
      --fail string      failure reporting — fast (first problem) or collect (all); defaults to validate.fail in the conf, else collect [fast|collect]
  -w, --watch            watch the spec and conf for changes and re-validate
      --release X.Y.Z    fail for each item whose removed_in is at or below this release; defaults to the variable validate.release_env names
      --format string    how to report problems — text, or json (one object listing every problem, on stdout) (default text) [text|json]

Global Flags:
  -v, --version    print version
  -h, --help       print help

Output:
  Every problem validate found. Exits 1 when any is an error, 0 when there are only warnings or none.
  object
    problems []object    the problems, errors first, each as it is reported in text (required)

Examples:
  rotini validate
  rotini val ./path/to/.rotini.spec.yaml

Use "rotini help <command>" for more information about a command.
{{< /code >}}

## rotini tree

Prints a spec's command tree, so a large or composed CLI can be read at a glance. Each command
is on its own line, indented two spaces under its parent, with its aliases after its name. A
composed child (`$ref`) is resolved and shown with its children. Markers follow the names:
`[$ref …]` with the reference as written, `[hidden]`, `[deprecated]` and `[passthrough]`; a
declared plugin is listed as a child marked `[plugin]`, and plugin discovery as a last `*` line.
A deprecated alias is suffixed `(deprecated alias)`. A command under a hidden one isn't marked
itself.

{{< code title="rotini tree — output" language="text" open="true" collapsible="false" copy="false" >}}
musak
  artists  [$ref ../artists/.rotini.spec.yaml]
    list, ls
    show
  debug  [hidden]
  remove, rm, delete (deprecated alias)
  exec  [passthrough]
  cloud  [plugin]
  *  [discovers musak-*]
{{< /code >}}

The output is the tree alone, so it can be piped to `grep`. A spec with errors is refused with
the errors `validate` reports. A `mod://` child is downloaded through the module cache, as
`generate` does, which needs a `go.mod` and may use the network.

{{< code title="$ rotini help tree" language="text" open="true" collapsible="false" copy="false" >}}
Print the command tree of a rotini spec, with composed children ($ref) resolved: each
command's name and aliases, indented under its parent, with markers for hidden, deprecated,
passthrough and composed commands and declared plugins.

A mod:// child is downloaded through the module cache, as generate does, so it needs a
go.mod and may use the network. A spec with errors is refused; rotini validate explains them.

Effects: read, idempotent

Usage:
  rotini tree [spec_file_path]

Arguments:
  [spec_file_path]    path to the spec file (default the .rotini.spec.* in the working directory)

Global Flags:
  -v, --version    print version
  -h, --help       print help

Examples:
  rotini tree
  rotini tree ./cmd/musak/.rotini.spec.yaml

Use "rotini help <command>" for more information about a command.
{{< /code >}}

## rotini diff

Compares two versions of a CLI's contract (the document `generate.contract.file` writes) and
reports what changed for the people and scripts using the CLI. Each finding has a severity:

- **breaking**: a command line, script or reader that worked before stops working, such as a
  removed flag, a new required argument or an output property that's gone;
- **possibly breaking**: it may, such as a changed default, a new output enum value or a newly
  hidden command;
- **expected**: a removal the old contract planned with `removed_in` at or below `--release`;
- **safe**: nothing that worked stops working.

On an `experimental` item every finding is safe, and on a `beta` item a breaking one is possibly
breaking. See [checking for breaking changes](/docs#checking-for-breaking-changes) for the CI
recipe and every rule.

`<old>` is a contract file, `git:<ref>` (the conf's `generate.contract.file` as committed at that
revision, read with `git show`, which never fetches), `git:<ref>:<path>` (a module-root-relative
file at that revision), or `mod://<module>@<version>/<path>`. `<new>` takes the same forms and
defaults to the contract built from the current spec and conf, whether or not the conf writes a
contract file. Because both arguments are contracts, the spec is given with `--spec`, unlike
`generate` and `validate`.

It exits 0 when nothing is at or above `--fail-on` (`breaking` by default), 2 when something is,
and 1 on an error. A `diff.accept` entry in the conf acknowledges one intended change; an entry
that matches no change also exits 2, so the list doesn't outlive its release. `--format json`
writes the findings as one JSON document, described by the command's `output` in the
companion's own contract. `--release` falls back to the variable `validate.release_env` names.

{{< code title="$ rotini help diff" language="text" open="true" collapsible="false" copy="false" >}}
Compare two versions of a cli's contract and report each change for the people and
scripts using it: breaking, possibly breaking, expected (a removal planned for this
release) or safe. Findings are written to stdout, grouped by severity.

<old> is a contract file, git:<ref> (the conf's generate.contract.file at that
revision, read with git show), git:<ref>:<path> (a module-root-relative file at that
revision) or mod://<module>@<version>/<path>. <new> takes the same forms and defaults
to the contract built from the current spec and conf. Because both arguments are
contracts, the spec is given with --spec.

The conf's diff.accept entries acknowledge intended changes, one finding each; an entry
that matches no finding fails the run.

Usage:
  rotini diff [flags] <old> [new]

Arguments:
  <old>    the earlier contract: a file, git:<ref>[:<path>] or mod://<module>@<version>/<path>
  [new]    the later contract (default the one built from the current spec and conf)

Flags:
      --spec string       path to the spec file (default the .rotini.spec.* in the working directory)
  -c, --config string     path to the rotini conf file (default the .rotini.conf.* beside the spec)
      --release X.Y.Z     report a removal the old contract planned for this release or earlier as expected; defaults to the variable validate.release_env names
      --fail-on string    the least severe change that fails the run (default breaking) [breaking|possibly|never]
      --format string     how to write the findings (default text) [text|json]

Global Flags:
  -v, --version    print version
  -h, --help       print help

Output:
  Every change between the contracts, the acknowledgements that matched none, and the counts.
  object
    findings []object             The changes, most severe first, then by where. (required)
    summary object                The changes by severity; an accepted change counts only as accepted. (required)
    unmatched_accepts []object    The diff.accept entries that matched no change. (required)

Examples:
  rotini diff git:v1.2.0
  rotini diff git:v1.2.0 --release 2.0.0
  rotini diff old/cli-contract.json cli-contract.json --format json

Use "rotini help <command>" for more information about a command.
{{< /code >}}

## rotini explain

Prints what a spec or conf key means: its description, type, allowed values, default, examples
and pattern, read from the same schemas as the [spec](/specification) and
[conf](/configuration) reference pages. Write the keys from the top of the document, separated
by dots, without list positions: `command.flags.schema.from`. A key both documents have, such as
`version`, is shown for each. The argument completes one key at a time.

{{< code title="$ rotini help explain" language="text" open="true" collapsible="false" copy="false" >}}
Print what a spec or conf key means: its description, type, allowed values, default and examples, read from the same schemas as the reference pages. Write the keys from the document's top, separated by dots and without list positions: command.flags.schema.from. A key both documents have, such as version, is shown for each.

Effects: read, idempotent, local only

Usage:
  rotini explain <key>

Arguments:
  <key>    the key path, such as command.flags.schema.from

Global Flags:
  -v, --version    print version
  -h, --help       print help

Examples:
  rotini explain command.flags.role
  rotini explain generate.features.targets

Use "rotini help <command>" for more information about a command.
{{< /code >}}

## rotini fmt

Rewrites rotini spec, conf and composed command files in one canonical layout, so specs read
the same across a project and diffs show real changes. Keys go in reading order: a command
starts with its name, aliases and documentation, then its inputs, its output and its
sub-commands; an input starts with its name, identifiers and documentation, then how it
behaves, then its `schema:`, where `type` comes first. Indentation is two spaces, with lists
indented under their key, and runs of blank lines fold into one. Comments move with the entries they sit above or beside, and every
value is copied exactly as written: quoting, flow collections like `{ type: bool }` and block
scalar text are untouched. A mapping whose order matters to the author, such as `headings`, or
whose reordering would put an alias before its anchor, keeps its order.

With no file it formats the spec in the working directory and the conf beside it, found the way
`validate` finds them. A file's kind comes from its keys (`command:` for a spec, `generate:` or
`validate:` for a conf, `name:` for a composed command), then from its name; `--kind` settles a
file that has neither. `--check` writes nothing and exits 2 when a file isn't formatted, 0 when
all are, and 1 on an error. Only YAML files can be formatted for now; JSON, JSONC and TOML files
are reported as not supported yet.

{{< code title="$ rotini help fmt" language="text" open="true" collapsible="false" copy="false" >}}
Rewrite rotini spec, conf and composed command files in canonical form: keys in
reading order (a command's name and docs, then its inputs, output and sub-commands;
an input's name and docs, then its behavior, then its schema); two-space indentation,
with lists indented under their key; and runs of blank lines folded into one.
Comments stay with the entries they describe, and every value is kept exactly as
written.

With no file, it formats the .rotini.spec.* in the working directory and the conf
beside it. --check writes nothing and exits 2 when a file isn't formatted, for CI.
Only YAML files can be formatted for now.

Usage:
  rotini fmt [flags] [files...]

Arguments:
  [files...]    the files to format (default the spec in the working directory and its conf)

Flags:
      --check          write nothing; exit 2 when a file isn't formatted
      --kind string    what the files are, when their keys and names don't tell [spec|conf|command]

Global Flags:
  -v, --version    print version
  -h, --help       print help

Examples:
  rotini fmt
  rotini fmt cmd/app/.rotini.spec.yaml cmd/app/.rotini.conf.yaml
  rotini fmt --check

Use "rotini help <command>" for more information about a command.
{{< /code >}}

## rotini help

Prints help for any command.

{{< code title="$ rotini help help" language="text" open="true" collapsible="false" copy="false" >}}
Print help for a specific command.

Usage:
  rotini help [command...]

Arguments:
  [command...]    name of the command to print help for

Global Flags:
  -v, --version    print version
  -h, --help       print help

Examples:
  rotini help
  rotini help generate
  rotini help init

Use "rotini help <command>" for more information about a command.
{{< /code >}}

## rotini completion

Prints the shell completion script for bash, zsh, fish, PowerShell or Nushell, using the same generated
`Completion(shell)` function any rotini CLI gets with the `completion` feature on.

{{< code title="$ rotini help completion" language="text" open="true" collapsible="false" copy="false" >}}
Print the completion script for a shell. Load it once per session, or install it so
every new shell has it:

  bash        source <(rotini completion bash)
              or save it to ~/.local/share/bash-completion/completions/rotini
  zsh         rotini completion zsh > "${fpath[1]}/_rotini"
              then start a new shell (compinit must be enabled)
  fish        rotini completion fish > ~/.config/fish/completions/rotini.fish
  powershell  rotini completion powershell | Out-String | Invoke-Expression
              add that line to $PROFILE to load it in every session
  nushell     rotini completion nushell | save -f ($nu.user-autoload-dirs | first | path join rotini.nu)
              Nushell 0.116 or later; create the directory first if it is missing

Usage:
  rotini completion <shell>

Arguments:
  <shell>    the shell to print the script for [bash|zsh|fish|powershell|nushell]

Global Flags:
  -v, --version    print version
  -h, --help       print help

Examples:
  rotini completion bash
  rotini completion zsh > "${fpath[1]}/_rotini"

Use "rotini help <command>" for more information about a command.
{{< /code >}}

## rotini man

Prints a command's man page, or with `--dir` writes every page into a directory, named
`rotini.1`, `rotini-generate.1` and so on. It uses the generated `Man`, `ManPages()` and
`ManSection` that any rotini CLI gets with the `man` feature on, so you can ship your own CLI's
pages the same way.

{{< code title="$ rotini help man" language="text" open="true" collapsible="false" copy="false" >}}
Print a command's man page as roff, the markup the man program reads, or write every
page into a directory with --dir. With no command, it prints the page for rotini itself.

The pages are named after the command path, rotini-generate.1, so a directory written
with --dir can be added to MANPATH or copied into a man1 directory.

Usage:
  rotini man [flags] [command...]

Arguments:
  [command...]    the command whose page to print (default rotini itself)

Flags:
      --dir string    write every page into this directory instead of printing one

Global Flags:
  -v, --version    print version
  -h, --help       print help

Examples:
  rotini man generate > rotini-generate.1
  rotini man --dir ~/.local/share/man/man1

Use "rotini help <command>" for more information about a command.
{{< /code >}}

## rotini version

Prints the tool's version. `generate` and `validate` compare it with the `version:` key in your
spec and conf.

That key is a minimum, not an exact match: it names the rotini your files were written for, and
any rotini of the same major version at or beyond it accepts them, so a patch or minor upgrade
never requires editing your spec. Two cases are errors: a rotini older than your files, which may
not know keys they use, and a different major version:

| document `version:` | rotini | result |
|---|---|---|
| `1.2.0` | `1.2.0` | ok |
| `1.2.0` | `1.4.1` | ok: newer, same major |
| `1.4.0` | `1.2.0` | error: this rotini may not know keys the document uses |
| `1.x` | `2.x` | error: a different major version |
| any | a development build with no version | not checked |

{{< alert type="info" title="WHERE THE VERSION COMES FROM:" >}}
When rotini is installed through the module graph — `go get -tool`, then `go tool rotini` — the version is the one your `go.mod` requires, read from the binary's build info. A build from source can stamp one with `-ldflags "-X main.version=…"`, which applies only when build info carries no release version (a development build or a pseudo-version); a real module version always wins.
{{< /alert >}}

`--format json` prints the version with the Go version that built the binary and the module path
and version from its build info, as one JSON object. The text output and `--version` stay
`v1.x.y`.

{{< code title="rotini version --format json — output" language="json" open="true" collapsible="false" copy="false" >}}
{
  "go_version": "go1.27.1",
  "module_path": "github.com/go-rotini/rotini",
  "module_version": "v1.4.0",
  "version": "v1.4.0"
}
{{< /code >}}

{{< code title="$ rotini help version" language="text" open="true" collapsible="false" copy="false" >}}
Print the rotini cli version. With --format json, print the version, the Go version that built
the binary, and the module path and version from its build info, as one JSON object.

Usage:
  rotini version [flags]

Flags:
      --format string    output format (default text) [text|json]

Global Flags:
  -v, --version    print version
  -h, --help       print help

Output:
  The version and build of the rotini binary.
  object
    go_version string        the Go toolchain that built the binary, such as go1.27.1 (required)
    module_path string       the main module path from the build info
    module_version string    the main module version from the build info: a release, a pseudo-version (with +dirty when built from a modified checkout), or (devel)
    version string           what rotini version prints, such as v1.4.0 (required)

Examples:
  rotini version
  rotini version --format json

Use "rotini help <command>" for more information about a command.
{{< /code >}}
