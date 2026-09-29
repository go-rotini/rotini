---
title: "batteries"
---

# Batteries

Parsing is the first 10% of a CLI. rotini carries the other 90% in the same import — and **nothing is wired unless you wire it**. Importing rotini starts no goroutine, touches no terminal, and changes no behavior; each battery does something only because a handler constructed it and called it.

{{< alert type="info" title="NOTE:" >}}
Two rules hold across every battery. **Configuration chains** — `New*(…).WithX(…).WithY(…)` — because one package cannot hold eight different package-level `WithTimeout` functions. And **every battery degrades safely when there is no terminal**: prompts fail fast instead of hanging, indicators stay silent instead of smearing a CI log with carriage returns, and the pager passes text straight through.
{{< /alert >}}

## Printing output

### Printer

The complement to `Collect`'s "data in": one writer that renders a value as text, JSON, YAML, TOML or a table, chosen from whatever your `--output` flag carried.

It pairs with the spec's command `output:` key, which generates a typed `<Prefix>Output` struct. The spec declares the shape, the Printer renders it, and **neither wires a flag** — you declare `--output` yourself and hand the value in.

{{< code title="printer" language="golang" open="true" collapsible="false" copy="true" >}}
format, err := rotini.ParseFormat(inputs.List.Flags.Output) // "json" -> rotini.FormatJSON
if err != nil {
	rtx.RecordError(err)
	return
}

out := rotini.NewPrinter(rtx.Stdout).WithFormat(format)
if err := out.Print(tasks); err != nil {
	rtx.RecordError(err)
}
{{< /code >}}

A value with no table shape degrades to text under `--output table`, so the flag is a preference rather than an assertion about the result.

### Table

Aligned columns, measured by **display width** — styled cells and wide East-Asian runes line up correctly, where a byte-length measurement would not. Optionally bounded to a width budget (truncating with an ellipsis) and optionally styled through a `Styler`.

{{< code title="table" language="golang" open="true" collapsible="false" copy="true" >}}
rotini.NewTable("NAME", "SIZE").
	WithAlign(rotini.AlignLeft, rotini.AlignRight).
	Row("alpha", "1").
	Row("beta", "1000").
	Fprint(rtx.Stdout)
{{< /code >}}

An empty table prints **nothing** — not a blank line — so a command with no results stays quiet.

### Pager

Sends long output through `$PAGER`, and passes it straight through when there is no terminal, so `mycli list | grep x` is never hijacked. A pager that fails to start is not an error: the text still reaches the writer, because failing to display output is worse than displaying it unpaged.

### Editor

The counterpart: brings text **in** through `$VISUAL`, then `$EDITOR` — the `git commit` / `crontab -e` shape.

{{< code title="edit a draft" language="golang" open="true" collapsible="false" copy="true" >}}
body, err := rotini.NewEditor().WithExtension(".md").Edit(ctx, draft)
switch {
case errors.Is(err, rotini.ErrEditAborted):
	rtx.RecordInfo("no changes")
	return
case err != nil:
	rtx.HaltWith(err)
	return
}
{{< /code >}}

`ErrEditAborted` folds the two ways a user says *never mind* — quit without saving, or save an empty buffer — into one sentinel, so you branch once instead of diffing. The temp file carries your extension so the editor highlights it, and is removed on every path.

### Wrapping, truncating, measuring

`Width` measures **display cells**, so styled and wide-rune text lines up. `Wrap` and `Truncate` do the cut, and neither can split an escape sequence — which would leave the terminal wearing whatever style the fragment half-opened.

{{< code title="fit output to the terminal" language="golang" open="true" collapsible="false" copy="true" >}}
cols, _, ok := rotini.TerminalSize(os.Stdout)
if !ok {
	cols = 80 // a pipe, or a platform that cannot say
}
fmt.Fprintln(rtx.Stdout, rotini.Wrap(description, cols))
rotini.NewTable(headers...).WithWidth(cols).Fprint(rtx.Stdout)
{{< /code >}}

`Wrap` does not reopen styling across a break, because it does not need to: a newline does not reset a terminal, so a color opened before the break is still in effect after it. `TerminalSize` honors `COLUMNS`/`LINES` first — the conventional override, and how a test pins a width.

