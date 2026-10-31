package rotini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
)

// Outcome is everything a run recorded, handed to the [Reporter]. Each slice is in recording
// order and is a copy. The author's own [Channel] records are read with [Outcome.Records].
// [Context] exposes no other way to read the records.
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

	// ExitCode is the exit code the lifecycle set before the reporter ran: by
	// [Context.HaltWithCode], [Context.Exit], an [ExitCause], or 128+n after a trapped signal.
	// It is 0 when none was set. It is information only: the reporter sets the final code with
	// [Context.Exit].
	ExitCode int
	// Signal is the signal rotini's trap caught that stopped the run, or nil. See [CausedBySignal]
	// for the errors it caused.
	Signal os.Signal

	// channels are the author's [Channel] records, read with [Outcome.Records].
	channels []*channelRecords
}

// Empty reports whether the run recorded nothing, in the built-in record fields or any
// [Channel]; ExitCode and Signal are not records. The runtime does not call the reporter for an
// empty Outcome unless [Program.WithReporterOnEmpty] asks it to.
func (o Outcome) Empty() bool {
	return len(o.Infos)+len(o.Successes)+len(o.Warnings)+len(o.Errors)+len(o.Panics)+len(o.channels) == 0
}

// Failed reports whether the run recorded an error, a panic or a record in a
// [ChannelClassError] channel, the condition for the default reporter's exit floor (which
// leaves out an error the run's own signal caused).
func (o Outcome) Failed() bool {
	return len(o.Errors) > 0 || len(o.Panics) > 0 || failedChannel(o.channels)
}

// Reporter is the program's outcome reporter. The runtime calls it once per run, after the
// lifecycle and its teardown finish, when the [Outcome] is not empty, or on every run with
// [Program.WithReporterOnEmpty].
//
// The reporter decides what to print and where, and sets the final exit code: [Context.Exit]
// inside it overrides the code the lifecycle set, and [Context.HaltWithCode] is a no-op.
//
// The error [Program.Run] returns is built before the reporter is called, so modifying the
// Outcome does not change it. Records made inside the reporter are dropped.
//
// A run that records nothing has an empty Outcome, and the reporter is not called. A program
// that reports something on every run, such as a closing {"ok":true} line or the run's timing,
// turns on [Program.WithReporterOnEmpty], so its reporter is called with the empty Outcome too.
//
// A panic inside a reporter is not recovered. A reporter that can fail handles its own
// failure, for example by writing to rtx.Stderr and setting a code with [Context.Exit].
type Reporter func(ctx context.Context, rtx *Context, out Outcome)

// WithReporter sets the program's outcome reporter. See [Reporter]. The default is
// [DefaultReporter]; a custom reporter receives every recorded error and owns the exit code
// entirely, and can call DefaultReporter for the rest of the report:
//
//	p.WithReporter(func(ctx context.Context, rtx *rotini.Context, out rotini.Outcome) {
//		rotini.DefaultReporter(ctx, rtx, out)
//		if out.Failed() {
//			fmt.Fprintln(rtx.Stderr, "Run 'taskr --help' for usage.")
//		}
//	})
//
// A nil fn restores the default reporter.
func (p *Program) WithReporter(fn Reporter) *Program {
	p.reporterFn = fn
	return p
}

// WithReporterOnEmpty controls whether the reporter is called for a run that recorded nothing.
// The default, false, skips it, so a clean run is silent. True calls it with the empty
// [Outcome] on every run that settles, for a reporter that reports every run, such as one that
// writes a closing {"ok":true} line or the run's timing:
//
//	p.WithReporterOnEmpty(true).WithReporter(func(ctx context.Context, rtx *rotini.Context, out rotini.Outcome) {
//		rotini.DefaultReporter(ctx, rtx, out)
//		if out.Empty() {
//			fmt.Fprintln(rtx.Stderr, `{"ok":true}`)
//		}
//	})
//
// [DefaultReporter] and [StructuredReporter] print nothing for an empty Outcome and leave the
// exit code as the run set it. A completion request, and a plugin that ran without rotini
// recording anything, are still not reported.
func (p *Program) WithReporterOnEmpty(on bool) *Program {
	p.reportEmpty = on
	return p
}

