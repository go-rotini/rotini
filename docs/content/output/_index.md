---
title: "output"
---

# Structured output

A command's inputs are already a contract: the spec says what it accepts, and rotini parses,
checks and documents it. `output:` does the same for the **shape** of what a command writes.
rotini generates a Go type for it, documents it, and publishes it as JSON Schema for scripts and
other tools.

How the command writes that output, and in which format, stays the handler's own code. rotini
adds no format flag and wires none. There are a few optional helpers for writing and checking
output, and a handler is free to ignore them.

Everything here is opt-in. A command with no `output:` behaves exactly as before.

## Declare the shape

`output:` is a schema. It generates a typed `<Prefix>Output` Go type:

{{< code title=".rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
command:
  name: taskr
  schemas:
    TaskList:
      type: object
      description: Every task, oldest first.
      properties:
        tasks: { type: array, items: { $ref: "#/schemas/Task" } }
  commands:
    - name: list
      output: { $ref: "#/schemas/TaskList" }
{{< /code >}}

A command that writes a stream of items, one at a time, declares the shape of **one item**.

An exit status can declare a shape too, for an outcome that still writes data:

{{< code title=".rotini.spec.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
exit_status:
  - code: 3
    summary: some tasks failed; stdout lists the ones that succeeded
    output: { type: array, items: { $ref: "#/schemas/Task" } }
{{< /code >}}

## Write it

Choosing a format is up to you. Typically you declare a flag like any other, such as
`-o, --output` with an enum, and use its value. `rtx.WriteOutput` is an optional helper that
writes a value to stdout in the format you pass. It writes json (indented), yaml and toml
itself, and hands any other format to your renderer. Pass `nil` as the renderer if you only
ever use those three:

{{< code title="internal/cmd/taskr/taskr_list.go" language="go" open="true" collapsible="false" copy="true" >}}
func (*taskrListHandler) Run(ctx context.Context, rtx *rotini.Context) {
	in, err := rtx.Inputs[TaskrListInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	if err := rtx.WriteOutput(TaskrListOutput{Tasks: load()}, in.Taskr.Flags.Output, renderTable); err != nil {
		rtx.HaltWith(err)
	}
}

func renderTable(w io.Writer, format string, v TaskrListOutput) error {
	fmt.Fprintln(w, "ID  STATUS  TITLE")
	for _, t := range v.Tasks {
		fmt.Fprintf(w, "%-3d %-7s %s\n", t.ID, t.Status, t.Title)
	}
	return nil
}
{{< /code >}}

An empty format means json. `rtx.WriteOutputItem` writes one item of a stream as it is ready:
one compact JSON value per line, or one YAML document per item. toml can't be streamed.

Both return an internal error, and write nothing, when the value is not the command's
`<Prefix>Output` type or when a format they don't write has no renderer. Both are the program's
bugs, not the user's.

**Keep stdout for the output.** A script reading `taskr list -o json` breaks if anything else
lands on stdout, such as a progress line before the JSON document. Write progress, notes and
prompts to `rtx.Stderr`.

## Check it

`Program.WithOutputChecks()` makes every `WriteOutput` and `WriteOutputItem` call check its value
against the declared shape before writing. It is off by default. Turn it on in tests, or in a
debug build:

{{< code title="taskr_test.go" language="go" open="true" collapsible="false" copy="true" >}}
p := NewProgram(Handlers()).WithOutputChecks()
{{< /code >}}

A value that does not match is an internal error naming each field at fault:

```
taskr list: output does not match its contract: output.tasks[2].status: value must be one of "open", "done"
```

`rtx.CheckOutput(v)` makes the same check without writing anything.

`rotini.DecodeOutput[T]` reads captured stdout back in a test. It decodes json, yaml or toml
into the command's output type, checking it against the shape first. To read a stream written
with `WriteOutputItem`, use a slice of the type:

{{< code title="taskr_test.go" language="go" open="true" collapsible="false" copy="true" >}}
var stdout bytes.Buffer
p := NewProgram(Handlers()).WithStdout(&stdout)
p.Run([]string{"list", "-o", "yaml"})
list, err := rotini.DecodeOutput[TaskrListOutput](p, stdout.Bytes(), "yaml")
{{< /code >}}

## Errors scripts can read

`rotini.StructuredReporter` builds a reporter for programs whose output scripts read. You pass
it your own rule for when a run is structured. When the rule says yes, it writes each error,
warning, info and success to **stderr** as one JSON object per line, so stdout carries only the
output. When the rule says no, it reports exactly as the default reporter does.

The reporter runs after the command, and the run may have failed because parsing did, so the
rule reads the command line itself rather than validated inputs:

{{< code title="cmd/taskr/main.go" language="go" open="true" collapsible="false" copy="true" >}}
cmd.Program.WithReporter(rotini.StructuredReporter(func(rtx *rotini.Context) bool {
	return slices.Contains(rtx.Argv, "--json")
})).Execute()
{{< /code >}}

```
$ taskr list --json --bogus
{"error":{"category":"usage","command":"taskr list","exit_code":1,"flag":"--bogus","kind":"unknown-flag","message":"unknown flag \"--bogus\"","token":"--bogus"}}
```

Exit codes are decided exactly as the default reporter decides them. Each line's shape is
described by
[schema-error.json](https://github.com/go-rotini/rotini/blob/main/schema-error.json).

## What gets generated

- **An OUTPUT section** in help, man and markdown pages: the shape's description, its type, and
  its top-level fields with their types and descriptions. An exit status that writes output
  says so. Its help heading is `headings.output`.
- **One JSON Schema per output**, with `generate.schemas.output.dir` in the conf:
  `taskr-list.output.json` for a command, and `taskr.exit-3.output.json` for an exit status.
  Each is standard JSON Schema (draft-07), with the named schemas it uses included.
- **The contract document**, with `generate.contract.file`: one JSON file describing every
  visible command, including its arguments, flags, environment variables, configuration keys,
  stdin, output shape and exit statuses. Each command also has a `parameters` JSON Schema
  covering its arguments and flags, so it maps directly onto a tool definition for an AI
  agent. Its format is described by
  [schema-contract.json](https://github.com/go-rotini/rotini/blob/main/schema-contract.json).

{{< code title=".rotini.conf.yaml" language="yaml" open="true" collapsible="false" copy="true" >}}
generate:
  schemas:
    output:
      dir: schemas/output
  contract:
    file: cli-contract.json
{{< /code >}}
