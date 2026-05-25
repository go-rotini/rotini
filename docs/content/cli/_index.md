---
title: "cli"
---

# CLI

The rotini cli framework companion cli has three primary purposes:
1. `initialize` for scaffolding a go project with rotini files
2. `validate` for ensuring rotini spec files are valid structures
3. `generate` for generating go type-safe source code from rotini spec files

## rotini

The root command or bin for the rotini cli.

{{< code title="$ rotini help" language="text" open="true" collapsible="false" copy="false" >}}
The rotini cli framework companion cli.

Find more information at: https://rotini.dev

Usage:
  rotini <command> [flags]
         [-v | --version] [-h | --help]

Commands:
  initialize;init     initialize a cli program
  generate;gen        generate a cli program
  validate;val        validate a spec file
  completion          generate shell completion
  version             print version
  help                print help

Flags:
  -v,--version        print version
  -h,--help           print help

Examples:
  rotini init mycli
  rotini validate .rotini.yaml
  rotini generate ./path/to/.rotini.json

Use "rotini help <command>" for more information about a command.
{{< /code >}}

## rotini initialize

Scaffolds a new rotini project. Creates the spec file (`.rotini.yaml` or `.rotini.json`), a `main.go` entry point, and initial handler stubs under `internal/cmd/`. The `name` argument sets the root command name written to the spec file — this should match the intended binary name.

Use `--format` to choose between YAML and JSON for the spec file. The `--force` flag allows re-initialization in a directory that already contains rotini files, overwriting any existing generated files.

{{< code title="$ rotini help init" language="text" open="true" collapsible="false" copy="false" >}}
Initialize a new rotini cli program spec file.

Usage:
  rotini initialize <name> [--format=yaml|json] [--force] [-h | --help]

Arguments:
  name          the root command name written to created spec file (expected binary name)

Flags:
  --format      specifiy the created rotini spec file format as yaml or json (default: yaml)
  --force       forces re-initialization if files exist that init would overwrite
  -h,--help     print help

Examples:
  rotini initialize mycli
  rotini init mycli --format json
  rotini init mycli --force

Use "rotini help <command>" for more information about a command.
{{< /code >}}

## rotini generate

Reads a rotini spec file and produces the generated Go source code. By default, it looks for a `.rotini.yaml` or `.rotini.json` file in the current directory. Pass an explicit path to use a different file.

The `--watch` flag is used to watch the spec file; useful during active development to keep generated code in sync without manually re-running the command directly or through the generate directive with `go generate ./...`.

{{< code title="$ rotini help gen" language="text" open="true" collapsible="false" copy="false" >}}
Generate a cli program from a rotini spec file.

Usage:
  rotini generate [./path/to/.rotini.yaml] [-w | --watch] [-h | --help]

Arguments:
  file           path to the spec file (default: .rotini.yaml)

Flags:
  -w,--watch     watch a rotini spec file for changes and re-generate
  -h,--help      print help

Examples:
  rotini generate
  rotini generate ./path/to/.rotini.json --watch

Use "rotini help <command>" for more information about a command.
{{< /code >}}

## rotini validate

Validates a rotini spec file for structural correctness without generating any code. This is useful for CI pipelines or pre-commit checks to catch spec errors early. Like `generate`, it defaults to looking for a `.rotini.yaml` or `.rotini.json` file in the current directory if no path is provided.

{{< code title="$ rotini help val" language="text" open="true" collapsible="false" copy="false" >}}
Validate a rotini spec file for correctness.

Usage:
  rotini validate [./path/to/.rotini.yaml] [-h | --help]

Arguments:
  file          path to the spec file (default: .rotini.yaml)

Flags:
  -h,--help     print help

Examples:
  rotini validate
  rotini val ./path/to/.rotini.yaml

Use "rotini help <command>" for more information about a command.
{{< /code >}}

## rotini completion

Generates shell completion scripts for the rotini CLI. Supports zsh, bash, fish, powershell, nushell, and elvish. The output is written to stdout and can be piped to the correct location, evaluated from your shell rc, or run adhoc if your rotini binary version changes frequently or between projects.

{{< code title="$ rotini help completion" language="text" open="true" collapsible="false" copy="false" >}}
Generate shell completion scripts.

Usage:
  rotini completion <shell> [-h | --help]

Arguments:
  shell         the shell to generate completions for (zsh, bash, fish, powershell, nushell, elvish)

Flags:
  -h,--help     print help

Examples:
  rotini completion bash
  rotini completion zsh >> ~/.zshrc (eval "$(rotini completion zsh)")
  rotini completion fish > ~/.config/fish/completions/rotini.fish
  rotini completion powershell | Out-String | Invoke-Expression
  rotini completion nushell | save -f ~/.config/nushell/rotini.nu
  rotini completion elvish | save -f ~/.config/elvish/lib/rotini.elv

Use "rotini help <command>" for more information about a command.
{{< /code >}}

## rotini version

Prints the installed rotini CLI version. Useful for verifying which version is active when debugging `$schema` version mismatches during `generate`.

{{< code title="$ rotini help version" language="text" open="true" collapsible="false" copy="false" >}}
Print the rotini cli version.

Usage:
  rotini version [-h | --help]

Flags:
  -h,--help     print help

Examples:
  rotini version

Use "rotini help <command>" for more information about a command.
{{< /code >}}

## rotini help

Prints help for the rotini CLI or a specific subcommand. Equivalent to passing `--help` to any command, but allows navigating help for nested commands by name.

{{< code title="$ rotini help -h" language="text" open="true" collapsible="false" copy="false" >}}
Print help for a specific command.

Usage:
  rotini help [command] [-h | --help]

Arguments:
  command       name of the command to print help for

Flags:
  -h,--help     print help

Examples:
  rotini help
  rotini help generate
  rotini help init

Use "rotini help <command>" for more information about a command.
{{< /code >}}
