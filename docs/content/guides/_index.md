---
title: "guides"
---

# Guides

Task-oriented recipes. Each one is a small, complete change to the same CLI — a `todo` list, the project [setup](/docs) leaves you with — so they read in order or stand alone.

## Add a command

Commands are spec entries. Write one, regenerate, and rotini seeds a stub for it.

{{< code title="cmd/todo/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
  commands:
    - name: done
      summary: mark a task done
      arguments:
        - name: id
          summary: which task
          schema: { type: int, required: true, minimum: 1 }
{{< /code >}}

{{< code title="regenerate" language="text" open="true" collapsible="false" copy="true" >}}
$ go generate ./...
$ ls internal/cmd/todo/
todo.go  todo_add.go  todo_done.go  todo_help.go  todo_list.go  zz_rotini.go
{{< /code >}}

`todo_done.go` is new and now yours — later passes never touch it. Everything rotini owns is in the one `zz_` file.

Deleting the command from the spec reverses this, and says so rather than doing it silently:

{{< code title="removing a command" language="text" open="true" collapsible="false" copy="true" >}}
Note: pruned todo_version.go — its command is no longer in the spec
{{< /code >}}

{{< alert type="info" title="TRY IT WITHOUT GENERATING:" >}}
`rotini validate` runs the schema and all 34 lint rules and writes nothing. It is the right thing to put in CI, and fast enough to bind to a keystroke.
{{< /alert >}}

## Declare inputs

Every input a command accepts is declared, and every declaration becomes a typed field. Flags and arguments are the two argv channels:

{{< code title="cmd/todo/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
    - name: add
      summary: add a task
      arguments:
        - name: title
          summary: what to do
          schema: { type: string, required: true, minLength: 1 }
      flags:
        - name: priority
          summary: how urgent it is
          identifiers: [-p, --priority]
          schema:
            type: string
            enum: [low, normal, high]
            default: normal
        - name: tag
          summary: label it (repeatable)
          identifiers: [--tag]
          schema: { type: '[]string', default: [inbox] }
{{< /code >}}

The generated struct mirrors the shape — one sub-struct per command in the chain, each with the channels that command declares:

{{< code title="internal/cmd/todo/zz_rotini.go" language="golang" open="true" collapsible="false" copy="true" >}}
type TodoAddFlags struct {
	Priority string   `rotini:"priority"`
	Tag      []string `rotini:"tag"`
}

type TodoAddArguments struct {
	Title string `rotini:"title"`
}

type TodoAddInputs struct {
	Todo    TodoCommandInputs    // the root's own flags, including cascading ones
	TodoAdd TodoAddCommandInputs // Flags + Arguments
}
{{< /code >}}

`Collect` fills it, validated, in one line:

{{< code title="internal/cmd/todo/todo_add.go" language="golang" open="true" collapsible="false" copy="true" >}}
func (*todoAddHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rotini.Collect[TodoAddInputs](rtx)
	if err != nil {
		rtx.RecordError(err)
		rtx.Halt()
		return
	}

	add := inputs.TodoAdd
	fmt.Fprintf(rtx.Stdout, "added %q [%s] (%s)\n",
		add.Arguments.Title, add.Flags.Priority, strings.Join(add.Flags.Tag, ", "))
}
{{< /code >}}

Nothing above validates anything, because the spec already did:

{{< code title="what the user sees" language="text" open="true" collapsible="false" copy="true" >}}
$ todo add "write the docs"
added "write the docs" [normal] (inbox)

$ todo add "ship it" --priority high --tag work --tag urgent
added "ship it" [high] (work, urgent)

$ todo add
Error: missing required input: <title>

$ todo add "x" --priority nope
Error: invalid value "nope" for -p (one of: low, normal, high)
{{< /code >}}

A list `default` seeds one occurrence per element, and an explicit `--tag` replaces the whole list rather than appending to it.

{{< alert type="info" title="COUNT FLAGS CASCADE:" >}}
`schema: {type: count}` with `cascading: true` on the root is the conventional `-v`/`-vv`: declared once, reachable from every command as `inputs.Todo.Flags.Verbose`, an `int`.
{{< /alert >}}

See [the spec reference](/specification) for the other channels — `stdin`, value sentinels (`@file`, `-`), `passthrough`, flag groups and dependencies.

## Read environment and configuration

Environment variables and configuration files are channels of their own, declared beside the flags. They land in their own sub-structs, so a handler reads them exactly like a flag.

{{< code title="cmd/todo/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
command:
  name: todo
  env_prefix: TODO
  config_files:
    - name: project
      discover: { strategy: walk-up, file: .todorc.yaml }   # git-style: search upward
  commands:
    - name: list
      env:
        - name: editor
          schema: { type: string, variable: EDITOR, default: vi }
      config:
        - name: limit
          schema: { type: int, key: list.limit, default: 20, minimum: 1 }
{{< /code >}}

{{< code title="internal/cmd/todo/todo_list.go" language="golang" open="true" collapsible="false" copy="true" >}}
inputs, err := rotini.Collect[TodoListInputs](rtx)
// ...
fmt.Fprintf(rtx.Stdout, "limit=%d editor=%s\n",
	inputs.TodoList.Config.Limit, inputs.TodoList.Env.Editor)
{{< /code >}}

{{< code title="what the user sees" language="text" open="true" collapsible="false" copy="true" >}}
$ todo list
limit=20 editor=vi

$ EDITOR=nvim todo list
limit=20 editor=nvim

$ cat .todorc.yaml
list:
  limit: 5
$ todo list
limit=5 editor=vi
{{< /code >}}

`variable: EDITOR` names an exact variable, exempt from `env_prefix`; without it, `env_prefix` plus the input name is the variable. `discover` replaces a fixed `path` with a search: `walk-up` from the working directory, or `xdg` under `$XDG_CONFIG_HOME/<app>`.

### One input, several sources

A **flag** can fall back through the other channels. Give it a `key:` (a dotted configuration path) and a `variable:`, and it reads from all three:

{{< code title="a flag with fallbacks" language="yaml" open="true" collapsible="false" copy="true" >}}
        - name: priority
          identifiers: [-p, --priority]
          schema:
            type: string
            enum: [low, normal, high]
            default: normal
            key: defaults.priority     # .todorc.yaml → defaults.priority
            variable: TODO_PRIORITY    # $TODO_PRIORITY
{{< /code >}}

Precedence is **argv → environment → configuration file → default**, highest first:

{{< code title="the ladder, top to bottom" language="text" open="true" collapsible="false" copy="true" >}}
$ cat .todorc.yaml
defaults:
  priority: high

$ todo add "from the config file"
added "from the config file" [high] (inbox)

$ TODO_PRIORITY=low todo add "env beats the file"
added "env beats the file" [low] (inbox)

$ TODO_PRIORITY=low todo add "argv beats env" -p normal
added "argv beats env" [normal] (inbox)
{{< /code >}}

When more than one configuration file is in scope, the nearest wins, and declaration order is precedence order. `CollectP` returns a provenance report if a handler needs to know which source actually supplied a value.

## Report errors and choose exit codes

A handler does not print failures. It **records** them and stops; one funnel reports everything once, after teardown.

{{< code title="the pattern" language="golang" open="true" collapsible="false" copy="true" >}}
if err := store.Save(task); err != nil {
	rtx.RecordError(err)
	rtx.Halt()
	return
}
{{< /code >}}

Recording without halting is the mistake to know about: the next hook collects the same inputs, fails the same way, and records the same error twice. `Halt` stops without claiming an exit code; use `SignalExit(n)` only when the number itself is the point — a filter reporting "no match" as `1`, a wrapper passing a child's status through.

Errors carry a category so a funnel can classify one in a single call. Tag your own domain errors the same way:

{{< code title="tagging a domain error" language="golang" open="true" collapsible="false" copy="true" >}}
if !store.Has(id) {
	// UsageError says "the user can fix this" without changing the message.
	rtx.RecordError(rotini.UsageError(fmt.Errorf("no task %d", id)))
	rtx.Halt()
	return
}
{{< /code >}}

Any funnel — the default or your own — reads the tag back with one call:

{{< code title="classifying" language="golang" open="true" collapsible="false" copy="true" >}}
rotini.CategoryOf(err)           // CategoryUsage | CategoryInternal | CategoryNone
errors.Is(err, rotini.ErrUsage)  // or match the sentinel directly
{{< /code >}}

The default funnel prints each channel with a severity label and exits non-zero when anything failed, never downgrading a code a handler deliberately set. Replace it to own reporting and exit codes outright:

{{< code title="cmd/todo/main.go" language="golang" open="true" collapsible="false" copy="true" >}}
cmd.Program.
	WithVersion(version).
	WithFunnel(func(ctx context.Context, rtx *rotini.Context, out rotini.Outcome) {
		for _, err := range out.Errors {
			fmt.Fprintf(rtx.Stderr, "todo: %v\n", err)
		}
		if out.Failed() {
			rtx.Exit(64) // the funnel is the last authority on the code
		}
	}).
	Execute()
{{< /code >}}

{{< code title="what the user sees" language="text" open="true" collapsible="false" copy="true" >}}
$ todo add
todo: missing required input: <title>
$ echo $?
64
{{< /code >}}

The full taxonomy — `*ParseError`, `*BindError`, `*RemoteError` and the fault types — is on the [API page](/api#errors).

## Test your CLI

A `Program` is an ordinary value with replaceable streams, an argument vector and an exit action. That makes an end-to-end test of the real binary an ordinary Go test: no subprocess, no golden binary, no `os.Exit` to work around.

{{< code title="internal/cmd/todo/todo_test.go" language="golang" open="true" collapsible="false" copy="true" >}}
package todo_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/go-rotini/rotini"

	todo "github.com/me/todo/internal/cmd/todo"
)

// run executes the CLI in-process, the way main does, and returns what a user would see.
func run(t *testing.T, argv ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errs bytes.Buffer
	code = -1
	todo.NewProgram(todo.Handlers()).
		WithVersion("0.0.0-test").
			WithArgs(argv).
		WithStdin(strings.NewReader("")).
		WithStdout(&out).
		WithStderr(&errs).
		WithExit(func(c int) { code = c }).
		Execute()
	return out.String(), errs.String(), code
}
{{< /code >}}

Every input channel is reachable from the test, because every one of them is a seam:

{{< code title="the four channels, four ways" language="golang" open="true" collapsible="false" copy="true" >}}
func TestAdd(t *testing.T) {
	out, _, code := run(t, "add", "write the docs", "--priority", "high")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if want := "added \"write the docs\" [high] (inbox)\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
}

func TestAddRejectsBadPriority(t *testing.T) {
	_, errs, code := run(t, "add", "x", "--priority", "nope")
	if code == 0 {
		t.Fatal("exit = 0, want non-zero")
	}
	if !strings.Contains(errs, "one of: low, normal, high") {
		t.Errorf("stderr = %q", errs)
	}
}

func TestListReadsEnv(t *testing.T) {
	t.Setenv("EDITOR", "nvim")           // the env channel
	out, _, _ := run(t, "list")
	if !strings.Contains(out, "editor=nvim") {
		t.Errorf("stdout = %q", out)
	}
}

func TestListReadsConfigFile(t *testing.T) {
	dir := t.TempDir()                   // the config-file channel
	os.WriteFile(dir+"/.todorc.yaml", []byte("list:\n  limit: 5\n"), 0o644)
	t.Chdir(dir)                         // walk-up discovery starts here
	out, _, _ := run(t, "list")
	if !strings.Contains(out, "limit=5") {
		t.Errorf("stdout = %q", out)
	}
}
{{< /code >}}

Three details make this work, and they are worth knowing before you write the first test:

- **`WithExit`** replaces `os.Exit`, so `Execute` returns to the test with the code recorded instead of killing the test binary. It is also the only way to see the error `Execute` returns — the run's recorded failures, joined — since under `os.Exit` the process ends before the return runs.
- **`WithStdout` / `WithStderr`** are why handlers write to `rtx.Stdout`. A handler that reaches for `os.Stdout` is the one thing that will not be captured.
- **A fresh `Program` per call.** Each run gets its own `Context`, so one test never inherits another's recorded outcomes. Build it in the helper, not in a package variable.

For a handler with dependencies, bind a fake through the same registry the entrypoint uses:

{{< code title="substituting a dependency" language="golang" open="true" collapsible="false" copy="true" >}}
StoreKey.Provide(todo.NewProgram(todo.Handlers()), newFakeStore()).
	WithArgs(argv).
	WithStdout(&out).
	WithExit(func(c int) { code = c }).
	Execute()
{{< /code >}}

{{< alert type="info" title="TWO SMALLER SEAMS:" >}}
`Program.Run(argv)` returns the exit code instead of exiting — the same thing as `WithExit`, without the closure. And `rotini.NewContextFor(def, argv)` builds a bare `Context` against a `Definition` you construct by hand, for exercising the `Parser` alone. Reach for it only there: a handler test wants the real program, because the real program is what has the real command tree.
{{< /alert >}}

## Share handlers between CLIs

One codebase can ship several binaries that share command implementations. A parent spec pulls a child in by reference, and the child stays independently buildable:

{{< code title="cmd/acme/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
  commands:
    - $ref: ../db/.rotini.spec.yaml
      name: db          # the overlay renames it under the parent
{{< /code >}}

`acme db migrate` and the standalone `acme-db migrate` then run the same handler, because they *are* the same handler. Inputs bind to the **end** of the command chain, so a child's generated type lands on its own frames whichever tree it was grafted into.

A child's **input channels come with it**, not just its commands. `config_files` and `env_prefix` declared on the child travel with the graft — its configuration sources re-scoped to where the graft sits — so the parent re-declares nothing and both binaries read the same file and the same variables. A parent that declares its own `env_prefix` wins; two children that disagree are rejected at generate time, because one descriptor carries one prefix.

To keep the handlers in a package other CLIs import, point the command at it. If that package also needs the generated input types, declare a `models` target so the structs live somewhere both packages can import — the cmd package imports the handler package, so the handler package cannot import it back:

{{< code title="handler package + models target" language="yaml" open="true" collapsible="false" copy="true" >}}
# spec
    - name: health
      handler:
        import: healthh example.com/acme/handlers/health
        convention: Health

# conf
  packages:
    - type: models
      file: internal/models/zz_models.go
      package: models
{{< /code >}}

The five composition modes — inline, passthrough, local `$ref`, module `$ref`, and dispatch to a sibling binary — are laid out on the [specification page](/specification#composition).

## Ship it

A rotini CLI is an ordinary Go binary, so `go build` and `go install` are the whole distribution story. What rotini adds is everything a packaged CLI is expected to carry — a version, a completion script, man and markdown pages — generated from the spec you already wrote.

### Stamp the version

The entrypoint binds whatever the binary was built with, and `--version` reports it:

{{< code title="cmd/todo/main.go" language="golang" open="true" collapsible="false" copy="true" >}}
var version = "0.0.0" // overridden at build time

func main() {
	cmd.Program.
		WithVersion(version).
			Execute()
}
{{< /code >}}

{{< code title="building a release" language="text" open="true" collapsible="false" copy="true" >}}
$ go build -ldflags "-X main.version=1.2.3" -o todo ./cmd/todo
$ ./todo --version
1.2.3
{{< /code >}}

### Offer shell completion

Turn the feature on and rotini generates a script per shell, plus a resolver to hand them out:

{{< code title="cmd/todo/.rotini.conf.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
  features:
    - type: completion
      enabled: true
{{< /code >}}

The scripts call a hidden `__complete` entry point that every rotini binary answers, so completion reflects the live command tree — including values a dynamic completer supplies at run time. Expose them with an ordinary command:

{{< code title="cmd/todo/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
    - name: completion
      summary: print a shell completion script
      arguments:
        - name: shell
          summary: which shell
          schema: { type: string, required: true, enum: [bash, zsh, fish, powershell] }
{{< /code >}}

{{< code title="internal/cmd/todo/todo_completion.go" language="golang" open="true" collapsible="false" copy="true" >}}
script, err := Completion(inputs.TodoCompletion.Arguments.Shell) // generated resolver
if err != nil {
	rtx.RecordError(err)
	rtx.Halt()
	return
}
fmt.Fprint(rtx.Stdout, script)
{{< /code >}}

{{< code title="what the user does" language="text" open="true" collapsible="false" copy="true" >}}
$ todo completion zsh > "${fpath[1]}/_todo"

$ todo completion nope
Error: invalid value "nope" for <shell> (one of: bash, zsh, fish, powershell)
{{< /code >}}

The `enum` is why the second case is a clean message rather than an empty script — the command validates its own argument because the spec declared what is valid.

### Man and markdown pages

Same shape, one page per command. `Man(path...)` and `Markdown(path...)` resolve them, and `embed` decides whether the content is a string literal in the generated file or a real file referenced with `//go:embed`:

{{< code title="cmd/todo/.rotini.conf.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
    - type: man
      enabled: true
      embed: true
      embed_dir: internal/cmd/todo/man      # module-root-relative, under the cmd package
    - type: markdown
      enabled: true
      embed: true
      embed_dir: internal/cmd/todo/markdown
{{< /code >}}

`embed: true` is the one to choose when a packaging step needs the files on disk — an `.rpm` shipping `man/man_todo.txt`, or a docs site publishing the markdown. `embed: false`, the default, keeps the generated `.go` self-contained.

{{< alert type="info" title="EMBED PATHS ARE CHECKED:" >}}
`//go:embed` cannot reach outside its own package, so rotini rejects an `embed_dir` that resolves elsewhere rather than emitting code that will not compile:

`generate.features.man.embed_dir: "docs/man" must resolve under the cmd package "internal/cmd/todo" so //go:embed can reach it`
{{< /alert >}}

### Build for other platforms

Nothing rotini-specific: the generated code is pure Go with no cgo, so cross-compilation is the usual environment variables.

{{< code title="cross-compiling" language="sh" open="true" collapsible="false" copy="true" >}}
for target in darwin/arm64 linux/amd64 windows/amd64; do
	out="dist/todo_${target//\//_}"
	GOOS=${target%/*} GOARCH=${target#*/} go build -ldflags "-X main.version=$VERSION" -o "$out" ./cmd/todo
done
{{< /code >}}

Your users install it the way they install anything else — `go install github.com/me/todo/cmd/todo@latest`, or a release archive from whatever your CI publishes.

## Next

- [Examples](/examples) — ten complete CLIs built on everything above
- [Specification](/specification) — every spec key
- [Configuration](/configuration) — codegen targets, features, templates
- [API](/api) — the runtime contract in full
- [Toolkit](/batteries) — printers, tables, prompts, progress, REPL, daemon