## Asking questions

`Prompt`, `Confirm` and `Select` read from an `io.Reader`, so the same code works interactively, from a pipe (`echo y | mycli`), and in a test with a `strings.Reader`.

{{< alert type="warning" title="THE CONTRACT:" >}}
Input that ends without an answer returns `ErrNotInteractive` — **never a hang**. That is what makes an interactive command safe to run in CI. A prompt with a default never fails for lack of a human: the default *is* the non-interactive answer.
{{< /alert >}}

{{< code title="prompt / confirm / select" language="golang" open="true" collapsible="false" copy="true" >}}
name, err := rotini.NewPrompt(rtx.Stdin, rtx.Stdout).
	WithLabel("Project name").
	WithDefault("my-app").
	WithValidate(validName).
	WithRetries(2).
	Ask(ctx)

ok, err := rotini.NewConfirm(rtx.Stdin, rtx.Stdout).
	WithLabel("Delete everything?").
	WithDefault(false).
	Ask(ctx)

i, choice, err := rotini.NewSelect(rtx.Stdin, rtx.Stdout, "staging", "production").
	WithLabel("Target").
	WithSuggestor(rotini.NewSuggestor()). // a typo'd answer still resolves
	Ask(ctx)
{{< /code >}}

A secret is read without echo, and the terminal is restored on every path — including a `SIGINT`, because rotini owns the signal trap. Off a terminal it degrades to a plain read, so a handler that asks for a token stays testable.

{{< code title="read a secret" language="golang" open="true" collapsible="false" copy="true" >}}
token, err := rotini.NewPrompt(rtx.Stdin, rtx.Stdout).
	WithLabel("token").
	WithSecret().   // or WithMask('*') to echo the shape of what was typed
	Ask(ctx)
{{< /code >}}

`Select` is a **numbered menu**, not arrow-key navigation. Raw terminal mode would need a platform dependency rotini does not carry, and would not work over a pipe at all. A numbered menu answers the same question, stays scriptable, and resolves a typed answer by number, by exact text, or — with a `Suggestor` — by fuzzy match.

## Showing progress

`Spinner` (indeterminate) and `Progress` (determinate) each redraw **one line in place**.

{{< alert type="info" title="NOTE:" >}}
Both stay **silent on a non-terminal writer**. A `\r` redraw written into a log file is not a cosmetic issue — it is corruption — so this is the one place rotini decides something for you by default. `WithAnimation(bool)` overrides it in either direction.
{{< /alert >}}

To write **past** a live indicator, use `Print`. Writing to the same stream directly smears into the frame — the indicator owns that line and never cleared it:

{{< code title="printing while a spinner runs" language="golang" open="true" collapsible="false" copy="true" >}}
spinner.Print(func() { fmt.Fprintln(rtx.Stdout, "done:", f) })
{{< /code >}}

{{< code title="spinner / progress" language="golang" open="true" collapsible="false" copy="true" >}}
spinner := rotini.NewSpinner(rtx.Stdout).WithMessage("fetching").Start(ctx)
defer spinner.Stop()

bar := rotini.NewProgress(rtx.Stdout, int64(len(files))).WithMessage("uploading")
for _, f := range files {
	upload(f)
	bar.Add(1)
}
bar.Done()
{{< /code >}}

## Shelling out

`Subprocess` wraps `os/exec` with environment, working-directory and timeout control. A non-zero exit is a `*SubprocessError` that **quotes the child's stderr** rather than the useless "exit status 1".

{{< code title="subprocess" language="golang" open="true" collapsible="false" copy="true" >}}
out, err := rotini.NewSubprocess("git", "rev-parse", "HEAD").
	WithDir(repo).
	WithTimeout(5 * time.Second).
	Output(ctx)

// Streamed output is an ITERATOR, so you can break out — which kills the child —
// and the run's error arrives in the loop rather than in a callback.
for line, err := range rotini.NewSubprocess("go", "test", "./...").Lines(ctx) {
	if err != nil {
		return err
	}
	if line.Stream == rotini.StreamStderr {
		fmt.Fprintln(rtx.Stderr, line.Text)
	}
}
{{< /code >}}

