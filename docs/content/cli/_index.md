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
The rotini cli framework companion cli.

Find more information at: https://rotini.dev

Usage:
  rotini [flags] <command> <arguments>
        [-v | --version] [-h | --help]

Commands:
  initialize, init    scaffold a cli program
  generate, gen       generate a cli program
  validate, val       validate a spec and conf
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

{{< code title="$ rotini help initialize" language="text" open="true" collapsible="false" copy="false" >}}
Scaffold a new rotini cli — write the spec + conf, then run the first generate (entrypoint, wired handler stubs, codegen) so it is ready to build.

Usage:
  rotini initialize [flags] <name>

Arguments:
  <name>    the root command name written to the created spec file (expected binary name)

Flags:
      --format string    the created rotini spec file format (default yaml) [yaml|yml|json|jsonc|toml]
      --force            replace an existing spec and conf with the seed (never deletes a file)
  -n, --dry-run          show what init would write, and change nothing

Global Flags:
  -h, --help    print help

Examples:
  rotini initialize mycli
  rotini init mycli --format json
  rotini init mycli --force
  rotini init mycli --dry-run

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
  -h, --help    print help

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
  -h, --help    print help

Output:
  Every problem validate found. Exits 1 when any is an error, 0 when there are only warnings or none.
  object
    problems []object    the problems, errors first, each as it is reported in text (required)

Examples:
  rotini validate
  rotini val ./path/to/.rotini.spec.yaml

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
  -h, --help    print help

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
  -h, --help    print help

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
  -h, --help    print help

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
  -h, --help    print help

Examples:
  rotini help
  rotini help generate
  rotini help init

Use "rotini help <command>" for more information about a command.
{{< /code >}}

## rotini completion

Prints the shell completion script for bash, zsh, fish or PowerShell, using the same generated
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

Usage:
  rotini completion <shell>

Arguments:
  <shell>    the shell to print the script for [bash|zsh|fish|powershell]

Global Flags:
  -h, --help    print help

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
  -h, --help    print help

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

{{< code title="$ rotini help version" language="text" open="true" collapsible="false" copy="false" >}}
Print the rotini cli version.

Usage:
  rotini version

Global Flags:
  -h, --help    print help

Examples:
  rotini version

Use "rotini help <command>" for more information about a command.
{{< /code >}}
