---
title: "docs"
---

# Using rotini

This guide builds `todo`, a small task CLI, from install to tests, and covers what you need to
write your own rotini program. The [quick start](/) is the short version; the
[spec](/specification) and [conf](/configuration) references list every key.

## Install

Rotini is one module with two parts: the **tool** that generates your code and the **runtime**
package that code imports. Add the tool as a tool dependency, so everyone working on the project
runs the same version through `go.mod`:

{{< code title="terminal" language="sh" open="true" collapsible="false" copy="true" >}}
mkdir todo
cd todo
go mod init github.com/me/todo
go get -tool github.com/go-rotini/rotini/cmd/rotini@latest
{{< /code >}}

Rotini requires Go 1.27 or later. The tool and the runtime are one module, so this also adds the
runtime to `go.mod`, as an indirect dependency until your code imports it.

## The files and the loop

`rotini init` sets up a program that builds and runs as it is. Run `go mod tidy` after it to
record the runtime as a direct dependency, since the generated code imports it:

{{< code title="terminal" language="sh" open="true" collapsible="false" copy="true" >}}
go tool rotini init todo
go mod tidy
{{< /code >}}

These are the files it writes:

| File | What it is | Who edits it |
|---|---|---|
| `cmd/todo/.rotini.spec.yaml` | the [spec](/specification): commands, flags, arguments and every other input | you |
| `cmd/todo/.rotini.conf.yaml` | the [conf](/configuration): where code is written, which extras are on | you |
| `cmd/todo/.rotini-schema.*.json` | JSON Schemas for the spec and conf, for editor completion | rotini, on every `go generate` |
| `cmd/todo/main.go` | the entrypoint, with the `//go:generate` line | you (created once) |
| `internal/cmd/todo/zz_rotini.go` | the [generated](/generated) types and wiring | rotini, on every `go generate` |
| `internal/cmd/todo/todo*.go` | one handler file per command | you (created once) |

Specs and confs can also be JSON, JSONC or TOML: `rotini init todo --format toml`.

The spec `init` writes declares a `--help` flag, a `--version` flag (long only, so `-v` stays free
for a `--verbose`), and `help` and `version` commands. Both flags are cascading, so every
command's help lists them. Rotini adds no flags or commands of its own, so these are ordinary spec
entries you can rename, change or delete.

From then on, development is a loop: change the spec, regenerate, implement any new handler, build.

{{< code title="the loop" language="sh" open="true" collapsible="false" copy="true" >}}
go tool rotini validate ./cmd/todo/.rotini.spec.yaml --config ./cmd/todo/.rotini.conf.yaml   # optional
go generate ./...
go build ./cmd/todo
{{< /code >}}

`validate` checks the spec and conf against their JSON Schemas and rotini's lint rules, and
reports each problem with its `file:line:col`:

{{< code title="a mistyped key" language="text" open="true" collapsible="false" copy="false" >}}
$ go tool rotini validate ./cmd/todo/.rotini.spec.yaml --config ./cmd/todo/.rotini.conf.yaml
spec: ./cmd/todo/.rotini.spec.yaml
conf: ./cmd/todo/.rotini.conf.yaml
Error: spec: ./cmd/todo/.rotini.spec.yaml:31:19: /command/commands/0/flags/0/sumary: unknown key "sumary" on a flag input
{{< /code >}}

Beyond the schemas, `validate` catches specs that can never work: bounds or lengths that leave no
value, enum values that aren't the declared type, `required` with a `default`, contradictory flag
groups, config keys read with two types, an identifier that hides every identifier of a parent's
cascading or short-circuit flag, and `-ab` beside `-a` and `-b`. It warns, without failing,
about an identifier that hides only some of them, one-dash multi-letter identifiers, flags with
no long form, and a literal default on a secret input. Two opt-in conf keys add warnings:
`validate.flags_first` for env and config inputs no flag or argument can set, and
`validate.posix_names` for a program name that isn't a POSIX utility name.

