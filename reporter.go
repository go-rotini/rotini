package rotini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Outcome is everything a run recorded, handed to the [Reporter]. Each slice is in recording
// order and is a copy. [Context] exposes no other way to read the records.
type Outcome struct {
	// Infos are [Context.RecordInfo] messages: neutral output, no bearing on the exit code.
	Infos []string
	// Successes are [Context.RecordSuccess] messages.
	Successes []string
	// Warnings are [Context.RecordWarning] values: non-fatal, never raising the exit code.
	Warnings []error
	// Errors are [Context.RecordError] values.
	Errors []error
	// Panics are recovered panics and rotini-detected faults, captured by the runtime; there
	// is no record call for them.
	Panics []*PanicError
}

// Empty reports whether the run recorded nothing. The runtime does not call the reporter for
// an empty Outcome.
func (o Outcome) Empty() bool {
	return len(o.Infos)+len(o.Successes)+len(o.Warnings)+len(o.Errors)+len(o.Panics) == 0
}

// Failed reports whether the run recorded an error or a panic, the condition for the default
// reporter's exit floor.
func (o Outcome) Failed() bool { return len(o.Errors) > 0 || len(o.Panics) > 0 }

// Reporter is the program's outcome reporter. The runtime calls it once per run, after the
// lifecycle and its teardown finish, when the [Outcome] is not empty.
//
// The reporter decides what to print and where, and sets the final exit code: [Context.Exit]
// inside it overrides the code the lifecycle set, and [Context.HaltWithCode] is a no-op.
//
// The error [Program.Run] returns is built before the reporter is called, so modifying the
// Outcome does not change it. Records made inside the reporter are dropped.
//
// A panic inside a reporter is not recovered. A reporter that can fail handles its own
// failure, for example by writing to rtx.Stderr and setting a code with [Context.Exit].
type Reporter func(ctx context.Context, rtx *Context, out Outcome)

// WithReporter sets the program's outcome reporter. See [Reporter].
//
// The default prints infos, warnings, errors, panics, then successes (infos and successes to
// stdout, the rest to stderr), and applies an exit floor: a recorded error or panic exits 1
// unless a handler already set a non-zero code. A custom reporter owns the exit code entirely.
//
// A nil fn restores the default reporter.
func (p *Program) WithReporter(fn Reporter) *Program {
	p.reporterFn = fn
	return p
}

// settle is the single run tail every run path ends in: it hands the recorded outcome to the
// reporter and resolves the exit code.
//
// A handler's exit code is already in rtx.exitCode when the reporter runs; the reporter is
// the final authority, since rtx.Exit overrides it during the reporter stage. The exit floor
// lives in defaultReporter, so a custom reporter does not inherit it.
func (p *Program) settle(ctx context.Context, rtx *Context) (int, error) {
	out := Outcome{
		Infos:     rtx.copyInfos(),
		Successes: rtx.copySuccesses(),
		Warnings:  rtx.copyWarnings(),
		Errors:    rtx.copyErrors(),
		Panics:    rtx.copyFaults(),
	}

	// Build the run's error before the reporter runs: it shares the Outcome's backing arrays,
	// so a reporter writing to out.Errors[i] must not change what Run returns. rtx.exitCode
	// is read after the reporter, which is how the reporter controls the code.
	err := joinOutcome(out.Errors, out.Panics)

	if !out.Empty() {
		fn := p.reporterFn
		if fn == nil {
			fn = p.defaultReporter
		}
		rtx.setReporterStage(true) // rtx.Exit now overrides; rtx.HaltWithCode is a no-op
		fn(ctx, rtx, out)
		rtx.setReporterStage(false)
	}
	return rtx.code(), err
}

// joinOutcome is the error a run returns to its caller: every recorded error and captured
// fault, so errors.Is/As reach them all. It is nil for a clean run.
func joinOutcome(errs []error, faults []*PanicError) error {
	if len(errs) == 0 && len(faults) == 0 {
		return nil
	}
	all := make([]error, 0, len(errs)+len(faults))
	all = append(all, errs...)
	for _, f := range faults {
		all = append(all, f)
	}
	return errors.Join(all...)
}

// defaultReporter prints infos, warnings, errors, panics, then successes (infos and successes
// to stdout, the rest to stderr), then applies the exit floor: an error or fault exits 1
// unless a handler already set a non-zero code. Panic stacks are not printed.
func (p *Program) defaultReporter(_ context.Context, rtx *Context, out Outcome) {
	for _, s := range out.Infos {
		fmt.Fprintln(p.stdout, s)
	}
	for _, w := range out.Warnings {
		fmt.Fprintf(p.stderr, "Warning: %s\n", w.Error())
	}
	for _, e := range out.Errors {
		fmt.Fprintf(p.stderr, "Error: %s\n", e.Error())
	}
	for _, pe := range out.Panics {
		fmt.Fprintf(p.stderr, "Fatal Error: %v\n", pe)
	}
	for _, s := range out.Successes {
		fmt.Fprintln(p.stdout, s)
	}
	rtx.applyExitFloor(out.Failed())
}

