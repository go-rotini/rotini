---
title: "examples"
---

# Examples

Twelve programs, written the way you would write one — spec first, then conf, then `rotini generate`, then handler bodies — each built around a different part of the framework. They exist to answer a question the test suite cannot: what does a *real* rotini program look like once it stops being a tutorial.

Use them as shapes to copy. Each entry below says what the CLI is, what it looks like from the command line, and which part of rotini it leans on.

## Everyday shapes

### `taskr` — a task list

Lives in [`example-taskr`](https://github.com/go-rotini/example-taskr).

The one a newcomer would write, and the baseline everything else is measured against. Nothing exotic: a flat command tree, doc fields filled in, typed inputs, and all four derived outputs turned on.

{{< code title="taskr" language="text" open="true" collapsible="false" copy="false" >}}
taskr add       add a task
      list      list tasks
      done      mark tasks done
      remove    remove a task
      purge     delete every archived task
      compact   rewrite the store
{{< /code >}}

Leans on: the full command-key vocabulary (`usage`, `header`, `footer`, `examples`, `group`, `hidden`, `deprecated`, `aliases`, `see_also`), the `output:` key rendered by hand into text, JSON, YAML, TOML and columns, and help + man + markdown + completion together.

### `txt` — a text filter

Lives in [`example-txt`](https://github.com/go-rotini/example-txt).

The `grep`/`jq` family: a program whose primary input is a stream, not argv. `upper`, `count`, `filter`, `meta` and `page` each declare their stdin in the spec — which is what lets rotini validate a JSON payload *before* the handler runs.

{{< code title="txt" language="text" open="true" collapsible="false" copy="false" >}}
txt upper     upper-case the whole stream
    count     count lines, words and bytes
    filter    keep lines matching a pattern
    meta      read a typed JSON payload
    hash      hash a value
    scan      report on the text files in a directory
    page      send the stream through $PAGER
    exec      run a command and tag its output by stream
{{< /code >}}

Leans on: `stdin` in all three formats (`text`, `lines`, and `json` into a `$ref`-typed struct), the value sentinels — one flag accepting a literal, `@path` **and** `-` — the path types `existingfile`/`existingdir`, and completion hints including `kind: none`.

### `cfgctl` — a deploy tool

Lives in [`example-cfgctl`](https://github.com/go-rotini/example-cfgctl).

The configuration story, end to end. Four configuration sources at once, each found a different way, plus every environment idiom and a `doctor` command that prints where each value actually came from.

{{< code title="cfgctl" language="text" open="true" collapsible="false" copy="false" >}}
cfgctl deploy    deploy a service
       doctor    show where every value came from
       show      resolve one channel at a time
{{< /code >}}

Leans on: `config_files` with a fixed `path`, `discover: {strategy: walk-up}` and `discover: {strategy: xdg}`; per-key precedence across files in declared order; `config_source` (the two-phase parse, where the path comes from a flag); nested environment families; and `CollectP`'s provenance `Report` — including the part where a `secret` input reports `[redacted]` instead of its value.

## Structure

### `mig` — a schema migrator

Lives in [`example-lifecycle`](https://github.com/go-rotini/example-lifecycle).

Hooks, failures and exit codes. A nested tree deep enough that cascading matters, a `boom` command that panics on purpose, and `--trace` printing every lifecycle event as it happens — so the order is something you watch rather than something you read about.

{{< code title="mig" language="text" open="true" collapsible="false" copy="false" >}}
mig db migrate up      apply pending migrations
                down   revert applied migrations
      status           show which migrations have been applied
   boom                panic on purpose, to show what a panic looks like
{{< /code >}}

Every code it can exit with is declared, which is what puts them in the generated help:

{{< code title="cmd/mig/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
  exit_status:
    - code: 0
      summary: success
    - code: 1
      summary: a migration failed
    - code: 2
      summary: the command line was wrong
    - code: 70
      summary: an internal error — a bug in mig
    - code: 75
      summary: the database was unavailable; retrying may help
{{< /code >}}

Leans on: the five lifecycle hooks and their reverse unwind, a custom funnel, the error taxonomy, flag groups and dependencies.

### `mono` — a monorepo CLI

Lives in [`example-suite`](https://github.com/go-rotini/example-suite).

Four ways to pull a command in from somewhere else, in one spec. Read this one when you want to know what `$ref` can actually do.

{{< code title="cmd/mono/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
  commands:
    - $ref: ../build/.rotini.spec.yaml
      summary: compile things                    # overrides the child's own summary
    - $ref: ../release/.rotini.spec.yaml         # which $refs a child of its own
    - $ref: mod://example.com/specsuite@v1.2.3/scan/.rotini.spec.yaml
    - name: lint                                 # declared here, handled by a hand-written package
      summary: run the linters
      handler:
        import: lintcmd github.com/go-rotini/example-suite/handlers/lint
{{< /code >}}

Leans on: local `$ref` with an overlay, transitive `$ref`, `mod://` from the module cache, commands merged additively beside a `$ref`, a command whose handler lives in a package rotini never generated (`handler: import:`), and the `models` package target that keeps that package out of an import cycle. The `mod://` child comes from a module proxy checked into the repository — `modproxy/`, which the Makefile points `GOPROXY` at and `republish.sh` rebuilds — so it resolves like a published module, go.sum verification included, without a registry.

### `acme` — parent and child CLIs in one codebase

Lives in [`example-multi`](https://github.com/go-rotini/example-multi).

Where `mono` composes into one binary, this one composes into **four**. `acme-db`, `acme-cache` and `acme-queue` each ship on their own; `acme` grafts all three in. One `health` implementation serves every one of them.

{{< code title="four binaries, one repository" language="text" open="true" collapsible="false" copy="false" >}}
cmd/db/.rotini.spec.yaml      ──┐            acme-db      ships on its own
cmd/cache/.rotini.spec.yaml   ──┼─ $ref ──▶  acme-cache   ships on its own
cmd/queue/.rotini.spec.yaml   ──┘            acme-queue   ships on its own
cmd/acme/.rotini.spec.yaml                   acme         composes all three

handlers/health/              one implementation, referenced by all four
{{< /code >}}

Its test suite is the interesting part: every case asserts that the composed result is **byte-identical** to the standalone one, because that equality is the feature. See [sharing handlers between CLIs](/guides#share-handlers-between-clis).

### `musak` — a music catalogue over a real API

Lives in [`example-musak`](https://github.com/go-rotini/example-musak).

Where `acme` composes four binaries, this one does it with a **network** in the middle — the place composition usually stops being free. Three child CLIs over the public iTunes Search API, one HTTP client, one shared `search` command, and no key or account to make it run.

{{< code title="four binaries, one client" language="text" open="true" collapsible="false" copy="false" >}}
musak songs   search · show · preview
      albums  search · show · tracks
      artists search · show · albums · top
      about                                  the umbrella's own command

handlers/search/   ONE search command, run by all four
itunes/            the HTTP client
{{< /code >}}

The three children differ in exactly one value — the API's `entity` — so they share one handler. Each binds its own identity in its own root hook, which is why the sharing works under the umbrella as well as standalone:

{{< code title="internal/cmd/albums/musak_albums.go" language="golang" open="true" collapsible="false" copy="true" >}}
func (*musakAlbumsHandlers) CascadingPreRun(ctx context.Context, rtx *rotini.Context) {
	// BindTo, not Provide: per-invocation, so `musak albums search` followed by
	// `musak songs search` in one process cannot inherit the wrong entity.
	musak.EntityKey.BindTo(rtx, itunes.EntityAlbum)
}
{{< /code >}}

Its test suite never touches the network: an `httptest.Server` speaks the API's shape, and the client is pointed at it through the same `MUSAK_ENDPOINT` env input a user would use.

### `plug` — a CLI whose commands are other programs

Lives in [`example-plug`](https://github.com/go-rotini/example-plug).

Sub-commands that are separate binaries, `git`-style — both the declared kind and the discovered kind, so the difference between "broken install" and "typo" is visible from the outside.

{{< code title="cmd/plug/.rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
  plugin_path: ./plugins/bin

  remote_discovery:            # any plug-* executable becomes a sub-command
    prefix: plug-

  remote_commands:             # declared: listed in help even when not installed
    - name: sync
      aliases: [sy]
    - name: report
      timeout: 5s
    - name: slow
      timeout: 300ms
    - name: absent             # deliberately not installed
    - name: vendored           # installed only in plugin_path
{{< /code >}}

A relative `plugin_path` is relative to the working directory; a leading `~` and `$VAR` references are expanded the way a shell would.

It also ships a `plugins` command — the doctor — that reports what is installed now, using `DiscoveredPlugins`, `DiscoveryDiagnostics` and `RemoteBinaryPath`.

## Interaction and long-running work

### `flow` — a project scaffolder

Lives in [`example-flow`](https://github.com/go-rotini/example-flow).

**Where rotini stops and your UI begins.** rotini ships no prompt, no select, no spinner and no styler; `flow` writes all of them out — the whole asking layer is one file of about 130 lines — and the same `flow new` still runs as a guided flow, as a flag-driven one-liner, and from a pipe.

{{< code title="flow" language="text" open="true" collapsible="false" copy="false" >}}
flow new      a guided flow: name → template → deploy? → [region] → confirm
     build    a progress line that goes quiet when nobody is watching
     theme    what styling resolves to, here, now
{{< /code >}}

Leans on: `ErrNotInteractive` as the answer to "no input at all", `IsTerminal`/`EnvNoColor`/`TerminalSize`, `Suggestor` for a typo'd answer, `Strip`, and a `negatable` + `nullable` flag that can override detection in **both** directions.

### `syncd` — a sync agent

Lives in [`example-daemon`](https://github.com/go-rotini/example-daemon).

One binary, four long-running shapes, all over the same command tree.

{{< code title="syncd" language="text" open="true" collapsible="false" copy="false" >}}
syncd serve      a Service   — concurrent workers with an ordered shutdown
      schedule   periodic work — syncd's own timers, supervised by a Service
      rpc        JSON-RPC over stdin/stdout — on sourcegraph/jsonrpc2, not rotini
      shell      a REPL      — the same commands, interactively, with readline
      status | queue add|list|drain      ordinary commands, shared by all four
{{< /code >}}

The REPL is the one to look at first: it dispatches against the same tree the binary uses, so `syncd queue add x` and `queue add x` typed at the prompt run the identical handler. State that must survive a line is bound on the `Program`; state that must not goes on the `Context`.

It is also where the REPL seams are worked through end to end: `chzyer/readline` plugged in via `WithLineReader` behind one `IsTerminal` check, TAB wired to `Complete` so it completes syncd's real commands, `^C` routed to `WithInterrupts` so it cancels the running command instead of the session, `\h`/`\l` handled by `WithIntercept`, and a prompt that tracks the queue.

`rpc` is the one that shows the boundary. rotini ships no JSON-RPC server, so the protocol is a third-party library's job — and `queue/drain` proves why: it is a notification that gets no reply and then **notifies the peer anyway**, which is server→client traffic no inbound-only server could ever express.

## Everything at once

### `rubectl` — a kubectl-shaped CLI

Lives in [`rubectl`](https://github.com/go-rotini/rubectl).

The large one. It controls a pretend cluster — a directory of YAML files — and is modelled on kubectl command for command, to find out what building a CLI of that size is actually like. Every command earns its place by exercising something the others do not, and every one is driven end to end in its tests against a private copy of `testdata/store`.

{{< code title="rubectl" language="text" open="true" collapsible="false" copy="false" >}}
rubectl get · describe · apply · create · delete · patch · label · annotate
        logs · exec · port-forward · rollout status|history|undo
        explain · api-resources · plugin list · completion · shell
        config view|get-contexts|current-context|use-context|set-context|delete-context
        hello                          a declared plugin (rubectl-hello)
        whoami                         a discovered plugin (rubectl-whoami)
{{< /code >}}

{{< code title="running it" language="text" open="true" collapsible="false" copy="false" >}}
make                                              # vet, test, build (plugins included)
bin/rubectl --kubeconfig testdata/config get pods
{{< /code >}}

Two specs make one binary: `cmd/rubectl/.rotini.spec.yaml` composes `cmd/config/.rotini.spec.yaml` with `$ref`, which is why `make generate` generates `config` first.

Leans on:

- **every input channel** — flags, positionals, `RUBECTL_*` environment, an exactly-named `variable: [RUBECONFIG, KUBECONFIG]`, walk-up and XDG `config_files`, and stdin (`apply -f -`)
- **structured flags** — `patch --patch` decoded into a `$ref` schema from a file, and `patch --set a.b.c=v`, a map with `dotted_keys`
- **deprecations** — a deprecated `--store` flag and a deprecated `api-versions` spelling, reported with `rotini.Deprecations`
- **`ReadSecret`** — `create secret generic --prompt` reads each value without echo at a terminal, one line per value from a pipe
- **`Subprocess`** — `exec` runs a real local command in place of the pod's, with the pod's name in its environment
- **`Service`** — `port-forward` serves until interrupted, with a shutdown hook bounded by `--shutdown-timeout` even though it ignores its context
- **plugins** — a declared remote and a discovered one, `plugin_path: ~/.rubectl/plugins` (the `~` is expanded), and `plugin list` built on `DiscoveredPlugins`
- **completion** — the generated scripts, tested against whichever of bash, fish, zsh and pwsh are installed
- **a REPL** — `rubectl shell`, with `:ns` to switch namespace and a prompt showing the current context

Its generated stub files show the naming rules at scale: `port-forward` becomes `rubectl_port_forward.go`, and `create secret generic` becomes `rubectl_create_secret_generic.go`.

## The negative rig

### `strict` — every rejection, judged

Lives in [`example-strict`](https://github.com/go-rotini/example-strict).

No binary and no handlers. A directory of documents rotini must **refuse**, and a recorded bar for what it says about each one.

{{< code title="running it" language="text" open="true" collapsible="false" copy="false" >}}
go test .            # check every rejection against the bar
go test . -update    # re-record the expected output — then read it
{{< /code >}}

The other eleven ask whether rotini works. This one asks whether rotini is any good to be *wrong* in front of, which is most of what a validating tool is for. A correct rejection with an unhelpful message counts as a failure here.

## Next

- [Guides](/guides) — the same material as recipes you can follow
- [Specification](/specification) — every key these specs use
- [Batteries](/batteries) — the shelf `flow`, `syncd` and `rubectl` draw from
