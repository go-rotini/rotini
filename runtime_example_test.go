package rotini_test

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/go-rotini/rotini"
)

// A middleware sees every hook of every command in the chain, with the error it returns.
func ExampleProgram_WithMiddleware() {
	trace := func(h rotini.Hook, next rotini.HookFunc) rotini.HookFunc {
		return func(ctx context.Context, rtx *rotini.Context) error {
			err := next(ctx, rtx)
			fmt.Printf("%s %s: %v\n", h.Command.Name, h.Kind, err)
			return err
		}
	}
	rotini.NewProgramFunc(taskrDef(), taskrLookup).
		WithStderr(os.Stdout).
		WithMiddleware(trace).
		Run([]string{"add"})
	// Output:
	// taskr CascadingPreRun: <nil>
	// add CascadingPreRun: <nil>
	// add PreRun: <nil>
	// add Run: <nil>
	// add PostRun: <nil>
	// add CascadingPostRun: <nil>
	// taskr CascadingPostRun: <nil>
	// Warning: the cache is stale
	// Error: missing title
}

type skippedFile struct{ Path, Reason string }

func (s skippedFile) String() string { return s.Path + " (" + s.Reason + ")" }

// skipped is an author channel: declared once, recorded into by a hook and read back, typed,
// by the reporter.
var skipped = rotini.NewChannel[skippedFile]("skipped", rotini.ChannelClassInfo)

type scanHandler struct{ rotini.NoHooksE }

func (scanHandler) Run(_ context.Context, rtx *rotini.Context) error {
	rtx.Record(skipped, skippedFile{Path: "logo.png", Reason: "binary"})
	rtx.Record(skipped, skippedFile{Path: "vendor", Reason: "ignored"})
	return nil
}

// A channel carries typed records from the hooks to the reporter. A reporter that doesn't read
// it prints its records by its class; this one reads them itself.
func ExampleNewChannel() {
	def := rotini.Definition{Name: "scan", Handler: "Scan"}
	rotini.NewProgramFunc(def, func(string) (rotini.Handler, bool) {
		return rotini.FromHandlerE(scanHandler{}), true
	}).
		WithReporter(func(_ context.Context, _ *rotini.Context, out rotini.Outcome) {
			for _, s := range out.Records(skipped) {
				fmt.Printf("skipped %s: %s\n", s.Path, s.Reason)
			}
		}).
		Run(nil)
	// Output:
	// skipped logo.png: binary
	// skipped vendor: ignored
}

type deployHandler struct{ rotini.NoHooksE }

func (deployHandler) PreRun(_ context.Context, rtx *rotini.Context) error {
	if len(rtx.Argv) == 0 {
		return rotini.UsageError(errors.New("deploy: a target is required"))
	}
	return nil
}

func (deployHandler) Run(_ context.Context, rtx *rotini.Context) error {
	rtx.RecordSuccess("deployed " + rtx.Argv[0])
	return nil
}

// A HandlerE's hooks fail by returning an error: a failed PreRun stops the run before Run,
// and the error reaches the reporter. FromHandlerE adapts one where a Handler is expected.
func ExampleFromHandlerE() {
	def := rotini.Definition{
		Name: "app", Handler: "App",
		Arguments: []rotini.ArgDef{{Name: "target", Type: "string"}},
	}
	p := rotini.NewProgramFunc(def, func(string) (rotini.Handler, bool) {
		return rotini.FromHandlerE(deployHandler{}), true
	}).WithStderr(os.Stdout)

	code, _ := p.Run(nil)
	fmt.Println("exit", code)
	code, _ = p.Run([]string{"prod"})
	fmt.Println("exit", code)
	// Output:
	// Error: deploy: a target is required
	// exit 1
	// deployed prod
	// exit 0
}

// A custom reporter can keep the default report and add to it: here, a usage hint after a
// usage error.
func ExampleProgram_WithReporter() {
	rotini.NewProgramFunc(taskrDef(), taskrLookup).
		WithStderr(os.Stdout).
		WithReporter(func(ctx context.Context, rtx *rotini.Context, out rotini.Outcome) {
			rotini.DefaultReporter(ctx, rtx, out)
			if len(out.Errors) > 0 && rotini.CategoryOf(out.Errors[0]) == rotini.CategoryUsage {
				fmt.Fprintln(rtx.Stderr, "Run 'taskr --help' for usage.")
			}
		}).
		Run([]string{"add"})
	// Output:
	// Warning: the cache is stale
	// Error: missing title
	// Run 'taskr --help' for usage.
}
