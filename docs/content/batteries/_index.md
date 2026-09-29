---
title: "batteries"
---

# Batteries

Parsing is the first 10% of a CLI. rotini carries the rest of the *dispatch* problem in the same import — and **nothing is wired unless you wire it**. Importing rotini starts no goroutine, touches no terminal, and changes no behavior; each battery does something only because a handler constructed it and called it.

{{< alert type="info" title="NOTE:" >}}
The shelf is deliberately **short**. rotini ships no styler, no table, no spinner, no prompt and no pager — see [what rotini does not ship](#what-rotini-does-not-ship). What is here is the part underneath those decisions: process work that is subtly wrong in most hand-rolled versions, and platform questions the standard library will not answer.
{{< /alert >}}

## Terminal questions

Two questions come before any decision about how to write to a stream, and the standard library answers neither. rotini never asks them for you — nothing in the runtime calls these.

{{< code title="detection" language="golang" open="true" collapsible="false" copy="true" >}}
tty := rotini.IsTerminal(os.Stdout)  // a character device, not a pipe or a file
plain := rotini.EnvNoColor()         // NO_COLOR, with the CLICOLOR_FORCE override

cols, rows, ok := rotini.TerminalSize(os.Stdout)
if !ok {
	cols, rows = 80, 24 // a pipe, or a platform that cannot say
}
{{< /code >}}

`TerminalSize` honors `COLUMNS`/`LINES` first — the conventional override, and how a test pins a width. Measure the stream you are **about to write to**: a program piping stdout to a file while a human watches stderr has two different answers, and only you know which one matters.

Hand the answers to whatever draws your output.

### Reading a secret

The one piece of interactive input rotini keeps, because it is the one you cannot safely fake: it needs a termios ioctl to clear the `ECHO` bit, and it must put the bit back on **every** path. The failure mode is not a wrong value — it is a shell left with echo off, which survives your process and confuses the user's next command.

{{< code title="read a secret" language="golang" open="true" collapsible="false" copy="true" >}}
fmt.Fprint(rtx.Stdout, "token: ")
secret, err := rotini.ReadSecret(os.Stdin)
fmt.Fprintln(rtx.Stdout) // the user's Enter was not echoed either
{{< /code >}}

Off a terminal — a pipe, a test, a CI runner — there is no echo to disable and the line is read normally, which keeps a secret-reading command testable. Input that ends without an answer is `ErrNotInteractive`, **never a hang**: that is what makes an interactive command safe to run in CI.

### Stripping escapes

`Strip` removes every ANSI escape sequence, SGR styling and OSC alike. It is what makes a styled string safe to put somewhere that would print the escapes literally — which is exactly what codegen does to your man pages, markdown pages and completion descriptions.

{{< code title="strip" language="golang" open="true" collapsible="false" copy="true" >}}
plain := rotini.Strip(styled)
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

### Service

`Service` runs long-lived workers until the context ends or one fails. It is **not** a supervisor — `errgroup` is a supervisor. It is the part of a daemon that is not about doing the work, but about **ending**.

{{< code title="service" language="golang" open="true" collapsible="false" copy="true" >}}
err := rotini.NewService().
	Go("http", serveHTTP).
	Go("reconciler", reconcile).
	WithShutdown(closeDB).
	WithShutdownTimeout(30 * time.Second).
	Run(ctx)
{{< /code >}}

Three behaviours, all about the shutdown, and none of them free anywhere else:

- **Teardown runs on a context that is not already dead.** Hooks get a context derived with `context.WithoutCancel`, under a fresh budget. After a Ctrl-C the run context is *already cancelled*, so naive cleanup — `db.Close(ctx)`, `flush(ctx)` — fails instantly and silently, taking the buffer you were trying to flush with it.
- **A context *ended* from outside is a graceful stop, so `Run` returns nil.** `errgroup.Wait` returns `context.Canceled`, which a CLI would turn into a non-zero exit for a clean SIGTERM. "Ended" covers a deadline as well as a cancel, so a worker writing the idiomatic `<-ctx.Done(); return ctx.Err()` does not fail a bounded run merely because the bound was a timeout.
- **One budget across both halves, surfaced as `ErrShutdownTimeout`.** Workers that will not stop and hooks that overrun both produce the same typed sentinel, because the question a supervisor asks is the same either way: did teardown complete, or is this process exiting with work possibly unflushed? It exists to become an **exit code** — and when workers are what overran, it **names them**: `rotini: shutdown timed out: worker "indexer" did not stop`.

Hooks run in **reverse** registration order, like deferred calls, so a resource is released before whatever it depends on. They run in every case — clean stop, worker failure, cancellation alike.

**A panic does not take the process with it.** A worker that panics is recovered on its own goroutine, becomes the service's failure as a `*PanicError` carrying the stack, and teardown still runs. `Program.WithPanicRecover` cannot do this for you: it guards the dispatch goroutine, and a goroutine's panic is unrecoverable from anywhere but itself — so the thing that spawned the goroutine has to be the thing that guards it. A panicking shutdown hook is contained the same way and does **not** cost the hooks after it, because teardown is the one phase where best-effort beats fail-fast.

{{< alert type="info" title="A PANICKING WORKER IS WHEN THE JOURNAL MOST NEEDS FLUSHING:" >}}
Without containment, one worker panic ends the binary with Go's panic dump on stderr and every hook unrun — no flush, no close, no unlock, no funnel, no exit code. That is the single worst way for a daemon to end, and it is the reason `Service` is in rotini at all.
{{< /alert >}}

Because the runtime already cancels the run context on SIGINT/SIGTERM, a handler that builds a Service on its own `ctx` gets signal-driven graceful shutdown for free.

{{< alert type="info" title="THERE IS NO SCHEDULER:" >}}
`rotini.Scheduler` was removed — it was a *consumer* of this seam rather than a peer of it, and everything it added was a `time.Timer` loop and a jitter multiplication. Run your timers as workers, or put a real scheduler inside one: `svc.Go("cron", func(ctx) error { c.Start(); <-ctx.Done(); <-c.Stop().Done(); return nil })`. [robfig/cron](https://github.com/robfig/cron) and [go-co-op/gocron](https://github.com/go-co-op/gocron) give you cron expressions, timezones and skip-if-still-running, none of which rotini's version had. `example-daemon` shows both halves.
{{< /alert >}}

## What rotini does not ship

### Drawing

Styling, tables, spinners, progress bars, prompts, forms, paging and editor round-trips are all how a program **draws**. That is a design decision belonging to your program and to libraries built for it — not to a CLI framework.

A framework that shipped its own would either be worse than they are or grow into a second product; either way you would end up with two vocabularies for the same screen. rotini's job is turning a spec into a parsed, bound, dispatched invocation and handing your handler a `Context` that knows what the user asked for. What the handler prints, and how, is yours.

| Need | Use |
|---|---|
| styling, layout, borders, adaptive light/dark | [lipgloss](https://github.com/charmbracelet/lipgloss) |
| forms, prompts, selects, confirms | [huh](https://github.com/charmbracelet/huh), [survey](https://github.com/AlecAivazis/survey) |
| spinners, progress bars, live views | [bubbles](https://github.com/charmbracelet/bubbles), [progressbar](https://github.com/schollz/progressbar) |
| a full TUI | [bubbletea](https://github.com/charmbracelet/bubbletea) |
| wrapping and truncating styled text | [reflow](https://github.com/muesli/reflow) |
| aligned columns | `text/tabwriter`, in the standard library |
| paging, `$EDITOR` round-trips | a dozen lines of `os/exec` — see [example-txt](https://github.com/go-rotini/example-txt) and [example-flow](https://github.com/go-rotini/example-flow) |

`example-flow` is the worked version of this boundary: the whole asking layer, written out, is about eighty lines.

### Elsewhere in the family

| Need | Use |
|---|---|
| watch files or directories | `fs.NewWatcher` — debounce, recursion, polling ([go-rotini/fs](https://github.com/go-rotini/fs)) |
| stop two copies running at once | `fs.PIDLock` — advisory lock with stale-lock recovery |
| cache in a long-running program | [go-rotini/memcache](https://github.com/go-rotini/memcache) — bounded, generic, thread-safe |

rotini does not wrap these. A facade would give each API two names, put its documentation in the wrong package, and pull another module's surface inside rotini's frozen compatibility promise — all for zero added capability.

The "one import" promise is about not shopping the ecosystem for the parts of a CLI that are genuinely rotini's job. These are the same author, the same release cadence, the same house style: importing them directly is not the problem that promise solves.
