---
title: "rotini"
---

<div class="rotini_text">rotini</div>

<p class="hero_tagline">Define your CLI declaratively,<br>write its behavior imperatively.</p>

<p class="hero_beats">Validate. Generate. Ship.</p>

<p class="hero_sub">Describe your CLI in one spec file and rotini turns it into typed, validated Go — so the only code you write is what each command does.</p>

---

## Quick start

Requires Go 1.27 or later.

### 1. Initialize

{{< code title="terminal" language="bash" open="true" collapsible="false" copy="true" >}}
mkdir helloworld && cd helloworld
go mod init github.com/me/helloworld

go get -tool github.com/go-rotini/rotini/cmd/rotini@latest
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

`go generate ./...` creates `internal/cmd/helloworld/helloworld_hello.go`. Replace its `TODO` with the command's work:

{{< code title="internal/cmd/helloworld/helloworld_hello.go" language="go" open="true" collapsible="false" copy="true" >}}
func (*helloworldHelloHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	if argv, err := rotini.ParseArgv[HelloworldHelloInputs](rtx); err == nil {
		if argv.Values.HelloworldHello.Flags.Help {
			fmt.Fprintln(rtx.Stdout, rtx.Help())
			rtx.HaltWithCode(0)
			return
		}
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
