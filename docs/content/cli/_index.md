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
rotini suggests nothing unless you opt in the same way.

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
  completion          print a shell completion script
  man                 print or install the man pages

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
removes them and says so.

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

Turns a spec and conf into Go code: the command tree, the typed inputs for each command, the table
that maps each command to its handler, the outputs of any features the conf turns on (help, man
pages and so on), and a handler file for each new command. A handler file whose command has left
the spec is removed. See [generated code](/generated) for what each file holds and which ones
are yours.

With no arguments, `generate` reads the `.rotini.spec.*` in the working directory and the
`.rotini.conf.*` beside it; with no conf there, the conf defaults apply. It prints the two paths
it read first. A `--config` path that does not exist is an error. It runs the same checks as
`validate` first and prints the same warnings.

`--watch` keeps running and regenerates whenever the spec or conf changes. Leave it open in a
terminal while you edit.

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

Checks a spec and conf without generating anything, which makes it the command to run in CI and
in a pre-commit hook. It applies the JSON Schemas and rotini's lint rules to both files and
reports each problem with a `file:line:col`.

It finds the spec and conf the same way `generate` does. `--fail fast` stops at the first
problem; `--fail collect` reports every problem at once, and is the default unless the conf's
`validate.fail` says otherwise. `--watch` validates again whenever the spec or conf changes.

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

## rotini completion

Prints the shell completion script for bash, zsh, fish or PowerShell. It is the companion's own
generated `Completion(shell)`: the same function any rotini CLI gets with the `completion`
feature on.

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
  rotini completion <shell> [flags]

Arguments:
  <shell>    the shell to print the script for [bash|zsh|fish|powershell]

Flags:
  -h, --help    print help

Examples:
  rotini completion bash
  rotini completion zsh > "${fpath[1]}/_rotini"

Use "rotini help <command>" for more information about a command.
{{< /code >}}

## rotini man

Prints a command's man page, or with `--dir` writes every page into a directory, named
`rotini.1`, `rotini-generate.1` and so on. It is built on the companion's own generated `Man`,
`ManPages()` and `ManSection`, the functions any rotini CLI gets with the `man` feature on, so
its `--dir` is a working example of shipping every page of your own CLI.

{{< code title="$ rotini help man" language="text" open="true" collapsible="false" copy="false" >}}
Print a command's man page as roff, the markup the man program reads, or write every
page into a directory with --dir. With no command, it prints the page for rotini itself.

The pages are named after the command path, rotini-generate.1, so a directory written
with --dir can be added to MANPATH or copied into a man1 directory.

Usage:
  rotini man [command...] [flags]

Arguments:
  [command...]    the command whose page to print (default rotini itself)

Flags:
  --dir string    write every page into this directory instead of printing one
  -h, --help      print help

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
not know keys they use, and a different major version. See
[COMPATIBILITY.md](https://github.com/go-rotini/rotini/blob/main/COMPATIBILITY.md).

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
