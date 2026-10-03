---
title: "rotini"
---

<div class="rotini_text">rotini</div>

<p class="hero_tagline">Define your CLI declaratively,<br>write its behavior imperatively.</p>

<p class="hero_beats">Validate. Generate. Ship.</p>

<p class="hero_sub">Rotini is a spec-driven CLI package for Go, with a codegen tool and a runtime library. You declare your commands, flags and arguments in a spec file (YAML, JSON, JSONC or TOML). Rotini validates it and generates the typed Go, the command tree and a handler stub per command. You write each command's handler, and the runtime parses and validates input before calling it. The loop is: edit the spec, generate, provide/update handler implementations, build.</p>

---

## Quick start

Requires Go 1.27 or later.

### 1. Initialize

{{< code title="terminal" language="bash" open="true" collapsible="false" copy="true" >}}
mkdir helloworld
cd helloworld
go mod init github.com/me/helloworld

go get -tool github.com/go-rotini/rotini/cmd/rotini@latest
go get github.com/go-rotini/rotini@latest
go tool rotini init helloworld
{{< /code >}}

{{< code title="what init writes" language="text" open="true" collapsible="false" copy="false" >}}
cmd/helloworld/
  .rotini.spec.yaml        the spec — what the CLI accepts
  .rotini.conf.yaml        the conf — where generated code goes
  .rotini-schema.*.json    schemas for editor completion
  main.go                  the entrypoint
internal/cmd/helloworld/
  zz_rotini.go             generated on every `go generate` — don't edit
  helloworld*.go           one handler file per command — yours to edit
{{< /code >}}

### 2. Add a command to the spec

{{< code title="cmd/helloworld/.rotini.spec.yaml — under commands:" language="yaml" open="true" collapsible="false" copy="true" >}}
    - name: hello
      summary: say hello
      arguments:
        - name: name
          summary: who to greet
          schema: { type: string, default: world }
      flags:
        - name: shout
          summary: greet in capitals
          identifiers: [-s, --shout]
          schema: { type: bool }
        - name: help
          summary: print help
          identifiers: [-h, --help]
          schema: { type: bool }
{{< /code >}}

### 3. Generate, and fill in the handler

`go generate ./...` creates `internal/cmd/helloworld/helloworld_hello.go`. Replace the line that prints `inputs` with the command's work, and add `"strings"` to its imports:

{{< code title="internal/cmd/helloworld/helloworld_hello.go" language="go" open="true" collapsible="false" copy="true" >}}
func (*helloworldHelloHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	if argv, err := rotini.ParseArgv[HelloworldHelloInputs](rtx); err == nil && argv.Values.HelloworldHello.Flags.Help {
		fmt.Fprintln(rtx.Stdout, rtx.Help())
		rtx.HaltWithCode(0)
		return
	}

	inputs, err := rotini.Collect[HelloworldHelloInputs](rtx)
	if err != nil {
		rtx.HaltWith(err)
		return
	}

	greeting := "hello, " + inputs.HelloworldHello.Arguments.Name
	if inputs.HelloworldHello.Flags.Shout {
		greeting = strings.ToUpper(greeting)
	}
	fmt.Fprintln(rtx.Stdout, greeting)
}
{{< /code >}}

### 4. Build and run

{{< code title="terminal" language="bash" open="true" collapsible="false" copy="true" >}}
go generate ./...
go build ./cmd/...

./helloworld hello --shout rotini   # HELLO, ROTINI
./helloworld --help
{{< /code >}}

Change the spec, `go generate ./...`, fill in any new handler, build — that's the whole loop.

---

## Next

- [README.md](/docs) — using rotini in depth: inputs, handlers, errors, testing, composition
- [.rotini.spec.yaml](/specification) — every spec key
- [.rotini.conf.yaml](/configuration) — every conf key
- [api.go](/api) — what you call from `main.go` and your handlers
- [cli](/cli) — the `rotini` command