// Reporter returns the program's outcome reporter: the one [Program.WithReporter] set, or
// [DefaultReporter]. Wrapping it keeps the program's report while adding to it:
//
//	report := p.Reporter()
//	p.WithReporter(func(ctx context.Context, rtx *rotini.Context, out rotini.Outcome) {
//		audit(out)
//		report(ctx, rtx, out)
//	})
func (p *Program) Reporter() Reporter {
	if p.reporterFn == nil {
		return DefaultReporter
	}
	return p.reporterFn
}

// settle is the single run tail every run path ends in: it discards output files left open,
// flushes buffered stdout (recording a failed flush as an error), hands the recorded outcome to
// the reporter, releases what the run's providers built, and resolves the exit code.
//
// A handler's exit code is already in rtx.exitCode when the reporter runs; the reporter is
// the final authority, since rtx.Exit overrides it during the reporter stage. The exit floor
// lives in DefaultReporter, so a custom reporter does not inherit it.
//
// The providers' values are released after the reporter, so it can use them; a run the
// reporter would skip, having recorded nothing, releases them first, so a cleanup failure
// is reported. A cleanup failure after the reporter joins the returned error and turns exit
// code 0 into 1, the one exit floor that outranks the reporter: the failure was never shown
// to it.
func (p *Program) settle(ctx context.Context, rtx *Context) (code int, err error) {
	// A signal that arrived after the last forward step (during teardown, say) still ends
	// the run with its code, however forward progress stopped, unless a hook exited.
	if _, exitNow := rtx.stopState(); !exitNow && signalCanceled(ctx) {
		rtx.HaltWithCode(canceledExitCode(ctx))
	}

	rtx.endStdin()
	rtx.abortOutputs()
	rtx.settleOutput()
	out := rtx.outcome(ctx)
	report := p.reportEmpty || !out.Empty()
	if !report {
		// Nothing to report: release now, so a cleanup failure is recorded and reported. A
		// value the reporter then asks for is built again and released after it.
		p.releaseProvided(rtx, rtx.RecordError, rtx.recordFault)
		rtx.forgetProvided()
		out = rtx.outcome(ctx)
		report = !out.Empty()
	}
	// Released after the reporter, and while a panic from it unwinds.
	defer func() {
		var late []error
		p.releaseProvided(rtx, func(e error) { late = append(late, e) }, func(pe *PanicError) { late = append(late, pe) })
		if len(late) > 0 {
			err = errors.Join(append([]error{err}, late...)...)
			if code == 0 {
				code = 1
			}
		}
	}()

	// Build the run's error before the reporter runs: it shares the Outcome's backing arrays,
	// so a reporter writing to out.Errors[i] must not change what Run returns. rtx.exitCode
	// is read after the reporter, which is how the reporter controls the code.
	err = joinOutcome(append(slices.Clip(out.Errors), out.channelErrors()...), out.Panics)

	if report {
		fn := p.Reporter()
		rtx.setReporterStage(true) // rtx.Exit now overrides; rtx.HaltWithCode is a no-op
		fn(ctx, rtx, out)
		rtx.setReporterStage(false)
	}
	return rtx.code(), err
}

// outcome is what the run recorded so far, copied for the reporter.
func (rtx *Context) outcome(ctx context.Context) Outcome {
	return Outcome{
		Infos:     rtx.copyInfos(),
		Successes: rtx.copySuccesses(),
		Warnings:  rtx.copyWarnings(),
		Errors:    rtx.copyErrors(),
		Panics:    rtx.copyFaults(),
		ExitCode:  rtx.code(),
		Signal:    runSignal(ctx),
		channels:  rtx.copyChannels(),
	}
}