// StructuredReporter returns a [Reporter] for programs whose output scripts read. When
// structured reports true for the run, it writes each thing the run recorded to stderr as one
// JSON object per line, and leaves stdout alone, so a partial result there is never
// interleaved with an error:
//
//	{"error":{"message":"missing required input: <title>","category":"usage","kind":"missing-required","command":"taskr add","exit_code":1}}
//	{"warning":{"message":"the cache is stale","command":"taskr list"}}
//
// Infos and successes are written the same way, as {"info":{…}} and {"success":{…}}, rather
// than to stdout. A field is present only when rotini knows it: kind, flag and token come from
// a [*ParseError], kind from a [*PluginError] too, and token from any error [SuggestionFacts]
// reads. Each line's shape is described by
// schema-error.json in the rotini repository, and the contract document includes it.
//
// When structured reports false, or is nil, it reports as the default reporter does. Either way
// the exit code follows the default reporter's rule.
//
// structured is the program's own rule, typically whether its format flag asks for json. The
// run may have failed in parsing, so the rule should read [Context.Argv], not validated inputs:
//
//	cmd.Program.WithReporter(rotini.StructuredReporter(func(rtx *rotini.Context) bool {
//	    return slices.Contains(rtx.Argv, "--json")
//	})).Execute()
func StructuredReporter(structured func(rtx *Context) bool) Reporter {
	return func(_ context.Context, rtx *Context, out Outcome) {
		if structured == nil || !structured(rtx) {
			reportText(rtx, out)
			return
		}
		reportStructured(rtx, out)
	}
}

// reportStructured writes the outcome as JSON lines on stderr.
func reportStructured(rtx *Context, out Outcome) {
	// A deliberate handler exit is never downgraded, as in the default reporter.
	exitCode := rtx.applyExitFloor(out.Failed())
	command := rtx.commandName()
	lines := make([]map[string]any, 0, len(out.Infos)+len(out.Warnings)+len(out.Errors)+len(out.Panics)+len(out.Successes))
	message := func(kind, msg string) map[string]any {
		body := map[string]any{"message": msg}
		if command != "" {
			body["command"] = command
		}
		return map[string]any{kind: body}
	}
	failure := func(err error) map[string]any {
		line := message("error", err.Error())
		body, _ := line["error"].(map[string]any)
		body["category"] = CategoryOf(err).String()
		body["exit_code"] = exitCode
		if pe, ok := errors.AsType[*ParseError](err); ok {
			if pe.Kind != ParseKindUnspecified {
				body["kind"] = pe.Kind.String()
			}
			if pe.Flag != "" {
				body["flag"] = pe.Flag
			}
			if pe.Token != "" {
				body["token"] = pe.Token
			}
		} else if pe, ok := errors.AsType[*PluginError](err); ok {
			body["kind"] = pe.Kind.String()
		}
		if _, has := body["token"]; !has {
			if token, _, ok := SuggestionFacts(err); ok {
				body["token"] = token
			}
		}
		return line
	}
	for _, s := range out.Infos {
		lines = append(lines, message("info", s))
	}
	for _, w := range out.Warnings {
		lines = append(lines, message("warning", w.Error()))
	}
	for _, e := range out.Errors {
		lines = append(lines, failure(e))
	}
	for _, pe := range out.Panics {
		lines = append(lines, failure(pe))
	}
	for _, s := range out.Successes {
		lines = append(lines, message("success", s))
	}
	for _, line := range lines {
		b, err := json.Marshal(line)
		if err != nil {
			continue // unreachable: every value is a string or an int
		}
		fmt.Fprintf(rtx.Stderr, "%s\n", b)
	}
}

// reportText reports as the default reporter does, through the run's streams.
func reportText(rtx *Context, out Outcome) {
	for _, s := range out.Infos {
		fmt.Fprintln(rtx.Stdout, s)
	}
	for _, w := range out.Warnings {
		fmt.Fprintf(rtx.Stderr, "Warning: %s\n", w.Error())
	}
	for _, e := range out.Errors {
		fmt.Fprintf(rtx.Stderr, "Error: %s\n", e.Error())
	}
	for _, pe := range out.Panics {
		fmt.Fprintf(rtx.Stderr, "Fatal Error: %v\n", pe)
	}
	for _, s := range out.Successes {
		fmt.Fprintln(rtx.Stdout, s)
	}
	rtx.applyExitFloor(out.Failed())
}