`generate` runs the same checks first, so `validate` is mostly for CI. To also check in CI that
the committed code matches the spec, run `go tool rotini generate --dry-run`: it writes nothing,
lists what would change, and exits 2 if anything would. See
[the companion CLI](/cli#rotini-generate) for its exit codes and the `dry_run_env` conf key.

### Keeping specs tidy

`go tool rotini fmt` rewrites the spec and conf in one canonical layout: keys in reading order
(a command's name and documentation, then its inputs, output and sub-commands), two-space
indentation, and lists indented under their key. Comments move with the entries they describe, and every value is kept exactly as written,
quotes and block scalars included. With no file it formats the spec in the working directory
and the conf beside it; name files to format others, such as a composed command's file.

In CI, `--check` writes nothing and exits 2 when a file isn't formatted:

{{< code title="terminal" language="sh" open="true" collapsible="false" copy="true" >}}
go tool rotini validate ./cmd/todo/.rotini.spec.yaml
go tool rotini fmt --check ./cmd/todo/.rotini.spec.yaml ./cmd/todo/.rotini.conf.yaml
go tool rotini generate --dry-run ./cmd/todo/.rotini.spec.yaml
{{< /code >}}

Only YAML files can be formatted for now.

### Starting from a template

`rotini init <name> --template <shape>` starts from a shape other than the plain seed. Every
format works (`--format yaml|json|jsonc|toml`), and `--dry-run` lists what it would write.

| Shape | What you get |
|---|---|
| `plugin` | A kubectl-style plugin. Name it `<host>-<plugin>` (`rotini init kubectl-hello --template plugin`); help shows it as `kubectl hello`. Completion is on, and `multicall: {complete: kubectl_complete-}` makes the same binary answer the host's completion requests: install a copy of it (or a link) named `kubectl_complete-hello` beside `kubectl-hello` on `PATH`. |
| `daemon` | A long-running `serve` command. Its handler does the work every `--interval` until Ctrl-C, SIGTERM or `--for` stops it, then tears down in `PostRun`. The handler is yours from the start: `generate` never rewrites it. |
| `suite` | Two binaries, `<name>` and `<name>-admin`, that both compose a shared `status` command from its own spec in `cmd/status`. Change `status` once and both binaries pick it up. `<name>`'s `main.go` regenerates `status` before itself, so `go generate ./...` keeps all three in step. |

### Examples that parse

`validate` checks each `examples:` line that runs the program against the spec, with the same
parser a run uses: the command path, the flags, enum values and the number of arguments. Values
aren't run, nothing is read from files or stdin, and an input with an environment or config
fallback may be left out. A line that doesn't start with the program's name (or its
`display_name`) is prose and is skipped; in a pipeline or a list (`|`, `&&`, `;`) each command
that runs the program is checked. A word with `$`, a backtick or a glob, and a `<placeholder>`,
stands for any value. Words after a composed command or a plugin aren't checked.

{{< code title="a stale example" language="text" open="true" collapsible="false" copy="false" >}}
Error: spec: ./cmd/todo/.rotini.spec.yaml:12:7: command todo: example "todo lst --all" does not parse: unknown command "lst" for "todo"
{{< /code >}}

### Importing a Cobra CLI

An existing Cobra program can start its spec for you; Cobra is the one framework `rotini import`
reads today. Point `rotini import cobra` at the package that builds the command tree, usually the
one declaring `rootCmd`:

{{< code title="terminal" language="sh" open="true" collapsible="false" copy="true" >}}
go tool rotini import cobra ./cmd
go get github.com/go-rotini/rotini
go build ./cmd/acme
{{< /code >}}

It reads the tree the program builds at run time, so it sees what the program declares, and
writes `cmd/<name>/.rotini.spec.yaml` and its conf, then generates as `init` does: a handler for
every command, plus the help and version handling `init` seeds. Your program's files and
`go.mod` are left as they are, so both CLIs build side by side while you port; when Cobra's
code already lives in `internal/cmd/<name>`, pass `--dir <name>-rotini`. If the root isn't a
package-level variable or a function like `NewRootCmd()`, name it with `--root`, any Go
expression that yields a command.

| Cobra | rotini spec |
|---|---|
| `Use` name, `Short`, `Long`, `Aliases`, `Hidden`, `Deprecated`, `Example` | `name`, `summary`, `description`, `aliases`, `hidden`, `deprecated`, `examples` |
| `GroupID` and the group's title | `group` |
| `PersistentFlags()` / `Flags()` | flags with / without `cascading: true` |
| pflag types, defaults, `NoOptDefVal` | `type`, `default`, `implicit_value` |
| required, filename and dirname annotations | `required`, `complete: {kind: file}` with `extensions`, `complete: {kind: directory}` |
| `MarkFlagsRequiredTogether`, `MarkFlagsOneRequired`, `MarkFlagsMutuallyExclusive` | `flag_groups` kinds `required_together`, `at_least_one`, `mutually_exclusive`; the last two over one set become `one_of` |
| `Args` validators and the `Use` line's placeholders | `arguments` with their counts |
| `ValidArgs` | an `enum` on the first argument, with descriptions |
| `DisableFlagParsing` | `passthrough: true` |
| `Flags().SetInterspersed(false)` | `options_first: true` |
| `help` and `completion` commands, `--help` and `--version` | the seeded help command and flags; one `completion` command taking the shell, with the completion feature on |

A command with no `Args` rule accepts any positionals in Cobra, so it imports with a trailing
`args` list; `--strict` leaves that out and keeps only the arguments its `Use` line names.

What can't be carried across exactly is reported on stderr, one line each: `lossy` (carried
across with something lost, such as a custom `pflag.Value` imported as a string or a completion
function to rewrite as a completer), `unsupported` (dropped), and `info`. In a YAML spec the
`lossy` and `unsupported` lines also sit as comments above the command they concern, next to a
pointer to each hook to port:

{{< code title="cmd/acme/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="false" >}}
    # cobra RunE -> rotini Run: cmd/deploy.go:42
    # [lossy] --at is a time flag: the formats it accepts are not exported; set layout: if they are not RFC 3339
    - name: deploy
{{< /code >}}

Move each hook's code into the matching method of the command's handler: `PersistentPreRun` to
`CascadingPreRun`, `PreRun` to `PreRun`, `Run` to `Run`, and so on. A few behaviors differ, and
the import notes each one that applies:

- Rotini prints no deprecation warnings itself; a hook reads `rotini.Deprecations` and prints
  them.
- A command with sub-commands runs its own handler when invoked bare; the generated handler
  prints help.
- Every `CascadingPreRun` from the root down runs, not only the nearest persistent hook.
- A command's own flags are also accepted after a sub-command's name.

Flags and commands a program adds while running (inside a run hook, an `OnInitialize` function
or a plugin loader) aren't in the tree, so they aren't imported. Neither are viper's bindings:
declare `variable:` and `key:` on the inputs, `env_prefix`, and `config_files` with `discover`.
The import runs the package's `init` functions, as its tests do, and fails with an explanation
when one calls `flag.Parse`. See [the companion CLI](/cli#rotini-import) for its flags.

## Commands, flags and arguments

The root command is the binary, and `commands:` nests sub-commands to any depth. Each command
declares its own `flags:` and `arguments:`, and each input has a `schema:` that sets its type and
rules:

{{< code title="cmd/todo/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
  commands:
    - name: add
      summary: add a task
      aliases: [a]
      arguments:
        - name: title
          summary: the task title
          schema: { type: string, required: true, minLength: 1 }
      flags:
        - name: priority
          summary: how urgent
          identifiers: [-p, --priority]
          schema: { type: string, default: normal, enum: [low, normal, high] }
        - name: tag
          summary: a label
          identifiers: [--tag]
          schema: { type: '[]string' }
{{< /code >}}

- **Types** are Go names (`string`, `int`, `bool`, `[]string`, `map[string]string`) or value
  types that rotini parses for you: `duration`, `date`, `url`, `ip`, `bytesize` and
  [more](/specification#type).
- **Rules** such as `required`, `default`, `enum`, `pattern`, `minimum` and `maximum`, lengths and
  item counts are checked when the handler reads its inputs with `rtx.Inputs`, which the generated
  handler does first. A bad value is a usage error naming the flag the user typed.
  An empty value (`--name ""`, `--name=`, `''`) counts as given; add `minLength: 1` to refuse it.
- **A flag works anywhere after the command that declares it**, including after a sub-command's
  name. A flag written *before* a sub-command's name belongs to a parent, which is what lets a
  parent and a sub-command both declare a flag with the same name. A *different* flag that
  reuses an identifier of a cascading or short-circuit parent flag is flagged by `validate`.
- **`cascading: true` also lists a flag in its sub-commands' help**, under "Global Flags"
  (GLOBAL OPTIONS in man pages). It changes help only: parsing is the same, and a sub-command's
  handler sees a parent's flags either way, since its generated inputs include them. Mark a flag
  that sub-commands use `cascading: true` so their help shows it. To rename the heading, set
  `cascading:` under the command's [`headings:`](/specification#headings).

Mistakes are reported before your code runs:

{{< code title="terminal" language="text" open="true" collapsible="false" copy="false" >}}
$ ./todo add "buy milk" -p urgent
Error: invalid value "urgent" for -p (one of: low, normal, high)
$ ./todo add
Error: missing required input: <title>
$ ./todo add "buy milk" --loud
Error: unknown flag "--loud"
{{< /code >}}

### Where flags stop

A flag works anywhere after its command until a `--`, which makes every later word an argument.
A command can move that point earlier:

- **`options_first: true`** stops flags at the command's first argument, as POSIX utilities do,
  so `app ssh host -v ls` passes `-v ls` on. A `--` after that argument is an argument too, and so
  is a `--help` there. Sub-commands don't inherit it.
- **`passthrough: true` on the last argument** (a `[]string`) takes every word from that argument
  on as typed. The command's own flags still parse before it: `app exec --region eu ls -la --help`
  sets `--region` and passes `ls -la --help` on. A first word that starts with `-` goes after `--`
  (`app exec -- -x`). `rtx.DashIndex()` says how many arguments came before a `--` the user typed,
  for a handler that forwards them. `passthrough: true` on the command itself takes every word
  after its name raw, with no flags at all.

Other argument shapes:

- **A digit option**, `identifiers: ['-4', --ipv4]` on a bool or count flag, makes `-4` (and
  `-46`, clustered) a flag, while other negative numbers stay numbers. Quote it in YAML.
  `validate` rejects one where an argument of the command, or of a command below it, takes
  negative numbers.
- **A variadic argument before fixed ones**, `<src...> <dst>`: the fixed arguments after it must
  be required, and take the last words.
- **A literal `@`** for a `from: [file]` flag is doubled: `--to @@alice` gives `@alice`, and
  `--to @./@name` reads a file whose name starts with `@`.
- **Enum values with summaries**, `enum: [json, {value: yaml, summary: human-friendly}]`, are
  described on the help, man and markdown pages and in completion; the value is still what the
  user types.

### Response files

`response_files: {prefix: "@"}` on the root reads arguments from a file: before the first `--`,
`@args.rsp` is replaced by the file's lines, one argument per line, with blank lines and `#`
comment lines skipped. Words read from a file aren't expanded again, and `@@x` is the literal
argument `@x`. Expansion happens before anything else reads the command line, so a file can hold
the command's name, and `rtx.Argv` holds the expanded words. Choose a prefix other than `@` when a
flag or argument reads `from: [file]`. The root man and markdown pages tell users about the
prefix and the doubled form.

### Repeated values

A single-value flag given twice keeps the last value, so a script can override an alias's
setting. Two schema keys make repeats an error instead:

- **`repeatable: false`** on a single-value flag rejects a second occurrence, naming both values:
  `--name was given more than once ("a", then "b"); it takes one value`. `-vv` and
  `--color --no-color` are repeats too, and so is a root flag given before and after a
  sub-command's name. Environment and config values never count as repeats.
- **`uniqueItems: true`** on a list rejects one that holds the same value twice:
  `--port must not repeat a value (got "01" twice)`. Values compare as their type reads them,
  so `01` and `1` are the same int and `1h` and `60m` the same duration; a time compares as the
  instant it names, and a list of objects compares whole objects. It applies on every channel,
  including an environment list (`PORTS=80,80`), and in `rtx.CheckInputs`.

### Deprecating a command or flag

`deprecated: <message>` marks a command, flag, argument, environment or config input as
deprecated: help, man and markdown show the message beside it. To deprecate only some
spellings, list them in `deprecated_identifiers` (aliases for a command, identifiers for a
flag); the others are the ones to move to.

Rotini prints nothing when a deprecated spelling is used. `rotini.Deprecations(rtx)` lists what
this run used, and the handler decides what to say. In the root handler's `CascadingPreRun`,
one loop covers every command on the path:

{{< code title="internal/cmd/todo/todo.go" language="go" open="true" collapsible="false" copy="true" >}}
for _, d := range rotini.Deprecations(rtx) {
	rtx.RecordWarning(d)
}
{{< /code >}}

Plan the removal with `deprecated_since` and `removed_in` (both `X.Y.Z`) beside `deprecated`.
Pages then read `(deprecated since 1.4.0, removed in 2.0.0: use --config)`, the contract
carries both, and each `Deprecation` has them as `Since` and `RemovedIn`, so a handler can
compare them with `rtx.Version()` (for example with `golang.org/x/mod/semver`, which wants a
leading `v`). To plan the removal of a deprecated spelling while the command or flag stays, map
it to its release with `deprecated_identifiers_removed_in`:

{{< code title=".rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
flags:
  - name: config
    identifiers: [--config, --conf]
    deprecated_identifiers: [--conf]
    deprecated_identifiers_removed_in: {--conf: 2.0.0}
  - name: legacy
    deprecated: use --config
    deprecated_since: 1.4.0
    removed_in: 2.0.0
{{< /code >}}

`rotini validate --release 2.0.0` then fails for each item still declared whose removal is due
at or before that release, so a planned removal can't ship by accident. Set the conf's
`validate.release_env` to the name of a variable your release job sets, and `rotini validate`
and `rotini diff` read the release from it; `--release` wins. `rotini generate` never runs this
check.

### Pointing at a replacement

`replaced_by` beside `deprecated` names what to use instead: on a command, its path below the
root (`purge`, `remote add`), without the program's name; on a flag, one of its identifiers. A
command from a composed spec names a command of that spec, and rotini writes it under the path
the parent mounts it at. Help, man and markdown add it to the note, `(deprecated: renamed; use
todo purge instead)`, and `validate` checks that it exists and isn't deprecated itself:

{{< code title="cmd/todo/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
commands:
  - name: clear
    deprecated: renamed
    replaced_by: purge
    flags:
      - name: out
        deprecated: renamed
        replaced_by: --output
        schema: { type: string }
{{< /code >}}

Each `Deprecation` carries it as `ReplacedBy`, written as the user types it (`todo purge`,
`--output`, or the enum value), so the handler can say it:

{{< code title="internal/cmd/todo/todo.go" language="go" open="true" collapsible="false" copy="true" >}}
for _, d := range rotini.Deprecations(rtx) {
	if d.ReplacedBy != "" {
		rtx.RecordWarning(fmt.Errorf("%w; use %s instead", d, d.ReplacedBy))
		continue
	}
	rtx.RecordWarning(d)
}
{{< /code >}}

### Times, patterns and negated forms

- **Several time layouts**, `layout: ['2006-01-02 15:04', '2006-01-02', unix]`, are tried in
  order and the first that parses wins. Help shows the first, an error names them all, and
  `validate` warns when an earlier layout would read a later one's values as a different time.
- **Relative times**, `relative: past`, also accept a time measured from now: `2h` or `3d` ago,
  `now`, `today` and `yesterday`. `future` takes `2h` or `+2h` from now and `tomorrow`; `both`
  needs a sign (`-2h`, `+3d`). Absolute values are still read first. On a `date` the result is the
  calendar date it falls on, and an offset must be whole days. Every relative value in a run is
  measured from one clock reading, which `rtx.Now()` returns; set the clock in tests with
  `Program.WithClock`:

  ```go
  at := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
  code, err := cmd.NewProgram(cmd.Handlers()).
  	WithClock(func() time.Time { return at }).
  	Run([]string{"log", "--since", "2h"}) // since is 10:00
  ```

  `today` follows the clock's time zone, which is the process's unless the clock says otherwise.
- **`type: regexp`** is compiled when parsed into a `*regexp.Regexp`, so a bad expression is a
  usage error naming the flag. The syntax is Go's RE2, which has no backreferences or lookaround.
- **`type: glob`** is a `rotini.Glob`, a `path.Match` pattern checked when parsed, with a `Match`
  method that reads `\` in a name as `/` on Windows. `**` is not recursive: it is two `*`.
  It keeps the pattern as a value; it doesn't expand it into file names.
- **`negatable: --plain`** names the negated form of a bool flag instead of deriving
  `--no-color`: `--plain` sets `--color` false, and `--no-color` isn't accepted. Help lists it
  beside the flag, `--color, --plain`.

### Enum aliases, hidden and deprecated values

An enum value written as an object can declare more than a summary:

```yaml
schema:
  type: string
  enum:
    - json
    - { value: yaml, aliases: [yml] }
    - { value: xml, hidden: true }
    - { value: ini, deprecated: INI output is going away, deprecated_since: 1.4.0, removed_in: 2.0.0, replaced_by: json }
```

- An **alias** is accepted on every channel and bound as its value, so the handler only ever
  sees `yaml`. `deprecated_aliases: [yml]` retires one spelling while the value stays.
- A **hidden** value is accepted but never offered: help, completion and error messages leave it
  out.
- A **deprecated** value is still accepted and left out of the same lists. Given on the command
  line, it is reported by `rotini.Deprecations` with `Deprecation.Value` set, as a deprecated
  alias is. Man and markdown pages list each value with its aliases and deprecation; help keeps
  its short list of the values to use.

### Sharing flags between commands

Flags that several commands take are declared once under the root's `flag_sets`, and each
command adds them with `use:`. A set's flags come after the command's own, with the set's
`flag_groups` and `flag_dependencies`, and from then on they are the command's flags: they
parse, fall back, show in help and appear in the contract like any other. `group:` on the set
lists its flags under one heading in help, unless a flag names its own:

{{< code title="cmd/todo/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
command:
  name: todo
  flag_sets:
    Output:
      group: Output
      flags:
        - name: format
          summary: how to print
          schema: { type: string, enum: [text, json], default: text }
  commands:
    - name: list
      use: [Output]
    - name: show
      use: [Output]
{{< /code >}}

The generated code has one struct per set, embedded in each command's flags, so a handler reads
`in.TodoList.Flags.Format` whether the flag is the command's own or from a set, and moving a
flag into a set changes no handler code. A helper that every using command shares can take the
set's struct:

{{< code title="internal/cmd/todo/print.go" language="go" open="true" collapsible="false" copy="true" >}}
func print(rtx *rotini.Context, out TodoOutputFlagSet, items []Item) { /* … */ }

print(rtx, in.TodoList.Flags.TodoOutputFlagSet, items)
{{< /code >}}

Set names are PascalCase, since each names a struct. A set belongs to the spec that declares
it: a composed child uses its own sets. `validate` rejects a set flag whose name or generated
field repeats one of the command's, and warns about a set no command uses.

### Flags that depend on other flags

`flag_groups` constrain which flags go together (`mutually_exclusive`, `required_together`,
`one_of`, `at_least_one`). A `flag_dependencies` entry is a rule with a trigger and an effect:

- **`when: <flag>`** triggers the rule when that flag is set, and **`equals: [...]`** narrows it
  to some of the flag's values. The value is compared as the flag stores it, so `ignore_case`
  and enum aliases apply, and a bool compares as `true` or `false` (`--no-x` is `false`).
- **`unless: [...]`** turns the rule off when any of those flags is set. Without `when`, the
  rule applies on every run unless one of them is set.
- **`requires: [...]`** must then be set, and **`forbids: [...]`** can't be.

{{< code title="cmd/todo/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
flag_dependencies:
  - when: format
    equals: [csv]
    requires: [delimiter]
  - when: all
    forbids: [limit]
  - unless: [anonymous]
    requires: [token]
{{< /code >}}

{{< code title="terminal" language="text" open="true" collapsible="false" copy="false" >}}
$ ./todo export --format csv
Error: flag --delimiter is required when --format is csv
$ ./todo export --all --limit 5
Error: flag --limit can't be used when --all is set
{{< /code >}}

Only the command line counts, for groups and dependencies alike: a value from the environment,
a config file or a default neither triggers a rule nor satisfies one. A short-circuit flag such
as `--help` waives them, and `rtx.CheckInputs` applies them to values you collected yourself,
comparing the typed values.

### Hidden aliases and identifiers

`hidden_aliases` on a command and `hidden_identifiers` on a flag are accepted on the command
line but listed nowhere: not in help, completion or suggestions. Use them for an old spelling
that scripts still use, without advertising it. The help resolvers accept a hidden alias too,
the contract lists them as `hidden_aliases` and `hidden_identifiers`, and they may be listed in
`deprecated_identifiers` to report their use:

{{< code title="cmd/todo/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
commands:
  - name: remove
    aliases: [rm]
    hidden_aliases: [del]
    flags:
      - name: output
        identifiers: [-o, --output]
        hidden_identifiers: [--out]
        schema: { type: string }
{{< /code >}}

## Where values come from

A flag can also be read from an environment variable and a configuration file. Give it a `key:`
and declare the file:

{{< code title="cmd/todo/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
command:
  name: todo
  env_prefix: TODO
  config_files:
    - name: user
      discover: { strategy: xdg, app: todo, file: config.yaml }
  commands:
    - name: add
      flags:
        - name: priority
          identifiers: [-p, --priority]
          schema: { type: string, default: normal, key: defaults.priority }
{{< /code >}}

`--priority` now falls back to `TODO_DEFAULTS_PRIORITY`, then to `defaults.priority` in
`$XDG_CONFIG_HOME/todo/config.yaml` (by default `~/.config/todo/config.yaml`), then to its
default. The command line always wins:

{{< code title="terminal" language="text" open="true" collapsible="false" copy="false" >}}
$ cat ~/.config/todo/config.yaml
defaults:
  priority: high
$ ./todo add "buy milk"
added "buy milk" (priority high)
$ TODO_DEFAULTS_PRIORITY=low ./todo add "buy milk"
added "buy milk" (priority low)
$ TODO_DEFAULTS_PRIORITY=low ./todo add "buy milk" -p normal
added "buy milk" (priority normal)
{{< /code >}}

Help shows both sources on the line under the flag, the variable first because it wins. The
config key appears only for a command that reads a config file:

{{< code title="terminal" language="text" open="true" collapsible="false" copy="false" >}}
$ ./todo add --help
...
Flags:
  -p, --priority string    how urgent (default normal) [low|normal|high]
                           also set by TODO_DEFAULTS_PRIORITY or config key defaults.priority
      --tag []string       a label (repeatable)
{{< /code >}}

Use `variable:` to name the environment variable exactly instead of deriving it. Config files can
also be found by walking up from the working directory (`strategy: walk-up`) or read from a fixed
`path:`; see [`config_files`](/specification#config_files).

Commands can also declare pure `env:` and `config:` inputs, and a typed `stdin:` payload. They all
land in the same generated inputs struct.

### Arguments from the environment and files

An argument takes `variable:` and `key:` as a flag does, for a position the command line leaves
out: the command line, then the environment, then the config file, then the default.

```yaml
- name: deploy
  arguments:
    - name: env
      schema: { type: string, required: true, variable: DEPLOY_ENV }
    - name: service
      schema: { type: string, default: api }
```

`DEPLOY_ENV=prod ./app deploy` deploys `api` to `prod`, and `./app deploy dev` still wins over the
variable. Positionals fill left to right, so a fallback applies only when every argument before
it has a value, and `validate` asks for a fallback or a default on any argument after one with a
fallback: otherwise `./app deploy web` would put `web` into `<env>`. A variadic argument splits
its variable on its `separator`; without one, the whole value is one item. Help shows the
sources under the argument, and an error about such a value names the variable it came from.

`from: [file, stdin]` works on an argument before any variadic one, as on a flag: `./app cat -`
reads stdin and `./app cat @notes.txt` the file. Stdin can be read once, so one input on a
command path may take it.

An env list input splits its variable on commas; `separator: ':'` splits a `PATH`-style value
instead (`KUBECONFIG=a:b`). It's a plain split with spaces trimmed, not the CSV quoting a flag's
separator allows, and item counts and per-item rules are checked on the split items.

### Reading stdin

A declared `stdin:` is read once per run, whole, and held in memory; a second `rtx.Inputs` call
sees the same payload. For a filter that should handle any size, stream it: `stream: true` on
`format: lines` (or `jsonl`, one JSON record per line) makes the field an iterator that reads one
item at a time.

`unless_argument:` names an optional file argument: stdin is read only when that argument gets
no file, or `-`. This is the `grep PATTERN [FILE...]` shape, and it keeps a command that was
given a file from waiting on a stdin its caller holds open:

{{< code title="cmd/logs/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
commands:
  - name: grep
    arguments:
      - name: pattern
        schema: { type: string, required: true }
      - name: files
        schema: { type: '[]inputfile' }
    stdin:
      format: lines
      stream: true
      unless_argument: files
{{< /code >}}

{{< code title="internal/cmd/logs/logs_grep.go" language="go" open="true" collapsible="false" copy="true" >}}
in, err := rtx.Inputs[LogsGrepInputs]()
if err != nil {
	rtx.HaltWith(err)
	return
}
if in.LogsGrep.Stdin == nil {
	// Files were given (or stdin is a terminal): read in.LogsGrep.Arguments.Files.
	return
}
for line, err := range in.LogsGrep.Stdin {
	if err != nil {
		rtx.HaltWith(err) // a read error, or Ctrl-C: the run exits 130
		return
	}
	if strings.Contains(line, in.LogsGrep.Arguments.Pattern) {
		fmt.Fprintln(rtx.Stdout, line)
	}
}
{{< /code >}}

The iterator is nil whenever stdin is not read: a terminal, a short-circuit flag such as
`--help`, or a file given. Ranging over a nil iterator panics, so check it first. It reads the
run's one stdin: a `break` leaves the rest unread, and a later range continues from there. A
`jsonl` record is checked against the schema as it is read, and an error names its line
(`stdin line 12: …`). A line is limited only by memory. `CheckInputs` and
`InputReport.Validate` carry a stream but don't check its items.

`format: bytes` binds the payload byte for byte (`*[]byte`); every other format removes one
leading byte-order mark. `separator: nul` splits `lines` on NUL bytes instead of newlines, as
`find -print0` writes them, with each item kept exactly:

{{< code title="terminal" language="text" open="true" collapsible="false" copy="false" >}}
$ find . -name '*.log' -print0 | logs rotate
{{< /code >}}

A list or map flag with `from: [file]` or `from: [stdin]` takes one value per line of the file
(blank lines skipped), each split further on its `separator`; `separator: nul` splits that
content on NUL instead. `--tags @tags.txt` with `a,b` and `c` on two lines is `[a b c]`.

### Secrets in files and .env files

`variable_file:` names a variable holding the path of a file to read the value from, the way
Docker and Kubernetes mount secrets. Setting both forms is an error:

{{< code title="cmd/todo/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
env:
  - name: token
    schema: { type: string, variable: TODO_TOKEN, variable_file: TODO_TOKEN_FILE, secret: true }
{{< /code >}}

{{< code title="terminal" language="text" open="true" collapsible="false" copy="false" >}}
$ TODO_TOKEN_FILE=/run/secrets/todo ./todo sync
{{< /code >}}

A `config_files` entry with `format: dotenv` and `as: env` supplies environment variables
instead of configuration. Env inputs and flag fallbacks read it, and the real environment wins
variable by variable, so the order is: command line, environment, `.env` file, configuration
files, default. Its values are literal (`${OTHER}` is not expanded), and rotini's own variables
(`HOME`, `XDG_*`, `PATH`) never come from it. Prefer a fixed `path: .env` to discovery:

{{< code title="cmd/todo/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
config_files:
  - name: dotenv
    path: .env
    format: dotenv
    as: env
{{< /code >}}

### Secrets on the command line

A value typed on the command line shows in the process list and in shell history. A secret flag
or argument whose `from:` leaves out `value` refuses one, so the secret can only come from a
file or stdin:

{{< code title="cmd/todo/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
flags:
  - name: token
    schema: { type: string, secret: true, from: [file, stdin] }
{{< /code >}}

{{< code title="terminal" language="text" open="true" collapsible="false" copy="false" >}}
$ ./todo login --token sk_live_123
Error: --token takes @file or -, not a value
$ ./todo login --token @token.txt
$ ./todo login --token - < token.txt
{{< /code >}}

Environment variables and config files are still read as written, so `variable:` or
`variable_file:` beside it keeps those routes open. `validate` warns about a secret flag that
accepts a typed value; add `value` to `from` to keep accepting one on purpose. `rotini.ArgvOf`
can't write such a flag, since it has no file to point at: pass it through a file or stdin
yourself.

### Config directories

`discover:` finds a config file at run time with one of four strategies:

| Strategy | Searches |
|---|---|
| `walk-up` | the working directory, then each parent |
| `xdg` | `$XDG_CONFIG_HOME/<app>` (default `~/.config/<app>`) on every platform, the portable choice |
| `native` | the platform's own: `%AppData%\<app>` on Windows, `~/Library/Application Support/<app>` on macOS, as `xdg` elsewhere |
| `xdg-system` | each directory of `$XDG_CONFIG_DIRS` (default `/etc/xdg`), joined with `<app>` |

Declare a system tier as its own entry after the user's: the first declared entry wins per key,
so the user's file overrides the system one key by key.

### Profiles

One config file can hold several named sets of settings, with a flag or variable choosing one per
run, like `--profile prod`. Declare where the profiles live and which input chooses:

{{< code title="cmd/todo/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
command:
  name: todo
  config_files:
    - name: user
      discover: { strategy: xdg, app: todo, file: config.yaml }
      profiles: { under: profiles, select: profile, default: default }
  flags:
    - name: profile
      summary: the configuration profile to use
      identifiers: [--profile]
      cascading: true
      schema: { type: string, variable: TODO_PROFILE }
{{< /code >}}

{{< code title="~/.config/todo/config.yaml" language="yaml" open="true" collapsible="false" copy="false" >}}
server: { host: todo.example.com, port: 443 }
profiles:
  default: {}
  local:
    server: { host: localhost }
{{< /code >}}

The chosen profile's keys are read as if they sat at the top of the file, and win over the
file's other top-level keys, which every profile shares. With `--profile local`, the host is
`localhost` and the port still `443`. Merging is per key: a list in a profile replaces the shared
list, and a map's entries merge.

- **The choice:** `--profile` on the command line, then the first set variable (the flag's own
  `variable:` names, then an env input named by `select`), then the default. A `.env` file can set
  the variable. The choice is never read from a config file, so the selector can't declare `key:`.
- **Precedence** is otherwise unchanged: command line, environment, config files (the chosen
  profile, then the shared keys), default.
- **An unknown profile** chosen on the command line or in the environment is a usage error that
  lists the defined ones, and carries them as `SuggestionFacts` candidates:
  `--profile: profile "lcoal" is not defined in configuration file /home/ada/.config/todo/config.yaml (profiles: default, local)`.
  A default that a file doesn't define just leaves the shared keys, and `--help` never fails on a
  bad choice.
- **Several files** may share a selector: a profile is unknown only when none of them defines it.
- **The file's `schema:`** checks the shared keys merged with the chosen profile, without the
  `profiles` key. Profiles that aren't chosen aren't read or checked.
- **Provenance** names the profile: `config:user#profiles.local.server.host` for a key the
  profile set, `config:user#server.port` for a shared one.

Profiles are a single top-level key of named maps; a kubeconfig-style list of contexts isn't
supported. The generated [config schema](#editor-support-for-your-users-config) describes the
profiles key, and leaves out the file's own `schema:`, which checks the merged view instead.
`rotini.ConfigProfiles(rtx, "user")` lists the defined names for a
[completer](/recipes#a-profile-flag-with-completion).

### Writing config values

Rotini adds no `config` command, but a `config set` you declare can write a value into a config
file the way your users would by hand:

```go
err := rotini.SetConfigValue(rtx, "user", "server.port", "8443")
```

The arguments are the `config_files` name, the key as written in the file, and the value as the
user typed it. `rotini.UnsetConfigValue` removes a key, and `rotini.ConfigFilePath` reports
which file a write goes to and whether it exists yet. The
[recipe](/recipes#config-set-unset-and-path-commands) builds all three commands.

- **Checks:** the key must be one an input reads from the file. The value is checked as the
  command line checks the same input's flag: its type, enum, bounds, pattern and item counts, and
  the file's `schema:` after the edit. A list value is split on the input's `separator` (`,` when
  none), a map's entries are `key=value`, and an enum value is written in its declared spelling.
  A failed check is a usage error and leaves the file as it was:
  `invalid value "loud" for config key level (one of: debug, info, warn)`.
- **Which file:** the one the command reads, through its `config_source` path, fixed `path` or
  `discover` search. A missing file is created there, with its directories: `walk-up` creates it
  in the run's directory and `xdg` under `$XDG_CONFIG_HOME/<app>`. A file found by `xdg-system`
  is refused, since a user's file would hide every system setting. A symbolic link is followed.
- **The edit:** only the value's text changes; comments, key order, quoting and indentation stay.
  A new key goes at the end of its parent mapping. YAML, JSON, JSONC, TOML and dotenv files are
  written; a layout that can't be edited safely, such as a key reached through a YAML alias, is a
  usage error asking the user to edit by hand. The file is replaced atomically, keeping its
  permissions. Two commands writing one file at once can lose one of the changes.
- **Secrets:** a key declared `secret: true` is refused unless the call passes
  `rotini.AllowSecretWrite()`. A new file holding any secret key gets mode `0600`, and an existing
  file that others can read is refused rather than changed.
- **Profiles:** in a file with [profiles](#profiles), the value goes into the chosen profile,
  which is created if the file doesn't define it yet. `rotini.ToProfile(name)` and
  `rotini.ToSharedKeys()` pick another target. A shared key that the chosen profile overrides is
  written, with a warning.
- **Custom types:** an input whose type comes from `import:` needs
  `rotini.ParseConfigValue(fn)` to check its value; an object-valued key is edited by hand.

Each config file's keys, with their types and rules, are in the generated
`InputSettings.ConfigFiles[i].Keys`, which a key completer can offer.

### Paths

A value is taken as written unless its input declares `expand`. `home` expands a leading `~`
(or `~name`) and `env` expands `$NAME` and `${NAME}`, in values from every source: the command
line, environment variables, `.env` files, configuration files and the default. The expanded
path is what gets checked and bound. An `existingfile` or `existingdir` input is checked on
every channel, environment and configuration included:

{{< code title="cmd/todo/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
flags:
  - name: file
    identifiers: [--file]
    schema: { type: existingfile, expand: [home, env] }
config:
  - name: cache
    schema: { type: string, default: ~/.cache/todo, expand: [home], relative_to: config }
{{< /code >}}

{{< code title="terminal" language="text" open="true" collapsible="false" copy="false" >}}
$ ./todo add --file '~/notes.txt'
$ ./todo add --file '$NOTES/today.txt'
Error: --file: $NOTES is not set (in "$NOTES/today.txt")
{{< /code >}}

- An unset or empty variable is an error, and so are shell operators such as `${NAME:-x}`.
  Expansion is one pass: text a variable supplies is never expanded again.
- A command-line path that doesn't exist is named as expanded and as written:
  `--file: no such file: "/home/ada/x" (written as "~/x")`.
- `~name` reads the system's user database, so an injected environment can't change it.
- `%VAR%` is not expanded on Windows; `$NAME` and `~\` are.
- There is no escape for a literal `$NAME`. Leave `env` out for inputs that need one.
- `relative_to: config` resolves a relative value read from a configuration file against that
  file's directory. Values from anywhere else stay relative to the run's directory.
- A project file found by `walk-up` can set inputs it reads, so prefer `expand: [home]` there:
  with `env`, a cloned repository's file could write a token from your environment into a
  path. `rotini validate` warns about the combination. `expand` is refused on secret inputs.

### Files your program keeps

`rotini.AppDirs(rtx, app, strategy)` returns the config, data, cache and state directories for
`app`, read through the run's environment. It creates nothing. `strategy` takes the words of
`discover.strategy`: `xdg` for the XDG layout on every platform, or `native` for the
platform's own (`~/Library` on macOS, `%AppData%` and `%LocalAppData%` on Windows). Use the
same strategy as your config files, and create data and state directories with `0o700`:

{{< code title="internal/cmd/todo/todo_sync.go" language="go" open="true" collapsible="false" copy="true" >}}
dirs, err := rotini.AppDirs(rtx, "todo", "xdg")
if err != nil {
	rtx.HaltWith(err)
	return
}
if err := os.MkdirAll(dirs.State, 0o700); err != nil {
	rtx.HaltWith(err)
	return
}
{{< /code >}}

### A directory flag

`role: chdir` on a root flag makes it a directory flag, like git's `-C dir`: the program runs as
if it had been started in that directory. Rotini acts on whichever flag carries the role, under
the identifiers you declare; `-C, --dir` below is one spelling, and `-w, --workdir` works the
same.

{{< code title=".rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
command:
  name: todo
  flags:
    - name: dir
      summary: run as if started in this directory
      identifiers: [-C, --dir]
      role: chdir
      cascading: true
      schema: { type: existingdir }
{{< /code >}}

Rotini reads the flag before anything else, wherever it is typed on the command line. Walk-up
config discovery starts from the directory, and relative config paths, `@file` values,
`existingfile` and `existingdir` checks, and `rotini.OpenInput` and `rotini.CreateOutput` resolve
against it. A plugin typed after it runs in it. A relative directory is resolved against the run's
directory (`WithDir`, else the process's), and the flag binds as the absolute path. Given more
than once, the last wins; one that isn't a directory is a usage error, even beside `--help`.

The flag is read from the command line only, so `rotini validate` keeps it narrow: on the root,
cascading, an `existingdir` or `string`, one per program, with no default, `key`, `variable` or
`from:`, and outside flag groups and dependencies.

Rotini never changes the process's working directory. `rtx.Dir()` returns the run's, so a
handler that opens a relative path joins it first:

{{< code title="opening a file" language="golang" open="true" collapsible="false" copy="true" >}}
f, err := os.Open(filepath.Join(rtx.Dir(), name))
{{< /code >}}

A shell completes the words after the directory flag against its own directory; Rotini's
completers see `rtx.Dir()`.

### Explaining where values came from

`rtx.InputsWithReport` returns the inputs with an `InputReport`, and `report.Format(w)` writes one
line per field a layer set: the value, where it came from, and what it overrode, nearest first.
A secret prints `[redacted]`, whatever layer supplied it:

```console
$ todo deploy prod --token s3cr3t --explain-inputs
TodoDeploy.Arguments.Target = "prod" from argv:<target>
TodoDeploy.Flags.ExplainInputs = "true" from argv:--explain-inputs
TodoDeploy.Flags.Port = "9000" from env:TODO_PORT; overrides config:user#deploy.port "8000", default "8080"
TodoDeploy.Flags.Token = [redacted] from argv:--token
```

Each `InputSource` names its origin: `argv:<identifier as typed>`, `argv:<argument>`,
`env:<VARIABLE>` (`env:TODO_HTTP__*` for a nested family; the `variable_file` variable, such as
`env:TODO_TOKEN_FILE`, for a value read from its file), `config:<file name>#<key>`, `default` or
`stdin`. A hand-built layer's origin is empty, and the line names its layer. A streamed stdin
prints `(stream)` and is never read.

To offer this to users, declare a hidden short-circuit flag on the command and print the report
in its handler, before checking the error, since the run may be the one being explained:

{{< code title="internal/cmd/todo/todo_deploy.go" language="golang" open="true" collapsible="false" copy="true" >}}
func (*todoDeployHandler) Run(ctx context.Context, rtx *rotini.Context) {
	in, report, err := rtx.InputsWithReport[TodoDeployInputs]()
	if in.TodoDeploy.Flags.ExplainInputs {
		_ = report.Format(rtx.Stdout)
		return
	}
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	// …
}
{{< /code >}}

With a short-circuit flag set, the requirements are waived and a declared `stdin:` isn't read,
so the explanation works on a command line that is still incomplete. Inputs belong to the
running command, so the flag goes on each command that explains its own, not on the root.

To list each field with only the value that won, as a `config list --show-origin` command does,
walk `report.Fields()` and read `report.Winner(path)`; `report.History(path)` returns every
layer that set it, the winner last. See [the recipe](/recipes#showing-where-settings-came-from).

### Editor support for your users' config

With `generate.schemas.config.dir` in the conf, `rotini generate` writes a JSON Schema for each
config file the spec declares: `todo.user.config.json` for a file named `user` on the root,
`todo-deploy.team.config.json` for one declared on `deploy`. It lists every key a command that
reads the file binds, nested by dot, with its type, enum values and their summaries, default
(never a secret's), bounds, description and deprecation. Other keys are allowed, since rotini
ignores them. A file's own `schema:` is included, and dotenv files get none.

{{< code title=".rotini.conf.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
generate:
  schemas:
    config:
      dir: schemas/config
{{< /code >}}

Publish the files with your docs, and your users point their editor at them: a
`# yaml-language-server: $schema=<url>` first line in YAML, `#:schema <url>` in TOML, or a
`"$schema"` key in JSON.

### Example config and .env files

The `config_example` feature writes an example of each config file the spec declares, in the
file's own format, for your users to copy: `examples/config/<name>.example.yaml` (or `.toml`,
`.jsonc`, `.json`, `.env`), with the conf's `file` choosing the directory. `env_example` writes
`.env.example`, every environment variable the program reads.

{{< code title=".rotini.conf.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
generate:
  features:
    - type: config_example
      enabled: true
    - type: env_example
      enabled: true
{{< /code >}}

Every key is commented out, so a copy changes nothing until a line is uncommented. A line starting
with `# ` (`// ` in JSONC) is a key, nested by dot as the file holds it; a line starting with `## `
(`/// `) is a note: the summary, type, enum values and default. A key any file may hold is listed
once, in the first such file's example; a key pinned to a file with `file:` is listed in that
file's. A secret's value is left blank, and a file with profiles shows where they go. JSON has no
comments, so a JSON file's example holds only the keys with a default, set. With
`generate.schemas.config` on, YAML and TOML examples point editors at the file's schema.

{{< code title="examples/config/app.example.yaml" language="yaml" open="true" collapsible="false" copy="false" >}}
## app: ./todo.yaml, read by todo and its sub-commands.
## Each line starting with "# " is a key: remove the "# " to set it.

# deploy:
##   how long a deploy may take (duration; default 5m)
#   timeout: 5m
{{< /code >}}

Both features also generate `ConfigExample(name string) (string, error)`, keyed by the config
file's name, and `EnvExample() string`, so a command of your own can print them (`todo config
example app`). `embed` and `embed_dir` choose how that text is stored, as for help. The files are
rewritten on every generate and never removed.

## Handlers

`go generate` creates one handler file per command, once, and never overwrites it: it is yours.
When its command leaves the spec, the next `go generate` disables the file (adding
`//go:build ignore`) and the one after deletes it. If the command comes back in between, the file
returns to the build as it was. To keep it, delete its `var _ rotini.Handler` line or list it
under the conf's [`keep:`](/configuration#keep). Fill in `Run`:

{{< code title="internal/cmd/todo/todo_add.go" language="golang" open="true" collapsible="false" copy="true" >}}
package todo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*todoAddHandler)(nil)

type todoAddHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*todoAddHandler) Run(ctx context.Context, rtx *rotini.Context) {
	// One call reads every declared source (command line, environment, config files, stdin
	// and defaults) in precedence order, and validates the result.
	inputs, err := rtx.Inputs[TodoAddInputs]()
	if err != nil {
		rtx.HaltWith(err) // record the error and stop; the reporter prints it and sets the exit code
		return
	}

	add := inputs.TodoAdd
	fmt.Fprintf(rtx.Stdout, "added %q (priority %s)\n", add.Arguments.Title, add.Flags.Priority)
}
{{< /code >}}

- **`TodoAddInputs` is generated** from the spec, so the compiler holds the handler to it.
- **Five hooks, in this order:** `CascadingPreRun` runs for every command on the path from the
  root to the invoked command, root first. `PreRun`, `Run` and `PostRun` run only for the invoked
  command. `CascadingPostRun` runs for every command on the path again, invoked command first.
  Teardown (`PostRun` and `CascadingPostRun`) runs even after a halt or panic, for exactly the
  hooks whose setup ran. The `No*` embeds are no-ops; declare the method to use a hook.
- **Write to `rtx.Stdout`**, not `os.Stdout`, so tests can capture it.
- **Record results and errors** with `rtx.RecordSuccess`, `rtx.RecordWarning` and `rtx.HaltWith`
  rather than printing them. The runtime reports them once, after teardown.

The runtime itself only works out which command was invoked. Flags and arguments are parsed and
validated when a handler calls `rtx.Inputs[T]()`, or one of the per-source methods
`rtx.ArgvInputs`, `EnvInputs`, `FileInputs`, `StdinInputs` and `DefaultInputs`. A handler that
calls none of them gets the raw `rtx.Argv` and no validation, so you can bring your own parser or
collect inputs another way, and check what you collect with
[`rtx.CheckInputs`](#checking-inputs-you-collected-yourself).

A dependency the handlers share, such as a database or an API client, is registered once in
`main.go` and read in any hook:

{{< code title="sharing a dependency" language="golang" open="true" collapsible="false" copy="true" >}}
var Store = rotini.NewDependency[*store.Store]("todo.store") // in the cmd package

cmd.NewProgram(cmd.Handlers()).WithDependency(cmd.Store, openStore()) // in main.go
s := rtx.MustGetDependency(Store)                            // in a handler
{{< /code >}}

A dependency built from the command line, such as a client for the server a flag names, is set
per run instead; see [per-run dependencies](#per-run-dependencies).

### Flags that skip the run

Some flags replace a command's run instead of changing it: `--help`, `--version`, or a flag of
your own such as `--print-plan`. Mark one `short_circuit: true` in the spec. When it is set on
the command line, Rotini waives every requirement the spec declares for the command chain
(required inputs, enums, bounds, patterns, flag groups and flag dependencies), so `rtx.Inputs`
succeeds and your handler can act on the flag. Input that can't be read is still an error: an
unknown flag or command, a value of the wrong type, or too many arguments. A configuration file
that can't be parsed, or holds a value of the wrong type, is skipped for that run, so `--help`
works in a broken directory; a normal run still reports it. Each hook reads only the channels its
inputs type describes: a hook whose inputs have no configuration values or fallbacks opens no
configuration file. Rotini takes no action of its own; your handler checks the flag and decides
what to do.

`rotini init` marks `--help` and `--version` this way, makes both cascading so every command's
page lists them, and writes one `CascadingPreRun` in the root handler that answers both for every
command: `--version` prints the program's name, then its version (`todo 1.2.3`). So a command's handler needs no help code, and `todo add --help` works even with the
title missing.

A short-circuit flag can belong to one command, too:

{{< code title="a command-local short circuit" language="golang" open="true" collapsible="false" copy="true" >}}
// spec, under the deploy command:
//   - name: print-plan
//     summary: print what would be deployed, and deploy nothing
//     short_circuit: true
//     schema: { type: bool }

func (*deployHandler) Run(ctx context.Context, rtx *rotini.Context) {
	in, err := rtx.Inputs[DeployInputs]() // succeeds even with <service> missing
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	if in.Deploy.Flags.PrintPlan {
		fmt.Fprintf(rtx.Stdout, "%+v\n", in.Deploy)
		return
	}
	// … deploy
}
{{< /code >}}

A short-circuit flag must be a `bool`. It can't be `required`, default to `true`, be `negatable`,
be read from an environment variable or config key, or be named in a flag group or dependency,
since it waives those; `rotini validate` reports any of these.
Completion offers nothing more once one is on the line.

### Checking inputs you collected yourself

When values come from somewhere rotini didn't read, such as a prompt, a secrets service or a
test, check them against the spec with `rtx.CheckInputs`. It applies the same rules `rtx.Inputs`
applies (required inputs, enums, bounds, patterns, flag groups and dependencies) and returns the
same errors:

{{< code title="checking prompted answers" language="golang" open="true" collapsible="false" copy="true" >}}
argv, err := rtx.ArgvInputs[TodoAddInputs]()
if err != nil {
	rtx.HaltWith(err)
	return
}

in := askForMissing(rtx, argv.Values) // your own prompting code

if err := rtx.CheckInputs(in, rotini.PresenceOf(in)); err != nil {
	rtx.HaltWith(err)
	return
}
{{< /code >}}

The second argument says which fields were supplied. `rotini.PresenceOf(in)` treats every
non-zero field as supplied; when a zero value (`false`, `0`, `""`) must count as supplied, build
the `rotini.Presence` yourself. A value's rules are checked only when it was supplied, with one
exception that matches the command line: a list flag or variadic argument left out has zero
items, so a `minItems` still applies to it unless it has a default. `CheckInputs` reads nothing,
applies no defaults and never changes the value, and it names each input by its usual spelling
(`--priority`, `<title>`).

A layer you build yourself can also join rotini's own in a merge. Its values are checked too when
you validate the merged report:

{{< code title="mixing your own source with rotini's" language="golang" open="true" collapsible="false" copy="true" >}}
argv, _ := rtx.ArgvInputs[TodoAddInputs]()
env, _ := rtx.EnvInputs[TodoAddInputs]()
vault := rotini.InputLayer[TodoAddInputs]{Name: "vault", Values: fromVault(), Set: vaultSet}

merged, report := rotini.MergeInputsWithReport(env, vault, argv)
if err := report.Validate(); err != nil {
	rtx.HaltWith(err)
	return
}
{{< /code >}}

## Errors and exit codes

A handler that fails calls `rtx.HaltWith(err)`. The program's reporter runs once, after
teardown, and decides what to print and which exit code to use. The default reporter prints each
warning to stderr as `Warning: …` and each error as `Error: …`, and exits 1 when anything failed,
unless a handler already set a non-zero code with `rtx.HaltWithCode`. It prints infos and
successes to stderr too, so stdout carries only what handlers write. After Ctrl-C (or another
trapped signal) it leaves out the error the cancellation caused, and the run exits 128+n.

Every error carries a category, which `rotini.CategoryOf(err)` returns: `rotini.CategoryUsage`
for a mistake the user can fix, `rotini.CategoryInternal` for a fault in the program, and
`rotini.CategoryNone` for an error nobody classified. Parse and validation failures are already
usage errors. Mark your own with `rotini.UsageError(err)` or `rotini.InternalError(err)`.

To give each kind of failure its own exit code, write a reporter. A reporter replaces the default
entirely: it prints only what it prints, and it sets the exit code with `rtx.Exit`. If it
doesn't, the run keeps any code a handler set, and otherwise exits 0, so give it a fallback
code for errors with no category. The codes below are common conventions, not Rotini defaults:

{{< code title="internal/cmd/todo/report.go" language="golang" open="true" collapsible="false" copy="true" >}}
package todo

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-rotini/rotini"
)

// Exit codes: 2 for a usage error (BSD's sysexits.h uses 64, EX_USAGE), 70 (EX_SOFTWARE) for
// a fault in the program, and the shell's 127 and 126 for a plugin that is missing or can't
// be run.
const (
	exitUsage         = 2
	exitInternal      = 70
	exitNotExecutable = 126
	exitNotFound      = 127
)

// Report prints warnings and errors to stderr, points at help after a usage error, and sets the
// exit code from the first error.
func Report(ctx context.Context, rtx *rotini.Context, out rotini.Outcome) {
	if ctx.Err() != nil {
		return // stopped by a signal: keep the 128+n exit code the run already set
	}
	for _, w := range out.Warnings {
		fmt.Fprintln(rtx.Stderr, "Warning:", w)
	}
	for _, err := range out.Errors {
		fmt.Fprintln(rtx.Stderr, "Error:", err)
	}
	for _, p := range out.Panics {
		fmt.Fprintln(rtx.Stderr, "Error:", p)
	}
	if !out.Failed() {
		return
	}
	if len(out.Errors) > 0 && rotini.CategoryOf(out.Errors[0]) == rotini.CategoryUsage {
		fmt.Fprintf(rtx.Stderr, "Run '%s --help' for usage.\n", rtx.CommandPath())
	}
	rtx.Exit(exitCode(out))
}

func exitCode(out rotini.Outcome) int {
	if len(out.Errors) == 0 {
		return exitInternal // only panics
	}
	err := out.Errors[0]
	var pe *rotini.PluginError
	if errors.As(err, &pe) { // before the category: a mistyped plugin name is also a usage error
		switch pe.Kind {
		case rotini.PluginNotFound:
			return exitNotFound
		case rotini.PluginStartFailed:
			return exitNotExecutable
		}
	}
	switch rotini.CategoryOf(err) {
	case rotini.CategoryUsage:
		return exitUsage
	case rotini.CategoryInternal:
		return exitInternal
	}
	return 1
}
{{< /code >}}

- **Check `ctx.Err()` first.** After Ctrl-C or SIGTERM the run has already set 128+n (130 or
  143), and the canceled context's error is in `out.Errors`; without the check, the reporter
  would replace that code. The same goes for a cancellation with
  [`ExitCause`](#signals-and-cancellation).
- **A reporter can't read the code a handler set.** `rtx.Exit` replaces any code from
  `rtx.HaltWithCode`, so map only the errors you mean to, or carry a handler's code in an error
  type of your own, as in [linking exit codes to docs](#linking-exit-codes-to-docs).
- **A plugin's own exit code** passes through unchanged and never reaches the reporter. A file
  found next to the binary or in `plugin_path` that can't be run is `PluginStartFailed`; one on
  `PATH` without the execute bit isn't found at all, so it exits 127. A plugin that runs past its
  `timeout:` falls through to 1 here.
- **List the codes** under `exit_status:`, so man and markdown pages and the contract say what
  they mean. `--help` leaves them out.

Install it in `main.go` with `cmd.NewProgram(cmd.Handlers()).WithReporter(cmd.Report)`. Keeping it in the command
package, rather than in `main.go`, lets tests install the same reporter (see [Testing](#testing)).
The `Outcome` also carries `Infos` and `Successes`, from `rtx.RecordInfo` and
`rtx.RecordSuccess`, for a reporter that prints those too.

For errors that scripts parse, `rotini.StructuredReporter` writes them as JSON lines; see
[errors scripts can read](#errors-scripts-can-read). Declare `exit_status:` in the spec to
document a command's codes in its man and markdown pages.

Once a command declares `exit_status:`, `rotini generate` checks its handler against it: a code
the handler passes to `rtx.HaltWithCode` or `rtx.Exit` as a number or a constant, but that the
list leaves out, is reported as a warning with its file and line. Code 0 needs no entry. A
command that prints its help when called without a sub-command exits 1, so list 1 for it. (The
stub `rotini generate` writes for a root or a group command does this, printing the help on
stderr.) The check reads only the methods of the command's handler type, so it can't see a code
computed at run time, set in another function or package, or set by a reporter, and it skips a
command whose handler lives in another package (`handler:`). `rotini validate` also reports a
code listed twice, and warns about a code above 128, which a process stopped by a signal also
exits with.

Give a code a `name` and `rotini generate` writes a constant for it, named after the command,
so a handler doesn't repeat the number:

{{< code title=".rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
- name: get
  exit_status:
    - { code: 3, name: not_found, summary: no such task }
    - { code: 4, name: busy, summary: the store is locked, retryable: true }
{{< /code >}}

{{< code title="internal/cmd/todo/todo_get.go" language="go" open="true" collapsible="false" copy="true" >}}
rtx.HaltWithCode(TodoGetExitNotFound)
{{< /code >}}

Names are snake_case and unique within a command. The man and markdown EXIT STATUS sections
and the contract show them, and `retryable: true` tells callers such as agents that running the
command again may succeed. The constants are in the cmd package, or in the models package when
the conf declares one, re-exported in the cmd package. The check above reads a constant like its
number, and warns when a handler uses another command's constant: declare the code on the
command that exits with it.

### Linking exit codes to docs

`docs_url` on an `exit_status` entry links the code to a page about it. The man and markdown EXIT
STATUS sections show the link, and the contract carries it. Help and the default reporter don't
print it. To print it when the command exits with that code, write a reporter: the generated
Definition carries each code's link as `ExitStatusDef.DocsURL`.

{{< code title=".rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
- name: get
  exit_status:
    - { code: 3, name: not_found, summary: no such task, docs_url: "https://todo.example/errors/not-found" }
{{< /code >}}

A reporter can't read the code a handler set, so the handler records it with the error:

{{< code title="internal/cmd/todo/report_links.go" language="golang" open="true" collapsible="false" copy="true" >}}
package todo

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-rotini/rotini"
)

// ExitError ends a command with one of its documented exit codes:
// rtx.HaltWith(&ExitError{Code: TodoGetExitNotFound, Err: err}).
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }

// ReportWithLinks prints warnings and errors to stderr, and after an ExitError the link its
// code declares, then exits with that code (1 for any other failure).
func ReportWithLinks(def rotini.Definition) rotini.Reporter {
	return func(ctx context.Context, rtx *rotini.Context, out rotini.Outcome) {
		for _, w := range out.Warnings {
			fmt.Fprintln(rtx.Stderr, "Warning:", w)
		}
		code := 0
		for _, err := range out.Errors {
			fmt.Fprintln(rtx.Stderr, "Error:", err)
			var exit *ExitError
			if errors.As(err, &exit) && code == 0 {
				code = exit.Code
				if url := docsURL(def, rtx.CommandChain(), code); url != "" {
					fmt.Fprintln(rtx.Stderr, "See", url)
				}
			}
		}
		for _, p := range out.Panics {
			fmt.Fprintln(rtx.Stderr, "Error:", p)
		}
		switch {
		case !out.Failed() || ctx.Err() != nil:
		case code != 0:
			rtx.Exit(code)
		default:
			rtx.Exit(1)
		}
	}
}

// docsURL returns the docs_url the invoked command declares for code, or "".
func docsURL(def rotini.Definition, chain []rotini.Command, code int) string {
	exits, cmds := def.ExitStatus, def.Commands
	for _, c := range chain[1:] {
		for _, d := range cmds {
			if d.Name == c.Name {
				exits, cmds = d.ExitStatus, d.Commands
				break
			}
		}
	}
	for _, e := range exits {
		if e.Code == code {
			return e.DocsURL
		}
	}
	return ""
}
{{< /code >}}

Install it with the program's own Definition: `p := cmd.NewProgram(cmd.Handlers())`, then
`p.WithReporter(cmd.ReportWithLinks(p.Definition()))`.

### Handling errors in a handler

An error about the user's input, from `rtx.Inputs`, the per-source methods, `rtx.CheckInputs` or
`report.Validate`, is one of two types:

- **`*rotini.ParseError`**: the command line, and validation of any value. Its `Kind` says what
  went wrong, `Token` holds the value at fault and `Candidates` what it could have been.
- **`*rotini.InputError`**: reading an environment variable, a config file or stdin. `Channel`
  and `Input` say which.

(`rtx.Inputs` can also return a `*rotini.WiringError`, for a fault in the program's own setup
rather than the input.) Wrapping an error with `rotini.UsageError` or `rotini.InternalError`
changes only its category: `errors.Is` and `errors.As` still reach the original.

Branch on the type, the kind or the category, never on the message text, which can improve
between releases:

{{< code title="branching on an error" language="golang" open="true" collapsible="false" copy="true" >}}
inputs, err := rtx.Inputs[TodoAddInputs]()
if err != nil {
	var pe *rotini.ParseError
	switch {
	case errors.As(err, &pe) && pe.Kind == rotini.ParseKindMissingRequired:
		fmt.Fprintf(rtx.Stderr, "%s\nRun '%s --help' for usage.\n", err, rtx.CommandPath())
		rtx.HaltWithCode(2)
	case rotini.CategoryOf(err) == rotini.CategoryUsage:
		rtx.RecordError(err)
		rtx.HaltWithCode(2)
	default:
		rtx.HaltWith(err)
	}
	return
}
{{< /code >}}

The kinds are `ParseKindUnknownFlag`, `ParseKindUnknownCommand`, `ParseKindNeedsValue`,
`ParseKindInvalidValue`, `ParseKindEnumViolation`, `ParseKindConstraintViolation`,
`ParseKindMissingRequired`, `ParseKindNoArguments`, `ParseKindTooManyArguments`,
`ParseKindMisplacedFlag` and `ParseKindInternal`. `ParseKindMisplacedFlag` is a flag typed
where it can't apply: before a plugin's name, where the plugin would never see it
(`todo --verbose sync` is refused; `todo sync --verbose` passes `--verbose` to the plugin). A
short-circuit flag such as `--help` and a [directory flag](#a-directory-flag) are the exceptions:
the program answers or applies them itself. This error reaches the reporter, not a handler.

Each response is one call:

- **Point at help**: print a hint built from `rtx.CommandPath()`, or the whole page with
  `rtx.Help()`.
- **Suggest a correction**: `rotini.NewSuggestor().For(err)` returns the nearest candidates; see
  [suggesting a correction](#suggesting-a-correction).
- **Choose the exit code**: `rtx.HaltWithCode(n)`. The first non-zero code wins, and the default
  reporter keeps it. `rtx.HaltWithCode` records no error, so print the error yourself (as the
  first branch does) or record it with `rtx.RecordError` for the reporter to print.
- **Or just stop**: `rtx.HaltWith(err)` records the error and leaves the code to the reporter.
- **Or carry on**: `rtx.RecordError(err)` records the error without stopping, so a handler can
  collect several and check `rtx.Failed()` later.

When an exit code means something, list it under the command's `exit_status:`. Handle an error
in the handler when the response depends on the command; for one policy across the whole
program, use a [reporter](#errors-and-exit-codes).

#### Suggesting a correction

When a user mistypes something from a fixed list, the error carries the word they typed and the
words it could have been:

- a flag, a command, or a value outside a flag's or argument's `enum`: a `*rotini.ParseError`,
  in `Token` and `Candidates`;
- an environment variable or config value outside its `enum`: a `*rotini.InputError`, in
  `Token` and `Candidates`;
- a mistyped sub-command at a command with `plugin_discovery`: a `*rotini.PluginError`, in
  `Name` and `Candidates`. It reaches the reporter, not a handler.

`rotini.NewSuggestor().For(err)` reads them from any of the three and returns the nearest
candidates, closest first, or nothing when none is close. The wording is yours:

{{< code title="in a handler or reporter" language="golang" open="true" collapsible="false" copy="true" >}}
if hits := rotini.NewSuggestor().For(err); len(hits) > 0 {
	fmt.Fprintf(rtx.Stderr, "Did you mean %q?\n", hits[0])
}
{{< /code >}}

To rank them your own way, read them with `rotini.SuggestionFacts(err)`, which returns the
typed word, the candidates and whether `err` carried them:

{{< code title="your own ranking" language="golang" open="true" collapsible="false" copy="true" >}}
if typed, candidates, ok := rotini.SuggestionFacts(err); ok {
	if best := closest(typed, candidates); best != "" {
		fmt.Fprintf(rtx.Stderr, "Did you mean %q?\n", best)
	}
}
{{< /code >}}

`WithMaxResults(n)` caps how many suggestions `For` returns, and `WithMinScore(s)` sets how close
one must be, from 0 to 1. `Closest(typed, candidates)` returns the single nearest. A secret
input's value is never offered for ranking. Rotini never prints a suggestion itself.

### Printing the usage line

`rtx.Usage()` returns the usage line of the command whose hook is running, the line its help
prints under `Usage:` (`todo add [flags] <title>`): the spec's `usage:` when set, else the line
rotini derives from the command's shape. The generated `Usage(path...)` returns any command's,
by names or aliases, and is generated whether or not the help feature is on. Nothing prints the
line by default. A reporter can print it after a usage error:

{{< code title="internal/cmd/todo/report_usage.go" language="golang" open="true" collapsible="false" copy="true" >}}
package todo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

// ReportWithUsage prints warnings and errors to stderr, then the command's usage line after a
// usage error, and exits 1 when the run failed.
func ReportWithUsage(ctx context.Context, rtx *rotini.Context, out rotini.Outcome) {
	for _, w := range out.Warnings {
		fmt.Fprintln(rtx.Stderr, "Warning:", w)
	}
	for _, err := range out.Errors {
		fmt.Fprintln(rtx.Stderr, "Error:", err)
	}
	for _, p := range out.Panics {
		fmt.Fprintln(rtx.Stderr, "Error:", p)
	}
	if len(out.Errors) > 0 && rotini.CategoryOf(out.Errors[0]) == rotini.CategoryUsage {
		if u := rtx.Usage(); u != "" {
			fmt.Fprintf(rtx.Stderr, "Usage: %s\n", u)
		}
		fmt.Fprintf(rtx.Stderr, "Run '%s --help' for more.\n", rtx.CommandPath())
	}
	if out.Failed() && ctx.Err() == nil {
		rtx.Exit(1)
	}
}
{{< /code >}}

```console
$ todo add
Error: missing required input: <title>
Usage: todo add [flags] <title>
Run 'todo add --help' for more.
```

A command with a verbatim `help:` page still has the spec's line, so the two can differ. In a
handler, use `rtx.Usage()` rather than the generated function: a command composed from another
spec then reports the line of the program it runs in. A schema named `Usage` would collide with
the generated function, and `rotini validate` reports it.

## Runtime control

How a run stops: on a signal, on a canceled context or a deadline, and after a panic, and how
to give one run its own dependencies. What a run used is covered elsewhere:
[where each value came from](#explaining-where-values-came-from) and
[which deprecated spellings it used](#deprecating-a-command-or-flag).

### Signals and cancellation

By default Rotini traps Ctrl-C (SIGINT) and SIGTERM. The first signal cancels the run's context
and stops the run as `rtx.HaltWithCode` does: no further hook starts, the teardown of every hook
whose setup ran still runs, and the run exits 128+n, so 130 for SIGINT and 143 for SIGTERM. A
second signal exits 130 at once. Cancellation never interrupts a running hook, so a hook that
waits on something passes `ctx` to it or checks `ctx.Err()`.

| Setting | Signals trapped |
|---|---|
| none | SIGINT and SIGTERM |
| `WithSignals(sigs...)` | exactly these, also with a context you supply (the trap cancels a child of it) |
| `WithContext(ctx)`, or `RunContext(ctx, argv)` | none, unless `WithSignals` is set: canceling `ctx` is yours |
| `WithoutSignalHandling()` | none |

`WithSignals()` with no signals restores the default. Signals Rotini doesn't trap keep Go's
behaviour: SIGHUP ends the process at once with no teardown (129), unless you add it to
`WithSignals` or [handle it yourself](/recipes#reloading-config-on-sighup), and a write to a
closed pipe, as in `todo list | head -1`, ends the program quietly with 141.

To stop a run with an exit code of your own, cancel its context with `rotini.ExitCause`:

{{< code title="cmd/todo/main.go (time limit)" language="golang" open="true" collapsible="false" copy="true" >}}
//go:generate go tool rotini generate ./.rotini.spec.yaml --config ./.rotini.conf.yaml
package main

import (
	"context"
	"os"
	"syscall"
	"time"

	"github.com/go-rotini/rotini"
	cmd "github.com/me/todo/internal/cmd/todo"
)

var version = "0.0.0"

// main stops a run that takes longer than ten minutes with exit code 124, and still stops on
// Ctrl-C and SIGTERM.
func main() {
	ctx, cancel := context.WithCancelCause(context.Background())
	time.AfterFunc(10*time.Minute, func() { cancel(rotini.ExitCause(124)) })

	cmd.NewProgram(cmd.Handlers()).
		WithContext(ctx).
		WithSignals(os.Interrupt, syscall.SIGTERM).
		WithVersion(version).
		Execute()
}
{{< /code >}}

A cancellation without an `ExitCause` stops the run the same way, and the exit code is decided
as usual. The default reporter leaves out the error a signal's cancellation caused; a
[custom reporter](#errors-and-exit-codes) checks `ctx.Err()` to keep the code the run set.

### Passing a context on

Every hook receives the run's `context.Context`. A hook that derives one, to add a deadline or a
tracing span, hands it to the hooks after it with `rtx.SetContext`, so a root `--timeout` can
limit the command's `Run`:

{{< code title="internal/cmd/todo/todo.go" language="golang" open="true" collapsible="false" copy="true" >}}
type todoHandler struct {
	rotini.NoPreRun
	rotini.NoPostRun
	cancel context.CancelFunc
}

func (h *todoHandler) CascadingPreRun(ctx context.Context, rtx *rotini.Context) {
	ctx, h.cancel = context.WithTimeout(ctx, 30*time.Second)
	rtx.SetContext(ctx)
}

func (h *todoHandler) CascadingPostRun(ctx context.Context, rtx *rotini.Context) {
	h.cancel()
}
{{< /code >}}

- **Later hooks get it**, `Run` included, and so do their teardowns. A teardown gets the context
  its own setup hook received: the `CascadingPostRun` above gets the run's context, never a
  deadline that has already passed.
- **Keep the cancel function in a handler field** and call it in the matching teardown. Handlers
  are created once per run, so the field belongs to this run.
- **A derived context ending doesn't stop the run.** Check `ctx.Err()` in the hook and stop with
  `rtx.HaltWith(err)`. A stdin read still waiting for data does end with it, returning an error
  the hook can halt with. Canceling the run's own context still stops it between hooks. An
  `ExitCause` on a derived context sets no exit code: use `rtx.HaltWithCode`.
- **Calls from a teardown change nothing**, and the reporter always gets the run's context.
  `rtx.Context()` returns the running hook's context; read it in the hook.

### A deadline after a signal

On Ctrl-C or SIGTERM, rotini cancels the run's context and waits for the hooks and their
teardowns to finish; a second signal exits at once with 130. A process that a supervisor stops
with one SIGTERM can't send the second, so a handler that ignores its context would keep it
running. `WithTerminationTimeout` bounds the wait:

{{< code title="cmd/todo/main.go" language="go" open="true" collapsible="false" copy="true" >}}
cmd.NewProgram(cmd.Handlers()).WithTerminationTimeout(10 * time.Second).Execute()
{{< /code >}}

If the run hasn't returned 10 seconds after the first signal, the program exits with that
signal's code (143 for SIGTERM, 130 for SIGINT), as a second signal would. The exit is abrupt:
buffered stdout is flushed if it can be, but the remaining teardown doesn't run and the reporter
may not have printed. The timeout covers the reporter too, and it has no effect when rotini
traps no signals (`WithoutSignalHandling`, or `WithContext` without `WithSignals`).

### Panics

A panic in a hook is recovered: the hooks after it don't start, teardown runs, and the reporter
receives it in `out.Panics` as a `*rotini.PanicError`, whose `Value` is what was passed to
`panic` and `Stack` the stack where it happened. The default reporter prints
`Fatal Error: <value>` without the stack, and the run exits 1. A dependency that
`rtx.MustGetDependency` can't find panics with a `*rotini.DependencyError`, and faults in the
program's own wiring, such as a command with no handler, arrive the same way with no stack.

Two settings change this:

| `WithPanicRecover` | `WithTeardownOnPanic` | What happens |
|---|---|---|
| `true` (default) | `true` (default) | teardown runs, then the reporter gets the panic |
| `true` | `false` | teardown is skipped, then the reporter gets the panic |
| `false` | `true` | teardown runs, then the panic is raised again, with a stack that starts there |
| `false` | `false` | the panic is never caught, so it keeps its original stack and no teardown runs |

Re-raising is for a host that recovers panics itself, or for debugging. Only the hook's own
goroutine is covered: a panic in a goroutine a handler starts crashes the process. To print the
stack on request, see [a DEBUG variable](#a-debug-variable); to save crash reports, see
[the recipe](/recipes#crash-reports).

### Per-run dependencies

`Program.WithDependency` gives every run the same value, which suits something built once in
`main.go`. A dependency that depends on the command line, such as a client for the server a
`--endpoint` flag names, is built in the root's `CascadingPreRun` and set for that run only:

{{< code title="internal/cmd/todo/client.go" language="golang" open="true" collapsible="false" copy="true" >}}
package todo

import "github.com/go-rotini/rotini"

// Client talks to the server --endpoint names.
type Client struct{ Endpoint string }

// clientDep is the run's client. Each run sets its own, so two runs in one process, such as
// parallel tests, never share one.
var clientDep = rotini.NewDependency[*Client]("todo.client")

// connect builds the run's client from its flags, from the root's CascadingPreRun. A client
// that a test registered with Program.WithDependency stays.
func connect(rtx *rotini.Context, in TodoInputs) {
	rtx.SetDependencyIfAbsent(clientDep, &Client{Endpoint: in.Todo.Flags.Endpoint})
}
{{< /code >}}

The root handler calls `connect(rtx, inputs)` after reading its inputs, and a command reads the
client with `rtx.MustGetDependency(clientDep)`. Each run starts with a copy of the program's
dependencies, so `rtx.SetDependency` and `rtx.SetDependencyIfAbsent` never reach another run.
`SetDependencyIfAbsent` keeps a value already registered, so a test that passes a double with
`NewProgram(Handlers()).WithDependency(clientDep, fake)` gets it. `rtx.GetDependency` returns
the value and whether it is set; `rtx.MustGetDependency` treats a missing one as a fault, which
reaches the reporter as a panic.

## Structured output

`output:` declares the **shape** of what a command writes. Rotini generates a Go type for it,
documents it, and publishes it as JSON Schema for scripts and other tools.

Writing the output, in whatever format, stays the handler's job: rotini adds no format flag. The
helpers below for writing and checking output are optional, and a command without `output:` gets
none of this.

### Declare the shape

`output:` is a schema. Named shapes go under the root's `schemas:`, and each becomes an exported
Go type. The format is an ordinary flag, declared here on the root with `cascading: true` so every
command's help lists it:

{{< code title=".rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
command:
  name: todo
  schemas:
    Task:
      type: object
      required: [id, title, status]
      properties:
        id: { type: integer }
        title: { type: string }
        status: { type: string, enum: [open, done] }
    TaskList:
      type: object
      description: Every task, oldest first.
      properties:
        tasks: { type: array, items: { $ref: "#/schemas/Task" } }
  flags:
    # … the help and version flags `rotini init` declared
    - name: output
      summary: how to write the result
      identifiers: [-o, --output]
      cascading: true
      schema: { type: string, default: table, enum: [table, json, yaml, toml] }
  commands:
    - name: list
      summary: list tasks
      output: { $ref: "#/schemas/TaskList" }
{{< /code >}}

`rotini generate` writes the types `Task` and `TaskList`, and `TodoListOutput`, the `list`
command's output type. A command that writes a stream of items, one at a time, declares the
shape of **one item**.

An exit status can declare a shape too, for an outcome that still writes data:

{{< code title=".rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
command:
  name: todo
  exit_status:
    - code: 3
      summary: some tasks failed; stdout lists the ones that succeeded
      output: { type: array, items: { $ref: "#/schemas/Task" } }
{{< /code >}}

### Write it

Choosing a format is up to you; here it is the value of the `--output` flag above.
`rtx.WriteOutput` is an optional helper that writes a value to stdout in the format you pass. It
writes json (indented), yaml and toml itself, and hands any other format to your renderer. Pass
`nil` as the renderer if you only ever use those three:

{{< code title="internal/cmd/todo/todo_list.go" language="go" open="true" collapsible="false" copy="true" >}}
func (*todoListHandler) Run(ctx context.Context, rtx *rotini.Context) {
	in, err := rtx.Inputs[TodoListInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	if err := rtx.WriteOutput(TodoListOutput{Tasks: load()}, in.Todo.Flags.Output, renderTable); err != nil {
		rtx.HaltWith(err)
	}
}

func renderTable(w io.Writer, format string, v TodoListOutput) error {
	fmt.Fprintln(w, "ID  STATUS  TITLE")
	for _, t := range v.Tasks {
		fmt.Fprintf(w, "%-3d %-7s %s\n", t.ID, t.Status, t.Title)
	}
	return nil
}
{{< /code >}}

`load()` stands for wherever your tasks come from. The same value comes out in each format:

```console
$ todo list
ID  STATUS  TITLE
1   open    write docs
2   done    ship
$ todo list -o json
{
  "tasks": [
    {
      "id": 1,
      "status": "open",
      "title": "write docs"
    },
    {
      "id": 2,
      "status": "done",
      "title": "ship"
    }
  ]
}
```

An empty format means json. A command that writes a stream of items, rather than one value,
declares `output_stream: true`, and its `output:` is then the shape of one item.
`rtx.WriteOutputItem` writes each item as it is ready: one compact JSON value per line, or one
YAML document per item, each starting `---`. A stream can't be written as toml. The pages'
OUTPUT section says the command writes a stream, and the contract marks it `stream: true`.
`WriteOutput` on such a command is an internal error. `rotini generate` warns when a handler
calls `WriteOutputItem` on a command that doesn't declare `output_stream: true`; the items are
still written.

Both return an internal error, and write nothing, when the value is not the command's
`<Prefix>Output` type or when a format they don't write has no renderer. Both are bugs in the
program, not mistakes by the user. A renderer's own error is returned as it is.

**Keep stdout for the output.** A script reading `todo list -o json` breaks if anything else
lands on stdout, such as a progress line before the JSON document. Write progress, notes and
prompts to `rtx.Stderr`.

### Templates

The `github.com/go-rotini/rotini/shape` package lets the user format the output with a Go
template, as in `--format '{{.title}}'`. Declare the flag with the type `shape.Template`,
imported through `import:`. A placeholder keeps help from showing the Go type:

{{< code title=".rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
command:
  name: todo
  flags:
    - name: format
      summary: format the output with a Go template
      cascading: true
      schema: { type: shape.Template, import: github.com/go-rotini/rotini/shape, placeholder: TEMPLATE }
{{< /code >}}

When the flag is set, pass `shape.Render` to `WriteOutput` or `WriteOutputItem` as the renderer:

{{< code title="internal/cmd/todo/todo_list.go" language="go" open="true" collapsible="false" copy="true" >}}
list := TodoListOutput{Tasks: load()}
if format := in.Todo.Flags.Format; !format.IsZero() {
	rtx.HaltWith(rtx.WriteOutput(list, "template", shape.Render[TodoListOutput](format)))
	return
}
rtx.HaltWith(rtx.WriteOutput(list, in.Todo.Flags.Output, renderTable))
{{< /code >}}

A template names fields by their JSON property names, the names `-o json` shows, not the Go
field names. `Render` ends what it writes with a newline, once per item for a stream:

```console
$ todo list --format '{{range .tasks}}{{.id}} {{.title}}{{"\n"}}{{end}}{{len .tasks}} tasks'
1 write docs
2 ship
2 tasks
```

Besides Go's template builtins (`printf`, `len`, `index`, `eq`, …), a template can call `json`,
the compact JSON of a value (`{{json .tags}}`), and `join`, a list joined by a separator
(`{{join ", " .tags}}`). Numbers print as JSON prints them, and every field the output type
declares is there, an empty optional one included.

A template that does not parse is a usage error when the flag is read, before the command runs.
One that names a field the output doesn't have is a usage error when it runs, and nothing is
written:

```console
$ todo list --format '{{.Title}}'
Error: template:1:2: executing "template" at <.Title>: map has no entry for key "Title"
```

A `default:` template is checked when the flag is read, not by `rotini validate`.

The template is the user's own code, run by your program. It sees only the value you pass, as
plain data, and its functions reach no files, environment or network. Pass outputs, never the
inputs value, which may hold secrets.

Importing `shape` links Go's `text/template`, which makes the binary larger. A program that does
not import it links no template code.

### Choosing fields and order

A `--json id,title` flag that writes only some fields, or a `--sort-by title` flag, takes its
values from the output's field names. Declare `values_from:` instead of an `enum`, and give the
flags their roles so a program driving the CLI knows what they do:

{{< code title=".rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
- name: list
  output: { $ref: "#/schemas/TaskList" }
  flags:
    - name: json
      summary: write these fields as json
      role: fields
      schema: { type: '[]string', separator: ',', values_from: output.tasks }
    - name: sort-by
      summary: sort by this field
      role: sort
      schema: { type: string, values_from: output.tasks }
    - name: reverse
      summary: sort in descending order
      schema: { type: bool }
{{< /code >}}

`output` is the command's output, or one item when the output is a list or a stream.
`output.tasks` is its `tasks` property, and a list on the way stands for its items, so here the
values are Task's fields. They become the flag's enum, sorted by name: help shows
`[id|status|title]`, completion offers them (after a comma too: `--json id,<TAB>` offers the
rest), a name the output doesn't have is a usage error, and the contract lists them. Rename an
output field and the flag follows. Only the item's own fields are listed, not nested ones.

`rotini.SortBy` sorts a slice by one field, and `rotini.SelectFields` keeps the fields the user
chose. Fields are named by their JSON names, the names `-o json` shows. Sort first, since the
sort field need not be selected:

{{< code title="internal/cmd/todo/todo_list.go" language="go" open="true" collapsible="false" copy="true" >}}
list := TodoListOutput{Tasks: load()}
flags := in.TodoList.Flags
if flags.SortBy != "" {
	if err := rotini.SortBy(list.Tasks, flags.SortBy, flags.Reverse); err != nil {
		rtx.HaltWith(err)
		return
	}
}
if len(flags.JSON) > 0 {
	sel, err := rotini.SelectFields(list, "tasks", flags.JSON)
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	rtx.HaltWith(rtx.WriteOutput(sel, "json", nil))
	return
}
rtx.HaltWith(rtx.WriteOutput(list, in.Todo.Flags.Output, renderTable))
{{< /code >}}

```console
$ todo list --json id,title --sort-by title
{
  "tasks": [
    {
      "id": 2,
      "title": "ship"
    },
    {
      "id": 1,
      "title": "write docs"
    }
  ]
}
```

`SelectFields`' second argument is where the items are: `values_from` without its `output`
prefix, so `""` for the output itself. Everything outside it, such as an envelope's `total`, is
kept, and a field an item lacks is left out of that item. A dotted field (`owner.login`) reaches
into a nested object, for a flag that declares such names in its own `enum`.

`WriteOutput` and `WriteOutputItem` accept the `rotini.Selection` it returns in place of the
output type. With output checks on, a selection is checked against the shape with every
`required` removed, since it leaves fields out. `DecodeOutput` still checks the whole shape, so
use it on full outputs only.

`SortBy` compares numbers as numbers, strings that are all RFC 3339 times as times, other
strings byte by byte, and `false` before `true`. Items without the field sort last in both
directions, and items whose values in it are of different kinds are an error. Both helpers read
the value through JSON, which is fine for the lists a command line prints.

### Check it

`Program.WithOutputChecks(true)` makes every `WriteOutput` and `WriteOutputItem` call check its value
against the declared shape before writing. It is off by default. Turn it on in tests, or in a
debug build:

{{< code title="todo_test.go" language="go" open="true" collapsible="false" copy="true" >}}
p := NewProgram(Handlers()).WithOutputChecks(true)
{{< /code >}}

With checks on, a value that does not match is an internal error naming each field at fault,
and nothing is written to stdout:

```console
$ todo list -o json
Error: todo list: output does not match its contract: output.tasks[2].status: value is not in enum
```

`rtx.CheckOutput(v)` makes the same check without writing anything.

`rotini.DecodeOutput[T]` reads captured stdout back in a test. It decodes json, yaml or toml
into the command's output type, checking it against the shape first. A stream is read into a
slice of the item type, and only a stream is:

{{< code title="todo_test.go" language="go" open="true" collapsible="false" copy="true" >}}
var stdout bytes.Buffer
p := NewProgram(Handlers()).WithStdout(&stdout)
p.Run([]string{"list", "-o", "yaml"})
list, err := rotini.DecodeOutput[TodoListOutput](p, stdout.Bytes(), "yaml")
{{< /code >}}

### Errors scripts can read

`rotini.StructuredReporter` builds a reporter for programs whose output scripts read. You pass
it your own rule for when a run is structured. When the rule says yes, it writes each error,
warning, info and success to **stderr** as one JSON object per line, so stdout carries only the
output. When the rule says no, it reports exactly as the default reporter does.

The reporter runs after the command, and the run may have failed because parsing did, so the
rule reads the command line itself rather than validated inputs. For a program that declares a
`--json` flag:

{{< code title="cmd/todo/main.go" language="go" open="true" collapsible="false" copy="true" >}}
cmd.NewProgram(cmd.Handlers()).WithReporter(rotini.StructuredReporter(func(rtx *rotini.Context) bool {
	return slices.Contains(rtx.Argv, "--json")
})).Execute()
{{< /code >}}

```console
$ todo list --json --bogus
{"schema_version":1,"error":{"candidates":["--json","--all"],"category":"usage","command":"todo list","exit_code":1,"flag":"--bogus","kind":"unknown-flag","message":"unknown flag \"--bogus\"","token":"--bogus"}}
```

Exit codes are decided exactly as the default reporter decides them. Each line's shape is
described by
[schema-error.json](https://github.com/go-rotini/rotini/blob/main/schema-error.json).

#### Candidates

When an error names the word at fault in `token`, the line also lists `candidates`: the words it
was checked against, as `rotini.SuggestionFacts` returns them. For a mistyped command they are
the command's visible sub-commands and their aliases, for an unknown flag its flag spellings, and
for a value outside an enum the allowed values, from the command line, the environment or a
config file. Rotini doesn't rank them or print a suggestion; a tool reading the line can offer the
nearest. Hidden spellings and values never appear, and a secret input's error carries neither a
token nor candidates.

### What gets generated

- **An OUTPUT section** in help, man and markdown pages: the shape's description, its type, and
  its top-level fields with their types and descriptions. An exit status that writes output
  says so. Its help heading is `headings.output`.
- **A STDIN section** in help, man and markdown pages for a command that declares `stdin:`: the
  format, whether it is required, and for a document its type, description and top-level
  fields. Its help heading is `headings.stdin`.
- **One JSON Schema per output**, with `generate.schemas.output.dir` in the conf:
  `todo-list.output.json` for a command, and `todo.exit-3.output.json` for an exit status.
  Each is standard JSON Schema (draft-07), with the named schemas it uses included. Hidden
  commands get none.
- **The contract document**, with `generate.contract.file`: one JSON file describing every
  command, including its arguments, flags, environment variables, configuration keys and files,
  stdin, output shape and exit statuses. Beside each input's JSON Schema it states what a
  caller needs to build a command line: the rotini type (`int8`, `duration`), the kind of value
  (`count` means repeat the flag, never `=3`), negated forms, separators, time layouts, and
  which inputs are secret. Hidden commands and inputs are listed with `hidden: true`. Each
  command also has a `parameters` JSON Schema covering its visible arguments and flags, so it
  maps directly onto a tool definition for an AI agent; `parameters`, `output` and the stdin
  schema each work on their own, carrying the definitions they use. Its format is described by
  [schema-contract.json](https://github.com/go-rotini/rotini/blob/main/schema-contract.json);
  validate a contract with the copy from the rotini that wrote it.
- **The contract in the binary**, with `generate.contract.go: true`: the generated cmd file holds
  the same document as `var Contract string`. Declare a command for it, such as `describe`, and
  print it from the handler: `fmt.Fprint(rtx.Stdout, Contract)`. It adds the document's size to
  the binary.

{{< code title=".rotini.conf.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
generate:
  schemas:
    output:
      dir: schemas/output
  contract:
    file: cli-contract.json
{{< /code >}}

## Files and streams

File arguments, `-` for stdin and stdout, files written safely, buffered stdout, file
patterns on Windows, and filters.

### Files and the standard streams

Two value kinds give a command the usual `-` conventions:

- **`inputfile`** is a file to read, where `-` means stdin. It is checked like `existingfile`,
  except that `-` passes, and only one `-` may be given in a run. `rotini.OpenInput(rtx, path)`
  opens either; stdin comes byte for byte, and closing it leaves stdin open. When stdin is a
  terminal, `-` is a usage error rather than a wait for typing.
- **`outputfile`** is a file to write, where `-` means stdout. It is checked to be no directory
  and to sit in an existing directory. `rotini.CreateOutput(rtx, path)` writes it atomically:
  to a temporary file that `Close` renames into place and `Abort` removes. A file the run leaves
  open is removed when the run ends. An existing file is replaced only with
  `rotini.Overwrite(true)`. Devices such as `/dev/null` are written directly.

Help, man and markdown pages add `(- for stdin)` or `(- for stdout)` to the input's line.

{{< code title="internal/cmd/todo/todo_cat.go" language="go" open="true" collapsible="false" copy="true" >}}
files := in.TodoCat.Arguments.Files // type: '[]inputfile'
if len(files) == 0 {
	files = []string{"-"}
}
for _, name := range files {
	f, err := rotini.OpenInput(rtx, name)
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	_, err = io.Copy(rtx.Stdout, f)
	f.Close()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
}
{{< /code >}}

{{< code title="internal/cmd/todo/todo_export.go" language="go" open="true" collapsible="false" copy="true" >}}
func export(rtx *rotini.Context, path string, force bool) error {
	out, err := rotini.CreateOutput(rtx, path, rotini.Overwrite(force))
	if err != nil {
		return err
	}
	defer out.Abort() // discards the file unless Close commits it
	if err := rtx.WriteOutputTo(out, TodoExportOutput{Tasks: load()}, "json", nil); err != nil {
		return err
	}
	return out.Close()
}
{{< /code >}}

`rtx.WriteOutputTo` and `rtx.WriteOutputItemTo` are `WriteOutput` and `WriteOutputItem` with a
writer of your choice. Mark the flag that allows replacing a file with `role: force`, so a
program driving the CLI knows which one it is. To write to a file and stdout at once, see
[the tee recipe](/recipes#writing-to-a-file-and-to-stdout-at-once).

### Buffered output

A command that writes many small pieces of output is much faster with stdout buffered.
`Program.WithBufferedOutput(true)` buffers `rtx.Stdout` for every run (not on a terminal, where
output shows as it is written). Rotini flushes it before the reporter runs and checks the
result, so a full disk or a closed pipe is an error exit even when the handler ignored its own
write errors:

{{< code title="cmd/todo/main.go (buffered)" language="go" open="true" collapsible="false" copy="true" >}}
cmd.NewProgram(cmd.Handlers()).WithBufferedOutput(true).Execute()
{{< /code >}}

Write through `rtx.Stdout`, never `os.Stdout`, or the output comes out of order. Flush before
waiting for input; `rtx.Stdout` then has a `Flush() error` method. It is no longer an
`*os.File`, so check for a terminal with `rotini.IsTerminal(rtx.Stdout)`, which reports whether
any stream with an `Fd` method is a terminal (`/dev/null` is not).

### Patterns on Windows

POSIX shells expand `*.txt` before the program sees it; the Windows shells don't. `glob: true`
on a variadic argument (`string`, `existingfile`, `existingdir` or `inputfile` elements) expands
such words on Windows only, relative to the run's directory. A word naming an existing path is
kept, and a pattern matching nothing is passed through, so the path check reports it as a POSIX
shell would. Matching is case-sensitive and `**` is not supported.

### Filters

A filter reads input, writes a result and sits anywhere in a pipeline. `logs grep PATTERN
[FILE...]` reads the files it is given in order, or stdin when there are none, with `-`
standing for stdin among them. Three declarations give that shape:

- an optional `[]inputfile` argument, so each file is checked before the run and `-` passes
  (see [files and the standard streams](#files-and-the-standard-streams));
- `stdin:` with `unless_argument:` naming that argument, so stdin is read only when no file is
  given, or one is `-` (see [reading stdin](#reading-stdin));
- `stream: true`, so stdin is read a line at a time and memory stays flat at any input size.

{{< code title="cmd/logs/.rotini.spec.yaml (filter)" language="yaml" open="true" collapsible="false" copy="true" >}}
version: 0.0.0
command:
  name: logs
  summary: read logs
  commands:
    - name: grep
      summary: print the lines that contain a pattern
      arguments:
        - name: pattern
          schema: { type: string, required: true }
        - name: files
          summary: files to read; stdin when none, or -
          schema: { type: '[]inputfile' }
      flags:
        - name: "null"
          identifiers: [-z, --null]
          summary: end each line with NUL instead of a newline, for xargs -0
          schema: { type: bool }
      stdin:
        format: lines
        stream: true
        unless_argument: files
{{< /code >}}

The handler reads each file through an iterator of the same type as the streamed stdin, so one
loop serves both. `bufio.Scanner` drops each line's `\n` or `\r\n`, as the stream does; its
buffer is raised here because by default a line over 64 KiB is an error. With `-z`, each match
ends with NUL instead of a newline, so lines holding spaces or quotes reach `xargs -0` intact:

{{< code title="internal/cmd/logs/logs_grep.go (filter)" language="go" open="true" collapsible="false" copy="true" >}}
package logs

import (
	"bufio"
	"context"
	"io"
	"iter"
	"strings"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*logsGrepHandler)(nil)

type logsGrepHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*logsGrepHandler) Run(ctx context.Context, rtx *rotini.Context) {
	in, err := rtx.Inputs[LogsGrepInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	g := in.LogsGrep
	end := "\n"
	if g.Flags.Null {
		end = "\x00"
	}
	files := g.Arguments.Files
	if len(files) == 0 {
		files = []string{"-"}
	}
	for _, name := range files {
		lines := g.Stdin // stdin, streamed; nil when it wasn't read
		if name != "-" || lines == nil {
			lines = readLines(rtx, name)
		}
		for line, err := range lines {
			if err == nil && strings.Contains(line, g.Arguments.Pattern) {
				_, err = io.WriteString(rtx.Stdout, line+end)
			}
			if err != nil {
				rtx.HaltWith(err)
				return
			}
		}
	}
}

// readLines reads a file, or stdin for "-", a line at a time, without line ends.
func readLines(rtx *rotini.Context, name string) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		f, err := rotini.OpenInput(rtx, name)
		if err != nil {
			yield("", err)
			return
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(nil, 16<<20) // lines up to 16 MiB; the default stops at 64 KiB
		for sc.Scan() {
			if !yield(sc.Text(), nil) {
				return
			}
		}
		if err := sc.Err(); err != nil {
			yield("", err)
		}
	}
}
{{< /code >}}

{{< code title="terminal" language="text" open="true" collapsible="false" copy="false" >}}
$ logs grep -z draft notes.txt | xargs -0 rm --
{{< /code >}}

When stdin is a terminal and no file is given, the stream is nil and `OpenInput(rtx, "-")`
returns a usage error rather than waiting for typing. For NUL-separated input, as
`find -print0` writes it, set `separator: nul` on `stdin:`. A filter that writes many small
pieces is much faster with [buffered output](#buffered-output).

A binary filter, such as a hash or a decompressor, wants stdin byte for byte at any size.
`format: bytes` holds the whole payload in memory, so leave `stdin:` out and open `-` yourself;
`OpenInput` makes no line-ending or byte-order-mark changes, and closing it leaves stdin open:

{{< code title="hashing stdin" language="go" open="true" collapsible="false" copy="true" >}}
f, err := rotini.OpenInput(rtx, "-")
if err != nil {
	rtx.HaltWith(err)
	return
}
defer f.Close()
h := sha256.New()
if _, err := io.Copy(h, f); err != nil {
	rtx.HaltWith(err)
	return
}
if _, err := fmt.Fprintf(rtx.Stdout, "%x\n", h.Sum(nil)); err != nil {
	rtx.HaltWith(err)
}
{{< /code >}}

On Windows, text files often end lines with `\r\n` and may start with a UTF-8 byte-order mark.
Stdin's `lines` format drops each line's `\r` (`separator: nul` keeps bytes exact), and every
stdin format but `bytes` removes one leading mark; a file opened with `OpenInput` keeps its
mark, so trim `"\ufeff"` (U+FEFF) from its first line when that matters. UTF-16, which Windows
PowerShell 5.1 writes with `>` and `Out-File`, isn't decoded: a stdin read whole stops with a
usage error saying so, while streamed stdin and opened files arrive as raw bytes. Have users
write UTF-8 (`Out-File -Encoding utf8`, or PowerShell 7, where it is the default), or decode it
with `golang.org/x/text/encoding/unicode`.

## Agent-ready CLIs

An AI agent runs a CLI the way a script does, and needs the same things: output it can parse,
errors it can read, and exit codes that mean something. It also needs to know what a command
will do before it runs it. Rotini turns what the spec declares into the files agents and their
tools read. Nothing appears unless you declare it, and none of it adds a flag or changes a run.

### Streams, errors and exit codes

- **Keep stdout for the output.** Write the declared output with `rtx.WriteOutput`, and leave
  messages to the reporter. The default reporter writes every record, infos and successes
  included, to stderr, so stdout holds only what handlers write.
- **Errors as JSON.** `StructuredReporter` (see [Errors scripts can read](#errors-scripts-can-read))
  writes each error to stderr as one JSON line, starting with `"schema_version":1`. The version
  changes only when a line's shape changes in a way that breaks a reader. Pick it from a
  variable you name, so an agent's harness can ask for it:

  {{< code title="cmd/todo/main.go" language="go" open="true" collapsible="false" copy="true" >}}
cmd.NewProgram(cmd.Handlers()).WithReporter(rotini.StructuredReporter(func(rtx *rotini.Context) bool {
	return os.Getenv("TODO_AGENT") == "1"
})).Execute()
{{< /code >}}

- **Exit codes** are yours to compose in the reporter (see
  [Errors and exit codes](#errors-and-exit-codes)); list them in `exit_status` with a `name` and
  `retryable`, and every agent output below carries them.
- **JSON when stdout isn't a terminal** is a choice your handler makes:
  `if !rotini.IsTerminal(rtx.Stdout) { format = "json" }` (see [Conventions](#conventions)).
- **A stream** of results is JSON Lines when the command declares `output_stream: true`, so a
  reader knows to read one value per line.
- **The contract as a command:** with `generate.contract.go: true`, a command such as
  `describe` can print `Contract`, so the binary describes itself (see
  [What gets generated](#what-gets-generated)).

### Effects

`effects` says what running a command does: `read` changes nothing, `write` creates or changes
things, `destructive` deletes or overwrites what can't be got back. `idempotent` and
`open_world` (it reaches the network or other systems) are optional. Help, man and markdown
show a line (`Effects: destructive, idempotent`); the contract and the generated
`CommandDef.Effects` carry it.

A flag can raise its command's effects for the run that gives it, never lower them, so a
preview flag is a role, not a `read` flag:

{{< code title="cmd/todo/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
- name: sync
  effects: { kind: write, idempotent: true, open_world: true }
  flags:
    - name: prune
      summary: also delete tasks the server doesn't have
      effects: { kind: destructive }   # sync --prune is destructive
    - name: dry-run
      role: dry-run
{{< /code >}}

A run's effect is the highest of the command's and every given flag's; it is idempotent only
when every stated value says so, and reaches outside the machine when any does. Sub-commands
don't inherit effects. Where the actual command line isn't known (tool annotations, permission
rules, the skill page), the worst case over the command and its flags is used. A command without
`effects` says nothing, and agents assume the worst.

### Roles agents read

`role` on a flag tells a program driving the CLI what the flag is for. Beside `force`, `fields`,
`sort` and `chdir`:

- `dry-run`: the bool flag that previews without changing anything;
- `confirm`: the bool flag that answers yes, so the command never prompts;
- `machine-output`: the flag that writes the declared output as JSON: a bool flag, or a string
  flag with an `enum` and `role_value: json` naming the value that does it;
- `page`: the flag that selects a page of results.

A command has each role at most once, counting the cascading flags it inherits. `rotini
validate` warns about a destructive command with neither a `dry-run` nor a `confirm` flag.

### Keeping things from agents

Some commands and inputs are left out of everything below by default: hidden and deprecated
ones; a command with sub-commands (it prints help) or a passthrough command (it would run
whatever it is given); and inputs that are secret (a value an agent types lands in the model's
context), read `from:` a file or stdin (an agent could read the host's files), short-circuit
(`--help`), or the [directory flag](#a-directory-flag). `agent: true` brings one back;
`agent: false` keeps a command, with its sub-commands, or an input out while it stays in help.
On env and config inputs, which are never tool parameters, `agent` only decides whether the
skill page lists them. `rotini validate` warns about `agent: true` on a secret flag or argument:
tool servers never pass a secret on the command line, where other local users can see it, so
supply it through its environment variable.

### Tool definitions

The `tools` feature writes tool definitions for each target, from the contract:

{{< code title=".rotini.conf.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
generate:
  features:
    - type: tools
      enabled: true
      file: tools/              # the default
      targets: [mcp, openai-strict, gemini]
{{< /code >}}

- **`mcp.json`** is a Model Context Protocol `tools/list` result. Each tool is named after the
  command path (`todo_remote_add`), its parameters are the command's arguments and flags, plus a
  `stdin` parameter when it reads stdin, and its `outputSchema` is the declared output (an array
  of items for a stream). Annotations come from `effects`, and the description lists the exit
  codes and the variables the server's environment must supply. Each tool's
  `_meta["dev.rotini/invoke"]` holds what a server needs to rebuild the command line: the
  command path, each parameter's identifier and kind (and whether it is secret or read `from:` a
  file or stdin), whether the command is passthrough, and the machine-output flag to add. It also
  carries what quoting needs: the response-file prefix (`response_prefix`) to double on a raw
  word before the first `--`, the `separator` of a variadic argument whose items are each quoted
  as list items, and `dotted_keys` on a map flag written as `a.b=value` pairs. It is
  written for MCP 2026-07-28; `mcp_revision: 2025-11-25` wraps an output that isn't an object,
  as that revision requires. With `go: true` the generated package also holds it as
  `var ToolsMCP string`, so the binary can serve itself; see
  [serving tools over MCP](#serving-tools-over-mcp).
- **`openai.json`** holds OpenAI Responses API function tools in strict mode: every property
  required, optional ones nullable, and constraints strict mode doesn't take (lengths, defaults)
  written into the description. A command with a map flag can't be expressed and is left out,
  with a warning.
- **`gemini.json`** holds Gemini function declarations with JSON Schema parameters, references
  inlined, and `-` in parameter names written `_` (`dry-run` is `dry_run`).

Env and config inputs are never parameters; on a server they come from its environment. A
command whose required input can't be a parameter is left out with a warning, unless it is a
secret the server's environment can supply. Env inputs are listed for the server.

### Serving tools over MCP

The [go-rotini/mcp](https://github.com/go-rotini/mcp) module serves `mcp.json` as an MCP server on
stdio, running the program for each call and returning what it prints:

```console
$ go install github.com/go-rotini/mcp/cmd/rotini-mcp@latest
$ rotini-mcp serve --tools tools/mcp.json --bin ./todo
```

`rotini-mcp config enable` adds the server to an MCP client's configuration. A program can serve
itself instead: with the feature's `go: true`, declare an `mcp` command with `agent: false` and
pass `ToolsMCP` to `mcp.Serve` in its handler, as the module's README shows. A call that is
canceled or runs past `--timeout` gets SIGTERM, so the program's signal handling runs, and is
killed after `--grace` (5s by default). Windows has no SIGTERM, so there it is killed at once.

### Agent pages

- **`skill`** writes `skills/<name>/SKILL.md`, an [Agent Skills](https://agentskills.io) page:
  the commands with their effects, how to call them (command words first, the JSON flag, the
  confirm flag instead of a prompt), the exit codes and error kinds, the environment, and each
  command's details and examples. `allowed-tools` lists the read-only commands. With the
  markdown feature on, each command's page goes in `references/`. `name` sets the skill's name
  (the root's, lowercased, by default) and `description` adds when to use it.
- **`llms`** writes an [llms.txt](https://llmstxt.org): the program's name and summary, then a
  link to each command's markdown page at `base_url`.

Both render from editable templates (`template: true` seeds `skill.md.tmpl` and
`llms.txt.tmpl`), like help.

### Permission rules

The `permissions` feature writes rules for agent harnesses from `effects`: read commands are
allowed, destructive ones ask first, and write commands get no rule, so the harness asks as it
does by default.

{{< code title=".rotini.conf.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
generate:
  features:
    - type: permissions
      enabled: true
      file: agents/             # the default
      harnesses: [claude, codex, gemini]
{{< /code >}}

`claude-settings.json` is a fragment to merge into Claude Code's `.claude/settings.json`,
`<name>.rules` holds Codex prefix rules, and `<name>-policy.toml` is a Gemini CLI policy. Rules
name leaf commands and all their aliases, never the root or a group, since harnesses match the
start of the command text and a rule for `todo remote` would also allow every command under it.
That also means a flag written before the command defeats a rule; it fails safe, since the
harness then asks. A flag that makes a command destructive gets its own ask rule where the
harness can match it. Gemini CLI treats `ask_user` as deny when it runs non-interactively.

These features write files into the repository, rewrite them on every generate, and never remove
them; turning one off leaves its files in place.

## Help, completion and docs

The conf's `generate.features:` turn on output generated from the spec. Each adds functions to the
generated package:

| Feature | What you get |
|---|---|
| `help` (on in the conf `rotini init` writes) | `Help(path...)` pages, printed by `--help` and `help <command>` |
| `completion` | `Completion(shell)` scripts for bash, zsh, fish, PowerShell and Nushell |
| `man` | `Man(path...)` man pages in roff, `ManPages()` for all of them, and a `ManSection` constant |
| `markdown` | `Markdown(path...)` reference pages and `MarkdownPages()` for all of them |
| `tools` | tool definitions for AI agents (MCP, OpenAI, Gemini) in the repository; with `go: true`, `ToolsMCP` (see [Tool definitions](#tool-definitions)) |
| `skill` | an Agent Skills page, `skills/<name>/SKILL.md` (see [Agent pages](#agent-pages)) |
| `llms` | an `llms.txt` (see [Agent pages](#agent-pages)) |
| `permissions` | permission rules for agent harnesses (see [Permission rules](#permission-rules)) |
| `carapace` | a carapace-spec completion file in the repository (see [Carapace](#carapace)) |
| `config_example` | an example of each config file in the repository, and `ConfigExample(name)` (see [Example config and .env files](#example-config-and-env-files)) |
| `env_example` | a `.env.example` in the repository, and `EnvExample()` (see [Example config and .env files](#example-config-and-env-files)) |

To expose one, add a command for it to the spec and call the function from its handler; for
example, a `completion` command whose handler prints the script `Completion(shell)` returns.

Pages show what the spec declares, so they stay accurate without editing:

- **Usage lines** put flags first: `todo add [flags] <title>`. A passthrough argument gets a
  `[--]` before it: `app exec [flags] [--] <command...>`.
- **Constraints** follow each row's summary: bounds (`1..65535`, `>= 1`), lengths, item counts
  and `repeatable` (or `once` for `repeatable: false`, and `unique` for `uniqueItems`) in help,
  and as sentences in man and markdown, which also give patterns and separators.
- **Groups**: commands and flags that share a `group` appear under its heading. List the groups
  in the command's `groups` to give each a description, shown under its heading, and to set
  their order (the example below).
- **A passthrough command's** page says that every word after its name is passed through, so
  flags go before it.
- **Enum values** are a short list on their row. When values have summaries, they are also
  listed under the row, one per line in the declared order.

{{< code title="cmd/todo/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
command:
  name: todo
  groups:
    - name: Maintenance
      description: Commands that tidy the task list.
  commands:
    - name: prune
      summary: remove finished tasks
      group: Maintenance
{{< /code >}}

Man pages are roff, the markup the `man` program reads, so `man -l todo-add.1` displays one and a
package installs them like any other. Each page is named after its command path joined with `-`:
`todo`, `todo-add`. With `embed: true` the pages are also written as files under that name with the
section as the extension (`todo-add.1`), so `cp renders/*.1 /usr/local/share/man/man1/` installs
them. The section is 1 unless the man feature sets `section:` (8 for a daemon or admin tool). The
header's date stays empty, so regenerating never changes a page, unless `SOURCE_DATE_EPOCH` is set
when you generate.

### Long descriptions

An input's `summary` is the one line help shows beside it. Give a flag, argument, environment
variable or config value a `description` too, for the text that needs more room: man and
markdown pages show it under the input, paragraphs separated by blank lines, and the contract
uses it as the input's description in `parameters`. Help keeps the summary only; an editable help
template can show both.

{{< code title=".rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
flags:
  - name: retries
    summary: how often to retry
    description: |-
      How many times a failed request is sent again, waiting twice as long each time.

      0 sends each request once.
    schema: { type: int, default: 3 }
{{< /code >}}

`rotini validate` warns about a description with no summary, which would leave the help row
empty.

### Stability

Mark a command or input that may still change with `stability: experimental` (it may change or be
removed in any release) or `stability: beta` (it may change in a minor release). Help, man and
markdown show `(experimental)` or `(beta)` beside it, and a command's own pages add a line saying
what the marker promises. The contract carries the marker for callers that care.

A command's inputs and sub-commands are never more stable than it is, so a marker inherits:
everything under an experimental command is experimental, and `rotini validate` warns about an
item marked more stable than its command. It also warns about a required experimental input on a
command that isn't experimental, since the command would depend on something that may go away.
Nothing changes at run time; to keep an experimental command behind a switch, check for it in the
handler.

### Help topics

Some help isn't about one command: how filter expressions work, which environment variables the
program reads. Declare such pages as `topics:` on the root:

{{< code title=".rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
command:
  name: todo
  topics:
    - name: filters
      summary: how filter expressions work
      body: |-
        A filter is a word that narrows a list: todo list urgent.

        Several filters must all match.
    - name: environment
      summary: the environment variables todo reads
      generate: environment
{{< /code >}}

The root's help lists them under `Help Topics:` (`headings.topics` renames it), and the generated
`Help("filters")` returns the page, so a `help` command whose handler calls `Help(path...)` shows
`todo help filters`. The man and markdown features write a page for each topic too, in the man
feature's section (`todo-filters.1`), listed after the commands in `ManPages()` and
`MarkdownPages()` with `Topic` set. `help <TAB>` completes topic names along with command names.

`generate: environment` builds the page from the spec: every environment variable the program
reads (env inputs, flag and argument fallbacks, `variable_file` names, the completion switches the
conf declares, and the XDG variables config discovery uses), each with the commands that read it.

A topic name can't also be a root command's name or alias, or a declared plugin's. With plugin
discovery on, don't install an executable named after a topic (`todo-filters`): it would run as
`todo filters` while `todo help filters` shows the topic.

Only the root spec's topics are part of the program, so declare them there: `rotini validate`
warns about topics in a spec mounted with `$ref`, which `help <topic>` can't reach.

### Files ready to package

Set `install_dir` on the completion or man feature, and `rotini generate` also writes the scripts
and pages as files named the way packages install them, whether or not the feature embeds them:

{{< code title=".rotini.conf.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
generate:
  features:
    - type: completion
      enabled: true
      install_dir: share
    - type: man
      enabled: true
      install_dir: share
{{< /code >}}

```
share/completions/todo.bash
share/completions/_todo          # zsh
share/completions/todo.fish
share/completions/todo.ps1
share/completions/todo.nu
share/man/man1/todo.1
share/man/man1/todo-add.1
```

Each file holds exactly what `Completion(shell)` or `Man(path...)` returns. A man page whose
command or topic is gone is removed on the next generate, and hidden commands get none. Don't use
GoReleaser's `dist/` directory, which it empties. Commit the files and let `rotini generate
--dry-run` in CI keep them current, or generate them in the release job. A GoReleaser
configuration then ships them:

{{< code title=".goreleaser.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
archives:
  - files: [share/completions/*, share/man/**/*]
nfpms:
  - contents:
      - { src: share/completions/todo.bash, dst: /usr/share/bash-completion/completions/todo }
      - { src: share/completions/_todo, dst: /usr/share/zsh/vendor-completions/_todo }
      - { src: share/completions/todo.fish, dst: /usr/share/fish/vendor_completions.d/todo.fish }
      - { src: share/man/man1/*, dst: /usr/share/man/man1/ }
{{< /code >}}

bash-completion loads a script by the command's name, so the package installs `todo.bash` as
`todo`. The [GoReleaser recipe](/recipes#shipping-with-goreleaser) covers the rest of a
release: archives, Homebrew, Scoop, winget, checksums and signing.

### Completion messages

When a user presses TAB on a value with nothing to offer, such as a free-text argument or an enum
value the typed prefix rules out, the shell can show a line of guidance instead:

{{< code title="terminal" language="text" open="true" collapsible="false" copy="false" >}}
$ todo add <TAB>
a short title; quote it if it has spaces
{{< /code >}}

Turn messages on with `messages:` on the completion feature. `declared` shows only the lines you
write in the spec as `complete.message`; `all` also shows a line made from each other flag's and
argument's summary:

{{< code title="cmd/todo/.rotini.conf.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
generate:
  features:
    - type: completion
      enabled: true
      messages: all
      messages_env: TODO_COMPLETION_MESSAGES
{{< /code >}}

{{< code title="cmd/todo/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
arguments:
  - name: title
    summary: the task title
    schema:
      type: string
      complete: { kind: none, message: "a short title; quote it if it has spaces" }
{{< /code >}}

A completer can add its own with `rtx.AddCompletionMessage`. These are shown even beside
candidates, in the order added, and take the place of the static line:

{{< code title="internal/cmd/todo/todo_done.go" language="golang" open="true" collapsible="false" copy="true" >}}
func (*todoDoneHandler) CompleteArgValue(rtx *rotini.Context, arg, partial string) []string {
	tasks, err := rtx.MustGetDependency(Store).Open()
	if err != nil {
		rtx.AddCompletionMessage("could not read the task list: " + err.Error())
		return nil
	}
	return tasks
}
{{< /code >}}

Your users can hide messages by setting the variable `messages_env` names to `0`, `false` or
`off`. Unset or any other value leaves them on. The variable is listed in the root man page, in
the contract document and at the top of each completion script. To decide some other way, pass
your own rule to `Program.WithCompletionMessages`, which replaces the variable check. Without
`messages_env` or a rule, messages always show when the conf turns them on.

zsh and bash 4.4 or later show messages; bash shows them on the second TAB, the one that lists
the candidates. fish, PowerShell and older bash, including the
`/bin/bash` macOS ships, skip them. A plugin for kubectl, Docker or Flux shows them the way the
host's completion does.

A rotini program can also answer completion requests from another program, such as the host of a
plugin, in whatever format that host reads. A `rotini.CompletionFormat` writes the answer in the
host's protocol; `rotini.PluginCompletion` is the built-in one for kubectl, Docker and Flux, and
[tab completion](#tab-completion) shows how to use it.

### What completion offers

Completion reads the spec the way the parser does, so it offers only what the command line can
still take:

- **Flags already set** are left out, read the way the parser reads them (`-vx` sets both, and
  `--no-color` sets `--color`). List, map, count and object flags stay, since they repeat.
- **Flag groups**: once one flag of a `mutually_exclusive` or `one_of` group is set, the others
  are left out, and so are the flags a set flag forbids in `flag_dependencies`. Required flags not
  yet set come first, including those a group or a `flag_dependencies` entry requires.
- **Deprecated** flags, identifiers, commands and aliases are left out. They still work when typed.
- **Negatable flags** offer their `--no-` form.
- **Enum values** come in the order the spec declares them, each with its summary.
- **Map keys** (`--set image=`) are completed up to the `=`, with no space after it, so the user
  types the value straight on. macOS's bash 3.2 still adds the space.

`complete.kind` names what a value is. Beside `file`, `directory` and `none`:

| Kind | Offers |
|---|---|
| `command` | command paths below the root, for a help command's argument: `help remote <TAB>` offers `remote`'s sub-commands. `rotini init` seeds it on `help`. |
| `executable` | program names (bash offers every command name it knows) |
| `user`, `group` | local account and group names |
| `host` | the host names the shell knows (`/etc/hosts`, ssh known hosts) |

Each shell's own completer supplies `executable`, `user`, `group` and `host`. PowerShell has no
host completer, and lists users and groups on Windows only. A plugin host offers nothing for
them.

Descriptions show beside each candidate in zsh, fish and PowerShell, and in bash 4.0 or later on
the second TAB, the one that lists the candidates. Give your users an off switch with
`descriptions_env`; setting the variable it names to `0`, `false` or `off` hides descriptions in
every shell. `Program.WithCompletionDescriptions` replaces the variable check with a rule of your
own.

{{< code title="cmd/todo/.rotini.conf.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
generate:
  features:
    - type: completion
      enabled: true
      descriptions_env: TODO_COMPLETION_DESCRIPTIONS
{{< /code >}}

Regenerate and reinstall the completion scripts to get these. A script and a binary from
different rotini versions still work together.

### Completers

A `CompleteFlagValue` or `CompleteArgValue` method on a handler supplies candidates at run time.
Its candidates are offered in the order it returns them (bash before 4.4 sorts them). It runs as
the command that declares the flag, and `rtx` gives it:

- `rtx.PartialInputs[T]()`: what has been typed so far, over the environment and the defaults.
  Nothing is validated or required, an unknown or half-typed flag is skipped, and stdin and
  files are never read, so it is safe on every TAB. It returns the inputs, which fields were
  supplied, and an error only for a type that doesn't describe the command.
- `rtx.Context()`: the program's base context, from `Program.WithContext` or `RunContext`.
  Rotini sets no deadline and traps no signal for a completion request, so a completer that
  calls the network sets its own.
- `rtx.SetCompletionOptions(rotini.CompletionOptions{NoSpace: true})`: no space after the
  inserted candidate, for a value the user goes on typing. fish adds none only after a value
  ending in one of `@=/:.,`. `KeepOrder: true` keeps the whole answer in the order given,
  sub-command names included.

{{< code title="internal/cmd/todo/todo_done.go" language="golang" open="true" collapsible="false" copy="true" >}}
func (*todoDoneHandler) CompleteArgValue(rtx *rotini.Context, arg, partial string) []string {
	in, _, _ := rtx.PartialInputs[TodoDoneInputs]()
	ctx, cancel := context.WithTimeout(rtx.Context(), 2*time.Second)
	defer cancel()
	return listTasks(ctx, in.Todo.Flags.List)
}
{{< /code >}}

`rotini.PluginCompletion` passes the options on to kubectl, Docker and Flux as their no-space
and keep-order directives.

### Nushell

`Completion("nushell")` is a script for Nushell 0.116 or later. It attaches a completer to the
program's command that calls `__complete`, so it leaves a carapace or other external completer
alone. Nushell runs the files in its autoload directory at startup:

{{< code title="Nushell" language="sh" open="true" collapsible="false" copy="true" >}}
mkdir ($nu.user-autoload-dirs | first)
todo completion nushell | save -f ($nu.user-autoload-dirs | first | path join todo.nu)
{{< /code >}}

Nushell shows descriptions and keeps a completer's order. It has no place for completion
messages, and no completer for program, user, group or host names, so those hints offer nothing.
A value the user goes on typing (`key=`) gets no space after it.

### Carapace

The `carapace` feature writes a [carapace-spec](https://carapace.sh) file for users who complete
through carapace, `completions/carapace/<name>.yaml` (the conf's `file` changes where; carapace
needs the file named after the program). It adds nothing to the binary. Users copy it into
carapace's `specs` directory: `$XDG_CONFIG_HOME/carapace/specs` when that variable is set, on
every system; otherwise `~/.config/carapace/specs` on Linux and
`~/Library/Application Support/carapace/specs` on macOS.

{{< code title=".rotini.conf.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
generate:
  features:
    - type: carapace
      enabled: true
{{< /code >}}

It carries what the spec declares: commands, aliases, groups and summaries; flags with their
modifiers (a value, repeatable, an optional value, required, hidden), cascading flags as
persistent ones, and `mutually_exclusive` flag groups; enum values with their summaries; and
completion hints (`file` with its extensions, `directory`, `executable`, `user`, `group`, `host`,
and `command` as the top-level command names). A completion message shows when nothing else is
offered and the completion feature's `messages` are on. A passthrough command stops flag parsing,
and so does a passthrough argument when it is the first. Hidden and deprecated flag spellings
are listed as hidden, so carapace doesn't offer them; hidden and deprecated command aliases are
left out. Values a Go completer supplies at run time have no carapace form, so they get
no candidates. A program that needs them can answer completion in `rotini.PluginCompletion`'s
format (`Program.WithCompletion(rotini.PluginCompletion)`), the one kubectl, Docker and Flux
plugins use, and have users point carapace's bridge for that format at the binary, which then
gets the whole command line.

## Conventions

Users expect some behaviour from any CLI: plain output when piped, a way to turn color off, no
prompts in scripts. Rotini adds none of it, so nothing happens that the spec doesn't declare;
each is a flag or variable in the spec and a few lines in a hook. For what agents and scripts
need beyond this, see [Agent-ready CLIs](#agent-ready-clis).

### Output, color and prompts

Declare the flags on the root, `cascading: true`, so every command takes them:

{{< code title="cmd/todo/.rotini.spec.yaml (conventions)" language="yaml" open="true" collapsible="false" copy="true" >}}
command:
  name: todo
  flags:
    - name: output
      summary: how to write the result; json when stdout isn't a terminal
      identifiers: [-o, --output]
      cascading: true
      schema: { type: string, enum: [table, json] }
    - name: color
      summary: when to color the output
      identifiers: [--color]
      cascading: true
      schema: { type: string, enum: [auto, always, never], default: auto, implicit_value: always }
    - name: no-input
      summary: never prompt; fail when a value is missing
      identifiers: [--no-input]
      cascading: true
      schema: { type: bool, variable: TODO_NO_INPUT }
  env:
    - name: no-color
      summary: turns color off when set to anything but an empty value
      schema: { type: string, variable: NO_COLOR }
{{< /code >}}

`implicit_value` makes `--color` alone mean `always`, while `--color=never` still takes a value.
Then decide once per run, in the root handler's `CascadingPreRun`, and hand the result to every
command as a [per-run dependency](#per-run-dependencies):

{{< code title="internal/cmd/todo/conventions.go" language="golang" open="true" collapsible="false" copy="true" >}}
package todo

import "github.com/go-rotini/rotini"

// Style is how this run writes its output, and whether it may prompt.
type Style struct {
	Format string // table or json
	Color  bool
	Prompt bool
}

var styleDep = rotini.NewDependency[Style]("todo.style")

// decideStyle applies the conventions once per run, from the root's CascadingPreRun: json
// when -o isn't given and stdout isn't a terminal; color for --color=always, or for auto on a
// terminal unless NO_COLOR is set or TERM is dumb; prompts only on a terminal, without
// --no-input.
func decideStyle(rtx *rotini.Context, in TodoInputs) {
	flags := in.Todo.Flags
	s := Style{Format: flags.Output}
	if s.Format == "" {
		s.Format = "table"
		if !rotini.IsTerminal(rtx.Stdout) {
			s.Format = "json"
		}
	}
	term, _ := rtx.LookupEnv("TERM")
	switch flags.Color {
	case "always":
		s.Color = true
	case "auto":
		s.Color = in.Todo.Env.NoColor == "" && term != "dumb" && rotini.IsTerminal(rtx.Stdout)
	}
	s.Prompt = !flags.NoInput && rotini.IsTerminal(rtx.Stdin)
	rtx.SetDependency(styleDep, s)
}
{{< /code >}}

- **Output:** no `default` on `--output`, so an empty value means the user didn't choose.
  `rotini.IsTerminal` checks the stream itself, so it is right under
  [buffered output](#buffered-output) too, and `/dev/null` isn't a terminal.
- **Color:** [`NO_COLOR`](https://no-color.org) turns color off when it is set and not empty,
  whatever its value, and `TERM=dumb` means the terminal can't show it. An explicit
  `--color=always` wins over both. Rotini never writes color itself, so this covers only your
  output.
- **Prompts:** ask only when stdin is a terminal and `--no-input` isn't set; otherwise fail
  with the missing value, so a script never hangs. See
  [prompting for missing inputs](/recipes#prompting-for-missing-inputs).

### A DEBUG variable

A crash should tell the user what to do, not show a stack they can't use. Declare a variable
that asks for the stack, in the spec's root `env:` so the help and man pages list it:

```yaml
env:
  - name: debug
    summary: print the stack of a crash when set
    schema: { type: string, variable: TODO_DEBUG }
```

The reporter reads it with `rtx.LookupEnv`, since a run that failed to parse its inputs is still
reported:

{{< code title="internal/cmd/todo/report_debug.go" language="golang" open="true" collapsible="false" copy="true" >}}
package todo

import (
	"context"
	"fmt"
	"net/url"
	"runtime"

	"github.com/go-rotini/rotini"
)

// ReportWithStacks reports warnings and errors to stderr. After a crash it prints the stack
// when TODO_DEBUG is set, and otherwise a link that opens a bug report.
func ReportWithStacks(ctx context.Context, rtx *rotini.Context, out rotini.Outcome) {
	for _, w := range out.Warnings {
		fmt.Fprintln(rtx.Stderr, "Warning:", w)
	}
	for _, err := range out.Errors {
		fmt.Fprintln(rtx.Stderr, "Error:", err)
	}
	debug, _ := rtx.LookupEnv("TODO_DEBUG")
	for _, p := range out.Panics {
		fmt.Fprintln(rtx.Stderr, "Error:", p)
		if debug != "" {
			fmt.Fprintf(rtx.Stderr, "%s", p.Stack)
			continue
		}
		// No arguments or inputs: they may hold secrets.
		body := fmt.Sprintf("version: %s\nplatform: %s/%s\npanic: %v",
			rtx.Version(), runtime.GOOS, runtime.GOARCH, p.Value)
		fmt.Fprintln(rtx.Stderr, "This is a bug; please report it:")
		fmt.Fprintln(rtx.Stderr, "https://github.com/me/todo/issues/new?body="+url.QueryEscape(body))
	}
	if out.Failed() && ctx.Err() == nil {
		rtx.Exit(1)
	}
}
{{< /code >}}

Install it with `WithReporter(cmd.ReportWithStacks)`. The link fills in a new issue with the
version, the platform and the panic value. Leave out arguments and inputs, which may hold
secrets.

### Config files: project before user

Among `config_files`, the first declared entry wins for each key. Declare a project file, found
by walking up from the working directory, before the user's own file, so a repository's
settings override the user's defaults and the command line and environment override both:

```yaml
config_files:
  - name: project
    discover: { strategy: walk-up, file: .todo.yaml }
  - name: user
    discover: { strategy: xdg, app: todo, file: config.yaml }
```

A system-wide file goes last (`strategy: xdg-system`; see [config directories](#config-directories)).
A project file comes with whatever repository the user is in, so prefer `expand: [home]` over
`env` on the inputs it can set (see [paths](#paths)).

### Secrets

Read a secret from a file or stdin, not from the command line or the environment. A value on
the command line shows in the process list and the shell's history; the environment is passed
to every child process and can end up in crash dumps and debug output. Declare the flag
`secret: true` with `from: [file, stdin]` (see
[secrets on the command line](#secrets-on-the-command-line)), or give an environment input a
`variable_file:` that names a file, as container platforms mount secrets (see
[secrets in files](#secrets-in-files-and-env-files)). The [secrets recipe](/recipes#secrets)
covers keychains and secret managers.

### Flag names

The names users try first. Each is an ordinary spec entry:

| Flag | Meaning | Declared with |
|---|---|---|
| `-h`, `--help` | print help | `short_circuit: true`; `rotini init` declares it |
| `--version` | print the version | `short_circuit: true`, long only so `-v` stays free; `rotini init` declares it |
| `-v`, `--verbose` | more output; `-vv` for more still | `type: count` (see [the recipe](/recipes#-v-and--vv-as-log-levels)) |
| `-q`, `--quiet` | less output | `type: bool` |
| `-o`, `--output <format>` | output format | an `enum`; `role: machine-output` with `role_value: json` |
| `--json` | write JSON | `type: bool` and `role: machine-output`, or `-o json` |
| `-n`, `--dry-run` | show what would change, change nothing | `role: dry-run` |
| `-f`, `--force` | overwrite or skip a safety check | `role: force` |
| `-y`, `--yes` | answer yes instead of prompting | `role: confirm` |
| `--no-input` | never prompt | `type: bool` |
| `--color[=when]` | `auto`, `always` or `never` | an `enum` with `implicit_value: always` |
| `--config <file>` | read this config file | `config_source: <entry>` on a string flag, naming a `config_files` entry |
| `-C <dir>` | run as if started in this directory | `role: chdir` on any flag you name (see [a directory flag](#a-directory-flag)) |

## Plugins

A plugin is a separate program that runs as a sub-command of another one: `git lfs` runs a
binary named `git-lfs`. A rotini CLI can work with plugins in two directions: it can run other
programs as its own sub-commands, and it can itself be a plugin for kubectl, Docker or Flux.

### Run other programs as sub-commands

Declare the plugins your CLI knows about under `plugins:`, or turn on `plugin_discovery:` to
pick up any executable whose name starts with a prefix. Both can be used together:

{{< code title="cmd/plug/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
command:
  name: plug
  plugin_path: ./plugins/bin
  plugins:
    - name: sync
      summary: synchronize with the remote store
      aliases: [sy]
    - name: report
      summary: render a report
      timeout: 5s
  plugin_discovery:
    prefix: plug-
{{< /code >}}

- **A declared plugin** runs the binary `<program>-<name>`: `plug sync` runs `plug-sync`. It is
  listed in help and completion whether or not it is installed, so a missing binary is reported
  as an internal error: an install problem.
- **Discovery** makes any executable named `<prefix><word>` a sub-command: `plug hello` runs
  `plug-hello` if one exists. The prefix defaults to the program's name followed by `-`. A word
  that matches nothing is a usage error, since it is most likely a typo. A discovered name that
  collides with a declared command or plugin is skipped. Completion offers discovered plugins
  unless `hidden: true` is set, but the generated help can't list them, since they aren't known
  when you generate.
- **Both are searched for in the same order:** next to the program's own binary, then in
  `plugin_path` (relative to the working directory), then on `PATH`. A binary that isn't found
  is reported with the places searched:

  ```console
  $ plug absent
  Error: "plug-absent" not found; searched next to the binary /usr/local/bin, then the plugin path ./plugins/bin, then PATH
  ```

- **The plugin runs with** every argument after its name, and with the program's stdin, stdout
  and stderr. Its exit code passes through unchanged, and a plugin ended by a signal exits
  128+n, as a shell reports it. A plugin that runs past its `timeout:` is killed, and so is one
  still running when the run is canceled, for example by Ctrl+C, which exits 130.

Rotini prints nothing about discovered plugins itself. To list them, in your own help or a
`plugins` command, call `DiscoveredPlugins()` on the command that has discovery:

{{< code title="internal/cmd/plug/plug_plugins.go" language="golang" open="true" collapsible="false" copy="true" >}}
for _, p := range rtx.CommandChain()[0].DiscoveredPlugins() {
	fmt.Fprintf(rtx.Stdout, "  %s\t%s\n", p.Name, p.Path)
}
{{< /code >}}

`PluginBinary(name)` reports which binary a plugin would run, searching where dispatch searches.
`PluginDiscoveryErrors()` returns what went wrong scanning `plugin_path`, such as a directory that
can't be read; Rotini doesn't print it, since that would break completion output.
A failure to run a plugin is a `*rotini.PluginError`, whose `Kind` says whether the binary was
not found, timed out or could not be started. The [spec reference](/specification#pluginspec)
lists every plugin key.

### Be a plugin for kubectl, Docker or Flux

kubectl, Docker and Flux each run plugins as their own sub-commands (`kubectl ctx` runs a binary
named `kubectl-ctx`), and a rotini CLI can be one of them.

#### How each host runs a plugin

| | kubectl | Docker | Flux |
|---|---|---|---|
| Binary name | `kubectl-<name>` | `docker-<name>` | `flux-<name>` |
| Where it is found | anywhere on `PATH` | `~/.docker/cli-plugins` (or `$DOCKER_CONFIG/cli-plugins`), plus system plugin directories | `$FLUXCD_PLUGINS`, default `~/.fluxcd/plugins` (Flux 2.9 and later) |
| Arguments it receives | everything after the plugin's name | **Docker's own arguments**: `docker -c prod where x` runs `docker-where -c prod where x` | everything after the plugin's name, plus flags typed before it |
| Before running it | nothing | runs `docker-<name> docker-cli-plugin-metadata` and reads one JSON object | nothing |

For kubectl, each `-` in the binary name is a level of sub-command: `kubectl-ctx-use` answers
`kubectl ctx use`. A `-` inside a single command name is written as `_` in the file name.

**Docker passes its own arguments through**, so a Docker plugin's spec stands in for `docker`
itself. The root accepts Docker's global options (`--config`, `-c`/`--context`, `-D`, `-H`,
`-l`, `--tls…`), marked `cascading: true`, and the plugin is a sub-command beneath it:

{{< code title="cmd/docker-where/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
command:
  name: docker-where
  display_name: docker
  flags:
    - name: context
      identifiers: [-c, --context]
      cascading: true
      schema: { type: string }
    # … the rest of Docker's global options
  commands:
    - name: where
      summary: Show which context and daemon endpoint docker would use
    - name: docker-cli-plugin-metadata
      summary: print the plugin's metadata for docker
      hidden: true
{{< /code >}}

The hidden `docker-cli-plugin-metadata` command answers Docker's probe. Its handler prints one
JSON object and nothing else. Docker refuses a plugin whose `SchemaVersion` is not `0.1.0` or
whose `Vendor` is empty, and lists the plugin in `docker --help` with its `ShortDescription`:

{{< code title="internal/cmd/docker-where/docker_where_docker_cli_plugin_metadata.go" language="golang" open="true" collapsible="false" copy="true" >}}
func (*dockerWhereDockerCliPluginMetadataHandler) Run(ctx context.Context, rtx *rotini.Context) {
	meta := map[string]string{
		"SchemaVersion":    "0.1.0",
		"Vendor":           "example",
		"Version":          rtx.Version(),
		"ShortDescription": "Show which context and daemon endpoint docker would use",
	}
	if err := json.NewEncoder(rtx.Stdout).Encode(meta); err != nil {
		rtx.HaltWith(err)
	}
}
{{< /code >}}

#### Tab completion

Each host completes a plugin's arguments by asking the plugin, and all three read the same
completion format: one candidate per line, `value<TAB>description`, then a final `:<number>`
line with directives such as "don't fall back to file names". Rotini computes the answer from
the spec, your completers and each input's `complete:` hint, and `rotini.PluginCompletion`
writes it in that format. The hidden `__complete` command that rotini's generated scripts call
answers in rotini's own format, described in
[debugging completion](/recipes#debugging-completion): a script skips lines it doesn't know and
the kind line stays last, so a script and a binary from different rotini releases work together.
Another program reads a rotini CLI's completion through `Program.Complete` or
`Program.WithCompletion`, with a `rotini.CompletionFormat` that writes its own protocol.

**kubectl** runs a separate executable, `kubectl_complete-<name>`, found on `PATH`. Declare
`multicall` on the root and install the plugin's binary a second time under that name (a copy or
a symlink); run that way, it answers in `rotini.PluginCompletion`'s format:

{{< code title="cmd/kubectl-ctx/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
command:
  name: kubectl-ctx
  multicall: {complete: kubectl_complete-}
{{< /code >}}

See [one binary, several names](#one-binary-several-names). A program that decides in its own
`main.go` calls `p.Complete(os.Args[1:], rotini.PluginCompletion)` instead.

**Docker and Flux** run the plugin's own hidden `__complete` command:
`docker-where __complete where <words…>`, `flux-suspended __complete <words…>`. Every rotini CLI
has that command; by default it answers in the format rotini's own generated shell scripts read.
Set the plugin format in `main.go`:

{{< code title="cmd/docker-where/main.go" language="golang" open="true" collapsible="false" copy="true" >}}
cmd.NewProgram(cmd.Handlers()).
	WithVersion(version).
	WithCompletion(rotini.PluginCompletion).
	Execute()
{{< /code >}}

Both hosts treat the last line as the directive line, always. Without the setting, the last real
candidate would be read as a directive and lost. Leave it unset for a standalone CLI, whose
completion scripts come from the `completion` feature and expect rotini's format.

#### Help that reads like the host

Set `display_name` on the root, and every generated help, man and markdown page shows the
command the user types instead of the binary's name:

{{< code title=".rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
command:
  name: kubectl-ctx
  display_name: kubectl ctx
{{< /code >}}

`kubectl ctx use --help` then shows `Usage: kubectl ctx use …`. File names, the completion
target and anything you wrote verbatim keep the binary's real name.

#### kubectl's connection flags

kubectl users expect a plugin to take the same connection flags kubectl does: `--kubeconfig`,
`--context`, `-n`/`--namespace`, `--as`, `--token`, `--server` and the rest. Declare them on the
plugin's root with `cascading: true`, so they work on every sub-command and its help lists them,
and hand the parsed values to `ConfigFlags` from `k8s.io/cli-runtime`. It then does everything
kubectl does with them: loading and merging kubeconfigs, choosing the context, impersonation, TLS,
and the namespace default.

{{< code title="internal/cmd/kubectl-ctx/kube.go" language="golang" open="true" collapsible="false" copy="true" >}}
func configFlags(f KubectlCtxFlags) *genericclioptions.ConfigFlags {
	cf := genericclioptions.NewConfigFlags(true)
	*cf.KubeConfig = f.Kubeconfig
	*cf.Context = f.Context
	*cf.Namespace = f.Namespace
	*cf.BearerToken = f.Token
	*cf.Impersonate = f.As
	// … one line per flag
	return cf
}
{{< /code >}}

{{< alert type="warning" title="DON'T BIND KUBECONFIG TO THE FLAG:" >}}
Leave the `--kubeconfig` flag without an environment fallback. `KUBECONFIG` is a list of files,
separated by `:`, that client-go merges. Bound to the flag it would become one path, and a user
with two kubeconfig files would get neither. Left alone, client-go reads it exactly as kubectl
does.
{{< /alert >}}

The short spellings kubectl users type from habit parse as they expect: `-nfoo`, `-lapp=api`,
`-ojson`, and `-A` grouped with other short flags.

Flux's plugins follow the same pattern with Flux's conventions: `-n` falls back to
`$FLUX_SYSTEM_NAMESPACE`, then to `flux-system`, not to the kubeconfig context's namespace.

### One binary, several names

The root's `multicall` key makes the program dispatch on the name its binary was invoked as, so
one binary can be installed under several names with links or copies:

{{< code title=".rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
command:
  name: busybox
  multicall: true   # a link named ls runs `busybox ls`
{{< /code >}}

```console
$ ln -s busybox ls
$ ./ls -l          # runs `busybox ls -l`
```

The object form sets `prefix`, stripped before the name is matched (`acme-ls` runs `ls` with
`prefix: acme-`), and `complete`, a prefix that answers shell completion for the root instead of
running it. That is kubectl's convention: it completes a plugin by running
`kubectl_complete-<name>` with the words to complete. With `multicall: {complete:
kubectl_complete-}`, install the plugin a second time as `kubectl_complete-ctx` and it answers in
`rotini.PluginCompletion`'s format (or the one `WithCompletion` sets), with no check in `main.go`.

The name is the base name the binary was run as, never a resolved symlink. On Windows a
trailing `.exe` is dropped and names match without regard to case; a shim that runs the real
binary passes the real name, so multicall doesn't apply. A name that matches no top-level command
or plugin, the root's own name included, runs the root. Pages and `rtx.Usage()` still show the
root's invocation (`busybox ls`), and shell completion scripts are registered for the root's
name only. Tests set the invoked name with `Program.WithArgv0`.

## Testing

Build a fresh `Program` per test and run it with arguments, in-process: no subprocess and no
`os.Exit`. `Run` returns the exit code and the error the run recorded:

{{< code title="internal/cmd/todo/todo_test.go" language="golang" open="true" collapsible="false" copy="true" >}}
func TestAdd(t *testing.T) {
	var out bytes.Buffer
	code, err := NewProgram(Handlers()).WithStdout(&out).Run([]string{"add", "buy milk"})
	if err != nil || code != 0 {
		t.Fatalf("code %d, err %v", code, err)
	}
	if !strings.Contains(out.String(), `added "buy milk"`) {
		t.Errorf("stdout = %q", out.String())
	}
}

func TestAddRejectsUnknownPriority(t *testing.T) {
	var stderr bytes.Buffer
	code, err := NewProgram(Handlers()).WithStderr(&stderr).Run([]string{"add", "buy milk", "-p", "urgent"})
	if err == nil || code != 1 {
		t.Fatalf("code %d, err %v", code, err)
	}
}
{{< /code >}}

Keep two things in mind:

- **`NewProgram(Handlers())` is built the way `main.go` builds it,** but the settings `main.go`
  adds, such as a reporter, a version or dependencies, are missing until the test adds them too. With the
  [reporter above](#errors-and-exit-codes), the second test would call
  `NewProgram(Handlers()).WithReporter(Report)` and expect exit code 2.
- **Environment variables and config files come from the process,** unless the test gives the
  run its own. `WithEnviron` sets the whole environment a run reads (env inputs, flag fallbacks,
  `HOME` and `XDG_CONFIG_HOME` for config discovery, plugin lookup), and `WithDir` sets the
  directory walk-up discovery, relative config paths and `@file` values start from. A test that
  sets both doesn't touch the process, so it can run in parallel:

{{< code title="internal/cmd/todo/todo_test.go" language="golang" open="true" collapsible="false" copy="true" >}}
func TestAddDefaultPriority(t *testing.T) {
	t.Parallel()
	code, err := NewProgram(Handlers()).
		WithEnviron([]string{"HOME=" + t.TempDir(), "TODO_DEFAULTS_PRIORITY=high"}).
		WithDir(t.TempDir()).
		Run([]string{"add", "buy milk"})
	if err != nil || code != 0 {
		t.Fatalf("code %d, err %v", code, err)
	}
}
{{< /code >}}

A handler reads the same environment with `rtx.LookupEnv`. It still runs in the process's working
directory, so it opens a relative path with `filepath.Join(rtx.Dir(), path)`, including the value
of an `existingfile` input, which Rotini checks against the run's directory but binds as typed.

### Testing with typed inputs

The `rotinitest` package (`github.com/go-rotini/rotini/rotinitest`) runs a command from the
inputs type `rotini generate` wrote for it, so a test sets fields instead of spelling out
arguments:

{{< code title="internal/cmd/todo/todo_test.go" language="golang" open="true" collapsible="false" copy="true" >}}
func TestAddHigh(t *testing.T) {
	t.Parallel()
	var in TodoAddInputs
	in.TodoAdd.Arguments.Title = "buy milk"
	in.TodoAdd.Flags.Priority = "high"
	res := rotinitest.Run(t, NewProgram(Handlers()), in)
	if res.Code != 0 {
		t.Fatalf("code %d, err %v", res.Code, res.Err)
	}
	if got := string(res.Stdout); got != "added \"buy milk\" (priority high)\n" {
		t.Errorf("stdout = %q", got)
	}
}
{{< /code >}}

- **`Run`** writes the inputs as the command line that supplies them (`add --priority=high --
  "buy milk"`), runs the program in-process, and returns the exit code, the error, stdout,
  stderr and the argv it ran. Only the fields that hold a value are written; to write a zero
  value, such as `--count=0`, name it with `rotinitest.Presence`. A value no command line can
  express, such as a config input or a secret flag, fails the test (`rotinitest.Secrets()`
  allows secrets). A stdin payload comes from the inputs' `Stdin` field.
- **Each run is isolated.** It gets its own environment, holding only the env inputs you set,
  plus `HOME` and `XDG_CONFIG_HOME` pointing into a temporary directory, and its own working
  directory. Your shell's variables and config files never leak in. `t.Setenv` changes the whole
  process, and Go doesn't allow it in a parallel test; a `rotinitest` run never needs it, so
  tests can call `t.Parallel()`. Add variables with `rotinitest.Env`, and write a config file
  into a directory you pass with `rotinitest.Dir`.
- **`Output`** decodes stdout as JSON into the output type of a command that declares
  [`output:`](#structured-output), checking it against the output schema:
  `rotinitest.Output[TodoListOutput](t, res)`. `OutputAs` reads yaml or toml. Output checks are
  on for the run, so a handler that writes a value off its schema fails it.
- **`ExitDocumented`** fails the test when the exit code isn't one the command lists in its
  `exit_status`. 0 always passes; a command that lists none passes only 0.
- **`Clock(t)`** fixes the run's clock, so relative times such as `2h` and `today` read the same
  instant on every run.
- **`Argv0(name)`** runs a `multicall` program as if invoked as `name`; pass the inputs of the
  command that name runs.

Pass a fresh `NewProgram(Handlers())` to every `Run`, with the dependencies `main.go` adds:
`Run` sets the program's streams and environment. A command line that typed inputs can't express,
such as a typo or an unknown flag, is still tested with `Program.Run`.

### Turning inputs back into a command line

`rotini.ArgvOf` is what `rotinitest` builds on: it turns an inputs value into argv and env, for
your own test helpers or for tools that drive a CLI.

{{< code language="golang" open="true" collapsible="false" copy="true" >}}
p := NewProgram(Handlers())
argv, env, err := rotini.ArgvOf(p.Definition(), in, rotini.PresenceOf(in))
code, err := p.WithEnviron(env).Run(argv)
{{< /code >}}

The third argument says which fields to write. `rotini.PresenceOf` names every field that holds
a value; build the `rotini.Presence` yourself to write a zero value. Flags are written attached
(`--name=value`), each after its own command's name, and `--` always comes before the
positionals, so a value that looks like a flag or a command stays a positional (it shows in
`rtx.DashIndex()`). On a flag that reads `@file` values, a value starting with `@` is written
`@@…`. Secrets are refused unless you pass `rotini.ArgvSecrets()`, since argv shows in the
process list. A value no command line can supply is a `*rotini.ArgvError` naming the field:
config and stdin inputs, an argument of a command other than the invoked one, `-` on a flag that
reads stdin from it, an explicit empty list on a flag without a separator, and a time its layout
can't show exactly.

## Checking for breaking changes

`rotini diff` compares two versions of your CLI's contract and reports what changed for the
people and scripts using it. Each change is **breaking** (something that worked stops working),
**possibly breaking** (it may), **expected** (a removal you planned for this release) or **safe**.
It compares commands, flags, arguments, environment variables, config keys and files, stdin, output
shapes and exit statuses, so a renamed output field or a dropped exit code is caught as surely as
a removed flag.

### In CI

1. Turn on the contract with `generate.contract.file`, and commit the file it writes.
2. `rotini generate --dry-run` fails when the committed contract is stale.
3. `rotini diff git:<last release tag>` fails on breaking changes since that release.

{{< code title=".rotini.conf.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
generate:
  contract:
    file: cli-contract.json
{{< /code >}}

{{< code title=".github/workflows/ci.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
- uses: actions/checkout@v4
  with:
    fetch-depth: 0 # git:<ref> reads the tag locally and never fetches
- run: go tool rotini generate --dry-run
- run: go tool rotini diff "git:$(git describe --tags --abbrev=0)"
{{< /code >}}

Compare against the last release tag rather than the main branch: an intended break you
acknowledge in one pull request is then still a change on the next one, until you release.

`<old>` is a file, `git:<ref>` (the conf's `generate.contract.file` as committed at that
revision), `git:<ref>:<path>` (another module-root-relative file at that revision), or
`mod://<module>@<version>/<path>`. A `mod://` contract is read from the module cache, downloaded
first when it isn't there, and verified by `go.sum`, or by the checksum database for a version
`go.sum` doesn't list, such as an older version of your own module. `<new>` defaults to the
contract built from your current spec and conf.

The exit status is 0 when nothing is at or above `--fail-on`, 2 when something is, and 1 on an
error. `--fail-on possibly` also fails on possibly breaking changes; `--fail-on never` only
reports. `--format json` writes one document: `findings` (each with `severity`, `rule`,
`where`, `message`, `note` and, when acknowledged, `accepted`), `unmatched_accepts` and
`summary`.

Commit a baseline contract written by rotini 1.4 or later. An older contract records fewer facts,
so the new one is compared only on what the old one states; it can still show a few false
findings (rotini 1.3 left out a stdin `required`, for example).

### Acknowledging a break

When a break is intended, acknowledge it in the conf, one entry per finding, with the `rule` and
`where` exactly as `rotini diff` prints them:

{{< code title=".rotini.conf.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
diff:
  accept:
    - rule: COMMAND_NO_DELETE
      where: taskr compact
      reason: replaced by purge
{{< /code >}}

The finding is then reported as accepted, with its reason, and doesn't fail the run. An entry
that matches no finding fails the run, naming the entry, so the list doesn't outlive its release:
clear it when you release. There is no way to ignore a rule everywhere.

A removal you planned needs no entry. Give the item `removed_in` (see
[deprecating a command or flag](#deprecating-a-command-or-flag)) and pass the release you're
preparing with `--release 2.0.0`, or name a variable holding it with the conf's
`validate.release_env`: an item whose `removed_in` is at or below that release is reported as
expected. Without a release it stays breaking, with a note to pass one. A command, flag or enum
value removed with a `replaced_by` that names something still there is an expected rename.

### How changes are matched and rated

Commands match by path, flags by identifier (so a lost short form is caught and a renamed
logical name isn't a removal), arguments by position, environment variables by variable, and
config entries by key. What a caller passes and what the program writes are rated in opposite
directions: narrowing an input (a new enum, a tighter bound, a new required property) breaks
callers, and widening an output (a new enum value, a property that may be absent) may break
readers. A changed default is possibly breaking, since the same command line now behaves
differently. A schema in `definitions` is compared once, where it's defined, with a note naming
the commands that use it.

Stability lowers the rating: on an item that is `experimental` in the old contract (or under an
experimental command) every finding is safe, and on a `beta` one a breaking finding is possibly
breaking. Removing a hidden item is possibly breaking, and adding a hidden one is not reported.

A finding's `where` names the item in the old contract, written as on the command line:

| Item | `where` |
|---|---|
| a command | `taskr compact` |
| an alias | `taskr list alias ls` |
| a flag, or one identifier | `taskr add --priority`, `taskr add -p` |
| an argument | `taskr done <ids>` |
| an environment variable | `taskr list $TASKR_STORE` |
| a config key | `taskr list config store.path` |
| an enum value | `taskr add --priority value low` |
| a field inside an input, stdin or output | `taskr add --meta.owner`, `taskr upper stdin.items[]`, `taskr list output.tasks[].status` |
| an exit status, or its output | `taskr get exit 3`, `taskr get exit 3 output.reason` |
| a flag group or dependency | `taskr add group mutually_exclusive(json,yaml)`, `taskr add dependency force unless dry-run` |
| a config file, plugin discovery, stdin | `taskr config_files user`, `taskr plugin_discovery`, `taskr upper stdin` |
| a profile selector variable | `taskr config_files user $TASKR_PROFILE` |
| a shared schema | `definitions.Task.status` |
| program-wide | `multicall`, `response_files`, `completion.messages_env`, `taskr help env`, `errors` |

### Rules

Rule IDs are stable; `diff.accept` entries name them. A rule's rating can depend on the direction
or on what the old contract planned.

| Rule | Change | Rating |
|---|---|---|
| `ROOT_RENAMED` | the program's name | breaking |
| `ERRORS_CHANGED` | the error-line schema (another rotini release) | safe |
| `MULTICALL_NO_DELETE`, `MULTICALL_CHANGED`, `MULTICALL_ADDED` | dispatch on the invoked name removed, its prefix or completion name changed, or added | breaking, breaking, safe |
| `TOPIC_REMOVED`, `TOPIC_ADDED` | a help topic | possibly breaking, safe |
| `COMPLETION_ENV_REMOVED`, `COMPLETION_ENV_CHANGED`, `COMPLETION_ENV_ADDED` | a completion switch variable | possibly breaking, possibly breaking, safe |
| `RESPONSE_FILES_ADDED`, `RESPONSE_FILES_PREFIX_CHANGED`, `RESPONSE_FILES_REMOVED` | response files | breaking, breaking, possibly breaking |
| `COMMAND_NO_DELETE` | a command removed | breaking; expected when planned; possibly breaking when it was hidden |
| `COMMAND_REPLACED` | a command removed for the `replaced_by` it named | expected |
| `COMMAND_RENAMED_ALIAS_KEPT` | renamed, the old name kept as an alias | safe |
| `COMMAND_ADDED`, `PLUGIN_ADDED` | a command or plugin added | safe |
| `PLUGIN_NO_DELETE` | a declared plugin removed | breaking |
| `COMMAND_HIDDEN`, `COMMAND_UNHIDDEN` | a command hidden or shown | possibly breaking, safe |
| `ALIAS_NO_DELETE`, `HIDDEN_ALIAS_NO_DELETE` | an alias removed | breaking; expected when planned |
| `ALIAS_ADDED`, `HIDDEN_ALIAS_ADDED`, `ALIAS_MOVED` | an alias added, or moved between listed and hidden | safe |
| `OPTIONS_FIRST_CHANGED`, `COMMAND_PASSTHROUGH_CHANGED` | how the command line is read | breaking |
| `DIGIT_FLAG_ADDED` | a flag like `-4` on a command with arguments | possibly breaking |
| `FLAG_GROUP_ADDED`, `FLAG_GROUP_TIGHTENED` | a flag group added, or accepting fewer command lines | breaking |
| `FLAG_GROUP_REMOVED`, `FLAG_GROUP_LOOSENED` | a flag group removed, or accepting more | safe |
| `FLAG_DEPENDENCY_ADDED`, `FLAG_DEPENDENCY_TIGHTENED` | a dependency added; `requires` or `forbids` grown, `equals` widened | breaking |
| `FLAG_DEPENDENCY_REMOVED`, `FLAG_DEPENDENCY_LOOSENED` | a dependency removed; `requires` or `forbids` shrunk, `equals` narrowed | safe |
| `CONFIG_FILE_NO_DELETE`, `CONFIG_FILE_MOVED`, `CONFIG_FILE_AS_CHANGED` | a config file removed, moved (path, discovery, format), or read as variables | breaking |
| `CONFIG_FILE_ADDED` | a config file added | safe |
| `PLUGIN_DISCOVERY_NO_DELETE`, `PLUGIN_DISCOVERY_PREFIX_CHANGED`, `PLUGIN_DISCOVERY_ADDED` | plugin discovery | breaking, breaking, safe |
| `STDIN_ADDED` | starts reading stdin | possibly breaking |
| `STDIN_NO_DELETE`, `STDIN_FORMAT_CHANGED`, `STDIN_REQUIRED_ADDED`, `STDIN_TYPE_CHANGED`, `STDIN_SEPARATOR_CHANGED` | stdin removed, or read differently | breaking |
| `STDIN_REQUIRED_REMOVED` | stdin no longer required | safe |
| `STDIN_UNLESS_ARGUMENT_CHANGED` | the argument that replaces stdin | possibly breaking |
| `OUTPUT_NO_DELETE`, `OUTPUT_STREAM_CHANGED` | an output removed, or a stream turned on or off | breaking |
| `OUTPUT_ADDED` | an output declared | safe |
| `EXIT_STATUS_NO_DELETE`, `EXIT_SUMMARY_CHANGED`, `EXIT_NAME_CHANGED`, `EXIT_RETRYABLE_REMOVED` | an exit status removed, reworded, renamed, or no longer retryable | possibly breaking |
| `EXIT_STATUS_ADDED`, `EXIT_NAME_ADDED`, `EXIT_RETRYABLE_ADDED` | an exit status added, named, or now retryable | safe |
| `DEPRECATION_ADDED`, `DEPRECATION_CHANGED`, `DEPRECATION_REMOVED`, `LIFECYCLE_CHANGED`, `REPLACED_BY_CHANGED` | deprecation, planned removal and replacement | safe |
| `STABILITY_PROMOTED`, `STABILITY_DEMOTED` | stability | safe, possibly breaking |
| `FLAG_NO_DELETE` | a flag removed | breaking; expected when planned; possibly breaking when it was hidden |
| `FLAG_REPLACED` | a flag removed for the `replaced_by` it named | expected |
| `FLAG_ADDED`, `FLAG_REQUIRED_ADDED` | a flag added, optional or required (a short-circuit flag is never required) | safe, breaking |
| `FLAG_REQUIRED_REMOVED` | no longer required | safe |
| `FLAG_NAME_CHANGED` | the logical name, same identifiers | possibly breaking |
| `FLAG_IDENTIFIER_NO_DELETE`, `FLAG_HIDDEN_IDENTIFIER_NO_DELETE`, `FLAG_NEGATED_NO_DELETE` | an identifier or a negated form removed | breaking; expected when planned |
| `FLAG_IDENTIFIER_ADDED`, `FLAG_HIDDEN_IDENTIFIER_ADDED`, `FLAG_IDENTIFIER_MOVED`, `FLAG_NEGATED_ADDED` | an identifier or negated form added, or moved | safe |
| `FLAG_NO_LONGER_CASCADES`, `FLAG_CASCADES` | sub-commands stop or start accepting it | breaking, safe |
| `FLAG_SHORT_CIRCUIT_REMOVED`, `FLAG_SHORT_CIRCUIT_ADDED` | it waives the command's requirements | possibly breaking, safe |
| `FLAG_REPEAT_FORBIDDEN`, `FLAG_REPEAT_ALLOWED` | giving it twice | breaking, safe |
| `FLAG_ROLE_ADDED`, `FLAG_ROLE_CHANGED` | a `role` declared, or changed or removed (declaring `chdir` counts as a change) | safe, possibly breaking |
| `FLAG_ROLE_VALUE_CHANGED` | the `role_value` that selects JSON | safe when added, possibly breaking when changed or removed |
| `ARGUMENT_NO_DELETE` | an argument removed | breaking; expected when planned |
| `ARGUMENT_ADDED`, `ARGUMENT_REQUIRED_ADDED`, `ARGUMENT_REQUIRED_REMOVED` | an optional one added at the end; a required one added, or now required; no longer required | safe, breaking, safe |
| `ARGUMENT_NAME_CHANGED`, `ARGUMENT_VARIADIC_ADDED`, `ARGUMENT_GLOB_CHANGED` | renamed, now variadic, or patterns expanded on Windows | possibly breaking |
| `ARGUMENT_VARIADIC_REMOVED`, `ARGUMENT_PASSTHROUGH_CHANGED` | no longer variadic, or raw words start elsewhere | breaking |
| `ENV_NO_DELETE`, `ENV_VARIABLE_NO_DELETE`, `ENV_REQUIRED_ADDED`, `ENV_NESTING_CHANGED` | a variable removed or renamed, now required, or nested differently | breaking |
| `ENV_ADDED`, `ENV_VARIABLE_ADDED`, `ENV_REQUIRED_REMOVED` | a variable added, or no longer required | safe |
| `CONFIG_NO_DELETE`, `CONFIG_MOVED`, `CONFIG_REQUIRED_ADDED` | a config key removed, moved to another file, or now required | breaking |
| `CONFIG_ADDED`, `CONFIG_REQUIRED_REMOVED` | a config key added, or no longer required | safe |
| `INPUT_TYPE_CHANGED`, `INPUT_TYPE_WIDENED`, `INPUT_TYPE_NOW_DESCRIBED` | an input's type narrowed or changed (`int` → `int8`, `[]int` → `int`), widened, or newly stated where it was open | breaking, safe, safe |
| `INPUT_KIND_CHANGED`, `INPUT_SEPARATOR_CHANGED` | how the value is supplied or split | breaking |
| `INPUT_FROM_NO_DELETE`, `INPUT_FROM_ADDED` | where a typed value may come from (`@file`, `-`) | breaking, possibly breaking |
| `INPUT_IMPLICIT_VALUE_ADDED`, `INPUT_IMPLICIT_VALUE_REMOVED`, `INPUT_IMPLICIT_VALUE_CHANGED` | an optional value | breaking, breaking, possibly breaking |
| `INPUT_IGNORE_CASE_REMOVED`, `INPUT_IGNORE_CASE_ADDED` | enum case matching | breaking, safe |
| `INPUT_LAYOUT_NO_DELETE`, `INPUT_LAYOUT_ADDED`, `INPUT_LAYOUT_FIRST_CHANGED` | time layouts | breaking, safe, possibly breaking |
| `INPUT_RELATIVE_ADDED`, `INPUT_RELATIVE_CHANGED`, `INPUT_RELATIVE_TO_CHANGED` | relative times, or what relative paths resolve against | safe, breaking, breaking |
| `INPUT_EXPAND_ADDED`, `INPUT_EXPAND_REMOVED` | `~` or `$VAR` expansion | possibly breaking, breaking |
| `INPUT_VALUES_FROM_ADDED`, `INPUT_VALUES_FROM_REMOVED`, `INPUT_VALUES_FROM_CHANGED` | an enum that follows output fields | breaking, safe, possibly breaking |
| `INPUT_ENV_NO_DELETE`, `INPUT_ENV_ADDED`, `INPUT_CONFIG_KEY_CHANGED`, `INPUT_CONFIG_KEY_ADDED`, `INPUT_VARIABLE_FILE_NO_DELETE`, `INPUT_VARIABLE_FILE_ADDED` | a fallback variable, config key or file variable | breaking when removed or renamed, safe when added |
| `INPUT_CONFIG_SOURCE_CHANGED`, `INPUT_DOTTED_KEYS_CHANGED` | where a config path comes from, or how dotted keys nest | breaking |
| `INPUT_SECRET_CHANGED` | the value is or isn't a secret | safe |
| `INPUT_HIDDEN`, `INPUT_UNHIDDEN` | an input hidden or shown | possibly breaking, safe |
| `INPUT_DEFAULT_CHANGED`, `INPUT_DEFAULT_REMOVED`, `INPUT_DEFAULT_ADDED` | a default | possibly breaking, possibly breaking, safe |
| `INPUT_ENUM_ADDED`, `INPUT_ENUM_REMOVED` | an input limited to a set of values, or no longer | breaking, safe |
| `ENUM_VALUE_NO_DELETE`, `ENUM_VALUE_REPLACED`, `ENUM_VALUE_ADDED` | an input enum value removed, removed for its `replaced_by`, or added | breaking (expected when planned), expected, safe |
| `ENUM_ALIAS_NO_DELETE`, `ENUM_ALIAS_ADDED` | an enum value's alias | breaking, safe |
| `ENUM_VALUE_HIDDEN`, `ENUM_VALUE_UNHIDDEN` | an enum value hidden or shown | possibly breaking, safe |
| `INPUT_BOUND_ADDED`, `INPUT_BOUND_NARROWED`, `INPUT_BOUND_WIDENED`, `INPUT_BOUND_REMOVED` | bounds, lengths, item counts, `multipleOf`, `uniqueItems` | breaking, breaking, safe, safe |
| `INPUT_PATTERN_ADDED`, `INPUT_PATTERN_CHANGED`, `INPUT_PATTERN_REMOVED` | a pattern | breaking, possibly breaking, safe |
| `INPUT_FORMAT_CHANGED`, `INPUT_FORMAT_REMOVED` | a JSON Schema `format` | possibly breaking, safe |
| `INPUT_PROPERTY_REQUIRED_ADDED`, `INPUT_PROPERTY_NO_DELETE`, `INPUT_ADDITIONAL_PROPERTIES_REMOVED` | an object input: a property now required, removed while unknown ones are refused, or unknown ones now refused | breaking |
| `INPUT_PROPERTY_ADDED` | an optional property added | safe |
| `SCHEMA_COMPOSITION_CHANGED` | a different number of `anyOf`, `oneOf` or `allOf` branches | possibly breaking |
| `OUTPUT_TYPE_CHANGED`, `OUTPUT_PROPERTY_NO_DELETE`, `OUTPUT_PROPERTY_REQUIRED_REMOVED` | an output value's type, a property removed, or no longer always written | breaking |
| `OUTPUT_PROPERTY_ADDED`, `OUTPUT_ENUM_ADDED`, `OUTPUT_ENUM_VALUE_REMOVED` | a property added; an output value limited to fewer values | safe |
| `OUTPUT_ENUM_VALUE_ADDED`, `OUTPUT_ENUM_REMOVED` | an output value that may now be something new | possibly breaking |
| `EFFECTS_ADDED`, `EFFECTS_REMOVED` | a command's or flag's `effects` declared, or no longer declared (agents then assume the worst) | safe, possibly breaking |
| `EFFECTS_RAISED`, `EFFECTS_LOWERED` | a riskier kind, no longer idempotent or no longer local; or the reverse | possibly breaking, safe |
| `AGENT_REMOVED`, `AGENT_ADDED` | a command or input kept from AI agents (`agent: false`, or `agent: true` dropped); or offered again | possibly breaking, safe |
| `PROFILES_ADDED` | a config file gains profiles | safe; possibly breaking with a `default`, which a run that selects none now reads |
| `PROFILES_NO_DELETE`, `PROFILES_UNDER_CHANGED` | profiles no longer read, or read under another key | breaking |
| `PROFILES_FLAG_NO_DELETE`, `PROFILES_ENV_NO_DELETE` | a flag or variable no longer selects a profile | breaking |
| `PROFILES_FLAG_ADDED`, `PROFILES_ENV_ADDED` | a flag or variable now selects a profile | safe |
| `PROFILES_DEFAULT_CHANGED` | the profile a run reads when none is selected | possibly breaking |

## Composing CLIs

A command can be another CLI's spec: `- $ref: ../db/.rotini.spec.yaml` mounts it as a
sub-command, and it still builds and ships on its own. To mount a spec from a module your project
depends on, use `$ref: mod://<module>@<version>/<path>`; it is read from the module cache and
verified by `go.sum`. Keys set next to the `$ref`, such as a new `name`, `summary` or `group`, adjust
it for its new parent; see [`$ref`](/specification#ref).

A `mod://` child stays at the version `go.sum` pins, so upgrading rotini doesn't move it. Upgrade
the child's module on its own, then regenerate; its own `version:` check reports it if it needs
a newer rotini than you have. A local `$ref` is rebuilt whenever you regenerate the parent.

## Versions

`main.go` passes `version` to `WithVersion`, and the root handler `rotini init` writes prints it
for `--version`. A binary built with `go install` or `go build` already knows its version from
Go's build information:

- `go install github.com/me/todo/cmd/todo@v1.2.3` reports `v1.2.3`;
- `go build` in a clean checkout at a tag reports the tag;
- after commits since the tag, it reports a pseudo-version, such as
  `v1.2.4-0.20261010090704-ce9ee44061fd`;
- with uncommitted changes, it adds `+dirty`, as in `v1.2.3+dirty`;
- `go run`, and `go build` outside version control or with `-buildvcs=false`, report `(devel)`.

To use it, read the build information when no version was stamped:

{{< code title="cmd/todo/main.go (build info)" language="go" open="true" collapsible="false" copy="true" >}}
//go:generate go tool rotini generate ./.rotini.spec.yaml --config ./.rotini.conf.yaml
package main

import (
	"runtime/debug"

	cmd "github.com/me/todo/internal/cmd/todo"
)

// version is set with -ldflags "-X main.version=1.2.3" when building outside version control.
var version = ""

func main() {
	v := version
	if info, ok := debug.ReadBuildInfo(); ok && v == "" {
		v = info.Main.Version // a tag, a pseudo-version (+dirty with local edits), or (devel)
	}
	cmd.NewProgram(cmd.Handlers()).
		WithVersion(v).
		Execute()
}
{{< /code >}}

Stamp the version yourself when building from a source archive, with no version control, or
with `-buildvcs=false`. `-ldflags` wins over the build information:

{{< code title="terminal" language="sh" open="true" collapsible="false" copy="true" >}}
go build -ldflags "-X main.version=1.2.3" ./cmd/todo
./todo --version   # todo 1.2.3
{{< /code >}}

The `version:` key at the top of your spec and conf is the minimum rotini version they need. An
older rotini, or a different major version, refuses to generate from them.

## Upgrading

Rotini is still evolving, so a minor release can include breaking changes. Every release lists
them in its release notes, with how to migrate.

The tool and the runtime are one module, so upgrade them together, then regenerate and read the
diff:

{{< code title="terminal" language="sh" open="true" collapsible="false" copy="true" >}}
go get -u github.com/go-rotini/rotini
go get -tool github.com/go-rotini/rotini/cmd/rotini@latest
go generate ./...
git diff
go build ./... && go test ./...
{{< /code >}}

To see what an upgrade would change before it writes anything, run
`go tool rotini generate --dry-run <spec>` after the `go get` lines and before
`go generate`.

You don't need to edit your spec or conf to upgrade: their `version:` is the oldest rotini they
need, not the one they must use (see [the version check](/cli#rotini-version)). Raise it when you
start using a key that needs a newer one; validation tells you which.

What to expect in the diff:

- **the generated file changes**: new glue, reordered literals, new helpers. A change to a name
  your handlers use is listed in the release notes.
- **rendered help, man and markdown pages may change layout.** If you test `--help` against a
  saved copy, update it, or keep the layout fixed by setting the feature's `template: true` and
  editing the template it seeds.
- **your handler files and `main.go` don't change.** They are created once and never
  rewritten; see [what stays yours](/generated#what-stays-yours).

If an upgrade breaks something the release notes don't list, open an issue with both versions
and the diff.