// releaseProvided calls the cleanups of what the run's providers built and, for
// [Program.Execute]'s run, releases the program's shared values, passing each cleanup error
// to record and each cleanup panic to fault.
func (p *Program) releaseProvided(rtx *Context, record func(error), fault func(*PanicError)) {
	rtx.cleanUpProvided(record, fault)
	if rtx.closesShared {
		p.closeShared(record, fault)
	}
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

// DefaultReporter is the [Reporter] a program uses unless [Program.WithReporter] sets another.
// It prints infos, warnings, errors, panics, then successes, each followed by the records of the
// [Channel]s of that [ChannelClass], all to the run's stderr ([Context.Stderr], so a handler
// that replaces it redirects the report too), so stdout carries only what handlers write. Panic
// stacks are not printed. Each line is the record's text; a warning starts "Warning: ", an error
// "Error: " and a panic "Fatal Error: ".
//
// It applies an exit floor: a recorded error or panic exits 1 unless a non-zero code was
// already set ([Outcome.ExitCode]). It leaves out an error the run's own signal caused
// ([CausedBySignal]), since a signal is a graceful stop and the exit code, 128+n, already says why
// the run ended.
//
// It prints nothing for an empty Outcome, and the runtime does not call it for one unless
// [Program.WithReporterOnEmpty] asks it to, so a clean run that recorded nothing prints nothing;
// see [Reporter].
//
// A custom reporter calls it to keep the default report and add to it; see
// [Program.WithReporter].
func DefaultReporter(ctx context.Context, rtx *Context, out Outcome) {
	writeText(ctx, rtx, rtx.Stderr, out)
}

// writeText is the default reporter's output, written to w: errors the run's own signal caused
// are left out, and the exit floor applies to the rest.
func writeText(ctx context.Context, rtx *Context, w io.Writer, out Outcome) {
	errs, _ := settleReported(ctx, rtx, out)
	for _, s := range out.Infos {
		fmt.Fprintln(w, s)
	}
	for _, v := range out.channelValues(ChannelClassInfo) {
		fmt.Fprintln(w, v)
	}
	for _, warn := range out.Warnings {
		fmt.Fprintf(w, "Warning: %s\n", warn.Error())
	}
	for _, v := range out.channelValues(ChannelClassWarning) {
		fmt.Fprintf(w, "Warning: %v\n", v)
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
	for _, v := range out.channelValues(ChannelClassSuccess) {
		fmt.Fprintln(w, v)
	}
}

// settleReported returns the recorded errors rotini's reporters print, leaving out those the
// run's own signal caused, followed by the records of every [ChannelClassError] channel, and
// applies the exit floor to that list: a reported error or a panic exits 1 unless a code was
// already set. A run whose only errors came from its signal keeps the signal's code. It returns the
// resulting exit code.
func settleReported(ctx context.Context, rtx *Context, out Outcome) ([]error, int) {
	errs := out.Errors
	if signalCanceled(ctx) {
		errs = slices.DeleteFunc(slices.Clone(errs), func(err error) bool { return CausedBySignal(ctx, err) })
	}
	kept := len(errs)
	if custom := out.channelErrors(); len(custom) > 0 {
		errs = append(slices.Clip(errs), custom...)
	}
	code := rtx.applyExitFloor(len(errs) > 0 || len(out.Panics) > 0)
	if code == 0 && kept < len(out.Errors) {
		code = canceledExitCode(ctx)
		rtx.Exit(code)
	}
	return errs, code
}

// StructuredReporter returns a [Reporter] for programs whose output scripts read. When
// structured reports true for the run, it writes each thing the run recorded to the run's
// stderr ([Context.Stderr]) as one JSON object per line, and leaves stdout alone, so a partial
// result there is never interleaved with an error:
//
//	{"schema_version":1,"error":{"category":"usage","command":"taskr add","exit_code":1,"kind":"missing-required","message":"taskr add: missing required input: <title>"}}
//	{"schema_version":1,"error":{"candidates":["add","list","done"],"category":"usage","command":"taskr","exit_code":1,"kind":"unknown-command","message":"taskr: unknown command \"lst\"","token":"lst"}}
//	{"schema_version":1,"warning":{"command":"taskr list","message":"the cache is stale"}}
//
// Infos and successes are written the same way, as {"info":{…}} and {"success":{…}}. A
// [Channel] the spec declares (a [ChannelDef] of the Definition) writes each record as a line of
// its own kind, the record as JSON: {"schema_version":1,"skipped":{"path":"a.bin","reason":"binary"}}.
// A record of a declared error channel is also an error line, so every failure has one. Any
// other channel's records are written as lines of their class's kind, with the record's text
// (its Error, String or %v) as the message. A field of an error line is present only when
// rotini knows it: kind, flag and token come from a [*ParseError], kind from an [*InputError]
// or a [*PluginError] too, and token from any error [SuggestionFacts] reads. With such a token, candidates lists the words it was checked against, as
// SuggestionFacts returns them, unranked: a tool can offer the nearest, while rotini itself
// suggests nothing. Each line's shape is described by schema-error.json in the rotini
// repository, and the contract document includes it.
//
// When structured reports false, or is nil, it reports with [DefaultReporter]. Either way the
// exit code follows DefaultReporter's rule, and an error the run's own signal caused is left
// out, as DefaultReporter leaves it out.
//
// structured is the program's own rule, typically whether its format flag asks for json. The
// run may have failed in parsing, so the rule should read [Context.Argv], not validated inputs:
//
//	cmd.NewProgram().WithReporter(rotini.StructuredReporter(func(rtx *rotini.Context) bool {
//		return slices.Contains(rtx.Argv, "--json")
//	})).Execute()
func StructuredReporter(structured func(rtx *Context) bool) Reporter {
	return func(ctx context.Context, rtx *Context, out Outcome) {
		if structured == nil || !structured(rtx) {
			DefaultReporter(ctx, rtx, out)
			return
		}
		reportStructured(ctx, rtx, out)
	}
}

// channelLines returns the structured lines of the records of out's channels of class. A channel
// the Definition declares writes each record as a line of its own kind, the record as JSON; any
// other channel's records are messages of their class's kind, made by message. An error
// channel's records are already error lines, so only a declared one adds lines.
func (rtx *Context) channelLines(out Outcome, class ChannelClass, message func(kind, msg string) map[string]any) []map[string]any {
	var lines []map[string]any
	for _, c := range out.channels {
		if c.class != class {
			continue
		}
		declared := rtx.declaresChannel(c)
		for _, v := range c.values {
			if declared {
				if b, err := json.Marshal(v); err == nil && len(b) > 0 && b[0] == '{' {
					lines = append(lines, map[string]any{c.name: json.RawMessage(b)})
					continue
				}
			}
			if class != ChannelClassError {
				lines = append(lines, message(class.String(), fmt.Sprint(v)))
			}
		}
	}
	return lines
}

// structuredSchemaVersion is the version of StructuredReporter's line format, written first on
// every line. It changes only when a line's shape changes in a way that breaks a reader.
const structuredSchemaVersion = 1

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
		if pe, ok := asError[*ParseError](err); ok {
			if pe.Kind != ParseKindUnspecified {
				body["kind"] = pe.Kind.String()
			}
			if pe.Flag != "" {
				body["flag"] = pe.Flag
			}
			if pe.Token != "" {
				body["token"] = pe.Token
			}
		} else if ie, ok := asError[*InputError](err); ok {
			if ie.Kind != InputErrorKindUnspecified {
				body["kind"] = ie.Kind.String()
			}
		} else if pe, ok := asError[*PluginError](err); ok {
			if pe.Kind != PluginErrorKindUnspecified {
				body["kind"] = pe.Kind.String()
			}
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
	records := func(class ChannelClass) { lines = append(lines, rtx.channelLines(out, class, message)...) }
	for _, s := range out.Infos {
		lines = append(lines, message("info", s))
	}
	records(ChannelClassInfo)
	for _, w := range out.Warnings {
		lines = append(lines, message("warning", w.Error()))
	}
	records(ChannelClassWarning)
	for _, e := range errs {
		lines = append(lines, failure(e))
	}
	records(ChannelClassError)
	for _, pe := range out.Panics {
		lines = append(lines, failure(pe))
	}
	for _, s := range out.Successes {
		lines = append(lines, message("success", s))
	}
	records(ChannelClassSuccess)
	for _, line := range lines {
		for kind, body := range line { // one kind per line
			b, err := json.Marshal(body)
			if err != nil {
				continue // unreachable: every value is a string, an int, a []string or marshaled JSON
			}
			fmt.Fprintf(rtx.Stderr, "{\"schema_version\":%d,%q:%s}\n", structuredSchemaVersion, kind, b)
		}
	}
}
