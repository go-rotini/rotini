package rtk

import (
	"bytes"
	"strings"

	"github.com/go-rotini/rotini"
)

// HarnessConfig configures a [NewHarness]. The zero value is valid — it yields a bare
// harness whose service doubles are wired to capture buffers, with no resolved command.
type HarnessConfig struct {
	// Def and Argv resolve the command chain so the bound Parser/Binder can parse typed
	// inputs, exactly as the runtime would. Leave Def zero for a handler that doesn't
	// parse (it only reads IO/Printer/etc.).
	Def  rotini.Definition
	Argv []string

	// Stdin is the scripted input. IO.Stdin and the Prompter each read it from an
	// independent reader, so a handler that uses either gets the full input.
	Stdin string

	// Color forces the color level reported by the Terminal, Printer, and Progress
	// doubles (default ColorNone — monochrome, the deterministic test default). Set it
	// to exercise a handler's colored-output branch.
	Color ColorLevel

	// Binder, when non-nil, additionally binds a Binder built from this meta under the
	// "binder" key — for handlers that bind env/config/stdin inputs.
	Binder *rotini.BindMeta
}

// Harness bundles in-memory doubles for the rtk services so a command handler can be
// unit-tested without a real terminal: output is captured to buffers, stdin is fed from
// a string, and the typed-input services resolve a supplied argv. It binds the doubles
// under the standard keys ("io", "terminal", "printer", "prompt", "progress", "parser",
// and "binder" when requested), so handler code that retrieves them via
// rotini.MustGet runs unchanged.
//
// The harness does not run the lifecycle — it hands you a ready Rtx; the test invokes
// whatever handler method it wants and asserts on Out/Err (and the exposed doubles):
//
//	h := rtk.NewHarness(rtk.HarnessConfig{Def: app.Def, Argv: []string{"build", "--mode", "fast"}})
//	(&buildHandler{}).Run(context.Background(), h.Rtx)
//	if !strings.Contains(h.Out.String(), "built") {
//	    t.Errorf("missing build output: %q", h.Out)
//	}
//
// The Terminal double reports a non-terminal with the forced color level and the default
// size — deterministic and side-effect-free. The interactive-TTY branch (StdinIsTerminal
// true) cannot be forced without a pty and is out of scope for the harness.
type Harness struct {
	// Rtx is the context to hand to the handler under test: the doubles are bound and
	// the chain is resolved from Def+Argv.
	Rtx *rotini.Context

	// Out and Err capture everything the handler writes through IO/Printer/Prompter/
	// Progress — Out is the stdout stream, Err the stderr stream (Printer.Warning/Error
	// and IO.Stderr land here), matching how the streams split in production.
	Out *bytes.Buffer
	Err *bytes.Buffer

	// The bound doubles, exposed for direct assertions and further configuration. Binder
	// is nil unless HarnessConfig.Binder was set.
	IO       *IO
	Terminal *Terminal
	Printer  *Printer
	Prompter *Prompter
	Progress *Progress
	Parser   *Parser
	Binder   *Binder
}

// NewHarness builds a Harness from cfg: capture buffers, in-memory service doubles bound
// under the standard keys, and a context whose chain is resolved from cfg.Def/cfg.Argv.
func NewHarness(cfg HarnessConfig) *Harness {
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}

	h := &Harness{
		Out: out,
		Err: errb,
		// IO and the Prompter read independent readers over the same scripted input.
		IO: NewIO().
			WithStdin(strings.NewReader(cfg.Stdin)).
			WithStdout(out).
			WithStderr(errb),
		// Nil streams make every IsTerminal check false (isTTY(nil) == false) with no real
		// fds to leak; color and size are forced so detection never touches the host.
		Terminal: NewTerminal().
			WithStdin(nil).WithStdout(nil).WithStderr(nil).
			WithColorLevel(cfg.Color).
			WithSize(DefaultSize),
		// WithColorLevel last so it wins over the re-detection WithOutput triggers.
		Printer: NewPrinter().
			WithOutput(out).WithError(errb).WithColorLevel(cfg.Color),
		Prompter: NewPrompter().
			WithInput(strings.NewReader(cfg.Stdin)).WithOutput(out),
		Progress: NewProgress().
			WithOutput(out).WithColorLevel(cfg.Color),
		Parser: NewParser(),
	}

	rtx := rotini.NewContextFor(cfg.Def, cfg.Argv)
	rtx.Bind("io", h.IO).
		Bind("terminal", h.Terminal).
		Bind("printer", h.Printer).
		Bind("prompt", h.Prompter).
		Bind("progress", h.Progress).
		Bind("parser", h.Parser)

	if cfg.Binder != nil {
		h.Binder = NewBinder(*cfg.Binder)
		rtx.Bind("binder", h.Binder)
	}
	h.Rtx = rtx
	return h
}
