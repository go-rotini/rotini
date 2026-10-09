---
title: "recipes"
---

# recipes

Short, working answers to common tasks. Each assumes a program set up as in the
[guide](/docs), and the code in each is built and run by Rotini's own tests.

## Working with generated code

### Removing a command

Delete the command from the spec and run `go generate` twice. The first run disables its handler
file, adding `//go:build ignore` so the program still builds while you move anything you want to
keep. The second run deletes the file. If the command comes back before then, the file returns to
the build as it was, and once deleted, a committed file is still in Git's history.

### A dependency opened only when needed

A dependency that is costly to set up, such as a database or an API client, shouldn't slow down
`--help` or the commands that never use it. Register a function that opens it on first use:
`sync.OnceValues` runs the opener once and hands every later caller the same result.

{{< code title="internal/cmd/lazydemo/store.go" language="golang" open="true" collapsible="false" copy="true" >}}
package lazydemo

import (
	"os"
	"strings"
	"sync"

	"github.com/go-rotini/rotini"
)

// Store is costly to open, so only the commands that use it open it.
type Store struct{ values map[string]string }

// storeDep holds a function that opens the store on its first call and returns the same
// store, or the same error, on every later call.
var storeDep = rotini.NewDependency[func() (*Store, error)]("lazydemo.store")

// registerStore makes the store available without opening it. A test that registered its own
// opener first keeps it.
func registerStore(rtx *rotini.Context) {
	rtx.SetDependencyIfAbsent(storeDep, sync.OnceValues(openStore))
}

func openStore() (*Store, error) {
	data, err := os.ReadFile("store.txt")
	if err != nil {
		return nil, err
	}
	s := &Store{values: map[string]string{}}
	for line := range strings.Lines(string(data)) {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			s.values[k] = v
		}
	}
	return s, nil
}
{{< /code >}}

Call `registerStore(rtx)` first in the root handler's `CascadingPreRun`, which runs before every
command. A command that needs the store calls the function; one that doesn't never opens it:

{{< code title="internal/cmd/lazydemo/lazydemo_get.go" language="golang" open="true" collapsible="false" copy="true" >}}
package lazydemo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*lazydemoGetHandler)(nil)

type lazydemoGetHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*lazydemoGetHandler) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[LazydemoGetInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	store, err := rtx.MustGetDependency(storeDep)()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	fmt.Fprintln(rtx.Stdout, store.values[inputs.LazydemoGet.Arguments.Key])
}
{{< /code >}}

A test registers a double under `storeDep` with `Program.WithDependency`, and
`SetDependencyIfAbsent` leaves it in place.

## Shipping

### All man pages in one document

Some packages want a single man page rather than one per command. The man feature's `ManPages()`
returns every visible command's page, root first, and `man` reads several pages in one stream
as one document. Declare a `man` command and print them all:

{{< code title="internal/cmd/mandemo/mandemo_man.go" language="golang" open="true" collapsible="false" copy="true" >}}
package mandemo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*mandemoManHandler)(nil)

type mandemoManHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*mandemoManHandler) Run(ctx context.Context, rtx *rotini.Context) {
	for _, page := range ManPages() {
		if _, err := fmt.Fprint(rtx.Stdout, page.Content); err != nil {
			rtx.HaltWith(err)
			return
		}
	}
}
{{< /code >}}

{{< code title="terminal" language="sh" open="true" collapsible="false" copy="true" >}}
mandemo man > mandemo.1
man ./mandemo.1
{{< /code >}}

## Observability

### Tracing with OpenTelemetry

