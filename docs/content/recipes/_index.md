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
