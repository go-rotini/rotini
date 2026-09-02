package rotini

// A worked "fully unopinionated" program: a handler that imports NONE of the opt-in
// input helpers (no Collect/Parser/Binder) and instead takes every data-in point off
// the Context. It proves a rotini program can be as bare as the author wants — rotini
// resolves the command and runs the lifecycle; everything else is the handler's, using
// the Context surface plus the standard library.

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// unopinionatedCmd overrides only Run; the four embeddable no-op Default* hooks satisfy
// the rest of [Handlers]. Run reads the raw argv from [Context.Args], consults the
// resolved frame's declared flags via [Context.Chain] (spec-aware without a parser), reads
// an env var with the standard library (env is NOT runtime-mediated — only the streams
// are), writes through [Context.Stdout] so the program's streams stay injectable, and
// reports its outcome by recording + [Context.SignalExit] rather than printing inline.
// (Stdin would likewise be read via [Context.Stdin], never os.Stdin.)
type unopinionatedCmd struct {
	DefaultCascadingPreRun
	DefaultPreRun
	DefaultPostRun
	DefaultCascadingPostRun
}

func (unopinionatedCmd) Run(_ context.Context, rtx *Context) {
	leaf := rtx.Chain()[len(rtx.Chain())-1] // the resolved command frame

	// Hand-rolled argv scan — no Parser. The declared flag's identifiers come from the
	// resolved frame, so the scan stays spec-aware without importing the input helpers.
	var ids []string
	for _, f := range leaf.Flags {
		if f.Name == "name" {
			ids = f.Identifiers
		}
	}
	name := ""
	for i := 0; i+1 < len(rtx.Args); i++ {
		for _, id := range ids {
			if rtx.Args[i] == id {
				name = rtx.Args[i+1]
			}
		}
	}
	if name == "" {
		// Record the outcome; the runtime reports it through the funnels after teardown,
		// and the default OnError floors the exit to 1.
		rtx.RecordError(UsageError(errors.New("--name is required")))
		rtx.SignalExit(1)
		return
	}

	greeting := os.Getenv("GREETING") // env via stdlib, not a rotini helper
	if greeting == "" {
		greeting = "hello"
	}
	fmt.Fprintf(rtx.Stdout, "%s, %s! (command %q)\n", greeting, name, leaf.Name)
}

// unopinionatedApp is the aggregate handler set NewProgram resolves "Main" against.
type unopinionatedApp struct{}

func (unopinionatedApp) Main() Handlers { return unopinionatedCmd{} }

// Example_unopinionated drives the bare program end-to-end through the real Program
// surface — WithArgs feeds argv, WithExit captures the code without os.Exit, and the
// handler's [Context.Stdout] is the example's output. No opt-in input helper is imported;
// WithoutSignalHandling keeps the program minimal (rotini still owns the context, just
// installs no signal trap).
func Example_unopinionated() {
	def := Definition{
		Name: "greet", Handler: "Main",
		Flags: []FlagDef{{Name: "name", Identifiers: []string{"--name"}, Type: "string"}},
	}

	os.Setenv("GREETING", "hi")
	defer os.Unsetenv("GREETING")

	code := -1
	NewProgram(def, unopinionatedApp{}).
		WithArgs([]string{"--name", "ada"}).
		WithStdout(os.Stdout).
		WithoutSignalHandling().
		WithExit(func(c int) { code = c }).
		Execute()

	fmt.Println("exit:", code)
	// Output:
	// hi, ada! (command "greet")
	// exit: 0
}
