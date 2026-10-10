package rotini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
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
// reporter's exit floor (which leaves out an error the run's own signal caused).
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
// The default prints infos, warnings, errors, panics, then successes, all to stderr, so stdout
// carries only what handlers write. It applies an exit floor: a recorded error or panic exits 1
// unless a handler already set a non-zero code. It leaves out an error caused by the run's own
// signal trap (the trap's cancellation, or context.Canceled after it), since a signal is a
// graceful stop and the exit code, 128+n, already says why the run ended. A custom reporter
// receives every recorded error and owns the exit code entirely.
//
// A nil fn restores the default reporter.
func (p *Program) WithReporter(fn Reporter) *Program {
	p.reporterFn = fn
	return p
}

// settle is the single run tail every run path ends in: it discards output files left open,
// flushes buffered stdout (recording a failed flush as an error), hands the recorded outcome to
// the reporter and resolves the exit code.
//
// A handler's exit code is already in rtx.exitCode when the reporter runs; the reporter is
// the final authority, since rtx.Exit overrides it during the reporter stage. The exit floor
// lives in defaultReporter, so a custom reporter does not inherit it.
func (p *Program) settle(ctx context.Context, rtx *Context) (int, error) {
	rtx.endStdin()
	rtx.abortOutputs()
	rtx.settleOutput()
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

// defaultReporter prints infos, warnings, errors, panics, then successes to the Program's
// stderr, then applies the exit floor. Panic stacks are not printed.
func (p *Program) defaultReporter(ctx context.Context, rtx *Context, out Outcome) {
	writeText(ctx, rtx, p.stderr, out)
}

// writeText is the default reporter's output, written to w: errors the run's own signal caused
// are left out, and the exit floor applies to the rest.
func writeText(ctx context.Context, rtx *Context, w io.Writer, out Outcome) {
	errs, _ := settleReported(ctx, rtx, out)
	for _, s := range out.Infos {
		fmt.Fprintln(w, s)
	}
	for _, warn := range out.Warnings {
		fmt.Fprintf(w, "Warning: %s\n", warn.Error())
	}
	for _, e := range errs {
		fmt.Fprintf(w, "Error: %s\n", e.Error())
	}
	for _, pe := range out.Panics {
		fmt.Fprintf(w, "Fatal Error: %v\n", pe)
	}
	for _, s := range out.Successes {
		fmt.Fprintln(w, s)
	}
}

// settleReported returns the recorded errors rotini's reporters print, leaving out those the
// run's own signal caused, and applies the exit floor to what is left: a reported error or a
// panic exits 1 unless a code was already set. A run whose only errors came from its signal
// keeps the signal's code. It returns the resulting exit code.
func settleReported(ctx context.Context, rtx *Context, out Outcome) ([]error, int) {
	errs := out.Errors
	if signalCanceled(ctx) {
		errs = slices.DeleteFunc(slices.Clone(errs), func(err error) bool { return fromRunSignal(ctx, err) })
	}
	code := rtx.applyExitFloor(len(errs) > 0 || len(out.Panics) > 0)
	if code == 0 && len(errs) < len(out.Errors) {
		code = canceledExitCode(ctx)
		rtx.Exit(code)
	}
	return errs, code
}

// StructuredReporter returns a [Reporter] for programs whose output scripts read. When
// structured reports true for the run, it writes each thing the run recorded to stderr as one
// JSON object per line, and leaves stdout alone, so a partial result there is never
// interleaved with an error:
//
//	{"error":{"category":"usage","command":"taskr add","exit_code":1,"kind":"missing-required","message":"missing required input: <title>"}}
//	{"error":{"candidates":["add","list","done"],"category":"usage","command":"taskr","exit_code":1,"kind":"unknown-command","message":"unknown command \"lst\" for \"taskr\"","token":"lst"}}
//	{"warning":{"command":"taskr list","message":"the cache is stale"}}
//
// Infos and successes are written the same way, as {"info":{…}} and {"success":{…}}. A field
// is present only when rotini knows it: kind, flag and token come from a [*ParseError], kind
// from a [*PluginError] too, and token from any error [SuggestionFacts] reads. With such a
// token, candidates lists the words it was checked against, as SuggestionFacts returns them,
// unranked: a tool can offer the nearest, while rotini itself suggests nothing. Each line's
// shape is described by schema-error.json in the rotini repository, and the contract document
// includes it.
//
// When structured reports false, or is nil, it reports as the default reporter does. Either way
// the exit code follows the default reporter's rule, and an error the run's own signal caused
// is left out, as the default reporter leaves it out.
//
// structured is the program's own rule, typically whether its format flag asks for json. The
// run may have failed in parsing, so the rule should read [Context.Argv], not validated inputs:
//
//	cmd.NewProgram(cmd.Handlers()).WithReporter(rotini.StructuredReporter(func(rtx *rotini.Context) bool {
//	    return slices.Contains(rtx.Argv, "--json")
//	})).Execute()
func StructuredReporter(structured func(rtx *Context) bool) Reporter {
	return func(ctx context.Context, rtx *Context, out Outcome) {
		if structured == nil || !structured(rtx) {
			reportText(ctx, rtx, out)
			return
		}
		reportStructured(ctx, rtx, out)
	}
}

// reportStructured writes the outcome as JSON lines on stderr.
func reportStructured(ctx context.Context, rtx *Context, out Outcome) {
	// A non-zero code a handler set is kept, as in the default reporter.
	errs, exitCode := settleReported(ctx, rtx, out)
	command := rtx.commandName()
	lines := make([]map[string]any, 0, len(out.Infos)+len(out.Warnings)+len(errs)+len(out.Panics)+len(out.Successes))
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
		// Candidates go with the token they were checked against, so both come from one error.
		if token, candidates, ok := SuggestionFacts(err); ok {
			if have, has := body["token"]; !has || have == token {
				body["token"] = token
				body["candidates"] = candidates
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
	for _, e := range errs {
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
			continue // unreachable: every value is a string, an int or a []string
		}
		fmt.Fprintf(rtx.Stderr, "%s\n", b)
	}
}

// reportText reports as the default reporter does, through the run's stderr.
func reportText(ctx context.Context, rtx *Context, out Outcome) {
	writeText(ctx, rtx, rtx.Stderr, out)
}