One span per run, named after the invoked command, with every span a command starts nested
under it. The root handler's `CascadingPreRun`, which runs first in every run, starts the span
and passes its context on with [`rtx.SetContext`](/docs#passing-a-context-on), so `Run` and
every later hook receive it:

{{< code title="internal/cmd/tracedemo/tracedemo.go" language="golang" open="true" collapsible="false" copy="true" >}}
package tracedemo

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-rotini/rotini"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var _ rotini.Handler = (*tracedemoHandler)(nil)

var tracer = otel.Tracer("example.com/tracedemo")

type tracedemoHandler struct {
	rotini.NoPreRun
	rotini.NoPostRun

	// span is the run's span. CascadingPostRun receives the context this handler's
	// CascadingPreRun received, not the one it set, so the span is kept here to end it.
	span trace.Span
}

// CascadingPreRun starts the run's span, named after the invoked command, and hands its
// context to every later hook.
func (h *tracedemoHandler) CascadingPreRun(ctx context.Context, rtx *rotini.Context) {
	var names []string
	for _, c := range rtx.CommandChain() {
		names = append(names, c.Name)
	}
	ctx, h.span = tracer.Start(ctx, strings.Join(names, " "))
	rtx.SetContext(ctx)
}

func (h *tracedemoHandler) CascadingPostRun(ctx context.Context, rtx *rotini.Context) {
	if rtx.Failed() {
		h.span.SetStatus(codes.Error, "the command failed")
	}
	h.span.End()
}

func (*tracedemoHandler) Run(ctx context.Context, rtx *rotini.Context) {
	fmt.Fprintln(rtx.Stderr, rtx.Help())
	rtx.HaltWithCode(1)
}
{{< /code >}}

The root's `CascadingPostRun` receives the context its `CascadingPreRun` received, not the one
it set, so the span is kept in a handler field to end there. A command starts its own spans
from the context it is given:

{{< code title="internal/cmd/tracedemo/tracedemo_work.go" language="golang" open="true" collapsible="false" copy="true" >}}
package tracedemo

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*tracedemoWorkHandler)(nil)

type tracedemoWorkHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*tracedemoWorkHandler) Run(ctx context.Context, rtx *rotini.Context) {
	_, span := tracer.Start(ctx, "load")
	defer span.End()
	fmt.Fprintln(rtx.Stdout, "worked")
}
{{< /code >}}

Set up the tracer provider and its exporter in `main.go` as for any Go program. `Execute` exits
the process, so call `Run` instead, shut the provider down to send the last spans, then exit
with the code `Run` returned.

## Testing

### Debugging completion

The completion scripts ask the program itself what to offer, through a hidden `__complete`
command that takes the words typed so far, the last one being the word under the cursor. Run it
directly to see exactly what a shell receives, with no shell in the way. `''` stands for an
empty word, as when you press TAB after a space:

{{< code title="$ taskr __complete list --status ''" language="text" open="true" collapsible="false" copy="false" >}}
all
done
open
{{< /code >}}

Each line is a candidate; a candidate with a description has it after a tab. Lines starting with
`:rotini:` are instructions for the script rather than candidates: `:rotini:message` is a line
of guidance to show (see [completion messages](/docs#completion-messages)), and
`:rotini:file` asks the shell to complete file names, here only those ending in `.txt`:

{{< code title="$ taskr __complete add ''" language="text" open="true" collapsible="false" copy="false" >}}
:rotini:message a short title, in quotes
{{< /code >}}

{{< code title="$ taskr __complete list --out ''" language="text" open="true" collapsible="false" copy="false" >}}
:rotini:message --out string: write the list to a file
:rotini:file txt
{{< /code >}}

When this output is right but the shell shows something else, the problem is in the shell's
setup: reload the script (`source <(taskr completion bash)`) or start a new shell.

## Input and output

### Writing to a file and to stdout at once

A command can show its output and keep a copy, as `tee` does. Open the file with
`rotini.CreateOutput`, and write once through `io.MultiWriter` with `rtx.WriteOutputTo`. The
file is written to a temporary name and renamed into place by `Close`, so a run that fails
part-way leaves any earlier file as it was. The flag is `type: outputfile`, and `--force`
carries `role: force`:

{{< code title="internal/cmd/teedemo/teedemo_export.go" language="golang" open="true" collapsible="false" copy="true" >}}
package teedemo

import (
	"context"
	"io"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*teedemoExportHandler)(nil)

type teedemoExportHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*teedemoExportHandler) Run(ctx context.Context, rtx *rotini.Context) {
	in, err := rtx.Inputs[TeedemoExportInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	flags := in.TeedemoExport.Flags
	if err := export(rtx, flags.Output, flags.Force); err != nil {
		rtx.HaltWith(err)
	}
}

func export(rtx *rotini.Context, path string, force bool) error {
	out, err := rotini.CreateOutput(rtx, path, rotini.Overwrite(force))
	if err != nil {
		return err
	}
	defer out.Abort() // discards the file unless Close below commits it
	report := TeedemoExportOutput{Count: 3}
	if err := rtx.WriteOutputTo(io.MultiWriter(rtx.Stdout, out), report, "json", nil); err != nil {
		return err
	}
	return out.Close()
}
{{< /code >}}

{{< code title="terminal" language="sh" open="true" collapsible="false" copy="true" >}}
teedemo export -o report.json
teedemo export -o report.json --force
{{< /code >}}

`io.MultiWriter` stops at the first writer that fails, so a closed stdout also ends the file,
and `Close` is never reached. With `-o -` the output goes to stdout twice.