## Program shapes

A rotini binary is not always a one-shot command. These run the **same program** in a different shape, and all of them rest on `Program.Run` being re-entrant: each dispatch gets a fresh `Context`, so nothing leaks between invocations, while services bound once up front reach all of them.

### REPL

Each typed line is tokenized like a shell command line and dispatched against the **same command tree** the binary uses, so every command, flag and handler behaves identically. A failing command never ends the session.

State that should survive a command is bound on the **Program** with `Provide`, so every line sees it; state that should not goes on the **Context**, which is fresh each line.

{{< code title="repl" language="golang" open="true" collapsible="false" copy="true" >}}
rotini.NewREPL(Program).WithPrompt("todo> ").Run(ctx)
{{< /code >}}

The program's funnel already reports a failing command, so the REPL adds nothing of its own. `WithErrorEcho(true)` turns on a second report for a program whose funnel is deliberately silent.

### Service and Scheduler

`Service` runs long-lived workers until the context ends or one fails, with shutdown hooks that run in reverse order **in every case** — clean stop, failure, and cancellation alike. The budget covers both halves of teardown: workers that will not stop and hooks that overrun both surface as `ErrShutdownTimeout`, so a supervisor can tell "finished" from "exited with work possibly unflushed". Because the runtime already cancels the run context on SIGINT/SIGTERM, a handler that builds a Service on its own `ctx` gets signal-driven graceful shutdown for free.

`Scheduler` is `Service` with timers, so it inherits exactly those semantics.

{{< code title="service / scheduler" language="golang" open="true" collapsible="false" copy="true" >}}
err := rotini.NewService().
	Go("http", serveHTTP).
	Go("reconciler", reconcile).
	WithShutdown(closeDB).
	WithShutdownTimeout(30 * time.Second).
	Run(ctx)

err = rotini.NewScheduler().
	Every("refresh", time.Minute, refresh).
	WithJitter(0.1). // so a fleet does not stampede in lockstep
	Run(ctx)
{{< /code >}}

### StdioServer

JSON-RPC 2.0 over stdin/stdout, in newline-delimited or `Content-Length` framing — how LSP language servers and MCP servers speak. It is the shape your CLI takes when a tool drives it instead of a human.

{{< code title="stdio server" language="golang" open="true" collapsible="false" copy="true" >}}
rotini.NewStdioServer(rtx.Stdin, rtx.Stdout).
	WithFraming(rotini.FramingContentLength).
	Handle("tools/list", listTools).
	Notify("notifications/initialized", ignore).
	Run(ctx)
{{< /code >}}

Requests are served one at a time, in arrival order: a stdio peer shares one pipe, so concurrent handlers would interleave their writes. A handler with slow work hands it to a `Service` and answers immediately.

### Wizard

A guided multi-step flow with branching (`When`) and back navigation (`ErrWizardBack`). It owns no streams — a step does its own asking — which keeps the flow pure orchestration, testable with plain functions.

{{< code title="wizard" language="golang" open="true" collapsible="false" copy="true" >}}
answers, err := rotini.NewWizard().
	Step("name", askName).
	Add(rotini.WizardStep{
		Key:  "region",
		When: func(a map[string]string) bool { return a["deploy"] == "yes" },
		Ask:  askRegion,
	}).
	Run(ctx)
{{< /code >}}

Going back over a branch that was skipped skips it again — the flow does not resurface a question it already decided was irrelevant.

## What rotini does not ship

| Need | Use |
|---|---|
| watch files or directories | `fs.NewWatcher` — debounce, recursion, polling ([go-rotini/fs](https://github.com/go-rotini/fs)) |
| stop two copies running at once | `fs.PIDLock` — advisory lock with stale-lock recovery |
| cache in a long-running program | [go-rotini/memcache](https://github.com/go-rotini/memcache) — bounded, generic, thread-safe |

rotini does not wrap these. A facade would give each API two names, put its documentation in the wrong package, and pull another module's surface inside rotini's frozen compatibility promise — all for zero added capability.

The "one import" promise is about not shopping the ecosystem and reconciling four libraries' idioms. These are the same author, the same release cadence, the same house style: importing them directly is not the problem that promise solves.
