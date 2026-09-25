---
title: "rotini"
---

<div class="rotini_text">rotini</div>

<p class="hero_tagline">Define your CLI declaratively,<br>write its behavior imperatively.</p>

<p class="hero_beats">Validate. Generate. Ship.</p>

<p class="hero_sub">A spec-first CLI framework for Go. Your command tree, inputs, help, completion and docs are declared once and validated before a line of your code exists — then generated as small, legible Go you own.</p>

<div class="hero_actions">
  <a class="primary" href="/docs">Setup</a>
  <a href="/specification">Reference</a>
  <a href="/batteries">Toolkit</a>
</div>

{{< code title="install" language="bash" open="true" collapsible="false" copy="true" >}}
go get -tool github.com/go-rotini/rotini/cmd/rotini   # the codegen tool
go get github.com/go-rotini/rotini                    # the runtime your code imports
{{< /code >}}

One module, one version, both faces. The tool that generates your code and the runtime that code imports are the same dependency — so they cannot drift apart.

---

## How it works

<div class="split">

{{< code title="cmd/todo/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
version: 0.0.0
command:
  name: todo
  summary: a task list
  commands:
    - name: add
      summary: add a task
      arguments:
        - name: title
          summary: what to do
          schema: {type: string, required: true, minLength: 1}
      flags:
        - name: priority
          summary: how urgent
          identifiers: [-p, --priority]
          schema:
            type: string
            default: normal
            enum: [low, normal, high]
        - name: tag
          summary: label it (repeatable)
          identifiers: [--tag]
          schema:
            type: '[]string'
            default: [inbox]
{{< /code >}}

{{< code title="internal/cmd/todo/todo_add.go — yours to fill in" language="go" open="true" collapsible="false" copy="true" >}}
func (*todoAddHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	in, err := rotini.Collect[TodoAddInputs](rtx)
	if err != nil {
		rtx.RecordError(err)
		rtx.Halt()
		return
	}
	add := in.TodoAdd

	// Parsed, coerced and validated before you see it:
	//   Title    is non-empty       (minLength)
	//   Priority is one of three    (enum, defaulted to "normal")
	//   Tag      is []string        and already holds ["inbox"]
	rtx.RecordSuccess(fmt.Sprintf("added %q [%s] %v",
		add.Arguments.Title, add.Flags.Priority, add.Flags.Tag))
}
{{< /code >}}

</div>

`go generate ./...` turns the spec into a typed `TodoAddInputs`, the command tree, the help pages, the completion scripts — and one editable handler stub per command. You write the body; rotini writes everything around it.

{{< code title="and it already behaves" language="bash" open="true" collapsible="false" copy="true" >}}
$ todo add "write the docs"
added "write the docs" [normal] [inbox]

$ todo add "ship it" -p high --tag release --tag urgent
added "ship it" [high] [release urgent]

$ todo add "x" -p urgent
Error: invalid value "urgent" for -p (one of: low, normal, high)
{{< /code >}}

Nothing above was hand-written except the body of `Run`. The enum, the default, the repeatable flag and the error message all come from the spec.

---

## What you get

<div class="feature_grid">

<div class="feature_card">
<h3>Typed inputs</h3>
<p>One generated struct per command, already parsed and validated — <code>time.Duration</code>, slices, maps, enums, your own <code>TextUnmarshaler</code>. Four channels reconcile into it: argv, environment, config files and stdin, in one documented precedence.</p>
</div>

<div class="feature_card">
<h3>Checked before it compiles</h3>
<p>A JSON Schema and 34 spec lint rules reject a misspelled key, a duplicate identifier, a <code>$ref</code> cycle or a bound that can never fire — each with a <code>file:line:col</code>, from <code>rotini validate</code>.</p>
</div>

<div class="feature_card">
<h3>Composition</h3>
<p>One CLI grafts another in whole, by local path or by a published module pinned in <code>go.sum</code>. Ship the children on their own, the umbrella, or both — from one codebase.</p>
</div>

<div class="feature_card">
<h3>Docs and completion</h3>
<p>Help, man pages, markdown and completion for bash, zsh, fish and PowerShell, rendered from the same spec — so they cannot disagree with the binary. Bring your own template if you want to.</p>
</div>

<div class="feature_card">
<h3>Lifecycle and outcomes</h3>
<p>Five hooks per command, cascading down and unwinding in reverse — teardown runs after a failure or a panic. Every result reaches one funnel that decides what prints and what the process exits with.</p>
</div>

<div class="feature_card">
<h3>Batteries, all opt-in</h3>
<p>Tables, paging, prompts, progress, styling, subprocesses, plugins — and the long-running shapes: <code>Service</code>, <code>Scheduler</code>, <code>REPL</code> and a JSON-RPC <code>StdioServer</code>. Importing rotini starts none of it.</p>
</div>

</div>

---

## Why rotini

### Checked before your code exists

The spec is validated by a JSON Schema plus 34 spec lint rules — a misspelled key, a duplicate flag identifier, a configuration file nothing reads, a `$ref` cycle, an input whose type is not a Go type. Each is reported with a `file:line:col`, by `rotini validate`, before a line of Go is generated.

These are mistakes a compiler has no reason to notice, so catching them is the spec's job — in CI, without building anything.

### CLIs compose

A rotini CLI can graft in another rotini CLI, whole, by reference:

{{< code title="one umbrella, three CLIs that also ship on their own" language="yaml" open="true" collapsible="false" copy="true" >}}
commands:
  - $ref: ../db/.rotini.spec.yaml            # a sibling in this repo
    name: db                                  # renamed on the way in
  - $ref: ../cache/.rotini.spec.yaml
    name: cache
  - $ref: mod://example.com/tools@v1.2.0/scan/.rotini.spec.yaml
{{< /code >}}

`acme db migrate` runs the identical code `acme-db migrate` runs, because it *is* that code — one generated package, one set of handlers. A `mod://` reference is an ordinary Go dependency, pinned in `go.mod` and verified by `go.sum`.

### The generated code is yours

`rotini init` writes a working CLI — one that already answers `--help`, `--version`, `help <command>` and `version`, because the seeded spec declares them and the seeded handlers are wired to the pages codegen just produced. It is short enough to read in one sitting and review in a diff, and every line of it is yours to edit or delete.

The machinery stays an ordinary import you upgrade with `go get -u`, not a vendored copy you must never edit.

## The trade-off

rotini adds a **codegen step**: a tool dependency, a `go generate` pass, and generated files in version control. That is real overhead, and it is the price of everything above being checked and generated rather than written.

The cost is fixed; the benefit scales with the CLI. For a three-command internal script, rotini is heavier than it is worth. For a long-lived, multi-command tool with configuration files, environment variables, documentation and shell completion to keep in sync, the spec becomes the single place all of it is declared — and checked.

{{< alert type="info" title="TRY IT IN TWO MINUTES:" >}}
`go get -tool github.com/go-rotini/rotini/cmd/rotini` then `go tool rotini init mycli` writes a working CLI you can build and run immediately. The [setup guide](/docs) walks the whole loop, and its commands are executed by rotini's own test suite — so the guide cannot quietly stop being true.
{{< /alert >}}

## Start here

- [docs](/docs) — set up a project, end to end
- [guides](/guides) — commands, inputs, configuration, errors, testing, composition
- [specification](/specification) — the `.rotini.spec.*` file
- [configuration](/configuration) — the `.rotini.conf.*` file
- [generated](/generated) — what rotini writes into your project
- [examples](/examples) — ten complete CLIs, and what each one shows
- [batteries](/batteries) — everything past parsing
- [api](/api) — the runtime contract
- [cli](/cli) — the `rotini` command itself
