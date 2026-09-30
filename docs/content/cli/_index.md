---
title: "cli"
---

# CLI

The `rotini` companion CLI is itself built with rotini: its spec lives at `cmd/rotini/.rotini.spec.yaml`, and everything below is its own generated help output.

{{< code title="go get -tool" language="text" open="true" collapsible="false" copy="true" >}}
go get -tool github.com/go-rotini/rotini/cmd/rotini@latest
{{< /code >}}

Invoke it with `go tool rotini <command>`, or install it globally and call `rotini` directly.

A mistyped command, flag or value names the nearest one it accepts — `unknown command "genrate" for "rotini"; did you mean "generate"?`. That is the tool's own choice, made with rotini's `Suggestor`; a CLI built with rotini suggests nothing unless its author opts in the same way.

## rotini

{{< code title="$ rotini --help" language="text" open="true" collapsible="false" copy="false" >}}
The rotini cli framework companion cli.

Find more information at: https://rotini.dev

Usage:
  rotini <command> <arguments> [flags]
        [-v | --version] [-h | --help]

Commands:
  initialize, init    scaffold a cli program
  generate, gen       generate a cli program
  validate, val       validate a spec and conf
  help                print help
  version             print version

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

Scaffolds a new CLI: writes the seed spec and conf under `cmd/<name>/`, then runs the same `generate` every later pass runs — producing the entrypoint, the framework file, copies of rotini's JSON Schemas for your editor, and three handler stubs — the root, `help` and `version` — already wired so the new CLI answers `--help` and `--version` on its first build. The `name` argument becomes the root command name and the expected binary name.

The entrypoint is **create-once**: it carries your build metadata, so it is never overwritten. `--force` replaces an existing spec and conf with the seed, and nothing else: init never deletes a file. Handlers for commands the seed does not have stay until your next `generate`, which removes them and says so.

`init` prints nothing when it succeeds. The one thing it warns about is a `go.mod` that does not yet require the rotini runtime, since the first build would fail without it.

{{< code title="$ rotini help initialize" language="text" open="true" collapsible="false" copy="false" >}}
Scaffold a new rotini cli — write the spec + conf, then run the first generate (entrypoint, wired handler stubs, codegen) so it is ready to build.

Usage:
  rotini initialize <name> [flags]

Arguments:
  <name>    the root command name written to the created spec file (expected binary name)

Flags:
  --format string    the created rotini spec file format (default yaml) [yaml|yml|json|jsonc|toml]
  --force            replace an existing spec and conf with the seed (never deletes a file)
  -h, --help         print help

Examples:
  rotini initialize mycli
  rotini init mycli --format json
  rotini init mycli --force

Use "rotini help <command>" for more information about a command.
{{< /code >}}

## rotini generate

Compiles a spec + conf into Go: the command tree as a `Definition` literal, the typed input structs, the table that maps each command to its handler, any enabled feature outputs, and one editable stub per new command. Generated files that no longer map to a command are pruned.

With no arguments, `generate` reads the `.rotini.spec.*` in the working directory and the `.rotini.conf.*` beside the spec; with no conf there, the conf defaults apply. It prints the two paths it read first. A `--config` path that does not exist is an error. It runs the same checks as `validate` first and prints the same warnings.

`--watch` keeps running and regenerates on every change to the spec or conf — leave it open in a terminal while you edit.

{{< code title="$ rotini help generate" language="text" open="true" collapsible="false" copy="false" >}}
Generate a cli program from a rotini spec file and its conf.

Usage:
  rotini generate [spec_file_path] [flags]

Arguments:
  [spec_file_path]    path to the spec file (default the .rotini.spec.* in the working directory)

Flags:
  -c, --config string    path to the rotini conf file (default the .rotini.conf.* beside the spec)
  -w, --watch            watch the spec and conf for changes and re-generate
  -h, --help             print help

Examples:
  rotini generate
  rotini generate ./path/to/.rotini.spec.json --watch

Use "rotini help <command>" for more information about a command.
{{< /code >}}

## rotini validate

Checks a spec + conf without generating anything — the right thing to run in CI and in a pre-commit hook. It applies the JSON Schema *and* rotini's 43 spec lint rules (plus 8 for the conf), reporting each problem with a `file:line:col`.

It finds the spec and conf the same way `generate` does. `--fail fast` stops at the first problem; `collect` reports every problem at once, and is the default unless the conf's `validate.fail` says otherwise. `--watch` re-validates on every change to the spec or conf.

{{< code title="$ rotini help validate" language="text" open="true" collapsible="false" copy="false" >}}
Validate a rotini spec file and its conf for correctness.

Usage:
  rotini validate [spec_file_path] [flags]

Arguments:
  [spec_file_path]    path to the spec file (default the .rotini.spec.* in the working directory)

Flags:
  -c, --config string    path to the rotini conf file (default the .rotini.conf.* beside the spec)
  --fail string          failure reporting — fast (first problem) or collect (all); defaults to validate.fail in the conf, else collect [fast|collect]
  -w, --watch            watch the spec and conf for changes and re-validate
  -h, --help             print help

Examples:
  rotini validate
  rotini val ./path/to/.rotini.spec.yaml

Use "rotini help <command>" for more information about a command.
{{< /code >}}

## rotini help

Prints help for any command.

{{< code title="$ rotini help help" language="text" open="true" collapsible="false" copy="false" >}}
Print help for a specific command.

Usage:
  rotini help [command...] [flags]

Arguments:
  [command...]    name of the command to print help for

Flags:
  -h, --help    print help

Examples:
  rotini help
  rotini help generate
  rotini help init

Use "rotini help <command>" for more information about a command.
{{< /code >}}

## rotini version

Prints the tool version. This is the version compared against the `version:` key in your spec and conf, so generated code never diverges quietly from the definition it came from.

The comparison is a **minimum**, not an equality: your documents declare the feature set they were written against, and any rotini of the same major at or beyond it accepts them. Taking a patch or minor release never requires editing a spec. Two cases are errors — a rotini *older* than your documents, which may not know the keys they use, and a different major. See [COMPATIBILITY.md](https://github.com/go-rotini/rotini/blob/main/COMPATIBILITY.md).

{{< alert type="info" title="WHERE THE VERSION COMES FROM:" >}}
When rotini is installed through the module graph — `go get -tool`, then `go tool rotini` — the version is the one in your `go.mod`, read from the binary's build info. That is what makes the tool version and your `require` line the same fact. A build from source may stamp one in with `-ldflags "-X main.version=…"`, which applies only when build info carries no release version (a development build, or a pseudo-version); a real module version always wins.
{{< /alert >}}

{{< code title="$ rotini help version" language="text" open="true" collapsible="false" copy="false" >}}
Print the rotini cli version.

Usage:
  rotini version [flags]

Flags:
  -h, --help    print help

Examples:
  rotini version

Use "rotini help <command>" for more information about a command.
{{< /code >}}
